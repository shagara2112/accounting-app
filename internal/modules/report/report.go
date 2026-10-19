// Package report adalah modul laporan keuangan dan dashboard (RPT-01–RPT-07). Semua angka
// dihitung langsung dari buku besar: saldo agregat per bulan untuk bulan penuh dan baris jurnal
// untuk sisa harinya, sehingga laporan untuk tanggal mana pun tetap cepat dan selalu sama
// dengan buku besar. Modul ini hanya membaca tabel ledger (TDD, aturan antar modul nomor 3);
// data dokumen dibaca lewat service modul penjualan, pembelian, dan kas.
package report

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/core"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/report/internal/store"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/money"
)

// Period adalah rentang tanggal satu kolom laporan.
type Period struct {
	From civil.Date `json:"from"`
	To   civil.Date `json:"to"`
}

// Column adalah satu kolom angka di laporan.
type Column struct {
	Label string     `json:"label"`
	From  civil.Date `json:"from"`
	To    civil.Date `json:"to"`
	// Saringan cabang/departemen kolom ini (id, atau "none" untuk tanpa dimensi); dipakai
	// tautan ke buku besar supaya rinciannya sama dengan angka di kolom.
	Branch     string `json:"branch,omitempty"`
	Department string `json:"department,omitempty"`
}

// Jenis baris laporan.
const (
	LineSection  = "section"  // judul bagian, mis. ASET
	LineGroup    = "group"    // akun header
	LineAccount  = "account"  // akun detail
	LineComputed = "computed" // baris hitungan, mis. laba tahun berjalan
	LineSubtotal = "subtotal" // jumlah satu akun header
	LineTotal    = "total"    // jumlah satu bagian
	LineResult   = "result"   // laba kotor, laba usaha, laba bersih
)

type Line struct {
	Kind      string         `json:"kind"`
	Code      string         `json:"code,omitempty"`
	Name      string         `json:"name"`
	Depth     int            `json:"depth"`
	AccountID *uuid.UUID     `json:"accountId,omitempty"`
	Values    []money.Amount `json:"values,omitempty"`
}

// Statement adalah laporan posisi keuangan atau laba rugi, dengan satu kolom per periode.
type Statement struct {
	Title    string   `json:"title"`
	Company  string   `json:"company"`
	Columns  []Column `json:"columns"`
	Lines    []Line   `json:"lines"`
	Balanced []bool   `json:"balanced,omitempty"` // neraca: aset = liabilitas + ekuitas per kolom
}

type Service interface {
	BalanceSheet(ctx context.Context, companyID uuid.UUID, dates []civil.Date) (Statement, error)
	ProfitLoss(ctx context.Context, companyID uuid.UUID, periods []Period) (Statement, error)
	// ProfitLossBy memilah laba rugi per cabang/departemen atau satu kolom per cabang (GL-09).
	ProfitLossBy(ctx context.Context, companyID uuid.UUID, periods []Period, f DimFilter) (Statement, error)
	CashFlow(ctx context.Context, companyID uuid.UUID, p Period) (CashFlow, error)
	TrialBalance(ctx context.Context, companyID uuid.UUID, date civil.Date) (TrialBalance, error)
	Summary(ctx context.Context, companyID uuid.UUID, kind string, p Period) (Summary, error)
	ContactCard(ctx context.Context, companyID uuid.UUID, side string, contactID uuid.UUID, p Period) (ContactCard, error)
	Dashboard(ctx context.Context, companyID uuid.UUID, today civil.Date) (Dashboard, error)
	// CashProjection memproyeksikan saldo kas per minggu dari piutang dan utang jatuh tempo (AP-06).
	CashProjection(ctx context.Context, companyID uuid.UUID, asOf civil.Date, weeks int) (CashProjection, error)

	// Net mengembalikan debit − kredit per akun untuk rentang [from, to]; dipakai konsolidasi.
	Net(ctx context.Context, companyID uuid.UUID, from, to civil.Date, skipClosing bool) (map[uuid.UUID]money.Amount, error)
	// FiscalStart adalah tanggal 1 tahun buku PT yang memuat date.
	FiscalStart(ctx context.Context, companyID uuid.UUID, date civil.Date) (civil.Date, error)
}

type Deps struct {
	Pool     *pgxpool.Pool
	Ledger   ledger.Service
	Core     core.Service
	Cash     cash.Service
	Sales    sales.Service
	Purchase purchase.Service
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

// beginning adalah tanggal awal untuk saldo "sejak awal" akun neraca.
var beginning = civil.New(2000, 1, 1)

// net mengembalikan debit − kredit per akun untuk rentang [from, to]. Bulan penuh dibaca dari
// saldo agregat, sisa hari di awal dan akhir rentang dari baris jurnal.
func (s *service) Net(ctx context.Context, companyID uuid.UUID, from, to civil.Date, skipClosing bool) (map[uuid.UUID]money.Amount, error) {
	return s.net(ctx, companyID, from, to, skipClosing)
}

func (s *service) FiscalStart(ctx context.Context, companyID uuid.UUID, date civil.Date) (civil.Date, error) {
	m, err := s.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return civil.Date{}, err
	}
	return fiscalStart(m, date), nil
}

func (s *service) net(ctx context.Context, companyID uuid.UUID, from, to civil.Date, skipClosing bool) (map[uuid.UUID]money.Amount, error) {
	out := map[uuid.UUID]money.Amount{}
	if to.Before(from) {
		return out, nil
	}
	next := to.AddDays(1)
	fullFrom := from
	if from.Day() != 1 {
		fullFrom = from.MonthStart().AddMonths(1)
	}
	fullTo := next.MonthStart() // eksklusif
	p := store.NetByAccountParams{CompanyID: companyID, SkipClosing: skipClosing}
	if fullFrom.Before(fullTo) {
		p.FullFrom, p.FullTo = fullFrom, fullTo
		p.AFrom, p.ATo = from, fullFrom // hari di bulan pertama yang tidak penuh
		p.BFrom, p.BTo = fullTo, to     // hari di bulan terakhir yang tidak penuh
	} else {
		p.FullFrom, p.FullTo = from, from // tanpa bulan penuh
		p.AFrom, p.ATo = from, next
		p.BFrom, p.BTo = next, from
	}
	rows, err := s.q(ctx).NetByAccount(ctx, p)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.AccountID] = r.Debit.Sub(r.Credit)
	}
	return out, nil
}

// fiscalStart adalah tanggal 1 tahun buku yang memuat d.
func fiscalStart(startMonth int, d civil.Date) civil.Date {
	if int(d.Month()) >= startMonth {
		return civil.New(d.Year(), time.Month(startMonth), 1)
	}
	return civil.New(d.Year()-1, time.Month(startMonth), 1)
}

// Chart adalah bagan akun sebagai pohon. Konsolidasi memakainya untuk bagan akun grup.
type Chart struct {
	byID     map[uuid.UUID]ledger.Account
	children map[uuid.UUID][]ledger.Account
	roots    map[int]ledger.Account
}

func (s *service) chart(ctx context.Context, companyID uuid.UUID) (Chart, error) {
	list, err := s.Ledger.Accounts(ctx, companyID)
	if err != nil {
		return Chart{}, err
	}
	return NewChart(list), nil
}

// NewChart menyusun pohon dari daftar akun (induk lewat ParentID, akar = akun level 1).
func NewChart(list []ledger.Account) Chart {
	c := Chart{byID: map[uuid.UUID]ledger.Account{}, children: map[uuid.UUID][]ledger.Account{}, roots: map[int]ledger.Account{}}
	for _, a := range list {
		c.byID[a.ID] = a
		if a.ParentID == nil {
			c.roots[a.Class] = a
		} else {
			c.children[*a.ParentID] = append(c.children[*a.ParentID], a)
		}
	}
	for k := range c.children {
		sort.Slice(c.children[k], func(i, j int) bool { return c.children[k][i].Code < c.children[k][j].Code })
	}
	return c
}

// SumClass menjumlahkan debit − kredit akun berkelas tertentu.
func (c Chart) SumClass(values map[uuid.UUID]money.Amount, classes ...int) money.Amount {
	return c.sumClass(values, classes...)
}

// ancestor mengembalikan leluhur akun di level tertentu (1 = kelompok kelas).
func (c Chart) ancestor(a ledger.Account, level int) (ledger.Account, bool) {
	for a.Level > level && a.ParentID != nil {
		p, ok := c.byID[*a.ParentID]
		if !ok {
			return a, false
		}
		a = p
	}
	return a, a.Level == level
}

// sumClass menjumlahkan debit − kredit akun berkelas tertentu.
func (c Chart) sumClass(values map[uuid.UUID]money.Amount, classes ...int) money.Amount {
	want := map[int]bool{}
	for _, k := range classes {
		want[k] = true
	}
	var t money.Amount
	for id, v := range values {
		if want[c.byID[id].Class] {
			t = t.Add(v)
		}
	}
	return t
}

func (s *service) companyName(ctx context.Context, companyID uuid.UUID) (string, error) {
	c, err := s.Core.Company(ctx, companyID)
	return c.Name, err
}

func (s *service) fiscalStartMonth(ctx context.Context, companyID uuid.UUID) (int, error) {
	st, err := s.Ledger.Settings(ctx, companyID)
	if err != nil {
		return 0, err
	}
	if st.FiscalYearStartMonth < 1 {
		return 1, nil
	}
	return st.FiscalYearStartMonth, nil
}

// cardOf membaca kartu pelanggan atau pemasok dari modulnya.
func (s *service) cardOf(ctx context.Context, companyID uuid.UUID, side string, contactID uuid.UUID, p Period) (trade.Card, error) {
	if side == "supplier" {
		return s.Purchase.SupplierCard(ctx, companyID, contactID, p.From, p.To)
	}
	return s.Sales.CustomerCard(ctx, companyID, contactID, p.From, p.To)
}

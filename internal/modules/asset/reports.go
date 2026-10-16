package asset

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// RecRow membandingkan satu akun: total register dengan saldo buku besarnya. Untuk akun aset,
// register = harga perolehan aset aktif; untuk akun akumulasi, register = akumulasi penyusutan
// aset aktif (saldo kredit di buku besar).
type RecRow struct {
	AccountID  uuid.UUID    `json:"accountId"`
	Code       string       `json:"code"`
	Name       string       `json:"name"`
	Side       string       `json:"side"` // aset atau akumulasi
	Register   money.Amount `json:"register"`
	Ledger     money.Amount `json:"ledger"`
	Difference money.Amount `json:"difference"`
}

type Reconciliation struct {
	Rows    []RecRow `json:"rows"`
	Matched bool     `json:"matched"`
}

// Reconciliation mencocokkan register dengan buku besar untuk setiap akun aset dan akumulasi
// yang dipakai kategori atau aset. Selisih biasanya berarti aset yang dibeli belum didaftarkan,
// atau penyusutan yang dijurnal manual di luar modul ini.
func (s *service) Reconciliation(ctx context.Context, companyID uuid.UUID) (Reconciliation, error) {
	q := s.q(ctx)
	type key struct {
		account uuid.UUID
		side    string
	}
	register := map[key]money.Amount{}
	totals, err := q.RegisterTotals(ctx, companyID)
	if err != nil {
		return Reconciliation{}, err
	}
	for _, t := range totals {
		register[key{t.AccountID, t.Side}] = t.Amount
	}
	cats, err := q.ListCategories(ctx, companyID)
	if err != nil {
		return Reconciliation{}, err
	}
	for _, c := range cats {
		if _, ok := register[key{c.AssetAccountID, "aset"}]; !ok {
			register[key{c.AssetAccountID, "aset"}] = money.Amount{}
		}
		if c.AccumAccountID != nil {
			if _, ok := register[key{*c.AccumAccountID, "akumulasi"}]; !ok {
				register[key{*c.AccumAccountID, "akumulasi"}] = money.Amount{}
			}
		}
	}
	accounts, err := s.Ledger.Accounts(ctx, companyID)
	if err != nil {
		return Reconciliation{}, err
	}
	byID := map[uuid.UUID]struct{ code, name string }{}
	for _, a := range accounts {
		byID[a.ID] = struct{ code, name string }{a.Code, a.Name}
	}
	out := Reconciliation{Rows: []RecRow{}, Matched: true}
	for k, reg := range register {
		net, err := s.Ledger.AccountNet(ctx, companyID, k.account)
		if err != nil {
			return Reconciliation{}, err
		}
		if k.side == "akumulasi" {
			net = net.Neg()
		}
		a := byID[k.account]
		row := RecRow{AccountID: k.account, Code: a.code, Name: a.name, Side: k.side, Register: reg, Ledger: net, Difference: net.Sub(reg)}
		if !row.Difference.IsZero() {
			out.Matched = false
		}
		out.Rows = append(out.Rows, row)
	}
	sort.Slice(out.Rows, func(i, j int) bool { return out.Rows[i].Code < out.Rows[j].Code })
	return out, nil
}

// FiscalRow adalah satu aset di daftar penyusutan fiskal (lampiran SPT Tahunan Badan) beserta
// penyusutan komersialnya, sehingga selisihnya menjadi dasar koreksi fiskal.
type FiscalRow struct {
	AssetID     uuid.UUID    `json:"assetId"`
	Code        string       `json:"code"`
	Name        string       `json:"name"`
	Group       string       `json:"group"`
	GroupLabel  string       `json:"groupLabel"`
	Method      string       `json:"method"`
	AcquiredOn  civil.Date   `json:"acquiredOn"`
	Cost        money.Amount `json:"cost"`
	OpeningBook money.Amount `json:"openingBook"` // nilai sisa buku fiskal awal tahun
	Fiscal      money.Amount `json:"fiscal"`      // penyusutan fiskal tahun ini
	ClosingBook money.Amount `json:"closingBook"` // nilai sisa buku fiskal akhir tahun
	Commercial  money.Amount `json:"commercial"`  // penyusutan komersial tahun ini
	Correction  money.Amount `json:"correction"`  // komersial − fiskal: positif = koreksi positif
	Disposed    bool         `json:"disposed"`
}

type FiscalReport struct {
	Year            int           `json:"year"`
	Rows            []FiscalRow   `json:"rows"`
	TotalFiscal     money.Amount  `json:"totalFiscal"`
	TotalCommercial money.Amount  `json:"totalCommercial"`
	TotalCorrection money.Amount  `json:"totalCorrection"`
	Groups          []FiscalGroup `json:"groups"`
}

// FiscalReport menyusun penyusutan fiskal tahun pajak year untuk aset yang sudah diperoleh
// dan belum dilepas sebelum tahun itu. Penyusutan komersial diambil dari jadwal aset.
func (s *service) FiscalReport(ctx context.Context, companyID uuid.UUID, year int) (FiscalReport, error) {
	out := FiscalReport{Year: year, Rows: []FiscalRow{}, Groups: FiscalGroups}
	rows, err := s.q(ctx).ListAssets(ctx, companyID)
	if err != nil {
		return out, err
	}
	for _, a := range rows {
		if a.AcquiredOn.Year() > year || (a.DisposedOn != nil && a.DisposedOn.Year() < year) {
			continue
		}
		g, _ := fiscalGroup(a.FiscalGroup)
		row := FiscalRow{
			AssetID: a.ID, Code: a.Code, Name: a.Name, Group: a.FiscalGroup, GroupLabel: g.Label, Method: a.FiscalMethod,
			AcquiredOn: a.AcquiredOn, Cost: a.Cost, OpeningBook: a.Cost, ClosingBook: a.Cost, Disposed: a.DisposedOn != nil,
		}
		for _, y := range Fiscal(a.FiscalGroup, a.FiscalMethod, a.Cost, a.DepreciationStart) {
			switch {
			case y.Year < year:
				row.OpeningBook, row.ClosingBook = y.BookValue, y.BookValue
			case y.Year == year:
				row.Fiscal, row.ClosingBook = y.Amount, y.BookValue
			}
		}
		plan := Plan{Method: a.Method, Cost: a.Cost, Residual: a.Residual, LifeMonths: int(a.LifeMonths), Start: a.DepreciationStart,
			OpeningAccum: a.OpeningAccum, OpeningMonth: a.OpeningMonth}
		for _, y := range ByYear(plan.Months()) {
			if y.Year == year {
				row.Commercial = y.Amount
			}
		}
		row.Correction = row.Commercial.Sub(row.Fiscal)
		out.TotalFiscal = out.TotalFiscal.Add(row.Fiscal)
		out.TotalCommercial = out.TotalCommercial.Add(row.Commercial)
		out.TotalCorrection = out.TotalCorrection.Add(row.Correction)
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

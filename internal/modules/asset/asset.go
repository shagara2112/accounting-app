// Package asset adalah modul aset tetap (FA-01–03): register aset per kategori, penyusutan
// bulanan dua buku, dan pelepasan aset.
//
// Buku komersial dijurnal: satu jurnal penyesuaian per bulan (seri PNY) untuk semua aset,
// termasuk bulan-bulan yang belum pernah diposting. Buku fiskal mengikuti kelompok harta PPh
// dan hanya dihitung untuk koreksi fiskal dan lampiran SPT, tanpa jurnal.
//
// Perolehan aset tidak dijurnal di sini, karena aset dibeli lewat tagihan, kas, jurnal, atau
// saldo awal. Register dicocokkan dengan saldo akun aset dan akumulasi penyusutan di buku
// besar, sehingga selisihnya langsung terlihat.
package asset

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/asset/internal/store"
	"lajur/internal/modules/core"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const (
	SourceDepreciation = "asset.depreciation"
	SourceDisposal     = "asset.disposal"

	seriesDepreciation = "PNY"
	seriesDisposal     = "PAT"

	StatusActive   = "aktif"
	StatusDisposed = "dilepas"
)

type Category struct {
	ID               uuid.UUID  `json:"id"`
	Name             string     `json:"name"`
	AssetAccountID   uuid.UUID  `json:"assetAccountId"`
	AccumAccountID   *uuid.UUID `json:"accumAccountId,omitempty"`
	ExpenseAccountID *uuid.UUID `json:"expenseAccountId,omitempty"`
	Method           string     `json:"method"`
	LifeMonths       int        `json:"lifeMonths"`
	FiscalGroup      string     `json:"fiscalGroup"`
	FiscalMethod     string     `json:"fiscalMethod"`
	Active           bool       `json:"active"`
	Version          int        `json:"version"`
}

type CategoryInput struct {
	Name             string     `json:"name"`
	AssetAccountID   uuid.UUID  `json:"assetAccountId"`
	AccumAccountID   *uuid.UUID `json:"accumAccountId"`
	ExpenseAccountID *uuid.UUID `json:"expenseAccountId"`
	Method           string     `json:"method"`
	LifeMonths       int        `json:"lifeMonths"`
	FiscalGroup      string     `json:"fiscalGroup"`
	FiscalMethod     string     `json:"fiscalMethod"`
	Active           bool       `json:"active"`
	Version          int        `json:"version"`
}

// Asset adalah satu baris register.
type Asset struct {
	ID                uuid.UUID    `json:"id"`
	Code              string       `json:"code"`
	Name              string       `json:"name"`
	CategoryID        uuid.UUID    `json:"categoryId"`
	CategoryName      string       `json:"categoryName"`
	AcquiredOn        civil.Date   `json:"acquiredOn"`
	Cost              money.Amount `json:"cost"`
	Residual          money.Amount `json:"residual"`
	Method            string       `json:"method"`
	LifeMonths        int          `json:"lifeMonths"`
	DepreciationStart civil.Date   `json:"depreciationStart"`
	// Accumulated = saldo awal + penyusutan yang sudah dijurnal; BookValue = Cost − Accumulated.
	Accumulated  money.Amount `json:"accumulated"`
	BookValue    money.Amount `json:"bookValue"`
	Location     string       `json:"location"`
	Custodian    string       `json:"custodian"`
	BranchID     *uuid.UUID   `json:"branchId,omitempty"`
	DepartmentID *uuid.UUID   `json:"departmentId,omitempty"`
	Status       string       `json:"status"`
	DisposedOn   *civil.Date  `json:"disposedOn,omitempty"`
	Version      int          `json:"version"`
}

// Posted adalah penyusutan satu bulan yang sudah dijurnal.
type Posted struct {
	Month          civil.Date   `json:"month"`
	Amount         money.Amount `json:"amount"`
	Number         string       `json:"number"`
	JournalEntryID uuid.UUID    `json:"journalEntryId"`
	Disposal       bool         `json:"disposal"` // penyusutan susulan di jurnal pelepasan
}

type AssetDetail struct {
	Asset
	AssetAccountID   uuid.UUID    `json:"assetAccountId"`
	AccumAccountID   *uuid.UUID   `json:"accumAccountId,omitempty"`
	ExpenseAccountID *uuid.UUID   `json:"expenseAccountId,omitempty"`
	FiscalGroup      string       `json:"fiscalGroup"`
	FiscalMethod     string       `json:"fiscalMethod"`
	OpeningAccum     money.Amount `json:"openingAccum"`
	OpeningMonth     *civil.Date  `json:"openingMonth,omitempty"`
	Notes            string       `json:"notes"`
	BranchCode       string       `json:"branchCode,omitempty"`
	DepartmentCode   string       `json:"departmentCode,omitempty"`
	CreatedBy        *string      `json:"createdBy,omitempty"`
	CreatedAt        time.Time    `json:"createdAt"`
	// Jadwal komersial per tahun (sesudah saldo awal) dan fiskal per tahun pajak.
	Schedule []Year `json:"schedule"`
	Fiscal   []Year `json:"fiscal"`
	// NextAmount adalah penyusutan bulan berikutnya yang belum dijurnal (0 bila sudah habis).
	NextMonth  *civil.Date  `json:"nextMonth,omitempty"`
	NextAmount money.Amount `json:"nextAmount"`
	Posted     []Posted     `json:"posted"`
	Disposals  []Disposal   `json:"disposals"`
	// Locked: nilai, umur, metode, dan akun tidak bisa diubah lagi karena penyusutannya
	// sudah dijurnal atau asetnya sudah dilepas.
	Locked bool `json:"locked"`
}

type AssetInput struct {
	Code              string       `json:"code"` // kosong = nomor otomatis AT-0001
	Name              string       `json:"name"`
	CategoryID        uuid.UUID    `json:"categoryId"`
	AcquiredOn        civil.Date   `json:"acquiredOn"`
	Cost              money.Amount `json:"cost"`
	Residual          money.Amount `json:"residual"`
	Method            string       `json:"method"`     // kosong = dari kategori
	LifeMonths        int          `json:"lifeMonths"` // 0 = dari kategori
	DepreciationStart civil.Date   `json:"depreciationStart"`
	AssetAccountID    *uuid.UUID   `json:"assetAccountId"`
	AccumAccountID    *uuid.UUID   `json:"accumAccountId"`
	ExpenseAccountID  *uuid.UUID   `json:"expenseAccountId"`
	FiscalGroup       string       `json:"fiscalGroup"`
	FiscalMethod      string       `json:"fiscalMethod"`
	OpeningAccum      money.Amount `json:"openingAccum"`
	OpeningMonth      *civil.Date  `json:"openingMonth"`
	Location          string       `json:"location"`
	Custodian         string       `json:"custodian"`
	Notes             string       `json:"notes"`
	BranchID          *uuid.UUID   `json:"branchId"`
	DepartmentID      *uuid.UUID   `json:"departmentId"`
	Version           int          `json:"version"`
}

type Service interface {
	Categories(ctx context.Context, companyID uuid.UUID) ([]Category, error)
	SaveCategory(ctx context.Context, companyID uuid.UUID, id *uuid.UUID, in CategoryInput, actor audit.Actor) (Category, error)
	// DefaultCategories membuat kategori bawaan dari akun template (tanah, bangunan, kendaraan,
	// peralatan) yang ada di bagan akun PT; kategori yang namanya sudah ada dilewati.
	DefaultCategories(ctx context.Context, companyID uuid.UUID, actor audit.Actor) ([]Category, error)

	Assets(ctx context.Context, companyID uuid.UUID) ([]Asset, error)
	Asset(ctx context.Context, companyID, id uuid.UUID) (AssetDetail, error)
	CreateAsset(ctx context.Context, companyID uuid.UUID, in AssetInput, actor audit.Actor) (AssetDetail, error)
	UpdateAsset(ctx context.Context, companyID, id uuid.UUID, in AssetInput, actor audit.Actor) (AssetDetail, error)
	DeleteAsset(ctx context.Context, companyID, id uuid.UUID, version int, actor audit.Actor) error
	// Preview menghitung jadwal penyusutan isian formulir tanpa menyimpannya.
	Preview(ctx context.Context, companyID uuid.UUID, in AssetInput) (SchedulePreview, error)

	Runs(ctx context.Context, companyID uuid.UUID) ([]Run, error)
	Run(ctx context.Context, companyID, id uuid.UUID) (RunDetail, error)
	PreviewRun(ctx context.Context, companyID uuid.UUID, month civil.Date) (RunPreview, error)
	PostRun(ctx context.Context, companyID uuid.UUID, month civil.Date, actor audit.Actor) (RunDetail, error)
	VoidRun(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (RunDetail, error)
	// RunDue memposting penyusutan bulan lalu untuk semua PT yang belum memostingnya
	// (dipanggil penjadwal). Mengembalikan jumlah jurnal yang dibuat.
	RunDue(ctx context.Context, today civil.Date) (int, error)

	Dispose(ctx context.Context, companyID, assetID uuid.UUID, in DisposalInput, actor audit.Actor) (AssetDetail, error)
	// DisposalAsset membuka aset dari jurnal pelepasannya (sumber jurnal = id pelepasan).
	DisposalAsset(ctx context.Context, companyID, disposalID uuid.UUID) (AssetDetail, error)
	VoidDisposal(ctx context.Context, companyID, disposalID uuid.UUID, date civil.Date, reason string, actor audit.Actor) (AssetDetail, error)

	Reconciliation(ctx context.Context, companyID uuid.UUID) (Reconciliation, error)
	FiscalReport(ctx context.Context, companyID uuid.UUID, year int) (FiscalReport, error)
}

type Deps struct {
	Pool   *pgxpool.Pool
	Ledger ledger.Service
	Core   core.Service
}

type service struct{ Deps }

func New(d Deps) Service { return &service{d} }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func firstMessage(f map[string]string) string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return f[keys[0]]
}

func invalid(code string, f map[string]string) error {
	return httpx.Unprocessable(code, firstMessage(f)).WithFields(f)
}

// withField menempelkan galat akun dari buku besar ke field formulirnya.
func withField(err error, field string) error {
	var p *httpx.Problem
	if errors.As(err, &p) && p.Status == 422 {
		return httpx.Unprocessable(p.Code, p.Detail).WithFields(map[string]string{field: p.Detail})
	}
	return err
}

func validMethod(m string) bool { return m == Straight || m == Declining || m == None }

// Kategori ------------------------------------------------------------------------------

func toCategory(c store.AssetCategory) Category {
	return Category{
		ID: c.ID, Name: c.Name, AssetAccountID: c.AssetAccountID, AccumAccountID: c.AccumAccountID, ExpenseAccountID: c.ExpenseAccountID,
		Method: c.Method, LifeMonths: int(c.LifeMonths), FiscalGroup: c.FiscalGroup, FiscalMethod: c.FiscalMethod,
		Active: c.Active, Version: int(c.Version),
	}
}

func (s *service) Categories(ctx context.Context, companyID uuid.UUID) ([]Category, error) {
	rows, err := s.q(ctx).ListCategories(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Category, len(rows))
	for i, r := range rows {
		out[i] = toCategory(r)
	}
	return out, nil
}

// checkAccounts memeriksa akun aset (kepala 1), akumulasi (kepala 1), dan beban penyusutan
// (kepala 5, 6, atau 8) untuk metode yang disusutkan.
func (s *service) checkAccounts(ctx context.Context, companyID uuid.UUID, method string, asset uuid.UUID, accum, expense *uuid.UUID, f map[string]string) error {
	check := func(id uuid.UUID, field string, classes []int) error {
		if err := s.Ledger.CheckAccount(ctx, companyID, id, classes); err != nil {
			var p *httpx.Problem
			if !errors.As(err, &p) {
				return err
			}
			f[field] = p.Detail
		}
		return nil
	}
	if asset == uuid.Nil {
		f["assetAccountId"] = "Pilih akun aset."
	} else if err := check(asset, "assetAccountId", []int{1}); err != nil {
		return err
	}
	if method == None {
		return nil
	}
	if accum == nil {
		f["accumAccountId"] = "Pilih akun akumulasi penyusutan."
	} else if err := check(*accum, "accumAccountId", []int{1}); err != nil {
		return err
	}
	if expense == nil {
		f["expenseAccountId"] = "Pilih akun beban penyusutan."
	} else if err := check(*expense, "expenseAccountId", []int{5, 6, 8}); err != nil {
		return err
	}
	if accum != nil && *accum == asset {
		f["accumAccountId"] = "Akun akumulasi harus berbeda dari akun aset."
	}
	return nil
}

func checkFiscal(group, method string, f map[string]string) {
	g, ok := fiscalGroup(group)
	switch {
	case !ok:
		f["fiscalGroup"] = "Pilih kelompok harta fiskal."
	case method != Straight && method != Declining:
		f["fiscalMethod"] = "Pilih metode fiskal garis lurus atau saldo menurun."
	case method == Declining && !g.Declining:
		f["fiscalMethod"] = g.Label + " hanya boleh disusutkan garis lurus secara fiskal."
	}
}

func (s *service) SaveCategory(ctx context.Context, companyID uuid.UUID, id *uuid.UUID, in CategoryInput, actor audit.Actor) (Category, error) {
	in.Name = strings.TrimSpace(in.Name)
	f := map[string]string{}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 100 {
		f["name"] = "Nama kategori wajib diisi (maksimal 100 karakter)."
	}
	if !validMethod(in.Method) {
		f["method"] = "Pilih metode penyusutan."
	}
	if in.Method == None {
		in.LifeMonths, in.AccumAccountID, in.ExpenseAccountID = 0, nil, nil
	} else if in.LifeMonths < 1 || in.LifeMonths > 600 {
		f["lifeMonths"] = "Umur manfaat 1–50 tahun."
	}
	checkFiscal(in.FiscalGroup, in.FiscalMethod, f)
	var out Category
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if err := s.checkAccounts(ctx, companyID, in.Method, in.AssetAccountID, in.AccumAccountID, in.ExpenseAccountID, f); err != nil {
			return err
		}
		if len(f) > 0 {
			return invalid("category.invalid", f)
		}
		q := s.q(ctx)
		cid := uuid.Must(uuid.NewV7())
		if id == nil {
			if err := q.InsertCategory(ctx, store.InsertCategoryParams{
				ID: cid, CompanyID: companyID, Name: in.Name, AssetAccountID: in.AssetAccountID, AccumAccountID: in.AccumAccountID,
				ExpenseAccountID: in.ExpenseAccountID, Method: in.Method, LifeMonths: int32(in.LifeMonths),
				FiscalGroup: in.FiscalGroup, FiscalMethod: in.FiscalMethod,
			}); err != nil {
				return nameTaken(err)
			}
		} else {
			cid = *id
			n, err := q.UpdateCategory(ctx, store.UpdateCategoryParams{
				CompanyID: companyID, ID: cid, Name: in.Name, AssetAccountID: in.AssetAccountID, AccumAccountID: in.AccumAccountID,
				ExpenseAccountID: in.ExpenseAccountID, Method: in.Method, LifeMonths: int32(in.LifeMonths),
				FiscalGroup: in.FiscalGroup, FiscalMethod: in.FiscalMethod, Active: in.Active, Expected: int32(in.Version),
			})
			if err != nil {
				return nameTaken(err)
			}
			if n != 1 {
				return httpx.Conflict("conflict.version", "Kategori ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
			}
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "asset.category.save", ObjectType: "asset_category",
			ObjectID: cid.String(), IP: actor.IP, After: map[string]any{"name": in.Name, "method": in.Method, "lifeMonths": in.LifeMonths},
		}); err != nil {
			return err
		}
		c, err := q.GetCategory(ctx, store.GetCategoryParams{CompanyID: companyID, ID: cid})
		out = toCategory(c)
		return err
	})
	return out, err
}

func nameTaken(err error) error {
	if db.IsUniqueViolation(err) {
		msg := "Nama kategori sudah dipakai."
		return httpx.Unprocessable("category.duplicate", msg).WithFields(map[string]string{"name": msg})
	}
	return err
}

// defaults: kategori bawaan dari akun template bagan akun (lihat ledger/coa.go). Umur komersial
// dan kelompok fiskal adalah titik awal yang umum; periksa dengan konsultan pajak Anda.
var defaults = []struct {
	name                  string
	asset, accum, expense string
	method                string
	life                  int
	group                 string
}{
	{"Tanah", "1-2101", "", "", None, 0, "tidak_disusutkan"},
	{"Bangunan", "1-2102", "1-2202", "6-3002", Straight, 240, "bangunan_permanen"},
	{"Kendaraan", "1-2103", "1-2203", "6-3003", Straight, 96, "kelompok_2"},
	{"Peralatan kantor", "1-2104", "1-2204", "6-3004", Straight, 48, "kelompok_1"},
	{"Alat berat", "1-2105", "1-2205", "6-3005", Straight, 96, "kelompok_2"},
	{"Peralatan toko", "1-2106", "1-2206", "6-3006", Straight, 48, "kelompok_1"},
}

func (s *service) DefaultCategories(ctx context.Context, companyID uuid.UUID, actor audit.Actor) ([]Category, error) {
	var out []Category
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		existing, err := q.ListCategories(ctx, companyID)
		if err != nil {
			return err
		}
		taken := map[string]bool{}
		for _, c := range existing {
			taken[strings.ToLower(c.Name)] = true
		}
		var codes []string
		for _, d := range defaults {
			codes = append(codes, d.asset, d.accum, d.expense)
		}
		rows, err := q.AccountIDsByCode(ctx, store.AccountIDsByCodeParams{CompanyID: companyID, Codes: codes})
		if err != nil {
			return err
		}
		byCode := map[string]uuid.UUID{}
		for _, r := range rows {
			byCode[r.Code] = r.ID
		}
		ref := func(code string) *uuid.UUID {
			if id, ok := byCode[code]; ok {
				return &id
			}
			return nil
		}
		created := 0
		for _, d := range defaults {
			asset, ok := byCode[d.asset]
			if !ok || taken[strings.ToLower(d.name)] {
				continue
			}
			accum, expense := ref(d.accum), ref(d.expense)
			if d.method != None && (accum == nil || expense == nil) {
				continue
			}
			if err := q.InsertCategory(ctx, store.InsertCategoryParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, Name: d.name, AssetAccountID: asset, AccumAccountID: accum,
				ExpenseAccountID: expense, Method: d.method, LifeMonths: int32(d.life), FiscalGroup: d.group, FiscalMethod: Straight,
			}); err != nil {
				return err
			}
			created++
		}
		if created == 0 && len(existing) == 0 {
			return httpx.Unprocessable("category.no_accounts", "Bagan akun PT ini tidak punya akun aset tetap bawaan (1-21xx). Buat kategori sendiri dengan akun Anda.")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "asset.category.defaults", ObjectType: "asset_category",
			ObjectID: companyID.String(), IP: actor.IP, After: map[string]any{"created": created},
		}); err != nil {
			return err
		}
		out, err = s.Categories(ctx, companyID)
		return err
	})
	return out, err
}

// Aset ---------------------------------------------------------------------------------

func planOf(a store.AssetAsset) Plan {
	return Plan{
		Method: a.Method, Cost: a.Cost, Residual: a.Residual, LifeMonths: int(a.LifeMonths), Start: a.DepreciationStart,
		OpeningAccum: a.OpeningAccum, OpeningMonth: a.OpeningMonth,
	}
}

func (s *service) Assets(ctx context.Context, companyID uuid.UUID) ([]Asset, error) {
	rows, err := s.q(ctx).ListAssets(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Asset, len(rows))
	for i, r := range rows {
		accum := r.OpeningAccum.Add(r.Depreciated)
		out[i] = Asset{
			ID: r.ID, Code: r.Code, Name: r.Name, CategoryID: r.CategoryID, CategoryName: r.CategoryName, AcquiredOn: r.AcquiredOn,
			Cost: r.Cost, Residual: r.Residual, Method: r.Method, LifeMonths: int(r.LifeMonths), DepreciationStart: r.DepreciationStart,
			Accumulated: accum, BookValue: r.Cost.Sub(accum), Location: r.Location, Custodian: r.Custodian,
			BranchID: r.BranchID, DepartmentID: r.DepartmentID, Status: r.Status, DisposedOn: r.DisposedOn, Version: int(r.Version),
		}
	}
	return out, nil
}

func (s *service) Asset(ctx context.Context, companyID, id uuid.UUID) (AssetDetail, error) {
	q := s.q(ctx)
	r, err := q.GetAsset(ctx, store.GetAssetParams{CompanyID: companyID, ID: id})
	if isNoRows(err) {
		return AssetDetail{}, httpx.NotFound("asset.not_found", "Aset tidak ditemukan.")
	}
	if err != nil {
		return AssetDetail{}, err
	}
	a := store.AssetAsset{
		ID: r.ID, CompanyID: r.CompanyID, Code: r.Code, Name: r.Name, CategoryID: r.CategoryID, AcquiredOn: r.AcquiredOn, Cost: r.Cost,
		Residual: r.Residual, Method: r.Method, LifeMonths: r.LifeMonths, DepreciationStart: r.DepreciationStart,
		OpeningAccum: r.OpeningAccum, OpeningMonth: r.OpeningMonth,
	}
	lines, err := q.AssetDepreciation(ctx, store.AssetDepreciationParams{CompanyID: companyID, AssetID: id})
	if err != nil {
		return AssetDetail{}, err
	}
	accum := r.OpeningAccum
	posted := make([]Posted, len(lines))
	done := map[civil.Date]bool{}
	for i, l := range lines {
		accum = accum.Add(l.Amount)
		done[l.Month] = true
		posted[i] = Posted{Month: l.Month, Amount: l.Amount, Number: l.Number, JournalEntryID: l.JournalEntryID, Disposal: l.DisposalID != nil}
	}
	months := planOf(a).Months()
	out := AssetDetail{
		Asset: Asset{
			ID: r.ID, Code: r.Code, Name: r.Name, CategoryID: r.CategoryID, CategoryName: r.CategoryName, AcquiredOn: r.AcquiredOn,
			Cost: r.Cost, Residual: r.Residual, Method: r.Method, LifeMonths: int(r.LifeMonths), DepreciationStart: r.DepreciationStart,
			Accumulated: accum, BookValue: r.Cost.Sub(accum), Location: r.Location, Custodian: r.Custodian,
			BranchID: r.BranchID, DepartmentID: r.DepartmentID, Status: r.Status, DisposedOn: r.DisposedOn, Version: int(r.Version),
		},
		AssetAccountID: r.AssetAccountID, AccumAccountID: r.AccumAccountID, ExpenseAccountID: r.ExpenseAccountID,
		FiscalGroup: r.FiscalGroup, FiscalMethod: r.FiscalMethod, OpeningAccum: r.OpeningAccum, OpeningMonth: r.OpeningMonth,
		Notes: r.Notes, BranchCode: deref(r.BranchCode), DepartmentCode: deref(r.DepartmentCode), CreatedBy: r.CreatedByName, CreatedAt: r.CreatedAt,
		Schedule: ByYear(months), Fiscal: Fiscal(r.FiscalGroup, r.FiscalMethod, r.Cost, r.DepreciationStart),
		Posted: posted, Disposals: []Disposal{}, Locked: len(lines) > 0 || r.Status == StatusDisposed,
	}
	if r.Status == StatusActive {
		for _, m := range months {
			if !done[m.Month] {
				mm := m.Month
				out.NextMonth, out.NextAmount = &mm, m.Amount
				break
			}
		}
	}
	ds, err := q.AssetDisposals(ctx, store.AssetDisposalsParams{CompanyID: companyID, AssetID: id})
	if err != nil {
		return AssetDetail{}, err
	}
	for _, d := range ds {
		out.Disposals = append(out.Disposals, toDisposal(d))
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var codePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,29}$`)

// normalize melengkapi isian dari kategori dan memeriksa semuanya. Mengembalikan kategori.
func (s *service) normalize(ctx context.Context, companyID uuid.UUID, in *AssetInput, id uuid.UUID, isNew bool) error {
	in.Code, in.Name = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name)
	in.Location, in.Custodian, in.Notes = strings.TrimSpace(in.Location), strings.TrimSpace(in.Custodian), strings.TrimSpace(in.Notes)
	f := map[string]string{}
	q := s.q(ctx)
	if in.Code != "" {
		if !codePattern.MatchString(in.Code) {
			f["code"] = "Kode 1–30 karakter: huruf, angka, titik, garis miring, atau tanda hubung."
		} else if taken, err := q.AssetCodeTaken(ctx, store.AssetCodeTakenParams{CompanyID: companyID, Code: in.Code, Exclude: id}); err != nil {
			return err
		} else if taken {
			f["code"] = "Kode aset sudah dipakai."
		}
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 200 {
		f["name"] = "Nama aset wajib diisi (maksimal 200 karakter)."
	}
	if utf8.RuneCountInString(in.Location) > 200 || utf8.RuneCountInString(in.Custodian) > 200 {
		f["location"] = "Lokasi dan penanggung jawab maksimal 200 karakter."
	}
	if utf8.RuneCountInString(in.Notes) > 1000 {
		f["notes"] = "Catatan maksimal 1000 karakter."
	}
	cat, err := q.GetCategory(ctx, store.GetCategoryParams{CompanyID: companyID, ID: in.CategoryID})
	switch {
	case isNoRows(err):
		f["categoryId"] = "Pilih kategori aset."
		return invalid("asset.invalid", f)
	case err != nil:
		return err
	case !cat.Active && isNew:
		f["categoryId"] = fmt.Sprintf("Kategori %s nonaktif.", cat.Name)
	}
	if in.Method == "" {
		in.Method = cat.Method
	}
	if in.LifeMonths == 0 {
		in.LifeMonths = int(cat.LifeMonths)
	}
	if in.AssetAccountID == nil {
		in.AssetAccountID = &cat.AssetAccountID
	}
	if in.AccumAccountID == nil {
		in.AccumAccountID = cat.AccumAccountID
	}
	if in.ExpenseAccountID == nil {
		in.ExpenseAccountID = cat.ExpenseAccountID
	}
	if in.FiscalGroup == "" {
		in.FiscalGroup = cat.FiscalGroup
	}
	if in.FiscalMethod == "" {
		in.FiscalMethod = cat.FiscalMethod
	}
	if !validMethod(in.Method) {
		f["method"] = "Pilih metode penyusutan."
	}
	if in.Method == None {
		in.LifeMonths, in.Residual, in.AccumAccountID, in.ExpenseAccountID = 0, money.Amount{}, nil, nil
		in.OpeningAccum, in.OpeningMonth = money.Amount{}, nil
	} else if in.LifeMonths < 1 || in.LifeMonths > 600 {
		f["lifeMonths"] = "Umur manfaat 1–50 tahun."
	}
	checkFiscal(in.FiscalGroup, in.FiscalMethod, f)
	if in.AcquiredOn.IsZero() {
		f["acquiredOn"] = "Tanggal perolehan wajib diisi."
	}
	if in.Cost.Sign() <= 0 {
		f["cost"] = "Harga perolehan harus lebih dari 0."
	}
	if in.Residual.Sign() < 0 || in.Residual.Cmp(in.Cost) >= 0 && in.Cost.Sign() > 0 && in.Method != None {
		f["residual"] = "Nilai sisa harus 0 atau lebih kecil dari harga perolehan."
	}
	if !in.AcquiredOn.IsZero() {
		if in.DepreciationStart.IsZero() {
			in.DepreciationStart = in.AcquiredOn.MonthStart()
		}
		in.DepreciationStart = in.DepreciationStart.MonthStart()
		if in.DepreciationStart.Before(in.AcquiredOn.MonthStart()) {
			f["depreciationStart"] = "Penyusutan tidak bisa dimulai sebelum bulan perolehan."
		}
	}
	if in.OpeningAccum.Sign() < 0 {
		f["openingAccum"] = "Akumulasi penyusutan awal tidak boleh negatif."
	}
	if in.OpeningAccum.Sign() > 0 {
		switch {
		case in.OpeningMonth == nil:
			f["openingMonth"] = "Isi bulan terakhir yang sudah termasuk dalam akumulasi awal."
		case in.OpeningAccum.Cmp(in.Cost.Sub(in.Residual)) > 0:
			f["openingAccum"] = "Akumulasi awal melebihi nilai yang bisa disusutkan."
		case !in.DepreciationStart.IsZero() && in.OpeningMonth.MonthStart().Before(in.DepreciationStart):
			f["openingMonth"] = "Bulan akumulasi awal tidak boleh sebelum bulan mulai penyusutan."
		}
		if in.OpeningMonth != nil {
			m := in.OpeningMonth.MonthStart()
			in.OpeningMonth = &m
		}
	} else {
		in.OpeningMonth = nil
	}
	if err := s.checkAccounts(ctx, companyID, in.Method, *in.AssetAccountID, in.AccumAccountID, in.ExpenseAccountID, f); err != nil {
		return err
	}
	if len(f) > 0 {
		return invalid("asset.invalid", f)
	}
	return s.Core.CheckDimensions(ctx, companyID, in.BranchID, in.DepartmentID)
}

func (s *service) nextCode(ctx context.Context, companyID uuid.UUID) (string, error) {
	q := s.q(ctx)
	n, err := q.CountAssets(ctx, companyID)
	if err != nil {
		return "", err
	}
	for i := n + 1; ; i++ {
		code := fmt.Sprintf("AT-%04d", i)
		taken, err := q.AssetCodeTaken(ctx, store.AssetCodeTakenParams{CompanyID: companyID, Code: code, Exclude: uuid.Nil})
		if err != nil || !taken {
			return code, err
		}
	}
}

func (s *service) CreateAsset(ctx context.Context, companyID uuid.UUID, in AssetInput, actor audit.Actor) (AssetDetail, error) {
	var out AssetDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if err := s.normalize(ctx, companyID, &in, uuid.Nil, true); err != nil {
			return err
		}
		if in.Code == "" {
			code, err := s.nextCode(ctx, companyID)
			if err != nil {
				return err
			}
			in.Code = code
		}
		id := uuid.Must(uuid.NewV7())
		if err := s.q(ctx).InsertAsset(ctx, store.InsertAssetParams{
			ID: id, CompanyID: companyID, Code: in.Code, Name: in.Name, CategoryID: in.CategoryID, AcquiredOn: in.AcquiredOn,
			Cost: in.Cost, Residual: in.Residual, Method: in.Method, LifeMonths: int32(in.LifeMonths), DepreciationStart: in.DepreciationStart,
			AssetAccountID: *in.AssetAccountID, AccumAccountID: in.AccumAccountID, ExpenseAccountID: in.ExpenseAccountID,
			FiscalGroup: in.FiscalGroup, FiscalMethod: in.FiscalMethod, OpeningAccum: in.OpeningAccum, OpeningMonth: in.OpeningMonth,
			Location: in.Location, Custodian: in.Custodian, Notes: in.Notes, BranchID: in.BranchID, DepartmentID: in.DepartmentID,
			CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "asset.create", ObjectType: "fixed_asset", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"code": in.Code, "name": in.Name, "cost": in.Cost.String(), "acquiredOn": in.AcquiredOn.String(), "method": in.Method, "lifeMonths": in.LifeMonths},
		}); err != nil {
			return err
		}
		var err error
		out, err = s.Asset(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) UpdateAsset(ctx context.Context, companyID, id uuid.UUID, in AssetInput, actor audit.Actor) (AssetDetail, error) {
	var out AssetDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := q.GetAssetForUpdate(ctx, store.GetAssetForUpdateParams{CompanyID: companyID, ID: id})
		if isNoRows(err) {
			return httpx.NotFound("asset.not_found", "Aset tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if int(cur.Version) != in.Version {
			return httpx.Conflict("conflict.version", "Aset ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if in.Code == "" {
			in.Code = cur.Code
		}
		if err := s.normalize(ctx, companyID, &in, id, false); err != nil {
			return err
		}
		lines, err := q.RecordedMonths(ctx, store.RecordedMonthsParams{CompanyID: companyID, Ids: []uuid.UUID{id}})
		if err != nil {
			return err
		}
		// Sesudah penyusutan dijurnal, angka dan akun yang membentuk jurnal itu terkunci.
		if len(lines) > 0 || cur.Status == StatusDisposed {
			same := in.CategoryID == cur.CategoryID && in.AcquiredOn == cur.AcquiredOn && in.Cost.Equal(cur.Cost) &&
				in.Residual.Equal(cur.Residual) && in.Method == cur.Method && in.LifeMonths == int(cur.LifeMonths) &&
				in.DepreciationStart == cur.DepreciationStart && *in.AssetAccountID == cur.AssetAccountID &&
				sameID(in.AccumAccountID, cur.AccumAccountID) && sameID(in.ExpenseAccountID, cur.ExpenseAccountID) &&
				in.OpeningAccum.Equal(cur.OpeningAccum) && sameDate(in.OpeningMonth, cur.OpeningMonth)
			if !same {
				return httpx.Conflict("asset.locked", "Penyusutan aset ini sudah dijurnal, sehingga kategori, nilai, umur, metode, dan akunnya tidak bisa diubah lagi. Void jurnal penyusutannya dulu bila perlu koreksi.")
			}
		}
		n, err := q.UpdateAsset(ctx, store.UpdateAssetParams{
			CompanyID: companyID, ID: id, Code: in.Code, Name: in.Name, CategoryID: in.CategoryID, AcquiredOn: in.AcquiredOn,
			Cost: in.Cost, Residual: in.Residual, Method: in.Method, LifeMonths: int32(in.LifeMonths), DepreciationStart: in.DepreciationStart,
			AssetAccountID: *in.AssetAccountID, AccumAccountID: in.AccumAccountID, ExpenseAccountID: in.ExpenseAccountID,
			FiscalGroup: in.FiscalGroup, FiscalMethod: in.FiscalMethod, OpeningAccum: in.OpeningAccum, OpeningMonth: in.OpeningMonth,
			Location: in.Location, Custodian: in.Custodian, Notes: in.Notes, BranchID: in.BranchID, DepartmentID: in.DepartmentID,
			Expected: int32(in.Version),
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Aset ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "asset.update", ObjectType: "fixed_asset", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"code": cur.Code, "name": cur.Name, "cost": cur.Cost.String(), "lifeMonths": cur.LifeMonths},
			After:  map[string]any{"code": in.Code, "name": in.Name, "cost": in.Cost.String(), "lifeMonths": in.LifeMonths},
		}); err != nil {
			return err
		}
		out, err = s.Asset(ctx, companyID, id)
		return err
	})
	return out, err
}

func sameID(a, b *uuid.UUID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameDate(a, b *civil.Date) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (s *service) DeleteAsset(ctx context.Context, companyID, id uuid.UUID, version int, actor audit.Actor) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := q.GetAssetForUpdate(ctx, store.GetAssetForUpdateParams{CompanyID: companyID, ID: id})
		if isNoRows(err) {
			return httpx.NotFound("asset.not_found", "Aset tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		lines, err := q.RecordedMonths(ctx, store.RecordedMonthsParams{CompanyID: companyID, Ids: []uuid.UUID{id}})
		if err != nil {
			return err
		}
		ds, err := q.AssetDisposals(ctx, store.AssetDisposalsParams{CompanyID: companyID, AssetID: id})
		if err != nil {
			return err
		}
		if len(lines) > 0 || len(ds) > 0 {
			return httpx.Conflict("asset.in_use", fmt.Sprintf("%s sudah punya jurnal penyusutan atau pelepasan, sehingga tidak bisa dihapus. Lepas asetnya bila sudah tidak dipakai.", cur.Code))
		}
		n, err := q.DeleteAsset(ctx, store.DeleteAssetParams{CompanyID: companyID, ID: id, Expected: int32(version)})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Aset ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "asset.delete", ObjectType: "fixed_asset", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"code": cur.Code, "name": cur.Name, "cost": cur.Cost.String()},
		})
		return err
	})
}

// SchedulePreview adalah jadwal penyusutan isian formulir sebelum disimpan.
type SchedulePreview struct {
	Monthly  money.Amount `json:"monthly"` // penyusutan bulan pertama
	Schedule []Year       `json:"schedule"`
	Fiscal   []Year       `json:"fiscal"`
}

func (s *service) Preview(ctx context.Context, companyID uuid.UUID, in AssetInput) (SchedulePreview, error) {
	if err := s.normalize(ctx, companyID, &in, uuid.Nil, false); err != nil {
		var p *httpx.Problem
		if errors.As(err, &p) && p.Code == "asset.invalid" {
			// Pratinjau tetap dihitung dari isian yang sudah lengkap; galat lain ditampilkan saat simpan.
			if in.Cost.Sign() <= 0 || in.AcquiredOn.IsZero() {
				return SchedulePreview{Schedule: []Year{}, Fiscal: []Year{}}, nil
			}
		} else {
			return SchedulePreview{}, err
		}
	}
	plan := Plan{Method: in.Method, Cost: in.Cost, Residual: in.Residual, LifeMonths: in.LifeMonths, Start: in.DepreciationStart,
		OpeningAccum: in.OpeningAccum, OpeningMonth: in.OpeningMonth}
	months := plan.Months()
	out := SchedulePreview{Schedule: ByYear(months), Fiscal: Fiscal(in.FiscalGroup, in.FiscalMethod, in.Cost, in.DepreciationStart)}
	if len(months) > 0 {
		out.Monthly = months[0].Amount
	}
	if out.Schedule == nil {
		out.Schedule = []Year{}
	}
	if out.Fiscal == nil {
		out.Fiscal = []Year{}
	}
	return out, nil
}

// Package budget adalah anggaran tahunan (BUD-01) dan laporan anggaran vs realisasi (BUD-02).
// Anggaran disusun per tahun buku dalam satu atau beberapa versi; satu versi aktif dipakai laporan.
// Baris anggaran per akun laba rugi (kelas 4–9), boleh dirinci per departemen atau proyek, dengan
// nominal per bulan tahun buku menurut saldo normal akunnya. Realisasi dibaca langsung dari buku
// besar (saldo agregat, baris jurnal per departemen, atau saldo per proyek), tanpa jurnal penutup.
package budget

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/budget/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// NearPct: realisasi beban setahun berjalan sebesar ini (persen dari anggaran setahun) atau lebih
// ditandai mendekati batas (BUD-02).
const NearPct = 90

type Budget struct {
	ID         uuid.UUID `json:"id"`
	FiscalYear int       `json:"fiscalYear"`
	Name       string    `json:"name"`
	Active     bool      `json:"active"`
	Lines      int       `json:"lines"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Version    int       `json:"version"`
}

// Line adalah satu baris anggaran: akun dan irisannya (tingkat PT, satu departemen, atau satu
// proyek) dengan nominal per bulan tahun buku.
type Line struct {
	AccountID    uuid.UUID      `json:"accountId"`
	DepartmentID *uuid.UUID     `json:"departmentId,omitempty"`
	ProjectID    *uuid.UUID     `json:"projectId,omitempty"`
	Months       []money.Amount `json:"months"` // 12 bulan, mulai bulan pertama tahun buku
	Total        money.Amount   `json:"total"`
}

type Detail struct {
	Budget
	Months []string `json:"months"` // yyyy-mm untuk m01..m12
	Lines  []Line   `json:"lines"`
}

type NewBudget struct {
	FiscalYear int        `json:"fiscalYear"`
	Name       string     `json:"name"`
	CopyFrom   *uuid.UUID `json:"copyFrom,omitempty"` // salin baris dari versi lain (revisi)
}

type Rename struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// SliceInput mengganti semua baris satu irisan anggaran sekaligus.
type SliceInput struct {
	DepartmentID *uuid.UUID  `json:"departmentId,omitempty"`
	ProjectID    *uuid.UUID  `json:"projectId,omitempty"`
	Lines        []LineInput `json:"lines"`
	Version      int         `json:"version"`
}

type LineInput struct {
	AccountID uuid.UUID      `json:"accountId"`
	Months    []money.Amount `json:"months"`
}

// Slice memilih irisan untuk usulan atau laporan: keduanya kosong = seluruh PT.
type Slice struct {
	DepartmentID *uuid.UUID
	ProjectID    *uuid.UUID
}

type Service interface {
	Budgets(ctx context.Context, companyID uuid.UUID) ([]Budget, error)
	Budget(ctx context.Context, companyID, id uuid.UUID) (Detail, error)
	CreateBudget(ctx context.Context, companyID uuid.UUID, in NewBudget, actor audit.Actor) (Budget, error)
	RenameBudget(ctx context.Context, companyID, id uuid.UUID, in Rename, actor audit.Actor) (Budget, error)
	ActivateBudget(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (Budget, error)
	DeleteBudget(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error
	SaveSlice(ctx context.Context, companyID, id uuid.UUID, in SliceInput, actor audit.Actor) (Detail, error)
	// Suggest mengusulkan anggaran dari realisasi tahun buku sebelumnya ditambah growth persen.
	Suggest(ctx context.Context, companyID uuid.UUID, fiscalYear int, s Slice, growth money.Amount) ([]LineInput, error)
	Compare(ctx context.Context, companyID uuid.UUID, f CompareFilter) (Comparison, error)
}

type Deps struct {
	Pool   *pgxpool.Pool
	Ledger ledger.Service
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

// fiscalStart adalah tanggal 1 bulan pertama tahun buku fiscalYear.
func (s *service) fiscalStart(ctx context.Context, companyID uuid.UUID, fiscalYear int) (civil.Date, error) {
	st, err := s.Ledger.Settings(ctx, companyID)
	if err != nil {
		return civil.Date{}, err
	}
	m := st.FiscalYearStartMonth
	if m < 1 || m > 12 {
		m = 1
	}
	return civil.New(fiscalYear, time.Month(m), 1), nil
}

func monthKeys(start civil.Date) []string {
	out := make([]string, 12)
	for i := range out {
		out[i] = start.AddMonths(i).MonthKey()
	}
	return out
}

func toBudget(r store.BudgetBudget, lines int) Budget {
	return Budget{ID: r.ID, FiscalYear: int(r.FiscalYear), Name: r.Name, Active: r.Active, Lines: lines, UpdatedAt: r.UpdatedAt, Version: int(r.Version)}
}

func notFound() error { return httpx.NotFound("budget.not_found", "Anggaran tidak ditemukan.") }

func (s *service) Budgets(ctx context.Context, companyID uuid.UUID) ([]Budget, error) {
	rows, err := s.q(ctx).ListBudgets(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Budget, len(rows))
	for i, r := range rows {
		out[i] = Budget{ID: r.ID, FiscalYear: int(r.FiscalYear), Name: r.Name, Active: r.Active, Lines: int(r.LineCount), UpdatedAt: r.UpdatedAt, Version: int(r.Version)}
	}
	return out, nil
}

func (s *service) Budget(ctx context.Context, companyID, id uuid.UUID) (Detail, error) {
	q := s.q(ctx)
	b, err := q.GetBudget(ctx, store.GetBudgetParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, notFound()
	}
	if err != nil {
		return Detail{}, err
	}
	rows, err := q.BudgetLines(ctx, store.BudgetLinesParams{CompanyID: companyID, BudgetID: id})
	if err != nil {
		return Detail{}, err
	}
	start, err := s.fiscalStart(ctx, companyID, int(b.FiscalYear))
	if err != nil {
		return Detail{}, err
	}
	out := Detail{Budget: toBudget(b, len(rows)), Months: monthKeys(start), Lines: make([]Line, 0, len(rows))}
	for _, r := range rows {
		out.Lines = append(out.Lines, lineOf(r))
	}
	return out, nil
}

func months(r store.BudgetLine) []money.Amount {
	return []money.Amount{r.M01, r.M02, r.M03, r.M04, r.M05, r.M06, r.M07, r.M08, r.M09, r.M10, r.M11, r.M12}
}

func lineOf(r store.BudgetLine) Line {
	l := Line{AccountID: r.AccountID, DepartmentID: r.DepartmentID, ProjectID: r.ProjectID, Months: months(r)}
	for _, m := range l.Months {
		l.Total = l.Total.Add(m)
	}
	return l
}

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 60 {
		msg := "Nama versi wajib diisi (maksimal 60 karakter), mis. Awal atau Revisi 1."
		return "", httpx.Unprocessable("budget.invalid", msg).WithFields(map[string]string{"name": msg})
	}
	return name, nil
}

func nameTaken(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		msg := "Nama versi ini sudah dipakai di tahun buku yang sama."
		return httpx.Conflict("budget.name_taken", msg).WithFields(map[string]string{"name": msg})
	}
	return err
}

// CreateBudget membuat versi anggaran. Versi pertama di satu tahun buku langsung aktif; versi
// berikutnya (revisi) bisa menyalin baris versi lain lalu diaktifkan setelah siap.
func (s *service) CreateBudget(ctx context.Context, companyID uuid.UUID, in NewBudget, actor audit.Actor) (Budget, error) {
	name, err := checkName(in.Name)
	if err != nil {
		return Budget{}, err
	}
	if in.FiscalYear < 2000 || in.FiscalYear > 2100 {
		msg := "Tahun buku tidak valid."
		return Budget{}, httpx.Unprocessable("budget.invalid", msg).WithFields(map[string]string{"fiscalYear": msg})
	}
	var out Budget
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		n, err := q.CountBudgetsInYear(ctx, store.CountBudgetsInYearParams{CompanyID: companyID, FiscalYear: int32(in.FiscalYear)})
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		if err := q.InsertBudget(ctx, store.InsertBudgetParams{
			ID: id, CompanyID: companyID, FiscalYear: int32(in.FiscalYear), Name: name, Active: n == 0, CreatedBy: actor.UserRef(),
		}); err != nil {
			return nameTaken(err)
		}
		if in.CopyFrom != nil {
			if _, err := q.GetBudget(ctx, store.GetBudgetParams{CompanyID: companyID, ID: *in.CopyFrom}); errors.Is(err, pgx.ErrNoRows) {
				return notFound()
			} else if err != nil {
				return err
			}
			if err := q.CopyLines(ctx, store.CopyLinesParams{ToBudget: id, SrcCompanyID: companyID, FromBudget: *in.CopyFrom}); err != nil {
				return err
			}
		}
		b, err := q.GetBudget(ctx, store.GetBudgetParams{CompanyID: companyID, ID: id})
		if err != nil {
			return err
		}
		lines, err := q.BudgetLines(ctx, store.BudgetLinesParams{CompanyID: companyID, BudgetID: id})
		if err != nil {
			return err
		}
		out = toBudget(b, len(lines))
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "budget.create", ObjectType: "budget", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"fiscalYear": in.FiscalYear, "name": name, "active": out.Active, "copyFrom": in.CopyFrom},
		})
		return err
	})
	return out, err
}

func (s *service) RenameBudget(ctx context.Context, companyID, id uuid.UUID, in Rename, actor audit.Actor) (Budget, error) {
	name, err := checkName(in.Name)
	if err != nil {
		return Budget{}, err
	}
	var out Budget
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := q.GetBudgetForUpdate(ctx, store.GetBudgetForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		n, err := q.RenameBudget(ctx, store.RenameBudgetParams{CompanyID: companyID, ID: id, Name: name, Version: int32(in.Version)})
		if err != nil {
			return nameTaken(err)
		}
		if n == 0 {
			return httpx.Conflict("conflict.version", "Anggaran ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		b, err := q.GetBudget(ctx, store.GetBudgetParams{CompanyID: companyID, ID: id})
		if err != nil {
			return err
		}
		out = toBudget(b, 0)
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "budget.rename", ObjectType: "budget", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"name": cur.Name}, After: map[string]any{"name": name},
		})
		return err
	})
	return out, err
}

// ActivateBudget menjadikan satu versi dipakai laporan anggaran vs realisasi tahun bukunya.
func (s *service) ActivateBudget(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (Budget, error) {
	var out Budget
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := q.GetBudgetForUpdate(ctx, store.GetBudgetForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if !cur.Active {
			if err := q.DeactivateYear(ctx, store.DeactivateYearParams{CompanyID: companyID, FiscalYear: cur.FiscalYear}); err != nil {
				return err
			}
			if err := q.ActivateBudget(ctx, store.ActivateBudgetParams{CompanyID: companyID, ID: id}); err != nil {
				return err
			}
		}
		b, err := q.GetBudget(ctx, store.GetBudgetParams{CompanyID: companyID, ID: id})
		if err != nil {
			return err
		}
		out = toBudget(b, 0)
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "budget.activate", ObjectType: "budget", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"fiscalYear": cur.FiscalYear, "name": cur.Name},
		})
		return err
	})
	return out, err
}

// DeleteBudget menghapus versi yang tidak aktif beserta barisnya.
func (s *service) DeleteBudget(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := q.GetBudgetForUpdate(ctx, store.GetBudgetForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if cur.Active {
			return httpx.Conflict("budget.active", "Versi aktif tidak bisa dihapus. Aktifkan versi lain dulu.")
		}
		if _, err := q.DeleteBudget(ctx, store.DeleteBudgetParams{CompanyID: companyID, ID: id}); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "budget.delete", ObjectType: "budget", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"fiscalYear": cur.FiscalYear, "name": cur.Name},
		})
		return err
	})
}

// SaveSlice mengganti semua baris satu irisan (tingkat PT, satu departemen, atau satu proyek).
// Akun harus akun detail laba rugi (kelas 4–9); baris yang semua bulannya nol tidak disimpan.
func (s *service) SaveSlice(ctx context.Context, companyID, id uuid.UUID, in SliceInput, actor audit.Actor) (Detail, error) {
	if in.DepartmentID != nil && in.ProjectID != nil {
		msg := "Pilih departemen atau proyek, tidak keduanya."
		return Detail{}, httpx.Unprocessable("budget.invalid", msg)
	}
	accounts, err := s.Ledger.Accounts(ctx, companyID)
	if err != nil {
		return Detail{}, err
	}
	byID := make(map[uuid.UUID]ledger.Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	fields := map[string]string{}
	seen := map[uuid.UUID]bool{}
	var keep []LineInput
	for _, l := range in.Lines {
		key := "lines." + l.AccountID.String()
		a, ok := byID[l.AccountID]
		switch {
		case !ok:
			fields[key] = "Akun tidak ditemukan di PT ini."
			continue
		case !a.Postable || a.Class < 4:
			fields[key] = fmt.Sprintf("%s %s bukan akun detail laba rugi.", a.Code, a.Name)
			continue
		case len(l.Months) != 12:
			fields[key] = "Isi 12 bulan."
			continue
		case seen[a.ID]:
			fields[key] = fmt.Sprintf("Akun %s diisi dua kali.", a.Code)
			continue
		}
		seen[a.ID] = true
		zero := true
		for _, m := range l.Months {
			if m.Sign() < 0 {
				fields[key] = "Anggaran tidak boleh negatif."
			}
			zero = zero && m.IsZero()
		}
		if !zero {
			keep = append(keep, l)
		}
	}
	if len(fields) > 0 {
		first := ""
		for k := range fields {
			if first == "" || k < first {
				first = k
			}
		}
		return Detail{}, httpx.Unprocessable("budget.invalid", fields[first]).WithFields(fields)
	}

	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		if _, err := q.GetBudgetForUpdate(ctx, store.GetBudgetForUpdateParams{CompanyID: companyID, ID: id}); errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return err
		}
		n, err := q.TouchBudget(ctx, store.TouchBudgetParams{CompanyID: companyID, ID: id, Version: int32(in.Version)})
		if err != nil {
			return err
		}
		if n == 0 {
			return httpx.Conflict("conflict.version", "Anggaran ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if err := q.DeleteSlice(ctx, store.DeleteSliceParams{CompanyID: companyID, BudgetID: id, DepartmentID: in.DepartmentID, ProjectID: in.ProjectID}); err != nil {
			return err
		}
		var total money.Amount
		for _, l := range keep {
			m := l.Months
			for _, v := range m {
				total = total.Add(v)
			}
			if err := q.InsertLine(ctx, store.InsertLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, BudgetID: id, AccountID: l.AccountID,
				DepartmentID: in.DepartmentID, ProjectID: in.ProjectID,
				M01: m[0], M02: m[1], M03: m[2], M04: m[3], M05: m[4], M06: m[5], M07: m[6], M08: m[7], M09: m[8], M10: m[9], M11: m[10], M12: m[11],
			}); err != nil {
				var pg *pgconn.PgError
				if errors.As(err, &pg) && pg.Code == "23503" {
					return httpx.Unprocessable("budget.invalid", "Departemen atau proyek tidak ditemukan di PT ini.")
				}
				return err
			}
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "budget.lines", ObjectType: "budget", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"departmentId": in.DepartmentID, "projectId": in.ProjectID, "lines": len(keep), "total": total.String()},
		})
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Budget(ctx, companyID, id)
}

// actual mengembalikan realisasi akun laba rugi (debit − kredit) per akun per bulan tahun buku
// (indeks 0–11) untuk irisan s.
func (s *service) actual(ctx context.Context, companyID uuid.UUID, start civil.Date, sl Slice) (map[uuid.UUID][12]money.Amount, error) {
	q := s.q(ctx)
	end := start.AddMonths(12)
	type row struct {
		account uuid.UUID
		month   civil.Date
		net     money.Amount
	}
	var rows []row
	switch {
	case sl.DepartmentID != nil:
		rs, err := q.ActualDepartment(ctx, store.ActualDepartmentParams{CompanyID: companyID, FromMonth: start, ToMonth: end, DepartmentID: sl.DepartmentID})
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			rows = append(rows, row{r.AccountID, r.Month, r.Net})
		}
	case sl.ProjectID != nil:
		rs, err := q.ActualProject(ctx, store.ActualProjectParams{CompanyID: companyID, ProjectID: *sl.ProjectID, FromMonth: start, ToMonth: end})
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			rows = append(rows, row{r.AccountID, r.Month, r.Net})
		}
	default:
		rs, err := q.ActualAll(ctx, store.ActualAllParams{CompanyID: companyID, FromMonth: start, ToMonth: end})
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			rows = append(rows, row{r.AccountID, r.Month, r.Net})
		}
	}
	out := map[uuid.UUID][12]money.Amount{}
	for _, r := range rows {
		i := monthIndex(start, r.month)
		if i < 0 || i > 11 {
			continue
		}
		v := out[r.account]
		v[i] = v[i].Add(r.net)
		out[r.account] = v
	}
	return out, nil
}

func monthIndex(start, d civil.Date) int {
	return (d.Year()-start.Year())*12 + int(d.Month()) - int(start.Month())
}

// normal mengubah debit − kredit menjadi nilai menurut saldo normal kelas akun.
func normal(class int, net money.Amount) money.Amount { return ledger.ClassOf(class).Present(net) }

func (s *service) Suggest(ctx context.Context, companyID uuid.UUID, fiscalYear int, sl Slice, growth money.Amount) ([]LineInput, error) {
	if growth.Cmp(money.FromInt(-100)) <= 0 || growth.Cmp(money.FromInt(1000)) > 0 {
		return nil, httpx.Unprocessable("budget.invalid", "Kenaikan harus di antara −99% dan 1.000%.")
	}
	start, err := s.fiscalStart(ctx, companyID, fiscalYear-1)
	if err != nil {
		return nil, err
	}
	act, err := s.actual(ctx, companyID, start, sl)
	if err != nil {
		return nil, err
	}
	accounts, err := s.Ledger.Accounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	hundred := money.FromInt(100)
	factor := hundred.Add(growth)
	out := []LineInput{}
	for _, a := range accounts {
		v, ok := act[a.ID]
		if !ok || !a.Postable || !a.Active || a.Class < 4 {
			continue
		}
		l := LineInput{AccountID: a.ID, Months: make([]money.Amount, 12)}
		filled := false
		for i, n := range v {
			m := normal(a.Class, n)
			if m.Sign() <= 0 {
				continue
			}
			l.Months[i] = m.MulDiv(factor, hundred, 0, money.HalfUp)
			filled = filled || l.Months[i].Sign() > 0
		}
		if filled {
			out = append(out, l)
		}
	}
	return out, nil
}

// CompareFilter: Budget kosong = versi aktif tahun buku FiscalYear. FromMonth/ToMonth adalah
// bulan ke-1 sampai ke-12 tahun buku (inklusif).
type CompareFilter struct {
	FiscalYear int
	BudgetID   *uuid.UUID
	FromMonth  int
	ToMonth    int
	Slice
}

// Row adalah satu akun di laporan anggaran vs realisasi. Semua nilai menurut saldo normal akun.
// Status untuk akun beban dihitung dari realisasi setahun berjalan (s.d. ToMonth) terhadap
// anggaran setahun: near (≥ NearPct%), over (> 100%), unbudgeted (ada realisasi tanpa anggaran).
type Row struct {
	AccountID  uuid.UUID    `json:"accountId"`
	Code       string       `json:"code"`
	Name       string       `json:"name"`
	Budget     money.Amount `json:"budget"`
	Actual     money.Amount `json:"actual"`
	Variance   money.Amount `json:"variance"` // realisasi − anggaran
	Pct        *float64     `json:"pct,omitempty"`
	YearBudget money.Amount `json:"yearBudget"`
	YearActual money.Amount `json:"yearActual"` // awal tahun buku s.d. ToMonth
	Remaining  money.Amount `json:"remaining"`  // anggaran setahun − realisasi setahun berjalan
	Status     string       `json:"status,omitempty"`
}

type Section struct {
	Class   int    `json:"class"`
	Name    string `json:"name"`
	Expense bool   `json:"expense"`
	Rows    []Row  `json:"rows"`
	Total   Row    `json:"total"`
}

type MonthPoint struct {
	Month         string       `json:"month"`
	BudgetRevenue money.Amount `json:"budgetRevenue"`
	ActualRevenue money.Amount `json:"actualRevenue"`
	BudgetExpense money.Amount `json:"budgetExpense"`
	ActualExpense money.Amount `json:"actualExpense"`
}

type Comparison struct {
	Budget   *Budget      `json:"budget,omitempty"` // kosong bila tahun buku itu belum punya anggaran
	Months   []string     `json:"months"`
	From     string       `json:"from"`
	To       string       `json:"to"`
	Sections []Section    `json:"sections"`
	Revenue  Row          `json:"revenue"`
	Expense  Row          `json:"expense"`
	Profit   Row          `json:"profit"`
	Monthly  []MonthPoint `json:"monthly"`
	Near     int          `json:"near"`
	Over     int          `json:"over"`
	NearPct  int          `json:"nearPct"`
}

func expenseClass(c int) bool { return c == 5 || c == 6 || c == 8 || c == 9 }

func pct(actual, budget money.Amount) *float64 {
	if budget.Sign() <= 0 {
		return nil
	}
	a, _ := strconv.ParseFloat(actual.String(), 64)
	b, _ := strconv.ParseFloat(budget.String(), 64)
	v := a / b * 100
	return &v
}

func (r *Row) add(o Row) {
	r.Budget, r.Actual = r.Budget.Add(o.Budget), r.Actual.Add(o.Actual)
	r.YearBudget, r.YearActual = r.YearBudget.Add(o.YearBudget), r.YearActual.Add(o.YearActual)
}

func (r *Row) finish() {
	r.Variance = r.Actual.Sub(r.Budget)
	r.Remaining = r.YearBudget.Sub(r.YearActual)
	r.Pct = pct(r.Actual, r.Budget)
}

func (s *service) Compare(ctx context.Context, companyID uuid.UUID, f CompareFilter) (Comparison, error) {
	if f.FromMonth < 1 || f.ToMonth > 12 || f.FromMonth > f.ToMonth {
		return Comparison{}, httpx.BadRequest("request.query", "Rentang bulan harus di antara bulan ke-1 dan ke-12 tahun buku.")
	}
	q := s.q(ctx)
	var b store.BudgetBudget
	var err error
	if f.BudgetID != nil {
		b, err = q.GetBudget(ctx, store.GetBudgetParams{CompanyID: companyID, ID: *f.BudgetID})
	} else {
		b, err = q.ActiveBudget(ctx, store.ActiveBudgetParams{CompanyID: companyID, FiscalYear: int32(f.FiscalYear)})
	}
	has := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Comparison{}, err
	}
	if f.BudgetID != nil && !has {
		return Comparison{}, notFound()
	}
	year := f.FiscalYear
	if has {
		year = int(b.FiscalYear)
	}
	start, err := s.fiscalStart(ctx, companyID, year)
	if err != nil {
		return Comparison{}, err
	}
	keys := monthKeys(start)
	out := Comparison{Months: keys, From: keys[f.FromMonth-1], To: keys[f.ToMonth-1], Sections: []Section{}, NearPct: NearPct}

	// Anggaran per akun per bulan untuk irisan yang diminta (seluruh PT = semua baris).
	budget := map[uuid.UUID][12]money.Amount{}
	if has {
		bb := toBudget(b, 0)
		out.Budget = &bb
		lines, err := q.BudgetLines(ctx, store.BudgetLinesParams{CompanyID: companyID, BudgetID: b.ID})
		if err != nil {
			return Comparison{}, err
		}
		out.Budget.Lines = len(lines)
		for _, l := range lines {
			switch {
			case f.DepartmentID != nil && (l.DepartmentID == nil || *l.DepartmentID != *f.DepartmentID):
				continue
			case f.ProjectID != nil && (l.ProjectID == nil || *l.ProjectID != *f.ProjectID):
				continue
			}
			v := budget[l.AccountID]
			for i, m := range months(l) {
				v[i] = v[i].Add(m)
			}
			budget[l.AccountID] = v
		}
	}
	act, err := s.actual(ctx, companyID, start, f.Slice)
	if err != nil {
		return Comparison{}, err
	}
	accounts, err := s.Ledger.Accounts(ctx, companyID)
	if err != nil {
		return Comparison{}, err
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Code < accounts[j].Code })

	out.Monthly = make([]MonthPoint, 12)
	for i := range out.Monthly {
		out.Monthly[i].Month = keys[i]
	}
	sections := map[int]*Section{}
	for _, a := range accounts {
		bv, hasB := budget[a.ID]
		av, hasA := act[a.ID]
		if (!hasB && !hasA) || a.Class < 4 {
			continue
		}
		r := Row{AccountID: a.ID, Code: a.Code, Name: a.Name}
		for i := 0; i < 12; i++ {
			bm, am := bv[i], normal(a.Class, av[i])
			r.YearBudget = r.YearBudget.Add(bm)
			if i < f.ToMonth {
				r.YearActual = r.YearActual.Add(am)
			}
			if i >= f.FromMonth-1 && i < f.ToMonth {
				r.Budget, r.Actual = r.Budget.Add(bm), r.Actual.Add(am)
			}
			p := &out.Monthly[i]
			if expenseClass(a.Class) {
				p.BudgetExpense, p.ActualExpense = p.BudgetExpense.Add(bm), p.ActualExpense.Add(am)
			} else {
				p.BudgetRevenue, p.ActualRevenue = p.BudgetRevenue.Add(bm), p.ActualRevenue.Add(am)
			}
		}
		if r.Budget.IsZero() && r.Actual.IsZero() && r.YearBudget.IsZero() && r.YearActual.IsZero() {
			continue
		}
		r.finish()
		if expenseClass(a.Class) {
			switch {
			case r.YearBudget.IsZero() && r.YearActual.Sign() > 0:
				r.Status = "unbudgeted"
			case r.YearBudget.Sign() > 0 && r.YearActual.Cmp(r.YearBudget) > 0:
				r.Status = "over"
			case r.YearBudget.Sign() > 0 && r.YearActual.MulFrac(100, 1, 4, money.HalfUp).Cmp(r.YearBudget.MulFrac(NearPct, 1, 4, money.HalfUp)) >= 0:
				r.Status = "near"
			}
			switch r.Status {
			case "near":
				out.Near++
			case "over", "unbudgeted":
				out.Over++
			}
		}
		sec, ok := sections[a.Class]
		if !ok {
			sec = &Section{Class: a.Class, Name: ledger.ClassOf(a.Class).Name, Expense: expenseClass(a.Class), Rows: []Row{}}
			sections[a.Class] = sec
		}
		sec.Rows = append(sec.Rows, r)
		sec.Total.add(r)
		if sec.Expense {
			out.Expense.add(r)
		} else {
			out.Revenue.add(r)
		}
	}
	for c := 4; c <= 9; c++ {
		if sec, ok := sections[c]; ok {
			sec.Total.Name = "Jumlah " + strings.ToLower(sec.Name)
			sec.Total.finish()
			out.Sections = append(out.Sections, *sec)
		}
	}
	out.Revenue.Name, out.Expense.Name, out.Profit.Name = "Pendapatan", "Beban", "Laba"
	out.Revenue.finish()
	out.Expense.finish()
	out.Profit = Row{
		Name: "Laba", Budget: out.Revenue.Budget.Sub(out.Expense.Budget), Actual: out.Revenue.Actual.Sub(out.Expense.Actual),
		YearBudget: out.Revenue.YearBudget.Sub(out.Expense.YearBudget), YearActual: out.Revenue.YearActual.Sub(out.Expense.YearActual),
	}
	out.Profit.finish()
	return out, nil
}

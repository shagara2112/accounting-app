// Package imports adalah impor massal dari Excel atau CSV (PLT-07) untuk master data, saldo
// awal dan migrasi (GL-06), jurnal, dan rekap penjualan harian retail (IND-07). Setiap berkas
// diproses dalam satu transaksi: baris divalidasi lewat service modul pemiliknya (aturan sama
// dengan input manual), lalu seluruh berkas diterapkan atau seluruhnya batal. Pratinjau
// menjalankan proses yang sama lalu membatalkannya, sehingga hasilnya persis seperti impor.
package imports

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/export"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/sheet"
)

// Issue adalah masalah di satu baris berkas. Peringatan tidak membatalkan impor.
type Issue struct {
	Row     int    `json:"row"`
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
	Warning bool   `json:"warning,omitempty"`
}

type Result struct {
	Kind    string   `json:"kind"`
	Rows    int      `json:"rows"`
	DryRun  bool     `json:"dryRun"`
	Applied bool     `json:"applied"`
	Errors  int      `json:"errors"`
	Issues  []Issue  `json:"issues"`
	Summary []string `json:"summary"`
}

// Options: Date untuk jenis yang butuh tanggal saldo awal (neraca saldo dan stok).
type Options struct {
	Date   civil.Date
	DryRun bool
}

// Status menunjukkan kelengkapan saldo awal: akun ekuitas saldo awal bernilai nol setelah
// neraca saldo, piutang, utang, dan stok awal selesai diimpor.
type Status struct {
	OpeningAccount struct {
		Code    string       `json:"code"`
		Name    string       `json:"name"`
		Balance money.Amount `json:"balance"`
	} `json:"openingAccount"`
	OpeningEntries []ledger.EntrySummary `json:"openingEntries"`
}

type Service interface {
	Kinds() []KindInfo
	Template(ctx context.Context, companyID uuid.UUID, kind string) (export.Table, error)
	Import(ctx context.Context, companyID uuid.UUID, kind, fileName string, data []byte, o Options, actor audit.Actor) (Result, error)
	Status(ctx context.Context, companyID uuid.UUID) (Status, error)
	// Opening dan SaveOpening melayani halaman Saldo awal: saldo per akun diketik di layar.
	Opening(ctx context.Context, companyID uuid.UUID) (OpeningState, error)
	SaveOpening(ctx context.Context, companyID uuid.UUID, in OpeningInput, actor audit.Actor) (OpeningState, error)
}

type Deps struct {
	Pool      *pgxpool.Pool
	Ledger    ledger.Service
	Core      core.Service
	Inventory inventory.Service
	Sales     sales.Service
	Purchase  purchase.Service
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

// KindInfo untuk halaman impor dan perangkaian route.
type KindInfo struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Perm     string `json:"perm"`
	NeedDate bool   `json:"needDate"`
}

// kind adalah satu jenis impor: kolomnya, contoh baris untuk template, dan penerapannya.
type kind struct {
	KindInfo
	notes   []string
	columns []sheet.Column
	example func(ctx context.Context, s *service, companyID uuid.UUID) [][]string
	apply   func(ctx context.Context, s *service, j *job) error
}

var kinds = []kind{contactKind, productKind, openingKind, receivableKind, payableKind, stockKind, journalKind, dailyKind}

func find(k string) (kind, bool) {
	for _, x := range kinds {
		if x.Kind == k {
			return x, true
		}
	}
	return kind{}, false
}

func (s *service) Kinds() []KindInfo {
	out := make([]KindInfo, len(kinds))
	for i, k := range kinds {
		out[i] = k.KindInfo
	}
	return out
}

// Perms memetakan jenis impor ke izin yang dibutuhkan, untuk route.
func Perms() map[string]string {
	m := map[string]string{}
	for _, k := range kinds {
		m[k.Kind] = k.Perm
	}
	return m
}

// job adalah satu proses impor yang sedang berjalan.
type job struct {
	companyID uuid.UUID
	actor     audit.Actor
	opts      Options
	table     sheet.Table
	issues    []Issue
	summary   []string
	// header kolom per key, untuk menyebut kolom di pesan galat.
	header map[string]string
}

func (j *job) fail(row int, key, msg string) {
	j.issues = append(j.issues, Issue{Row: row, Column: j.header[key], Message: msg})
}

func (j *job) warn(row int, key, msg string) {
	j.issues = append(j.issues, Issue{Row: row, Column: j.header[key], Message: msg, Warning: true})
}

func (j *job) failed() bool {
	for _, i := range j.issues {
		if !i.Warning {
			return true
		}
	}
	return false
}

var lineField = regexp.MustCompile(`^(lines|payments|allocations)\.(\d+)\.(\w+)$`)

// failErr mencatat galat dari service modul. Field "lines.N.x" dipetakan ke baris ke-N dari
// rows (urutan baris berkas yang menjadi baris dokumen); field lain ke baris pertama. fields
// memetakan nama field service ke key kolom berkas.
func (j *job) failErr(err error, rows []int, fields map[string]string) {
	var p *httpx.Problem
	if !errors.As(err, &p) {
		j.fail(rows[0], "", "Gagal diproses: "+err.Error())
		return
	}
	if len(p.Fields) == 0 {
		j.fail(rows[0], "", p.Detail)
		return
	}
	keys := make([]string, 0, len(p.Fields))
	for k := range p.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		row, field := rows[0], k
		if m := lineField.FindStringSubmatch(k); m != nil {
			field = m[1]
			if m[1] == "lines" {
				if n, _ := strconv.Atoi(m[2]); n < len(rows) {
					row = rows[n]
				}
				field = m[3]
			}
		}
		j.fail(row, fields[field], p.Fields[k])
	}
}

// savepoint menjalankan fn di dalam savepoint: bila gagal, perubahan fn dibatalkan tanpa
// membatalkan baris lain, sehingga semua baris tetap divalidasi.
func (s *service) savepoint(ctx context.Context, fn func() error) error {
	conn := db.Conn(ctx, s.Pool)
	if _, err := conn.Exec(ctx, "SAVEPOINT import_row"); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rbErr := conn.Exec(ctx, "ROLLBACK TO SAVEPOINT import_row"); rbErr != nil {
			return rbErr
		}
		_, _ = conn.Exec(ctx, "RELEASE SAVEPOINT import_row")
		return err
	}
	_, err := conn.Exec(ctx, "RELEASE SAVEPOINT import_row")
	return err
}

func (s *service) Import(ctx context.Context, companyID uuid.UUID, kindName, fileName string, data []byte, o Options, actor audit.Actor) (Result, error) {
	k, ok := find(kindName)
	if !ok {
		return Result{}, httpx.NotFound("import.kind", "Jenis impor tidak dikenal.")
	}
	if k.NeedDate && o.Date.IsZero() {
		return Result{}, httpx.Unprocessable("import.date", "Isi tanggal saldo awal.").WithFields(map[string]string{"date": "Isi tanggal saldo awal."})
	}
	rows, err := sheet.Read(fileName, data)
	if err != nil {
		return Result{}, err
	}
	table, err := sheet.Parse(rows, k.columns)
	if err != nil {
		return Result{}, err
	}
	if len(table.Rows) == 0 {
		return Result{}, httpx.Unprocessable("import.empty", "Berkas tidak berisi baris data di bawah baris judul.")
	}
	j := &job{companyID: companyID, actor: actor, opts: o, table: table, header: map[string]string{}}
	for _, c := range k.columns {
		j.header[c.Key] = strings.TrimSuffix(c.Names[0], "*")
	}
	out := Result{Kind: k.Kind, Rows: len(table.Rows), DryRun: o.DryRun}
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		conn := db.Conn(ctx, s.Pool)
		if _, err := conn.Exec(ctx, "SAVEPOINT import_file"); err != nil {
			return err
		}
		if err := k.apply(ctx, s, j); err != nil {
			return err
		}
		if o.DryRun || j.failed() {
			_, err := conn.Exec(ctx, "ROLLBACK TO SAVEPOINT import_file")
			return err
		}
		out.Applied = true
		_, err := audit.Write(ctx, conn, audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "import." + k.Kind,
			ObjectType: "import", ObjectID: k.Kind, IP: actor.IP,
			After: map[string]any{"file": fileName, "rows": len(table.Rows), "summary": j.summary},
		})
		return err
	})
	if err != nil {
		return Result{}, err
	}
	sort.SliceStable(j.issues, func(a, b int) bool { return j.issues[a].Row < j.issues[b].Row })
	for _, i := range j.issues {
		if !i.Warning {
			out.Errors++
		}
	}
	if len(j.issues) > 300 {
		j.issues = j.issues[:300]
	}
	out.Issues = append([]Issue{}, j.issues...)
	out.Summary = append([]string{}, j.summary...)
	return out, nil
}

func (s *service) Template(ctx context.Context, companyID uuid.UUID, kindName string) (export.Table, error) {
	k, ok := find(kindName)
	if !ok {
		return export.Table{}, httpx.NotFound("import.kind", "Jenis impor tidak dikenal.")
	}
	t := export.Table{
		Title:    "Template impor " + strings.ToLower(k.Title),
		Subtitle: append([]string{"Kolom bertanda * wajib diisi. Jangan ubah baris judul; hapus baris contoh sebelum mengimpor."}, k.notes...),
	}
	for _, c := range k.columns {
		t.Columns = append(t.Columns, export.Column{Header: c.Names[0], Width: max(14, len(c.Names[0])+4)})
	}
	if k.example != nil {
		for _, r := range k.example(ctx, s, companyID) {
			row := export.Row{}
			for _, v := range r {
				row.Cells = append(row.Cells, export.Text(v))
			}
			t.Rows = append(t.Rows, row)
		}
	}
	return t, nil
}

func (s *service) Status(ctx context.Context, companyID uuid.UUID) (Status, error) {
	var out Status
	acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.OpeningEquity)
	if err != nil {
		return out, err
	}
	bal, err := s.Ledger.AccountNet(ctx, companyID, acc.ID)
	if err != nil {
		return out, err
	}
	out.OpeningAccount.Code, out.OpeningAccount.Name, out.OpeningAccount.Balance = acc.Code, acc.Name, bal
	out.OpeningEntries, err = s.Ledger.EntriesBySource(ctx, companyID, ledger.SourceOpening)
	return out, err
}

// accountsByCode memetakan kode akun ke akunnya.
func (s *service) accountsByCode(ctx context.Context, companyID uuid.UUID) (map[string]ledger.Account, error) {
	list, err := s.Ledger.Accounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	m := make(map[string]ledger.Account, len(list))
	for _, a := range list {
		m[a.Code] = a
	}
	return m, nil
}

// amount membaca angka di satu kolom; kosong = nol.
func (j *job) amount(r sheet.Row, key string) (money.Amount, bool) {
	v := r.Get(key)
	if v == "" {
		return money.Amount{}, true
	}
	a, err := sheet.Amount(v)
	if err != nil {
		j.fail(r.Line, key, err.Error())
		return money.Amount{}, false
	}
	return a, true
}

func (j *job) date(r sheet.Row, key string, required bool) (civil.Date, bool) {
	v := r.Get(key)
	if v == "" {
		if required {
			j.fail(r.Line, key, "Wajib diisi.")
			return civil.Date{}, false
		}
		return civil.Date{}, true
	}
	d, err := sheet.Date(v)
	if err != nil {
		j.fail(r.Line, key, err.Error())
		return civil.Date{}, false
	}
	return d, true
}

func (j *job) required(r sheet.Row, key string) (string, bool) {
	v := r.Get(key)
	if v == "" {
		j.fail(r.Line, key, "Wajib diisi.")
		return "", false
	}
	return v, true
}

func plural(n int, what string) string { return fmt.Sprintf("%d %s", n, what) }

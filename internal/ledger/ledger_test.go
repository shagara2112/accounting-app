package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/dbtest"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/store"
)

type fixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	svc     ledger.Service
	company uuid.UUID
	other   uuid.UUID
	actor   ledger.Actor
	acc     map[string]uuid.UUID
}

func setup(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.Pool(t)
	ctx := context.Background()
	cs := core.New(pool)
	a, err := cs.CreateCompany(ctx, core.NewCompany{Name: "PT Uji Jasa", ShortName: "Jasa"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := cs.CreateCompany(ctx, core.NewCompany{Name: "PT Uji Lain", ShortName: "Lain"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.New(pool).CreateUser(ctx, store.CreateUserParams{
		ID: uuid.Must(uuid.NewV7()), Email: "akuntansi@uji.test", Name: "Staf Akuntansi", PasswordHash: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ctx: ctx, pool: pool, svc: ledger.New(pool), company: a.ID, other: b.ID,
		actor: ledger.Actor{UserID: u.ID, IP: "127.0.0.1"}, acc: map[string]uuid.UUID{}}
	for _, c := range []uuid.UUID{a.ID, b.ID} {
		if _, err := f.svc.ApplyTemplate(ctx, c, "jasa", f.actor); err != nil {
			t.Fatal(err)
		}
	}
	list, err := f.svc.Accounts(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		f.acc[x.Code] = x.ID
	}
	return f
}

func day(s string) civil.Date {
	d, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

func (f *fixture) line(code, debit, credit string) ledger.LineDraft {
	id, ok := f.acc[code]
	if !ok {
		f.t.Fatalf("akun %s tidak ada", code)
	}
	return ledger.LineDraft{AccountID: id, Debit: money.MustParse(debit), Credit: money.MustParse(credit)}
}

func (f *fixture) post(kind ledger.Kind, date string, lines ...ledger.LineDraft) (ledger.Posted, error) {
	return f.svc.Post(f.ctx, ledger.EntryDraft{
		CompanyID: f.company, Kind: kind, Date: day(date), Memo: "uji", Lines: lines, Actor: f.actor,
	})
}

func (f *fixture) mustPost(kind ledger.Kind, date string, lines ...ledger.LineDraft) ledger.Posted {
	f.t.Helper()
	p, err := f.post(kind, date, lines...)
	if err != nil {
		f.t.Fatalf("posting %s %s gagal: %v", kind, date, err)
	}
	return p
}

func problem(err error) *httpx.Problem {
	var p *httpx.Problem
	if errors.As(err, &p) {
		return p
	}
	return nil
}

func expectCode(t *testing.T, err error, code string) *httpx.Problem {
	t.Helper()
	p := problem(err)
	if p == nil || p.Code != code {
		t.Fatalf("error = %v, seharusnya problem %s", err, code)
	}
	return p
}

func (f *fixture) worksheet(month string) ledger.Worksheet {
	f.t.Helper()
	m, _ := civil.ParseMonth(month)
	w, err := f.svc.Worksheet(f.ctx, f.company, m)
	if err != nil {
		f.t.Fatal(err)
	}
	if !w.Balanced {
		f.t.Fatalf("neraca lajur %s tidak seimbang: %+v", month, w.Final)
	}
	return w
}

// adjusted mengembalikan saldo disesuaikan (debit − kredit) satu akun di neraca lajur.
func adjusted(w ledger.Worksheet, code string) money.Amount {
	for _, r := range w.Rows {
		if r.Code == code {
			return r.Adjusted.Debit.Sub(r.Adjusted.Credit)
		}
	}
	return money.Amount{}
}

func (f *fixture) verifyBalances() {
	f.t.Helper()
	n, err := f.svc.VerifyBalances(f.ctx, f.company)
	if err != nil {
		f.t.Fatal(err)
	}
	if n != 0 {
		f.t.Fatalf("%d sel saldo tidak cocok dengan baris jurnal", n)
	}
}

func TestPostNumbersAndUpdatesBalances(t *testing.T) {
	f := setup(t)
	p1 := f.mustPost(ledger.General, "2026-10-05", f.line("1-1111", "100000000", "0"), f.line("3-1001", "0", "100000000"))
	p2 := f.mustPost(ledger.General, "2026-10-06",
		f.line("1-1201", "111000000", "0"), f.line("4-1001", "0", "100000000"), f.line("2-1201", "0", "11000000"))
	p3 := f.mustPost(ledger.General, "2026-11-01", f.line("6-2001", "5000000", "0"), f.line("1-1111", "0", "5000000"))
	if p1.Number != "JU/2026/10/0001" || p2.Number != "JU/2026/10/0002" || p3.Number != "JU/2026/11/0001" {
		t.Fatalf("nomor = %s, %s, %s", p1.Number, p2.Number, p3.Number)
	}

	w := f.worksheet("2026-10")
	if got := adjusted(w, "4-1001"); !got.Equal(money.FromInt(-100_000_000)) {
		t.Fatalf("pendapatan Oktober = %s", got)
	}
	if !w.NetIncome.Equal(money.FromInt(100_000_000)) {
		t.Fatalf("laba Oktober = %s", w.NetIncome)
	}
	if !f.worksheet("2026-11").NetIncome.Equal(money.FromInt(95_000_000)) {
		t.Fatal("laba November seharusnya kumulatif sejak awal tahun buku")
	}

	accounts, err := f.svc.Accounts(f.ctx, f.company)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		// Saldo per hari ini ditampilkan menurut sisi normal: pendapatan (kredit) positif.
		if a.Code == "4-1000" && a.Balance.Sign() < 0 {
			t.Fatalf("saldo header pendapatan = %s, seharusnya positif", a.Balance)
		}
	}

	d, err := f.svc.Entry(f.ctx, f.company, p2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Lines) != 3 || d.Lines[0].AccountCode != "1-1201" || !d.Total.Equal(money.FromInt(111_000_000)) {
		t.Fatalf("detail jurnal salah: %+v", d)
	}
	f.verifyBalances()
}

// PRD: jurnal dengan selisih Rp 1 ditolak, dan pesan galat menyebut besar selisihnya.
func TestUnbalancedJournalStatesDifference(t *testing.T) {
	f := setup(t)
	_, err := f.post(ledger.General, "2026-10-05", f.line("6-2002", "1000000", "0"), f.line("1-1101", "0", "999999"))
	p := expectCode(t, err, "ledger.unbalanced")
	if !strings.Contains(p.Detail, "selisih Rp 1.") {
		t.Fatalf("pesan tidak menyebut selisih: %q", p.Detail)
	}

	_, err = f.post(ledger.General, "2026-10-05", f.line("6-2002", "1000", "0"))
	expectCode(t, err, "journal.too_few_lines")
	_, err = f.post(ledger.General, "2026-10-05", f.line("6-2002", "1000", "1000"), f.line("1-1101", "0", "0"))
	p = expectCode(t, err, "journal.invalid")
	if p.Fields["lines.0.amount"] == "" || p.Fields["lines.1.amount"] == "" {
		t.Fatalf("field error per baris tidak lengkap: %v", p.Fields)
	}
}

func TestRejectsHeaderInactiveAndForeignAccounts(t *testing.T) {
	f := setup(t)
	_, err := f.post(ledger.General, "2026-10-05", f.line("1-1100", "1000", "0"), f.line("3-1001", "0", "1000"))
	p := expectCode(t, err, "journal.account")
	if !strings.Contains(p.Fields["lines.0.accountId"], "akun header") {
		t.Fatalf("pesan akun header: %v", p.Fields)
	}

	foreign, err := f.svc.AccountByCode(f.ctx, f.other, "1-1101")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Post(f.ctx, ledger.EntryDraft{CompanyID: f.company, Kind: ledger.General, Date: day("2026-10-05"), Actor: f.actor,
		Lines: []ledger.LineDraft{
			{AccountID: foreign.ID, Debit: money.FromInt(1000)},
			f.line("3-1001", "0", "1000"),
		}})
	expectCode(t, err, "journal.account")

	kecil, _ := f.svc.AccountByCode(f.ctx, f.company, "1-1102")
	if _, err := f.svc.UpdateAccount(f.ctx, f.company, kecil.ID, ledger.AccountChange{Name: kecil.Name, Active: false, Version: kecil.Version}, f.actor); err != nil {
		t.Fatal(err)
	}
	_, err = f.post(ledger.General, "2026-10-05", f.line("1-1102", "1000", "0"), f.line("3-1001", "0", "1000"))
	expectCode(t, err, "journal.account")
}

// Basis data sendiri menolak jurnal tidak seimbang dan perubahan baris yang sudah diposting,
// walaupun ada kode yang melewati ledger.Post.
func TestDatabaseGuardsLedgerInvariants(t *testing.T) {
	f := setup(t)
	p := f.mustPost(ledger.General, "2026-10-05", f.line("1-1111", "500000", "0"), f.line("3-1001", "0", "500000"))

	err := db.InTx(f.ctx, f.pool, pgx.TxOptions{}, func(ctx context.Context) error {
		q := db.Conn(ctx, f.pool)
		id := uuid.Must(uuid.NewV7())
		if _, err := q.Exec(ctx, `INSERT INTO ledger.journal_entries (id, company_id, number, kind, entry_date, source_type, total)
			VALUES ($1, $2, 'X/1', 'umum', '2026-10-05', 'manual', 100)`, id, f.company); err != nil {
			return err
		}
		_, err := q.Exec(ctx, `INSERT INTO ledger.journal_lines (id, company_id, entry_id, entry_date, line_no, account_id, debit, credit)
			VALUES ($1, $2, $3, '2026-10-05', 1, $4, 100, 0), ($5, $2, $3, '2026-10-05', 2, $6, 0, 99)`,
			uuid.Must(uuid.NewV7()), f.company, id, f.acc["1-1111"], uuid.Must(uuid.NewV7()), f.acc["3-1001"])
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "tidak seimbang") {
		t.Fatalf("jurnal tidak seimbang lolos sampai commit: %v", err)
	}

	for _, sql := range []string{
		"UPDATE ledger.journal_lines SET debit = debit + 1",
		"DELETE FROM ledger.journal_lines",
		"UPDATE ledger.journal_entries SET memo = 'diubah'",
		"DELETE FROM ledger.journal_entries",
		"UPDATE ledger.accounts SET code = '1-1199' WHERE code = '1-1101'",
		"DELETE FROM ledger.accounts WHERE code = '1-1101'",
	} {
		if _, err := f.pool.Exec(f.ctx, sql); err == nil {
			t.Errorf("%q seharusnya ditolak basis data", sql)
		}
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO ledger.journal_lines (id, company_id, entry_id, entry_date, line_no, account_id, debit)
		VALUES ($1, $2, $3, '2026-10-05', 9, $4, 1)`, uuid.Must(uuid.NewV7()), f.company, p.ID, f.acc["1-1100"]); err == nil {
		t.Error("baris ke akun header seharusnya ditolak basis data")
	}
	f.verifyBalances()
}

// Tutup buku dua tahap: open → adjusting (hanya penyesuaian) → locked; buka kembali wajib beralasan.
func TestTwoStagePeriodClose(t *testing.T) {
	f := setup(t)
	oct := day("2026-10-01")
	if err := f.svc.SetPeriodStatus(f.ctx, f.company, oct, "adjusting", "", f.actor); err != nil {
		t.Fatal(err)
	}
	_, err := f.post(ledger.General, "2026-10-20", f.line("6-2002", "100", "0"), f.line("1-1101", "0", "100"))
	p := expectCode(t, err, "period.adjusting")
	if !strings.Contains(p.Detail, "Oktober 2026") {
		t.Fatalf("pesan tidak menyebut periode: %q", p.Detail)
	}
	f.mustPost(ledger.Adjustment, "2026-10-31", f.line("6-3004", "250000", "0"), f.line("1-2204", "0", "250000"))

	if err := f.svc.SetPeriodStatus(f.ctx, f.company, oct, "locked", "", f.actor); err != nil {
		t.Fatal(err)
	}
	_, err = f.post(ledger.Adjustment, "2026-10-31", f.line("6-3004", "1", "0"), f.line("1-2204", "0", "1"))
	expectCode(t, err, "period.locked")

	expectCode(t, f.svc.SetPeriodStatus(f.ctx, f.company, oct, "adjusting", "", f.actor), "period.reason_required")
	if err := f.svc.SetPeriodStatus(f.ctx, f.company, oct, "adjusting", "Koreksi penyusutan dari auditor", f.actor); err != nil {
		t.Fatal(err)
	}
	fy, err := f.svc.Periods(f.ctx, f.company, 2026)
	if err != nil {
		t.Fatal(err)
	}
	octP := fy.Periods[9]
	if octP.Month != "2026-10" || octP.Status != "adjusting" || octP.Adjustments != 1 || octP.Reason == nil {
		t.Fatalf("periode Oktober = %+v", octP)
	}
	if fy.Periods[10].Status != "open" {
		t.Fatal("bulan tanpa catatan seharusnya open")
	}
}

// Jurnal penyesuaian bertanda balik otomatis langsung punya jurnal pembalik tanggal 1 bulan berikutnya.
func TestAdjustmentWithAutoReversal(t *testing.T) {
	f := setup(t)
	p, err := f.svc.Post(f.ctx, ledger.EntryDraft{
		CompanyID: f.company, Kind: ledger.Adjustment, Date: day("2026-10-31"), Memo: "Akrual listrik Oktober",
		AutoReverse: true, Actor: f.actor,
		Lines: []ledger.LineDraft{f.line("6-2002", "3500000", "0"), f.line("2-1302", "0", "3500000")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Number != "JP/2026/10/0001" || p.AutoReversal == nil || p.AutoReversal.Number != "JB/2026/11/0001" {
		t.Fatalf("hasil posting = %+v", p)
	}
	rev, err := f.svc.Entry(f.ctx, f.company, p.AutoReversal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Date.String() != "2026-11-01" || rev.Kind != ledger.Reversal || rev.ReversalOfRef == nil || rev.ReversalOfRef.ID != p.ID {
		t.Fatalf("jurnal pembalik = %+v", rev)
	}
	if !rev.Lines[0].Credit.Equal(money.FromInt(3_500_000)) {
		t.Fatal("debit dan kredit pembalik seharusnya tertukar")
	}

	oct := f.worksheet("2026-10")
	for _, r := range oct.Rows {
		if r.Code == "6-2002" && (!r.Unadjusted.Debit.IsZero() || !r.Adjustment.Debit.Equal(money.FromInt(3_500_000))) {
			t.Fatalf("kolom penyesuaian Oktober salah: %+v", r)
		}
	}
	nov := f.worksheet("2026-11")
	if !adjusted(nov, "2-1302").IsZero() {
		t.Fatal("akrual seharusnya nol setelah dibalik di November")
	}

	_, err = f.svc.Post(f.ctx, ledger.EntryDraft{CompanyID: f.company, Kind: ledger.General, Date: day("2026-10-31"),
		AutoReverse: true, Actor: f.actor, Lines: []ledger.LineDraft{f.line("6-2002", "1", "0"), f.line("2-1302", "0", "1")}})
	expectCode(t, err, "journal.auto_reverse")
}

// PRD: void membuat jurnal pembalik bertanggal void; saldo kembali seperti sebelumnya.
func TestVoidRestoresBalances(t *testing.T) {
	f := setup(t)
	f.mustPost(ledger.General, "2026-10-01", f.line("1-1111", "50000000", "0"), f.line("3-1001", "0", "50000000"))
	before := f.worksheet("2026-10")
	p := f.mustPost(ledger.General, "2026-10-10", f.line("6-2001", "12000000", "0"), f.line("1-1111", "0", "12000000"))

	_, err := f.svc.Void(f.ctx, f.company, p.ID, day("2026-10-09"), "Salah input sewa", f.actor)
	expectCode(t, err, "journal.void_date")
	_, err = f.svc.Void(f.ctx, f.company, p.ID, day("2026-10-15"), "aku", f.actor)
	expectCode(t, err, "journal.void_reason")

	v, err := f.svc.Void(f.ctx, f.company, p.ID, day("2026-10-15"), "Salah input sewa", f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if v.Reversal.Number != "JV/2026/10/0001" {
		t.Fatalf("nomor pembalik = %s", v.Reversal.Number)
	}
	after := f.worksheet("2026-10")
	for _, code := range []string{"1-1111", "6-2001", "3-1001"} {
		if !adjusted(after, code).Equal(adjusted(before, code)) {
			t.Fatalf("saldo %s setelah void = %s, sebelum = %s", code, adjusted(after, code), adjusted(before, code))
		}
	}

	d, _ := f.svc.Entry(f.ctx, f.company, p.ID)
	if d.Status != "void" || d.VoidReason == nil || len(d.Reversals) != 1 {
		t.Fatalf("jurnal asli setelah void = %+v", d)
	}
	_, err = f.svc.Void(f.ctx, f.company, p.ID, day("2026-10-15"), "Void kedua kali", f.actor)
	expectCode(t, err, "journal.already_void")
	_, err = f.svc.Void(f.ctx, f.company, v.Reversal.ID, day("2026-10-15"), "Membatalkan pembatalan", f.actor)
	expectCode(t, err, "journal.void_reversal")
	_, err = f.svc.Void(f.ctx, f.other, p.ID, day("2026-10-15"), "Dari PT lain", f.actor)
	expectCode(t, err, "journal.not_found")
	f.verifyBalances()
}

// Void jurnal penyesuaian ikut membatalkan pembalik otomatisnya, sehingga saldo setiap bulan kembali.
func TestVoidAdjustmentAlsoVoidsAutoReversal(t *testing.T) {
	f := setup(t)
	p, err := f.svc.Post(f.ctx, ledger.EntryDraft{
		CompanyID: f.company, Kind: ledger.Adjustment, Date: day("2026-10-31"), AutoReverse: true, Actor: f.actor,
		Lines: []ledger.LineDraft{f.line("6-2002", "800000", "0"), f.line("2-1302", "0", "800000")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Void(f.ctx, f.company, p.AutoReversal.ID, day("2026-11-02"), "Hanya pembaliknya", f.actor)
	expectCode(t, err, "journal.void_auto_reversal")

	v, err := f.svc.Void(f.ctx, f.company, p.ID, day("2026-10-31"), "Tagihan listrik ternyata sudah dibayar", f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.AlsoVoids) != 1 || v.AlsoVoids[0].ID != p.AutoReversal.ID {
		t.Fatalf("pembalik otomatis tidak ikut di-void: %+v", v)
	}
	for _, month := range []string{"2026-10", "2026-11"} {
		w := f.worksheet(month)
		for _, code := range []string{"6-2002", "2-1302"} {
			if !adjusted(w, code).IsZero() {
				t.Fatalf("%s %s = %s setelah void, seharusnya nol", month, code, adjusted(w, code))
			}
		}
	}
	rev, _ := f.svc.Entry(f.ctx, f.company, p.AutoReversal.ID)
	if rev.Status != "void" || len(rev.Reversals) != 1 || rev.Reversals[0].Date.String() != "2026-11-01" {
		t.Fatalf("pembalik otomatis setelah void = %+v", rev)
	}
	f.verifyBalances()
}

// Nomor yang diambil transaksi yang batal ikut di-rollback, sehingga tidak ada celah.
func TestNumberingHasNoGaps(t *testing.T) {
	f := setup(t)
	errBatal := errors.New("batal")
	err := db.InTx(f.ctx, f.pool, pgx.TxOptions{}, func(ctx context.Context) error {
		if _, err := f.svc.Post(ctx, ledger.EntryDraft{CompanyID: f.company, Kind: ledger.General, Date: day("2026-10-05"), Actor: f.actor,
			Lines: []ledger.LineDraft{f.line("1-1111", "1", "0"), f.line("3-1001", "0", "1")}}); err != nil {
			return err
		}
		return errBatal
	})
	if !errors.Is(err, errBatal) {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	numbers := make([]string, 12)
	errs := make([]error, 12)
	for i := range numbers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := f.post(ledger.General, "2026-10-05", f.line("1-1111", "1000", "0"), f.line("3-1001", "0", "1000"))
			numbers[i], errs[i] = p.Number, err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(numbers)
	for i, n := range numbers {
		if want := fmt.Sprintf("JU/2026/10/%04d", i+1); n != want {
			t.Fatalf("nomor ke-%d = %s, seharusnya %s (semua: %v)", i+1, n, want, numbers)
		}
	}
	f.verifyBalances()
}

// Laba rugi tahun lalu yang belum ditutup muncul sebagai satu baris ekuitas, dan laba
// tahun berjalan hanya menghitung sejak awal tahun buku.
func TestWorksheetAcrossFiscalYears(t *testing.T) {
	f := setup(t)
	f.mustPost(ledger.General, "2025-12-15", f.line("1-1111", "40000000", "0"), f.line("4-1001", "0", "40000000"))
	f.mustPost(ledger.General, "2026-01-10", f.line("1-1111", "7000000", "0"), f.line("4-1001", "0", "7000000"))
	w := f.worksheet("2026-01")
	if !w.NetIncome.Equal(money.FromInt(7_000_000)) {
		t.Fatalf("laba Januari 2026 = %s, seharusnya hanya tahun berjalan", w.NetIncome)
	}
	var prior *ledger.WorksheetRow
	for i := range w.Rows {
		if w.Rows[i].AccountID == nil {
			prior = &w.Rows[i]
		}
	}
	if prior == nil || !prior.BalanceSheet.Credit.Equal(money.FromInt(40_000_000)) {
		t.Fatalf("baris laba tahun lalu = %+v", prior)
	}

	// Tahun buku April–Maret hanya bisa dipasang sebelum ada jurnal.
	expectCode(t, f.svc.SetFiscalYearStart(f.ctx, f.company, 4, f.actor), "settings.has_entries")
	if err := f.svc.SetFiscalYearStart(f.ctx, f.other, 4, f.actor); err != nil {
		t.Fatal(err)
	}
	fy, err := f.svc.Periods(f.ctx, f.other, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if fy.Periods[0].Month != "2026-04" || fy.Periods[11].Month != "2027-03" {
		t.Fatalf("periode tahun buku April = %s … %s", fy.Periods[0].Month, fy.Periods[11].Month)
	}
}

func TestAccountRules(t *testing.T) {
	f := setup(t)
	parent := f.acc["1-1100"]
	cases := []struct {
		name string
		a    ledger.NewAccount
		code string
	}{
		{"kelas beda dengan induk", ledger.NewAccount{Code: "2-1199", Name: "Salah kelas", ParentID: &parent, Postable: true}, "account.invalid"},
		{"induk akun detail", ledger.NewAccount{Code: "1-1199", Name: "Anak detail", ParentID: ptr(f.acc["1-1101"]), Postable: true}, "account.invalid"},
		{"kode dipakai", ledger.NewAccount{Code: "1-1101", Name: "Kas lagi", ParentID: &parent, Postable: true}, "account.code_taken"},
		{"format kode", ledger.NewAccount{Code: "11-01", Name: "Format", ParentID: &parent, Postable: true}, "account.invalid"},
		{"tanpa induk", ledger.NewAccount{Code: "1-1199", Name: "Yatim", Postable: true}, "account.invalid"},
	}
	for _, c := range cases {
		_, err := f.svc.CreateAccount(f.ctx, f.company, c.a, f.actor)
		if p := problem(err); p == nil || p.Code != c.code {
			t.Errorf("%s: error = %v, seharusnya %s", c.name, err, c.code)
		}
	}

	kas, err := f.svc.CreateAccount(f.ctx, f.company, ledger.NewAccount{Code: "1-1113", Name: "Bank BRI", ParentID: &parent, Postable: true}, f.actor)
	if err != nil || kas.Level != 4 || kas.Class != 1 {
		t.Fatalf("akun baru = %+v, %v", kas, err)
	}
	f.acc["1-1113"] = kas.ID
	f.mustPost(ledger.General, "2026-10-05", f.line("1-1113", "1000", "0"), f.line("3-1001", "0", "1000"))

	_, err = f.svc.UpdateAccount(f.ctx, f.company, kas.ID, ledger.AccountChange{Name: "Bank BRI", Active: false, Version: kas.Version}, f.actor)
	p := expectCode(t, err, "account.has_balance")
	if !strings.Contains(p.Detail, "Rp 1.000") {
		t.Fatalf("pesan saldo: %q", p.Detail)
	}
	_, err = f.svc.UpdateAccount(f.ctx, f.company, parent, ledger.AccountChange{Name: "Kas dan setara kas", Active: false, Version: 1}, f.actor)
	expectCode(t, err, "account.has_active_children")

	renamed, err := f.svc.UpdateAccount(f.ctx, f.company, kas.ID, ledger.AccountChange{Name: "Bank BRI Giro", Active: true, Version: kas.Version}, f.actor)
	if err != nil || renamed.Version != kas.Version+1 {
		t.Fatalf("ganti nama = %+v, %v", renamed, err)
	}
	_, err = f.svc.UpdateAccount(f.ctx, f.company, kas.ID, ledger.AccountChange{Name: "Basi", Active: true, Version: kas.Version}, f.actor)
	expectCode(t, err, "conflict.version")

	_, err = f.svc.ApplyTemplate(f.ctx, f.company, "retail", f.actor)
	expectCode(t, err, "coa.not_empty")
}

// Tes acak: serangkaian posting dan void sembarang tidak pernah membuat saldo agregat
// berbeda dari baris jurnal, dan neraca lajur setiap bulan selalu seimbang.
func TestRandomPostingsKeepInvariants(t *testing.T) {
	f := setup(t)
	rng := rand.New(rand.NewPCG(2027, 8))
	codes := []string{"1-1101", "1-1111", "1-1201", "2-1101", "2-1302", "3-1001", "4-1001", "5-1001", "6-1001", "6-2002", "7-1001", "8-1001"}
	var posted []uuid.UUID
	for i := 0; i < 120; i++ {
		date := civil.New(2026, time.Month(9+rng.IntN(3)), 1+rng.IntN(28))
		n := 2 + rng.IntN(4)
		lines := make([]ledger.LineDraft, 0, n+1)
		var total money.Amount
		for j := 0; j < n; j++ {
			amt := money.MustParse(fmt.Sprintf("%d.%04d", rng.IntN(5_000_000), rng.IntN(10_000)))
			if amt.IsZero() {
				amt = money.FromInt(1)
			}
			lines = append(lines, ledger.LineDraft{AccountID: f.acc[codes[rng.IntN(len(codes))]], Debit: amt})
			total = total.Add(amt)
		}
		lines = append(lines, ledger.LineDraft{AccountID: f.acc[codes[rng.IntN(len(codes))]], Credit: total})
		kind := ledger.General
		if rng.IntN(5) == 0 {
			kind = ledger.Adjustment
		}
		p, err := f.svc.Post(f.ctx, ledger.EntryDraft{CompanyID: f.company, Kind: kind, Date: date, Lines: lines,
			AutoReverse: kind == ledger.Adjustment && date.Month() < 11 && rng.IntN(2) == 0, Actor: f.actor})
		if err != nil {
			t.Fatal(err)
		}
		posted = append(posted, p.ID)
		if rng.IntN(6) == 0 {
			id := posted[rng.IntN(len(posted))]
			_, err := f.svc.Void(f.ctx, f.company, id, civil.New(2026, 11, 28), "Koreksi acak", f.actor)
			if p := problem(err); err != nil && (p == nil || p.Code != "journal.already_void") {
				t.Fatal(err)
			}
		}
	}
	f.verifyBalances()
	for _, m := range []string{"2026-09", "2026-10", "2026-11", "2026-12"} {
		f.worksheet(m)
	}
}

func ptr[T any](v T) *T { return &v }

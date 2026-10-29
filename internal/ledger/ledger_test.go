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
		"UPDATE ledger.accounts SET code = '2-1199' WHERE code = '1-1101'",
		// Akun yang sudah dipakai jurnal dan akun kelompok tidak bisa dihapus (akun yang belum
		// dipakai boleh, lihat TestDeleteUnusedAccount).
		"DELETE FROM ledger.accounts WHERE code = '1-1111'",
		"DELETE FROM ledger.accounts WHERE code = '1'",
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

// Kode akun yang salah ketik bisa dikoreksi selama angka kepalanya sama; riwayat jurnal ikut
// memakai kode baru karena baris jurnal menunjuk akun lewat id. Akun sistem dan kelompok terkunci.
func TestAccountCodeCorrection(t *testing.T) {
	f := setup(t)
	parent := f.acc["1-1100"]
	bri, err := f.svc.CreateAccount(f.ctx, f.company, ledger.NewAccount{Code: "1-1131", Name: "Bank BRI", ParentID: &parent, Postable: true}, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	f.acc["1-1131"] = bri.ID
	f.mustPost(ledger.General, "2026-10-05", f.line("1-1131", "1000", "0"), f.line("3-1001", "0", "1000"))

	change := func(id uuid.UUID, code string, version int) (ledger.Account, error) {
		return f.svc.UpdateAccount(f.ctx, f.company, id, ledger.AccountChange{Code: code, Name: "Bank BRI", Active: true, Version: version}, f.actor)
	}
	_, err = change(bri.ID, "2-1131", bri.Version)
	expectCode(t, err, "account.invalid")
	_, err = change(bri.ID, "1-1101", bri.Version)
	expectCode(t, err, "account.code_taken")
	_, err = change(bri.ID, "1131", bri.Version)
	expectCode(t, err, "account.invalid")

	fixed, err := change(bri.ID, "1-1113", bri.Version)
	if err != nil || fixed.Code != "1-1113" || fixed.ID != bri.ID {
		t.Fatalf("koreksi kode = %+v, %v", fixed, err)
	}
	l, err := f.svc.AccountLedger(f.ctx, f.company, bri.ID, civil.New(2026, 10, 1), civil.New(2026, 10, 31))
	if err != nil || !l.Closing.Equal(money.MustParse("1000")) || l.Account.Code != "1-1113" {
		t.Fatalf("buku besar sesudah koreksi kode = %+v, %v", l, err)
	}

	receivable := f.acc["1-1201"]
	accounts, err := f.svc.Accounts(f.ctx, f.company)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		if a.ID == receivable && !a.System {
			t.Fatal("1-1201 piutang usaha seharusnya ditandai akun sistem")
		}
		if a.ID == bri.ID && a.System {
			t.Fatal("akun biasa tidak boleh ditandai akun sistem")
		}
	}
	// Akun sistem boleh diganti kodenya (skema kode PT sendiri); perannya dikunci ke akun itu.
	recoded, err := f.svc.UpdateAccount(f.ctx, f.company, receivable, ledger.AccountChange{Code: "1-13101", Name: "Piutang usaha", Active: true, Version: 1}, f.actor)
	if err != nil || recoded.Code != "1-13101" {
		t.Fatalf("ganti kode akun sistem = %+v, %v", recoded, err)
	}
	if r, err := f.svc.RoleAccount(f.ctx, f.company, ledger.Receivable); err != nil || r.ID != receivable {
		t.Fatalf("piutang usaha sesudah ganti kode = %+v, %v", r, err)
	}
	// Akun baru berkode bawaan 1-1201 tidak mengambil alih peran yang sudah dikunci.
	fresh, err := f.svc.CreateAccount(f.ctx, f.company, ledger.NewAccount{Code: "1-1201", Name: "Piutang baru", ParentID: ptr(f.acc["1-1200"]), Postable: true}, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := f.svc.RoleAccount(f.ctx, f.company, ledger.Receivable); r.ID != receivable {
		t.Fatalf("akun baru 1-1201 mengambil alih piutang usaha: %+v", r)
	}
	accounts, _ = f.svc.Accounts(f.ctx, f.company)
	for _, a := range accounts {
		switch a.ID {
		case receivable:
			if !a.System || len(a.SystemRoles) != 1 || a.SystemRoles[0] != "piutang usaha" {
				t.Fatalf("akun sistem sesudah ganti kode = %+v", a)
			}
		case fresh.ID:
			if a.System {
				t.Fatal("akun baru berkode 1-1201 tidak boleh menjadi akun sistem")
			}
		}
	}
	_, err = f.svc.UpdateAccount(f.ctx, f.company, f.acc["1"], ledger.AccountChange{Code: "1-9", Name: "Aset", Active: true, Version: 1}, f.actor)
	expectCode(t, err, "account.invalid")

	// Penjaga di basis data tetap menolak pindah kelas walau lewat SQL langsung.
	if _, err := f.pool.Exec(f.ctx, "UPDATE ledger.accounts SET code = '2-1113' WHERE id = $1", bri.ID); err == nil {
		t.Fatal("trigger seharusnya menolak kode berkepala lain")
	}
}

// Revisi klien: PT yang menyusun bagan akun sendiri (template kosong, tanpa kode bawaan) memilih
// akun untuk tiap peran akun sistem di halaman Akun sistem.
func TestSystemAccountsForOwnChart(t *testing.T) {
	f := setup(t)
	c, err := core.New(f.pool).CreateCompany(f.ctx, core.NewCompany{Name: "PT Uji Skema Sendiri", ShortName: "Skema"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApplyTemplate(f.ctx, c.ID, "kosong", f.actor); err != nil {
		t.Fatal(err)
	}
	groups := map[string]uuid.UUID{}
	list, _ := f.svc.Accounts(f.ctx, c.ID)
	for _, a := range list {
		groups[a.Code] = a.ID
	}
	create := func(code, name string, parent uuid.UUID, postable bool) uuid.UUID {
		t.Helper()
		a, err := f.svc.CreateAccount(f.ctx, c.ID, ledger.NewAccount{Code: code, Name: name, ParentID: &parent, Postable: postable}, f.actor)
		if err != nil {
			t.Fatalf("buat akun %s: %v", code, err)
		}
		return a.ID
	}
	header := create("1-12", "Piutang", groups["1"], false)
	piutang := create("1-12101", "Piutang Usaha", header, true)
	piutangLain := create("1-12102", "Piutang Usaha Cabang", header, true)
	utang := create("2-11101", "Utang Usaha", groups["2"], true)
	modal := create("3-11101", "Modal Disetor", groups["3"], true)
	bebanPajak := create("6-19101", "Beban Pajak Final", groups["6"], true)

	status := func(company uuid.UUID, role ledger.Role) ledger.SystemAccount {
		t.Helper()
		all, err := f.svc.SystemAccounts(f.ctx, company)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range all {
			if s.Role == role {
				return s
			}
		}
		t.Fatalf("peran %s tidak ada di daftar", role)
		return ledger.SystemAccount{}
	}
	if s := status(c.ID, ledger.Receivable); s.AccountID != nil || s.Problem == "" || s.Label != "piutang usaha" {
		t.Fatalf("piutang usaha sebelum dipilih = %+v", s)
	}
	_, err = f.svc.RoleAccount(f.ctx, c.ID, ledger.Receivable)
	if p := expectCode(t, err, "ledger.role_account"); !strings.Contains(p.Detail, "Akun sistem") {
		t.Fatalf("pesan galat seharusnya menunjuk halaman Akun sistem: %q", p.Detail)
	}
	// PT bertemplate tetap memakai kode bawaannya tanpa dipilih.
	if s := status(f.company, ledger.Receivable); s.AccountID == nil || *s.AccountID != f.acc["1-1201"] || s.Chosen || s.Problem != "" {
		t.Fatalf("piutang usaha PT bertemplate = %+v", s)
	}

	set := func(role ledger.Role, id uuid.UUID) error {
		_, err := f.svc.SetSystemAccount(f.ctx, c.ID, role, id, f.actor)
		return err
	}
	expectCode(t, set(ledger.Receivable, header), "ledger.role_account_invalid")
	expectCode(t, set(ledger.Receivable, utang), "ledger.role_account_invalid")
	expectCode(t, set(ledger.Role("tidak_ada"), piutang), "ledger.role_unknown")
	for role, id := range map[ledger.Role]uuid.UUID{ledger.Receivable: piutang, ledger.Payable: utang, ledger.OpeningEquity: modal, ledger.FinalTaxExpense: bebanPajak} {
		if err := set(role, id); err != nil {
			t.Fatalf("pilih akun %s: %v", role, err)
		}
	}
	if s := status(c.ID, ledger.Receivable); s.AccountID == nil || *s.AccountID != piutang || !s.Chosen || s.Problem != "" {
		t.Fatalf("piutang usaha sesudah dipilih = %+v", s)
	}
	if r, err := f.svc.RoleAccount(f.ctx, c.ID, ledger.Receivable); err != nil || r.ID != piutang {
		t.Fatalf("RoleAccount piutang usaha = %+v, %v", r, err)
	}
	accounts, _ := f.svc.Accounts(f.ctx, c.ID)
	for _, a := range accounts {
		if a.ID == piutang && (!a.System || a.SystemRoles[0] != "piutang usaha") {
			t.Fatalf("akun yang dipilih seharusnya ditandai akun sistem: %+v", a)
		}
	}

	// Akun lama yang sudah bersaldo tidak bisa ditinggalkan; akun tanpa saldo bisa diganti.
	if _, err := f.svc.Post(f.ctx, ledger.EntryDraft{CompanyID: c.ID, Kind: ledger.General, Date: day("2026-10-05"), Memo: "uji",
		Lines: []ledger.LineDraft{{AccountID: piutang, Debit: money.MustParse("500")}, {AccountID: modal, Credit: money.MustParse("500")}}, Actor: f.actor}); err != nil {
		t.Fatal(err)
	}
	if p := expectCode(t, set(ledger.Receivable, piutangLain), "ledger.role_account_invalid"); !strings.Contains(p.Detail, "bersaldo") {
		t.Fatalf("pesan akun bersaldo = %q", p.Detail)
	}
	if err := set(ledger.Payable, create("2-11102", "Utang Dagang", groups["2"], true)); err != nil {
		t.Fatalf("ganti akun utang usaha yang belum bersaldo: %v", err)
	}

	// PPN keluaran per jenis faktur memakai akun PPN keluaran sampai dipilih sendiri.
	_, err = f.svc.RoleAccount(f.ctx, c.ID, ledger.OutputVATCollected)
	expectCode(t, err, "ledger.role_account")
	ppn := create("2-14102", "PPN Keluaran (Disetor Sendiri)", groups["2"], true)
	wapu := create("2-14103", "PPN Keluaran (Dipungut WAPU)", groups["2"], true)
	if err := set(ledger.OutputVAT, ppn); err != nil {
		t.Fatal(err)
	}
	if s := status(c.ID, ledger.OutputVATCollected); s.Fallback != ledger.OutputVAT || s.Problem != "" || s.AccountID != nil {
		t.Fatalf("PPN dipungut sebelum dipilih = %+v", s)
	}
	if r, err := f.svc.RoleAccount(f.ctx, c.ID, ledger.OutputVATCollected); err != nil || r.ID != ppn {
		t.Fatalf("PPN dipungut seharusnya memakai PPN keluaran: %+v, %v", r, err)
	}
	if err := set(ledger.OutputVATCollected, wapu); err != nil {
		t.Fatal(err)
	}
	if r, err := f.svc.RoleAccount(f.ctx, c.ID, ledger.OutputVATCollected); err != nil || r.ID != wapu {
		t.Fatalf("PPN dipungut sesudah dipilih = %+v, %v", r, err)
	}
	if r, _ := f.svc.RoleAccount(f.ctx, f.company, ledger.OutputVATRetail); r.ID != f.acc["2-1201"] {
		t.Fatalf("PPN digunggung PT bertemplate seharusnya 2-1201: %+v", r)
	}
}

// Revisi klien: akun yang salah dibuat dan belum pernah dipakai bisa dihapus; akun kelompok,
// akun yang punya anak, akun sistem, dan akun yang sudah dipakai jurnal atau data lain ditolak.
func TestDeleteUnusedAccount(t *testing.T) {
	f := setup(t)
	create := func(code, name string, parent uuid.UUID, postable bool) ledger.Account {
		t.Helper()
		a, err := f.svc.CreateAccount(f.ctx, f.company, ledger.NewAccount{Code: code, Name: name, ParentID: &parent, Postable: postable}, f.actor)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	del := func(a ledger.Account) error { return f.svc.DeleteAccount(f.ctx, f.company, a.ID, a.Version, f.actor) }
	exists := func(id uuid.UUID) bool {
		list, _ := f.svc.Accounts(f.ctx, f.company)
		for _, a := range list {
			if a.ID == id {
				return true
			}
		}
		return false
	}

	wrong := create("1-1139", "Bank salah ketik", f.acc["1-1100"], true)
	if err := f.svc.DeleteAccount(f.ctx, f.company, wrong.ID, wrong.Version+1, f.actor); errCode(err) != "conflict.version" {
		t.Fatalf("versi lama seharusnya ditolak: %v", err)
	}
	if err := del(wrong); err != nil || exists(wrong.ID) {
		t.Fatalf("hapus akun yang belum dipakai: %v", err)
	}

	header := create("1-1190", "Kelompok salah", f.acc["1-1100"], false)
	child := create("1-1191", "Anak", header.ID, true)
	if err := del(header); errCode(err) != "account.in_use" {
		t.Fatalf("akun header yang punya anak seharusnya ditolak: %v", err)
	}
	if err := del(child); err != nil {
		t.Fatal(err)
	}
	if err := del(header); err != nil {
		t.Fatalf("akun header kosong seharusnya bisa dihapus: %v", err)
	}

	group := ledger.Account{ID: f.acc["1"], Version: 1}
	if err := del(group); errCode(err) != "account.in_use" {
		t.Fatalf("akun kelompok seharusnya ditolak: %v", err)
	}
	if err := del(ledger.Account{ID: f.acc["1-1201"], Version: 1}); errCode(err) != "account.in_use" || !strings.Contains(problem(err).Detail, "akun sistem") {
		t.Fatalf("akun sistem seharusnya ditolak: %v", err)
	}

	used := create("1-1138", "Bank dipakai", f.acc["1-1100"], true)
	f.acc["1-1138"] = used.ID
	f.mustPost(ledger.General, "2026-10-05", f.line("1-1138", "1000", "0"), f.line("3-1001", "0", "1000"))
	if err := del(used); errCode(err) != "account.in_use" || !strings.Contains(problem(err).Detail, "jurnal") {
		t.Fatalf("akun yang sudah dipakai jurnal seharusnya ditolak: %v", err)
	}

	// Akun yang dipakai data lain (mis. didaftarkan sebagai rekening bank) ditolak foreign key.
	bank := create("1-1137", "Bank belum dipakai jurnal", f.acc["1-1100"], true)
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO cash.accounts (company_id, account_id, kind) VALUES ($1, $2, 'bank')", f.company, bank.ID); err != nil {
		t.Fatal(err)
	}
	if err := del(bank); errCode(err) != "account.in_use" || !strings.Contains(problem(err).Detail, "rekening kas/bank") {
		t.Fatalf("akun rekening bank seharusnya ditolak: %v", err)
	}

	// Penjaga di basis data tetap menolak akun kelompok dan akun berjurnal walau lewat SQL langsung.
	if _, err := f.pool.Exec(f.ctx, "DELETE FROM ledger.accounts WHERE id = $1", f.acc["1"]); err == nil {
		t.Fatal("trigger seharusnya menolak hapus akun kelompok")
	}
	if _, err := f.pool.Exec(f.ctx, "DELETE FROM ledger.accounts WHERE id = $1", used.ID); err == nil {
		t.Fatal("trigger seharusnya menolak hapus akun yang sudah dipakai jurnal")
	}
}

// Koreksi pengelompokan: akun detail atau akun header (beserta turunannya) bisa dipindah ke induk
// lain di kelas yang sama; induk harus header aktif, bukan turunannya sendiri, maksimal 5 tingkat.
func TestAccountReparent(t *testing.T) {
	f := setup(t)
	accounts := func() map[uuid.UUID]ledger.Account {
		list, err := f.svc.Accounts(f.ctx, f.company)
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID]ledger.Account{}
		for _, a := range list {
			out[a.ID] = a
		}
		return out
	}
	move := func(code, parent string) (ledger.Account, error) {
		a := accounts()[f.acc[code]]
		p := f.acc[parent]
		return f.svc.UpdateAccount(f.ctx, f.company, a.ID, ledger.AccountChange{Name: a.Name, Active: a.Active, Version: a.Version, ParentID: &p}, f.actor)
	}
	f.mustPost(ledger.General, "2026-10-05", f.line("6-1003", "500", "0"), f.line("1-1111", "0", "500"))

	bpjs, err := move("6-1003", "6-2000")
	if err != nil || bpjs.ParentID == nil || *bpjs.ParentID != f.acc["6-2000"] || bpjs.Level != 3 {
		t.Fatalf("pindah akun detail = %+v, %v", bpjs, err)
	}
	if l, err := f.svc.AccountLedger(f.ctx, f.company, bpjs.ID, day("2026-10-01"), day("2026-10-31")); err != nil || !l.Closing.Equal(money.MustParse("500")) {
		t.Fatalf("saldo sesudah pindah induk = %+v, %v", l, err)
	}

	// Akun header beserta anaknya: 6-1000 pindah ke bawah 6-2000, anak-anaknya turun ke level 4.
	pegawai, err := move("6-1000", "6-2000")
	if err != nil || pegawai.Level != 3 {
		t.Fatalf("pindah akun header = %+v, %v", pegawai, err)
	}
	if gaji := accounts()[f.acc["6-1001"]]; gaji.Level != 4 || *gaji.ParentID != f.acc["6-1000"] {
		t.Fatalf("anak akun header = %+v", gaji)
	}

	for _, c := range []struct{ name, code, parent string }{
		{"induk turunannya sendiri", "6-2000", "6-1000"},
		{"induk akun detail", "6-2002", "6-2001"},
		{"kelas lain", "6-2002", "1-1100"},
		{"akun kelompok", "6", "1"},
	} {
		if _, err := move(c.code, c.parent); errCode(err) != "account.invalid" {
			t.Errorf("%s: error = %v, seharusnya account.invalid", c.name, err)
		}
	}

	// Terlalu dalam: 6-1000 (dengan anak) ke bawah header level 4 membuat anaknya di level 6.
	h3, err := f.svc.CreateAccount(f.ctx, f.company, ledger.NewAccount{Code: "6-2100", Name: "Beban kantor", ParentID: ptr(f.acc["6-2000"])}, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	h4, err := f.svc.CreateAccount(f.ctx, f.company, ledger.NewAccount{Code: "6-2110", Name: "Beban kantor pusat", ParentID: &h3.ID}, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	f.acc["6-2110"] = h4.ID
	if _, err := move("6-1000", "6-2110"); errCode(err) != "account.invalid" {
		t.Fatalf("pohon lebih dari 5 tingkat seharusnya ditolak: %v", err)
	}

	// Penjaga di basis data tetap menolak induk dari kelas lain walau lewat SQL langsung.
	if _, err := f.pool.Exec(f.ctx, "UPDATE ledger.accounts SET parent_id = $1, level = 3 WHERE id = $2", f.acc["1-1100"], f.acc["6-2002"]); err == nil {
		t.Fatal("trigger seharusnya menolak induk dari kelas lain")
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE ledger.accounts SET parent_id = $1, level = 4 WHERE id = $2", f.acc["6-1000"], f.acc["6-2000"]); err == nil {
		t.Fatal("trigger seharusnya menolak induk yang merupakan turunan akun itu sendiri")
	}
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

func errCode(err error) string {
	if p := problem(err); p != nil {
		return p.Code
	}
	return ""
}

// Revisi klien: jurnal manual bisa diubah selama periodenya belum dikunci. Nomor tetap, versi
// lama tersimpan, saldo agregat ikut, dan pembalik otomatis jurnal penyesuaian ikut berubah.
func TestReviseManualJournal(t *testing.T) {
	f := setup(t)
	p := f.mustPost(ledger.General, "2026-10-05", f.line("6-2002", "100000", "0"), f.line("1-1101", "0", "100000"))
	revise := func(id uuid.UUID, date string, rev int, lines ...ledger.LineDraft) error {
		_, err := f.svc.Revise(f.ctx, f.company, id, ledger.ReviseInput{
			Date: day(date), Memo: "Listrik kantor (koreksi)", Lines: lines, Reason: "Salah akun", Revision: rev,
		}, f.actor)
		return err
	}
	if err := revise(p.ID, "2026-10-07", 0, f.line("6-2005", "150000", "0"), f.line("1-1101", "0", "150000")); err != nil {
		t.Fatal(err)
	}
	e, err := f.svc.Entry(f.ctx, f.company, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Number != p.Number || e.Revision != 1 || e.Date.String() != "2026-10-07" || !e.Total.Equal(money.FromInt(150_000)) ||
		e.Lines[0].AccountCode != "6-2005" || e.RevisedBy == nil || !e.Editable {
		t.Fatalf("jurnal setelah diubah = %+v", e)
	}
	if len(e.Revisions) != 1 || e.Revisions[0].Revision != 0 || e.Revisions[0].Lines[0].AccountCode != "6-2002" ||
		!e.Revisions[0].Total.Equal(money.FromInt(100_000)) || e.Revisions[0].Reason != "Salah akun" {
		t.Fatalf("riwayat versi = %+v", e.Revisions)
	}
	oct := f.worksheet("2026-10")
	if !adjusted(oct, "6-2002").IsZero() || !adjusted(oct, "6-2005").Equal(money.FromInt(150_000)) {
		t.Fatal("neraca lajur belum mengikuti isi jurnal yang baru")
	}
	f.verifyBalances()

	expectCode(t, revise(p.ID, "2026-10-07", 0, f.line("6-2005", "1", "0"), f.line("1-1101", "0", "1")), "conflict.version")
	expectCode(t, revise(p.ID, "2026-11-02", 1, f.line("6-2005", "1", "0"), f.line("1-1101", "0", "1")), "journal.edit_month")
	expectCode(t, revise(p.ID, "2026-10-07", 1, f.line("6-2005", "1", "0"), f.line("1-1101", "0", "2")), "ledger.unbalanced")

	// Penyesuaian dengan balik otomatis: pembaliknya di November ikut berubah.
	adj, err := f.svc.Post(f.ctx, ledger.EntryDraft{
		CompanyID: f.company, Kind: ledger.Adjustment, Date: day("2026-10-31"), Memo: "Akrual listrik",
		AutoReverse: true, Actor: f.actor, Lines: []ledger.LineDraft{f.line("6-2002", "3500000", "0"), f.line("2-1302", "0", "3500000")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := revise(adj.ID, "2026-10-31", 0, f.line("6-2002", "4000000", "0"), f.line("2-1302", "0", "4000000")); err != nil {
		t.Fatal(err)
	}
	rev, err := f.svc.Entry(f.ctx, f.company, adj.AutoReversal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Revision != 1 || !rev.Total.Equal(money.FromInt(4_000_000)) || !rev.Lines[0].Credit.Equal(money.FromInt(4_000_000)) {
		t.Fatalf("pembalik otomatis setelah diubah = %+v", rev)
	}
	expectCode(t, revise(adj.AutoReversal.ID, "2026-11-01", 1, f.line("6-2002", "1", "0"), f.line("2-1302", "0", "1")), "journal.edit_auto_reversal")
	if !adjusted(f.worksheet("2026-11"), "2-1302").IsZero() {
		t.Fatal("akrual seharusnya tetap nol setelah dibalik di November")
	}
	f.verifyBalances()

	// Periode ditutup untuk transaksi: jurnal umum terkunci, penyesuaian masih boleh.
	if err := f.svc.SetPeriodStatus(f.ctx, f.company, day("2026-10-01"), "adjusting", "", f.actor); err != nil {
		t.Fatal(err)
	}
	expectCode(t, revise(p.ID, "2026-10-07", 1, f.line("6-2005", "1", "0"), f.line("1-1101", "0", "1")), "period.adjusting")
	if e, err := f.svc.Entry(f.ctx, f.company, p.ID); err != nil || e.Editable || !strings.Contains(e.EditBlock, "Oktober 2026") {
		t.Fatalf("jurnal umum di periode penyesuaian seharusnya tidak bisa diubah: %+v %v", e.EditBlock, err)
	}
	if err := revise(adj.ID, "2026-10-31", 1, f.line("6-2002", "4200000", "0"), f.line("2-1302", "0", "4200000")); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetPeriodStatus(f.ctx, f.company, day("2026-10-01"), "locked", "", f.actor); err != nil {
		t.Fatal(err)
	}
	expectCode(t, revise(adj.ID, "2026-10-31", 2, f.line("6-2002", "1", "0"), f.line("2-1302", "0", "1")), "period.locked")

	// Jurnal yang sudah di-void tidak bisa diubah, dan basis data tetap menolak perubahan langsung.
	if err := f.svc.SetPeriodStatus(f.ctx, f.company, day("2026-10-01"), "open", "Koreksi dari auditor", f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Void(f.ctx, f.company, p.ID, day("2026-10-08"), "Dobel input", f.actor); err != nil {
		t.Fatal(err)
	}
	expectCode(t, revise(p.ID, "2026-10-07", 1, f.line("6-2005", "1", "0"), f.line("1-1101", "0", "1")), "journal.edit_void")
	for _, sql := range []string{
		"UPDATE ledger.journal_entries SET memo = 'diubah'",
		"DELETE FROM ledger.journal_lines",
		"DELETE FROM ledger.journal_revisions",
	} {
		if _, err := f.pool.Exec(f.ctx, sql); err == nil {
			t.Errorf("%q seharusnya ditolak basis data", sql)
		}
	}
	f.verifyBalances()
}

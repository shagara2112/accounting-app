package report_test

import (
	"testing"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/report"
)

// column mencari indeks kolom laporan berlabel label.
func column(t *testing.T, st report.Statement, label string) int {
	t.Helper()
	for i, c := range st.Columns {
		if c.Label == label {
			return i
		}
	}
	t.Fatalf("kolom %q tidak ada di %s", label, st.Title)
	return -1
}

// RPT-08: arus kas metode langsung. Setiap jurnal yang menyentuh kas dipilah menurut akun lawannya;
// transfer antarrekening kas tidak muncul, dan total per aktivitas sama dengan metode tidak langsung.
func TestCashFlowDirect(t *testing.T) {
	e := scenario(t)
	if _, err := e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{Kind: "out", Date: day("2026-10-25"), CashAccountID: e.Bank, Lines: []cash.LineInput{
		{AccountID: e.Acc["6-2002"], Amount: rp(500_000)},
		{AccountID: e.Acc["2-1201"], Amount: rp(1_100_000)}, // setor PPN keluaran
	}}, e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{Kind: "transfer", Date: day("2026-10-26"), CashAccountID: e.Bank, ToAccountID: &e.Petty, Amount: rp(2_000_000)}, e.Actor); err != nil {
		t.Fatal(err)
	}
	p := report.Period{From: day("2026-10-01"), To: day("2026-10-31")}
	cf, err := e.Report.CashFlowDirect(e.Ctx, e.Company, p)
	if err != nil {
		t.Fatal(err)
	}
	if !cf.Reconciled {
		t.Fatalf("arus kas langsung tidak cocok: awal %s + %s ≠ akhir %s", cf.Opening, cf.NetChange, cf.Closing)
	}
	st := cf.Statement
	eq(t, "dari pelanggan", line(t, st, "Penerimaan dari pelanggan").Values[0], 11_100_000)
	eq(t, "pelunasan piutang", line(t, st, "1-1201 Piutang usaha").Values[0], 11_100_000)
	eq(t, "ke pemasok dan karyawan", line(t, st, "Pembayaran kepada pemasok dan karyawan").Values[0], -500_000)
	eq(t, "pajak", line(t, st, "Penerimaan dan pembayaran pajak").Values[0], -1_100_000)
	eq(t, "operasi", line(t, st, "Kas bersih dari aktivitas operasi").Values[0], 9_500_000)
	eq(t, "investasi", line(t, st, "Kas bersih dari aktivitas investasi").Values[0], -20_000_000)
	eq(t, "pendanaan", line(t, st, "Kas bersih dari aktivitas pendanaan").Values[0], 50_000_000)
	eq(t, "kas awal", cf.Opening, 100_000_000)
	eq(t, "kas akhir", cf.Closing, 139_500_000)
	for _, l := range st.Lines {
		if l.Code == "1-1101" || l.Code == "1-1111" {
			t.Fatalf("rekening kas tidak boleh muncul sebagai lawan: %+v", l)
		}
	}

	ind, err := e.Report.CashFlow(e.Ctx, e.Company, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Kas bersih dari aktivitas operasi", "Kas bersih dari aktivitas investasi", "Kas bersih dari aktivitas pendanaan"} {
		if !line(t, ind.Statement, name).Values[0].Equal(line(t, st, name).Values[0]) {
			t.Errorf("%s: tidak langsung %s, langsung %s", name, line(t, ind.Statement, name).Values[0], line(t, st, name).Values[0])
		}
	}
}

// RPT-08: laporan perubahan ekuitas dengan kolom per akun ekuitas, reklas laba tahun lalu ke laba
// ditahan, penambahan, pengurangan, dan laba periode; saldo akhir sama dengan neraca.
func TestEquityChanges(t *testing.T) {
	e := fixture.New(t)
	journal(t, e, "2025-01-10", ledger.General, "1-1111", "3-1001", 100_000_000)
	journal(t, e, "2025-06-01", ledger.General, "1-1111", "4-1001", 30_000_000)
	journal(t, e, "2025-07-01", ledger.General, "6-2002", "1-1111", 12_000_000)
	if _, err := e.Ledger.CloseYear(e.Ctx, e.Company, 2025, e.Actor); err != nil {
		t.Fatal(err)
	}
	journal(t, e, "2026-02-01", ledger.General, "1-1111", "3-1002", 20_000_000) // tambahan modal
	journal(t, e, "2026-04-01", ledger.General, "3-2003", "1-1111", 5_000_000)  // dividen
	journal(t, e, "2026-05-01", ledger.General, "1-1111", "4-1001", 8_000_000)
	// Jurnal yang di-void dalam periode yang sama tidak menambah baris penambahan atau pengurangan.
	posted, err := e.Ledger.Post(e.Ctx, ledger.EntryDraft{CompanyID: e.Company, Kind: ledger.General, Date: day("2026-06-01"), Memo: "salah input",
		Lines: []ledger.LineDraft{{AccountID: e.Acc["1-1111"], Debit: rp(7_000_000)}, {AccountID: e.Acc["3-1002"], Credit: rp(7_000_000)}}, Actor: e.Actor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ledger.Void(e.Ctx, e.Company, posted.ID, day("2026-06-02"), "salah input modal", e.Actor); err != nil {
		t.Fatal(err)
	}

	ec, err := e.Report.EquityChanges(e.Ctx, e.Company, report.Period{From: day("2026-01-01"), To: day("2026-09-30")})
	if err != nil {
		t.Fatal(err)
	}
	if !ec.Reconciled {
		t.Fatalf("perubahan ekuitas tidak cocok: %+v", ec.Lines)
	}
	st := ec.Statement
	modal, tambahan, ditahan, dividen := column(t, st, "3-1001 Modal disetor"), column(t, st, "3-1002 Tambahan modal disetor"), column(t, st, "3-2001 Laba ditahan"), column(t, st, "3-2003 Dividen")
	laba, total := column(t, st, "Laba (rugi) belum direklas"), column(t, st, "Jumlah ekuitas")
	if len(st.Columns) != 6 {
		t.Fatalf("kolom = %+v", st.Columns)
	}
	first := line(t, st, "Saldo awal per 31 Desember 2025")
	eq(t, "modal awal", first.Values[modal], 100_000_000)
	eq(t, "laba 2025 belum direklas per awal", first.Values[laba], 18_000_000)
	eq(t, "ekuitas awal", first.Values[total], 118_000_000)
	reclass := line(t, st, "Reklas laba (rugi) ke laba ditahan")
	eq(t, "reklas ke laba ditahan", reclass.Values[ditahan], 18_000_000)
	eq(t, "reklas dari laba", reclass.Values[laba], -18_000_000)
	eq(t, "reklas tidak mengubah jumlah", reclass.Values[total], 0)
	eq(t, "penambahan", line(t, st, "Penambahan (setoran, alokasi, dan lainnya)").Values[tambahan], 20_000_000)
	eq(t, "pengurangan", line(t, st, "Pengurangan (penarikan, pembagian, dan lainnya)").Values[dividen], -5_000_000)
	eq(t, "laba periode", line(t, st, "Laba (rugi) periode ini").Values[laba], 8_000_000)
	last := line(t, st, "Saldo akhir per 30 September 2026")
	eq(t, "ekuitas akhir", last.Values[total], 141_000_000)
	eq(t, "laba ditahan akhir", last.Values[ditahan], 18_000_000)

	bs := balanceSheet(t, e, "2026-09-30")
	eq(t, "sama dengan neraca", line(t, bs, "Jumlah ekuitas").Values[0], 141_000_000)
	if _, err := e.Report.EquityChanges(e.Ctx, e.Company, report.Period{From: day("2026-02-01"), To: day("2026-01-01")}); code(err) != "report.range" {
		t.Fatalf("rentang terbalik: %v", err)
	}
}

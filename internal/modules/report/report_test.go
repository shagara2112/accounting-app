package report_test

import (
	"bytes"
	"testing"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/report"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/export"
	"lajur/internal/platform/money"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

func journal(t *testing.T, e *fixture.Env, date string, kind ledger.Kind, debit, credit string, amount int64) {
	t.Helper()
	if _, err := e.Ledger.Post(e.Ctx, ledger.EntryDraft{
		CompanyID: e.Company, Kind: kind, Date: day(date), Memo: "Uji laporan",
		Lines: []ledger.LineDraft{{AccountID: e.Acc[debit], Debit: rp(amount)}, {AccountID: e.Acc[credit], Credit: rp(amount)}},
		Actor: e.Actor,
	}); err != nil {
		t.Fatal(err)
	}
}

// scenario: modal 100 jt (September); Oktober: faktur jasa 10 jt + PPN dilunasi, tagihan sewa 4 jt +
// PPN belum dibayar, beli peralatan 20 jt tunai, pinjaman bank 50 jt, dan penyusutan 1 jt.
func scenario(t *testing.T) *fixture.Env {
	e := fixture.New(t)
	journal(t, e, "2026-09-15", ledger.General, "1-1111", "3-1001", 100_000_000)

	inv, err := e.Sales.CreateInvoice(e.Ctx, e.Company, sales.InvoiceInput{CustomerID: e.Customer, Date: day("2026-10-05"), Lines: []trade.LineInput{{
		Description: "Jasa", AccountID: e.Acc["4-1001"], Qty: money.FromInt(1), UnitPrice: rp(10_000_000), TaxCode: "PPN",
	}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if inv, err = e.Sales.PostInvoice(e.Ctx, e.Company, inv.ID, e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{CustomerID: e.Customer, Date: day("2026-10-20"), CashAccountID: e.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: inv.Total}}}, e.Actor); err != nil {
		t.Fatal(err)
	}
	b, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{SupplierID: e.Supplier, Date: day("2026-10-07"), Lines: []trade.LineInput{{
		Description: "Sewa", AccountID: e.Acc["6-2001"], Qty: money.FromInt(1), UnitPrice: rp(4_000_000), TaxCode: "PPN",
	}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.PostBill(e.Ctx, e.Company, b.ID, e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{Kind: "out", Date: day("2026-10-10"), CashAccountID: e.Bank,
		Lines: []cash.LineInput{{AccountID: e.Acc["1-2104"], Amount: rp(20_000_000)}}}, e.Actor); err != nil {
		t.Fatal(err)
	}
	journal(t, e, "2026-10-12", ledger.General, "1-1111", "2-2101", 50_000_000)
	journal(t, e, "2026-10-31", ledger.Adjustment, "6-3004", "1-2204", 1_000_000)
	return e
}

func line(t *testing.T, st report.Statement, name string) report.Line {
	t.Helper()
	for _, l := range st.Lines {
		if l.Name == name || l.Code+" "+l.Name == name {
			return l
		}
	}
	t.Fatalf("baris %q tidak ada di %s", name, st.Title)
	return report.Line{}
}

func eq(t *testing.T, label string, got money.Amount, want int64) {
	t.Helper()
	if !got.Equal(rp(want)) {
		t.Errorf("%s = %s, seharusnya %d", label, got, want)
	}
}

// RPT-01: laba rugi bertingkat, termasuk rentang yang tidak mulai di awal bulan.
func TestProfitLoss(t *testing.T) {
	e := scenario(t)
	st, err := e.Report.ProfitLoss(e.Ctx, e.Company, []report.Period{
		{From: day("2026-10-01"), To: day("2026-10-31")},
		{From: day("2026-10-05"), To: day("2026-10-10")},
		{From: day("2026-09-15"), To: day("2026-10-31")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Columns[0].Label != "Oktober 2026" {
		t.Errorf("label kolom = %q", st.Columns[0].Label)
	}
	eq(t, "pendapatan Okt", line(t, st, "Jumlah pendapatan usaha").Values[0], 10_000_000)
	eq(t, "beban usaha Okt", line(t, st, "Jumlah beban usaha").Values[0], 5_000_000)
	eq(t, "laba bersih Okt", line(t, st, "Laba (rugi) bersih").Values[0], 5_000_000)
	// 5–10 Okt: pendapatan 10 jt, sewa 4 jt; penyusutan akhir bulan belum masuk.
	eq(t, "laba bersih 5–10 Okt", line(t, st, "Laba (rugi) bersih").Values[1], 6_000_000)
	eq(t, "laba bersih 15 Sep–31 Okt", line(t, st, "Laba (rugi) bersih").Values[2], 5_000_000)
}

// RPT-01: neraca seimbang dengan laba tahun berjalan di ekuitas, dan komparatif per tanggal lain.
func TestBalanceSheet(t *testing.T) {
	e := scenario(t)
	st, err := e.Report.BalanceSheet(e.Ctx, e.Company, []civil.Date{day("2026-10-31"), day("2026-09-30"), day("2026-10-12")})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Balanced[0] || !st.Balanced[1] || !st.Balanced[2] {
		t.Fatalf("neraca tidak seimbang: %v", st.Balanced)
	}
	// Per 12 Oktober (hari pinjaman bank masuk): piutang belum dilunasi dan penyusutan akhir bulan belum ada.
	eq(t, "aset 12 Okt", line(t, st, "Jumlah aset").Values[2], 161_540_000)
	eq(t, "laba s.d. 12 Okt", line(t, st, "Laba (rugi) tahun berjalan").Values[2], 6_000_000)
	eq(t, "aset 31 Okt", line(t, st, "Jumlah aset").Values[0], 160_540_000)
	eq(t, "aset 30 Sep", line(t, st, "Jumlah aset").Values[1], 100_000_000)
	eq(t, "liabilitas", line(t, st, "Jumlah liabilitas").Values[0], 55_540_000)
	eq(t, "laba tahun berjalan", line(t, st, "Laba (rugi) tahun berjalan").Values[0], 5_000_000)
	eq(t, "akumulasi penyusutan (kontra aset)", line(t, st, "1-2204 Akumulasi penyusutan peralatan kantor").Values[0], -1_000_000)

	var b bytes.Buffer
	if err := export.WriteXLSX(&b, st.Table("Per 31 Oktober 2026")); err != nil || b.Len() == 0 {
		t.Fatalf("ekspor Excel gagal: %v", err)
	}
}

// RPT-02: arus kas tidak langsung dari laba bersih; kas awal + kenaikan bersih = kas akhir.
func TestCashFlow(t *testing.T) {
	e := scenario(t)
	cf, err := e.Report.CashFlow(e.Ctx, e.Company, report.Period{From: day("2026-10-01"), To: day("2026-10-31")})
	if err != nil {
		t.Fatal(err)
	}
	if !cf.Reconciled {
		t.Fatalf("arus kas tidak cocok: awal %s + %s ≠ akhir %s", cf.Opening, cf.NetChange, cf.Closing)
	}
	eq(t, "operasi", line(t, cf.Statement, "Kas bersih dari aktivitas operasi").Values[0], 11_100_000)
	eq(t, "investasi", line(t, cf.Statement, "Kas bersih dari aktivitas investasi").Values[0], -20_000_000)
	eq(t, "pendanaan", line(t, cf.Statement, "Kas bersih dari aktivitas pendanaan").Values[0], 50_000_000)
	eq(t, "penyusutan", line(t, cf.Statement, "1-2204 Akumulasi penyusutan peralatan kantor").Values[0], 1_000_000)
	eq(t, "kas awal", cf.Opening, 100_000_000)
	eq(t, "kas akhir", cf.Closing, 141_100_000)
}

// RPT-03, RPT-04, RPT-05, RPT-07.
func TestTrialBalanceSummariesCardsAndDashboard(t *testing.T) {
	e := scenario(t)
	tb, err := e.Report.TrialBalance(e.Ctx, e.Company, day("2026-10-31"))
	if err != nil {
		t.Fatal(err)
	}
	if !tb.Balanced || tb.TotalDebit.IsZero() {
		t.Fatalf("neraca saldo = %s / %s", tb.TotalDebit, tb.TotalCredit)
	}

	p := report.Period{From: day("2026-10-01"), To: day("2026-10-31")}
	s, err := e.Report.Summary(e.Ctx, e.Company, report.ByCustomer, p)
	if err != nil || len(s.Rows) != 1 || !s.Total.DPP.Equal(rp(10_000_000)) || s.Total.Documents != 1 {
		t.Fatalf("penjualan per pelanggan = %+v (%v)", s, err)
	}
	s, err = e.Report.Summary(e.Ctx, e.Company, report.BySupplier, p)
	if err != nil || len(s.Rows) != 1 || !s.Total.Total.Equal(rp(4_440_000)) {
		t.Fatalf("pembelian per pemasok = %+v (%v)", s, err)
	}

	card, err := e.Report.ContactCard(e.Ctx, e.Company, "customer", e.Customer, p)
	if err != nil || len(card.Lines) != 2 || !card.Closing.IsZero() || !card.Lines[0].Balance.Equal(rp(11_100_000)) {
		t.Fatalf("kartu pelanggan = %+v (%v)", card, err)
	}
	card, err = e.Report.ContactCard(e.Ctx, e.Company, "supplier", e.Supplier, report.Period{From: day("2026-10-08"), To: day("2026-10-31")})
	if err != nil || !card.Opening.Equal(rp(4_440_000)) || len(card.Lines) != 0 || !card.Closing.Equal(rp(4_440_000)) {
		t.Fatalf("kartu pemasok dengan saldo awal = %+v (%v)", card, err)
	}

	d, err := e.Report.Dashboard(e.Ctx, e.Company, day("2026-10-31"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "kas", d.CashTotal, 141_100_000)
	eq(t, "utang lewat jatuh tempo", d.Payable.Overdue, 4_440_000)
	eq(t, "piutang", d.Receivable.Total, 0)
	eq(t, "laba bulan ini", d.ThisMonth.Profit, 5_000_000)
	eq(t, "laba bulan lalu", d.LastMonth.Profit, 0)
	if len(d.Trend) != 6 || d.Trend[5].Month != "2026-10" {
		t.Fatalf("tren = %+v", d.Trend)
	}
	eq(t, "pendapatan tren Okt", d.Trend[5].Revenue, 10_000_000)
}

// AP-06: proyeksi kas per minggu dari saldo kas, piutang, dan utang yang jatuh tempo.
func TestCashProjection(t *testing.T) {
	e := scenario(t) // bank 141,1 jt; tagihan sewa 4,44 jt jatuh tempo 21 Oktober
	inv, err := e.Sales.CreateInvoice(e.Ctx, e.Company, sales.InvoiceInput{CustomerID: e.Customer, Date: day("2026-10-25"), DueDate: day("2026-11-10"), Lines: []trade.LineInput{{
		Description: "Jasa", AccountID: e.Acc["4-1001"], Qty: money.FromInt(1), UnitPrice: rp(2_000_000), TaxCode: "PPN",
	}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sales.PostInvoice(e.Ctx, e.Company, inv.ID, e.Actor); err != nil {
		t.Fatal(err)
	}
	p, err := e.Report.CashProjection(e.Ctx, e.Company, day("2026-11-01"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Buckets) != 6 || !p.Cash.Equal(rp(141_100_000)) {
		t.Fatalf("proyeksi = %+v", p)
	}
	// Tagihan sewa sudah lewat jatuh tempo; faktur 2,22 jt jatuh tempo di minggu kedua.
	eq(t, "utang lewat jatuh tempo", p.Buckets[0].Out, 4_440_000)
	eq(t, "piutang minggu 2", p.Buckets[2].In, 2_220_000)
	eq(t, "saldo akhir", p.Buckets[5].Balance, 141_100_000-4_440_000+2_220_000)
	if p.FirstShortfall != nil {
		t.Fatalf("tidak seharusnya ada kekurangan kas: %s", *p.FirstShortfall)
	}
}

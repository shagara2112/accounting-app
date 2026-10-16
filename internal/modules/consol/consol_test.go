package consol_test

import (
	"testing"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/consol"
	"lajur/internal/modules/core"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/report"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/money"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

// group membuat PT A (jasa) dan PT B (konstruksi) dalam satu grup.
func group(t *testing.T) (*fixture.Env, *fixture.Env) {
	a := fixture.NewWith(t, "jasa")
	b := a.Sibling("PT Uji konstruksi", "konstruksi")
	return a, b
}

// link membuat kontak di PT `in` yang mewakili PT `other`, sebagai pelanggan dan pemasok.
func link(t *testing.T, in *fixture.Env, other uuid.UUID, name, revenue, expense string) core.Contact {
	t.Helper()
	rev, exp := in.Acc[revenue], in.Acc[expense]
	c, err := in.Core.CreateContact(in.Ctx, in.Company, core.ContactInput{
		Name: name, IsCustomer: true, IsSupplier: true, PKP: true, NPWP: "0101010101010101", PaymentTermsDays: 30,
		LinkedCompanyID: &other, ICRevenueAccountID: &rev, ICExpenseAccountID: &exp, ICCashAccountID: &in.Bank,
	}, in.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func invoice(t *testing.T, e *fixture.Env, customer uuid.UUID, date string, amount int64) (sales.InvoiceDetail, error) {
	t.Helper()
	d, err := e.Sales.CreateInvoice(e.Ctx, e.Company, sales.InvoiceInput{CustomerID: customer, Date: day(date), Lines: []trade.LineInput{{
		Description: "Jasa desain struktur", AccountID: e.Acc["4-1001"], Qty: money.FromInt(1), UnitPrice: rp(amount), TaxCode: "PPN",
	}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return e.Sales.PostInvoice(e.Ctx, e.Company, d.ID, e.Actor)
}

// ME-03: faktur PT A ke PT B membuat tagihan diposting di PT B; void di salah satu sisi
// mem-void sisi lainnya, dan void ditolak bila pasangannya sudah dibayar.
func TestIntercompanyMirror(t *testing.T) {
	a, b := group(t)
	toB := link(t, a, b.Company, "PT Uji konstruksi (grup)", "4-1001", "6-2008")

	// PT B belum punya kontak balik: posting ditolak dan faktur tetap draf.
	if _, err := invoice(t, a, toB.ID, "2026-10-05", 10_000_000); code(err) != "intercompany.no_contact" {
		t.Fatalf("faktur ke PT grup tanpa kontak balik seharusnya ditolak: %v", err)
	}
	toA := link(t, b, a.Company, "PT Uji jasa (grup)", "4-1002", "5-1003")

	inv, err := invoice(t, a, toB.ID, "2026-10-06", 10_000_000)
	if err != nil {
		t.Fatal(err)
	}
	l, err := a.Consol.Link(a.Ctx, a.Company, sales.SourceInvoice, inv.ID)
	if err != nil || l == nil || l.CompanyID != b.Company || l.DocType != purchase.SourceBill || !l.Mirror {
		t.Fatalf("pasangan faktur = %+v (%v)", l, err)
	}
	bill, err := b.Purchase.Bill(b.Ctx, b.Company, l.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if bill.Status != "posted" || !bill.Total.Equal(inv.Total) || bill.SupplierID != toA.ID || bill.SupplierInvoiceNo != *inv.Number {
		t.Fatalf("tagihan cerminan = %+v", bill.Bill)
	}
	if !b.Net("5-1003", day("2026-10-31")).Equal(rp(10_000_000)) || !b.Net("1-1401", day("2026-10-31")).Equal(rp(1_100_000)) {
		t.Fatal("tagihan cerminan harus membebani akun beban dan PPN masukan PT B")
	}
	back, err := b.Consol.Link(b.Ctx, b.Company, purchase.SourceBill, bill.ID)
	if err != nil || back == nil || back.DocID != inv.ID || back.Mirror {
		t.Fatalf("pasangan dari sisi tagihan = %+v (%v)", back, err)
	}

	// Void tagihan di PT B ikut mem-void faktur di PT A.
	if _, err := b.Purchase.VoidBill(b.Ctx, b.Company, bill.ID, day("2026-10-10"), "Salah tagih antar-PT", b.Actor); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.Sales.Invoice(a.Ctx, a.Company, inv.ID); d.Status != "void" {
		t.Fatalf("faktur pasangan harus ikut void, status %s", d.Status)
	}

	// Arah sebaliknya: tagihan PT B dari PT A membuat faktur di PT A.
	draft, err := b.Purchase.CreateBill(b.Ctx, b.Company, purchase.BillInput{SupplierID: toA.ID, Date: day("2026-10-12"), Lines: []trade.LineInput{{
		Description: "Konsultasi", AccountID: b.Acc["5-1003"], Qty: money.FromInt(1), UnitPrice: rp(4_000_000), TaxCode: "PPN",
	}}}, b.Actor)
	if err != nil {
		t.Fatal(err)
	}
	bill2, err := b.Purchase.PostBill(b.Ctx, b.Company, draft.ID, b.Actor)
	if err != nil {
		t.Fatal(err)
	}
	l2, _ := b.Consol.Link(b.Ctx, b.Company, purchase.SourceBill, bill2.ID)
	if l2 == nil || l2.DocType != sales.SourceInvoice {
		t.Fatalf("faktur cerminan = %+v", l2)
	}
	if _, err := b.Purchase.CreatePayment(b.Ctx, b.Company, purchase.PaymentInput{SupplierID: toA.ID, Date: day("2026-10-15"), CashAccountID: b.Bank,
		Allocations: []purchase.AllocationInput{{BillID: bill2.ID, Amount: bill2.Payable}}}, b.Actor); err != nil {
		t.Fatal(err)
	}
	// Pembayaran di PT B ikut melunasi faktur cerminannya di PT A (ME-03).
	if d, _ := a.Sales.Invoice(a.Ctx, a.Company, l2.DocID); d.State != "paid" || len(d.Payments) != 1 {
		t.Fatalf("faktur cerminan harus ikut lunas: %+v", d.Invoice)
	}
	if _, err := a.Sales.VoidInvoice(a.Ctx, a.Company, l2.DocID, day("2026-10-16"), "Batal antar-PT", a.Actor); code(err) != "invoice.has_receipts" {
		t.Fatalf("void faktur yang pasangannya sudah dibayar seharusnya ditolak: %v", err)
	}
	if d, _ := a.Sales.Invoice(a.Ctx, a.Company, l2.DocID); d.Status != "posted" {
		t.Fatal("faktur harus tetap diposting bila void pasangannya gagal")
	}
	a.VerifyLedger()
	b.VerifyLedger()
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

func journal(t *testing.T, e *fixture.Env, date, debit, credit string, amount int64) {
	t.Helper()
	if _, err := e.Ledger.Post(e.Ctx, ledger.EntryDraft{
		CompanyID: e.Company, Kind: ledger.General, Date: day(date), Memo: "Uji konsolidasi",
		Lines: []ledger.LineDraft{{AccountID: e.Acc[debit], Debit: rp(amount)}, {AccountID: e.Acc[credit], Credit: rp(amount)}},
		Actor: e.Actor,
	}); err != nil {
		t.Fatal(err)
	}
}

// ME-04: konsolidasi = jumlah PT + eliminasi piutang–utang dan pendapatan–beban antar-PT;
// akun berkode sama tetapi berbeda isi dipisah lewat pemetaan.
func TestConsolidation(t *testing.T) {
	a, b := group(t)
	toB := link(t, a, b.Company, "PT Uji konstruksi (grup)", "4-1001", "6-2008")
	link(t, b, a.Company, "PT Uji jasa (grup)", "4-1002", "5-1003")
	journal(t, a, "2026-09-01", "1-1111", "3-1001", 100_000_000)
	journal(t, b, "2026-09-01", "1-1111", "3-1001", 200_000_000)
	// PT B menagih pelanggan luar 50 jt; PT A menagih PT B 10 jt + PPN, dibayar 4 jt.
	if _, err := invoice(t, b, b.Customer, "2026-10-03", 50_000_000); err != nil {
		t.Fatal(err)
	}
	inv, err := invoice(t, a, toB.ID, "2026-10-06", 10_000_000)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := a.Consol.Link(a.Ctx, a.Company, sales.SourceInvoice, inv.ID)
	if _, err := b.Purchase.CreatePayment(b.Ctx, b.Company, purchase.PaymentInput{SupplierID: mustLinked(t, b, a.Company), Date: day("2026-10-20"), CashAccountID: b.Bank,
		Allocations: []purchase.AllocationInput{{BillID: l.DocID, Amount: rp(4_000_000)}}}, b.Actor); err != nil {
		t.Fatal(err)
	}
	// Pelunasan faktur di PT A tercatat otomatis dari pembayaran PT B (ME-03).
	if d, _ := a.Sales.Invoice(a.Ctx, a.Company, inv.ID); !d.Paid.Equal(rp(4_000_000)) {
		t.Fatalf("pelunasan cerminan di PT A = %s", d.Paid)
	}

	// 4-1001 berarti "Pendapatan jasa" di PT A tetapi "Pendapatan proyek konstruksi" di PT B.
	chart, err := a.Consol.GroupChart(a.Ctx, a.Actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	conflict := false
	for _, g := range chart.Accounts {
		if g.Code == "4-1001" {
			conflict = g.Conflict && len(g.Members) == 2
		}
	}
	if !conflict {
		t.Fatal("4-1001 seharusnya ditandai bernama berbeda di dua PT")
	}
	parent := "4-1000"
	if err := a.Consol.SaveGroupAccount(a.Ctx, consol.GroupAccountInput{Code: "4-1011", Name: "Pendapatan proyek konstruksi", ParentCode: &parent}, a.Actor); err != nil {
		t.Fatal(err)
	}
	if err := a.Consol.SetMapping(b.Ctx, b.Company, b.Acc["4-1001"], "4-1000", b.Actor); code(err) != "consol.invalid" {
		t.Fatalf("pemetaan ke akun header seharusnya ditolak: %v", err)
	}
	if err := a.Consol.SetMapping(b.Ctx, b.Company, b.Acc["4-1001"], "5-1003", b.Actor); code(err) != "consol.invalid" {
		t.Fatalf("pemetaan beda kelas seharusnya ditolak: %v", err)
	}
	if err := a.Consol.SetMapping(b.Ctx, b.Company, b.Acc["4-1001"], "4-1011", b.Actor); err != nil {
		t.Fatal(err)
	}

	c, err := a.Consol.Consolidate(a.Ctx, a.Actor.UserID, []uuid.UUID{a.Company, b.Company}, day("2026-10-31"),
		report.Period{From: day("2026-10-01"), To: day("2026-10-31")})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Problems) > 0 {
		t.Fatalf("masalah pemetaan: %v", c.Problems)
	}
	for i, ok := range c.BalanceSheet.Balanced {
		if !ok {
			t.Fatalf("kolom %s neraca konsolidasi tidak seimbang", c.BalanceSheet.Columns[i].Label)
		}
	}
	if len(c.Differences) != 0 || len(c.Eliminations) != 2 {
		t.Fatalf("eliminasi %+v, selisih %+v", c.Eliminations, c.Differences)
	}
	last := len(c.BalanceSheet.Columns) - 1 // kolom konsolidasi
	// Piutang A ke B (7,1 jt) hilang dari piutang grup; yang tersisa hanya piutang pelanggan luar.
	if got := line(t, c.BalanceSheet, "1-1201 Piutang usaha"); !got.Values[last].Equal(rp(55_500_000)) || !got.Values[last-1].Equal(rp(-7_100_000)) {
		t.Fatalf("piutang konsolidasi = %v", got.Values)
	}
	if got := line(t, c.BalanceSheet, "Jumlah ekuitas"); !got.Values[last].Equal(rp(350_000_000)) {
		t.Fatalf("ekuitas grup = %s, seharusnya modal 300 jt + laba 50 jt", got.Values[last])
	}
	// Pendapatan antar-PT 10 jt dan beban PT B 10 jt dieliminasi; laba grup tetap 50 jt.
	if got := line(t, c.ProfitLoss, "Jumlah pendapatan usaha"); !got.Values[last].Equal(rp(50_000_000)) {
		t.Fatalf("pendapatan grup = %v", got.Values)
	}
	if got := line(t, c.ProfitLoss, "4-1011 Pendapatan proyek konstruksi"); !got.Values[1].Equal(rp(50_000_000)) {
		t.Fatalf("pendapatan konstruksi terpetakan = %v", got.Values)
	}
	if got := line(t, c.ProfitLoss, "Laba (rugi) bersih"); !got.Values[last].Equal(rp(50_000_000)) {
		t.Fatalf("laba grup = %v", got.Values)
	}

	// Tanpa izin di salah satu PT, konsolidasi ditolak.
	if _, err := a.Consol.Consolidate(a.Ctx, a.Other.UserID, []uuid.UUID{a.Company}, day("2026-10-31"),
		report.Period{From: day("2026-10-01"), To: day("2026-10-31")}); code(err) != "consol.denied" {
		t.Fatalf("pengguna tanpa izin konsolidasi seharusnya ditolak: %v", err)
	}
}

func mustLinked(t *testing.T, e *fixture.Env, other uuid.UUID) uuid.UUID {
	t.Helper()
	c, err := e.Core.LinkedContact(e.Ctx, e.Company, other)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// ME-03, AR-05: nota kredit atas faktur antar-PT membuat nota debit atas tagihan cerminannya
// dengan DPP dan PPN yang sama; void ikut berpasangan; nota atas dokumen cerminan ditolak.
func TestIntercompanyNotesMirror(t *testing.T) {
	a, b := group(t)
	toB := link(t, a, b.Company, "PT Uji konstruksi (grup)", "4-1001", "6-2008")
	link(t, b, a.Company, "PT Uji jasa (grup)", "4-1002", "5-1003")
	inv, err := invoice(t, a, toB.ID, "2026-10-06", 10_000_000)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := a.Consol.Link(a.Ctx, a.Company, sales.SourceInvoice, inv.ID)
	price := rp(2_500_000)
	n, err := a.Sales.CreateCreditNote(a.Ctx, a.Company, sales.CreditNoteInput{InvoiceID: inv.ID, Date: day("2026-10-09"), Memo: "Revisi lingkup",
		Lines: []sales.CreditLineInput{{InvoiceLineID: inv.Lines[0].ID, Qty: money.FromInt(1), UnitPrice: &price}}}, a.Actor)
	if err != nil {
		t.Fatal(err)
	}
	nl, err := a.Consol.Link(a.Ctx, a.Company, sales.SourceCreditNote, n.ID)
	if err != nil || nl == nil || nl.DocType != purchase.SourceDebitNote || nl.CompanyID != b.Company {
		t.Fatalf("pasangan nota kredit = %+v (%v)", nl, err)
	}
	d, err := b.Purchase.DebitNote(b.Ctx, b.Company, nl.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if d.BillID != l.DocID || !d.DPP.Equal(n.DPP) || !d.VAT.Equal(n.VAT) || !d.Total.Equal(n.Total) {
		t.Fatalf("nota debit cerminan = %+v, nota kredit = %+v", d.DebitNote, n.CreditNote)
	}
	if !b.Net("5-1003", day("2026-10-31")).Equal(rp(7_500_000)) {
		t.Fatalf("beban PT B setelah nota = %s", b.Net("5-1003", day("2026-10-31")))
	}

	// Nota debit langsung atas tagihan cerminan ditolak: retur dicatat dari faktur aslinya.
	bill, _ := b.Purchase.Bill(b.Ctx, b.Company, l.DocID)
	if _, err := b.Purchase.CreateDebitNote(b.Ctx, b.Company, purchase.DebitNoteInput{BillID: bill.ID, Date: day("2026-10-10"),
		Lines: []purchase.DebitLineInput{{BillLineID: bill.Lines[0].ID, Qty: money.FromInt(1), UnitPrice: &price}}}, b.Actor); code(err) != "intercompany.note_source" {
		t.Fatalf("nota atas dokumen cerminan seharusnya ditolak: %v", err)
	}

	// Void nota debit di PT B ikut mem-void nota kredit di PT A.
	if _, err := b.Purchase.VoidDebitNote(b.Ctx, b.Company, d.ID, day("2026-10-12"), "Batal revisi", b.Actor); err != nil {
		t.Fatal(err)
	}
	if c, _ := a.Sales.CreditNote(a.Ctx, a.Company, n.ID); c.Status != "void" {
		t.Fatalf("nota kredit pasangan harus ikut void, status %s", c.Status)
	}
	a.VerifyLedger()
	b.VerifyLedger()
}

// ME-03: pelunasan faktur antar-PT di PT A otomatis membayar tagihan cerminannya di PT B dari
// rekening kas antar-PT; void satu sisi mem-void sisi lainnya.
func TestIntercompanyReceiptMirror(t *testing.T) {
	a, b := group(t)
	toB := link(t, a, b.Company, "PT Uji konstruksi (grup)", "4-1001", "6-2008")
	link(t, b, a.Company, "PT Uji jasa (grup)", "4-1002", "5-1003")
	inv, err := invoice(t, a, toB.ID, "2026-10-06", 10_000_000)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := a.Sales.CreateReceipt(a.Ctx, a.Company, sales.ReceiptInput{CustomerID: toB.ID, Date: day("2026-10-15"), CashAccountID: a.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: rp(5_000_000)}}}, a.Actor)
	if err != nil {
		t.Fatal(err)
	}
	l, err := a.Consol.Link(a.Ctx, a.Company, sales.SourceReceipt, rc.ID)
	if err != nil || l == nil || l.DocType != purchase.SourcePayment {
		t.Fatalf("pasangan pelunasan = %+v (%v)", l, err)
	}
	if !b.Net("1-1111", day("2026-10-31")).Equal(rp(-5_000_000)) || !b.Net("2-1101", day("2026-10-31")).Equal(rp(-6_100_000)) {
		t.Fatalf("PT B: bank %s, utang %s", b.Net("1-1111", day("2026-10-31")), b.Net("2-1101", day("2026-10-31")))
	}
	if _, err := b.Purchase.VoidPayment(b.Ctx, b.Company, l.DocID, day("2026-10-16"), "Salah tanggal", b.Actor); err != nil {
		t.Fatal(err)
	}
	if r, _ := a.Sales.Receipt(a.Ctx, a.Company, rc.ID); r.Status != "void" {
		t.Fatalf("pelunasan pasangan harus ikut void, status %s", r.Status)
	}
	// Pelunasan antar-PT dengan PPh dipotong tidak dicerminkan otomatis.
	if _, err := a.Sales.CreateReceipt(a.Ctx, a.Company, sales.ReceiptInput{CustomerID: toB.ID, Date: day("2026-10-20"), CashAccountID: a.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: rp(1_000_000), WithholdingCode: "PPH23", Withholding: rp(20_000)}}}, a.Actor); code(err) != "intercompany.withholding" {
		t.Fatalf("pelunasan antar-PT dengan PPh seharusnya ditolak: %v", err)
	}
	a.VerifyLedger()
	b.VerifyLedger()
}

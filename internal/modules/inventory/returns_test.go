package inventory_test

import (
	"strings"
	"testing"

	"lajur/internal/modules/fixture"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
)

// AR-05: retur penjualan sebagian mengembalikan barang ke stok dengan HPP saat barang keluar,
// mengurangi piutang, pendapatan, dan PPN keluaran; nota kredit atas faktur yang sudah lunas
// menjadi uang muka pelanggan; faktur bernota kredit tidak bisa di-void sebelum notanya.
func TestSalesReturnRestocksAtIssueCost(t *testing.T) {
	e := fixture.NewWith(t, "retail")
	p := goods(t, e, "BRG-1")
	buy(t, e, "2026-10-01", p, "10", 1_000)
	buy(t, e, "2026-10-02", p, "10", 1_300)
	inv, err := e.Sales.PostInvoice(e.Ctx, e.Company, sellDraft(t, e, "2026-10-05", p, "10").ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	// HPP 10 × 1.150 = 11.500; piutang 20.000 + PPN 2.200 = 22.200.
	buy(t, e, "2026-10-06", p, "10", 2_000) // rata-rata baru tidak memengaruhi nilai retur

	in := sales.CreditNoteInput{InvoiceID: inv.ID, Date: day("2026-10-08"), Memo: "Barang cacat",
		Lines: []sales.CreditLineInput{{InvoiceLineID: inv.Lines[0].ID, Qty: qty("4"), Restock: true}}}
	n, err := e.Sales.CreateCreditNote(e.Ctx, e.Company, in, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if n.Number != "NK/2026/10/0001" || !n.DPP.Equal(rp(8_000)) || !n.VAT.Equal(rp(880)) || !n.Applied.Equal(rp(8_880)) || !n.Excess.IsZero() {
		t.Fatalf("nota kredit = %+v", n.CreditNote)
	}
	// Barang kembali senilai 4 × 1.150 = 4.600, bukan rata-rata saat retur.
	if !e.Net("5-1001", day("2026-10-31")).Equal(rp(11_500 - 4_600)) {
		t.Fatalf("HPP setelah retur = %s", e.Net("5-1001", day("2026-10-31")))
	}
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.Equal(qty("24")) {
		t.Fatalf("stok setelah retur = %+v", s)
	}
	if !e.Net("1-1201", day("2026-10-31")).Equal(rp(22_200-8_880)) || !e.Net("2-1201", day("2026-10-31")).Equal(rp(-(2_200 - 880))) ||
		!e.Net("4-1001", day("2026-10-31")).Equal(rp(-(20_000 - 8_000))) {
		t.Fatalf("piutang %s, PPN keluaran %s, pendapatan %s tidak berkurang sebesar nota",
			e.Net("1-1201", day("2026-10-31")), e.Net("2-1201", day("2026-10-31")), e.Net("4-1001", day("2026-10-31")))
	}
	d, err := e.Sales.Invoice(e.Ctx, e.Company, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Credited.Equal(rp(8_880)) || !d.Outstanding.Equal(rp(13_320)) || len(d.CreditNotes) != 1 ||
		!d.Lines[0].ReturnedQty.Equal(qty("4")) || !d.Lines[0].CreditedNet.Equal(rp(8_000)) {
		t.Fatalf("faktur setelah nota = %+v", d)
	}

	// Retur melebihi kuantitas faktur ditolak.
	in.Lines[0].Qty = qty("7")
	if _, err := e.Sales.CreateCreditNote(e.Ctx, e.Company, in, e.Actor); code(err) != "credit_note.invalid" ||
		!strings.Contains(fixture.Fields(err)["lines.0.qty"], "sudah diretur 4") {
		t.Fatalf("retur berlebih seharusnya ditolak: %v", err)
	}
	if _, err := e.Sales.VoidInvoice(e.Ctx, e.Company, inv.ID, day("2026-10-09"), "Batal", e.Actor); code(err) != "invoice.has_credit_notes" {
		t.Fatalf("void faktur bernota kredit seharusnya ditolak: %v", err)
	}

	// Lunasi sisanya; potongan harga sesudahnya menjadi uang muka pelanggan.
	if _, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{
		CustomerID: e.Customer, Date: day("2026-10-10"), CashAccountID: e.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: rp(13_320)}},
	}, e.Actor); err != nil {
		t.Fatal(err)
	}
	price := rp(100)
	n2, err := e.Sales.CreateCreditNote(e.Ctx, e.Company, sales.CreditNoteInput{InvoiceID: inv.ID, Date: day("2026-10-12"), Memo: "Potongan harga",
		Lines: []sales.CreditLineInput{{InvoiceLineID: inv.Lines[0].ID, Qty: qty("6"), UnitPrice: &price}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if !n2.Applied.IsZero() || !n2.Excess.Equal(rp(666)) || !e.Net("2-1401", day("2026-10-31")).Equal(rp(-666)) {
		t.Fatalf("kelebihan nota = %+v, uang muka %s", n2.CreditNote, e.Net("2-1401", day("2026-10-31")))
	}
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.Equal(qty("24")) {
		t.Fatal("potongan harga tanpa retur tidak boleh mengubah stok")
	}
	vat, err := e.Sales.VATByMonth(e.Ctx, e.Company, day("2026-10-01"), day("2026-11-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(vat) != 1 || !vat[0].VAT.Equal(rp(2_200-880-66)) {
		t.Fatalf("PPN keluaran masa Oktober = %+v", vat)
	}

	// Void nota pertama: barang keluar lagi dengan nilai yang sama, piutang kembali.
	if _, err := e.Sales.VoidCreditNote(e.Ctx, e.Company, n.ID, day("2026-10-15"), "Salah input", e.Actor); err != nil {
		t.Fatal(err)
	}
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.Equal(qty("20")) {
		t.Fatalf("stok setelah void nota = %+v", s)
	}
	if !e.Net("5-1001", day("2026-10-31")).Equal(rp(11_500)) {
		t.Fatalf("HPP setelah void nota = %s", e.Net("5-1001", day("2026-10-31")))
	}
	d, _ = e.Sales.Invoice(e.Ctx, e.Company, inv.ID)
	if d.State != "open" || !d.Outstanding.Equal(rp(8_880)) {
		t.Fatalf("faktur setelah void nota = %+v", d.Invoice)
	}
	e.VerifyLedger()
}

// AP-05: retur pembelian mengeluarkan barang dengan harga tagihannya, mengurangi utang dan
// PPN masukan; PPh yang dipotong ikut berkurang; kelebihan atas tagihan lunas menjadi uang
// muka pembelian.
func TestPurchaseReturnSendsGoodsBack(t *testing.T) {
	e := fixture.NewWith(t, "retail")
	p := goods(t, e, "BRG-1")
	bill := buy(t, e, "2026-10-01", p, "10", 1_000)
	buy(t, e, "2026-10-02", p, "10", 1_300)

	n, err := e.Purchase.CreateDebitNote(e.Ctx, e.Company, purchase.DebitNoteInput{BillID: bill.ID, Date: day("2026-10-05"), Memo: "Rusak",
		Lines: []purchase.DebitLineInput{{BillLineID: bill.Lines[0].ID, Qty: qty("3")}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if n.Number != "ND/2026/10/0001" || !n.DPP.Equal(rp(3_000)) || !n.VAT.Equal(rp(330)) || !n.Applied.Equal(rp(3_330)) {
		t.Fatalf("nota debit = %+v", n.DebitNote)
	}
	// Persediaan 10.000 + 13.000 − 3.000 (harga tagihan asal, bukan rata-rata 1.150).
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.Equal(qty("17")) || !s.Value.Equal(rp(20_000)) {
		t.Fatalf("stok setelah retur = %+v", s)
	}
	if !e.Net("2-1101", day("2026-10-31")).Equal(rp(-(11_100 + 14_430 - 3_330))) || !e.Net("1-1401", day("2026-10-31")).Equal(rp(1_100+1_430-330)) {
		t.Fatalf("utang %s atau PPN masukan %s tidak berkurang sebesar nota", e.Net("2-1101", day("2026-10-31")), e.Net("1-1401", day("2026-10-31")))
	}
	if _, err := e.Purchase.CreateDebitNote(e.Ctx, e.Company, purchase.DebitNoteInput{BillID: bill.ID, Date: day("2026-10-06"),
		Lines: []purchase.DebitLineInput{{BillLineID: bill.Lines[0].ID, Qty: qty("8")}}}, e.Actor); code(err) != "debit_note.invalid" {
		t.Fatalf("retur melebihi kuantitas tagihan seharusnya ditolak: %v", err)
	}
	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, bill.ID, day("2026-10-07"), "Batal", e.Actor); code(err) != "bill.has_debit_notes" {
		t.Fatalf("void tagihan bernota debit seharusnya ditolak: %v", err)
	}
	if _, err := e.Purchase.VoidDebitNote(e.Ctx, e.Company, n.ID, day("2026-10-08"), "Batal retur", e.Actor); err != nil {
		t.Fatal(err)
	}
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.Equal(qty("20")) || !s.Value.Equal(rp(23_000)) {
		t.Fatalf("stok setelah void nota = %+v", s)
	}

	// Jasa dengan PPh 23: tagihan lunas, nota debit mengurangi utang PPh dan menjadi uang muka pembelian.
	svc, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{SupplierID: e.Supplier, Date: day("2026-10-10"), WithholdingCode: "PPH23",
		Lines: []trade.LineInput{{Description: "Jasa servis", AccountID: e.Acc["6-2008"], Qty: qty("1"), UnitPrice: rp(1_000_000), TaxCode: "PPN"}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	svc, err = e.Purchase.PostBill(e.Ctx, e.Company, svc.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.CreatePayment(e.Ctx, e.Company, purchase.PaymentInput{SupplierID: e.Supplier, Date: day("2026-10-11"), CashAccountID: e.Bank,
		Allocations: []purchase.AllocationInput{{BillID: svc.ID, Amount: svc.Payable}}}, e.Actor); err != nil {
		t.Fatal(err)
	}
	price := rp(400_000)
	n2, err := e.Purchase.CreateDebitNote(e.Ctx, e.Company, purchase.DebitNoteInput{BillID: svc.ID, Date: day("2026-10-12"),
		Lines: []purchase.DebitLineInput{{BillLineID: svc.Lines[0].ID, Qty: qty("1"), UnitPrice: &price}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	// DPP 400.000, PPN 44.000, PPh 23 2% = 8.000 → pengurang utang 436.000, seluruhnya uang muka.
	if !n2.Withholding.Equal(rp(8_000)) || !n2.Payable.Equal(rp(436_000)) || !n2.Applied.IsZero() ||
		!e.Net("1-1503", day("2026-10-31")).Equal(rp(436_000)) || !e.Net("2-1203", day("2026-10-31")).Equal(rp(-12_000)) {
		t.Fatalf("nota debit jasa = %+v, uang muka %s, utang PPh 23 %s", n2.DebitNote, e.Net("1-1503", day("2026-10-31")), e.Net("2-1203", day("2026-10-31")))
	}
	e.VerifyLedger()
}

package coretax_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"lajur/internal/modules/core"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/tax/coretax"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/money"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

func setup(t *testing.T) *fixture.Env {
	t.Helper()
	e := fixture.New(t)
	c, err := e.Core.Company(e.Ctx, e.Company)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Core.UpdateTaxProfile(e.Ctx, e.Company, core.TaxProfile{
		NPWP: "0123.4567.8901.2345", Address: "Jl. Penjual No. 1, Jakarta", PKP: true, Version: c.Version,
	}, e.Actor); err != nil {
		t.Fatal(err)
	}
	return e
}

func postInvoice(t *testing.T, e *fixture.Env, date, price, taxCode string) sales.InvoiceDetail {
	t.Helper()
	d, err := e.Sales.CreateInvoice(e.Ctx, e.Company, sales.InvoiceInput{
		CustomerID: e.Customer, Date: day(date),
		Lines: []trade.LineInput{{Description: "Jasa konsultansi", AccountID: e.Acc["4-1001"], Qty: money.FromInt(1), UnitPrice: money.MustParse(price), TaxCode: taxCode}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Sales.PostInvoice(e.Ctx, e.Company, d.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TAX-03, TAX-04: faktur ber-PPN siap diekspor dengan kode 04, XML berisi referensi faktur Lajur,
// dan faktur yang faktur pajaknya terbit tidak bisa di-void sebelum faktur pajaknya dibatalkan.
func TestOutputInvoiceLifecycle(t *testing.T) {
	e := setup(t)
	inv := postInvoice(t, e, "2026-10-05", "10000000", "PPN")
	postInvoice(t, e, "2026-10-06", "500000", "NON-PPN") // tanpa PPN: tidak perlu faktur pajak

	list, err := e.Coretax.Outputs(e.Ctx, e.Company, day("2026-10-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || len(list.Problems) != 0 {
		t.Fatalf("daftar faktur keluaran = %+v", list)
	}
	item := list.Items[0]
	if item.TrxCode != "04" || item.Status != coretax.Ready || !item.VAT.Equal(rp(1_100_000)) || len(item.Problems) != 0 {
		t.Fatalf("faktur keluaran = %+v", item)
	}

	batch, err := e.Coretax.Export(e.Ctx, e.Company, day("2026-10-01"), []uuid.UUID{inv.ID}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	xml := string(batch.Content)
	for _, want := range []string{
		"<TIN>0123456789012345</TIN>", "<TrxCode>04</TrxCode>", "<RefDesc>FJ/2026/10/0001</RefDesc>",
		"<BuyerTin>0123456789012345</BuyerTin>", "<TaxBase>10000000.00</TaxBase>", "<OtherTaxBase>9166666.67</OtherTaxBase>",
		"<VAT>1100000.00</VAT>", "<Opt>B</Opt>",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("XML tidak berisi %s", want)
		}
	}
	saved, err := e.Coretax.Batch(e.Ctx, e.Company, batch.ID)
	if err != nil || string(saved.Content) != xml {
		t.Fatalf("XML tersimpan berbeda: %v", err)
	}

	if err := e.Coretax.Approve(e.Ctx, e.Company, inv.ID, "040026-00000000017", day("2026-10-07"), e.Actor); err != nil {
		t.Fatal(err)
	}
	list, _ = e.Coretax.Outputs(e.Ctx, e.Company, day("2026-10-01"))
	if list.Items[0].Status != coretax.Approved || *list.Items[0].TaxInvoiceNumber != "04002600000000017" {
		t.Fatalf("setelah terbit = %+v", list.Items[0])
	}
	if _, err := e.Coretax.Export(e.Ctx, e.Company, day("2026-10-01"), []uuid.UUID{inv.ID}, e.Actor); code(err) != "tax.export_blocked" {
		t.Fatalf("faktur yang sudah terbit tidak boleh diekspor lagi: %v", err)
	}
	if err := e.Coretax.SetTrxCode(e.Ctx, e.Company, []uuid.UUID{inv.ID}, "01", e.Actor); code(err) != "tax.locked" {
		t.Fatalf("kode transaksi faktur terbit tidak boleh diubah: %v", err)
	}

	if _, err := e.Sales.VoidInvoice(e.Ctx, e.Company, inv.ID, day("2026-10-10"), "Salah harga", e.Actor); code(err) != "tax.invoice_locked" {
		t.Fatalf("void seharusnya ditolak selama faktur pajak terbit: %v", err)
	}
	if err := e.Coretax.Cancel(e.Ctx, e.Company, inv.ID, "Dibatalkan di Coretax karena salah harga", e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sales.VoidInvoice(e.Ctx, e.Company, inv.ID, day("2026-10-10"), "Salah harga", e.Actor); err != nil {
		t.Fatalf("setelah faktur pajak dibatalkan, void seharusnya boleh: %v", err)
	}
	list, _ = e.Coretax.Outputs(e.Ctx, e.Company, day("2026-10-01"))
	if len(list.Items) != 1 || list.Items[0].Status != coretax.Cancelled || list.Items[0].InvoiceStatus != "void" {
		t.Fatalf("faktur pajak dibatalkan tetap tercatat: %+v", list.Items)
	}
}

func TestExportBlockedWithoutTaxIdentity(t *testing.T) {
	e := fixture.New(t) // PT belum punya NPWP
	inv := postInvoice(t, e, "2026-10-05", "1000000", "PPN")
	list, err := e.Coretax.Outputs(e.Ctx, e.Company, day("2026-10-01"))
	if err != nil || len(list.Problems) == 0 {
		t.Fatalf("NPWP PT kosong seharusnya ditandai: %+v %v", list, err)
	}
	if _, err := e.Coretax.Export(e.Ctx, e.Company, day("2026-10-01"), []uuid.UUID{inv.ID}, e.Actor); code(err) != "tax.company_profile" {
		t.Fatalf("ekspor tanpa NPWP PT seharusnya ditolak: %v", err)
	}

	e2 := setup(t)
	buyer, err := e2.Core.CreateContact(e2.Ctx, e2.Company, core.ContactInput{Name: "Pembeli Tanpa NPWP", IsCustomer: true, PaymentTermsDays: 7}, e2.Actor)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := e2.Sales.CreateInvoice(e2.Ctx, e2.Company, sales.InvoiceInput{CustomerID: buyer.ID, Date: day("2026-10-05"),
		Lines: []trade.LineInput{{AccountID: e2.Acc["4-1001"], Qty: money.FromInt(1), UnitPrice: rp(1_000_000), TaxCode: "PPN"}}}, e2.Actor)
	p, err := e2.Sales.PostInvoice(e2.Ctx, e2.Company, d.ID, e2.Actor)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e2.Coretax.Export(e2.Ctx, e2.Company, day("2026-10-01"), []uuid.UUID{p.ID}, e2.Actor)
	if code(err) != "tax.export_blocked" || !strings.Contains(err.Error(), "NPWP atau NIK") {
		t.Fatalf("pembeli tanpa NPWP seharusnya menahan ekspor: %v", err)
	}
}

func TestImportNumbersFromCSV(t *testing.T) {
	e := setup(t)
	a := postInvoice(t, e, "2026-10-05", "1000000", "PPN")
	postInvoice(t, e, "2026-10-06", "2000000", "PPN")
	csv := "\ufeffReferensi;Nomor Faktur Pajak;Tanggal Faktur\n" +
		*a.Number + ";04002600000000021;07/10/2026\n" +
		"FJ/2026/10/9999;04002600000000022;07/10/2026\n" +
		"FJ/2026/10/0002;bukan-nomor;07/10/2026\n"
	res, err := e.Coretax.ImportNumbers(e.Ctx, e.Company, csv, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Recorded) != 1 || res.Recorded[0] != *a.Number || len(res.Skipped) != 2 {
		t.Fatalf("hasil impor = %+v", res)
	}
	list, _ := e.Coretax.Outputs(e.Ctx, e.Company, day("2026-10-01"))
	approved := 0
	for _, it := range list.Items {
		if it.Status == coretax.Approved {
			approved++
		}
	}
	if approved != 1 {
		t.Fatalf("faktur terbit = %d, seharusnya 1", approved)
	}
	if _, err := e.Coretax.ImportNumbers(e.Ctx, e.Company, "a,b\n1,2", e.Actor); code(err) != "tax.import_format" {
		t.Fatalf("CSV tanpa kolom yang dikenal seharusnya ditolak: %v", err)
	}
}

// TAX-06: lebih bayar masa September dikompensasikan ke Oktober.
func TestVATRecapCarriesOverpayment(t *testing.T) {
	e := setup(t)
	postInvoice(t, e, "2026-09-10", "10000000", "PPN") // PPN keluaran 1,1 jt
	bill := func(date, price string) {
		d, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{SupplierID: e.Supplier, Date: day(date),
			Lines: []trade.LineInput{{AccountID: e.Acc["6-2008"], Qty: money.FromInt(1), UnitPrice: money.MustParse(price), TaxCode: "PPN"}}}, e.Actor)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Purchase.PostBill(e.Ctx, e.Company, d.ID, e.Actor); err != nil {
			t.Fatal(err)
		}
	}
	bill("2026-09-15", "20000000")                     // PPN masukan 2,2 jt → lebih bayar 1,1 jt
	postInvoice(t, e, "2026-10-10", "30000000", "PPN") // PPN keluaran 3,3 jt

	rec, err := e.Coretax.VATRecap(e.Ctx, e.Company, 2026)
	if err != nil {
		t.Fatal(err)
	}
	sep, oct := rec.Months[8], rec.Months[9]
	if sep.Month != "2026-09" || !sep.Overpaid.Equal(rp(1_100_000)) || !sep.Payable.IsZero() {
		t.Fatalf("September = %+v", sep)
	}
	if !oct.Compensated.Equal(rp(1_100_000)) || !oct.Payable.Equal(rp(2_200_000)) {
		t.Fatalf("Oktober = %+v", oct)
	}
	if !rec.Totals.Output.Equal(rp(4_400_000)) || !rec.Totals.Input.Equal(rp(2_200_000)) || !rec.Totals.Payable.Equal(rp(2_200_000)) {
		t.Fatalf("total tahun = %+v", rec.Totals)
	}
}

func TestWithholdingRecap(t *testing.T) {
	e := setup(t)
	inv := postInvoice(t, e, "2026-10-05", "10000000", "PPN")
	if _, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{CustomerID: e.Customer, Date: day("2026-10-20"), CashAccountID: e.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: inv.Total, WithholdingCode: "PPH23", Withholding: rp(200_000)}}}, e.Actor); err != nil {
		t.Fatal(err)
	}
	d, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{SupplierID: e.Supplier, Date: day("2026-10-08"), WithholdingCode: "PPH23",
		Lines: []trade.LineInput{{AccountID: e.Acc["6-2008"], Qty: money.FromInt(1), UnitPrice: rp(5_000_000), TaxCode: "PPN"}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.PostBill(e.Ctx, e.Company, d.ID, e.Actor); err != nil {
		t.Fatal(err)
	}
	rec, err := e.Coretax.Withholding(e.Ctx, e.Company, day("2026-10-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.ByCustomers) != 1 || !rec.CustomerSums[0].Amount.Equal(rp(200_000)) {
		t.Fatalf("PPh dipotong pelanggan = %+v", rec.CustomerSums)
	}
	if len(rec.ByUs) != 1 || rec.ByUs[0].ContactNPWP != "9876543210987654" || !rec.OurSums[0].Amount.Equal(rp(100_000)) {
		t.Fatalf("PPh yang kita potong = %+v", rec.ByUs)
	}
}

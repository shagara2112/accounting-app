package sales_test

import (
	"testing"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/sales"
)

// Revisi klien: pelanggan pemungut PPN (WAPU, kode 02/03) menyetor sendiri PPN faktur. PPN-nya
// dikredit ke akun PPN keluaran dipungut, pelunasan mencatat PPN dipungut tanpa uang masuk,
// dan rekap PPN tidak menghitungnya sebagai kurang bayar.
func TestVATCollectorInvoiceAndReceipt(t *testing.T) {
	e := fixture.New(t)
	bumn, err := e.Core.CreateContact(e.Ctx, e.Company, core.ContactInput{
		Name: "PT BUMN Uji", IsCustomer: true, NPWP: "0987654321098765", PKP: true, PaymentTermsDays: 30, VATCollector: "03",
	}, e.Actor)
	if err != nil || bumn.VATCollector != "03" {
		t.Fatalf("pelanggan pemungut = %+v, %v", bumn, err)
	}
	if _, err := e.Core.CreateContact(e.Ctx, e.Company, core.ContactInput{
		Name: "Salah", IsCustomer: true, PaymentTermsDays: 30, VATCollector: "05",
	}, e.Actor); code(err) != "contact.invalid" {
		t.Fatalf("kode pemungut tidak sah seharusnya ditolak: %v", err)
	}
	accounts, err := e.Ledger.Accounts(e.Ctx, e.Company)
	if err != nil {
		t.Fatal(err)
	}
	var taxes uuid.UUID // 2-1200 Utang pajak
	for _, a := range accounts {
		if a.Code == "2-1200" {
			taxes = a.ID
		}
	}
	wapu, err := e.Ledger.CreateAccount(e.Ctx, e.Company, ledger.NewAccount{
		Code: "2-1209", Name: "PPN keluaran dipungut WAPU", ParentID: &taxes, Postable: true,
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	e.Acc["2-1209"] = wapu.ID
	if _, err := e.Ledger.SetSystemAccount(e.Ctx, e.Company, ledger.OutputVATCollected, wapu.ID, e.Actor); err != nil {
		t.Fatal(err)
	}

	in := invoiceInput(e, "2026-10-05", line(e, "4-1001", "1", "10000000", "0", "PPN"))
	in.CustomerID = bumn.ID
	inv := posted(t, e, in)
	if inv.VATCollector != "03" || !inv.VAT.Equal(rp(1_100_000)) {
		t.Fatalf("faktur ke pemungut = %+v", inv.Invoice)
	}
	if !e.Net("2-1209", day("2026-10-31")).Equal(rp(-1_100_000)) || !e.Net("2-1201", day("2026-10-31")).IsZero() {
		t.Fatal("PPN faktur pemungut seharusnya masuk 2-1209, bukan 2-1201")
	}
	// Faktur pelanggan biasa tetap ke PPN keluaran.
	regular := posted(t, e, invoiceInput(e, "2026-10-06", line(e, "4-1001", "1", "2000000", "0", "PPN")))
	if regular.VATCollector != "" || !e.Net("2-1201", day("2026-10-31")).Equal(rp(-220_000)) {
		t.Fatalf("faktur biasa = %+v", regular.Invoice)
	}

	receipt := func(alloc sales.AllocationInput, cash bool) (sales.ReceiptDetail, error) {
		r := sales.ReceiptInput{CustomerID: bumn.ID, Date: day("2026-10-20"), Allocations: []sales.AllocationInput{alloc}}
		if cash {
			r.CashAccountID = e.Bank
		}
		return e.Sales.CreateReceipt(e.Ctx, e.Company, r, e.Actor)
	}
	_, err = receipt(sales.AllocationInput{InvoiceID: inv.ID, Amount: rp(11_100_000), VATCollected: rp(1_200_000)}, true)
	if code(err) != "receipt.invalid" {
		t.Fatalf("PPN dipungut melebihi PPN faktur seharusnya ditolak: %v", err)
	}
	_, err = receipt(sales.AllocationInput{InvoiceID: inv.ID, Amount: rp(1_000_000), WithholdingCode: "PPH23", Withholding: rp(200_000), VATCollected: rp(900_000)}, true)
	if code(err) != "receipt.invalid" {
		t.Fatalf("potongan melebihi nominal pelunasan seharusnya ditolak: %v", err)
	}

	// Pelanggan membayar DPP − PPh 23; PPN 1,1 juta disetor pelanggan sendiri.
	r, err := receipt(sales.AllocationInput{
		InvoiceID: inv.ID, Amount: rp(11_100_000), WithholdingCode: "PPH23", Withholding: rp(200_000), VATCollected: rp(1_100_000),
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Amount.Equal(rp(9_800_000)) || !r.VATCollected.Equal(rp(1_100_000)) || !r.Allocations[0].VATCollected.Equal(rp(1_100_000)) {
		t.Fatalf("pelunasan = %+v", r)
	}
	if !e.Net("2-1209", day("2026-10-31")).IsZero() || !e.Net("1-1111", day("2026-10-31")).Equal(rp(9_800_000)) {
		t.Fatal("PPN dipungut seharusnya menutup saldo 2-1209 dan bank hanya menerima 9,8 juta")
	}
	d, err := e.Sales.Invoice(e.Ctx, e.Company, inv.ID)
	if err != nil || d.State != "paid" || !d.VATCollected.Equal(rp(1_100_000)) || !d.Payments[0].VATCollected.Equal(rp(1_100_000)) {
		t.Fatalf("faktur sesudah pelunasan = %+v, %v", d.Invoice, err)
	}

	// Faktur tanpa PPN tidak bisa dicatat PPN dipungut.
	in = invoiceInput(e, "2026-10-07", line(e, "4-1001", "1", "500000", "0", "NON-PPN"))
	in.CustomerID = bumn.ID
	plain := posted(t, e, in)
	if _, err := receipt(sales.AllocationInput{InvoiceID: plain.ID, Amount: rp(500_000), VATCollected: rp(50_000)}, true); code(err) != "receipt.invalid" {
		t.Fatalf("PPN dipungut faktur tanpa PPN seharusnya ditolak: %v", err)
	}

	// Pelunasan yang hanya mencatat PPN dipungut (uangnya sudah diterima sebelumnya).
	in = invoiceInput(e, "2026-10-08", line(e, "4-1001", "1", "1000000", "0", "PPN"))
	in.CustomerID = bumn.ID
	second := posted(t, e, in)
	if _, err := receipt(sales.AllocationInput{InvoiceID: second.ID, Amount: rp(1_000_000)}, true); err != nil {
		t.Fatal(err)
	}
	vatOnly, err := receipt(sales.AllocationInput{InvoiceID: second.ID, Amount: rp(110_000), VATCollected: rp(110_000)}, false)
	if err != nil || !vatOnly.Amount.IsZero() || vatOnly.CashAccountID != nil {
		t.Fatalf("pelunasan PPN dipungut saja = %+v, %v", vatOnly, err)
	}
	if d, _ := e.Sales.Invoice(e.Ctx, e.Company, second.ID); d.State != "paid" {
		t.Fatalf("faktur kedua seharusnya lunas: %+v", d.Invoice)
	}

	// Rekap PPN: PPN dipungut pemungut tidak ikut kurang bayar; kode yang diubah staf pajak ikut.
	months, err := e.Sales.VATByMonth(e.Ctx, e.Company, day("2026-10-01"), day("2026-11-01"), sales.VATCodeOverrides{})
	if err != nil || len(months) != 1 || !months[0].VAT.Equal(rp(1_430_000)) || !months[0].Collected.Equal(rp(1_210_000)) {
		t.Fatalf("PPN per masa = %+v, %v", months, err)
	}
	months, _ = e.Sales.VATByMonth(e.Ctx, e.Company, day("2026-10-01"), day("2026-11-01"),
		sales.VATCodeOverrides{Collected: []uuid.UUID{regular.ID}, NotCollected: []uuid.UUID{second.ID}})
	if !months[0].Collected.Equal(rp(1_320_000)) {
		t.Fatalf("PPN dipungut dengan kode yang diubah = %s", months[0].Collected)
	}
	recap, err := e.Coretax.VATRecap(e.Ctx, e.Company, 2026)
	if err != nil {
		t.Fatal(err)
	}
	oct := recap.Months[9]
	if !oct.Output.Equal(rp(1_430_000)) || !oct.Collected.Equal(rp(1_210_000)) || !oct.Payable.Equal(rp(220_000)) {
		t.Fatalf("rekap PPN Oktober = %+v", oct)
	}

	// Modul pajak memberi kode transaksi 03 pada faktur ke pemungut, 04 pada faktur DPP nilai lain.
	outputs, err := e.Coretax.Outputs(e.Ctx, e.Company, day("2026-10-01"))
	if err != nil {
		t.Fatal(err)
	}
	codes := map[uuid.UUID]string{}
	for _, o := range outputs.Items {
		codes[o.InvoiceID] = o.TrxCode
	}
	if codes[inv.ID] != "03" || codes[regular.ID] != "04" {
		t.Fatalf("kode transaksi bawaan = %v", codes)
	}

	// PPN faktur DPP nilai lain (kode 04) masuk akunnya sendiri bila dipilih.
	other, err := e.Ledger.CreateAccount(e.Ctx, e.Company, ledger.NewAccount{
		Code: "2-1210", Name: "PPN keluaran 04", ParentID: &taxes, Postable: true,
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	e.Acc["2-1210"] = other.ID
	if _, err := e.Ledger.SetSystemAccount(e.Ctx, e.Company, ledger.OutputVATOtherDPP, other.ID, e.Actor); err != nil {
		t.Fatal(err)
	}
	posted(t, e, invoiceInput(e, "2026-10-09", line(e, "4-1001", "1", "1000000", "0", "PPN")))
	if !e.Net("2-1210", day("2026-10-31")).Equal(rp(-110_000)) {
		t.Fatal("PPN faktur DPP nilai lain seharusnya masuk 2-1210")
	}

	// Void pelunasan mengembalikan PPN dipungut ke faktur.
	if _, err := e.Sales.VoidReceipt(e.Ctx, e.Company, r.ID, day("2026-10-21"), "salah catat", e.Actor); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.Sales.Invoice(e.Ctx, e.Company, inv.ID); d.State != "open" || !d.VATCollected.IsZero() {
		t.Fatalf("faktur sesudah void pelunasan = %+v", d.Invoice)
	}
	if !e.Net("2-1209", day("2026-10-31")).Equal(rp(-1_100_000)) {
		t.Fatal("void pelunasan seharusnya mengembalikan saldo PPN dipungut")
	}
	e.VerifyLedger()
}

package sales_test

import (
	"strings"
	"testing"

	"lajur/internal/modules/core"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/sales"
	"lajur/internal/platform/money"
)

// AR-04: uang muka pelanggan di luar proyek diterima tanpa faktur, dipakai melunasi faktur
// tanpa uang masuk, dan sisanya dikembalikan; uang muka yang sudah dipakai tidak bisa di-void.
func TestCustomerAdvances(t *testing.T) {
	e := fixture.New(t)
	adv, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{CustomerID: e.Customer, Date: day("2026-10-01"), CashAccountID: e.Bank, Advance: rp(5_000_000)}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if !adv.Amount.Equal(rp(5_000_000)) || !e.Net("2-1401", day("2026-10-31")).Equal(rp(-5_000_000)) {
		t.Fatalf("uang muka = %+v, saldo 2-1401 %s", adv.Receipt, e.Net("2-1401", day("2026-10-31")))
	}
	inv := posted(t, e, invoiceInput(e, "2026-10-05", line(e, "4-1001", "1", "3000000", "0", "PPN"))) // 3.330.000
	use := sales.ReceiptInput{CustomerID: e.Customer, Date: day("2026-10-06"), FromAdvance: rp(3_330_000),
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: rp(3_330_000)}}}
	rc, err := e.Sales.CreateReceipt(e.Ctx, e.Company, use, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if !rc.Amount.IsZero() || rc.CashAccountID != nil {
		t.Fatalf("pelunasan dari uang muka tidak boleh ada uang masuk: %+v", rc.Receipt)
	}
	if d, _ := e.Sales.Invoice(e.Ctx, e.Company, inv.ID); d.State != "paid" {
		t.Fatalf("faktur harus lunas dari uang muka: %+v", d.Invoice)
	}
	bal, _ := e.Sales.AdvanceBalance(e.Ctx, e.Company, e.Customer)
	if !bal.Equal(rp(1_670_000)) {
		t.Fatalf("sisa uang muka = %s", bal)
	}
	// Uang muka yang sudah terpakai tidak bisa di-void.
	if _, err := e.Sales.VoidReceipt(e.Ctx, e.Company, adv.ID, day("2026-10-07"), "Salah", e.Actor); code(err) != "advance.used" {
		t.Fatalf("void uang muka terpakai seharusnya ditolak: %v", err)
	}
	if _, err := e.Sales.RefundAdvance(e.Ctx, e.Company, sales.AdvanceRefundInput{CustomerID: e.Customer, Date: day("2026-10-08"), CashAccountID: e.Bank, Amount: rp(2_000_000)}, e.Actor); code(err) != "refund.invalid" {
		t.Fatalf("pengembalian melebihi saldo seharusnya ditolak: %v", err)
	}
	ref, err := e.Sales.RefundAdvance(e.Ctx, e.Company, sales.AdvanceRefundInput{CustomerID: e.Customer, Date: day("2026-10-08"), CashAccountID: e.Bank, Amount: rp(1_670_000)}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != sales.ReceiptKindRefund || !strings.HasPrefix(ref.Number, "BKK/") || !e.Net("2-1401", day("2026-10-31")).IsZero() {
		t.Fatalf("pengembalian = %+v, saldo 2-1401 %s", ref.Receipt, e.Net("2-1401", day("2026-10-31")))
	}
	if !e.Net("1-1111", day("2026-10-31")).Equal(rp(5_000_000 - 1_670_000)) {
		t.Fatalf("bank = %s", e.Net("1-1111", day("2026-10-31")))
	}
	e.VerifyLedger()
}

// AR-01: draf faktur yang membuat piutang melewati limit kredit pelanggan diberi peringatan.
func TestCreditLimitWarning(t *testing.T) {
	e := fixture.New(t)
	c, err := e.Core.Contact(e.Ctx, e.Company, e.Customer)
	if err != nil {
		t.Fatal(err)
	}
	limit := rp(5_000_000)
	_, err = e.Core.UpdateContact(e.Ctx, e.Company, e.Customer, core.ContactInput{
		Name: c.Name, IsCustomer: true, NPWP: c.NPWP, PKP: true, PaymentTermsDays: 30, Active: true, Version: c.Version,
		CreditLimit: &limit, BillingContact: "Ibu Sari (keuangan)",
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	posted(t, e, invoiceInput(e, "2026-10-01", line(e, "4-1001", "1", "3000000", "0", "PPN")))
	d, err := e.Sales.CreateInvoice(e.Ctx, e.Company, invoiceInput(e, "2026-10-02", line(e, "4-1001", "1", "2000000", "0", "PPN")), e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if d.CreditWarning == nil || !strings.Contains(*d.CreditWarning, "5.550.000") {
		t.Fatalf("peringatan limit kredit = %v", d.CreditWarning)
	}
	_ = money.Amount{}
}

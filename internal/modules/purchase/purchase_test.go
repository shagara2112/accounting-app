package purchase_test

import (
	"testing"

	"lajur/internal/modules/fixture"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/money"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

func bill(e *fixture.Env, date, supplierNo, wh string, lines ...trade.LineInput) purchase.BillInput {
	return purchase.BillInput{SupplierID: e.Supplier, SupplierInvoiceNo: supplierNo, Date: day(date), WithholdingCode: wh, Lines: lines}
}

func line(e *fixture.Env, code, price, taxCode string) trade.LineInput {
	return trade.LineInput{Description: "Jasa " + code, AccountID: e.Acc[code], Qty: money.MustParse("1"),
		UnitPrice: money.MustParse(price), TaxCode: taxCode}
}

// AP-02: tagihan jasa dengan PPN masukan dan PPh 23 yang kita potong; yang dibayar ke pemasok
// adalah total dikurangi PPh, dan PPh menjadi utang pajak.
func TestBillWithVATAndWithholding(t *testing.T) {
	e := fixture.New(t)
	d, err := e.Purchase.CreateBill(e.Ctx, e.Company, bill(e, "2026-10-03", "INV-77/X/2026", "PPH23", line(e, "6-2008", "10000000", "PPN")), e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Totals.VAT.Equal(rp(1_100_000)) || !d.Withholding.Equal(rp(200_000)) || !d.Payable.Equal(rp(10_900_000)) {
		t.Fatalf("tagihan = PPN %s PPh %s dibayar %s", d.Totals.VAT, d.Withholding, d.Payable)
	}
	if d.DueDate.String() != "2026-10-17" {
		t.Fatalf("jatuh tempo = %s, seharusnya termin 14 hari", d.DueDate)
	}
	p, err := e.Purchase.PostBill(e.Ctx, e.Company, d.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if *p.Number != "FB/2026/10/0001" {
		t.Fatalf("nomor tagihan = %s", *p.Number)
	}
	end := day("2026-10-31")
	checks := map[string]int64{"6-2008": 10_000_000, "1-1401": 1_100_000, "2-1101": -10_900_000, "2-1203": -200_000}
	for c, want := range checks {
		if got := e.Net(c, end); !got.Equal(rp(want)) {
			t.Errorf("saldo %s = %s, seharusnya %d", c, got, want)
		}
	}

	dup, err := e.Purchase.CreateBill(e.Ctx, e.Company, bill(e, "2026-10-04", "inv-77/x/2026", "", line(e, "6-2008", "1", "NON-PPN")), e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(dup.Duplicates) != 1 || dup.Duplicates[0].ID != p.ID {
		t.Fatalf("nomor faktur pemasok yang sama seharusnya ditandai: %+v", dup.Duplicates)
	}
	if err := e.Purchase.DeleteBill(e.Ctx, e.Company, dup.ID, e.Other, false); code(err) != "bill.not_owner" {
		t.Fatalf("draf orang lain seharusnya tidak bisa dihapus: %v", err)
	}
	if err := e.Purchase.DeleteBill(e.Ctx, e.Company, dup.ID, e.Actor, false); err != nil {
		t.Fatal(err)
	}

	pay, err := e.Purchase.CreatePayment(e.Ctx, e.Company, purchase.PaymentInput{
		SupplierID: e.Supplier, Date: day("2026-10-15"), CashAccountID: e.Bank,
		Allocations: []purchase.AllocationInput{{BillID: p.ID, Amount: rp(10_900_000)}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if pay.Number != "BKK/2026/10/0001" {
		t.Fatalf("nomor pembayaran = %s", pay.Number)
	}
	paid, _ := e.Purchase.Bill(e.Ctx, e.Company, p.ID)
	if paid.State != "paid" || !e.Net("2-1101", end).IsZero() || !e.Net("1-1111", end).Equal(rp(-10_900_000)) {
		t.Fatalf("setelah dibayar: status %s, utang %s, bank %s", paid.State, e.Net("2-1101", end), e.Net("1-1111", end))
	}
	if _, err := e.Purchase.CreatePayment(e.Ctx, e.Company, purchase.PaymentInput{
		SupplierID: e.Supplier, Date: day("2026-10-16"), CashAccountID: e.Bank,
		Allocations: []purchase.AllocationInput{{BillID: p.ID, Amount: rp(1)}},
	}, e.Actor); code(err) != "payment.invalid" {
		t.Fatalf("membayar tagihan lunas seharusnya ditolak: %v", err)
	}

	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, p.ID, day("2026-10-20"), "Tagihan dobel", e.Actor); code(err) != "bill.has_payments" {
		t.Fatalf("tagihan yang sudah dibayar tidak boleh di-void: %v", err)
	}
	if _, err := e.Purchase.VoidPayment(e.Ctx, e.Company, pay.ID, day("2026-10-20"), "Transfer gagal", e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, p.ID, day("2026-10-20"), "Tagihan dobel", e.Actor); err != nil {
		t.Fatal(err)
	}
	for c := range checks {
		if !e.Net(c, end).IsZero() {
			t.Errorf("saldo %s = %s setelah void, seharusnya nol", c, e.Net(c, end))
		}
	}
	e.VerifyLedger()
}

// PT non-PKP tidak bisa mengkreditkan PPN masukan; PPN ikut menjadi beban.
func TestNonPKPExpensesInputVAT(t *testing.T) {
	e := fixture.New(t)
	if _, err := e.Pool.Exec(e.Ctx, "UPDATE core.companies SET pkp = false WHERE id = $1", e.Company); err != nil {
		t.Fatal(err)
	}
	d, err := e.Purchase.CreateBill(e.Ctx, e.Company, bill(e, "2026-10-03", "", "", line(e, "6-2004", "1000000", "PPN")), e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.PostBill(e.Ctx, e.Company, d.ID, e.Actor); err != nil {
		t.Fatal(err)
	}
	end := day("2026-10-31")
	if !e.Net("6-2004", end).Equal(rp(1_110_000)) || !e.Net("1-1401", end).IsZero() {
		t.Fatalf("beban %s, PPN masukan %s", e.Net("6-2004", end), e.Net("1-1401", end))
	}
	_, err = e.Purchase.CreateBill(e.Ctx, e.Company, bill(e, "2026-10-03", "", "", line(e, "4-1001", "1000", "NON-PPN")), e.Actor)
	if fixture.Fields(err)["lines.0.accountId"] == "" {
		t.Fatalf("tagihan ke akun pendapatan seharusnya ditolak: %v", err)
	}
}

func TestPayablesAging(t *testing.T) {
	e := fixture.New(t)
	for _, b := range []purchase.BillInput{
		{SupplierID: e.Supplier, Date: day("2026-08-01"), DueDate: day("2026-08-15"), Lines: []trade.LineInput{line(e, "6-2001", "3000000", "NON-PPN")}},
		{SupplierID: e.Supplier, Date: day("2026-10-01"), DueDate: day("2026-11-15"), Lines: []trade.LineInput{line(e, "6-2001", "1000000", "NON-PPN")}},
	} {
		d, err := e.Purchase.CreateBill(e.Ctx, e.Company, b, e.Actor)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Purchase.PostBill(e.Ctx, e.Company, d.ID, e.Actor); err != nil {
			t.Fatal(err)
		}
	}
	a, err := e.Purchase.Aging(e.Ctx, e.Company, day("2026-10-31"))
	if err != nil {
		t.Fatal(err)
	}
	if !a.Totals[trade.Days90].Equal(rp(3_000_000)) || !a.Totals[trade.Current].Equal(rp(1_000_000)) || len(a.Rows) != 1 {
		t.Fatalf("umur utang = %+v", a.Totals)
	}
}

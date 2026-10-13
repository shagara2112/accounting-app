package sales_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"lajur/internal/modules/fixture"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/money"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

func invoiceInput(e *fixture.Env, date string, lines ...trade.LineInput) sales.InvoiceInput {
	return sales.InvoiceInput{CustomerID: e.Customer, Date: day(date), Memo: "Jasa konsultasi", Lines: lines}
}

func line(e *fixture.Env, code, qty, price, disc, taxCode string) trade.LineInput {
	return trade.LineInput{
		Description: "Jasa " + code, AccountID: e.Acc[code], Qty: money.MustParse(qty),
		UnitPrice: money.MustParse(price), DiscountPct: money.MustParse(disc), TaxCode: taxCode,
	}
}

func posted(t *testing.T, e *fixture.Env, in sales.InvoiceInput) sales.InvoiceDetail {
	t.Helper()
	d, err := e.Sales.CreateInvoice(e.Ctx, e.Company, in, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Sales.PostInvoice(e.Ctx, e.Company, d.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Faktur draf bisa diubah; setelah diposting, jurnalnya debit piutang, kredit pendapatan dan PPN.
func TestInvoiceDraftPostAndJournal(t *testing.T) {
	e := fixture.New(t)
	d, err := e.Sales.CreateInvoice(e.Ctx, e.Company, invoiceInput(e, "2026-10-05",
		line(e, "4-1001", "1", "8000000", "0", "PPN"),
	), e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "draft" || d.Number != nil || d.DueDate.String() != "2026-11-04" {
		t.Fatalf("draf = %+v (jatuh tempo dari termin 30 hari)", d.Invoice)
	}

	in := invoiceInput(e, "2026-10-05",
		line(e, "4-1001", "1", "8000000", "0", "PPN"),
		line(e, "4-1002", "2", "1000000", "0", "PPN"),
	)
	in.Version = d.Version
	d, err = e.Sales.UpdateInvoice(e.Ctx, e.Company, d.ID, in, e.Actor, false)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Totals.DPP.Equal(rp(10_000_000)) || !d.Totals.VAT.Equal(rp(1_100_000)) || !d.Total.Equal(rp(11_100_000)) {
		t.Fatalf("total draf = %+v", d.Totals)
	}
	_, err = e.Sales.UpdateInvoice(e.Ctx, e.Company, d.ID, in, e.Other, false)
	if code(err) != "invoice.not_owner" {
		t.Fatalf("draf orang lain seharusnya ditolak: %v", err)
	}
	stale := in
	stale.Version = d.Version - 1
	if _, err := e.Sales.UpdateInvoice(e.Ctx, e.Company, d.ID, stale, e.Actor, false); code(err) != "conflict.version" {
		t.Fatalf("versi lama seharusnya ditolak: %v", err)
	}

	p, err := e.Sales.PostInvoice(e.Ctx, e.Company, d.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if p.Number == nil || *p.Number != "FJ/2026/10/0001" || p.State != "open" || !p.Outstanding.Equal(rp(11_100_000)) {
		t.Fatalf("faktur diposting = %+v", p.Invoice)
	}
	j, err := e.Ledger.Entry(e.Ctx, e.Company, *p.JournalEntryID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Number != "FJ/2026/10/0001" || j.SourceType != sales.SourceInvoice || len(j.Lines) != 4 {
		t.Fatalf("jurnal faktur = %+v", j)
	}
	if !e.Net("1-1201", day("2026-10-31")).Equal(rp(11_100_000)) ||
		!e.Net("2-1201", day("2026-10-31")).Equal(rp(-1_100_000)) ||
		!e.Net("4-1001", day("2026-10-31")).Equal(rp(-8_000_000)) {
		t.Fatal("saldo piutang, PPN keluaran, atau pendapatan salah")
	}

	if _, err := e.Sales.UpdateInvoice(e.Ctx, e.Company, d.ID, in, e.Actor, true); code(err) != "invoice.not_draft" {
		t.Fatalf("faktur diposting tidak boleh diubah: %v", err)
	}
	if _, err := e.Pool.Exec(context.Background(), "UPDATE sales.invoice_lines SET qty = 9"); err == nil {
		t.Fatal("basis data seharusnya menolak perubahan baris faktur yang sudah diposting")
	}
	if _, err := e.Pool.Exec(context.Background(), "DELETE FROM sales.invoices"); err == nil {
		t.Fatal("basis data seharusnya menolak penghapusan faktur yang sudah diposting")
	}
	e.VerifyLedger()
}

// AR-04: pelunasan sebagian lalu sisanya dengan PPh 23 dipotong pelanggan; piutang tertutup penuh.
func TestReceiptsWithWithholdingAndVoid(t *testing.T) {
	e := fixture.New(t)
	inv := posted(t, e, invoiceInput(e, "2026-10-05", line(e, "4-1001", "1", "10000000", "0", "PPN")))

	r1, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{
		CustomerID: e.Customer, Date: day("2026-10-10"), CashAccountID: e.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: rp(5_000_000)}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Number != "BKM/2026/10/0001" {
		t.Fatalf("nomor pelunasan = %s", r1.Number)
	}
	after1, _ := e.Sales.Invoice(e.Ctx, e.Company, inv.ID)
	if after1.State != "open" || !after1.Outstanding.Equal(rp(6_100_000)) {
		t.Fatalf("setelah pelunasan sebagian = %+v", after1.Invoice)
	}

	over := sales.ReceiptInput{
		CustomerID: e.Customer, Date: day("2026-10-20"), CashAccountID: e.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: rp(6_100_001)}},
	}
	if _, err := e.Sales.CreateReceipt(e.Ctx, e.Company, over, e.Actor); code(err) != "receipt.invalid" ||
		!strings.Contains(fixture.Fields(err)["allocations.0.amount"], "Melebihi sisa") {
		t.Fatalf("kelebihan bayar seharusnya ditolak: %v", err)
	}
	if _, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{
		CustomerID: e.Customer, Date: day("2026-10-20"), CashAccountID: e.Acc["1-1112"],
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: rp(1)}},
	}, e.Actor); code(err) != "cash.not_cash_account" {
		t.Fatalf("rekening yang belum terdaftar seharusnya ditolak: %v", err)
	}

	r2, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{
		CustomerID: e.Customer, Date: day("2026-10-20"), CashAccountID: e.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: rp(6_100_000), WithholdingCode: "PPH23", Withholding: rp(200_000)}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Amount.Equal(rp(5_900_000)) || !r2.Withholding.Equal(rp(200_000)) {
		t.Fatalf("pelunasan kedua = %+v", r2)
	}
	paid, _ := e.Sales.Invoice(e.Ctx, e.Company, inv.ID)
	if paid.State != "paid" || !paid.Outstanding.IsZero() || len(paid.Payments) != 2 {
		t.Fatalf("faktur seharusnya lunas: %+v", paid.Invoice)
	}
	end := day("2026-10-31")
	if !e.Net("1-1201", end).IsZero() || !e.Net("1-1403", end).Equal(rp(200_000)) || !e.Net("1-1111", end).Equal(rp(10_900_000)) {
		t.Fatalf("saldo setelah lunas: piutang %s, PPh 23 %s, bank %s", e.Net("1-1201", end), e.Net("1-1403", end), e.Net("1-1111", end))
	}

	if _, err := e.Sales.VoidInvoice(e.Ctx, e.Company, inv.ID, day("2026-10-25"), "Salah pelanggan", e.Actor); code(err) != "invoice.has_receipts" {
		t.Fatalf("faktur berpelunasan tidak boleh di-void: %v", err)
	}
	for _, r := range []uuid.UUID{r2.ID, r1.ID} {
		if _, err := e.Sales.VoidReceipt(e.Ctx, e.Company, r, day("2026-10-25"), "Salah input pelunasan", e.Actor); err != nil {
			t.Fatal(err)
		}
	}
	reopened, _ := e.Sales.Invoice(e.Ctx, e.Company, inv.ID)
	if reopened.State != "open" || !reopened.Outstanding.Equal(rp(11_100_000)) {
		t.Fatalf("setelah pelunasan di-void = %+v", reopened.Invoice)
	}
	if _, err := e.Sales.VoidInvoice(e.Ctx, e.Company, inv.ID, day("2026-10-25"), "Salah pelanggan", e.Actor); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"1-1201", "1-1403", "1-1111", "2-1201", "4-1001"} {
		if !e.Net(c, end).IsZero() {
			t.Errorf("saldo %s = %s setelah semua di-void, seharusnya nol", c, e.Net(c, end))
		}
	}
	e.VerifyLedger()
}

// Dua pelunasan bersamaan untuk sisa faktur yang sama: hanya satu yang boleh berhasil.
func TestConcurrentReceiptsCannotOverpay(t *testing.T) {
	e := fixture.New(t)
	inv := posted(t, e, invoiceInput(e, "2026-10-05", line(e, "4-1001", "1", "1000000", "0", "NON-PPN")))
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{
				CustomerID: e.Customer, Date: day("2026-10-06"), CashAccountID: e.Bank,
				Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: rp(1_000_000)}},
			}, e.Actor)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if code(err) != "receipt.invalid" {
			t.Fatal(err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d pelunasan berhasil, seharusnya tepat 1", ok)
	}
	e.VerifyLedger()
}

func TestNonPKPCompanyCannotChargeVAT(t *testing.T) {
	e := fixture.New(t)
	if _, err := e.Pool.Exec(e.Ctx, "UPDATE core.companies SET pkp = false WHERE id = $1", e.Company); err != nil {
		t.Fatal(err)
	}
	_, err := e.Sales.CreateInvoice(e.Ctx, e.Company, invoiceInput(e, "2026-10-05", line(e, "4-1001", "1", "1000", "0", "PPN")), e.Actor)
	if !strings.Contains(fixture.Fields(err)["lines.0.taxCode"], "bukan PKP") {
		t.Fatalf("PT non-PKP seharusnya tidak boleh memungut PPN: %v", err)
	}
	_, err = e.Sales.CreateInvoice(e.Ctx, e.Company, invoiceInput(e, "2026-10-05", line(e, "6-1001", "1", "1000", "0", "NON-PPN")), e.Actor)
	if !strings.Contains(fixture.Fields(err)["lines.0.accountId"], "bukan akun pendapatan") {
		t.Fatalf("baris faktur ke akun beban seharusnya ditolak: %v", err)
	}
}

// AR-06: umur piutang per tanggal laporan hanya menghitung pelunasan sampai tanggal itu.
func TestAgingAsOfDate(t *testing.T) {
	e := fixture.New(t)
	old := posted(t, e, sales.InvoiceInput{CustomerID: e.Customer, Date: day("2026-07-01"), DueDate: day("2026-07-31"),
		Lines: []trade.LineInput{line(e, "4-1001", "1", "5000000", "0", "NON-PPN")}})
	posted(t, e, sales.InvoiceInput{CustomerID: e.Customer, Date: day("2026-10-01"), DueDate: day("2026-10-31"),
		Lines: []trade.LineInput{line(e, "4-1001", "1", "2000000", "0", "NON-PPN")}})
	if _, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{
		CustomerID: e.Customer, Date: day("2026-11-05"), CashAccountID: e.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: old.ID, Amount: rp(5_000_000)}},
	}, e.Actor); err != nil {
		t.Fatal(err)
	}

	oct, err := e.Sales.Aging(e.Ctx, e.Company, day("2026-10-31"))
	if err != nil {
		t.Fatal(err)
	}
	if !oct.Totals[trade.Over90].Equal(rp(5_000_000)) || !oct.Totals[trade.Current].Equal(rp(2_000_000)) {
		t.Fatalf("umur per 31 Okt = %+v", oct.Totals)
	}
	nov, _ := e.Sales.Aging(e.Ctx, e.Company, day("2026-11-30"))
	if !nov.Total.Equal(rp(2_000_000)) || !nov.Totals[trade.Days30].Equal(rp(2_000_000)) {
		t.Fatalf("umur per 30 Nov = %+v", nov.Totals)
	}
}

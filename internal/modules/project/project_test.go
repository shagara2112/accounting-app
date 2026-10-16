package project_test

import (
	"testing"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/project"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/money"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

func pct(s string) money.Amount { return money.MustParse(s) }

// gedung membuat proyek konstruksi Rp1 miliar, uang muka 20%, retensi 5%.
func gedung(t *testing.T, e *fixture.Env) project.Detail {
	t.Helper()
	rev := e.Acc["4-1001"]
	pph := "PPH42-K265"
	d, err := e.Project.CreateProject(e.Ctx, e.Company, project.ProjectInput{
		Code: "PRJ-01", Name: "Gedung Kantor Sudirman", CustomerID: &e.Customer, ContractValue: rp(1_000_000_000),
		DPPct: pct("20"), RetentionPct: pct("5"), PPhCode: &pph, RevenueAccountID: &rev,
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func bill(t *testing.T, e *fixture.Env, id uuid.UUID, kind, date, progress string) sales.InvoiceDetail {
	t.Helper()
	in := project.BillingInput{Kind: kind, Date: day(date)}
	if progress != "" {
		in.ProgressPct = pct(progress)
	}
	d, err := e.Project.CreateBilling(e.Ctx, e.Company, id, in, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Sales.PostInvoice(e.Ctx, e.Company, d.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// AR-08: uang muka, termin dengan potongan uang muka dan retensi, lalu pelepasan retensi.
// Uang muka habis persis di termin terakhir dan piutang retensi kembali nol.
func TestProgressBillingWithAdvanceAndRetention(t *testing.T) {
	e := fixture.NewWith(t, "konstruksi")
	p := gedung(t, e)

	if _, err := e.Project.CreateBilling(e.Ctx, e.Company, p.ID, project.BillingInput{
		Kind: sales.KindProgress, Date: day("2026-10-01"), ProgressPct: pct("30"),
	}, e.Actor); code(err) != "billing.advance_first" {
		t.Fatalf("termin sebelum uang muka diposting seharusnya ditolak: %v", err)
	}

	dp := bill(t, e, p.ID, sales.KindAdvance, "2026-10-01", "")
	// 2026: PPN 12% × DPP nilai lain 11/12 = 11% efektif.
	if !dp.DPP.Equal(rp(200_000_000)) || !dp.VAT.Equal(rp(22_000_000)) || dp.Kind != sales.KindAdvance {
		t.Fatalf("faktur uang muka = %+v", dp.Invoice)
	}
	if !e.Net("2-1402", day("2026-12-31")).Equal(rp(-200_000_000)) {
		t.Fatalf("uang muka proyek = %s", e.Net("2-1402", day("2026-12-31")))
	}
	if _, err := e.Project.CreateBilling(e.Ctx, e.Company, p.ID, project.BillingInput{Kind: sales.KindAdvance, Date: day("2026-10-02")}, e.Actor); code(err) != "billing.advance_exists" {
		t.Fatalf("uang muka kedua seharusnya ditolak: %v", err)
	}

	// Termin 1, progres 30%: nilai 300 jt, potongan uang muka 60 jt, DPP 240 jt, PPN 26,4 jt,
	// retensi 15 jt ditahan; yang ditagih sekarang 266,4 − 15 = 251,4 jt.
	t1 := bill(t, e, p.ID, sales.KindProgress, "2026-10-20", "30")
	if !t1.Subtotal.Equal(rp(300_000_000)) || !t1.Discount.Equal(rp(60_000_000)) || !t1.VAT.Equal(rp(26_400_000)) ||
		!t1.Retention.Equal(rp(15_000_000)) || !t1.Receivable.Equal(rp(251_400_000)) || !t1.Outstanding.Equal(rp(251_400_000)) {
		t.Fatalf("termin 1 = %+v", t1.Invoice)
	}
	j, err := e.Ledger.Entry(e.Ctx, e.Company, *t1.JournalEntryID)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range j.Lines {
		if l.ProjectID == nil || *l.ProjectID != p.ID {
			t.Fatalf("semua baris jurnal termin harus berproyek: %+v", l)
		}
	}

	if _, err := e.Project.CreateBilling(e.Ctx, e.Company, p.ID, project.BillingInput{
		Kind: sales.KindProgress, Date: day("2026-11-01"), ProgressPct: pct("30"),
	}, e.Actor); code(err) != "project.invalid" {
		t.Fatalf("progres yang tidak bertambah seharusnya ditolak: %v", err)
	}
	// Termin terakhir: potongan = sisa uang muka (140 jt) persis.
	t2 := bill(t, e, p.ID, sales.KindProgress, "2026-11-20", "100")
	if !t2.Subtotal.Equal(rp(700_000_000)) || !t2.Discount.Equal(rp(140_000_000)) || !t2.Retention.Equal(rp(35_000_000)) {
		t.Fatalf("termin 2 = %+v", t2.Invoice)
	}
	end := day("2026-12-31")
	if !e.Net("2-1402", end).IsZero() || !e.Net("1-1202", end).Equal(rp(50_000_000)) || !e.Net("4-1001", end).Equal(rp(-1_000_000_000)) {
		t.Fatalf("uang muka %s, retensi %s, pendapatan %s", e.Net("2-1402", end), e.Net("1-1202", end), e.Net("4-1001", end))
	}

	if _, err := e.Sales.VoidInvoice(e.Ctx, e.Company, dp.ID, day("2026-12-01"), "Salah nilai uang muka", e.Actor); code(err) != "invoice.project_order" {
		t.Fatalf("uang muka yang sudah dipotong termin tidak boleh di-void lebih dulu: %v", err)
	}

	ret := bill(t, e, p.ID, sales.KindRetention, "2026-12-20", "")
	if !ret.Total.Equal(rp(50_000_000)) || !ret.VAT.IsZero() {
		t.Fatalf("pelepasan retensi = %+v", ret.Invoice)
	}
	if !e.Net("1-1202", end).IsZero() {
		t.Fatal("piutang retensi harus nol setelah dilepas")
	}
	if _, err := e.Project.CreateBilling(e.Ctx, e.Company, p.ID, project.BillingInput{Kind: sales.KindRetention, Date: day("2026-12-21")}, e.Actor); code(err) != "billing.no_retention" {
		t.Fatalf("tidak ada lagi retensi yang tertahan: %v", err)
	}

	// Faktur termin dibuat dari halaman proyek dan tidak bisa diubah lewat formulir penjualan.
	draft, err := e.Project.CreateBilling(e.Ctx, e.Company, gedung2(t, e), project.BillingInput{Kind: sales.KindAdvance, Date: day("2026-12-01")}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sales.UpdateInvoice(e.Ctx, e.Company, draft.ID, sales.InvoiceInput{CustomerID: e.Customer, Date: day("2026-12-01")}, e.Actor, true); code(err) != "invoice.from_project" {
		t.Fatalf("draf dari proyek seharusnya tidak bisa diubah: %v", err)
	}

	d, err := e.Project.Project(e.Ctx, e.Company, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := d.Billing
	if !b.ProgressPct.Equal(pct("100")) || !b.Unbilled.IsZero() || !b.AdvanceRemaining.IsZero() || !b.RetentionHeld.IsZero() || b.Terms != 2 {
		t.Fatalf("ringkasan tagihan = %+v", b)
	}
	if !d.Revenue.Equal(rp(1_000_000_000)) {
		t.Fatalf("pendapatan proyek = %s", d.Revenue)
	}
	e.VerifyLedger()
}

func gedung2(t *testing.T, e *fixture.Env) uuid.UUID {
	t.Helper()
	d, err := e.Project.CreateProject(e.Ctx, e.Company, project.ProjectInput{
		Code: "PRJ-02", Name: "Gudang", CustomerID: &e.Customer, ContractValue: rp(100_000_000), DPPct: pct("10"),
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

// IND-02/IND-03: biaya proyek dari tagihan, kas, jurnal manual, pemakaian material, dan PPh final
// yang dipotong pemberi kerja; dibandingkan dengan RAB. Proyek batal tidak bisa dipakai lagi.
func TestProjectCostsBudgetAndProfit(t *testing.T) {
	e := fixture.NewWith(t, "konstruksi")
	p := gedung(t, e)
	if _, err := e.Project.SetBudget(e.Ctx, e.Company, p.ID, []project.BudgetLineInput{
		{AccountID: e.Acc["5-1001"], Description: "Material", Amount: rp(400_000_000)},
		{AccountID: e.Acc["5-1003"], Description: "Subkontraktor", Amount: rp(300_000_000)},
		{AccountID: e.Acc["5-1002"], Description: "Upah", Amount: rp(100_000_000)},
	}, e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Project.SetBudget(e.Ctx, e.Company, p.ID, []project.BudgetLineInput{
		{AccountID: e.Acc["4-1001"], Amount: rp(1)},
	}, e.Actor); code(err) != "budget.invalid" {
		t.Fatalf("RAB ke akun pendapatan seharusnya ditolak: %v", err)
	}

	id := p.ID
	b, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{
		SupplierID: e.Supplier, Date: day("2026-10-05"), WithholdingCode: "PPH42-K265",
		Lines: []trade.LineInput{
			{Description: "Pekerjaan struktur", AccountID: e.Acc["5-1003"], Qty: pct("1"), UnitPrice: rp(120_000_000), TaxCode: "PPN", ProjectID: &id},
			{Description: "ATK kantor", AccountID: e.Acc["6-2004"], Qty: pct("1"), UnitPrice: rp(500_000), TaxCode: "PPN"},
		},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.PostBill(e.Ctx, e.Company, b.ID, e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{
		Kind: "out", Date: day("2026-10-06"), CashAccountID: e.Petty, Memo: "Upah mandor minggu 1",
		Lines: []cash.LineInput{{AccountID: e.Acc["5-1002"], Amount: rp(15_000_000), ProjectID: &id}},
	}, e.Actor); err != nil {
		t.Fatal(err)
	}
	reclass := func(amount int64) []ledger.LineDraft {
		return []ledger.LineDraft{
			{AccountID: e.Acc["5-1004"], Debit: rp(amount), ProjectID: &id},
			{AccountID: e.Acc["1-1102"], Credit: rp(amount)},
		}
	}
	rc, err := e.Ledger.Post(e.Ctx, ledger.EntryDraft{
		CompanyID: e.Company, Kind: ledger.General, Date: day("2026-10-07"), Memo: "Reklasifikasi sewa alat",
		Lines: reclass(6_000_000), Actor: e.Actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Jurnal manual berproyek diubah: saldo per proyek ikut dikoreksi.
	if _, err := e.Ledger.Revise(e.Ctx, e.Company, rc.ID, ledger.ReviseInput{
		Date: day("2026-10-07"), Memo: "Reklasifikasi sewa alat", Lines: reclass(5_000_000), Reason: "Nominal salah ketik",
	}, e.Actor); err != nil {
		t.Fatal(err)
	}
	e.VerifyLedger()

	// Material dari gudang dipakai di proyek: biaya material sebesar HPP rata-rata.
	inv, hpp := e.Acc["1-1301"], e.Acc["5-1001"]
	semen, err := e.Inventory.CreateProduct(e.Ctx, e.Company, inventory.ProductInput{
		Code: "SMN-50", Name: "Semen 50 kg", Kind: inventory.KindGoods, Unit: "sak", InventoryAccountID: &inv, ExpenseAccountID: &hpp,
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	cost := rp(65_000)
	if _, err := e.Inventory.CreateAdjustment(e.Ctx, e.Company, inventory.AdjustmentInput{
		Kind: inventory.AdjManual, Date: day("2026-10-01"), AccountID: e.Acc["3-3001"],
		Lines: []inventory.AdjustmentLineInput{{ProductID: semen.ID, Qty: pct("100"), UnitCost: &cost}},
	}, e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Inventory.CreateAdjustment(e.Ctx, e.Company, inventory.AdjustmentInput{
		Kind: inventory.AdjIssue, Date: day("2026-10-08"), AccountID: e.Acc["5-1001"], ProjectID: &id,
		Lines: []inventory.AdjustmentLineInput{{ProductID: semen.ID, Qty: pct("40")}},
	}, e.Actor); err != nil {
		t.Fatal(err)
	}

	// Pendapatan: uang muka bukan pendapatan; termin 30% = 300 jt. Pelunasan termin dengan
	// PPh final 4(2) 2,65% × DPP 240 jt = 6,36 jt dipotong pemberi kerja → beban proyek.
	bill(t, e, id, sales.KindAdvance, "2026-10-01", "")
	t1 := bill(t, e, id, sales.KindProgress, "2026-10-20", "30")
	if _, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{
		CustomerID: e.Customer, Date: day("2026-10-28"), CashAccountID: e.Bank,
		Allocations: []sales.AllocationInput{{InvoiceID: t1.ID, Amount: t1.Receivable, WithholdingCode: "PPH42-K265", Withholding: rp(6_360_000)}},
	}, e.Actor); err != nil {
		t.Fatal(err)
	}

	rep, err := e.Project.Report(e.Ctx, e.Company, id, day("2026-10-01"), day("2026-10-01"))
	if err != nil {
		t.Fatal(err)
	}
	costs := map[string]project.ReportRow{}
	for _, r := range rep.Costs {
		costs[r.Code] = r
	}
	switch {
	case !rep.TotalRevenue.Equal(rp(300_000_000)):
		t.Fatalf("pendapatan = %s", rep.TotalRevenue)
	case !costs["5-1003"].Actual.Equal(rp(120_000_000)) || !costs["5-1003"].Remaining.Equal(rp(180_000_000)) || !costs["5-1003"].UsedPct.Equal(pct("40")):
		t.Fatalf("subkontraktor = %+v", costs["5-1003"])
	case !costs["5-1001"].Actual.Equal(rp(2_600_000)):
		t.Fatalf("material dari gudang = %+v", costs["5-1001"])
	case !costs["5-1002"].Actual.Equal(rp(15_000_000)) || !costs["5-1004"].Actual.Equal(rp(5_000_000)) || !costs["5-1004"].Budget.IsZero():
		t.Fatalf("upah dan sewa alat = %+v %+v", costs["5-1002"], costs["5-1004"])
	case !costs["9-1003"].Actual.Equal(rp(6_360_000)):
		t.Fatalf("PPh final proyek = %+v", costs["9-1003"])
	case costs["6-2004"].Code != "":
		t.Fatal("biaya kantor tanpa proyek tidak boleh masuk laporan proyek")
	case !rep.TotalBudget.Equal(rp(800_000_000)) || !rep.TotalCost.Equal(rp(148_960_000)) || !rep.Profit.Equal(rp(151_040_000)):
		t.Fatalf("RAB %s, biaya %s, laba %s", rep.TotalBudget, rep.TotalCost, rep.Profit)
	}
	list, err := e.Project.Projects(e.Ctx, e.Company, project.Filter{})
	if err != nil || len(list) != 1 || !list[0].Profit.Equal(rp(151_040_000)) || !list[0].Budget.Equal(rp(800_000_000)) {
		t.Fatalf("daftar proyek = %+v (%v)", list, err)
	}
	wages := costs["5-1002"].AccountID
	lines, _, err := e.Project.Lines(e.Ctx, e.Company, id, &wages, day("2026-10-01"), day("2026-10-31"))
	if err != nil || len(lines) != 1 || !lines[0].Debit.Equal(rp(15_000_000)) {
		t.Fatalf("rincian upah = %+v (%v)", lines, err)
	}

	// Proyek batal: transaksi baru ditolak, tetapi void transaksi lama tetap bisa.
	in := project.ProjectInput{
		Code: p.Code, Name: p.Name, CustomerID: p.CustomerID, ContractValue: p.ContractValue, DPPct: p.DPPct,
		RetentionPct: p.RetentionPct, PPhCode: p.PPhCode, RevenueAccountID: p.RevenueAccountID, Status: project.StatusCancelled,
	}
	cur, _ := e.Project.Project(e.Ctx, e.Company, id)
	in.Version = cur.Version
	if _, err := e.Project.UpdateProject(e.Ctx, e.Company, id, in, e.Actor); err != nil {
		t.Fatal(err)
	}
	_, err = e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{
		Kind: "out", Date: day("2026-10-30"), CashAccountID: e.Petty,
		Lines: []cash.LineInput{{AccountID: e.Acc["5-1002"], Amount: rp(1_000_000), ProjectID: &id}},
	}, e.Actor)
	if code(err) != "project.cancelled" {
		t.Fatalf("biaya ke proyek batal seharusnya ditolak: %v", err)
	}
	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, b.ID, day("2026-10-30"), "Tagihan dibatalkan", e.Actor); err != nil {
		t.Fatalf("void tagihan proyek batal tetap harus bisa: %v", err)
	}
	e.VerifyLedger()
}

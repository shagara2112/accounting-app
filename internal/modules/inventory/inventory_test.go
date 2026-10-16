package inventory_test

import (
	"testing"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/money"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

func qty(s string) money.Amount { return money.MustParse(s) }

// goods membuat barang dagang di PT retail: persediaan 1-1302, HPP 5-1001, penjualan 4-1001.
func goods(t *testing.T, e *fixture.Env, code string) inventory.Product {
	t.Helper()
	inv, hpp, rev := e.Acc["1-1302"], e.Acc["5-1001"], e.Acc["4-1001"]
	p, err := e.Inventory.CreateProduct(e.Ctx, e.Company, inventory.ProductInput{
		Code: code, Name: "Barang " + code, Kind: inventory.KindGoods, Unit: "pcs", SalePrice: rp(2_000),
		InventoryAccountID: &inv, ExpenseAccountID: &hpp, RevenueAccountID: &rev,
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func buy(t *testing.T, e *fixture.Env, date string, p inventory.Product, q string, price int64) purchase.BillDetail {
	t.Helper()
	id := p.ID
	b, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{
		SupplierID: e.Supplier, Date: day(date), Lines: []trade.LineInput{{
			Description: p.Name, ProductID: &id, Qty: qty(q), UnitPrice: rp(price), TaxCode: "PPN",
		}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	b, err = e.Purchase.PostBill(e.Ctx, e.Company, b.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sellDraft(t *testing.T, e *fixture.Env, date string, p inventory.Product, q string) sales.InvoiceDetail {
	t.Helper()
	id := p.ID
	d, err := e.Sales.CreateInvoice(e.Ctx, e.Company, sales.InvoiceInput{
		CustomerID: e.Customer, Date: day(date), Lines: []trade.LineInput{{
			Description: p.Name, ProductID: &id, Qty: qty(q), UnitPrice: rp(2_000), TaxCode: "PPN",
		}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func stock(t *testing.T, e *fixture.Env, p inventory.Product, asOf string) inventory.StockRow {
	t.Helper()
	r, err := e.Inventory.Stock(e.Ctx, e.Company, day(asOf))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range r.Accounts {
		if !a.Difference.IsZero() {
			t.Fatalf("nilai persediaan %s di buku besar %s ≠ kartu stok %s", a.Code, a.Ledger, a.Subledger)
		}
	}
	for _, row := range r.Rows {
		if row.ProductID == p.ID {
			return row
		}
	}
	t.Fatalf("%s tidak ada di laporan stok", p.Code)
	return inventory.StockRow{}
}

// INV-03: HPP rata-rata tertimbang saat dijual, koreksi HPP saat pembelian bertanggal mundur,
// stok tidak boleh negatif, dan void penjualan mengembalikan barang dengan HPP yang sama.
func TestWeightedAverageCOGSAndBackdating(t *testing.T) {
	e := fixture.NewWith(t, "retail")
	p := goods(t, e, "BRG-1")

	buy(t, e, "2026-10-01", p, "10", 1_000)
	buy(t, e, "2026-10-05", p, "10", 1_300)
	inv, err := e.Sales.PostInvoice(e.Ctx, e.Company, sellDraft(t, e, "2026-10-10", p, "5").ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	// Rata-rata (10.000 + 13.000) / 20 = 1.150 → HPP 5 unit = 5.750.
	if !e.Net("5-1001", day("2026-10-31")).Equal(rp(5_750)) || !e.Net("1-1302", day("2026-10-31")).Equal(rp(17_250)) {
		t.Fatalf("HPP %s, persediaan %s", e.Net("5-1001", day("2026-10-31")), e.Net("1-1302", day("2026-10-31")))
	}
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.Equal(qty("15")) || !s.AvgCost.Equal(rp(1_150)) {
		t.Fatalf("stok = %+v", s)
	}

	// Pembelian bertanggal 3 Oktober masuk belakangan: rata-rata saat penjualan menjadi
	// (10.000 + 7.000 + 13.000) / 30 = 1.000, sehingga HPP turun 750 dan dikoreksi otomatis.
	buy(t, e, "2026-10-03", p, "10", 700)
	if !e.Net("5-1001", day("2026-10-31")).Equal(rp(5_000)) || !e.Net("1-1302", day("2026-10-31")).Equal(rp(25_000)) {
		t.Fatalf("setelah koreksi: HPP %s, persediaan %s", e.Net("5-1001", day("2026-10-31")), e.Net("1-1302", day("2026-10-31")))
	}
	card, err := e.Inventory.Card(e.Ctx, e.Company, p.ID, day("2026-10-01"), day("2026-10-31"))
	if err != nil {
		t.Fatal(err)
	}
	if len(card.Lines) != 4 || card.Lines[1].Number == card.Lines[2].Number || !card.Lines[3].Value.Equal(rp(-5_000)) ||
		!card.ClosingQty.Equal(qty("25")) {
		t.Fatalf("kartu stok = %+v", card.Lines)
	}
	page, err := e.Ledger.Entries(e.Ctx, e.Company, ledgerFilter("HPP/"))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].SourceType != inventory.SourceCost {
		t.Fatalf("jurnal HPP = %+v", page.Items)
	}

	// Stok 25: penjualan 30 ditolak dan seluruh posting dibatalkan.
	big := sellDraft(t, e, "2026-10-20", p, "30")
	if _, err := e.Sales.PostInvoice(e.Ctx, e.Company, big.ID, e.Actor); code(err) != "inventory.insufficient" {
		t.Fatalf("penjualan melebihi stok seharusnya ditolak: %v", err)
	}
	if d, _ := e.Sales.Invoice(e.Ctx, e.Company, big.ID); d.Status != "draft" {
		t.Fatalf("faktur yang gagal diposting harus tetap draf: %s", d.Status)
	}

	// Void penjualan: 5 unit kembali senilai HPP-nya (5.000), HPP kembali 0.
	if _, err := e.Sales.VoidInvoice(e.Ctx, e.Company, inv.ID, day("2026-10-25"), "Salah input pelanggan", e.Actor); err != nil {
		t.Fatal(err)
	}
	if !e.Net("5-1001", day("2026-10-31")).IsZero() || !e.Net("1-1302", day("2026-10-31")).Equal(rp(30_000)) {
		t.Fatal("void penjualan harus mengembalikan HPP dan persediaan")
	}
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.Equal(qty("30")) || !s.Value.Equal(rp(30_000)) {
		t.Fatalf("stok setelah void = %+v", s)
	}
	e.VerifyLedger()
}

func ledgerFilter(search string) ledger.EntryFilter {
	return ledger.EntryFilter{From: day("2026-01-01"), To: day("2026-12-31"), Search: search, Limit: 50}
}

// Barang yang sudah terjual tidak bisa dikembalikan lewat void tagihan; void penjualannya dulu.
func TestVoidBillNeedsStock(t *testing.T) {
	e := fixture.NewWith(t, "retail")
	p := goods(t, e, "BRG-2")
	bill := buy(t, e, "2026-10-01", p, "10", 1_000)
	inv, err := e.Sales.PostInvoice(e.Ctx, e.Company, sellDraft(t, e, "2026-10-02", p, "8").ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, bill.ID, day("2026-10-05"), "Tagihan dobel", e.Actor); code(err) != "inventory.insufficient" {
		t.Fatalf("void tagihan yang barangnya sudah terjual seharusnya ditolak: %v", err)
	}
	if _, err := e.Sales.VoidInvoice(e.Ctx, e.Company, inv.ID, day("2026-10-05"), "Batal kirim", e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, bill.ID, day("2026-10-05"), "Tagihan dobel", e.Actor); err != nil {
		t.Fatal(err)
	}
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.IsZero() || !s.Value.IsZero() {
		t.Fatalf("stok setelah semua void = %+v", s)
	}
	if !e.Net("1-1302", day("2026-10-31")).IsZero() || !e.Net("5-1001", day("2026-10-31")).IsZero() {
		t.Fatal("persediaan dan HPP harus kembali nol")
	}
	e.VerifyLedger()
}

// INV-04: saldo awal lewat penyesuaian, stock opname dengan selisih, pemakaian barang, dan void.
func TestAdjustmentsOpnameAndIssue(t *testing.T) {
	e := fixture.NewWith(t, "retail")
	p := goods(t, e, "BRG-3")
	cost := rp(2_000)

	opening, err := e.Inventory.CreateAdjustment(e.Ctx, e.Company, inventory.AdjustmentInput{
		Kind: inventory.AdjManual, Date: day("2026-10-01"), AccountID: e.Acc["3-3001"], Memo: "Saldo awal",
		Lines: []inventory.AdjustmentLineInput{{ProductID: p.ID, Qty: qty("10"), UnitCost: &cost}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if opening.Number != "PS/2026/10/0001" || !opening.Total.Equal(rp(20_000)) {
		t.Fatalf("penyesuaian saldo awal = %+v", opening)
	}
	j, err := e.Ledger.Entries(e.Ctx, e.Company, ledgerFilter("PS/2026/10/0001"))
	if err != nil || len(j.Items) != 1 {
		t.Fatalf("jurnal saldo awal memakai nomor dokumennya: %v %+v", err, j.Items)
	}

	counted := qty("8")
	opname, err := e.Inventory.CreateAdjustment(e.Ctx, e.Company, inventory.AdjustmentInput{
		Kind: inventory.AdjCount, Date: day("2026-10-15"), AccountID: e.Acc["5-1003"],
		Lines: []inventory.AdjustmentLineInput{{ProductID: p.ID, CountedQty: &counted}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	l := opname.Lines[0]
	if !l.SystemQty.Equal(qty("10")) || !l.Qty.Equal(qty("-2")) || !l.Value.Equal(rp(-4_000)) {
		t.Fatalf("opname = %+v", l)
	}
	if !e.Net("5-1003", day("2026-10-31")).Equal(rp(4_000)) {
		t.Fatalf("selisih persediaan = %s", e.Net("5-1003", day("2026-10-31")))
	}

	if _, err := e.Inventory.CreateAdjustment(e.Ctx, e.Company, inventory.AdjustmentInput{
		Kind: inventory.AdjIssue, Date: day("2026-10-16"), AccountID: e.Acc["6-2004"],
		Lines: []inventory.AdjustmentLineInput{{ProductID: p.ID, Qty: qty("3")}},
	}, e.Actor); err != nil {
		t.Fatal(err)
	}
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.Equal(qty("5")) || !s.Value.Equal(rp(10_000)) {
		t.Fatalf("stok = %+v", s)
	}
	if _, err := e.Inventory.CreateAdjustment(e.Ctx, e.Company, inventory.AdjustmentInput{
		Kind: inventory.AdjIssue, Date: day("2026-10-16"), AccountID: e.Acc["1-1302"],
		Lines: []inventory.AdjustmentLineInput{{ProductID: p.ID, Qty: qty("1")}},
	}, e.Actor); code(err) != "adjustment.invalid" {
		t.Fatalf("akun lawan tidak boleh akun persediaan: %v", err)
	}

	// Void opname: 2 unit kembali dengan nilai yang sama persis.
	if _, err := e.Inventory.VoidAdjustment(e.Ctx, e.Company, opname.ID, day("2026-10-20"), "Hitung ulang gudang", e.Actor); err != nil {
		t.Fatal(err)
	}
	if !e.Net("5-1003", day("2026-10-31")).IsZero() {
		t.Fatal("void opname harus membalik selisih persediaan")
	}
	if s := stock(t, e, p, "2026-10-31"); !s.Qty.Equal(qty("7")) || !s.Value.Equal(rp(14_000)) {
		t.Fatalf("stok setelah void = %+v", s)
	}
	if _, err := e.Inventory.VoidAdjustment(e.Ctx, e.Company, opname.ID, day("2026-10-20"), "Hitung ulang gudang", e.Actor); code(err) != "adjustment.already_void" {
		t.Fatalf("void dua kali: %v", err)
	}
	e.VerifyLedger()
}

// Akun persediaan hanya bertambah lewat produk, supaya buku besar sama dengan kartu stok.
func TestInventoryAccountNeedsProduct(t *testing.T) {
	e := fixture.NewWith(t, "retail")
	goods(t, e, "BRG-4")
	_, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{
		SupplierID: e.Supplier, Date: day("2026-10-01"), Lines: []trade.LineInput{{
			Description: "Tanpa produk", AccountID: e.Acc["1-1302"], Qty: qty("1"), UnitPrice: rp(1_000), TaxCode: "PPN",
		}},
	}, e.Actor)
	if code(err) != "bill.invalid" || fixture.Fields(err)["lines.0.accountId"] == "" {
		t.Fatalf("tagihan ke akun persediaan tanpa produk seharusnya ditolak: %v", err)
	}
	_, err = e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{
		Kind: "out", Date: day("2026-10-01"), CashAccountID: e.Bank,
		Lines: []cash.LineInput{{AccountID: e.Acc["1-1302"], Amount: rp(1_000)}},
	}, e.Actor)
	if code(err) != "cash.invalid" {
		t.Fatalf("pengeluaran kas ke akun persediaan seharusnya ditolak: %v", err)
	}
	// Produk jasa tidak punya stok, dan barang wajib punya akun persediaan.
	_, err = e.Inventory.CreateProduct(e.Ctx, e.Company, inventory.ProductInput{Code: "X", Name: "X", Kind: inventory.KindGoods, Unit: "pcs"}, e.Actor)
	if code(err) != "product.invalid" {
		t.Fatalf("barang tanpa akun persediaan: %v", err)
	}
}

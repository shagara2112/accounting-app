package trade

import (
	"testing"

	"github.com/google/uuid"

	"lajur/internal/modules/tax"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

func day(s string) civil.Date {
	d, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

var table = tax.Table{
	{Code: "PPN", Kind: tax.KindPPN, RateBP: 1200, DPPNum: 11, DPPDen: 12, ValidFrom: day("2025-01-01")},
	{Code: "NON-PPN", Kind: tax.KindPPN, RateBP: 0, DPPNum: 1, DPPDen: 1, ValidFrom: day("2000-01-01")},
	{Code: "PPH23", Kind: tax.KindPPh, RateBP: 200, DPPNum: 1, DPPDen: 1, Article: "23", ValidFrom: day("2009-01-01")},
}

var acc = uuid.Must(uuid.NewV7())

func line(qty, price, disc, code string) LineInput {
	return LineInput{AccountID: acc, Qty: money.MustParse(qty), UnitPrice: money.MustParse(price), DiscountPct: money.MustParse(disc), TaxCode: code}
}

func TestComputeLinesAndTotals(t *testing.T) {
	lines, tot, err := Compute(table, day("2026-10-05"), []LineInput{
		line("3", "1500000", "10", "PPN"),      // 4.500.000 − 450.000 = 4.050.000; PPN 445.500
		line("2.5", "199999", "0", "PPN"),      // 499.997,5 → 499.998; PPN 54.999 (54.999,78 dibulatkan ke bawah)
		line("1", "750000", "12.5", "NON-PPN"), // 750.000 − 93.750 = 656.250; tanpa PPN
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ gross, discount, net, vat string }{
		{"4500000", "450000", "4050000", "445500"},
		{"499998", "0", "499998", "54999"},
		{"750000", "93750", "656250", "0"},
	}
	for i, w := range want {
		l := lines[i]
		if !l.Gross.Equal(money.MustParse(w.gross)) || !l.Discount.Equal(money.MustParse(w.discount)) ||
			!l.Net.Equal(money.MustParse(w.net)) || !l.VAT.Equal(money.MustParse(w.vat)) {
			t.Errorf("baris %d = gross %s diskon %s net %s ppn %s", i+1, l.Gross.IDR(), l.Discount.IDR(), l.Net.IDR(), l.VAT.IDR())
		}
	}
	if !tot.DPP.Equal(money.FromInt(5_206_248)) || !tot.VAT.Equal(money.FromInt(500_499)) || !tot.Total.Equal(money.FromInt(5_706_747)) {
		t.Fatalf("total = DPP %s PPN %s total %s", tot.DPP.IDR(), tot.VAT.IDR(), tot.Total.IDR())
	}
}

func TestComputeRejectsInvalidLines(t *testing.T) {
	_, _, err := Compute(table, day("2026-10-05"), []LineInput{
		line("0", "1000", "0", "PPN"),
		line("1", "1000", "100.5", "PPN"),
		line("1", "1000", "0", "PPH23"),
		line("1", "1000", "0", "PPN-99"),
		line("1", "1000", "1.234", "NON-PPN"),
	}, true)
	p, ok := err.(*httpx.Problem)
	if !ok {
		t.Fatalf("error = %v", err)
	}
	for _, f := range []string{"lines.0.qty", "lines.1.discountPct", "lines.2.taxCode", "lines.3.taxCode", "lines.4.discountPct"} {
		if p.Fields[f] == "" {
			t.Errorf("tidak ada pesan untuk %s: %v", f, p.Fields)
		}
	}

	_, _, err = Compute(table, day("2026-10-05"), []LineInput{line("1", "1000", "0", "PPN")}, false)
	if p, ok := err.(*httpx.Problem); !ok || p.Fields["lines.0.taxCode"] == "" {
		t.Fatalf("PT non-PKP seharusnya tidak boleh memungut PPN: %v", err)
	}
	if _, _, err := Compute(table, day("2026-10-05"), []LineInput{line("1", "1000", "0", "NON-PPN")}, false); err != nil {
		t.Fatalf("NON-PPN untuk non-PKP seharusnya boleh: %v", err)
	}
}

func TestAgingBuckets(t *testing.T) {
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	asOf := day("2026-10-31")
	aging := BuildAging(asOf, []AgingItem{
		{ContactID: a, Due: day("2026-11-15"), Outstanding: money.FromInt(100)}, // belum jatuh tempo
		{ContactID: a, Due: day("2026-10-31"), Outstanding: money.FromInt(50)},  // jatuh tempo hari ini
		{ContactID: a, Due: day("2026-10-01"), Outstanding: money.FromInt(30)},  // 30 hari
		{ContactID: b, Due: day("2026-08-01"), Outstanding: money.FromInt(500)}, // 91 hari
		{ContactID: b, Due: day("2026-09-01"), Outstanding: money.Amount{}},     // lunas, diabaikan
	}, map[uuid.UUID]string{a: "PT A", b: "PT B"})

	if len(aging.Rows) != 2 || aging.Rows[0].ContactName != "PT B" {
		t.Fatalf("baris umur = %+v", aging.Rows)
	}
	checks := map[Bucket]int64{Current: 150, Days30: 30, Days60: 0, Days90: 0, Over90: 500}
	for bucket, want := range checks {
		if !aging.Totals[bucket].Equal(money.FromInt(want)) {
			t.Errorf("total %s = %s, seharusnya %d", bucket, aging.Totals[bucket], want)
		}
	}
	if !aging.Total.Equal(money.FromInt(680)) || aging.Rows[1].Documents != 3 {
		t.Fatalf("total umur = %s, dokumen PT A = %d", aging.Total, aging.Rows[1].Documents)
	}
}

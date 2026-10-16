package asset

import (
	"testing"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

func month(s string) civil.Date {
	d, err := civil.ParseMonth(s)
	if err != nil {
		panic(err)
	}
	return d
}

func rp(n int64) money.Amount { return money.FromInt(n) }

func sum(ms []Month) money.Amount {
	var t money.Amount
	for _, m := range ms {
		t = t.Add(m.Amount)
	}
	return t
}

func TestStraightLine(t *testing.T) {
	ms := Plan{Method: Straight, Cost: rp(10_000_000), LifeMonths: 36, Start: month("2026-01")}.Months()
	if len(ms) != 36 || !ms[0].Amount.Equal(rp(277_778)) || !ms[35].Amount.Equal(rp(277_770)) || !sum(ms).Equal(rp(10_000_000)) {
		t.Fatalf("garis lurus: %d bulan, pertama %s, terakhir %s, total %s", len(ms), ms[0].Amount, ms[35].Amount, sum(ms))
	}
	if ms[35].Month != month("2028-12") || !ms[35].BookValue.IsZero() {
		t.Fatalf("bulan terakhir = %+v", ms[35])
	}
}

// Aset lama: akumulasi saldo awal dipakai, sisa nilai buku dibagi rata ke sisa masa manfaat.
func TestStraightLineFromOpening(t *testing.T) {
	om := month("2025-06")
	ms := Plan{
		Method: Straight, Cost: rp(120_000_000), Residual: rp(12_000_000), LifeMonths: 60, Start: month("2024-01"),
		OpeningAccum: rp(30_000_000), OpeningMonth: &om,
	}.Months()
	if len(ms) != 42 || ms[0].Month != month("2025-07") || !ms[0].Amount.Equal(rp(1_857_143)) || !ms[41].Amount.Equal(rp(1_857_137)) {
		t.Fatalf("lanjutan saldo awal: %d bulan, %+v … %+v", len(ms), ms[0], ms[len(ms)-1])
	}
	if !ms[41].BookValue.Equal(rp(12_000_000)) || !ms[41].Accum.Equal(rp(108_000_000)) {
		t.Fatalf("nilai buku akhir seharusnya nilai sisa: %+v", ms[41])
	}
}

func TestDecliningBalance(t *testing.T) {
	ms := Plan{Method: Declining, Cost: rp(100_000_000), LifeMonths: 48, Start: month("2026-01")}.Months()
	years := ByYear(ms)
	want := []int64{50_000_004, 24_999_996, 12_500_004, 12_499_996}
	if len(years) != 4 {
		t.Fatalf("tahun = %+v", years)
	}
	for i, w := range want {
		if !years[i].Amount.Equal(rp(w)) {
			t.Fatalf("tahun %d: %s, mau %d", years[i].Year, years[i].Amount, w)
		}
	}
	last := ms[len(ms)-1]
	if len(ms) != 48 || !last.BookValue.IsZero() || !ms[36].Amount.Equal(rp(1_041_666)) || !last.Amount.Equal(rp(1_041_670)) {
		t.Fatalf("12 bulan terakhir dibagi rata: %+v … %+v", ms[36], last)
	}
}

func TestNoDepreciation(t *testing.T) {
	if ms := (Plan{Method: None, Cost: rp(500_000_000), Start: month("2026-01")}).Months(); ms != nil {
		t.Fatalf("tanah tidak disusutkan: %+v", ms)
	}
	if ys := Fiscal("tidak_disusutkan", Straight, rp(500_000_000), month("2026-01")); ys != nil {
		t.Fatalf("tanah tidak disusutkan secara fiskal: %+v", ys)
	}
}

func TestFiscalStraightLine(t *testing.T) {
	ys := Fiscal("kelompok_1", Straight, rp(48_000_000), month("2026-04"))
	want := map[int]int64{2026: 9_000_000, 2027: 12_000_000, 2028: 12_000_000, 2029: 12_000_000, 2030: 3_000_000}
	if len(ys) != len(want) {
		t.Fatalf("tahun fiskal = %+v", ys)
	}
	for _, y := range ys {
		if !y.Amount.Equal(rp(want[y.Year])) {
			t.Fatalf("%d: %s, mau %d", y.Year, y.Amount, want[y.Year])
		}
	}
}

func TestFiscalDecliningBalance(t *testing.T) {
	ys := Fiscal("kelompok_1", Declining, rp(100_000_000), month("2026-07"))
	want := []int64{25_000_000, 37_500_000, 18_750_000, 9_375_000, 9_375_000}
	if len(ys) != len(want) || ys[4].Year != 2030 || !ys[4].BookValue.IsZero() {
		t.Fatalf("tahun fiskal = %+v", ys)
	}
	for i, w := range want {
		if !ys[i].Amount.Equal(rp(w)) {
			t.Fatalf("%d: %s, mau %d", ys[i].Year, ys[i].Amount, w)
		}
	}
	// Bangunan hanya boleh garis lurus.
	b := Fiscal("bangunan_permanen", Declining, rp(2_000_000_000), month("2026-01"))
	if len(b) != 20 || !b[0].Amount.Equal(rp(100_000_000)) {
		t.Fatalf("bangunan = %d tahun, pertama %s", len(b), b[0].Amount)
	}
}

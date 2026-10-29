package report_test

import (
	"math"
	"testing"

	"lajur/internal/ledger"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/report"
)

func ratio(t *testing.T, r report.Ratios, key string) report.RatioRow {
	t.Helper()
	for _, x := range r.Rows {
		if x.Key == key {
			return x
		}
	}
	t.Fatalf("rasio %s tidak ada", key)
	return report.RatioRow{}
}

func near(t *testing.T, label string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 0.01 {
		t.Fatalf("%s = %v, seharusnya %.2f", label, got, want)
	}
}

// RPT-09: rasio keuangan per akhir bulan dari neraca (sejak awal) dan laba rugi (sejak awal tahun
// buku), dengan status menurut panduan umum untuk bulan terakhir.
func TestFinancialRatios(t *testing.T) {
	e := fixture.New(t)
	journal(t, e, "2026-10-01", ledger.General, "1-1111", "3-1001", 1_000_000) // setoran modal ke bank
	journal(t, e, "2026-10-05", ledger.General, "1-1201", "4-1001", 300_000)   // pendapatan belum dibayar
	journal(t, e, "2026-10-06", ledger.General, "6-2002", "2-1101", 100_000)   // beban belum dibayar

	r, err := e.Report.Ratios(e.Ctx, e.Company, day("2026-10-01"), 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Months) != 6 || r.Months[0] != "2026-05" || r.Months[5] != "2026-10" || len(r.Basis) != 6 {
		t.Fatalf("bulan = %v", r.Months)
	}
	b := r.Basis[5]
	if b.Days != 304 || !b.CurrentAssets.Equal(rp(1_300_000)) || !b.CurrentLiabilities.Equal(rp(100_000)) ||
		!b.Cash.Equal(rp(1_000_000)) || !b.Equity.Equal(rp(1_200_000)) || !b.NetProfit.Equal(rp(200_000)) ||
		!b.Receivable.Equal(rp(300_000)) || !b.Payable.Equal(rp(100_000)) {
		t.Fatalf("penyusun Oktober = %+v", b)
	}
	cur := ratio(t, r, "current")
	near(t, "rasio lancar", cur.Values[5], 13)
	if cur.Values[0] != nil || cur.Status != "good" {
		t.Fatalf("rasio lancar Mei tanpa liabilitas seharusnya kosong, status Oktober good: %+v", cur)
	}
	near(t, "rasio kas", ratio(t, r, "cash").Values[5], 10)
	near(t, "utang terhadap ekuitas", ratio(t, r, "der").Values[5], 100_000.0/1_200_000)
	near(t, "margin laba kotor", ratio(t, r, "gross").Values[5], 100)
	near(t, "margin laba bersih", ratio(t, r, "net").Values[5], 200.0/3)
	dso := ratio(t, r, "dso")
	near(t, "DSO", dso.Values[5], 304)
	if dso.Status != "bad" {
		t.Fatalf("DSO 304 hari seharusnya buruk: %s", dso.Status)
	}
	// Tanpa beban pokok, DPO memakai beban usaha.
	near(t, "DPO", ratio(t, r, "dpo").Values[5], 304)
}

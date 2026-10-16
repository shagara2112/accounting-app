package tax

import (
	"testing"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

func day(s string) civil.Date {
	d, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// Salinan baris migrasi 00004, supaya perhitungan diuji tanpa basis data.
var table = Table{
	{Code: "PPN", Kind: KindPPN, RateBP: 1100, DPPNum: 1, DPPDen: 1, ValidFrom: day("2022-04-01"), ValidTo: ptr(day("2024-12-31"))},
	{Code: "PPN", Kind: KindPPN, RateBP: 1200, DPPNum: 11, DPPDen: 12, ValidFrom: day("2025-01-01")},
	{Code: "PPN-MEWAH", Kind: KindPPN, RateBP: 1200, DPPNum: 1, DPPDen: 1, ValidFrom: day("2025-01-01")},
	{Code: "NON-PPN", Kind: KindPPN, RateBP: 0, DPPNum: 1, DPPDen: 1, ValidFrom: day("2000-01-01")},
	{Code: "PPH23", Kind: KindPPh, RateBP: 200, DPPNum: 1, DPPDen: 1, Article: ArticlePPh23, ValidFrom: day("2009-01-01")},
	{Code: "PPH42-K265", Kind: KindPPh, RateBP: 265, DPPNum: 1, DPPDen: 1, Article: ArticleFinal, ValidFrom: day("2022-02-21")},
}

func ptr[T any](v T) *T { return &v }

// PMK 131/2024: PPN 12% × DPP nilai lain 11/12 untuk non-mewah, sehingga efektif 11%;
// transaksi 2024 tetap 11% × DPP penuh.
func TestVATFollowsTransactionDate(t *testing.T) {
	cases := []struct {
		code, date, dpp    string
		wantOther, wantVAT string
	}{
		{"PPN", "2026-10-05", "100000000", "91666666.6700", "11000000.0000"},
		{"PPN", "2026-10-05", "1234567", "1131686.4200", "135802.0000"},
		{"PPN", "2024-12-31", "1000000", "1000000.0000", "110000.0000"},
		{"PPN-MEWAH", "2026-10-05", "1000000", "1000000.0000", "120000.0000"},
		{"NON-PPN", "2026-10-05", "1000000", "1000000.0000", "0.0000"},
	}
	for _, c := range cases {
		code, err := table.Find(c.code, day(c.date))
		if err != nil {
			t.Fatal(err)
		}
		other, vat := code.VAT(money.MustParse(c.dpp))
		if other.String() != c.wantOther || vat.String() != c.wantVAT {
			t.Errorf("%s %s DPP %s: nilai lain %s PPN %s, seharusnya %s dan %s", c.code, c.date, c.dpp, other, vat, c.wantOther, c.wantVAT)
		}
	}
}

func TestFindRejectsUnknownAndExpiredCodes(t *testing.T) {
	if _, err := table.Find("PPN-MEWAH", day("2024-06-01")); err == nil {
		t.Error("PPN-MEWAH belum berlaku di 2024")
	}
	if _, err := table.Find("PPN-99", day("2026-01-01")); err == nil {
		t.Error("kode tidak dikenal seharusnya ditolak")
	}
	if n := len(table.On(day("2026-10-05"), KindPPN)); n != 3 {
		t.Errorf("kode PPN yang berlaku 2026 = %d, seharusnya 3", n)
	}
}

func TestWithholdingRoundsDown(t *testing.T) {
	pph23, _ := table.Find("PPH23", day("2026-10-05"))
	if got := pph23.Withholding(money.MustParse("333333")); got.String() != "6666.0000" {
		t.Errorf("PPh 23 = %s", got)
	}
	final, _ := table.Find("PPH42-K265", day("2026-10-05"))
	if got := final.Withholding(money.FromInt(150_000_000)); !got.Equal(money.FromInt(3_975_000)) {
		t.Errorf("PPh 4(2) 2,65%% = %s", got)
	}
}

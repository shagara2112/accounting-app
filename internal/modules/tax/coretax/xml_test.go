package coretax

import (
	"os"
	"path/filepath"
	"testing"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// Tes golden: perubahan pada XML harus disengaja. Jalankan dengan UPDATE_GOLDEN=1 untuk
// memperbarui file contoh setelah format diubah (dan diuji impor ke Coretax).
func TestEncodeV1Golden(t *testing.T) {
	d, _ := civil.Parse("2026-10-05")
	out, err := EncodeV1(XMLSeller{NPWP: "0123456789012345"}, []XMLInvoice{{
		Date: d, TrxCode: "04", Reference: "FJ/2026/10/0001",
		Buyer: XMLBuyer{Name: "PT Pembeli & Rekan", NPWP: "0987654321098765", NITKU: "0987654321098765000001", Address: "Jl. Contoh No. 1, Jakarta"},
		Lines: []XMLLine{
			{Opt: "B", Code: "000000", Name: "Jasa konsultansi", Unit: "UM.0021", Price: money.FromInt(4_500_000), Qty: money.FromInt(2),
				Discount: money.FromInt(900_000), TaxBase: money.FromInt(8_100_000), OtherDPP: money.MustParse("7425000"), RateBP: 1200, VAT: money.FromInt(891_000)},
			{Opt: "B", Code: "000000", Name: "Biaya perjalanan", Unit: "UM.0021", Price: money.MustParse("1234567"), Qty: money.MustParse("1.5"),
				TaxBase: money.MustParse("1851851"), OtherDPP: money.MustParse("1697530.08"), RateBP: 1200, VAT: money.FromInt(203_703)},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "faktur_keluaran_v1.xml")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(want) {
		t.Fatalf("XML berbeda dari %s:\n%s", golden, out)
	}
}

func TestNumberFormatting(t *testing.T) {
	cases := map[string]string{
		decimal2(money.MustParse("1250000")):     "1250000.00",
		decimal2(money.MustParse("916666.6667")): "916666.67",
		plain(money.MustParse("2")):              "2",
		plain(money.MustParse("2.5")):            "2.5",
		rate(1200):                               "12",
		rate(1175):                               "11.75",
		idtku("0123456789012345", ""):            "0123456789012345000000",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("hasil %q, seharusnya %q", got, want)
		}
	}
}

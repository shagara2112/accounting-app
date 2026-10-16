package money

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestParseAndString(t *testing.T) {
	cases := map[string]string{
		"0":                  "0.0000",
		"1":                  "1.0000",
		"1250000":            "1250000.0000",
		"1250000.5":          "1250000.5000",
		"-12.3456":           "-12.3456",
		"0.0001":             "0.0001",
		"-0.01":              "-0.0100",
		"999999999999999.99": "999999999999999.9900",
	}
	for in, want := range cases {
		a, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if got := a.String(); got != want {
			t.Errorf("Parse(%q).String() = %q, seharusnya %q", in, got, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"", " 1", "1.", ".5", "1,5", "1.000.000", "1e6", "+1", "01", "1.23456", "NaN",
		"1000000000000000", // 16 angka bulat melebihi NUMERIC(19,4)
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) seharusnya ditolak", in)
		}
	}
}

func TestArithmeticIsExact(t *testing.T) {
	// 0,1 + 0,2 = 0,3 persis; di float64 hasilnya 0,30000000000000004.
	sum := MustParse("0.1").Add(MustParse("0.2"))
	if !sum.Equal(MustParse("0.3")) {
		t.Fatalf("0.1 + 0.2 = %s", sum)
	}
	var total Amount
	for i := 0; i < 1000; i++ {
		total = total.Add(MustParse("0.0001"))
	}
	if total.String() != "0.1000" {
		t.Fatalf("akumulasi = %s", total)
	}
	a := FromInt(100)
	b := a.Sub(FromInt(150))
	if b.Sign() >= 0 || b.Abs().String() != "50.0000" || !a.Equal(FromInt(100)) {
		t.Fatalf("operasi mengubah operand atau salah: a=%s b=%s", a, b)
	}
}

func TestIDR(t *testing.T) {
	cases := map[string]string{
		"0":          "Rp 0",
		"1":          "Rp 1",
		"1250000":    "Rp 1.250.000",
		"1250000.5":  "Rp 1.250.000,50",
		"999.1234":   "Rp 999,1234",
		"-25000000":  "-Rp 25.000.000",
		"100000.000": "Rp 100.000",
	}
	for in, want := range cases {
		if got := MustParse(in).IDR(); got != want {
			t.Errorf("IDR(%s) = %q, seharusnya %q", in, got, want)
		}
	}
}

func TestJSON(t *testing.T) {
	b, err := json.Marshal(struct{ V Amount }{MustParse("1500.25")})
	if err != nil || string(b) != `{"V":"1500.2500"}` {
		t.Fatalf("marshal = %s, %v", b, err)
	}
	var v struct{ V Amount }
	if err := json.Unmarshal([]byte(`{"V":"42.5"}`), &v); err != nil || v.V.String() != "42.5000" {
		t.Fatalf("unmarshal = %s, %v", v.V, err)
	}
	if err := json.Unmarshal([]byte(`{"V":42.5}`), &v); err == nil {
		t.Fatal("angka JSON seharusnya ditolak; nominal wajib string")
	}
}

func TestNumericRoundTrip(t *testing.T) {
	cases := []struct {
		n    pgtype.Numeric
		want string
	}{
		{pgtype.Numeric{Int: big.NewInt(12500000000), Exp: -4, Valid: true}, "1250000.0000"},
		{pgtype.Numeric{Int: big.NewInt(125), Exp: 4, Valid: true}, "1250000.0000"},
		{pgtype.Numeric{Int: big.NewInt(-5), Exp: -1, Valid: true}, "-0.5000"},
		{pgtype.Numeric{Int: big.NewInt(1230000), Exp: -6, Valid: true}, "1.2300"},
	}
	for _, c := range cases {
		var a Amount
		if err := a.ScanNumeric(c.n); err != nil {
			t.Fatal(err)
		}
		if a.String() != c.want {
			t.Errorf("scan = %s, seharusnya %s", a, c.want)
		}
		n, _ := a.NumericValue()
		var back Amount
		if err := back.ScanNumeric(n); err != nil || !back.Equal(a) {
			t.Errorf("round trip %s gagal: %s %v", a, back, err)
		}
	}
	var a Amount
	if err := a.ScanNumeric(pgtype.Numeric{Int: big.NewInt(123456), Exp: -5, Valid: true}); err == nil {
		t.Error("5 angka desimal seharusnya ditolak, bukan dibulatkan")
	}
	if err := a.ScanNumeric(pgtype.Numeric{}); err == nil {
		t.Error("NULL seharusnya ditolak untuk Amount non-pointer")
	}
}

func TestMulFracAndRounding(t *testing.T) {
	cases := []struct {
		a        string
		num, den int64
		places   int
		mode     Rounding
		want     string
	}{
		// PPN 12% × DPP nilai lain 11/12 = 11% efektif, dibulatkan ke bawah ke rupiah.
		{"1000000", 11 * 1200, 12 * 10000, 0, Down, "110000.0000"},
		{"1234567", 11 * 1200, 12 * 10000, 0, Down, "135802.0000"},
		// DPP nilai lain dengan 2 desimal.
		{"1000000", 11, 12, 2, HalfUp, "916666.6700"},
		// PPh 23 2%, PPh 4(2) 2,65%.
		{"333333", 200, 10000, 0, Down, "6666.0000"},
		{"100000000", 265, 10000, 0, Down, "2650000.0000"},
		// Diskon 12,5% dari 99.999 = 12.499,875 → 12.500 (half up).
		{"99999", 125, 1000, 0, HalfUp, "12500.0000"},
		{"-10.5", 1, 1, 0, HalfUp, "-11.0000"},
		{"-10.5", 1, 1, 0, Down, "-10.0000"},
	}
	for _, c := range cases {
		got := MustParse(c.a).MulFrac(c.num, c.den, c.places, c.mode)
		if got.String() != c.want {
			t.Errorf("%s × %d/%d (%d desimal) = %s, seharusnya %s", c.a, c.num, c.den, c.places, got, c.want)
		}
	}
	if got := MustParse("3.5").Mul(MustParse("1999.99"), 0, HalfUp); got.String() != "7000.0000" {
		t.Errorf("3,5 × 1.999,99 = %s, seharusnya 7000 (6.999,965 dibulatkan)", got)
	}
	if got := MustParse("0.3333").Mul(MustParse("3"), 4, HalfUp); got.String() != "0.9999" {
		t.Errorf("0,3333 × 3 = %s", got)
	}
}

func TestMulDiv(t *testing.T) {
	cases := []struct{ a, num, den, want string }{
		// Biaya rata-rata: stok 3 unit senilai 10.000, keluar 1 → 3.333,33 dibulatkan ke 3.333.
		{"10000", "1", "3", "3333.0000"},
		{"10000", "2", "3", "6667.0000"},
		// Kuantitas pecahan: 2,5 kg dari 7,5 kg senilai 1.000.001.
		{"1000001", "2.5", "7.5", "333334.0000"},
		{"-10000", "2", "3", "-6667.0000"},
	}
	for _, c := range cases {
		got := MustParse(c.a).MulDiv(MustParse(c.num), MustParse(c.den), 0, HalfUp)
		if got.String() != c.want {
			t.Errorf("%s × %s ÷ %s = %s, seharusnya %s", c.a, c.num, c.den, got, c.want)
		}
	}
}

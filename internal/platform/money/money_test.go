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

package coretax

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"testing"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// elements membaca elemen ke-n bernama name (mis. Bpu ke-0) sebagai daftar "Nama=isi" dan
// "Nama@nil", sehingga urutan, nama, isi, dan xsi:nil bisa dibandingkan persis.
func elements(t *testing.T, doc []byte, name string, n int) []string {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var out []string
	seen, depth, inside := -1, 0, false
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			if v.Name.Local == name {
				seen++
				inside = seen == n
				depth = 0
				continue
			}
			if inside {
				depth++
				text.Reset()
				for _, a := range v.Attr {
					if a.Name.Local == "nil" && a.Value == "true" {
						out = append(out, v.Name.Local+"@nil")
					}
				}
			}
		case xml.CharData:
			if inside {
				text.Write(v)
			}
		case xml.EndElement:
			if v.Name.Local == name && inside {
				return out
			}
			if inside && depth > 0 {
				if s := strings.TrimSpace(text.String()); s != "" {
					out = append(out, v.Name.Local+"="+s)
				}
				text.Reset()
				depth--
			}
		}
	}
	return out
}

func ym(s string) civil.Date {
	d, err := civil.ParseMonth(s)
	if err != nil {
		panic(err)
	}
	return d
}

func date(s string) civil.Date {
	d, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

func diff(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("XML berbeda dari template DJP\nkita:\n%s\n\ntemplate:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Struktur BPPU sama persis dengan bukti potong pertama template resmi DJP.
func TestBPPUMatchesDJPTemplate(t *testing.T) {
	tmpl, err := os.ReadFile("testdata/djp-bppu-template.xml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := BuildBPPU(XMLWithholder{NPWP: "0029482015507000"}, []XMLSlip{{
		Period: ym("2024-01"), CounterpartTIN: "1671112505640003", Facility: "N/A", TaxObjectCode: "24-101-01",
		TaxBase: money.FromInt(100_000_000), RateBP: 1500, Document: "CommercialInvoice", DocumentNumber: "ABC123",
		DocumentDate: date("2024-01-13"), WithholdingDate: date("2024-01-21"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	diff(t, elements(t, got, "Bpu", 0), elements(t, tmpl, "Bpu", 0))
	if !bytes.Contains(got, []byte("<BpuBulk xmlns:xsi=")) || !bytes.Contains(got, []byte("<TIN>0029482015507000</TIN>")) {
		t.Fatalf("kepala XML:\n%s", got)
	}
	if rateText(175) != "1.75" || rateText(1000) != "10" || rateText(50) != "0.5" {
		t.Fatal("format tarif")
	}
}

// Struktur BPNR (PPh 26) sama persis dengan bukti potong kedua template resmi DJP.
func TestBPNRMatchesDJPTemplate(t *testing.T) {
	tmpl, err := os.ReadFile("testdata/djp-bpnr-template.xml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := BuildBPNR(XMLWithholder{NPWP: "0029482015507000"}, []XMLSlip{{
		Period: ym("2024-10"), CounterpartTIN: "NPWP-JEPANG-123", Name: "IKARI SHINJI", Country: "JPN", Address: "SHIBUYA",
		Facility: "N/A", TaxObjectCode: "27-100-05", TaxBase: money.FromInt(200_000_000), Deemed: 25, RateBP: 2000,
		Document: "CommercialInvoice", DocumentNumber: "ABC123", DocumentDate: date("2024-01-13"), WithholdingDate: date("2024-10-21"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	diff(t, elements(t, got, "BPNR", 0), elements(t, tmpl, "BPNR", 1))
	if !bytes.Contains(got, []byte("<BPNRBulk xmlns:xsi=")) {
		t.Fatalf("kepala XML:\n%s", got)
	}
}

func TestBupotRefLoads(t *testing.T) {
	r := Ref()
	o, ok := findObject(r.BPPUObjects, "28-403-02")
	if !ok || o.Rate != 10 || len(r.BPNRObjects) == 0 || !hasItem(r.Documents, "CommercialInvoice") || !hasItem(r.Countries, "SGP") {
		t.Fatalf("referensi = %+v, %v", o, ok)
	}
	if r.Source == "" {
		t.Fatal("sumber referensi kosong")
	}
}

package export

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"lajur/internal/platform/money"
)

func sample() Table {
	return Table{
		Title:    "Laba rugi & <uji>",
		Subtitle: []string{"PT Uji", "Oktober 2026"},
		Columns:  []Column{{Header: "Akun"}, {Header: "Okt 2026", Numeric: true}},
		Rows: []Row{
			{Cells: []Cell{Text("Pendapatan, jasa")}},
			{Cells: []Cell{Text("4-1001 Pendapatan \"jasa\""), Num(money.MustParse("1250000"))}, Indent: 1},
			{Cells: []Cell{Text("Kuantitas"), Num(money.MustParse("2.5"))}, Indent: 1},
			{Cells: []Cell{Text("Rugi"), Num(money.MustParse("-300"))}, Bold: true},
		},
	}
}

func TestCSV(t *testing.T) {
	var b bytes.Buffer
	if err := WriteCSV(&b, sample()); err != nil {
		t.Fatal(err)
	}
	want := "\ufeffAkun,Okt 2026\n\"Pendapatan, jasa\",\n\"  4-1001 Pendapatan \"\"jasa\"\"\",1250000\n\"  Kuantitas\",2.5\nRugi,-300\n"
	if b.String() != want {
		t.Fatalf("CSV =\n%q\nseharusnya\n%q", b.String(), want)
	}
}

// XLSX adalah zip berisi XML yang sah; angka ditulis sebagai angka, teks di-escape.
func TestXLSX(t *testing.T) {
	var b bytes.Buffer
	if err := WriteXLSX(&b, sample()); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var sheet string
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r)
		dec := xml.NewDecoder(bytes.NewReader(body))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s bukan XML yang sah: %v", f.Name, err)
			}
		}
		if f.Name == "xl/worksheets/sheet1.xml" {
			sheet = string(body)
		}
	}
	for _, want := range []string{
		`<v>1250000</v>`, `<v>-300</v>`, `<v>2.5</v>`, `Laba rugi &amp; &lt;uji&gt;`, `Pendapatan &quot;jasa&quot;`, `state="frozen"`,
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("sheet tidak memuat %s", want)
		}
	}
	if colName(0) != "A" || colName(25) != "Z" || colName(26) != "AA" {
		t.Error("nama kolom salah")
	}
}

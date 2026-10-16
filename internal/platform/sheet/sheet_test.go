package sheet

import (
	"bytes"
	"testing"

	"lajur/internal/platform/export"
	"lajur/internal/platform/money"
)

var cols = []Column{
	{Key: "name", Names: []string{"Nama*", "Nama kontak"}, Required: true},
	{Key: "amount", Names: []string{"Saldo"}},
	{Key: "date", Names: []string{"Tanggal"}},
}

// Template dari Lajur (judul dan petunjuk di atas baris judul) terbaca kembali, termasuk angka
// pecahan yang ditulis sebagai angka Excel.
func TestReadXLSXFromTemplate(t *testing.T) {
	var buf bytes.Buffer
	frac := money.MustParse("1.125")
	err := export.WriteXLSX(&buf, export.Table{
		Title: "Template impor", Subtitle: []string{"Kolom bertanda * wajib diisi."},
		Columns: []export.Column{{Header: "Nama*"}, {Header: "Saldo", Numeric: true}, {Header: "Tanggal"}},
		Rows: []export.Row{
			{Cells: []export.Cell{export.Text("PT Satu & Dua"), export.Num(money.FromInt(1_250_000)), export.Text("31/10/2026")}},
			{Cells: []export.Cell{export.Text("CV Tiga"), export.Num(frac), export.Text("2026-10-05")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := Read("impor.xlsx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	tab, err := Parse(rows, cols)
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Rows) != 2 || tab.Rows[0].Line != 5 || tab.Rows[0].Get("name") != "PT Satu & Dua" {
		t.Fatalf("baris = %+v", tab.Rows)
	}
	a, err := Amount(tab.Rows[0].Get("amount"))
	if err != nil || !a.Equal(money.FromInt(1_250_000)) {
		t.Fatalf("saldo = %s (%v)", a, err)
	}
	if b, _ := Amount(tab.Rows[1].Get("amount")); !b.Equal(frac) {
		t.Fatalf("angka pecahan Excel terbaca %s", b)
	}
	if d, err := Date(tab.Rows[0].Get("date")); err != nil || d.String() != "2026-10-31" {
		t.Fatalf("tanggal = %s (%v)", d, err)
	}
}

func TestReadCSVSemicolonAndValues(t *testing.T) {
	data := []byte("\ufeffNama kontak;Saldo;Tanggal\nPT A;1.250.000,50;45931\n;;\nPT B;(2.000);1/2/2026\n")
	rows, err := Read("x.csv", data)
	if err != nil {
		t.Fatal(err)
	}
	tab, err := Parse(rows, cols)
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Rows) != 2 || tab.Rows[1].Line != 4 {
		t.Fatalf("baris = %+v", tab.Rows)
	}
	if a, _ := Amount(tab.Rows[0].Get("amount")); !a.Equal(money.MustParse("1250000.5")) {
		t.Fatalf("saldo = %s", a)
	}
	if a, _ := Amount(tab.Rows[1].Get("amount")); !a.Equal(money.FromInt(-2000)) {
		t.Fatalf("saldo negatif = %s", a)
	}
	if d, _ := Date(tab.Rows[0].Get("date")); d.String() != "2025-10-01" {
		t.Fatalf("tanggal Excel = %s", d)
	}
	if d, _ := Date(tab.Rows[1].Get("date")); d.String() != "2026-02-01" {
		t.Fatalf("tanggal hari/bulan = %s", d)
	}
	if _, err := Parse([][]string{{"Nama", "Saldo"}, {"x", "1"}}, []Column{{Key: "a", Names: []string{"Nama"}, Required: true}, {Key: "b", Names: []string{"Kode"}, Required: true}}); err == nil {
		t.Fatal("kolom wajib yang hilang seharusnya ditolak")
	}
	if _, err := Read("x.pdf", data); err == nil {
		t.Fatal("format lain seharusnya ditolak")
	}
}

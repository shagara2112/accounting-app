// Package sheet membaca tabel dari berkas Excel (.xlsx) atau CSV untuk impor massal (PLT-07),
// tanpa dependensi luar. Hanya lembar pertama yang dibaca; sel dikembalikan sebagai teks apa
// adanya (angka dan tanggal Excel tetap berupa angka mentah, mis. "45930" untuk tanggal).
package sheet

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"lajur/internal/platform/httpx"
)

const (
	MaxRows = 5000
	maxCols = 60
	// Batas isi zip yang dibuka, supaya berkas kecil yang memuai besar (zip bomb) ditolak.
	maxUnzipped = 64 << 20
)

var errFormat = httpx.Unprocessable("import.format", "Berkas harus Excel (.xlsx) atau CSV.")

// Read membaca semua baris lembar pertama. Baris kosong di akhir dibuang; baris kosong di
// tengah dipertahankan supaya nomor baris sama dengan yang terlihat di Excel.
func Read(name string, data []byte) ([][]string, error) {
	var rows [][]string
	var err error
	switch strings.ToLower(path.Ext(name)) {
	case ".xlsx":
		rows, err = readXLSX(data)
	case ".csv", ".txt":
		rows, err = readCSV(data)
	default:
		return nil, errFormat
	}
	if err != nil {
		return nil, err
	}
	for len(rows) > 0 && blank(rows[len(rows)-1]) {
		rows = rows[:len(rows)-1]
	}
	if len(rows) > MaxRows+20 {
		return nil, httpx.Unprocessable("import.too_many_rows", fmt.Sprintf("Maksimal %d baris per berkas; pecah berkasnya.", MaxRows))
	}
	return rows, nil
}

func blank(r []string) bool {
	for _, c := range r {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// readCSV menerima koma atau titik koma (Excel berbahasa Indonesia menyimpan CSV dengan titik
// koma) dan BOM UTF-8.
func readCSV(data []byte) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\ufeff"))
	if !utf8.Valid(data) {
		return nil, httpx.Unprocessable("import.encoding", "CSV harus disimpan sebagai UTF-8 (Excel: Simpan sebagai → CSV UTF-8).")
	}
	first, _, _ := bytes.Cut(data, []byte("\n"))
	r := csv.NewReader(bytes.NewReader(data))
	if bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
		r.Comma = ';'
	}
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var rows [][]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, httpx.Unprocessable("import.csv", "CSV tidak terbaca: "+err.Error())
		}
		if len(rows) > MaxRows+20 {
			return rows, nil
		}
		rows = append(rows, trimCols(rec))
	}
}

func trimCols(r []string) []string {
	if len(r) > maxCols {
		r = r[:maxCols]
	}
	return r
}

type xlsxRel struct {
	ID     string `xml:"Id,attr"`
	Target string `xml:"Target,attr"`
}

type xlsxCell struct {
	Ref    string `xml:"r,attr"`
	Type   string `xml:"t,attr"`
	Value  string `xml:"v"`
	Inline struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}

func readXLSX(data []byte) ([][]string, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errFormat
	}
	files := map[string]*zip.File{}
	var total uint64
	for _, f := range z.File {
		files[f.Name] = f
		total += f.UncompressedSize64
	}
	if total > maxUnzipped {
		return nil, httpx.Unprocessable("import.too_large", "Isi berkas Excel terlalu besar.")
	}
	sheetPath, err := firstSheet(files)
	if err != nil {
		return nil, err
	}
	shared, err := sharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return nil, err
	}
	f := files[sheetPath]
	if f == nil {
		return nil, errFormat
	}
	rc, err := f.Open()
	if err != nil {
		return nil, errFormat
	}
	defer rc.Close()

	dec := xml.NewDecoder(io.LimitReader(rc, maxUnzipped))
	var rows [][]string
	var cur []string
	rowNum := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, httpx.Unprocessable("import.xlsx", "Lembar Excel tidak terbaca.")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				n := rowNum + 1
				for _, a := range t.Attr {
					if a.Name.Local == "r" {
						if v, err := strconv.Atoi(a.Value); err == nil && v > rowNum {
							n = v
						}
					}
				}
				// Baris yang dilewati (kosong) tetap diisi supaya nomor baris sesuai Excel.
				for rowNum+1 < n && len(rows) <= MaxRows+20 {
					rows = append(rows, nil)
					rowNum++
				}
				rowNum = n
				cur = nil
			case "c":
				var c xlsxCell
				if err := dec.DecodeElement(&c, &t); err != nil {
					return nil, httpx.Unprocessable("import.xlsx", "Sel Excel tidak terbaca.")
				}
				col := colIndex(c.Ref, len(cur))
				if col >= maxCols {
					continue
				}
				for len(cur) < col {
					cur = append(cur, "")
				}
				v := c.Value
				switch c.Type {
				case "s":
					i, err := strconv.Atoi(c.Value)
					if err != nil || i < 0 || i >= len(shared) {
						return nil, httpx.Unprocessable("import.xlsx", "Teks sel Excel tidak ditemukan.")
					}
					v = shared[i]
				case "inlineStr":
					v = c.Inline.Text
					for _, r := range c.Inline.Runs {
						v += r.Text
					}
				case "b":
					if v == "1" {
						v = "TRUE"
					} else {
						v = "FALSE"
					}
				case "", "n":
					v = numeric(v)
				}
				cur = append(cur, v)
			}
		case xml.EndElement:
			if t.Name.Local == "row" {
				rows = append(rows, cur)
				if len(rows) > MaxRows+20 {
					return rows, nil
				}
			}
		}
	}
	return rows, nil
}

// firstSheet mencari berkas lembar pertama lewat workbook.xml dan relasinya.
func firstSheet(files map[string]*zip.File) (string, error) {
	var wb struct {
		Sheets []struct {
			RID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := decode(files["xl/workbook.xml"], &wb); err != nil || len(wb.Sheets) == 0 {
		return "", errFormat
	}
	var rels struct {
		Rels []xlsxRel `xml:"Relationship"`
	}
	if err := decode(files["xl/_rels/workbook.xml.rels"], &rels); err != nil {
		return "", errFormat
	}
	for _, r := range rels.Rels {
		if r.ID == wb.Sheets[0].RID {
			t := strings.TrimPrefix(r.Target, "/")
			if !strings.HasPrefix(t, "xl/") {
				t = path.Join("xl", t)
			}
			return t, nil
		}
	}
	return "", errFormat
}

func sharedStrings(f *zip.File) ([]string, error) {
	if f == nil {
		return nil, nil
	}
	var sst struct {
		Items []struct {
			Text string `xml:"t"`
			Runs []struct {
				Text string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := decode(f, &sst); err != nil {
		return nil, httpx.Unprocessable("import.xlsx", "Teks Excel tidak terbaca.")
	}
	out := make([]string, len(sst.Items))
	for i, it := range sst.Items {
		s := it.Text
		for _, r := range it.Runs {
			s += r.Text
		}
		out[i] = s
	}
	return out, nil
}

func decode(f *zip.File, v any) error {
	if f == nil {
		return errFormat
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return xml.NewDecoder(io.LimitReader(rc, maxUnzipped)).Decode(v)
}

// colIndex mengubah referensi sel (mis. "C12") menjadi indeks kolom berbasis 0; tanpa
// referensi, sel dianggap berada di kolom berikutnya.
func colIndex(ref string, next int) int {
	if ref == "" {
		return next
	}
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
	}
	if n == 0 {
		return next
	}
	return n - 1
}

// numeric menulis angka mentah Excel dengan koma desimal, supaya "1.125" (satu koma sekian)
// tidak tertukar dengan penulisan ribuan Indonesia saat dibaca Amount.
func numeric(v string) string {
	if strings.ContainsAny(v, "eE") {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			v = strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	return strings.Replace(v, ".", ",", 1)
}

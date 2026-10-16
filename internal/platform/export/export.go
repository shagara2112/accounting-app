// Package export menulis laporan berbentuk tabel ke CSV dan XLSX (Excel) tanpa dependensi luar.
// Angka ditulis sebagai angka asli (bukan teks), supaya bisa langsung dijumlah di Excel.
package export

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"lajur/internal/platform/money"
)

// Cell berisi teks atau angka. Angka nil = sel kosong.
type Cell struct {
	Text string
	Num  *money.Amount
}

func Text(s string) Cell          { return Cell{Text: s} }
func Num(a money.Amount) Cell     { return Cell{Num: &a} }
func NumPtr(a *money.Amount) Cell { return Cell{Num: a} }

type Column struct {
	Header  string
	Numeric bool
	Width   int // lebar kolom Excel dalam karakter; 0 = otomatis
}

type Row struct {
	Cells  []Cell
	Bold   bool // subtotal dan total
	Indent int  // kedalaman baris (dipakai sel pertama)
}

// Table adalah satu laporan: judul, keterangan (PT, periode), kolom, dan baris.
type Table struct {
	Title    string
	Subtitle []string
	Columns  []Column
	Rows     []Row
}

// WriteCSV menulis CSV UTF-8 ber-BOM (supaya Excel membaca huruf dengan benar), koma sebagai
// pemisah, dan angka tanpa pemisah ribuan dengan titik desimal.
func WriteCSV(w io.Writer, t Table) error {
	if _, err := w.Write([]byte("\ufeff")); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	header := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = c.Header
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, r := range t.Rows {
		rec := make([]string, len(t.Columns))
		for i := range t.Columns {
			if i >= len(r.Cells) {
				continue
			}
			c := r.Cells[i]
			switch {
			case c.Num != nil:
				rec[i] = plain(*c.Num)
			case i == 0 && r.Indent > 0:
				rec[i] = strings.Repeat("  ", r.Indent) + c.Text
			default:
				rec[i] = c.Text
			}
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// plain menulis nominal tanpa nol di belakang koma: 1250000 atau 1250000.5.
func plain(a money.Amount) string {
	s := a.String()
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// WriteXLSX menulis satu sheet Excel. Baris judul dan keterangan di atas tabel, header tebal
// dan dibekukan, angka berformat ribuan, baris total tebal, dan indentasi untuk akun anak.
func WriteXLSX(w io.Writer, t Table) error {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	files := []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rootRels},
		{"xl/workbook.xml", fmt.Sprintf(workbook, xmlEscape(sheetName(t.Title)))},
		{"xl/_rels/workbook.xml.rels", workbookRels},
		{"xl/styles.xml", styles},
		{"xl/worksheets/sheet1.xml", sheet(t)},
	}
	for _, f := range files {
		fw, err := z.Create(f.name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(fw, f.body); err != nil {
			return err
		}
	}
	if err := z.Close(); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// Gaya sel di styles.xml (indeks cellXfs).
const (
	styleText      = 0
	styleBold      = 1
	styleNum       = 2
	styleNumBold   = 3
	styleTitle     = 4
	styleHeader    = 5
	styleHeaderNum = 6
	styleIndent1   = 7 // teks berindentasi 1–3
	styleFrac      = 10
	styleFracBold  = 11
)

func sheet(t Table) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	headerRow := 2 + len(t.Subtitle) + 1 // judul, keterangan, satu baris kosong, header
	fmt.Fprintf(&b, `<sheetViews><sheetView workbookViewId="0"><pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`, headerRow, headerRow+1)
	b.WriteString(`<cols>`)
	for i, c := range t.Columns {
		width := c.Width
		if width == 0 {
			width = autoWidth(t, i)
		}
		fmt.Fprintf(&b, `<col min="%d" max="%d" width="%d" customWidth="1"/>`, i+1, i+1, width)
	}
	b.WriteString(`</cols><sheetData>`)

	row := 1
	writeText := func(r int, text string, style int) {
		fmt.Fprintf(&b, `<row r="%d"><c r="A%d" t="inlineStr" s="%d"><is><t xml:space="preserve">%s</t></is></c></row>`, r, r, style, xmlEscape(text))
	}
	writeText(row, t.Title, styleTitle)
	row++
	for _, s := range t.Subtitle {
		writeText(row, s, styleText)
		row++
	}
	row++ // baris kosong
	fmt.Fprintf(&b, `<row r="%d">`, row)
	for i, c := range t.Columns {
		style := styleHeader
		if c.Numeric {
			style = styleHeaderNum
		}
		fmt.Fprintf(&b, `<c r="%s%d" t="inlineStr" s="%d"><is><t>%s</t></is></c>`, colName(i), row, style, xmlEscape(c.Header))
	}
	b.WriteString(`</row>`)
	row++
	for _, r := range t.Rows {
		fmt.Fprintf(&b, `<row r="%d">`, row)
		for i := range t.Columns {
			if i >= len(r.Cells) {
				break
			}
			c := r.Cells[i]
			ref := fmt.Sprintf("%s%d", colName(i), row)
			switch {
			case c.Num != nil:
				style := styleNum
				frac := !c.Num.MulFrac(1, 1, 0, money.Down).Equal(*c.Num)
				switch {
				case frac && r.Bold:
					style = styleFracBold
				case frac:
					style = styleFrac
				case r.Bold:
					style = styleNumBold
				}
				fmt.Fprintf(&b, `<c r="%s" s="%d"><v>%s</v></c>`, ref, style, plain(*c.Num))
			case c.Text != "":
				style := styleText
				switch {
				case r.Bold:
					style = styleBold
				case i == 0 && r.Indent > 0:
					style = styleIndent1 + min(r.Indent, 3) - 1
				}
				fmt.Fprintf(&b, `<c r="%s" t="inlineStr" s="%d"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, xmlEscape(c.Text))
			}
		}
		b.WriteString(`</row>`)
		row++
	}
	b.WriteString(`</sheetData><pageSetup orientation="portrait" fitToWidth="1" fitToHeight="0"/></worksheet>`)
	return b.String()
}

func autoWidth(t Table, col int) int {
	w := utf8.RuneCountInString(t.Columns[col].Header)
	for _, r := range t.Rows {
		if col >= len(r.Cells) {
			continue
		}
		c := r.Cells[col]
		n := utf8.RuneCountInString(c.Text) + r.Indent*2
		if c.Num != nil {
			n = len(plain(*c.Num)) + len(plain(*c.Num))/3 + 2
		}
		w = max(w, n)
	}
	return min(max(w+2, 10), 70)
}

func colName(i int) string {
	name := ""
	for i >= 0 {
		name = string(rune('A'+i%26)) + name
		i = i/26 - 1
	}
	return name
}

// sheetName: nama sheet Excel maksimal 31 karakter tanpa karakter khusus.
func sheetName(title string) string {
	s := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) {
			return '-'
		}
		return r
	}, title)
	if s == "" {
		s = "Laporan"
	}
	if r := []rune(s); len(r) > 31 {
		s = string(r[:31])
	}
	return s
}

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			// Karakter kendali tidak sah di XML 1.0.
			if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/></Types>`

const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`

const workbook = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="%s" sheetId="1" r:id="rId1"/></sheets></workbook>`

const workbookRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`

// Format angka 164: ribuan tanpa desimal, negatif dalam kurung seperti di laporan keuangan, nol
// ditulis "-". Angka berdesimal (mis. kuantitas 2,5) memakai format bawaan 4 (#,##0.00).
const styles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="1"><numFmt numFmtId="164" formatCode="#,##0;(#,##0);&quot;-&quot;"/></numFmts><fonts count="3"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="14"/><name val="Calibri"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FFEEF1F6"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="2"><border/><border><bottom style="thin"><color rgb="FFB0B7C3"/></bottom></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="12"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/><xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/><xf numFmtId="164" fontId="1" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1"/><xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/><xf numFmtId="0" fontId="1" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1"/><xf numFmtId="0" fontId="1" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment horizontal="right"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" applyAlignment="1"><alignment indent="1"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" applyAlignment="1"><alignment indent="2"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" applyAlignment="1"><alignment indent="3"/></xf><xf numFmtId="4" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/><xf numFmtId="4" fontId="1" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1"/></cellXfs></styleSheet>`

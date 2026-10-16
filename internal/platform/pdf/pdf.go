// Package pdf adalah penulis PDF kecil untuk dokumen bisnis (faktur, bukti): halaman A4,
// teks Helvetica dan Helvetica-Bold (WinAnsi), garis, kotak berisi warna, dan gambar JPEG.
// Ditulis sendiri supaya Lajur tetap tanpa dependensi luar dan bekerja tanpa internet.
package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // logo boleh PNG; diubah menjadi JPEG saat disisipkan
	"io"
	"strings"
)

// Ukuran A4 dalam point (1/72 inci).
const (
	A4Width  = 595.28
	A4Height = 841.89
)

type Font int

const (
	Regular Font = iota
	Bold
)

// Doc mengumpulkan halaman. Koordinat memakai sistem PDF: (0,0) di kiri bawah.
type Doc struct {
	pages  []*bytes.Buffer
	cur    *bytes.Buffer
	images []img
	Title  string
}

type img struct {
	data          []byte
	width, height int
}

func New() *Doc {
	d := &Doc{}
	d.AddPage()
	return d
}

func (d *Doc) AddPage() {
	d.cur = &bytes.Buffer{}
	d.pages = append(d.pages, d.cur)
}

// winAnsi mengubah teks ke WinAnsiEncoding; karakter di luar Latin-1 menjadi "?".
func winAnsi(s string) []byte {
	var b []byte
	for _, r := range s {
		switch {
		case r == '–':
			b = append(b, 0x96)
		case r == '—':
			b = append(b, 0x97)
		case r == '‘' || r == '’':
			b = append(b, '\'')
		case r == '“' || r == '”':
			b = append(b, '"')
		case r == '•':
			b = append(b, 0x95)
		case r == '€':
			b = append(b, 0x80)
		case r < 256:
			b = append(b, byte(r))
		default:
			b = append(b, '?')
		}
	}
	return b
}

func escape(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		switch c {
		case '(', ')', '\\':
			s.WriteByte('\\')
			s.WriteByte(c)
		case '\n', '\r':
			s.WriteByte(' ')
		default:
			s.WriteByte(c)
		}
	}
	return s.String()
}

// Text menulis teks dengan garis dasar di (x, y).
func (d *Doc) Text(x, y float64, f Font, size float64, s string) {
	fmt.Fprintf(d.cur, "BT /F%d %.2f Tf %.2f %.2f Td (%s) Tj ET\n", f+1, size, x, y, escape(winAnsi(s)))
}

// TextRight menulis teks rata kanan pada x.
func (d *Doc) TextRight(x, y float64, f Font, size float64, s string) {
	d.Text(x-Width(f, size, s), y, f, size, s)
}

// Color mengatur warna isi teks dan bidang (0–1).
func (d *Doc) Color(r, g, b float64) {
	fmt.Fprintf(d.cur, "%.3f %.3f %.3f rg %.3f %.3f %.3f RG\n", r, g, b, r, g, b)
}

func (d *Doc) Line(x1, y1, x2, y2, width float64) {
	fmt.Fprintf(d.cur, "%.2f w %.2f %.2f m %.2f %.2f l S\n", width, x1, y1, x2, y2)
}

// Rect mengisi kotak (x, y = sudut kiri bawah).
func (d *Doc) Rect(x, y, w, h float64) {
	fmt.Fprintf(d.cur, "%.2f %.2f %.2f %.2f re f\n", x, y, w, h)
}

// Image menyisipkan gambar (PNG atau JPEG) selebar w; tingginya mengikuti rasio gambar.
// Bagian transparan diberi latar putih. Mengembalikan tinggi gambar.
func (d *Doc) Image(r io.Reader, x, yTop, w float64) (float64, error) {
	src, _, err := image.Decode(r)
	if err != nil {
		return 0, err
	}
	b := src.Bounds()
	rgb := image.NewRGBA(b)
	draw.Draw(rgb, b, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(rgb, b, src, b.Min, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgb, &jpeg.Options{Quality: 88}); err != nil {
		return 0, err
	}
	d.images = append(d.images, img{data: buf.Bytes(), width: b.Dx(), height: b.Dy()})
	h := w * float64(b.Dy()) / float64(b.Dx())
	fmt.Fprintf(d.cur, "q %.2f 0 0 %.2f %.2f %.2f cm /Im%d Do Q\n", w, h, x, yTop-h, len(d.images))
	return h, nil
}

// Wrap memecah teks menjadi baris yang muat dalam lebar w.
func Wrap(f Font, size, w float64, s string) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if Width(f, size, line+" "+word) <= w {
				line += " " + word
			} else {
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

// Width menghitung lebar teks dalam point memakai metrik standar Helvetica.
func Width(f Font, size float64, s string) float64 {
	widths := helvetica
	if f == Bold {
		widths = helveticaBold
	}
	total := 0
	for _, c := range winAnsi(s) {
		w := 556
		if int(c) >= 32 && int(c) < 32+len(widths) {
			w = widths[c-32]
		}
		total += w
	}
	return float64(total) * size / 1000
}

// Write menyusun berkas PDF lengkap.
func (d *Doc) Write(w io.Writer) error {
	var buf bytes.Buffer
	var offsets []int
	obj := func(body string) int {
		offsets = append(offsets, buf.Len())
		n := len(offsets)
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
		return n
	}
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	catalog := obj("<< /Type /Catalog /Pages 2 0 R >>")
	_ = catalog
	pagesIdx := len(offsets) // objek 2 diisi setelah halaman diketahui
	offsets = append(offsets, 0)
	fontReg := obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	fontBold := obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	var xobjects strings.Builder
	for i, im := range d.images {
		offsets = append(offsets, buf.Len())
		n := len(offsets)
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
			n, im.width, im.height, len(im.data))
		buf.Write(im.data)
		buf.WriteString("\nendstream\nendobj\n")
		fmt.Fprintf(&xobjects, " /Im%d %d 0 R", i+1, n)
	}
	resources := fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R >> /XObject <<%s >> >>", fontReg, fontBold, xobjects.String())
	var kids []string
	for _, p := range d.pages {
		offsets = append(offsets, buf.Len())
		content := len(offsets)
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Length %d >>\nstream\n", content, p.Len())
		buf.Write(p.Bytes())
		buf.WriteString("\nendstream\nendobj\n")
		page := obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources %s /Contents %d 0 R >>", A4Width, A4Height, resources, content))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	offsets[pagesIdx] = buf.Len()
	fmt.Fprintf(&buf, "2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, " "), len(kids))
	info := obj(fmt.Sprintf("<< /Producer (Lajur) /Title (%s) >>", escape(winAnsi(d.Title))))

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, info, xref)
	_, err := w.Write(buf.Bytes())
	return err
}

// Metrik lebar karakter 32–255 (WinAnsi) dari Adobe Core14 AFM, dalam 1/1000 em.
var helvetica = []int{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
	1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
	333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
	556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584, 350,
	556, 350, 222, 556, 333, 1000, 556, 556, 333, 1000, 667, 333, 1000, 350, 611, 350,
	350, 222, 222, 333, 333, 350, 556, 1000, 333, 1000, 500, 333, 944, 350, 500, 667,
	278, 333, 556, 556, 556, 556, 260, 556, 333, 737, 370, 556, 584, 333, 737, 333,
	400, 584, 333, 333, 333, 556, 537, 278, 333, 333, 365, 556, 834, 834, 834, 611,
	667, 667, 667, 667, 667, 667, 1000, 722, 667, 667, 667, 667, 278, 278, 278, 278,
	722, 722, 778, 778, 778, 778, 778, 584, 778, 722, 722, 722, 722, 667, 667, 611,
	556, 556, 556, 556, 556, 556, 889, 500, 556, 556, 556, 556, 278, 278, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 584, 611, 556, 556, 556, 556, 500, 556, 500,
}

var helveticaBold = []int{
	278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 333, 333, 584, 584, 584, 611,
	975, 722, 722, 722, 722, 667, 611, 778, 722, 278, 556, 722, 611, 833, 722, 778,
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 333, 278, 333, 584, 556,
	333, 556, 611, 556, 611, 556, 333, 611, 611, 278, 278, 556, 278, 889, 611, 611,
	611, 611, 389, 556, 333, 611, 556, 778, 556, 556, 500, 389, 280, 389, 584, 350,
	556, 350, 278, 556, 500, 1000, 556, 556, 333, 1000, 667, 333, 1000, 350, 611, 350,
	350, 278, 278, 500, 500, 350, 556, 1000, 333, 1000, 556, 333, 944, 350, 500, 667,
	278, 333, 556, 556, 556, 556, 280, 556, 333, 737, 370, 556, 584, 333, 737, 333,
	400, 584, 333, 333, 333, 611, 556, 278, 333, 333, 365, 556, 834, 834, 834, 611,
	722, 722, 722, 722, 722, 722, 1000, 722, 667, 667, 667, 667, 278, 278, 278, 278,
	722, 722, 778, 778, 778, 778, 778, 584, 778, 722, 722, 722, 722, 667, 667, 611,
	556, 556, 556, 556, 556, 556, 889, 556, 556, 556, 556, 556, 278, 278, 278, 278,
	611, 611, 611, 611, 611, 611, 611, 584, 611, 611, 611, 611, 611, 556, 611, 556,
}

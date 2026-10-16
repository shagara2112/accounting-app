package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestWriteProducesValidXref(t *testing.T) {
	d := New()
	d.Title = "Faktur (uji)"
	d.Text(40, 800, Bold, 16, "FAKTUR PENJUALAN — PT Contoh (Tbk) \\ é")
	d.Color(0.9, 0.9, 0.9)
	d.Rect(40, 700, 200, 20)
	d.Line(40, 690, 555, 690, 0.5)
	logo := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		for y := range 20 {
			logo.Set(x, y, color.NRGBA{R: 30, G: 80, B: 200, A: uint8(x * 6)})
		}
	}
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, logo)
	h, err := d.Image(&pngBuf, 450, 820, 100)
	if err != nil || h != 50 {
		t.Fatalf("gambar: h=%v err=%v", h, err)
	}
	d.AddPage()
	d.Text(40, 800, Regular, 10, "Halaman 2")
	var out bytes.Buffer
	if err := d.Write(&out); err != nil {
		t.Fatal(err)
	}
	raw := out.Bytes()
	if !bytes.HasPrefix(raw, []byte("%PDF-1.4")) || !bytes.HasSuffix(raw, []byte("%%EOF\n")) {
		t.Fatal("kepala atau ekor PDF salah")
	}
	m := regexp.MustCompile(`startxref\n(\d+)\n`).FindSubmatch(raw)
	start, _ := strconv.Atoi(string(m[1]))
	if !bytes.HasPrefix(raw[start:], []byte("xref\n")) {
		t.Fatal("startxref tidak menunjuk ke tabel xref")
	}
	lines := strings.Split(string(raw[start:]), "\n")
	var count int
	fmt.Sscanf(lines[1], "0 %d", &count)
	for i := 1; i < count; i++ {
		off, _ := strconv.Atoi(lines[2+i][:10])
		want := fmt.Sprintf("%d 0 obj", i)
		if !bytes.HasPrefix(raw[off:], []byte(want)) {
			t.Fatalf("xref objek %d menunjuk ke %q", i, raw[off:off+12])
		}
	}
	if !bytes.Contains(raw, []byte("(FAKTUR PENJUALAN \x97 PT Contoh \\(Tbk\\) \\\\ \xe9)")) {
		t.Fatal("teks tidak di-escape atau dienkode WinAnsi dengan benar")
	}
	if !bytes.Contains(raw, []byte("/Count 2")) || !bytes.Contains(raw, []byte("/Filter /DCTDecode")) {
		t.Fatal("halaman atau gambar hilang")
	}
}

func TestWidthAndWrap(t *testing.T) {
	// H 722 + e 556 + l 222 + l 222 + o 556 = 2278/1000 em.
	if w := Width(Regular, 10, "Hello"); w != 22.78 {
		t.Fatalf("lebar Hello 10pt = %v", w)
	}
	lines := Wrap(Regular, 10, 60, "Jasa konsultasi pajak bulanan untuk masa Oktober")
	for _, l := range lines {
		if Width(Regular, 10, l) > 60 && strings.Contains(l, " ") {
			t.Fatalf("baris melebihi lebar: %q", l)
		}
	}
	if len(lines) < 3 {
		t.Fatalf("pemotongan baris = %v", lines)
	}
}

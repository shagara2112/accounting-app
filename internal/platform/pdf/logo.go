package pdf

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
)

// NormalizeLogo membaca logo PNG atau JPEG, memperkecilnya sampai lebar paling besar 800 px,
// memberi latar putih pada bagian transparan, lalu menyimpannya sebagai JPEG.
func NormalizeLogo(r io.Reader) ([]byte, error) {
	src, _, err := image.Decode(io.LimitReader(r, 8<<20))
	if err != nil {
		return nil, errors.New("logo harus gambar PNG atau JPEG")
	}
	b := src.Bounds()
	if b.Dx() < 16 || b.Dy() < 16 || b.Dx() > 8000 || b.Dy() > 8000 {
		return nil, errors.New("ukuran logo harus 16–8000 piksel")
	}
	w, h := b.Dx(), b.Dy()
	if w > 800 {
		h, w = h*800/w, 800
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	// Perkecil dengan rata-rata kotak sederhana supaya tepi huruf logo tetap halus.
	for y := range h {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+(y+1)*b.Dy()/h
		for x := range w {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+(x+1)*b.Dx()/w
			var rs, gs, bs, n uint32
			for yy := y0; yy < max(y1, y0+1); yy++ {
				for xx := x0; xx < max(x1, x0+1); xx++ {
					c := color.NRGBAModel.Convert(src.At(xx, yy)).(color.NRGBA)
					a := uint32(c.A)
					rs += (uint32(c.R)*a + 255*(255-a)) / 255
					gs += (uint32(c.G)*a + 255*(255-a)) / 255
					bs += (uint32(c.B)*a + 255*(255-a)) / 255
					n++
				}
			}
			dst.Set(x, y, color.RGBA{R: uint8(rs / n), G: uint8(gs / n), B: uint8(bs / n), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	if buf.Len() > 512<<10 {
		return nil, errors.New("logo terlalu rumit; gunakan gambar yang lebih sederhana")
	}
	return buf.Bytes(), nil
}

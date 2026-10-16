// Package docs menghasilkan dokumen untuk pelanggan: PDF faktur bertemplate logo dan rekening
// perusahaan, serta pengirimannya lewat email (AR-03).
package docs

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/modules/cash"
	"lajur/internal/modules/core"
	"lajur/internal/modules/sales"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/mail"
	"lajur/internal/platform/money"
	"lajur/internal/platform/pdf"
)

type Deps struct {
	Core  core.Service
	Sales sales.Service
	Cash  cash.Service
	Mail  *mail.Service
}

type Service struct{ Deps }

func New(d Deps) *Service { return &Service{d} }

var titles = map[string]string{
	sales.KindRegular:   "FAKTUR PENJUALAN",
	sales.KindAdvance:   "FAKTUR UANG MUKA",
	sales.KindProgress:  "FAKTUR TERMIN",
	sales.KindRetention: "FAKTUR RETENSI",
}

var months = []string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}

func longDate(d civil.Date) string {
	return fmt.Sprintf("%d %s %d", d.Day(), months[d.Month()-1], d.Year())
}

// num menulis nominal tanpa "Rp", mis. 1.250.000 atau 1.250.000,5.
func num(a money.Amount) string {
	return strings.TrimPrefix(strings.Replace(a.IDR(), "Rp ", "", 1), "-")
}

func signed(a money.Amount) string {
	if a.Sign() < 0 {
		return "(" + num(a) + ")"
	}
	return num(a)
}

// qty menulis kuantitas tanpa nol berlebih, mis. 2 atau 1,5.
func qty(a money.Amount) string {
	whole, frac, _ := strings.Cut(a.String(), ".")
	frac = strings.TrimRight(frac, "0")
	if frac == "" {
		return whole
	}
	return whole + "," + frac
}

// fileName: nomor faktur dengan garis miring diganti, mis. FJ-2026-10-0001.pdf.
func fileName(inv sales.InvoiceDetail) string {
	n := "draf-faktur"
	if inv.Number != nil {
		n = *inv.Number
	}
	return strings.NewReplacer("/", "-", " ", "-").Replace(n) + ".pdf"
}

// InvoicePDF menyusun PDF faktur penjualan.
func (s *Service) InvoicePDF(ctx context.Context, companyID, invoiceID uuid.UUID) ([]byte, string, error) {
	inv, err := s.Sales.Invoice(ctx, companyID, invoiceID)
	if err != nil {
		return nil, "", err
	}
	co, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return nil, "", err
	}
	cust, err := s.Core.Contact(ctx, companyID, inv.CustomerID)
	if err != nil {
		return nil, "", err
	}
	logo, err := s.Core.Logo(ctx, companyID)
	if err != nil {
		return nil, "", err
	}
	accounts, err := s.Cash.Accounts(ctx, companyID)
	if err != nil {
		return nil, "", err
	}
	var banks []string
	for _, a := range accounts {
		if a.Kind == "bank" && a.Active && a.AccountNumber != "" {
			banks = append(banks, fmt.Sprintf("%s %s a.n. %s", a.BankName, a.AccountNumber, co.Name))
		}
	}

	d := pdf.New()
	title := titles[inv.Kind]
	if title == "" {
		title = titles[sales.KindRegular]
	}
	number := "(draf, belum bernomor)"
	if inv.Number != nil {
		number = *inv.Number
	}
	d.Title = title + " " + number
	r := renderer{d: d, left: 40, right: pdf.A4Width - 40}
	r.header(co, logo, title, number, inv)
	r.customer(cust, inv)
	r.lines(inv)
	r.totals(inv)
	r.footerInfo(inv, banks)
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), fileName(inv), nil
}

type renderer struct {
	d           *pdf.Doc
	left, right float64
	y           float64
	page        int
}

func (r *renderer) gray(v float64) { r.d.Color(v, v, v) }

func (r *renderer) header(co core.Company, logo []byte, title, number string, inv sales.InvoiceDetail) {
	d := r.d
	r.page = 1
	top := pdf.A4Height - 40
	x := r.left
	if logo != nil {
		// Logo maksimal 120 × 55 pt, mengikuti rasio gambarnya.
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(logo)); err == nil && cfg.Height > 0 {
			w := min(120, 55*float64(cfg.Width)/float64(cfg.Height))
			if h, err := d.Image(bytes.NewReader(logo), r.left, top, w); err == nil {
				top -= h + 8
			}
		}
	}
	r.gray(0.1)
	d.Text(x, top-12, pdf.Bold, 13, co.Name)
	y := top - 26
	for _, l := range pdf.Wrap(pdf.Regular, 8.5, 260, co.Address) {
		if l == "" {
			continue
		}
		d.Text(x, y, pdf.Regular, 8.5, l)
		y -= 11
	}
	if co.NPWP != "" {
		d.Text(x, y, pdf.Regular, 8.5, "NPWP "+co.NPWP)
		y -= 11
	}

	// Blok judul di kanan.
	ty := pdf.A4Height - 52
	d.Color(0.15, 0.25, 0.65)
	d.TextRight(r.right, ty, pdf.Bold, 16, title)
	r.gray(0.1)
	rows := [][2]string{{"Nomor", number}, {"Tanggal", longDate(inv.Date)}, {"Jatuh tempo", longDate(inv.DueDate)}}
	if inv.ProgressPct != nil {
		rows = append(rows, [2]string{"Progres", qty(*inv.ProgressPct) + "%"})
	}
	ty -= 20
	for _, row := range rows {
		d.TextRight(r.right-120, ty, pdf.Regular, 9, row[0])
		d.TextRight(r.right, ty, pdf.Bold, 9, row[1])
		ty -= 13
	}
	if inv.Status == "draft" || inv.Status == "void" {
		label := map[string]string{"draft": "DRAF", "void": "VOID"}[inv.Status]
		d.Color(0.85, 0.2, 0.2)
		d.TextRight(r.right, ty-6, pdf.Bold, 20, label)
	}
	r.y = min(y, ty) - 18
	r.gray(0.8)
	d.Line(r.left, r.y, r.right, r.y, 0.6)
	r.y -= 18
}

func (r *renderer) customer(c core.Contact, inv sales.InvoiceDetail) {
	d := r.d
	r.gray(0.45)
	d.Text(r.left, r.y, pdf.Bold, 8, "KEPADA")
	r.y -= 13
	r.gray(0.1)
	d.Text(r.left, r.y, pdf.Bold, 10.5, c.Name)
	r.y -= 12
	for _, l := range pdf.Wrap(pdf.Regular, 9, 300, c.Address) {
		if l == "" {
			continue
		}
		d.Text(r.left, r.y, pdf.Regular, 9, l)
		r.y -= 11
	}
	switch {
	case c.NPWP != "":
		d.Text(r.left, r.y, pdf.Regular, 9, "NPWP "+c.NPWP)
		r.y -= 11
	case c.NIK != "":
		d.Text(r.left, r.y, pdf.Regular, 9, "NIK "+c.NIK)
		r.y -= 11
	}
	r.y -= 12
}

// Kolom tabel baris: uraian, kuantitas, harga, diskon, jumlah (rata kanan).
var cols = struct{ desc, qty, price, disc, amount float64 }{24, 300, 390, 460, 515.28}

func (r *renderer) tableHeader() {
	d := r.d
	r.d.Color(0.93, 0.94, 0.97)
	d.Rect(r.left, r.y-6, r.right-r.left, 20)
	r.gray(0.25)
	d.Text(r.left+6, r.y, pdf.Bold, 8.5, "No")
	d.Text(r.left+cols.desc, r.y, pdf.Bold, 8.5, "Uraian")
	d.TextRight(r.left+cols.qty, r.y, pdf.Bold, 8.5, "Kuantitas")
	d.TextRight(r.left+cols.price, r.y, pdf.Bold, 8.5, "Harga satuan")
	d.TextRight(r.left+cols.disc, r.y, pdf.Bold, 8.5, "Diskon")
	d.TextRight(r.left+cols.amount, r.y, pdf.Bold, 8.5, "Jumlah")
	r.y -= 22
}

func (r *renderer) newPage() {
	r.d.AddPage()
	r.page++
	r.y = pdf.A4Height - 50
}

func (r *renderer) lines(inv sales.InvoiceDetail) {
	d := r.d
	r.tableHeader()
	for i, l := range inv.Lines {
		desc := pdf.Wrap(pdf.Regular, 9, cols.qty-cols.desc-60, l.Description)
		need := float64(len(desc))*11 + 6
		if r.y-need < 170 {
			r.newPage()
			r.tableHeader()
		}
		r.gray(0.1)
		d.Text(r.left+6, r.y, pdf.Regular, 9, fmt.Sprint(i+1))
		for j, t := range desc {
			d.Text(r.left+cols.desc, r.y-float64(j)*11, pdf.Regular, 9, t)
		}
		d.TextRight(r.left+cols.qty, r.y, pdf.Regular, 9, qty(l.Qty))
		d.TextRight(r.left+cols.price, r.y, pdf.Regular, 9, num(l.UnitPrice))
		disc := "—"
		if !l.Discount.IsZero() {
			disc = num(l.Discount)
		}
		d.TextRight(r.left+cols.disc, r.y, pdf.Regular, 9, disc)
		d.TextRight(r.left+cols.amount, r.y, pdf.Regular, 9, num(l.Net))
		r.y -= need
		r.gray(0.9)
		d.Line(r.left, r.y+8, r.right, r.y+8, 0.4)
	}
	r.y -= 6
}

func (r *renderer) totals(inv sales.InvoiceDetail) {
	d := r.d
	t := inv.Totals
	rows := [][3]string{{"Subtotal", signed(t.Subtotal), ""}}
	if !t.Discount.IsZero() {
		rows = append(rows, [3]string{"Diskon", "(" + num(t.Discount) + ")", ""})
	}
	rows = append(rows, [3]string{"Dasar pengenaan pajak", num(t.DPP), ""})
	if !t.OtherDPP.IsZero() && !t.OtherDPP.Equal(t.DPP) {
		rows = append(rows, [3]string{"DPP nilai lain", num(t.OtherDPP), ""})
	}
	rows = append(rows, [3]string{"PPN", num(t.VAT), ""}, [3]string{"Total", "Rp " + num(t.Total), "b"})
	if !inv.Retention.IsZero() {
		rows = append(rows, [3]string{"Retensi ditahan", "(" + num(inv.Retention) + ")", ""}, [3]string{"Jumlah ditagih", "Rp " + num(inv.Receivable), "b"})
	}
	if !inv.Paid.IsZero() {
		rows = append(rows, [3]string{"Sudah dibayar", "(" + num(inv.Paid) + ")", ""})
	}
	if !inv.Credited.IsZero() {
		rows = append(rows, [3]string{"Nota kredit/retur", "(" + num(inv.Credited) + ")", ""})
	}
	if !inv.Paid.IsZero() || !inv.Credited.IsZero() {
		rows = append(rows, [3]string{"Sisa tagihan", "Rp " + num(inv.Outstanding), "b"})
	}
	if r.y-float64(len(rows))*15 < 120 {
		r.newPage()
	}
	for _, row := range rows {
		f, size := pdf.Regular, 9.0
		if row[2] == "b" {
			f, size = pdf.Bold, 10.5
			r.gray(0.85)
			d.Line(r.right-210, r.y+12, r.right, r.y+12, 0.5)
		}
		r.gray(0.1)
		d.TextRight(r.right-110, r.y, f, size, row[0])
		d.TextRight(r.right, r.y, f, size, row[1])
		r.y -= 15
	}
	r.y -= 10
}

func (r *renderer) footerInfo(inv sales.InvoiceDetail, banks []string) {
	d := r.d
	block := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		if r.y-float64(len(lines))*11-20 < 60 {
			r.newPage()
		}
		r.gray(0.45)
		d.Text(r.left, r.y, pdf.Bold, 8, title)
		r.y -= 12
		r.gray(0.15)
		for _, l := range lines {
			d.Text(r.left, r.y, pdf.Regular, 9, l)
			r.y -= 11
		}
		r.y -= 8
	}
	if strings.TrimSpace(inv.Memo) != "" {
		block("CATATAN", pdf.Wrap(pdf.Regular, 9, r.right-r.left, inv.Memo))
	}
	if len(banks) > 0 {
		block("PEMBAYARAN KE", banks)
	}
	r.gray(0.55)
	d.Text(r.left, 30, pdf.Regular, 7.5, "Dokumen ini dibuat dengan Lajur. Mohon cantumkan nomor faktur pada keterangan pembayaran.")
}

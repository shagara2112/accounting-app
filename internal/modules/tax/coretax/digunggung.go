package coretax

import (
	"context"

	"github.com/google/uuid"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/export"
	"lajur/internal/platform/money"
)

// Rekap PPN digunggung (TAX-12): penjualan eceran ke konsumen akhir tidak memakai faktur pajak
// per pembeli; PPN-nya dilaporkan sebagai jumlah per masa di SPT Masa PPN. Sumbernya rekap
// penjualan harian retail (IND-07) yang diposting.

type DigunggungRow struct {
	ID       uuid.UUID    `json:"id"`
	Date     civil.Date   `json:"date"`
	Number   string       `json:"number"`
	Store    string       `json:"store"`
	DPP      money.Amount `json:"dpp"`
	OtherDPP money.Amount `json:"otherDpp"`
	VAT      money.Amount `json:"vat"`
	Total    money.Amount `json:"total"`
}

type Digunggung struct {
	Month   string          `json:"month"`
	Company string          `json:"company"`
	PKP     bool            `json:"pkp"`
	Rows    []DigunggungRow `json:"rows"`
	Totals  DigunggungRow   `json:"totals"`
}

func (s *service) Digunggung(ctx context.Context, companyID uuid.UUID, month civil.Date) (Digunggung, error) {
	company, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return Digunggung{}, err
	}
	out := Digunggung{Month: month.String()[:7], Company: company.Name, PKP: company.PKP, Rows: []DigunggungRow{}}
	list, err := s.Sales.DailySales(ctx, companyID, month.MonthStart(), month.MonthEnd())
	if err != nil {
		return Digunggung{}, err
	}
	// Daftar rekap urut tanggal turun; laporan pajak urut tanggal naik.
	for i := len(list) - 1; i >= 0; i-- {
		d := list[i]
		if d.Status != "posted" {
			continue
		}
		out.Rows = append(out.Rows, DigunggungRow{ID: d.ID, Date: d.Date, Number: d.Number, Store: d.Store, DPP: d.DPP, OtherDPP: d.OtherDPP, VAT: d.VAT, Total: d.Total})
		t := &out.Totals
		t.DPP, t.OtherDPP, t.VAT, t.Total = t.DPP.Add(d.DPP), t.OtherDPP.Add(d.OtherDPP), t.VAT.Add(d.VAT), t.Total.Add(d.Total)
	}
	return out, nil
}

// DigunggungTable menyusun rekap untuk diekspor ke Excel atau CSV.
func DigunggungTable(d Digunggung, month civil.Date) export.Table {
	t := export.Table{
		Title: "Rekap PPN digunggung " + month.MonthLabel(),
		Subtitle: []string{d.Company, "Penjualan eceran per hari dan toko dari rekap penjualan harian (TAX-12).",
			"Format v1: cocokkan dengan converter Coretax yang berlaku sebelum diunggah."},
		Columns: []export.Column{
			{Header: "Tanggal"}, {Header: "Nomor rekap"}, {Header: "Toko"},
			{Header: "DPP", Numeric: true}, {Header: "DPP nilai lain", Numeric: true}, {Header: "PPN", Numeric: true}, {Header: "Total", Numeric: true},
		},
	}
	for _, r := range d.Rows {
		t.Rows = append(t.Rows, export.Row{Cells: []export.Cell{
			export.Text(r.Date.String()), export.Text(r.Number), export.Text(r.Store),
			export.Num(r.DPP), export.Num(r.OtherDPP), export.Num(r.VAT), export.Num(r.Total),
		}})
	}
	t.Rows = append(t.Rows, export.Row{Bold: true, Cells: []export.Cell{
		export.Text("Jumlah"), export.Text(""), export.Text(""),
		export.Num(d.Totals.DPP), export.Num(d.Totals.OtherDPP), export.Num(d.Totals.VAT), export.Num(d.Totals.Total),
	}})
	return t
}

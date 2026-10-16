package report

import (
	"strings"

	"lajur/internal/modules/trade"
	"lajur/internal/platform/export"
	"lajur/internal/platform/money"
)

// Konversi laporan ke tabel untuk ekspor CSV dan Excel. Angka ditampilkan sama dengan di layar.

func (st Statement) Table(period string) export.Table {
	t := export.Table{Title: st.Title, Subtitle: []string{st.Company, period}, Columns: []export.Column{{Header: "Pos", Width: 56}}}
	for _, c := range st.Columns {
		t.Columns = append(t.Columns, export.Column{Header: c.Label, Numeric: true})
	}
	for _, l := range st.Lines {
		row := export.Row{Indent: l.Depth}
		name := l.Name
		switch l.Kind {
		case LineSection:
			name = strings.ToUpper(name)
			row.Bold = true
		case LineAccount:
			name = l.Code + " " + l.Name
			row.Indent = l.Depth + 1
		case LineGroup:
			if l.Code != "" {
				name = l.Code + " " + l.Name
			}
			row.Indent = l.Depth + 1
		case LineSubtotal:
			row.Indent = l.Depth + 1
			row.Bold = true
		case LineComputed:
			row.Indent = 1
		case LineTotal, LineResult:
			row.Bold = true
		}
		row.Cells = append(row.Cells, export.Text(name))
		for _, v := range l.Values {
			row.Cells = append(row.Cells, export.Num(v))
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func (tb TrialBalance) Table() export.Table {
	t := export.Table{
		Title:    "Neraca saldo",
		Subtitle: []string{tb.Company, "Per " + tb.Date.Indonesian() + " · akun laba rugi sejak " + tb.FiscalStart.Indonesian()},
		Columns:  []export.Column{{Header: "Kode", Width: 14}, {Header: "Akun", Width: 48}, {Header: "Debit", Numeric: true}, {Header: "Kredit", Numeric: true}},
	}
	cell := func(a money.Amount) export.Cell {
		if a.IsZero() {
			return export.Text("")
		}
		return export.Num(a)
	}
	for _, r := range tb.Rows {
		t.Rows = append(t.Rows, export.Row{Cells: []export.Cell{export.Text(r.Code), export.Text(r.Name), cell(r.Debit), cell(r.Credit)}})
	}
	t.Rows = append(t.Rows, export.Row{Bold: true, Cells: []export.Cell{export.Text(""), export.Text("Jumlah"), export.Num(tb.TotalDebit), export.Num(tb.TotalCredit)}})
	return t
}

func (s Summary) Table() export.Table {
	t := export.Table{Title: s.Title, Subtitle: []string{s.Company, s.From.Indonesian() + " – " + s.To.Indonesian()}}
	row := func(r trade.SummaryRow, bold bool) export.Row {
		var cells []export.Cell
		switch s.Kind {
		case ByProduct:
			qty := export.Text("")
			if r.Qty != nil {
				qty = export.NumPtr(r.Qty)
			}
			cells = []export.Cell{export.Text(r.Code), export.Text(r.Name), export.Text(r.Unit), qty, export.Num(money.FromInt(int64(r.Documents))), export.Num(r.DPP), export.Num(r.VAT)}
		case BySupplier:
			cells = []export.Cell{export.Text(r.Name), export.Num(money.FromInt(int64(r.Documents))), export.Num(r.DPP), export.Num(r.VAT), export.Num(r.Withholding), export.Num(r.Total)}
		default:
			cells = []export.Cell{export.Text(r.Name), export.Num(money.FromInt(int64(r.Documents))), export.Num(r.DPP), export.Num(r.VAT), export.Num(r.Total)}
		}
		return export.Row{Cells: cells, Bold: bold}
	}
	num := func(h string) export.Column { return export.Column{Header: h, Numeric: true} }
	switch s.Kind {
	case ByProduct:
		t.Columns = []export.Column{{Header: "Kode"}, {Header: "Produk", Width: 40}, {Header: "Satuan"}, num("Kuantitas"), num("Faktur"), num("DPP"), num("PPN")}
	case BySupplier:
		t.Columns = []export.Column{{Header: "Pemasok", Width: 40}, num("Tagihan"), num("DPP"), num("PPN"), num("PPh dipotong"), num("Total")}
	default:
		t.Columns = []export.Column{{Header: "Pelanggan", Width: 40}, num("Faktur"), num("DPP"), num("PPN"), num("Total")}
	}
	for _, r := range s.Rows {
		t.Rows = append(t.Rows, row(r, false))
	}
	total := row(s.Total, true)
	if s.Kind == ByProduct {
		total.Cells[1] = export.Text("Jumlah")
	}
	t.Rows = append(t.Rows, total)
	return t
}

func (c ContactCard) Table() export.Table {
	title := "Kartu piutang pelanggan"
	if c.Side == "supplier" {
		title = "Kartu utang pemasok"
	}
	t := export.Table{
		Title:    title,
		Subtitle: []string{c.Company, c.ContactName, c.From.Indonesian() + " – " + c.To.Indonesian()},
		Columns: []export.Column{
			{Header: "Tanggal", Width: 12}, {Header: "Dokumen"}, {Header: "Nomor", Width: 20}, {Header: "Keterangan", Width: 40},
			{Header: "Debit", Numeric: true}, {Header: "Kredit", Numeric: true}, {Header: "Saldo", Numeric: true},
		},
	}
	cell := func(a money.Amount) export.Cell {
		if a.IsZero() {
			return export.Text("")
		}
		return export.Num(a)
	}
	t.Rows = append(t.Rows, export.Row{Cells: []export.Cell{export.Text(c.From.String()), export.Text(""), export.Text(""), export.Text("Saldo awal"), export.Text(""), export.Text(""), export.Num(c.Opening)}})
	for _, l := range c.Lines {
		t.Rows = append(t.Rows, export.Row{Cells: []export.Cell{
			export.Text(l.Date.String()), export.Text(l.Kind), export.Text(l.Number), export.Text(l.Memo), cell(l.Debit), cell(l.Credit), export.Num(l.Balance),
		}})
	}
	t.Rows = append(t.Rows, export.Row{Bold: true, Cells: []export.Cell{
		export.Text(c.To.String()), export.Text(""), export.Text(""), export.Text("Saldo akhir"), export.Num(c.TotalDebit), export.Num(c.TotalCredit), export.Num(c.Closing),
	}})
	return t
}

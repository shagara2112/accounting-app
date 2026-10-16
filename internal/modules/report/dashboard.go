package report

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"lajur/internal/modules/cash"
	"lajur/internal/modules/report/internal/store"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Jenis ringkasan dokumen (RPT-05).
const (
	ByCustomer = "customer"
	ByProduct  = "product"
	BySupplier = "supplier"
)

type Summary struct {
	Title   string             `json:"title"`
	Company string             `json:"company"`
	Kind    string             `json:"kind"`
	From    civil.Date         `json:"from"`
	To      civil.Date         `json:"to"`
	Rows    []trade.SummaryRow `json:"rows"`
	Total   trade.SummaryRow   `json:"total"`
}

func (s *service) Summary(ctx context.Context, companyID uuid.UUID, kind string, p Period) (Summary, error) {
	if p.To.Before(p.From) {
		return Summary{}, httpx.BadRequest("report.range", "Tanggal akhir periode sebelum tanggal awal.")
	}
	name, err := s.companyName(ctx, companyID)
	if err != nil {
		return Summary{}, err
	}
	out := Summary{Company: name, Kind: kind, From: p.From, To: p.To}
	switch kind {
	case ByCustomer:
		out.Title = "Penjualan per pelanggan"
		out.Rows, err = s.Sales.ByCustomer(ctx, companyID, p.From, p.To)
	case ByProduct:
		out.Title = "Penjualan per produk"
		out.Rows, err = s.Sales.ByProduct(ctx, companyID, p.From, p.To)
	case BySupplier:
		out.Title = "Pembelian per pemasok"
		out.Rows, err = s.Purchase.BySupplier(ctx, companyID, p.From, p.To)
	default:
		return Summary{}, httpx.BadRequest("report.kind", "Pilih ringkasan per pelanggan, produk, atau pemasok.")
	}
	if err != nil {
		return Summary{}, err
	}
	if out.Rows == nil {
		out.Rows = []trade.SummaryRow{}
	}
	out.Total.Name = "Jumlah"
	for _, r := range out.Rows {
		out.Total.DPP = out.Total.DPP.Add(r.DPP)
		out.Total.VAT = out.Total.VAT.Add(r.VAT)
		out.Total.Withholding = out.Total.Withholding.Add(r.Withholding)
		out.Total.Total = out.Total.Total.Add(r.Total)
		out.Total.Documents += r.Documents
	}
	return out, nil
}

type ContactCard struct {
	trade.Card
	Company string `json:"company"`
	Side    string `json:"side"` // customer atau supplier
}

func (s *service) ContactCard(ctx context.Context, companyID uuid.UUID, side string, contactID uuid.UUID, p Period) (ContactCard, error) {
	if side != "customer" && side != "supplier" {
		return ContactCard{}, httpx.BadRequest("report.side", "Pilih kartu pelanggan atau pemasok.")
	}
	if p.To.Before(p.From) {
		return ContactCard{}, httpx.BadRequest("report.range", "Tanggal akhir periode sebelum tanggal awal.")
	}
	name, err := s.companyName(ctx, companyID)
	if err != nil {
		return ContactCard{}, err
	}
	card, err := s.cardOf(ctx, companyID, side, contactID, p)
	return ContactCard{Card: card, Company: name, Side: side}, err
}

// Exposure adalah piutang atau utang yang belum lunas per hari ini.
type Exposure struct {
	Total        money.Amount `json:"total"`
	Overdue      money.Amount `json:"overdue"`
	OverdueCount int          `json:"overdueCount"`
	DueSoon      money.Amount `json:"dueSoon"` // jatuh tempo 7 hari ke depan
	DueSoonCount int          `json:"dueSoonCount"`
}

type Result struct {
	Revenue money.Amount `json:"revenue"` // kelas 4 dan 7
	Expense money.Amount `json:"expense"` // kelas 5, 6, 8, dan 9
	Profit  money.Amount `json:"profit"`
}

type MonthResult struct {
	Month string `json:"month"` // yyyy-mm
	Result
}

// Dashboard (RPT-07): saldo kas dan bank, piutang dan utang jatuh tempo, laba bulan berjalan
// dibanding bulan lalu, tren enam bulan, dan faktur yang paling lama lewat jatuh tempo.
type Dashboard struct {
	Today      civil.Date      `json:"today"`
	Cash       []cash.Account  `json:"cash"`
	CashTotal  money.Amount    `json:"cashTotal"`
	CashDrafts int             `json:"cashDrafts"` // draf kas/bank yang belum diposting (belum masuk saldo)
	DraftTotal money.Amount    `json:"draftTotal"`
	Receivable Exposure        `json:"receivable"`
	Payable    Exposure        `json:"payable"`
	ThisMonth  Result          `json:"thisMonth"` // awal bulan s.d. hari ini
	LastMonth  Result          `json:"lastMonth"` // bulan lalu penuh
	Trend      []MonthResult   `json:"trend"`
	Overdue    []sales.Invoice `json:"overdue"`
}

func (s *service) Dashboard(ctx context.Context, companyID uuid.UUID, today civil.Date) (Dashboard, error) {
	out := Dashboard{Today: today, Cash: []cash.Account{}, Overdue: []sales.Invoice{}}
	accounts, err := s.Cash.Accounts(ctx, companyID)
	if err != nil {
		return Dashboard{}, err
	}
	for _, a := range accounts {
		if a.Active {
			out.Cash = append(out.Cash, a)
			out.CashTotal = out.CashTotal.Add(a.Balance)
		}
	}
	if out.CashDrafts, out.DraftTotal, err = s.Cash.DraftSummary(ctx, companyID); err != nil {
		return Dashboard{}, err
	}

	soon := today.AddDays(7)
	expose := func(e *Exposure, outstanding money.Amount, due civil.Date) {
		if outstanding.Sign() <= 0 {
			return
		}
		e.Total = e.Total.Add(outstanding)
		switch {
		case due.Before(today):
			e.Overdue = e.Overdue.Add(outstanding)
			e.OverdueCount++
		case !due.After(soon):
			e.DueSoon = e.DueSoon.Add(outstanding)
			e.DueSoonCount++
		}
	}
	invoices, err := s.Sales.OpenInvoices(ctx, companyID, nil)
	if err != nil {
		return Dashboard{}, err
	}
	for _, inv := range invoices {
		expose(&out.Receivable, inv.Outstanding, inv.DueDate)
		if inv.DueDate.Before(today) {
			out.Overdue = append(out.Overdue, inv)
		}
	}
	sort.SliceStable(out.Overdue, func(i, j int) bool { return out.Overdue[i].DueDate.Before(out.Overdue[j].DueDate) })
	if len(out.Overdue) > 5 {
		out.Overdue = out.Overdue[:5]
	}
	bills, err := s.Purchase.OpenBills(ctx, companyID, nil)
	if err != nil {
		return Dashboard{}, err
	}
	for _, b := range bills {
		expose(&out.Payable, b.Outstanding, b.DueDate)
	}

	c, err := s.chart(ctx, companyID)
	if err != nil {
		return Dashboard{}, err
	}
	result := func(from, to civil.Date) (Result, error) {
		v, err := s.net(ctx, companyID, from, to, true)
		if err != nil {
			return Result{}, err
		}
		r := Result{Revenue: c.sumClass(v, 4, 7).Neg(), Expense: c.sumClass(v, 5, 6, 8, 9)}
		r.Profit = r.Revenue.Sub(r.Expense)
		return r, nil
	}
	month := today.MonthStart()
	if out.ThisMonth, err = result(month, today); err != nil {
		return Dashboard{}, err
	}
	prev := month.AddMonths(-1)
	if out.LastMonth, err = result(prev, prev.MonthEnd()); err != nil {
		return Dashboard{}, err
	}

	first := month.AddMonths(-5)
	rows, err := s.q(ctx).MonthlyResult(ctx, store.MonthlyResultParams{CompanyID: companyID, FromMonth: first, ToMonth: month})
	if err != nil {
		return Dashboard{}, err
	}
	byMonth := map[string]store.MonthlyResultRow{}
	for _, r := range rows {
		byMonth[r.Month.MonthKey()] = r
	}
	for m := first; !m.After(month); m = m.AddMonths(1) {
		r := byMonth[m.MonthKey()]
		out.Trend = append(out.Trend, MonthResult{Month: m.MonthKey(), Result: Result{Revenue: r.Revenue, Expense: r.Expense, Profit: r.Revenue.Sub(r.Expense)}})
	}
	return out, nil
}

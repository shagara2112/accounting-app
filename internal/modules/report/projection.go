package report

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// Proyeksi kebutuhan kas (AP-06): saldo kas dan bank hari ini, lalu per minggu ke depan
// ditambah piutang yang jatuh tempo dan dikurangi utang yang jatuh tempo. Dokumen lewat jatuh
// tempo dianggap diterima/dibayar segera. Saldo proyeksi negatif berarti perlu dana tambahan
// atau penjadwalan ulang pembayaran.

type ProjectionDoc struct {
	ID      uuid.UUID    `json:"id"`
	Number  string       `json:"number"`
	Contact string       `json:"contact"`
	Due     civil.Date   `json:"due"`
	Amount  money.Amount `json:"amount"`
}

type ProjectionBucket struct {
	Label    string          `json:"label"`
	From     *civil.Date     `json:"from,omitempty"`
	To       *civil.Date     `json:"to,omitempty"`
	In       money.Amount    `json:"in"`  // piutang jatuh tempo
	Out      money.Amount    `json:"out"` // utang jatuh tempo
	Balance  money.Amount    `json:"balance"`
	Invoices []ProjectionDoc `json:"invoices"`
	Bills    []ProjectionDoc `json:"bills"`
}

type CashProjection struct {
	AsOf    civil.Date         `json:"asOf"`
	Cash    money.Amount       `json:"cash"`
	Buckets []ProjectionBucket `json:"buckets"`
	// FirstShortfall: periode pertama yang saldo proyeksinya negatif.
	FirstShortfall *string `json:"firstShortfall,omitempty"`
}

func (s *service) CashProjection(ctx context.Context, companyID uuid.UUID, asOf civil.Date, weeks int) (CashProjection, error) {
	if weeks < 1 || weeks > 26 {
		weeks = 8
	}
	out := CashProjection{AsOf: asOf}
	accounts, err := s.Cash.Accounts(ctx, companyID)
	if err != nil {
		return out, err
	}
	for _, a := range accounts {
		if a.Active {
			out.Cash = out.Cash.Add(a.Balance)
		}
	}
	buckets := make([]ProjectionBucket, weeks+2)
	buckets[0].Label = "Lewat jatuh tempo"
	for i := 1; i <= weeks; i++ {
		from, to := asOf.AddDays(7*(i-1)), asOf.AddDays(7*i-1)
		buckets[i] = ProjectionBucket{Label: fmt.Sprintf("Minggu %d", i), From: &from, To: &to}
	}
	later := asOf.AddDays(7 * weeks)
	buckets[weeks+1] = ProjectionBucket{Label: fmt.Sprintf("Setelah %d minggu", weeks), From: &later}
	slot := func(due civil.Date) int {
		if due.Before(asOf) {
			return 0
		}
		return min(due.DaysSince(asOf)/7+1, weeks+1)
	}
	invoices, err := s.Sales.OpenInvoices(ctx, companyID, nil)
	if err != nil {
		return out, err
	}
	for _, inv := range invoices {
		if inv.Outstanding.Sign() <= 0 {
			continue
		}
		b := &buckets[slot(inv.DueDate)]
		b.In = b.In.Add(inv.Outstanding)
		b.Invoices = append(b.Invoices, ProjectionDoc{ID: inv.ID, Number: deref(inv.Number), Contact: inv.CustomerName, Due: inv.DueDate, Amount: inv.Outstanding})
	}
	bills, err := s.Purchase.OpenBills(ctx, companyID, nil)
	if err != nil {
		return out, err
	}
	for _, b := range bills {
		if b.Outstanding.Sign() <= 0 {
			continue
		}
		k := &buckets[slot(b.DueDate)]
		k.Out = k.Out.Add(b.Outstanding)
		k.Bills = append(k.Bills, ProjectionDoc{ID: b.ID, Number: deref(b.Number), Contact: b.SupplierName, Due: b.DueDate, Amount: b.Outstanding})
	}
	bal := out.Cash
	for i := range buckets {
		bal = bal.Add(buckets[i].In).Sub(buckets[i].Out)
		buckets[i].Balance = bal
		if buckets[i].Invoices == nil {
			buckets[i].Invoices = []ProjectionDoc{}
		}
		if buckets[i].Bills == nil {
			buckets[i].Bills = []ProjectionDoc{}
		}
		if bal.Sign() < 0 && out.FirstShortfall == nil {
			l := buckets[i].Label
			out.FirstShortfall = &l
		}
	}
	out.Buckets = buckets
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

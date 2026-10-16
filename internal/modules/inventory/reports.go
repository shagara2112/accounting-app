package inventory

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/modules/inventory/internal/store"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

type StockRow struct {
	ProductID          uuid.UUID    `json:"productId"`
	Code               string       `json:"code"`
	Name               string       `json:"name"`
	Unit               string       `json:"unit"`
	Active             bool         `json:"active"`
	InventoryAccountID uuid.UUID    `json:"inventoryAccountId"`
	Qty                money.Amount `json:"qty"`
	Value              money.Amount `json:"value"`
	AvgCost            money.Amount `json:"avgCost"`
}

// AccountCheck mencocokkan nilai kartu stok dengan saldo akun persediaan di buku besar.
// Selisih muncul bila ada jurnal manual ke akun persediaan.
type AccountCheck struct {
	AccountID  uuid.UUID    `json:"accountId"`
	Code       string       `json:"code"`
	Name       string       `json:"name"`
	Subledger  money.Amount `json:"subledger"`
	Ledger     money.Amount `json:"ledger"`
	Difference money.Amount `json:"difference"`
}

// StockReport adalah posisi stok dan nilai persediaan per tanggal (INV-03).
type StockReport struct {
	AsOf       civil.Date     `json:"asOf"`
	Rows       []StockRow     `json:"rows"`
	TotalValue money.Amount   `json:"totalValue"`
	Accounts   []AccountCheck `json:"accounts"`
}

func (s *service) Stock(ctx context.Context, companyID uuid.UUID, asOf civil.Date) (StockReport, error) {
	products, err := s.Products(ctx, companyID, ProductFilter{Kind: KindGoods, IncludeInactive: true})
	if err != nil {
		return StockReport{}, err
	}
	levels, err := s.q(ctx).StockAsOf(ctx, store.StockAsOfParams{CompanyID: companyID, AsOf: asOf})
	if err != nil {
		return StockReport{}, err
	}
	at := map[uuid.UUID]store.StockAsOfRow{}
	for _, l := range levels {
		at[l.ProductID] = l
	}
	out := StockReport{AsOf: asOf, Rows: []StockRow{}, Accounts: []AccountCheck{}}
	byAccount := map[uuid.UUID]money.Amount{}
	for _, p := range products {
		l, moved := at[p.ID]
		if !p.Active && (!moved || l.QtyAfter.IsZero() && l.ValueAfter.IsZero()) {
			continue
		}
		row := StockRow{
			ProductID: p.ID, Code: p.Code, Name: p.Name, Unit: p.Unit, Active: p.Active,
			InventoryAccountID: *p.InventoryAccountID, Qty: l.QtyAfter, Value: l.ValueAfter,
			AvgCost: avgCost(l.QtyAfter, l.ValueAfter),
		}
		out.Rows = append(out.Rows, row)
		out.TotalValue = out.TotalValue.Add(row.Value)
		byAccount[row.InventoryAccountID] = byAccount[row.InventoryAccountID].Add(row.Value)
	}

	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return StockReport{}, err
	}
	for id, sub := range byAccount {
		l, err := s.Ledger.AccountLedger(ctx, companyID, id, civil.New(2000, 1, 1), asOf)
		if err != nil {
			return StockReport{}, err
		}
		a := accounts[id]
		out.Accounts = append(out.Accounts, AccountCheck{
			AccountID: id, Code: a.Code, Name: a.Name, Subledger: sub, Ledger: l.Closing, Difference: l.Closing.Sub(sub),
		})
	}
	sort.Slice(out.Accounts, func(i, j int) bool { return out.Accounts[i].Code < out.Accounts[j].Code })
	return out, nil
}

type CardLine struct {
	Date       civil.Date   `json:"date"`
	SourceType string       `json:"sourceType"`
	SourceID   uuid.UUID    `json:"sourceId"`
	Number     string       `json:"number"`
	Memo       string       `json:"memo"`
	QtyIn      money.Amount `json:"qtyIn"`
	QtyOut     money.Amount `json:"qtyOut"`
	Value      money.Amount `json:"value"` // bertanda
	UnitCost   money.Amount `json:"unitCost"`
	QtyAfter   money.Amount `json:"qtyAfter"`
	ValueAfter money.Amount `json:"valueAfter"`
	Reversal   bool         `json:"reversal"`
}

// StockCard adalah kartu stok satu barang untuk rentang tanggal.
type StockCard struct {
	Product      Product      `json:"product"`
	From         civil.Date   `json:"from"`
	To           civil.Date   `json:"to"`
	OpeningQty   money.Amount `json:"openingQty"`
	OpeningValue money.Amount `json:"openingValue"`
	Lines        []CardLine   `json:"lines"`
	ClosingQty   money.Amount `json:"closingQty"`
	ClosingValue money.Amount `json:"closingValue"`
	Truncated    bool         `json:"truncated"`
}

const maxCardLines = 2000

func (s *service) Card(ctx context.Context, companyID, productID uuid.UUID, from, to civil.Date) (StockCard, error) {
	p, err := s.Product(ctx, companyID, productID)
	if err != nil {
		return StockCard{}, err
	}
	q := s.q(ctx)
	out := StockCard{Product: p, From: from, To: to, Lines: []CardLine{}}
	prev, err := q.MovementBefore(ctx, store.MovementBeforeParams{CompanyID: companyID, ProductID: productID, Before: from})
	switch {
	case err == nil:
		out.OpeningQty, out.OpeningValue = prev.QtyAfter, prev.ValueAfter
	case !errors.Is(err, pgx.ErrNoRows):
		return StockCard{}, err
	}
	out.ClosingQty, out.ClosingValue = out.OpeningQty, out.OpeningValue
	rows, err := q.CardMovements(ctx, store.CardMovementsParams{
		CompanyID: companyID, ProductID: productID, FromDate: from, ToDate: to, RowLimit: maxCardLines,
	})
	if err != nil {
		return StockCard{}, err
	}
	for _, r := range rows {
		l := CardLine{
			Date: r.MoveDate, SourceType: r.SourceType, SourceID: r.SourceID, Number: r.SourceNumber, Memo: r.Memo,
			Value: r.Value, QtyAfter: r.QtyAfter, ValueAfter: r.ValueAfter, Reversal: r.MirrorOf != nil,
		}
		if r.Qty.Sign() > 0 {
			l.QtyIn = r.Qty
		} else {
			l.QtyOut = r.Qty.Neg()
		}
		l.UnitCost = r.Value.Abs().MulDiv(money.FromInt(1), r.Qty.Abs(), 4, money.HalfUp)
		out.Lines = append(out.Lines, l)
		out.ClosingQty, out.ClosingValue = r.QtyAfter, r.ValueAfter
	}
	out.Truncated = len(rows) == maxCardLines
	return out, nil
}

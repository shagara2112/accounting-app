package trade

import (
	"github.com/google/uuid"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// SummaryRow adalah satu baris ringkasan penjualan atau pembelian (RPT-05), per kontak atau produk.
type SummaryRow struct {
	ID          *uuid.UUID    `json:"id,omitempty"` // kosong = baris tanpa produk
	Code        string        `json:"code,omitempty"`
	Name        string        `json:"name"`
	Unit        string        `json:"unit,omitempty"`
	Documents   int           `json:"documents"`
	Qty         *money.Amount `json:"qty,omitempty"`
	DPP         money.Amount  `json:"dpp"`
	VAT         money.Amount  `json:"vat"`
	Withholding money.Amount  `json:"withholding"`
	Total       money.Amount  `json:"total"`
}

// CardLine adalah satu dokumen di kartu pelanggan atau pemasok.
type CardLine struct {
	Kind        string       `json:"kind"` // faktur, pelunasan, tagihan, pembayaran
	DocID       uuid.UUID    `json:"docId"`
	Number      string       `json:"number"`
	Date        civil.Date   `json:"date"`
	Memo        string       `json:"memo"`
	Debit       money.Amount `json:"debit"`
	Credit      money.Amount `json:"credit"`
	Withholding money.Amount `json:"withholding"`
	Balance     money.Amount `json:"balance"`
}

// Card adalah kartu piutang pelanggan atau kartu utang pemasok (RPT-04). Saldo piutang =
// debit − kredit; saldo utang = kredit − debit. Dokumen yang di-void tidak ikut.
type Card struct {
	ContactID   uuid.UUID    `json:"contactId"`
	ContactName string       `json:"contactName"`
	From        civil.Date   `json:"from"`
	To          civil.Date   `json:"to"`
	Opening     money.Amount `json:"opening"`
	Lines       []CardLine   `json:"lines"`
	TotalDebit  money.Amount `json:"totalDebit"`
	TotalCredit money.Amount `json:"totalCredit"`
	Closing     money.Amount `json:"closing"`
}

// Fill menghitung saldo berjalan dan total kartu. creditNormal = kartu utang.
func (c *Card) Fill(creditNormal bool) {
	bal := c.Opening
	for i := range c.Lines {
		l := &c.Lines[i]
		if creditNormal {
			bal = bal.Add(l.Credit).Sub(l.Debit)
		} else {
			bal = bal.Add(l.Debit).Sub(l.Credit)
		}
		l.Balance = bal
		c.TotalDebit = c.TotalDebit.Add(l.Debit)
		c.TotalCredit = c.TotalCredit.Add(l.Credit)
	}
	c.Closing = bal
	if c.Lines == nil {
		c.Lines = []CardLine{}
	}
}

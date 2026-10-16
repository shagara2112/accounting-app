package purchase

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/modules/core"
	"lajur/internal/modules/purchase/internal/store"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Data pendukung modul pajak untuk faktur pajak masukan (TAX-07) dan bukti potong (TAX-08).
// Data tagihannya tetap milik modul ini; modul pajak hanya menyimpan status pajaknya.

// InputBill adalah tagihan ber-PPN masukan. NotedVAT adalah PPN yang sudah dibatalkan nota debit.
type InputBill struct {
	BillID            uuid.UUID    `json:"billId"`
	Number            string       `json:"number"`
	Date              civil.Date   `json:"date"`
	Supplier          core.Contact `json:"supplier"`
	SupplierInvoiceNo string       `json:"supplierInvoiceNo"`
	DPP               money.Amount `json:"dpp"`
	VAT               money.Amount `json:"vat"`
	NotedVAT          money.Amount `json:"notedVat"`
}

// InputCredit adalah PPN nota debit yang mengurangi PPN masukan pada tanggal notanya.
type InputCredit struct {
	NoteID uuid.UUID
	BillID uuid.UUID
	Date   civil.Date
	VAT    money.Amount
}

// VATLine adalah PPN satu baris tagihan sesudah nota debit, beserta akun biayanya.
type VATLine struct {
	AccountID uuid.UUID
	ProjectID *uuid.UUID
	VAT       money.Amount
	Inventory bool // akun persediaan: hanya berubah lewat kartu stok
}

// BillVAT adalah PPN masukan satu tagihan per baris, untuk jurnal PPN tidak dikreditkan.
type BillVAT struct {
	BillID       uuid.UUID
	Number       string
	Date         civil.Date
	BranchID     *uuid.UUID
	DepartmentID *uuid.UUID
	Lines        []VATLine
}

// WithholdingBill adalah tagihan yang memotong PPh, dasar bukti potong. Noted* adalah DPP dan
// PPh yang dibatalkan nota debit; bukti potong memakai nilai bersihnya.
type WithholdingBill struct {
	BillID            uuid.UUID
	Number            string
	Date              civil.Date
	Supplier          core.Contact
	SupplierInvoiceNo string
	Code              string
	DPP               money.Amount
	Withholding       money.Amount
	NotedDPP          money.Amount
	NotedWithholding  money.Amount
}

type contactCache struct {
	s    *service
	byID map[uuid.UUID]core.Contact
}

func (c *contactCache) get(ctx context.Context, companyID, id uuid.UUID) (core.Contact, error) {
	if v, ok := c.byID[id]; ok {
		return v, nil
	}
	v, err := c.s.Core.Contact(ctx, companyID, id)
	if err == nil {
		c.byID[id] = v
	}
	return v, err
}

func (s *service) InputBills(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]InputBill, error) {
	rows, err := s.q(ctx).InputVATBills(ctx, store.InputVATBillsParams{CompanyID: companyID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	cache := &contactCache{s: s, byID: map[uuid.UUID]core.Contact{}}
	out := make([]InputBill, len(rows))
	for i, r := range rows {
		c, err := cache.get(ctx, companyID, r.SupplierID)
		if err != nil {
			return nil, err
		}
		out[i] = InputBill{
			BillID: r.ID, Number: deref(r.Number), Date: r.BillDate, Supplier: c, SupplierInvoiceNo: r.SupplierInvoiceNo,
			DPP: r.Dpp, VAT: r.Vat, NotedVAT: r.NotedVat,
		}
	}
	return out, nil
}

func (s *service) InputCredits(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]InputCredit, error) {
	rows, err := s.q(ctx).DebitNoteVAT(ctx, store.DebitNoteVATParams{CompanyID: companyID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	out := make([]InputCredit, len(rows))
	for i, r := range rows {
		out[i] = InputCredit{NoteID: r.ID, BillID: r.BillID, Date: r.NoteDate, VAT: r.Vat}
	}
	return out, nil
}

func (s *service) BillVAT(ctx context.Context, companyID, billID uuid.UUID) (BillVAT, error) {
	q := s.q(ctx)
	b, err := q.GetBill(ctx, store.GetBillParams{CompanyID: companyID, ID: billID})
	if errors.Is(err, pgx.ErrNoRows) {
		return BillVAT{}, httpx.NotFound("bill.not_found", "Tagihan tidak ditemukan.")
	}
	if err != nil {
		return BillVAT{}, err
	}
	rows, err := q.BillVATLines(ctx, store.BillVATLinesParams{CompanyID: companyID, BillID: billID})
	if err != nil {
		return BillVAT{}, err
	}
	inv, err := s.Inventory.InventoryAccounts(ctx, companyID)
	if err != nil {
		return BillVAT{}, err
	}
	out := BillVAT{BillID: b.ID, Number: deref(b.Number), Date: b.BillDate, BranchID: b.BranchID, DepartmentID: b.DepartmentID}
	for _, r := range rows {
		if r.Vat.Sign() <= 0 {
			continue
		}
		out.Lines = append(out.Lines, VATLine{AccountID: r.AccountID, ProjectID: r.ProjectID, VAT: r.Vat, Inventory: inv[r.AccountID]})
	}
	return out, nil
}

func (s *service) WithholdingBills(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]WithholdingBill, error) {
	rows, err := s.q(ctx).WithholdingBills(ctx, store.WithholdingBillsParams{CompanyID: companyID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	cache := &contactCache{s: s, byID: map[uuid.UUID]core.Contact{}}
	out := make([]WithholdingBill, len(rows))
	for i, r := range rows {
		c, err := cache.get(ctx, companyID, r.SupplierID)
		if err != nil {
			return nil, err
		}
		out[i] = WithholdingBill{
			BillID: r.ID, Number: deref(r.Number), Date: r.BillDate, Supplier: c, SupplierInvoiceNo: r.SupplierInvoiceNo,
			Code: deref(r.WithholdingCode), DPP: r.Dpp, Withholding: r.Withholding, NotedDPP: r.NotedDpp, NotedWithholding: r.NotedWithholding,
		}
	}
	return out, nil
}

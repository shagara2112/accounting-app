package sales

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger"
	"lajur/internal/modules/sales/internal/store"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

type AllocationInput struct {
	InvoiceID       uuid.UUID    `json:"invoiceId"`
	Amount          money.Amount `json:"amount"`          // mengurangi sisa faktur
	WithholdingCode string       `json:"withholdingCode"` // PPh yang dipotong pelanggan, kosong bila tidak ada
	Withholding     money.Amount `json:"withholding"`
}

type ReceiptInput struct {
	CustomerID    uuid.UUID         `json:"customerId"`
	Date          civil.Date        `json:"date"`
	CashAccountID uuid.UUID         `json:"cashAccountId"`
	Memo          string            `json:"memo"`
	Allocations   []AllocationInput `json:"allocations"`
}

type Receipt struct {
	ID             uuid.UUID    `json:"id"`
	Number         string       `json:"number"`
	CustomerID     uuid.UUID    `json:"customerId"`
	CustomerName   string       `json:"customerName"`
	Date           civil.Date   `json:"date"`
	CashAccountID  uuid.UUID    `json:"cashAccountId"`
	CashAccount    string       `json:"cashAccount"`
	Amount         money.Amount `json:"amount"` // uang yang diterima
	Memo           string       `json:"memo"`
	Status         string       `json:"status"`
	JournalEntryID uuid.UUID    `json:"journalEntryId"`
}

type ReceiptAllocation struct {
	InvoiceID       uuid.UUID    `json:"invoiceId"`
	InvoiceNumber   string       `json:"invoiceNumber"`
	Amount          money.Amount `json:"amount"`
	WithholdingCode *string      `json:"withholdingCode,omitempty"`
	Withholding     money.Amount `json:"withholding"`
}

type ReceiptDetail struct {
	Receipt
	Allocated   money.Amount        `json:"allocated"`
	Withholding money.Amount        `json:"withholding"`
	Allocations []ReceiptAllocation `json:"allocations"`
	CreatedBy   *string             `json:"createdBy,omitempty"`
	VoidedBy    *string             `json:"voidedBy,omitempty"`
	VoidReason  *string             `json:"voidReason,omitempty"`
}

type ReceiptFilter struct {
	From, To   civil.Date
	CustomerID *uuid.UUID
}

// CreateReceipt memposting pelunasan (AR-04): debit kas/bank sebesar uang diterima, debit PPh 23
// dibayar di muka atau beban PPh final sebesar potongan, kredit piutang usaha sebesar alokasi.
func (s *service) CreateReceipt(ctx context.Context, companyID uuid.UUID, in ReceiptInput, actor audit.Actor) (ReceiptDetail, error) {
	in.Memo = strings.TrimSpace(in.Memo)
	f := map[string]string{}
	if in.Date.IsZero() {
		f["date"] = "Tanggal wajib diisi."
	}
	if in.CustomerID == uuid.Nil {
		f["customerId"] = "Pilih pelanggan."
	}
	if in.CashAccountID == uuid.Nil {
		f["cashAccountId"] = "Pilih rekening kas atau bank."
	}
	if utf8.RuneCountInString(in.Memo) > 500 {
		f["memo"] = "Catatan maksimal 500 karakter."
	}
	if len(in.Allocations) == 0 || len(in.Allocations) > 100 {
		f["allocations"] = "Pilih 1–100 faktur yang dilunasi."
	}
	seen := map[uuid.UUID]bool{}
	for i, a := range in.Allocations {
		key := fmt.Sprintf("allocations.%d.", i)
		if seen[a.InvoiceID] {
			f[key+"invoiceId"] = "Faktur yang sama dipilih dua kali."
		}
		seen[a.InvoiceID] = true
		if a.Amount.Sign() <= 0 {
			f[key+"amount"] = "Nominal pelunasan harus lebih dari 0."
		}
		if a.Withholding.Sign() < 0 || (a.Withholding.Sign() > 0 && a.Withholding.Cmp(a.Amount) >= 0) {
			f[key+"withholding"] = "Potongan PPh harus di bawah nominal pelunasan."
		}
		if (a.Withholding.Sign() > 0) != (a.WithholdingCode != "") {
			f[key+"withholdingCode"] = "Isi kode dan nominal PPh bersamaan, atau kosongkan keduanya."
		}
	}
	if len(f) > 0 {
		return ReceiptDetail{}, httpx.Unprocessable("receipt.invalid", trade.FirstMessage(f)).WithFields(f)
	}

	var out ReceiptDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		customer, err := s.Core.Contact(ctx, companyID, in.CustomerID)
		if err != nil {
			return fieldErr(err, "customerId")
		}
		if !customer.IsCustomer {
			msg := fmt.Sprintf("%s bukan pelanggan.", customer.Name)
			return httpx.Unprocessable("receipt.invalid", msg).WithFields(map[string]string{"customerId": msg})
		}
		cashAcc, err := s.Cash.Account(ctx, companyID, in.CashAccountID)
		if err != nil {
			return fieldErr(err, "cashAccountId")
		}
		table, err := s.Tax.Table(ctx)
		if err != nil {
			return err
		}

		ids := make([]uuid.UUID, len(in.Allocations))
		for i, a := range in.Allocations {
			ids[i] = a.InvoiceID
		}
		// Kunci faktur supaya dua pelunasan bersamaan tidak bisa melebihi sisa faktur.
		invs, err := q.LockInvoices(ctx, store.LockInvoicesParams{CompanyID: companyID, Ids: ids})
		if err != nil {
			return err
		}
		byID := map[uuid.UUID]store.SalesInvoice{}
		for _, inv := range invs {
			byID[inv.ID] = inv
		}
		paidRows, err := q.PaidByInvoice(ctx, store.PaidByInvoiceParams{CompanyID: companyID, Ids: ids})
		if err != nil {
			return err
		}
		paid := map[uuid.UUID]money.Amount{}
		for _, p := range paidRows {
			paid[p.InvoiceID] = p.Paid
		}

		var allocated, cashIn, wh23, whFinal money.Amount
		af := map[string]string{}
		var numbers []string
		for i, a := range in.Allocations {
			key := fmt.Sprintf("allocations.%d.", i)
			inv, ok := byID[a.InvoiceID]
			switch {
			case !ok:
				af[key+"invoiceId"] = "Faktur tidak ditemukan di PT ini."
				continue
			case inv.Status != "posted":
				af[key+"invoiceId"] = fmt.Sprintf("Faktur %s belum diposting atau sudah di-void.", deref(inv.Number))
				continue
			case inv.CustomerID != in.CustomerID:
				af[key+"invoiceId"] = fmt.Sprintf("Faktur %s milik pelanggan lain.", deref(inv.Number))
				continue
			case in.Date.Before(inv.InvoiceDate):
				af[key+"invoiceId"] = fmt.Sprintf("Tanggal pelunasan sebelum tanggal faktur %s.", deref(inv.Number))
				continue
			}
			if rest := inv.Total.Sub(paid[inv.ID]); a.Amount.Cmp(rest) > 0 {
				af[key+"amount"] = fmt.Sprintf("Melebihi sisa faktur %s (%s).", deref(inv.Number), rest.IDR())
				continue
			}
			if a.WithholdingCode != "" {
				code, err := table.Find(a.WithholdingCode, in.Date)
				switch {
				case err != nil:
					af[key+"withholdingCode"] = problemText(err)
					continue
				case code.Kind != tax.KindPPh:
					af[key+"withholdingCode"] = fmt.Sprintf("%s bukan kode PPh.", code.Code)
					continue
				case code.Article == tax.ArticlePPh23:
					wh23 = wh23.Add(a.Withholding)
				default:
					whFinal = whFinal.Add(a.Withholding)
				}
			}
			allocated = allocated.Add(a.Amount)
			cashIn = cashIn.Add(a.Amount.Sub(a.Withholding))
			numbers = append(numbers, deref(inv.Number))
		}
		if len(af) > 0 {
			return httpx.Unprocessable("receipt.invalid", trade.FirstMessage(af)).WithFields(af)
		}

		receivable, err := s.Ledger.RoleAccount(ctx, companyID, ledger.Receivable)
		if err != nil {
			return err
		}
		number, err := s.Ledger.NextNumber(ctx, companyID, seriesReceipt, in.Date)
		if err != nil {
			return err
		}
		memo := fmt.Sprintf("Pelunasan %s dari %s", strings.Join(numbers, ", "), customer.Name)
		if utf8.RuneCountInString(memo) > 400 {
			memo = string([]rune(memo)[:400])
		}
		drafts := []ledger.LineDraft{{AccountID: cashAcc.AccountID, Debit: cashIn, Memo: memo}}
		if wh23.Sign() > 0 {
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.PrepaidPPh23)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Debit: wh23, Memo: "PPh 23 dipotong " + customer.Name})
		}
		if whFinal.Sign() > 0 {
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.FinalTaxExpense)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Debit: whFinal, Memo: "PPh final 4(2) dipotong " + customer.Name})
		}
		drafts = append(drafts, ledger.LineDraft{AccountID: receivable.ID, Credit: allocated, Memo: memo})

		id := uuid.Must(uuid.NewV7())
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: in.Date, Memo: memo, SourceType: SourceReceipt,
			SourceID: &id, Number: number, Lines: drafts, Actor: actor,
		})
		if err != nil {
			return err
		}
		if err := q.InsertReceipt(ctx, store.InsertReceiptParams{
			ID: id, CompanyID: companyID, Number: number, CustomerID: in.CustomerID, ReceiptDate: in.Date,
			CashAccountID: in.CashAccountID, Amount: cashIn, Memo: in.Memo, JournalEntryID: posted.ID,
			CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		for _, a := range in.Allocations {
			var code *string
			if a.WithholdingCode != "" {
				c := a.WithholdingCode
				code = &c
			}
			if err := q.InsertAllocation(ctx, store.InsertAllocationParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, ReceiptID: id, InvoiceID: a.InvoiceID,
				Amount: a.Amount, WithholdingCode: code, Withholding: a.Withholding,
			}); err != nil {
				return err
			}
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.receipt.post",
			ObjectType: "sales_receipt", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "amount": cashIn.String(), "allocated": allocated.String(), "invoices": numbers},
		}); err != nil {
			return err
		}
		out, err = s.Receipt(ctx, companyID, id)
		return err
	})
	return out, err
}

func problemText(err error) string {
	var p *httpx.Problem
	if errors.As(err, &p) {
		return p.Detail
	}
	return err.Error()
}

func (s *service) VoidReceipt(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (ReceiptDetail, error) {
	var out ReceiptDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		r, err := q.GetReceiptForUpdate(ctx, store.GetReceiptForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("receipt.not_found", "Pelunasan tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if r.Status != "posted" {
			return httpx.Conflict("receipt.already_void", fmt.Sprintf("%s sudah di-void.", r.Number))
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		v, err := s.Ledger.VoidSource(ctx, companyID, r.JournalEntryID, SourceReceipt, date, reason, actor)
		if err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if n, err := q.MarkReceiptVoid(ctx, store.MarkReceiptVoidParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || n != 1 {
			return errOr(err, "pelunasan gagal ditandai void")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.receipt.void",
			ObjectType: "sales_receipt", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": r.Number, "status": "posted"},
			After:  map[string]any{"number": r.Number, "status": "void", "reason": reason, "reversal": v.Reversal.Number},
		}); err != nil {
			return err
		}
		out, err = s.Receipt(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) cashNames(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]string, error) {
	list, err := s.Cash.Accounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	m := make(map[uuid.UUID]string, len(list))
	for _, a := range list {
		m[a.AccountID] = a.Name
	}
	return m, nil
}

func (s *service) Receipts(ctx context.Context, companyID uuid.UUID, f ReceiptFilter) ([]Receipt, error) {
	rows, err := s.q(ctx).ListReceipts(ctx, store.ListReceiptsParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, CustomerID: f.CustomerID})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.CustomerID
	}
	names, err := s.Core.ContactNames(ctx, companyID, ids)
	if err != nil {
		return nil, err
	}
	cashNames, err := s.cashNames(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Receipt, len(rows))
	for i, r := range rows {
		out[i] = Receipt{
			ID: r.ID, Number: r.Number, CustomerID: r.CustomerID, CustomerName: names[r.CustomerID], Date: r.ReceiptDate,
			CashAccountID: r.CashAccountID, CashAccount: cashNames[r.CashAccountID], Amount: r.Amount, Memo: r.Memo,
			Status: r.Status, JournalEntryID: r.JournalEntryID,
		}
	}
	return out, nil
}

func (s *service) Receipt(ctx context.Context, companyID, id uuid.UUID) (ReceiptDetail, error) {
	q := s.q(ctx)
	r, err := q.GetReceipt(ctx, store.GetReceiptParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReceiptDetail{}, httpx.NotFound("receipt.not_found", "Pelunasan tidak ditemukan.")
	}
	if err != nil {
		return ReceiptDetail{}, err
	}
	names, err := s.Core.ContactNames(ctx, companyID, []uuid.UUID{r.CustomerID})
	if err != nil {
		return ReceiptDetail{}, err
	}
	cashNames, err := s.cashNames(ctx, companyID)
	if err != nil {
		return ReceiptDetail{}, err
	}
	allocs, err := q.ReceiptAllocations(ctx, store.ReceiptAllocationsParams{CompanyID: companyID, ReceiptID: id})
	if err != nil {
		return ReceiptDetail{}, err
	}
	out := ReceiptDetail{
		Receipt: Receipt{
			ID: r.ID, Number: r.Number, CustomerID: r.CustomerID, CustomerName: names[r.CustomerID], Date: r.ReceiptDate,
			CashAccountID: r.CashAccountID, CashAccount: cashNames[r.CashAccountID], Amount: r.Amount, Memo: r.Memo,
			Status: r.Status, JournalEntryID: r.JournalEntryID,
		},
		Allocations: make([]ReceiptAllocation, 0, len(allocs)), CreatedBy: r.CreatedByName,
		VoidedBy: r.VoidedByName, VoidReason: r.VoidReason,
	}
	for _, a := range allocs {
		out.Allocated = out.Allocated.Add(a.Amount)
		out.Withholding = out.Withholding.Add(a.Withholding)
		out.Allocations = append(out.Allocations, ReceiptAllocation{
			InvoiceID: a.InvoiceID, InvoiceNumber: deref(a.InvoiceNumber), Amount: a.Amount,
			WithholdingCode: a.WithholdingCode, Withholding: a.Withholding,
		})
	}
	return out, nil
}

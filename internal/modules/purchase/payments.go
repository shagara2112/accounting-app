package purchase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger"
	"lajur/internal/modules/purchase/internal/store"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

type AllocationInput struct {
	BillID uuid.UUID    `json:"billId"`
	Amount money.Amount `json:"amount"`
}

type PaymentInput struct {
	SupplierID    uuid.UUID         `json:"supplierId"`
	Date          civil.Date        `json:"date"`
	CashAccountID uuid.UUID         `json:"cashAccountId"`
	Memo          string            `json:"memo"`
	Allocations   []AllocationInput `json:"allocations"`
}

type Payment struct {
	ID             uuid.UUID    `json:"id"`
	Number         string       `json:"number"`
	SupplierID     uuid.UUID    `json:"supplierId"`
	SupplierName   string       `json:"supplierName"`
	Date           civil.Date   `json:"date"`
	CashAccountID  uuid.UUID    `json:"cashAccountId"`
	CashAccount    string       `json:"cashAccount"`
	Amount         money.Amount `json:"amount"`
	Memo           string       `json:"memo"`
	Status         string       `json:"status"`
	JournalEntryID uuid.UUID    `json:"journalEntryId"`
}

type PaymentAllocation struct {
	BillID     uuid.UUID    `json:"billId"`
	BillNumber string       `json:"billNumber"`
	Amount     money.Amount `json:"amount"`
}

type PaymentDetail struct {
	Payment
	Allocations []PaymentAllocation `json:"allocations"`
	CreatedBy   *string             `json:"createdBy,omitempty"`
	VoidedBy    *string             `json:"voidedBy,omitempty"`
	VoidReason  *string             `json:"voidReason,omitempty"`
}

type PaymentFilter struct {
	From, To   civil.Date
	SupplierID *uuid.UUID
}

// CreatePayment memposting pembayaran (AP-03): debit utang usaha, kredit kas/bank.
func (s *service) CreatePayment(ctx context.Context, companyID uuid.UUID, in PaymentInput, actor audit.Actor) (PaymentDetail, error) {
	in.Memo = strings.TrimSpace(in.Memo)
	f := map[string]string{}
	if in.Date.IsZero() {
		f["date"] = "Tanggal wajib diisi."
	}
	if in.SupplierID == uuid.Nil {
		f["supplierId"] = "Pilih pemasok."
	}
	if in.CashAccountID == uuid.Nil {
		f["cashAccountId"] = "Pilih rekening kas atau bank."
	}
	if utf8.RuneCountInString(in.Memo) > 500 {
		f["memo"] = "Catatan maksimal 500 karakter."
	}
	if len(in.Allocations) == 0 || len(in.Allocations) > 100 {
		f["allocations"] = "Pilih 1–100 tagihan yang dibayar."
	}
	seen := map[uuid.UUID]bool{}
	for i, a := range in.Allocations {
		key := fmt.Sprintf("allocations.%d.", i)
		if seen[a.BillID] {
			f[key+"billId"] = "Tagihan yang sama dipilih dua kali."
		}
		seen[a.BillID] = true
		if a.Amount.Sign() <= 0 {
			f[key+"amount"] = "Nominal pembayaran harus lebih dari 0."
		}
	}
	if len(f) > 0 {
		return PaymentDetail{}, httpx.Unprocessable("payment.invalid", trade.FirstMessage(f)).WithFields(f)
	}

	var out PaymentDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		supplier, err := s.Core.Contact(ctx, companyID, in.SupplierID)
		if err != nil {
			return fieldErr(err, "supplierId")
		}
		if !supplier.IsSupplier {
			msg := fmt.Sprintf("%s bukan pemasok.", supplier.Name)
			return httpx.Unprocessable("payment.invalid", msg).WithFields(map[string]string{"supplierId": msg})
		}
		cashAcc, err := s.Cash.Account(ctx, companyID, in.CashAccountID)
		if err != nil {
			return fieldErr(err, "cashAccountId")
		}
		ids := make([]uuid.UUID, len(in.Allocations))
		for i, a := range in.Allocations {
			ids[i] = a.BillID
		}
		bills, err := q.LockBills(ctx, store.LockBillsParams{CompanyID: companyID, Ids: ids})
		if err != nil {
			return err
		}
		byID := map[uuid.UUID]store.PurchaseBill{}
		for _, b := range bills {
			byID[b.ID] = b
		}
		paidRows, err := q.PaidByBill(ctx, store.PaidByBillParams{CompanyID: companyID, Ids: ids})
		if err != nil {
			return err
		}
		paid := map[uuid.UUID]money.Amount{}
		for _, p := range paidRows {
			paid[p.BillID] = p.Paid
		}

		var total money.Amount
		var numbers []string
		af := map[string]string{}
		for i, a := range in.Allocations {
			key := fmt.Sprintf("allocations.%d.", i)
			b, ok := byID[a.BillID]
			switch {
			case !ok:
				af[key+"billId"] = "Tagihan tidak ditemukan di PT ini."
				continue
			case b.Status != "posted":
				af[key+"billId"] = fmt.Sprintf("Tagihan %s belum diposting atau sudah di-void.", deref(b.Number))
				continue
			case b.SupplierID != in.SupplierID:
				af[key+"billId"] = fmt.Sprintf("Tagihan %s milik pemasok lain.", deref(b.Number))
				continue
			case in.Date.Before(b.BillDate):
				af[key+"billId"] = fmt.Sprintf("Tanggal pembayaran sebelum tanggal tagihan %s.", deref(b.Number))
				continue
			}
			if rest := b.Payable.Sub(paid[b.ID]); a.Amount.Cmp(rest) > 0 {
				af[key+"amount"] = fmt.Sprintf("Melebihi sisa tagihan %s (%s).", deref(b.Number), rest.IDR())
				continue
			}
			total = total.Add(a.Amount)
			numbers = append(numbers, deref(b.Number))
		}
		if len(af) > 0 {
			return httpx.Unprocessable("payment.invalid", trade.FirstMessage(af)).WithFields(af)
		}

		payable, err := s.Ledger.RoleAccount(ctx, companyID, ledger.Payable)
		if err != nil {
			return err
		}
		number, err := s.Ledger.NextNumber(ctx, companyID, seriesPayment, in.Date)
		if err != nil {
			return err
		}
		memo := fmt.Sprintf("Pembayaran %s ke %s", strings.Join(numbers, ", "), supplier.Name)
		if utf8.RuneCountInString(memo) > 400 {
			memo = string([]rune(memo)[:400])
		}
		id := uuid.Must(uuid.NewV7())
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: in.Date, Memo: memo, SourceType: SourcePayment,
			SourceID: &id, Number: number, Actor: actor,
			Lines: []ledger.LineDraft{
				{AccountID: payable.ID, Debit: total, Memo: memo},
				{AccountID: cashAcc.AccountID, Credit: total, Memo: memo},
			},
		})
		if err != nil {
			return err
		}
		if err := q.InsertPayment(ctx, store.InsertPaymentParams{
			ID: id, CompanyID: companyID, Number: number, SupplierID: in.SupplierID, PaymentDate: in.Date,
			CashAccountID: in.CashAccountID, Amount: total, Memo: in.Memo, JournalEntryID: posted.ID,
			CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		for _, a := range in.Allocations {
			if err := q.InsertAllocation(ctx, store.InsertAllocationParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, PaymentID: id, BillID: a.BillID, Amount: a.Amount,
			}); err != nil {
				return err
			}
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "purchase.payment.post",
			ObjectType: "purchase_payment", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "amount": total.String(), "bills": numbers},
		}); err != nil {
			return err
		}
		out, err = s.Payment(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) VoidPayment(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (PaymentDetail, error) {
	var out PaymentDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		p, err := q.GetPaymentForUpdate(ctx, store.GetPaymentForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("payment.not_found", "Pembayaran tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if p.Status != "posted" {
			return httpx.Conflict("payment.already_void", fmt.Sprintf("%s sudah di-void.", p.Number))
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		v, err := s.Ledger.VoidSource(ctx, companyID, p.JournalEntryID, SourcePayment, date, reason, actor)
		if err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if n, err := q.MarkPaymentVoid(ctx, store.MarkPaymentVoidParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || n != 1 {
			return errOr(err, "pembayaran gagal ditandai void")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "purchase.payment.void",
			ObjectType: "purchase_payment", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": p.Number, "status": "posted"},
			After:  map[string]any{"number": p.Number, "status": "void", "reason": reason, "reversal": v.Reversal.Number},
		}); err != nil {
			return err
		}
		out, err = s.Payment(ctx, companyID, id)
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

func (s *service) Payments(ctx context.Context, companyID uuid.UUID, f PaymentFilter) ([]Payment, error) {
	rows, err := s.q(ctx).ListPayments(ctx, store.ListPaymentsParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, SupplierID: f.SupplierID})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.SupplierID
	}
	names, err := s.Core.ContactNames(ctx, companyID, ids)
	if err != nil {
		return nil, err
	}
	cashNames, err := s.cashNames(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Payment, len(rows))
	for i, r := range rows {
		out[i] = Payment{
			ID: r.ID, Number: r.Number, SupplierID: r.SupplierID, SupplierName: names[r.SupplierID], Date: r.PaymentDate,
			CashAccountID: r.CashAccountID, CashAccount: cashNames[r.CashAccountID], Amount: r.Amount, Memo: r.Memo,
			Status: r.Status, JournalEntryID: r.JournalEntryID,
		}
	}
	return out, nil
}

func (s *service) Payment(ctx context.Context, companyID, id uuid.UUID) (PaymentDetail, error) {
	q := s.q(ctx)
	r, err := q.GetPayment(ctx, store.GetPaymentParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return PaymentDetail{}, httpx.NotFound("payment.not_found", "Pembayaran tidak ditemukan.")
	}
	if err != nil {
		return PaymentDetail{}, err
	}
	names, err := s.Core.ContactNames(ctx, companyID, []uuid.UUID{r.SupplierID})
	if err != nil {
		return PaymentDetail{}, err
	}
	cashNames, err := s.cashNames(ctx, companyID)
	if err != nil {
		return PaymentDetail{}, err
	}
	allocs, err := q.PaymentAllocations(ctx, store.PaymentAllocationsParams{CompanyID: companyID, PaymentID: id})
	if err != nil {
		return PaymentDetail{}, err
	}
	out := PaymentDetail{
		Payment: Payment{
			ID: r.ID, Number: r.Number, SupplierID: r.SupplierID, SupplierName: names[r.SupplierID], Date: r.PaymentDate,
			CashAccountID: r.CashAccountID, CashAccount: cashNames[r.CashAccountID], Amount: r.Amount, Memo: r.Memo,
			Status: r.Status, JournalEntryID: r.JournalEntryID,
		},
		Allocations: make([]PaymentAllocation, 0, len(allocs)), CreatedBy: r.CreatedByName,
		VoidedBy: r.VoidedByName, VoidReason: r.VoidReason,
	}
	for _, a := range allocs {
		out.Allocations = append(out.Allocations, PaymentAllocation{BillID: a.BillID, BillNumber: deref(a.BillNumber), Amount: a.Amount})
	}
	return out, nil
}

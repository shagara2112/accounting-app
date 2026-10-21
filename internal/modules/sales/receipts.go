package sales

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
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
	"lajur/internal/platform/rbac"
)

type AllocationInput struct {
	InvoiceID       uuid.UUID    `json:"invoiceId"`
	Amount          money.Amount `json:"amount"`          // mengurangi sisa faktur
	WithholdingCode string       `json:"withholdingCode"` // PPh yang dipotong pelanggan, kosong bila tidak ada
	Withholding     money.Amount `json:"withholding"`
	// VATCollected: PPN faktur yang dipungut dan disetor sendiri oleh pelanggan pemungut (WAPU).
	VATCollected money.Amount `json:"vatCollected"`
}

// ReceiptInput: Advance adalah uang muka yang diterima di luar faktur (AR-04); FromAdvance
// adalah saldo uang muka pelanggan yang dipakai melunasi faktur. Rekening kas hanya wajib bila
// ada uang yang benar-benar diterima.
type ReceiptInput struct {
	CustomerID    uuid.UUID         `json:"customerId"`
	Date          civil.Date        `json:"date"`
	CashAccountID uuid.UUID         `json:"cashAccountId"`
	Memo          string            `json:"memo"`
	Allocations   []AllocationInput `json:"allocations"`
	Advance       money.Amount      `json:"advance"`
	FromAdvance   money.Amount      `json:"fromAdvance"`
}

// AdvanceRefundInput mengembalikan uang muka pelanggan dari kas atau bank.
type AdvanceRefundInput struct {
	CustomerID    uuid.UUID    `json:"customerId"`
	Date          civil.Date   `json:"date"`
	CashAccountID uuid.UUID    `json:"cashAccountId"`
	Amount        money.Amount `json:"amount"`
	Memo          string       `json:"memo"`
}

const (
	ReceiptKindSettle = "pelunasan"
	ReceiptKindRefund = "pengembalian"
)

type Receipt struct {
	ID             uuid.UUID    `json:"id"`
	Number         string       `json:"number"`
	Kind           string       `json:"kind"` // pelunasan atau pengembalian uang muka
	CustomerID     uuid.UUID    `json:"customerId"`
	CustomerName   string       `json:"customerName"`
	Date           civil.Date   `json:"date"`
	CashAccountID  *uuid.UUID   `json:"cashAccountId,omitempty"`
	CashAccount    string       `json:"cashAccount"`
	Amount         money.Amount `json:"amount"` // uang yang diterima (pengembalian: dikeluarkan)
	Advance        money.Amount `json:"advance"`
	FromAdvance    money.Amount `json:"fromAdvance"`
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
	VATCollected    money.Amount `json:"vatCollected"`
}

type ReceiptDetail struct {
	Receipt
	Allocated    money.Amount        `json:"allocated"`
	Withholding  money.Amount        `json:"withholding"`
	VATCollected money.Amount        `json:"vatCollected"`
	Allocations  []ReceiptAllocation `json:"allocations"`
	CreatedBy    *string             `json:"createdBy,omitempty"`
	VoidedBy     *string             `json:"voidedBy,omitempty"`
	VoidReason   *string             `json:"voidReason,omitempty"`
}

type ReceiptFilter struct {
	From, To   civil.Date
	CustomerID *uuid.UUID
}

// CreateReceipt memposting pelunasan (AR-04): debit kas/bank sebesar uang diterima, debit PPh 23
// dibayar di muka atau beban PPh final sebesar potongan, debit akun PPN keluaran faktur sebesar
// PPN yang dipungut pelanggan pemungut, kredit piutang usaha sebesar alokasi.
func (s *service) CreateReceipt(ctx context.Context, companyID uuid.UUID, in ReceiptInput, actor audit.Actor) (ReceiptDetail, error) {
	in.Memo = strings.TrimSpace(in.Memo)
	f := map[string]string{}
	if in.Date.IsZero() {
		f["date"] = "Tanggal wajib diisi."
	}
	if in.CustomerID == uuid.Nil {
		f["customerId"] = "Pilih pelanggan."
	}
	if utf8.RuneCountInString(in.Memo) > 500 {
		f["memo"] = "Catatan maksimal 500 karakter."
	}
	switch {
	case in.Advance.Sign() < 0:
		f["advance"] = "Uang muka tidak boleh negatif."
	case in.FromAdvance.Sign() < 0:
		f["fromAdvance"] = "Pemakaian uang muka tidak boleh negatif."
	case in.Advance.Sign() > 0 && in.FromAdvance.Sign() > 0:
		f["fromAdvance"] = "Terima uang muka baru atau pakai saldo uang muka, tidak keduanya sekaligus."
	}
	if len(in.Allocations) > 100 || (len(in.Allocations) == 0 && in.Advance.Sign() <= 0) {
		f["allocations"] = "Pilih 1–100 faktur yang dilunasi, atau isi uang muka."
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
		switch {
		case a.VATCollected.Sign() < 0:
			f[key+"vatCollected"] = "PPN dipungut tidak boleh negatif."
		case a.VATCollected.Sign() > 0 && a.Withholding.Add(a.VATCollected).Cmp(a.Amount) > 0:
			f[key+"vatCollected"] = "Potongan PPh dan PPN dipungut melebihi nominal pelunasan."
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
		collectedRows, err := q.CollectedByInvoice(ctx, store.CollectedByInvoiceParams{CompanyID: companyID, Ids: ids})
		if err != nil {
			return err
		}
		collected := map[uuid.UUID]money.Amount{}
		for _, c := range collectedRows {
			collected[c.InvoiceID] = c.Collected
		}

		// Piutang dan potongan PPh dijurnal per proyek faktur, supaya laba dan piutang per
		// proyek (termasuk beban PPh final 4(2)) ikut terbaca.
		type sums struct {
			allocated, wh23, whFinal money.Amount
			vat                      map[uuid.UUID]money.Amount // PPN dipungut per akun PPN keluaran
			vatAccounts              []uuid.UUID                // urut kemunculan
		}
		byProject := map[uuid.UUID]*sums{}
		var projects []uuid.UUID // urut kemunculan; uuid.Nil = tanpa proyek
		sumFor := func(p *uuid.UUID) *sums {
			k := uuid.Nil
			if p != nil {
				k = *p
			}
			if _, ok := byProject[k]; !ok {
				byProject[k] = &sums{vat: map[uuid.UUID]money.Amount{}}
				projects = append(projects, k)
			}
			return byProject[k]
		}
		var allocated, cashIn, vatCollected money.Amount
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
			case rbac.CheckBranch(ctx, companyID, inv.BranchID) != nil:
				af[key+"invoiceId"] = fmt.Sprintf("Faktur %s di luar cabang Anda.", deref(inv.Number))
				continue
			}
			if rest := receivableOf(inv).Sub(paid[inv.ID]); a.Amount.Cmp(rest) > 0 {
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
					sumFor(inv.ProjectID).wh23 = sumFor(inv.ProjectID).wh23.Add(a.Withholding)
				default:
					sumFor(inv.ProjectID).whFinal = sumFor(inv.ProjectID).whFinal.Add(a.Withholding)
				}
			}
			if a.VATCollected.Sign() > 0 {
				acc, msg, err := s.collectedVATAccount(ctx, companyID, inv, a.VATCollected, collected[inv.ID])
				if err != nil {
					return err
				}
				if msg != "" {
					af[key+"vatCollected"] = msg
					continue
				}
				sm := sumFor(inv.ProjectID)
				if _, ok := sm.vat[acc]; !ok {
					sm.vatAccounts = append(sm.vatAccounts, acc)
				}
				sm.vat[acc] = sm.vat[acc].Add(a.VATCollected)
				vatCollected = vatCollected.Add(a.VATCollected)
			}
			sumFor(inv.ProjectID).allocated = sumFor(inv.ProjectID).allocated.Add(a.Amount)
			allocated = allocated.Add(a.Amount)
			cashIn = cashIn.Add(a.Amount.Sub(a.Withholding).Sub(a.VATCollected))
			numbers = append(numbers, deref(inv.Number))
		}
		if len(af) > 0 {
			return httpx.Unprocessable("receipt.invalid", trade.FirstMessage(af)).WithFields(af)
		}
		if len(in.Allocations) == 0 {
			if err := rbac.CheckBranch(ctx, companyID, nil); err != nil {
				return err // uang muka tanpa faktur tidak punya cabang
			}
		}
		// Uang muka: saldo per pelanggan dikunci supaya dua pelunasan tidak memakai saldo yang sama.
		if err := q.LockAdvance(ctx, advanceLock("sales", companyID, in.CustomerID)); err != nil {
			return err
		}
		if in.FromAdvance.Sign() > 0 {
			bal, err := q.CustomerAdvanceBalance(ctx, store.CustomerAdvanceBalanceParams{CompanyID: companyID, CustomerID: in.CustomerID})
			if err != nil {
				return err
			}
			switch {
			case in.FromAdvance.Cmp(bal) > 0:
				msg := fmt.Sprintf("Saldo uang muka %s hanya %s.", customer.Name, bal.IDR())
				return httpx.Unprocessable("receipt.invalid", msg).WithFields(map[string]string{"fromAdvance": msg})
			case in.FromAdvance.Cmp(cashIn) > 0:
				msg := fmt.Sprintf("Uang muka yang dipakai melebihi nilai pelunasan (%s).", cashIn.IDR())
				return httpx.Unprocessable("receipt.invalid", msg).WithFields(map[string]string{"fromAdvance": msg})
			}
		}
		cashIn = cashIn.Add(in.Advance).Sub(in.FromAdvance)
		var cashAccount *uuid.UUID
		if cashIn.Sign() > 0 {
			if in.CashAccountID == uuid.Nil {
				return httpx.Unprocessable("receipt.invalid", "Pilih rekening kas atau bank.").WithFields(map[string]string{"cashAccountId": "Pilih rekening kas atau bank."})
			}
			acc, err := s.Cash.Account(ctx, companyID, in.CashAccountID)
			if err != nil {
				return fieldErr(err, "cashAccountId")
			}
			cashAccount = &acc.AccountID
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
		if len(numbers) == 0 {
			memo = "Uang muka dari " + customer.Name
		}
		if utf8.RuneCountInString(memo) > 400 {
			memo = string([]rune(memo)[:400])
		}
		var drafts []ledger.LineDraft
		if cashAccount != nil {
			drafts = append(drafts, ledger.LineDraft{AccountID: *cashAccount, Debit: cashIn, Memo: memo})
		}
		var advanceAcc ledger.Account
		if in.Advance.Sign() > 0 || in.FromAdvance.Sign() > 0 {
			if advanceAcc, err = s.Ledger.RoleAccount(ctx, companyID, ledger.CustomerAdvance); err != nil {
				return err
			}
		}
		if in.FromAdvance.Sign() > 0 {
			drafts = append(drafts, ledger.LineDraft{AccountID: advanceAcc.ID, Debit: in.FromAdvance, Memo: "Pemakaian uang muka " + customer.Name})
		}
		var prepaid23, finalTax ledger.Account
		for _, k := range projects {
			sm := byProject[k]
			var project *uuid.UUID
			if k != uuid.Nil {
				p := k
				project = &p
			}
			if sm.wh23.Sign() > 0 {
				if prepaid23.ID == uuid.Nil {
					if prepaid23, err = s.Ledger.RoleAccount(ctx, companyID, ledger.PrepaidPPh23); err != nil {
						return err
					}
				}
				drafts = append(drafts, ledger.LineDraft{AccountID: prepaid23.ID, Debit: sm.wh23, Memo: "PPh 23 dipotong " + customer.Name, ProjectID: project})
			}
			if sm.whFinal.Sign() > 0 {
				if finalTax.ID == uuid.Nil {
					if finalTax, err = s.Ledger.RoleAccount(ctx, companyID, ledger.FinalTaxExpense); err != nil {
						return err
					}
				}
				drafts = append(drafts, ledger.LineDraft{AccountID: finalTax.ID, Debit: sm.whFinal, Memo: "PPh final 4(2) dipotong " + customer.Name, ProjectID: project})
			}
			for _, acc := range sm.vatAccounts {
				drafts = append(drafts, ledger.LineDraft{AccountID: acc, Debit: sm.vat[acc], Memo: "PPN dipungut " + customer.Name, ProjectID: project})
			}
		}
		for _, k := range projects {
			var project *uuid.UUID
			if k != uuid.Nil {
				p := k
				project = &p
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: receivable.ID, Credit: byProject[k].allocated, Memo: memo, ProjectID: project})
		}
		if in.Advance.Sign() > 0 {
			drafts = append(drafts, ledger.LineDraft{AccountID: advanceAcc.ID, Credit: in.Advance, Memo: "Uang muka " + customer.Name})
		}

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
			CashAccountID: cashAccount, Amount: cashIn, Memo: in.Memo, JournalEntryID: posted.ID,
			CreatedBy: actor.UserRef(), Kind: ReceiptKindSettle, Advance: in.Advance, FromAdvance: in.FromAdvance,
			VatCollected: vatCollected,
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
				Amount: a.Amount, WithholdingCode: code, Withholding: a.Withholding, VatCollected: a.VATCollected,
			}); err != nil {
				return err
			}
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.receipt.post",
			ObjectType: "sales_receipt", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "amount": cashIn.String(), "allocated": allocated.String(), "invoices": numbers,
				"advance": in.Advance.String(), "fromAdvance": in.FromAdvance.String(), "vatCollected": vatCollected.String()},
		}); err != nil {
			return err
		}
		if s.AfterReceiptPost != nil {
			if err := s.AfterReceiptPost(ctx, companyID, id, actor); err != nil {
				return err
			}
		}
		out, err = s.Receipt(ctx, companyID, id)
		return err
	})
	return out, err
}

// collectedVATAccount memeriksa PPN dipungut satu faktur dan mengembalikan akun yang didebit:
// akun PPN keluaran yang dikredit faktur itu saat diposting, sehingga saldo akun PPN yang
// disetor sendiri berkurang sebesar PPN yang disetor pelanggan. Faktur lama memakai PPN keluaran;
// faktur saldo awal (tanpa PPN tercatat) memakai PPN keluaran dipungut. msg berisi alasan
// penolakan untuk pengguna.
func (s *service) collectedVATAccount(ctx context.Context, companyID uuid.UUID, inv store.SalesInvoice, amount, before money.Amount) (uuid.UUID, string, error) {
	number := deref(inv.Number)
	switch {
	case inv.Vat.IsZero() && inv.Kind != KindOpening:
		return uuid.Nil, fmt.Sprintf("Faktur %s tidak ber-PPN.", number), nil
	case inv.Vat.Sign() > 0 && amount.Cmp(inv.Vat.Sub(before)) > 0:
		return uuid.Nil, fmt.Sprintf("PPN dipungut melebihi PPN faktur %s yang belum dipungut (%s).", number, inv.Vat.Sub(before).IDR()), nil
	}
	if inv.VatAccountID != nil {
		return *inv.VatAccountID, "", nil
	}
	role := ledger.OutputVAT
	if inv.Vat.IsZero() {
		role = ledger.OutputVATCollected
	}
	acc, err := s.Ledger.RoleAccount(ctx, companyID, role)
	return acc.ID, "", err
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
		if r.Advance.Sign() > 0 {
			if err := s.checkAdvanceRemoval(ctx, companyID, r.CustomerID, r.Advance, r.Number); err != nil {
				return err
			}
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
		if s.AfterReceiptVoid != nil {
			if err := s.AfterReceiptVoid(ctx, companyID, id, date, reason, actor); err != nil {
				return err
			}
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
	rows, err := s.q(ctx).ListReceipts(ctx, store.ListReceiptsParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, CustomerID: f.CustomerID, Branches: rbac.BranchesFor(ctx, companyID)})
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
			CashAccountID: r.CashAccountID, CashAccount: cashNames[derefID(r.CashAccountID)], Amount: r.Amount, Memo: r.Memo,
			Status: r.Status, JournalEntryID: r.JournalEntryID, Kind: r.Kind, Advance: r.Advance, FromAdvance: r.FromAdvance,
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
			CashAccountID: r.CashAccountID, CashAccount: cashNames[derefID(r.CashAccountID)], Amount: r.Amount, Memo: r.Memo,
			Status: r.Status, JournalEntryID: r.JournalEntryID, Kind: r.Kind, Advance: r.Advance, FromAdvance: r.FromAdvance,
		},
		Allocations: make([]ReceiptAllocation, 0, len(allocs)), CreatedBy: r.CreatedByName,
		VoidedBy: r.VoidedByName, VoidReason: r.VoidReason,
	}
	for _, a := range allocs {
		out.Allocated = out.Allocated.Add(a.Amount)
		out.Withholding = out.Withholding.Add(a.Withholding)
		out.VATCollected = out.VATCollected.Add(a.VatCollected)
		out.Allocations = append(out.Allocations, ReceiptAllocation{
			InvoiceID: a.InvoiceID, InvoiceNumber: deref(a.InvoiceNumber), Amount: a.Amount,
			WithholdingCode: a.WithholdingCode, Withholding: a.Withholding, VATCollected: a.VatCollected,
		})
	}
	return out, nil
}

// advanceLock adalah kunci advisory saldo uang muka satu kontak.
func advanceLock(scope string, companyID, contactID uuid.UUID) int64 {
	h := fnv.New64a()
	h.Write([]byte(scope))
	h.Write(companyID[:])
	h.Write(contactID[:])
	return int64(h.Sum64())
}

// checkAdvanceRemoval menolak pembatalan uang muka yang sudah dipakai atau dikembalikan.
func (s *service) checkAdvanceRemoval(ctx context.Context, companyID, customerID uuid.UUID, amount money.Amount, number string) error {
	q := s.q(ctx)
	if err := q.LockAdvance(ctx, advanceLock("sales", companyID, customerID)); err != nil {
		return err
	}
	bal, err := q.CustomerAdvanceBalance(ctx, store.CustomerAdvanceBalanceParams{CompanyID: companyID, CustomerID: customerID})
	if err != nil {
		return err
	}
	if bal.Cmp(amount) < 0 {
		return httpx.Unprocessable("advance.used", fmt.Sprintf(
			"Uang muka di %s sudah dipakai atau dikembalikan (sisa saldo %s). Void pelunasan atau pengembalian yang memakainya dulu.", number, bal.IDR()))
	}
	return nil
}

// AdvanceBalance adalah saldo uang muka satu pelanggan.
func (s *service) AdvanceBalance(ctx context.Context, companyID, customerID uuid.UUID) (money.Amount, error) {
	return s.q(ctx).CustomerAdvanceBalance(ctx, store.CustomerAdvanceBalanceParams{CompanyID: companyID, CustomerID: customerID})
}

// AdvanceBalances adalah saldo uang muka per pelanggan yang bersaldo.
func (s *service) AdvanceBalances(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]money.Amount, error) {
	rows, err := s.q(ctx).CustomerAdvanceBalances(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]money.Amount, len(rows))
	for _, r := range rows {
		out[r.CustomerID] = r.Balance
	}
	return out, nil
}

// RefundAdvance mengembalikan uang muka pelanggan (bukti kas keluar): debit uang muka
// pelanggan, kredit kas/bank.
func (s *service) RefundAdvance(ctx context.Context, companyID uuid.UUID, in AdvanceRefundInput, actor audit.Actor) (ReceiptDetail, error) {
	var out ReceiptDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		in.Memo = strings.TrimSpace(in.Memo)
		f := map[string]string{}
		if in.Date.IsZero() {
			f["date"] = "Tanggal wajib diisi."
		}
		if in.Amount.Sign() <= 0 {
			f["amount"] = "Nominal harus lebih dari 0."
		}
		if in.CashAccountID == uuid.Nil {
			f["cashAccountId"] = "Pilih rekening kas atau bank."
		}
		if utf8.RuneCountInString(in.Memo) > 500 {
			f["memo"] = "Catatan maksimal 500 karakter."
		}
		if len(f) > 0 {
			return httpx.Unprocessable("refund.invalid", trade.FirstMessage(f)).WithFields(f)
		}
		customer, err := s.Core.Contact(ctx, companyID, in.CustomerID)
		if err != nil {
			return fieldErr(err, "customerId")
		}
		acc, err := s.Cash.Account(ctx, companyID, in.CashAccountID)
		if err != nil {
			return fieldErr(err, "cashAccountId")
		}
		if err := q.LockAdvance(ctx, advanceLock("sales", companyID, in.CustomerID)); err != nil {
			return err
		}
		bal, err := q.CustomerAdvanceBalance(ctx, store.CustomerAdvanceBalanceParams{CompanyID: companyID, CustomerID: in.CustomerID})
		if err != nil {
			return err
		}
		if in.Amount.Cmp(bal) > 0 {
			msg := fmt.Sprintf("Saldo uang muka %s hanya %s.", customer.Name, bal.IDR())
			return httpx.Unprocessable("refund.invalid", msg).WithFields(map[string]string{"amount": msg})
		}
		advanceAcc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.CustomerAdvance)
		if err != nil {
			return err
		}
		number, err := s.Ledger.NextNumber(ctx, companyID, "BKK", in.Date)
		if err != nil {
			return err
		}
		memo := "Pengembalian uang muka " + customer.Name
		id := uuid.Must(uuid.NewV7())
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: in.Date, Memo: memo, SourceType: SourceReceipt, SourceID: &id, Number: number,
			Lines: []ledger.LineDraft{
				{AccountID: advanceAcc.ID, Debit: in.Amount, Memo: memo},
				{AccountID: acc.AccountID, Credit: in.Amount, Memo: memo},
			}, Actor: actor,
		})
		if err != nil {
			return err
		}
		cash := acc.AccountID
		if err := q.InsertReceipt(ctx, store.InsertReceiptParams{
			ID: id, CompanyID: companyID, Number: number, CustomerID: in.CustomerID, ReceiptDate: in.Date, CashAccountID: &cash,
			Amount: in.Amount, Memo: in.Memo, JournalEntryID: posted.ID, CreatedBy: actor.UserRef(),
			Kind: ReceiptKindRefund, FromAdvance: in.Amount,
		}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.advance.refund",
			ObjectType: "sales_receipt", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "amount": in.Amount.String(), "customer": customer.Name},
		}); err != nil {
			return err
		}
		out, err = s.Receipt(ctx, companyID, id)
		return err
	})
	return out, err
}

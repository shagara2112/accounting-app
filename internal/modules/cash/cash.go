// Package cash adalah modul kas dan bank (input manual): rekening kas/bank (CB-01) dan
// penerimaan, pengeluaran, serta transfer antar rekening (CB-02, CB-03). Saldo rekening adalah
// saldo akun buku besarnya, sehingga pelunasan dari modul penjualan dan pembelian ikut terhitung.
package cash

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// SourceType menandai jurnal yang dibuat modul ini di buku besar.
const SourceType = "cash.transaction"

type Account struct {
	AccountID     uuid.UUID    `json:"accountId"`
	Code          string       `json:"code"`
	Name          string       `json:"name"`
	Kind          string       `json:"kind"` // kas atau bank
	BankName      string       `json:"bankName"`
	AccountNumber string       `json:"accountNumber"`
	Active        bool         `json:"active"`
	Balance       money.Amount `json:"balance"`
}

type AccountInput struct {
	AccountID     uuid.UUID `json:"accountId"`
	Kind          string    `json:"kind"`
	BankName      string    `json:"bankName"`
	AccountNumber string    `json:"accountNumber"`
	Active        bool      `json:"active"`
}

type LineInput struct {
	AccountID uuid.UUID    `json:"accountId"`
	Amount    money.Amount `json:"amount"`
	Memo      string       `json:"memo"`
}

// TransactionInput: in dan out memakai baris lawan (mis. beban admin bank, pendapatan bunga);
// transfer memakai ToAccountID dan Amount tanpa baris.
type TransactionInput struct {
	Kind          string       `json:"kind"`
	Date          civil.Date   `json:"date"`
	CashAccountID uuid.UUID    `json:"cashAccountId"`
	ToAccountID   *uuid.UUID   `json:"toAccountId"`
	Amount        money.Amount `json:"amount"`
	Memo          string       `json:"memo"`
	Reference     string       `json:"reference"`
	Lines         []LineInput  `json:"lines"`
}

type Transaction struct {
	ID             uuid.UUID    `json:"id"`
	Number         string       `json:"number"`
	Kind           string       `json:"kind"`
	Date           civil.Date   `json:"date"`
	CashAccountID  uuid.UUID    `json:"cashAccountId"`
	CashAccount    string       `json:"cashAccount"`
	ToAccountID    *uuid.UUID   `json:"toAccountId,omitempty"`
	ToAccount      string       `json:"toAccount,omitempty"`
	Amount         money.Amount `json:"amount"`
	Memo           string       `json:"memo"`
	Reference      string       `json:"reference"`
	Status         string       `json:"status"`
	JournalEntryID uuid.UUID    `json:"journalEntryId"`
}

type Line struct {
	AccountID   uuid.UUID    `json:"accountId"`
	AccountCode string       `json:"accountCode"`
	AccountName string       `json:"accountName"`
	Amount      money.Amount `json:"amount"`
	Memo        string       `json:"memo"`
}

type TransactionDetail struct {
	Transaction
	Lines      []Line  `json:"lines"`
	CreatedBy  *string `json:"createdBy,omitempty"`
	VoidReason *string `json:"voidReason,omitempty"`
}

type Filter struct {
	From, To  civil.Date
	AccountID *uuid.UUID
}

// Service dipakai handler HTTP serta modul penjualan dan pembelian (untuk memeriksa rekening).
type Service interface {
	Accounts(ctx context.Context, companyID uuid.UUID) ([]Account, error)
	// Account memastikan akun terdaftar sebagai rekening kas/bank yang aktif.
	Account(ctx context.Context, companyID, accountID uuid.UUID) (Account, error)
	RegisterAccount(ctx context.Context, companyID uuid.UUID, in AccountInput, actor audit.Actor) (Account, error)
	Transactions(ctx context.Context, companyID uuid.UUID, f Filter) ([]Transaction, error)
	Transaction(ctx context.Context, companyID, id uuid.UUID) (TransactionDetail, error)
	Record(ctx context.Context, companyID uuid.UUID, in TransactionInput, actor audit.Actor) (TransactionDetail, error)
	Void(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (TransactionDetail, error)
}

func New(pool *pgxpool.Pool, l ledger.Service) Service { return &service{pool: pool, ledger: l} }

type service struct {
	pool   *pgxpool.Pool
	ledger ledger.Service
}

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.pool)) }

func (s *service) ledgerAccounts(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]ledger.Account, error) {
	list, err := s.ledger.Accounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	m := make(map[uuid.UUID]ledger.Account, len(list))
	for _, a := range list {
		m[a.ID] = a
	}
	return m, nil
}

func toAccount(r store.CashAccount, la ledger.Account) Account {
	return Account{
		AccountID: r.AccountID, Code: la.Code, Name: la.Name, Kind: r.Kind, BankName: r.BankName,
		AccountNumber: r.AccountNumber, Active: r.Active && la.Active, Balance: la.Balance,
	}
}

func (s *service) Accounts(ctx context.Context, companyID uuid.UUID) ([]Account, error) {
	rows, err := s.q(ctx).ListCashAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	la, err := s.ledgerAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAccount(r, la[r.AccountID]))
	}
	return out, nil
}

func (s *service) Account(ctx context.Context, companyID, accountID uuid.UUID) (Account, error) {
	r, err := s.q(ctx).GetCashAccount(ctx, store.GetCashAccountParams{CompanyID: companyID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, httpx.Unprocessable("cash.not_cash_account", "Akun itu belum terdaftar sebagai rekening kas atau bank.")
	}
	if err != nil {
		return Account{}, err
	}
	la, err := s.ledgerAccounts(ctx, companyID)
	if err != nil {
		return Account{}, err
	}
	a := toAccount(r, la[accountID])
	if !a.Active {
		return Account{}, httpx.Unprocessable("cash.inactive", fmt.Sprintf("Rekening %s %s nonaktif.", a.Code, a.Name))
	}
	return a, nil
}

// RegisterAccount menandai akun aset sebagai rekening kas atau bank, atau mengubah datanya.
func (s *service) RegisterAccount(ctx context.Context, companyID uuid.UUID, in AccountInput, actor audit.Actor) (Account, error) {
	in.BankName, in.AccountNumber = strings.TrimSpace(in.BankName), strings.TrimSpace(in.AccountNumber)
	if in.Kind != "kas" && in.Kind != "bank" {
		return Account{}, httpx.Unprocessable("cash.invalid", "Jenis rekening harus kas atau bank.").WithFields(map[string]string{"kind": "Pilih kas atau bank."})
	}
	var out Account
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		la, err := s.ledgerAccounts(ctx, companyID)
		if err != nil {
			return err
		}
		a, ok := la[in.AccountID]
		switch {
		case !ok:
			return httpx.Unprocessable("cash.invalid", "Akun tidak ditemukan di PT ini.").WithFields(map[string]string{"accountId": "Pilih akun."})
		case a.Class != 1 || !a.Postable:
			msg := fmt.Sprintf("%s %s bukan akun detail aset; rekening kas/bank harus akun berkepala 1.", a.Code, a.Name)
			return httpx.Unprocessable("cash.invalid", msg).WithFields(map[string]string{"accountId": msg})
		}
		r, err := s.q(ctx).UpsertCashAccount(ctx, store.UpsertCashAccountParams{
			CompanyID: companyID, AccountID: in.AccountID, Kind: in.Kind, BankName: in.BankName,
			AccountNumber: in.AccountNumber, Active: in.Active,
		})
		if err != nil {
			return err
		}
		out = toAccount(r, a)
		_, err = audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.account.register",
			ObjectType: "cash_account", ObjectID: in.AccountID.String(), IP: actor.IP,
			After: map[string]any{"code": a.Code, "name": a.Name, "kind": in.Kind, "active": in.Active},
		})
		return err
	})
	return out, err
}

var series = map[string]string{"in": "BKM", "out": "BKK", "transfer": "TRF"}

// Record memposting penerimaan, pengeluaran, atau transfer beserta jurnalnya.
func (s *service) Record(ctx context.Context, companyID uuid.UUID, in TransactionInput, actor audit.Actor) (TransactionDetail, error) {
	in.Memo, in.Reference = strings.TrimSpace(in.Memo), strings.TrimSpace(in.Reference)
	f := map[string]string{}
	if _, ok := series[in.Kind]; !ok {
		f["kind"] = "Pilih penerimaan, pengeluaran, atau transfer."
	}
	if in.Date.IsZero() {
		f["date"] = "Tanggal wajib diisi."
	}
	if utf8.RuneCountInString(in.Memo) > 500 {
		f["memo"] = "Keterangan maksimal 500 karakter."
	}
	if utf8.RuneCountInString(in.Reference) > 100 {
		f["reference"] = "Referensi maksimal 100 karakter."
	}
	var total money.Amount
	switch in.Kind {
	case "transfer":
		if in.ToAccountID == nil {
			f["toAccountId"] = "Pilih rekening tujuan."
		} else if *in.ToAccountID == in.CashAccountID {
			f["toAccountId"] = "Rekening tujuan harus berbeda."
		}
		if in.Amount.Sign() <= 0 {
			f["amount"] = "Nominal transfer harus lebih dari 0."
		}
		total = in.Amount
	case "in", "out":
		if len(in.Lines) == 0 || len(in.Lines) > 100 {
			f["lines"] = "Isi 1–100 baris."
		}
		for i, ln := range in.Lines {
			key := fmt.Sprintf("lines.%d.", i)
			if ln.AccountID == uuid.Nil {
				f[key+"accountId"] = "Pilih akun."
			} else if ln.AccountID == in.CashAccountID {
				f[key+"accountId"] = "Akun lawan tidak boleh rekening yang sama."
			}
			if ln.Amount.Sign() <= 0 {
				f[key+"amount"] = "Nominal harus lebih dari 0."
			}
			if utf8.RuneCountInString(ln.Memo) > 250 {
				f[key+"memo"] = "Keterangan baris maksimal 250 karakter."
			}
			total = total.Add(ln.Amount)
		}
	}
	if len(f) > 0 {
		return TransactionDetail{}, httpx.Unprocessable("cash.invalid", firstMessage(f)).WithFields(f)
	}

	var out TransactionDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		from, err := s.Account(ctx, companyID, in.CashAccountID)
		if err != nil {
			return withField(err, "cashAccountId")
		}
		la, err := s.ledgerAccounts(ctx, companyID)
		if err != nil {
			return err
		}
		lf := map[string]string{}
		for i, ln := range in.Lines {
			a, ok := la[ln.AccountID]
			key := fmt.Sprintf("lines.%d.accountId", i)
			switch {
			case !ok:
				lf[key] = "Akun tidak ditemukan di PT ini."
			case !a.Postable:
				lf[key] = fmt.Sprintf("%s %s adalah akun header; pilih akun detail.", a.Code, a.Name)
			case !a.Active:
				lf[key] = fmt.Sprintf("Akun %s %s nonaktif.", a.Code, a.Name)
			}
		}
		if len(lf) > 0 {
			return httpx.Unprocessable("cash.invalid", firstMessage(lf)).WithFields(lf)
		}
		var drafts []ledger.LineDraft
		switch in.Kind {
		case "transfer":
			to, err := s.Account(ctx, companyID, *in.ToAccountID)
			if err != nil {
				return withField(err, "toAccountId")
			}
			drafts = []ledger.LineDraft{
				{AccountID: to.AccountID, Debit: total, Memo: "Transfer dari " + from.Name},
				{AccountID: from.AccountID, Credit: total, Memo: "Transfer ke " + to.Name},
			}
		case "in":
			drafts = append(drafts, ledger.LineDraft{AccountID: from.AccountID, Debit: total, Memo: in.Memo})
			for _, ln := range in.Lines {
				drafts = append(drafts, ledger.LineDraft{AccountID: ln.AccountID, Credit: ln.Amount, Memo: ln.Memo})
			}
		case "out":
			for _, ln := range in.Lines {
				drafts = append(drafts, ledger.LineDraft{AccountID: ln.AccountID, Debit: ln.Amount, Memo: ln.Memo})
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: from.AccountID, Credit: total, Memo: in.Memo})
		}

		id := uuid.Must(uuid.NewV7())
		number, err := s.ledger.NextNumber(ctx, companyID, series[in.Kind], in.Date)
		if err != nil {
			return err
		}
		posted, err := s.ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: in.Date, Memo: in.Memo, SourceType: SourceType,
			SourceID: &id, Number: number, Lines: drafts, Actor: actor,
		})
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if err := q.InsertTransaction(ctx, store.InsertTransactionParams{
			ID: id, CompanyID: companyID, Number: number, Kind: in.Kind, TrxDate: in.Date,
			CashAccountID: in.CashAccountID, ToAccountID: in.ToAccountID, Amount: total, Memo: in.Memo,
			Reference: in.Reference, JournalEntryID: posted.ID, CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		for i, ln := range in.Lines {
			if in.Kind == "transfer" {
				break
			}
			if err := q.InsertTransactionLine(ctx, store.InsertTransactionLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, TransactionID: id, LineNo: int16(i + 1),
				AccountID: ln.AccountID, Amount: ln.Amount, Memo: strings.TrimSpace(ln.Memo),
			}); err != nil {
				return err
			}
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.transaction.post",
			ObjectType: "cash_transaction", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "kind": in.Kind, "amount": total.String(), "date": in.Date.String()},
		}); err != nil {
			return err
		}
		out, err = s.Transaction(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) Void(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (TransactionDetail, error) {
	var out TransactionDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		q := s.q(ctx)
		t, err := q.GetTransactionForUpdate(ctx, store.GetTransactionForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("cash.not_found", "Transaksi kas tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if t.Status != "posted" {
			return httpx.Conflict("cash.already_void", fmt.Sprintf("%s sudah di-void.", t.Number))
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		v, err := s.ledger.VoidSource(ctx, companyID, t.JournalEntryID, SourceType, date, reason, actor)
		if err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if n, err := q.VoidTransaction(ctx, store.VoidTransactionParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || n != 1 {
			if err == nil {
				err = fmt.Errorf("transaksi %s gagal ditandai void", t.Number)
			}
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.transaction.void",
			ObjectType: "cash_transaction", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": t.Number, "status": "posted"},
			After:  map[string]any{"number": t.Number, "status": "void", "reason": reason, "reversal": v.Reversal.Number},
		}); err != nil {
			return err
		}
		out, err = s.Transaction(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) Transactions(ctx context.Context, companyID uuid.UUID, f Filter) ([]Transaction, error) {
	rows, err := s.q(ctx).ListTransactions(ctx, store.ListTransactionsParams{
		CompanyID: companyID, FromDate: f.From, ToDate: f.To, AccountID: f.AccountID,
	})
	if err != nil {
		return nil, err
	}
	la, err := s.ledgerAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Transaction, len(rows))
	for i, r := range rows {
		out[i] = toTransaction(r, la)
	}
	return out, nil
}

func toTransaction(r store.CashTransaction, la map[uuid.UUID]ledger.Account) Transaction {
	t := Transaction{
		ID: r.ID, Number: r.Number, Kind: r.Kind, Date: r.TrxDate, CashAccountID: r.CashAccountID,
		CashAccount: la[r.CashAccountID].Name, ToAccountID: r.ToAccountID, Amount: r.Amount, Memo: r.Memo,
		Reference: r.Reference, Status: r.Status, JournalEntryID: r.JournalEntryID,
	}
	if r.ToAccountID != nil {
		t.ToAccount = la[*r.ToAccountID].Name
	}
	return t
}

func (s *service) Transaction(ctx context.Context, companyID, id uuid.UUID) (TransactionDetail, error) {
	q := s.q(ctx)
	r, err := q.GetTransaction(ctx, store.GetTransactionParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return TransactionDetail{}, httpx.NotFound("cash.not_found", "Transaksi kas tidak ditemukan.")
	}
	if err != nil {
		return TransactionDetail{}, err
	}
	la, err := s.ledgerAccounts(ctx, companyID)
	if err != nil {
		return TransactionDetail{}, err
	}
	out := TransactionDetail{
		Transaction: toTransaction(store.CashTransaction{
			ID: r.ID, Number: r.Number, Kind: r.Kind, TrxDate: r.TrxDate, CashAccountID: r.CashAccountID,
			ToAccountID: r.ToAccountID, Amount: r.Amount, Memo: r.Memo, Reference: r.Reference, Status: r.Status,
			JournalEntryID: r.JournalEntryID,
		}, la),
		Lines: []Line{}, CreatedBy: r.CreatedByName, VoidReason: r.VoidReason,
	}
	lines, err := q.TransactionLines(ctx, store.TransactionLinesParams{CompanyID: companyID, TransactionID: id})
	if err != nil {
		return TransactionDetail{}, err
	}
	for _, ln := range lines {
		a := la[ln.AccountID]
		out.Lines = append(out.Lines, Line{AccountID: ln.AccountID, AccountCode: a.Code, AccountName: a.Name, Amount: ln.Amount, Memo: ln.Memo})
	}
	return out, nil
}

// withField menambahkan field ke problem yang tidak punya field, supaya formulir bisa menandai isiannya.
func withField(err error, field string) error {
	var p *httpx.Problem
	if errors.As(err, &p) && len(p.Fields) == 0 {
		p.Fields = map[string]string{field: p.Detail}
	}
	return err
}

func firstMessage(f map[string]string) string {
	for _, k := range []string{"kind", "date", "cashAccountId", "toAccountId", "amount", "lines", "memo", "reference"} {
		if m, ok := f[k]; ok {
			return m
		}
	}
	for _, m := range f {
		return m
	}
	return ""
}

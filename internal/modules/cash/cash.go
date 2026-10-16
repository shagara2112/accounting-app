// Package cash adalah modul kas dan bank (input manual): rekening kas/bank (CB-01) dan
// penerimaan, pengeluaran, serta transfer antar rekening (CB-02, CB-03). Saldo rekening adalah
// saldo akun buku besarnya, sehingga pelunasan dari modul penjualan dan pembelian ikut terhitung.
package cash

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash/internal/store"
	"lajur/internal/modules/inventory"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
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

// LineInput adalah satu baris lawan. Di draf, AccountID boleh kosong (uuid nol).
type LineInput struct {
	AccountID uuid.UUID    `json:"accountId"`
	Amount    money.Amount `json:"amount"`
	Memo      string       `json:"memo"`
	ProjectID *uuid.UUID   `json:"projectId"` // mis. biaya proyek yang dibayar tunai (IND-02)
	// DepartmentID memilah baris ini ke departemen tertentu; kosong = departemen kepala transaksi.
	DepartmentID *uuid.UUID `json:"departmentId"`
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
	// Dimensi cabang dan departemen (GL-09), diwariskan ke setiap baris jurnal.
	BranchID     *uuid.UUID `json:"branchId"`
	DepartmentID *uuid.UUID `json:"departmentId"`
}

type Transaction struct {
	ID             uuid.UUID    `json:"id"`
	Number         *string      `json:"number"` // kosong selama draf
	Kind           string       `json:"kind"`
	Date           civil.Date   `json:"date"`
	CashAccountID  uuid.UUID    `json:"cashAccountId"`
	CashAccount    string       `json:"cashAccount"`
	ToAccountID    *uuid.UUID   `json:"toAccountId,omitempty"`
	ToAccount      string       `json:"toAccount,omitempty"`
	Amount         money.Amount `json:"amount"`
	Memo           string       `json:"memo"`
	Reference      string       `json:"reference"`
	Status         string       `json:"status"` // draft, posted, atau void
	JournalEntryID *uuid.UUID   `json:"journalEntryId,omitempty"`
	BranchID       *uuid.UUID   `json:"branchId,omitempty"`
	DepartmentID   *uuid.UUID   `json:"departmentId,omitempty"`
}

type Line struct {
	AccountID    *uuid.UUID   `json:"accountId,omitempty"` // kosong di draf yang belum dilengkapi
	AccountCode  string       `json:"accountCode"`
	AccountName  string       `json:"accountName"`
	Amount       money.Amount `json:"amount"`
	Memo         string       `json:"memo"`
	ProjectID    *uuid.UUID   `json:"projectId,omitempty"`
	DepartmentID *uuid.UUID   `json:"departmentId,omitempty"`
}

type TransactionDetail struct {
	Transaction
	Lines       []Line     `json:"lines"`
	CreatedByID *uuid.UUID `json:"createdById,omitempty"`
	CreatedBy   *string    `json:"createdBy,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	PostedBy    *string    `json:"postedBy,omitempty"`
	PostedAt    *time.Time `json:"postedAt,omitempty"`
	VoidReason  *string    `json:"voidReason,omitempty"`
	Version     int        `json:"version"`
}

type Filter struct {
	From, To   civil.Date
	AccountID  *uuid.UUID
	DraftsOnly bool // semua draf, tanpa batas tanggal
}

// Service dipakai handler HTTP serta modul penjualan dan pembelian (untuk memeriksa rekening).
type Service interface {
	Accounts(ctx context.Context, companyID uuid.UUID) ([]Account, error)
	// Account memastikan akun terdaftar sebagai rekening kas/bank yang aktif.
	Account(ctx context.Context, companyID, accountID uuid.UUID) (Account, error)
	RegisterAccount(ctx context.Context, companyID uuid.UUID, in AccountInput, actor audit.Actor) (Account, error)
	Transactions(ctx context.Context, companyID uuid.UUID, f Filter) ([]Transaction, error)
	Transaction(ctx context.Context, companyID, id uuid.UUID) (TransactionDetail, error)
	// Record langsung memposting transaksi beserta jurnalnya.
	Record(ctx context.Context, companyID uuid.UUID, in TransactionInput, actor audit.Actor) (TransactionDetail, error)
	// SaveDraft membuat (id nil) atau mengubah draf; akun lawan boleh belum diisi. Draf belum
	// bernomor dan belum masuk buku besar.
	SaveDraft(ctx context.Context, companyID uuid.UUID, id *uuid.UUID, in TransactionInput, version int, actor audit.Actor) (TransactionDetail, error)
	PostDraft(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (TransactionDetail, error)
	DeleteDraft(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor, anyDraft bool) error
	DraftSummary(ctx context.Context, companyID uuid.UUID) (int, money.Amount, error)

	// Rekonsiliasi bank (CB-04, CB-05).
	Reconciliations(ctx context.Context, companyID uuid.UUID, accountID *uuid.UUID) ([]Reconciliation, error)
	Reconciliation(ctx context.Context, companyID, id uuid.UUID) (ReconDetail, error)
	StartReconciliation(ctx context.Context, companyID uuid.UUID, in ReconInput, actor audit.Actor) (ReconDetail, error)
	UpdateReconciliation(ctx context.Context, companyID, id uuid.UUID, in ReconInput, actor audit.Actor) (ReconDetail, error)
	SetCleared(ctx context.Context, companyID, id uuid.UUID, lineIDs []uuid.UUID, cleared bool, actor audit.Actor) (ReconDetail, error)
	CloseReconciliation(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (ReconDetail, error)
	ReopenReconciliation(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (ReconDetail, error)
	DeleteReconciliation(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error
	Void(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (TransactionDetail, error)
}

func New(pool *pgxpool.Pool, l ledger.Service, inv inventory.Service) Service {
	return &service{pool: pool, ledger: l, inventory: inv}
}

type service struct {
	pool      *pgxpool.Pool
	ledger    ledger.Service
	inventory inventory.Service
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

// check memeriksa isian transaksi dan mengembalikan totalnya. final = siap diposting: setiap
// baris penerimaan/pengeluaran wajib punya akun lawan. Draf boleh belum punya akun, tetapi akun
// yang sudah diisi tetap diperiksa.
func (s *service) check(ctx context.Context, companyID uuid.UUID, in *TransactionInput, final bool) (money.Amount, error) {
	if err := rbac.CheckBranch(ctx, companyID, in.BranchID); err != nil {
		return money.Amount{}, err
	}
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
		in.Lines = nil
	case "in", "out":
		in.ToAccountID = nil
		if len(in.Lines) == 0 || len(in.Lines) > 100 {
			f["lines"] = "Isi 1–100 baris."
		}
		for i, ln := range in.Lines {
			key := fmt.Sprintf("lines.%d.", i)
			switch {
			case ln.AccountID == uuid.Nil && final:
				f[key+"accountId"] = "Pilih akun lawan sebelum memposting."
			case ln.AccountID != uuid.Nil && ln.AccountID == in.CashAccountID:
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
		return money.Amount{}, httpx.Unprocessable("cash.invalid", firstMessage(f)).WithFields(f)
	}

	if _, err := s.Account(ctx, companyID, in.CashAccountID); err != nil {
		return money.Amount{}, withField(err, "cashAccountId")
	}
	if in.Kind == "transfer" {
		if _, err := s.Account(ctx, companyID, *in.ToAccountID); err != nil {
			return money.Amount{}, withField(err, "toAccountId")
		}
		return total, nil
	}
	la, err := s.ledgerAccounts(ctx, companyID)
	if err != nil {
		return money.Amount{}, err
	}
	invAccounts, err := s.inventory.InventoryAccounts(ctx, companyID)
	if err != nil {
		return money.Amount{}, err
	}
	lf := map[string]string{}
	if err := s.checkLineDepartments(ctx, companyID, in.Lines, lf); err != nil {
		return money.Amount{}, err
	}
	for i, ln := range in.Lines {
		if ln.AccountID == uuid.Nil {
			continue
		}
		a, ok := la[ln.AccountID]
		key := fmt.Sprintf("lines.%d.accountId", i)
		switch {
		case !ok:
			lf[key] = "Akun tidak ditemukan di PT ini."
		case !a.Postable:
			lf[key] = fmt.Sprintf("%s %s adalah akun header; pilih akun detail.", a.Code, a.Name)
		case !a.Active:
			lf[key] = fmt.Sprintf("Akun %s %s nonaktif.", a.Code, a.Name)
		case invAccounts[a.ID]:
			lf[key] = fmt.Sprintf("%s %s adalah akun persediaan; catat pembelian barang lewat tagihan supaya stok ikut bertambah.", a.Code, a.Name)
		}
	}
	if len(lf) > 0 {
		return money.Amount{}, httpx.Unprocessable("cash.invalid", firstMessage(lf)).WithFields(lf)
	}
	return total, nil
}

// checkLineDepartments memastikan departemen per baris milik PT ini dan masih aktif, dengan
// pesan galat di baris kas yang benar (bukan nomor baris jurnalnya).
func (s *service) checkLineDepartments(ctx context.Context, companyID uuid.UUID, lines []LineInput, f map[string]string) error {
	var ids []uuid.UUID
	for _, ln := range lines {
		if ln.DepartmentID != nil {
			ids = append(ids, *ln.DepartmentID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.q(ctx).DepartmentsByID(ctx, store.DepartmentsByIDParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return err
	}
	found := map[uuid.UUID]store.DepartmentsByIDRow{}
	for _, r := range rows {
		found[r.ID] = r
	}
	for i, ln := range lines {
		if ln.DepartmentID == nil {
			continue
		}
		key := fmt.Sprintf("lines.%d.departmentId", i)
		if d, ok := found[*ln.DepartmentID]; !ok {
			f[key] = "Departemen tidak ditemukan di PT ini."
		} else if !d.Active {
			f[key] = fmt.Sprintf("Departemen %s nonaktif.", d.Code)
		}
	}
	return nil
}

// Record memposting penerimaan, pengeluaran, atau transfer beserta jurnalnya sekaligus.
func (s *service) Record(ctx context.Context, companyID uuid.UUID, in TransactionInput, actor audit.Actor) (TransactionDetail, error) {
	var out TransactionDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		total, err := s.check(ctx, companyID, &in, true)
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		if err := s.post(ctx, companyID, id, in, total, actor, false); err != nil {
			return err
		}
		out, err = s.Transaction(ctx, companyID, id)
		return err
	})
	return out, err
}

// post membuat nomor BKM/BKK/TRF dan jurnalnya. Untuk draf, baris disimpan ulang dengan akun
// finalnya lalu transaksinya ditandai diposting.
func (s *service) post(ctx context.Context, companyID, id uuid.UUID, in TransactionInput, total money.Amount, actor audit.Actor, draft bool) error {
	from, err := s.Account(ctx, companyID, in.CashAccountID)
	if err != nil {
		return withField(err, "cashAccountId")
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
			drafts = append(drafts, ledger.LineDraft{AccountID: ln.AccountID, Credit: ln.Amount, Memo: ln.Memo, ProjectID: ln.ProjectID, DepartmentID: ln.DepartmentID})
		}
	case "out":
		for _, ln := range in.Lines {
			drafts = append(drafts, ledger.LineDraft{AccountID: ln.AccountID, Debit: ln.Amount, Memo: ln.Memo, ProjectID: ln.ProjectID, DepartmentID: ln.DepartmentID})
		}
		drafts = append(drafts, ledger.LineDraft{AccountID: from.AccountID, Credit: total, Memo: in.Memo})
	}

	// Cabang kepala berlaku untuk semua baris; departemen kepala hanya untuk baris yang tidak
	// memilih departemennya sendiri (termasuk baris rekening kas).
	for i := range drafts {
		drafts[i].BranchID = in.BranchID
		if drafts[i].DepartmentID == nil {
			drafts[i].DepartmentID = in.DepartmentID
		}
	}
	number, err := s.ledger.NextNumberFor(ctx, companyID, series[in.Kind], in.Date, in.BranchID)
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
	// Transaksi langsung juga disimpan dulu sebagai draf: baris hanya bisa ditulis selama
	// transaksinya draf (dijaga trigger), lalu ditandai diposting.
	q := s.q(ctx)
	if !draft {
		if err := q.InsertDraft(ctx, store.InsertDraftParams{
			ID: id, CompanyID: companyID, Kind: in.Kind, TrxDate: in.Date, CashAccountID: in.CashAccountID,
			ToAccountID: in.ToAccountID, Amount: total, Memo: in.Memo, Reference: in.Reference, CreatedBy: actor.UserRef(),
			BranchID: in.BranchID, DepartmentID: in.DepartmentID,
		}); err != nil {
			return err
		}
	}
	if err := s.writeLines(ctx, companyID, id, in); err != nil {
		return err
	}
	if n, err := q.MarkPosted(ctx, store.MarkPostedParams{
		CompanyID: companyID, ID: id, Number: &number, JournalEntryID: &posted.ID, PostedBy: actor.UserRef(),
	}); err != nil || n != 1 {
		if err == nil {
			err = fmt.Errorf("transaksi %s gagal ditandai diposting", id)
		}
		return err
	}
	_, err = audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
		UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.transaction.post",
		ObjectType: "cash_transaction", ObjectID: id.String(), IP: actor.IP,
		After: map[string]any{"number": number, "kind": in.Kind, "amount": total.String(), "date": in.Date.String(), "fromDraft": draft},
	})
	return err
}

// writeLines mengganti baris transaksi (hanya bisa selama transaksinya draf, dijaga trigger).
func (s *service) writeLines(ctx context.Context, companyID, id uuid.UUID, in TransactionInput) error {
	q := s.q(ctx)
	if err := q.DeleteLines(ctx, store.DeleteLinesParams{CompanyID: companyID, TransactionID: id}); err != nil {
		return err
	}
	for i, ln := range in.Lines {
		var account *uuid.UUID
		if ln.AccountID != uuid.Nil {
			a := ln.AccountID
			account = &a
		}
		if err := q.InsertTransactionLine(ctx, store.InsertTransactionLineParams{
			ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, TransactionID: id, LineNo: int16(i + 1),
			AccountID: account, Amount: ln.Amount, Memo: strings.TrimSpace(ln.Memo), ProjectID: ln.ProjectID, DepartmentID: ln.DepartmentID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// lockDraft mengunci draf. Draf orang lain boleh dilengkapi siapa pun yang boleh mencatat kas
// (itu tujuan draf: staf mencatat, akuntan melengkapi akunnya), tetapi hanya pembuatnya,
// Manager, atau Pemilik yang boleh menghapusnya.
func (s *service) lockDraft(ctx context.Context, companyID, id uuid.UUID) (store.CashTransaction, error) {
	t, err := s.q(ctx).GetTransactionForUpdate(ctx, store.GetTransactionForUpdateParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return t, httpx.NotFound("cash.not_found", "Transaksi kas tidak ditemukan.")
	}
	if err != nil {
		return t, err
	}
	if t.Status != "draft" {
		return t, httpx.Conflict("cash.not_draft", fmt.Sprintf("%s sudah diposting; koreksi lewat void.", deref(t.Number)))
	}
	return t, nil
}

// SaveDraft membuat (id nil) atau mengubah draf tanpa memposting. Akun lawan boleh kosong.
func (s *service) SaveDraft(ctx context.Context, companyID uuid.UUID, id *uuid.UUID, in TransactionInput, version int, actor audit.Actor) (TransactionDetail, error) {
	var out TransactionDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		total, err := s.check(ctx, companyID, &in, false)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		action := "cash.transaction.draft"
		target := uuid.Must(uuid.NewV7())
		if id == nil {
			if err := q.InsertDraft(ctx, store.InsertDraftParams{
				ID: target, CompanyID: companyID, Kind: in.Kind, TrxDate: in.Date, CashAccountID: in.CashAccountID,
				ToAccountID: in.ToAccountID, Amount: total, Memo: in.Memo, Reference: in.Reference, CreatedBy: actor.UserRef(),
				BranchID: in.BranchID, DepartmentID: in.DepartmentID,
			}); err != nil {
				return err
			}
		} else {
			target, action = *id, "cash.transaction.draft_update"
			if _, err := s.lockDraft(ctx, companyID, target); err != nil {
				return err
			}
			n, err := q.UpdateDraft(ctx, store.UpdateDraftParams{
				CompanyID: companyID, ID: target, Version: int32(version), Kind: in.Kind, TrxDate: in.Date,
				CashAccountID: in.CashAccountID, ToAccountID: in.ToAccountID, Amount: total, Memo: in.Memo, Reference: in.Reference,
				BranchID: in.BranchID, DepartmentID: in.DepartmentID,
			})
			if err != nil {
				return err
			}
			if n != 1 {
				return httpx.Conflict("conflict.version", "Draf ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
			}
		}
		if err := s.writeLines(ctx, companyID, target, in); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: action,
			ObjectType: "cash_transaction", ObjectID: target.String(), IP: actor.IP,
			After: map[string]any{"kind": in.Kind, "amount": total.String(), "date": in.Date.String()},
		}); err != nil {
			return err
		}
		out, err = s.Transaction(ctx, companyID, target)
		return err
	})
	return out, err
}

// PostDraft memposting draf yang semua barisnya sudah punya akun lawan.
func (s *service) PostDraft(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (TransactionDetail, error) {
	var out TransactionDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		t, err := s.lockDraft(ctx, companyID, id)
		if err != nil {
			return err
		}
		lines, err := s.q(ctx).TransactionLines(ctx, store.TransactionLinesParams{CompanyID: companyID, TransactionID: id})
		if err != nil {
			return err
		}
		in := TransactionInput{
			Kind: t.Kind, Date: t.TrxDate, CashAccountID: t.CashAccountID, ToAccountID: t.ToAccountID,
			Amount: t.Amount, Memo: t.Memo, Reference: t.Reference, BranchID: t.BranchID, DepartmentID: t.DepartmentID,
		}
		for _, ln := range lines {
			li := LineInput{Amount: ln.Amount, Memo: ln.Memo, ProjectID: ln.ProjectID, DepartmentID: ln.DepartmentID}
			if ln.AccountID != nil {
				li.AccountID = *ln.AccountID
			}
			in.Lines = append(in.Lines, li)
		}
		total, err := s.check(ctx, companyID, &in, true)
		if err != nil {
			return err
		}
		if err := s.post(ctx, companyID, id, in, total, actor, true); err != nil {
			return err
		}
		out, err = s.Transaction(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) DeleteDraft(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor, anyDraft bool) error {
	return db.Run(ctx, s.pool, func(ctx context.Context) error {
		t, err := s.lockDraft(ctx, companyID, id)
		if err != nil {
			return err
		}
		if !anyDraft && (t.CreatedBy == nil || *t.CreatedBy != actor.UserID) {
			return httpx.Forbidden("cash.not_owner", "Draf ini dicatat orang lain; hanya pembuatnya, Manager, atau Pemilik yang boleh menghapusnya.")
		}
		q := s.q(ctx)
		if err := q.DeleteLines(ctx, store.DeleteLinesParams{CompanyID: companyID, TransactionID: id}); err != nil {
			return err
		}
		if _, err := q.DeleteDraft(ctx, store.DeleteDraftParams{CompanyID: companyID, ID: id}); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.transaction.draft_delete",
			ObjectType: "cash_transaction", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"kind": t.Kind, "amount": t.Amount.String(), "date": t.TrxDate.String()},
		})
		return err
	})
}

// DraftSummary adalah jumlah draf yang belum diposting dan totalnya.
func (s *service) DraftSummary(ctx context.Context, companyID uuid.UUID) (int, money.Amount, error) {
	r, err := s.q(ctx).DraftSummary(ctx, companyID)
	return int(r.Drafts), r.Amount, err
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
		switch t.Status {
		case "draft":
			return httpx.Conflict("cash.is_draft", "Draf belum diposting; hapus saja drafnya.")
		case "void":
			return httpx.Conflict("cash.already_void", fmt.Sprintf("%s sudah di-void.", deref(t.Number)))
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		v, err := s.ledger.VoidSource(ctx, companyID, *t.JournalEntryID, SourceType, date, reason, actor)
		if err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if n, err := q.VoidTransaction(ctx, store.VoidTransactionParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || n != 1 {
			if err == nil {
				err = fmt.Errorf("transaksi %s gagal ditandai void", deref(t.Number))
			}
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.transaction.void",
			ObjectType: "cash_transaction", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": deref(t.Number), "status": "posted"},
			After:  map[string]any{"number": deref(t.Number), "status": "void", "reason": reason, "reversal": v.Reversal.Number},
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
		CompanyID: companyID, FromDate: f.From, ToDate: f.To, AccountID: f.AccountID, DraftsOnly: f.DraftsOnly,
		Branches: rbac.BranchesFor(ctx, companyID),
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
		BranchID: r.BranchID, DepartmentID: r.DepartmentID,
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
	if rbac.CheckBranch(ctx, companyID, r.BranchID) != nil {
		return TransactionDetail{}, httpx.NotFound("cash.not_found", "Transaksi kas tidak ditemukan.")
	}
	la, err := s.ledgerAccounts(ctx, companyID)
	if err != nil {
		return TransactionDetail{}, err
	}
	out := TransactionDetail{
		Transaction: toTransaction(store.CashTransaction{
			ID: r.ID, Number: r.Number, Kind: r.Kind, TrxDate: r.TrxDate, CashAccountID: r.CashAccountID,
			ToAccountID: r.ToAccountID, Amount: r.Amount, Memo: r.Memo, Reference: r.Reference, Status: r.Status,
			JournalEntryID: r.JournalEntryID, BranchID: r.BranchID, DepartmentID: r.DepartmentID,
		}, la),
		Lines: []Line{}, CreatedByID: r.CreatedBy, CreatedBy: r.CreatedByName, CreatedAt: r.CreatedAt,
		PostedBy: r.PostedByName, PostedAt: r.PostedAt, VoidReason: r.VoidReason, Version: int(r.Version),
	}
	lines, err := q.TransactionLines(ctx, store.TransactionLinesParams{CompanyID: companyID, TransactionID: id})
	if err != nil {
		return TransactionDetail{}, err
	}
	for _, ln := range lines {
		l := Line{AccountID: ln.AccountID, Amount: ln.Amount, Memo: ln.Memo, ProjectID: ln.ProjectID, DepartmentID: ln.DepartmentID}
		if ln.AccountID != nil {
			a := la[*ln.AccountID]
			l.AccountCode, l.AccountName = a.Code, a.Name
		}
		out.Lines = append(out.Lines, l)
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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

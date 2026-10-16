package cash

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/modules/cash/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/export"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Rekonsiliasi bank manual (CB-04, CB-05). Saldo rekening koran dibandingkan dengan saldo awal
// (saldo koran rekonsiliasi sebelumnya) ditambah baris buku besar yang dicentang; rekonsiliasi
// hanya bisa ditutup bila selisihnya nol. Baris yang tidak dicentang terbawa ke rekonsiliasi
// berikutnya sebagai setoran dalam perjalanan atau penarikan yang belum dicairkan.

type Reconciliation struct {
	ID               uuid.UUID    `json:"id"`
	AccountID        uuid.UUID    `json:"accountId"`
	AccountCode      string       `json:"accountCode"`
	AccountName      string       `json:"accountName"`
	StatementDate    civil.Date   `json:"statementDate"`
	StatementBalance money.Amount `json:"statementBalance"`
	OpeningBalance   money.Amount `json:"openingBalance"`
	Status           string       `json:"status"`
	Items            int          `json:"items"`
	ClosedAt         *time.Time   `json:"closedAt,omitempty"`
	Version          int          `json:"version"`
}

type ReconLine struct {
	ID         uuid.UUID    `json:"id"`
	EntryID    uuid.UUID    `json:"entryId"`
	Date       civil.Date   `json:"date"`
	Number     string       `json:"number"`
	Memo       string       `json:"memo"`
	Debit      money.Amount `json:"debit"`  // uang masuk ke rekening
	Credit     money.Amount `json:"credit"` // uang keluar dari rekening
	SourceType string       `json:"sourceType"`
	Cleared    bool         `json:"cleared"`
}

type ReconDetail struct {
	Reconciliation
	Lines []ReconLine `json:"lines"`
	// Cleared: jumlah baris dicentang (masuk − keluar); Expected = saldo awal + Cleared;
	// Difference = saldo rekening koran − Expected (harus nol untuk ditutup).
	Cleared    money.Amount `json:"cleared"`
	Expected   money.Amount `json:"expected"`
	Difference money.Amount `json:"difference"`
	// Rekap CB-05: saldo buku per tanggal koran, setoran dalam perjalanan (masuk yang belum
	// tercentang), dan penarikan yang belum dicairkan (keluar yang belum tercentang).
	BookBalance money.Amount `json:"bookBalance"`
	InTransit   money.Amount `json:"inTransit"`
	Outstanding money.Amount `json:"outstanding"`
	FirstRecon  bool         `json:"firstRecon"`
}

type ReconInput struct {
	AccountID        uuid.UUID    `json:"accountId"`
	StatementDate    civil.Date   `json:"statementDate"`
	StatementBalance money.Amount `json:"statementBalance"`
	// OpeningBalance hanya dipakai di rekonsiliasi pertama rekening itu.
	OpeningBalance money.Amount `json:"openingBalance"`
	Version        int          `json:"version"`
}

func (s *service) toRecon(ctx context.Context, companyID uuid.UUID, r store.CashReconciliation, items int) (Reconciliation, error) {
	out := Reconciliation{
		ID: r.ID, AccountID: r.AccountID, StatementDate: r.StatementDate, StatementBalance: r.StatementBalance,
		OpeningBalance: r.OpeningBalance, Status: r.Status, Items: items, ClosedAt: r.ClosedAt, Version: int(r.Version),
	}
	la, err := s.ledgerAccounts(ctx, companyID)
	if err != nil {
		return out, err
	}
	out.AccountCode, out.AccountName = la[r.AccountID].Code, la[r.AccountID].Name
	return out, nil
}

func (s *service) Reconciliations(ctx context.Context, companyID uuid.UUID, accountID *uuid.UUID) ([]Reconciliation, error) {
	rows, err := s.q(ctx).ListReconciliations(ctx, store.ListReconciliationsParams{CompanyID: companyID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	out := make([]Reconciliation, 0, len(rows))
	for _, r := range rows {
		rec, err := s.toRecon(ctx, companyID, store.CashReconciliation{
			ID: r.ID, CompanyID: r.CompanyID, AccountID: r.AccountID, StatementDate: r.StatementDate, StatementBalance: r.StatementBalance,
			OpeningBalance: r.OpeningBalance, Status: r.Status, ClosedAt: r.ClosedAt, Version: r.Version,
		}, int(r.Items))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *service) lockRecon(ctx context.Context, companyID, id uuid.UUID) (store.CashReconciliation, error) {
	r, err := s.q(ctx).LockReconciliation(ctx, store.LockReconciliationParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return r, httpx.NotFound("recon.not_found", "Rekonsiliasi tidak ditemukan.")
	}
	return r, err
}

func needOpen(r store.CashReconciliation) error {
	if r.Status != "open" {
		return httpx.Conflict("recon.closed", "Rekonsiliasi ini sudah ditutup. Buka kembali dulu bila perlu diubah.")
	}
	return nil
}

// checkDate: tanggal rekening koran harus setelah rekonsiliasi tertutup terakhir.
func (s *service) checkDate(ctx context.Context, companyID, accountID uuid.UUID, date civil.Date) (*store.CashReconciliation, error) {
	if date.IsZero() {
		return nil, httpx.Unprocessable("validation", "Tanggal rekening koran wajib diisi.").WithFields(map[string]string{"statementDate": "Tanggal rekening koran wajib diisi."})
	}
	prev, err := s.q(ctx).LastClosedReconciliation(ctx, store.LastClosedReconciliationParams{CompanyID: companyID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !date.After(prev.StatementDate) {
		msg := fmt.Sprintf("Harus setelah rekonsiliasi terakhir (%s).", prev.StatementDate.Indonesian())
		return nil, httpx.Unprocessable("validation", msg).WithFields(map[string]string{"statementDate": msg})
	}
	return &prev, nil
}

func (s *service) StartReconciliation(ctx context.Context, companyID uuid.UUID, in ReconInput, actor audit.Actor) (ReconDetail, error) {
	var out ReconDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		if _, err := s.Account(ctx, companyID, in.AccountID); err != nil {
			return err
		}
		prev, err := s.checkDate(ctx, companyID, in.AccountID, in.StatementDate)
		if err != nil {
			return err
		}
		opening := in.OpeningBalance
		if prev != nil {
			opening = prev.StatementBalance
		}
		id := uuid.Must(uuid.NewV7())
		if err := s.q(ctx).InsertReconciliation(ctx, store.InsertReconciliationParams{
			ID: id, CompanyID: companyID, AccountID: in.AccountID, StatementDate: in.StatementDate,
			StatementBalance: in.StatementBalance, OpeningBalance: opening, CreatedBy: actor.UserRef(),
		}); err != nil {
			if db.IsUniqueViolation(err) {
				return httpx.Conflict("recon.open_exists", "Rekening ini masih punya rekonsiliasi terbuka. Selesaikan atau hapus dulu.")
			}
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.recon.start", ObjectType: "reconciliation", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"account": in.AccountID, "statementDate": in.StatementDate.String(), "statementBalance": in.StatementBalance.String(), "opening": opening.String()},
		}); err != nil {
			return err
		}
		out, err = s.Reconciliation(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) Reconciliation(ctx context.Context, companyID, id uuid.UUID) (ReconDetail, error) {
	q := s.q(ctx)
	r, err := q.GetReconciliation(ctx, store.GetReconciliationParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReconDetail{}, httpx.NotFound("recon.not_found", "Rekonsiliasi tidak ditemukan.")
	}
	if err != nil {
		return ReconDetail{}, err
	}
	rows, err := q.ReconCandidates(ctx, store.ReconCandidatesParams{CompanyID: companyID, AccountID: r.AccountID, Until: r.StatementDate, ReconciliationID: r.ID})
	if err != nil {
		return ReconDetail{}, err
	}
	d := ReconDetail{Lines: make([]ReconLine, 0, len(rows))}
	items := 0
	var cleared, inTransit, outstanding money.Amount
	for _, l := range rows {
		memo := l.Memo
		if memo == "" {
			memo = l.EntryMemo
		}
		d.Lines = append(d.Lines, ReconLine{ID: l.ID, EntryID: l.EntryID, Date: l.EntryDate, Number: l.Number, Memo: memo,
			Debit: l.Debit, Credit: l.Credit, SourceType: l.SourceType, Cleared: l.Cleared})
		if l.Cleared {
			items++
			cleared = cleared.Add(l.Debit).Sub(l.Credit)
		} else {
			inTransit, outstanding = inTransit.Add(l.Debit), outstanding.Add(l.Credit)
		}
	}
	if d.Reconciliation, err = s.toRecon(ctx, companyID, r, items); err != nil {
		return ReconDetail{}, err
	}
	book, err := q.BookBalanceAt(ctx, store.BookBalanceAtParams{CompanyID: companyID, AccountID: r.AccountID, EntryDate: r.StatementDate})
	if err != nil {
		return ReconDetail{}, err
	}
	d.Cleared, d.InTransit, d.Outstanding, d.BookBalance = cleared, inTransit, outstanding, book
	d.Expected = r.OpeningBalance.Add(cleared)
	d.Difference = r.StatementBalance.Sub(d.Expected)
	// Rekonsiliasi pertama rekening ini: saldo awalnya diisi pengguna, bukan dari rekonsiliasi sebelumnya.
	if err := db.Conn(ctx, s.pool).QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM cash.reconciliations
		WHERE company_id = $1 AND account_id = $2 AND status = 'closed' AND statement_date < $3)`,
		companyID, r.AccountID, r.StatementDate).Scan(&d.FirstRecon); err != nil {
		return ReconDetail{}, err
	}
	return d, nil
}

func (s *service) UpdateReconciliation(ctx context.Context, companyID, id uuid.UUID, in ReconInput, actor audit.Actor) (ReconDetail, error) {
	var out ReconDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		r, err := s.lockRecon(ctx, companyID, id)
		if err != nil {
			return err
		}
		if err := needOpen(r); err != nil {
			return err
		}
		prev, err := s.checkDate(ctx, companyID, r.AccountID, in.StatementDate)
		if err != nil {
			return err
		}
		opening := in.OpeningBalance
		if prev != nil {
			opening = prev.StatementBalance
		}
		// Baris setelah tanggal koran yang baru tidak lagi relevan.
		if in.StatementDate.Before(r.StatementDate) {
			if _, err := db.Conn(ctx, s.pool).Exec(ctx, `DELETE FROM cash.recon_items ri USING ledger.journal_lines l
				WHERE ri.reconciliation_id = $1 AND l.id = ri.journal_line_id AND l.entry_date > $2`, id, in.StatementDate); err != nil {
				return err
			}
		}
		n, err := s.q(ctx).UpdateReconciliation(ctx, store.UpdateReconciliationParams{
			CompanyID: companyID, ID: id, StatementDate: in.StatementDate, StatementBalance: in.StatementBalance, OpeningBalance: opening, Version: int32(in.Version),
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Rekonsiliasi ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		out, err = s.Reconciliation(ctx, companyID, id)
		return err
	})
	return out, err
}

// SetCleared mencentang atau melepas centang baris buku besar.
func (s *service) SetCleared(ctx context.Context, companyID, id uuid.UUID, lineIDs []uuid.UUID, cleared bool, actor audit.Actor) (ReconDetail, error) {
	var out ReconDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		r, err := s.lockRecon(ctx, companyID, id)
		if err != nil {
			return err
		}
		if err := needOpen(r); err != nil {
			return err
		}
		q := s.q(ctx)
		for _, line := range lineIDs {
			ok, err := q.LineOnAccount(ctx, store.LineOnAccountParams{CompanyID: companyID, ID: line, AccountID: r.AccountID, EntryDate: r.StatementDate})
			if err != nil {
				return err
			}
			if !ok {
				return httpx.Unprocessable("recon.line", "Baris itu bukan mutasi rekening ini sampai tanggal rekening koran.")
			}
			if cleared {
				err = q.ClearLine(ctx, store.ClearLineParams{ReconciliationID: id, CompanyID: companyID, JournalLineID: line})
			} else {
				err = q.UnclearLine(ctx, store.UnclearLineParams{ReconciliationID: id, JournalLineID: line})
			}
			if err != nil {
				return err
			}
		}
		out, err = s.Reconciliation(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) CloseReconciliation(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (ReconDetail, error) {
	var out ReconDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		r, err := s.lockRecon(ctx, companyID, id)
		if err != nil {
			return err
		}
		if err := needOpen(r); err != nil {
			return err
		}
		d, err := s.Reconciliation(ctx, companyID, id)
		if err != nil {
			return err
		}
		if !d.Difference.IsZero() {
			return httpx.Unprocessable("recon.difference", fmt.Sprintf("Masih ada selisih %s. Rekonsiliasi hanya bisa ditutup bila selisihnya nol (CB-04).", d.Difference.IDR()))
		}
		if err := s.q(ctx).SetReconciliationStatus(ctx, store.SetReconciliationStatusParams{CompanyID: companyID, ID: id, Status: "closed", Actor: actor.UserRef()}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.recon.close", ObjectType: "reconciliation", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"statementDate": r.StatementDate.String(), "statementBalance": r.StatementBalance.String(), "items": d.Items},
		}); err != nil {
			return err
		}
		out, err = s.Reconciliation(ctx, companyID, id)
		return err
	})
	return out, err
}

// ReopenReconciliation membuka kembali rekonsiliasi tertutup yang paling akhir di rekeningnya.
func (s *service) ReopenReconciliation(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (ReconDetail, error) {
	var out ReconDetail
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		r, err := s.lockRecon(ctx, companyID, id)
		if err != nil {
			return err
		}
		if r.Status != "closed" {
			return httpx.Conflict("recon.open", "Rekonsiliasi ini masih terbuka.")
		}
		latest, err := s.q(ctx).LatestReconciliation(ctx, store.LatestReconciliationParams{CompanyID: companyID, AccountID: r.AccountID})
		if err != nil {
			return err
		}
		if latest.ID != r.ID {
			return httpx.Conflict("recon.not_latest", "Hanya rekonsiliasi terakhir rekening ini yang bisa dibuka kembali.")
		}
		if err := s.q(ctx).SetReconciliationStatus(ctx, store.SetReconciliationStatusParams{CompanyID: companyID, ID: id, Status: "open"}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.recon.reopen", ObjectType: "reconciliation", ObjectID: id.String(), IP: actor.IP,
		}); err != nil {
			return err
		}
		out, err = s.Reconciliation(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) DeleteReconciliation(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error {
	return db.Run(ctx, s.pool, func(ctx context.Context) error {
		n, err := s.q(ctx).DeleteReconciliation(ctx, store.DeleteReconciliationParams{CompanyID: companyID, ID: id})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("recon.closed", "Hanya rekonsiliasi terbuka yang bisa dihapus.")
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "cash.recon.delete", ObjectType: "reconciliation", ObjectID: id.String(), IP: actor.IP,
		})
		return err
	})
}

// ReconTable: laporan rekonsiliasi bank (CB-05) untuk ekspor XLSX/CSV.
func ReconTable(d ReconDetail) export.Table {
	t := export.Table{
		Title:    "Rekonsiliasi bank " + d.AccountCode + " " + d.AccountName,
		Subtitle: []string{"Rekening koran per " + d.StatementDate.Indonesian()},
		Columns:  []export.Column{{Header: "Keterangan", Width: 46}, {Header: "Tanggal", Width: 12}, {Header: "Nomor", Width: 18}, {Header: "Jumlah", Numeric: true}},
	}
	add := func(label string, a money.Amount, bold bool) {
		t.Rows = append(t.Rows, export.Row{Cells: []export.Cell{export.Text(label), export.Text(""), export.Text(""), export.Num(a)}, Bold: bold})
	}
	add("Saldo menurut buku", d.BookBalance, true)
	t.Rows = append(t.Rows, export.Row{Cells: []export.Cell{export.Text("Dikurangi setoran dalam perjalanan")}})
	for _, l := range d.Lines {
		if !l.Cleared && l.Debit.Sign() > 0 {
			t.Rows = append(t.Rows, export.Row{Indent: 1, Cells: []export.Cell{export.Text(l.Memo), export.Text(l.Date.String()), export.Text(l.Number), export.Num(l.Debit.Neg())}})
		}
	}
	t.Rows = append(t.Rows, export.Row{Cells: []export.Cell{export.Text("Ditambah penarikan yang belum dicairkan")}})
	for _, l := range d.Lines {
		if !l.Cleared && l.Credit.Sign() > 0 {
			t.Rows = append(t.Rows, export.Row{Indent: 1, Cells: []export.Cell{export.Text(l.Memo), export.Text(l.Date.String()), export.Text(l.Number), export.Num(l.Credit)}})
		}
	}
	add("Saldo menurut buku setelah penyesuaian", d.BookBalance.Sub(d.InTransit).Add(d.Outstanding), true)
	add("Saldo menurut rekening koran", d.StatementBalance, true)
	add("Selisih", d.StatementBalance.Sub(d.BookBalance.Sub(d.InTransit).Add(d.Outstanding)), true)
	return t
}

package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Revisi jurnal manual (revisi klien): jurnal umum dan penyesuaian yang diinput manual boleh
// diubah selama periodenya belum dikunci. Nomor jurnal tetap. Versi yang digantikan disimpan
// utuh di ledger.journal_revisions dan audit trail, sehingga auditor tetap bisa menelusuri
// setiap perubahan. Jurnal dari dokumen (faktur, kas, dan lainnya) tetap dikoreksi lewat
// dokumennya, dan periode yang sudah dikunci tetap hanya bisa dikoreksi dengan void.

// ReviseInput adalah isi baru jurnal. Revision adalah versi yang sedang diubah, supaya dua
// orang yang mengubah jurnal yang sama tidak saling menimpa.
type ReviseInput struct {
	Date     civil.Date  `json:"date"`
	Memo     string      `json:"memo"`
	Lines    []LineDraft `json:"lines"`
	Reason   string      `json:"reason"`
	Revision int         `json:"revision"`
}

// Revision adalah satu versi jurnal yang sudah digantikan, beserta siapa yang menggantinya.
type Revision struct {
	Revision  int          `json:"revision"` // 0 = versi saat pertama diposting
	Date      civil.Date   `json:"date"`
	Memo      string       `json:"memo"`
	Total     money.Amount `json:"total"`
	Lines     []EntryLine  `json:"lines"`
	Reason    string       `json:"reason,omitempty"`
	RevisedAt time.Time    `json:"revisedAt"`
	RevisedBy *string      `json:"revisedBy,omitempty"`
}

// revisable menolak jurnal yang memang tidak boleh diubah, apa pun periodenya.
func revisable(ctx context.Context, q *store.Queries, e store.LedgerJournalEntry) error {
	if e.Status != "posted" {
		return httpx.Conflict("journal.edit_void", fmt.Sprintf("Jurnal %s sudah di-void dan tidak bisa diubah.", e.Number))
	}
	switch e.SourceType {
	case SourceManual:
		return nil
	case SourceAutoReversal:
		origin := "jurnal penyesuaiannya"
		if e.ReversalOf != nil {
			if o, err := q.GetEntry(ctx, store.GetEntryParams{CompanyID: e.CompanyID, ID: *e.ReversalOf}); err == nil {
				origin = o.Number
			}
		}
		return httpx.Unprocessable("journal.edit_auto_reversal", fmt.Sprintf(
			"%s dibuat otomatis dari %s. Ubah %s; jurnal pembalik ini ikut berubah.", e.Number, origin, origin))
	case SourceVoid:
		return httpx.Unprocessable("journal.edit_reversal", fmt.Sprintf("%s adalah jurnal pembatalan dan tidak bisa diubah.", e.Number))
	case SourceOpening:
		return httpx.Unprocessable("journal.edit_opening", fmt.Sprintf("%s adalah jurnal saldo awal dari impor; void lalu impor ulang bila perlu koreksi.", e.Number))
	default:
		return httpx.Unprocessable("journal.edit_via_source", fmt.Sprintf(
			"%s berasal dari dokumen %s; koreksi lewat dokumen itu.", e.Number, e.SourceType))
	}
}

// autoReversalOf mencari pembalik otomatis yang masih berlaku milik jurnal penyesuaian e.
func autoReversalOf(ctx context.Context, q *store.Queries, e store.LedgerJournalEntry) (*store.LedgerJournalEntry, error) {
	children, err := q.ReversalsOf(ctx, store.ReversalsOfParams{CompanyID: e.CompanyID, ReversalOf: &e.ID})
	if err != nil {
		return nil, err
	}
	for _, c := range children {
		if c.SourceType == SourceAutoReversal && c.Status == "posted" {
			child, err := q.GetEntryForUpdate(ctx, store.GetEntryForUpdateParams{CompanyID: e.CompanyID, ID: c.ID})
			if err != nil {
				return nil, err
			}
			return &child, nil
		}
	}
	return nil, nil
}

// Revise mengganti isi jurnal manual yang sudah diposting.
func (l *ledger) Revise(ctx context.Context, companyID, entryID uuid.UUID, in ReviseInput, actor Actor) (Ref, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if utf8.RuneCountInString(in.Reason) > maxVoidReason {
		msg := fmt.Sprintf("Alasan perubahan maksimal %d karakter.", maxVoidReason)
		return Ref{}, httpx.Unprocessable("journal.edit_reason", msg).WithFields(map[string]string{"reason": msg})
	}
	var out Ref
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		e, err := q.GetEntryForUpdate(ctx, store.GetEntryForUpdateParams{CompanyID: companyID, ID: entryID})
		if isNoRows(err) {
			return httpx.NotFound("journal.not_found", "Jurnal tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if err := revisable(ctx, q, e); err != nil {
			return err
		}
		if int(e.Revision) != in.Revision {
			return httpx.Conflict("conflict.version", fmt.Sprintf("Jurnal %s baru saja diubah orang lain. Muat ulang lalu coba lagi.", e.Number))
		}
		// Nomor jurnal memuat bulannya, jadi tanggal hanya bisa digeser di bulan yang sama.
		if !in.Date.IsZero() && in.Date.MonthStart() != e.EntryDate.MonthStart() {
			msg := fmt.Sprintf("Tanggal hanya bisa diubah dalam bulan %s. Untuk memindahkan ke bulan lain, void jurnal ini lalu buat jurnal baru.", e.EntryDate.MonthLabel())
			return httpx.Unprocessable("journal.edit_month", msg).WithFields(map[string]string{"date": msg})
		}
		kind := Kind(e.Kind)
		total, err := l.check(ctx, q, entry{EntryDraft: EntryDraft{
			CompanyID: companyID, Kind: kind, Date: in.Date, Memo: in.Memo, SourceType: SourceManual,
			Number: e.Number, Lines: in.Lines, Actor: actor,
		}})
		if err != nil {
			return err
		}
		child, err := autoReversalOf(ctx, q, e)
		if err != nil {
			return err
		}
		if child != nil {
			if err := checkPeriod(ctx, q, companyID, child.EntryDate.MonthStart(), Reversal); err != nil {
				var p *httpx.Problem
				if errors.As(err, &p) {
					p.Detail = fmt.Sprintf("Jurnal pembalik otomatisnya (%s) ikut berubah. %s", child.Number, p.Detail)
				}
				return err
			}
		}

		before, err := l.replace(ctx, q, e, in.Date, in.Memo, total, in.Lines, in.Reason, actor)
		if err != nil {
			return err
		}
		if child != nil {
			if _, err := l.replace(ctx, q, *child, child.EntryDate, child.Memo, total, swap(in.Lines), "Ikut berubah bersama "+e.Number, actor); err != nil {
				return err
			}
		}
		if err := q.SetRevising(ctx, ""); err != nil {
			return err
		}

		out = Ref{ID: e.ID, Number: e.Number}
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.journal.revise",
			ObjectType: "journal_entry", ObjectID: e.ID.String(), IP: actor.IP,
			Before: before,
			After: map[string]any{
				"number": e.Number, "revision": e.Revision + 1, "date": in.Date.String(), "memo": strings.TrimSpace(in.Memo),
				"total": total.String(), "lines": snapshotLines(in.Lines), "reason": in.Reason,
			},
		})
		return err
	})
	return out, err
}

// replace menyimpan versi lama e ke riwayat, lalu mengganti kepala dan seluruh barisnya.
// Mengembalikan ringkasan versi lama untuk audit trail.
func (l *ledger) replace(ctx context.Context, q *store.Queries, e store.LedgerJournalEntry, date civil.Date, memo string,
	total money.Amount, lines []LineDraft, reason string, actor Actor) (map[string]any, error) {
	old, err := q.EntryLines(ctx, store.EntryLinesParams{CompanyID: e.CompanyID, EntryID: e.ID})
	if err != nil {
		return nil, err
	}
	snapshot := make([]EntryLine, len(old))
	for i, ln := range old {
		snapshot[i] = EntryLine{
			LineNo: int(ln.LineNo), AccountID: ln.AccountID, AccountCode: ln.AccountCode, AccountName: ln.AccountName,
			Debit: ln.Debit, Credit: ln.Credit, Memo: ln.Memo, ProjectID: ln.ProjectID,
			BranchID: ln.BranchID, DepartmentID: ln.DepartmentID, BranchCode: deref(ln.BranchCode), DepartmentCode: deref(ln.DepartmentCode),
		}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if err := q.InsertRevision(ctx, store.InsertRevisionParams{
		ID: uuid.Must(uuid.NewV7()), CompanyID: e.CompanyID, EntryID: e.ID, Revision: e.Revision,
		EntryDate: e.EntryDate, Memo: e.Memo, Total: e.Total, Lines: raw, Reason: reason, RevisedBy: actor.UserRef(),
	}); err != nil {
		return nil, err
	}

	if err := q.SetRevising(ctx, e.ID.String()); err != nil {
		return nil, err
	}
	if err := q.DeleteEntryLines(ctx, store.DeleteEntryLinesParams{CompanyID: e.CompanyID, EntryID: e.ID}); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23503" {
			return nil, httpx.Conflict("journal.reconciled", fmt.Sprintf(
				"Baris %s sudah dicentang di rekonsiliasi bank. Hapus centangnya di rekonsiliasi dulu, lalu ubah jurnal ini.", e.Number))
		}
		return nil, err
	}
	n, err := q.ReviseEntry(ctx, store.ReviseEntryParams{
		CompanyID: e.CompanyID, ID: e.ID, EntryDate: date, Memo: strings.TrimSpace(memo), Total: total,
		RevisedBy: actor.UserRef(), Expected: e.Revision,
	})
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, fmt.Errorf("jurnal %s gagal direvisi", e.Number)
	}
	if err := insertLines(ctx, q, e.CompanyID, e.ID, date, lines); err != nil {
		return nil, err
	}
	return map[string]any{
		"number": e.Number, "revision": e.Revision, "date": e.EntryDate.String(), "memo": e.Memo,
		"total": e.Total.String(), "lines": snapshotLinesFrom(snapshot),
	}, nil
}

// Ringkasan baris untuk audit trail: akun dan nominal saja, supaya tetap ringkas.
func snapshotLines(lines []LineDraft) []map[string]string {
	out := make([]map[string]string, len(lines))
	for i, ln := range lines {
		out[i] = map[string]string{"account": ln.AccountID.String(), "debit": ln.Debit.String(), "credit": ln.Credit.String()}
	}
	return out
}

func snapshotLinesFrom(lines []EntryLine) []map[string]string {
	out := make([]map[string]string, len(lines))
	for i, ln := range lines {
		out[i] = map[string]string{"account": ln.AccountCode, "debit": ln.Debit.String(), "credit": ln.Credit.String()}
	}
	return out
}

// editState menjelaskan apakah jurnal bisa diubah sekarang, tanpa mengunci apa pun. Pesannya
// ditampilkan di layar detail jurnal; pemeriksaan sebenarnya tetap terjadi di Revise.
func editState(ctx context.Context, q *store.Queries, e store.GetEntryRow) (bool, string, error) {
	if e.Status != "posted" || e.SourceType != SourceManual {
		return false, "", nil
	}
	allowed := func(month civil.Date, kind Kind) (bool, string, error) {
		status, err := q.PeriodStatus(ctx, store.PeriodStatusParams{CompanyID: e.CompanyID, Month: month})
		if err != nil {
			return false, "", err
		}
		switch {
		case status == "open", status == "adjusting" && kind == Adjustment:
			return true, "", nil
		case status == "adjusting":
			return false, fmt.Sprintf("Periode %s sudah ditutup untuk transaksi; koreksi lewat jurnal penyesuaian atau void.", month.MonthLabel()), nil
		default:
			return false, fmt.Sprintf("Periode %s sudah dikunci; koreksi hanya lewat void.", month.MonthLabel()), nil
		}
	}
	ok, why, err := allowed(e.EntryDate.MonthStart(), Kind(e.Kind))
	if err != nil || !ok {
		return ok, why, err
	}
	if e.AutoReverse {
		children, err := q.ReversalsOf(ctx, store.ReversalsOfParams{CompanyID: e.CompanyID, ReversalOf: &e.ID})
		if err != nil {
			return false, "", err
		}
		for _, c := range children {
			if c.SourceType == SourceAutoReversal && c.Status == "posted" {
				return allowed(c.EntryDate.MonthStart(), Reversal)
			}
		}
	}
	return true, "", nil
}

// Revisions membaca riwayat versi jurnal, terbaru lebih dulu.
func revisions(ctx context.Context, q *store.Queries, companyID, entryID uuid.UUID) ([]Revision, error) {
	rows, err := q.EntryRevisions(ctx, store.EntryRevisionsParams{CompanyID: companyID, EntryID: entryID})
	if err != nil {
		return nil, err
	}
	out := make([]Revision, 0, len(rows))
	for _, r := range rows {
		rv := Revision{Revision: int(r.Revision), Date: r.EntryDate, Memo: r.Memo, Total: r.Total, Reason: r.Reason, RevisedAt: r.RevisedAt, RevisedBy: r.RevisedByName}
		if err := json.Unmarshal(r.Lines, &rv.Lines); err != nil {
			return nil, err
		}
		out = append(out, rv)
	}
	return out, nil
}

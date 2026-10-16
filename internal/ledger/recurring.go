package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Jurnal berulang (GL-08, GL-11): template yang disalin dari jurnal manual yang sudah diposting
// (mis. akrual sewa atau penyusutan bulanan) dan diposting otomatis setiap bulan pada tanggal
// jadwalnya. Jurnal penyesuaian bertanda balik otomatis tetap dibalik awal bulan berikutnya.
// Jadwal yang terlewat (server mati) dikejar satu per satu dengan tanggal aslinya.

type RecurringLine struct {
	LineDraft
	AccountCode string `json:"accountCode"`
	AccountName string `json:"accountName"`
}

type RecurringRun struct {
	Date    civil.Date `json:"date"`
	EntryID uuid.UUID  `json:"entryId"`
	Number  string     `json:"number"`
	Status  string     `json:"status"`
}

type Recurring struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Kind        Kind            `json:"kind"`
	Memo        string          `json:"memo"`
	AutoReverse bool            `json:"autoReverse"`
	DayOfMonth  int             `json:"dayOfMonth"` // 0 = hari terakhir bulan
	NextDate    civil.Date      `json:"nextDate"`
	EndDate     *civil.Date     `json:"endDate,omitempty"`
	Active      bool            `json:"active"`
	LastError   *string         `json:"lastError,omitempty"`
	LastRunAt   *time.Time      `json:"lastRunAt,omitempty"`
	Total       money.Amount    `json:"total"`
	Version     int             `json:"version"`
	Lines       []RecurringLine `json:"lines,omitempty"`
	Runs        []RecurringRun  `json:"runs,omitempty"`
}

// RecurringInput: StartDate menentukan jadwal pertama (bulan dan, bila DayOfMonth 0, akhir bulannya).
type RecurringInput struct {
	EntryID    uuid.UUID   `json:"entryId"` // hanya saat membuat
	Name       string      `json:"name"`
	DayOfMonth int         `json:"dayOfMonth"`
	StartDate  civil.Date  `json:"startDate"`
	EndDate    *civil.Date `json:"endDate"`
	Active     bool        `json:"active"`
	Version    int         `json:"version"`
}

// scheduleOn adalah tanggal jadwal di bulan month.
func scheduleOn(month civil.Date, day int) civil.Date {
	if day == 0 {
		return month.MonthEnd()
	}
	return civil.New(month.Year(), month.Month(), day)
}

func (in *RecurringInput) validate() (civil.Date, error) {
	in.Name = strings.TrimSpace(in.Name)
	f := map[string]string{}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 200 {
		f["name"] = "Nama wajib diisi (maksimal 200 karakter)."
	}
	if in.DayOfMonth < 0 || in.DayOfMonth > 28 {
		f["dayOfMonth"] = "Tanggal 1–28, atau akhir bulan."
	}
	if in.StartDate.IsZero() {
		f["startDate"] = "Isi bulan mulai."
	}
	var next civil.Date
	if len(f) == 0 {
		next = scheduleOn(in.StartDate.MonthStart(), in.DayOfMonth)
		if in.EndDate != nil && in.EndDate.Before(next) {
			f["endDate"] = "Tanggal selesai sebelum jadwal pertama."
		}
	}
	if len(f) > 0 {
		return next, httpx.Unprocessable("recurring.invalid", firstMessage(f)).WithFields(f)
	}
	return next, nil
}

func (l *ledger) CreateRecurring(ctx context.Context, companyID uuid.UUID, in RecurringInput, actor Actor) (Recurring, error) {
	next, err := in.validate()
	if err != nil {
		return Recurring{}, err
	}
	var out Recurring
	err = db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		e, err := q.GetEntry(ctx, store.GetEntryParams{CompanyID: companyID, ID: in.EntryID})
		if isNoRows(err) {
			return httpx.NotFound("journal.not_found", "Jurnal tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if e.SourceType != SourceManual || (e.Kind != string(General) && e.Kind != string(Adjustment)) {
			return httpx.Unprocessable("recurring.source", "Hanya jurnal umum atau penyesuaian yang diinput manual yang bisa dijadikan berulang.")
		}
		lines, err := q.EntryLines(ctx, store.EntryLinesParams{CompanyID: companyID, EntryID: e.ID})
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		if err := q.InsertRecurring(ctx, store.InsertRecurringParams{
			ID: id, CompanyID: companyID, Name: in.Name, Kind: e.Kind, Memo: e.Memo, AutoReverse: e.AutoReverse,
			DayOfMonth: int16(in.DayOfMonth), NextDate: next, EndDate: in.EndDate, SourceEntryID: &e.ID, CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		for i, ln := range lines {
			if err := q.InsertRecurringLine(ctx, store.InsertRecurringLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, RecurringID: id, LineNo: int16(i + 1), AccountID: ln.AccountID,
				Debit: ln.Debit, Credit: ln.Credit, Memo: ln.Memo, ProjectID: ln.ProjectID, BranchID: ln.BranchID, DepartmentID: ln.DepartmentID,
			}); err != nil {
				return err
			}
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "ledger.recurring.create", ObjectType: "recurring_journal", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"name": in.Name, "from": e.Number, "day": in.DayOfMonth, "next": next.String()},
		}); err != nil {
			return err
		}
		out, err = l.RecurringJournal(ctx, companyID, id)
		return err
	})
	return out, err
}

func (l *ledger) UpdateRecurring(ctx context.Context, companyID, id uuid.UUID, in RecurringInput, actor Actor) (Recurring, error) {
	next, err := in.validate()
	if err != nil {
		return Recurring{}, err
	}
	var out Recurring
	err = db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		n, err := q.UpdateRecurring(ctx, store.UpdateRecurringParams{
			CompanyID: companyID, ID: id, Version: int32(in.Version), Name: in.Name, DayOfMonth: int16(in.DayOfMonth),
			NextDate: next, EndDate: in.EndDate, Active: in.Active,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Jurnal berulang ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "ledger.recurring.update", ObjectType: "recurring_journal", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"name": in.Name, "day": in.DayOfMonth, "next": next.String(), "active": in.Active},
		}); err != nil {
			return err
		}
		out, err = l.RecurringJournal(ctx, companyID, id)
		return err
	})
	return out, err
}

func (l *ledger) DeleteRecurring(ctx context.Context, companyID, id uuid.UUID, actor Actor) error {
	return db.Run(ctx, l.pool, func(ctx context.Context) error {
		n, err := l.q(ctx).DeleteRecurring(ctx, store.DeleteRecurringParams{CompanyID: companyID, ID: id})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.NotFound("recurring.not_found", "Jurnal berulang tidak ditemukan.")
		}
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "ledger.recurring.delete", ObjectType: "recurring_journal", ObjectID: id.String(), IP: actor.IP,
		})
		return err
	})
}

func toRecurring(r store.LedgerRecurringJournal) Recurring {
	return Recurring{
		ID: r.ID, Name: r.Name, Kind: Kind(r.Kind), Memo: r.Memo, AutoReverse: r.AutoReverse, DayOfMonth: int(r.DayOfMonth),
		NextDate: r.NextDate, EndDate: r.EndDate, Active: r.Active, LastError: r.LastError, LastRunAt: r.LastRunAt, Version: int(r.Version),
	}
}

func (l *ledger) RecurringJournals(ctx context.Context, companyID uuid.UUID) ([]Recurring, error) {
	rows, err := l.q(ctx).ListRecurring(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Recurring, len(rows))
	for i, r := range rows {
		out[i] = toRecurring(store.LedgerRecurringJournal{
			ID: r.ID, Name: r.Name, Kind: r.Kind, Memo: r.Memo, AutoReverse: r.AutoReverse, DayOfMonth: r.DayOfMonth,
			NextDate: r.NextDate, EndDate: r.EndDate, Active: r.Active, LastError: r.LastError, LastRunAt: r.LastRunAt, Version: r.Version,
		})
		out[i].Total = r.Total
	}
	return out, nil
}

func (l *ledger) RecurringJournal(ctx context.Context, companyID, id uuid.UUID) (Recurring, error) {
	q := l.q(ctx)
	r, err := q.GetRecurring(ctx, store.GetRecurringParams{CompanyID: companyID, ID: id})
	if isNoRows(err) {
		return Recurring{}, httpx.NotFound("recurring.not_found", "Jurnal berulang tidak ditemukan.")
	}
	if err != nil {
		return Recurring{}, err
	}
	out := toRecurring(r)
	lines, err := q.RecurringLines(ctx, store.RecurringLinesParams{CompanyID: companyID, RecurringID: id})
	if err != nil {
		return Recurring{}, err
	}
	for _, ln := range lines {
		out.Lines = append(out.Lines, RecurringLine{
			LineDraft: LineDraft{AccountID: ln.AccountID, Debit: ln.Debit, Credit: ln.Credit, Memo: ln.Memo, ProjectID: ln.ProjectID,
				BranchID: ln.BranchID, DepartmentID: ln.DepartmentID},
			AccountCode: ln.AccountCode, AccountName: ln.AccountName,
		})
		out.Total = out.Total.Add(ln.Debit)
	}
	runs, err := q.RecurringRuns(ctx, id)
	if err != nil {
		return Recurring{}, err
	}
	out.Runs = []RecurringRun{}
	for _, rn := range runs {
		out.Runs = append(out.Runs, RecurringRun{Date: rn.RunDate, EntryID: rn.EntryID, Number: rn.Number, Status: rn.Status})
	}
	return out, nil
}

// RunRecurring memposting semua jadwal yang sudah jatuh tempo sampai today. Mengembalikan
// jumlah jurnal yang diposting; kegagalan dicatat di template dan menghentikan template itu.
func (l *ledger) RunRecurring(ctx context.Context, companyID, id uuid.UUID, today civil.Date, actor Actor) (int, error) {
	posted := 0
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		r, err := q.LockRecurring(ctx, store.LockRecurringParams{CompanyID: companyID, ID: id})
		if isNoRows(err) {
			return httpx.NotFound("recurring.not_found", "Jurnal berulang tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if !r.Active {
			return httpx.Conflict("recurring.inactive", "Jurnal berulang ini nonaktif.")
		}
		lines, err := q.RecurringLines(ctx, store.RecurringLinesParams{CompanyID: companyID, RecurringID: id})
		if err != nil {
			return err
		}
		drafts := make([]LineDraft, len(lines))
		for i, ln := range lines {
			drafts[i] = LineDraft{AccountID: ln.AccountID, Debit: ln.Debit, Credit: ln.Credit, Memo: ln.Memo, ProjectID: ln.ProjectID,
				BranchID: ln.BranchID, DepartmentID: ln.DepartmentID}
		}
		next := r.NextDate
		for !next.After(today) && (r.EndDate == nil || !next.After(*r.EndDate)) {
			memo := r.Memo
			if memo == "" {
				memo = r.Name
			}
			memo = fmt.Sprintf("%s · %s", memo, next.MonthLabel())
			if utf8.RuneCountInString(memo) > maxMemo {
				memo = string([]rune(memo)[:maxMemo])
			}
			var ref Posted
			// Satu savepoint per jadwal: jadwal yang gagal (mis. periode terkunci) tidak
			// membatalkan jadwal sebelumnya yang sudah berhasil.
			err := db.Savepoint(ctx, l.pool, func(ctx context.Context) error {
				var err error
				ref, err = l.Post(ctx, EntryDraft{
					CompanyID: companyID, Kind: Kind(r.Kind), Date: next, Memo: memo, AutoReverse: r.AutoReverse,
					SourceType: SourceManual, SourceID: &r.ID, Lines: drafts, Actor: actor,
				})
				if err != nil {
					return err
				}
				n, err := q.InsertRecurringRun(ctx, store.InsertRecurringRunParams{RecurringID: r.ID, RunDate: next, EntryID: ref.ID})
				if err == nil && n != 1 {
					err = fmt.Errorf("jadwal %s sudah diposting", next)
				}
				return err
			})
			if err != nil {
				msg := err.Error()
				var p *httpx.Problem
				if errors.As(err, &p) {
					msg = p.Detail
				}
				msg = fmt.Sprintf("Jadwal %s gagal diposting: %s", next.Indonesian(), msg)
				return q.FailRecurring(ctx, store.FailRecurringParams{ID: r.ID, LastError: &msg})
			}
			posted++
			next = scheduleOn(next.MonthStart().AddMonths(1), int(r.DayOfMonth))
			if err := q.AdvanceRecurring(ctx, store.AdvanceRecurringParams{ID: r.ID, NextDate: next}); err != nil {
				return err
			}
		}
		return nil
	})
	return posted, err
}

// RunDueRecurring dijalankan penjadwal: memposting jadwal jatuh tempo semua PT atas nama
// pembuat templatenya.
func (l *ledger) RunDueRecurring(ctx context.Context, today civil.Date) (int, error) {
	due, err := l.q(ctx).DueRecurring(ctx, today)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, d := range due {
		actor := audit.Actor{IP: "penjadwal"}
		if d.CreatedBy != nil {
			actor.UserID = *d.CreatedBy
		}
		n, err := l.RunRecurring(ctx, d.CompanyID, d.ID, today, actor)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			var p *httpx.Problem
			if !errors.As(err, &p) {
				return total, err
			}
		}
		total += n
	}
	return total, nil
}

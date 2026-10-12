package ledger

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
)

// Urutan status tutup buku dua tahap; bergerak mundur berarti membuka kembali.
var statusOrder = map[string]int{"open": 0, "adjusting": 1, "locked": 2}

func (l *ledger) Settings(ctx context.Context, companyID uuid.UUID) (Settings, error) {
	m, err := l.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return Settings{}, err
	}
	n, err := l.q(ctx).CountEntries(ctx, companyID)
	if err != nil {
		return Settings{}, err
	}
	return Settings{FiscalYearStartMonth: m, HasEntries: n > 0}, nil
}

// SetFiscalYearStart mengatur bulan awal tahun buku (GL-05). Hanya bisa sebelum ada jurnal,
// karena mengubahnya belakangan menggeser saldo laba rugi tahun berjalan.
func (l *ledger) SetFiscalYearStart(ctx context.Context, companyID uuid.UUID, month int, actor Actor) error {
	if month < 1 || month > 12 {
		return httpx.Unprocessable("settings.invalid", "Bulan awal tahun buku harus 1–12.")
	}
	return db.Run(ctx, l.pool, func(ctx context.Context) error {
		cur, err := l.Settings(ctx, companyID)
		if err != nil {
			return err
		}
		if cur.FiscalYearStartMonth == month {
			return nil
		}
		if cur.HasEntries {
			return httpx.Unprocessable("settings.has_entries", "Tahun buku hanya bisa diubah sebelum ada jurnal.")
		}
		if err := l.q(ctx).UpsertSettings(ctx, store.UpsertSettingsParams{
			CompanyID: companyID, FiscalYearStartMonth: int16(month), UpdatedBy: userRef(actor),
		}); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.settings.update",
			ObjectType: "company", ObjectID: companyID.String(), IP: actor.IP,
			Before: map[string]any{"fiscalYearStartMonth": cur.FiscalYearStartMonth},
			After:  map[string]any{"fiscalYearStartMonth": month},
		})
		return err
	})
}

// Periods mengembalikan 12 periode tahun buku yang dimulai di tahun kalender year.
func (l *ledger) Periods(ctx context.Context, companyID uuid.UUID, year int) (FiscalYear, error) {
	if year < 2000 || year > 2100 {
		return FiscalYear{}, httpx.BadRequest("period.year", "Tahun tidak valid.")
	}
	startMonth, err := l.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return FiscalYear{}, err
	}
	from := civil.New(year, time.Month(startMonth), 1)
	to := from.AddMonths(12)
	q := l.q(ctx)
	rows, err := q.ListPeriods(ctx, store.ListPeriodsParams{CompanyID: companyID, FromMonth: from, ToMonth: to.AddMonths(-1)})
	if err != nil {
		return FiscalYear{}, err
	}
	counts, err := q.EntryCountsByMonth(ctx, store.EntryCountsByMonthParams{CompanyID: companyID, FromDate: from, ToDate: to})
	if err != nil {
		return FiscalYear{}, err
	}

	out := FiscalYear{Year: year, StartMonth: startMonth, Periods: make([]Period, 12)}
	index := map[string]int{}
	for i := range out.Periods {
		m := from.AddMonths(i)
		out.Periods[i] = Period{Month: m.MonthKey(), Status: "open"}
		index[m.MonthKey()] = i
	}
	for _, r := range rows {
		p := &out.Periods[index[r.Month.MonthKey()]]
		p.Status, p.ChangedAt, p.ChangedBy, p.Reason = r.Status, r.ChangedAt, r.ChangedByName, r.Reason
	}
	for _, c := range counts {
		p := &out.Periods[index[c.Month.MonthKey()]]
		if Kind(c.Kind) == Adjustment {
			p.Adjustments += c.Entries
		} else {
			p.Entries += c.Entries
		}
	}
	return out, nil
}

// SetPeriodStatus memindahkan periode: open → adjusting (tutup untuk transaksi) → locked
// (kunci final). Membuka kembali wajib disertai alasan, dan semuanya tercatat di audit trail.
func (l *ledger) SetPeriodStatus(ctx context.Context, companyID uuid.UUID, month civil.Date, status, reason string, actor Actor) error {
	to, ok := statusOrder[status]
	if !ok {
		return httpx.BadRequest("period.status", "Status periode harus open, adjusting, atau locked.")
	}
	month = month.MonthStart()
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 300 {
		return httpx.Unprocessable("period.reason", "Alasan maksimal 300 karakter.")
	}
	return db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		if err := q.EnsurePeriod(ctx, store.EnsurePeriodParams{CompanyID: companyID, Month: month}); err != nil {
			return err
		}
		cur, err := q.LockPeriodForUpdate(ctx, store.LockPeriodForUpdateParams{CompanyID: companyID, Month: month})
		if err != nil {
			return err
		}
		if cur == status {
			return nil
		}
		if to < statusOrder[cur] && utf8.RuneCountInString(reason) < 5 {
			msg := fmt.Sprintf("Tulis alasan membuka kembali periode %s (minimal 5 karakter).", month.MonthLabel())
			return httpx.Unprocessable("period.reason_required", msg).WithFields(map[string]string{"reason": msg})
		}
		var reasonPtr *string
		if reason != "" {
			reasonPtr = &reason
		}
		if err := q.SetPeriodStatus(ctx, store.SetPeriodStatusParams{
			CompanyID: companyID, Month: month, Status: status, ChangedBy: userRef(actor), Reason: reasonPtr,
		}); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.period.status",
			ObjectType: "period", ObjectID: month.MonthKey(), IP: actor.IP,
			Before: map[string]any{"status": cur},
			After:  map[string]any{"status": status, "reason": reason},
		})
		return err
	})
}

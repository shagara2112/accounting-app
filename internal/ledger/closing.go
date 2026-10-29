package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Reklas laba akhir tahun (GL-10). Jurnal penutup bertanggal hari terakhir tahun buku membalik
// saldo setiap akun laba rugi selama tahun itu dan memindahkan selisihnya ke laba ditahan.
// Laporan memperlakukannya seperti tutup buku: laba rugi, arus kas, anggaran, dan neraca lajur
// tahun itu tidak memakai jurnal penutupnya sendiri, sehingga laba tahun berjalan per akhir tahun
// tetap tampil; mulai tahun buku berikutnya laba itu sudah menjadi saldo laba ditahan.
//
// Reklas tidak mengunci apa pun. Selama bulan terakhir tahun buku belum dikunci final, revisi
// tetap boleh masuk; reklas lalu ditandai berubah dan cukup diperbarui (jurnal lama di-void,
// jurnal baru diposting, keduanya di tanggal akhir tahun buku), atau dibatalkan.

const (
	seriesClosing = "TB"

	YearEmpty   = "kosong"
	YearOpen    = "belum"
	YearClosed  = "sudah"
	YearChanged = "berubah"
)

// yearRange mengembalikan tanggal awal dan akhir tahun buku yang dimulai di tahun kalender year.
func (l *ledger) yearRange(ctx context.Context, companyID uuid.UUID, year int) (civil.Date, civil.Date, error) {
	if year < 2000 || year > 2100 {
		return civil.Date{}, civil.Date{}, httpx.BadRequest("period.year", "Tahun tidak valid.")
	}
	startMonth, err := l.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return civil.Date{}, civil.Date{}, err
	}
	from := civil.New(year, time.Month(startMonth), 1)
	return from, from.AddMonths(12).AddDays(-1), nil
}

func (l *ledger) YearEnd(ctx context.Context, companyID uuid.UUID, year int) (YearEnd, error) {
	from, to, err := l.yearRange(ctx, companyID, year)
	if err != nil {
		return YearEnd{}, err
	}
	q := l.q(ctx)
	out := YearEnd{FiscalYear: year, From: from, To: to, Ended: !l.today().Before(to), PeriodStatus: "open"}
	periods, err := q.ListPeriods(ctx, store.ListPeriodsParams{CompanyID: companyID, FromMonth: to.MonthStart(), ToMonth: to.MonthStart()})
	if err != nil {
		return YearEnd{}, err
	}
	for _, p := range periods {
		out.PeriodStatus = p.Status
	}

	result, err := q.YearResultByAccount(ctx, store.YearResultByAccountParams{CompanyID: companyID, FromMonth: from, ToMonth: to.MonthStart()})
	if err != nil {
		return YearEnd{}, err
	}
	for _, r := range result {
		out.Profit = out.Profit.Sub(r.Net)
	}
	residual, err := q.YearResultByAccount(ctx, store.YearResultByAccountParams{CompanyID: companyID, FromMonth: from, ToMonth: to.MonthStart(), WithClosing: true})
	if err != nil {
		return YearEnd{}, err
	}
	c, err := q.ActiveClosing(ctx, store.ActiveClosingParams{CompanyID: companyID, EntryDate: to})
	switch {
	case isNoRows(err):
		if len(result) == 0 {
			out.Status = YearEmpty
		} else {
			out.Status = YearOpen
		}
	case err != nil:
		return YearEnd{}, err
	default:
		out.Closing = &ClosingEntry{Ref: Ref{ID: c.ID, Number: c.Number}, Profit: c.Profit, PostedAt: c.PostedAt}
		if c.PostedByName != nil {
			out.Closing.PostedByName = *c.PostedByName
		}
		out.Status = YearClosed
		if len(residual) > 0 {
			out.Status = YearChanged
		}
	}

	if a, err := l.RoleAccount(ctx, companyID, RetainedEarnings); err == nil {
		out.Account, out.AccountName = &Ref{ID: a.ID, Number: a.Code}, a.Name
	} else {
		var p *httpx.Problem
		if !errors.As(err, &p) {
			return YearEnd{}, err
		}
		out.AccountProblem = p.Detail
	}
	return out, nil
}

// CloseYear memposting reklas tahun buku, atau menggantinya bila buku tahun itu berubah sesudah
// reklas terakhir.
func (l *ledger) CloseYear(ctx context.Context, companyID uuid.UUID, year int, actor Actor) (YearEnd, error) {
	var out YearEnd
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		cur, err := l.lockYear(ctx, companyID, year)
		if err != nil {
			return err
		}
		switch {
		case !cur.Ended:
			return httpx.Unprocessable("year_end.not_ended", fmt.Sprintf(
				"Tahun buku %s belum berakhir (sampai %s).", yearLabel(cur), cur.To.Indonesian()))
		case cur.AccountProblem != "":
			return httpx.Unprocessable("ledger.role_account", cur.AccountProblem)
		case cur.Status == YearEmpty:
			return httpx.Unprocessable("year_end.empty", fmt.Sprintf(
				"Belum ada pendapatan atau beban di tahun buku %s, jadi tidak ada yang direklas.", yearLabel(cur)))
		case cur.Status == YearClosed:
			return httpx.Conflict("year_end.closed", fmt.Sprintf(
				"Laba tahun buku %s sudah direklas (%s) dan masih sesuai dengan buku.", yearLabel(cur), cur.Closing.Number))
		}

		var replaced string
		if cur.Closing != nil {
			replaced = cur.Closing.Number
			reason := fmt.Sprintf("Diperbarui: buku tahun %s berubah sesudah reklas", yearLabel(cur))
			if _, err := l.VoidSource(ctx, companyID, cur.Closing.ID, SourceClosing, cur.To, reason, actor); err != nil {
				return err
			}
		}
		q := l.q(ctx)
		result, err := q.YearResultByAccount(ctx, store.YearResultByAccountParams{CompanyID: companyID, FromMonth: cur.From, ToMonth: cur.To.MonthStart()})
		if err != nil {
			return err
		}
		lines := make([]LineDraft, 0, len(result)+1)
		var profit money.Amount
		for _, r := range result {
			ln := LineDraft{AccountID: r.AccountID}
			if r.Net.Sign() > 0 {
				ln.Credit = r.Net
			} else {
				ln.Debit = r.Net.Neg()
			}
			lines = append(lines, ln)
			profit = profit.Sub(r.Net)
		}
		switch profit.Sign() {
		case 1:
			lines = append(lines, LineDraft{AccountID: cur.Account.ID, Credit: profit})
		case -1:
			lines = append(lines, LineDraft{AccountID: cur.Account.ID, Debit: profit.Neg()})
		}
		ref, err := l.post(ctx, entry{
			EntryDraft: EntryDraft{
				CompanyID: companyID, Kind: Closing, Date: cur.To, SourceType: SourceClosing, Lines: lines, Actor: actor,
				Memo: fmt.Sprintf("Reklas laba (rugi) tahun buku %s ke laba ditahan", yearLabel(cur)),
			},
			series: seriesClosing,
		})
		if err != nil {
			return err
		}
		if out, err = l.YearEnd(ctx, companyID, year); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.year_end.close",
			ObjectType: "fiscal_year", ObjectID: fmt.Sprint(year), IP: actor.IP,
			After: map[string]any{"number": ref.Number, "profit": profit.String(), "replaces": replaced},
		})
		return err
	})
	return out, err
}

// ReopenYear membatalkan reklas tahun buku. Jurnal pembaliknya bertanggal akhir tahun buku yang
// sama, sehingga laporan tahun-tahun berikutnya kembali seperti sebelum reklas.
func (l *ledger) ReopenYear(ctx context.Context, companyID uuid.UUID, year int, reason string, actor Actor) (YearEnd, error) {
	var out YearEnd
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		cur, err := l.lockYear(ctx, companyID, year)
		if err != nil {
			return err
		}
		if cur.Closing == nil {
			return httpx.Conflict("year_end.not_closed", fmt.Sprintf("Laba tahun buku %s belum direklas.", yearLabel(cur)))
		}
		if _, err := l.VoidSource(ctx, companyID, cur.Closing.ID, SourceClosing, cur.To, reason, actor); err != nil {
			return err
		}
		if out, err = l.YearEnd(ctx, companyID, year); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.year_end.reopen",
			ObjectType: "fiscal_year", ObjectID: fmt.Sprint(year), IP: actor.IP,
			Before: map[string]any{"number": cur.Closing.Number, "profit": cur.Closing.Profit.String()},
			After:  map[string]any{"reason": reason},
		})
		return err
	})
	return out, err
}

// lockYear mengunci periode terakhir tahun buku (supaya reklas yang sama tidak berjalan dua kali
// bersamaan) lalu membaca statusnya. Periode yang sudah dikunci final menolak perubahan reklas.
func (l *ledger) lockYear(ctx context.Context, companyID uuid.UUID, year int) (YearEnd, error) {
	_, to, err := l.yearRange(ctx, companyID, year)
	if err != nil {
		return YearEnd{}, err
	}
	q := l.q(ctx)
	if err := q.EnsurePeriod(ctx, store.EnsurePeriodParams{CompanyID: companyID, Month: to.MonthStart()}); err != nil {
		return YearEnd{}, err
	}
	status, err := q.LockPeriodForUpdate(ctx, store.LockPeriodForUpdateParams{CompanyID: companyID, Month: to.MonthStart()})
	if err != nil {
		return YearEnd{}, err
	}
	if status == "locked" {
		return YearEnd{}, httpx.Unprocessable("period.locked", fmt.Sprintf(
			"Periode %s sudah dikunci final. Buka dulu periode itu untuk mengubah reklas akhir tahun.", to.MonthLabel()))
	}
	return l.YearEnd(ctx, companyID, year)
}

// yearLabel: "2026" untuk tahun buku Januari–Desember, "2026/27" bila melewati tahun kalender.
func yearLabel(y YearEnd) string {
	if y.From.Year() == y.To.Year() {
		return fmt.Sprint(y.From.Year())
	}
	return fmt.Sprintf("%d/%02d", y.From.Year(), y.To.Year()%100)
}

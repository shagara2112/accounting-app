package ledger

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const (
	defaultPage    = 50
	maxPage        = 200
	maxLedgerLines = 5000
)

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Entries mengembalikan daftar jurnal terbaru lebih dulu, dengan pagination kursor.
func (l *ledger) Entries(ctx context.Context, companyID uuid.UUID, f EntryFilter) (EntryPage, error) {
	if f.From.IsZero() || f.To.IsZero() || f.To.Before(f.From) {
		return EntryPage{}, httpx.BadRequest("journal.range", "Rentang tanggal tidak valid.")
	}
	limit := f.Limit
	if limit <= 0 || limit > maxPage {
		limit = defaultPage
	}
	p := store.ListEntriesParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, RowLimit: int32(limit + 1)}
	if f.Kind != "" {
		p.Kind = &f.Kind
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		s = likeEscaper.Replace(s)
		p.Search = &s
	}
	if f.Cursor != "" {
		d, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return EntryPage{}, httpx.BadRequest("journal.cursor", "Kursor halaman tidak valid.")
		}
		p.CursorDate, p.CursorID = &d, &id
	}
	rows, err := l.q(ctx).ListEntries(ctx, p)
	if err != nil {
		return EntryPage{}, err
	}
	out := EntryPage{Items: make([]EntrySummary, 0, len(rows))}
	for i, r := range rows {
		if i == limit {
			last := out.Items[limit-1]
			out.NextCursor = encodeCursor(last.Date, last.ID)
			break
		}
		out.Items = append(out.Items, EntrySummary{
			ID: r.ID, Number: r.Number, Kind: Kind(r.Kind), Date: r.EntryDate, Memo: r.Memo,
			SourceType: r.SourceType, Total: r.Total, Status: r.Status, ReversalOf: r.ReversalOf,
		})
	}
	return out, nil
}

func (l *ledger) EntriesBySource(ctx context.Context, companyID uuid.UUID, sourceType string) ([]EntrySummary, error) {
	rows, err := l.q(ctx).EntriesBySource(ctx, store.EntriesBySourceParams{CompanyID: companyID, SourceType: sourceType})
	if err != nil {
		return nil, err
	}
	out := make([]EntrySummary, len(rows))
	for i, r := range rows {
		out[i] = EntrySummary{
			ID: r.ID, Number: r.Number, Kind: Kind(r.Kind), Date: r.EntryDate, Memo: r.Memo, SourceType: r.SourceType,
			SourceID: r.SourceID, Total: r.Total, Status: r.Status, ReversalOf: r.ReversalOf,
		}
	}
	return out, nil
}

func (l *ledger) AccountNet(ctx context.Context, companyID, accountID uuid.UUID) (money.Amount, error) {
	return l.q(ctx).AccountNetAllTime(ctx, store.AccountNetAllTimeParams{CompanyID: companyID, AccountID: accountID})
}

func encodeCursor(d civil.Date, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(d.String() + "|" + id.String()))
}

func decodeCursor(s string) (civil.Date, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return civil.Date{}, uuid.Nil, err
	}
	ds, ids, ok := strings.Cut(string(b), "|")
	if !ok {
		return civil.Date{}, uuid.Nil, fmt.Errorf("kursor rusak")
	}
	d, err := civil.Parse(ds)
	if err != nil {
		return civil.Date{}, uuid.Nil, err
	}
	id, err := uuid.Parse(ids)
	return d, id, err
}

// Entry mengembalikan satu jurnal lengkap dengan baris dan jurnal yang terkait.
func (l *ledger) Entry(ctx context.Context, companyID, entryID uuid.UUID) (EntryDetail, error) {
	q := l.q(ctx)
	e, err := q.GetEntry(ctx, store.GetEntryParams{CompanyID: companyID, ID: entryID})
	if isNoRows(err) {
		return EntryDetail{}, httpx.NotFound("journal.not_found", "Jurnal tidak ditemukan.")
	}
	if err != nil {
		return EntryDetail{}, err
	}
	out := EntryDetail{
		EntrySummary: EntrySummary{
			ID: e.ID, Number: e.Number, Kind: Kind(e.Kind), Date: e.EntryDate, Memo: e.Memo,
			SourceType: e.SourceType, SourceID: e.SourceID, Total: e.Total, Status: e.Status, ReversalOf: e.ReversalOf,
		},
		AutoReverse: e.AutoReverse, PostedAt: e.PostedAt, PostedBy: e.PostedByName,
		VoidedAt: e.VoidedAt, VoidedBy: e.VoidedByName, VoidReason: e.VoidReason,
		Reversals: []RelatedEntry{},
		Revision:  int(e.Revision), RevisedAt: e.RevisedAt, RevisedBy: e.RevisedByName,
	}
	if out.Editable, out.EditBlock, err = editState(ctx, q, e); err != nil {
		return EntryDetail{}, err
	}
	if out.Revisions, err = revisions(ctx, q, companyID, entryID); err != nil {
		return EntryDetail{}, err
	}
	lines, err := q.EntryLines(ctx, store.EntryLinesParams{CompanyID: companyID, EntryID: entryID})
	if err != nil {
		return EntryDetail{}, err
	}
	for _, ln := range lines {
		out.Lines = append(out.Lines, EntryLine{
			LineNo: int(ln.LineNo), AccountID: ln.AccountID, AccountCode: ln.AccountCode, AccountName: ln.AccountName,
			Debit: ln.Debit, Credit: ln.Credit, Memo: ln.Memo, ProjectID: ln.ProjectID,
			BranchID: ln.BranchID, DepartmentID: ln.DepartmentID, BranchCode: deref(ln.BranchCode), DepartmentCode: deref(ln.DepartmentCode),
		})
	}
	if e.ReversalOf != nil {
		o, err := q.GetEntry(ctx, store.GetEntryParams{CompanyID: companyID, ID: *e.ReversalOf})
		if err != nil {
			return EntryDetail{}, err
		}
		out.ReversalOfRef = &Ref{ID: o.ID, Number: o.Number}
	}
	rel, err := q.ReversalsOf(ctx, store.ReversalsOfParams{CompanyID: companyID, ReversalOf: &entryID})
	if err != nil {
		return EntryDetail{}, err
	}
	for _, r := range rel {
		out.Reversals = append(out.Reversals, RelatedEntry{
			Ref: Ref{ID: r.ID, Number: r.Number}, SourceType: r.SourceType, Date: r.EntryDate, Status: r.Status,
		})
	}
	return out, nil
}

// Worksheet menyusun neraca lajur bulan month dari saldo agregat, tanpa menjumlah ulang
// baris jurnal. Kolom penyesuaian berisi jurnal penyesuaian bertanggal di bulan itu.
func (l *ledger) Worksheet(ctx context.Context, companyID uuid.UUID, month civil.Date) (Worksheet, error) {
	month = month.MonthStart()
	startMonth, err := l.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return Worksheet{}, err
	}
	fs := fiscalStart(startMonth, month)
	q := l.q(ctx)
	rows, err := q.WorksheetRows(ctx, store.WorksheetRowsParams{CompanyID: companyID, Month: month, FiscalStart: fs})
	if err != nil {
		return Worksheet{}, err
	}
	prior, err := q.PriorYearsResult(ctx, store.PriorYearsResultParams{CompanyID: companyID, FiscalStart: fs})
	if err != nil {
		return Worksheet{}, err
	}

	out := Worksheet{Month: month.MonthKey(), FiscalStart: fs, MonthEnd: month.MonthEnd(), Rows: []WorksheetRow{}}
	add := func(r WorksheetRow) {
		out.Rows = append(out.Rows, r)
		t := &out.Totals
		for _, p := range []struct{ sum, v *Pair }{
			{&t.Unadjusted, &r.Unadjusted}, {&t.Adjustment, &r.Adjustment}, {&t.Adjusted, &r.Adjusted},
			{&t.ProfitLoss, &r.ProfitLoss}, {&t.BalanceSheet, &r.BalanceSheet},
		} {
			p.sum.Debit = p.sum.Debit.Add(p.v.Debit)
			p.sum.Credit = p.sum.Credit.Add(p.v.Credit)
		}
	}
	priorAdded := prior.IsZero()
	for _, r := range rows {
		if !priorAdded && r.Class >= 4 {
			add(priorRow(prior))
			priorAdded = true
		}
		adjusted := r.Unadjusted.Add(r.AdjDebit).Sub(r.AdjCredit)
		if r.Unadjusted.IsZero() && r.AdjDebit.IsZero() && r.AdjCredit.IsZero() {
			continue
		}
		id := r.ID
		row := WorksheetRow{
			AccountID: &id, Code: r.Code, Name: r.Name, Class: int(r.Class),
			Unadjusted: pairOf(r.Unadjusted),
			Adjustment: Pair{Debit: r.AdjDebit, Credit: r.AdjCredit},
			Adjusted:   pairOf(adjusted),
		}
		if r.Class >= 4 {
			row.ProfitLoss = row.Adjusted
		} else {
			row.BalanceSheet = row.Adjusted
		}
		add(row)
	}
	if !priorAdded {
		add(priorRow(prior))
	}

	// Laba bersih menyeimbangkan kolom laba rugi dan neraca.
	t := out.Totals
	out.NetIncome = t.ProfitLoss.Credit.Sub(t.ProfitLoss.Debit)
	out.Final = t
	if out.NetIncome.Sign() >= 0 {
		out.Final.ProfitLoss.Debit = t.ProfitLoss.Debit.Add(out.NetIncome)
		out.Final.BalanceSheet.Credit = t.BalanceSheet.Credit.Add(out.NetIncome)
	} else {
		out.Final.ProfitLoss.Credit = t.ProfitLoss.Credit.Add(out.NetIncome.Neg())
		out.Final.BalanceSheet.Debit = t.BalanceSheet.Debit.Add(out.NetIncome.Neg())
	}
	f := out.Final
	out.Balanced = f.Unadjusted.Debit.Equal(f.Unadjusted.Credit) && f.Adjustment.Debit.Equal(f.Adjustment.Credit) &&
		f.Adjusted.Debit.Equal(f.Adjusted.Credit) && f.ProfitLoss.Debit.Equal(f.ProfitLoss.Credit) &&
		f.BalanceSheet.Debit.Equal(f.BalanceSheet.Credit)
	return out, nil
}

// priorRow adalah laba rugi tahun lalu yang belum ditutup ke laba ditahan (tutup tahun
// otomatis menyusul di GL-10), supaya neraca lajur tetap seimbang.
func priorRow(net money.Amount) WorksheetRow {
	p := pairOf(net)
	return WorksheetRow{Code: "3-*", Name: "Laba (rugi) tahun lalu yang belum ditutup", Class: 3,
		Unadjusted: p, Adjusted: p, BalanceSheet: p}
}

// pairOf menaruh saldo debit − kredit di kolom debit bila positif, kredit bila negatif.
func pairOf(net money.Amount) Pair {
	if net.Sign() >= 0 {
		return Pair{Debit: net}
	}
	return Pair{Credit: net.Neg()}
}

// AccountLedger mengembalikan buku besar satu akun: saldo awal, mutasi, dan saldo berjalan.
func (l *ledger) AccountLedger(ctx context.Context, companyID, accountID uuid.UUID, from, to civil.Date) (AccountLedger, error) {
	return l.AccountLedgerBy(ctx, companyID, accountID, from, to, LedgerDim{})
}

func (l *ledger) AccountLedgerBy(ctx context.Context, companyID, accountID uuid.UUID, from, to civil.Date, dim LedgerDim) (AccountLedger, error) {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return AccountLedger{}, httpx.BadRequest("ledger.range", "Rentang tanggal tidak valid.")
	}
	q := l.q(ctx)
	a, err := q.GetAccount(ctx, store.GetAccountParams{CompanyID: companyID, ID: accountID})
	if isNoRows(err) {
		return AccountLedger{}, httpx.NotFound("account.not_found", "Akun tidak ditemukan.")
	}
	if err != nil {
		return AccountLedger{}, err
	}
	class := ClassOf(int(a.Class))
	since := civil.New(1900, 1, 1)
	if class.Statement == "laba_rugi" {
		startMonth, err := l.fiscalStartMonth(ctx, companyID)
		if err != nil {
			return AccountLedger{}, err
		}
		since = fiscalStart(startMonth, from)
	}
	opening, err := q.AccountOpening(ctx, store.AccountOpeningParams{
		CompanyID: companyID, AccountID: accountID, BeforeDate: from, SinceDate: since,
		BranchID: dim.BranchID, BranchNull: dim.NoBranch, DepartmentID: dim.DepartmentID, DepartmentNull: dim.NoDepartment,
	})
	if err != nil {
		return AccountLedger{}, err
	}
	closing, err := q.AccountOpening(ctx, store.AccountOpeningParams{
		CompanyID: companyID, AccountID: accountID, BeforeDate: to.AddDays(1), SinceDate: since,
		BranchID: dim.BranchID, BranchNull: dim.NoBranch, DepartmentID: dim.DepartmentID, DepartmentNull: dim.NoDepartment,
	})
	if err != nil {
		return AccountLedger{}, err
	}
	rows, err := q.AccountLines(ctx, store.AccountLinesParams{
		CompanyID: companyID, AccountID: accountID, FromDate: from, ToDate: to, RowLimit: maxLedgerLines,
		BranchID: dim.BranchID, BranchNull: dim.NoBranch, DepartmentID: dim.DepartmentID, DepartmentNull: dim.NoDepartment,
	})
	if err != nil {
		return AccountLedger{}, err
	}

	out := AccountLedger{
		Account: toAccount(a), Normal: class.Normal, From: from, To: to,
		Opening: class.Present(opening), Closing: class.Present(closing),
		Lines: make([]LedgerLine, 0, len(rows)), Truncated: len(rows) == maxLedgerLines,
	}
	running := opening
	for _, r := range rows {
		running = running.Add(r.Debit).Sub(r.Credit)
		memo := r.Memo
		if memo == "" {
			memo = r.EntryMemo
		}
		out.Lines = append(out.Lines, LedgerLine{
			EntryID: r.EntryID, Number: r.Number, Kind: Kind(r.Kind), Status: r.Status, Date: r.EntryDate,
			Memo: memo, Debit: r.Debit, Credit: r.Credit, Balance: class.Present(running), ProjectID: r.ProjectID,
			BranchID: r.BranchID, DepartmentID: r.DepartmentID,
		})
		out.TotalDebit = out.TotalDebit.Add(r.Debit)
		out.TotalCredit = out.TotalCredit.Add(r.Credit)
	}
	return out, nil
}

// VerifyBalances menghitung sel saldo yang tidak cocok dengan baris jurnal; seharusnya 0.
// Saldo per proyek ikut diperiksa.
func (l *ledger) VerifyBalances(ctx context.Context, companyID uuid.UUID) (int64, error) {
	q := l.q(ctx)
	n, err := q.BalanceMismatches(ctx, companyID)
	if err != nil {
		return 0, err
	}
	m, err := q.ProjectBalanceMismatches(ctx, companyID)
	return n + m, err
}

func (l *ledger) ProjectBalances(ctx context.Context, companyID uuid.UUID, projectID *uuid.UUID, from, to civil.Date) ([]ProjectBalance, error) {
	rows, err := l.q(ctx).ProjectNet(ctx, store.ProjectNetParams{
		CompanyID: companyID, FromMonth: from.MonthStart(), ToMonth: to.MonthStart(), ProjectID: projectID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ProjectBalance, len(rows))
	for i, r := range rows {
		out[i] = ProjectBalance{ProjectID: r.ProjectID, AccountID: r.AccountID, Debit: r.Debit, Credit: r.Credit}
	}
	return out, nil
}

func (l *ledger) ProjectLines(ctx context.Context, companyID, projectID uuid.UUID, accountID *uuid.UUID, from, to civil.Date) ([]ProjectLine, bool, error) {
	rows, err := l.q(ctx).ProjectLines(ctx, store.ProjectLinesParams{
		CompanyID: companyID, ProjectID: &projectID, AccountID: accountID, FromDate: from, ToDate: to, RowLimit: maxLedgerLines,
	})
	if err != nil {
		return nil, false, err
	}
	out := make([]ProjectLine, len(rows))
	for i, r := range rows {
		memo := r.Memo
		if memo == "" {
			memo = r.EntryMemo
		}
		out[i] = ProjectLine{
			EntryID: r.EntryID, Number: r.Number, Status: r.Status, SourceType: r.SourceType, SourceID: r.SourceID,
			Date: r.EntryDate, Memo: memo, AccountID: r.AccountID, AccountCode: r.AccountCode, AccountName: r.AccountName,
			Debit: r.Debit, Credit: r.Credit,
		}
	}
	return out, len(rows) == maxLedgerLines, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

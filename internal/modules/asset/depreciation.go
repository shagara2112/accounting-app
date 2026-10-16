package asset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/asset/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Run adalah satu jurnal penyusutan bulanan.
type Run struct {
	ID             uuid.UUID    `json:"id"`
	Month          civil.Date   `json:"month"`
	Number         string       `json:"number"`
	JournalEntryID uuid.UUID    `json:"journalEntryId"`
	Total          money.Amount `json:"total"`
	Assets         int          `json:"assets"`
	Status         string       `json:"status"`
	PostedAt       time.Time    `json:"postedAt"`
	PostedBy       *string      `json:"postedBy,omitempty"`
	VoidReason     *string      `json:"voidReason,omitempty"`
}

// RunAsset adalah penyusutan satu aset di satu jurnal (bisa lebih dari satu bulan susulan).
type RunAsset struct {
	AssetID    uuid.UUID    `json:"assetId"`
	Code       string       `json:"code"`
	Name       string       `json:"name"`
	Months     int          `json:"months"`
	FirstMonth civil.Date   `json:"firstMonth"`
	Amount     money.Amount `json:"amount"`
}

type RunDetail struct {
	Run
	VoidedBy *string    `json:"voidedBy,omitempty"`
	Lines    []RunAsset `json:"lines"`
}

// RunPreview adalah penyusutan yang akan dijurnal bila bulan itu diposting sekarang.
type RunPreview struct {
	Month  civil.Date   `json:"month"`
	Total  money.Amount `json:"total"`
	Lines  []RunAsset   `json:"lines"`
	Posted *Run         `json:"posted,omitempty"` // jurnal bulan itu bila sudah diposting
}

// due adalah penyusutan satu aset yang belum dijurnal s.d. bulan tertentu.
type due struct {
	asset  store.AssetAsset
	months []Month
	total  money.Amount
}

// dueFor menghitung penyusutan yang jatuh tempo s.d. bulan upTo (inklusif) dan belum pernah
// dijurnal, untuk setiap aset.
func (s *service) dueFor(ctx context.Context, companyID uuid.UUID, assets []store.AssetAsset, upTo civil.Date) ([]due, error) {
	ids := make([]uuid.UUID, len(assets))
	for i, a := range assets {
		ids[i] = a.ID
	}
	recorded, err := s.q(ctx).RecordedMonths(ctx, store.RecordedMonthsParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	done := map[uuid.UUID]map[civil.Date]bool{}
	for _, r := range recorded {
		if done[r.AssetID] == nil {
			done[r.AssetID] = map[civil.Date]bool{}
		}
		done[r.AssetID][r.Month] = true
	}
	var out []due
	for _, a := range assets {
		d := due{asset: a}
		for _, m := range planOf(a).Months() {
			if m.Month.After(upTo) {
				break
			}
			if !done[a.ID][m.Month] {
				d.months = append(d.months, m)
				d.total = d.total.Add(m.Amount)
			}
		}
		if len(d.months) > 0 {
			out = append(out, d)
		}
	}
	return out, nil
}

func previewLines(dues []due) ([]RunAsset, money.Amount) {
	lines := make([]RunAsset, len(dues))
	var total money.Amount
	for i, d := range dues {
		lines[i] = RunAsset{AssetID: d.asset.ID, Code: d.asset.Code, Name: d.asset.Name, Months: len(d.months), FirstMonth: d.months[0].Month, Amount: d.total}
		total = total.Add(d.total)
	}
	return lines, total
}

// journalLines menyusun baris jurnal penyusutan: debit beban dan kredit akumulasi, digabung
// per akun beserta cabang dan departemen asetnya.
func journalLines(dues []due, memo string) []ledger.LineDraft {
	type key struct {
		account      uuid.UUID
		branch, dept uuid.UUID
		debit        bool
	}
	sums := map[key]money.Amount{}
	var order []key
	add := func(k key, amount money.Amount) {
		if _, ok := sums[k]; !ok {
			order = append(order, k)
		}
		sums[k] = sums[k].Add(amount)
	}
	for _, d := range dues {
		var b, dp uuid.UUID
		if d.asset.BranchID != nil {
			b = *d.asset.BranchID
		}
		if d.asset.DepartmentID != nil {
			dp = *d.asset.DepartmentID
		}
		add(key{*d.asset.ExpenseAccountID, b, dp, true}, d.total)
		add(key{*d.asset.AccumAccountID, b, dp, false}, d.total)
	}
	// Debit dulu, lalu kredit, supaya jurnalnya mudah dibaca.
	var lines []ledger.LineDraft
	for _, debit := range []bool{true, false} {
		for _, k := range order {
			if k.debit != debit {
				continue
			}
			ln := ledger.LineDraft{AccountID: k.account, Memo: memo}
			if debit {
				ln.Debit = sums[k]
			} else {
				ln.Credit = sums[k]
			}
			if k.branch != uuid.Nil {
				b := k.branch
				ln.BranchID = &b
			}
			if k.dept != uuid.Nil {
				d := k.dept
				ln.DepartmentID = &d
			}
			lines = append(lines, ln)
		}
	}
	return lines
}

func (s *service) PreviewRun(ctx context.Context, companyID uuid.UUID, month civil.Date) (RunPreview, error) {
	month = month.MonthStart()
	q := s.q(ctx)
	out := RunPreview{Month: month, Lines: []RunAsset{}}
	if id, err := q.PostedRunForMonth(ctx, store.PostedRunForMonthParams{CompanyID: companyID, Month: month}); err == nil {
		r, err := s.Run(ctx, companyID, id)
		if err != nil {
			return out, err
		}
		out.Posted = &r.Run
	} else if !isNoRows(err) {
		return out, err
	}
	assets, err := q.DepreciableAssetsPreview(ctx, store.DepreciableAssetsPreviewParams{CompanyID: companyID, Month: month})
	if err != nil {
		return out, err
	}
	dues, err := s.dueFor(ctx, companyID, assets, month)
	if err != nil {
		return out, err
	}
	if lines, total := previewLines(dues); len(lines) > 0 {
		out.Lines, out.Total = lines, total
	}
	return out, nil
}

func (s *service) PostRun(ctx context.Context, companyID uuid.UUID, month civil.Date, actor audit.Actor) (RunDetail, error) {
	var out RunDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		id, err := s.postRun(ctx, companyID, month, actor)
		if err != nil {
			return err
		}
		out, err = s.Run(ctx, companyID, id)
		return err
	})
	return out, err
}

var errNothingDue = errors.New("tidak ada penyusutan jatuh tempo")

func (s *service) postRun(ctx context.Context, companyID uuid.UUID, month civil.Date, actor audit.Actor) (uuid.UUID, error) {
	month = month.MonthStart()
	if month.IsZero() {
		msg := "Pilih bulan penyusutan."
		return uuid.Nil, httpx.Unprocessable("depreciation.month", msg).WithFields(map[string]string{"month": msg})
	}
	q := s.q(ctx)
	if _, err := q.PostedRunForMonth(ctx, store.PostedRunForMonthParams{CompanyID: companyID, Month: month}); err == nil {
		return uuid.Nil, httpx.Conflict("depreciation.exists", fmt.Sprintf("Penyusutan %s sudah diposting.", month.MonthLabel()))
	} else if !isNoRows(err) {
		return uuid.Nil, err
	}
	assets, err := q.DepreciableAssets(ctx, store.DepreciableAssetsParams{CompanyID: companyID, Month: month})
	if err != nil {
		return uuid.Nil, err
	}
	dues, err := s.dueFor(ctx, companyID, assets, month)
	if err != nil {
		return uuid.Nil, err
	}
	if len(dues) == 0 {
		return uuid.Nil, fmt.Errorf("%w: %w", errNothingDue, httpx.Unprocessable("depreciation.nothing_due",
			fmt.Sprintf("Tidak ada penyusutan yang jatuh tempo s.d. %s.", month.MonthLabel())))
	}
	_, total := previewLines(dues)
	id := uuid.Must(uuid.NewV7())
	date := month.MonthEnd()
	number, err := s.Ledger.NextNumber(ctx, companyID, seriesDepreciation, date)
	if err != nil {
		return uuid.Nil, err
	}
	memo := "Penyusutan aset tetap " + month.MonthLabel()
	posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
		CompanyID: companyID, Kind: ledger.Adjustment, Date: date, Memo: memo, SourceType: SourceDepreciation, SourceID: &id,
		Number: number, Lines: journalLines(dues, memo), Actor: actor,
	})
	if err != nil {
		return uuid.Nil, err
	}
	if err := q.InsertRun(ctx, store.InsertRunParams{
		ID: id, CompanyID: companyID, Month: month, Number: number, JournalEntryID: posted.ID, Total: total,
		Assets: int32(len(dues)), PostedBy: actor.UserRef(),
	}); err != nil {
		return uuid.Nil, err
	}
	for _, d := range dues {
		for _, m := range d.months {
			if err := q.InsertDepreciationLine(ctx, store.InsertDepreciationLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, AssetID: d.asset.ID, Month: m.Month, Amount: m.Amount, RunID: &id,
			}); err != nil {
				return uuid.Nil, err
			}
		}
	}
	_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
		UserID: actor.UserRef(), CompanyID: &companyID, Action: "asset.depreciation.post", ObjectType: "depreciation_run",
		ObjectID: id.String(), IP: actor.IP,
		After: map[string]any{"number": number, "month": month.MonthKey(), "total": total.String(), "assets": len(dues)},
	})
	return id, err
}

func toRun(r store.AssetDepreciationRun, postedBy *string) Run {
	return Run{
		ID: r.ID, Month: r.Month, Number: r.Number, JournalEntryID: r.JournalEntryID, Total: r.Total, Assets: int(r.Assets),
		Status: r.Status, PostedAt: r.PostedAt, PostedBy: postedBy, VoidReason: r.VoidReason,
	}
}

func (s *service) Runs(ctx context.Context, companyID uuid.UUID) ([]Run, error) {
	rows, err := s.q(ctx).ListRuns(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = toRun(store.AssetDepreciationRun{
			ID: r.ID, Month: r.Month, Number: r.Number, JournalEntryID: r.JournalEntryID, Total: r.Total, Assets: r.Assets,
			Status: r.Status, PostedAt: r.PostedAt, VoidReason: r.VoidReason,
		}, r.PostedByName)
	}
	return out, nil
}

func (s *service) Run(ctx context.Context, companyID, id uuid.UUID) (RunDetail, error) {
	q := s.q(ctx)
	r, err := q.GetRun(ctx, store.GetRunParams{CompanyID: companyID, ID: id})
	if isNoRows(err) {
		return RunDetail{}, httpx.NotFound("depreciation.not_found", "Jurnal penyusutan tidak ditemukan.")
	}
	if err != nil {
		return RunDetail{}, err
	}
	out := RunDetail{
		Run: toRun(store.AssetDepreciationRun{
			ID: r.ID, Month: r.Month, Number: r.Number, JournalEntryID: r.JournalEntryID, Total: r.Total, Assets: r.Assets,
			Status: r.Status, PostedAt: r.PostedAt, VoidReason: r.VoidReason,
		}, r.PostedByName),
		VoidedBy: r.VoidedByName, Lines: []RunAsset{},
	}
	rows, err := q.RunAssets(ctx, store.RunAssetsParams{CompanyID: companyID, RunID: &id})
	if err != nil {
		return RunDetail{}, err
	}
	for _, a := range rows {
		out.Lines = append(out.Lines, RunAsset{AssetID: a.ID, Code: a.Code, Name: a.Name, Months: int(a.Months), FirstMonth: a.FirstMonth, Amount: a.Amount})
	}
	return out, nil
}

func voidReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 5 || n > 300 {
		msg := "Tulis alasan void (5–300 karakter)."
		return reason, httpx.Unprocessable("void.reason", msg).WithFields(map[string]string{"reason": msg})
	}
	return reason, nil
}

// VoidRun membatalkan jurnal penyusutan bulanan. Bulan-bulannya kembali belum dijurnal dan
// ikut jurnal penyusutan berikutnya.
func (s *service) VoidRun(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (RunDetail, error) {
	reason, err := voidReason(reason)
	if err != nil {
		return RunDetail{}, err
	}
	var out RunDetail
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		r, err := q.GetRunForUpdate(ctx, store.GetRunForUpdateParams{CompanyID: companyID, ID: id})
		if isNoRows(err) {
			return httpx.NotFound("depreciation.not_found", "Jurnal penyusutan tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if r.Status != "posted" {
			return httpx.Conflict("depreciation.already_void", fmt.Sprintf("%s sudah di-void.", r.Number))
		}
		disposed, err := q.RunDisposedAssets(ctx, store.RunDisposedAssetsParams{CompanyID: companyID, RunID: &id})
		if err != nil {
			return err
		}
		if len(disposed) > 0 {
			return httpx.Conflict("depreciation.disposed", fmt.Sprintf(
				"%s memuat penyusutan aset yang sudah dilepas (%s). Batalkan pelepasannya dulu.", r.Number, strings.Join(disposed, ", ")))
		}
		if date.IsZero() {
			date = r.Month.MonthEnd()
		}
		if _, err := s.Ledger.VoidSource(ctx, companyID, r.JournalEntryID, SourceDepreciation, date, reason, actor); err != nil {
			return err
		}
		if err := q.DeleteRunLines(ctx, store.DeleteRunLinesParams{CompanyID: companyID, RunID: &id}); err != nil {
			return err
		}
		if n, err := q.VoidRun(ctx, store.VoidRunParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || n != 1 {
			if err == nil {
				err = fmt.Errorf("jurnal penyusutan %s gagal ditandai void", r.Number)
			}
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "asset.depreciation.void", ObjectType: "depreciation_run",
			ObjectID: id.String(), IP: actor.IP, After: map[string]any{"number": r.Number, "reason": reason},
		}); err != nil {
			return err
		}
		out, err = s.Run(ctx, companyID, id)
		return err
	})
	return out, err
}

// RunDue dipanggil penjadwal: sejak tanggal 1, penyusutan bulan lalu diposting otomatis untuk
// setiap PT yang punya aset disusutkan dan belum memostingnya. PT yang periodenya sudah
// dikunci atau tidak punya penyusutan jatuh tempo dilewati tanpa galat.
func (s *service) RunDue(ctx context.Context, today civil.Date) (int, error) {
	companies, err := s.q(ctx).CompaniesWithDepreciableAssets(ctx)
	if err != nil {
		return 0, err
	}
	month := today.MonthStart().AddMonths(-1)
	posted := 0
	for _, c := range companies {
		err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
			if _, err := s.q(ctx).PostedRunForMonth(ctx, store.PostedRunForMonthParams{CompanyID: c, Month: month}); err == nil {
				return nil
			} else if !isNoRows(err) {
				return err
			}
			if _, err := s.postRun(ctx, c, month, audit.Actor{IP: "penjadwal"}); err != nil {
				return err
			}
			posted++
			return nil
		})
		var p *httpx.Problem
		if err != nil && !errors.Is(err, errNothingDue) && !errors.As(err, &p) {
			return posted, err
		}
	}
	return posted, nil
}

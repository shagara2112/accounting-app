package asset

import (
	"context"
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

// Jenis pelepasan aset (FA-03).
const (
	DisposeSale     = "jual"   // dijual, uangnya langsung diterima di akun pilihan
	DisposeInvoice  = "faktur" // dijual lewat faktur penjualan (mis. karena terutang PPN)
	DisposeWriteOff = "hapus"  // rusak, hilang, atau dibuang
)

type DisposalInput struct {
	Date              civil.Date   `json:"date"`
	Kind              string       `json:"kind"`
	Proceeds          money.Amount `json:"proceeds"`          // hanya jual: harga jual sebelum PPN
	ProceedsAccountID *uuid.UUID   `json:"proceedsAccountId"` // hanya jual: kas, bank, atau piutang lain-lain
	Memo              string       `json:"memo"`
}

type Disposal struct {
	ID                uuid.UUID    `json:"id"`
	Number            string       `json:"number"`
	Kind              string       `json:"kind"`
	Date              civil.Date   `json:"date"`
	Proceeds          money.Amount `json:"proceeds"`
	ProceedsAccountID *uuid.UUID   `json:"proceedsAccountId,omitempty"`
	Cost              money.Amount `json:"cost"`
	Accumulated       money.Amount `json:"accumulated"`
	BookValue         money.Amount `json:"bookValue"`
	// Gain: laba (positif) atau rugi (negatif). Untuk penjualan lewat faktur, nilai buku
	// dibebankan ke akun laba pelepasan dan harga jualnya masuk lewat faktur.
	Gain           money.Amount `json:"gain"`
	Memo           string       `json:"memo"`
	JournalEntryID uuid.UUID    `json:"journalEntryId"`
	Status         string       `json:"status"`
	CreatedAt      time.Time    `json:"createdAt"`
	CreatedBy      *string      `json:"createdBy,omitempty"`
	VoidedBy       *string      `json:"voidedBy,omitempty"`
	VoidReason     *string      `json:"voidReason,omitempty"`
}

func toDisposal(d store.AssetDisposalsRow) Disposal {
	return Disposal{
		ID: d.ID, Number: d.Number, Kind: d.Kind, Date: d.DisposalDate, Proceeds: d.Proceeds, ProceedsAccountID: d.ProceedsAccountID,
		Cost: d.Cost, Accumulated: d.Accumulated, BookValue: d.BookValue, Gain: d.Gain, Memo: d.Memo, JournalEntryID: d.JournalEntryID,
		Status: d.Status, CreatedAt: d.CreatedAt, CreatedBy: d.CreatedByName, VoidedBy: d.VoidedByName, VoidReason: d.VoidReason,
	}
}

// Dispose melepas aset. Penyusutan yang belum dijurnal s.d. bulan pelepasan ikut dijurnal di
// jurnal pelepasan (seri PAT), lalu harga perolehan dan akumulasinya dikeluarkan dan selisih
// dengan harga jual dicatat sebagai laba atau rugi.
func (s *service) Dispose(ctx context.Context, companyID, assetID uuid.UUID, in DisposalInput, actor audit.Actor) (AssetDetail, error) {
	in.Memo = strings.TrimSpace(in.Memo)
	f := map[string]string{}
	switch in.Kind {
	case DisposeSale:
		if in.Proceeds.Sign() <= 0 {
			f["proceeds"] = "Isi harga jual; bila tidak ada uang yang diterima, pilih dihapus."
		}
		if in.ProceedsAccountID == nil {
			f["proceedsAccountId"] = "Pilih akun penerimaan uangnya."
		}
	case DisposeInvoice, DisposeWriteOff:
		in.Proceeds, in.ProceedsAccountID = money.Amount{}, nil
	default:
		f["kind"] = "Pilih jenis pelepasan."
	}
	if in.Date.IsZero() {
		f["date"] = "Tanggal pelepasan wajib diisi."
	}
	if utf8.RuneCountInString(in.Memo) > 300 {
		f["memo"] = "Keterangan maksimal 300 karakter."
	}
	if len(f) > 0 {
		return AssetDetail{}, invalid("disposal.invalid", f)
	}
	var out AssetDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		a, err := q.GetAssetForUpdate(ctx, store.GetAssetForUpdateParams{CompanyID: companyID, ID: assetID})
		if isNoRows(err) {
			return httpx.NotFound("asset.not_found", "Aset tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if a.Status != StatusActive {
			return httpx.Conflict("asset.disposed", fmt.Sprintf("%s sudah dilepas.", a.Code))
		}
		if in.Date.Before(a.AcquiredOn) {
			msg := fmt.Sprintf("Tanggal pelepasan tidak boleh sebelum tanggal perolehan (%s).", a.AcquiredOn.Indonesian())
			return httpx.Unprocessable("disposal.date", msg).WithFields(map[string]string{"date": msg})
		}
		if in.ProceedsAccountID != nil {
			if err := s.Ledger.CheckAccount(ctx, companyID, *in.ProceedsAccountID, []int{1}); err != nil {
				return withField(err, "proceedsAccountId")
			}
		}
		recorded, err := q.RecordedMonths(ctx, store.RecordedMonthsParams{CompanyID: companyID, Ids: []uuid.UUID{a.ID}})
		if err != nil {
			return err
		}
		month := in.Date.MonthStart()
		for _, r := range recorded {
			if r.Month.After(month) {
				msg := fmt.Sprintf("Penyusutan %s sudah dijurnal sampai %s. Pakai tanggal pelepasan sesudahnya, atau void jurnal penyusutan itu dulu.", a.Code, r.Month.MonthLabel())
				return httpx.Unprocessable("disposal.date", msg).WithFields(map[string]string{"date": msg})
			}
		}
		dues, err := s.dueFor(ctx, companyID, []store.AssetAsset{a}, month)
		if err != nil {
			return err
		}
		var catchUp money.Amount
		var months []Month
		if len(dues) == 1 {
			catchUp, months = dues[0].total, dues[0].months
		}
		accum := a.OpeningAccum.Add(catchUp)
		lines, err := q.AssetDepreciation(ctx, store.AssetDepreciationParams{CompanyID: companyID, AssetID: a.ID})
		if err != nil {
			return err
		}
		for _, l := range lines {
			accum = accum.Add(l.Amount)
		}
		bookValue := a.Cost.Sub(accum)
		gain := bookValue.Neg()
		if in.Kind == DisposeSale {
			gain = in.Proceeds.Sub(bookValue)
		}

		var gainAcc, lossAcc ledger.Account
		if gainAcc, err = s.Ledger.RoleAccount(ctx, companyID, ledger.AssetGain); err != nil {
			return err
		}
		if lossAcc, err = s.Ledger.RoleAccount(ctx, companyID, ledger.AssetLoss); err != nil {
			return err
		}
		memo := fmt.Sprintf("Pelepasan %s %s", a.Code, a.Name)
		if in.Memo != "" {
			memo += ": " + in.Memo
		}
		if utf8.RuneCountInString(memo) > 500 {
			memo = string([]rune(memo)[:500])
		}
		var drafts []ledger.LineDraft
		line := func(account uuid.UUID, debit, credit money.Amount, m string) {
			if debit.Sign() == 0 && credit.Sign() == 0 {
				return
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: account, Debit: debit, Credit: credit, Memo: m,
				BranchID: a.BranchID, DepartmentID: a.DepartmentID})
		}
		zero := money.Amount{}
		if catchUp.Sign() > 0 {
			m := "Penyusutan s.d. " + month.MonthLabel()
			line(*a.ExpenseAccountID, catchUp, zero, m)
			line(*a.AccumAccountID, zero, catchUp, m)
		}
		if a.AccumAccountID != nil {
			line(*a.AccumAccountID, accum, zero, "Akumulasi penyusutan "+a.Code)
		}
		switch in.Kind {
		case DisposeSale:
			line(*in.ProceedsAccountID, in.Proceeds, zero, "Hasil penjualan "+a.Code)
			if gain.Sign() > 0 {
				line(gainAcc.ID, zero, gain, "Laba pelepasan "+a.Code)
			} else {
				line(lossAcc.ID, gain.Neg(), zero, "Rugi pelepasan "+a.Code)
			}
		case DisposeInvoice:
			line(gainAcc.ID, bookValue, zero, "Nilai buku "+a.Code+" (harga jual lewat faktur)")
		case DisposeWriteOff:
			line(lossAcc.ID, bookValue, zero, "Nilai buku "+a.Code+" dihapus")
		}
		line(a.AssetAccountID, zero, a.Cost, "Harga perolehan "+a.Code)

		id := uuid.Must(uuid.NewV7())
		number, err := s.Ledger.NextNumberFor(ctx, companyID, seriesDisposal, in.Date, a.BranchID)
		if err != nil {
			return err
		}
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: in.Date, Memo: memo, SourceType: SourceDisposal, SourceID: &id,
			Number: number, Lines: drafts, Actor: actor,
		})
		if err != nil {
			return err
		}
		if err := q.InsertDisposal(ctx, store.InsertDisposalParams{
			ID: id, CompanyID: companyID, AssetID: a.ID, Number: number, Kind: in.Kind, DisposalDate: in.Date, Proceeds: in.Proceeds,
			ProceedsAccountID: in.ProceedsAccountID, Cost: a.Cost, Accumulated: accum, BookValue: bookValue, Gain: gain, Memo: in.Memo,
			JournalEntryID: posted.ID, CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		for _, m := range months {
			if err := q.InsertDepreciationLine(ctx, store.InsertDepreciationLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, AssetID: a.ID, Month: m.Month, Amount: m.Amount, DisposalID: &id,
			}); err != nil {
				return err
			}
		}
		date := in.Date
		if err := q.SetAssetStatus(ctx, store.SetAssetStatusParams{CompanyID: companyID, ID: a.ID, Status: StatusDisposed, DisposedOn: &date}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "asset.dispose", ObjectType: "fixed_asset", ObjectID: a.ID.String(), IP: actor.IP,
			After: map[string]any{"number": number, "kind": in.Kind, "date": in.Date.String(), "proceeds": in.Proceeds.String(),
				"bookValue": bookValue.String(), "gain": gain.String(), "catchUp": catchUp.String()},
		}); err != nil {
			return err
		}
		out, err = s.Asset(ctx, companyID, a.ID)
		return err
	})
	return out, err
}

func (s *service) DisposalAsset(ctx context.Context, companyID, disposalID uuid.UUID) (AssetDetail, error) {
	id, err := s.q(ctx).DisposalAsset(ctx, store.DisposalAssetParams{CompanyID: companyID, ID: disposalID})
	if isNoRows(err) {
		return AssetDetail{}, httpx.NotFound("disposal.not_found", "Pelepasan aset tidak ditemukan.")
	}
	if err != nil {
		return AssetDetail{}, err
	}
	return s.Asset(ctx, companyID, id)
}

// VoidDisposal membatalkan pelepasan: jurnalnya dibalik, penyusutan susulannya kembali belum
// dijurnal, dan asetnya aktif lagi.
func (s *service) VoidDisposal(ctx context.Context, companyID, disposalID uuid.UUID, date civil.Date, reason string, actor audit.Actor) (AssetDetail, error) {
	reason, err := voidReason(reason)
	if err != nil {
		return AssetDetail{}, err
	}
	var out AssetDetail
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		d, err := q.GetDisposalForUpdate(ctx, store.GetDisposalForUpdateParams{CompanyID: companyID, ID: disposalID})
		if isNoRows(err) {
			return httpx.NotFound("disposal.not_found", "Pelepasan aset tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if d.Status != "posted" {
			return httpx.Conflict("disposal.already_void", fmt.Sprintf("%s sudah di-void.", d.Number))
		}
		if _, err := q.GetAssetForUpdate(ctx, store.GetAssetForUpdateParams{CompanyID: companyID, ID: d.AssetID}); err != nil {
			return err
		}
		if date.IsZero() {
			date = d.DisposalDate
		}
		if _, err := s.Ledger.VoidSource(ctx, companyID, d.JournalEntryID, SourceDisposal, date, reason, actor); err != nil {
			return err
		}
		if err := q.DeleteDisposalLines(ctx, store.DeleteDisposalLinesParams{CompanyID: companyID, DisposalID: &d.ID}); err != nil {
			return err
		}
		if n, err := q.VoidDisposal(ctx, store.VoidDisposalParams{CompanyID: companyID, ID: d.ID, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || n != 1 {
			if err == nil {
				err = fmt.Errorf("pelepasan %s gagal ditandai void", d.Number)
			}
			return err
		}
		if err := q.SetAssetStatus(ctx, store.SetAssetStatusParams{CompanyID: companyID, ID: d.AssetID, Status: StatusActive}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "asset.disposal.void", ObjectType: "fixed_asset", ObjectID: d.AssetID.String(),
			IP: actor.IP, After: map[string]any{"number": d.Number, "reason": reason},
		}); err != nil {
			return err
		}
		out, err = s.Asset(ctx, companyID, d.AssetID)
		return err
	})
	return out, err
}

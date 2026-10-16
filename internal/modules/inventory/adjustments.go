package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/modules/inventory/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Jenis dokumen persediaan (INV-04).
const (
	AdjManual = "penyesuaian" // selisih bertanda per barang, mis. rusak, hilang, atau saldo awal
	AdjIssue  = "pemakaian"   // barang dipakai, mis. material ke proyek
	AdjCount  = "opname"      // hitung fisik; selisih terhadap stok sistem dijurnal otomatis
)

const maxAdjustmentLines = 500

type AdjustmentInput struct {
	Kind      string                `json:"kind"`
	Date      civil.Date            `json:"date"`
	AccountID uuid.UUID             `json:"accountId"` // lawan akun persediaan
	ProjectID *uuid.UUID            `json:"projectId"`
	Memo      string                `json:"memo"`
	Lines     []AdjustmentLineInput `json:"lines"`
}

type AdjustmentLineInput struct {
	ProductID  uuid.UUID     `json:"productId"`
	Qty        money.Amount  `json:"qty"`        // penyesuaian: bertanda; pemakaian: jumlah dipakai
	CountedQty *money.Amount `json:"countedQty"` // opname: hasil hitung fisik
	UnitCost   *money.Amount `json:"unitCost"`   // barang masuk; kosong = biaya rata-rata saat itu
	Memo       string        `json:"memo"`
}

type Adjustment struct {
	ID          uuid.UUID  `json:"id"`
	Number      string     `json:"number"`
	Kind        string     `json:"kind"`
	Date        civil.Date `json:"date"`
	AccountID   uuid.UUID  `json:"accountId"`
	AccountCode string     `json:"accountCode"`
	AccountName string     `json:"accountName"`
	ProjectID   *uuid.UUID `json:"projectId,omitempty"`
	Memo        string     `json:"memo"`
	Status      string     `json:"status"`
	LineCount   int        `json:"lineCount"`
}

type AdjustmentLine struct {
	ProductID   uuid.UUID     `json:"productId"`
	ProductCode string        `json:"productCode"`
	ProductName string        `json:"productName"`
	Unit        string        `json:"unit"`
	SystemQty   *money.Amount `json:"systemQty,omitempty"`
	CountedQty  *money.Amount `json:"countedQty,omitempty"`
	Qty         money.Amount  `json:"qty"`
	UnitCost    *money.Amount `json:"unitCost,omitempty"`
	Value       money.Amount  `json:"value"` // nilai bertanda yang masuk atau keluar dari persediaan
	Memo        string        `json:"memo"`
}

type AdjustmentDetail struct {
	Adjustment
	Lines      []AdjustmentLine `json:"lines"`
	Total      money.Amount     `json:"total"` // perubahan nilai persediaan
	CreatedBy  *string          `json:"createdBy,omitempty"`
	CreatedAt  time.Time        `json:"createdAt"`
	VoidedBy   *string          `json:"voidedBy,omitempty"`
	VoidedAt   *time.Time       `json:"voidedAt,omitempty"`
	VoidDate   *civil.Date      `json:"voidDate,omitempty"`
	VoidReason *string          `json:"voidReason,omitempty"`
}

// stateAt adalah stok dan nilainya pada akhir tanggal d.
func (s *service) stateAt(ctx context.Context, companyID, productID uuid.UUID, d civil.Date) (money.Amount, money.Amount, error) {
	r, err := s.q(ctx).MovementBefore(ctx, store.MovementBeforeParams{CompanyID: companyID, ProductID: productID, Before: d.AddDays(1)})
	if errors.Is(err, pgx.ErrNoRows) {
		return money.Amount{}, money.Amount{}, nil
	}
	return r.QtyAfter, r.ValueAfter, err
}

type plannedLine struct {
	in        AdjustmentLineInput
	systemQty *money.Amount
	qty       money.Amount
	unitCost  *money.Amount
}

func (s *service) CreateAdjustment(ctx context.Context, companyID uuid.UUID, in AdjustmentInput, actor audit.Actor) (AdjustmentDetail, error) {
	in.Memo = strings.TrimSpace(in.Memo)
	f := map[string]string{}
	if in.Kind != AdjManual && in.Kind != AdjIssue && in.Kind != AdjCount {
		f["kind"] = "Pilih penyesuaian, pemakaian, atau stock opname."
	}
	if in.Date.IsZero() {
		f["date"] = "Tanggal wajib diisi."
	}
	if in.AccountID == uuid.Nil {
		f["accountId"] = "Pilih akun lawan persediaan."
	}
	if utf8.RuneCountInString(in.Memo) > 500 {
		f["memo"] = "Keterangan maksimal 500 karakter."
	}
	if len(in.Lines) == 0 || len(in.Lines) > maxAdjustmentLines {
		f["lines"] = fmt.Sprintf("Isi 1–%d baris barang.", maxAdjustmentLines)
	}
	seen := map[uuid.UUID]bool{}
	for i, l := range in.Lines {
		key := fmt.Sprintf("lines.%d.", i)
		switch {
		case l.ProductID == uuid.Nil:
			f[key+"productId"] = "Pilih barang."
		case seen[l.ProductID]:
			f[key+"productId"] = "Barang yang sama dipilih dua kali."
		}
		seen[l.ProductID] = true
		switch in.Kind {
		case AdjManual:
			if l.Qty.IsZero() {
				f[key+"qty"] = "Isi selisih: positif untuk menambah, negatif untuk mengurangi stok."
			}
		case AdjIssue:
			if l.Qty.Sign() <= 0 {
				f[key+"qty"] = "Jumlah dipakai harus lebih dari 0."
			}
		case AdjCount:
			if l.CountedQty == nil || l.CountedQty.Sign() < 0 {
				f[key+"countedQty"] = "Isi hasil hitung fisik (0 atau lebih)."
			}
		}
		if l.UnitCost != nil && l.UnitCost.Sign() < 0 {
			f[key+"unitCost"] = "Harga pokok tidak boleh negatif."
		}
		if utf8.RuneCountInString(l.Memo) > 250 {
			f[key+"memo"] = "Keterangan baris maksimal 250 karakter."
		}
	}
	if len(f) > 0 {
		return AdjustmentDetail{}, httpx.Unprocessable("adjustment.invalid", adjFirst(f)).WithFields(f)
	}

	var out AdjustmentDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		accounts, err := s.accounts(ctx, companyID)
		if err != nil {
			return err
		}
		invAccounts, err := s.InventoryAccounts(ctx, companyID)
		if err != nil {
			return err
		}
		a, ok := accounts[in.AccountID]
		switch {
		case !ok:
			return fieldProblem("accountId", "Akun tidak ditemukan di PT ini.")
		case !a.Postable || !a.Active:
			return fieldProblem("accountId", fmt.Sprintf("%s %s bukan akun detail yang aktif.", a.Code, a.Name))
		case invAccounts[a.ID]:
			return fieldProblem("accountId", fmt.Sprintf("%s %s adalah akun persediaan; pilih akun lawannya, mis. selisih persediaan atau biaya pemakaian.", a.Code, a.Name))
		}

		ids := make([]uuid.UUID, len(in.Lines))
		for i, l := range in.Lines {
			ids[i] = l.ProductID
		}
		if err := s.lockStock(ctx, companyID, sortedIDs(ids)); err != nil {
			return err
		}
		products, err := s.ProductsByIDs(ctx, companyID, ids)
		if err != nil {
			return err
		}
		lf := map[string]string{}
		plan := make([]plannedLine, len(in.Lines))
		for i, l := range in.Lines {
			key := fmt.Sprintf("lines.%d.", i)
			p, ok := products[l.ProductID]
			switch {
			case !ok:
				lf[key+"productId"] = "Barang tidak ditemukan di PT ini."
				continue
			case !p.Goods():
				lf[key+"productId"] = fmt.Sprintf("%s adalah jasa dan tidak punya stok.", p.Code)
				continue
			case !p.Active:
				lf[key+"productId"] = fmt.Sprintf("%s nonaktif.", p.Code)
				continue
			}
			qty, value, err := s.stateAt(ctx, companyID, p.ID, in.Date)
			if err != nil {
				return err
			}
			pl := plannedLine{in: l}
			switch in.Kind {
			case AdjManual:
				pl.qty = l.Qty
			case AdjIssue:
				pl.qty = l.Qty.Neg()
			case AdjCount:
				sys := qty
				pl.systemQty = &sys
				pl.qty = l.CountedQty.Sub(qty)
			}
			if pl.qty.Sign() > 0 {
				switch {
				case l.UnitCost != nil:
					c := *l.UnitCost
					pl.unitCost = &c
				case qty.Sign() > 0:
					c := avgCost(qty, value)
					pl.unitCost = &c
				case p.PurchasePrice.Sign() > 0:
					c := p.PurchasePrice
					pl.unitCost = &c
				default:
					lf[key+"unitCost"] = fmt.Sprintf("Stok %s kosong; isi harga pokok satuan untuk barang yang masuk.", p.Code)
				}
			}
			plan[i] = pl
		}
		if len(lf) > 0 {
			return httpx.Unprocessable("adjustment.invalid", adjFirst(lf)).WithFields(lf)
		}

		id := uuid.Must(uuid.NewV7())
		number, err := s.Ledger.NextNumber(ctx, companyID, seriesAdjustment, in.Date)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if err := q.InsertAdjustment(ctx, store.InsertAdjustmentParams{
			ID: id, CompanyID: companyID, Number: number, Kind: in.Kind, AdjDate: in.Date, AccountID: in.AccountID,
			ProjectID: in.ProjectID, Memo: in.Memo, CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		var moves []move
		for i, pl := range plan {
			p := products[pl.in.ProductID]
			var counted *money.Amount
			if in.Kind == AdjCount {
				counted = pl.in.CountedQty
			}
			if err := q.InsertAdjustmentLine(ctx, store.InsertAdjustmentLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, AdjustmentID: id, LineNo: int16(i + 1),
				ProductID: p.ID, SystemQty: pl.systemQty, CountedQty: counted, Qty: pl.qty, UnitCost: pl.unitCost,
				Memo: strings.TrimSpace(pl.in.Memo),
			}); err != nil {
				return err
			}
			if pl.qty.IsZero() {
				continue
			}
			counter := in.AccountID
			m := move{
				productID: p.ID, date: in.Date, qty: pl.qty, owned: true, counter: &counter, projectID: in.ProjectID,
				sourceType: SourceAdjustment, sourceID: id, number: number, memo: memoOr(pl.in.Memo, memoOr(in.Memo, p.Name)),
			}
			if pl.qty.Sign() > 0 {
				v := pl.qty.Mul(*pl.unitCost, 0, money.HalfUp)
				m.valuation, m.fixedValue = valFixed, &v
			} else {
				m.valuation = valAverage
			}
			moves = append(moves, m)
		}
		if err := s.record(ctx, companyID, moves, actor, map[uuid.UUID]string{id: number}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "inventory.adjustment.post",
			ObjectType: "inventory_adjustment", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "kind": in.Kind, "date": in.Date.String(), "lines": len(plan)},
		}); err != nil {
			return err
		}
		out, err = s.Adjustment(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) VoidAdjustment(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (AdjustmentDetail, error) {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 5 || n > 300 {
		return AdjustmentDetail{}, fieldProblem("reason", "Tulis alasan void (5–300 karakter).")
	}
	var out AdjustmentDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		a, err := q.GetAdjustmentForUpdate(ctx, store.GetAdjustmentForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("adjustment.not_found", "Dokumen persediaan tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if a.Status != "posted" {
			return httpx.Conflict("adjustment.already_void", fmt.Sprintf("%s sudah di-void.", a.Number))
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		if date.Before(a.AdjDate) {
			return fieldProblem("date", fmt.Sprintf("Tanggal void tidak boleh sebelum tanggal dokumen (%s).", a.AdjDate.Indonesian()))
		}
		if err := s.ReverseSource(ctx, companyID, SourceAdjustment, id, date, actor); err != nil {
			return err
		}
		if n, err := q.MarkAdjustmentVoid(ctx, store.MarkAdjustmentVoidParams{
			CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidDate: &date, VoidReason: &reason,
		}); err != nil || n != 1 {
			if err == nil {
				err = fmt.Errorf("dokumen %s gagal ditandai void", a.Number)
			}
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "inventory.adjustment.void",
			ObjectType: "inventory_adjustment", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": a.Number, "status": "posted"},
			After:  map[string]any{"number": a.Number, "status": "void", "reason": reason, "date": date.String()},
		}); err != nil {
			return err
		}
		out, err = s.Adjustment(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) Adjustments(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]Adjustment, error) {
	rows, err := s.q(ctx).ListAdjustments(ctx, store.ListAdjustmentsParams{CompanyID: companyID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Adjustment, len(rows))
	for i, r := range rows {
		a := accounts[r.AccountID]
		out[i] = Adjustment{
			ID: r.ID, Number: r.Number, Kind: r.Kind, Date: r.AdjDate, AccountID: r.AccountID, AccountCode: a.Code,
			AccountName: a.Name, ProjectID: r.ProjectID, Memo: r.Memo, Status: r.Status, LineCount: int(r.LineCount),
		}
	}
	return out, nil
}

func (s *service) Adjustment(ctx context.Context, companyID, id uuid.UUID) (AdjustmentDetail, error) {
	q := s.q(ctx)
	r, err := q.GetAdjustment(ctx, store.GetAdjustmentParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return AdjustmentDetail{}, httpx.NotFound("adjustment.not_found", "Dokumen persediaan tidak ditemukan.")
	}
	if err != nil {
		return AdjustmentDetail{}, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return AdjustmentDetail{}, err
	}
	lines, err := q.AdjustmentLines(ctx, store.AdjustmentLinesParams{CompanyID: companyID, AdjustmentID: id})
	if err != nil {
		return AdjustmentDetail{}, err
	}
	moves, err := q.MovementsBySource(ctx, store.MovementsBySourceParams{CompanyID: companyID, SourceType: SourceAdjustment, SourceID: id})
	if err != nil {
		return AdjustmentDetail{}, err
	}
	values := map[uuid.UUID]money.Amount{}
	for _, m := range moves {
		values[m.ProductID] = values[m.ProductID].Add(m.Value)
	}
	a := accounts[r.AccountID]
	out := AdjustmentDetail{
		Adjustment: Adjustment{
			ID: r.ID, Number: r.Number, Kind: r.Kind, Date: r.AdjDate, AccountID: r.AccountID, AccountCode: a.Code,
			AccountName: a.Name, ProjectID: r.ProjectID, Memo: r.Memo, Status: r.Status, LineCount: len(lines),
		},
		Lines: make([]AdjustmentLine, len(lines)), CreatedBy: r.CreatedByName, CreatedAt: r.CreatedAt,
		VoidedBy: r.VoidedByName, VoidedAt: r.VoidedAt, VoidDate: r.VoidDate, VoidReason: r.VoidReason,
	}
	for i, l := range lines {
		out.Lines[i] = AdjustmentLine{
			ProductID: l.ProductID, ProductCode: l.ProductCode, ProductName: l.ProductName, Unit: l.ProductUnit,
			SystemQty: l.SystemQty, CountedQty: l.CountedQty, Qty: l.Qty, UnitCost: l.UnitCost,
			Value: values[l.ProductID], Memo: l.Memo,
		}
		out.Total = out.Total.Add(values[l.ProductID])
	}
	return out, nil
}

func fieldProblem(field, msg string) error {
	return httpx.Unprocessable("adjustment.invalid", msg).WithFields(map[string]string{field: msg})
}

func adjFirst(f map[string]string) string {
	for _, k := range []string{"kind", "date", "accountId", "memo", "lines"} {
		if m, ok := f[k]; ok {
			return m
		}
	}
	for i := 0; i < maxAdjustmentLines; i++ {
		for _, k := range []string{"productId", "qty", "countedQty", "unitCost", "memo"} {
			if m, ok := f[fmt.Sprintf("lines.%d.%s", i, k)]; ok {
				return m
			}
		}
	}
	for _, m := range f {
		return m
	}
	return ""
}

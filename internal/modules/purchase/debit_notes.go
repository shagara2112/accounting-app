package purchase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger"
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/purchase/internal/store"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
)

// Nota debit dan retur pembelian (AP-05). Nota selalu merujuk baris tagihan asalnya, memakai
// kode pajak pada tanggal tagihan, dan langsung diposting saat disimpan: kredit akun baris dan
// PPN masukan; debit utang usaha sebesar sisa tagihan, PPh yang batal dipotong, dan sisanya
// (tagihan sudah lunas) ke uang muka pembelian sebagai tagihan ke pemasok. Barang persediaan
// selalu keluar dari stok dengan nilai di tagihan asalnya.
const (
	SourceDebitNote = "purchase.debit_note"
	seriesDebitNote = "ND"
)

type DebitLineInput struct {
	BillLineID uuid.UUID     `json:"billLineId"`
	Qty        money.Amount  `json:"qty"`
	UnitPrice  *money.Amount `json:"unitPrice"` // kosong = harga di tagihan
}

type DebitNoteInput struct {
	BillID uuid.UUID        `json:"billId"`
	Date   civil.Date       `json:"date"`
	Memo   string           `json:"memo"`
	Lines  []DebitLineInput `json:"lines"`
}

// DebitNote: Applied mengurangi sisa utang tagihan; Excess menjadi uang muka pembelian.
type DebitNote struct {
	ID           uuid.UUID    `json:"id"`
	Number       string       `json:"number"`
	Status       string       `json:"status"`
	SupplierID   uuid.UUID    `json:"supplierId"`
	SupplierName string       `json:"supplierName"`
	BillID       uuid.UUID    `json:"billId"`
	BillNumber   string       `json:"billNumber"`
	Date         civil.Date   `json:"date"`
	Memo         string       `json:"memo"`
	DPP          money.Amount `json:"dpp"`
	VAT          money.Amount `json:"vat"`
	Withholding  money.Amount `json:"withholding"`
	Total        money.Amount `json:"total"`
	Payable      money.Amount `json:"payable"`
	Applied      money.Amount `json:"applied"`
	Excess       money.Amount `json:"excess"`
}

type DebitNoteLine struct {
	trade.Line
	BillLineID  uuid.UUID `json:"billLineId"`
	AccountCode string    `json:"accountCode"`
	AccountName string    `json:"accountName"`
}

type DebitNoteDetail struct {
	DebitNote
	SupplierInvoiceNo string          `json:"supplierInvoiceNo"`
	Totals            trade.Totals    `json:"totals"`
	Lines             []DebitNoteLine `json:"lines"`
	JournalEntryID    uuid.UUID       `json:"journalEntryId"`
	CreatedAt         time.Time       `json:"createdAt"`
	CreatedBy         *string         `json:"createdBy,omitempty"`
	VoidedBy          *string         `json:"voidedBy,omitempty"`
	VoidedAt          *time.Time      `json:"voidedAt,omitempty"`
	VoidReason        *string         `json:"voidReason,omitempty"`
}

// BillDebit adalah nota debit di halaman tagihan.
type BillDebit struct {
	DebitNoteID uuid.UUID    `json:"debitNoteId"`
	Number      string       `json:"number"`
	Date        civil.Date   `json:"date"`
	Status      string       `json:"status"`
	Total       money.Amount `json:"total"`
	Applied     money.Amount `json:"applied"`
}

type DebitNoteFilter struct {
	From, To   civil.Date
	SupplierID *uuid.UUID
}

func (s *service) CreateDebitNote(ctx context.Context, companyID uuid.UUID, in DebitNoteInput, actor audit.Actor) (DebitNoteDetail, error) {
	var out DebitNoteDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		in.Memo = strings.TrimSpace(in.Memo)
		f := map[string]string{}
		if in.Date.IsZero() {
			f["date"] = "Tanggal nota wajib diisi."
		}
		if utf8.RuneCountInString(in.Memo) > 500 {
			f["memo"] = "Alasan maksimal 500 karakter."
		}
		if len(in.Lines) == 0 {
			f["lines"] = "Pilih minimal satu baris tagihan."
		}
		if len(f) > 0 {
			return httpx.Unprocessable("debit_note.invalid", trade.FirstMessage(f)).WithFields(f)
		}
		// Kunci tagihan: nota debit dan pembayaran bersamaan tidak boleh melebihi sisa tagihan.
		b, err := q.GetBillForUpdate(ctx, store.GetBillForUpdateParams{CompanyID: companyID, ID: in.BillID})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Unprocessable("debit_note.invalid", "Tagihan tidak ditemukan.").WithFields(map[string]string{"billId": "Tagihan tidak ditemukan."})
		}
		if err != nil {
			return err
		}
		number := deref(b.Number)
		if err := rbac.CheckBranch(ctx, companyID, b.BranchID); err != nil {
			return err
		}
		switch {
		case b.Status != "posted":
			return httpx.Conflict("debit_note.bill_state", "Nota debit hanya untuk tagihan yang sudah diposting dan tidak di-void.")
		case b.Opening:
			return httpx.Conflict("debit_note.opening", fmt.Sprintf("Tagihan %s adalah saldo awal; koreksinya lewat void dan impor ulang.", number))
		case in.Date.Before(b.BillDate):
			msg := fmt.Sprintf("Tanggal nota tidak boleh sebelum tanggal tagihan (%s).", b.BillDate.Indonesian())
			return httpx.Unprocessable("debit_note.invalid", msg).WithFields(map[string]string{"date": msg})
		}

		stored, err := q.BillLines(ctx, store.BillLinesParams{CompanyID: companyID, BillID: b.ID})
		if err != nil {
			return err
		}
		byID := map[uuid.UUID]store.PurchaseBillLine{}
		var productIDs []uuid.UUID
		for _, l := range stored {
			byID[l.ID] = l
			if l.ProductID != nil {
				productIDs = append(productIDs, *l.ProductID)
			}
		}
		debited, err := q.DebitedByLine(ctx, store.DebitedByLineParams{CompanyID: companyID, BillID: b.ID})
		if err != nil {
			return err
		}
		prev := map[uuid.UUID]store.DebitedByLineRow{}
		for _, d := range debited {
			prev[d.BillLineID] = d
		}
		products, err := s.Inventory.ProductsByIDs(ctx, companyID, productIDs)
		if err != nil {
			return err
		}
		goods := func(o store.PurchaseBillLine) bool {
			return o.ProductID != nil && products[*o.ProductID].Goods()
		}

		inputs := make([]trade.LineInput, len(in.Lines))
		seen := map[uuid.UUID]bool{}
		for i, l := range in.Lines {
			key := fmt.Sprintf("lines.%d.", i)
			o, ok := byID[l.BillLineID]
			switch {
			case !ok:
				f[key+"billLineId"] = "Baris ini bukan baris tagihan asal."
				continue
			case seen[l.BillLineID]:
				f[key+"billLineId"] = "Baris tagihan ini sudah dipilih."
				continue
			}
			seen[l.BillLineID] = true
			price := o.UnitPrice
			if l.UnitPrice != nil {
				price = *l.UnitPrice
			}
			switch {
			case l.Qty.Sign() <= 0:
				f[key+"qty"] = "Kuantitas harus lebih dari 0."
			case l.Qty.Cmp(o.Qty) > 0:
				f[key+"qty"] = fmt.Sprintf("Melebihi kuantitas di tagihan (%s).", inventory.FormatQty(o.Qty))
			case goods(o) && prev[o.ID].Qty.Add(l.Qty).Cmp(o.Qty) > 0:
				f[key+"qty"] = fmt.Sprintf("Barang yang diretur melebihi kuantitas tagihan; sudah diretur %s.", inventory.FormatQty(prev[o.ID].Qty))
			}
			if price.Sign() < 0 || price.Cmp(o.UnitPrice) > 0 {
				f[key+"unitPrice"] = fmt.Sprintf("Harga 0 sampai harga di tagihan (%s).", o.UnitPrice.IDR())
			}
			inputs[i] = trade.LineInput{
				Description: o.Description, AccountID: o.AccountID, Qty: l.Qty, UnitPrice: price,
				DiscountPct: o.DiscountPct, TaxCode: o.TaxCode, ProductID: o.ProductID, ProjectID: o.ProjectID,
			}
		}
		if len(f) > 0 {
			return httpx.Unprocessable("debit_note.invalid", trade.FirstMessage(f)).WithFields(f)
		}
		table, err := s.Tax.Table(ctx)
		if err != nil {
			return err
		}
		lines, t, err := trade.Compute(table, b.BillDate, inputs, true)
		if err != nil {
			return err
		}
		for i, l := range lines {
			o := byID[in.Lines[i].BillLineID]
			if left := o.Net.Sub(prev[o.ID].Net); l.Net.Cmp(left) > 0 {
				f[fmt.Sprintf("lines.%d.qty", i)] = fmt.Sprintf("Nilai melebihi sisa baris tagihan yang belum dikembalikan (%s).", left.IDR())
			}
		}
		if len(f) > 0 {
			return httpx.Unprocessable("debit_note.invalid", trade.FirstMessage(f)).WithFields(f)
		}
		company, err := s.Core.Company(ctx, companyID)
		if err != nil {
			return err
		}
		var whCode *tax.Code
		var withholding money.Amount
		if b.WithholdingCode != nil {
			code, err := table.Find(*b.WithholdingCode, b.BillDate)
			if err != nil {
				return err
			}
			whCode, withholding = &code, code.Withholding(t.DPP)
		}
		payable := t.Total.Sub(withholding)

		settled, err := q.PaidByBill(ctx, store.PaidByBillParams{CompanyID: companyID, Ids: []uuid.UUID{b.ID}})
		if err != nil {
			return err
		}
		rest := b.Payable
		for _, p := range settled {
			rest = rest.Sub(p.Paid)
		}
		applied := payable
		if rest.Cmp(applied) < 0 {
			applied = rest
		}
		if applied.Sign() < 0 {
			applied = money.Amount{}
		}
		excess := payable.Sub(applied)

		noteNumber, err := s.Ledger.NextNumberFor(ctx, companyID, seriesDebitNote, in.Date, b.BranchID)
		if err != nil {
			return err
		}
		supplierName, err := s.Core.ContactNames(ctx, companyID, []uuid.UUID{b.SupplierID})
		if err != nil {
			return err
		}
		memo := "Nota debit " + noteNumber + " atas " + number + " " + supplierName[b.SupplierID]
		if in.Memo != "" {
			memo += " · " + in.Memo
		}
		var drafts []ledger.LineDraft
		if applied.Sign() > 0 {
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.Payable)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Debit: applied, Memo: memo})
		}
		if excess.Sign() > 0 {
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.SupplierAdvance)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Debit: excess, Memo: "Kelebihan nota debit " + noteNumber + " (tagihan sudah lunas)"})
		}
		if withholding.Sign() > 0 {
			role := ledger.WithholdingPPh23
			if whCode.Article == tax.ArticleFinal {
				role = ledger.WithholdingPPhFinal
			}
			acc, err := s.Ledger.RoleAccount(ctx, companyID, role)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Debit: withholding, Memo: whCode.Name + " batal dipotong " + noteNumber})
		}
		for _, d := range debitsByAccount(lines, !company.PKP) {
			d.Credit, d.Debit = d.Debit, money.Amount{}
			drafts = append(drafts, d)
		}
		if company.PKP && t.VAT.Sign() > 0 {
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.InputVAT)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Credit: t.VAT, Memo: "PPN masukan retur " + noteNumber})
		}
		for i := range drafts {
			drafts[i].BranchID, drafts[i].DepartmentID = b.BranchID, b.DepartmentID
		}
		id := uuid.Must(uuid.NewV7())
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: in.Date, Memo: memo,
			SourceType: SourceDebitNote, SourceID: &id, Number: noteNumber, Lines: drafts, Actor: actor,
		})
		if err != nil {
			return err
		}
		if err := q.InsertDebitNote(ctx, store.InsertDebitNoteParams{
			ID: id, CompanyID: companyID, Number: noteNumber, SupplierID: b.SupplierID, BillID: b.ID,
			NoteDate: in.Date, Memo: in.Memo, Subtotal: t.Subtotal, Discount: t.Discount, Dpp: t.DPP,
			OtherDpp: t.OtherDPP, Vat: t.VAT, Withholding: withholding, Total: t.Total, Payable: payable,
			Applied: applied, JournalEntryID: posted.ID, CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		var back []inventory.Receipt
		for i, l := range lines {
			if err := q.InsertDebitNoteLine(ctx, store.InsertDebitNoteLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, DebitNoteID: id, LineNo: int16(i + 1),
				BillLineID: in.Lines[i].BillLineID, Description: l.Description, AccountID: l.AccountID,
				ProductID: l.ProductID, ProjectID: l.ProjectID, Qty: l.Qty, UnitPrice: l.UnitPrice,
				DiscountPct: l.DiscountPct, Discount: l.Discount, Net: l.Net, TaxCode: l.TaxCode,
				OtherDpp: l.OtherDPP, Vat: l.VAT,
			}); err != nil {
				return err
			}
			// Barang keluar bernilai sama dengan kredit persediaannya di jurnal nota.
			if goods(byID[in.Lines[i].BillLineID]) {
				value := l.Net
				if !company.PKP {
					value = value.Add(l.VAT)
				}
				back = append(back, inventory.Receipt{ProductID: *l.ProductID, Qty: l.Qty, Value: value, Memo: "Retur " + l.Description})
			}
		}
		if err := s.Inventory.SendBack(ctx, companyID, inventory.Doc{
			SourceType: SourceDebitNote, SourceID: id, Number: noteNumber, Date: in.Date, BranchID: b.BranchID, DepartmentID: b.DepartmentID,
		}, back, actor); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "purchase.debit_note.post",
			ObjectType: "purchase_debit_note", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": noteNumber, "bill": number, "total": t.Total.String(), "vat": t.VAT.String(),
				"withholding": withholding.String(), "applied": applied.String(), "date": in.Date.String()},
		}); err != nil {
			return err
		}
		if s.AfterDebitPost != nil {
			if err := s.AfterDebitPost(ctx, companyID, id, actor); err != nil {
				return err
			}
		}
		out, err = s.DebitNote(ctx, companyID, id)
		return err
	})
	return out, err
}

// VoidDebitNote membalik jurnal nota pada tanggal void dan memasukkan lagi barang returnya.
func (s *service) VoidDebitNote(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (DebitNoteDetail, error) {
	var out DebitNoteDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		n, err := q.GetDebitNoteForUpdate(ctx, store.GetDebitNoteForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("debit_note.not_found", "Nota debit tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if n.Status == "void" {
			return httpx.Conflict("debit_note.already_void", fmt.Sprintf("Nota debit %s sudah di-void.", n.Number))
		}
		if excess := n.Payable.Sub(n.Applied); excess.Sign() > 0 {
			if err := s.checkAdvanceRemoval(ctx, companyID, n.SupplierID, excess, n.Number); err != nil {
				return err
			}
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		v, err := s.Ledger.VoidSource(ctx, companyID, n.JournalEntryID, SourceDebitNote, date, reason, actor)
		if err != nil {
			return err
		}
		if err := s.Inventory.ReverseSource(ctx, companyID, SourceDebitNote, id, date, actor); err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if c, err := q.MarkDebitNoteVoid(ctx, store.MarkDebitNoteVoidParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || c != 1 {
			return errOr(err, "nota debit gagal ditandai void")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "purchase.debit_note.void",
			ObjectType: "purchase_debit_note", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": n.Number, "status": "posted"},
			After:  map[string]any{"number": n.Number, "status": "void", "reason": reason, "reversal": v.Reversal.Number},
		}); err != nil {
			return err
		}
		if s.AfterDebitVoid != nil {
			if err := s.AfterDebitVoid(ctx, companyID, id, date, reason, actor); err != nil {
				return err
			}
		}
		out, err = s.DebitNote(ctx, companyID, id)
		return err
	})
	return out, err
}

func debitSummary(n store.PurchaseDebitNote, billNumber *string, names map[uuid.UUID]string) DebitNote {
	return DebitNote{
		ID: n.ID, Number: n.Number, Status: n.Status, SupplierID: n.SupplierID, SupplierName: names[n.SupplierID],
		BillID: n.BillID, BillNumber: deref(billNumber), Date: n.NoteDate, Memo: n.Memo, DPP: n.Dpp, VAT: n.Vat,
		Withholding: n.Withholding, Total: n.Total, Payable: n.Payable, Applied: n.Applied, Excess: n.Payable.Sub(n.Applied),
	}
}

func (s *service) DebitNotes(ctx context.Context, companyID uuid.UUID, f DebitNoteFilter) ([]DebitNote, error) {
	rows, err := s.q(ctx).ListDebitNotes(ctx, store.ListDebitNotesParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, SupplierID: f.SupplierID, Branches: rbac.BranchesFor(ctx, companyID)})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.SupplierID
	}
	names, err := s.Core.ContactNames(ctx, companyID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]DebitNote, len(rows))
	for i, r := range rows {
		out[i] = debitSummary(store.PurchaseDebitNote{
			ID: r.ID, Number: r.Number, Status: r.Status, SupplierID: r.SupplierID, BillID: r.BillID, NoteDate: r.NoteDate,
			Memo: r.Memo, Dpp: r.Dpp, Vat: r.Vat, Withholding: r.Withholding, Total: r.Total, Payable: r.Payable, Applied: r.Applied,
		}, r.BillNumber, names)
	}
	return out, nil
}

func (s *service) DebitNote(ctx context.Context, companyID, id uuid.UUID) (DebitNoteDetail, error) {
	q := s.q(ctx)
	r, err := q.GetDebitNote(ctx, store.GetDebitNoteParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return DebitNoteDetail{}, httpx.NotFound("debit_note.not_found", "Nota debit tidak ditemukan.")
	}
	if err != nil {
		return DebitNoteDetail{}, err
	}
	names, err := s.Core.ContactNames(ctx, companyID, []uuid.UUID{r.SupplierID})
	if err != nil {
		return DebitNoteDetail{}, err
	}
	out := DebitNoteDetail{
		DebitNote: debitSummary(store.PurchaseDebitNote{
			ID: r.ID, Number: r.Number, Status: r.Status, SupplierID: r.SupplierID, BillID: r.BillID, NoteDate: r.NoteDate,
			Memo: r.Memo, Dpp: r.Dpp, Vat: r.Vat, Withholding: r.Withholding, Total: r.Total, Payable: r.Payable, Applied: r.Applied,
		}, r.BillNumber, names),
		SupplierInvoiceNo: r.SupplierInvoiceNo,
		Totals:            trade.Totals{Subtotal: r.Subtotal, Discount: r.Discount, DPP: r.Dpp, OtherDPP: r.OtherDpp, VAT: r.Vat, Total: r.Total},
		JournalEntryID:    r.JournalEntryID, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedByName,
		VoidedBy: r.VoidedByName, VoidedAt: r.VoidedAt, VoidReason: r.VoidReason,
	}
	lines, err := q.DebitNoteLines(ctx, store.DebitNoteLinesParams{CompanyID: companyID, DebitNoteID: id})
	if err != nil {
		return DebitNoteDetail{}, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return DebitNoteDetail{}, err
	}
	for _, l := range lines {
		a := accounts[l.AccountID]
		out.Lines = append(out.Lines, DebitNoteLine{
			Line: trade.Line{
				LineInput: trade.LineInput{
					Description: l.Description, AccountID: l.AccountID, Qty: l.Qty, UnitPrice: l.UnitPrice,
					DiscountPct: l.DiscountPct, TaxCode: l.TaxCode, ProductID: l.ProductID, ProjectID: l.ProjectID,
				},
				Gross: l.Net.Add(l.Discount), Discount: l.Discount, Net: l.Net, OtherDPP: l.OtherDpp, VAT: l.Vat,
			},
			BillLineID: l.BillLineID, AccountCode: a.Code, AccountName: a.Name,
		})
	}
	return out, nil
}

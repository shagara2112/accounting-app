package sales

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
	"lajur/internal/modules/sales/internal/store"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
)

// Nota kredit dan retur penjualan (AR-05). Nota selalu merujuk baris faktur asalnya, memakai
// kode pajak dan tarif pada tanggal faktur, dan langsung diposting saat disimpan:
// debit pendapatan dan PPN keluaran; kredit piutang usaha sebesar sisa faktur, dan sisanya
// (faktur sudah lunas) ke uang muka pelanggan. Barang yang dicentang kembali ke stok dengan
// HPP saat barang itu keluar.
const (
	SourceCreditNote = "sales.credit_note"
	seriesCreditNote = "NK"
)

type CreditLineInput struct {
	InvoiceLineID uuid.UUID     `json:"invoiceLineId"`
	Qty           money.Amount  `json:"qty"`
	UnitPrice     *money.Amount `json:"unitPrice"` // kosong = harga di faktur
	Restock       bool          `json:"restock"`
}

type CreditNoteInput struct {
	InvoiceID uuid.UUID         `json:"invoiceId"`
	Date      civil.Date        `json:"date"`
	Memo      string            `json:"memo"`
	Lines     []CreditLineInput `json:"lines"`
}

// CreditNote: Applied mengurangi sisa piutang faktur; Excess menjadi uang muka pelanggan.
type CreditNote struct {
	ID            uuid.UUID    `json:"id"`
	Number        string       `json:"number"`
	Status        string       `json:"status"`
	CustomerID    uuid.UUID    `json:"customerId"`
	CustomerName  string       `json:"customerName"`
	InvoiceID     uuid.UUID    `json:"invoiceId"`
	InvoiceNumber string       `json:"invoiceNumber"`
	Date          civil.Date   `json:"date"`
	Memo          string       `json:"memo"`
	DPP           money.Amount `json:"dpp"`
	VAT           money.Amount `json:"vat"`
	Total         money.Amount `json:"total"`
	Applied       money.Amount `json:"applied"`
	Excess        money.Amount `json:"excess"`
}

type CreditNoteLine struct {
	trade.Line
	InvoiceLineID uuid.UUID `json:"invoiceLineId"`
	Restock       bool      `json:"restock"`
	AccountCode   string    `json:"accountCode"`
	AccountName   string    `json:"accountName"`
}

type CreditNoteDetail struct {
	CreditNote
	Totals         trade.Totals     `json:"totals"`
	Lines          []CreditNoteLine `json:"lines"`
	JournalEntryID uuid.UUID        `json:"journalEntryId"`
	CreatedAt      time.Time        `json:"createdAt"`
	CreatedBy      *string          `json:"createdBy,omitempty"`
	VoidedBy       *string          `json:"voidedBy,omitempty"`
	VoidedAt       *time.Time       `json:"voidedAt,omitempty"`
	VoidReason     *string          `json:"voidReason,omitempty"`
}

// InvoiceCredit adalah nota kredit di halaman faktur.
type InvoiceCredit struct {
	CreditNoteID uuid.UUID    `json:"creditNoteId"`
	Number       string       `json:"number"`
	Date         civil.Date   `json:"date"`
	Status       string       `json:"status"`
	Total        money.Amount `json:"total"`
	Applied      money.Amount `json:"applied"`
}

type CreditNoteFilter struct {
	From, To   civil.Date
	CustomerID *uuid.UUID
}

func (s *service) CreateCreditNote(ctx context.Context, companyID uuid.UUID, in CreditNoteInput, actor audit.Actor) (CreditNoteDetail, error) {
	var out CreditNoteDetail
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
			f["lines"] = "Pilih minimal satu baris faktur."
		}
		if len(f) > 0 {
			return httpx.Unprocessable("credit_note.invalid", trade.FirstMessage(f)).WithFields(f)
		}
		// Kunci faktur: nota kredit dan pelunasan bersamaan tidak boleh melebihi sisa faktur.
		inv, err := q.GetInvoiceForUpdate(ctx, store.GetInvoiceForUpdateParams{CompanyID: companyID, ID: in.InvoiceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Unprocessable("credit_note.invalid", "Faktur tidak ditemukan.").WithFields(map[string]string{"invoiceId": "Faktur tidak ditemukan."})
		}
		if err != nil {
			return err
		}
		number := deref(inv.Number)
		if err := rbac.CheckBranch(ctx, companyID, inv.BranchID); err != nil {
			return err
		}
		switch {
		case inv.Status != "posted":
			return httpx.Conflict("credit_note.invoice_state", "Nota kredit hanya untuk faktur yang sudah diposting dan tidak di-void.")
		case inv.Kind != KindRegular:
			return httpx.Conflict("credit_note.invoice_kind", fmt.Sprintf(
				"Faktur %s adalah faktur %s proyek; koreksinya lewat void dan faktur baru dari halaman proyek.", number, kindLabel(inv.Kind)))
		case in.Date.Before(inv.InvoiceDate):
			msg := fmt.Sprintf("Tanggal nota tidak boleh sebelum tanggal faktur (%s).", inv.InvoiceDate.Indonesian())
			return httpx.Unprocessable("credit_note.invalid", msg).WithFields(map[string]string{"date": msg})
		}

		stored, err := q.InvoiceLines(ctx, store.InvoiceLinesParams{CompanyID: companyID, InvoiceID: inv.ID})
		if err != nil {
			return err
		}
		byID := map[uuid.UUID]store.SalesInvoiceLine{}
		var productIDs []uuid.UUID
		for _, l := range stored {
			byID[l.ID] = l
			if l.ProductID != nil {
				productIDs = append(productIDs, *l.ProductID)
			}
		}
		credited, err := q.CreditedByLine(ctx, store.CreditedByLineParams{CompanyID: companyID, InvoiceID: inv.ID})
		if err != nil {
			return err
		}
		prev := map[uuid.UUID]store.CreditedByLineRow{}
		for _, c := range credited {
			prev[c.InvoiceLineID] = c
		}
		products, err := s.Inventory.ProductsByIDs(ctx, companyID, productIDs)
		if err != nil {
			return err
		}

		inputs := make([]trade.LineInput, len(in.Lines))
		seen := map[uuid.UUID]bool{}
		for i, l := range in.Lines {
			key := fmt.Sprintf("lines.%d.", i)
			o, ok := byID[l.InvoiceLineID]
			switch {
			case !ok:
				f[key+"invoiceLineId"] = "Baris ini bukan baris faktur asal."
				continue
			case seen[l.InvoiceLineID]:
				f[key+"invoiceLineId"] = "Baris faktur ini sudah dipilih."
				continue
			}
			seen[l.InvoiceLineID] = true
			price := o.UnitPrice
			if l.UnitPrice != nil {
				price = *l.UnitPrice
			}
			switch {
			case l.Qty.Sign() <= 0:
				f[key+"qty"] = "Kuantitas harus lebih dari 0."
			case l.Qty.Cmp(o.Qty) > 0:
				f[key+"qty"] = fmt.Sprintf("Melebihi kuantitas di faktur (%s).", inventory.FormatQty(o.Qty))
			}
			if price.Sign() < 0 || price.Cmp(o.UnitPrice) > 0 {
				f[key+"unitPrice"] = fmt.Sprintf("Harga 0 sampai harga di faktur (%s).", o.UnitPrice.IDR())
			}
			if l.Restock {
				p, ok := products[derefID(o.ProductID)]
				switch {
				case o.ProductID == nil || !ok || !p.Goods():
					f[key+"restock"] = "Hanya barang persediaan yang bisa kembali ke stok."
				case prev[o.ID].Restocked.Add(l.Qty).Cmp(o.Qty) > 0:
					f[key+"qty"] = fmt.Sprintf("Barang yang kembali ke stok melebihi kuantitas faktur; sudah diretur %s.", inventory.FormatQty(prev[o.ID].Restocked))
				}
			}
			inputs[i] = trade.LineInput{
				Description: o.Description, AccountID: o.AccountID, Qty: l.Qty, UnitPrice: price,
				DiscountPct: o.DiscountPct, TaxCode: o.TaxCode, ProductID: o.ProductID,
			}
		}
		if len(f) > 0 {
			return httpx.Unprocessable("credit_note.invalid", trade.FirstMessage(f)).WithFields(f)
		}
		table, err := s.Tax.Table(ctx)
		if err != nil {
			return err
		}
		// Pajak retur mengikuti faktur asalnya, termasuk tarifnya.
		lines, t, err := trade.Compute(table, inv.InvoiceDate, inputs, true)
		if err != nil {
			return err
		}
		for i, l := range lines {
			o := byID[in.Lines[i].InvoiceLineID]
			if left := o.Net.Sub(prev[o.ID].Net); l.Net.Cmp(left) > 0 {
				f[fmt.Sprintf("lines.%d.qty", i)] = fmt.Sprintf("Nilai melebihi sisa baris faktur yang belum dikreditkan (%s).", left.IDR())
			}
		}
		if len(f) > 0 {
			return httpx.Unprocessable("credit_note.invalid", trade.FirstMessage(f)).WithFields(f)
		}

		settled, err := q.PaidByInvoice(ctx, store.PaidByInvoiceParams{CompanyID: companyID, Ids: []uuid.UUID{inv.ID}})
		if err != nil {
			return err
		}
		rest := receivableOf(inv)
		for _, p := range settled {
			rest = rest.Sub(p.Paid)
		}
		applied := t.Total
		if rest.Cmp(applied) < 0 {
			applied = rest
		}
		if applied.Sign() < 0 {
			applied = money.Amount{}
		}
		excess := t.Total.Sub(applied)

		noteNumber, err := s.Ledger.NextNumberFor(ctx, companyID, seriesCreditNote, in.Date, inv.BranchID)
		if err != nil {
			return err
		}
		memo := "Nota kredit " + noteNumber + " atas " + number
		if in.Memo != "" {
			memo += " · " + in.Memo
		}
		project := inv.ProjectID
		drafts := revenueDebits(lines, project)
		if t.VAT.Sign() > 0 {
			// Retur membalik PPN di akun yang dikredit fakturnya (mis. PPN keluaran dipungut).
			vatAccount := inv.VatAccountID
			if vatAccount == nil {
				acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.OutputVAT)
				if err != nil {
					return err
				}
				vatAccount = &acc.ID
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: *vatAccount, Debit: t.VAT, Memo: "PPN keluaran retur " + noteNumber, ProjectID: project})
		}
		if applied.Sign() > 0 {
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.Receivable)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Credit: applied, Memo: memo, ProjectID: project})
		}
		if excess.Sign() > 0 {
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.CustomerAdvance)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Credit: excess, Memo: "Kelebihan nota kredit " + noteNumber + " (faktur sudah lunas)", ProjectID: project})
		}
		withDimensions(drafts, inv.BranchID, inv.DepartmentID)
		id := uuid.Must(uuid.NewV7())
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: in.Date, Memo: memo,
			SourceType: SourceCreditNote, SourceID: &id, Number: noteNumber, Lines: drafts, Actor: actor,
		})
		if err != nil {
			return err
		}
		if err := q.InsertCreditNote(ctx, store.InsertCreditNoteParams{
			ID: id, CompanyID: companyID, Number: noteNumber, CustomerID: inv.CustomerID, InvoiceID: inv.ID,
			NoteDate: in.Date, Memo: in.Memo, Subtotal: t.Subtotal, Discount: t.Discount, Dpp: t.DPP,
			OtherDpp: t.OtherDPP, Vat: t.VAT, Total: t.Total, Applied: applied, JournalEntryID: posted.ID,
			CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		var returns []inventory.Issue
		for i, l := range lines {
			if err := q.InsertCreditNoteLine(ctx, store.InsertCreditNoteLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, CreditNoteID: id, LineNo: int16(i + 1),
				InvoiceLineID: in.Lines[i].InvoiceLineID, Description: l.Description, AccountID: l.AccountID,
				ProductID: l.ProductID, Qty: l.Qty, UnitPrice: l.UnitPrice, DiscountPct: l.DiscountPct,
				Discount: l.Discount, Net: l.Net, TaxCode: l.TaxCode, OtherDpp: l.OtherDPP, Vat: l.VAT,
				Restock: in.Lines[i].Restock,
			}); err != nil {
				return err
			}
			if in.Lines[i].Restock {
				returns = append(returns, inventory.Issue{ProductID: *l.ProductID, Qty: l.Qty, Memo: "Retur " + l.Description})
			}
		}
		if err := s.Inventory.ReturnGoods(ctx, companyID, inventory.Doc{
			SourceType: SourceCreditNote, SourceID: id, Number: noteNumber, Date: in.Date, ProjectID: project,
		}, SourceInvoice, inv.ID, returns, actor); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.credit_note.post",
			ObjectType: "sales_credit_note", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": noteNumber, "invoice": number, "total": t.Total.String(), "vat": t.VAT.String(),
				"applied": applied.String(), "date": in.Date.String()},
		}); err != nil {
			return err
		}
		if s.AfterCreditPost != nil {
			if err := s.AfterCreditPost(ctx, companyID, id, actor); err != nil {
				return err
			}
		}
		out, err = s.CreditNote(ctx, companyID, id)
		return err
	})
	return out, err
}

// revenueDebits membalik kredit pendapatan faktur: debit per akun, urut kemunculan pertama.
func revenueDebits(lines []trade.Line, project *uuid.UUID) []ledger.LineDraft {
	out := creditsByAccount(lines, false, project)
	for i := range out {
		out[i].Debit, out[i].Credit = out[i].Credit, money.Amount{}
	}
	return out
}

// VoidCreditNote membalik jurnal nota pada tanggal void dan mengeluarkan lagi barang returnya.
func (s *service) VoidCreditNote(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (CreditNoteDetail, error) {
	var out CreditNoteDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		n, err := q.GetCreditNoteForUpdate(ctx, store.GetCreditNoteForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("credit_note.not_found", "Nota kredit tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if n.Status == "void" {
			return httpx.Conflict("credit_note.already_void", fmt.Sprintf("Nota kredit %s sudah di-void.", n.Number))
		}
		if excess := n.Total.Sub(n.Applied); excess.Sign() > 0 {
			if err := s.checkAdvanceRemoval(ctx, companyID, n.CustomerID, excess, n.Number); err != nil {
				return err
			}
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		v, err := s.Ledger.VoidSource(ctx, companyID, n.JournalEntryID, SourceCreditNote, date, reason, actor)
		if err != nil {
			return err
		}
		if err := s.Inventory.ReverseSource(ctx, companyID, SourceCreditNote, id, date, actor); err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if c, err := q.MarkCreditNoteVoid(ctx, store.MarkCreditNoteVoidParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || c != 1 {
			return errOr(err, "nota kredit gagal ditandai void")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.credit_note.void",
			ObjectType: "sales_credit_note", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": n.Number, "status": "posted"},
			After:  map[string]any{"number": n.Number, "status": "void", "reason": reason, "reversal": v.Reversal.Number},
		}); err != nil {
			return err
		}
		if s.AfterCreditVoid != nil {
			if err := s.AfterCreditVoid(ctx, companyID, id, date, reason, actor); err != nil {
				return err
			}
		}
		out, err = s.CreditNote(ctx, companyID, id)
		return err
	})
	return out, err
}

func noteSummary(n store.SalesCreditNote, invoiceNumber *string, names map[uuid.UUID]string) CreditNote {
	return CreditNote{
		ID: n.ID, Number: n.Number, Status: n.Status, CustomerID: n.CustomerID, CustomerName: names[n.CustomerID],
		InvoiceID: n.InvoiceID, InvoiceNumber: deref(invoiceNumber), Date: n.NoteDate, Memo: n.Memo, DPP: n.Dpp,
		VAT: n.Vat, Total: n.Total, Applied: n.Applied, Excess: n.Total.Sub(n.Applied),
	}
}

func (s *service) CreditNotes(ctx context.Context, companyID uuid.UUID, f CreditNoteFilter) ([]CreditNote, error) {
	rows, err := s.q(ctx).ListCreditNotes(ctx, store.ListCreditNotesParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, CustomerID: f.CustomerID, Branches: rbac.BranchesFor(ctx, companyID)})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.CustomerID
	}
	names, err := s.Core.ContactNames(ctx, companyID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]CreditNote, len(rows))
	for i, r := range rows {
		out[i] = noteSummary(store.SalesCreditNote{
			ID: r.ID, Number: r.Number, Status: r.Status, CustomerID: r.CustomerID, InvoiceID: r.InvoiceID,
			NoteDate: r.NoteDate, Memo: r.Memo, Dpp: r.Dpp, Vat: r.Vat, Total: r.Total, Applied: r.Applied,
		}, r.InvoiceNumber, names)
	}
	return out, nil
}

func (s *service) CreditNote(ctx context.Context, companyID, id uuid.UUID) (CreditNoteDetail, error) {
	q := s.q(ctx)
	r, err := q.GetCreditNote(ctx, store.GetCreditNoteParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return CreditNoteDetail{}, httpx.NotFound("credit_note.not_found", "Nota kredit tidak ditemukan.")
	}
	if err != nil {
		return CreditNoteDetail{}, err
	}
	names, err := s.Core.ContactNames(ctx, companyID, []uuid.UUID{r.CustomerID})
	if err != nil {
		return CreditNoteDetail{}, err
	}
	out := CreditNoteDetail{
		CreditNote: noteSummary(store.SalesCreditNote{
			ID: r.ID, Number: r.Number, Status: r.Status, CustomerID: r.CustomerID, InvoiceID: r.InvoiceID,
			NoteDate: r.NoteDate, Memo: r.Memo, Dpp: r.Dpp, Vat: r.Vat, Total: r.Total, Applied: r.Applied,
		}, r.InvoiceNumber, names),
		Totals:         trade.Totals{Subtotal: r.Subtotal, Discount: r.Discount, DPP: r.Dpp, OtherDPP: r.OtherDpp, VAT: r.Vat, Total: r.Total},
		JournalEntryID: r.JournalEntryID, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedByName,
		VoidedBy: r.VoidedByName, VoidedAt: r.VoidedAt, VoidReason: r.VoidReason,
	}
	lines, err := q.CreditNoteLines(ctx, store.CreditNoteLinesParams{CompanyID: companyID, CreditNoteID: id})
	if err != nil {
		return CreditNoteDetail{}, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return CreditNoteDetail{}, err
	}
	for _, l := range lines {
		a := accounts[l.AccountID]
		out.Lines = append(out.Lines, CreditNoteLine{
			Line: trade.Line{
				LineInput: trade.LineInput{
					Description: l.Description, AccountID: l.AccountID, Qty: l.Qty, UnitPrice: l.UnitPrice,
					DiscountPct: l.DiscountPct, TaxCode: l.TaxCode, ProductID: l.ProductID,
				},
				Gross: l.Net.Add(l.Discount), Discount: l.Discount, Net: l.Net, OtherDPP: l.OtherDpp, VAT: l.Vat,
			},
			InvoiceLineID: l.InvoiceLineID, Restock: l.Restock, AccountCode: a.Code, AccountName: a.Name,
		})
	}
	return out, nil
}

func derefID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

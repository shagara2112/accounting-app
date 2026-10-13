// Package sales adalah modul penjualan dan piutang (AR): faktur penjualan (AR-02), pelunasan
// dengan potongan PPh 23 atau PPh final 4(2) (AR-04), dan umur piutang (AR-06). Setiap faktur
// memposting jurnal: debit piutang usaha, kredit pendapatan dan PPN keluaran.
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
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/core"
	"lajur/internal/modules/sales/internal/store"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const (
	SourceInvoice = "sales.invoice"
	SourceReceipt = "sales.receipt"

	seriesInvoice = "FJ"  // faktur jual
	seriesReceipt = "BKM" // bukti kas masuk, satu seri dengan penerimaan kas
)

type InvoiceInput struct {
	CustomerID uuid.UUID         `json:"customerId"`
	Date       civil.Date        `json:"date"`
	DueDate    civil.Date        `json:"dueDate"` // kosong = tanggal + termin pelanggan
	Memo       string            `json:"memo"`
	Lines      []trade.LineInput `json:"lines"`
	Version    int               `json:"version"` // hanya untuk perubahan draf
}

// Invoice adalah ringkasan faktur. State: draft, open (belum lunas), paid, atau void.
type Invoice struct {
	ID           uuid.UUID    `json:"id"`
	Number       *string      `json:"number"`
	Status       string       `json:"status"`
	State        string       `json:"state"`
	CustomerID   uuid.UUID    `json:"customerId"`
	CustomerName string       `json:"customerName"`
	Date         civil.Date   `json:"date"`
	DueDate      civil.Date   `json:"dueDate"`
	Memo         string       `json:"memo"`
	DPP          money.Amount `json:"dpp"` // dasar PPh yang dipotong pelanggan
	OtherDPP     money.Amount `json:"otherDpp"`
	VAT          money.Amount `json:"vat"`
	Total        money.Amount `json:"total"`
	Paid         money.Amount `json:"paid"`
	Outstanding  money.Amount `json:"outstanding"`
	DaysOverdue  int          `json:"daysOverdue"`
}

type InvoiceLine struct {
	trade.Line
	AccountCode string `json:"accountCode"`
	AccountName string `json:"accountName"`
}

type InvoicePayment struct {
	ReceiptID       uuid.UUID    `json:"receiptId"`
	Number          string       `json:"number"`
	Date            civil.Date   `json:"date"`
	Status          string       `json:"status"`
	Amount          money.Amount `json:"amount"`
	Withholding     money.Amount `json:"withholding"`
	WithholdingCode *string      `json:"withholdingCode,omitempty"`
}

type InvoiceDetail struct {
	Invoice
	Totals         trade.Totals     `json:"totals"`
	Lines          []InvoiceLine    `json:"lines"`
	Payments       []InvoicePayment `json:"payments"`
	JournalEntryID *uuid.UUID       `json:"journalEntryId,omitempty"`
	CreatedByID    *uuid.UUID       `json:"createdById,omitempty"`
	CreatedBy      *string          `json:"createdBy,omitempty"`
	PostedBy       *string          `json:"postedBy,omitempty"`
	PostedAt       *time.Time       `json:"postedAt,omitempty"`
	VoidedBy       *string          `json:"voidedBy,omitempty"`
	VoidedAt       *time.Time       `json:"voidedAt,omitempty"`
	VoidReason     *string          `json:"voidReason,omitempty"`
	Version        int              `json:"version"`
}

type InvoiceFilter struct {
	From, To   civil.Date
	State      string
	CustomerID *uuid.UUID
	Search     string
}

type Service interface {
	Invoices(ctx context.Context, companyID uuid.UUID, f InvoiceFilter) ([]Invoice, error)
	Invoice(ctx context.Context, companyID, id uuid.UUID) (InvoiceDetail, error)
	CreateInvoice(ctx context.Context, companyID uuid.UUID, in InvoiceInput, actor audit.Actor) (InvoiceDetail, error)
	// UpdateInvoice dan DeleteInvoice hanya untuk draf; draf orang lain butuh anyDraft.
	UpdateInvoice(ctx context.Context, companyID, id uuid.UUID, in InvoiceInput, actor audit.Actor, anyDraft bool) (InvoiceDetail, error)
	DeleteInvoice(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor, anyDraft bool) error
	PostInvoice(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (InvoiceDetail, error)
	VoidInvoice(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (InvoiceDetail, error)
	OpenInvoices(ctx context.Context, companyID uuid.UUID, customerID *uuid.UUID) ([]Invoice, error)

	Receipts(ctx context.Context, companyID uuid.UUID, f ReceiptFilter) ([]Receipt, error)
	Receipt(ctx context.Context, companyID, id uuid.UUID) (ReceiptDetail, error)
	CreateReceipt(ctx context.Context, companyID uuid.UUID, in ReceiptInput, actor audit.Actor) (ReceiptDetail, error)
	VoidReceipt(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (ReceiptDetail, error)

	Aging(ctx context.Context, companyID uuid.UUID, asOf civil.Date) (trade.Aging, error)

	// VATByMonth dan Withheld adalah data pendukung modul pajak (TAX-06, TAX-09).
	VATByMonth(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]trade.MonthVAT, error)
	Withheld(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]Withheld, error)
}

type Deps struct {
	Pool   *pgxpool.Pool
	Ledger ledger.Service
	Core   core.Service
	Tax    tax.Service
	Cash   cash.Service
	// VoidGuard dipanggil sebelum faktur di-void, mis. untuk menolak faktur yang faktur
	// pajaknya sudah terbit di Coretax. Diisi modul pajak saat perangkaian; boleh nil.
	VoidGuard func(ctx context.Context, companyID, invoiceID uuid.UUID) error
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

// revenueClasses: baris faktur penjualan hanya ke akun pendapatan usaha (4) atau lain-lain (7).
var revenueClasses = map[int]bool{4: true, 7: true}

// prepare memeriksa isi faktur dan menghitung baris serta totalnya.
func (s *service) prepare(ctx context.Context, companyID uuid.UUID, in *InvoiceInput) ([]trade.Line, trade.Totals, error) {
	in.Memo = strings.TrimSpace(in.Memo)
	f := map[string]string{}
	if in.Date.IsZero() {
		f["date"] = "Tanggal faktur wajib diisi."
	}
	if utf8.RuneCountInString(in.Memo) > 500 {
		f["memo"] = "Catatan maksimal 500 karakter."
	}
	if in.CustomerID == uuid.Nil {
		f["customerId"] = "Pilih pelanggan."
	}
	if len(f) > 0 {
		return nil, trade.Totals{}, httpx.Unprocessable("invoice.invalid", trade.FirstMessage(f)).WithFields(f)
	}
	customer, err := s.Core.Contact(ctx, companyID, in.CustomerID)
	if err != nil {
		return nil, trade.Totals{}, fieldErr(err, "customerId")
	}
	if !customer.IsCustomer || !customer.Active {
		msg := fmt.Sprintf("%s bukan pelanggan aktif.", customer.Name)
		return nil, trade.Totals{}, httpx.Unprocessable("invoice.invalid", msg).WithFields(map[string]string{"customerId": msg})
	}
	if in.DueDate.IsZero() {
		in.DueDate = in.Date.AddDays(customer.PaymentTermsDays)
	}
	if in.DueDate.Before(in.Date) {
		msg := "Jatuh tempo tidak boleh sebelum tanggal faktur."
		return nil, trade.Totals{}, httpx.Unprocessable("invoice.invalid", msg).WithFields(map[string]string{"dueDate": msg})
	}
	company, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return nil, trade.Totals{}, err
	}
	table, err := s.Tax.Table(ctx)
	if err != nil {
		return nil, trade.Totals{}, err
	}
	lines, totals, err := trade.Compute(table, in.Date, in.Lines, company.PKP)
	if err != nil {
		return nil, trade.Totals{}, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return nil, trade.Totals{}, err
	}
	af := map[string]string{}
	for i, l := range lines {
		a, ok := accounts[l.AccountID]
		key := fmt.Sprintf("lines.%d.accountId", i)
		switch {
		case !ok:
			af[key] = "Akun tidak ditemukan di PT ini."
		case !a.Postable || !a.Active:
			af[key] = fmt.Sprintf("%s %s bukan akun detail yang aktif.", a.Code, a.Name)
		case !revenueClasses[a.Class]:
			af[key] = fmt.Sprintf("%s %s bukan akun pendapatan; pilih akun berkepala 4 atau 7.", a.Code, a.Name)
		}
	}
	if len(af) > 0 {
		return nil, trade.Totals{}, httpx.Unprocessable("invoice.invalid", trade.FirstMessage(af)).WithFields(af)
	}
	return lines, totals, nil
}

func (s *service) accounts(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]ledger.Account, error) {
	list, err := s.Ledger.Accounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	m := make(map[uuid.UUID]ledger.Account, len(list))
	for _, a := range list {
		m[a.ID] = a
	}
	return m, nil
}

func (s *service) writeLines(ctx context.Context, companyID, invoiceID uuid.UUID, lines []trade.Line) error {
	q := s.q(ctx)
	if err := q.DeleteInvoiceLines(ctx, store.DeleteInvoiceLinesParams{CompanyID: companyID, InvoiceID: invoiceID}); err != nil {
		return err
	}
	for i, l := range lines {
		if err := q.InsertInvoiceLine(ctx, store.InsertInvoiceLineParams{
			ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, InvoiceID: invoiceID, LineNo: int16(i + 1),
			Description: l.Description, AccountID: l.AccountID, Qty: l.Qty, UnitPrice: l.UnitPrice,
			DiscountPct: l.DiscountPct, Discount: l.Discount, Net: l.Net, TaxCode: l.TaxCode,
			OtherDpp: l.OtherDPP, Vat: l.VAT,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) CreateInvoice(ctx context.Context, companyID uuid.UUID, in InvoiceInput, actor audit.Actor) (InvoiceDetail, error) {
	var out InvoiceDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		lines, t, err := s.prepare(ctx, companyID, &in)
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		if err := s.q(ctx).InsertInvoice(ctx, store.InsertInvoiceParams{
			ID: id, CompanyID: companyID, CustomerID: in.CustomerID, InvoiceDate: in.Date, DueDate: in.DueDate,
			Memo: in.Memo, Subtotal: t.Subtotal, Discount: t.Discount, Dpp: t.DPP, OtherDpp: t.OtherDPP,
			Vat: t.VAT, Total: t.Total, CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		if err := s.writeLines(ctx, companyID, id, lines); err != nil {
			return err
		}
		out, err = s.Invoice(ctx, companyID, id)
		return err
	})
	return out, err
}

// lockDraft mengunci draf dan memastikan pengguna boleh mengubahnya.
func (s *service) lockDraft(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor, anyDraft bool) (store.SalesInvoice, error) {
	inv, err := s.q(ctx).GetInvoiceForUpdate(ctx, store.GetInvoiceForUpdateParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return inv, httpx.NotFound("invoice.not_found", "Faktur tidak ditemukan.")
	}
	if err != nil {
		return inv, err
	}
	if inv.Status != "draft" {
		return inv, httpx.Conflict("invoice.not_draft", fmt.Sprintf("Faktur %s sudah diposting; koreksi lewat void.", deref(inv.Number)))
	}
	if !anyDraft && (inv.CreatedBy == nil || *inv.CreatedBy != actor.UserID) {
		return inv, httpx.Forbidden("invoice.not_owner", "Draf ini dibuat orang lain; hanya pembuatnya, Manager, atau Pemilik yang boleh mengubahnya.")
	}
	return inv, nil
}

func (s *service) UpdateInvoice(ctx context.Context, companyID, id uuid.UUID, in InvoiceInput, actor audit.Actor, anyDraft bool) (InvoiceDetail, error) {
	var out InvoiceDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if _, err := s.lockDraft(ctx, companyID, id, actor, anyDraft); err != nil {
			return err
		}
		lines, t, err := s.prepare(ctx, companyID, &in)
		if err != nil {
			return err
		}
		n, err := s.q(ctx).UpdateDraftInvoice(ctx, store.UpdateDraftInvoiceParams{
			CompanyID: companyID, ID: id, Version: int32(in.Version), CustomerID: in.CustomerID,
			InvoiceDate: in.Date, DueDate: in.DueDate, Memo: in.Memo, Subtotal: t.Subtotal, Discount: t.Discount,
			Dpp: t.DPP, OtherDpp: t.OtherDPP, Vat: t.VAT, Total: t.Total,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Draf ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if err := s.writeLines(ctx, companyID, id, lines); err != nil {
			return err
		}
		out, err = s.Invoice(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) DeleteInvoice(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor, anyDraft bool) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if _, err := s.lockDraft(ctx, companyID, id, actor, anyDraft); err != nil {
			return err
		}
		_, err := s.q(ctx).DeleteDraftInvoice(ctx, store.DeleteDraftInvoiceParams{CompanyID: companyID, ID: id})
		return err
	})
}

// PostInvoice menghitung ulang draf dengan aturan pajak pada tanggal faktur, memberi nomor FJ,
// lalu memposting jurnal: debit piutang usaha; kredit pendapatan per akun dan PPN keluaran.
func (s *service) PostInvoice(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (InvoiceDetail, error) {
	var out InvoiceDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		inv, err := s.lockDraft(ctx, companyID, id, actor, true)
		if err != nil {
			return err
		}
		stored, err := q.InvoiceLines(ctx, store.InvoiceLinesParams{CompanyID: companyID, InvoiceID: id})
		if err != nil {
			return err
		}
		in := InvoiceInput{CustomerID: inv.CustomerID, Date: inv.InvoiceDate, DueDate: inv.DueDate, Memo: inv.Memo, Version: int(inv.Version)}
		for _, l := range stored {
			in.Lines = append(in.Lines, trade.LineInput{
				Description: l.Description, AccountID: l.AccountID, Qty: l.Qty, UnitPrice: l.UnitPrice,
				DiscountPct: l.DiscountPct, TaxCode: l.TaxCode,
			})
		}
		lines, t, err := s.prepare(ctx, companyID, &in)
		if err != nil {
			return err
		}
		receivable, err := s.Ledger.RoleAccount(ctx, companyID, ledger.Receivable)
		if err != nil {
			return err
		}
		number, err := s.Ledger.NextNumber(ctx, companyID, seriesInvoice, inv.InvoiceDate)
		if err != nil {
			return err
		}
		memo := "Faktur " + number
		if inv.Memo != "" {
			memo += " · " + inv.Memo
		}
		drafts := []ledger.LineDraft{{AccountID: receivable.ID, Debit: t.Total, Memo: memo}}
		drafts = append(drafts, creditsByAccount(lines)...)
		if t.VAT.Sign() > 0 {
			vat, err := s.Ledger.RoleAccount(ctx, companyID, ledger.OutputVAT)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: vat.ID, Credit: t.VAT, Memo: "PPN keluaran " + number})
		}
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: inv.InvoiceDate, Memo: memo,
			SourceType: SourceInvoice, SourceID: &id, Number: number, Lines: drafts, Actor: actor,
		})
		if err != nil {
			return err
		}
		// Simpan hasil hitung ulang, lalu tandai diposting.
		if n, err := q.UpdateDraftInvoice(ctx, store.UpdateDraftInvoiceParams{
			CompanyID: companyID, ID: id, Version: inv.Version, CustomerID: in.CustomerID, InvoiceDate: in.Date,
			DueDate: in.DueDate, Memo: in.Memo, Subtotal: t.Subtotal, Discount: t.Discount, Dpp: t.DPP,
			OtherDpp: t.OtherDPP, Vat: t.VAT, Total: t.Total,
		}); err != nil || n != 1 {
			return errOr(err, "faktur gagal diperbarui sebelum posting")
		}
		if err := s.writeLines(ctx, companyID, id, lines); err != nil {
			return err
		}
		if n, err := q.MarkInvoicePosted(ctx, store.MarkInvoicePostedParams{
			CompanyID: companyID, ID: id, Number: &number, JournalEntryID: &posted.ID, PostedBy: actor.UserRef(),
		}); err != nil || n != 1 {
			return errOr(err, "faktur gagal ditandai diposting")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.invoice.post",
			ObjectType: "sales_invoice", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "total": t.Total.String(), "vat": t.VAT.String(), "date": inv.InvoiceDate.String()},
		}); err != nil {
			return err
		}
		out, err = s.Invoice(ctx, companyID, id)
		return err
	})
	return out, err
}

// creditsByAccount menjumlahkan DPP baris per akun, urut kemunculan pertama.
func creditsByAccount(lines []trade.Line) []ledger.LineDraft {
	var out []ledger.LineDraft
	index := map[uuid.UUID]int{}
	for _, l := range lines {
		if l.Net.IsZero() {
			continue
		}
		i, ok := index[l.AccountID]
		if !ok {
			index[l.AccountID] = len(out)
			out = append(out, ledger.LineDraft{AccountID: l.AccountID, Memo: l.Description})
			i = len(out) - 1
		}
		out[i].Credit = out[i].Credit.Add(l.Net)
	}
	return out
}

// VoidInvoice membatalkan faktur yang belum punya pelunasan; jurnalnya dibalik di tanggal void.
func (s *service) VoidInvoice(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (InvoiceDetail, error) {
	var out InvoiceDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		inv, err := q.GetInvoiceForUpdate(ctx, store.GetInvoiceForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("invoice.not_found", "Faktur tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		switch inv.Status {
		case "draft":
			return httpx.Conflict("invoice.is_draft", "Draf belum diposting; hapus saja drafnya.")
		case "void":
			return httpx.Conflict("invoice.already_void", fmt.Sprintf("Faktur %s sudah di-void.", deref(inv.Number)))
		}
		pays, err := q.InvoiceReceipts(ctx, store.InvoiceReceiptsParams{CompanyID: companyID, InvoiceID: id})
		if err != nil {
			return err
		}
		var active []string
		for _, p := range pays {
			if p.Status == "posted" {
				active = append(active, p.Number)
			}
		}
		if len(active) > 0 {
			return httpx.Unprocessable("invoice.has_receipts", fmt.Sprintf(
				"Faktur %s sudah punya pelunasan %s; void pelunasannya dulu.", deref(inv.Number), strings.Join(active, ", ")))
		}
		if s.VoidGuard != nil {
			if err := s.VoidGuard(ctx, companyID, id); err != nil {
				return err
			}
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		v, err := s.Ledger.VoidSource(ctx, companyID, *inv.JournalEntryID, SourceInvoice, date, reason, actor)
		if err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if n, err := q.MarkInvoiceVoid(ctx, store.MarkInvoiceVoidParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || n != 1 {
			return errOr(err, "faktur gagal ditandai void")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.invoice.void",
			ObjectType: "sales_invoice", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": deref(inv.Number), "status": "posted"},
			After:  map[string]any{"number": deref(inv.Number), "status": "void", "reason": reason, "reversal": v.Reversal.Number},
		}); err != nil {
			return err
		}
		out, err = s.Invoice(ctx, companyID, id)
		return err
	})
	return out, err
}

func state(status string, total, paid money.Amount) string {
	switch {
	case status != "posted":
		return status
	case paid.Cmp(total) >= 0:
		return "paid"
	default:
		return "open"
	}
}

func summary(id uuid.UUID, number *string, status string, customer uuid.UUID, date, due civil.Date, memo string, dpp, otherDPP, vat, total, paid money.Amount, names map[uuid.UUID]string) Invoice {
	inv := Invoice{
		ID: id, Number: number, Status: status, State: state(status, total, paid), CustomerID: customer,
		CustomerName: names[customer], Date: date, DueDate: due, Memo: memo, DPP: dpp, OtherDPP: otherDPP, VAT: vat, Total: total, Paid: paid,
	}
	if status == "posted" {
		inv.Outstanding = total.Sub(paid)
		if inv.Outstanding.Sign() > 0 {
			if late := civil.Today(civil.Jakarta).DaysSince(due); late > 0 {
				inv.DaysOverdue = late
			}
		}
	}
	return inv
}

func (s *service) Invoices(ctx context.Context, companyID uuid.UUID, f InvoiceFilter) ([]Invoice, error) {
	p := store.ListInvoicesParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, CustomerID: f.CustomerID}
	if f.State != "" {
		p.State = &f.State
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		p.Search = &q
	}
	rows, err := s.q(ctx).ListInvoices(ctx, p)
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
	out := make([]Invoice, len(rows))
	for i, r := range rows {
		out[i] = summary(r.ID, r.Number, r.Status, r.CustomerID, r.InvoiceDate, r.DueDate, r.Memo, r.Dpp, r.OtherDpp, r.Vat, r.Total, r.Paid, names)
	}
	return out, nil
}

func (s *service) OpenInvoices(ctx context.Context, companyID uuid.UUID, customerID *uuid.UUID) ([]Invoice, error) {
	rows, err := s.q(ctx).OpenInvoices(ctx, store.OpenInvoicesParams{CompanyID: companyID, CustomerID: customerID})
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
	out := make([]Invoice, len(rows))
	for i, r := range rows {
		out[i] = summary(r.ID, r.Number, r.Status, r.CustomerID, r.InvoiceDate, r.DueDate, r.Memo, r.Dpp, r.OtherDpp, r.Vat, r.Total, r.Paid, names)
	}
	return out, nil
}

func (s *service) Invoice(ctx context.Context, companyID, id uuid.UUID) (InvoiceDetail, error) {
	q := s.q(ctx)
	r, err := q.GetInvoice(ctx, store.GetInvoiceParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return InvoiceDetail{}, httpx.NotFound("invoice.not_found", "Faktur tidak ditemukan.")
	}
	if err != nil {
		return InvoiceDetail{}, err
	}
	names, err := s.Core.ContactNames(ctx, companyID, []uuid.UUID{r.CustomerID})
	if err != nil {
		return InvoiceDetail{}, err
	}
	pays, err := q.InvoiceReceipts(ctx, store.InvoiceReceiptsParams{CompanyID: companyID, InvoiceID: id})
	if err != nil {
		return InvoiceDetail{}, err
	}
	var paid money.Amount
	payments := make([]InvoicePayment, 0, len(pays))
	for _, p := range pays {
		if p.Status == "posted" {
			paid = paid.Add(p.Amount)
		}
		payments = append(payments, InvoicePayment{
			ReceiptID: p.ID, Number: p.Number, Date: p.ReceiptDate, Status: p.Status, Amount: p.Amount,
			Withholding: p.Withholding, WithholdingCode: p.WithholdingCode,
		})
	}
	out := InvoiceDetail{
		Invoice: summary(r.ID, r.Number, r.Status, r.CustomerID, r.InvoiceDate, r.DueDate, r.Memo, r.Dpp, r.OtherDpp, r.Vat, r.Total, paid, names),
		Totals: trade.Totals{
			Subtotal: r.Subtotal, Discount: r.Discount, DPP: r.Dpp, OtherDPP: r.OtherDpp, VAT: r.Vat, Total: r.Total,
		},
		Payments: payments, JournalEntryID: r.JournalEntryID, CreatedByID: r.CreatedBy, CreatedBy: r.CreatedByName,
		PostedBy: r.PostedByName, PostedAt: r.PostedAt, VoidedBy: r.VoidedByName, VoidedAt: r.VoidedAt,
		VoidReason: r.VoidReason, Version: int(r.Version),
	}
	lines, err := q.InvoiceLines(ctx, store.InvoiceLinesParams{CompanyID: companyID, InvoiceID: id})
	if err != nil {
		return InvoiceDetail{}, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	for _, l := range lines {
		a := accounts[l.AccountID]
		out.Lines = append(out.Lines, InvoiceLine{
			Line: trade.Line{
				LineInput: trade.LineInput{
					Description: l.Description, AccountID: l.AccountID, Qty: l.Qty, UnitPrice: l.UnitPrice,
					DiscountPct: l.DiscountPct, TaxCode: l.TaxCode,
				},
				Gross: l.Net.Add(l.Discount), Discount: l.Discount, Net: l.Net, OtherDPP: l.OtherDpp, VAT: l.Vat,
			},
			AccountCode: a.Code, AccountName: a.Name,
		})
	}
	return out, nil
}

func (s *service) Aging(ctx context.Context, companyID uuid.UUID, asOf civil.Date) (trade.Aging, error) {
	rows, err := s.q(ctx).AgingInvoices(ctx, store.AgingInvoicesParams{CompanyID: companyID, AsOf: asOf})
	if err != nil {
		return trade.Aging{}, err
	}
	items := make([]trade.AgingItem, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out := r.Total.Sub(r.Paid)
		if out.Sign() <= 0 {
			continue
		}
		items = append(items, trade.AgingItem{ContactID: r.CustomerID, Due: r.DueDate, Outstanding: out})
		ids = append(ids, r.CustomerID)
	}
	names, err := s.Core.ContactNames(ctx, companyID, ids)
	if err != nil {
		return trade.Aging{}, err
	}
	return trade.BuildAging(asOf, items, names), nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func errOr(err error, msg string) error {
	if err != nil {
		return err
	}
	return errors.New(msg)
}

// fieldErr menambahkan field ke problem tanpa field, supaya formulir bisa menandai isiannya.
func fieldErr(err error, field string) error {
	var p *httpx.Problem
	if errors.As(err, &p) && len(p.Fields) == 0 {
		p.Fields = map[string]string{field: p.Detail}
	}
	return err
}

// Withheld adalah satu potongan PPh oleh pelanggan saat pelunasan.
type Withheld struct {
	ReceiptID     uuid.UUID    `json:"receiptId"`
	ReceiptNumber string       `json:"receiptNumber"`
	Date          civil.Date   `json:"date"`
	ContactID     uuid.UUID    `json:"contactId"`
	ContactName   string       `json:"contactName"`
	InvoiceNumber string       `json:"invoiceNumber"`
	DPP           money.Amount `json:"dpp"`
	Code          string       `json:"code"`
	Amount        money.Amount `json:"amount"`
}

func (s *service) VATByMonth(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]trade.MonthVAT, error) {
	rows, err := s.q(ctx).VATByMonth(ctx, store.VATByMonthParams{CompanyID: companyID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	out := make([]trade.MonthVAT, len(rows))
	for i, r := range rows {
		out[i] = trade.MonthVAT{Month: r.Month, Documents: r.Invoices, DPP: r.Dpp, VAT: r.Vat}
	}
	return out, nil
}

func (s *service) Withheld(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]Withheld, error) {
	rows, err := s.q(ctx).WithheldByCustomers(ctx, store.WithheldByCustomersParams{CompanyID: companyID, FromDate: from, ToDate: to})
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
	out := make([]Withheld, len(rows))
	for i, r := range rows {
		out[i] = Withheld{
			ReceiptID: r.ReceiptID, ReceiptNumber: r.ReceiptNumber, Date: r.ReceiptDate, ContactID: r.CustomerID,
			ContactName: names[r.CustomerID], InvoiceNumber: deref(r.InvoiceNumber), DPP: r.Dpp,
			Code: deref(r.WithholdingCode), Amount: r.Withholding,
		}
	}
	return out, nil
}

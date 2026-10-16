// Package consol adalah modul multi-entitas: transaksi antar-PT dengan dokumen cerminan otomatis
// (ME-03) dan konsolidasi grup dengan pemetaan bagan akun dan eliminasi antar-PT (ME-04).
//
// Faktur PT A ke kontak yang mewakili PT B langsung membuat dan memposting tagihan di PT B di
// transaksi yang sama, dan sebaliknya; void di satu sisi mem-void sisi lainnya. Dengan begitu
// saldo antar-PT tidak pernah timpang, dan eliminasi di konsolidasi selalu berpasangan.
package consol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/consol/internal/store"
	"lajur/internal/modules/core"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/report"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const (
	docInvoice    = sales.SourceInvoice      // sales.invoice
	docBill       = purchase.SourceBill      // purchase.bill
	docCreditNote = sales.SourceCreditNote   // sales.credit_note
	docDebitNote  = purchase.SourceDebitNote // purchase.debit_note
)

type Service interface {
	// Hook dokumen penjualan dan pembelian (dipasang saat perangkaian).
	InvoicePosted(ctx context.Context, companyID, invoiceID uuid.UUID, actor audit.Actor) error
	InvoiceVoided(ctx context.Context, companyID, invoiceID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error
	BillPosted(ctx context.Context, companyID, billID uuid.UUID, actor audit.Actor) error
	BillVoided(ctx context.Context, companyID, billID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error
	CreditNotePosted(ctx context.Context, companyID, noteID uuid.UUID, actor audit.Actor) error
	DebitNotePosted(ctx context.Context, companyID, noteID uuid.UUID, actor audit.Actor) error
	NoteVoided(ctx context.Context, companyID uuid.UUID, docType string, noteID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error
	ReceiptPosted(ctx context.Context, companyID, receiptID uuid.UUID, actor audit.Actor) error
	PaymentPosted(ctx context.Context, companyID, paymentID uuid.UUID, actor audit.Actor) error
	SettlementVoided(ctx context.Context, companyID uuid.UUID, docType string, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) error

	// Link mengembalikan pasangan dokumen antar-PT, atau nil bila dokumen tidak berpasangan.
	Link(ctx context.Context, companyID uuid.UUID, docType string, docID uuid.UUID) (*Link, error)

	GroupChart(ctx context.Context, userID uuid.UUID) (GroupChart, error)
	SaveGroupAccount(ctx context.Context, in GroupAccountInput, actor audit.Actor) error
	SetMapping(ctx context.Context, companyID, accountID uuid.UUID, groupCode string, actor audit.Actor) error

	Consolidate(ctx context.Context, userID uuid.UUID, companies []uuid.UUID, asOf civil.Date, p report.Period) (Consolidation, error)
}

type Deps struct {
	Pool     *pgxpool.Pool
	Core     core.Service
	Ledger   ledger.Service
	Sales    sales.Service
	Purchase purchase.Service
	Report   report.Service
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

type mirrorKey struct{}

// Dokumen cerminan dibuat lewat service penjualan dan pembelian biasa; penanda ini mencegah
// dokumen cerminan memicu cerminan berikutnya.
func mirroring(ctx context.Context) bool { return ctx.Value(mirrorKey{}) != nil }
func withMirroring(ctx context.Context) context.Context {
	return context.WithValue(ctx, mirrorKey{}, true)
}

// Link adalah dokumen pasangan di PT lain.
type Link struct {
	CompanyID   uuid.UUID `json:"companyId"`
	CompanyName string    `json:"companyName"`
	DocType     string    `json:"docType"`
	DocID       uuid.UUID `json:"docId"`
	Number      string    `json:"number"`
	Mirror      bool      `json:"mirror"` // true bila dokumen pasangan dibuat otomatis dari dokumen ini
}

func (s *service) companyName(ctx context.Context, id uuid.UUID) string {
	c, err := s.Core.Company(ctx, id)
	if err != nil {
		return "PT grup"
	}
	return c.Name
}

// counterpart mencari kontak di PT lawan yang mewakili PT asal, dengan peran dan akun cerminannya.
func (s *service) counterpart(ctx context.Context, target, source uuid.UUID, supplier bool) (core.Contact, uuid.UUID, error) {
	targetName, sourceName := s.companyName(ctx, target), s.companyName(ctx, source)
	role, field := "pelanggan", "akun pendapatan faktur cerminan"
	if supplier {
		role, field = "pemasok", "akun beban tagihan cerminan"
	}
	c, err := s.Core.LinkedContact(ctx, target, source)
	if err != nil {
		return core.Contact{}, uuid.Nil, httpx.Unprocessable("intercompany.no_contact", fmt.Sprintf(
			"Di %s belum ada kontak %s yang ditautkan ke %s. Tambahkan dulu di menu Kontak %s supaya dokumen cerminannya bisa dibuat.",
			targetName, role, sourceName, targetName))
	}
	account := c.ICRevenueAccountID
	if supplier {
		account = c.ICExpenseAccountID
	}
	switch {
	case supplier && !c.IsSupplier, !supplier && !c.IsCustomer:
		return core.Contact{}, uuid.Nil, httpx.Unprocessable("intercompany.contact_role", fmt.Sprintf(
			"Kontak %s di %s belum ditandai sebagai %s.", c.Name, targetName, role))
	case !c.Active:
		return core.Contact{}, uuid.Nil, httpx.Unprocessable("intercompany.contact_inactive", fmt.Sprintf(
			"Kontak %s di %s nonaktif.", c.Name, targetName))
	case account == nil:
		return core.Contact{}, uuid.Nil, httpx.Unprocessable("intercompany.no_account", fmt.Sprintf(
			"Isi %s di kontak %s pada %s.", field, c.Name, targetName))
	}
	return c, *account, nil
}

// mirrorLines menyalin baris dokumen sebagai satu baris per baris asal senilai DPP-nya, dengan
// kode pajak yang sama, sehingga DPP, PPN, dan total kedua dokumen sama persis.
func mirrorLines(lines []trade.Line, account uuid.UUID) []trade.LineInput {
	out := make([]trade.LineInput, len(lines))
	for i, l := range lines {
		out[i] = trade.LineInput{
			Description: l.Description, AccountID: account, Qty: money.FromInt(1), UnitPrice: l.Net, TaxCode: l.TaxCode,
		}
	}
	return out
}

func mirrorMemo(number, company string) string {
	m := "Cerminan " + number + " dari " + company
	if utf8.RuneCountInString(m) > 500 {
		m = string([]rune(m)[:500])
	}
	return m
}

// InvoicePosted: faktur PT A ke kontak PT B → tagihan diposting di PT B.
func (s *service) InvoicePosted(ctx context.Context, companyID, invoiceID uuid.UUID, actor audit.Actor) error {
	if mirroring(ctx) {
		return nil
	}
	inv, err := s.Sales.Invoice(ctx, companyID, invoiceID)
	if err != nil || inv.Kind == sales.KindOpening {
		return err // saldo awal diimpor masing-masing PT, tidak dicerminkan
	}
	c, err := s.Core.Contact(ctx, companyID, inv.CustomerID)
	if err != nil || c.LinkedCompanyID == nil {
		return err
	}
	if inv.Kind != sales.KindRegular {
		return httpx.Unprocessable("intercompany.kind",
			"Faktur uang muka, termin, dan pelepasan retensi ke PT dalam grup belum didukung; gunakan faktur biasa.")
	}
	target := *c.LinkedCompanyID
	supplier, account, err := s.counterpart(ctx, target, companyID, true)
	if err != nil {
		return err
	}
	mctx := withMirroring(ctx)
	draft, err := s.Purchase.CreateBill(mctx, target, purchase.BillInput{
		SupplierID: supplier.ID, SupplierInvoiceNo: deref(inv.Number), Date: inv.Date, DueDate: inv.DueDate,
		Memo: mirrorMemo(deref(inv.Number), s.companyName(ctx, companyID)), Lines: mirrorLines(lineList(inv.Lines), account),
	}, actor)
	if err != nil {
		return wrap(err, s.companyName(ctx, target))
	}
	bill, err := s.Purchase.PostBill(mctx, target, draft.ID, actor)
	if err != nil {
		return wrap(err, s.companyName(ctx, target))
	}
	return s.link(ctx, companyID, docInvoice, invoiceID, target, docBill, bill.ID, actor)
}

// BillPosted: tagihan PT B dari kontak PT A → faktur diposting di PT A.
func (s *service) BillPosted(ctx context.Context, companyID, billID uuid.UUID, actor audit.Actor) error {
	if mirroring(ctx) {
		return nil
	}
	bill, err := s.Purchase.Bill(ctx, companyID, billID)
	if err != nil || bill.Opening {
		return err // saldo awal diimpor masing-masing PT, tidak dicerminkan
	}
	c, err := s.Core.Contact(ctx, companyID, bill.SupplierID)
	if err != nil || c.LinkedCompanyID == nil {
		return err
	}
	target := *c.LinkedCompanyID
	customer, account, err := s.counterpart(ctx, target, companyID, false)
	if err != nil {
		return err
	}
	lines := make([]trade.Line, len(bill.Lines))
	for i, l := range bill.Lines {
		lines[i] = l.Line
	}
	mctx := withMirroring(ctx)
	draft, err := s.Sales.CreateInvoice(mctx, target, sales.InvoiceInput{
		CustomerID: customer.ID, Date: bill.Date, DueDate: bill.DueDate,
		Memo: mirrorMemo(deref(bill.Number), s.companyName(ctx, companyID)), Lines: mirrorLines(lines, account),
	}, actor)
	if err != nil {
		return wrap(err, s.companyName(ctx, target))
	}
	inv, err := s.Sales.PostInvoice(mctx, target, draft.ID, actor)
	if err != nil {
		return wrap(err, s.companyName(ctx, target))
	}
	return s.link(ctx, companyID, docBill, billID, target, docInvoice, inv.ID, actor)
}

func lineList(lines []sales.InvoiceLine) []trade.Line {
	out := make([]trade.Line, len(lines))
	for i, l := range lines {
		out[i] = l.Line
	}
	return out
}

func (s *service) link(ctx context.Context, sourceCompany uuid.UUID, sourceType string, sourceID, targetCompany uuid.UUID, targetType string, targetID uuid.UUID, actor audit.Actor) error {
	id := uuid.Must(uuid.NewV7())
	if err := s.q(ctx).InsertLink(ctx, store.InsertLinkParams{
		ID: id, SourceCompanyID: sourceCompany, SourceType: sourceType, SourceID: sourceID,
		TargetCompanyID: targetCompany, TargetType: targetType, TargetID: targetID, CreatedBy: actor.UserRef(),
	}); err != nil {
		return err
	}
	_, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
		UserID: actor.UserRef(), CompanyID: &sourceCompany, Action: "consol.intercompany.mirror",
		ObjectType: "ic_link", ObjectID: id.String(), IP: actor.IP,
		After: map[string]any{"source": sourceType, "sourceId": sourceID.String(), "targetCompany": targetCompany.String(), "target": targetType, "targetId": targetID.String()},
	})
	return err
}

// Nota kredit dan nota debit antar-PT dicerminkan seperti dokumen asalnya, tetapi hanya dari
// dokumen sumber: baris dokumen cerminan selalu satu baris per baris sumber (kuantitas 1,
// harga = DPP baris, tanpa produk), sehingga nota cerminannya punya DPP dan PPN yang sama.
// Retur atas dokumen cerminan harus dicatat dari dokumen aslinya supaya stok di PT itu ikut.

// CreditNotePosted: nota kredit atas faktur sumber → nota debit atas tagihan cerminannya.
func (s *service) CreditNotePosted(ctx context.Context, companyID, noteID uuid.UUID, actor audit.Actor) error {
	if mirroring(ctx) {
		return nil
	}
	n, err := s.Sales.CreditNote(ctx, companyID, noteID)
	if err != nil {
		return err
	}
	l, err := s.Link(ctx, companyID, docInvoice, n.InvoiceID)
	if err != nil || l == nil {
		return err
	}
	if !l.Mirror {
		return httpx.Unprocessable("intercompany.note_source", fmt.Sprintf(
			"Faktur %s adalah cerminan tagihan %s di %s; buat nota debit dari tagihan aslinya supaya kedua PT tercatat sama.",
			n.InvoiceNumber, l.Number, l.CompanyName))
	}
	inv, err := s.Sales.Invoice(ctx, companyID, n.InvoiceID)
	if err != nil {
		return err
	}
	bill, err := s.Purchase.Bill(ctx, l.CompanyID, l.DocID)
	if err != nil {
		return err
	}
	index := map[uuid.UUID]int{}
	for i, line := range inv.Lines {
		index[line.ID] = i
	}
	lines := make([]purchase.DebitLineInput, len(n.Lines))
	for i, nl := range n.Lines {
		j, ok := index[nl.InvoiceLineID]
		if !ok || j >= len(bill.Lines) {
			return fmt.Errorf("baris tagihan cerminan untuk %s tidak ditemukan", nl.Description)
		}
		net := nl.Net
		lines[i] = purchase.DebitLineInput{BillLineID: bill.Lines[j].ID, Qty: money.FromInt(1), UnitPrice: &net}
	}
	d, err := s.Purchase.CreateDebitNote(withMirroring(ctx), l.CompanyID, purchase.DebitNoteInput{
		BillID: bill.ID, Date: n.Date, Memo: mirrorMemo(n.Number, s.companyName(ctx, companyID)), Lines: lines,
	}, actor)
	if err != nil {
		return wrap(err, l.CompanyName)
	}
	return s.link(ctx, companyID, docCreditNote, noteID, l.CompanyID, docDebitNote, d.ID, actor)
}

// DebitNotePosted: nota debit atas tagihan sumber → nota kredit atas faktur cerminannya.
func (s *service) DebitNotePosted(ctx context.Context, companyID, noteID uuid.UUID, actor audit.Actor) error {
	if mirroring(ctx) {
		return nil
	}
	n, err := s.Purchase.DebitNote(ctx, companyID, noteID)
	if err != nil {
		return err
	}
	l, err := s.Link(ctx, companyID, docBill, n.BillID)
	if err != nil || l == nil {
		return err
	}
	if !l.Mirror {
		return httpx.Unprocessable("intercompany.note_source", fmt.Sprintf(
			"Tagihan %s adalah cerminan faktur %s di %s; buat nota kredit dari faktur aslinya supaya kedua PT tercatat sama.",
			n.BillNumber, l.Number, l.CompanyName))
	}
	bill, err := s.Purchase.Bill(ctx, companyID, n.BillID)
	if err != nil {
		return err
	}
	inv, err := s.Sales.Invoice(ctx, l.CompanyID, l.DocID)
	if err != nil {
		return err
	}
	index := map[uuid.UUID]int{}
	for i, line := range bill.Lines {
		index[line.ID] = i
	}
	lines := make([]sales.CreditLineInput, len(n.Lines))
	for i, nl := range n.Lines {
		j, ok := index[nl.BillLineID]
		if !ok || j >= len(inv.Lines) {
			return fmt.Errorf("baris faktur cerminan untuk %s tidak ditemukan", nl.Description)
		}
		net := nl.Net
		lines[i] = sales.CreditLineInput{InvoiceLineID: inv.Lines[j].ID, Qty: money.FromInt(1), UnitPrice: &net}
	}
	c, err := s.Sales.CreateCreditNote(withMirroring(ctx), l.CompanyID, sales.CreditNoteInput{
		InvoiceID: inv.ID, Date: n.Date, Memo: mirrorMemo(n.Number, s.companyName(ctx, companyID)), Lines: lines,
	}, actor)
	if err != nil {
		return wrap(err, l.CompanyName)
	}
	return s.link(ctx, companyID, docDebitNote, noteID, l.CompanyID, docCreditNote, c.ID, actor)
}

func (s *service) NoteVoided(ctx context.Context, companyID uuid.UUID, docType string, noteID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
	return s.voidPair(ctx, companyID, docType, noteID, date, reason, actor)
}

func (s *service) InvoiceVoided(ctx context.Context, companyID, invoiceID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
	return s.voidPair(ctx, companyID, docInvoice, invoiceID, date, reason, actor)
}

func (s *service) BillVoided(ctx context.Context, companyID, billID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
	return s.voidPair(ctx, companyID, docBill, billID, date, reason, actor)
}

// voidPair mem-void dokumen pasangan di PT lain pada tanggal void yang sama. Bila pasangan
// tidak bisa di-void (mis. sudah dibayar), seluruh void dibatalkan.
func (s *service) voidPair(ctx context.Context, companyID uuid.UUID, docType string, docID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
	if mirroring(ctx) {
		return nil
	}
	l, err := s.Link(ctx, companyID, docType, docID)
	if err != nil || l == nil {
		return err
	}
	mctx := withMirroring(ctx)
	why := "Ikut void dokumen pasangan dari " + s.companyName(ctx, companyID) + ": " + reason
	if utf8.RuneCountInString(why) > 300 {
		why = string([]rune(why)[:300])
	}
	switch l.DocType {
	case docBill:
		_, err = s.Purchase.VoidBill(mctx, l.CompanyID, l.DocID, date, why, actor)
	case docInvoice:
		_, err = s.Sales.VoidInvoice(mctx, l.CompanyID, l.DocID, date, why, actor)
	case docDebitNote:
		_, err = s.Purchase.VoidDebitNote(mctx, l.CompanyID, l.DocID, date, why, actor)
	case docCreditNote:
		_, err = s.Sales.VoidCreditNote(mctx, l.CompanyID, l.DocID, date, why, actor)
	case docReceipt:
		_, err = s.Sales.VoidReceipt(mctx, l.CompanyID, l.DocID, date, why, actor)
	case docPayment:
		_, err = s.Purchase.VoidPayment(mctx, l.CompanyID, l.DocID, date, why, actor)
	}
	var p *httpx.Problem
	if errors.As(err, &p) && strings.HasSuffix(p.Code, ".already_void") {
		return nil
	}
	return wrap(err, l.CompanyName)
}

func (s *service) Link(ctx context.Context, companyID uuid.UUID, docType string, docID uuid.UUID) (*Link, error) {
	r, err := s.q(ctx).LinkOf(ctx, store.LinkOfParams{DocType: docType, DocID: docID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := &Link{}
	switch {
	case r.SourceType == docType && r.SourceID == docID && r.SourceCompanyID == companyID:
		out.CompanyID, out.DocType, out.DocID, out.Mirror = r.TargetCompanyID, r.TargetType, r.TargetID, true
	case r.TargetType == docType && r.TargetID == docID && r.TargetCompanyID == companyID:
		out.CompanyID, out.DocType, out.DocID = r.SourceCompanyID, r.SourceType, r.SourceID
	default:
		return nil, nil
	}
	out.CompanyName = s.companyName(ctx, out.CompanyID)
	switch out.DocType {
	case docBill:
		if b, err := s.Purchase.Bill(ctx, out.CompanyID, out.DocID); err == nil {
			out.Number = deref(b.Number)
		}
	case docInvoice:
		if inv, err := s.Sales.Invoice(ctx, out.CompanyID, out.DocID); err == nil {
			out.Number = deref(inv.Number)
		}
	case docCreditNote:
		if n, err := s.Sales.CreditNote(ctx, out.CompanyID, out.DocID); err == nil {
			out.Number = n.Number
		}
	case docDebitNote:
		if n, err := s.Purchase.DebitNote(ctx, out.CompanyID, out.DocID); err == nil {
			out.Number = n.Number
		}
	case docReceipt:
		if r, err := s.Sales.Receipt(ctx, out.CompanyID, out.DocID); err == nil {
			out.Number = r.Number
		}
	case docPayment:
		if p, err := s.Purchase.Payment(ctx, out.CompanyID, out.DocID); err == nil {
			out.Number = p.Number
		}
	}
	return out, nil
}

// wrap menambahkan nama PT lawan ke pesan galat dari dokumen pasangan.
func wrap(err error, company string) error {
	var p *httpx.Problem
	if errors.As(err, &p) {
		p.Detail = "Dokumen pasangan di " + company + ": " + p.Detail
		p.Fields = nil
	}
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

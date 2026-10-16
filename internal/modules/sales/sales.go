// Package sales adalah modul penjualan dan piutang (AR): faktur penjualan (AR-02), faktur
// uang muka, termin, dan pelepasan retensi proyek (AR-08), pelunasan dengan potongan PPh 23
// atau PPh final 4(2) (AR-04), dan umur piutang (AR-06). Setiap faktur memposting jurnal:
// debit piutang usaha, kredit pendapatan dan PPN keluaran. Barang persediaan di faktur
// mengeluarkan stok dengan HPP rata-rata lewat modul persediaan.
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
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/sales/internal/store"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
)

const (
	SourceInvoice = "sales.invoice"
	SourceReceipt = "sales.receipt"

	seriesInvoice = "FJ"  // faktur jual
	seriesReceipt = "BKM" // bukti kas masuk, satu seri dengan penerimaan kas
	seriesOpening = "SAP" // saldo awal piutang
)

// Jenis faktur. Selain faktur biasa, semuanya dibuat dari halaman proyek (modul proyek).
const (
	KindRegular   = "biasa"
	KindAdvance   = "uang_muka"  // kredit uang muka proyek, bukan pendapatan
	KindProgress  = "termin"     // pendapatan = nilai progres; potongan uang muka dan retensi
	KindRetention = "retensi"    // menagih piutang retensi yang ditahan di faktur termin
	KindOpening   = "saldo_awal" // piutang dari sistem lama, melawan ekuitas saldo awal (GL-06)
)

type InvoiceInput struct {
	CustomerID uuid.UUID         `json:"customerId"`
	Date       civil.Date        `json:"date"`
	DueDate    civil.Date        `json:"dueDate"` // kosong = tanggal + termin pelanggan
	Memo       string            `json:"memo"`
	ProjectID  *uuid.UUID        `json:"projectId"`
	Lines      []trade.LineInput `json:"lines"`
	Version    int               `json:"version"` // hanya untuk perubahan draf
	// Dimensi cabang dan departemen (GL-09), diwariskan ke setiap baris jurnal.
	BranchID     *uuid.UUID `json:"branchId"`
	DepartmentID *uuid.UUID `json:"departmentId"`

	// Diisi modul proyek; endpoint HTTP penjualan hanya membuat faktur biasa.
	Kind        string        `json:"-"`
	ProgressPct *money.Amount `json:"-"` // termin: progres kumulatif
	Retention   money.Amount  `json:"-"` // termin: bagian yang ditahan sebagai piutang retensi
}

// Invoice adalah ringkasan faktur. State: draft, open (belum lunas), paid, atau void.
// Receivable = total − retensi, yaitu yang ditagih sekarang.
type Invoice struct {
	ID           uuid.UUID     `json:"id"`
	Number       *string       `json:"number"`
	Kind         string        `json:"kind"`
	Status       string        `json:"status"`
	State        string        `json:"state"`
	CustomerID   uuid.UUID     `json:"customerId"`
	CustomerName string        `json:"customerName"`
	ProjectID    *uuid.UUID    `json:"projectId,omitempty"`
	BranchID     *uuid.UUID    `json:"branchId,omitempty"`
	DepartmentID *uuid.UUID    `json:"departmentId,omitempty"`
	ProgressPct  *money.Amount `json:"progressPct,omitempty"`
	Date         civil.Date    `json:"date"`
	DueDate      civil.Date    `json:"dueDate"`
	Memo         string        `json:"memo"`
	Subtotal     money.Amount  `json:"subtotal"`
	Discount     money.Amount  `json:"discount"`
	DPP          money.Amount  `json:"dpp"` // dasar PPh yang dipotong pelanggan
	OtherDPP     money.Amount  `json:"otherDpp"`
	VAT          money.Amount  `json:"vat"`
	Total        money.Amount  `json:"total"`
	Retention    money.Amount  `json:"retention"`
	Receivable   money.Amount  `json:"receivable"`
	Paid         money.Amount  `json:"paid"`
	Credited     money.Amount  `json:"credited"` // nota kredit yang mengurangi sisa faktur
	Outstanding  money.Amount  `json:"outstanding"`
	DaysOverdue  int           `json:"daysOverdue"`
}

type InvoiceLine struct {
	trade.Line
	ID          uuid.UUID    `json:"id"`
	AccountCode string       `json:"accountCode"`
	AccountName string       `json:"accountName"`
	CreditedNet money.Amount `json:"creditedNet"` // DPP yang sudah dikreditkan nota kredit
	ReturnedQty money.Amount `json:"returnedQty"` // barang yang sudah kembali ke stok
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
	Totals      trade.Totals     `json:"totals"`
	Lines       []InvoiceLine    `json:"lines"`
	Payments    []InvoicePayment `json:"payments"`
	CreditNotes []InvoiceCredit  `json:"creditNotes"`
	// CreditWarning: draf ini membuat piutang pelanggan melewati limit kreditnya (AR-01).
	CreditWarning  *string    `json:"creditWarning,omitempty"`
	JournalEntryID *uuid.UUID `json:"journalEntryId,omitempty"`
	CreatedByID    *uuid.UUID `json:"createdById,omitempty"`
	CreatedBy      *string    `json:"createdBy,omitempty"`
	PostedBy       *string    `json:"postedBy,omitempty"`
	PostedAt       *time.Time `json:"postedAt,omitempty"`
	VoidedBy       *string    `json:"voidedBy,omitempty"`
	VoidedAt       *time.Time `json:"voidedAt,omitempty"`
	VoidReason     *string    `json:"voidReason,omitempty"`
	Version        int        `json:"version"`
}

type InvoiceFilter struct {
	From, To   civil.Date
	State      string
	CustomerID *uuid.UUID
	ProjectID  *uuid.UUID
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
	// CreateOpeningInvoice membuat dan memposting faktur saldo awal piutang (GL-06).
	CreateOpeningInvoice(ctx context.Context, companyID uuid.UUID, in OpeningInput, actor audit.Actor) (InvoiceDetail, error)
	OpenInvoices(ctx context.Context, companyID uuid.UUID, customerID *uuid.UUID) ([]Invoice, error)

	CreditNotes(ctx context.Context, companyID uuid.UUID, f CreditNoteFilter) ([]CreditNote, error)
	CreditNote(ctx context.Context, companyID, id uuid.UUID) (CreditNoteDetail, error)
	CreateCreditNote(ctx context.Context, companyID uuid.UUID, in CreditNoteInput, actor audit.Actor) (CreditNoteDetail, error)
	VoidCreditNote(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (CreditNoteDetail, error)

	// Rekap penjualan harian retail (IND-07) dengan PPN digunggung (TAX-12).
	DailySales(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]DailySale, error)
	DailySale(ctx context.Context, companyID, id uuid.UUID) (DailySaleDetail, error)
	CreateDailySale(ctx context.Context, companyID uuid.UUID, in DailySaleInput, actor audit.Actor) (DailySaleDetail, error)
	VoidDailySale(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (DailySaleDetail, error)

	Receipts(ctx context.Context, companyID uuid.UUID, f ReceiptFilter) ([]Receipt, error)
	Receipt(ctx context.Context, companyID, id uuid.UUID) (ReceiptDetail, error)
	CreateReceipt(ctx context.Context, companyID uuid.UUID, in ReceiptInput, actor audit.Actor) (ReceiptDetail, error)
	VoidReceipt(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (ReceiptDetail, error)
	// Uang muka pelanggan di luar proyek (AR-04): saldo per pelanggan dan pengembaliannya.
	AdvanceBalance(ctx context.Context, companyID, customerID uuid.UUID) (money.Amount, error)
	AdvanceBalances(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]money.Amount, error)
	RefundAdvance(ctx context.Context, companyID uuid.UUID, in AdvanceRefundInput, actor audit.Actor) (ReceiptDetail, error)

	Aging(ctx context.Context, companyID uuid.UUID, asOf civil.Date) (trade.Aging, error)

	// VATByMonth dan Withheld adalah data pendukung modul pajak (TAX-06, TAX-09).
	VATByMonth(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]trade.MonthVAT, error)
	Withheld(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]Withheld, error)

	// Data pendukung modul laporan (RPT-04, RPT-05).
	ByCustomer(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]trade.SummaryRow, error)
	ByProduct(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]trade.SummaryRow, error)
	CustomerCard(ctx context.Context, companyID, customerID uuid.UUID, from, to civil.Date) (trade.Card, error)
	// RevenueByAccount menjumlahkan DPP faktur diposting ke satu pelanggan per akun pendapatan,
	// untuk eliminasi antar-PT di konsolidasi (ME-04).
	RevenueByAccount(ctx context.Context, companyID, customerID uuid.UUID, from, to civil.Date) (map[uuid.UUID]money.Amount, error)
}

type Deps struct {
	Pool      *pgxpool.Pool
	Ledger    ledger.Service
	Core      core.Service
	Tax       tax.Service
	Cash      cash.Service
	Inventory inventory.Service
	// VoidGuard dipanggil sebelum faktur di-void, mis. untuk menolak faktur yang faktur
	// pajaknya sudah terbit di Coretax. Diisi modul pajak saat perangkaian; boleh nil.
	VoidGuard func(ctx context.Context, companyID, invoiceID uuid.UUID) error
	// ProjectCheck memeriksa proyek pada draf (mis. proyek yang dibatalkan); boleh nil.
	ProjectCheck ledger.ProjectCheck
	// AfterPost dan AfterVoid dipanggil di transaksi yang sama setelah faktur diposting atau
	// di-void, mis. untuk dokumen cerminan di PT grup (ME-03). Boleh nil.
	AfterPost func(ctx context.Context, companyID, invoiceID uuid.UUID, actor audit.Actor) error
	AfterVoid func(ctx context.Context, companyID, invoiceID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error
	// AfterCreditPost dan AfterCreditVoid sama untuk nota kredit. Boleh nil.
	AfterCreditPost func(ctx context.Context, companyID, noteID uuid.UUID, actor audit.Actor) error
	AfterCreditVoid func(ctx context.Context, companyID, noteID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error
	// AfterReceiptPost dan AfterReceiptVoid sama untuk pelunasan. Boleh nil.
	AfterReceiptPost func(ctx context.Context, companyID, receiptID uuid.UUID, actor audit.Actor) error
	AfterReceiptVoid func(ctx context.Context, companyID, receiptID uuid.UUID, date civil.Date, reason string, actor audit.Actor) error
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

// revenueClasses: baris faktur penjualan hanya ke akun pendapatan usaha (4) atau lain-lain (7).
var revenueClasses = map[int]bool{4: true, 7: true}

// prepared adalah faktur yang sudah diperiksa dan dihitung.
type prepared struct {
	lines    []trade.Line
	totals   trade.Totals
	products map[uuid.UUID]inventory.Product
}

// prepare memeriksa isi faktur dan menghitung baris serta totalnya.
func (s *service) prepare(ctx context.Context, companyID uuid.UUID, in *InvoiceInput) (prepared, error) {
	in.Memo = strings.TrimSpace(in.Memo)
	if in.Kind == "" {
		in.Kind = KindRegular
	}
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
	switch in.Kind {
	case KindRegular, KindAdvance, KindRetention, KindOpening:
		if in.ProgressPct != nil || in.Retention.Sign() != 0 {
			return prepared{}, fmt.Errorf("progres dan retensi hanya untuk faktur termin")
		}
	case KindProgress:
		if in.ProgressPct == nil || in.Retention.Sign() < 0 {
			return prepared{}, fmt.Errorf("faktur termin wajib punya progres dan retensi tidak negatif")
		}
	default:
		return prepared{}, fmt.Errorf("jenis faktur %q tidak dikenal", in.Kind)
	}
	if in.Kind != KindRegular && in.Kind != KindOpening && in.ProjectID == nil {
		return prepared{}, fmt.Errorf("faktur %s wajib punya proyek", in.Kind)
	}
	for i := range in.Lines {
		in.Lines[i].ProjectID = nil // proyek faktur penjualan ada di kepala faktur
		if in.Lines[i].DiscountAmount != nil && in.Kind != KindProgress {
			f[fmt.Sprintf("lines.%d.discountAmount", i)] = "Potongan nominal hanya untuk faktur termin."
		}
	}
	if len(f) > 0 {
		return prepared{}, httpx.Unprocessable("invoice.invalid", trade.FirstMessage(f)).WithFields(f)
	}
	customer, err := s.Core.Contact(ctx, companyID, in.CustomerID)
	if err != nil {
		return prepared{}, fieldErr(err, "customerId")
	}
	if !customer.IsCustomer || !customer.Active {
		msg := fmt.Sprintf("%s bukan pelanggan aktif.", customer.Name)
		return prepared{}, httpx.Unprocessable("invoice.invalid", msg).WithFields(map[string]string{"customerId": msg})
	}
	if in.ProjectID != nil && s.ProjectCheck != nil {
		if err := s.ProjectCheck(ctx, companyID, []uuid.UUID{*in.ProjectID}); err != nil {
			return prepared{}, fieldErr(err, "projectId")
		}
	}
	if err := rbac.CheckBranch(ctx, companyID, in.BranchID); err != nil {
		return prepared{}, err
	}
	if err := s.Core.CheckDimensions(ctx, companyID, in.BranchID, in.DepartmentID); err != nil {
		return prepared{}, err
	}
	if in.DueDate.IsZero() {
		in.DueDate = in.Date.AddDays(customer.PaymentTermsDays)
	}
	if in.DueDate.Before(in.Date) {
		msg := "Jatuh tempo tidak boleh sebelum tanggal faktur."
		return prepared{}, httpx.Unprocessable("invoice.invalid", msg).WithFields(map[string]string{"dueDate": msg})
	}
	products, err := s.lineProducts(ctx, companyID, in.Lines)
	if err != nil {
		return prepared{}, err
	}
	company, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return prepared{}, err
	}
	table, err := s.Tax.Table(ctx)
	if err != nil {
		return prepared{}, err
	}
	lines, totals, err := trade.Compute(table, in.Date, in.Lines, company.PKP)
	if err != nil {
		return prepared{}, err
	}
	if in.Kind == KindProgress && in.Retention.Cmp(totals.Total) > 0 {
		return prepared{}, fmt.Errorf("retensi %s melebihi total faktur %s", in.Retention, totals.Total)
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return prepared{}, err
	}
	allowed, err := s.allowedAccounts(ctx, companyID, in.Kind)
	if err != nil {
		return prepared{}, err
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
		case allowed != nil && !allowed[a.ID]:
			af[key] = fmt.Sprintf("%s %s bukan akun untuk faktur %s.", a.Code, a.Name, kindLabel(in.Kind))
		case allowed == nil && !revenueClasses[a.Class]:
			af[key] = fmt.Sprintf("%s %s bukan akun pendapatan; pilih akun berkepala 4 atau 7.", a.Code, a.Name)
		}
	}
	if len(af) > 0 {
		return prepared{}, httpx.Unprocessable("invoice.invalid", trade.FirstMessage(af)).WithFields(af)
	}
	return prepared{lines: lines, totals: totals, products: products}, nil
}

// allowedAccounts membatasi akun baris faktur uang muka dan retensi ke akun sistemnya;
// nil berarti akun pendapatan biasa.
func (s *service) allowedAccounts(ctx context.Context, companyID uuid.UUID, kind string) (map[uuid.UUID]bool, error) {
	var roles []ledger.Role
	switch kind {
	case KindAdvance:
		roles = []ledger.Role{ledger.ProjectAdvance, ledger.CustomerAdvance}
	case KindRetention:
		roles = []ledger.Role{ledger.RetentionReceivable}
	case KindOpening:
		roles = []ledger.Role{ledger.OpeningEquity}
	default:
		return nil, nil
	}
	out := map[uuid.UUID]bool{}
	for _, r := range roles {
		a, err := s.Ledger.RoleAccount(ctx, companyID, r)
		if err != nil {
			return nil, err
		}
		out[a.ID] = true
	}
	return out, nil
}

func kindLabel(kind string) string {
	switch kind {
	case KindAdvance:
		return "uang muka"
	case KindProgress:
		return "termin"
	case KindRetention:
		return "pelepasan retensi"
	case KindOpening:
		return "saldo awal"
	default:
		return "penjualan"
	}
}

// lineProducts memeriksa produk di baris faktur dan mengisi akun pendapatan default.
func (s *service) lineProducts(ctx context.Context, companyID uuid.UUID, lines []trade.LineInput) (map[uuid.UUID]inventory.Product, error) {
	var ids []uuid.UUID
	for _, l := range lines {
		if l.ProductID != nil {
			ids = append(ids, *l.ProductID)
		}
	}
	if len(ids) == 0 {
		return map[uuid.UUID]inventory.Product{}, nil
	}
	products, err := s.Inventory.ProductsByIDs(ctx, companyID, ids)
	if err != nil {
		return nil, err
	}
	f := map[string]string{}
	for i := range lines {
		l := &lines[i]
		if l.ProductID == nil {
			continue
		}
		p, ok := products[*l.ProductID]
		key := fmt.Sprintf("lines.%d.productId", i)
		switch {
		case !ok:
			f[key] = "Produk tidak ditemukan di PT ini."
		case !p.Active:
			f[key] = fmt.Sprintf("Produk %s nonaktif.", p.Code)
		case l.AccountID == uuid.Nil && p.RevenueAccountID != nil:
			l.AccountID = *p.RevenueAccountID
		}
	}
	if len(f) > 0 {
		return nil, httpx.Unprocessable("invoice.invalid", trade.FirstMessage(f)).WithFields(f)
	}
	return products, nil
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
			OtherDpp: l.OtherDPP, Vat: l.VAT, ProductID: l.ProductID, DiscountFixed: l.DiscountAmount != nil,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) CreateInvoice(ctx context.Context, companyID uuid.UUID, in InvoiceInput, actor audit.Actor) (InvoiceDetail, error) {
	var out InvoiceDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		p, err := s.prepare(ctx, companyID, &in)
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		t := p.totals
		if err := s.q(ctx).InsertInvoice(ctx, store.InsertInvoiceParams{
			ID: id, CompanyID: companyID, CustomerID: in.CustomerID, InvoiceDate: in.Date, DueDate: in.DueDate,
			Memo: in.Memo, Subtotal: t.Subtotal, Discount: t.Discount, Dpp: t.DPP, OtherDpp: t.OtherDPP,
			Vat: t.VAT, Total: t.Total, CreatedBy: actor.UserRef(), Kind: in.Kind, ProjectID: in.ProjectID,
			ProgressPct: in.ProgressPct, Retention: in.Retention, BranchID: in.BranchID, DepartmentID: in.DepartmentID,
		}); err != nil {
			return err
		}
		if err := s.writeLines(ctx, companyID, id, p.lines); err != nil {
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
		inv, err := s.lockDraft(ctx, companyID, id, actor, anyDraft)
		if err != nil {
			return err
		}
		if inv.Kind != KindRegular {
			return httpx.Conflict("invoice.from_project", fmt.Sprintf(
				"Draf faktur %s dibuat dari halaman proyek dan tidak bisa diubah di sini; hapus drafnya lalu buat ulang dari halaman proyek.", kindLabel(inv.Kind)))
		}
		in.Kind, in.ProgressPct, in.Retention = KindRegular, nil, money.Amount{}
		p, err := s.prepare(ctx, companyID, &in)
		if err != nil {
			return err
		}
		t := p.totals
		n, err := s.q(ctx).UpdateDraftInvoice(ctx, store.UpdateDraftInvoiceParams{
			CompanyID: companyID, ID: id, Version: int32(in.Version), CustomerID: in.CustomerID,
			InvoiceDate: in.Date, DueDate: in.DueDate, Memo: in.Memo, Subtotal: t.Subtotal, Discount: t.Discount,
			Dpp: t.DPP, OtherDpp: t.OtherDPP, Vat: t.VAT, Total: t.Total, ProjectID: in.ProjectID,
			BranchID: in.BranchID, DepartmentID: in.DepartmentID,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Draf ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if err := s.writeLines(ctx, companyID, id, p.lines); err != nil {
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

// storedInput menyusun ulang input faktur dari draf yang tersimpan.
func storedInput(inv store.SalesInvoice, stored []store.SalesInvoiceLine) InvoiceInput {
	in := InvoiceInput{
		CustomerID: inv.CustomerID, Date: inv.InvoiceDate, DueDate: inv.DueDate, Memo: inv.Memo, ProjectID: inv.ProjectID,
		Version: int(inv.Version), Kind: inv.Kind, ProgressPct: inv.ProgressPct, Retention: inv.Retention,
		BranchID: inv.BranchID, DepartmentID: inv.DepartmentID,
	}
	for _, l := range stored {
		li := trade.LineInput{
			Description: l.Description, AccountID: l.AccountID, Qty: l.Qty, UnitPrice: l.UnitPrice,
			DiscountPct: l.DiscountPct, TaxCode: l.TaxCode, ProductID: l.ProductID,
		}
		if l.DiscountFixed {
			d := l.Discount
			li.DiscountAmount = &d
		}
		in.Lines = append(in.Lines, li)
	}
	return in
}

// PostInvoice menghitung ulang draf dengan aturan pajak pada tanggal faktur, memberi nomor FJ,
// lalu memposting jurnal: debit piutang usaha; kredit pendapatan per akun dan PPN keluaran.
// Faktur termin mengakui pendapatan sebesar nilai progres, dengan debit uang muka proyek
// (potongan uang muka) dan piutang retensi (bagian yang ditahan). Barang persediaan
// mengeluarkan stok dan HPP-nya dijurnal modul persediaan.
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
		in := storedInput(inv, stored)
		p, err := s.prepare(ctx, companyID, &in)
		if err != nil {
			return err
		}
		t := p.totals
		receivable, err := s.Ledger.RoleAccount(ctx, companyID, ledger.Receivable)
		if err != nil {
			return err
		}
		series, label := seriesInvoice, "Faktur "
		if in.Kind == KindOpening {
			series, label = seriesOpening, "Saldo awal piutang "
		}
		number, err := s.Ledger.NextNumberFor(ctx, companyID, series, inv.InvoiceDate, in.BranchID)
		if err != nil {
			return err
		}
		memo := label + number
		if inv.Memo != "" {
			memo += " · " + inv.Memo
		}
		project := in.ProjectID
		drafts := []ledger.LineDraft{{AccountID: receivable.ID, Debit: t.Total.Sub(in.Retention), Memo: memo, ProjectID: project}}
		if in.Kind == KindProgress {
			if in.Retention.Sign() > 0 {
				acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.RetentionReceivable)
				if err != nil {
					return err
				}
				drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Debit: in.Retention, Memo: "Retensi ditahan " + number, ProjectID: project})
			}
			if t.Discount.Sign() > 0 {
				acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.ProjectAdvance)
				if err != nil {
					return err
				}
				drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Debit: t.Discount, Memo: "Potongan uang muka " + number, ProjectID: project})
			}
		}
		drafts = append(drafts, creditsByAccount(p.lines, in.Kind == KindProgress, project)...)
		if t.VAT.Sign() > 0 {
			vat, err := s.Ledger.RoleAccount(ctx, companyID, ledger.OutputVAT)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: vat.ID, Credit: t.VAT, Memo: "PPN keluaran " + number, ProjectID: project})
		}
		withDimensions(drafts, in.BranchID, in.DepartmentID)
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: inv.InvoiceDate, Memo: memo,
			SourceType: SourceInvoice, SourceID: &id, Number: number, Lines: drafts, Actor: actor,
		})
		if err != nil {
			return err
		}
		var issues []inventory.Issue
		for _, l := range p.lines {
			if l.ProductID != nil && p.products[*l.ProductID].Goods() {
				issues = append(issues, inventory.Issue{ProductID: *l.ProductID, Qty: l.Qty, Memo: l.Description})
			}
		}
		if err := s.Inventory.IssueGoods(ctx, companyID, inventory.Doc{
			SourceType: SourceInvoice, SourceID: id, Number: number, Date: inv.InvoiceDate, ProjectID: project,
			BranchID: in.BranchID, DepartmentID: in.DepartmentID,
		}, issues, actor); err != nil {
			return err
		}
		// Simpan hasil hitung ulang, lalu tandai diposting.
		if n, err := q.UpdateDraftInvoice(ctx, store.UpdateDraftInvoiceParams{
			CompanyID: companyID, ID: id, Version: inv.Version, CustomerID: in.CustomerID, InvoiceDate: in.Date,
			DueDate: in.DueDate, Memo: in.Memo, Subtotal: t.Subtotal, Discount: t.Discount, Dpp: t.DPP,
			OtherDpp: t.OtherDPP, Vat: t.VAT, Total: t.Total, ProjectID: in.ProjectID,
			BranchID: in.BranchID, DepartmentID: in.DepartmentID,
		}); err != nil || n != 1 {
			return errOr(err, "faktur gagal diperbarui sebelum posting")
		}
		if err := s.writeLines(ctx, companyID, id, p.lines); err != nil {
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
			After: map[string]any{"number": number, "kind": in.Kind, "total": t.Total.String(), "vat": t.VAT.String(), "date": inv.InvoiceDate.String()},
		}); err != nil {
			return err
		}
		if s.AfterPost != nil {
			if err := s.AfterPost(ctx, companyID, id, actor); err != nil {
				return err
			}
		}
		out, err = s.Invoice(ctx, companyID, id)
		return err
	})
	return out, err
}

// creditsByAccount menjumlahkan nilai baris per akun, urut kemunculan pertama. Faktur termin
// mengkredit nilai sebelum potongan (gross), karena potongannya adalah uang muka yang sudah
// diterima, bukan pengurang pendapatan.
func creditsByAccount(lines []trade.Line, gross bool, project *uuid.UUID) []ledger.LineDraft {
	var out []ledger.LineDraft
	index := map[uuid.UUID]int{}
	for _, l := range lines {
		amount := l.Net
		if gross {
			amount = l.Gross
		}
		if amount.IsZero() {
			continue
		}
		i, ok := index[l.AccountID]
		if !ok {
			index[l.AccountID] = len(out)
			out = append(out, ledger.LineDraft{AccountID: l.AccountID, Memo: l.Description, ProjectID: project})
			i = len(out) - 1
		}
		out[i].Credit = out[i].Credit.Add(amount)
	}
	return out
}

// VoidInvoice membatalkan faktur yang belum punya pelunasan; jurnalnya dibalik di tanggal void
// dan barang persediaannya kembali ke stok dengan HPP semula.
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
		if err := rbac.CheckBranch(ctx, companyID, inv.BranchID); err != nil {
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
		notes, err := q.InvoiceCreditNotes(ctx, store.InvoiceCreditNotesParams{CompanyID: companyID, InvoiceID: id})
		if err != nil {
			return err
		}
		for _, n := range notes {
			if n.Status == "posted" {
				active = append(active, n.Number)
			}
		}
		if len(active) > 0 {
			return httpx.Unprocessable("invoice.has_credit_notes", fmt.Sprintf(
				"Faktur %s sudah punya nota kredit %s; void nota kreditnya dulu.", deref(inv.Number), strings.Join(active, ", ")))
		}
		if err := s.checkProjectVoid(ctx, companyID, inv); err != nil {
			return err
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
		if err := s.Inventory.ReverseSource(ctx, companyID, SourceInvoice, id, date, actor); err != nil {
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
		if s.AfterVoid != nil {
			if err := s.AfterVoid(ctx, companyID, id, date, reason, actor); err != nil {
				return err
			}
		}
		out, err = s.Invoice(ctx, companyID, id)
		return err
	})
	return out, err
}

// checkProjectVoid menjaga urutan tagihan proyek: uang muka yang sudah dipotong di faktur
// termin, atau termin yang retensinya sudah dilepas, tidak bisa di-void lebih dulu.
func (s *service) checkProjectVoid(ctx context.Context, companyID uuid.UUID, inv store.SalesInvoice) error {
	if inv.ProjectID == nil || (inv.Kind != KindAdvance && inv.Kind != KindProgress) {
		return nil
	}
	rows, err := s.q(ctx).ListInvoices(ctx, store.ListInvoicesParams{
		CompanyID: companyID, FromDate: civil.New(2000, 1, 1), ToDate: civil.New(2100, 12, 31), ProjectID: inv.ProjectID,
	})
	if err != nil {
		return err
	}
	var blockers []string
	for _, r := range rows {
		if r.Status == "void" || r.ID == inv.ID {
			continue
		}
		switch {
		case inv.Kind == KindAdvance && r.Kind == KindProgress && r.Discount.Sign() > 0:
			blockers = append(blockers, numberOrDraft(r.Number))
		case inv.Kind == KindProgress && r.Kind == KindRetention:
			blockers = append(blockers, numberOrDraft(r.Number))
		}
	}
	if len(blockers) == 0 {
		return nil
	}
	what := "faktur termin yang memotong uang muka ini"
	if inv.Kind == KindProgress {
		what = "faktur pelepasan retensi proyek ini"
	}
	return httpx.Unprocessable("invoice.project_order", fmt.Sprintf(
		"Void atau hapus dulu %s: %s.", what, strings.Join(blockers, ", ")))
}

func numberOrDraft(n *string) string {
	if n == nil {
		return "draf"
	}
	return *n
}

func receivableOf(inv store.SalesInvoice) money.Amount {
	if inv.Receivable != nil {
		return *inv.Receivable
	}
	return inv.Total.Sub(inv.Retention)
}

// state: paid berarti pelunasan ditambah nota kredit sudah menutup piutang faktur.
func state(status string, receivable, paid money.Amount) string {
	switch {
	case status != "posted":
		return status
	case paid.Cmp(receivable) >= 0:
		return "paid"
	default:
		return "open"
	}
}

// summary menyusun ringkasan faktur dari baris tabel.
func summary(r store.SalesInvoice, paid, credited money.Amount, names map[uuid.UUID]string) Invoice {
	rec := receivableOf(r)
	inv := Invoice{
		ID: r.ID, Number: r.Number, Kind: r.Kind, Status: r.Status, State: state(r.Status, rec, paid.Add(credited)),
		CustomerID: r.CustomerID, CustomerName: names[r.CustomerID], ProjectID: r.ProjectID, ProgressPct: r.ProgressPct,
		BranchID: r.BranchID, DepartmentID: r.DepartmentID,
		Date: r.InvoiceDate, DueDate: r.DueDate, Memo: r.Memo, Subtotal: r.Subtotal, Discount: r.Discount,
		DPP: r.Dpp, OtherDPP: r.OtherDpp, VAT: r.Vat, Total: r.Total, Retention: r.Retention, Receivable: rec, Paid: paid,
		Credited: credited,
	}
	if r.Status == "posted" {
		inv.Outstanding = rec.Sub(paid).Sub(credited)
		if inv.Outstanding.Sign() > 0 {
			if late := civil.Today(civil.Jakarta).DaysSince(r.DueDate); late > 0 {
				inv.DaysOverdue = late
			}
		}
	}
	return inv
}

func (s *service) Invoices(ctx context.Context, companyID uuid.UUID, f InvoiceFilter) ([]Invoice, error) {
	p := store.ListInvoicesParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, CustomerID: f.CustomerID, ProjectID: f.ProjectID, Branches: rbac.BranchesFor(ctx, companyID)}
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
		out[i] = summary(invoiceOf(r), r.Paid, r.Credited, names)
	}
	return out, nil
}

func (s *service) OpenInvoices(ctx context.Context, companyID uuid.UUID, customerID *uuid.UUID) ([]Invoice, error) {
	rows, err := s.q(ctx).OpenInvoices(ctx, store.OpenInvoicesParams{CompanyID: companyID, CustomerID: customerID, Branches: rbac.BranchesFor(ctx, companyID)})
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
		out[i] = summary(openInvoiceOf(r), r.Paid, r.Credited, names)
	}
	return out, nil
}

func (s *service) Invoice(ctx context.Context, companyID, id uuid.UUID) (InvoiceDetail, error) {
	q := s.q(ctx)
	r, err := q.GetInvoice(ctx, store.GetInvoiceParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && rbac.CheckBranch(ctx, companyID, r.BranchID) != nil) {
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
	notes, err := q.InvoiceCreditNotes(ctx, store.InvoiceCreditNotesParams{CompanyID: companyID, InvoiceID: id})
	if err != nil {
		return InvoiceDetail{}, err
	}
	var credited money.Amount
	credits := make([]InvoiceCredit, 0, len(notes))
	for _, n := range notes {
		if n.Status == "posted" {
			credited = credited.Add(n.Applied)
		}
		credits = append(credits, InvoiceCredit{CreditNoteID: n.ID, Number: n.Number, Date: n.NoteDate, Status: n.Status, Total: n.Total, Applied: n.Applied})
	}
	out := InvoiceDetail{
		Invoice: summary(store.SalesInvoice{
			ID: r.ID, CompanyID: r.CompanyID, Number: r.Number, Status: r.Status, CustomerID: r.CustomerID,
			InvoiceDate: r.InvoiceDate, DueDate: r.DueDate, Memo: r.Memo, Subtotal: r.Subtotal, Discount: r.Discount,
			Dpp: r.Dpp, OtherDpp: r.OtherDpp, Vat: r.Vat, Total: r.Total, Kind: r.Kind, ProjectID: r.ProjectID,
			ProgressPct: r.ProgressPct, Retention: r.Retention, Receivable: r.Receivable,
			BranchID: r.BranchID, DepartmentID: r.DepartmentID,
		}, paid, credited, names),
		Totals: trade.Totals{
			Subtotal: r.Subtotal, Discount: r.Discount, DPP: r.Dpp, OtherDPP: r.OtherDpp, VAT: r.Vat, Total: r.Total,
		},
		Payments: payments, CreditNotes: credits, JournalEntryID: r.JournalEntryID, CreatedByID: r.CreatedBy, CreatedBy: r.CreatedByName,
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
	if r.Status == "draft" {
		if out.CreditWarning, err = s.creditWarning(ctx, companyID, r.CustomerID, out.Receivable); err != nil {
			return InvoiceDetail{}, err
		}
	}
	byLine, err := q.CreditedByLine(ctx, store.CreditedByLineParams{CompanyID: companyID, InvoiceID: id})
	if err != nil {
		return InvoiceDetail{}, err
	}
	creditedLine := map[uuid.UUID]store.CreditedByLineRow{}
	for _, c := range byLine {
		creditedLine[c.InvoiceLineID] = c
	}
	for _, l := range lines {
		a := accounts[l.AccountID]
		li := trade.LineInput{
			Description: l.Description, AccountID: l.AccountID, Qty: l.Qty, UnitPrice: l.UnitPrice,
			DiscountPct: l.DiscountPct, TaxCode: l.TaxCode, ProductID: l.ProductID,
		}
		if l.DiscountFixed {
			d := l.Discount
			li.DiscountAmount = &d
		}
		out.Lines = append(out.Lines, InvoiceLine{
			Line: trade.Line{
				LineInput: li, Gross: l.Net.Add(l.Discount), Discount: l.Discount, Net: l.Net, OtherDPP: l.OtherDpp, VAT: l.Vat,
			},
			ID: l.ID, AccountCode: a.Code, AccountName: a.Name,
			CreditedNet: creditedLine[l.ID].Net, ReturnedQty: creditedLine[l.ID].Restocked,
		})
	}
	return out, nil
}

func invoiceOf(r store.ListInvoicesRow) store.SalesInvoice {
	return store.SalesInvoice{
		ID: r.ID, CompanyID: r.CompanyID, Number: r.Number, Status: r.Status, CustomerID: r.CustomerID,
		InvoiceDate: r.InvoiceDate, DueDate: r.DueDate, Memo: r.Memo, Subtotal: r.Subtotal, Discount: r.Discount,
		Dpp: r.Dpp, OtherDpp: r.OtherDpp, Vat: r.Vat, Total: r.Total, Kind: r.Kind, ProjectID: r.ProjectID,
		ProgressPct: r.ProgressPct, Retention: r.Retention, Receivable: r.Receivable,
		BranchID: r.BranchID, DepartmentID: r.DepartmentID,
	}
}

func openInvoiceOf(r store.OpenInvoicesRow) store.SalesInvoice {
	return store.SalesInvoice{
		ID: r.ID, CompanyID: r.CompanyID, Number: r.Number, Status: r.Status, CustomerID: r.CustomerID,
		InvoiceDate: r.InvoiceDate, DueDate: r.DueDate, Memo: r.Memo, Subtotal: r.Subtotal, Discount: r.Discount,
		Dpp: r.Dpp, OtherDpp: r.OtherDpp, Vat: r.Vat, Total: r.Total, Kind: r.Kind, ProjectID: r.ProjectID,
		ProgressPct: r.ProgressPct, Retention: r.Retention, Receivable: r.Receivable,
		BranchID: r.BranchID, DepartmentID: r.DepartmentID,
	}
}

func (s *service) Aging(ctx context.Context, companyID uuid.UUID, asOf civil.Date) (trade.Aging, error) {
	rows, err := s.q(ctx).AgingInvoices(ctx, store.AgingInvoicesParams{CompanyID: companyID, AsOf: asOf, Branches: rbac.BranchesFor(ctx, companyID)})
	if err != nil {
		return trade.Aging{}, err
	}
	items := make([]trade.AgingItem, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		rec := money.Amount{}
		if r.Receivable != nil {
			rec = *r.Receivable
		}
		out := rec.Sub(r.Paid)
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

func (s *service) ByCustomer(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]trade.SummaryRow, error) {
	rows, err := s.q(ctx).SalesByCustomer(ctx, store.SalesByCustomerParams{CompanyID: companyID, FromDate: from, ToDate: to})
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
	out := make([]trade.SummaryRow, len(rows))
	for i, r := range rows {
		id := r.CustomerID
		out[i] = trade.SummaryRow{ID: &id, Name: names[id], Documents: int(r.Invoices), DPP: r.Dpp, VAT: r.Vat, Total: r.Total}
	}
	// Penjualan eceran tidak punya pelanggan; rekap hariannya menjadi satu baris sendiri.
	daily, err := s.dailyTotals(ctx, companyID, from, to)
	if err != nil {
		return nil, err
	}
	if daily.Docs > 0 {
		out = append(out, trade.SummaryRow{Name: "Penjualan eceran (rekap harian)", Documents: int(daily.Docs), DPP: daily.Dpp, VAT: daily.Vat, Total: daily.Total})
	}
	return out, nil
}

// ByProduct menjumlahkan baris faktur per produk; baris tanpa produk menjadi satu baris sendiri.
func (s *service) ByProduct(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]trade.SummaryRow, error) {
	rows, err := s.q(ctx).SalesByProduct(ctx, store.SalesByProductParams{CompanyID: companyID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, r := range rows {
		if r.ProductID != nil {
			ids = append(ids, *r.ProductID)
		}
	}
	products, err := s.Inventory.ProductsByIDs(ctx, companyID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]trade.SummaryRow, len(rows))
	for i, r := range rows {
		row := trade.SummaryRow{ID: r.ProductID, Name: "Tanpa produk", Documents: int(r.Invoices), DPP: r.Dpp, VAT: r.Vat, Total: r.Dpp.Add(r.Vat)}
		if r.ProductID != nil {
			p := products[*r.ProductID]
			qty := r.Qty
			row.Code, row.Name, row.Unit, row.Qty = p.Code, p.Name, p.Unit, &qty
		}
		out[i] = row
	}
	return out, nil
}

func (s *service) CustomerCard(ctx context.Context, companyID, customerID uuid.UUID, from, to civil.Date) (trade.Card, error) {
	c, err := s.Core.Contact(ctx, companyID, customerID)
	if err != nil {
		return trade.Card{}, err
	}
	q := s.q(ctx)
	opening, err := q.CustomerCardOpening(ctx, store.CustomerCardOpeningParams{CompanyID: companyID, CustomerID: customerID, FromDate: from})
	if err != nil {
		return trade.Card{}, err
	}
	rows, err := q.CustomerCardLines(ctx, store.CustomerCardLinesParams{CompanyID: companyID, CustomerID: customerID, FromDate: from, ToDate: to})
	if err != nil {
		return trade.Card{}, err
	}
	card := trade.Card{ContactID: customerID, ContactName: c.Name, From: from, To: to, Opening: opening}
	for _, r := range rows {
		card.Lines = append(card.Lines, trade.CardLine{
			Kind: r.Kind, DocID: r.ID, Number: r.Number, Date: r.DocDate, Memo: r.Memo, Debit: r.Debit, Credit: r.Credit, Withholding: r.Withholding,
		})
	}
	card.Fill(false)
	return card, nil
}

func (s *service) RevenueByAccount(ctx context.Context, companyID, customerID uuid.UUID, from, to civil.Date) (map[uuid.UUID]money.Amount, error) {
	rows, err := s.q(ctx).RevenueByAccount(ctx, store.RevenueByAccountParams{CompanyID: companyID, CustomerID: customerID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]money.Amount, len(rows))
	for _, r := range rows {
		out[r.AccountID] = r.Amount
	}
	return out, nil
}

// OpeningInput adalah sisa piutang satu faktur dari sistem lama (GL-06). Reference adalah
// nomor faktur lamanya; tanggal dan jatuh tempo asli dipakai untuk umur piutang.
type OpeningInput struct {
	ContactID uuid.UUID
	Reference string
	Date      civil.Date
	DueDate   civil.Date
	Amount    money.Amount
	Memo      string
}

// CreateOpeningInvoice memposting faktur saldo awal tanpa PPN: debit piutang usaha, kredit
// ekuitas saldo awal. Faktur ini tidak masuk ringkasan penjualan maupun ekspor Coretax.
func (s *service) CreateOpeningInvoice(ctx context.Context, companyID uuid.UUID, in OpeningInput, actor audit.Actor) (InvoiceDetail, error) {
	var out InvoiceDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if in.Amount.Sign() <= 0 {
			return httpx.Unprocessable("invoice.invalid", "Sisa piutang harus lebih dari 0.").WithFields(map[string]string{"amount": "Sisa piutang harus lebih dari 0."})
		}
		acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.OpeningEquity)
		if err != nil {
			return err
		}
		memo := "Saldo awal piutang"
		if ref := strings.TrimSpace(in.Reference); ref != "" {
			memo += " no. " + ref
		}
		if m := strings.TrimSpace(in.Memo); m != "" {
			memo += " · " + m
		}
		if utf8.RuneCountInString(memo) > 500 {
			memo = string([]rune(memo)[:500])
		}
		d, err := s.CreateInvoice(ctx, companyID, InvoiceInput{
			CustomerID: in.ContactID, Date: in.Date, DueDate: in.DueDate, Memo: memo, Kind: KindOpening,
			Lines: []trade.LineInput{{Description: memo, AccountID: acc.ID, Qty: money.FromInt(1), UnitPrice: in.Amount, TaxCode: "NON-PPN"}},
		}, actor)
		if err != nil {
			return err
		}
		out, err = s.PostInvoice(ctx, companyID, d.ID, actor)
		return err
	})
	return out, err
}

// creditWarning membandingkan piutang pelanggan yang belum lunas ditambah faktur baru dengan
// limit kreditnya (AR-01). Hanya peringatan; faktur tetap boleh diposting.
func (s *service) creditWarning(ctx context.Context, companyID, customerID uuid.UUID, amount money.Amount) (*string, error) {
	c, err := s.Core.Contact(ctx, companyID, customerID)
	if err != nil || c.CreditLimit == nil {
		return nil, err
	}
	open, err := s.OpenInvoices(ctx, companyID, &customerID)
	if err != nil {
		return nil, err
	}
	exposure := amount
	for _, inv := range open {
		exposure = exposure.Add(inv.Outstanding)
	}
	if exposure.Cmp(*c.CreditLimit) <= 0 {
		return nil, nil
	}
	msg := fmt.Sprintf("Piutang %s menjadi %s bila faktur ini diposting, melewati limit kreditnya %s.", c.Name, exposure.IDR(), c.CreditLimit.IDR())
	return &msg, nil
}

// withDimensions menempelkan cabang dan departemen dokumen ke setiap baris jurnalnya (GL-09).
func withDimensions(lines []ledger.LineDraft, branch, department *uuid.UUID) {
	for i := range lines {
		lines[i].BranchID, lines[i].DepartmentID = branch, department
	}
}

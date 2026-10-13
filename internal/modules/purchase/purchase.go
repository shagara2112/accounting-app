// Package purchase adalah modul pembelian dan utang (AP): tagihan pemasok dengan PPN masukan dan
// PPh yang dipotong (AP-02), pembayaran sebagian atau banyak tagihan sekaligus (AP-03), dan umur
// utang (AP-06). Setiap tagihan memposting jurnal: debit beban atau aset dan PPN masukan, kredit
// utang usaha dan utang PPh.
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
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/core"
	"lajur/internal/modules/purchase/internal/store"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const (
	SourceBill    = "purchase.bill"
	SourcePayment = "purchase.payment"

	seriesBill    = "FB"  // faktur beli
	seriesPayment = "BKK" // bukti kas keluar, satu seri dengan pengeluaran kas
)

type BillInput struct {
	SupplierID        uuid.UUID         `json:"supplierId"`
	SupplierInvoiceNo string            `json:"supplierInvoiceNo"`
	Date              civil.Date        `json:"date"`
	DueDate           civil.Date        `json:"dueDate"` // kosong = tanggal + termin pemasok
	Memo              string            `json:"memo"`
	WithholdingCode   string            `json:"withholdingCode"` // PPh 23 atau 4(2) yang kita potong; kosong bila tidak ada
	Lines             []trade.LineInput `json:"lines"`
	Version           int               `json:"version"`
}

// Bill adalah ringkasan tagihan. Payable = total − PPh dipotong, yaitu yang dibayar ke pemasok.
type Bill struct {
	ID                uuid.UUID    `json:"id"`
	Number            *string      `json:"number"`
	Status            string       `json:"status"`
	State             string       `json:"state"`
	SupplierID        uuid.UUID    `json:"supplierId"`
	SupplierName      string       `json:"supplierName"`
	SupplierInvoiceNo string       `json:"supplierInvoiceNo"`
	Date              civil.Date   `json:"date"`
	DueDate           civil.Date   `json:"dueDate"`
	Memo              string       `json:"memo"`
	Total             money.Amount `json:"total"`
	Payable           money.Amount `json:"payable"`
	Paid              money.Amount `json:"paid"`
	Outstanding       money.Amount `json:"outstanding"`
	DaysOverdue       int          `json:"daysOverdue"`
}

type BillLine struct {
	trade.Line
	AccountCode string `json:"accountCode"`
	AccountName string `json:"accountName"`
}

type BillPayment struct {
	PaymentID uuid.UUID    `json:"paymentId"`
	Number    string       `json:"number"`
	Date      civil.Date   `json:"date"`
	Status    string       `json:"status"`
	Amount    money.Amount `json:"amount"`
}

type Ref struct {
	ID     uuid.UUID `json:"id"`
	Number *string   `json:"number"`
	Status string    `json:"status"`
}

type BillDetail struct {
	Bill
	Totals          trade.Totals  `json:"totals"`
	WithholdingCode *string       `json:"withholdingCode,omitempty"`
	Withholding     money.Amount  `json:"withholding"`
	Lines           []BillLine    `json:"lines"`
	Payments        []BillPayment `json:"payments"`
	Duplicates      []Ref         `json:"duplicates"` // tagihan lain dengan nomor faktur pemasok yang sama
	JournalEntryID  *uuid.UUID    `json:"journalEntryId,omitempty"`
	CreatedByID     *uuid.UUID    `json:"createdById,omitempty"`
	CreatedBy       *string       `json:"createdBy,omitempty"`
	PostedBy        *string       `json:"postedBy,omitempty"`
	PostedAt        *time.Time    `json:"postedAt,omitempty"`
	VoidedBy        *string       `json:"voidedBy,omitempty"`
	VoidedAt        *time.Time    `json:"voidedAt,omitempty"`
	VoidReason      *string       `json:"voidReason,omitempty"`
	Version         int           `json:"version"`
}

type BillFilter struct {
	From, To   civil.Date
	State      string
	SupplierID *uuid.UUID
	Search     string
}

type Service interface {
	Bills(ctx context.Context, companyID uuid.UUID, f BillFilter) ([]Bill, error)
	Bill(ctx context.Context, companyID, id uuid.UUID) (BillDetail, error)
	CreateBill(ctx context.Context, companyID uuid.UUID, in BillInput, actor audit.Actor) (BillDetail, error)
	UpdateBill(ctx context.Context, companyID, id uuid.UUID, in BillInput, actor audit.Actor, anyDraft bool) (BillDetail, error)
	DeleteBill(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor, anyDraft bool) error
	PostBill(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (BillDetail, error)
	VoidBill(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (BillDetail, error)
	OpenBills(ctx context.Context, companyID uuid.UUID, supplierID *uuid.UUID) ([]Bill, error)

	Payments(ctx context.Context, companyID uuid.UUID, f PaymentFilter) ([]Payment, error)
	Payment(ctx context.Context, companyID, id uuid.UUID) (PaymentDetail, error)
	CreatePayment(ctx context.Context, companyID uuid.UUID, in PaymentInput, actor audit.Actor) (PaymentDetail, error)
	VoidPayment(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (PaymentDetail, error)

	Aging(ctx context.Context, companyID uuid.UUID, asOf civil.Date) (trade.Aging, error)

	// VATByMonth dan Withheld adalah data pendukung modul pajak (TAX-06, TAX-08).
	VATByMonth(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]trade.MonthVAT, error)
	Withheld(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]Withheld, error)
}

type Deps struct {
	Pool   *pgxpool.Pool
	Ledger ledger.Service
	Core   core.Service
	Tax    tax.Service
	Cash   cash.Service
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

// costClasses: baris tagihan ke aset (persediaan, aset tetap, biaya dibayar di muka), beban pokok,
// beban usaha, atau beban lain-lain.
var costClasses = map[int]bool{1: true, 5: true, 6: true, 8: true}

type prepared struct {
	lines       []trade.Line
	totals      trade.Totals
	whCode      *tax.Code
	withholding money.Amount
	supplier    core.Contact
	pkp         bool
}

func (s *service) prepare(ctx context.Context, companyID uuid.UUID, in *BillInput) (prepared, error) {
	in.Memo = strings.TrimSpace(in.Memo)
	in.SupplierInvoiceNo = strings.TrimSpace(in.SupplierInvoiceNo)
	in.WithholdingCode = strings.TrimSpace(in.WithholdingCode)
	f := map[string]string{}
	if in.Date.IsZero() {
		f["date"] = "Tanggal tagihan wajib diisi."
	}
	if utf8.RuneCountInString(in.Memo) > 500 {
		f["memo"] = "Catatan maksimal 500 karakter."
	}
	if utf8.RuneCountInString(in.SupplierInvoiceNo) > 60 {
		f["supplierInvoiceNo"] = "Nomor faktur pemasok maksimal 60 karakter."
	}
	if in.SupplierID == uuid.Nil {
		f["supplierId"] = "Pilih pemasok."
	}
	if len(f) > 0 {
		return prepared{}, httpx.Unprocessable("bill.invalid", trade.FirstMessage(f)).WithFields(f)
	}
	supplier, err := s.Core.Contact(ctx, companyID, in.SupplierID)
	if err != nil {
		return prepared{}, fieldErr(err, "supplierId")
	}
	if !supplier.IsSupplier || !supplier.Active {
		msg := fmt.Sprintf("%s bukan pemasok aktif.", supplier.Name)
		return prepared{}, httpx.Unprocessable("bill.invalid", msg).WithFields(map[string]string{"supplierId": msg})
	}
	if in.DueDate.IsZero() {
		in.DueDate = in.Date.AddDays(supplier.PaymentTermsDays)
	}
	if in.DueDate.Before(in.Date) {
		msg := "Jatuh tempo tidak boleh sebelum tanggal tagihan."
		return prepared{}, httpx.Unprocessable("bill.invalid", msg).WithFields(map[string]string{"dueDate": msg})
	}
	company, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return prepared{}, err
	}
	table, err := s.Tax.Table(ctx)
	if err != nil {
		return prepared{}, err
	}
	// PT non-PKP tetap membayar PPN ke pemasok PKP; PPN-nya tidak bisa dikreditkan dan menjadi biaya.
	lines, totals, err := trade.Compute(table, in.Date, in.Lines, true)
	if err != nil {
		return prepared{}, err
	}
	accounts, err := s.accounts(ctx, companyID)
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
		case !costClasses[a.Class]:
			af[key] = fmt.Sprintf("%s %s tidak bisa dipakai di tagihan; pilih akun aset, beban pokok, atau beban.", a.Code, a.Name)
		}
	}
	if len(af) > 0 {
		return prepared{}, httpx.Unprocessable("bill.invalid", trade.FirstMessage(af)).WithFields(af)
	}
	p := prepared{lines: lines, totals: totals, supplier: supplier, pkp: company.PKP}
	if in.WithholdingCode != "" {
		code, err := table.Find(in.WithholdingCode, in.Date)
		if err != nil {
			return prepared{}, fieldErr(err, "withholdingCode")
		}
		if code.Kind != tax.KindPPh {
			msg := fmt.Sprintf("%s bukan kode PPh.", code.Code)
			return prepared{}, httpx.Unprocessable("bill.invalid", msg).WithFields(map[string]string{"withholdingCode": msg})
		}
		p.whCode = &code
		p.withholding = code.Withholding(totals.DPP)
	}
	return p, nil
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

func (s *service) writeLines(ctx context.Context, companyID, billID uuid.UUID, lines []trade.Line) error {
	q := s.q(ctx)
	if err := q.DeleteBillLines(ctx, store.DeleteBillLinesParams{CompanyID: companyID, BillID: billID}); err != nil {
		return err
	}
	for i, l := range lines {
		if err := q.InsertBillLine(ctx, store.InsertBillLineParams{
			ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, BillID: billID, LineNo: int16(i + 1),
			Description: l.Description, AccountID: l.AccountID, Qty: l.Qty, UnitPrice: l.UnitPrice,
			DiscountPct: l.DiscountPct, Discount: l.Discount, Net: l.Net, TaxCode: l.TaxCode,
			OtherDpp: l.OtherDPP, Vat: l.VAT,
		}); err != nil {
			return err
		}
	}
	return nil
}

func whCodePtr(p prepared) *string {
	if p.whCode == nil {
		return nil
	}
	c := p.whCode.Code
	return &c
}

func (s *service) CreateBill(ctx context.Context, companyID uuid.UUID, in BillInput, actor audit.Actor) (BillDetail, error) {
	var out BillDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		p, err := s.prepare(ctx, companyID, &in)
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		t := p.totals
		if err := s.q(ctx).InsertBill(ctx, store.InsertBillParams{
			ID: id, CompanyID: companyID, SupplierID: in.SupplierID, SupplierInvoiceNo: in.SupplierInvoiceNo,
			BillDate: in.Date, DueDate: in.DueDate, Memo: in.Memo, Subtotal: t.Subtotal, Discount: t.Discount,
			Dpp: t.DPP, OtherDpp: t.OtherDPP, Vat: t.VAT, WithholdingCode: whCodePtr(p), Withholding: p.withholding,
			Total: t.Total, Payable: t.Total.Sub(p.withholding), CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		if err := s.writeLines(ctx, companyID, id, p.lines); err != nil {
			return err
		}
		out, err = s.Bill(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) lockDraft(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor, anyDraft bool) (store.PurchaseBill, error) {
	b, err := s.q(ctx).GetBillForUpdate(ctx, store.GetBillForUpdateParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return b, httpx.NotFound("bill.not_found", "Tagihan tidak ditemukan.")
	}
	if err != nil {
		return b, err
	}
	if b.Status != "draft" {
		return b, httpx.Conflict("bill.not_draft", fmt.Sprintf("Tagihan %s sudah diposting; koreksi lewat void.", deref(b.Number)))
	}
	if !anyDraft && (b.CreatedBy == nil || *b.CreatedBy != actor.UserID) {
		return b, httpx.Forbidden("bill.not_owner", "Draf ini dibuat orang lain; hanya pembuatnya, Manager, atau Pemilik yang boleh mengubahnya.")
	}
	return b, nil
}

func (s *service) UpdateBill(ctx context.Context, companyID, id uuid.UUID, in BillInput, actor audit.Actor, anyDraft bool) (BillDetail, error) {
	var out BillDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if _, err := s.lockDraft(ctx, companyID, id, actor, anyDraft); err != nil {
			return err
		}
		p, err := s.prepare(ctx, companyID, &in)
		if err != nil {
			return err
		}
		t := p.totals
		n, err := s.q(ctx).UpdateDraftBill(ctx, store.UpdateDraftBillParams{
			CompanyID: companyID, ID: id, Version: int32(in.Version), SupplierID: in.SupplierID,
			SupplierInvoiceNo: in.SupplierInvoiceNo, BillDate: in.Date, DueDate: in.DueDate, Memo: in.Memo,
			Subtotal: t.Subtotal, Discount: t.Discount, Dpp: t.DPP, OtherDpp: t.OtherDPP, Vat: t.VAT,
			WithholdingCode: whCodePtr(p), Withholding: p.withholding, Total: t.Total, Payable: t.Total.Sub(p.withholding),
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
		out, err = s.Bill(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) DeleteBill(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor, anyDraft bool) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if _, err := s.lockDraft(ctx, companyID, id, actor, anyDraft); err != nil {
			return err
		}
		_, err := s.q(ctx).DeleteDraftBill(ctx, store.DeleteDraftBillParams{CompanyID: companyID, ID: id})
		return err
	})
}

// PostBill: debit akun baris (DPP) dan PPN masukan; kredit utang usaha (yang dibayar ke pemasok)
// dan utang PPh yang dipotong. PT non-PKP membebankan PPN ke akun barisnya sendiri.
func (s *service) PostBill(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (BillDetail, error) {
	var out BillDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		b, err := s.lockDraft(ctx, companyID, id, actor, true)
		if err != nil {
			return err
		}
		stored, err := q.BillLines(ctx, store.BillLinesParams{CompanyID: companyID, BillID: id})
		if err != nil {
			return err
		}
		in := BillInput{
			SupplierID: b.SupplierID, SupplierInvoiceNo: b.SupplierInvoiceNo, Date: b.BillDate, DueDate: b.DueDate,
			Memo: b.Memo, WithholdingCode: deref(b.WithholdingCode), Version: int(b.Version),
		}
		for _, l := range stored {
			in.Lines = append(in.Lines, trade.LineInput{
				Description: l.Description, AccountID: l.AccountID, Qty: l.Qty, UnitPrice: l.UnitPrice,
				DiscountPct: l.DiscountPct, TaxCode: l.TaxCode,
			})
		}
		p, err := s.prepare(ctx, companyID, &in)
		if err != nil {
			return err
		}
		t := p.totals
		payable := t.Total.Sub(p.withholding)
		payableAcc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.Payable)
		if err != nil {
			return err
		}
		number, err := s.Ledger.NextNumber(ctx, companyID, seriesBill, b.BillDate)
		if err != nil {
			return err
		}
		memo := "Tagihan " + p.supplier.Name
		if in.SupplierInvoiceNo != "" {
			memo += " no. " + in.SupplierInvoiceNo
		}
		drafts := debitsByAccount(p.lines, !p.pkp)
		if p.pkp && t.VAT.Sign() > 0 {
			vat, err := s.Ledger.RoleAccount(ctx, companyID, ledger.InputVAT)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: vat.ID, Debit: t.VAT, Memo: "PPN masukan " + number})
		}
		drafts = append(drafts, ledger.LineDraft{AccountID: payableAcc.ID, Credit: payable, Memo: memo})
		if p.withholding.Sign() > 0 {
			role := ledger.WithholdingPPh23
			if p.whCode.Article == tax.ArticleFinal {
				role = ledger.WithholdingPPhFinal
			}
			acc, err := s.Ledger.RoleAccount(ctx, companyID, role)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Credit: p.withholding, Memo: p.whCode.Name + " " + p.supplier.Name})
		}
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: b.BillDate, Memo: memo, SourceType: SourceBill,
			SourceID: &id, Number: number, Lines: drafts, Actor: actor,
		})
		if err != nil {
			return err
		}
		if n, err := q.UpdateDraftBill(ctx, store.UpdateDraftBillParams{
			CompanyID: companyID, ID: id, Version: b.Version, SupplierID: in.SupplierID,
			SupplierInvoiceNo: in.SupplierInvoiceNo, BillDate: in.Date, DueDate: in.DueDate, Memo: in.Memo,
			Subtotal: t.Subtotal, Discount: t.Discount, Dpp: t.DPP, OtherDpp: t.OtherDPP, Vat: t.VAT,
			WithholdingCode: whCodePtr(p), Withholding: p.withholding, Total: t.Total, Payable: payable,
		}); err != nil || n != 1 {
			return errOr(err, "tagihan gagal diperbarui sebelum posting")
		}
		if err := s.writeLines(ctx, companyID, id, p.lines); err != nil {
			return err
		}
		if n, err := q.MarkBillPosted(ctx, store.MarkBillPostedParams{
			CompanyID: companyID, ID: id, Number: &number, JournalEntryID: &posted.ID, PostedBy: actor.UserRef(),
		}); err != nil || n != 1 {
			return errOr(err, "tagihan gagal ditandai diposting")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "purchase.bill.post",
			ObjectType: "purchase_bill", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "total": t.Total.String(), "withholding": p.withholding.String(), "date": b.BillDate.String()},
		}); err != nil {
			return err
		}
		out, err = s.Bill(ctx, companyID, id)
		return err
	})
	return out, err
}

// debitsByAccount menjumlahkan DPP baris per akun; withVAT menambahkan PPN baris ke akunnya
// (untuk PT non-PKP yang tidak bisa mengkreditkan PPN masukan).
func debitsByAccount(lines []trade.Line, withVAT bool) []ledger.LineDraft {
	var out []ledger.LineDraft
	index := map[uuid.UUID]int{}
	for _, l := range lines {
		amount := l.Net
		if withVAT {
			amount = amount.Add(l.VAT)
		}
		if amount.IsZero() {
			continue
		}
		i, ok := index[l.AccountID]
		if !ok {
			index[l.AccountID] = len(out)
			out = append(out, ledger.LineDraft{AccountID: l.AccountID, Memo: l.Description})
			i = len(out) - 1
		}
		out[i].Debit = out[i].Debit.Add(amount)
	}
	return out
}

func (s *service) VoidBill(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (BillDetail, error) {
	var out BillDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		b, err := q.GetBillForUpdate(ctx, store.GetBillForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("bill.not_found", "Tagihan tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		switch b.Status {
		case "draft":
			return httpx.Conflict("bill.is_draft", "Draf belum diposting; hapus saja drafnya.")
		case "void":
			return httpx.Conflict("bill.already_void", fmt.Sprintf("Tagihan %s sudah di-void.", deref(b.Number)))
		}
		pays, err := q.BillPayments(ctx, store.BillPaymentsParams{CompanyID: companyID, BillID: id})
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
			return httpx.Unprocessable("bill.has_payments", fmt.Sprintf(
				"Tagihan %s sudah dibayar lewat %s; void pembayarannya dulu.", deref(b.Number), strings.Join(active, ", ")))
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		v, err := s.Ledger.VoidSource(ctx, companyID, *b.JournalEntryID, SourceBill, date, reason, actor)
		if err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if n, err := q.MarkBillVoid(ctx, store.MarkBillVoidParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || n != 1 {
			return errOr(err, "tagihan gagal ditandai void")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "purchase.bill.void",
			ObjectType: "purchase_bill", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": deref(b.Number), "status": "posted"},
			After:  map[string]any{"number": deref(b.Number), "status": "void", "reason": reason, "reversal": v.Reversal.Number},
		}); err != nil {
			return err
		}
		out, err = s.Bill(ctx, companyID, id)
		return err
	})
	return out, err
}

func state(status string, payable, paid money.Amount) string {
	switch {
	case status != "posted":
		return status
	case paid.Cmp(payable) >= 0:
		return "paid"
	default:
		return "open"
	}
}

func summary(id uuid.UUID, number *string, status string, supplier uuid.UUID, supplierNo string, date, due civil.Date,
	memo string, total, payable, paid money.Amount, names map[uuid.UUID]string) Bill {
	b := Bill{
		ID: id, Number: number, Status: status, State: state(status, payable, paid), SupplierID: supplier,
		SupplierName: names[supplier], SupplierInvoiceNo: supplierNo, Date: date, DueDate: due, Memo: memo,
		Total: total, Payable: payable, Paid: paid,
	}
	if status == "posted" {
		b.Outstanding = payable.Sub(paid)
		if b.Outstanding.Sign() > 0 {
			if late := civil.Today(civil.Jakarta).DaysSince(due); late > 0 {
				b.DaysOverdue = late
			}
		}
	}
	return b
}

func (s *service) Bills(ctx context.Context, companyID uuid.UUID, f BillFilter) ([]Bill, error) {
	p := store.ListBillsParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, SupplierID: f.SupplierID}
	if f.State != "" {
		p.State = &f.State
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		p.Search = &q
	}
	rows, err := s.q(ctx).ListBills(ctx, p)
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
	out := make([]Bill, len(rows))
	for i, r := range rows {
		out[i] = summary(r.ID, r.Number, r.Status, r.SupplierID, r.SupplierInvoiceNo, r.BillDate, r.DueDate, r.Memo, r.Total, r.Payable, r.Paid, names)
	}
	return out, nil
}

func (s *service) OpenBills(ctx context.Context, companyID uuid.UUID, supplierID *uuid.UUID) ([]Bill, error) {
	rows, err := s.q(ctx).OpenBills(ctx, store.OpenBillsParams{CompanyID: companyID, SupplierID: supplierID})
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
	out := make([]Bill, len(rows))
	for i, r := range rows {
		out[i] = summary(r.ID, r.Number, r.Status, r.SupplierID, r.SupplierInvoiceNo, r.BillDate, r.DueDate, r.Memo, r.Total, r.Payable, r.Paid, names)
	}
	return out, nil
}

func (s *service) Bill(ctx context.Context, companyID, id uuid.UUID) (BillDetail, error) {
	q := s.q(ctx)
	r, err := q.GetBill(ctx, store.GetBillParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return BillDetail{}, httpx.NotFound("bill.not_found", "Tagihan tidak ditemukan.")
	}
	if err != nil {
		return BillDetail{}, err
	}
	names, err := s.Core.ContactNames(ctx, companyID, []uuid.UUID{r.SupplierID})
	if err != nil {
		return BillDetail{}, err
	}
	pays, err := q.BillPayments(ctx, store.BillPaymentsParams{CompanyID: companyID, BillID: id})
	if err != nil {
		return BillDetail{}, err
	}
	var paid money.Amount
	payments := make([]BillPayment, 0, len(pays))
	for _, p := range pays {
		if p.Status == "posted" {
			paid = paid.Add(p.Amount)
		}
		payments = append(payments, BillPayment{PaymentID: p.ID, Number: p.Number, Date: p.PaymentDate, Status: p.Status, Amount: p.Amount})
	}
	dups, err := q.DuplicateSupplierInvoice(ctx, store.DuplicateSupplierInvoiceParams{
		CompanyID: companyID, SupplierID: r.SupplierID, ID: id, SupplierInvoiceNo: r.SupplierInvoiceNo,
	})
	if err != nil {
		return BillDetail{}, err
	}
	out := BillDetail{
		Bill: summary(r.ID, r.Number, r.Status, r.SupplierID, r.SupplierInvoiceNo, r.BillDate, r.DueDate, r.Memo, r.Total, r.Payable, paid, names),
		Totals: trade.Totals{
			Subtotal: r.Subtotal, Discount: r.Discount, DPP: r.Dpp, OtherDPP: r.OtherDpp, VAT: r.Vat, Total: r.Total,
		},
		WithholdingCode: r.WithholdingCode, Withholding: r.Withholding, Payments: payments, Duplicates: []Ref{},
		JournalEntryID: r.JournalEntryID, CreatedByID: r.CreatedBy, CreatedBy: r.CreatedByName,
		PostedBy: r.PostedByName, PostedAt: r.PostedAt, VoidedBy: r.VoidedByName, VoidedAt: r.VoidedAt,
		VoidReason: r.VoidReason, Version: int(r.Version),
	}
	for _, d := range dups {
		out.Duplicates = append(out.Duplicates, Ref{ID: d.ID, Number: d.Number, Status: d.Status})
	}
	lines, err := q.BillLines(ctx, store.BillLinesParams{CompanyID: companyID, BillID: id})
	if err != nil {
		return BillDetail{}, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return BillDetail{}, err
	}
	for _, l := range lines {
		a := accounts[l.AccountID]
		out.Lines = append(out.Lines, BillLine{
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
	rows, err := s.q(ctx).AgingBills(ctx, store.AgingBillsParams{CompanyID: companyID, AsOf: asOf})
	if err != nil {
		return trade.Aging{}, err
	}
	items := make([]trade.AgingItem, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out := r.Payable.Sub(r.Paid)
		if out.Sign() <= 0 {
			continue
		}
		items = append(items, trade.AgingItem{ContactID: r.SupplierID, Due: r.DueDate, Outstanding: out})
		ids = append(ids, r.SupplierID)
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

func fieldErr(err error, field string) error {
	var p *httpx.Problem
	if errors.As(err, &p) && len(p.Fields) == 0 {
		p.Fields = map[string]string{field: p.Detail}
	}
	return err
}

// Withheld adalah PPh yang kita potong dari pemasok lewat tagihan.
type Withheld struct {
	BillID            uuid.UUID    `json:"billId"`
	BillNumber        string       `json:"billNumber"`
	Date              civil.Date   `json:"date"`
	ContactID         uuid.UUID    `json:"contactId"`
	ContactName       string       `json:"contactName"`
	ContactNPWP       string       `json:"contactNpwp"`
	SupplierInvoiceNo string       `json:"supplierInvoiceNo"`
	DPP               money.Amount `json:"dpp"`
	Code              string       `json:"code"`
	Amount            money.Amount `json:"amount"`
}

func (s *service) VATByMonth(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]trade.MonthVAT, error) {
	rows, err := s.q(ctx).VATByMonth(ctx, store.VATByMonthParams{CompanyID: companyID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	out := make([]trade.MonthVAT, len(rows))
	for i, r := range rows {
		out[i] = trade.MonthVAT{Month: r.Month, Documents: r.Bills, DPP: r.Dpp, VAT: r.Vat}
	}
	return out, nil
}

func (s *service) Withheld(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]Withheld, error) {
	rows, err := s.q(ctx).WithheldFromSuppliers(ctx, store.WithheldFromSuppliersParams{CompanyID: companyID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	out := make([]Withheld, len(rows))
	for i, r := range rows {
		c, err := s.Core.Contact(ctx, companyID, r.SupplierID)
		if err != nil {
			return nil, err
		}
		out[i] = Withheld{
			BillID: r.ID, BillNumber: deref(r.Number), Date: r.BillDate, ContactID: r.SupplierID, ContactName: c.Name,
			ContactNPWP: c.NPWP, SupplierInvoiceNo: r.SupplierInvoiceNo, DPP: r.Dpp, Code: deref(r.WithholdingCode),
			Amount: r.Withholding,
		}
	}
	return out, nil
}

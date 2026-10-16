package consol

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"

	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
)

// Pelunasan dan pembayaran antar-PT (ME-03): pelunasan faktur antar-PT di PT penjual otomatis
// membayar tagihan cerminannya di PT pembeli, dan sebaliknya. Uang keluar/masuk di PT pasangan
// memakai rekening kas antar-PT yang diisi di kontak yang mewakili PT ini. Pelunasan dengan PPh
// dipotong atau uang muka tidak dicerminkan otomatis karena dokumen cerminannya tidak memuat PPh.

const (
	docReceipt = sales.SourceReceipt    // sales.receipt
	docPayment = purchase.SourcePayment // purchase.payment
)

// ReceiptPosted: pelunasan di PT A atas faktur cerminan → pembayaran tagihan pasangannya di PT B.
func (s *service) ReceiptPosted(ctx context.Context, companyID, receiptID uuid.UUID, actor audit.Actor) error {
	if mirroring(ctx) {
		return nil
	}
	rc, err := s.Sales.Receipt(ctx, companyID, receiptID)
	if err != nil || rc.Kind != sales.ReceiptKindSettle {
		return err
	}
	var target uuid.UUID
	var allocs []purchase.AllocationInput
	for _, a := range rc.Allocations {
		l, err := s.Link(ctx, companyID, docInvoice, a.InvoiceID)
		if err != nil {
			return err
		}
		if l == nil {
			continue
		}
		if a.Withholding.Sign() > 0 {
			return httpx.Unprocessable("intercompany.withholding", fmt.Sprintf(
				"Faktur %s adalah transaksi antar-PT; pelunasan dengan PPh dipotong belum bisa dicerminkan. Catat tanpa PPh, atau lunasi dari tagihan di %s.", a.InvoiceNumber, l.CompanyName))
		}
		target = l.CompanyID
		allocs = append(allocs, purchase.AllocationInput{BillID: l.DocID, Amount: a.Amount})
	}
	if len(allocs) == 0 {
		return nil
	}
	if rc.Advance.Sign() > 0 || rc.FromAdvance.Sign() > 0 {
		return httpx.Unprocessable("intercompany.advance", "Uang muka antar-PT belum bisa dicerminkan; pisahkan pelunasan faktur antar-PT dari uang muka.")
	}
	contact, cash, err := s.settlementContact(ctx, target, companyID, true)
	if err != nil {
		return err
	}
	p, err := s.Purchase.CreatePayment(withMirroring(ctx), target, purchase.PaymentInput{
		SupplierID: contact, Date: rc.Date, CashAccountID: cash, Memo: settleMemo(rc.Number, s.companyName(ctx, companyID)), Allocations: allocs,
	}, actor)
	if err != nil {
		return wrap(err, s.companyName(ctx, target))
	}
	return s.link(ctx, companyID, docReceipt, receiptID, target, docPayment, p.ID, actor)
}

// PaymentPosted: pembayaran di PT B atas tagihan cerminan → pelunasan faktur pasangannya di PT A.
func (s *service) PaymentPosted(ctx context.Context, companyID, paymentID uuid.UUID, actor audit.Actor) error {
	if mirroring(ctx) {
		return nil
	}
	p, err := s.Purchase.Payment(ctx, companyID, paymentID)
	if err != nil || p.Kind != purchase.PaymentKindPay {
		return err
	}
	var target uuid.UUID
	var allocs []sales.AllocationInput
	for _, a := range p.Allocations {
		l, err := s.Link(ctx, companyID, docBill, a.BillID)
		if err != nil {
			return err
		}
		if l == nil {
			continue
		}
		target = l.CompanyID
		allocs = append(allocs, sales.AllocationInput{InvoiceID: l.DocID, Amount: a.Amount})
	}
	if len(allocs) == 0 {
		return nil
	}
	if p.Advance.Sign() > 0 || p.FromAdvance.Sign() > 0 {
		return httpx.Unprocessable("intercompany.advance", "Uang muka antar-PT belum bisa dicerminkan; pisahkan pembayaran tagihan antar-PT dari uang muka.")
	}
	contact, cash, err := s.settlementContact(ctx, target, companyID, false)
	if err != nil {
		return err
	}
	rc, err := s.Sales.CreateReceipt(withMirroring(ctx), target, sales.ReceiptInput{
		CustomerID: contact, Date: p.Date, CashAccountID: cash, Memo: settleMemo(p.Number, s.companyName(ctx, companyID)), Allocations: allocs,
	}, actor)
	if err != nil {
		return wrap(err, s.companyName(ctx, target))
	}
	return s.link(ctx, companyID, docPayment, paymentID, target, docReceipt, rc.ID, actor)
}

// settlementContact mengembalikan kontak di PT target yang mewakili PT sumber beserta rekening
// kas antar-PT-nya.
func (s *service) settlementContact(ctx context.Context, target, source uuid.UUID, supplier bool) (uuid.UUID, uuid.UUID, error) {
	c, _, err := s.counterpart(ctx, target, source, supplier)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if c.ICCashAccountID == nil {
		return uuid.Nil, uuid.Nil, httpx.Unprocessable("intercompany.no_cash_account", fmt.Sprintf(
			"Isi rekening kas pelunasan antar-PT di kontak %s pada %s supaya pelunasannya bisa dicerminkan.", c.Name, s.companyName(ctx, target)))
	}
	return c.ID, *c.ICCashAccountID, nil
}

func settleMemo(number, company string) string {
	m := "Cerminan " + number + " dari " + company
	if utf8.RuneCountInString(m) > 500 {
		m = string([]rune(m)[:500])
	}
	return m
}

func (s *service) SettlementVoided(ctx context.Context, companyID uuid.UUID, docType string, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
	return s.voidPair(ctx, companyID, docType, id, date, reason, actor)
}

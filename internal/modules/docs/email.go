package docs

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/mail"
)

type EmailInput struct {
	To      []string `json:"to"`
	Cc      []string `json:"cc"`
	Subject string   `json:"subject"`
	Message string   `json:"message"`
}

// InvoiceEmailDraft mengisi penerima, subjek, dan pesan bawaan dari data pelanggan.
func (s *Service) InvoiceEmailDraft(ctx context.Context, companyID, invoiceID uuid.UUID) (EmailInput, error) {
	inv, err := s.Sales.Invoice(ctx, companyID, invoiceID)
	if err != nil {
		return EmailInput{}, err
	}
	co, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return EmailInput{}, err
	}
	cust, err := s.Core.Contact(ctx, companyID, inv.CustomerID)
	if err != nil {
		return EmailInput{}, err
	}
	number := "(draf)"
	if inv.Number != nil {
		number = *inv.Number
	}
	to := []string{}
	if cust.Email != "" {
		to = append(to, cust.Email)
	}
	amount := inv.Receivable
	if !inv.Paid.IsZero() || !inv.Credited.IsZero() {
		amount = inv.Outstanding
	}
	return EmailInput{
		To: to, Cc: []string{},
		Subject: fmt.Sprintf("Faktur %s dari %s", number, co.Name),
		Message: fmt.Sprintf("Yth. %s,\n\nTerlampir faktur %s tanggal %s sebesar %s, jatuh tempo %s.\n\nTerima kasih atas kerja samanya.\n\nHormat kami,\n%s",
			cust.Name, number, longDate(inv.Date), amount.IDR(), longDate(inv.DueDate), co.Name),
	}, nil
}

// EmailInvoice mengantrekan email berisi PDF faktur yang sudah diposting.
func (s *Service) EmailInvoice(ctx context.Context, companyID, invoiceID uuid.UUID, in EmailInput, actor audit.Actor) error {
	set, err := s.Mail.Settings(ctx)
	if err != nil {
		return err
	}
	if !set.Enabled {
		return mail.ErrDisabled
	}
	inv, err := s.Sales.Invoice(ctx, companyID, invoiceID)
	if err != nil {
		return err
	}
	if inv.Status != "posted" || inv.Number == nil {
		return httpx.Conflict("invoice.not_posted", "Hanya faktur yang sudah diposting yang bisa dikirim ke pelanggan.")
	}
	if strings.TrimSpace(in.Subject) == "" {
		return httpx.Unprocessable("validation", "Subjek wajib diisi.").WithFields(map[string]string{"subject": "Subjek wajib diisi."})
	}
	body, name, err := s.InvoicePDF(ctx, companyID, invoiceID)
	if err != nil {
		return err
	}
	if _, err := s.Mail.Enqueue(ctx, mail.Queued{
		CompanyID: &companyID, Kind: "invoice", CreatedBy: actor.UserRef(),
		Message: mail.Message{To: in.To, Cc: in.Cc, Subject: strings.TrimSpace(in.Subject), Text: in.Message,
			AttachmentName: name, AttachmentMime: "application/pdf", Attachment: body},
	}); err != nil {
		return err
	}
	_, err = audit.Write(ctx, db.Conn(ctx, s.Mail.Pool), audit.Entry{
		UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.invoice.email", ObjectType: "sales_invoice", ObjectID: invoiceID.String(), IP: actor.IP,
		After: map[string]any{"number": *inv.Number, "to": in.To, "cc": in.Cc},
	})
	return err
}

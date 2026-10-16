package docs_test

import (
	"bytes"
	"errors"
	"testing"

	"lajur/internal/modules/docs"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/mail"
	"lajur/internal/platform/money"
	"lajur/internal/platform/secret"
)

func TestInvoicePDFAndEmail(t *testing.T) {
	e := fixture.New(t)
	box, _ := secret.New([]byte("kunci-uji-lajur-tiga-puluh-dua-b"))
	m := mail.New(e.Pool, box, nil)
	s := docs.New(docs.Deps{Core: e.Core, Sales: e.Sales, Cash: e.Cash, Mail: m})

	line := trade.LineInput{Description: "Jasa konsultasi pajak bulanan untuk masa Oktober 2026 termasuk pelaporan SPT Masa PPN",
		AccountID: e.Acc["4-1001"], Qty: money.MustParse("1.5"), UnitPrice: money.MustParse("8000000"), DiscountPct: money.MustParse("0"), TaxCode: "PPN"}
	d, err := e.Sales.CreateInvoice(e.Ctx, e.Company, sales.InvoiceInput{CustomerID: e.Customer, Date: fixture.Day("2026-10-14"), Memo: "Kontrak 12/2026", Lines: []trade.LineInput{line}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}

	// Draf: PDF boleh (bertanda DRAF), email tidak.
	draft, name, err := s.InvoicePDF(e.Ctx, e.Company, d.ID)
	if err != nil || !bytes.HasPrefix(draft, []byte("%PDF-1.4")) || name != "draf-faktur.pdf" || !bytes.Contains(draft, []byte("(DRAF)")) {
		t.Fatalf("PDF draf: %v %q", err, name)
	}
	set, _ := m.Settings(e.Ctx)
	set.Enabled, set.Host, set.Port, set.Security, set.From = true, "smtp.contoh.test", 587, "starttls", "faktur@pt.test"
	if _, err := m.SaveSettings(e.Ctx, set); err != nil {
		t.Fatal(err)
	}
	var p *httpx.Problem
	if err := s.EmailInvoice(e.Ctx, e.Company, d.ID, docs.EmailInput{To: []string{"a@b.test"}, Subject: "x"}, e.Actor); !errors.As(err, &p) || p.Code != "invoice.not_posted" {
		t.Fatalf("email draf = %v", err)
	}

	inv, err := e.Sales.PostInvoice(e.Ctx, e.Company, d.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	body, name, err := s.InvoicePDF(e.Ctx, e.Company, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"(FAKTUR PENJUALAN)", "(" + *inv.Number + ")", "(PT Pelanggan Uji)", "(BCA 1234567890 a.n. ", "(Rp 13.320.000)", "(12.000.000)", "(1,5)"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("PDF tidak memuat %s", want)
		}
	}
	if bytes.Contains(body, []byte("(DRAF)")) || name == "draf-faktur.pdf" {
		t.Fatal("faktur terposting masih bertanda draf")
	}

	draftMail, err := s.InvoiceEmailDraft(e.Ctx, e.Company, d.ID)
	if err != nil || draftMail.Subject == "" {
		t.Fatalf("draf email = %+v %v", draftMail, err)
	}
	if err := s.EmailInvoice(e.Ctx, e.Company, d.ID, docs.EmailInput{To: []string{"budi@pelanggan.test"}, Subject: draftMail.Subject, Message: draftMail.Message}, e.Actor); err != nil {
		t.Fatal(err)
	}
	out, _ := m.Outbox(e.Ctx)
	if len(out) != 1 || out[0].Kind != "invoice" || out[0].Attachment == nil || *out[0].Attachment != name {
		t.Fatalf("outbox = %+v", out)
	}
}

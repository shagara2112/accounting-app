package core

import (
	"context"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"lajur/internal/modules/core/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Contact adalah pelanggan, pemasok, atau keduanya (AR-01, AP-01).
type Contact struct {
	ID               uuid.UUID `json:"id"`
	Name             string    `json:"name"`
	IsCustomer       bool      `json:"isCustomer"`
	IsSupplier       bool      `json:"isSupplier"`
	NPWP             string    `json:"npwp"`
	NIK              string    `json:"nik"`
	NITKU            string    `json:"nitku"`
	PKP              bool      `json:"pkp"`
	Address          string    `json:"address"`
	Email            string    `json:"email"`
	Phone            string    `json:"phone"`
	PaymentTermsDays int       `json:"paymentTermsDays"`
	BankName         string    `json:"bankName"`
	BankAccount      string    `json:"bankAccount"`
	Active           bool      `json:"active"`
	Version          int       `json:"version"`
	// Kontak yang mewakili PT lain dalam grup (ME-03), beserta akun default dokumen cerminan.
	LinkedCompanyID    *uuid.UUID `json:"linkedCompanyId"`
	LinkedCompanyName  string     `json:"linkedCompanyName,omitempty"`
	ICExpenseAccountID *uuid.UUID `json:"icExpenseAccountId"`
	ICRevenueAccountID *uuid.UUID `json:"icRevenueAccountId"`
	// ICCashAccountID: rekening kas PT ini untuk pelunasan antar-PT yang dicerminkan.
	ICCashAccountID *uuid.UUID `json:"icCashAccountId"`
	// AR-01: limit kredit pelanggan (kosong = tanpa limit) dan nama kontak penagihan.
	CreditLimit    *money.Amount `json:"creditLimit"`
	BillingContact string        `json:"billingContact"`
	// Lawan transaksi luar negeri (PPh 26, TAX-08): kode negara ISO alfa-3 dan TIN di negaranya.
	CountryCode string `json:"countryCode"`
	ForeignTIN  string `json:"foreignTin"`
	// VATCollector: pelanggan pemungut PPN (WAPU) dengan kode transaksi fakturnya, "02" instansi
	// pemerintah atau "03" pemungut lain (mis. BUMN); kosong bila bukan pemungut.
	VATCollector string `json:"vatCollector"`
}

// Foreign menandai kontak luar negeri (bukan Indonesia).
func (c Contact) Foreign() bool { return c.CountryCode != "" && c.CountryCode != "IDN" }

type ContactInput struct {
	Name             string `json:"name"`
	IsCustomer       bool   `json:"isCustomer"`
	IsSupplier       bool   `json:"isSupplier"`
	NPWP             string `json:"npwp"`
	NIK              string `json:"nik"`
	NITKU            string `json:"nitku"`
	PKP              bool   `json:"pkp"`
	Address          string `json:"address"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	PaymentTermsDays int    `json:"paymentTermsDays"`
	BankName         string `json:"bankName"`
	BankAccount      string `json:"bankAccount"`
	Active           bool   `json:"active"`
	Version          int    `json:"version"` // hanya untuk perubahan

	LinkedCompanyID    *uuid.UUID `json:"linkedCompanyId"`
	ICExpenseAccountID *uuid.UUID `json:"icExpenseAccountId"` // tagihan cerminan di PT ini
	ICRevenueAccountID *uuid.UUID `json:"icRevenueAccountId"` // faktur cerminan di PT ini
	ICCashAccountID    *uuid.UUID `json:"icCashAccountId"`    // pelunasan cerminan di PT ini

	CreditLimit    *money.Amount `json:"creditLimit"`
	BillingContact string        `json:"billingContact"`
	CountryCode    string        `json:"countryCode"` // kosong = IDN
	ForeignTIN     string        `json:"foreignTin"`
	VATCollector   string        `json:"vatCollector"` // "", "02", atau "03"
}

type ContactFilter struct {
	Role            string // "customer", "supplier", atau kosong
	Search          string
	IncludeInactive bool
}

var nonDigit = regexp.MustCompile(`[\s.\-]`)

// digits membuang spasi, titik, dan tanda hubung yang biasa dipakai saat menulis NPWP.
func digits(s string) string { return nonDigit.ReplaceAllString(strings.TrimSpace(s), "") }

var allDigits = regexp.MustCompile(`^[0-9]*$`)

var countryCode = regexp.MustCompile(`^[A-Z]{3}$`)

// validate merapikan dan memeriksa isian kontak (TAX-05: NPWP 16 digit atau NIK, NITKU).
func (in *ContactInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	in.NPWP, in.NIK, in.NITKU = digits(in.NPWP), digits(in.NIK), digits(in.NITKU)
	in.Email, in.Phone = strings.TrimSpace(in.Email), strings.TrimSpace(in.Phone)
	in.Address, in.BankName, in.BankAccount = strings.TrimSpace(in.Address), strings.TrimSpace(in.BankName), strings.TrimSpace(in.BankAccount)

	f := map[string]string{}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 200 {
		f["name"] = "Nama wajib diisi (maksimal 200 karakter)."
	}
	if !in.IsCustomer && !in.IsSupplier {
		f["role"] = "Pilih pelanggan, pemasok, atau keduanya."
	}
	if in.NPWP != "" && (len(in.NPWP) != 16 || !allDigits.MatchString(in.NPWP)) {
		f["npwp"] = "NPWP harus 16 digit angka."
	}
	if in.NIK != "" && (len(in.NIK) != 16 || !allDigits.MatchString(in.NIK)) {
		f["nik"] = "NIK harus 16 digit angka."
	}
	if in.NITKU != "" {
		switch {
		case len(in.NITKU) != 22 || !allDigits.MatchString(in.NITKU):
			f["nitku"] = "NITKU harus 22 digit angka (NPWP diikuti 6 digit)."
		case in.NPWP != "" && !strings.HasPrefix(in.NITKU, in.NPWP):
			f["nitku"] = "NITKU harus diawali NPWP kontak ini."
		}
	}
	if in.PKP && in.NPWP == "" {
		f["npwp"] = "Kontak PKP wajib punya NPWP."
	}
	if in.Email != "" {
		if _, err := mail.ParseAddress(in.Email); err != nil {
			f["email"] = "Format email tidak valid."
		}
	}
	if in.PaymentTermsDays < 0 || in.PaymentTermsDays > 365 {
		f["paymentTermsDays"] = "Termin 0–365 hari."
	}
	in.BillingContact = strings.TrimSpace(in.BillingContact)
	if utf8.RuneCountInString(in.BillingContact) > 200 {
		f["billingContact"] = "Kontak penagihan maksimal 200 karakter."
	}
	if in.CreditLimit != nil && in.CreditLimit.Sign() < 0 {
		f["creditLimit"] = "Limit kredit tidak boleh negatif."
	}
	in.CountryCode, in.ForeignTIN = strings.ToUpper(strings.TrimSpace(in.CountryCode)), strings.TrimSpace(in.ForeignTIN)
	if in.CountryCode == "" {
		in.CountryCode = "IDN"
	}
	switch {
	case !countryCode.MatchString(in.CountryCode):
		f["countryCode"] = "Kode negara 3 huruf (ISO), mis. SGP."
	case in.CountryCode == "IDN":
		in.ForeignTIN = ""
	case utf8.RuneCountInString(in.ForeignTIN) > 50:
		f["foreignTin"] = "TIN luar negeri maksimal 50 karakter."
	case in.PKP:
		f["pkp"] = "Kontak luar negeri tidak bisa berstatus PKP."
	}
	switch {
	case !in.IsCustomer:
		in.VATCollector = ""
	case in.VATCollector != "" && in.VATCollector != "02" && in.VATCollector != "03":
		f["vatCollector"] = "Pilih bukan pemungut, 02 (instansi pemerintah), atau 03 (pemungut lain)."
	}
	if len(f) > 0 {
		return httpx.Unprocessable("contact.invalid", firstOf(f)).WithFields(f)
	}
	return nil
}

func firstOf(f map[string]string) string {
	for _, k := range []string{"name", "role", "npwp", "nik", "nitku", "email", "paymentTermsDays", "countryCode", "foreignTin", "pkp", "creditLimit"} {
		if m, ok := f[k]; ok {
			return m
		}
	}
	return "Periksa kembali isian kontak."
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toContact(r store.CoreContact) Contact {
	return Contact{
		ID: r.ID, Name: r.Name, IsCustomer: r.IsCustomer, IsSupplier: r.IsSupplier,
		NPWP: deref(r.Npwp), NIK: deref(r.Nik), NITKU: deref(r.Nitku), PKP: r.Pkp,
		Address: r.Address, Email: r.Email, Phone: r.Phone, PaymentTermsDays: int(r.PaymentTermsDays),
		BankName: r.BankName, BankAccount: r.BankAccount, Active: r.Active, Version: int(r.Version),
		LinkedCompanyID: r.LinkedCompanyID, ICExpenseAccountID: r.IcExpenseAccountID, ICRevenueAccountID: r.IcRevenueAccountID,
		ICCashAccountID: r.IcCashAccountID, CreditLimit: r.CreditLimit, BillingContact: r.BillingContact,
		CountryCode: r.CountryCode, ForeignTIN: r.ForeignTin, VATCollector: r.VatCollector,
	}
}

// withLinkedName melengkapi nama PT grup yang ditautkan.
func (s *service) withLinkedName(ctx context.Context, c Contact) (Contact, error) {
	if c.LinkedCompanyID == nil {
		return c, nil
	}
	co, err := s.Company(ctx, *c.LinkedCompanyID)
	if err != nil {
		return c, err
	}
	c.LinkedCompanyName = co.Name
	return c, nil
}

// Kelas akun yang boleh menjadi akun default dokumen cerminan.
var (
	icExpenseClasses = []int{1, 5, 6, 8}
	icRevenueClasses = []int{4, 7}
)

// checkLink memeriksa tautan PT grup: PT ada, bukan PT ini sendiri, hanya satu kontak per PT
// grup, dan akun default cerminan adalah akun yang cocok di PT ini.
func (s *service) checkLink(ctx context.Context, companyID, id uuid.UUID, in ContactInput) error {
	if in.LinkedCompanyID == nil {
		if in.ICExpenseAccountID != nil || in.ICRevenueAccountID != nil || in.ICCashAccountID != nil {
			return fieldProblem("linkedCompanyId", "Pilih PT grup yang diwakili kontak ini, atau kosongkan akun cerminan.")
		}
		return nil
	}
	if *in.LinkedCompanyID == companyID {
		return fieldProblem("linkedCompanyId", "Kontak tidak bisa ditautkan ke PT ini sendiri.")
	}
	co, err := s.Company(ctx, *in.LinkedCompanyID)
	if err != nil {
		return fieldProblem("linkedCompanyId", "PT grup tidak ditemukan.")
	}
	if co.Kind != "pt" {
		return fieldProblem("linkedCompanyId", co.Name+" bukan PT yang bertransaksi.")
	}
	taken, err := s.q(ctx).LinkedContactTaken(ctx, store.LinkedContactTakenParams{CompanyID: companyID, LinkedCompanyID: in.LinkedCompanyID, ID: id})
	if err != nil {
		return err
	}
	if taken {
		return fieldProblem("linkedCompanyId", "Sudah ada kontak lain yang ditautkan ke "+co.Name+".")
	}
	if s.accountCheck == nil {
		return nil
	}
	if in.ICExpenseAccountID != nil {
		if err := s.accountCheck(ctx, companyID, *in.ICExpenseAccountID, icExpenseClasses); err != nil {
			return fieldProblem("icExpenseAccountId", problemDetail(err))
		}
	}
	if in.ICRevenueAccountID != nil {
		if err := s.accountCheck(ctx, companyID, *in.ICRevenueAccountID, icRevenueClasses); err != nil {
			return fieldProblem("icRevenueAccountId", problemDetail(err))
		}
	}
	return nil
}

func fieldProblem(field, msg string) error {
	return httpx.Unprocessable("contact.invalid", msg).WithFields(map[string]string{field: msg})
}

func problemDetail(err error) string {
	var p *httpx.Problem
	if errors.As(err, &p) {
		return p.Detail
	}
	return err.Error()
}

// LinkedContact mengembalikan kontak di PT ini yang mewakili PT grup lain.
func (s *service) LinkedContact(ctx context.Context, companyID, linkedCompanyID uuid.UUID) (Contact, error) {
	r, err := s.q(ctx).GetLinkedContact(ctx, store.GetLinkedContactParams{CompanyID: companyID, LinkedCompanyID: &linkedCompanyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, httpx.NotFound("contact.not_linked", "Belum ada kontak yang ditautkan ke PT itu.")
	}
	if err != nil {
		return Contact{}, err
	}
	return s.withLinkedName(ctx, toContact(r))
}

func (s *service) Contacts(ctx context.Context, companyID uuid.UUID, f ContactFilter) ([]Contact, error) {
	p := store.ListContactsParams{
		CompanyID: companyID, CustomersOnly: f.Role == "customer", SuppliersOnly: f.Role == "supplier",
		IncludeInactive: f.IncludeInactive,
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		p.Search = &q
	}
	rows, err := s.q(ctx).ListContacts(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]Contact, len(rows))
	for i, r := range rows {
		if out[i], err = s.withLinkedName(ctx, toContact(r)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *service) Contact(ctx context.Context, companyID, id uuid.UUID) (Contact, error) {
	r, err := s.q(ctx).GetContact(ctx, store.GetContactParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, httpx.NotFound("contact.not_found", "Kontak tidak ditemukan di PT ini.")
	}
	if err != nil {
		return Contact{}, err
	}
	return s.withLinkedName(ctx, toContact(r))
}

func (s *service) ContactNames(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	for _, id := range ids {
		if _, ok := out[id]; ok {
			continue
		}
		c, err := s.Contact(ctx, companyID, id)
		if err != nil {
			return nil, err
		}
		out[id] = c.Name
	}
	return out, nil
}

func (s *service) CreateContact(ctx context.Context, companyID uuid.UUID, in ContactInput, actor audit.Actor) (Contact, error) {
	if err := in.validate(); err != nil {
		return Contact{}, err
	}
	var out Contact
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		id := uuid.Must(uuid.NewV7())
		if err := s.checkLink(ctx, companyID, id, in); err != nil {
			return err
		}
		r, err := s.q(ctx).InsertContact(ctx, store.InsertContactParams{
			ID: id, CompanyID: companyID, Name: in.Name, IsCustomer: in.IsCustomer,
			IsSupplier: in.IsSupplier, Npwp: nullable(in.NPWP), Nik: nullable(in.NIK), Nitku: nullable(in.NITKU),
			Pkp: in.PKP, Address: in.Address, Email: in.Email, Phone: in.Phone,
			PaymentTermsDays: int32(in.PaymentTermsDays), BankName: in.BankName, BankAccount: in.BankAccount,
			CreatedBy: actor.UserRef(), LinkedCompanyID: in.LinkedCompanyID, IcExpenseAccountID: in.ICExpenseAccountID,
			IcRevenueAccountID: in.ICRevenueAccountID, CreditLimit: in.CreditLimit, BillingContact: in.BillingContact,
			IcCashAccountID: in.ICCashAccountID, CountryCode: in.CountryCode, ForeignTin: in.ForeignTIN,
			VatCollector: in.VATCollector,
		})
		if err != nil {
			return cashAccountErr(err)
		}
		if out, err = s.withLinkedName(ctx, toContact(r)); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "core.contact.create",
			ObjectType: "contact", ObjectID: r.ID.String(), IP: actor.IP,
			After: map[string]any{"name": r.Name, "customer": r.IsCustomer, "supplier": r.IsSupplier},
		})
		return err
	})
	return out, err
}

func (s *service) UpdateContact(ctx context.Context, companyID, id uuid.UUID, in ContactInput, actor audit.Actor) (Contact, error) {
	if err := in.validate(); err != nil {
		return Contact{}, err
	}
	var out Contact
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		before, err := s.Contact(ctx, companyID, id)
		if err != nil {
			return err
		}
		if err := s.checkLink(ctx, companyID, id, in); err != nil {
			return err
		}
		r, err := s.q(ctx).UpdateContact(ctx, store.UpdateContactParams{
			CompanyID: companyID, ID: id, Version: int32(in.Version), Name: in.Name, IsCustomer: in.IsCustomer,
			IsSupplier: in.IsSupplier, Npwp: nullable(in.NPWP), Nik: nullable(in.NIK), Nitku: nullable(in.NITKU),
			Pkp: in.PKP, Address: in.Address, Email: in.Email, Phone: in.Phone,
			PaymentTermsDays: int32(in.PaymentTermsDays), BankName: in.BankName, BankAccount: in.BankAccount,
			Active: in.Active, LinkedCompanyID: in.LinkedCompanyID, IcExpenseAccountID: in.ICExpenseAccountID,
			IcRevenueAccountID: in.ICRevenueAccountID, CreditLimit: in.CreditLimit, BillingContact: in.BillingContact,
			IcCashAccountID: in.ICCashAccountID, CountryCode: in.CountryCode, ForeignTin: in.ForeignTIN,
			VatCollector: in.VATCollector,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Conflict("conflict.version", "Kontak ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if err != nil {
			return cashAccountErr(err)
		}
		if out, err = s.withLinkedName(ctx, toContact(r)); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "core.contact.update",
			ObjectType: "contact", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"name": before.Name, "npwp": before.NPWP, "active": before.Active, "linkedCompany": before.LinkedCompanyName},
			After:  map[string]any{"name": out.Name, "npwp": out.NPWP, "active": out.Active, "linkedCompany": out.LinkedCompanyName},
		})
		return err
	})
	return out, err
}

// cashAccountErr menerjemahkan rekening kas antar-PT yang tidak terdaftar (foreign key).
func cashAccountErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23503" && strings.Contains(pg.ConstraintName, "ic_cash_account") {
		return fieldProblem("icCashAccountId", "Pilih rekening kas atau bank yang terdaftar di PT ini.")
	}
	return err
}

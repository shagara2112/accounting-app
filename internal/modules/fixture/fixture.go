// Package fixture menyiapkan PT lengkap untuk tes integrasi modul: bagan akun, rekening bank,
// pelanggan, pemasok, dan pengguna. Hanya dipakai tes.
package fixture

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/asset"
	"lajur/internal/modules/budget"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/consol"
	"lajur/internal/modules/core"
	"lajur/internal/modules/imports"
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/panjar"
	"lajur/internal/modules/project"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/report"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/tax/coretax"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/dbtest"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/store"
	"lajur/internal/server"
)

type Env struct {
	T         testing.TB
	Ctx       context.Context
	Pool      *pgxpool.Pool
	Core      core.Service
	Ledger    ledger.Service
	Tax       tax.Service
	Inventory inventory.Service
	Cash      cash.Service
	Sales     sales.Service
	Purchase  purchase.Service
	Coretax   coretax.Service
	Project   project.Service
	Asset     asset.Service
	Budget    budget.Service
	Panjar    panjar.Service
	Report    report.Service
	Consol    consol.Service
	Imports   imports.Service

	Company  uuid.UUID
	Actor    audit.Actor // staf akuntansi
	Other    audit.Actor // pengguna kedua
	Acc      map[string]uuid.UUID
	Bank     uuid.UUID // 1-1111 Bank BCA, terdaftar sebagai rekening bank
	Petty    uuid.UUID // 1-1101 Kas kecil, terdaftar sebagai kas
	Customer uuid.UUID
	Supplier uuid.UUID
}

// New membuat basis data sementara dan satu PT jasa yang siap bertransaksi.
func New(t testing.TB) *Env { return NewWith(t, "jasa") }

// NewWith sama dengan New untuk industri lain: jasa, konstruksi, atau retail.
func NewWith(t testing.TB, industry string) *Env {
	t.Helper()
	pool := dbtest.Pool(t)
	ctx := context.Background()
	e := &Env{T: t, Ctx: ctx, Pool: pool, Acc: map[string]uuid.UUID{}}
	m := server.Modules(pool)
	e.Core, e.Ledger, e.Tax, e.Cash, e.Sales, e.Purchase, e.Coretax = m.Core, m.Ledger, m.Tax, m.Cash, m.Sales, m.Purchase, m.Coretax
	e.Inventory, e.Project, e.Report, e.Consol, e.Imports, e.Asset = m.Inventory, m.Project, m.Report, m.Consol, m.Imports, m.Asset
	e.Budget = m.Budget
	e.Panjar = m.Panjar
	e.Actor = audit.Actor{UserID: e.user("akuntansi@uji.test"), IP: "127.0.0.1"}
	e.Other = audit.Actor{UserID: e.user("akuntansi2@uji.test"), IP: "127.0.0.1"}
	e.setup("PT Uji "+industry, industry)
	return e
}

// Sibling membuat PT lain di basis data dan instalasi yang sama (grup yang sama), dengan
// pengguna dan service yang sama, mis. untuk transaksi antar-PT dan konsolidasi.
func (e *Env) Sibling(name, industry string) *Env {
	e.T.Helper()
	s := *e
	s.Acc = map[string]uuid.UUID{}
	s.setup(name, industry)
	return &s
}

func (e *Env) setup(name, industry string) {
	t, ctx := e.T, e.Ctx
	c, err := e.Core.CreateCompany(ctx, core.NewCompany{Name: name, ShortName: industry, Industry: ptr(industry)})
	must(t, err)
	e.Company = c.ID
	// Staf akuntansi uji menjadi Manager di setiap PT, supaya punya izin konsolidasi.
	must(t, e.Core.Assign(ctx, e.Actor.UserID, c.ID, "manager"))

	_, err = e.Ledger.ApplyTemplate(ctx, e.Company, industry, e.Actor)
	must(t, err)
	list, err := e.Ledger.Accounts(ctx, e.Company)
	must(t, err)
	for _, a := range list {
		e.Acc[a.Code] = a.ID
	}
	e.Bank, e.Petty = e.Acc["1-1111"], e.Acc["1-1101"]
	_, err = e.Cash.RegisterAccount(ctx, e.Company, cash.AccountInput{AccountID: e.Bank, Kind: "bank", BankName: "BCA", AccountNumber: "1234567890", Active: true}, e.Actor)
	must(t, err)
	_, err = e.Cash.RegisterAccount(ctx, e.Company, cash.AccountInput{AccountID: e.Petty, Kind: "kas", Active: true}, e.Actor)
	must(t, err)

	cust, err := e.Core.CreateContact(ctx, e.Company, core.ContactInput{
		Name: "PT Pelanggan Uji", IsCustomer: true, NPWP: "0123.4567.8901.2345", PKP: true, PaymentTermsDays: 30,
	}, e.Actor)
	must(t, err)
	e.Customer = cust.ID
	sup, err := e.Core.CreateContact(ctx, e.Company, core.ContactInput{
		Name: "CV Pemasok Uji", IsSupplier: true, NPWP: "9876543210987654", PKP: true, PaymentTermsDays: 14,
	}, e.Actor)
	must(t, err)
	e.Supplier = sup.ID
}

func (e *Env) user(email string) uuid.UUID {
	u, err := store.New(e.Pool).CreateUser(e.Ctx, store.CreateUserParams{
		ID: uuid.Must(uuid.NewV7()), Email: email, Name: email, PasswordHash: "x",
	})
	must(e.T, err)
	return u.ID
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

// Day membaca tanggal yyyy-mm-dd untuk tes.
func Day(s string) civil.Date {
	d, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// Rp membuat nominal rupiah bulat.
func Rp(n int64) money.Amount { return money.FromInt(n) }

// Code mengembalikan kode problem dari error, atau teks error bila bukan problem.
func Code(err error) string {
	var p *httpx.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	if err == nil {
		return ""
	}
	return "error: " + err.Error()
}

// Fields mengembalikan pesan per field dari problem.
func Fields(err error) map[string]string {
	var p *httpx.Problem
	if errors.As(err, &p) {
		return p.Fields
	}
	return nil
}

// Net mengembalikan saldo debit − kredit akun berkode code dari buku besar per tanggal to.
func (e *Env) Net(code string, to civil.Date) money.Amount {
	e.T.Helper()
	l, err := e.Ledger.AccountLedger(e.Ctx, e.Company, e.Acc[code], civil.New(2000, 1, 1), to)
	must(e.T, err)
	if l.Normal == ledger.Credit {
		return l.Closing.Neg()
	}
	return l.Closing
}

// VerifyLedger memastikan saldo agregat sama dengan baris jurnal.
func (e *Env) VerifyLedger() {
	e.T.Helper()
	n, err := e.Ledger.VerifyBalances(e.Ctx, e.Company)
	must(e.T, err)
	if n != 0 {
		e.T.Fatalf("%d sel saldo tidak cocok dengan baris jurnal", n)
	}
}

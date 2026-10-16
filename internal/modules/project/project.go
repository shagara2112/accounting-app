// Package project adalah modul proyek (IND-01–IND-03, IND-05): data proyek dengan nilai kontrak,
// uang muka, retensi, dan tarif PPh final 4(2); RAB per pos biaya; serta laba per proyek dan
// biaya aktual vs RAB. Proyek adalah dimensi baris jurnal, sehingga angka laporannya dibaca
// dari saldo per proyek di buku besar, bukan dihitung ulang dari dokumen.
//
// Faktur uang muka, termin progres, dan pelepasan retensi (AR-08) dihitung di sini lalu
// dibuat sebagai draf faktur lewat modul penjualan.
package project

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/modules/project/internal/store"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/tax"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const (
	StatusRunning   = "berjalan"
	StatusDone      = "selesai"
	StatusCancelled = "batal"
)

type Project struct {
	ID               uuid.UUID    `json:"id"`
	Code             string       `json:"code"`
	Name             string       `json:"name"`
	CustomerID       *uuid.UUID   `json:"customerId"`
	CustomerName     string       `json:"customerName"`
	ContractValue    money.Amount `json:"contractValue"` // sebelum PPN
	DPPct            money.Amount `json:"dpPct"`
	RetentionPct     money.Amount `json:"retentionPct"`
	PPhCode          *string      `json:"pphCode"`
	RevenueAccountID *uuid.UUID   `json:"revenueAccountId"`
	StartDate        *civil.Date  `json:"startDate"`
	EndDate          *civil.Date  `json:"endDate"`
	Status           string       `json:"status"`
	Notes            string       `json:"notes"`
	Version          int          `json:"version"`
}

type ProjectInput struct {
	Code             string       `json:"code"`
	Name             string       `json:"name"`
	CustomerID       *uuid.UUID   `json:"customerId"`
	ContractValue    money.Amount `json:"contractValue"`
	DPPct            money.Amount `json:"dpPct"`
	RetentionPct     money.Amount `json:"retentionPct"`
	PPhCode          *string      `json:"pphCode"`
	RevenueAccountID *uuid.UUID   `json:"revenueAccountId"`
	StartDate        *civil.Date  `json:"startDate"`
	EndDate          *civil.Date  `json:"endDate"`
	Status           string       `json:"status"`
	Notes            string       `json:"notes"`
	Version          int          `json:"version"`
}

// Summary adalah proyek beserta angka utamanya sepanjang umur proyek.
type Summary struct {
	Project
	Budget  money.Amount  `json:"budget"`
	Revenue money.Amount  `json:"revenue"`
	Cost    money.Amount  `json:"cost"`
	Profit  money.Amount  `json:"profit"`
	CostPct *money.Amount `json:"costPct,omitempty"` // biaya aktual ÷ RAB × 100
}

type BudgetLine struct {
	AccountID   uuid.UUID    `json:"accountId"`
	AccountCode string       `json:"accountCode"`
	AccountName string       `json:"accountName"`
	Description string       `json:"description"`
	Amount      money.Amount `json:"amount"`
}

type BudgetLineInput struct {
	AccountID   uuid.UUID    `json:"accountId"`
	Description string       `json:"description"`
	Amount      money.Amount `json:"amount"`
}

type Detail struct {
	Summary
	BudgetLines []BudgetLine    `json:"budgetLines"`
	Billing     Billing         `json:"billing"`
	Invoices    []sales.Invoice `json:"invoices"`
}

type Filter struct {
	Status string
	Search string
}

type Service interface {
	Projects(ctx context.Context, companyID uuid.UUID, f Filter) ([]Summary, error)
	Project(ctx context.Context, companyID, id uuid.UUID) (Detail, error)
	CreateProject(ctx context.Context, companyID uuid.UUID, in ProjectInput, actor audit.Actor) (Detail, error)
	UpdateProject(ctx context.Context, companyID, id uuid.UUID, in ProjectInput, actor audit.Actor) (Detail, error)
	SetBudget(ctx context.Context, companyID, id uuid.UUID, lines []BudgetLineInput, actor audit.Actor) (Detail, error)
	Report(ctx context.Context, companyID, id uuid.UUID, from, to civil.Date) (Report, error)
	Lines(ctx context.Context, companyID, id uuid.UUID, accountID *uuid.UUID, from, to civil.Date) ([]ledger.ProjectLine, bool, error)
	CreateBilling(ctx context.Context, companyID, id uuid.UUID, in BillingInput, actor audit.Actor) (sales.InvoiceDetail, error)
}

type Deps struct {
	Pool   *pgxpool.Pool
	Ledger ledger.Service
	Core   core.Service
	Tax    tax.Service
	Sales  sales.Service
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

// Check adalah pemeriksaan proyek untuk buku besar dan dokumen modul lain: proyek harus ada
// di PT ini dan belum dibatalkan.
func Check(pool *pgxpool.Pool) ledger.ProjectCheck {
	return func(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) error {
		rows, err := store.New(db.Conn(ctx, pool)).ProjectsByIDs(ctx, store.ProjectsByIDsParams{CompanyID: companyID, Ids: ids})
		if err != nil {
			return err
		}
		found := map[uuid.UUID]store.ProjectsByIDsRow{}
		for _, r := range rows {
			found[r.ID] = r
		}
		for _, id := range ids {
			p, ok := found[id]
			switch {
			case !ok:
				return httpx.Unprocessable("project.not_found", "Proyek tidak ditemukan di PT ini.")
			case p.Status == StatusCancelled:
				return httpx.Unprocessable("project.cancelled", fmt.Sprintf(
					"Proyek %s %s sudah dibatalkan; transaksi baru tidak bisa memakai proyek ini.", p.Code, p.Name))
			}
		}
		return nil
	}
}

var codePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,29}$`)

var hundred = money.FromInt(100)

func validPct(p money.Amount) bool {
	return p.Sign() >= 0 && p.Cmp(hundred) <= 0 && p.MulFrac(1, 1, 2, money.Down).Equal(p)
}

func (s *service) validate(ctx context.Context, companyID, id uuid.UUID, in *ProjectInput) error {
	in.Code, in.Name, in.Notes = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name), strings.TrimSpace(in.Notes)
	if in.PPhCode != nil && strings.TrimSpace(*in.PPhCode) == "" {
		in.PPhCode = nil
	}
	if in.Status == "" {
		in.Status = StatusRunning
	}
	f := map[string]string{}
	if !codePattern.MatchString(in.Code) {
		f["code"] = "Kode 1–30 karakter: huruf, angka, titik, garis miring, atau tanda hubung."
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 200 {
		f["name"] = "Nama proyek wajib diisi (maksimal 200 karakter)."
	}
	if in.ContractValue.Sign() < 0 {
		f["contractValue"] = "Nilai kontrak tidak boleh negatif."
	}
	if !validPct(in.DPPct) {
		f["dpPct"] = "Uang muka 0–100% dengan maksimal 2 desimal."
	}
	if !validPct(in.RetentionPct) {
		f["retentionPct"] = "Retensi 0–100% dengan maksimal 2 desimal."
	}
	if validPct(in.DPPct) && validPct(in.RetentionPct) && in.DPPct.Add(in.RetentionPct).Cmp(hundred) > 0 {
		f["retentionPct"] = "Uang muka ditambah retensi tidak boleh lebih dari 100%."
	}
	if in.StartDate != nil && in.EndDate != nil && in.EndDate.Before(*in.StartDate) {
		f["endDate"] = "Tanggal selesai tidak boleh sebelum tanggal mulai."
	}
	switch in.Status {
	case StatusRunning, StatusDone, StatusCancelled:
	default:
		f["status"] = "Status harus berjalan, selesai, atau batal."
	}
	if utf8.RuneCountInString(in.Notes) > 1000 {
		f["notes"] = "Catatan maksimal 1.000 karakter."
	}
	if len(f) > 0 {
		return httpx.Unprocessable("project.invalid", firstOf(f)).WithFields(f)
	}

	taken, err := s.q(ctx).ProjectCodeTaken(ctx, store.ProjectCodeTakenParams{CompanyID: companyID, Code: in.Code, ID: id})
	if err != nil {
		return err
	}
	if taken {
		msg := fmt.Sprintf("Kode %s sudah dipakai proyek lain.", in.Code)
		return httpx.Conflict("project.code_taken", msg).WithFields(map[string]string{"code": msg})
	}
	if in.CustomerID != nil {
		c, err := s.Core.Contact(ctx, companyID, *in.CustomerID)
		if err != nil {
			return fieldErr(err, "customerId")
		}
		if !c.IsCustomer {
			return field("customerId", fmt.Sprintf("%s bukan pelanggan.", c.Name))
		}
	}
	if in.PPhCode != nil {
		table, err := s.Tax.Table(ctx)
		if err != nil {
			return err
		}
		day := civil.Today(civil.Jakarta)
		if in.StartDate != nil {
			day = *in.StartDate
		}
		code, err := table.Find(*in.PPhCode, day)
		if err != nil {
			return fieldErr(err, "pphCode")
		}
		if code.Kind != tax.KindPPh {
			return field("pphCode", fmt.Sprintf("%s bukan kode PPh.", code.Code))
		}
	}
	if in.RevenueAccountID != nil {
		accounts, err := s.accounts(ctx, companyID)
		if err != nil {
			return err
		}
		a, ok := accounts[*in.RevenueAccountID]
		switch {
		case !ok:
			return field("revenueAccountId", "Akun tidak ditemukan di PT ini.")
		case !a.Postable || !a.Active:
			return field("revenueAccountId", fmt.Sprintf("%s %s bukan akun detail yang aktif.", a.Code, a.Name))
		case a.Class != 4 && a.Class != 7:
			return field("revenueAccountId", fmt.Sprintf("%s %s bukan akun pendapatan.", a.Code, a.Name))
		}
	}
	return nil
}

func firstOf(f map[string]string) string {
	for _, k := range []string{"code", "name", "customerId", "contractValue", "dpPct", "retentionPct", "endDate", "status", "notes"} {
		if m, ok := f[k]; ok {
			return m
		}
	}
	for _, m := range f {
		return m
	}
	return ""
}

func field(name, msg string) error {
	return httpx.Unprocessable("project.invalid", msg).WithFields(map[string]string{name: msg})
}

// fieldErr menambahkan field ke problem tanpa field, supaya formulir bisa menandai isiannya.
func fieldErr(err error, name string) error {
	var p *httpx.Problem
	if errors.As(err, &p) && len(p.Fields) == 0 {
		p.Fields = map[string]string{name: p.Detail}
	}
	return err
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

func toProject(r store.ProjectProject, names map[uuid.UUID]string) Project {
	p := Project{
		ID: r.ID, Code: r.Code, Name: r.Name, CustomerID: r.CustomerID, ContractValue: r.ContractValue,
		DPPct: r.DpPct, RetentionPct: r.RetentionPct, PPhCode: r.PphCode, RevenueAccountID: r.RevenueAccountID,
		StartDate: r.StartDate, EndDate: r.EndDate, Status: r.Status, Notes: r.Notes, Version: int(r.Version),
	}
	if r.CustomerID != nil {
		p.CustomerName = names[*r.CustomerID]
	}
	return p
}

func (s *service) CreateProject(ctx context.Context, companyID uuid.UUID, in ProjectInput, actor audit.Actor) (Detail, error) {
	var out Detail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		id := uuid.Must(uuid.NewV7())
		if err := s.validate(ctx, companyID, id, &in); err != nil {
			return err
		}
		if err := s.q(ctx).InsertProject(ctx, store.InsertProjectParams{
			ID: id, CompanyID: companyID, Code: in.Code, Name: in.Name, CustomerID: in.CustomerID,
			ContractValue: in.ContractValue, DpPct: in.DPPct, RetentionPct: in.RetentionPct, PphCode: in.PPhCode,
			RevenueAccountID: in.RevenueAccountID, StartDate: in.StartDate, EndDate: in.EndDate, Status: in.Status,
			Notes: in.Notes, CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "project.create",
			ObjectType: "project", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"code": in.Code, "name": in.Name, "contractValue": in.ContractValue.String()},
		}); err != nil {
			return err
		}
		var err error
		out, err = s.Project(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) UpdateProject(ctx context.Context, companyID, id uuid.UUID, in ProjectInput, actor audit.Actor) (Detail, error) {
	var out Detail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		before, err := s.q(ctx).GetProjectForUpdate(ctx, store.GetProjectForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("project.not_found", "Proyek tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if err := s.validate(ctx, companyID, id, &in); err != nil {
			return err
		}
		n, err := s.q(ctx).UpdateProject(ctx, store.UpdateProjectParams{
			CompanyID: companyID, ID: id, Version: int32(in.Version), Code: in.Code, Name: in.Name,
			CustomerID: in.CustomerID, ContractValue: in.ContractValue, DpPct: in.DPPct, RetentionPct: in.RetentionPct,
			PphCode: in.PPhCode, RevenueAccountID: in.RevenueAccountID, StartDate: in.StartDate, EndDate: in.EndDate,
			Status: in.Status, Notes: in.Notes,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Proyek ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "project.update",
			ObjectType: "project", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"code": before.Code, "name": before.Name, "status": before.Status,
				"contractValue": before.ContractValue.String(), "dpPct": before.DpPct.String(), "retentionPct": before.RetentionPct.String()},
			After: map[string]any{"code": in.Code, "name": in.Name, "status": in.Status,
				"contractValue": in.ContractValue.String(), "dpPct": in.DPPct.String(), "retentionPct": in.RetentionPct.String()},
		}); err != nil {
			return err
		}
		out, err = s.Project(ctx, companyID, id)
		return err
	})
	return out, err
}

// costClasses: pos RAB adalah akun beban pokok, beban usaha, beban lain-lain, atau pajak.
var costClasses = map[int]bool{5: true, 6: true, 8: true, 9: true}

// SetBudget mengganti seluruh RAB proyek.
func (s *service) SetBudget(ctx context.Context, companyID, id uuid.UUID, lines []BudgetLineInput, actor audit.Actor) (Detail, error) {
	if len(lines) > 200 {
		return Detail{}, httpx.Unprocessable("budget.invalid", "RAB maksimal 200 pos.")
	}
	var out Detail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		if _, err := q.GetProjectForUpdate(ctx, store.GetProjectForUpdateParams{CompanyID: companyID, ID: id}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.NotFound("project.not_found", "Proyek tidak ditemukan.")
			}
			return err
		}
		accounts, err := s.accounts(ctx, companyID)
		if err != nil {
			return err
		}
		f := map[string]string{}
		seen := map[uuid.UUID]bool{}
		var total money.Amount
		for i, l := range lines {
			key := fmt.Sprintf("lines.%d.", i)
			a, ok := accounts[l.AccountID]
			switch {
			case !ok:
				f[key+"accountId"] = "Pilih akun biaya."
			case seen[a.ID]:
				f[key+"accountId"] = fmt.Sprintf("%s %s sudah ada di RAB; gabungkan nilainya.", a.Code, a.Name)
			case !a.Postable:
				f[key+"accountId"] = fmt.Sprintf("%s %s adalah akun header; pilih akun detail.", a.Code, a.Name)
			case !costClasses[a.Class]:
				f[key+"accountId"] = fmt.Sprintf("%s %s bukan akun biaya; pilih akun berkepala 5, 6, 8, atau 9.", a.Code, a.Name)
			}
			seen[l.AccountID] = true
			if l.Amount.Sign() < 0 || !l.Amount.MulFrac(1, 1, 0, money.Down).Equal(l.Amount) {
				f[key+"amount"] = "Nilai RAB harus rupiah bulat dan tidak negatif."
			}
			if utf8.RuneCountInString(strings.TrimSpace(l.Description)) > 200 {
				f[key+"description"] = "Uraian maksimal 200 karakter."
			}
			total = total.Add(l.Amount)
		}
		if len(f) > 0 {
			return httpx.Unprocessable("budget.invalid", firstLine(f)).WithFields(f)
		}
		if err := q.DeleteBudget(ctx, store.DeleteBudgetParams{CompanyID: companyID, ProjectID: id}); err != nil {
			return err
		}
		for i, l := range lines {
			if err := q.InsertBudgetLine(ctx, store.InsertBudgetLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, ProjectID: id, LineNo: int16(i + 1),
				AccountID: l.AccountID, Description: strings.TrimSpace(l.Description), Amount: l.Amount,
			}); err != nil {
				return err
			}
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "project.budget.set",
			ObjectType: "project", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"lines": len(lines), "total": total.String()},
		}); err != nil {
			return err
		}
		out, err = s.Project(ctx, companyID, id)
		return err
	})
	return out, err
}

func firstLine(f map[string]string) string {
	for i := 0; i < 200; i++ {
		for _, k := range []string{"accountId", "amount", "description"} {
			if m, ok := f[fmt.Sprintf("lines.%d.%s", i, k)]; ok {
				return m
			}
		}
	}
	for _, m := range f {
		return m
	}
	return ""
}

func (s *service) Projects(ctx context.Context, companyID uuid.UUID, f Filter) ([]Summary, error) {
	p := store.ListProjectsParams{CompanyID: companyID}
	if f.Status != "" {
		p.Status = &f.Status
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		p.Search = &q
	}
	q := s.q(ctx)
	rows, err := q.ListProjects(ctx, p)
	if err != nil {
		return nil, err
	}
	var customers []uuid.UUID
	for _, r := range rows {
		if r.CustomerID != nil {
			customers = append(customers, *r.CustomerID)
		}
	}
	names, err := s.Core.ContactNames(ctx, companyID, customers)
	if err != nil {
		return nil, err
	}
	budgets, err := q.BudgetTotals(ctx, companyID)
	if err != nil {
		return nil, err
	}
	budgetOf := map[uuid.UUID]money.Amount{}
	for _, b := range budgets {
		budgetOf[b.ProjectID] = b.Amount
	}
	totals, err := s.profit(ctx, companyID, nil, allTime())
	if err != nil {
		return nil, err
	}
	out := make([]Summary, len(rows))
	for i, r := range rows {
		t := totals[r.ID]
		out[i] = summarize(toProject(r, names), budgetOf[r.ID], t.revenue, t.cost)
	}
	return out, nil
}

func summarize(p Project, budget, revenue, cost money.Amount) Summary {
	s := Summary{Project: p, Budget: budget, Revenue: revenue, Cost: cost, Profit: revenue.Sub(cost)}
	s.CostPct = percent(cost, budget)
	return s
}

// percent = a ÷ b × 100 dengan 2 desimal; nil bila b nol.
func percent(a, b money.Amount) *money.Amount {
	if b.Sign() == 0 {
		return nil
	}
	p := a.MulDiv(hundred, b, 2, money.HalfUp)
	return &p
}

type period struct{ from, to civil.Date }

func allTime() period { return period{civil.New(2000, 1, 1), civil.New(2100, 12, 1)} }

type pl struct{ revenue, cost money.Amount }

// profit menjumlahkan pendapatan (kelas 4, 7) dan biaya (kelas 5, 6, 8, 9) per proyek.
func (s *service) profit(ctx context.Context, companyID uuid.UUID, projectID *uuid.UUID, p period) (map[uuid.UUID]pl, error) {
	balances, err := s.Ledger.ProjectBalances(ctx, companyID, projectID, p.from, p.to)
	if err != nil {
		return nil, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]pl{}
	for _, b := range balances {
		t := out[b.ProjectID]
		switch accounts[b.AccountID].Class {
		case 4, 7:
			t.revenue = t.revenue.Add(b.Credit.Sub(b.Debit))
		case 5, 6, 8, 9:
			t.cost = t.cost.Add(b.Debit.Sub(b.Credit))
		}
		out[b.ProjectID] = t
	}
	return out, nil
}

func (s *service) Project(ctx context.Context, companyID, id uuid.UUID) (Detail, error) {
	q := s.q(ctx)
	r, err := q.GetProject(ctx, store.GetProjectParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, httpx.NotFound("project.not_found", "Proyek tidak ditemukan.")
	}
	if err != nil {
		return Detail{}, err
	}
	var customers []uuid.UUID
	if r.CustomerID != nil {
		customers = append(customers, *r.CustomerID)
	}
	names, err := s.Core.ContactNames(ctx, companyID, customers)
	if err != nil {
		return Detail{}, err
	}
	lines, err := q.BudgetLines(ctx, store.BudgetLinesParams{CompanyID: companyID, ProjectID: id})
	if err != nil {
		return Detail{}, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return Detail{}, err
	}
	var budget money.Amount
	budgetLines := make([]BudgetLine, len(lines))
	for i, l := range lines {
		a := accounts[l.AccountID]
		budgetLines[i] = BudgetLine{AccountID: l.AccountID, AccountCode: a.Code, AccountName: a.Name, Description: l.Description, Amount: l.Amount}
		budget = budget.Add(l.Amount)
	}
	totals, err := s.profit(ctx, companyID, &id, allTime())
	if err != nil {
		return Detail{}, err
	}
	p := toProject(r, names)
	invoices, err := s.projectInvoices(ctx, companyID, id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{
		Summary:     summarize(p, budget, totals[id].revenue, totals[id].cost),
		BudgetLines: budgetLines, Billing: billingOf(p, invoices), Invoices: invoices,
	}, nil
}

func (s *service) projectInvoices(ctx context.Context, companyID, id uuid.UUID) ([]sales.Invoice, error) {
	list, err := s.Sales.Invoices(ctx, companyID, sales.InvoiceFilter{
		From: civil.New(2000, 1, 1), To: civil.New(2100, 12, 31), ProjectID: &id,
	})
	if list == nil {
		list = []sales.Invoice{}
	}
	return list, err
}

func (s *service) Lines(ctx context.Context, companyID, id uuid.UUID, accountID *uuid.UUID, from, to civil.Date) ([]ledger.ProjectLine, bool, error) {
	if _, err := s.q(ctx).GetProject(ctx, store.GetProjectParams{CompanyID: companyID, ID: id}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, httpx.NotFound("project.not_found", "Proyek tidak ditemukan.")
		}
		return nil, false, err
	}
	return s.Ledger.ProjectLines(ctx, companyID, id, accountID, from, to)
}

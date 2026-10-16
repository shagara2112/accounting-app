package project

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger"
	"lajur/internal/modules/project/internal/store"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Billing merangkum tagihan proyek dari faktur yang tidak di-void (draf ikut dihitung,
// supaya termin berikutnya tidak menagih ulang progres yang sudah ada drafnya).
type Billing struct {
	ContractValue     money.Amount `json:"contractValue"`
	AdvanceInvoiced   money.Amount `json:"advanceInvoiced"` // DPP faktur uang muka
	AdvancePosted     bool         `json:"advancePosted"`
	AdvanceDeducted   money.Amount `json:"advanceDeducted"` // potongan uang muka di faktur termin
	AdvanceRemaining  money.Amount `json:"advanceRemaining"`
	ProgressPct       money.Amount `json:"progressPct"`    // progres kumulatif tertinggi yang sudah ditagih
	ProgressBilled    money.Amount `json:"progressBilled"` // Σ nilai progres faktur termin
	Unbilled          money.Amount `json:"unbilled"`       // nilai kontrak − progres ditagih
	Terms             int          `json:"terms"`          // jumlah faktur termin
	RetentionHeld     money.Amount `json:"retentionHeld"`  // retensi termin diposting yang belum dilepas
	RetentionReleased money.Amount `json:"retentionReleased"`
	Drafts            int          `json:"drafts"`
}

func billingOf(p Project, invoices []sales.Invoice) Billing {
	b := Billing{ContractValue: p.ContractValue}
	var retained money.Amount
	for _, inv := range invoices {
		if inv.Status == "void" {
			continue
		}
		if inv.Status == "draft" {
			b.Drafts++
		}
		switch inv.Kind {
		case sales.KindAdvance:
			b.AdvanceInvoiced = b.AdvanceInvoiced.Add(inv.DPP)
			b.AdvancePosted = b.AdvancePosted || inv.Status == "posted"
		case sales.KindProgress:
			b.Terms++
			b.ProgressBilled = b.ProgressBilled.Add(inv.Subtotal)
			b.AdvanceDeducted = b.AdvanceDeducted.Add(inv.Discount)
			if inv.ProgressPct != nil && inv.ProgressPct.Cmp(b.ProgressPct) > 0 {
				b.ProgressPct = *inv.ProgressPct
			}
			if inv.Status == "posted" {
				retained = retained.Add(inv.Retention)
			}
		case sales.KindRetention:
			b.RetentionReleased = b.RetentionReleased.Add(inv.DPP)
		}
	}
	b.AdvanceRemaining = b.AdvanceInvoiced.Sub(b.AdvanceDeducted)
	b.Unbilled = b.ContractValue.Sub(b.ProgressBilled)
	b.RetentionHeld = retained.Sub(b.RetentionReleased)
	return b
}

// BillingInput membuat draf faktur uang muka, termin, atau pelepasan retensi (AR-08).
type BillingInput struct {
	Kind        string       `json:"kind"`
	Date        civil.Date   `json:"date"`
	ProgressPct money.Amount `json:"progressPct"` // termin: progres kumulatif s.d. termin ini
	TaxCode     string       `json:"taxCode"`     // kosong = PPN (atau NON-PPN untuk PT non-PKP)
	Memo        string       `json:"memo"`
}

// CreateBilling menghitung tagihan proyek lalu menyimpannya sebagai draf faktur penjualan:
//
//	nilai termin  = progres% × nilai kontrak − nilai termin sebelumnya
//	potongan UM   = uang muka% × nilai termin (termin terakhir: sisa uang muka, supaya habis persis)
//	retensi       = retensi% × nilai termin
//
// PPN dihitung dari nilai termin setelah potongan uang muka, karena uang muka sudah difakturkan.
func (s *service) CreateBilling(ctx context.Context, companyID, id uuid.UUID, in BillingInput, actor audit.Actor) (sales.InvoiceDetail, error) {
	in.Memo, in.TaxCode = strings.TrimSpace(in.Memo), strings.TrimSpace(in.TaxCode)
	if in.Date.IsZero() {
		return sales.InvoiceDetail{}, field("date", "Tanggal faktur wajib diisi.")
	}
	if utf8.RuneCountInString(in.Memo) > 500 {
		return sales.InvoiceDetail{}, field("memo", "Catatan maksimal 500 karakter.")
	}
	var out sales.InvoiceDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		// Kunci proyek supaya dua tagihan bersamaan tidak menghitung termin yang sama.
		r, err := s.q(ctx).GetProjectForUpdate(ctx, store.GetProjectForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("project.not_found", "Proyek tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		p := toProject(r, nil)
		switch {
		case p.Status == StatusCancelled:
			return httpx.Unprocessable("billing.cancelled", "Proyek ini sudah dibatalkan.")
		case p.CustomerID == nil:
			return httpx.Unprocessable("billing.no_customer", "Isi pemberi kerja di data proyek sebelum membuat tagihan.")
		}
		invoices, err := s.projectInvoices(ctx, companyID, id)
		if err != nil {
			return err
		}
		b := billingOf(p, invoices)
		if in.TaxCode == "" {
			company, err := s.Core.Company(ctx, companyID)
			if err != nil {
				return err
			}
			in.TaxCode = "PPN"
			if !company.PKP {
				in.TaxCode = "NON-PPN"
			}
		}

		inv := sales.InvoiceInput{CustomerID: *p.CustomerID, Date: in.Date, Memo: in.Memo, ProjectID: &id, Kind: in.Kind}
		one := money.FromInt(1)
		switch in.Kind {
		case sales.KindAdvance:
			if p.DPPct.Sign() <= 0 || p.ContractValue.Sign() <= 0 {
				return httpx.Unprocessable("billing.no_advance", "Isi nilai kontrak dan persen uang muka di data proyek lebih dulu.")
			}
			if b.AdvanceInvoiced.Sign() > 0 {
				return httpx.Conflict("billing.advance_exists", "Faktur uang muka proyek ini sudah dibuat. Void dulu bila ingin membuat ulang.")
			}
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.ProjectAdvance)
			if err != nil {
				return err
			}
			amount := p.ContractValue.MulDiv(p.DPPct, hundred, 0, money.HalfUp)
			inv.Lines = []trade.LineInput{{
				Description: fmt.Sprintf("Uang muka %s%% kontrak %s", pctText(p.DPPct), p.Name),
				AccountID:   acc.ID, Qty: one, UnitPrice: amount, TaxCode: in.TaxCode,
			}}

		case sales.KindProgress:
			pct := in.ProgressPct
			switch {
			case p.Status != StatusRunning:
				return httpx.Unprocessable("billing.not_running", "Termin hanya untuk proyek yang masih berjalan.")
			case p.ContractValue.Sign() <= 0:
				return httpx.Unprocessable("billing.no_contract", "Isi nilai kontrak di data proyek lebih dulu.")
			case p.RevenueAccountID == nil:
				return httpx.Unprocessable("billing.no_revenue_account", "Pilih akun pendapatan proyek di data proyek lebih dulu.")
			case pct.Sign() <= 0 || pct.Cmp(hundred) > 0 || !pct.MulFrac(1, 1, 2, money.Down).Equal(pct):
				return field("progressPct", "Progres 0–100% dengan maksimal 2 desimal.")
			case pct.Cmp(b.ProgressPct) <= 0:
				return field("progressPct", fmt.Sprintf("Progres harus melebihi progres yang sudah ditagih (%s%%).", pctText(b.ProgressPct)))
			case p.DPPct.Sign() > 0 && !b.AdvancePosted:
				return httpx.Unprocessable("billing.advance_first",
					"Proyek ini memakai uang muka: buat dan posting faktur uang muka dulu, supaya termin bisa memotongnya.")
			}
			gross := p.ContractValue.MulDiv(pct, hundred, 0, money.HalfUp).Sub(b.ProgressBilled)
			if gross.Sign() <= 0 {
				return field("progressPct", "Tidak ada nilai progres baru untuk ditagih.")
			}
			var deduction money.Amount
			if b.AdvanceRemaining.Sign() > 0 {
				if pct.Equal(hundred) {
					deduction = b.AdvanceRemaining
				} else {
					deduction = gross.MulDiv(p.DPPct, hundred, 0, money.HalfUp)
				}
				deduction = minAmount(minAmount(deduction, b.AdvanceRemaining), gross)
			}
			retention := minAmount(gross.MulDiv(p.RetentionPct, hundred, 0, money.HalfUp), gross.Sub(deduction))
			line := trade.LineInput{
				Description: fmt.Sprintf("Termin %d: progres pekerjaan s.d. %s%% · %s", b.Terms+1, pctText(pct), p.Name),
				AccountID:   *p.RevenueAccountID, Qty: one, UnitPrice: gross, TaxCode: in.TaxCode,
			}
			if deduction.Sign() > 0 {
				d := deduction
				line.DiscountAmount = &d
				line.DiscountPct = p.DPPct
			}
			inv.Lines = []trade.LineInput{line}
			inv.ProgressPct, inv.Retention = &pct, retention

		case sales.KindRetention:
			if b.RetentionHeld.Sign() <= 0 {
				return httpx.Unprocessable("billing.no_retention", "Tidak ada retensi yang tertahan dari faktur termin yang sudah diposting.")
			}
			for _, x := range invoices {
				if x.Kind == sales.KindRetention && x.Status == "draft" {
					return httpx.Conflict("billing.retention_draft", "Sudah ada draf pelepasan retensi; posting atau hapus draf itu dulu.")
				}
			}
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.RetentionReceivable)
			if err != nil {
				return err
			}
			inv.Lines = []trade.LineInput{{
				Description: "Pelepasan retensi " + p.Name, AccountID: acc.ID, Qty: one, UnitPrice: b.RetentionHeld,
				TaxCode: "NON-PPN", // PPN-nya sudah dipungut di faktur termin
			}}

		default:
			return field("kind", "Pilih uang muka, termin, atau pelepasan retensi.")
		}

		out, err = s.Sales.CreateInvoice(ctx, companyID, inv, actor)
		if err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "project.billing.create",
			ObjectType: "project", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"kind": in.Kind, "invoice": out.ID.String(), "total": out.Total.String(), "progressPct": in.ProgressPct.String()},
		})
		return err
	})
	return out, err
}

func minAmount(a, b money.Amount) money.Amount {
	if a.Cmp(b) < 0 {
		return a
	}
	return b
}

// pctText menulis persen tanpa nol di belakang koma: 37,5 atau 40.
func pctText(a money.Amount) string {
	s := strings.TrimRight(strings.TrimRight(a.String(), "0"), ".")
	return strings.Replace(s, ".", ",", 1)
}

// ReportRow adalah satu akun di laba rugi proyek; Budget hanya untuk akun biaya.
type ReportRow struct {
	AccountID uuid.UUID     `json:"accountId"`
	Code      string        `json:"code"`
	Name      string        `json:"name"`
	Class     int           `json:"class"`
	Budget    money.Amount  `json:"budget"`
	Actual    money.Amount  `json:"actual"`
	Remaining money.Amount  `json:"remaining"` // RAB − aktual
	UsedPct   *money.Amount `json:"usedPct,omitempty"`
}

// Report adalah laba rugi satu proyek dan biaya aktual vs RAB (IND-03).
type Report struct {
	Project      Project       `json:"project"`
	From         civil.Date    `json:"from"`
	To           civil.Date    `json:"to"`
	Revenue      []ReportRow   `json:"revenue"`
	Costs        []ReportRow   `json:"costs"`
	TotalRevenue money.Amount  `json:"totalRevenue"`
	TotalBudget  money.Amount  `json:"totalBudget"`
	TotalCost    money.Amount  `json:"totalCost"`
	Profit       money.Amount  `json:"profit"`
	Margin       *money.Amount `json:"margin,omitempty"`  // laba ÷ pendapatan × 100
	CostPct      *money.Amount `json:"costPct,omitempty"` // biaya ÷ RAB × 100
}

func (s *service) Report(ctx context.Context, companyID, id uuid.UUID, from, to civil.Date) (Report, error) {
	d, err := s.Project(ctx, companyID, id)
	if err != nil {
		return Report{}, err
	}
	balances, err := s.Ledger.ProjectBalances(ctx, companyID, &id, from, to)
	if err != nil {
		return Report{}, err
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return Report{}, err
	}
	rows := map[uuid.UUID]*ReportRow{}
	row := func(accountID uuid.UUID) *ReportRow {
		r, ok := rows[accountID]
		if !ok {
			a := accounts[accountID]
			r = &ReportRow{AccountID: accountID, Code: a.Code, Name: a.Name, Class: a.Class}
			rows[accountID] = r
		}
		return r
	}
	for _, b := range d.BudgetLines {
		row(b.AccountID).Budget = b.Amount
	}
	for _, b := range balances {
		switch accounts[b.AccountID].Class {
		case 4, 7:
			r := row(b.AccountID)
			r.Actual = r.Actual.Add(b.Credit.Sub(b.Debit))
		case 5, 6, 8, 9:
			r := row(b.AccountID)
			r.Actual = r.Actual.Add(b.Debit.Sub(b.Credit))
		}
	}
	out := Report{Project: d.Project, From: from.MonthStart(), To: to.MonthEnd(), Revenue: []ReportRow{}, Costs: []ReportRow{}}
	for _, r := range rows {
		if r.Class == 4 || r.Class == 7 {
			out.Revenue = append(out.Revenue, *r)
			out.TotalRevenue = out.TotalRevenue.Add(r.Actual)
			continue
		}
		r.Remaining = r.Budget.Sub(r.Actual)
		r.UsedPct = percent(r.Actual, r.Budget)
		out.Costs = append(out.Costs, *r)
		out.TotalBudget = out.TotalBudget.Add(r.Budget)
		out.TotalCost = out.TotalCost.Add(r.Actual)
	}
	sort.Slice(out.Revenue, func(i, j int) bool { return out.Revenue[i].Code < out.Revenue[j].Code })
	sort.Slice(out.Costs, func(i, j int) bool { return out.Costs[i].Code < out.Costs[j].Code })
	out.Profit = out.TotalRevenue.Sub(out.TotalCost)
	out.Margin = percent(out.Profit, out.TotalRevenue)
	out.CostPct = percent(out.TotalCost, out.TotalBudget)
	return out, nil
}

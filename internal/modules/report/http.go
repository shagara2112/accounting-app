package report

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"lajur/internal/platform/api"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/export"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes: laporan keuangan dan dashboard. Ekspor CSV dan Excel memakai mode lisensi Export,
// sehingga tetap bisa dipakai walau lisensi berakhir (PRD: ekspor tidak pernah dikunci), dan
// butuh izin ekspor data di samping izin melihat laporan.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	view := func(path string, fn httpx.Handler) api.Route {
		return api.Route{Method: http.MethodGet, Path: path, Access: api.Permission, Perm: rbac.ReportView, License: license.ModeRead, Handle: fn}
	}
	return []api.Route{
		view("/api/v1/reports/dashboard", h.dashboard),
		view("/api/v1/reports/cash-projection", h.cashProjection),
		view("/api/v1/reports/balance-sheet", h.json(h.balanceSheet)),
		view("/api/v1/reports/profit-loss", h.json(h.profitLoss)),
		view("/api/v1/reports/cash-flow", h.json(h.cashFlow)),
		view("/api/v1/reports/trial-balance", h.json(h.trialBalance)),
		view("/api/v1/reports/summary", h.json(h.summary)),
		view("/api/v1/reports/contact-card", h.json(h.contactCard)),
		{Method: http.MethodGet, Path: "/api/v1/reports/{name}/export", Access: api.Permission, Perm: rbac.DataExport, License: license.ModeExport, Handle: h.export},
	}
}

type handlers struct{ svc Service }

// built adalah laporan yang sudah dihitung beserta tabel ekspornya.
type built struct {
	body  any
	table export.Table
	file  string // nama berkas tanpa ekstensi
}

type builderFn func(r *http.Request) (built, error)

func (h handlers) json(fn builderFn) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		b, err := fn(r)
		if err != nil {
			return err
		}
		return httpx.WriteJSON(w, http.StatusOK, b.body)
	}
}

func (h handlers) export(w http.ResponseWriter, r *http.Request) error {
	if !api.Can(r, rbac.ReportView) {
		return httpx.Forbidden("report.forbidden", "Anda tidak punya izin melihat laporan.")
	}
	fns := map[string]builderFn{
		"balance-sheet": h.balanceSheet, "profit-loss": h.profitLoss, "cash-flow": h.cashFlow,
		"trial-balance": h.trialBalance, "summary": h.summary, "contact-card": h.contactCard,
	}
	fn, ok := fns[chi.URLParam(r, "name")]
	if !ok {
		return httpx.NotFound("report.not_found", "Laporan tidak dikenal.")
	}
	if chi.URLParam(r, "name") != "profit-loss" {
		if err := api.WholeCompany(r); err != nil {
			return err
		}
	}
	b, err := fn(r)
	if err != nil {
		return err
	}
	switch format := r.URL.Query().Get("format"); format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, b.file))
		return export.WriteCSV(w, b.table)
	case "xlsx", "":
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xlsx"`, b.file))
		return export.WriteXLSX(w, b.table)
	default:
		return httpx.BadRequest("report.format", "Format ekspor harus csv atau xlsx.")
	}
}

func today() civil.Date { return civil.Today(civil.Jakarta) }

func date(r *http.Request, name string, def civil.Date) (civil.Date, error) {
	return api.DateQuery(r, name, def)
}

// period membaca from dan to; bawaan awal bulan ini s.d. hari ini.
func period(r *http.Request, fromKey, toKey string) (Period, error) {
	t := today()
	from, err := date(r, fromKey, t.MonthStart())
	if err != nil {
		return Period{}, err
	}
	to, err := date(r, toKey, t)
	return Period{From: from, To: to}, err
}

func (h handlers) balanceSheet(r *http.Request) (built, error) {
	d, err := date(r, "date", today())
	if err != nil {
		return built{}, err
	}
	dates := []civil.Date{d}
	if r.URL.Query().Get("compare") != "" {
		c, err := date(r, "compare", d)
		if err != nil {
			return built{}, err
		}
		dates = append(dates, c)
	}
	st, err := h.svc.BalanceSheet(r.Context(), api.CompanyID(r), dates)
	return built{body: st, table: st.Table("Per " + d.Indonesian()), file: "neraca-" + d.String()}, err
}

func (h handlers) profitLoss(r *http.Request) (built, error) {
	p, err := period(r, "from", "to")
	if err != nil {
		return built{}, err
	}
	periods := []Period{p}
	if r.URL.Query().Get("compareFrom") != "" {
		c, err := period(r, "compareFrom", "compareTo")
		if err != nil {
			return built{}, err
		}
		periods = append(periods, c)
	}
	branch, err := api.UUIDQuery(r, "branch")
	if err != nil {
		return built{}, err
	}
	dept, err := api.UUIDQuery(r, "department")
	if err != nil {
		return built{}, err
	}
	f := DimFilter{BranchID: branch, DepartmentID: dept, PerBranch: r.URL.Query().Get("per") == "cabang",
		PerDepartment: r.URL.Query().Get("per") == "departemen", Branches: api.Branches(r)}
	st, err := h.svc.ProfitLossBy(r.Context(), api.CompanyID(r), periods, f)
	return built{body: st, table: st.Table(periodLabel(p)), file: fmt.Sprintf("laba-rugi-%s_%s", p.From, p.To)}, err
}

func (h handlers) cashFlow(r *http.Request) (built, error) {
	p, err := period(r, "from", "to")
	if err != nil {
		return built{}, err
	}
	cf, err := h.svc.CashFlow(r.Context(), api.CompanyID(r), p)
	return built{body: cf, table: cf.Statement.Table(periodLabel(p)), file: fmt.Sprintf("arus-kas-%s_%s", p.From, p.To)}, err
}

func (h handlers) trialBalance(r *http.Request) (built, error) {
	d, err := date(r, "date", today())
	if err != nil {
		return built{}, err
	}
	tb, err := h.svc.TrialBalance(r.Context(), api.CompanyID(r), d)
	return built{body: tb, table: tb.Table(), file: "neraca-saldo-" + d.String()}, err
}

func (h handlers) summary(r *http.Request) (built, error) {
	p, err := period(r, "from", "to")
	if err != nil {
		return built{}, err
	}
	kind := r.URL.Query().Get("kind")
	s, err := h.svc.Summary(r.Context(), api.CompanyID(r), kind, p)
	return built{body: s, table: s.Table(), file: fmt.Sprintf("ringkasan-%s-%s_%s", kind, p.From, p.To)}, err
}

func (h handlers) contactCard(r *http.Request) (built, error) {
	p, err := period(r, "from", "to")
	if err != nil {
		return built{}, err
	}
	contact, err := api.UUIDQuery(r, "contact")
	if err != nil {
		return built{}, err
	}
	if contact == nil {
		return built{}, httpx.BadRequest("report.contact", "Pilih kontak.")
	}
	side := r.URL.Query().Get("side")
	c, err := h.svc.ContactCard(r.Context(), api.CompanyID(r), side, *contact, p)
	return built{body: c, table: c.Table(), file: fmt.Sprintf("kartu-%s-%s_%s", side, p.From, p.To)}, err
}

func (h handlers) dashboard(w http.ResponseWriter, r *http.Request) error {
	d, err := h.svc.Dashboard(r.Context(), api.CompanyID(r), today())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) cashProjection(w http.ResponseWriter, r *http.Request) error {
	asOf, err := api.DateQuery(r, "date", today())
	if err != nil {
		return err
	}
	weeks := 8
	if v := r.URL.Query().Get("weeks"); v != "" {
		if _, err := fmt.Sscan(v, &weeks); err != nil {
			return httpx.BadRequest("request.query", "weeks harus angka.")
		}
	}
	p, err := h.svc.CashProjection(r.Context(), api.CompanyID(r), asOf, weeks)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, p)
}

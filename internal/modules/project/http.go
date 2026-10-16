package project

import (
	"net/http"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes: data proyek, RAB, laporan laba per proyek, dan pembuatan draf tagihan proyek.
// Tagihan proyek adalah faktur penjualan, sehingga butuh izin membuat penjualan.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	view := func(path string, fn httpx.Handler) api.Route {
		return api.Route{Method: http.MethodGet, Path: path, Access: api.Permission, Perm: rbac.ProjectView, License: license.ModeRead, Handle: fn}
	}
	write := func(method, path, perm string, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: license.ModeWrite, Handle: fn}
	}
	billing := write(http.MethodPost, "/api/v1/projects/{id}/billings", rbac.SalesCreate, h.createBilling)
	billing.Idempotent = true
	return []api.Route{
		view("/api/v1/projects", h.projects),
		write(http.MethodPost, "/api/v1/projects", rbac.ProjectCreate, h.create),
		view("/api/v1/projects/{id}", h.project),
		write(http.MethodPut, "/api/v1/projects/{id}", rbac.ProjectCreate, h.update),
		write(http.MethodPut, "/api/v1/projects/{id}/budget", rbac.ProjectCreate, h.setBudget),
		view("/api/v1/projects/{id}/report", h.report),
		view("/api/v1/projects/{id}/lines", h.lines),
		billing,
	}
}

type handlers struct{ svc Service }

func (h handlers) projects(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	list, err := h.svc.Projects(r.Context(), api.CompanyID(r), Filter{Status: q.Get("status"), Search: q.Get("q")})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) project(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	d, err := h.svc.Project(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) create(w http.ResponseWriter, r *http.Request) error {
	var in ProjectInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.CreateProject(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, d)
}

func (h handlers) update(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in ProjectInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.UpdateProject(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) setBudget(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Lines []BudgetLineInput `json:"lines"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	d, err := h.svc.SetBudget(r.Context(), api.CompanyID(r), id, req.Lines, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

// months membaca from dan to (yyyy-mm); kosong = sepanjang umur proyek.
func months(r *http.Request) (civil.Date, civil.Date, error) {
	q := r.URL.Query()
	p := allTime()
	from, to := p.from, p.to
	if v := q.Get("from"); v != "" {
		m, err := civil.ParseMonth(v)
		if err != nil {
			return from, to, httpx.BadRequest("request.query", err.Error())
		}
		from = m
	}
	if v := q.Get("to"); v != "" {
		m, err := civil.ParseMonth(v)
		if err != nil {
			return from, to, httpx.BadRequest("request.query", err.Error())
		}
		to = m
	}
	return from, to, nil
}

func (h handlers) report(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	from, to, err := months(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.Report(r.Context(), api.CompanyID(r), id, from, to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h handlers) lines(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	account, err := api.UUIDQuery(r, "account")
	if err != nil {
		return err
	}
	from, to, err := months(r)
	if err != nil {
		return err
	}
	list, truncated, err := h.svc.Lines(r.Context(), api.CompanyID(r), id, account, from.MonthStart(), to.MonthEnd())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list, "truncated": truncated})
}

func (h handlers) createBilling(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in BillingInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	inv, err := h.svc.CreateBilling(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, inv)
}

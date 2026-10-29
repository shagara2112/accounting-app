package budget

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
)

// Routes: versi anggaran, baris per irisan (PT, departemen, proyek), usulan dari realisasi
// tahun lalu, dan laporan anggaran vs realisasi. Melihat cukup izin laporan; menyusun butuh
// izin anggaran.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	view := func(path string, fn httpx.Handler) api.Route {
		return api.Route{Method: http.MethodGet, Path: path, Access: api.Permission, Perm: rbac.ReportView, License: license.ModeRead, Handle: fn}
	}
	write := func(method, path string, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: rbac.BudgetManage, License: license.ModeWrite, Handle: fn}
	}
	return []api.Route{
		view("/api/v1/budgets", h.list),
		write(http.MethodPost, "/api/v1/budgets", h.create),
		view("/api/v1/budgets/compare", h.compare),
		{Method: http.MethodGet, Path: "/api/v1/budgets/suggest", Access: api.Permission, Perm: rbac.BudgetManage, License: license.ModeRead, Handle: h.suggest},
		view("/api/v1/budgets/{id}", h.get),
		write(http.MethodPut, "/api/v1/budgets/{id}", h.rename),
		write(http.MethodPost, "/api/v1/budgets/{id}/activate", h.activate),
		write(http.MethodDelete, "/api/v1/budgets/{id}", h.delete),
		write(http.MethodPut, "/api/v1/budgets/{id}/lines", h.saveSlice),
	}
}

type handlers struct{ svc Service }

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.NotFound("budget.not_found", "Anggaran tidak ditemukan.")
	}
	return id, nil
}

func optionalID(r *http.Request, name string) (*uuid.UUID, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, httpx.BadRequest("request.query", "Parameter "+name+" tidak valid.")
	}
	return &id, nil
}

func intParam(r *http.Request, name string, def int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, httpx.BadRequest("request.query", "Parameter "+name+" tidak valid.")
	}
	return n, nil
}

func slice(r *http.Request) (Slice, error) {
	dept, err := optionalID(r, "department")
	if err != nil {
		return Slice{}, err
	}
	project, err := optionalID(r, "project")
	if err != nil {
		return Slice{}, err
	}
	if dept != nil && project != nil {
		return Slice{}, httpx.BadRequest("request.query", "Pilih departemen atau proyek, tidak keduanya.")
	}
	return Slice{DepartmentID: dept, ProjectID: project}, nil
}

func (h handlers) list(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Budgets(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) get(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	d, err := h.svc.Budget(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) create(w http.ResponseWriter, r *http.Request) error {
	var in NewBudget
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	b, err := h.svc.CreateBudget(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h handlers) rename(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in Rename
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	b, err := h.svc.RenameBudget(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h handlers) activate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	b, err := h.svc.ActivateBudget(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h handlers) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteBudget(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) saveSlice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in SliceInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.SaveSlice(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) suggest(w http.ResponseWriter, r *http.Request) error {
	year, err := intParam(r, "year", 0)
	if err != nil {
		return err
	}
	sl, err := slice(r)
	if err != nil {
		return err
	}
	growth := money.Amount{}
	if v := r.URL.Query().Get("growth"); v != "" {
		if growth, err = money.Parse(v); err != nil {
			return httpx.BadRequest("request.query", "Parameter growth tidak valid.")
		}
	}
	lines, err := h.svc.Suggest(r.Context(), api.CompanyID(r), year, sl, growth)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (h handlers) compare(w http.ResponseWriter, r *http.Request) error {
	year, err := intParam(r, "year", 0)
	if err != nil {
		return err
	}
	from, err := intParam(r, "from", 1)
	if err != nil {
		return err
	}
	to, err := intParam(r, "to", 12)
	if err != nil {
		return err
	}
	budgetID, err := optionalID(r, "budget")
	if err != nil {
		return err
	}
	sl, err := slice(r)
	if err != nil {
		return err
	}
	if year == 0 && budgetID == nil {
		return httpx.BadRequest("request.query", "Isi tahun buku atau versi anggaran.")
	}
	c, err := h.svc.Compare(r.Context(), api.CompanyID(r), CompareFilter{FiscalYear: year, BudgetID: budgetID, FromMonth: from, ToMonth: to, Slice: sl})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

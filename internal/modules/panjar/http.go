package panjar

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes: daftar dan rincian pengajuan untuk pengaju (hanya miliknya) dan peninjau (semua),
// pengajuan dan perubahannya oleh kepala unit, serta keputusan dan tanda bayar oleh finance.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	read := rbac.PanjarView + "|" + rbac.PanjarCreate
	route := func(method, path, perm string, fn httpx.Handler) api.Route {
		mode := license.ModeWrite
		if method == http.MethodGet {
			mode = license.ModeRead
		}
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: mode, Handle: fn}
	}
	create := route(http.MethodPost, "/api/v1/panjar", rbac.PanjarCreate, h.create)
	create.Idempotent = true
	return []api.Route{
		route(http.MethodGet, "/api/v1/panjar", read, h.list),
		create,
		route(http.MethodGet, "/api/v1/panjar/unit-budget", read, h.unitBudget),
		route(http.MethodGet, "/api/v1/panjar/{id}", read, h.get),
		route(http.MethodPut, "/api/v1/panjar/{id}", rbac.PanjarCreate, h.update),
		route(http.MethodPost, "/api/v1/panjar/{id}/cancel", rbac.PanjarCreate, h.cancel),
		route(http.MethodPost, "/api/v1/panjar/{id}/decide", rbac.PanjarApprove, h.decide),
		route(http.MethodPost, "/api/v1/panjar/{id}/paid", rbac.PanjarApprove, h.paid),
	}
}

type handlers struct{ svc Service }

// viewer: peninjau (izin lihat atau memutuskan) melihat semua pengajuan; pengaju hanya miliknya.
func viewer(r *http.Request) Viewer {
	v := Viewer{UserID: auth.ActorFrom(r).UserID}
	if s, ok := rbac.ScopeFrom(r.Context()); ok {
		v.All = s.Role.Perms.Has(rbac.PanjarView) || s.Role.Perms.Has(rbac.PanjarApprove)
	}
	return v
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, notFound()
	}
	return id, nil
}

func dateParam(r *http.Request, name string, def civil.Date) (civil.Date, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	d, err := civil.Parse(v)
	if err != nil {
		return civil.Date{}, httpx.BadRequest("request.query", "Parameter "+name+" tidak valid.")
	}
	return d, nil
}

func (h handlers) list(w http.ResponseWriter, r *http.Request) error {
	from, err := dateParam(r, "from", civil.New(2000, 1, 1))
	if err != nil {
		return err
	}
	to, err := dateParam(r, "to", civil.New(2100, 12, 31))
	if err != nil {
		return err
	}
	f := Filter{Status: r.URL.Query().Get("status"), From: from, To: to}
	if v := r.URL.Query().Get("department"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("request.query", "Parameter department tidak valid.")
		}
		f.DepartmentID = &id
	}
	list, err := h.svc.Requests(r.Context(), api.CompanyID(r), viewer(r), f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, list)
}

func (h handlers) get(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	d, err := h.svc.Request(r.Context(), api.CompanyID(r), id, viewer(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) create(w http.ResponseWriter, r *http.Request) error {
	var in Input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.Create(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, d)
}

func (h handlers) update(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.Update(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) cancel(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Version int `json:"version"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.Cancel(r.Context(), api.CompanyID(r), id, in.Version, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) decide(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in Decision
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.Decide(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) paid(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in Payment
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.MarkPaid(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) unitBudget(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.URL.Query().Get("department"))
	if err != nil {
		return httpx.BadRequest("request.query", "Pilih unit (departemen).")
	}
	b, err := h.svc.UnitBudget(r.Context(), api.CompanyID(r), id, civil.Today(civil.Jakarta))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

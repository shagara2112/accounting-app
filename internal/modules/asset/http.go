package asset

import (
	"net/http"
	"strconv"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes: register aset, kategori, jurnal penyusutan bulanan, pelepasan, pencocokan dengan
// buku besar, dan daftar penyusutan fiskal. Posting dan void jurnal wajib Idempotency-Key.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	view := func(path string, fn httpx.Handler) api.Route {
		return api.Route{Method: http.MethodGet, Path: path, Access: api.Permission, Perm: rbac.AssetView, License: license.ModeRead, Handle: fn}
	}
	write := func(method, path, perm string, idempotent bool, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: license.ModeWrite, Idempotent: idempotent, Handle: fn}
	}
	return []api.Route{
		view("/api/v1/assets", h.assets),
		write(http.MethodPost, "/api/v1/assets", rbac.AssetCreate, false, h.createAsset),
		write(http.MethodPost, "/api/v1/assets/preview", rbac.AssetView, false, h.preview),
		view("/api/v1/assets/{id}", h.asset),
		write(http.MethodPut, "/api/v1/assets/{id}", rbac.AssetCreate, false, h.updateAsset),
		write(http.MethodDelete, "/api/v1/assets/{id}", rbac.AssetVoid, false, h.deleteAsset),
		write(http.MethodPost, "/api/v1/assets/{id}/dispose", rbac.AssetCreate, true, h.dispose),
		view("/api/v1/assets/disposals/{id}", h.disposal),
		write(http.MethodPost, "/api/v1/assets/disposals/{id}/void", rbac.AssetVoid, true, h.voidDisposal),

		view("/api/v1/assets/categories", h.categories),
		write(http.MethodPost, "/api/v1/assets/categories", rbac.AssetCreate, false, h.createCategory),
		write(http.MethodPost, "/api/v1/assets/categories/defaults", rbac.AssetCreate, false, h.defaultCategories),
		write(http.MethodPut, "/api/v1/assets/categories/{id}", rbac.AssetCreate, false, h.updateCategory),

		view("/api/v1/assets/depreciation", h.runs),
		view("/api/v1/assets/depreciation/preview", h.previewRun),
		write(http.MethodPost, "/api/v1/assets/depreciation", rbac.AssetCreate, true, h.postRun),
		view("/api/v1/assets/depreciation/{id}", h.run),
		write(http.MethodPost, "/api/v1/assets/depreciation/{id}/void", rbac.AssetVoid, true, h.voidRun),

		view("/api/v1/assets/reconciliation", h.reconciliation),
		view("/api/v1/assets/fiscal", h.fiscal),
	}
}

type handlers struct{ svc Service }

func today() civil.Date { return civil.Today(civil.Jakarta) }

func (h handlers) assets(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Assets(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) asset(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	a, err := h.svc.Asset(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h handlers) createAsset(w http.ResponseWriter, r *http.Request) error {
	var in AssetInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	a, err := h.svc.CreateAsset(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h handlers) updateAsset(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in AssetInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	a, err := h.svc.UpdateAsset(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h handlers) deleteAsset(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	version, _ := strconv.Atoi(r.URL.Query().Get("version"))
	if err := h.svc.DeleteAsset(r.Context(), api.CompanyID(r), id, version, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) preview(w http.ResponseWriter, r *http.Request) error {
	var in AssetInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p, err := h.svc.Preview(r.Context(), api.CompanyID(r), in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, p)
}

func (h handlers) dispose(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in DisposalInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	a, err := h.svc.Dispose(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h handlers) disposal(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	a, err := h.svc.DisposalAsset(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

type voidRequest struct {
	Date   civil.Date `json:"date"`
	Reason string     `json:"reason"`
}

func (h handlers) voidDisposal(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req voidRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	a, err := h.svc.VoidDisposal(r.Context(), api.CompanyID(r), id, req.Date, req.Reason, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h handlers) categories(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Categories(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list, "fiscalGroups": FiscalGroups})
}

func (h handlers) createCategory(w http.ResponseWriter, r *http.Request) error {
	var in CategoryInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	c, err := h.svc.SaveCategory(r.Context(), api.CompanyID(r), nil, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h handlers) updateCategory(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in CategoryInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	c, err := h.svc.SaveCategory(r.Context(), api.CompanyID(r), &id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

func (h handlers) defaultCategories(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.DefaultCategories(r.Context(), api.CompanyID(r), auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list, "fiscalGroups": FiscalGroups})
}

func (h handlers) runs(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Runs(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func monthParam(r *http.Request) (civil.Date, error) {
	s := r.URL.Query().Get("month")
	if s == "" {
		return today().MonthStart(), nil
	}
	m, err := civil.ParseMonth(s)
	if err != nil {
		return civil.Date{}, httpx.BadRequest("query.month", "Format bulan harus YYYY-MM.")
	}
	return m, nil
}

func (h handlers) previewRun(w http.ResponseWriter, r *http.Request) error {
	month, err := monthParam(r)
	if err != nil {
		return err
	}
	p, err := h.svc.PreviewRun(r.Context(), api.CompanyID(r), month)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, p)
}

func (h handlers) postRun(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Month string `json:"month"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	month, err := civil.ParseMonth(req.Month)
	if err != nil {
		msg := "Pilih bulan penyusutan."
		return httpx.Unprocessable("depreciation.month", msg).WithFields(map[string]string{"month": msg})
	}
	if month.After(today().MonthStart()) {
		msg := "Penyusutan bulan depan belum bisa diposting."
		return httpx.Unprocessable("depreciation.month", msg).WithFields(map[string]string{"month": msg})
	}
	run, err := h.svc.PostRun(r.Context(), api.CompanyID(r), month, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, run)
}

func (h handlers) run(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	run, err := h.svc.Run(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, run)
}

func (h handlers) voidRun(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req voidRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	run, err := h.svc.VoidRun(r.Context(), api.CompanyID(r), id, req.Date, req.Reason, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, run)
}

func (h handlers) reconciliation(w http.ResponseWriter, r *http.Request) error {
	rec, err := h.svc.Reconciliation(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rec)
}

func (h handlers) fiscal(w http.ResponseWriter, r *http.Request) error {
	year := today().Year()
	if s := r.URL.Query().Get("year"); s != "" {
		y, err := strconv.Atoi(s)
		if err != nil || y < 2000 || y > 2100 {
			return httpx.BadRequest("query.year", "Tahun tidak valid.")
		}
		year = y
	}
	rep, err := h.svc.FiscalReport(r.Context(), api.CompanyID(r), year)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rep)
}

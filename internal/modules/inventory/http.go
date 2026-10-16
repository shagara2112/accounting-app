package inventory

import (
	"net/http"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes: master produk, posisi stok, kartu stok, dan dokumen persediaan. Membuat dan
// membatalkan dokumen persediaan wajib membawa Idempotency-Key.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	view := func(path string, fn httpx.Handler) api.Route {
		return api.Route{Method: http.MethodGet, Path: path, Access: api.Permission, Perm: rbac.InventoryView, License: license.ModeRead, Handle: fn}
	}
	write := func(method, path, perm string, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: license.ModeWrite,
			Idempotent: method == http.MethodPost && path != "/api/v1/inventory/products", Handle: fn}
	}
	return []api.Route{
		view("/api/v1/inventory/products", h.products),
		write(http.MethodPost, "/api/v1/inventory/products", rbac.InventoryCreate, h.createProduct),
		view("/api/v1/inventory/products/{id}", h.product),
		write(http.MethodPut, "/api/v1/inventory/products/{id}", rbac.InventoryCreate, h.updateProduct),
		view("/api/v1/inventory/products/{id}/card", h.card),
		view("/api/v1/inventory/stock", h.stock),
		view("/api/v1/inventory/adjustments", h.adjustments),
		write(http.MethodPost, "/api/v1/inventory/adjustments", rbac.InventoryCreate, h.createAdjustment),
		view("/api/v1/inventory/adjustments/{id}", h.adjustment),
		write(http.MethodPost, "/api/v1/inventory/adjustments/{id}/void", rbac.InventoryVoid, h.voidAdjustment),
	}
}

type handlers struct{ svc Service }

func (h handlers) products(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	list, err := h.svc.Products(r.Context(), api.CompanyID(r), ProductFilter{
		Kind: q.Get("kind"), Search: q.Get("q"), IncludeInactive: q.Get("all") == "1",
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) product(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	p, err := h.svc.Product(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, p)
}

func (h handlers) createProduct(w http.ResponseWriter, r *http.Request) error {
	var in ProductInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p, err := h.svc.CreateProduct(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, p)
}

func (h handlers) updateProduct(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in ProductInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p, err := h.svc.UpdateProduct(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, p)
}

func (h handlers) card(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	t := civil.Today(civil.Jakarta)
	from, err := api.DateQuery(r, "from", t.MonthStart())
	if err != nil {
		return err
	}
	to, err := api.DateQuery(r, "to", t)
	if err != nil {
		return err
	}
	c, err := h.svc.Card(r.Context(), api.CompanyID(r), id, from, to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

func (h handlers) stock(w http.ResponseWriter, r *http.Request) error {
	asOf, err := api.DateQuery(r, "date", civil.Today(civil.Jakarta))
	if err != nil {
		return err
	}
	rep, err := h.svc.Stock(r.Context(), api.CompanyID(r), asOf)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h handlers) adjustments(w http.ResponseWriter, r *http.Request) error {
	t := civil.Today(civil.Jakarta)
	from, err := api.DateQuery(r, "from", t.MonthStart())
	if err != nil {
		return err
	}
	to, err := api.DateQuery(r, "to", t.MonthEnd())
	if err != nil {
		return err
	}
	list, err := h.svc.Adjustments(r.Context(), api.CompanyID(r), from, to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) adjustment(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	a, err := h.svc.Adjustment(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h handlers) createAdjustment(w http.ResponseWriter, r *http.Request) error {
	var in AdjustmentInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	a, err := h.svc.CreateAdjustment(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h handlers) voidAdjustment(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Date   civil.Date `json:"date"`
		Reason string     `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	a, err := h.svc.VoidAdjustment(r.Context(), api.CompanyID(r), id, req.Date, req.Reason, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

package cash

import (
	"net/http"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes mengembalikan endpoint kas dan bank. POST transaksi dan void wajib Idempotency-Key.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	return []api.Route{
		{Method: http.MethodGet, Path: "/api/v1/cash/accounts", Access: api.Permission, Perm: rbac.CashView, License: license.ModeRead, Handle: h.accounts},
		{Method: http.MethodPost, Path: "/api/v1/cash/accounts", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Idempotent: true, Handle: h.register},
		{Method: http.MethodGet, Path: "/api/v1/cash/transactions", Access: api.Permission, Perm: rbac.CashView, License: license.ModeRead, Handle: h.list},
		{Method: http.MethodPost, Path: "/api/v1/cash/transactions", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Idempotent: true, Handle: h.record},
		{Method: http.MethodGet, Path: "/api/v1/cash/transactions/{id}", Access: api.Permission, Perm: rbac.CashView, License: license.ModeRead, Handle: h.detail},
		{Method: http.MethodPost, Path: "/api/v1/cash/transactions/{id}/void", Access: api.Permission, Perm: rbac.CashVoid, License: license.ModeWrite, Idempotent: true, Handle: h.void},
	}
}

type handlers struct{ svc Service }

func (h handlers) accounts(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Accounts(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) register(w http.ResponseWriter, r *http.Request) error {
	var in AccountInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	a, err := h.svc.RegisterAccount(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h handlers) list(w http.ResponseWriter, r *http.Request) error {
	t := civil.Today(civil.Jakarta)
	from, err := api.DateQuery(r, "from", t.MonthStart())
	if err != nil {
		return err
	}
	to, err := api.DateQuery(r, "to", t.MonthEnd())
	if err != nil {
		return err
	}
	acc, err := api.UUIDQuery(r, "account")
	if err != nil {
		return err
	}
	list, err := h.svc.Transactions(r.Context(), api.CompanyID(r), Filter{From: from, To: to, AccountID: acc})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) record(w http.ResponseWriter, r *http.Request) error {
	var in TransactionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	t, err := h.svc.Record(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, t)
}

func (h handlers) detail(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	t, err := h.svc.Transaction(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
}

func (h handlers) void(w http.ResponseWriter, r *http.Request) error {
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
	t, err := h.svc.Void(r.Context(), api.CompanyID(r), id, req.Date, req.Reason, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
}

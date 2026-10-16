package cash

import (
	"net/http"

	"github.com/google/uuid"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/export"
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
		{Method: http.MethodPost, Path: "/api/v1/cash/drafts", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Idempotent: true, Handle: h.createDraft},
		{Method: http.MethodPut, Path: "/api/v1/cash/drafts/{id}", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Handle: h.updateDraft},
		{Method: http.MethodDelete, Path: "/api/v1/cash/drafts/{id}", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Handle: h.deleteDraft},
		{Method: http.MethodPost, Path: "/api/v1/cash/drafts/{id}/post", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Idempotent: true, Handle: h.postDraft},

		{Method: http.MethodGet, Path: "/api/v1/cash/reconciliations", Access: api.Permission, Perm: rbac.CashView, License: license.ModeRead, Handle: h.recons},
		{Method: http.MethodPost, Path: "/api/v1/cash/reconciliations", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Idempotent: true, Handle: h.startRecon},
		{Method: http.MethodGet, Path: "/api/v1/cash/reconciliations/{id}", Access: api.Permission, Perm: rbac.CashView, License: license.ModeRead, Handle: h.recon},
		{Method: http.MethodPut, Path: "/api/v1/cash/reconciliations/{id}", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Handle: h.updateRecon},
		{Method: http.MethodPut, Path: "/api/v1/cash/reconciliations/{id}/items", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Handle: h.clearLines},
		{Method: http.MethodPost, Path: "/api/v1/cash/reconciliations/{id}/close", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Idempotent: true, Handle: h.closeRecon},
		{Method: http.MethodPost, Path: "/api/v1/cash/reconciliations/{id}/reopen", Access: api.Permission, Perm: rbac.CashVoid, License: license.ModeWrite, Idempotent: true, Handle: h.reopenRecon},
		{Method: http.MethodDelete, Path: "/api/v1/cash/reconciliations/{id}", Access: api.Permission, Perm: rbac.CashCreate, License: license.ModeWrite, Handle: h.deleteRecon},
		{Method: http.MethodGet, Path: "/api/v1/cash/reconciliations/{id}/export", Access: api.Permission, Perm: rbac.DataExport, License: license.ModeExport, Handle: h.exportRecon},
	}
}

func (h handlers) recons(w http.ResponseWriter, r *http.Request) error {
	var acc *uuid.UUID
	if v := r.URL.Query().Get("account"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("request.account", "Rekening tidak valid.")
		}
		acc = &id
	}
	list, err := h.svc.Reconciliations(r.Context(), api.CompanyID(r), acc)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) startRecon(w http.ResponseWriter, r *http.Request) error {
	var in ReconInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.StartReconciliation(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, d)
}

func (h handlers) recon(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	d, err := h.svc.Reconciliation(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) updateRecon(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in ReconInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.UpdateReconciliation(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) clearLines(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		LineIDs []uuid.UUID `json:"lineIds"`
		Cleared bool        `json:"cleared"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d, err := h.svc.SetCleared(r.Context(), api.CompanyID(r), id, in.LineIDs, in.Cleared, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) closeRecon(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	d, err := h.svc.CloseReconciliation(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) reopenRecon(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	d, err := h.svc.ReopenReconciliation(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) deleteRecon(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.DeleteReconciliation(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) exportRecon(w http.ResponseWriter, r *http.Request) error {
	if !api.Can(r, rbac.CashView) {
		return httpx.Forbidden("rbac.denied", "Anda tidak punya izin melihat kas dan bank.")
	}
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	d, err := h.svc.Reconciliation(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	t := ReconTable(d)
	file := "rekonsiliasi-" + d.AccountCode + "-" + d.StatementDate.String()
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.csv"`)
		return export.WriteCSV(w, t)
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.xlsx"`)
	return export.WriteXLSX(w, t)
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
	list, err := h.svc.Transactions(r.Context(), api.CompanyID(r), Filter{From: from, To: to, AccountID: acc, DraftsOnly: r.URL.Query().Get("drafts") == "1"})
	if err != nil {
		return err
	}
	drafts, amount, err := h.svc.DraftSummary(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list, "drafts": drafts, "draftAmount": amount})
}

func (h handlers) createDraft(w http.ResponseWriter, r *http.Request) error {
	var in TransactionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	t, err := h.svc.SaveDraft(r.Context(), api.CompanyID(r), nil, in, 0, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, t)
}

func (h handlers) updateDraft(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		TransactionInput
		Version int `json:"version"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	t, err := h.svc.SaveDraft(r.Context(), api.CompanyID(r), &id, req.TransactionInput, req.Version, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
}

func (h handlers) deleteDraft(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.DeleteDraft(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r), api.Can(r, rbac.CashVoid)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) postDraft(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	t, err := h.svc.PostDraft(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
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

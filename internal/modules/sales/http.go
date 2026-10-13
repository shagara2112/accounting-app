package sales

import (
	"net/http"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes mengembalikan endpoint penjualan. Semua POST yang membuat atau membatalkan dokumen
// wajib membawa Idempotency-Key.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	view := func(path string, fn httpx.Handler) api.Route {
		return api.Route{Method: http.MethodGet, Path: path, Access: api.Permission, Perm: rbac.SalesView, License: license.ModeRead, Handle: fn}
	}
	write := func(method, path, perm string, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: license.ModeWrite,
			Idempotent: method == http.MethodPost, Handle: fn}
	}
	return []api.Route{
		view("/api/v1/sales/invoices", h.invoices),
		write(http.MethodPost, "/api/v1/sales/invoices", rbac.SalesCreate, h.createInvoice),
		view("/api/v1/sales/invoices/{id}", h.invoice),
		write(http.MethodPut, "/api/v1/sales/invoices/{id}", rbac.SalesCreate, h.updateInvoice),
		write(http.MethodDelete, "/api/v1/sales/invoices/{id}", rbac.SalesCreate, h.deleteInvoice),
		write(http.MethodPost, "/api/v1/sales/invoices/{id}/post", rbac.SalesCreate, h.postInvoice),
		write(http.MethodPost, "/api/v1/sales/invoices/{id}/void", rbac.SalesVoid, h.voidInvoice),
		view("/api/v1/sales/open-invoices", h.openInvoices),
		view("/api/v1/sales/receipts", h.receipts),
		write(http.MethodPost, "/api/v1/sales/receipts", rbac.SalesCreate, h.createReceipt),
		view("/api/v1/sales/receipts/{id}", h.receipt),
		write(http.MethodPost, "/api/v1/sales/receipts/{id}/void", rbac.SalesVoid, h.voidReceipt),
		view("/api/v1/sales/aging", h.aging),
	}
}

type handlers struct{ svc Service }

type voidRequest struct {
	Date   civil.Date `json:"date"`
	Reason string     `json:"reason"`
}

func monthRange(r *http.Request) (civil.Date, civil.Date, error) {
	t := civil.Today(civil.Jakarta)
	from, err := api.DateQuery(r, "from", t.MonthStart())
	if err != nil {
		return from, from, err
	}
	to, err := api.DateQuery(r, "to", t.MonthEnd())
	return from, to, err
}

func (h handlers) invoices(w http.ResponseWriter, r *http.Request) error {
	from, to, err := monthRange(r)
	if err != nil {
		return err
	}
	customer, err := api.UUIDQuery(r, "customer")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	list, err := h.svc.Invoices(r.Context(), api.CompanyID(r), InvoiceFilter{
		From: from, To: to, State: q.Get("state"), CustomerID: customer, Search: q.Get("q"),
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) invoice(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	inv, err := h.svc.Invoice(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h handlers) createInvoice(w http.ResponseWriter, r *http.Request) error {
	var in InvoiceInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	inv, err := h.svc.CreateInvoice(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, inv)
}

func (h handlers) updateInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in InvoiceInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	inv, err := h.svc.UpdateInvoice(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r), api.Can(r, rbac.SalesVoid))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h handlers) deleteInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.DeleteInvoice(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r), api.Can(r, rbac.SalesVoid)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) postInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	inv, err := h.svc.PostInvoice(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h handlers) voidInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req voidRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	inv, err := h.svc.VoidInvoice(r.Context(), api.CompanyID(r), id, req.Date, req.Reason, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h handlers) openInvoices(w http.ResponseWriter, r *http.Request) error {
	customer, err := api.UUIDQuery(r, "customer")
	if err != nil {
		return err
	}
	list, err := h.svc.OpenInvoices(r.Context(), api.CompanyID(r), customer)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) receipts(w http.ResponseWriter, r *http.Request) error {
	from, to, err := monthRange(r)
	if err != nil {
		return err
	}
	customer, err := api.UUIDQuery(r, "customer")
	if err != nil {
		return err
	}
	list, err := h.svc.Receipts(r.Context(), api.CompanyID(r), ReceiptFilter{From: from, To: to, CustomerID: customer})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) receipt(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	rc, err := h.svc.Receipt(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rc)
}

func (h handlers) createReceipt(w http.ResponseWriter, r *http.Request) error {
	var in ReceiptInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	rc, err := h.svc.CreateReceipt(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, rc)
}

func (h handlers) voidReceipt(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req voidRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	rc, err := h.svc.VoidReceipt(r.Context(), api.CompanyID(r), id, req.Date, req.Reason, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rc)
}

func (h handlers) aging(w http.ResponseWriter, r *http.Request) error {
	asOf, err := api.DateQuery(r, "date", civil.Today(civil.Jakarta))
	if err != nil {
		return err
	}
	a, err := h.svc.Aging(r.Context(), api.CompanyID(r), asOf)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

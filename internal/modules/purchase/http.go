package purchase

import (
	"net/http"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes mengembalikan endpoint pembelian. Semua POST yang membuat atau membatalkan dokumen
// wajib membawa Idempotency-Key.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	view := func(path string, fn httpx.Handler) api.Route {
		return api.Route{Method: http.MethodGet, Path: path, Access: api.Permission, Perm: rbac.PurchaseView, License: license.ModeRead, Handle: fn}
	}
	write := func(method, path, perm string, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: license.ModeWrite,
			Idempotent: method == http.MethodPost, Handle: fn}
	}
	return []api.Route{
		view("/api/v1/purchase/bills", h.bills),
		write(http.MethodPost, "/api/v1/purchase/bills", rbac.PurchaseCreate, h.createBill),
		view("/api/v1/purchase/bills/{id}", h.bill),
		write(http.MethodPut, "/api/v1/purchase/bills/{id}", rbac.PurchaseCreate, h.updateBill),
		write(http.MethodDelete, "/api/v1/purchase/bills/{id}", rbac.PurchaseCreate, h.deleteBill),
		write(http.MethodPost, "/api/v1/purchase/bills/{id}/post", rbac.PurchaseCreate, h.postBill),
		write(http.MethodPost, "/api/v1/purchase/bills/{id}/void", rbac.PurchaseVoid, h.voidBill),
		view("/api/v1/purchase/open-bills", h.openBills),
		view("/api/v1/purchase/payments", h.payments),
		write(http.MethodPost, "/api/v1/purchase/payments", rbac.PurchaseCreate, h.createPayment),
		view("/api/v1/purchase/payments/{id}", h.payment),
		write(http.MethodPost, "/api/v1/purchase/payments/{id}/void", rbac.PurchaseVoid, h.voidPayment),
		view("/api/v1/purchase/aging", h.aging),
		view("/api/v1/purchase/advances", h.advances),
		write(http.MethodPost, "/api/v1/purchase/advance-refunds", rbac.PurchaseCreate, h.refundAdvance),
		view("/api/v1/purchase/debit-notes", h.debitNotes),
		write(http.MethodPost, "/api/v1/purchase/debit-notes", rbac.PurchaseCreate, h.createDebitNote),
		view("/api/v1/purchase/debit-notes/{id}", h.debitNote),
		write(http.MethodPost, "/api/v1/purchase/debit-notes/{id}/void", rbac.PurchaseVoid, h.voidDebitNote),
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

func (h handlers) bills(w http.ResponseWriter, r *http.Request) error {
	from, to, err := monthRange(r)
	if err != nil {
		return err
	}
	supplier, err := api.UUIDQuery(r, "supplier")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	list, err := h.svc.Bills(r.Context(), api.CompanyID(r), BillFilter{
		From: from, To: to, State: q.Get("state"), SupplierID: supplier, Search: q.Get("q"),
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) bill(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	inv, err := h.svc.Bill(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h handlers) createBill(w http.ResponseWriter, r *http.Request) error {
	var in BillInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	inv, err := h.svc.CreateBill(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, inv)
}

func (h handlers) updateBill(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in BillInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	inv, err := h.svc.UpdateBill(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r), api.Can(r, rbac.PurchaseVoid))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h handlers) deleteBill(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.DeleteBill(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r), api.Can(r, rbac.PurchaseVoid)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) postBill(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	inv, err := h.svc.PostBill(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h handlers) voidBill(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req voidRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	inv, err := h.svc.VoidBill(r.Context(), api.CompanyID(r), id, req.Date, req.Reason, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h handlers) openBills(w http.ResponseWriter, r *http.Request) error {
	supplier, err := api.UUIDQuery(r, "supplier")
	if err != nil {
		return err
	}
	list, err := h.svc.OpenBills(r.Context(), api.CompanyID(r), supplier)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) payments(w http.ResponseWriter, r *http.Request) error {
	from, to, err := monthRange(r)
	if err != nil {
		return err
	}
	supplier, err := api.UUIDQuery(r, "supplier")
	if err != nil {
		return err
	}
	list, err := h.svc.Payments(r.Context(), api.CompanyID(r), PaymentFilter{From: from, To: to, SupplierID: supplier})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) payment(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	rc, err := h.svc.Payment(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rc)
}

func (h handlers) createPayment(w http.ResponseWriter, r *http.Request) error {
	var in PaymentInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	rc, err := h.svc.CreatePayment(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, rc)
}

func (h handlers) voidPayment(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req voidRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	rc, err := h.svc.VoidPayment(r.Context(), api.CompanyID(r), id, req.Date, req.Reason, auth.ActorFrom(r))
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

func (h handlers) debitNotes(w http.ResponseWriter, r *http.Request) error {
	from, to, err := monthRange(r)
	if err != nil {
		return err
	}
	supplier, err := api.UUIDQuery(r, "supplier")
	if err != nil {
		return err
	}
	list, err := h.svc.DebitNotes(r.Context(), api.CompanyID(r), DebitNoteFilter{From: from, To: to, SupplierID: supplier})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) debitNote(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	n, err := h.svc.DebitNote(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, n)
}

func (h handlers) createDebitNote(w http.ResponseWriter, r *http.Request) error {
	var in DebitNoteInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	n, err := h.svc.CreateDebitNote(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, n)
}

func (h handlers) voidDebitNote(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req voidRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	n, err := h.svc.VoidDebitNote(r.Context(), api.CompanyID(r), id, req.Date, req.Reason, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, n)
}

// advances: saldo uang muka per pemasok; ?supplier= untuk satu pemasok.
func (h handlers) advances(w http.ResponseWriter, r *http.Request) error {
	supplier, err := api.UUIDQuery(r, "supplier")
	if err != nil {
		return err
	}
	if supplier != nil {
		bal, err := h.svc.AdvanceBalance(r.Context(), api.CompanyID(r), *supplier)
		if err != nil {
			return err
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": []map[string]any{{"contactId": *supplier, "balance": bal}}})
	}
	m, err := h.svc.AdvanceBalances(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(m))
	for id, bal := range m {
		items = append(items, map[string]any{"contactId": id, "balance": bal})
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h handlers) refundAdvance(w http.ResponseWriter, r *http.Request) error {
	var in AdvanceRefundInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p, err := h.svc.RefundAdvance(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, p)
}

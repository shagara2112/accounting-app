package docs

import (
	"mime"
	"net/http"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

func Routes(s *Service) []api.Route {
	h := handlers{s: s}
	return []api.Route{
		{Method: http.MethodGet, Path: "/api/v1/sales/invoices/{id}/pdf", Access: api.Permission, Perm: rbac.SalesView, License: license.ModeRead, Handle: h.pdf},
		{Method: http.MethodGet, Path: "/api/v1/sales/invoices/{id}/email", Access: api.Permission, Perm: rbac.SalesView, License: license.ModeRead, Handle: h.draft},
		// Idempoten: klik ganda tidak mengirim email dua kali.
		{Method: http.MethodPost, Path: "/api/v1/sales/invoices/{id}/email", Access: api.Permission, Perm: rbac.SalesCreate, License: license.ModeWrite, Idempotent: true, Handle: h.email},
	}
}

type handlers struct{ s *Service }

func (h handlers) pdf(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	body, name, err := h.s.InvoicePDF(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	disp := "inline"
	if r.URL.Query().Get("unduh") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": name}))
	_, err = w.Write(body)
	return err
}

func (h handlers) draft(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	d, err := h.s.InvoiceEmailDraft(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) email(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in EmailInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := h.s.EmailInvoice(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r)); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

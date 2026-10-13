package coretax

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes: faktur keluaran, ekspor XML, rekap PPN dan PPh. Ekspor memakai mode lisensi Export,
// sehingga tetap bisa dipakai walau lisensi berakhir (PRD: ekspor tidak pernah dikunci).
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	route := func(method, path, perm string, mode license.Mode, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: mode, Handle: fn}
	}
	return []api.Route{
		route(http.MethodGet, "/api/v1/tax/settings", rbac.TaxView, license.ModeRead, h.settings),
		route(http.MethodPut, "/api/v1/tax/settings", rbac.TaxCreate, license.ModeWrite, h.setSettings),
		route(http.MethodGet, "/api/v1/tax/trx-codes", rbac.TaxView, license.ModeRead, h.trxCodes),
		route(http.MethodGet, "/api/v1/tax/output-invoices", rbac.TaxView, license.ModeRead, h.outputs),
		route(http.MethodPut, "/api/v1/tax/output-invoices/trx-code", rbac.TaxCreate, license.ModeWrite, h.setTrxCode),
		route(http.MethodPost, "/api/v1/tax/output-invoices/export", rbac.TaxExport, license.ModeExport, h.export),
		route(http.MethodPost, "/api/v1/tax/output-invoices/import", rbac.TaxCreate, license.ModeWrite, h.importNumbers),
		route(http.MethodPost, "/api/v1/tax/output-invoices/{id}/approve", rbac.TaxCreate, license.ModeWrite, h.approve),
		route(http.MethodPost, "/api/v1/tax/output-invoices/{id}/cancel", rbac.TaxCreate, license.ModeWrite, h.cancel),
		route(http.MethodPost, "/api/v1/tax/output-invoices/{id}/replace", rbac.TaxCreate, license.ModeWrite, h.replace),
		route(http.MethodGet, "/api/v1/tax/exports", rbac.TaxView, license.ModeRead, h.batches),
		route(http.MethodGet, "/api/v1/tax/exports/{id}", rbac.TaxExport, license.ModeExport, h.download),
		route(http.MethodGet, "/api/v1/tax/vat-recap", rbac.TaxView, license.ModeRead, h.vatRecap),
		route(http.MethodGet, "/api/v1/tax/withholding", rbac.TaxView, license.ModeRead, h.withholding),
	}
}

type handlers struct{ svc Service }

func month(r *http.Request) (civil.Date, error) {
	v := r.URL.Query().Get("month")
	if v == "" {
		return civil.Today(civil.Jakarta).MonthStart(), nil
	}
	m, err := civil.ParseMonth(v)
	if err != nil {
		return civil.Date{}, httpx.BadRequest("request.query", err.Error())
	}
	return m, nil
}

func (h handlers) settings(w http.ResponseWriter, r *http.Request) error {
	s, err := h.svc.Settings(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, s)
}

func (h handlers) setSettings(w http.ResponseWriter, r *http.Request) error {
	var in Settings
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	s, err := h.svc.SetSettings(r.Context(), api.CompanyID(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, s)
}

func (h handlers) trxCodes(w http.ResponseWriter, _ *http.Request) error {
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": TrxCodes})
}

func (h handlers) outputs(w http.ResponseWriter, r *http.Request) error {
	m, err := month(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Outputs(r.Context(), api.CompanyID(r), m)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, list)
}

func (h handlers) setTrxCode(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs     []uuid.UUID `json:"ids"`
		TrxCode string      `json:"trxCode"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := h.svc.SetTrxCode(r.Context(), api.CompanyID(r), req.IDs, req.TrxCode, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func writeXML(w http.ResponseWriter, b Batch) error {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, b.FileName))
	w.Header().Set("X-Batch-Id", b.ID.String())
	w.Header().Set("X-Invoice-Count", strconv.Itoa(b.InvoiceCount))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(b.Content)
	return err
}

func (h handlers) export(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Month string      `json:"month"`
		IDs   []uuid.UUID `json:"ids"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	m, err := civil.ParseMonth(req.Month)
	if err != nil {
		return httpx.BadRequest("request.body", err.Error())
	}
	b, err := h.svc.Export(r.Context(), api.CompanyID(r), m, req.IDs, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return writeXML(w, b)
}

func (h handlers) batches(w http.ResponseWriter, r *http.Request) error {
	m, err := month(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Batches(r.Context(), api.CompanyID(r), m)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) download(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	b, err := h.svc.Batch(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	return writeXML(w, b)
}

func (h handlers) importNumbers(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		CSV string `json:"csv"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, err := h.svc.ImportNumbers(r.Context(), api.CompanyID(r), req.CSV, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h handlers) approve(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Number string     `json:"number"`
		Date   civil.Date `json:"date"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := h.svc.Approve(r.Context(), api.CompanyID(r), id, req.Number, req.Date, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) cancel(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Note string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := h.svc.Cancel(r.Context(), api.CompanyID(r), id, req.Note, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) replace(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		ReplacementID uuid.UUID `json:"replacementId"`
		Note          string    `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := h.svc.Replace(r.Context(), api.CompanyID(r), id, req.ReplacementID, req.Note, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) vatRecap(w http.ResponseWriter, r *http.Request) error {
	year := civil.Today(civil.Jakarta).Year()
	if v := r.URL.Query().Get("year"); v != "" {
		y, err := strconv.Atoi(v)
		if err != nil {
			return httpx.BadRequest("request.query", "Tahun tidak valid.")
		}
		year = y
	}
	rec, err := h.svc.VATRecap(r.Context(), api.CompanyID(r), year)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rec)
}

func (h handlers) withholding(w http.ResponseWriter, r *http.Request) error {
	m, err := month(r)
	if err != nil {
		return err
	}
	rec, err := h.svc.Withholding(r.Context(), api.CompanyID(r), m)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rec)
}

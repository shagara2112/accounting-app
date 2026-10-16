package coretax

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/export"
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
		route(http.MethodGet, "/api/v1/tax/digunggung", rbac.TaxView, license.ModeRead, h.digunggung),
		route(http.MethodGet, "/api/v1/tax/digunggung/export", rbac.TaxExport, license.ModeExport, h.digunggungExport),

		route(http.MethodGet, "/api/v1/tax/input-invoices", rbac.TaxView, license.ModeRead, h.inputs),
		route(http.MethodPost, "/api/v1/tax/input-invoices/import", rbac.TaxCreate, license.ModeWrite, h.importInputs),
		route(http.MethodPut, "/api/v1/tax/input-invoices/{id}", rbac.TaxCreate, license.ModeWrite, h.updateInput),
		idempotent(route(http.MethodPost, "/api/v1/tax/input-invoices/{id}/creditable", rbac.TaxCreate, license.ModeWrite, h.setCreditable)),

		route(http.MethodGet, "/api/v1/tax/bupot-ref", rbac.CompanyView, license.ModeRead, h.bupotRef),
		route(http.MethodGet, "/api/v1/tax/withholding-objects", rbac.TaxView, license.ModeRead, h.withholdingObjects),
		route(http.MethodPut, "/api/v1/tax/withholding-objects", rbac.TaxCreate, license.ModeWrite, h.setWithholdingObjects),
		route(http.MethodGet, "/api/v1/tax/slips", rbac.TaxView, license.ModeRead, h.slips),
		route(http.MethodPut, "/api/v1/tax/slips/object", rbac.TaxCreate, license.ModeWrite, h.setSlipObject),
		route(http.MethodPost, "/api/v1/tax/slips/export", rbac.TaxExport, license.ModeExport, h.exportSlips),
		route(http.MethodPost, "/api/v1/tax/slips/import", rbac.TaxCreate, license.ModeWrite, h.importSlips),
		route(http.MethodPut, "/api/v1/tax/slips/{id}", rbac.TaxCreate, license.ModeWrite, h.updateSlip),
		route(http.MethodPost, "/api/v1/tax/slips/{id}/approve", rbac.TaxCreate, license.ModeWrite, h.approveSlip),
		route(http.MethodPost, "/api/v1/tax/slips/{id}/reset", rbac.TaxCreate, license.ModeWrite, h.resetSlip(false)),
		route(http.MethodPost, "/api/v1/tax/slips/{id}/cancel", rbac.TaxCreate, license.ModeWrite, h.resetSlip(true)),
	}
}

// idempotent menandai POST yang memposting jurnal: wajib header Idempotency-Key.
func idempotent(rt api.Route) api.Route {
	rt.Idempotent = true
	return rt
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
	kinds := []string{BatchFaktur}
	if r.URL.Query().Get("kind") == "bupot" {
		kinds = []string{BatchBPPU, BatchBPNR}
	}
	list, err := h.svc.Batches(r.Context(), api.CompanyID(r), m, kinds)
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

func (h handlers) digunggung(w http.ResponseWriter, r *http.Request) error {
	m, err := month(r)
	if err != nil {
		return err
	}
	d, err := h.svc.Digunggung(r.Context(), api.CompanyID(r), m)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h handlers) digunggungExport(w http.ResponseWriter, r *http.Request) error {
	m, err := month(r)
	if err != nil {
		return err
	}
	d, err := h.svc.Digunggung(r.Context(), api.CompanyID(r), m)
	if err != nil {
		return err
	}
	t := DigunggungTable(d, m)
	file := "ppn-digunggung-" + d.Month
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.csv"`)
		return export.WriteCSV(w, t)
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.xlsx"`)
	return export.WriteXLSX(w, t)
}

func (h handlers) inputs(w http.ResponseWriter, r *http.Request) error {
	m, err := month(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Inputs(r.Context(), api.CompanyID(r), m)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, list)
}

func (h handlers) updateInput(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in InputUpdate
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	out, err := h.svc.UpdateInput(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h handlers) setCreditable(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Creditable bool       `json:"creditable"`
		Date       civil.Date `json:"date"`
		Reason     string     `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	out, err := h.svc.SetCreditable(r.Context(), api.CompanyID(r), id, req.Creditable, req.Date, req.Reason, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

// maxUpload: daftar faktur masukan atau bukti potong dari Coretax (Excel/CSV).
const maxUpload = 5 << 20

// upload membaca multipart: file dan field month (opsional, YYYY-MM).
func upload(r *http.Request) (name string, data []byte, m civil.Date, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", nil, m, httpx.BadRequest("tax.upload_form", "Kirim berkas sebagai multipart/form-data.")
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, m, httpx.BadRequest("tax.upload_form", "Formulir unggah tidak terbaca.")
		}
		switch part.FormName() {
		case "month":
			v, _ := io.ReadAll(io.LimitReader(part, 16))
			if s := strings.TrimSpace(string(v)); s != "" {
				if m, err = civil.ParseMonth(s); err != nil {
					return "", nil, m, httpx.Unprocessable("tax.upload_month", "Masa tidak valid (YYYY-MM).")
				}
			}
		case "file":
			name = part.FileName()
			if data, err = io.ReadAll(io.LimitReader(part, maxUpload+1)); err != nil {
				return "", nil, m, httpx.BadRequest("tax.upload_form", "Berkas tidak terbaca.")
			}
			if len(data) > maxUpload {
				return "", nil, m, httpx.Unprocessable("tax.upload_size", "Ukuran berkas maksimal 5 MB.")
			}
		}
	}
	if name == "" || len(data) == 0 {
		return "", nil, m, httpx.Unprocessable("tax.upload_file", "Pilih berkas Excel atau CSV dari Coretax.")
	}
	return name, data, m, nil
}

func (h handlers) importInputs(w http.ResponseWriter, r *http.Request) error {
	name, data, _, err := upload(r)
	if err != nil {
		return err
	}
	res, err := h.svc.ImportCoretaxInputs(r.Context(), api.CompanyID(r), name, data, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h handlers) bupotRef(w http.ResponseWriter, _ *http.Request) error {
	return httpx.WriteJSON(w, http.StatusOK, Ref())
}

func (h handlers) withholdingObjects(w http.ResponseWriter, r *http.Request) error {
	objects, err := h.svc.WithholdingObjects(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"objects": objects})
}

func (h handlers) setWithholdingObjects(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Objects map[string]string `json:"objects"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	objects, err := h.svc.SetWithholdingObjects(r.Context(), api.CompanyID(r), req.Objects, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"objects": objects})
}

func (h handlers) slips(w http.ResponseWriter, r *http.Request) error {
	m, err := month(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Slips(r.Context(), api.CompanyID(r), m)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, list)
}

func (h handlers) updateSlip(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in SlipUpdate
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	out, err := h.svc.UpdateSlip(r.Context(), api.CompanyID(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h handlers) setSlipObject(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs           []uuid.UUID `json:"ids"`
		TaxObjectCode string      `json:"taxObjectCode"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := h.svc.SetSlipObject(r.Context(), api.CompanyID(r), req.IDs, req.TaxObjectCode, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// exportSlips mengembalikan daftar berkas (BPPU dan/atau BPNR); isinya diunduh lewat
// GET /tax/exports/{id}.
func (h handlers) exportSlips(w http.ResponseWriter, r *http.Request) error {
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
	list, err := h.svc.ExportSlips(r.Context(), api.CompanyID(r), m, req.IDs, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) importSlips(w http.ResponseWriter, r *http.Request) error {
	name, data, m, err := upload(r)
	if err != nil {
		return err
	}
	if m.IsZero() {
		return httpx.Unprocessable("tax.upload_month", "Pilih masa bukti potong.")
	}
	res, err := h.svc.ImportSlipNumbers(r.Context(), api.CompanyID(r), m, name, data, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h handlers) approveSlip(w http.ResponseWriter, r *http.Request) error {
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
	if err := h.svc.ApproveSlip(r.Context(), api.CompanyID(r), id, req.Number, req.Date, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) resetSlip(cancel bool) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
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
		if err := h.svc.ResetSlip(r.Context(), api.CompanyID(r), id, cancel, req.Note, auth.ActorFrom(r)); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

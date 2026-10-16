package consol

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lajur/internal/modules/report"
	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/export"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes: pasangan dokumen antar-PT, bagan akun grup dan pemetaannya, serta laporan konsolidasi.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	route := func(method, path, perm string, mode license.Mode, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: mode, Handle: fn}
	}
	return []api.Route{
		route(http.MethodGet, "/api/v1/consol/links/{type}/{id}", rbac.CompanyView, license.ModeRead, h.link),
		route(http.MethodGet, "/api/v1/consol/accounts", rbac.ConsolView, license.ModeRead, h.chart),
		route(http.MethodPut, "/api/v1/consol/accounts", rbac.ConsolRun, license.ModeWrite, h.saveAccount),
		route(http.MethodPut, "/api/v1/consol/map", rbac.ConsolRun, license.ModeWrite, h.setMapping),
		route(http.MethodGet, "/api/v1/consol/report", rbac.ConsolView, license.ModeRead, h.report),
		route(http.MethodGet, "/api/v1/consol/report/export", rbac.DataExport, license.ModeExport, h.export),
	}
}

type handlers struct{ svc Service }

func (h handlers) link(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	l, err := h.svc.Link(r.Context(), api.CompanyID(r), strings.ReplaceAll(chi.URLParam(r, "type"), "-", "."), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"link": l})
}

func (h handlers) chart(w http.ResponseWriter, r *http.Request) error {
	c, err := h.svc.GroupChart(r.Context(), auth.ActorFrom(r).UserID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

func (h handlers) saveAccount(w http.ResponseWriter, r *http.Request) error {
	var in GroupAccountInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := h.svc.SaveGroupAccount(r.Context(), in, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// setMapping hanya untuk akun PT aktif, supaya izin pengguna di PT itu yang berlaku.
func (h handlers) setMapping(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		AccountID uuid.UUID `json:"accountId"`
		GroupCode string    `json:"groupCode"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := h.svc.SetMapping(r.Context(), api.CompanyID(r), in.AccountID, in.GroupCode, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) build(r *http.Request) (Consolidation, error) {
	t := civil.Today(civil.Jakarta)
	asOf, err := api.DateQuery(r, "date", t)
	if err != nil {
		return Consolidation{}, err
	}
	from, err := api.DateQuery(r, "from", civil.New(asOf.Year(), 1, 1))
	if err != nil {
		return Consolidation{}, err
	}
	to, err := api.DateQuery(r, "to", asOf)
	if err != nil {
		return Consolidation{}, err
	}
	var ids []uuid.UUID
	for _, v := range strings.Split(r.URL.Query().Get("companies"), ",") {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		id, err := uuid.Parse(v)
		if err != nil {
			return Consolidation{}, httpx.BadRequest("request.query", "Daftar PT tidak valid.")
		}
		ids = append(ids, id)
	}
	return h.svc.Consolidate(r.Context(), auth.ActorFrom(r).UserID, ids, asOf, report.Period{From: from, To: to})
}

func (h handlers) report(w http.ResponseWriter, r *http.Request) error {
	c, err := h.build(r)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

// export: statement=neraca (bawaan) atau laba-rugi, ke xlsx atau csv.
func (h handlers) export(w http.ResponseWriter, r *http.Request) error {
	if !api.Can(r, rbac.ConsolView) {
		return httpx.Forbidden("consol.forbidden", "Anda tidak punya izin melihat konsolidasi.")
	}
	c, err := h.build(r)
	if err != nil {
		return err
	}
	if len(c.Problems) > 0 {
		return httpx.Unprocessable("consol.mapping", "Pemetaan akun grup belum lengkap; bereskan dulu di tab Pemetaan akun.")
	}
	st, period, file := c.BalanceSheet, "Per "+c.AsOf.Indonesian(), "konsolidasi-neraca-"+c.AsOf.String()
	if r.URL.Query().Get("statement") == "laba-rugi" {
		st, period, file = c.ProfitLoss, c.From.Indonesian()+" – "+c.To.Indonesian(), "konsolidasi-laba-rugi-"+c.From.String()+"_"+c.To.String()
	}
	t := st.Table(period)
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.csv"`)
		return export.WriteCSV(w, t)
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.xlsx"`)
	return export.WriteXLSX(w, t)
}

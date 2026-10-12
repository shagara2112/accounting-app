package ledger

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes mengembalikan endpoint buku besar. Posting dan void wajib membawa Idempotency-Key.
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	read := func(method, path, perm string, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: license.ModeRead, Handle: fn}
	}
	write := func(method, path, perm string, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: license.ModeWrite, Handle: fn}
	}
	idempotent := func(rt api.Route) api.Route { rt.Idempotent = true; return rt }

	return []api.Route{
		read(http.MethodGet, "/api/v1/ledger/accounts", rbac.LedgerView, h.accounts),
		write(http.MethodPost, "/api/v1/ledger/accounts", rbac.AccountCreate, h.createAccount),
		write(http.MethodPost, "/api/v1/ledger/accounts/template", rbac.AccountCreate, h.applyTemplate),
		write(http.MethodPatch, "/api/v1/ledger/accounts/{id}", rbac.AccountEdit, h.updateAccount),
		read(http.MethodGet, "/api/v1/ledger/accounts/{id}/ledger", rbac.LedgerView, h.accountLedger),

		read(http.MethodGet, "/api/v1/journals", rbac.LedgerView, h.entries),
		idempotent(write(http.MethodPost, "/api/v1/journals", rbac.JournalCreate, h.postGeneral)),
		idempotent(write(http.MethodPost, "/api/v1/adjustments", rbac.AdjustmentPost, h.postAdjustment)),
		read(http.MethodGet, "/api/v1/journals/{id}", rbac.LedgerView, h.entry),
		idempotent(write(http.MethodPost, "/api/v1/journals/{id}/void", rbac.JournalVoid, h.void)),

		read(http.MethodGet, "/api/v1/ledger/worksheet", rbac.LedgerView, h.worksheet),
		read(http.MethodGet, "/api/v1/ledger/periods", rbac.LedgerView, h.periods),
		write(http.MethodPost, "/api/v1/ledger/periods/{month}/status", rbac.PeriodManage, h.setPeriodStatus),
		read(http.MethodGet, "/api/v1/ledger/settings", rbac.LedgerView, h.settings),
		write(http.MethodPut, "/api/v1/ledger/settings", rbac.PeriodManage, h.updateSettings),
	}
}

type handlers struct{ svc Service }

func company(r *http.Request) uuid.UUID {
	s, _ := rbac.ScopeFrom(r.Context())
	return s.CompanyID
}

func actor(r *http.Request) Actor {
	sess, _ := auth.SessionFrom(r.Context())
	return Actor{UserID: sess.UserID, IP: auth.ClientIP(r)}
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.NotFound("not_found", "Data tidak ditemukan.")
	}
	return id, nil
}

// dateParam membaca parameter query yyyy-mm-dd; kosong menghasilkan def.
func dateParam(r *http.Request, name string, def civil.Date) (civil.Date, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	d, err := civil.Parse(v)
	if err != nil {
		return civil.Date{}, httpx.BadRequest("request.query", err.Error())
	}
	return d, nil
}

func monthParam(r *http.Request, v string, def civil.Date) (civil.Date, error) {
	if v == "" {
		return def, nil
	}
	m, err := civil.ParseMonth(v)
	if err != nil {
		return civil.Date{}, httpx.BadRequest("request.query", err.Error())
	}
	return m, nil
}

func today() civil.Date { return civil.Today(civil.Jakarta) }

// Bagan akun ---------------------------------------------------------------------

func (h handlers) accounts(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Accounts(r.Context(), company(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list, "classes": Classes, "templates": Templates})
}

func (h handlers) createAccount(w http.ResponseWriter, r *http.Request) error {
	var req NewAccount
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	a, err := h.svc.CreateAccount(r.Context(), company(r), req, actor(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h handlers) updateAccount(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req AccountChange
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	a, err := h.svc.UpdateAccount(r.Context(), company(r), id, req, actor(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h handlers) applyTemplate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Template string `json:"template"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	n, err := h.svc.ApplyTemplate(r.Context(), company(r), req.Template, actor(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"created": n})
}

func (h handlers) accountLedger(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	t := today()
	from, err := dateParam(r, "from", t.MonthStart())
	if err != nil {
		return err
	}
	to, err := dateParam(r, "to", t.MonthEnd())
	if err != nil {
		return err
	}
	out, err := h.svc.AccountLedger(r.Context(), company(r), id, from, to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

// Jurnal ------------------------------------------------------------------------

type postRequest struct {
	Date        civil.Date  `json:"date"`
	Memo        string      `json:"memo"`
	AutoReverse bool        `json:"autoReverse"`
	Lines       []LineDraft `json:"lines"`
}

func (h handlers) postGeneral(w http.ResponseWriter, r *http.Request) error {
	return h.post(w, r, General)
}

func (h handlers) postAdjustment(w http.ResponseWriter, r *http.Request) error {
	return h.post(w, r, Adjustment)
}

func (h handlers) post(w http.ResponseWriter, r *http.Request, kind Kind) error {
	var req postRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.AutoReverse && kind != Adjustment {
		return httpx.Unprocessable("journal.auto_reverse", "Balik otomatis hanya untuk jurnal penyesuaian.")
	}
	out, err := h.svc.Post(r.Context(), EntryDraft{
		CompanyID: company(r), Kind: kind, Date: req.Date, Memo: req.Memo, AutoReverse: req.AutoReverse,
		SourceType: SourceManual, Lines: req.Lines, Actor: actor(r),
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h handlers) entries(w http.ResponseWriter, r *http.Request) error {
	t := today()
	from, err := dateParam(r, "from", t.MonthStart())
	if err != nil {
		return err
	}
	to, err := dateParam(r, "to", t.MonthEnd())
	if err != nil {
		return err
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	page, err := h.svc.Entries(r.Context(), company(r), EntryFilter{
		From: from, To: to, Kind: q.Get("kind"), Search: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit,
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, page)
}

func (h handlers) entry(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	e, err := h.svc.Entry(r.Context(), company(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, e)
}

func (h handlers) void(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
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
	out, err := h.svc.Void(r.Context(), company(r), id, req.Date, req.Reason, actor(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

// Laporan dan periode -------------------------------------------------------------

func (h handlers) worksheet(w http.ResponseWriter, r *http.Request) error {
	month, err := monthParam(r, r.URL.Query().Get("month"), today().MonthStart())
	if err != nil {
		return err
	}
	out, err := h.svc.Worksheet(r.Context(), company(r), month)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h handlers) periods(w http.ResponseWriter, r *http.Request) error {
	year := today().Year()
	if v := r.URL.Query().Get("year"); v != "" {
		y, err := strconv.Atoi(v)
		if err != nil {
			return httpx.BadRequest("request.query", "Tahun tidak valid.")
		}
		year = y
	}
	out, err := h.svc.Periods(r.Context(), company(r), year)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h handlers) setPeriodStatus(w http.ResponseWriter, r *http.Request) error {
	month, err := monthParam(r, chi.URLParam(r, "month"), civil.Date{})
	if err != nil {
		return err
	}
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := h.svc.SetPeriodStatus(r.Context(), company(r), month, req.Status, req.Reason, actor(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) settings(w http.ResponseWriter, r *http.Request) error {
	s, err := h.svc.Settings(r.Context(), company(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, s)
}

func (h handlers) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		FiscalYearStartMonth int `json:"fiscalYearStartMonth"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := h.svc.SetFiscalYearStart(r.Context(), company(r), req.FiscalYearStartMonth, actor(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

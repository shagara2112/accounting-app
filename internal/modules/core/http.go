package core

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes mengembalikan endpoint modul core.
func Routes(svc Service, sessions *auth.Sessions) []api.Route {
	h := handlers{svc: svc, sessions: sessions}
	return []api.Route{
		{Method: http.MethodGet, Path: "/api/v1/me", Access: api.Authenticated, License: license.ModeStatus, Handle: h.me},
		{Method: http.MethodGet, Path: "/api/v1/companies", Access: api.Authenticated, License: license.ModeRead, Handle: h.companies},
		{Method: http.MethodPost, Path: "/api/v1/session/company", Access: api.Authenticated, License: license.ModeRead, Handle: h.switchCompany},
	}
}

type handlers struct {
	svc      Service
	sessions *auth.Sessions
}

func (h handlers) me(w http.ResponseWriter, r *http.Request) error {
	sess, _ := auth.SessionFrom(r.Context())
	list, err := h.svc.CompaniesFor(r.Context(), sess.UserID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":            map[string]any{"id": sess.UserID, "name": sess.Name, "email": sess.Email},
		"activeCompanyId": sess.ActiveCompanyID,
		"companies":       list,
	})
}

func (h handlers) companies(w http.ResponseWriter, r *http.Request) error {
	sess, _ := auth.SessionFrom(r.Context())
	list, err := h.svc.CompaniesFor(r.Context(), sess.UserID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

type switchRequest struct {
	CompanyID uuid.UUID `json:"companyId"`
}

// switchCompany mengganti PT aktif tanpa login ulang (ME-02).
func (h handlers) switchCompany(w http.ResponseWriter, r *http.Request) error {
	var req switchRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	sess, _ := auth.SessionFrom(r.Context())
	if _, err := h.svc.Role(r.Context(), sess.UserID, req.CompanyID); err != nil {
		if errors.Is(err, rbac.ErrNoAccess) {
			return httpx.Forbidden("company.denied", "Anda tidak punya akses ke PT ini.")
		}
		return err
	}
	if err := h.sessions.SetCompany(r.Context(), sess, req.CompanyID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

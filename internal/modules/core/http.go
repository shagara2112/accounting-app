package core

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
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

		{Method: http.MethodGet, Path: "/api/v1/company/profile", Access: api.Permission, Perm: rbac.CompanyView, License: license.ModeRead, Handle: h.profile},
		{Method: http.MethodPut, Path: "/api/v1/company/profile", Access: api.Permission, Perm: rbac.CompanyEdit, License: license.ModeWrite, Handle: h.updateProfile},
		{Method: http.MethodGet, Path: "/api/v1/contacts", Access: api.Permission, Perm: rbac.ContactView, License: license.ModeRead, Handle: h.contacts},
		{Method: http.MethodGet, Path: "/api/v1/contacts/{id}", Access: api.Permission, Perm: rbac.ContactView, License: license.ModeRead, Handle: h.contact},
		{Method: http.MethodPost, Path: "/api/v1/contacts", Access: api.Permission, Perm: rbac.ContactCreate, License: license.ModeWrite, Idempotent: true, Handle: h.createContact},
		{Method: http.MethodPut, Path: "/api/v1/contacts/{id}", Access: api.Permission, Perm: rbac.ContactEdit, License: license.ModeWrite, Handle: h.updateContact},
	}
}

func companyOf(r *http.Request) uuid.UUID {
	s, _ := rbac.ScopeFrom(r.Context())
	return s.CompanyID
}

func idParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.NotFound("not_found", "Data tidak ditemukan.")
	}
	return id, nil
}

func (h handlers) contacts(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	list, err := h.svc.Contacts(r.Context(), companyOf(r), ContactFilter{
		Role: q.Get("role"), Search: q.Get("q"), IncludeInactive: q.Get("inactive") == "1",
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) contact(w http.ResponseWriter, r *http.Request) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	c, err := h.svc.Contact(r.Context(), companyOf(r), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

func (h handlers) createContact(w http.ResponseWriter, r *http.Request) error {
	var in ContactInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Active = true
	c, err := h.svc.CreateContact(r.Context(), companyOf(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h handlers) updateContact(w http.ResponseWriter, r *http.Request) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	var in ContactInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	c, err := h.svc.UpdateContact(r.Context(), companyOf(r), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
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

func (h handlers) profile(w http.ResponseWriter, r *http.Request) error {
	c, err := h.svc.Company(r.Context(), companyOf(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

func (h handlers) updateProfile(w http.ResponseWriter, r *http.Request) error {
	var in TaxProfile
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	c, err := h.svc.UpdateTaxProfile(r.Context(), companyOf(r), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

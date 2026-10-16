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
	"lajur/internal/platform/pdf"
	"lajur/internal/platform/rbac"
)

// Routes mengembalikan endpoint modul core.
func Routes(svc Service, sessions *auth.Sessions) []api.Route {
	h := handlers{svc: svc, sessions: sessions}
	return []api.Route{
		{Method: http.MethodGet, Path: "/api/v1/me", Access: api.Authenticated, License: license.ModeStatus, AllowMFASetup: true, Handle: h.me},
		{Method: http.MethodGet, Path: "/api/v1/companies", Access: api.Authenticated, License: license.ModeRead, Handle: h.companies},
		{Method: http.MethodPost, Path: "/api/v1/session/company", Access: api.Authenticated, License: license.ModeRead, Handle: h.switchCompany},

		{Method: http.MethodGet, Path: "/api/v1/company/profile", Access: api.Permission, Perm: rbac.CompanyView, License: license.ModeRead, Handle: h.profile},
		{Method: http.MethodPut, Path: "/api/v1/company/profile", Access: api.Permission, Perm: rbac.CompanyEdit, License: license.ModeWrite, Handle: h.updateProfile},
		{Method: http.MethodGet, Path: "/api/v1/company/logo", Access: api.Permission, Perm: rbac.CompanyView, License: license.ModeRead, Handle: h.logo},
		{Method: http.MethodPut, Path: "/api/v1/company/logo", Access: api.Permission, Perm: rbac.CompanyEdit, License: license.ModeWrite, Handle: h.setLogo},
		{Method: http.MethodDelete, Path: "/api/v1/company/logo", Access: api.Permission, Perm: rbac.CompanyEdit, License: license.ModeWrite, Handle: h.removeLogo},
		{Method: http.MethodGet, Path: "/api/v1/contacts", Access: api.Permission, Perm: rbac.ContactView, License: license.ModeRead, Handle: h.contacts},
		{Method: http.MethodGet, Path: "/api/v1/contacts/{id}", Access: api.Permission, Perm: rbac.ContactView, License: license.ModeRead, Handle: h.contact},
		{Method: http.MethodPost, Path: "/api/v1/contacts", Access: api.Permission, Perm: rbac.ContactCreate, License: license.ModeWrite, Idempotent: true, Handle: h.createContact},
		{Method: http.MethodPut, Path: "/api/v1/contacts/{id}", Access: api.Permission, Perm: rbac.ContactEdit, License: license.ModeWrite, Handle: h.updateContact},
		{Method: http.MethodGet, Path: "/api/v1/dimensions/{kind}", Access: api.Permission, Perm: rbac.CompanyView, License: license.ModeRead, Handle: h.dimensions},
		{Method: http.MethodPost, Path: "/api/v1/dimensions/{kind}", Access: api.Permission, Perm: rbac.CompanyEdit, License: license.ModeWrite, Handle: h.saveDimension},
		{Method: http.MethodPut, Path: "/api/v1/dimensions/{kind}/{id}", Access: api.Permission, Perm: rbac.CompanyEdit, License: license.ModeWrite, Handle: h.saveDimension},
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
		"user":             map[string]any{"id": sess.UserID, "name": sess.Name, "email": sess.Email},
		"activeCompanyId":  sess.ActiveCompanyID,
		"companies":        list,
		"mfaSetupRequired": sess.MFASetup,
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

func (h handlers) logo(w http.ResponseWriter, r *http.Request) error {
	b, err := h.svc.Logo(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	if b == nil {
		return httpx.NotFound("company.no_logo", "PT ini belum punya logo.")
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-cache")
	_, err = w.Write(b)
	return err
}

// setLogo menerima multipart field "file" berisi PNG atau JPEG.
func (h handlers) setLogo(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	f, _, err := r.FormFile("file")
	if err != nil {
		return httpx.BadRequest("company.logo_form", "Kirim logo sebagai multipart/form-data (field file), maksimal 8 MB.")
	}
	defer f.Close()
	jpeg, err := pdf.NormalizeLogo(f)
	if err != nil {
		return httpx.Unprocessable("company.logo", err.Error())
	}
	if err := h.svc.SetLogo(r.Context(), api.CompanyID(r), jpeg, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) removeLogo(w http.ResponseWriter, r *http.Request) error {
	if err := h.svc.SetLogo(r.Context(), api.CompanyID(r), nil, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) dimensions(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Dimensions(r.Context(), companyOf(r), chi.URLParam(r, "kind"))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h handlers) saveDimension(w http.ResponseWriter, r *http.Request) error {
	var in DimensionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	var id *uuid.UUID
	if v := chi.URLParam(r, "id"); v != "" {
		u, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("request.id", "ID tidak valid.")
		}
		id = &u
	}
	d, err := h.svc.SaveDimension(r.Context(), companyOf(r), chi.URLParam(r, "kind"), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	status := http.StatusOK
	if id == nil {
		status = http.StatusCreated
	}
	return httpx.WriteJSON(w, status, d)
}

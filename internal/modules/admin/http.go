package admin

import (
	"net/http"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes: setup wizard (publik, hanya selama belum ada pengguna), pengguna dan aksesnya,
// persona kustom, dan PT baru. authH dipakai untuk membuka sesi Pemilik setelah setup.
func Routes(svc Service, authH *auth.Handlers) []api.Route {
	h := handlers{svc: svc, auth: authH}
	route := func(method, path, perm string, mode license.Mode, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: perm, License: mode, Handle: fn}
	}
	return []api.Route{
		{Method: http.MethodGet, Path: "/api/v1/setup", Access: api.Public, License: license.ModeStatus, Handle: h.setupStatus},
		{Method: http.MethodPost, Path: "/api/v1/setup", Access: api.Public, License: license.ModeStatus, Handle: h.setup},

		route(http.MethodGet, "/api/v1/admin/users", rbac.UserManage, license.ModeRead, h.users),
		route(http.MethodPost, "/api/v1/admin/users", rbac.UserManage, license.ModeWrite, h.createUser),
		route(http.MethodPut, "/api/v1/admin/users/{id}", rbac.UserManage, license.ModeWrite, h.updateUser),
		route(http.MethodPut, "/api/v1/admin/users/{id}/access", rbac.UserManage, license.ModeWrite, h.setAccess),
		route(http.MethodPost, "/api/v1/admin/users/{id}/password", rbac.UserManage, license.ModeWrite, h.resetPassword),
		route(http.MethodPost, "/api/v1/admin/users/{id}/reset-2fa", rbac.UserManage, license.ModeWrite, h.resetMFA),
		route(http.MethodPost, "/api/v1/admin/users/{id}/unlock", rbac.UserManage, license.ModeWrite, h.unlock),

		route(http.MethodGet, "/api/v1/admin/roles", rbac.UserManage, license.ModeRead, h.roles),
		route(http.MethodPost, "/api/v1/admin/roles", rbac.UserManage, license.ModeWrite, h.createRole),
		route(http.MethodPut, "/api/v1/admin/roles/{id}", rbac.UserManage, license.ModeWrite, h.updateRole),
		route(http.MethodDelete, "/api/v1/admin/roles/{id}", rbac.UserManage, license.ModeWrite, h.deleteRole),

		route(http.MethodGet, "/api/v1/admin/companies", rbac.SystemManage, license.ModeRead, h.companies),
		route(http.MethodPost, "/api/v1/admin/companies", rbac.SystemManage, license.ModeWrite, h.createCompany),
	}
}

type handlers struct {
	svc  Service
	auth *auth.Handlers
}

func (h handlers) setupStatus(w http.ResponseWriter, r *http.Request) error {
	required, err := h.svc.SetupRequired(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"required": required, "remoteLicense": h.svc.RemoteLicense(), "industries": Industries,
	})
}

func (h handlers) setup(w http.ResponseWriter, r *http.Request) error {
	var in SetupInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	id, err := h.svc.Setup(r.Context(), in, auth.ClientIP(r))
	if err != nil {
		return err
	}
	// Pemilik wajib 2FA; sesi pertamanya langsung diarahkan ke pemasangan 2FA.
	return h.auth.StartSession(w, r, id, h.auth.MFARequired, "setup")
}

func (h handlers) users(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Users(r.Context(), auth.ActorFrom(r).UserID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, list)
}

func (h handlers) createUser(w http.ResponseWriter, r *http.Request) error {
	var in NewUser
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	u, err := h.svc.CreateUser(r.Context(), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, u)
}

func (h handlers) updateUser(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in UserUpdate
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	u, err := h.svc.UpdateUser(r.Context(), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, u)
}

func (h handlers) setAccess(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in AccessInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := h.svc.SetAccess(r.Context(), id, in.CompanyID, in.RoleKey, in.BranchIDs, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) resetPassword(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := h.svc.ResetPassword(r.Context(), id, in.Password, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) resetMFA(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.ResetMFA(r.Context(), id, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) unlock(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.Unlock(r.Context(), id, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) roles(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Roles(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list, "catalog": rbac.Catalog})
}

func (h handlers) createRole(w http.ResponseWriter, r *http.Request) error {
	var in RoleInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	role, err := h.svc.CreateRole(r.Context(), in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, role)
}

func (h handlers) updateRole(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	var in RoleInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	role, err := h.svc.UpdateRole(r.Context(), id, in, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, role)
}

func (h handlers) deleteRole(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.DeleteRole(r.Context(), id, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) companies(w http.ResponseWriter, r *http.Request) error {
	list, err := h.svc.Companies(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list, "industries": Industries})
}

func (h handlers) createCompany(w http.ResponseWriter, r *http.Request) error {
	var in NewCompany
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	scope, _ := rbac.ScopeFrom(r.Context())
	c, err := h.svc.CreateCompany(r.Context(), in, scope.Role.Key, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, c)
}

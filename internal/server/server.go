// Package server merangkai router, route semua modul, dan penyaji frontend.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/platform/api"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

type Deps struct {
	Pool     *pgxpool.Pool
	Log      *slog.Logger
	Sessions *auth.Sessions
	Core     core.Service
	Ledger   ledger.Service
	License  license.Provider
	Web      fs.FS // hasil build frontend
}

// New mengembalikan handler HTTP lengkap dan registry route-nya.
func New(d Deps) (http.Handler, *api.API) {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, logRequests(d.Log), middleware.Recoverer, securityHeaders)
	r.Get("/healthz", health(d))

	a := api.New(r, api.Deps{Pool: d.Pool, Sessions: d.Sessions, Access: d.Core, License: d.License, Log: d.Log})

	authH := &auth.Handlers{Pool: d.Pool, Sessions: d.Sessions, Access: d.Core, MaxFailures: 5, LockFor: 15 * time.Minute}
	a.Add(api.Route{Method: http.MethodPost, Path: "/api/v1/auth/login", Access: api.Public, License: license.ModeStatus, Handle: authH.Login})
	a.Add(api.Route{Method: http.MethodPost, Path: "/api/v1/auth/logout", Access: api.Authenticated, License: license.ModeStatus, Handle: authH.Logout})

	for _, rt := range core.Routes(d.Core, d.Sessions) {
		a.Add(rt)
	}
	for _, rt := range ledger.Routes(d.Ledger) {
		a.Add(rt)
	}

	a.Add(api.Route{Method: http.MethodGet, Path: "/api/v1/system/license", Access: api.Authenticated, License: license.ModeStatus, Handle: licenseStatus(d.License)})
	a.Add(api.Route{Method: http.MethodGet, Path: "/api/v1/audit", Access: api.Permission, Perm: rbac.AuditView, License: license.ModeRead, Handle: audit.ListHandler(d.Pool)})
	a.Add(api.Route{Method: http.MethodGet, Path: "/api/v1/audit/verify", Access: api.Permission, Perm: rbac.AuditView, License: license.ModeRead, Handle: audit.VerifyHandler(d.Pool)})

	r.Handle("/api/*", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteProblem(w, httpx.NotFound("api.not_found", "Endpoint tidak ada."))
	}))
	r.Handle("/*", spa(d.Web))
	return r, a
}

func health(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"status": "ok", "database": "ok", "license": d.License.Status(r.Context()).State}
		code := http.StatusOK
		if err := d.Pool.Ping(r.Context()); err != nil {
			out["status"], out["database"], code = "degraded", "tidak terhubung", http.StatusServiceUnavailable
		}
		_ = httpx.WriteJSON(w, code, out)
	}
}

func licenseStatus(p license.Provider) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		return httpx.WriteJSON(w, http.StatusOK, p.Status(r.Context()))
	}
}

// spa menyajikan file frontend; path yang tidak ada dijawab index.html agar routing di browser jalan.
func spa(files fs.FS) http.Handler {
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if _, err := fs.Stat(files, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "Frontend belum di-build. Jalankan: make web-build", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
			"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func logRequests(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			log.InfoContext(r.Context(), "request",
				"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
				"ms", time.Since(start).Milliseconds(), "request_id", middleware.GetReqID(r.Context()))
		})
	}
}

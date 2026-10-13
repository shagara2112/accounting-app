// Package server merangkai router, route semua modul, dan penyaji frontend.
package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/core"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/tax/coretax"
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
	Services
	License license.Provider
	Web     fs.FS // hasil build frontend
}

// Services adalah semua service modul yang dirangkai di atas pool yang sama.
type Services struct {
	Core     core.Service
	Ledger   ledger.Service
	Tax      tax.Service
	Cash     cash.Service
	Sales    sales.Service
	Purchase purchase.Service
	Coretax  coretax.Service
}

// Modules merangkai semua modul. Modul pajak (coretax) membaca penjualan, sedangkan
// penjualan perlu bertanya ke modul pajak sebelum void; pengunci void diisi belakangan
// supaya tidak ada dependensi melingkar.
func Modules(pool *pgxpool.Pool) Services {
	var m Services
	m.Core, m.Ledger, m.Tax = core.New(pool), ledger.New(pool), tax.New(pool)
	m.Cash = cash.New(pool, m.Ledger)
	m.Sales = sales.New(sales.Deps{
		Pool: pool, Ledger: m.Ledger, Core: m.Core, Tax: m.Tax, Cash: m.Cash,
		VoidGuard: func(ctx context.Context, companyID, invoiceID uuid.UUID) error {
			return m.Coretax.CanVoidInvoice(ctx, companyID, invoiceID)
		},
	})
	m.Purchase = purchase.New(purchase.Deps{Pool: pool, Ledger: m.Ledger, Core: m.Core, Tax: m.Tax, Cash: m.Cash})
	m.Coretax = coretax.New(coretax.Deps{Pool: pool, Core: m.Core, Tax: m.Tax, Sales: m.Sales, Purchase: m.Purchase})
	return m
}

// WithModules mengisi semua service modul di Deps.
func (d Deps) WithModules() Deps {
	d.Services = Modules(d.Pool)
	return d
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
	for _, group := range [][]api.Route{
		ledger.Routes(d.Ledger), tax.Routes(d.Tax), cash.Routes(d.Cash), sales.Routes(d.Sales), purchase.Routes(d.Purchase),
		coretax.Routes(d.Coretax),
	} {
		for _, rt := range group {
			a.Add(rt)
		}
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

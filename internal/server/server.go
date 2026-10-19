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
	"lajur/internal/modules/admin"
	"lajur/internal/modules/asset"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/consol"
	"lajur/internal/modules/core"
	"lajur/internal/modules/docs"
	"lajur/internal/modules/imports"
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/project"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/report"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/tax/coretax"
	"lajur/internal/platform/api"
	"lajur/internal/platform/attach"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/backup"
	"lajur/internal/platform/buildinfo"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/mail"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/secret"
)

type Deps struct {
	Pool     *pgxpool.Pool
	Log      *slog.Logger
	Sessions *auth.Sessions
	Services
	License license.Provider
	Web     fs.FS // hasil build frontend

	// Box mengenkripsi kolom sensitif dengan kunci aplikasi; MFARequired mewajibkan 2FA
	// untuk peran istimewa; LicenseSetup mengaktifkan lisensi di setup wizard.
	Box          *secret.Box
	MFARequired  bool
	LicenseSetup admin.LicenseSetup
	LicenseAdmin LicenseAdmin // nil di mode lisensi local
	Backup       *backup.Service
	Updates      *Updates
	// FilesDir: folder lampiran dokumen (ikut backup).
	FilesDir string
	Mail     *mail.Service
}

// Services adalah semua service modul yang dirangkai di atas pool yang sama.
type Services struct {
	Core      core.Service
	Ledger    ledger.Service
	Tax       tax.Service
	Inventory inventory.Service
	Cash      cash.Service
	Sales     sales.Service
	Purchase  purchase.Service
	Coretax   coretax.Service
	Project   project.Service
	Asset     asset.Service
	Report    report.Service
	Consol    consol.Service
	Imports   imports.Service
	Admin     admin.Service
}

// Modules merangkai semua modul. Modul pajak (coretax) membaca penjualan, sedangkan
// penjualan perlu bertanya ke modul pajak sebelum void; pengunci void diisi belakangan
// supaya tidak ada dependensi melingkar. Pemeriksaan proyek (proyek batal tidak boleh
// dipakai) dipasang ke buku besar dan dokumen lewat fungsi, karena modul proyek sendiri
// bergantung pada penjualan.
func Modules(pool *pgxpool.Pool) Services {
	var m Services
	check := project.Check(pool)
	m.Ledger, m.Tax = ledger.New(pool, ledger.WithProjectCheck(check)), tax.New(pool)
	m.Core = core.New(pool, core.WithAccountCheck(m.Ledger.CheckAccount))
	m.Inventory = inventory.New(inventory.Deps{Pool: pool, Ledger: m.Ledger})
	m.Cash = cash.New(pool, m.Ledger, m.Inventory)
	m.Sales = sales.New(sales.Deps{
		Pool: pool, Ledger: m.Ledger, Core: m.Core, Tax: m.Tax, Cash: m.Cash, Inventory: m.Inventory, ProjectCheck: check,
		VoidGuard: func(ctx context.Context, companyID, invoiceID uuid.UUID) error {
			return m.Coretax.CanVoidInvoice(ctx, companyID, invoiceID)
		},
		AfterPost: func(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error {
			return m.Consol.InvoicePosted(ctx, companyID, id, actor)
		},
		AfterVoid: func(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
			return m.Consol.InvoiceVoided(ctx, companyID, id, date, reason, actor)
		},
		AfterCreditPost: func(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error {
			return m.Consol.CreditNotePosted(ctx, companyID, id, actor)
		},
		AfterCreditVoid: func(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
			return m.Consol.NoteVoided(ctx, companyID, sales.SourceCreditNote, id, date, reason, actor)
		},
		AfterReceiptPost: func(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error {
			return m.Consol.ReceiptPosted(ctx, companyID, id, actor)
		},
		AfterReceiptVoid: func(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
			return m.Consol.SettlementVoided(ctx, companyID, sales.SourceReceipt, id, date, reason, actor)
		},
	})
	m.Purchase = purchase.New(purchase.Deps{
		Pool: pool, Ledger: m.Ledger, Core: m.Core, Tax: m.Tax, Cash: m.Cash, Inventory: m.Inventory, ProjectCheck: check,
		VoidGuard: func(ctx context.Context, companyID, billID uuid.UUID) error {
			return m.Coretax.CanVoidBill(ctx, companyID, billID)
		},
		NoteGuard: func(ctx context.Context, companyID, billID uuid.UUID) error {
			return m.Coretax.CanChangeBillVAT(ctx, companyID, billID)
		},
		AfterPost: func(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error {
			return m.Consol.BillPosted(ctx, companyID, id, actor)
		},
		AfterVoid: func(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
			return m.Consol.BillVoided(ctx, companyID, id, date, reason, actor)
		},
		AfterDebitPost: func(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error {
			return m.Consol.DebitNotePosted(ctx, companyID, id, actor)
		},
		AfterDebitVoid: func(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
			return m.Consol.NoteVoided(ctx, companyID, purchase.SourceDebitNote, id, date, reason, actor)
		},
		AfterPaymentPost: func(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error {
			return m.Consol.PaymentPosted(ctx, companyID, id, actor)
		},
		AfterPaymentVoid: func(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) error {
			return m.Consol.SettlementVoided(ctx, companyID, purchase.SourcePayment, id, date, reason, actor)
		},
	})
	m.Coretax = coretax.New(coretax.Deps{Pool: pool, Core: m.Core, Tax: m.Tax, Sales: m.Sales, Purchase: m.Purchase, Ledger: m.Ledger})
	m.Project = project.New(project.Deps{Pool: pool, Ledger: m.Ledger, Core: m.Core, Tax: m.Tax, Sales: m.Sales})
	m.Asset = asset.New(asset.Deps{Pool: pool, Ledger: m.Ledger, Core: m.Core})
	m.Report = report.New(report.Deps{Pool: pool, Ledger: m.Ledger, Core: m.Core, Cash: m.Cash, Sales: m.Sales, Purchase: m.Purchase})
	m.Consol = consol.New(consol.Deps{Pool: pool, Core: m.Core, Ledger: m.Ledger, Sales: m.Sales, Purchase: m.Purchase, Report: m.Report})
	m.Imports = imports.New(imports.Deps{Pool: pool, Ledger: m.Ledger, Core: m.Core, Inventory: m.Inventory, Sales: m.Sales, Purchase: m.Purchase})
	return m
}

// WithModules mengisi semua service modul di Deps.
func (d Deps) WithModules() Deps {
	d.Services = Modules(d.Pool)
	lic := d.LicenseSetup
	if lic == nil {
		lic = admin.LocalLicense{}
	}
	d.Admin = admin.New(admin.Deps{Pool: d.Pool, Core: d.Core, Ledger: d.Ledger, License: lic})
	return d
}

// New mengembalikan handler HTTP lengkap dan registry route-nya.
func New(d Deps) (http.Handler, *api.API) {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, logRequests(d.Log), middleware.Recoverer, securityHeaders)
	r.Get("/healthz", health(d))

	a := api.New(r, api.Deps{Pool: d.Pool, Sessions: d.Sessions, Access: d.Core, License: d.License, Log: d.Log})

	if d.Box == nil {
		panic("server: Deps.Box (kunci aplikasi) wajib diisi")
	}
	if d.Mail == nil {
		d.Mail = mail.New(d.Pool, d.Box, d.Log)
	}
	authH := &auth.Handlers{
		Pool: d.Pool, Sessions: d.Sessions, Access: d.Core, MaxFailures: 5, LockFor: 15 * time.Minute,
		Box: d.Box, MFARequired: d.MFARequired,
	}
	// Keamanan akun (2FA, kata sandi) tetap bisa diatur walau lisensi berakhir.
	self := func(method, path string, fn httpx.Handler, duringSetup bool) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Authenticated, License: license.ModeStatus, AllowMFASetup: duringSetup, Handle: fn}
	}
	for _, rt := range []api.Route{
		{Method: http.MethodPost, Path: "/api/v1/auth/login", Access: api.Public, License: license.ModeStatus, Handle: authH.Login},
		{Method: http.MethodPost, Path: "/api/v1/auth/totp", Access: api.Public, License: license.ModeStatus, Handle: authH.LoginTOTP},
		self(http.MethodPost, "/api/v1/auth/logout", authH.Logout, true),
		self(http.MethodGet, "/api/v1/auth/mfa", authH.MFAStatus, true),
		self(http.MethodPost, "/api/v1/auth/totp/setup", authH.TOTPSetup, true),
		self(http.MethodPost, "/api/v1/auth/totp/enable", authH.TOTPEnable, true),
		self(http.MethodPost, "/api/v1/auth/totp/disable", authH.TOTPDisable, false),
		self(http.MethodPost, "/api/v1/auth/password", authH.ChangePassword, true),
	} {
		a.Add(rt)
	}
	for _, rt := range admin.Routes(d.Admin, authH) {
		a.Add(rt)
	}

	for _, rt := range core.Routes(d.Core, d.Sessions) {
		a.Add(rt)
	}
	for _, group := range [][]api.Route{
		ledger.Routes(d.Ledger), tax.Routes(d.Tax), cash.Routes(d.Cash), sales.Routes(d.Sales), purchase.Routes(d.Purchase),
		coretax.Routes(d.Coretax), inventory.Routes(d.Inventory), project.Routes(d.Project), report.Routes(d.Report),
		consol.Routes(d.Consol), imports.Routes(d.Imports), asset.Routes(d.Asset),
	} {
		for _, rt := range group {
			a.Add(markWholeCompany(rt))
		}
	}

	a.Add(api.Route{Method: http.MethodGet, Path: "/api/v1/system/license", Access: api.Authenticated, License: license.ModeStatus, AllowMFASetup: true, Handle: licenseStatus(d.License)})
	for _, rt := range append(systemRoutes(d), updateRoutes(d)...) {
		a.Add(rt)
	}
	for _, rt := range attach.Routes(&attach.Service{Pool: d.Pool, FilesDir: d.FilesDir}) {
		a.Add(rt)
	}
	for _, rt := range docs.Routes(docs.New(docs.Deps{Core: d.Core, Sales: d.Sales, Cash: d.Cash, Mail: d.Mail})) {
		a.Add(rt)
	}
	a.Add(api.Route{Method: http.MethodGet, Path: "/api/v1/audit", Access: api.Permission, Perm: rbac.AuditView, License: license.ModeRead, Handle: audit.ListHandler(d.Pool)})
	a.Add(api.Route{Method: http.MethodGet, Path: "/api/v1/audit/verify", Access: api.Permission, Perm: rbac.AuditView, License: license.ModeRead, Handle: audit.VerifyHandler(d.Pool)})

	r.Handle("/api/*", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteProblem(w, httpx.NotFound("api.not_found", "Endpoint tidak ada."))
	}))
	r.Handle("/*", spa(d.Web))
	return r, a
}

// health dipakai updater (cek setelah pembaruan) dan pemantauan: aplikasi, basis data,
// lisensi, dan umur backup. Hanya ringkasan, tanpa data keuangan.
func health(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"status": "ok", "database": "ok", "license": d.License.Status(r.Context()).State, "version": buildinfo.Version}
		code := http.StatusOK
		if err := d.Pool.Ping(r.Context()); err != nil {
			out["status"], out["database"], code = "degraded", "tidak terhubung", http.StatusServiceUnavailable
		} else if d.Backup != nil {
			if st, err := d.Backup.Status(r.Context()); err == nil {
				out["backup"] = map[string]any{"configured": st.Configured, "lastOk": st.LastOK, "overdue": st.Overdue}
			}
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

// Tampilan yang mencakup seluruh PT. Pengguna yang dibatasi ke cabang tertentu (PLT-12) hanya
// memakai dokumen cabangnya dan laporan laba rugi per cabang.
var wholeCompanyPrefixes = []string{
	"/api/v1/journals", "/api/v1/adjustments", "/api/v1/ledger/worksheet", "/api/v1/ledger/periods", "/api/v1/ledger/settings",
	"/api/v1/ledger/recurring", "/api/v1/ledger/number-formats",
	"/api/v1/tax/", "/api/v1/consol/", "/api/v1/imports", "/api/v1/cash/reconciliations",
	"/api/v1/inventory/stock", "/api/v1/inventory/card", "/api/v1/inventory/adjustments",
	"/api/v1/reports/", "/api/v1/assets",
}

func markWholeCompany(rt api.Route) api.Route {
	if strings.HasSuffix(rt.Path, "/ledger") && strings.HasPrefix(rt.Path, "/api/v1/ledger/accounts/") {
		rt.WholeCompany = true
	}
	for _, p := range wholeCompanyPrefixes {
		if strings.HasPrefix(rt.Path, p) {
			rt.WholeCompany = true
		}
	}
	// Laba rugi tersaring per cabang; ekspornya diperiksa di handler laporan. Kode pajak dibutuhkan
	// formulir faktur dan tagihan cabang, referensi bukti potong (daftar negara) dibutuhkan formulir
	// kontak, dan awal tahun buku dibutuhkan pilihan periode laporan.
	switch {
	case rt.Path == "/api/v1/reports/profit-loss", rt.Path == "/api/v1/reports/{name}/export", rt.Path == "/api/v1/tax/codes",
		rt.Path == "/api/v1/tax/bupot-ref":
		rt.WholeCompany = false
	case rt.Path == "/api/v1/ledger/settings" && rt.Method == http.MethodGet:
		rt.WholeCompany = false
	}
	return rt
}

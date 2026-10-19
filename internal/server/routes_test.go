package server

import (
	"log/slog"
	"net/http"
	"testing"
	"testing/fstest"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/secret"
)

// Setiap route wajib menyatakan mode lisensi dan izin yang ada di katalog,
// dan hanya login (dua tahap) dan setup wizard yang boleh diakses tanpa sesi.
func TestEveryRouteDeclaresLicenseAndPermission(t *testing.T) {
	_, a := New(testDeps())
	catalog := rbac.NewPerms(rbac.All)
	publicAllowed := map[string]bool{
		"POST /api/v1/auth/login": true, "POST /api/v1/auth/totp": true,
		"GET /api/v1/setup": true, "POST /api/v1/setup": true,
	}

	if len(a.Routes()) == 0 {
		t.Fatal("tidak ada route terdaftar")
	}
	for _, rt := range a.Routes() {
		key := rt.Method + " " + rt.Path
		if rt.License == license.ModeUnset {
			t.Errorf("%s tidak menyatakan mode lisensi", key)
		}
		if rt.Access == api.Public && !publicAllowed[key] {
			t.Errorf("%s bisa diakses tanpa sesi", key)
		}
		if rt.Access == api.Permission && !catalog.Has(rt.Perm) {
			t.Errorf("%s memakai izin di luar katalog: %q", key, rt.Perm)
		}
		// Ekspor boleh memakai mode Export walau menulis riwayat ekspor: ekspor tidak pernah dikunci.
		if rt.Method != "GET" && rt.Access == api.Permission &&
			rt.License != license.ModeWrite && rt.License != license.ModeStatus && rt.License != license.ModeExport {
			t.Errorf("%s mengubah data tetapi tidak memakai mode lisensi Write", key)
		}
	}
}

// Posting dan void jurnal wajib idempotent, supaya klik ganda tidak membuat jurnal kedua.
func TestFinancialPostsAreIdempotent(t *testing.T) {
	_, a := New(testDeps())
	want := map[string]bool{
		"POST /api/v1/journals": true, "POST /api/v1/adjustments": true, "POST /api/v1/journals/{id}/void": true,
		"POST /api/v1/sales/invoices/{id}/post": true, "POST /api/v1/sales/receipts": true,
		"POST /api/v1/purchase/bills/{id}/post": true, "POST /api/v1/purchase/payments": true,
		"POST /api/v1/cash/transactions": true,
	}
	// Semua POST yang membuat atau membatalkan dokumen keuangan wajib idempotent.
	financial := map[string]bool{
		rbac.JournalCreate: true, rbac.AdjustmentPost: true, rbac.JournalVoid: true,
		rbac.SalesCreate: true, rbac.SalesVoid: true, rbac.PurchaseCreate: true, rbac.PurchaseVoid: true,
		rbac.CashCreate: true, rbac.CashVoid: true,
	}
	for _, rt := range a.Routes() {
		key := rt.Method + " " + rt.Path
		if rt.Method == "POST" && financial[rt.Perm] && !rt.Idempotent {
			t.Errorf("%s harus idempotent", key)
		}
		delete(want, key)
	}
	for key := range want {
		t.Errorf("route %s tidak terdaftar", key)
	}
}

func testDeps() Deps {
	return Deps{
		Log: slog.New(slog.DiscardHandler), Sessions: auth.NewSessions(nil, false),
		License: license.Static{}, Web: fstest.MapFS{}, Box: testBox(),
	}.WithModules()
}

func testBox() *secret.Box {
	b, err := secret.New([]byte("kunci-uji-lajur-tiga-puluh-dua-b"))
	if err != nil {
		panic(err)
	}
	return b
}

// Pengguna terbatas cabang (PLT-12) tetap bisa membuat dokumen dan melihat laba rugi cabangnya,
// tetapi tidak membuka buku besar, pajak, atau laporan seluruh PT.
func TestWholeCompanyRoutes(t *testing.T) {
	cases := map[string]bool{
		"/api/v1/journals":                    true,
		"/api/v1/ledger/accounts/{id}/ledger": true,
		"/api/v1/tax/output-invoices":         true,
		"/api/v1/reports/balance-sheet":       true,
		"/api/v1/reports/dashboard":           true,
		"/api/v1/reports/profit-loss":         false,
		"/api/v1/reports/{name}/export":       false,
		"/api/v1/tax/codes":                   false,
		"/api/v1/tax/bupot-ref":               false,
		"/api/v1/tax/slips":                   true,
		"/api/v1/tax/input-invoices":          true,
		"/api/v1/sales/invoices":              false,
		"/api/v1/cash/transactions":           false,
	}
	for path, want := range cases {
		if got := markWholeCompany(api.Route{Method: http.MethodGet, Path: path}).WholeCompany; got != want {
			t.Errorf("%s: WholeCompany = %v, mau %v", path, got, want)
		}
	}
	// Awal tahun buku boleh dibaca, tetapi hanya pengguna seluruh PT yang mengubahnya.
	if markWholeCompany(api.Route{Method: http.MethodGet, Path: "/api/v1/ledger/settings"}).WholeCompany {
		t.Error("GET /api/v1/ledger/settings tidak boleh dikunci")
	}
	if !markWholeCompany(api.Route{Method: http.MethodPut, Path: "/api/v1/ledger/settings"}).WholeCompany {
		t.Error("PUT /api/v1/ledger/settings harus dikunci")
	}
}

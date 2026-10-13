package server

import (
	"log/slog"
	"testing"
	"testing/fstest"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Setiap route wajib menyatakan mode lisensi dan izin yang ada di katalog,
// dan hanya login yang boleh diakses tanpa sesi.
func TestEveryRouteDeclaresLicenseAndPermission(t *testing.T) {
	_, a := New(testDeps())
	catalog := rbac.NewPerms(rbac.All)
	publicAllowed := map[string]bool{"POST /api/v1/auth/login": true}

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
		License: license.Static{}, Web: fstest.MapFS{},
	}.WithModules()
}

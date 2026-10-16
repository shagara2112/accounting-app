package server

import (
	"net/http"
	"testing"
	"time"

	"lajur/internal/platform/auth"
)

// clock adalah jam tiruan untuk sesi dan TOTP.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// wrongCode mengembalikan kode 6 digit yang pasti tidak sah di sekitar waktu t.
func wrongCode(secret []byte, t time.Time) string {
	valid := map[string]bool{}
	for d := -1; d <= 1; d++ {
		valid[auth.TOTPAt(secret, t.Add(time.Duration(d)*30*time.Second))] = true
	}
	for _, c := range []string{"000000", "111111", "222222", "333333"} {
		if !valid[c] {
			return c
		}
	}
	panic("tidak mungkin")
}

func mfaEnv(t *testing.T) (*testEnv, *clock) {
	t.Helper()
	// Mulai dari waktu nyata: last_seen_at sesi diisi now() basis data.
	clk := &clock{t: time.Now()}
	e := newEnvWith(t, activeLicense(), func(d *Deps) {
		d.MFARequired = true
		d.Sessions.Now = clk.now
	})
	return e, clk
}

func setupBody(email string) map[string]any {
	return map[string]any{
		"company": map[string]any{"name": "PT Setup Pertama", "shortName": "Pertama", "industry": "jasa"},
		"admin":   map[string]any{"name": "Pemilik Grup", "email": email, "password": testPassword},
	}
}

func TestSetupWizardThenMandatoryMFA(t *testing.T) {
	e, clk := mfaEnv(t)
	c := e.client()

	st := e.do(c, http.MethodGet, "/api/v1/setup", nil)
	expect(t, st, http.StatusOK, "")
	if st.body["required"] != true || st.body["remoteLicense"] != false {
		t.Fatalf("status setup = %v", st.body)
	}

	bad := setupBody("bukan-email")
	bad["admin"].(map[string]any)["password"] = "pendek"
	r := e.do(c, http.MethodPost, "/api/v1/setup", bad)
	expect(t, r, http.StatusUnprocessableEntity, "validation")
	fields := r.body["fields"].(map[string]any)
	if fields["admin.email"] == nil || fields["admin.password"] == nil {
		t.Fatalf("galat per field = %v", fields)
	}

	r = e.do(c, http.MethodPost, "/api/v1/setup", setupBody("pemilik@grup.test"))
	expect(t, r, http.StatusOK, "")
	if r.body["mfaSetupRequired"] != true {
		t.Fatal("Pemilik pertama harus langsung diminta memasang 2FA")
	}
	expect(t, e.do(e.client(), http.MethodPost, "/api/v1/setup", setupBody("lain@grup.test")), http.StatusConflict, "setup.done")

	// Sebelum 2FA terpasang, sesi hanya boleh dipakai untuk memasangnya.
	me := e.do(c, http.MethodGet, "/api/v1/me", nil)
	expect(t, me, http.StatusOK, "")
	if me.body["mfaSetupRequired"] != true || len(me.body["companies"].([]any)) != 1 {
		t.Fatalf("/me = %v", me.body)
	}
	expect(t, e.do(c, http.MethodGet, "/api/v1/admin/users", nil), http.StatusForbidden, "auth.mfa_setup_required")

	key := e.do(c, http.MethodPost, "/api/v1/auth/totp/setup", nil)
	expect(t, key, http.StatusOK, "")
	secret, err := auth.ParseTOTPKey(key.body["key"].(string))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp/enable", map[string]string{"code": wrongCode(secret, clk.t)}), http.StatusUnprocessableEntity, "validation")
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp/enable", map[string]string{"code": auth.TOTPAt(secret, clk.t)}), http.StatusOK, "")
	expect(t, e.do(c, http.MethodGet, "/api/v1/admin/users", nil), http.StatusOK, "")
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp/disable", map[string]string{"password": testPassword}), http.StatusForbidden, "auth.mfa_required")

	// Bagan akun PT pertama terisi dari template industrinya.
	accounts := e.do(c, http.MethodGet, "/api/v1/ledger/accounts", nil)
	expect(t, accounts, http.StatusOK, "")
	if len(accounts.body["items"].([]any)) < 50 {
		t.Fatalf("bagan akun PT pertama hanya %d akun", len(accounts.body["items"].([]any)))
	}

	// Login berikutnya butuh kode; kode yang sama tidak bisa dipakai dua kali.
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/logout", nil), http.StatusNoContent, "")
	c = e.client()
	step1 := e.login(c, "pemilik@grup.test", testPassword)
	expect(t, step1, http.StatusOK, "")
	if step1.body["totpRequired"] != true {
		t.Fatalf("login tahap pertama = %v", step1.body)
	}
	challenge := step1.body["challenge"].(string)
	expect(t, e.do(c, http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "auth.required")
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp", map[string]string{"challenge": challenge, "code": auth.TOTPAt(secret, clk.t)}), http.StatusUnauthorized, "auth.invalid_code")
	clk.t = clk.t.Add(30 * time.Second)
	code := auth.TOTPAt(secret, clk.t)
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp", map[string]string{"challenge": challenge, "code": code}), http.StatusOK, "")
	expect(t, e.do(c, http.MethodGet, "/api/v1/admin/users", nil), http.StatusOK, "")
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp", map[string]string{"challenge": challenge, "code": code}), http.StatusUnauthorized, "auth.challenge_expired")

	other := e.client()
	again := e.login(other, "pemilik@grup.test", testPassword)
	expect(t, e.do(other, http.MethodPost, "/api/v1/auth/totp", map[string]string{"challenge": again.body["challenge"].(string), "code": code}), http.StatusUnauthorized, "auth.invalid_code")

	// Challenge kedaluwarsa setelah lima menit.
	late := e.login(other, "pemilik@grup.test", testPassword)
	clk.t = clk.t.Add(6 * time.Minute)
	expect(t, e.do(other, http.MethodPost, "/api/v1/auth/totp", map[string]string{"challenge": late.body["challenge"].(string), "code": auth.TOTPAt(secret, clk.t)}), http.StatusUnauthorized, "auth.challenge_expired")
}

func TestTooManyWrongCodesEndsChallenge(t *testing.T) {
	e, clk := mfaEnv(t)
	c := e.client()
	expect(t, e.do(c, http.MethodPost, "/api/v1/setup", setupBody("pemilik@grup.test")), http.StatusOK, "")
	secret, _ := auth.ParseTOTPKey(e.do(c, http.MethodPost, "/api/v1/auth/totp/setup", nil).body["key"].(string))
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp/enable", map[string]string{"code": auth.TOTPAt(secret, clk.t)}), http.StatusOK, "")

	c = e.client()
	ch := e.login(c, "pemilik@grup.test", testPassword).body["challenge"].(string)
	for i := 0; i < 4; i++ {
		expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp", map[string]string{"challenge": ch, "code": wrongCode(secret, clk.t)}), http.StatusUnauthorized, "auth.invalid_code")
	}
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp", map[string]string{"challenge": ch, "code": wrongCode(secret, clk.t)}), http.StatusUnauthorized, "auth.challenge_expired")
	clk.t = clk.t.Add(30 * time.Second)
	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/totp", map[string]string{"challenge": ch, "code": auth.TOTPAt(secret, clk.t)}), http.StatusUnauthorized, "auth.challenge_expired")
}

func TestAdminManagesUsersRolesAndCompanies(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("pemilik@uji.test", "pemilik", e.companyA)
	admin := e.client()
	expect(t, e.login(admin, "pemilik@uji.test", testPassword), http.StatusOK, "")

	// Pengguna baru hanya bisa diberi akses di PT yang dikelola pembuatnya.
	r := e.do(admin, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"name": "Sari", "email": "sari@uji.test", "password": testPassword,
		"access": []map[string]any{{"companyId": e.companyB, "roleKey": "staf_akuntansi"}},
	})
	expect(t, r, http.StatusUnprocessableEntity, "validation")
	r = e.do(admin, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"name": "Sari", "email": "Sari@Uji.test", "password": testPassword,
		"access": []map[string]any{{"companyId": e.companyA, "roleKey": "staf_akuntansi"}},
	})
	expect(t, r, http.StatusCreated, "")
	sari := r.body["id"].(string)
	if r.body["email"] != "sari@uji.test" {
		t.Fatalf("email harus disimpan huruf kecil: %v", r.body["email"])
	}
	expect(t, e.do(admin, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"name": "Sari 2", "email": "sari@uji.test", "password": testPassword,
	}), http.StatusUnprocessableEntity, "validation")

	staff := e.client()
	expect(t, e.login(staff, "sari@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(staff, http.MethodGet, "/api/v1/admin/users", nil), http.StatusForbidden, "rbac.denied")

	// Persona kustom: izin hanya dari katalog, peran bawaan tidak bisa diubah.
	roles := e.do(admin, http.MethodGet, "/api/v1/admin/roles", nil)
	expect(t, roles, http.StatusOK, "")
	var builtinID string
	for _, it := range roles.body["items"].([]any) {
		if it.(map[string]any)["key"] == "manager" {
			builtinID = it.(map[string]any)["id"].(string)
		}
	}
	expect(t, e.do(admin, http.MethodPut, "/api/v1/admin/roles/"+builtinID, map[string]any{"name": "Manager", "permissions": []string{"cash.view"}, "version": 1}), http.StatusForbidden, "role.builtin")
	expect(t, e.do(admin, http.MethodPost, "/api/v1/admin/roles", map[string]any{"name": "Kasir", "permissions": []string{"ledger.journal.edit_posted"}}), http.StatusUnprocessableEntity, "validation")
	role := e.do(admin, http.MethodPost, "/api/v1/admin/roles", map[string]any{"name": "Kasir proyek", "permissions": []string{"project.view", "cash.create", "cash.view", "cash.view"}})
	expect(t, role, http.StatusCreated, "")
	if role.body["key"] != "custom_kasir_proyek" || len(role.body["permissions"].([]any)) != 3 {
		t.Fatalf("persona = %v", role.body)
	}
	roleID := role.body["id"].(string)
	expect(t, e.do(admin, http.MethodPut, "/api/v1/admin/users/"+sari+"/access", map[string]any{"companyId": e.companyA, "roleKey": "custom_kasir_proyek"}), http.StatusNoContent, "")
	me := e.do(staff, http.MethodGet, "/api/v1/me", nil)
	if me.body["companies"].([]any)[0].(map[string]any)["roleKey"] != "custom_kasir_proyek" {
		t.Fatalf("peran baru belum berlaku: %v", me.body)
	}
	expect(t, e.do(admin, http.MethodDelete, "/api/v1/admin/roles/"+roleID, nil), http.StatusConflict, "role.in_use")
	expect(t, e.do(admin, http.MethodPut, "/api/v1/admin/roles/"+roleID, map[string]any{"name": "Kasir proyek", "permissions": []string{"cash.view"}, "version": 9}), http.StatusConflict, "conflict.version")
	expect(t, e.do(admin, http.MethodPut, "/api/v1/admin/roles/"+roleID, map[string]any{"name": "Kasir lapangan", "permissions": []string{"cash.view", "cash.create"}, "version": 1}), http.StatusOK, "")

	// Pengelola tidak bisa mengubah akses atau status akunnya sendiri.
	me = e.do(admin, http.MethodGet, "/api/v1/me", nil)
	self := me.body["user"].(map[string]any)["id"].(string)
	expect(t, e.do(admin, http.MethodPut, "/api/v1/admin/users/"+self+"/access", map[string]any{"companyId": e.companyA, "roleKey": ""}), http.StatusForbidden, "admin.self")
	expect(t, e.do(admin, http.MethodPut, "/api/v1/admin/users/"+self, map[string]any{"name": "Pemilik", "email": "pemilik@uji.test", "active": false, "version": 1}), http.StatusForbidden, "admin.self")

	// Reset kata sandi dan menonaktifkan pengguna langsung mengakhiri sesinya.
	expect(t, e.do(admin, http.MethodPost, "/api/v1/admin/users/"+sari+"/password", map[string]any{"password": "kata-sandi-baru-sari"}), http.StatusNoContent, "")
	expect(t, e.do(staff, http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "auth.required")
	expect(t, e.login(staff, "sari@uji.test", "kata-sandi-baru-sari"), http.StatusOK, "")
	users := e.do(admin, http.MethodGet, "/api/v1/admin/users", nil)
	var version float64
	for _, it := range users.body["items"].([]any) {
		if it.(map[string]any)["id"] == sari {
			version = it.(map[string]any)["version"].(float64)
		}
	}
	expect(t, e.do(admin, http.MethodPut, "/api/v1/admin/users/"+sari, map[string]any{"name": "Sari W", "email": "sari@uji.test", "active": false, "version": version}), http.StatusOK, "")
	expect(t, e.do(staff, http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "auth.required")
	expect(t, e.login(staff, "sari@uji.test", "kata-sandi-baru-sari"), http.StatusUnauthorized, "auth.invalid_credentials")

	// PT baru langsung punya bagan akun, dan pembuatnya mendapat perannya di PT aktif.
	expect(t, e.do(admin, http.MethodPost, "/api/v1/admin/companies", map[string]any{"name": "PT Uji A", "shortName": "Dup"}), http.StatusUnprocessableEntity, "validation")
	pt := e.do(admin, http.MethodPost, "/api/v1/admin/companies", map[string]any{"name": "PT Cabang Baru", "shortName": "Cabang", "industry": "retail"})
	expect(t, pt, http.StatusCreated, "")
	ptID := pt.body["id"].(string)
	me = e.do(admin, http.MethodGet, "/api/v1/me", nil)
	found := false
	for _, c := range me.body["companies"].([]any) {
		m := c.(map[string]any)
		found = found || (m["id"] == ptID && m["roleKey"] == "pemilik")
	}
	if !found {
		t.Fatalf("pembuat PT belum mendapat akses Pemilik: %v", me.body["companies"])
	}
	acc := e.do(admin, http.MethodGet, "/api/v1/ledger/accounts", nil, "X-Company-Id", ptID)
	expect(t, acc, http.StatusOK, "")
	if len(acc.body["items"].([]any)) < 50 {
		t.Fatal("PT baru belum punya bagan akun dari template")
	}

	// Staf akuntansi tidak bisa membuat PT.
	expect(t, e.do(e.client(), http.MethodPost, "/api/v1/admin/companies", map[string]any{"name": "PT X", "shortName": "X"}), http.StatusUnauthorized, "auth.required")
}

func TestLastUserManagerCannotBeRemoved(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("pemilik@uji.test", "pemilik", e.companyA)
	e.user("ti@uji.test", "staf_akuntansi", e.companyA)
	pemilik := e.client()
	expect(t, e.login(pemilik, "pemilik@uji.test", testPassword), http.StatusOK, "")
	ids := map[string]string{}
	for _, it := range e.do(pemilik, http.MethodGet, "/api/v1/admin/users", nil).body["items"].([]any) {
		m := it.(map[string]any)
		ids[m["email"].(string)] = m["id"].(string)
	}
	role := e.do(pemilik, http.MethodPost, "/api/v1/admin/roles", map[string]any{"name": "Admin TI", "permissions": []string{"core.company.view", "platform.user.manage"}})
	expect(t, role, http.StatusCreated, "")
	expect(t, e.do(pemilik, http.MethodPut, "/api/v1/admin/users/"+ids["ti@uji.test"]+"/access", map[string]any{"companyId": e.companyA, "roleKey": role.body["key"]}), http.StatusNoContent, "")

	// Admin TI menurunkan Pemilik: masih ada pengelola (dirinya), jadi boleh.
	ti := e.client()
	expect(t, e.login(ti, "ti@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(ti, http.MethodPut, "/api/v1/admin/users/"+ids["pemilik@uji.test"]+"/access", map[string]any{"companyId": e.companyA, "roleKey": "staf_akuntansi"}), http.StatusNoContent, "")

	// Mencabut izin kelola pengguna dari persona pengelola terakhir ditolak.
	r := e.do(ti, http.MethodPut, "/api/v1/admin/roles/"+role.body["id"].(string), map[string]any{"name": "Admin TI", "permissions": []string{"core.company.view"}, "version": 1})
	expect(t, r, http.StatusConflict, "admin.last_admin")
	expect(t, e.do(ti, http.MethodGet, "/api/v1/admin/users", nil), http.StatusOK, "")
}

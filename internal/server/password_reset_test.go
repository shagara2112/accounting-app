package server

import (
	"context"
	"net/http"
	"regexp"
	"testing"
)

// Revisi klien: lupa kata sandi dari halaman masuk. Tanpa email server, permintaan hanya ditandai
// untuk Admin; dengan email server, kode 6 digit dikirim dan dipakai sekali untuk kata sandi baru.
func TestForgotPasswordFlow(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("lupa@uji.test", "staf_akuntansi", e.companyA)
	ctx := context.Background()
	c := e.client()
	request := func(email string) result {
		return e.do(c, http.MethodPost, "/api/v1/auth/password-reset/request", map[string]string{"email": email})
	}
	confirm := func(code, password string) result {
		return e.do(c, http.MethodPost, "/api/v1/auth/password-reset/confirm", map[string]string{"email": "lupa@uji.test", "code": code, "password": password})
	}
	var requested *string
	requestedAt := func() bool {
		requested = nil
		if err := e.pool.QueryRow(ctx, "SELECT reset_requested_at::text FROM platform.users WHERE email = 'lupa@uji.test'").Scan(&requested); err != nil {
			t.Fatal(err)
		}
		return requested != nil
	}

	// Email server belum diatur: jawaban sama untuk email terdaftar atau tidak, permintaan ditandai.
	r := request("lupa@uji.test")
	expect(t, r, http.StatusOK, "")
	if r.body["emailEnabled"] != false || !requestedAt() {
		t.Fatalf("tanpa email server = %v, ditandai %v", r.body, requested)
	}
	if u := request("tidak-ada@uji.test"); u.status != http.StatusOK || u.body["emailEnabled"] != false || len(u.body) != len(r.body) {
		t.Fatalf("email tak terdaftar seharusnya dijawab sama: %v", u.body)
	}
	expect(t, request("bukan-email"), http.StatusUnprocessableEntity, "validation")

	// Email server diatur: kode dikirim lewat antrean email.
	if _, err := e.pool.Exec(ctx, "UPDATE platform.mail_settings SET enabled = true, host = 'smtp.uji.test', from_addr = 'lajur@uji.test'"); err != nil {
		t.Fatal(err)
	}
	r = request("lupa@uji.test")
	if r.body["emailEnabled"] != true {
		t.Fatalf("dengan email server = %v", r.body)
	}
	var subject string
	if err := e.pool.QueryRow(ctx, "SELECT subject FROM platform.mail_outbox WHERE kind = 'password_reset' AND 'lupa@uji.test' = ANY(to_addrs) ORDER BY created_at DESC LIMIT 1").Scan(&subject); err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`\d{6}$`).FindString(subject)
	if code == "" {
		t.Fatalf("subjek email tanpa kode: %q", subject)
	}

	// Sesi lama ikut berakhir sesudah reset.
	old := e.client()
	expect(t, e.login(old, "lupa@uji.test", testPassword), http.StatusOK, "")

	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	expect(t, confirm(wrong, "kata-sandi-baru-2027"), http.StatusUnprocessableEntity, "auth.reset_invalid")
	expect(t, confirm(code, "pendek"), http.StatusUnprocessableEntity, "validation")
	expect(t, confirm(code, "kata-sandi-baru-2027"), http.StatusOK, "")
	if requestedAt() {
		t.Fatal("permintaan reset seharusnya selesai")
	}
	expect(t, confirm(code, "kata-sandi-lain-2027"), http.StatusUnprocessableEntity, "auth.reset_invalid")
	expect(t, e.do(old, http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "auth.required")
	expect(t, e.login(e.client(), "lupa@uji.test", testPassword), http.StatusUnauthorized, "auth.invalid_credentials")
	expect(t, e.login(e.client(), "lupa@uji.test", "kata-sandi-baru-2027"), http.StatusOK, "")

	// Paling banyak 3 kode per 15 menit; kode kedaluwarsa dan 5 kali salah ditolak.
	for range 4 {
		request("lupa@uji.test")
	}
	var n int
	if err := e.pool.QueryRow(ctx, "SELECT count(*) FROM platform.password_resets r JOIN platform.users u ON u.id = r.user_id WHERE u.email = 'lupa@uji.test' AND r.created_at > now() - interval '15 minutes'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("kode dalam 15 menit = %d, seharusnya 3", n)
	}
	if _, err := e.pool.Exec(ctx, "UPDATE platform.password_resets SET attempts = 5 WHERE used_at IS NULL"); err != nil {
		t.Fatal(err)
	}
	if r := confirm(code, "kata-sandi-baru-2028"); r.status != http.StatusUnprocessableEntity || r.body["code"] != "auth.reset_invalid" {
		t.Fatalf("kode yang terlalu banyak salah seharusnya ditolak: %v", r.body)
	}
}

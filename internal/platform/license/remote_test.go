package license_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lajur/internal/platform/dbtest"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/secret"
)

// fakeConsole meniru endpoint lajur.activate dan lajur.checkin di License Console.
type fakeConsole struct {
	mu           sync.Mutex
	key          *rsa.PrivateKey
	kid          string
	clientID     string
	licenseKey   string
	product      string
	installation string
	status       string
	expiresAt    string
	minVersion   string
	now          func() time.Time
	down         bool
	lastBody     map[string]any
}

func (f *fakeConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, "down", http.StatusBadGateway)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.lastBody = body
	reply := func(v map[string]any) { _ = json.NewEncoder(w).Encode(v) }
	if body["clientId"] != f.clientID || body["licenseKey"] != f.licenseKey {
		reply(map[string]any{"ok": false, "code": "INVALID_KEY", "message": "Kunci lisensi tidak valid."})
		return
	}
	if f.product != "lajur" {
		reply(map[string]any{"ok": false, "code": "WRONG_PRODUCT", "message": "salah produk"})
		return
	}
	inst, _ := body["installationId"].(string)
	switch body["action"] {
	case "lajur.activate":
		if f.installation != "" && f.installation != inst {
			reply(map[string]any{"ok": false, "code": "INSTALLATION_MISMATCH", "message": "server lain"})
			return
		}
		f.installation = inst
	case "lajur.checkin":
		if f.installation != inst {
			reply(map[string]any{"ok": false, "code": "NOT_ACTIVATED", "message": "belum aktif"})
			return
		}
	default:
		reply(map[string]any{"ok": false, "code": "BAD_REQUEST"})
		return
	}
	reply(map[string]any{"ok": true, "token": f.sign(inst)})
}

func (f *fakeConsole) sign(inst string) string {
	iat := f.now().Unix()
	claims := map[string]any{
		"clientId": f.clientID, "product": "lajur", "status": f.status, "expiresAt": f.expiresAt,
		"timezone": "Asia/Jakarta", "warningDays": 7, "minVersion": f.minVersion,
		"installationId": inst, "iat": iat, "exp": iat + 72*3600,
	}
	return signToken(f.key, f.kid, claims)
}

func signToken(key *rsa.PrivateKey, kid string, claims map[string]any) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	input := enc(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}) + "." + enc(claims)
	digest := sha256.Sum256([]byte(input))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	remote  *license.Remote
	console *fakeConsole
	url     string
	clk     *clock
	ctx     context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := dbtest.Pool(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Now()}
	fc := &fakeConsole{
		key: key, kid: "uji-1", clientID: "PT-ARTA", licenseKey: "LIC-AAAA-BBBB-CCCC-DDDD", product: "lajur",
		status: "ACTIVE", expiresAt: clk.now().AddDate(0, 3, 0).Format("2006-01-02"), now: clk.now,
	}
	srv := httptest.NewServer(fc)
	t.Cleanup(srv.Close)
	box, _ := secret.New([]byte("kunci-uji-lajur-tiga-puluh-dua-b"))
	ctx := context.Background()
	r, err := license.NewRemote(ctx, license.RemoteConfig{
		Pool: pool, Box: box, Keys: map[string]*rsa.PublicKey{"uji-1": &key.PublicKey},
		InstallationUUID: "0190a000-0000-7000-8000-000000000001", MachineID: "mesin-uji\n", Version: "1.2.0",
		Now: clk.now, AllowInsecure: true,
		Health: func(context.Context) map[string]any { return map[string]any{"companies": 3} },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &env{remote: r, console: fc, url: srv.URL + "/exec", clk: clk, ctx: ctx}
}

func (e *env) activate(t *testing.T) {
	t.Helper()
	if err := e.remote.Activate(e.ctx, license.Activation{ConsoleURL: e.url, ClientID: "PT-ARTA", LicenseKey: "LIC-AAAA-BBBB-CCCC-DDDD"}); err != nil {
		t.Fatalf("aktivasi: %v", err)
	}
}

func fieldErr(t *testing.T, err error, field string) string {
	t.Helper()
	var p *httpx.Problem
	if !errors.As(err, &p) || p.Fields[field] == "" {
		t.Fatalf("seharusnya galat di field %s, dapat %v", field, err)
	}
	return p.Fields[field]
}

func TestRemoteActivationBindsInstallation(t *testing.T) {
	e := newEnv(t)
	if st := e.remote.Status(e.ctx); st.State != license.Unverified {
		t.Fatalf("sebelum aktivasi status = %s", st.State)
	}
	fieldErr(t, e.remote.Activate(e.ctx, license.Activation{ConsoleURL: e.url, ClientID: "PT-ARTA", LicenseKey: "LIC-SALAH"}), "license.licenseKey")
	fieldErr(t, e.remote.Activate(e.ctx, license.Activation{ConsoleURL: "ftp://x", ClientID: "PT-ARTA", LicenseKey: "x"}), "license.consoleUrl")

	e.activate(t)
	st := e.remote.Status(e.ctx)
	if st.State != license.Active || !st.Allows(license.ModeWrite) {
		t.Fatalf("setelah aktivasi status = %+v", st)
	}
	if e.console.installation != e.remote.InstallationID() || len(e.remote.InstallationID()) != 64 {
		t.Fatalf("installationId tidak terikat: %q", e.console.installation)
	}
	d := e.remote.Detail(e.ctx)
	if !d.Configured || d.ClientID != "PT-ARTA" || d.TokenValidTo == nil {
		t.Fatalf("detail = %+v", d)
	}

	// Server lain dengan key yang sama ditolak sampai instalasinya direset di console.
	e.console.installation = "instalasi-lain"
	msg := fieldErr(t, e.remote.Activate(e.ctx, license.Activation{ConsoleURL: e.url, ClientID: "PT-ARTA", LicenseKey: "LIC-AAAA-BBBB-CCCC-DDDD"}), "license.licenseKey")
	if msg == "" {
		t.Fatal("pesan instalasi lain kosong")
	}
}

func TestRemoteCheckinFollowsConsoleState(t *testing.T) {
	e := newEnv(t)
	e.activate(t)

	if err := e.remote.CheckIn(e.ctx); err != nil {
		t.Fatal(err)
	}
	if h, _ := e.console.lastBody["health"].(map[string]any); h["companies"] != float64(3) {
		t.Fatalf("ringkasan kesehatan tidak terkirim: %v", e.console.lastBody)
	}

	e.console.status = "SUSPENDED"
	if err := e.remote.CheckIn(e.ctx); err != nil {
		t.Fatal(err)
	}
	if st := e.remote.Status(e.ctx); st.State != license.Suspended || st.Allows(license.ModeRead) || !st.Allows(license.ModeExport) {
		t.Fatalf("suspend = %+v", st)
	}

	e.console.status, e.console.expiresAt = "ACTIVE", e.clk.now().AddDate(0, 0, -1).Format("2006-01-02")
	_ = e.remote.CheckIn(e.ctx)
	if st := e.remote.Status(e.ctx); st.State != license.Expired || st.Allows(license.ModeWrite) || !st.Allows(license.ModeRead) {
		t.Fatalf("expired = %+v", st)
	}

	// Versi minimum dari console (LIC-10): hanya baca sampai diperbarui.
	e.console.expiresAt, e.console.minVersion = e.clk.now().AddDate(0, 6, 0).Format("2006-01-02"), "1.3.0"
	_ = e.remote.CheckIn(e.ctx)
	st := e.remote.Status(e.ctx)
	if !st.UpdateRequired || st.Allows(license.ModeWrite) || !st.Allows(license.ModeRead) || st.MinVersion != "1.3.0" {
		t.Fatalf("versi minimum = %+v", st)
	}

	// Key direset di console: check-in berikutnya mengunci aplikasi (INVALID_KEY).
	e.console.minVersion, e.console.licenseKey = "", "LIC-BARU-BARU-BARU-BARU"
	if err := e.remote.CheckIn(e.ctx); err == nil {
		t.Fatal("check-in dengan key lama seharusnya gagal")
	}
	if st := e.remote.Status(e.ctx); st.State != license.InvalidKey {
		t.Fatalf("setelah reset key status = %+v", st)
	}
}

func TestRemoteOfflineAndClockRollback(t *testing.T) {
	e := newEnv(t)
	e.activate(t)

	// Gangguan jaringan tidak terasa selama token masih berlaku 72 jam.
	e.console.down = true
	e.clk.add(48 * time.Hour)
	if err := e.remote.CheckIn(e.ctx); err == nil {
		t.Fatal("check-in saat console mati seharusnya gagal")
	}
	if st := e.remote.Status(e.ctx); st.State != license.Active {
		t.Fatalf("48 jam offline status = %+v", st)
	}
	e.clk.add(25 * time.Hour)
	if st := e.remote.Status(e.ctx); st.State != license.Unverified || st.Allows(license.ModeWrite) || !st.Allows(license.ModeRead) {
		t.Fatalf("73 jam offline status = %+v", st)
	}

	e.console.down = false
	if err := e.remote.CheckIn(e.ctx); err != nil {
		t.Fatal(err)
	}
	if st := e.remote.Status(e.ctx); st.State != license.Active {
		t.Fatalf("setelah online lagi status = %+v", st)
	}

	// Jam dimundurkan lebih dari 10 menit dari waktu tertinggi yang pernah terlihat.
	e.clk.add(-30 * time.Minute)
	if st := e.remote.Status(e.ctx); st.State != license.Unverified {
		t.Fatalf("jam mundur status = %+v", st)
	}
	e.clk.add(25 * time.Minute)
	if st := e.remote.Status(e.ctx); st.State != license.Active {
		t.Fatalf("selisih 5 menit masih ditoleransi, status = %+v", st)
	}
}

func TestRemoteRejectsForgedToken(t *testing.T) {
	e := newEnv(t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	e.console.key = other // console palsu dengan kunci lain
	fieldErr(t, e.remote.Activate(e.ctx, license.Activation{ConsoleURL: e.url, ClientID: "PT-ARTA", LicenseKey: "LIC-AAAA-BBBB-CCCC-DDDD"}), "license.consoleUrl")
	if st := e.remote.Status(e.ctx); st.State != license.Unverified {
		t.Fatalf("token palsu tidak boleh mengaktifkan lisensi: %+v", st)
	}
}

func TestRemoteRecheckIsRateLimited(t *testing.T) {
	e := newEnv(t)
	e.activate(t)
	if err := e.remote.Recheck(e.ctx); err != nil {
		t.Fatal(err)
	}
	var p *httpx.Problem
	if err := e.remote.Recheck(e.ctx); !errors.As(err, &p) || p.Status != http.StatusTooManyRequests {
		t.Fatalf("cek ulang kedua dalam 30 detik seharusnya ditolak, dapat %v", err)
	}
	e.clk.add(31 * time.Second)
	if err := e.remote.Recheck(e.ctx); err != nil {
		t.Fatal(err)
	}
}

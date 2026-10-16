package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lajur/internal/platform/backup"
	"lajur/internal/platform/jws"
	"lajur/internal/platform/license"
	"lajur/internal/platform/update"
)

func TestSystemUpdatesCheckAndApply(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	m := update.Manifest{Product: "lajur", Channel: "stable", Version: "99.0.0", Notes: "Versi uji", PublishedAt: time.Now(),
		Image: "ghcr.io/lajur/lajur@sha256:3333333333333333333333333333333333333333333333333333333333333333"}
	tok, _ := jws.Sign(key, "rilis", m)
	manifests := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tok)) }))
	defer manifests.Close()

	var mu sync.Mutex
	var applied, auth string
	updaterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auth = r.Header.Get("Authorization")
		if r.URL.Path == "/apply" {
			var body struct{ Manifest string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			applied = body.Manifest
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"running","step":"Mengunduh image 99.0.0","log":[]}`))
	}))
	defer updaterSrv.Close()

	_, pubStr, _ := backup.NewKey()
	pub, _ := backup.ParsePublicKey(pubStr)
	lic := &switchable{s: license.Status{State: license.Active, Message: "Lisensi aktif."}}
	e := newEnvWith(t, lic, func(d *Deps) {
		d.Updates = &Updates{ManifestURL: manifests.URL, UpdaterURL: updaterSrv.URL, Token: "token-updater-uji", Keys: map[string]*rsa.PublicKey{"rilis": &key.PublicKey}}
		d.Backup = backup.New(backup.Config{Pool: d.Pool, PublicKey: pub})
	})
	e.user("pemilik@uji.test", "pemilik", e.companyA)
	c := e.client()
	expect(t, e.login(c, "pemilik@uji.test", testPassword), http.StatusOK, "")

	chk := e.do(c, http.MethodGet, "/api/v1/system/updates", nil)
	expect(t, chk, http.StatusOK, "")
	if chk.body["available"] != true || chk.body["latest"].(map[string]any)["version"] != "99.0.0" || chk.body["licenseOk"] != true || chk.body["backupOk"] != true {
		t.Fatalf("cek pembaruan = %v", chk.body)
	}

	expect(t, e.do(c, http.MethodPost, "/api/v1/system/updates/apply", nil), http.StatusAccepted, "")
	mu.Lock()
	if applied != tok || auth != "Bearer token-updater-uji" {
		t.Fatalf("updater menerima manifest %q dengan auth %q", applied[:min(len(applied), 20)], auth)
	}
	mu.Unlock()
	st := e.do(c, http.MethodGet, "/api/v1/system/updates/status", nil)
	if st.body["status"] != "running" {
		t.Fatalf("status = %v", st.body)
	}

	// Lisensi berakhir: pembaruan tidak dilayani.
	lic.set(license.Status{State: license.Expired, Message: "Masa aktif lisensi telah berakhir."})
	expect(t, e.do(c, http.MethodPost, "/api/v1/system/updates/apply", nil), http.StatusForbidden, "update.license")
}

// switchable adalah provider lisensi tiruan yang statusnya bisa diganti di tengah tes.
type switchable struct {
	mu sync.Mutex
	s  license.Status
}

func (p *switchable) Status(context.Context) license.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.s
}

func (p *switchable) set(s license.Status) { p.mu.Lock(); p.s = s; p.mu.Unlock() }

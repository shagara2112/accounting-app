package update

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lajur/internal/platform/jws"
)

const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testKeys(t *testing.T) (*rsa.PrivateKey, map[string]*rsa.PublicKey) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k, map[string]*rsa.PublicKey{"rilis-uji": &k.PublicKey}
}

func valid() Manifest {
	return Manifest{Product: "lajur", Channel: "stable", Version: "1.3.0", Image: "ghcr.io/lajur/lajur@" + digest, MinFrom: "1.1.0", Notes: "Perbaikan Coretax", PublishedAt: time.Now()}
}

func TestManifestSignVerifyAndValidate(t *testing.T) {
	key, keys := testKeys(t)
	tok, err := jws.Sign(key, "rilis-uji", valid())
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(tok, keys)
	if err != nil || m.Version != "1.3.0" {
		t.Fatalf("parse = %+v, %v", m, err)
	}
	if !m.Newer("1.2.9") || m.Newer("1.3.0") || !m.Reachable("1.1.0") || m.Reachable("1.0.9") {
		t.Fatal("perbandingan versi salah")
	}

	// Isi diubah setelah ditandatangani.
	parts := strings.Split(tok, ".")
	other, _ := jws.Sign(key, "rilis-uji", Manifest{Product: "lajur", Version: "9.9.9", Image: "evil.example/x@" + digest})
	forged := parts[0] + "." + strings.Split(other, ".")[1] + "." + parts[2]
	if _, err := Parse(forged, keys); err == nil {
		t.Fatal("manifest yang diubah harus ditolak")
	}
	// Ditandatangani kunci lain.
	stranger, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok2, _ := jws.Sign(stranger, "rilis-uji", valid())
	if _, err := Parse(tok2, keys); err == nil {
		t.Fatal("kunci asing harus ditolak")
	}

	for name, mut := range map[string]func(*Manifest){
		"tag tanpa digest": func(m *Manifest) { m.Image = "ghcr.io/lajur/lajur:1.3.0" },
		"produk lain":      func(m *Manifest) { m.Product = "pos" },
		"versi":            func(m *Manifest) { m.Version = "1.3" },
		"updater tanpa digest": func(m *Manifest) {
			m.UpdaterImage = "ghcr.io/lajur/lajur-updater:latest"
		},
	} {
		m := valid()
		mut(&m)
		tok, _ := jws.Sign(key, "rilis-uji", m)
		if _, err := Parse(tok, keys); err == nil {
			t.Errorf("%s seharusnya ditolak", name)
		}
	}
}

func TestFetchSendsLicenseHeaders(t *testing.T) {
	key, keys := testKeys(t)
	tok, _ := jws.Sign(key, "rilis-uji", valid())
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(tok + "\n"))
	}))
	defer srv.Close()
	m, raw, err := Fetch(context.Background(), srv.Client(), srv.URL+"/stable.jws", map[string]string{"Authorization": "Bearer token-lisensi"}, keys)
	if err != nil || m.Version != "1.3.0" || raw != tok || got != "Bearer token-lisensi" {
		t.Fatalf("fetch = %+v %v %q", m, err, got)
	}
}

func TestTrustedKeysIncludeDevKeyInDevBuild(t *testing.T) {
	keys, err := TrustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	if keys["lajur-update-dev"] == nil {
		t.Fatalf("kunci dev tidak dimuat: %v", keys)
	}
}

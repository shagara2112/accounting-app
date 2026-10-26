package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRedirectHTTPSKeepsPathAndServesCA(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nuji\n-----END CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := redirectHTTPS(ca)

	req := httptest.NewRequest(http.MethodGet, "http://192.168.1.50:80/kas?id=1&draf=1", nil)
	req.Host = "192.168.1.50:80"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "https://192.168.1.50/kas?id=1&draf=1" {
		t.Fatalf("redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://kantor/ca.crt", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-x509-ca-cert" {
		t.Fatalf("ca.crt = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestHealthcheckCallsLocalServer(t *testing.T) {
	ok := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	t.Setenv("LAJUR_HEALTH_URL", ok.URL+"/healthz")
	if err := healthcheck(t.Context()); err != nil {
		t.Fatalf("server sehat seharusnya lolos: %v", err)
	}
	t.Setenv("LAJUR_HEALTH_URL", ok.URL+"/lain")
	if err := healthcheck(t.Context()); err == nil {
		t.Fatal("status selain 200 seharusnya gagal")
	}
}

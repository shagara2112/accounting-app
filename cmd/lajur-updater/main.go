// Perintah lajur-updater: layanan pembaruan di container terpisah (lihat internal/updater).
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"lajur/internal/platform/update"
	"lajur/internal/updater"
)

type dockerCLI struct{}

func (dockerCLI) Run(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return string(out), err
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		fmt.Fprintln(os.Stderr, "galat:", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	dir := env("LAJUR_DIR", "/opt/lajur")
	token := os.Getenv("LAJUR_UPDATER_TOKEN")
	if len(token) < 32 {
		return errors.New("LAJUR_UPDATER_TOKEN wajib diisi (minimal 32 karakter)")
	}
	keys, err := update.TrustedKeys()
	if err != nil {
		return err
	}
	health, err := healthChecker(env("LAJUR_APP_HEALTH", "https://lajur-app:8443/healthz"), filepath.Join(dir, "tls", "ca.crt"))
	if err != nil {
		return err
	}
	u := updater.New(updater.Config{Dir: dir, Token: token, Keys: keys, Docker: dockerCLI{}, Health: health, Log: log})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: env("LAJUR_UPDATER_LISTEN", ":9000"), Handler: u.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("updater berjalan", "alamat", srv.Addr)
	select {
	case <-ctx.Done():
		u.Wait()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case err := <-errc:
		return err
	}
}

// healthChecker memanggil /healthz aplikasi lewat jaringan internal. Sertifikat aplikasi
// diterbitkan CA lokal untuk nama "localhost" (selain hostname dan IP LAN), jadi nama itulah
// yang diverifikasi walau koneksi menuju container lajur-app.
func healthChecker(url, caFile string) (func(context.Context) (string, error), error) {
	pool := x509.NewCertPool()
	if pem, err := os.ReadFile(caFile); err == nil {
		pool.AppendCertsFromPEM(pem)
	} else {
		return nil, fmt.Errorf("CA lokal %s: %w", caFile, err)
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12},
	}}
	return func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var h struct {
			Status  string `json:"status"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusOK || h.Status != "ok" {
			return "", fmt.Errorf("health %d %s", resp.StatusCode, h.Status)
		}
		return h.Version, nil
	}, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

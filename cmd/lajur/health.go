package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"
)

// healthcheck dipakai HEALTHCHECK Docker: memanggil /healthz server di container yang sama dari
// binary ini sendiri. Sebelumnya dipakai wget busybox, yang setiap 30 detik meninggalkan proses
// zombie ssl_client (lajur sebagai PID 1 tidak memungutnya) sampai batas proses container habis
// dan docker exec (mis. backup sebelum pembaruan) gagal.
func healthcheck(ctx context.Context) error {
	url := os.Getenv("LAJUR_HEALTH_URL")
	if url == "" {
		url = "https://127.0.0.1:8443/healthz"
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// Sertifikat server lokal ditandatangani CA instalasi sendiri; yang dipanggil hanya 127.0.0.1.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz menjawab %d", resp.StatusCode)
	}
	return nil
}

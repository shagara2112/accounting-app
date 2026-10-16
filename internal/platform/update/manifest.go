// Package update menangani manifest rilis bertanda tangan (PLT-10): mengambil dan
// memverifikasinya di aplikasi, dan memverifikasinya lagi di updater sebelum image dijalankan.
package update

import (
	"context"
	"crypto/rsa"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"lajur/internal/platform/buildinfo"
	"lajur/internal/platform/jws"
	"lajur/internal/platform/license"
)

// Manifest menjelaskan satu rilis. Image selalu disematkan dengan digest, sehingga isi image
// yang dijalankan persis yang ditandatangani.
type Manifest struct {
	Product      string    `json:"product"`
	Channel      string    `json:"channel"`
	Version      string    `json:"version"`
	Image        string    `json:"image"`
	UpdaterImage string    `json:"updaterImage,omitempty"`
	MinFrom      string    `json:"minFrom,omitempty"` // versi terendah yang boleh langsung naik ke rilis ini
	Notes        string    `json:"notes"`
	PublishedAt  time.Time `json:"publishedAt"`
}

var (
	semver   = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	imageRef = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(\.[a-z0-9-]+)*(:\d+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+@sha256:[0-9a-f]{64}$`)
)

func (m Manifest) Validate() error {
	switch {
	case m.Product != "lajur":
		return errors.New("manifest bukan untuk Lajur")
	case !semver.MatchString(m.Version):
		return fmt.Errorf("versi %q tidak valid", m.Version)
	case !imageRef.MatchString(m.Image):
		return fmt.Errorf("image %q harus disematkan dengan digest sha256", m.Image)
	case m.UpdaterImage != "" && !imageRef.MatchString(m.UpdaterImage):
		return fmt.Errorf("image updater %q harus disematkan dengan digest sha256", m.UpdaterImage)
	case m.MinFrom != "" && !semver.MatchString(m.MinFrom):
		return fmt.Errorf("minFrom %q tidak valid", m.MinFrom)
	}
	return nil
}

// Newer: rilis ini lebih baru dari versi yang sedang berjalan.
func (m Manifest) Newer(current string) bool { return buildinfo.Less(current, m.Version) }

// Reachable: versi yang berjalan boleh langsung naik ke rilis ini.
func (m Manifest) Reachable(current string) bool {
	return m.MinFrom == "" || !buildinfo.Less(current, m.MinFrom)
}

// Parse memverifikasi tanda tangan manifest dan isinya.
func Parse(token string, keys map[string]*rsa.PublicKey) (Manifest, error) {
	var m Manifest
	if err := jws.Verify(token, keys, &m); err != nil {
		return m, fmt.Errorf("manifest rilis ditolak: %w", err)
	}
	return m, m.Validate()
}

//go:embed keys
var keyFiles embed.FS

// TrustedKeys: kunci publik manifest yang dibawa binary (dev hanya di build pengembangan).
func TrustedKeys() (map[string]*rsa.PublicKey, error) {
	out := map[string]*rsa.PublicKey{}
	dirs := []string{"keys/release"}
	if license.DevBuild {
		dirs = append(dirs, "keys/dev")
	}
	for _, dir := range dirs {
		entries, err := fs.ReadDir(keyFiles, dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".pem") {
				continue
			}
			raw, err := keyFiles.ReadFile(path.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			k, err := license.ParsePublicKey(raw)
			if err != nil {
				return nil, fmt.Errorf("kunci manifest %s: %w", e.Name(), err)
			}
			out[strings.TrimSuffix(e.Name(), ".pem")] = k
		}
	}
	if len(out) == 0 {
		return nil, errors.New("binary ini tidak membawa kunci publik manifest rilis (internal/platform/update/keys/release)")
	}
	return out, nil
}

// Fetch mengambil manifest dari update server. headers boleh berisi token lisensi supaya
// update server bisa menolak instalasi yang lisensinya tidak aktif.
func Fetch(ctx context.Context, client *http.Client, url string, headers map[string]string, keys map[string]*rsa.PublicKey) (Manifest, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Manifest{}, "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("update server tidak bisa dihubungi: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, "", fmt.Errorf("update server menjawab HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return Manifest{}, "", err
	}
	token := strings.TrimSpace(string(raw))
	m, err := Parse(token, keys)
	return m, token, err
}

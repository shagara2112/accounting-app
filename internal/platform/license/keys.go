package license

import (
	"crypto/rsa"
	"crypto/x509"
	"embed"
	"encoding/pem"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// Kunci publik License Console yang dibawa binary. Build pengembangan juga mempercayai
// kunci di keys/dev (pasangannya dipakai License Console tiruan di laptop); build rilis
// hanya mempercayai keys/release.
//
//go:embed keys
var keyFiles embed.FS

// TrustedKeys mengembalikan kunci publik per kid (nama file tanpa .pem).
func TrustedKeys() (map[string]*rsa.PublicKey, error) {
	out := map[string]*rsa.PublicKey{}
	dirs := []string{"keys/release"}
	if trustDevKeys {
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
			k, err := ParsePublicKey(raw)
			if err != nil {
				return nil, fmt.Errorf("kunci lisensi %s: %w", e.Name(), err)
			}
			out[strings.TrimSuffix(e.Name(), ".pem")] = k
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("binary ini tidak membawa kunci publik License Console (internal/platform/license/keys/release)")
	}
	return out, nil
}

// ParsePublicKey membaca kunci publik RSA dalam PEM "PUBLIC KEY".
func ParsePublicKey(raw []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("bukan PEM")
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("bukan kunci RSA")
	}
	return rk, nil
}

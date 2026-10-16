// Package secret mengenkripsi kolom sensitif (rahasia 2FA, license key) dengan
// AES-256-GCM memakai kunci aplikasi dari LAJUR_APP_KEY.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const version = 1

var ErrCorrupt = errors.New("data terenkripsi rusak atau kunci aplikasi berbeda")

// Box menyegel dan membuka data dengan satu kunci aplikasi.
type Box struct{ aead cipher.AEAD }

// ParseKey membaca kunci 32 byte dalam base64 (standar atau URL-safe, dengan atau tanpa padding).
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if k, err := enc.DecodeString(s); err == nil {
			if len(k) != 32 {
				return nil, fmt.Errorf("kunci aplikasi harus 32 byte, bukan %d", len(k))
			}
			return k, nil
		}
	}
	return nil, errors.New("kunci aplikasi bukan base64")
}

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("kunci aplikasi harus 32 byte, bukan %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal mengenkripsi plain. purpose ikut diautentikasi, sehingga data milik satu kolom
// tidak bisa dipindah ke kolom lain lalu dibuka.
func (b *Box) Seal(plain []byte, purpose string) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	_, _ = rand.Read(nonce)
	out := append([]byte{version}, nonce...)
	return b.aead.Seal(out, nonce, plain, []byte(purpose))
}

func (b *Box) Open(sealed []byte, purpose string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < 1+n+b.aead.Overhead() || sealed[0] != version {
		return nil, ErrCorrupt
	}
	plain, err := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(purpose))
	if err != nil {
		return nil, ErrCorrupt
	}
	return plain, nil
}

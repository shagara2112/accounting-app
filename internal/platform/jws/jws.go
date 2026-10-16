// Package jws menandatangani dan memverifikasi token JWS compact RS256 (dipakai manifest rilis).
package jws

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ,omitempty"`
	Kid string `json:"kid,omitempty"`
}

func Sign(key *rsa.PrivateKey, kid string, claims any) (string, error) {
	h, err := json.Marshal(header{Alg: "RS256", Typ: "JWT", Kid: kid})
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify memeriksa tanda tangan dengan kunci sesuai kid (atau semua kunci bila kid tidak
// dikenal) lalu mengisi dst dengan isi token.
func Verify(token string, keys map[string]*rsa.PublicKey, dst any) error {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return errors.New("format token salah")
	}
	var h header
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(raw, &h) != nil || h.Alg != "RS256" {
		return errors.New("header token tidak dikenal")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errors.New("tanda tangan rusak")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	candidates := keys
	if k, ok := keys[h.Kid]; ok {
		candidates = map[string]*rsa.PublicKey{h.Kid: k}
	}
	ok := false
	for _, k := range candidates {
		if rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) == nil {
			ok = true
			break
		}
	}
	if !ok {
		return errors.New("tanda tangan tidak cocok dengan kunci yang dipercaya")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errors.New("isi token rusak")
	}
	return json.Unmarshal(body, dst)
}

// ParsePrivateKey membaca kunci privat RSA PEM ("PRIVATE KEY" PKCS#8).
func ParsePrivateKey(raw []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("bukan PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("bukan kunci RSA")
	}
	return rk, nil
}

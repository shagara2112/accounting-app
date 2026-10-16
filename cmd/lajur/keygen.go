package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// licenseKeygen membuat pasangan kunci RSA-2048 untuk token lisensi:
// <kid>.private.pem untuk Script Properties License Console dan <kid>.pem untuk dibawa binary.
func licenseKeygen() error {
	if len(os.Args) < 3 || len(os.Args) > 4 {
		return fmt.Errorf("pemakaian: lajur license-keygen <folder-keluaran> [kid]")
	}
	dir := os.Args[2]
	kid := "lajur-" + time.Now().Format("20060102")
	if len(os.Args) == 4 {
		kid = os.Args[3]
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	privPath, pubPath := filepath.Join(dir, kid+".private.pem"), filepath.Join(dir, kid+".pem")
	for _, f := range []struct {
		path  string
		block *pem.Block
		mode  os.FileMode
	}{
		{privPath, &pem.Block{Type: "PRIVATE KEY", Bytes: priv}, 0o600},
		{pubPath, &pem.Block{Type: "PUBLIC KEY", Bytes: pub}, 0o644},
	} {
		if _, err := os.Stat(f.path); err == nil {
			return fmt.Errorf("%s sudah ada; tidak ditimpa", f.path)
		}
		if err := os.WriteFile(f.path, pem.EncodeToMemory(f.block), f.mode); err != nil {
			return err
		}
	}
	fmt.Printf("Kunci dibuat (kid %s):\n  privat: %s  → Script Property LAJUR_SIGNING_KEY, lalu hapus file ini\n  publik: %s → internal/platform/license/keys/release/\n", kid, privPath, pubPath)
	return nil
}

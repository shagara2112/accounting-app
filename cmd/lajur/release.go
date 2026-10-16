package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"lajur/internal/platform/jws"
	"lajur/internal/platform/update"
)

// signManifest menandatangani manifest rilis di mesin rilis; hasilnya (satu baris JWS)
// diunggah ke update server sebagai, mis., stable.jws.
func signManifest() error {
	fs := flag.NewFlagSet("sign-manifest", flag.ContinueOnError)
	keyFile := fs.String("key", "", "kunci privat rilis (PEM)")
	kid := fs.String("kid", "", "kid kunci, sama dengan nama file kunci publik di binary")
	m := update.Manifest{Product: "lajur", PublishedAt: time.Now().UTC()}
	fs.StringVar(&m.Channel, "channel", "stable", "kanal rilis: stable atau beta")
	fs.StringVar(&m.Version, "version", "", "versi rilis, mis. 1.2.3")
	fs.StringVar(&m.Image, "image", "", "image app dengan digest, mis. ghcr.io/lajur/lajur@sha256:…")
	fs.StringVar(&m.UpdaterImage, "updater-image", "", "image updater dengan digest (opsional)")
	fs.StringVar(&m.MinFrom, "min-from", "", "versi terendah yang boleh langsung naik (opsional)")
	notes := fs.String("notes-file", "", "berkas catatan rilis")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *keyFile == "" || *kid == "" {
		return fmt.Errorf("--key dan --kid wajib diisi")
	}
	if *notes != "" {
		b, err := os.ReadFile(*notes)
		if err != nil {
			return err
		}
		m.Notes = string(b)
	}
	if err := m.Validate(); err != nil {
		return err
	}
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	key, err := jws.ParsePrivateKey(raw)
	if err != nil {
		return err
	}
	tok, err := jws.Sign(key, *kid, m)
	if err != nil {
		return err
	}
	fmt.Println(tok)
	return nil
}

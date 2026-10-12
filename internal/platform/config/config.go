// Package config membaca konfigurasi Lajur dari environment variable.
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Listen      string // alamat HTTP, mis. ":8080"
	DatabaseURL string

	// LicenseMode: "local" untuk pengembangan, "remote" untuk produksi (License Console).
	LicenseMode         string
	LicenseLocalState   string // status lisensi tiruan untuk mode local, mis. ACTIVE
	LicenseLocalExpires string // tanggal expired tiruan, yyyy-MM-dd; kosong = tidak pernah

	SecureCookies   bool
	DevSeedPassword string
}

func Load() (Config, error) {
	c := Config{
		Listen:              env("LAJUR_LISTEN", ":8080"),
		DatabaseURL:         os.Getenv("LAJUR_DATABASE_URL"),
		LicenseMode:         env("LAJUR_LICENSE_MODE", "remote"),
		LicenseLocalState:   env("LAJUR_LICENSE_LOCAL_STATUS", "ACTIVE"),
		LicenseLocalExpires: os.Getenv("LAJUR_LICENSE_LOCAL_EXPIRES"),
		DevSeedPassword:     os.Getenv("LAJUR_DEV_SEED_PASSWORD"),
	}
	secure, err := strconv.ParseBool(env("LAJUR_SECURE_COOKIES", "true"))
	if err != nil {
		return c, fmt.Errorf("LAJUR_SECURE_COOKIES tidak valid: %w", err)
	}
	c.SecureCookies = secure
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("LAJUR_DATABASE_URL wajib diisi " +
			"(di laptop pengembang, jalankan lewat make, mis. `make serve`, supaya .env terbaca)")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

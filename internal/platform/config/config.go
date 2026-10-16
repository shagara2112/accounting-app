// Package config membaca konfigurasi Lajur dari environment variable.
package config

import (
	"fmt"
	"os"
	"strconv"

	"lajur/internal/platform/secret"
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

	// AppKey mengenkripsi kolom sensitif (rahasia 2FA, license key). Dibuat skrip instal
	// dan disimpan di /opt/lajur/.env; bila hilang, rahasia 2FA harus diatur ulang.
	AppKey []byte
	// MFARequired mewajibkan 2FA untuk peran yang mengelola pengguna atau sistem
	// (Pemilik, Manager, Admin pelanggan). Hanya boleh dimatikan di laptop pengembang.
	MFARequired bool

	// MachineIDFile: /etc/machine-id host, di-mount ke container sebagai /host/machine-id.
	MachineIDFile string
	// LicenseAllowHTTP mengizinkan License Console tiruan di http://localhost (hanya build pengembangan).
	LicenseAllowHTTP bool
	// DataDir: folder data aplikasi (lampiran, backup); dipakai juga untuk cek ruang disk.
	DataDir string

	// BackupPublicKey: kunci publik penerima backup (pasangan kunci privat milik Pemilik).
	BackupPublicKey string
	// PgDump dan PgRestore: perintah klien PostgreSQL; PgDocker: container basis data bila
	// klien dijalankan lewat docker exec (laptop pengembang).
	PgDump, PgRestore, PgDocker string
	// Timezone untuk jadwal backup harian.
	Timezone string

	// TLS: sertifikat server dari CA lokal (PLT-03). Kosong = HTTP biasa (laptop pengembang).
	TLSCert, TLSKey string
	// RedirectListen: alamat HTTP yang mengalihkan ke HTTPS dan menyajikan /ca.crt, mis. ":8080".
	RedirectListen string
	// CACert: sertifikat CA lokal yang dipasang di PC pengguna.
	CACert string

	// UpdateURL: manifest rilis bertanda tangan di update server (kosong = pembaruan tidak aktif).
	// UpdaterURL dan UpdaterToken: alamat dan token container updater di jaringan internal.
	UpdateURL, UpdaterURL, UpdaterToken string
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
	mfa, err := strconv.ParseBool(env("LAJUR_MFA_REQUIRED", "true"))
	if err != nil {
		return c, fmt.Errorf("LAJUR_MFA_REQUIRED tidak valid: %w", err)
	}
	c.MFARequired = mfa
	c.BackupPublicKey = os.Getenv("LAJUR_BACKUP_PUBLIC_KEY")
	c.PgDump, c.PgRestore, c.PgDocker = env("LAJUR_PG_DUMP", "pg_dump"), env("LAJUR_PG_RESTORE", "pg_restore"), os.Getenv("LAJUR_PG_DOCKER")
	c.Timezone = env("LAJUR_TIMEZONE", "Asia/Jakarta")
	c.TLSCert, c.TLSKey = os.Getenv("LAJUR_TLS_CERT"), os.Getenv("LAJUR_TLS_KEY")
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return c, fmt.Errorf("LAJUR_TLS_CERT dan LAJUR_TLS_KEY harus diisi berdua")
	}
	c.RedirectListen, c.CACert = os.Getenv("LAJUR_HTTP_REDIRECT"), os.Getenv("LAJUR_CA_CERT")
	c.UpdateURL, c.UpdaterURL, c.UpdaterToken = os.Getenv("LAJUR_UPDATE_URL"), os.Getenv("LAJUR_UPDATER_URL"), os.Getenv("LAJUR_UPDATER_TOKEN")
	c.MachineIDFile = env("LAJUR_MACHINE_ID_FILE", "/host/machine-id")
	c.DataDir = env("LAJUR_DATA_DIR", "/var/lib/lajur")
	if c.LicenseAllowHTTP, err = strconv.ParseBool(env("LAJUR_LICENSE_ALLOW_HTTP", "false")); err != nil {
		return c, fmt.Errorf("LAJUR_LICENSE_ALLOW_HTTP tidak valid: %w", err)
	}
	key := os.Getenv("LAJUR_APP_KEY")
	if key == "" {
		return c, fmt.Errorf("LAJUR_APP_KEY wajib diisi: 32 byte acak dalam base64, mis. hasil `openssl rand -base64 32`")
	}
	if c.AppKey, err = secret.ParseKey(key); err != nil {
		return c, fmt.Errorf("LAJUR_APP_KEY: %w", err)
	}
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

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/backup"
	"lajur/internal/platform/buildinfo"
	"lajur/internal/platform/config"
	"lajur/internal/platform/db"
	"lajur/internal/platform/secret"
	"lajur/internal/platform/store"
)

// newBackup merangkai layanan backup dari konfigurasi. Kunci publik kosong berarti backup
// belum dikonfigurasi (halaman Sistem dan banner memberi tahu admin).
func newBackup(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, box *secret.Box, log *slog.Logger, onFailure func(context.Context, backup.Record)) (*backup.Service, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("LAJUR_TIMEZONE: %w", err)
	}
	var instID string
	if inst, err := store.New(pool).GetInstallation(ctx); err == nil {
		instID = strings.ReplaceAll(inst.ID.String(), "-", "")
	}
	bc := backup.Config{
		Pool: pool, DatabaseURL: cfg.DatabaseURL, Dir: filepath.Join(cfg.DataDir, "backups"), FilesDir: filepath.Join(cfg.DataDir, "files"),
		PgDump: cfg.PgDump, PgRestore: cfg.PgRestore, Docker: cfg.PgDocker, Box: box, InstallationID: instID,
		Version: buildinfo.Version, Location: loc, Log: log, OnFailure: onFailure,
	}
	if cfg.BackupPublicKey != "" {
		if bc.PublicKey, err = backup.ParsePublicKey(cfg.BackupPublicKey); err != nil {
			return nil, fmt.Errorf("LAJUR_BACKUP_PUBLIC_KEY: %w", err)
		}
	}
	return backup.New(bc), nil
}

// backupKeygen mencetak pasangan kunci backup baru.
func backupKeygen() error {
	priv, pub, err := backup.NewKey()
	if err != nil {
		return err
	}
	fmt.Printf(`Kunci backup baru.

KUNCI PRIVAT (serahkan ke Pemilik; simpan di pengelola kata sandi atau cetak, JANGAN di server):
%s

Kunci publik (untuk server, baris LAJUR_BACKUP_PUBLIC_KEY di /opt/lajur/.env):
LAJUR_BACKUP_PUBLIC_KEY=%s
`, priv, pub)
	return nil
}

// backupOnce menjalankan satu backup dari baris perintah (dipakai updater sebelum pembaruan).
func backupOnce(ctx context.Context) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	kind := fs.String("kind", "manual", "jenis backup: manual atau pre_update")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	box, err := secret.New(cfg.AppKey)
	if err != nil {
		return err
	}
	svc, err := newBackup(ctx, cfg, pool, box, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		return err
	}
	r, err := svc.Run(ctx, *kind, nil)
	if err != nil {
		return err
	}
	fmt.Printf("Backup selesai: %s (%.1f MB, sha256 %s)\n", r.FileName, float64(r.SizeBytes)/(1<<20), r.SHA256[:16])
	return nil
}

// restoreBackup memulihkan basis data dan lampiran dari file backup dengan kunci privat Pemilik.
func restoreBackup(ctx context.Context) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	keyFile := fs.String("key-file", "", "file berisi kunci privat backup (lajur-backup-key-1:…); kosong = baca dari stdin")
	target := fs.String("database-url", "", "basis data tujuan; kosong = LAJUR_DATABASE_URL")
	noFiles := fs.Bool("no-files", false, "jangan pulihkan lampiran")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("pemakaian: lajur restore [--key-file F] [--database-url URL] <file.lajurbk>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var keyText []byte
	if *keyFile != "" {
		keyText, err = os.ReadFile(*keyFile)
	} else {
		fmt.Fprint(os.Stderr, "Kunci privat backup: ")
		var line string
		_, err = fmt.Fscanln(os.Stdin, &line)
		keyText = []byte(line)
	}
	if err != nil {
		return err
	}
	priv, err := backup.ParsePrivateKey(string(keyText))
	if err != nil {
		return err
	}
	dbURL := cfg.DatabaseURL
	if *target != "" {
		dbURL = *target
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	box, _ := secret.New(cfg.AppKey)
	svc, err := newBackup(ctx, cfg, pool, box, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		return err
	}
	pool.Close() // pg_restore --clean butuh tidak ada koneksi lain dari proses ini
	files := filepath.Join(cfg.DataDir, "files")
	if *noFiles {
		files = ""
	}
	if err := svc.Restore(ctx, fs.Arg(0), priv, dbURL, files, func(m string) { fmt.Println(m) }); err != nil {
		return err
	}
	fmt.Println("Pemulihan selesai. Jalankan aplikasi lagi; migrasi berjalan otomatis saat start.")
	return nil
}

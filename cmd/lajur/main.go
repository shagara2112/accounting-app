// Perintah lajur: server aplikasi dan alat bantu operasional.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/platform/audit"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/buildinfo"
	"lajur/internal/platform/config"
	"lajur/internal/platform/db"
	"lajur/internal/platform/license"
	"lajur/internal/platform/mail"
	"lajur/internal/platform/migrate"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/secret"
	"lajur/internal/platform/store"
	"lajur/internal/platform/update"
	"lajur/internal/server"
	"lajur/web"
)

// extraCommands diisi perintah khusus build pengembangan (mis. seed-dev).
var extraCommands = map[string]func(ctx context.Context) error{}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd := os.Args[1]; cmd {
	case "serve":
		err = serve(ctx, log)
	case "migrate":
		err = migrateOnly(ctx)
	case "version":
		fmt.Println(buildinfo.Version)
	case "healthcheck":
		err = healthcheck(ctx)
	case "audit-verify":
		err = auditVerify(ctx)
	case "reset-2fa":
		err = resetUser(ctx, "2fa")
	case "reset-password":
		err = resetUser(ctx, "password")
	case "license-keygen":
		err = licenseKeygen()
	case "backup-keygen":
		err = backupKeygen()
	case "backup":
		err = backupOnce(ctx)
	case "restore":
		err = restoreBackup(ctx)
	case "sign-manifest":
		err = signManifest()
	default:
		run, ok := extraCommands[cmd]
		if !ok {
			usage()
			os.Exit(2)
		}
		err = run(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "galat:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Pemakaian: lajur <perintah>

  serve                   menjalankan migrasi lalu server aplikasi
  version                 menampilkan versi aplikasi
  healthcheck             memeriksa server di container ini (HEALTHCHECK Docker)
  migrate                 hanya menjalankan migrasi basis data
  audit-verify            memeriksa keutuhan rantai audit trail
  reset-2fa <email>       menghapus 2FA pengguna yang kehilangan ponselnya
  reset-password <email>  mengganti kata sandi; kata sandi baru dibaca dari stdin
  license-keygen <folder> membuat pasangan kunci token lisensi untuk License Console
  backup-keygen           membuat pasangan kunci backup (privat untuk Pemilik)
  backup [--kind K]       membuat satu backup sekarang
  restore <file>          memulihkan basis data dan lampiran dari file backup
  sign-manifest           menandatangani manifest rilis untuk update server (mesin rilis)`)
	for name := range extraCommands {
		fmt.Fprintf(os.Stderr, "  %-13s (hanya build pengembangan)\n", name)
	}
}

// open menjalankan migrasi, membuka pool, dan menyamakan peran bawaan.
func open(ctx context.Context, cfg config.Config) (*server.Deps, func(), error) {
	if err := migrate.Up(ctx, cfg.DatabaseURL); err != nil {
		return nil, nil, err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := rbac.SyncBuiltinRoles(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("menyamakan peran bawaan: %w", err)
	}
	// UUID instalasi dibuat sekali dan tidak pernah berubah; ikut membentuk installationId lisensi.
	if err := store.New(pool).EnsureInstallation(ctx, uuid.Must(uuid.NewV7())); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("menyiapkan data instalasi: %w", err)
	}
	box, err := secret.New(cfg.AppKey)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	d := server.Deps{
		Pool: pool, Sessions: auth.NewSessions(pool, cfg.SecureCookies), Box: box, MFARequired: cfg.MFARequired,
		FilesDir: filepath.Join(cfg.DataDir, "files"),
	}
	jsonLog := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	d.Mail = mail.New(pool, box, jsonLog)
	if d.Backup, err = newBackup(ctx, cfg, pool, box, jsonLog, backupAlert(d.Mail, jsonLog)); err != nil {
		pool.Close()
		return nil, nil, err
	}
	if err := withLicense(ctx, cfg, &d); err != nil {
		pool.Close()
		return nil, nil, err
	}
	if cfg.UpdateURL != "" {
		keys, err := update.TrustedKeys()
		if err != nil {
			pool.Close()
			return nil, nil, err
		}
		d.Updates = &server.Updates{ManifestURL: cfg.UpdateURL, UpdaterURL: cfg.UpdaterURL, Token: cfg.UpdaterToken, Keys: keys}
	}
	d = d.WithModules()
	return &d, pool.Close, nil
}

func serve(ctx context.Context, log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	deps, closeDB, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeDB()
	deps.Log, deps.Web = log, web.Dist()
	lic := deps.License
	if remote, ok := lic.(*license.Remote); ok {
		go remote.Run(ctx)
	}
	go deps.Backup.Schedule(ctx)
	go deps.Mail.Run(ctx)
	if loc, err := time.LoadLocation(cfg.Timezone); err == nil {
		go dailyAlerts(ctx, deps, loc, log)
		go recurringJournals(ctx, deps, loc, log)
		go assetDepreciation(ctx, deps, loc, log)
	}

	go purgeIdempotencyKeys(ctx, deps, log)

	handler, _ := server.New(*deps)
	srv := &http.Server{
		Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	servers := []*http.Server{srv}
	errc := make(chan error, 2)
	go func() {
		log.Info("Lajur berjalan", "alamat", cfg.Listen, "https", cfg.TLSCert != "", "lisensi", lic.Status(ctx).State)
		if cfg.TLSCert != "" {
			errc <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	if cfg.RedirectListen != "" {
		red := &http.Server{Addr: cfg.RedirectListen, Handler: redirectHTTPS(cfg.CACert), ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, red)
		go func() { errc <- red.ListenAndServe() }()
	}
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, s := range servers {
			_ = s.Shutdown(shutdown)
		}
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// redirectHTTPS melayani port HTTP: /ca.crt untuk dipasang di PC pengguna (belum bisa lewat
// HTTPS karena CA-nya belum dipercaya), selebihnya dialihkan ke alamat HTTPS yang sama.
func redirectHTTPS(caCert string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ca.crt" && caCert != "" {
			w.Header().Set("Content-Type", "application/x-x509-ca-cert")
			w.Header().Set("Content-Disposition", `attachment; filename="lajur-ca.crt"`)
			http.ServeFile(w, r, caCert)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

// purgeIdempotencyKeys menghapus kunci idempotensi yang lebih tua dari 24 jam, tiap jam.
func purgeIdempotencyKeys(ctx context.Context, deps *server.Deps, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		n, err := store.New(deps.Pool).PurgeIdempotencyKeys(ctx)
		if err != nil && ctx.Err() == nil {
			log.Error("membersihkan kunci idempotensi", "err", err)
		} else if n > 0 {
			log.Info("kunci idempotensi kedaluwarsa dihapus", "jumlah", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func migrateOnly(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := migrate.Up(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	fmt.Println("Migrasi selesai.")
	return nil
}

func auditVerify(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	res, err := audit.Verify(ctx, pool)
	if err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("rantai audit trail RUSAK mulai baris %d (dari %d baris)", res.BrokenAt, res.Count)
	}
	fmt.Printf("Audit trail utuh: %d baris diperiksa.\n", res.Count)
	return nil
}

// resetUser adalah jalan darurat bagi admin server ketika satu-satunya Pemilik kehilangan
// ponsel 2FA atau lupa kata sandinya. Semua sesi pengguna itu diakhiri dan dicatat di audit trail.
func resetUser(ctx context.Context, what string) error {
	if len(os.Args) != 3 {
		return fmt.Errorf("pemakaian: lajur reset-%s <email>", what)
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
	var hash string
	if what == "password" {
		fmt.Fprint(os.Stderr, "Kata sandi baru (minimal 10 karakter): ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("membaca kata sandi: %w", err)
		}
		if hash, err = auth.HashPassword(strings.TrimRight(line, "\r\n")); err != nil {
			return err
		}
	}
	return db.InTx(ctx, pool, pgx.TxOptions{}, func(ctx context.Context) error {
		q := store.New(db.Conn(ctx, pool))
		u, err := q.GetUserByEmail(ctx, os.Args[2])
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("pengguna %s tidak ditemukan", os.Args[2])
		}
		if err != nil {
			return err
		}
		if what == "password" {
			err = q.SetPassword(ctx, store.SetPasswordParams{ID: u.ID, PasswordHash: hash})
		} else {
			err = q.ClearTOTP(ctx, u.ID)
		}
		if err != nil {
			return err
		}
		if err := q.DeleteUserSessions(ctx, u.ID); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, pool), audit.Entry{
			Action: "platform.user.cli_reset_" + what, ObjectType: "user", ObjectID: u.ID.String(), IP: "cli",
		}); err != nil {
			return err
		}
		fmt.Printf("Selesai: %s untuk %s diatur ulang; semua sesinya diakhiri.\n", map[string]string{"2fa": "2FA", "password": "kata sandi"}[what], u.Email)
		return nil
	})
}

// Perintah lajur: server aplikasi dan alat bantu operasional.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/config"
	"lajur/internal/platform/db"
	"lajur/internal/platform/license"
	"lajur/internal/platform/migrate"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/store"
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
	case "audit-verify":
		err = auditVerify(ctx)
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

  serve         menjalankan migrasi lalu server aplikasi
  migrate       hanya menjalankan migrasi basis data
  audit-verify  memeriksa keutuhan rantai audit trail`)
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
	return &server.Deps{
		Pool: pool, Sessions: auth.NewSessions(pool, cfg.SecureCookies), Core: core.New(pool), Ledger: ledger.New(pool),
	}, pool.Close, nil
}

func serve(ctx context.Context, log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	lic, err := license.New(license.Settings{Mode: cfg.LicenseMode, LocalState: cfg.LicenseLocalState, LocalExpires: cfg.LicenseLocalExpires})
	if err != nil {
		return err
	}
	deps, closeDB, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeDB()
	deps.Log, deps.License, deps.Web = log, lic, web.Dist()

	go purgeIdempotencyKeys(ctx, deps, log)

	handler, _ := server.New(*deps)
	srv := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() {
		log.Info("Lajur berjalan", "alamat", cfg.Listen, "lisensi", lic.Status(ctx).State)
		errc <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
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

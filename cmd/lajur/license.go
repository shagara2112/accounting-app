package main

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"syscall"

	"lajur/internal/platform/buildinfo"
	"lajur/internal/platform/config"
	"lajur/internal/platform/license"
	"lajur/internal/platform/store"
	"lajur/internal/server"
)

// withLicense memilih provider lisensi: local (pengembangan) atau remote (License Console).
func withLicense(ctx context.Context, cfg config.Config, d *server.Deps) error {
	if cfg.LicenseMode != "remote" {
		lic, err := license.New(license.Settings{Mode: cfg.LicenseMode, LocalState: cfg.LicenseLocalState, LocalExpires: cfg.LicenseLocalExpires})
		d.License = lic
		return err
	}
	inst, err := store.New(d.Pool).GetInstallation(ctx)
	if err != nil {
		return err
	}
	remote, err := license.NewRemote(ctx, license.RemoteConfig{
		Pool: d.Pool, Box: d.Box, InstallationUUID: inst.ID.String(), MachineID: machineID(cfg.MachineIDFile),
		Log: slog.New(slog.NewJSONHandler(os.Stdout, nil)), AllowInsecure: cfg.LicenseAllowHTTP && license.DevBuild,
		Health: func(ctx context.Context) map[string]any { return health(ctx, d, cfg) },
	})
	if err != nil {
		return err
	}
	d.License, d.LicenseSetup, d.LicenseAdmin = remote, remote, remote
	return nil
}

// machineID membaca machine-id host; kosong di laptop pengembang tanpa file itu.
func machineID(path string) string {
	for _, p := range []string{path, "/etc/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	return ""
}

// health adalah ringkasan teknis yang ikut check-in: tanpa data keuangan.
func health(ctx context.Context, d *server.Deps, cfg config.Config) map[string]any {
	out := map[string]any{"version": buildinfo.Version, "os": runtime.GOOS + "/" + runtime.GOARCH}
	if c, err := store.New(d.Pool).HealthCounts(ctx); err == nil {
		out["companies"], out["users"], out["dbMB"] = c.Companies, c.Users, c.DbMb
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(cfg.DataDir, &fs); err == nil {
		out["diskFreeMB"] = int64(fs.Bavail) * int64(fs.Bsize) / (1 << 20)
	}
	if d.Backup != nil {
		if st, err := d.Backup.Status(ctx); err == nil {
			out["backupConfigured"], out["lastBackupAt"], out["backupOverdue"] = st.Configured, st.LastOK, st.Overdue
		}
	}
	return out
}

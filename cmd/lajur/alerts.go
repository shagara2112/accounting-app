package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"lajur/internal/platform/backup"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/license"
	"lajur/internal/platform/mail"
	"lajur/internal/server"
)

var kindLabel = map[string]string{"scheduled": "terjadwal", "manual": "manual", "pre_update": "sebelum pembaruan"}

// backupAlert mengirim email ke admin saat backup gagal (paling banyak sekali sehari).
func backupAlert(m *mail.Service, log *slog.Logger) func(context.Context, backup.Record) {
	return func(ctx context.Context, r backup.Record) {
		day := r.StartedAt.Format("2006-01-02")
		text := fmt.Sprintf("Backup %s Lajur pada %s gagal:\n\n%s\n\nBuka Sistem → Backup di Lajur untuk melihat detail dan menjalankan backup lagi.",
			kindLabel[r.Kind], r.StartedAt.Format("02-01-2006 15:04"), r.Error)
		if err := m.AlertAdmins(ctx, "backup_alert", "backup-failed:"+day, "Backup Lajur gagal", text); err != nil {
			log.Error("mengantrekan email backup gagal", "err", err)
		}
	}
}

var licenseSubject = map[license.State]string{
	license.Warning:    "Lisensi Lajur segera berakhir",
	license.Expired:    "Lisensi Lajur sudah berakhir",
	license.Suspended:  "Lisensi Lajur ditangguhkan",
	license.Unverified: "Lisensi Lajur belum terverifikasi",
	license.InvalidKey: "License key Lajur tidak valid",
}

// dailyAlerts memeriksa tiap jam, dan mulai pukul 08.00 mengirim (sekali sehari) peringatan
// backup yang terlambat dan lisensi yang tidak aktif penuh ke admin.
func dailyAlerts(ctx context.Context, d *server.Deps, loc *time.Location, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		now := time.Now().In(loc)
		if now.Hour() >= 8 {
			day := now.Format("2006-01-02")
			if st, err := d.Backup.Status(ctx); err == nil && st.Configured && st.Overdue {
				last := "belum pernah"
				if st.LastOK != nil {
					last = st.LastOK.In(loc).Format("02-01-2006 15:04")
				}
				if err := d.Mail.AlertAdmins(ctx, "backup_alert", "backup-overdue:"+day, "Tidak ada backup Lajur dalam 48 jam",
					"Backup sukses terakhir: "+last+".\n\nBuka Sistem → Backup di Lajur untuk melihat penyebabnya."); err != nil {
					log.Error("mengantrekan email backup terlambat", "err", err)
				}
			}
			ls := d.License.Status(ctx)
			subject := licenseSubject[ls.State]
			if ls.UpdateRequired {
				subject = "Lajur wajib diperbarui"
			}
			if subject != "" {
				if err := d.Mail.AlertAdmins(ctx, "license_alert", fmt.Sprintf("license:%s:%s", ls.State, day), subject,
					ls.Message+"\n\nBuka Sistem → Lisensi di Lajur untuk detail, cek ulang, atau perpanjangan."); err != nil {
					log.Error("mengantrekan email lisensi", "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// assetDepreciation memposting penyusutan aset tetap bulan lalu (FA-02) sejak tanggal 1:
// sekali saat start lalu tiap jam, untuk PT yang belum memostingnya sendiri.
func assetDepreciation(ctx context.Context, d *server.Deps, loc *time.Location, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		now := time.Now().In(loc)
		today := civil.New(now.Year(), now.Month(), now.Day())
		if n, err := d.Asset.RunDue(ctx, today); err != nil && ctx.Err() == nil {
			log.Error("memposting penyusutan aset", "err", err)
		} else if n > 0 {
			log.Info("penyusutan aset diposting", "jumlah", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// recurringJournals memposting jurnal berulang yang jatuh tempo (GL-08): sekali saat start
// (mengejar jadwal yang terlewat saat server mati) lalu tiap jam.
func recurringJournals(ctx context.Context, d *server.Deps, loc *time.Location, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		now := time.Now().In(loc)
		today := civil.New(now.Year(), now.Month(), now.Day())
		if n, err := d.Ledger.RunDueRecurring(ctx, today); err != nil && ctx.Err() == nil {
			log.Error("memposting jurnal berulang", "err", err)
		} else if n > 0 {
			log.Info("jurnal berulang diposting", "jumlah", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

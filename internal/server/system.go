package server

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/api"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/backup"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/mail"
	"lajur/internal/platform/rbac"
)

// LicenseAdmin adalah sisi pengelolaan lisensi remote untuk halaman Sistem. Nil di mode local.
type LicenseAdmin interface {
	Detail(ctx context.Context) license.Detail
	Recheck(ctx context.Context) error
	Activate(ctx context.Context, in license.Activation) error
	Payment(ctx context.Context, action string, extra map[string]any) (map[string]any, error)
	Token(ctx context.Context) string
}

// systemRoutes: status lisensi rinci, cek ulang, ganti license key, dan pembayaran perpanjangan.
// Semuanya tetap bisa dipakai saat lisensi berakhir atau ditangguhkan (mode Status), karena
// justru di saat itulah admin perlu memperpanjang.
func systemRoutes(d Deps) []api.Route {
	h := systemHandlers{lic: d.LicenseAdmin, backup: d.Backup, pool: d.Pool, mail: d.Mail}
	route := func(method, path string, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: rbac.SystemManage, License: license.ModeStatus, Handle: fn}
	}
	download := route(http.MethodGet, "/api/v1/system/backups/{id}/download", h.downloadBackup)
	download.Stream = true
	return []api.Route{
		route(http.MethodGet, "/api/v1/system/license/detail", h.detail),
		route(http.MethodPost, "/api/v1/system/license/recheck", h.recheck),
		route(http.MethodPut, "/api/v1/system/license", h.replace),
		route(http.MethodGet, "/api/v1/system/license/payment", h.paymentInfo),
		route(http.MethodPost, "/api/v1/system/license/payment", h.submitPayment),

		// Backup tetap bisa dibuat dan diunduh walau lisensi berakhir: data milik pelanggan.
		route(http.MethodGet, "/api/v1/system/backups", h.backups),
		route(http.MethodGet, "/api/v1/system/backups/status", h.backupStatus),
		route(http.MethodPost, "/api/v1/system/backups", h.runBackup),
		route(http.MethodPut, "/api/v1/system/backups/settings", h.saveBackupSettings),
		download,

		route(http.MethodGet, "/api/v1/system/mail", h.mailSettings),
		route(http.MethodPut, "/api/v1/system/mail", h.saveMail),
		route(http.MethodPost, "/api/v1/system/mail/test", h.testMail),
	}
}

func (h systemHandlers) mailSettings(w http.ResponseWriter, r *http.Request) error {
	set, err := h.mail.Settings(r.Context())
	if err != nil {
		return err
	}
	outbox, err := h.mail.Outbox(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"settings": set, "outbox": outbox})
}

func (h systemHandlers) saveMail(w http.ResponseWriter, r *http.Request) error {
	var in mail.Settings
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	out, err := h.mail.SaveSettings(r.Context(), in)
	if err != nil {
		return err
	}
	if _, err := audit.Write(r.Context(), db.Conn(r.Context(), h.pool), audit.Entry{
		UserID: auth.ActorFrom(r).UserRef(), Action: "platform.mail.settings", ObjectType: "mail_settings", IP: auth.ClientIP(r),
		After: map[string]any{"enabled": out.Enabled, "host": out.Host, "port": out.Port, "security": out.Security, "from": out.From},
	}); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

// testMail mengirim email uji ke alamat yang diisi, atau ke email pengguna sendiri.
func (h systemHandlers) testMail(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		To string `json:"to"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.To == "" {
		sess, _ := auth.SessionFrom(r.Context())
		in.To = sess.Email
	}
	if err := h.mail.Test(r.Context(), in.To); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "to": in.To})
}

type systemHandlers struct {
	lic    LicenseAdmin
	backup *backup.Service
	pool   *pgxpool.Pool
	mail   *mail.Service
}

var errNoBackup = httpx.Conflict("backup.unavailable", "Layanan backup tidak berjalan di server ini.")

func (h systemHandlers) backups(w http.ResponseWriter, r *http.Request) error {
	if h.backup == nil {
		return errNoBackup
	}
	st, err := h.backup.Status(r.Context())
	if err != nil {
		return err
	}
	set, err := h.backup.Settings(r.Context())
	if err != nil {
		return err
	}
	items, err := h.backup.List(r.Context(), 60)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": st, "settings": set, "items": items})
}

func (h systemHandlers) backupStatus(w http.ResponseWriter, r *http.Request) error {
	if h.backup == nil {
		return httpx.WriteJSON(w, http.StatusOK, backup.Status{})
	}
	st, err := h.backup.Status(r.Context())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, st)
}

// runBackup memulai backup di latar belakang; halaman Sistem memantau statusnya.
func (h systemHandlers) runBackup(w http.ResponseWriter, r *http.Request) error {
	if h.backup == nil {
		return errNoBackup
	}
	if !h.backup.Configured() {
		return httpx.Conflict("backup.not_configured", "Kunci publik backup belum dipasang (LAJUR_BACKUP_PUBLIC_KEY).")
	}
	actor := auth.ActorFrom(r).UserRef()
	go func() {
		// Konteks baru: konteks request membawa transaksi yang selesai begitu respons dikirim.
		_, _ = h.backup.Run(context.Background(), "manual", actor)
	}()
	return httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (h systemHandlers) saveBackupSettings(w http.ResponseWriter, r *http.Request) error {
	if h.backup == nil {
		return errNoBackup
	}
	var in backup.Settings
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	out, err := h.backup.SaveSettings(r.Context(), in)
	if err != nil {
		return err
	}
	if _, err := audit.Write(r.Context(), db.Conn(r.Context(), h.pool), audit.Entry{
		UserID: auth.ActorFrom(r).UserRef(), Action: "platform.backup.settings", ObjectType: "backup_settings", IP: auth.ClientIP(r),
		After: map[string]any{"hour": out.Hour, "keep": out.Keep, "copyDir": out.CopyDir, "s3Bucket": out.S3Bucket, "s3Endpoint": out.S3Endpoint},
	}); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h systemHandlers) downloadBackup(w http.ResponseWriter, r *http.Request) error {
	if h.backup == nil {
		return errNoBackup
	}
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	f, rec, err := h.backup.Open(r.Context(), id)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+rec.FileName+`"`)
	http.ServeContent(w, r, rec.FileName, st.ModTime(), f)
	return nil
}

var errLocalLicense = httpx.Conflict("license.local", "Lajur berjalan dengan lisensi pengembangan (local); tidak ada License Console yang dihubungi.")

func (h systemHandlers) detail(w http.ResponseWriter, r *http.Request) error {
	if h.lic == nil {
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"mode": "local"})
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"mode": "remote", "detail": h.lic.Detail(r.Context())})
}

func (h systemHandlers) recheck(w http.ResponseWriter, r *http.Request) error {
	if h.lic == nil {
		return errLocalLicense
	}
	if err := h.lic.Recheck(r.Context()); err != nil {
		if _, ok := err.(*httpx.Problem); ok {
			return err
		}
		// Galat check-in sudah tercatat; kembalikan status terbaru dengan pesannya.
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error(), "detail": h.lic.Detail(r.Context())})
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": h.lic.Detail(r.Context())})
}

func (h systemHandlers) replace(w http.ResponseWriter, r *http.Request) error {
	if h.lic == nil {
		return errLocalLicense
	}
	var in license.Activation
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := h.lic.Activate(r.Context(), in); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h systemHandlers) paymentInfo(w http.ResponseWriter, r *http.Request) error {
	if h.lic == nil {
		return errLocalLicense
	}
	out, err := h.lic.Payment(r.Context(), "paymentInfo", nil)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

type paymentRequest struct {
	PackageID  string `json:"packageId"`
	Amount     int64  `json:"amount"`
	PayerName  string `json:"payerName"`
	Reference  string `json:"reference"`
	Note       string `json:"note"`
	FileBase64 string `json:"fileBase64"`
	MimeType   string `json:"mimeType"`
}

func (h systemHandlers) submitPayment(w http.ResponseWriter, r *http.Request) error {
	if h.lic == nil {
		return errLocalLicense
	}
	var in paymentRequest
	// Bukti bayar maksimal 6 MB, dikirim sebagai base64 (±8 MB).
	if err := httpx.DecodeJSONLimit(r, &in, 9<<20); err != nil {
		return err
	}
	out, err := h.lic.Payment(r.Context(), "submitPayment", map[string]any{
		"packageId": in.PackageID, "amount": in.Amount, "payerName": in.PayerName, "reference": in.Reference,
		"note": in.Note, "fileBase64": in.FileBase64, "mimeType": in.MimeType,
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

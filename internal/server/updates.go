package server

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"lajur/internal/platform/api"
	"lajur/internal/platform/buildinfo"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/update"
)

// Updates menghubungkan halaman Sistem dengan update server dan container updater.
type Updates struct {
	ManifestURL string // manifest bertanda tangan, mis. https://updates.lajur.id/stable.jws
	UpdaterURL  string // http://updater:9000 di jaringan internal
	Token       string
	Keys        map[string]*rsa.PublicKey
	Client      *http.Client
}

func (u *Updates) configured() bool {
	return u != nil && u.ManifestURL != "" && u.UpdaterURL != "" && u.Token != ""
}

func (u *Updates) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func updateRoutes(d Deps) []api.Route {
	h := updateHandlers{d: d}
	route := func(method, path string, fn httpx.Handler) api.Route {
		return api.Route{Method: method, Path: path, Access: api.Permission, Perm: rbac.SystemManage, License: license.ModeStatus, Handle: fn}
	}
	return []api.Route{
		route(http.MethodGet, "/api/v1/system/updates", h.check),
		route(http.MethodPost, "/api/v1/system/updates/apply", h.apply),
		route(http.MethodGet, "/api/v1/system/updates/status", h.status),
	}
}

type updateHandlers struct{ d Deps }

type updateCheck struct {
	Current    string `json:"current"`
	Configured bool   `json:"configured"`
	LicenseOK  bool   `json:"licenseOk"`
	BackupOK   bool   `json:"backupOk"`
	Latest     *struct {
		Version     string    `json:"version"`
		Channel     string    `json:"channel"`
		Notes       string    `json:"notes"`
		PublishedAt time.Time `json:"publishedAt"`
		MinFrom     string    `json:"minFrom,omitempty"`
	} `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

// manifest mengambil dan memverifikasi manifest terbaru. Token lisensi ikut dikirim supaya
// update server bisa menolak instalasi yang lisensinya tidak aktif.
func (h updateHandlers) manifest(ctx context.Context) (update.Manifest, string, error) {
	headers := map[string]string{"X-Lajur-Version": buildinfo.Version}
	if h.d.LicenseAdmin != nil {
		if tok := h.d.LicenseAdmin.Token(ctx); tok != "" {
			headers["Authorization"] = "Bearer " + tok
		}
		headers["X-Lajur-Installation"] = h.d.LicenseAdmin.Detail(ctx).InstallationID
	}
	return update.Fetch(ctx, h.d.Updates.client(), h.d.Updates.ManifestURL, headers, h.d.Updates.Keys)
}

func (h updateHandlers) check(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	st := h.d.License.Status(ctx)
	out := updateCheck{
		Current: buildinfo.Version, Configured: h.d.Updates.configured(),
		// Pembaruan tetap boleh saat versi wajib diperbarui (status aktif tetapi hanya baca).
		LicenseOK: st.State == license.Active || st.State == license.Warning,
		BackupOK:  h.d.Backup != nil && h.d.Backup.Configured(),
	}
	if !out.Configured {
		return httpx.WriteJSON(w, http.StatusOK, out)
	}
	m, _, err := h.manifest(ctx)
	if err != nil {
		out.Error = err.Error()
		return httpx.WriteJSON(w, http.StatusOK, out)
	}
	out.Latest = &struct {
		Version     string    `json:"version"`
		Channel     string    `json:"channel"`
		Notes       string    `json:"notes"`
		PublishedAt time.Time `json:"publishedAt"`
		MinFrom     string    `json:"minFrom,omitempty"`
	}{m.Version, m.Channel, m.Notes, m.PublishedAt, m.MinFrom}
	out.Available, out.Reachable = m.Newer(buildinfo.Version), m.Reachable(buildinfo.Version)
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h updateHandlers) apply(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !h.d.Updates.configured() {
		return httpx.Conflict("update.not_configured", "Pembaruan otomatis belum dikonfigurasi di server ini.")
	}
	st := h.d.License.Status(ctx)
	if st.State != license.Active && st.State != license.Warning {
		return httpx.Forbidden("update.license", "Pembaruan hanya tersedia untuk lisensi yang aktif.")
	}
	if h.d.Backup == nil || !h.d.Backup.Configured() {
		return httpx.Conflict("update.backup", "Backup belum dikonfigurasi; pembaruan selalu didahului backup.")
	}
	m, token, err := h.manifest(ctx)
	if err != nil {
		return httpx.NewProblem(http.StatusBadGateway, "update.manifest", "Manifest rilis tidak bisa diambil", err.Error())
	}
	if !m.Newer(buildinfo.Version) {
		return httpx.Conflict("update.current", "Lajur sudah versi terbaru.")
	}
	if !m.Reachable(buildinfo.Version) {
		return httpx.Conflict("update.min_from", fmt.Sprintf("Perbarui dulu ke versi %s sebelum ke %s.", m.MinFrom, m.Version))
	}
	body, _ := json.Marshal(map[string]string{"manifest": token})
	code, raw, err := h.callUpdater(ctx, http.MethodPost, "/apply", body)
	if err != nil {
		return err
	}
	if code != http.StatusAccepted {
		var res struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &res)
		return httpx.Conflict("update.rejected", "Updater menolak: "+res.Message)
	}
	return httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true, "version": m.Version})
}

func (h updateHandlers) status(w http.ResponseWriter, r *http.Request) error {
	if !h.d.Updates.configured() {
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "idle", "log": []any{}})
	}
	code, raw, err := h.callUpdater(r.Context(), http.MethodGet, "/status", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return httpx.NewProblem(http.StatusBadGateway, "update.updater", "Updater tidak menjawab", fmt.Sprintf("HTTP %d", code))
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(raw)
	return err
}

func (h updateHandlers) callUpdater(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.d.Updates.UpdaterURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.d.Updates.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.d.Updates.client().Do(req)
	if err != nil {
		return 0, nil, httpx.NewProblem(http.StatusBadGateway, "update.updater", "Updater tidak bisa dihubungi", err.Error())
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, err
}

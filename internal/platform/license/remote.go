package license

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/buildinfo"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/secret"
	"lajur/internal/platform/store"
)

const (
	product        = "lajur"
	keyPurpose     = "license-key"
	clockTolerance = 10 * time.Minute
	recheckEvery   = 30 * time.Second
	reloadEvery    = time.Minute
)

// Activation adalah isian aktivasi dari setup wizard atau halaman Sistem.
type Activation struct {
	ConsoleURL string `json:"consoleUrl"`
	ClientID   string `json:"clientId"`
	LicenseKey string `json:"licenseKey"`
}

// Claims adalah isi token JWS yang ditandatangani License Console.
type Claims struct {
	ClientID       string `json:"clientId"`
	Product        string `json:"product"`
	Status         string `json:"status"` // status tersimpan di console: ACTIVE atau SUSPENDED
	ExpiresAt      string `json:"expiresAt"`
	Timezone       string `json:"timezone"`
	WarningDays    *int   `json:"warningDays"`
	MinVersion     string `json:"minVersion"`
	InstallationID string `json:"installationId"`
	IssuedAt       int64  `json:"iat"`
	Expiry         int64  `json:"exp"`
}

type RemoteConfig struct {
	Pool *pgxpool.Pool
	Box  *secret.Box
	// Keys: kunci publik console per kid. Kosong = TrustedKeys().
	Keys map[string]*rsa.PublicKey
	// InstallationUUID dan MachineID membentuk installationId (LIC-02).
	InstallationUUID string
	MachineID        string
	Version          string
	Client           *http.Client
	Now              func() time.Time
	Log              *slog.Logger
	// AllowInsecure mengizinkan console http://localhost (License Console tiruan di laptop).
	AllowInsecure bool
	// Health melengkapi ringkasan teknis yang ikut check-in (tanpa data keuangan).
	Health func(ctx context.Context) map[string]any
}

// Remote adalah provider lisensi produksi: aktivasi dan check-in ke License Console,
// lalu memeriksa token bertanda tangan secara offline di setiap request.
type Remote struct {
	cfg            RemoteConfig
	installationID string

	mu       sync.RWMutex
	snap     snapshot
	loadedAt time.Time
	stale    bool

	recheckMu   sync.Mutex
	lastRecheck time.Time
}

type snapshot struct {
	configured  bool
	consoleURL  string
	clientID    string
	token       string
	claims      *Claims
	tokenErr    string
	checkedAt   *time.Time
	attemptedAt *time.Time
	errCode     string
	errMsg      string
	highWater   time.Time
}

func NewRemote(ctx context.Context, cfg RemoteConfig) (*Remote, error) {
	if cfg.Keys == nil {
		keys, err := TrustedKeys()
		if err != nil {
			return nil, err
		}
		cfg.Keys = keys
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Version == "" {
		cfg.Version = buildinfo.Version
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	sum := sha256.Sum256([]byte(cfg.InstallationUUID + ":" + strings.TrimSpace(cfg.MachineID)))
	r := &Remote{cfg: cfg, installationID: hex.EncodeToString(sum[:])}
	if err := r.reload(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// InstallationID: SHA-256 dari UUID instalasi dan machine-id host.
func (r *Remote) InstallationID() string { return r.installationID }

func (r *Remote) Remote() bool { return true }

// reload membaca konfigurasi dan token terakhir dari basis data (di luar transaksi request).
func (r *Remote) reload(ctx context.Context) error {
	row, err := store.New(r.cfg.Pool).GetLicense(ctx)
	var s snapshot
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return err
	default:
		s = snapshot{
			configured: true, consoleURL: row.ConsoleUrl, clientID: row.ClientID, checkedAt: row.CheckedAt,
			attemptedAt: row.AttemptedAt, errCode: deref(row.ErrorCode), errMsg: deref(row.ErrorMessage), highWater: row.LastActivity,
		}
		if row.Token != nil {
			s.token = *row.Token
			if c, err := r.verify(*row.Token, row.ClientID); err != nil {
				s.tokenErr = err.Error()
			} else {
				s.claims = c
			}
		}
	}
	r.mu.Lock()
	r.snap, r.loadedAt, r.stale = s, r.cfg.Now(), false
	r.mu.Unlock()
	return nil
}

func (r *Remote) current(ctx context.Context) snapshot {
	r.mu.RLock()
	s, fresh := r.snap, !r.stale && r.cfg.Now().Sub(r.loadedAt) < reloadEvery
	r.mu.RUnlock()
	if fresh {
		return s
	}
	if err := r.reload(ctx); err != nil {
		r.cfg.Log.Error("membaca status lisensi", "err", err)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snap
}

// Status menerapkan alur pemeriksaan token di TDD: tanda tangan, umur 72 jam, jam mundur,
// status dan tanggal expired, lalu versi minimum.
func (r *Remote) Status(ctx context.Context) Status {
	s := r.current(ctx)
	now := r.cfg.Now()
	if !s.configured {
		return Status{State: Unverified, Message: "Lisensi belum diaktifkan. Selesaikan setup wizard."}
	}
	// Check-in sukses selalu menghapus kode galat, jadi kode yang tersisa adalah jawaban
	// console pada percobaan terakhir. Penolakan key mengunci aplikasi walau token lama masih berlaku.
	switch s.errCode {
	case "INVALID_KEY", "WRONG_PRODUCT", "INSTALLATION_MISMATCH", "NOT_ACTIVATED":
		return Status{State: InvalidKey, Message: s.errMsg}
	}
	if s.tokenErr != "" {
		return Status{State: InvalidKey, Message: "Token lisensi tidak sah: " + s.tokenErr}
	}
	if s.claims == nil {
		return Status{State: Unverified, Message: "Lajur belum berhasil check-in ke License Console."}
	}
	c := s.claims
	if now.Unix() > c.Expiry {
		return Status{State: Unverified, Message: "Lajur tidak bisa menghubungi License Console lebih dari 72 jam. Data tetap bisa dilihat; periksa koneksi internet server."}
	}
	if now.Before(s.highWater.Add(-clockTolerance)) {
		return Status{State: Unverified, Message: "Jam server mundur dari waktu yang pernah tercatat. Perbaiki jam server; lisensi aktif lagi setelah check-in berikutnya."}
	}
	stored := Active
	if c.Status == string(Suspended) {
		stored = Suspended
	}
	warn := 7
	if c.WarningDays != nil {
		warn = *c.WarningDays
	}
	st := Evaluate(stored, c.ExpiresAt, warn, now, location(c.Timezone))
	if c.MinVersion != "" && buildinfo.Less(r.cfg.Version, c.MinVersion) {
		st.UpdateRequired = true
		st.MinVersion = c.MinVersion
		st.Message = fmt.Sprintf("Versi Lajur ini (%s) sudah tidak didukung. Perbarui ke versi %s atau yang lebih baru; sampai itu, data hanya bisa dibaca.", r.cfg.Version, c.MinVersion)
	}
	return st
}

var (
	locMu sync.Mutex
	locs  = map[string]*time.Location{}
)

func location(name string) *time.Location {
	if name == "" {
		return jakarta
	}
	locMu.Lock()
	defer locMu.Unlock()
	if l, ok := locs[name]; ok {
		return l
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		l = jakarta
	}
	locs[name] = l
	return l
}

// verify memeriksa tanda tangan RS256 dan bahwa token memang untuk instalasi dan client ini.
func (r *Remote) verify(token, clientID string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("format token salah")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &header); err != nil || header.Alg != "RS256" {
		return nil, errors.New("header token tidak dikenal")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("tanda tangan token rusak")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	keys := r.cfg.Keys
	if k, ok := keys[header.Kid]; ok {
		keys = map[string]*rsa.PublicKey{header.Kid: k}
	}
	ok := false
	for _, k := range keys {
		if rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) == nil {
			ok = true
			break
		}
	}
	if !ok {
		return nil, errors.New("tanda tangan tidak cocok dengan kunci License Console")
	}
	var c Claims
	if err := decodeSegment(parts[1], &c); err != nil {
		return nil, errors.New("isi token rusak")
	}
	switch {
	case c.Product != product:
		return nil, errors.New("token bukan untuk Lajur")
	case c.InstallationID != r.installationID:
		return nil, errors.New("token milik instalasi lain")
	case c.ClientID != clientID:
		return nil, errors.New("token milik client lain")
	case c.Expiry <= c.IssuedAt:
		return nil, errors.New("masa berlaku token salah")
	}
	return &c, nil
}

func decodeSegment(seg string, dst any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---------------------------------------------------------------- protokol console

type consoleReply struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Token   string `json:"token"`
}

// call mengirim satu aksi ke web app License Console. Apps Script menjawab POST dengan
// redirect 302 ke halaman hasil; klien HTTP Go mengikutinya sebagai GET, seperti seharusnya.
func (r *Remote) call(ctx context.Context, consoleURL string, body map[string]any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, consoleURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.cfg.Client.Do(req)
	if err != nil {
		return fmt.Errorf("License Console tidak bisa dihubungi: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("License Console menjawab HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("jawaban License Console bukan JSON; periksa alamatnya")
	}
	return nil
}

func (r *Remote) checkURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("alamat License Console tidak valid")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local && r.cfg.AllowInsecure) {
		return "", errors.New("alamat License Console harus diawali https://")
	}
	return raw, nil
}

func (r *Remote) baseBody(action, clientID, key string) map[string]any {
	return map[string]any{
		"action": action, "clientId": clientID, "licenseKey": key,
		"installationId": r.installationID, "appVersion": r.cfg.Version,
	}
}

var activationMessages = map[string]string{
	"INVALID_KEY":           "Client ID atau license key salah.",
	"WRONG_PRODUCT":         "License key ini bukan untuk Lajur.",
	"INSTALLATION_MISMATCH": "Lisensi ini sudah dipakai di server lain. Minta pemilik aplikasi mereset instalasinya di License Console.",
	"RATE_LIMITED":          "Terlalu banyak percobaan. Coba lagi satu jam lagi.",
}

// Activate mengikat license key ke instalasi ini (LIC-02) dan menyimpan token pertama.
// Dipanggil di dalam transaksi setup wizard atau penggantian key, jadi ikut batal bila
// transaksinya batal.
func (r *Remote) Activate(ctx context.Context, in Activation) error {
	fields := map[string]string{}
	consoleURL, err := r.checkURL(in.ConsoleURL)
	if err != nil {
		fields["license.consoleUrl"] = err.Error()
	}
	clientID, key := strings.TrimSpace(in.ClientID), strings.TrimSpace(in.LicenseKey)
	if clientID == "" {
		fields["license.clientId"] = "Client ID wajib diisi."
	}
	if key == "" {
		fields["license.licenseKey"] = "License key wajib diisi."
	}
	if len(fields) > 0 {
		return problem(fields)
	}
	var reply consoleReply
	if err := r.call(ctx, consoleURL, r.baseBody("lajur.activate", clientID, key), &reply); err != nil {
		return problem(map[string]string{"license.consoleUrl": err.Error()})
	}
	if !reply.OK {
		msg := activationMessages[reply.Code]
		if msg == "" {
			msg = "License Console menolak aktivasi: " + reply.Message
		}
		return problem(map[string]string{"license.licenseKey": msg})
	}
	claims, err := r.verify(reply.Token, clientID)
	if err != nil {
		return problem(map[string]string{"license.consoleUrl": "Jawaban License Console tidak bisa dipercaya: " + err.Error()})
	}
	now := r.cfg.Now()
	seen := now
	if iat := time.Unix(claims.IssuedAt, 0); iat.After(seen) {
		seen = iat
	}
	if err := store.New(db.Conn(ctx, r.cfg.Pool)).SaveLicenseConfig(ctx, store.SaveLicenseConfigParams{
		ConsoleUrl: consoleURL, ClientID: clientID, LicenseKey: r.cfg.Box.Seal([]byte(key), keyPurpose),
		Token: &reply.Token, CheckedAt: &seen,
	}); err != nil {
		return err
	}
	r.mu.Lock()
	r.stale = true
	r.mu.Unlock()
	return nil
}

func problem(fields map[string]string) error {
	first := ""
	for _, k := range []string{"license.consoleUrl", "license.clientId", "license.licenseKey"} {
		if fields[k] != "" && first == "" {
			first = fields[k]
		}
	}
	return httpx.Unprocessable("validation", first).WithFields(fields)
}

// credentials membaca alamat console, client ID, dan license key yang tersimpan.
func (r *Remote) credentials(ctx context.Context) (string, string, string, error) {
	row, err := store.New(r.cfg.Pool).GetLicense(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", errNotConfigured
	}
	if err != nil {
		return "", "", "", err
	}
	key, err := r.cfg.Box.Open(row.LicenseKey, keyPurpose)
	if err != nil {
		return "", "", "", fmt.Errorf("license key tidak bisa dibuka (LAJUR_APP_KEY berubah?): %w", err)
	}
	return row.ConsoleUrl, row.ClientID, string(key), nil
}

var errNotConfigured = httpx.Conflict("license.not_configured", "Lisensi belum diaktifkan.")

// CheckIn memperbarui token (tiap jam). Galat jaringan tidak menghapus token lama, jadi
// gangguan singkat tidak terasa selama token masih dalam 72 jam.
func (r *Remote) CheckIn(ctx context.Context) error {
	consoleURL, clientID, key, err := r.credentials(ctx)
	if err != nil {
		return err
	}
	body := r.baseBody("lajur.checkin", clientID, key)
	if r.cfg.Health != nil {
		body["health"] = r.cfg.Health(ctx)
	}
	now := r.cfg.Now()
	q := store.New(r.cfg.Pool)
	fail := func(code, msg string) error {
		if err := q.SaveCheckinError(ctx, store.SaveCheckinErrorParams{AttemptedAt: &now, ErrorCode: &code, ErrorMessage: &msg}); err != nil {
			return err
		}
		_ = r.reload(ctx)
		return fmt.Errorf("%s", msg)
	}
	var reply consoleReply
	if err := r.call(ctx, consoleURL, body, &reply); err != nil {
		return fail("NETWORK", err.Error())
	}
	if !reply.OK {
		msg := activationMessages[reply.Code]
		if reply.Code == "NOT_ACTIVATED" {
			msg = "Instalasi ini belum diaktifkan atau sudah direset di License Console. Aktifkan ulang dari halaman Sistem."
		}
		if msg == "" {
			msg = "License Console menolak check-in: " + reply.Message
		}
		return fail(reply.Code, msg)
	}
	claims, err := r.verify(reply.Token, clientID)
	if err != nil {
		return fail("BAD_TOKEN", "Token dari License Console ditolak: "+err.Error())
	}
	seen := now
	if iat := time.Unix(claims.IssuedAt, 0); iat.After(seen) {
		seen = iat
	}
	if err := q.SaveCheckin(ctx, store.SaveCheckinParams{Token: &reply.Token, CheckedAt: &now, Seen: seen}); err != nil {
		return err
	}
	return r.reload(ctx)
}

// Recheck adalah tombol "Cek ulang lisensi": paling sering sekali per 30 detik.
func (r *Remote) Recheck(ctx context.Context) error {
	r.recheckMu.Lock()
	if wait := recheckEvery - r.cfg.Now().Sub(r.lastRecheck); wait > 0 {
		r.recheckMu.Unlock()
		return httpx.NewProblem(http.StatusTooManyRequests, "license.recheck_wait", "Tunggu sebentar",
			fmt.Sprintf("Cek ulang lisensi bisa dilakukan lagi dalam %d detik.", int(wait.Seconds())+1))
	}
	r.lastRecheck = r.cfg.Now()
	r.recheckMu.Unlock()
	return r.CheckIn(ctx)
}

// Run menjalankan check-in tiap jam (dengan sedikit acak supaya server pelanggan tidak
// serentak) dan mencatat waktu terakhir yang terlihat untuk deteksi jam mundur.
func (r *Remote) Run(ctx context.Context) {
	next := time.NewTimer(5 * time.Second)
	bump := time.NewTicker(5 * time.Minute)
	defer next.Stop()
	defer bump.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-bump.C:
			if r.current(ctx).configured {
				if err := store.New(r.cfg.Pool).BumpHighWater(ctx, r.cfg.Now()); err != nil {
					r.cfg.Log.Error("mencatat waktu lisensi", "err", err)
				}
			}
		case <-next.C:
			wait := time.Hour + time.Duration(rand.IntN(300))*time.Second
			if r.current(ctx).configured {
				if err := r.CheckIn(ctx); err != nil && ctx.Err() == nil {
					r.cfg.Log.Warn("check-in lisensi gagal", "err", err)
					wait = 10 * time.Minute
				}
			}
			next.Reset(wait)
		}
	}
}

// Token adalah token lisensi terakhir yang sah; dikirim ke update server supaya unduhan
// pembaruan hanya dilayani untuk lisensi aktif.
func (r *Remote) Token(ctx context.Context) string {
	s := r.current(ctx)
	if s.claims == nil {
		return ""
	}
	return s.token
}

// Detail untuk halaman Sistem (Pemilik, Manager, Admin pelanggan).
type Detail struct {
	Configured     bool       `json:"configured"`
	ConsoleURL     string     `json:"consoleUrl,omitempty"`
	ClientID       string     `json:"clientId,omitempty"`
	InstallationID string     `json:"installationId"`
	Version        string     `json:"version"`
	CheckedAt      *time.Time `json:"checkedAt,omitempty"`
	AttemptedAt    *time.Time `json:"attemptedAt,omitempty"`
	ErrorCode      string     `json:"errorCode,omitempty"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
	TokenValidTo   *time.Time `json:"tokenValidTo,omitempty"`
	ExpiresAt      string     `json:"expiresAt,omitempty"`
	MinVersion     string     `json:"minVersion,omitempty"`
}

func (r *Remote) Detail(ctx context.Context) Detail {
	s := r.current(ctx)
	d := Detail{
		Configured: s.configured, ConsoleURL: s.consoleURL, ClientID: s.clientID, InstallationID: r.installationID,
		Version: r.cfg.Version, CheckedAt: s.checkedAt, AttemptedAt: s.attemptedAt, ErrorCode: s.errCode, ErrorMessage: s.errMsg,
	}
	if s.claims != nil {
		t := time.Unix(s.claims.Expiry, 0)
		d.TokenValidTo, d.ExpiresAt, d.MinVersion = &t, s.claims.ExpiresAt, s.claims.MinVersion
	}
	return d
}

// Payment meneruskan aksi pembayaran perpanjangan (LIC-07) ke License Console:
// "paymentInfo" (paket dan status bukti bayar terakhir) atau "submitPayment".
func (r *Remote) Payment(ctx context.Context, action string, extra map[string]any) (map[string]any, error) {
	if action != "paymentInfo" && action != "submitPayment" {
		return nil, fmt.Errorf("aksi pembayaran tidak dikenal: %s", action)
	}
	consoleURL, clientID, key, err := r.credentials(ctx)
	if err != nil {
		return nil, err
	}
	body := r.baseBody(action, clientID, key)
	for k, v := range extra {
		body[k] = v
	}
	var out map[string]any
	if err := r.call(ctx, consoleURL, body, &out); err != nil {
		return nil, httpx.NewProblem(http.StatusBadGateway, "license.console_unreachable", "License Console tidak bisa dihubungi", err.Error())
	}
	if ok, _ := out["ok"].(bool); !ok {
		msg, _ := out["message"].(string)
		if msg == "" {
			msg = "License Console menolak permintaan ini."
		}
		code, _ := out["code"].(string)
		return nil, httpx.Unprocessable("license."+strings.ToLower(code), msg)
	}
	return out, nil
}

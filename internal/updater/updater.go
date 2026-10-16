// Package updater menjalankan pembaruan satu klik (PLT-10) di container terpisah yang
// memegang docker.sock. Updater tidak membuka port ke luar; ia hanya menerima perintah dari
// container app lewat jaringan internal dengan token, memverifikasi ulang manifest rilis
// bertanda tangan, dan hanya menjalankan image yang disematkan dengan digest di manifest itu.
//
// Langkah pembaruan: backup sebelum pembaruan → unduh image → jalankan versi baru → periksa
// kesehatan. Bila versi baru tidak sehat, updater kembali ke image lama. Basis data tidak
// dipulihkan otomatis: migrasi memakai pola expand-then-contract sehingga versi lama tetap
// jalan di skema baru, dan kunci privat backup memang tidak ada di server. Backup sebelum
// pembaruan tetap tersedia untuk dipulihkan manual oleh Pemilik bila perlu.
package updater

import (
	"bufio"
	"context"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lajur/internal/platform/update"
)

// Docker menjalankan perintah CLI docker; dipalsukan di tes.
type Docker interface {
	Run(ctx context.Context, args ...string) (string, error)
}

type Config struct {
	Dir   string // folder compose, mis. /opt/lajur
	Token string
	Keys  map[string]*rsa.PublicKey
	Docker
	// Health mengembalikan versi aplikasi yang sedang menjawab /healthz.
	Health        func(ctx context.Context) (string, error)
	HealthTimeout time.Duration
	PollEvery     time.Duration
	Now           func() time.Time
	Log           *slog.Logger
	AppContainer  string
}

type LogLine struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
}

type State struct {
	Status     string     `json:"status"` // idle, running, succeeded, failed, rolled_back
	From       string     `json:"from,omitempty"`
	To         string     `json:"to,omitempty"`
	Step       string     `json:"step,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Log        []LogLine  `json:"log"`
}

type Updater struct {
	cfg   Config
	mu    sync.Mutex
	state State
	wg    sync.WaitGroup
}

func New(cfg Config) *Updater {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.HealthTimeout == 0 {
		cfg.HealthTimeout = 5 * time.Minute
	}
	if cfg.PollEvery == 0 {
		cfg.PollEvery = 2 * time.Second
	}
	if cfg.AppContainer == "" {
		cfg.AppContainer = "lajur-app"
	}
	return &Updater{cfg: cfg, state: State{Status: "idle", Log: []LogLine{}}}
}

var ErrBusy = errors.New("pembaruan lain sedang berjalan")

func (u *Updater) Status() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.state
	s.Log = append([]LogLine{}, u.state.Log...)
	return s
}

// Wait menunggu pembaruan yang sedang berjalan selesai (dipakai tes dan saat berhenti).
func (u *Updater) Wait() { u.wg.Wait() }

func (u *Updater) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	u.mu.Lock()
	u.state.Log = append(u.state.Log, LogLine{At: u.cfg.Now(), Message: msg})
	u.mu.Unlock()
	u.cfg.Log.Info("pembaruan", "langkah", msg)
}

func (u *Updater) step(s string) {
	u.mu.Lock()
	u.state.Step = s
	u.mu.Unlock()
	u.logf("%s", s)
}

// Apply memverifikasi manifest lalu menjalankan pembaruan di latar belakang.
func (u *Updater) Apply(token string) error {
	m, err := update.Parse(token, u.cfg.Keys)
	if err != nil {
		return err
	}
	u.mu.Lock()
	if u.state.Status == "running" {
		u.mu.Unlock()
		return ErrBusy
	}
	now := u.cfg.Now()
	u.state = State{Status: "running", To: m.Version, StartedAt: &now, Log: []LogLine{}}
	u.mu.Unlock()
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		u.run(context.Background(), m)
	}()
	return nil
}

func (u *Updater) finish(status, errMsg string) {
	now := u.cfg.Now()
	u.mu.Lock()
	u.state.Status, u.state.Error, u.state.FinishedAt, u.state.Step = status, errMsg, &now, ""
	u.mu.Unlock()
	if errMsg != "" {
		u.logf("Selesai: %s — %s", status, errMsg)
	} else {
		u.logf("Selesai: %s", status)
	}
}

func (u *Updater) run(ctx context.Context, m update.Manifest) {
	current, err := u.cfg.Health(ctx)
	if err != nil {
		u.finish("failed", "aplikasi tidak sehat sebelum pembaruan: "+err.Error())
		return
	}
	u.mu.Lock()
	u.state.From = current
	u.mu.Unlock()
	switch {
	case !m.Newer(current):
		u.finish("failed", fmt.Sprintf("versi %s sudah terpasang atau lebih baru dari %s", current, m.Version))
		return
	case !m.Reachable(current):
		u.finish("failed", fmt.Sprintf("perbarui dulu ke versi %s atau lebih baru sebelum ke %s", m.MinFrom, m.Version))
		return
	}

	u.step("Membuat backup sebelum pembaruan")
	if out, err := u.cfg.Run(ctx, "exec", u.cfg.AppContainer, "lajur", "backup", "--kind", "pre_update"); err != nil {
		u.finish("failed", "backup gagal, pembaruan dibatalkan: "+strings.TrimSpace(out+" "+err.Error()))
		return
	} else {
		u.logf("%s", strings.TrimSpace(out))
	}

	u.step("Mengunduh image " + m.Version)
	if out, err := u.cfg.Run(ctx, "pull", m.Image); err != nil {
		u.finish("failed", "image tidak bisa diunduh: "+strings.TrimSpace(out+" "+err.Error()))
		return
	}

	envFile := filepath.Join(u.cfg.Dir, ".env")
	previous, err := readEnv(envFile, "LAJUR_IMAGE")
	if err != nil {
		u.finish("failed", err.Error())
		return
	}
	u.step("Menjalankan versi " + m.Version)
	if err := setEnv(envFile, "LAJUR_IMAGE", m.Image); err != nil {
		u.finish("failed", err.Error())
		return
	}
	if err := u.up(ctx); err == nil {
		u.step("Memeriksa kesehatan versi baru")
		if err = u.waitVersion(ctx, m.Version); err == nil {
			u.finish("succeeded", "")
			return
		}
		u.rollback(ctx, envFile, previous, current, err)
		return
	} else {
		u.rollback(ctx, envFile, previous, current, err)
	}
}

func (u *Updater) up(ctx context.Context) error {
	out, err := u.cfg.Run(ctx, "compose", "--project-directory", u.cfg.Dir, "-f", filepath.Join(u.cfg.Dir, "docker-compose.yml"), "up", "-d", "app")
	if err != nil {
		return fmt.Errorf("%s %w", strings.TrimSpace(out), err)
	}
	return nil
}

// rollback mengembalikan image lama bila versi baru gagal dijalankan atau tidak sehat.
func (u *Updater) rollback(ctx context.Context, envFile, previous, oldVersion string, cause error) {
	u.step("Versi baru tidak sehat; kembali ke versi " + oldVersion)
	if err := setEnv(envFile, "LAJUR_IMAGE", previous); err != nil {
		u.finish("failed", "rollback gagal menulis .env: "+err.Error()+" (penyebab awal: "+cause.Error()+")")
		return
	}
	if err := u.up(ctx); err != nil {
		u.finish("failed", "rollback gagal: "+err.Error()+" (penyebab awal: "+cause.Error()+")")
		return
	}
	if err := u.waitVersion(ctx, oldVersion); err != nil {
		u.finish("failed", "versi lama belum sehat setelah rollback: "+err.Error())
		return
	}
	u.finish("rolled_back", cause.Error())
}

func (u *Updater) waitVersion(ctx context.Context, want string) error {
	deadline := u.cfg.Now().Add(u.cfg.HealthTimeout)
	var last error
	for {
		v, err := u.cfg.Health(ctx)
		if err == nil && v == want {
			return nil
		}
		if err == nil {
			last = fmt.Errorf("aplikasi menjawab versi %s, bukan %s", v, want)
		} else {
			last = err
		}
		if !u.cfg.Now().Before(deadline) {
			return fmt.Errorf("tidak sehat dalam %s: %w", u.cfg.HealthTimeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(u.cfg.PollEvery):
		}
	}
}

func readEnv(path, key string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), key+"="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("%s tidak ada di %s", key, path)
}

// setEnv mengganti satu baris di .env secara atomik (tulis berkas baru lalu rename).
func setEnv(path, key, value string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, key+"=") {
			lines[i], found = key+"="+value, true
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Handler: GET /status dan POST /apply, keduanya wajib "Authorization: Bearer <token>".
func (u *Updater) Handler() http.Handler {
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if u.cfg.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(u.cfg.Token)) != 1 {
				http.Error(w, "token salah", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /status", auth(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u.Status())
	}))
	mux.HandleFunc("POST /apply", auth(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Manifest string `json:"manifest"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
			http.Error(w, "body tidak valid", http.StatusBadRequest)
			return
		}
		err := u.Apply(body.Manifest)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case errors.Is(err, ErrBusy):
			w.WriteHeader(http.StatusConflict)
		case err != nil:
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": err == nil, "message": msg})
	}))
	return mux
}

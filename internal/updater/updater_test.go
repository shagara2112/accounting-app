package updater

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lajur/internal/platform/jws"
	"lajur/internal/platform/update"
)

const (
	oldImage = "ghcr.io/lajur/lajur@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	newImage = "ghcr.io/lajur/lajur@sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// fakeDocker mencatat perintah dan mensimulasikan versi aplikasi yang berjalan menurut
// image di .env setelah "compose up".
type fakeDocker struct {
	mu       sync.Mutex
	cmds     []string
	dir      string
	running  string // versi yang menjawab /healthz
	versions map[string]string
	fail     map[string]error
	broken   bool // versi baru jalan tapi tidak pernah sehat
}

func (f *fakeDocker) Run(_ context.Context, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := strings.Join(args, " ")
	f.cmds = append(f.cmds, cmd)
	for prefix, err := range f.fail {
		if strings.HasPrefix(cmd, prefix) {
			return "keluaran galat", err
		}
	}
	if strings.HasPrefix(cmd, "compose") {
		img, _ := readEnv(filepath.Join(f.dir, ".env"), "LAJUR_IMAGE")
		f.running = f.versions[img]
		if f.broken && img == newImage {
			f.running = "mati"
		}
	}
	if strings.HasPrefix(cmd, "exec") {
		return "Backup selesai: lajur-x.lajurbk", nil
	}
	return "", nil
}

func (f *fakeDocker) health(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running == "mati" {
		return "", errors.New("connection refused")
	}
	return f.running, nil
}

func setup(t *testing.T) (*Updater, *fakeDocker, *rsa.PrivateKey) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("POSTGRES_PASSWORD=x\nLAJUR_IMAGE="+oldImage+"\nLAJUR_APP_KEY=y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	fd := &fakeDocker{dir: dir, running: "1.2.0", versions: map[string]string{oldImage: "1.2.0", newImage: "1.3.0"}, fail: map[string]error{}}
	u := New(Config{
		Dir: dir, Token: "rahasia-updater", Keys: map[string]*rsa.PublicKey{"rilis": &key.PublicKey}, Docker: fd,
		Health: fd.health, HealthTimeout: 50 * time.Millisecond, PollEvery: 10 * time.Millisecond,
	})
	return u, fd, key
}

func manifest(t *testing.T, key *rsa.PrivateKey, version, image string) string {
	t.Helper()
	tok, err := jws.Sign(key, "rilis", update.Manifest{Product: "lajur", Channel: "stable", Version: version, Image: image, PublishedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestUpdateSucceeds(t *testing.T) {
	u, fd, key := setup(t)
	if err := u.Apply(manifest(t, key, "1.3.0", newImage)); err != nil {
		t.Fatal(err)
	}
	u.Wait()
	st := u.Status()
	if st.Status != "succeeded" || st.From != "1.2.0" || st.To != "1.3.0" {
		t.Fatalf("status = %+v", st)
	}
	if img, _ := readEnv(filepath.Join(fd.dir, ".env"), "LAJUR_IMAGE"); img != newImage {
		t.Fatalf("image di .env = %s", img)
	}
	if !strings.HasPrefix(fd.cmds[0], "exec lajur-app lajur backup --kind pre_update") || !strings.HasPrefix(fd.cmds[1], "pull "+newImage) || !strings.Contains(fd.cmds[2], "up -d app") {
		t.Fatalf("urutan perintah = %v", fd.cmds)
	}
	if raw, _ := os.ReadFile(filepath.Join(fd.dir, ".env")); !strings.Contains(string(raw), "LAJUR_APP_KEY=y") {
		t.Fatal("baris .env lain hilang")
	}
}

func TestBackupFailureAbortsBeforeAnyChange(t *testing.T) {
	u, fd, key := setup(t)
	fd.fail["exec"] = errors.New("exit 1")
	_ = u.Apply(manifest(t, key, "1.3.0", newImage))
	u.Wait()
	st := u.Status()
	if st.Status != "failed" || !strings.Contains(st.Error, "backup gagal") {
		t.Fatalf("status = %+v", st)
	}
	if len(fd.cmds) != 1 {
		t.Fatalf("tidak boleh ada langkah setelah backup gagal: %v", fd.cmds)
	}
	if img, _ := readEnv(filepath.Join(fd.dir, ".env"), "LAJUR_IMAGE"); img != oldImage {
		t.Fatal(".env berubah padahal pembaruan batal")
	}
}

func TestUnhealthyNewVersionRollsBack(t *testing.T) {
	u, fd, key := setup(t)
	fd.broken = true
	_ = u.Apply(manifest(t, key, "1.3.0", newImage))
	u.Wait()
	st := u.Status()
	if st.Status != "rolled_back" || fd.running != "1.2.0" {
		t.Fatalf("status = %+v, berjalan %s", st, fd.running)
	}
	if img, _ := readEnv(filepath.Join(fd.dir, ".env"), "LAJUR_IMAGE"); img != oldImage {
		t.Fatalf("image setelah rollback = %s", img)
	}
	ups := 0
	for _, c := range fd.cmds {
		if strings.Contains(c, "up -d app") {
			ups++
		}
	}
	if ups != 2 {
		t.Fatalf("compose up = %d kali, seharusnya 2 (baru lalu kembali): %v", ups, fd.cmds)
	}
}

func TestRejectsOldVersionUnsignedAndConcurrent(t *testing.T) {
	u, fd, key := setup(t)
	_ = u.Apply(manifest(t, key, "1.2.0", newImage))
	u.Wait()
	if st := u.Status(); st.Status != "failed" || len(fd.cmds) != 0 {
		t.Fatalf("versi sama harus ditolak tanpa perintah docker: %+v %v", st, fd.cmds)
	}
	stranger, _ := rsa.GenerateKey(rand.Reader, 2048)
	if err := u.Apply(manifest(t, stranger, "1.3.0", newImage)); err == nil {
		t.Fatal("manifest bertanda tangan kunci asing harus ditolak")
	}
	if err := u.Apply(manifest(t, key, "1.3.0", "ghcr.io/lajur/lajur:1.3.0")); err == nil {
		t.Fatal("image tanpa digest harus ditolak")
	}

	fd.mu.Lock() // tahan langkah pertama supaya pembaruan pertama masih berjalan
	if err := u.Apply(manifest(t, key, "1.3.0", newImage)); err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(manifest(t, key, "1.3.0", newImage)); !errors.Is(err, ErrBusy) {
		t.Fatalf("pembaruan kedua = %v, seharusnya ErrBusy", err)
	}
	fd.mu.Unlock()
	u.Wait()
}

func TestHandlerRequiresToken(t *testing.T) {
	u, _, key := setup(t)
	srv := httptest.NewServer(u.Handler())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/status", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tanpa token = %d", resp.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer rahasia-updater")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusOK {
		t.Fatalf("dengan token = %d", resp.StatusCode)
	}
	body := strings.NewReader(`{"manifest":"` + manifest(t, key, "1.3.0", newImage) + `"}`)
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/apply", body)
	req.Header.Set("Authorization", "Bearer salah")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("apply dengan token salah = %d", resp.StatusCode)
	}
}

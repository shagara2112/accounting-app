package backup_test

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lajur/internal/platform/audit"
	"lajur/internal/platform/backup"
	"lajur/internal/platform/db"
	"lajur/internal/platform/dbtest"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/secret"
	"lajur/internal/platform/store"
)

// env menyiapkan basis data uji berisi data contoh dan layanan backup. Tes dilewati bila
// pg_dump tidak tersedia (langsung atau lewat container LAJUR_TEST_PG_DOCKER).
func env(t *testing.T) (*backup.Service, string, string, string, backup.Config) {
	t.Helper()
	docker := os.Getenv("LAJUR_TEST_PG_DOCKER")
	if _, err := exec.LookPath("pg_dump"); err != nil && docker == "" {
		t.Skip("pg_dump tidak ada dan LAJUR_TEST_PG_DOCKER kosong")
	}
	pool, dbURL := dbtest.PoolURL(t)
	ctx := context.Background()
	if _, err := store.New(pool).CreateUser(ctx, store.CreateUserParams{ID: uuid.Must(uuid.NewV7()), Email: "uji@lajur.test", Name: "Uji", PasswordHash: "x"}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := audit.Write(ctx, pool, audit.Entry{Action: "uji.backup", ObjectType: "uji", ObjectID: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.New(pool).EnsureInstallation(ctx, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatal(err)
	}
	priv, pub, err := backup.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	pk, _ := backup.ParsePublicKey(pub)
	box, _ := secret.New([]byte("kunci-uji-lajur-tiga-puluh-dua-b"))
	dir := t.TempDir()
	files := t.TempDir()
	if err := os.MkdirAll(filepath.Join(files, "lampiran"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "lampiran", "faktur.pdf"), []byte("%PDF-uji"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := backup.Config{
		Pool: pool, DatabaseURL: dbURL, Dir: dir, FilesDir: files, PublicKey: pk, Docker: docker,
		Box: box, InstallationID: "0123456789abcdef", Version: "1.0.0-uji", Location: time.UTC,
	}
	svc := backup.New(cfg)
	// Basis data uji pulih tidak boleh tertinggal di server pengembangan.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP DATABASE IF EXISTS lajur_restore_check WITH (FORCE)")
	})
	return svc, priv, dir, dbURL, cfg
}

func TestBackupEncryptsRestoresAndRestoreTestPasses(t *testing.T) {
	svc, priv, dir, dbURL, _ := env(t)
	ctx := context.Background()

	r, err := svc.Run(ctx, "manual", nil)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if r.Status != "ok" || r.SizeBytes == 0 || len(r.SHA256) != 64 || r.Copies["local"] != "ok" || r.KeyID != svc.KeyID() {
		t.Fatalf("hasil backup = %+v", r)
	}
	raw, err := os.ReadFile(filepath.Join(dir, r.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("uji@lajur.test")) || bytes.Contains(raw, []byte("%PDF-uji")) {
		t.Fatal("isi backup terbaca tanpa kunci")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") || strings.HasSuffix(e.Name(), ".part") {
			t.Fatalf("berkas sementara tertinggal: %s", e.Name())
		}
	}

	// Uji pulih pertama langsung berjalan dan memverifikasi rantai audit hasil pulih.
	st, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.RestoreTest == nil || !st.RestoreTest.OK || !strings.Contains(st.RestoreTest.Detail, "audit trail utuh") {
		t.Fatalf("uji pulih = %+v", st.RestoreTest)
	}
	if st.LastOK == nil || st.Overdue || st.LastFailed != nil {
		t.Fatalf("status = %+v", st)
	}

	// Isi arsip: manifest, dump, dan lampiran.
	pk, _ := backup.ParsePrivateKey(priv)
	var plain bytes.Buffer
	if err := backup.Decrypt(&plain, bytes.NewReader(raw), pk); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	tr := tar.NewReader(&plain)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[h.Name] = true
	}
	if !names["manifest.json"] || !names["db.dump"] || !names["files/lampiran/faktur.pdf"] {
		t.Fatalf("isi arsip = %v", names)
	}

	// Pulihkan ke basis data baru dengan kunci Pemilik.
	target := newDatabase(t, dbURL)
	restored := t.TempDir()
	var steps []string
	if err := svc.Restore(ctx, filepath.Join(dir, r.FileName), pk, target, restored, func(m string) { steps = append(steps, m) }); err != nil {
		t.Fatalf("restore: %v (%v)", err, steps)
	}
	tp, err := db.Open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer tp.Close()
	if _, err := store.New(tp).GetUserByEmail(ctx, "uji@lajur.test"); err != nil {
		t.Fatalf("data tidak ikut pulih: %v", err)
	}
	if res, err := audit.Verify(ctx, tp); err != nil || !res.OK || res.Count != 3 {
		t.Fatalf("audit hasil pulih = %+v, %v", res, err)
	}
	if b, err := os.ReadFile(filepath.Join(restored, "lampiran", "faktur.pdf")); err != nil || string(b) != "%PDF-uji" {
		t.Fatalf("lampiran tidak pulih: %v", err)
	}

	// Kunci lain tidak bisa membuka backup.
	other, _, _ := backup.NewKey()
	opk, _ := backup.ParsePrivateKey(other)
	if err := svc.Restore(ctx, filepath.Join(dir, r.FileName), opk, target, "", func(string) {}); err == nil {
		t.Fatal("kunci lain seharusnya ditolak")
	}
}

func TestBackupRetentionCopyDirAndFailure(t *testing.T) {
	svc, _, dir, _, cfg := env(t)
	ctx := context.Background()

	set, err := svc.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var p *httpx.Problem
	set.CopyDir = "relatif/nas"
	if _, err := svc.SaveSettings(ctx, set); !errors.As(err, &p) || p.Fields["copyDir"] == "" {
		t.Fatalf("folder relatif harus ditolak: %v", err)
	}
	set.CopyDir, set.S3Endpoint, set.S3Bucket = "", "http://s3.contoh", "b"
	if _, err := svc.SaveSettings(ctx, set); !errors.As(err, &p) || p.Fields["s3Endpoint"] == "" {
		t.Fatalf("S3 tanpa https harus ditolak: %v", err)
	}
	nas := t.TempDir()
	set.CopyDir, set.Keep, set.S3Endpoint, set.S3Bucket = nas, 3, "", ""
	if set, err = svc.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveSettings(ctx, backup.Settings{Hour: 1, Keep: 30, Version: set.Version - 1}); err == nil {
		t.Fatal("versi lama harus ditolak")
	}

	var last backup.Record
	for range 4 {
		r, err := svc.Run(ctx, "manual", nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Copies["dir"] != "ok" {
			t.Fatalf("salinan folder = %v", r.Copies)
		}
		last = r
		time.Sleep(1100 * time.Millisecond) // nama file berresolusi detik
	}
	count := func(d string) int {
		n := 0
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), backup.Ext) {
				n++
			}
		}
		return n
	}
	if count(dir) != 3 || count(nas) != 3 {
		t.Fatalf("retensi: lokal %d, NAS %d, seharusnya 3", count(dir), count(nas))
	}
	list, _ := svc.List(ctx, 10)
	pruned := 0
	for _, r := range list {
		if r.Pruned {
			pruned++
		}
	}
	if pruned != 1 {
		t.Fatalf("backup yang dipangkas = %d", pruned)
	}
	f, rec, err := svc.Open(ctx, last.ID)
	if err != nil || rec.ID != last.ID {
		t.Fatal(err)
	}
	f.Close()

	// pg_dump gagal: backup tercatat gagal dan status menampilkannya.
	cfg.DatabaseURL = "postgres://lajur:salah@127.0.0.1:1/tidakada"
	bad := backup.New(cfg)
	if _, err := bad.Run(ctx, "manual", nil); err == nil {
		t.Fatal("backup dengan basis data salah seharusnya gagal")
	}
	st, _ := svc.Status(ctx)
	if st.LastFailed == nil || !strings.Contains(st.LastFailed.Error, "pg_dump") {
		t.Fatalf("galat terakhir = %+v", st.LastFailed)
	}
}

// newDatabase membuat basis data kosong di server yang sama untuk tujuan pemulihan.
func newDatabase(t *testing.T, base string) string {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	name := "lajur_pulih_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p, err := db.Open(context.Background(), base)
		if err == nil {
			_, _ = p.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			p.Close()
		}
	})
	u, _ := url.Parse(base)
	u.Path = "/" + name
	return u.String()
}

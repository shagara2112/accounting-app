package backup

import (
	"archive/tar"
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/secret"
	"lajur/internal/platform/store"
)

const (
	Ext            = ".lajurbk"
	restoreDB      = "lajur_restore_check"
	restoreEvery   = 7 * 24 * time.Hour
	overdueAfter   = 48 * time.Hour
	s3SecretPurpse = "backup-s3-secret"
)

type Config struct {
	Pool        *pgxpool.Pool
	DatabaseURL string
	// Dir: folder backup lokal (volume Docker). FilesDir: lampiran yang ikut dibackup.
	Dir      string
	FilesDir string
	// PublicKey penerima; nil berarti backup belum dikonfigurasi.
	PublicKey *ecdh.PublicKey
	PgDump    string
	PgRestore string
	// Docker: nama container PostgreSQL bila pg_dump/pg_restore dijalankan lewat
	// `docker exec` (laptop pengembang tanpa klien PostgreSQL).
	Docker         string
	Box            *secret.Box
	InstallationID string
	Version        string
	Location       *time.Location
	Now            func() time.Time
	Log            *slog.Logger
	// OnFailure dipanggil setelah backup gagal (mis. untuk email ke admin).
	OnFailure func(ctx context.Context, r Record)
}

type Service struct {
	cfg     Config
	mu      sync.Mutex  // satu backup pada satu waktu
	running atomic.Bool // untuk status di UI
}

func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.PgDump == "" {
		cfg.PgDump = "pg_dump"
	}
	if cfg.PgRestore == "" {
		cfg.PgRestore = "pg_restore"
	}
	return &Service{cfg: cfg}
}

// Configured: kunci publik backup sudah dipasang.
func (s *Service) Configured() bool { return s.cfg.PublicKey != nil }

func (s *Service) KeyID() string {
	if s.cfg.PublicKey == nil {
		return ""
	}
	return KeyID(s.cfg.PublicKey)
}

// Record adalah satu baris riwayat backup.
type Record struct {
	ID         uuid.UUID         `json:"id"`
	Kind       string            `json:"kind"`
	Status     string            `json:"status"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt *time.Time        `json:"finishedAt,omitempty"`
	FileName   string            `json:"fileName,omitempty"`
	SizeBytes  int64             `json:"sizeBytes,omitempty"`
	SHA256     string            `json:"sha256,omitempty"`
	KeyID      string            `json:"keyId,omitempty"`
	Copies     map[string]string `json:"copies"`
	Error      string            `json:"error,omitempty"`
	Pruned     bool              `json:"pruned"`
}

func toRecord(b store.PlatformBackup) Record {
	r := Record{ID: b.ID, Kind: b.Kind, Status: b.Status, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt, Pruned: b.Pruned, Copies: map[string]string{}}
	if b.FileName != nil {
		r.FileName = *b.FileName
	}
	if b.SizeBytes != nil {
		r.SizeBytes = *b.SizeBytes
	}
	if b.Sha256 != nil {
		r.SHA256 = *b.Sha256
	}
	if b.KeyID != nil {
		r.KeyID = *b.KeyID
	}
	if b.Error != nil {
		r.Error = *b.Error
	}
	_ = json.Unmarshal(b.Copies, &r.Copies)
	return r
}

// Run membuat satu backup lengkap. kind: scheduled, manual, atau pre_update.
func (s *Service) Run(ctx context.Context, kind string, actor *uuid.UUID) (Record, error) {
	if !s.Configured() {
		return Record{}, httpx.Conflict("backup.not_configured", "Kunci publik backup belum dipasang (LAJUR_BACKUP_PUBLIC_KEY). Jalankan ulang skrip instal atau hubungi pemasang.")
	}
	if !s.mu.TryLock() {
		return Record{}, httpx.Conflict("backup.running", "Backup lain sedang berjalan. Tunggu sampai selesai.")
	}
	defer s.mu.Unlock()

	q := store.New(s.cfg.Pool)
	id := uuid.Must(uuid.NewV7())
	start := s.cfg.Now()
	keyID := KeyID(s.cfg.PublicKey)
	if err := q.InsertBackup(ctx, store.InsertBackupParams{ID: id, Kind: kind, StartedAt: start, KeyID: &keyID, CreatedBy: actor}); err != nil {
		return Record{}, err
	}
	s.running.Store(true)
	defer s.running.Store(false)

	name, size, sum, copies, err := s.produce(ctx, id, start, kind)
	status, errMsg := "ok", (*string)(nil)
	if err != nil {
		status = "failed"
		m := err.Error()
		errMsg = &m
		s.cfg.Log.Error("backup gagal", "id", id, "err", err)
	}
	copiesJSON, _ := json.Marshal(copies)
	end := s.cfg.Now()
	var namePtr, sumPtr *string
	var sizePtr *int64
	if err == nil {
		namePtr, sumPtr, sizePtr = &name, &sum, &size
	}
	if ferr := q.FinishBackup(context.WithoutCancel(ctx), store.FinishBackupParams{
		ID: id, Status: status, FinishedAt: &end, FileName: namePtr, SizeBytes: sizePtr, Sha256: sumPtr, Copies: copiesJSON, Error: errMsg,
	}); ferr != nil {
		return Record{}, ferr
	}
	if err == nil {
		s.prune(ctx)
	}
	row, gerr := q.GetBackup(ctx, id)
	if gerr != nil {
		return Record{}, gerr
	}
	if err != nil && s.cfg.OnFailure != nil {
		s.cfg.OnFailure(context.WithoutCancel(ctx), toRecord(row))
	}
	return toRecord(row), err
}

// produce: pg_dump → (uji pulih bila sudah waktunya) → tar → enkripsi → salinan tambahan.
func (s *Service) produce(ctx context.Context, id uuid.UUID, start time.Time, kind string) (string, int64, string, map[string]string, error) {
	copies := map[string]string{}
	if err := os.MkdirAll(s.cfg.Dir, 0o700); err != nil {
		return "", 0, "", copies, fmt.Errorf("folder backup %s: %w", s.cfg.Dir, err)
	}
	tmp, err := os.MkdirTemp(s.cfg.Dir, ".tmp-")
	if err != nil {
		return "", 0, "", copies, err
	}
	defer os.RemoveAll(tmp)

	dump := filepath.Join(tmp, "db.dump")
	if err := s.pgDump(ctx, dump); err != nil {
		return "", 0, "", copies, err
	}
	if kind != "pre_update" && s.restoreDue(ctx) {
		s.restoreTest(ctx, id, dump)
	}

	name := fmt.Sprintf("lajur-%s-%s-%s%s", short(s.cfg.InstallationID), start.In(s.cfg.Location).Format("20060102-150405"), strings.ReplaceAll(kind, "_", "-"), Ext)
	final := filepath.Join(s.cfg.Dir, name)
	part := final + ".part"
	size, sum, err := s.seal(ctx, part, dump, start)
	if err != nil {
		os.Remove(part)
		return "", 0, "", copies, err
	}
	if err := os.Rename(part, final); err != nil {
		return "", 0, "", copies, err
	}
	copies["local"] = "ok"

	set, err := s.settings(ctx)
	if err != nil {
		return name, size, sum, copies, nil
	}
	if set.CopyDir != "" {
		if err := copyFile(final, filepath.Join(set.CopyDir, name)); err != nil {
			copies["dir"] = "gagal: " + err.Error()
		} else {
			copies["dir"] = "ok"
		}
	}
	if c := set.s3(); c != nil {
		f, err := os.Open(final)
		if err == nil {
			err = c.Put(ctx, name, f, size, sum)
			f.Close()
		}
		if err != nil {
			copies["s3"] = "gagal: " + err.Error()
		} else {
			copies["s3"] = "ok"
		}
	}
	return name, size, sum, copies, nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// dbArgs menyesuaikan perintah dan alamat basis data bila dijalankan lewat docker exec:
// di dalam container PostgreSQL mendengarkan di 127.0.0.1:5432.
func (s *Service) command(ctx context.Context, tool, dbURL string, args ...string) (*exec.Cmd, error) {
	if s.cfg.Docker == "" {
		return exec.CommandContext(ctx, tool, append([]string{"--dbname=" + dbURL}, args...)...), nil
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, err
	}
	u.Host = "127.0.0.1:5432"
	return exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", s.cfg.Docker, filepath.Base(tool), "--dbname=" + u.String()}, args...)...), nil
}

func (s *Service) pgDump(ctx context.Context, path string) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd, err := s.command(ctx, s.cfg.PgDump, s.cfg.DatabaseURL, "--format=custom", "--no-owner", "--no-privileges")
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump gagal: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Sync()
}

type manifest struct {
	Format         string    `json:"format"`
	CreatedAt      time.Time `json:"createdAt"`
	InstallationID string    `json:"installationId"`
	AppVersion     string    `json:"appVersion"`
	Files          int       `json:"files"`
}

// seal menulis arsip tar (manifest, dump, lampiran) yang langsung dienkripsi ke path.
func (s *Service) seal(ctx context.Context, path, dump string, created time.Time) (int64, string, error) {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, "", err
	}
	defer out.Close()
	h := sha256.New()
	counter := &countWriter{w: io.MultiWriter(out, h)}

	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(s.writeTar(ctx, pw, dump, created)) }()
	if err := Encrypt(counter, pr, s.cfg.PublicKey); err != nil {
		pr.CloseWithError(err)
		return 0, "", err
	}
	if err := out.Sync(); err != nil {
		return 0, "", err
	}
	return counter.n, hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Service) writeTar(ctx context.Context, w io.Writer, dump string, created time.Time) error {
	tw := tar.NewWriter(w)
	var files []string
	if s.cfg.FilesDir != "" {
		_ = filepath.WalkDir(s.cfg.FilesDir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				files = append(files, p)
			}
			return nil
		})
	}
	m, _ := json.MarshalIndent(manifest{Format: "lajur-backup/1", CreatedAt: created, InstallationID: s.cfg.InstallationID, AppVersion: s.cfg.Version, Files: len(files)}, "", "  ")
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(m)), ModTime: created}); err != nil {
		return err
	}
	if _, err := tw.Write(m); err != nil {
		return err
	}
	add := func(src, name string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime()}); err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return err
	}
	if err := add(dump, "db.dump"); err != nil {
		return err
	}
	for _, p := range files {
		rel, _ := filepath.Rel(s.cfg.FilesDir, p)
		if err := add(p, "files/"+filepath.ToSlash(rel)); err != nil {
			return err
		}
	}
	return tw.Close()
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst+".part", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".part", dst)
}

// ---------------------------------------------------------------- uji pulih

func (s *Service) restoreDue(ctx context.Context) bool {
	last, err := store.New(s.cfg.Pool).LastRestoreTest(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	return err == nil && s.cfg.Now().Sub(last.TestedAt) >= restoreEvery
}

// restoreTest memulihkan dump ke basis data sementara lalu memverifikasi rantai audit trail.
// Backup terenkripsi tidak bisa dibuka server (kunci privat hanya di tangan Pemilik), jadi
// yang diuji adalah dump yang sama yang baru saja dienkripsi.
func (s *Service) restoreTest(ctx context.Context, backupID uuid.UUID, dump string) {
	ok, detail := true, ""
	if err := s.restoreInto(ctx, dump); err != nil {
		ok, detail = false, err.Error()
	} else {
		ok, detail = s.verifyRestored(ctx)
	}
	if err := store.New(s.cfg.Pool).InsertRestoreTest(context.WithoutCancel(ctx), store.InsertRestoreTestParams{
		ID: uuid.Must(uuid.NewV7()), BackupID: &backupID, TestedAt: s.cfg.Now(), Ok: ok, Detail: detail,
	}); err != nil {
		s.cfg.Log.Error("mencatat uji pulih", "err", err)
	}
	_, _ = s.cfg.Pool.Exec(context.WithoutCancel(ctx), "DROP DATABASE IF EXISTS "+restoreDB+" WITH (FORCE)")
}

func (s *Service) restoreURL() (string, error) {
	u, err := url.Parse(s.cfg.DatabaseURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + restoreDB
	return u.String(), nil
}

func (s *Service) restoreInto(ctx context.Context, dump string) error {
	if _, err := s.cfg.Pool.Exec(ctx, "DROP DATABASE IF EXISTS "+restoreDB+" WITH (FORCE)"); err != nil {
		return err
	}
	if _, err := s.cfg.Pool.Exec(ctx, "CREATE DATABASE "+restoreDB); err != nil {
		return fmt.Errorf("membuat basis data uji: %w", err)
	}
	target, err := s.restoreURL()
	if err != nil {
		return err
	}
	return s.pgRestore(ctx, dump, target, false)
}

func (s *Service) pgRestore(ctx context.Context, dump, target string, clean bool) error {
	in, err := os.Open(dump)
	if err != nil {
		return err
	}
	defer in.Close()
	args := []string{"--no-owner", "--no-privileges", "--exit-on-error"}
	if clean {
		args = append(args, "--clean", "--if-exists")
	}
	cmd, err := s.command(ctx, s.cfg.PgRestore, target, args...)
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stdin, cmd.Stderr = in, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore gagal: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *Service) verifyRestored(ctx context.Context) (bool, string) {
	target, err := s.restoreURL()
	if err != nil {
		return false, err.Error()
	}
	pool, err := db.Open(ctx, target)
	if err != nil {
		return false, "tidak bisa membuka basis data uji: " + err.Error()
	}
	defer pool.Close()
	res, err := audit.Verify(ctx, pool)
	if err != nil {
		return false, "verifikasi audit trail: " + err.Error()
	}
	if !res.OK {
		return false, fmt.Sprintf("rantai audit trail hasil pulih rusak di baris %d", res.BrokenAt)
	}
	var companies int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM core.companies").Scan(&companies); err != nil {
		return false, err.Error()
	}
	return true, fmt.Sprintf("Pulih ke basis data sementara: %d PT, %d baris audit trail utuh.", companies, res.Count)
}

// ---------------------------------------------------------------- retensi, pengaturan, status

// prune menyimpan N backup sukses terbaru di setiap tujuan dan menghapus sisanya.
func (s *Service) prune(ctx context.Context) {
	set, err := s.settings(ctx)
	if err != nil {
		return
	}
	q := store.New(s.cfg.Pool)
	old, err := q.BackupsToPrune(ctx, int32(set.Keep))
	if err != nil {
		s.cfg.Log.Error("retensi backup", "err", err)
		return
	}
	for _, b := range old {
		if b.FileName != nil {
			_ = os.Remove(filepath.Join(s.cfg.Dir, *b.FileName))
		}
		_ = q.MarkPruned(ctx, b.ID)
	}
	if set.CopyDir != "" {
		pruneDir(set.CopyDir, set.Keep)
	}
	if c := set.s3(); c != nil {
		if objs, err := c.List(ctx); err == nil {
			var names []string
			for _, o := range objs {
				if strings.HasSuffix(o.Key, Ext) && !strings.Contains(o.Key, "/") {
					names = append(names, o.Key)
				}
			}
			sort.Sort(sort.Reverse(sort.StringSlice(names)))
			for i, n := range names {
				if i >= set.Keep {
					_ = c.Delete(ctx, n)
				}
			}
		}
	}
}

// pruneDir menghapus file backup Lajur lama di folder salinan; nama file berisi waktu
// sehingga urutan nama = urutan waktu.
func pruneDir(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "lajur-") && strings.HasSuffix(e.Name(), Ext) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for i, n := range names {
		if i >= keep {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
}

// Settings adalah pengaturan yang bisa diubah dari halaman Sistem.
type Settings struct {
	Hour        int    `json:"hour"`
	Keep        int    `json:"keep"`
	CopyDir     string `json:"copyDir"`
	S3Endpoint  string `json:"s3Endpoint"`
	S3Region    string `json:"s3Region"`
	S3Bucket    string `json:"s3Bucket"`
	S3Prefix    string `json:"s3Prefix"`
	S3AccessKey string `json:"s3AccessKey"`
	// S3Secret hanya dikirim saat mengganti; tidak pernah dikembalikan ke browser.
	S3Secret    string `json:"s3Secret,omitempty"`
	S3HasSecret bool   `json:"s3HasSecret"`
	Version     int    `json:"version"`
	secret      string
}

func (st Settings) s3() *S3 {
	if st.S3Endpoint == "" || st.S3Bucket == "" {
		return nil
	}
	return &S3{Endpoint: st.S3Endpoint, Region: st.S3Region, Bucket: st.S3Bucket, Prefix: st.S3Prefix, AccessKey: st.S3AccessKey, Secret: st.secret}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *Service) settings(ctx context.Context) (Settings, error) {
	row, err := store.New(db.Conn(ctx, s.cfg.Pool)).GetBackupSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	st := Settings{
		Hour: int(row.Hour), Keep: int(row.Keep), CopyDir: deref(row.CopyDir), S3Endpoint: deref(row.S3Endpoint), S3Region: deref(row.S3Region),
		S3Bucket: deref(row.S3Bucket), S3Prefix: deref(row.S3Prefix), S3AccessKey: deref(row.S3AccessKey), S3HasSecret: row.S3Secret != nil, Version: int(row.Version),
	}
	if row.S3Secret != nil && s.cfg.Box != nil {
		if plain, err := s.cfg.Box.Open(row.S3Secret, s3SecretPurpse); err == nil {
			st.secret = string(plain)
		}
	}
	return st, nil
}

func (s *Service) Settings(ctx context.Context) (Settings, error) { return s.settings(ctx) }

// SaveSettings memeriksa folder salinan bisa ditulisi dan kredensial S3 bisa dipakai
// sebelum menyimpan, supaya kesalahan ketahuan sekarang, bukan saat backup jam 01.00.
func (s *Service) SaveSettings(ctx context.Context, in Settings) (Settings, error) {
	fields := map[string]string{}
	if in.Hour < 0 || in.Hour > 23 {
		fields["hour"] = "Jam backup 0–23."
	}
	if in.Keep < 3 || in.Keep > 365 {
		fields["keep"] = "Simpan 3–365 salinan."
	}
	in.CopyDir = strings.TrimSpace(in.CopyDir)
	if in.CopyDir != "" {
		if !filepath.IsAbs(in.CopyDir) {
			fields["copyDir"] = "Gunakan alamat folder lengkap, mis. /mnt/nas/lajur."
		} else if err := probeDir(in.CopyDir); err != nil {
			fields["copyDir"] = "Folder tidak bisa ditulisi: " + err.Error()
		}
	}
	cur, err := s.settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	in.S3Endpoint, in.S3Bucket = strings.TrimRight(strings.TrimSpace(in.S3Endpoint), "/"), strings.TrimSpace(in.S3Bucket)
	in.secret = in.S3Secret
	if in.secret == "" && in.S3AccessKey == cur.S3AccessKey && in.S3Endpoint == cur.S3Endpoint {
		in.secret = cur.secret // secret lama tetap dipakai bila tidak diganti
	}
	if in.S3Prefix != "" && !strings.HasSuffix(in.S3Prefix, "/") {
		in.S3Prefix += "/"
	}
	if in.S3Endpoint != "" || in.S3Bucket != "" {
		switch {
		case !strings.HasPrefix(in.S3Endpoint, "https://"):
			fields["s3Endpoint"] = "Alamat S3 harus diawali https://."
		case in.S3Bucket == "":
			fields["s3Bucket"] = "Nama bucket wajib diisi."
		case in.S3AccessKey == "" || in.secret == "":
			fields["s3AccessKey"] = "Access key dan secret wajib diisi."
		default:
			if in.S3Region == "" {
				in.S3Region = "us-east-1"
			}
			if _, err := in.s3().List(ctx); err != nil {
				fields["s3Endpoint"] = err.Error()
			}
		}
	}
	if len(fields) > 0 {
		first := ""
		for _, k := range []string{"hour", "keep", "copyDir", "s3Endpoint", "s3Bucket", "s3AccessKey"} {
			if first == "" {
				first = fields[k]
			}
		}
		return Settings{}, httpx.Unprocessable("validation", first).WithFields(fields)
	}
	var sealed []byte
	if in.secret != "" && s.cfg.Box != nil {
		sealed = s.cfg.Box.Seal([]byte(in.secret), s3SecretPurpse)
	}
	opt := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	n, err := store.New(db.Conn(ctx, s.cfg.Pool)).SaveBackupSettings(ctx, store.SaveBackupSettingsParams{
		Version: int32(in.Version), Hour: int16(in.Hour), Keep: int32(in.Keep), CopyDir: opt(in.CopyDir),
		S3Endpoint: opt(in.S3Endpoint), S3Region: opt(in.S3Region), S3Bucket: opt(in.S3Bucket), S3Prefix: opt(in.S3Prefix),
		S3AccessKey: opt(in.S3AccessKey), S3Secret: sealed,
	})
	if err != nil {
		return Settings{}, err
	}
	if n != 1 {
		return Settings{}, httpx.Conflict("conflict.version", "Pengaturan backup baru saja diubah orang lain. Muat ulang lalu coba lagi.")
	}
	return s.settings(ctx)
}

func probeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".lajur-cek-")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// Status untuk halaman Sistem, banner, /healthz, dan check-in lisensi.
type Status struct {
	Configured  bool        `json:"configured"`
	KeyID       string      `json:"keyId,omitempty"`
	Running     bool        `json:"running"`
	LastOK      *time.Time  `json:"lastOk,omitempty"`
	LastFailed  *Record     `json:"lastFailed,omitempty"`
	Overdue     bool        `json:"overdue"`
	NextRun     time.Time   `json:"nextRun"`
	Timezone    string      `json:"timezone"`
	RestoreTest *RestoreRun `json:"restoreTest,omitempty"`
}

type RestoreRun struct {
	TestedAt time.Time `json:"testedAt"`
	OK       bool      `json:"ok"`
	Detail   string    `json:"detail"`
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	q := store.New(db.Conn(ctx, s.cfg.Pool))
	st := Status{Configured: s.Configured(), KeyID: s.KeyID(), Running: s.running.Load(), Timezone: s.cfg.Location.String()}
	now := s.cfg.Now()
	since := now
	if inst, err := q.GetInstallation(ctx); err == nil {
		since = inst.CreatedAt
	}
	if b, err := q.LastOKBackup(ctx); err == nil {
		st.LastOK, since = b.FinishedAt, *b.FinishedAt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return st, err
	}
	if b, err := q.LastFailedBackup(ctx); err == nil && (st.LastOK == nil || b.StartedAt.After(*st.LastOK)) {
		r := toRecord(b)
		st.LastFailed = &r
	}
	st.Overdue = now.Sub(since) > overdueAfter
	if t, err := q.LastRestoreTest(ctx); err == nil {
		st.RestoreTest = &RestoreRun{TestedAt: t.TestedAt, OK: t.Ok, Detail: t.Detail}
	}
	if set, err := s.settings(ctx); err == nil {
		st.NextRun = nextRun(now.In(s.cfg.Location), set.Hour)
	}
	return st, nil
}

func nextRun(now time.Time, hour int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func (s *Service) List(ctx context.Context, limit int) ([]Record, error) {
	rows, err := store.New(db.Conn(ctx, s.cfg.Pool)).ListBackups(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, toRecord(r))
	}
	return out, nil
}

// Open membuka file backup lokal untuk diunduh (tetap terenkripsi).
func (s *Service) Open(ctx context.Context, id uuid.UUID) (*os.File, Record, error) {
	row, err := store.New(s.cfg.Pool).GetBackup(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, Record{}, httpx.NotFound("backup.not_found", "Backup tidak ditemukan.")
	}
	if err != nil {
		return nil, Record{}, err
	}
	r := toRecord(row)
	if r.Status != "ok" || r.Pruned || r.FileName == "" {
		return nil, r, httpx.Conflict("backup.unavailable", "File backup ini sudah tidak ada di server.")
	}
	f, err := os.Open(filepath.Join(s.cfg.Dir, r.FileName))
	if err != nil {
		return nil, r, httpx.Conflict("backup.unavailable", "File backup tidak bisa dibuka: "+err.Error())
	}
	return f, r, nil
}

// Schedule menjalankan backup harian pada jam yang diatur (zona waktu Location). Backup yang
// gagal dicoba lagi sejam kemudian.
func (s *Service) Schedule(ctx context.Context) {
	_ = store.New(s.cfg.Pool).FailStaleBackups(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if s.Configured() && s.due(ctx) {
			if _, err := s.Run(ctx, "scheduled", nil); err != nil && ctx.Err() == nil {
				s.cfg.Log.Error("backup terjadwal gagal", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) due(ctx context.Context) bool {
	set, err := s.settings(ctx)
	if err != nil {
		return false
	}
	now := s.cfg.Now().In(s.cfg.Location)
	if now.Hour() < set.Hour {
		return false
	}
	last, err := store.New(s.cfg.Pool).LastScheduledBackup(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	if err != nil {
		return false
	}
	ls := last.StartedAt.In(s.cfg.Location)
	sameDay := ls.Year() == now.Year() && ls.YearDay() == now.YearDay() && ls.Hour() >= set.Hour
	if !sameDay {
		return true
	}
	return last.Status == "failed" && now.Sub(ls) >= time.Hour
}

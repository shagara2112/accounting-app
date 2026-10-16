package backup

import (
	"archive/tar"
	"context"
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Restore membuka file backup dengan kunci privat Pemilik lalu memulihkan basis data ke
// targetURL (objek lama dihapus dulu) dan lampiran ke filesDir (bila diisi). Aplikasi harus
// dihentikan selama pemulihan. progress dipanggil dengan pesan langkah untuk ditampilkan.
func (s *Service) Restore(ctx context.Context, file string, priv *ecdh.PrivateKey, targetURL, filesDir string, progress func(string)) error {
	in, err := os.Open(file)
	if err != nil {
		return err
	}
	defer in.Close()
	// Folder sementara di volume backup, bukan /tmp: dump bisa berukuran GB, sedangkan /tmp
	// di container adalah tmpfs (memori).
	if err := os.MkdirAll(s.cfg.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(s.cfg.Dir, ".restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	progress("Membuka file backup dengan kunci Pemilik…")
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(Decrypt(pw, in, priv)) }()
	tr := tar.NewReader(pr)
	var m manifest
	dump := filepath.Join(tmp, "db.dump")
	haveDump, files := false, 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("isi backup tidak bisa dibaca: %w", err)
		}
		name := path.Clean(h.Name)
		if strings.HasPrefix(name, "..") || path.IsAbs(name) {
			return fmt.Errorf("nama berkas tidak aman di backup: %q", h.Name)
		}
		switch {
		case name == "manifest.json":
			if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil {
				return fmt.Errorf("manifest backup rusak: %w", err)
			}
		case name == "db.dump":
			if err := writeFile(dump, tr); err != nil {
				return err
			}
			haveDump = true
		case strings.HasPrefix(name, "files/") && filesDir != "":
			if err := writeFile(filepath.Join(filesDir, filepath.FromSlash(strings.TrimPrefix(name, "files/"))), tr); err != nil {
				return err
			}
			files++
		}
	}
	if m.Format != "lajur-backup/1" || !haveDump {
		return errors.New("file ini bukan backup Lajur yang lengkap")
	}
	progress(fmt.Sprintf("Backup %s dari Lajur %s (instalasi %s); %d lampiran dipulihkan.", m.CreatedAt.Format("2006-01-02 15:04"), m.AppVersion, short(m.InstallationID), files))
	progress("Memulihkan basis data (objek lama dihapus dulu)…")
	return s.pgRestore(ctx, dump, targetURL, true)
}

func writeFile(dst string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

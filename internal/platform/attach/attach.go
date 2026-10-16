// Package attach menyimpan lampiran dokumen (PLT-08): PDF atau gambar nota, rekening koran,
// atau kontrak yang menempel di jurnal, faktur, tagihan, transaksi kas, dan dokumen lain.
//
// Izin mengikuti dokumennya: melihat lampiran butuh izin lihat modul itu, menambah butuh izin
// buat, dan menyembunyikan lampiran hanya bisa oleh pengunggahnya atau pemegang izin void.
// Berkas tidak pernah dihapus fisik supaya bukti transaksi tetap utuh untuk audit.
package attach

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/attach/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/rbac"
)

const MaxSize = 10 << 20

// kind memetakan jenis dokumen ke tabelnya dan izin lihat/buat/void modulnya.
type kind struct {
	table, view, create, void string
}

var kinds = map[string]kind{
	"journal":              {"ledger.journal_entries", rbac.LedgerView, rbac.JournalCreate, rbac.JournalVoid},
	"sales_invoice":        {"sales.invoices", rbac.SalesView, rbac.SalesCreate, rbac.SalesVoid},
	"sales_receipt":        {"sales.receipts", rbac.SalesView, rbac.SalesCreate, rbac.SalesVoid},
	"sales_credit_note":    {"sales.credit_notes", rbac.SalesView, rbac.SalesCreate, rbac.SalesVoid},
	"purchase_bill":        {"purchase.bills", rbac.PurchaseView, rbac.PurchaseCreate, rbac.PurchaseVoid},
	"purchase_payment":     {"purchase.payments", rbac.PurchaseView, rbac.PurchaseCreate, rbac.PurchaseVoid},
	"purchase_debit_note":  {"purchase.debit_notes", rbac.PurchaseView, rbac.PurchaseCreate, rbac.PurchaseVoid},
	"cash_transaction":     {"cash.transactions", rbac.CashView, rbac.CashCreate, rbac.CashVoid},
	"inventory_adjustment": {"inventory.adjustments", rbac.InventoryView, rbac.InventoryCreate, rbac.InventoryVoid},
	"project":              {"project.projects", rbac.ProjectView, rbac.ProjectCreate, rbac.ProjectVoid},
	"fixed_asset":          {"asset.assets", rbac.AssetView, rbac.AssetCreate, rbac.AssetVoid},
	"contact":              {"core.contacts", rbac.ContactView, rbac.ContactCreate, rbac.ContactEdit},
}

type Service struct {
	Pool     *pgxpool.Pool
	FilesDir string
	Now      func() time.Time
}

type Attachment struct {
	ID         uuid.UUID  `json:"id"`
	ObjectType string     `json:"objectType"`
	ObjectID   uuid.UUID  `json:"objectId"`
	FileName   string     `json:"fileName"`
	Mime       string     `json:"mime"`
	SizeBytes  int64      `json:"sizeBytes"`
	UploadedBy string     `json:"uploadedBy,omitempty"`
	UploaderID *uuid.UUID `json:"uploaderId,omitempty"`
	UploadedAt time.Time  `json:"uploadedAt"`
}

func (s *Service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// allow memeriksa izin peran di PT aktif untuk jenis dokumen dan aksi tertentu.
func allow(ctx context.Context, objectType string, pick func(kind) string) (kind, error) {
	k, ok := kinds[objectType]
	if !ok {
		return k, httpx.BadRequest("attachment.type", "Jenis dokumen tidak dikenal.")
	}
	scope, ok := rbac.ScopeFrom(ctx)
	if !ok || !scope.Role.Perms.Has(pick(k)) {
		return k, httpx.Forbidden("rbac.denied", "Peran Anda di PT ini tidak mengizinkan aksi ini.")
	}
	return k, nil
}

// exists memastikan dokumen tujuan memang milik PT aktif. Nama tabel berasal dari peta
// kinds di atas, bukan dari input pengguna.
func (s *Service) exists(ctx context.Context, k kind, companyID, objectID uuid.UUID) error {
	var ok bool
	if err := db.Conn(ctx, s.Pool).QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+k.table+" WHERE company_id = $1 AND id = $2)", companyID, objectID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return httpx.NotFound("attachment.object", "Dokumen tidak ditemukan di PT ini.")
	}
	return nil
}

func (s *Service) List(ctx context.Context, companyID uuid.UUID, objectType string, objectID uuid.UUID) ([]Attachment, error) {
	if _, err := allow(ctx, objectType, func(k kind) string { return k.view }); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListAttachments(ctx, store.ListAttachmentsParams{CompanyID: companyID, ObjectType: objectType, ObjectID: objectID})
	if err != nil {
		return nil, err
	}
	out := make([]Attachment, 0, len(rows))
	for _, r := range rows {
		a := Attachment{ID: r.ID, ObjectType: r.ObjectType, ObjectID: r.ObjectID, FileName: r.FileName, Mime: r.Mime, SizeBytes: r.SizeBytes, UploaderID: r.UploadedBy, UploadedAt: r.UploadedAt}
		if r.UploaderName != nil {
			a.UploadedBy = *r.UploaderName
		}
		out = append(out, a)
	}
	return out, nil
}

// detect menentukan jenis berkas dari isinya (bukan dari nama atau header browser). Hanya PDF,
// gambar, dan dokumen kantor yang diterima; SVG dan HTML ditolak karena bisa berisi skrip.
func detect(head []byte, name string) (string, error) {
	sniff := http.DetectContentType(head)
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case sniff == "application/pdf", sniff == "image/jpeg", sniff == "image/png", sniff == "image/webp", sniff == "image/gif":
		return sniff, nil
	case sniff == "application/zip" && ext == ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil
	case sniff == "application/zip" && ext == ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", nil
	case strings.HasPrefix(sniff, "text/plain") && ext == ".csv":
		return "text/csv; charset=utf-8", nil
	}
	return "", httpx.Unprocessable("attachment.file_type", "Lampiran harus PDF, gambar (JPG/PNG/WEBP/GIF), Excel (.xlsx), Word (.docx), atau CSV.")
}

func cleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '/' {
			return -1
		}
		return r
	}, name)
	for utf8.RuneCountInString(name) > 120 {
		ext := filepath.Ext(name)
		r := []rune(strings.TrimSuffix(name, ext))
		name = string(r[:len(r)-1]) + ext
	}
	if name == "" || name == "." {
		name = "lampiran"
	}
	return name
}

// Upload menyimpan berkas lalu mencatatnya. Berkas ditulis lewat berkas sementara dan
// rename, sehingga lampiran yang tercatat selalu utuh.
func (s *Service) Upload(ctx context.Context, companyID uuid.UUID, objectType string, objectID uuid.UUID, name string, r io.Reader, actor audit.Actor) (Attachment, error) {
	k, err := allow(ctx, objectType, func(k kind) string { return k.create })
	if err != nil {
		return Attachment{}, err
	}
	if err := s.exists(ctx, k, companyID, objectID); err != nil {
		return Attachment{}, err
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Attachment{}, err
	}
	if n == 0 {
		return Attachment{}, httpx.Unprocessable("attachment.empty", "Berkas kosong.")
	}
	name = cleanName(name)
	mimeType, err := detect(head[:n], name)
	if err != nil {
		return Attachment{}, err
	}

	id := uuid.Must(uuid.NewV7())
	rel := filepath.Join("attachments", companyID.String(), s.now().Format("2006-01"), id.String())
	dst := filepath.Join(s.FilesDir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return Attachment{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".up-")
	if err != nil {
		return Attachment{}, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), io.MultiReader(bytes.NewReader(head[:n]), io.LimitReader(r, MaxSize+1-int64(n))))
	if err != nil {
		tmp.Close()
		return Attachment{}, err
	}
	if size > MaxSize {
		tmp.Close()
		return Attachment{}, httpx.Unprocessable("attachment.too_large", fmt.Sprintf("Ukuran lampiran maksimal %d MB.", MaxSize>>20))
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Attachment{}, err
	}
	if err := tmp.Close(); err != nil {
		return Attachment{}, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return Attachment{}, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if err := s.q(ctx).InsertAttachment(ctx, store.InsertAttachmentParams{
		ID: id, CompanyID: companyID, ObjectType: objectType, ObjectID: objectID, FileName: name, Mime: mimeType,
		SizeBytes: size, Sha256: sum, StoragePath: rel, UploadedBy: actor.UserRef(),
	}); err != nil {
		os.Remove(dst)
		return Attachment{}, err
	}
	if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
		UserID: actor.UserRef(), CompanyID: &companyID, Action: "attachment.upload", ObjectType: objectType, ObjectID: objectID.String(), IP: actor.IP,
		After: map[string]any{"attachment": id, "file": name, "size": size, "sha256": sum},
	}); err != nil {
		return Attachment{}, err
	}
	return Attachment{ID: id, ObjectType: objectType, ObjectID: objectID, FileName: name, Mime: mimeType, SizeBytes: size, UploaderID: actor.UserRef(), UploadedAt: s.now()}, nil
}

// Open membuka berkas lampiran untuk diunduh, setelah memeriksa izin lihat dokumennya.
func (s *Service) Open(ctx context.Context, companyID, id uuid.UUID) (*os.File, store.PlatformAttachment, error) {
	a, err := s.q(ctx).GetAttachment(ctx, store.GetAttachmentParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, a, httpx.NotFound("attachment.not_found", "Lampiran tidak ditemukan.")
	}
	if err != nil {
		return nil, a, err
	}
	if _, err := allow(ctx, a.ObjectType, func(k kind) string { return k.view }); err != nil {
		return nil, a, err
	}
	f, err := os.Open(filepath.Join(s.FilesDir, a.StoragePath))
	if err != nil {
		return nil, a, httpx.Conflict("attachment.missing", "Berkas lampiran tidak ada di server; pulihkan dari backup.")
	}
	return f, a, nil
}

// Delete menyembunyikan lampiran (pengunggah atau pemegang izin void modulnya).
func (s *Service) Delete(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) error {
	q := s.q(ctx)
	a, err := q.GetAttachment(ctx, store.GetAttachmentParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound("attachment.not_found", "Lampiran tidak ditemukan.")
	}
	if err != nil {
		return err
	}
	own := a.UploadedBy != nil && *a.UploadedBy == actor.UserID
	if _, err := allow(ctx, a.ObjectType, func(k kind) string {
		if own {
			return k.create
		}
		return k.void
	}); err != nil {
		return httpx.Forbidden("attachment.delete_denied", "Lampiran ini hanya bisa dihapus pengunggahnya atau pemegang izin void.")
	}
	if _, err := q.DeleteAttachment(ctx, store.DeleteAttachmentParams{CompanyID: companyID, ID: id, DeletedBy: actor.UserRef()}); err != nil {
		return err
	}
	_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
		UserID: actor.UserRef(), CompanyID: &companyID, Action: "attachment.delete", ObjectType: a.ObjectType, ObjectID: a.ObjectID.String(), IP: actor.IP,
		Before: map[string]any{"attachment": a.ID, "file": a.FileName, "sha256": a.Sha256},
	})
	return err
}

// Disposition: PDF dan gambar dibuka langsung di browser, jenis lain diunduh.
func Disposition(a store.PlatformAttachment) string {
	kind := "attachment"
	if a.Mime == "application/pdf" || strings.HasPrefix(a.Mime, "image/") {
		kind = "inline"
	}
	return mime.FormatMediaType(kind, map[string]string{"filename": a.FileName})
}

// Package mail mengirim email lewat SMTP milik pelanggan (PLT-08): faktur ke pelanggan dan
// peringatan backup serta lisensi ke admin. Email masuk antrean (outbox) di transaksi yang
// sama dengan aksinya, lalu dikirim pekerja latar belakang dengan percobaan ulang.
package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/mail/internal/store"
	"lajur/internal/platform/secret"
)

const passwordPurpose = "smtp-password"

type Service struct {
	Pool *pgxpool.Pool
	Box  *secret.Box
	Now  func() time.Time
	Log  *slog.Logger
	// send dipalsukan di tes.
	send func(ctx context.Context, srv Server, m Message, now time.Time) error
}

func New(pool *pgxpool.Pool, box *secret.Box, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{Pool: pool, Box: box, Now: time.Now, Log: log, send: Send}
}

func (s *Service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

// Settings untuk halaman Sistem; password tidak pernah dikembalikan.
type Settings struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"hasPassword"`
	From        string `json:"from"`
	FromName    string `json:"fromName"`
	Version     int    `json:"version"`
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	r, err := s.q(ctx).GetSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		Enabled: r.Enabled, Host: r.Host, Port: int(r.Port), Security: r.Security, Username: r.Username,
		HasPassword: r.Password != nil, From: r.FromAddr, FromName: r.FromName, Version: int(r.Version),
	}, nil
}

// server membuka pengaturan untuk mengirim; error bila email belum diaktifkan.
func (s *Service) server(ctx context.Context) (Server, error) {
	r, err := s.q(ctx).GetSettings(ctx)
	if err != nil {
		return Server{}, err
	}
	if !r.Enabled || r.Host == "" || r.FromAddr == "" {
		return Server{}, ErrDisabled
	}
	srv := Server{Host: r.Host, Port: int(r.Port), Security: r.Security, Username: r.Username, From: r.FromAddr, FromName: r.FromName}
	if r.Password != nil {
		p, err := s.Box.Open(r.Password, passwordPurpose)
		if err != nil {
			return Server{}, err
		}
		srv.Password = string(p)
	}
	return srv, nil
}

// Ready menyatakan apakah email server sudah diatur dan aktif, mis. untuk kode lupa kata sandi.
func (s *Service) Ready(ctx context.Context) (bool, error) {
	_, err := s.server(ctx)
	if errors.Is(err, ErrDisabled) {
		return false, nil
	}
	return err == nil, err
}

var ErrDisabled = httpx.Conflict("mail.disabled", "Email belum diatur. Isi pengaturan SMTP di Sistem → Email.")

func (s *Service) SaveSettings(ctx context.Context, in Settings) (Settings, error) {
	in.Host, in.From, in.Username = strings.TrimSpace(in.Host), strings.TrimSpace(in.From), strings.TrimSpace(in.Username)
	fields := map[string]string{}
	if in.Enabled || in.Host != "" {
		if in.Host == "" {
			fields["host"] = "Alamat server SMTP wajib diisi."
		}
		if in.Port < 1 || in.Port > 65535 {
			fields["port"] = "Port 1–65535 (biasanya 587 atau 465)."
		}
		if a, err := mail.ParseAddress(in.From); err != nil || a.Address != in.From {
			fields["from"] = "Alamat pengirim tidak valid."
		}
	}
	switch in.Security {
	case "starttls", "tls", "none":
	default:
		fields["security"] = "Pilih STARTTLS, SSL/TLS, atau tanpa enkripsi."
	}
	if len(fields) > 0 {
		first := ""
		for _, k := range []string{"host", "port", "security", "from"} {
			if first == "" {
				first = fields[k]
			}
		}
		return Settings{}, httpx.Unprocessable("validation", first).WithFields(fields)
	}
	q := s.q(ctx)
	cur, err := q.GetSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	pw := cur.Password
	switch {
	case in.Password != "":
		pw = s.Box.Seal([]byte(in.Password), passwordPurpose)
	case in.Username == "":
		pw = nil
	}
	if in.FromName == "" {
		in.FromName = "Lajur"
	}
	n, err := q.SaveSettings(ctx, store.SaveSettingsParams{
		Version: int32(in.Version), Enabled: in.Enabled, Host: in.Host, Port: int32(in.Port), Security: in.Security,
		Username: in.Username, Password: pw, FromAddr: in.From, FromName: in.FromName,
	})
	if err != nil {
		return Settings{}, err
	}
	if n != 1 {
		return Settings{}, httpx.Conflict("conflict.version", "Pengaturan email baru saja diubah orang lain. Muat ulang lalu coba lagi.")
	}
	return s.Settings(ctx)
}

// Test mengirim email uji langsung (bukan lewat antrean) supaya kesalahan terlihat seketika.
func (s *Service) Test(ctx context.Context, to string) error {
	srv, err := s.server(ctx)
	if err != nil {
		return err
	}
	err = s.send(ctx, srv, Message{
		To: []string{to}, Subject: "Email uji dari Lajur",
		Text: "Pengaturan email Lajur sudah benar. Email faktur dan peringatan backup akan dikirim dari alamat ini.",
	}, s.Now())
	if err != nil {
		return httpx.NewProblem(502, "mail.test_failed", "Email uji gagal", err.Error())
	}
	return nil
}

// Queued adalah email yang dimasukkan ke antrean.
type Queued struct {
	CompanyID *uuid.UUID
	Kind      string
	// DedupeKey: email dengan kunci yang sama hanya diantrekan sekali (mis. peringatan harian).
	DedupeKey string
	CreatedBy *uuid.UUID
	Message
}

// Enqueue memasukkan email ke antrean di transaksi yang sedang berjalan. Mengembalikan false
// bila kunci dedupe sudah pernah dipakai.
func (s *Service) Enqueue(ctx context.Context, q Queued) (bool, error) {
	clean := func(list []string) ([]string, error) {
		var out []string
		for _, a := range list {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			p, err := mail.ParseAddress(a)
			if err != nil {
				return nil, httpx.Unprocessable("validation", fmt.Sprintf("Alamat email %q tidak valid.", a)).WithFields(map[string]string{"to": "Alamat email tidak valid."})
			}
			out = append(out, p.Address)
		}
		return out, nil
	}
	to, err := clean(q.To)
	if err != nil {
		return false, err
	}
	cc, err := clean(q.Cc)
	if err != nil {
		return false, err
	}
	if len(to) == 0 {
		return false, httpx.Unprocessable("validation", "Isi minimal satu penerima.").WithFields(map[string]string{"to": "Isi minimal satu penerima."})
	}
	if cc == nil {
		cc = []string{}
	}
	var dedupe, name, mimeType *string
	if q.DedupeKey != "" {
		dedupe = &q.DedupeKey
	}
	if len(q.Attachment) > 0 {
		name, mimeType = &q.AttachmentName, &q.AttachmentMime
	}
	n, err := s.q(ctx).Enqueue(ctx, store.EnqueueParams{
		ID: uuid.Must(uuid.NewV7()), CompanyID: q.CompanyID, Kind: q.Kind, DedupeKey: dedupe, ToAddrs: to, CcAddrs: cc,
		Subject: q.Subject, BodyText: q.Text, AttachmentName: name, AttachmentMime: mimeType, Attachment: q.Attachment, CreatedBy: q.CreatedBy,
	})
	return n == 1, err
}

// AlertAdmins mengantrekan peringatan ke semua pengguna pemegang izin sistem.
func (s *Service) AlertAdmins(ctx context.Context, kind, dedupe, subject, text string) error {
	to, err := s.q(ctx).AdminEmails(ctx)
	if err != nil || len(to) == 0 {
		return err
	}
	_, err = s.Enqueue(ctx, Queued{Kind: kind, DedupeKey: dedupe, Message: Message{To: to, Subject: subject, Text: text}})
	return err
}

type OutboxItem struct {
	ID         uuid.UUID  `json:"id"`
	Kind       string     `json:"kind"`
	To         []string   `json:"to"`
	Subject    string     `json:"subject"`
	Attachment *string    `json:"attachment,omitempty"`
	Status     string     `json:"status"`
	Attempts   int        `json:"attempts"`
	Error      *string    `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	SentAt     *time.Time `json:"sentAt,omitempty"`
}

func (s *Service) Outbox(ctx context.Context) ([]OutboxItem, error) {
	rows, err := s.q(ctx).ListOutbox(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]OutboxItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, OutboxItem{ID: r.ID, Kind: r.Kind, To: append(r.ToAddrs, r.CcAddrs...), Subject: r.Subject, Attachment: r.AttachmentName,
			Status: r.Status, Attempts: int(r.Attempts), Error: r.LastError, CreatedAt: r.CreatedAt, SentAt: r.SentAt})
	}
	return out, nil
}

// Flush mengirim email yang menunggu. Gagal kirim dicoba lagi dengan jeda yang makin
// panjang (1, 5, 15, 60 menit); setelah lima kali, email ditandai gagal.
func (s *Service) Flush(ctx context.Context) error {
	return db.InTx(ctx, s.Pool, pgx.TxOptions{}, func(ctx context.Context) error {
		q := s.q(ctx)
		items, err := q.ClaimQueued(ctx)
		if err != nil || len(items) == 0 {
			return err
		}
		srv, err := s.server(ctx)
		if errors.Is(err, ErrDisabled) {
			return nil // tetap menunggu sampai email diatur
		}
		if err != nil {
			return err
		}
		for _, it := range items {
			m := Message{To: it.ToAddrs, Cc: it.CcAddrs, Subject: it.Subject, Text: it.BodyText, Attachment: it.Attachment}
			if it.AttachmentName != nil {
				m.AttachmentName, m.AttachmentMime = *it.AttachmentName, deref(it.AttachmentMime)
			}
			if err := s.send(ctx, srv, m, s.Now()); err != nil {
				delays := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}
				d := delays[min(int(it.Attempts), len(delays)-1)]
				msg := err.Error()
				if err := q.MarkRetry(ctx, store.MarkRetryParams{ID: it.ID, LastError: &msg, NextAttemptAt: s.Now().Add(d)}); err != nil {
					return err
				}
				s.Log.Warn("email gagal dikirim", "id", it.ID, "err", err)
				continue
			}
			if err := q.MarkSent(ctx, it.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// Run mengirim antrean tiap 20 detik.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		if err := s.Flush(ctx); err != nil && ctx.Err() == nil {
			s.Log.Error("mengirim antrean email", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

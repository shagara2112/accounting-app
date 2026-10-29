package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/mail"
	"lajur/internal/platform/store"
)

// Lupa kata sandi dari halaman masuk. Server pelanggan bisa diakses lewat beberapa alamat (LAN,
// VPN), jadi yang dikirim adalah kode 6 digit, bukan tautan: tidak bergantung pada alamat server
// dan tidak bisa dibelokkan lewat header Host. Kode berlaku 15 menit, paling banyak 5 kali salah,
// sekali pakai, dan hanya hash-nya yang disimpan. Permintaan juga ditandai di pengguna supaya
// Admin bisa membuatkan kata sandi baru bila email server belum diatur.
const (
	resetTTL      = 15 * time.Minute
	resetAttempts = 5
	// resetPerWindow: paling banyak sekian kode per pengguna per resetTTL, supaya email tidak dibanjiri.
	resetPerWindow = 3
)

type forgotRequest struct {
	Email string `json:"email"`
}

// ForgotPassword selalu menjawab sama untuk email terdaftar atau tidak (tidak membocorkan akun);
// emailEnabled hanya menyatakan apakah email server sudah diatur.
func (h *Handlers) ForgotPassword(w http.ResponseWriter, r *http.Request) error {
	var req forgotRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	email := strings.TrimSpace(req.Email)
	if email == "" || !strings.Contains(email, "@") {
		msg := "Isi email akun Anda."
		return httpx.Unprocessable("validation", msg).WithFields(map[string]string{"email": msg})
	}
	ctx := r.Context()
	conn := db.Conn(ctx, h.Pool)
	q := store.New(conn)
	ready, err := h.mailReady(ctx)
	if err != nil {
		return err
	}
	out := map[string]any{"emailEnabled": ready}

	u, err := q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !u.Active) {
		return httpx.WriteJSON(w, http.StatusOK, out)
	}
	if err != nil {
		return err
	}
	now := h.Sessions.Now()
	n, err := q.CountRecentPasswordResets(ctx, store.CountRecentPasswordResetsParams{UserID: u.ID, CreatedAt: now.Add(-resetTTL)})
	if err != nil {
		return err
	}
	if n >= resetPerWindow {
		return httpx.WriteJSON(w, http.StatusOK, out)
	}
	if err := q.MarkResetRequested(ctx, u.ID); err != nil {
		return err
	}
	if ready {
		code, err := resetCode()
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		if err := q.InsertPasswordReset(ctx, store.InsertPasswordResetParams{
			ID: id, UserID: u.ID, CodeHash: hashResetCode(id, code), ExpiresAt: now.Add(resetTTL), CreatedIp: ClientIP(r),
		}); err != nil {
			return err
		}
		if _, err := h.Mail.Enqueue(ctx, mail.Queued{Kind: "password_reset", CreatedBy: &u.ID, Message: mail.Message{
			To:      []string{u.Email},
			Subject: "Kode reset kata sandi Lajur: " + code,
			Text: fmt.Sprintf("Halo %s,\n\nKode untuk membuat kata sandi baru di Lajur:\n\n    %s\n\n"+
				"Kode berlaku 15 menit dan hanya bisa dipakai sekali. Masukkan di halaman masuk Lajur → Lupa kata sandi.\n\n"+
				"Bila Anda tidak meminta reset kata sandi, abaikan email ini; kata sandi Anda tidak berubah.\n", u.Name, code),
		}}); err != nil {
			return err
		}
	}
	if _, err := audit.Write(ctx, conn, audit.Entry{
		UserID: &u.ID, Action: "auth.password_reset_requested", ObjectType: "user", ObjectID: u.ID.String(), IP: ClientIP(r),
		After: map[string]any{"emailSent": ready},
	}); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

type resetRequest struct {
	Email    string `json:"email"`
	Code     string `json:"code"`
	Password string `json:"password"`
}

func badResetCode(detail string) *httpx.Problem {
	return httpx.Unprocessable("auth.reset_invalid", detail).WithFields(map[string]string{"code": detail})
}

// ResetPassword mengganti kata sandi dengan kode dari email. Percobaan salah ditulis sebagai
// respons biasa (bukan error) supaya hitungannya tetap di-commit. Sesudah berhasil semua sesi
// pengguna diakhiri; 2FA tetap berlaku saat masuk berikutnya.
func (h *Handlers) ResetPassword(w http.ResponseWriter, r *http.Request) error {
	var req resetRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	conn := db.Conn(ctx, h.Pool)
	q := store.New(conn)
	code := strings.Join(strings.Fields(req.Code), "")

	hash, err := HashPassword(req.Password)
	if errors.Is(err, ErrWeakPassword) {
		return httpx.Unprocessable("validation", "Kata sandi baru minimal 10 karakter.").WithFields(map[string]string{"password": "Minimal 10 karakter."})
	}
	if err != nil {
		return err
	}
	invalid := "Kode salah atau sudah kedaluwarsa. Minta kode baru bila perlu."
	u, err := q.GetUserByEmail(ctx, strings.TrimSpace(req.Email))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !u.Active) {
		httpx.WriteProblem(w, badResetCode(invalid))
		return nil
	}
	if err != nil {
		return err
	}
	reset, err := q.LatestPasswordReset(ctx, u.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteProblem(w, badResetCode(invalid))
		return nil
	}
	if err != nil {
		return err
	}
	if h.Sessions.Now().After(reset.ExpiresAt) || reset.Attempts >= resetAttempts {
		httpx.WriteProblem(w, badResetCode("Kode sudah kedaluwarsa atau terlalu banyak percobaan. Minta kode baru."))
		return nil
	}
	if subtle.ConstantTimeCompare(hashResetCode(reset.ID, code), reset.CodeHash) != 1 {
		if err := q.BumpPasswordReset(ctx, reset.ID); err != nil {
			return err
		}
		left := resetAttempts - int(reset.Attempts) - 1
		msg := "Kode salah."
		if left > 0 {
			msg = fmt.Sprintf("Kode salah. Sisa %d percobaan.", left)
		}
		httpx.WriteProblem(w, badResetCode(msg))
		return nil
	}
	if err := q.SetPasswordFromReset(ctx, store.SetPasswordFromResetParams{ID: u.ID, PasswordHash: hash}); err != nil {
		return err
	}
	if err := q.UsePasswordResets(ctx, u.ID); err != nil {
		return err
	}
	if err := q.DeleteUserSessions(ctx, u.ID); err != nil {
		return err
	}
	if _, err := audit.Write(ctx, conn, audit.Entry{
		UserID: &u.ID, Action: "auth.password_reset", ObjectType: "user", ObjectID: u.ID.String(), IP: ClientIP(r),
	}); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handlers) mailReady(ctx context.Context) (bool, error) {
	if h.Mail == nil {
		return false, nil
	}
	return h.Mail.Ready(ctx)
}

// resetCode adalah 6 digit acak dari crypto/rand.
func resetCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func hashResetCode(id uuid.UUID, code string) []byte {
	h := sha256.New()
	h.Write(id[:])
	h.Write([]byte(code))
	return h.Sum(nil)
}

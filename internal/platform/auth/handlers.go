package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/secret"
	"lajur/internal/platform/store"
)

type Handlers struct {
	Pool        *pgxpool.Pool
	Sessions    *Sessions
	Access      rbac.Resolver
	MaxFailures int           // percobaan gagal sebelum akun dikunci
	LockFor     time.Duration // lama akun dikunci
	// Box mengenkripsi rahasia 2FA; MFARequired mewajibkan 2FA untuk peran istimewa.
	Box         *secret.Box
	MFARequired bool
}

// PrivilegedPerms: pemegang salah satu izin ini di PT mana pun wajib memakai 2FA
// (Pemilik, Manager, Admin pelanggan, dan persona kustom yang setara).
var PrivilegedPerms = []string{rbac.UserManage, rbac.SystemManage}

const (
	challengeTTL      = 5 * time.Minute
	challengeAttempts = 5
)

func totpPurpose(userID uuid.UUID) string { return "totp:" + userID.String() }

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func badCredentials() *httpx.Problem {
	return httpx.Unauthorized("auth.invalid_credentials", "Email atau kata sandi salah.")
}

// Login memeriksa kata sandi, mengunci akun setelah gagal berulang, lalu membuat sesi.
// Kegagalan ditulis sebagai respons biasa (bukan error) supaya hitungan gagal dan
// audit log tetap di-commit.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	conn := db.Conn(ctx, h.Pool)
	q := store.New(conn)
	ip := ClientIP(r)

	u, err := q.GetUserByEmail(ctx, strings.TrimSpace(req.Email))
	if errors.Is(err, pgx.ErrNoRows) {
		burnTime(req.Password)
		httpx.WriteProblem(w, badCredentials())
		return nil
	}
	if err != nil {
		return err
	}

	now := h.Sessions.Now()
	if u.LockedUntil != nil && now.Before(*u.LockedUntil) {
		p := httpx.NewProblem(http.StatusLocked, "auth.locked", "Akun terkunci sementara",
			"Terlalu banyak percobaan masuk yang gagal. Coba lagi setelah pukul "+
				u.LockedUntil.In(now.Location()).Format("15.04")+".")
		httpx.WriteProblem(w, p)
		return nil
	}

	ok, err := VerifyPassword(req.Password, u.PasswordHash)
	if err != nil {
		return err
	}
	if !ok || !u.Active {
		failures := u.FailedLogins + 1
		var lock *time.Time
		if int(failures) >= h.MaxFailures {
			t := now.Add(h.LockFor)
			lock, failures = &t, 0
		}
		if err := q.SetLoginFailures(ctx, store.SetLoginFailuresParams{ID: u.ID, FailedLogins: failures, LockedUntil: lock}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, conn, audit.Entry{
			UserID: &u.ID, Action: "auth.login_failed", ObjectType: "user", ObjectID: u.ID.String(), IP: ip,
		}); err != nil {
			return err
		}
		httpx.WriteProblem(w, badCredentials())
		return nil
	}

	if err := q.SetLoginFailures(ctx, store.SetLoginFailuresParams{ID: u.ID}); err != nil {
		return err
	}
	// Pengguna dengan 2FA aktif mendapat challenge; sesi baru dibuat setelah kodenya benar.
	if u.TotpEnabledAt != nil {
		token := newToken()
		if err := q.InsertLoginChallenge(ctx, store.InsertLoginChallengeParams{
			IDHash: hashToken(token), UserID: u.ID, Ip: &ip, ExpiresAt: now.Add(challengeTTL),
		}); err != nil {
			return err
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"totpRequired": true, "challenge": token})
	}
	mfaSetup, err := h.mfaRequiredFor(ctx, q, u.ID)
	if err != nil {
		return err
	}
	return h.StartSession(w, r, u.ID, mfaSetup, "password")
}

// StartSession membuat sesi, mencatat login, dan memasang cookie. Dipakai login biasa,
// login 2FA, dan setup wizard.
func (h *Handlers) StartSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID, mfaSetup bool, method string) error {
	ctx := r.Context()
	conn := db.Conn(ctx, h.Pool)
	ip := ClientIP(r)
	company, err := h.Access.DefaultCompany(ctx, userID)
	if err != nil {
		return err
	}
	token, exp, err := h.Sessions.Create(ctx, userID, company, ip, r.UserAgent(), mfaSetup)
	if err != nil {
		return err
	}
	if err := store.New(conn).SetLastLogin(ctx, userID); err != nil {
		return err
	}
	if _, err := audit.Write(ctx, conn, audit.Entry{
		UserID: &userID, CompanyID: company, Action: "auth.login", ObjectType: "user", ObjectID: userID.String(), IP: ip,
		After: map[string]any{"method": method},
	}); err != nil {
		return err
	}
	h.Sessions.SetCookie(w, token, exp)
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "mfaSetupRequired": mfaSetup})
}

// mfaRequiredFor: pengguna istimewa tanpa 2FA aktif wajib memasangnya dulu.
func (h *Handlers) mfaRequiredFor(ctx context.Context, q *store.Queries, userID uuid.UUID) (bool, error) {
	if !h.MFARequired {
		return false, nil
	}
	return q.UserPrivileged(ctx, store.UserPrivilegedParams{UserID: userID, Perms: PrivilegedPerms})
}

type totpLoginRequest struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
}

// LoginTOTP menyelesaikan login tahap kedua dengan kode autentikator.
func (h *Handlers) LoginTOTP(w http.ResponseWriter, r *http.Request) error {
	var req totpLoginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	conn := db.Conn(ctx, h.Pool)
	q := store.New(conn)
	expired := httpx.Unauthorized("auth.challenge_expired", "Waktu memasukkan kode habis. Masukkan kata sandi lagi.")

	ch, err := q.GetLoginChallenge(ctx, hashToken(req.Challenge))
	if errors.Is(err, pgx.ErrNoRows) {
		return expired
	}
	if err != nil {
		return err
	}
	now := h.Sessions.Now()
	if now.After(ch.ExpiresAt) {
		if err := q.DeleteLoginChallenge(ctx, ch.IDHash); err != nil {
			return err
		}
		httpx.WriteProblem(w, expired)
		return nil
	}
	u, err := q.GetUserByID(ctx, ch.UserID)
	if err != nil {
		return err
	}
	ok := false
	if u.Active && u.TotpEnabledAt != nil {
		key, err := h.Box.Open(u.TotpSecret, totpPurpose(u.ID))
		if err != nil {
			return err
		}
		if step, match := VerifyTOTP(key, req.Code, now, u.TotpLastStep); match {
			n, err := q.UseTOTPStep(ctx, store.UseTOTPStepParams{ID: u.ID, TotpLastStep: step})
			if err != nil {
				return err
			}
			ok = n == 1
		}
	}
	if !ok {
		if ch.Attempts+1 >= challengeAttempts {
			err = q.DeleteLoginChallenge(ctx, ch.IDHash)
		} else {
			err = q.BumpLoginChallenge(ctx, ch.IDHash)
		}
		if err != nil {
			return err
		}
		if _, err := audit.Write(ctx, conn, audit.Entry{
			UserID: &u.ID, Action: "auth.totp_failed", ObjectType: "user", ObjectID: u.ID.String(), IP: ClientIP(r),
		}); err != nil {
			return err
		}
		if ch.Attempts+1 >= challengeAttempts {
			httpx.WriteProblem(w, httpx.Unauthorized("auth.challenge_expired", "Terlalu banyak kode salah. Masukkan kata sandi lagi."))
		} else {
			httpx.WriteProblem(w, httpx.Unauthorized("auth.invalid_code", "Kode autentikator salah atau sudah dipakai."))
		}
		return nil
	}
	if err := q.DeleteLoginChallenge(ctx, ch.IDHash); err != nil {
		return err
	}
	return h.StartSession(w, r, u.ID, false, "totp")
}

// MFAStatus menjawab status 2FA pengguna sesi untuk halaman profil.
func (h *Handlers) MFAStatus(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	sess, _ := SessionFrom(ctx)
	q := store.New(db.Conn(ctx, h.Pool))
	u, err := q.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return err
	}
	required, err := h.mfaRequiredFor(ctx, q, u.ID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled": u.TotpEnabledAt != nil, "enabledAt": u.TotpEnabledAt, "required": required, "setupPending": sess.MFASetup,
	})
}

// TOTPSetup membuat rahasia 2FA baru (belum aktif) dan mengembalikan kunci untuk aplikasi autentikator.
func (h *Handlers) TOTPSetup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	sess, _ := SessionFrom(ctx)
	q := store.New(db.Conn(ctx, h.Pool))
	u, err := q.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return err
	}
	if u.TotpEnabledAt != nil {
		return httpx.Conflict("auth.totp_active", "2FA sudah aktif. Matikan dulu bila ingin memakai perangkat baru.")
	}
	key := NewTOTPSecret()
	if err := q.SetPendingTOTP(ctx, store.SetPendingTOTPParams{ID: u.ID, TotpSecret: h.Box.Seal(key, totpPurpose(u.ID))}); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"key": TOTPKey(key), "uri": TOTPURI("Lajur", u.Email, key)})
}

type codeRequest struct {
	Code string `json:"code"`
}

// TOTPEnable mengaktifkan 2FA setelah kode pertama dari aplikasi autentikator cocok.
func (h *Handlers) TOTPEnable(w http.ResponseWriter, r *http.Request) error {
	var req codeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	sess, _ := SessionFrom(ctx)
	conn := db.Conn(ctx, h.Pool)
	q := store.New(conn)
	u, err := q.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return err
	}
	if u.TotpSecret == nil || u.TotpEnabledAt != nil {
		return httpx.Conflict("auth.totp_not_pending", "Buat kunci 2FA baru dulu.")
	}
	key, err := h.Box.Open(u.TotpSecret, totpPurpose(u.ID))
	if err != nil {
		return err
	}
	step, ok := VerifyTOTP(key, req.Code, h.Sessions.Now(), 0)
	if !ok {
		return httpx.Unprocessable("validation", "Kode tidak cocok.").
			WithFields(map[string]string{"code": "Kode tidak cocok. Pastikan jam ponsel tepat, lalu masukkan kode terbaru."})
	}
	if _, err := q.EnableTOTP(ctx, store.EnableTOTPParams{ID: u.ID, TotpLastStep: step}); err != nil {
		return err
	}
	if err := q.ClearSessionMFASetup(ctx, u.ID); err != nil {
		return err
	}
	if _, err := audit.Write(ctx, conn, audit.Entry{
		UserID: &u.ID, Action: "auth.totp_enabled", ObjectType: "user", ObjectID: u.ID.String(), IP: ClientIP(r),
	}); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type passwordConfirm struct {
	Password string `json:"password"`
}

// TOTPDisable mematikan 2FA milik sendiri; tidak boleh bagi peran yang wajib 2FA.
func (h *Handlers) TOTPDisable(w http.ResponseWriter, r *http.Request) error {
	var req passwordConfirm
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	sess, _ := SessionFrom(ctx)
	conn := db.Conn(ctx, h.Pool)
	q := store.New(conn)
	u, err := q.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return err
	}
	required, err := h.mfaRequiredFor(ctx, q, u.ID)
	if err != nil {
		return err
	}
	if required {
		return httpx.Forbidden("auth.mfa_required", "Peran Anda wajib memakai 2FA, jadi 2FA tidak bisa dimatikan.")
	}
	if ok, err := VerifyPassword(req.Password, u.PasswordHash); err != nil {
		return err
	} else if !ok {
		return httpx.Unprocessable("validation", "Kata sandi salah.").WithFields(map[string]string{"password": "Kata sandi salah."})
	}
	if err := q.ClearTOTP(ctx, u.ID); err != nil {
		return err
	}
	_, err = audit.Write(ctx, conn, audit.Entry{
		UserID: &u.ID, Action: "auth.totp_disabled", ObjectType: "user", ObjectID: u.ID.String(), IP: ClientIP(r),
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type passwordChange struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// ChangePassword mengganti kata sandi sendiri dan mengakhiri sesi lain milik pengguna itu.
func (h *Handlers) ChangePassword(w http.ResponseWriter, r *http.Request) error {
	var req passwordChange
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	sess, _ := SessionFrom(ctx)
	conn := db.Conn(ctx, h.Pool)
	q := store.New(conn)
	u, err := q.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return err
	}
	if ok, err := VerifyPassword(req.Current, u.PasswordHash); err != nil {
		return err
	} else if !ok {
		return httpx.Unprocessable("validation", "Kata sandi lama salah.").WithFields(map[string]string{"current": "Kata sandi lama salah."})
	}
	hash, err := HashPassword(req.New)
	if errors.Is(err, ErrWeakPassword) {
		return httpx.Unprocessable("validation", "Kata sandi baru minimal 10 karakter.").WithFields(map[string]string{"new": "Minimal 10 karakter."})
	}
	if err != nil {
		return err
	}
	if err := q.SetPassword(ctx, store.SetPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
		return err
	}
	if err := q.DeleteOtherSessions(ctx, store.DeleteOtherSessionsParams{UserID: u.ID, IDHash: sess.TokenHash}); err != nil {
		return err
	}
	if _, err := audit.Write(ctx, conn, audit.Entry{
		UserID: &u.ID, Action: "auth.password_changed", ObjectType: "user", ObjectID: u.ID.String(), IP: ClientIP(r),
	}); err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	sess, _ := SessionFrom(ctx)
	conn := db.Conn(ctx, h.Pool)
	if err := store.New(conn).DeleteSession(ctx, sess.TokenHash); err != nil {
		return err
	}
	if _, err := audit.Write(ctx, conn, audit.Entry{
		UserID: &sess.UserID, CompanyID: sess.ActiveCompanyID, Action: "auth.logout",
		ObjectType: "user", ObjectID: sess.UserID.String(), IP: ClientIP(r),
	}); err != nil {
		return err
	}
	h.Sessions.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ActorFrom mengembalikan pengguna sesi dan alamat IP request untuk audit trail.
func ActorFrom(r *http.Request) audit.Actor {
	a := audit.Actor{IP: ClientIP(r)}
	if sess, ok := SessionFrom(r.Context()); ok {
		a.UserID = sess.UserID
	}
	return a
}

// ClientIP memakai alamat koneksi langsung; Lajur berjalan di LAN tanpa reverse proxy.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

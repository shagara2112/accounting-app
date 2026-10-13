package auth

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/store"
)

type Handlers struct {
	Pool        *pgxpool.Pool
	Sessions    *Sessions
	Access      rbac.Resolver
	MaxFailures int           // percobaan gagal sebelum akun dikunci
	LockFor     time.Duration // lama akun dikunci
}

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
	company, err := h.Access.DefaultCompany(ctx, u.ID)
	if err != nil {
		return err
	}
	token, exp, err := h.Sessions.Create(ctx, u.ID, company, ip, r.UserAgent())
	if err != nil {
		return err
	}
	if _, err := audit.Write(ctx, conn, audit.Entry{
		UserID: &u.ID, CompanyID: company, Action: "auth.login", ObjectType: "user", ObjectID: u.ID.String(), IP: ip,
	}); err != nil {
		return err
	}
	h.Sessions.SetCookie(w, token, exp)
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

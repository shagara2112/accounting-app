package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/db"
	"lajur/internal/platform/store"
)

const CookieName = "lajur_session"

var ErrNoSession = errors.New("sesi tidak ada atau sudah berakhir")

type Session struct {
	TokenHash       []byte
	UserID          uuid.UUID
	Email           string
	Name            string
	ActiveCompanyID *uuid.UUID
	ExpiresAt       time.Time
}

// Sessions mengelola sesi di server: berakhir setelah 30 menit tidak aktif atau 12 jam sejak login.
type Sessions struct {
	pool     *pgxpool.Pool
	Idle     time.Duration
	Absolute time.Duration
	Secure   bool
	Now      func() time.Time
}

func NewSessions(pool *pgxpool.Pool, secure bool) *Sessions {
	return &Sessions{pool: pool, Idle: 30 * time.Minute, Absolute: 12 * time.Hour, Secure: secure, Now: time.Now}
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Create membuat sesi baru di transaksi yang sedang berjalan dan mengembalikan tokennya.
func (s *Sessions) Create(ctx context.Context, userID uuid.UUID, company *uuid.UUID, ip, userAgent string) (string, time.Time, error) {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	exp := s.Now().Add(s.Absolute)
	err := store.New(db.Conn(ctx, s.pool)).CreateSession(ctx, store.CreateSessionParams{
		IDHash: hashToken(token), UserID: userID, ActiveCompanyID: company,
		ExpiresAt: exp, Ip: &ip, UserAgent: &userAgent,
	})
	return token, exp, err
}

// FromRequest membaca dan memvalidasi sesi dari cookie.
func (s *Sessions) FromRequest(ctx context.Context, r *http.Request) (*Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, ErrNoSession
	}
	h := hashToken(c.Value)
	q := store.New(s.pool)
	row, err := q.GetSession(ctx, h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	now := s.Now()
	if !row.Active || now.After(row.ExpiresAt) || now.Sub(row.LastSeenAt) > s.Idle {
		_ = q.DeleteSession(ctx, h)
		return nil, ErrNoSession
	}
	if now.Sub(row.LastSeenAt) > time.Minute {
		_ = q.TouchSession(ctx, h)
	}
	return &Session{
		TokenHash: h, UserID: row.UserID, Email: row.Email, Name: row.Name,
		ActiveCompanyID: row.ActiveCompanyID, ExpiresAt: row.ExpiresAt,
	}, nil
}

func (s *Sessions) SetCompany(ctx context.Context, sess *Session, company uuid.UUID) error {
	return store.New(db.Conn(ctx, s.pool)).SetSessionCompany(ctx, store.SetSessionCompanyParams{
		IDHash: sess.TokenHash, ActiveCompanyID: &company,
	})
}

func (s *Sessions) SetCookie(w http.ResponseWriter, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Expires: exp,
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Sessions) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteStrictMode,
	})
}

type sessionKey struct{}

func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

func SessionFrom(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionKey{}).(*Session)
	return s, ok
}

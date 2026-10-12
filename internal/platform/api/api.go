// Package api mendaftarkan route dan membungkus setiap handler dengan pemeriksaan
// lisensi, sesi, izin per PT, dan satu transaksi basis data per request.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/auth"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/store"
)

type Access int

const (
	Public        Access = iota + 1 // tanpa sesi; hanya untuk login
	Authenticated                   // butuh sesi, tanpa izin khusus
	Permission                      // butuh sesi dan izin di PT aktif
)

// Route mendeskripsikan satu endpoint. Access dan License wajib diisi;
// Perm wajib bila Access = Permission.
type Route struct {
	Method  string
	Path    string
	Access  Access
	Perm    string
	License license.Mode
	Handle  httpx.Handler
	// Idempotent: POST dokumen keuangan. Wajib header Idempotency-Key; pengiriman ulang
	// dengan kunci yang sama mendapat respons pertama, bukan dokumen kedua.
	Idempotent bool
}

type Deps struct {
	Pool     *pgxpool.Pool
	Sessions *auth.Sessions
	Access   rbac.Resolver
	License  license.Provider
	Log      *slog.Logger
}

type API struct {
	mux    chi.Router
	deps   Deps
	routes []Route
}

func New(mux chi.Router, deps Deps) *API { return &API{mux: mux, deps: deps} }

// Add mendaftarkan route. Route yang tidak lengkap membuat aplikasi gagal start,
// sehingga endpoint tanpa license guard atau izin tidak pernah lolos ke produksi.
func (a *API) Add(rt Route) {
	if rt.Access == 0 || rt.License == license.ModeUnset {
		panic(fmt.Sprintf("route %s %s wajib menyatakan Access dan License", rt.Method, rt.Path))
	}
	if rt.Access == Permission && rt.Perm == "" {
		panic(fmt.Sprintf("route %s %s wajib menyatakan izin", rt.Method, rt.Path))
	}
	if rt.Idempotent && (rt.Method != http.MethodPost || rt.Access == Public) {
		panic(fmt.Sprintf("route %s %s: idempotensi hanya untuk POST yang butuh sesi", rt.Method, rt.Path))
	}
	a.routes = append(a.routes, rt)
	a.mux.Method(rt.Method, rt.Path, a.wrap(rt))
}

// Routes mengembalikan semua route yang terdaftar; dipakai tes untuk memeriksa kelengkapan.
func (a *API) Routes() []Route { return a.routes }

func (a *API) wrap(rt Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.serve(w, r, rt); err != nil {
			httpx.WriteError(w, r, a.deps.Log, err)
		}
	})
}

func (a *API) serve(w http.ResponseWriter, r *http.Request, rt Route) error {
	ctx := r.Context()
	if r.Method != http.MethodGet && !sameOrigin(r) {
		return httpx.Forbidden("request.origin", "Permintaan dari situs lain ditolak.")
	}

	st := a.deps.License.Status(ctx)
	if !st.Allows(rt.License) {
		code := "license.read_only"
		if st.State == license.Suspended || st.State == license.InvalidKey {
			code = "license.locked"
		}
		return httpx.Forbidden(code, st.Message)
	}

	if rt.Access != Public {
		sess, err := a.deps.Sessions.FromRequest(ctx, r)
		if errors.Is(err, auth.ErrNoSession) {
			return httpx.Unauthorized("auth.required", "Silakan masuk terlebih dahulu.")
		}
		if err != nil {
			return err
		}
		ctx = auth.WithSession(ctx, sess)

		if rt.Access == Permission {
			scope, err := a.scope(r, sess)
			if err != nil {
				return err
			}
			if !scope.Role.Perms.Has(rt.Perm) {
				return httpx.Forbidden("rbac.denied", "Peran Anda di PT ini tidak mengizinkan aksi ini.")
			}
			ctx = rbac.WithScope(ctx, scope)
		}
	}

	opts := pgx.TxOptions{}
	if r.Method == http.MethodGet {
		opts.AccessMode = pgx.ReadOnly
	}
	buf := httpx.NewBufferedWriter()
	if err := db.InTx(ctx, a.deps.Pool, opts, func(ctx context.Context) error {
		if rt.Idempotent {
			return a.idempotent(buf, r.WithContext(ctx), rt)
		}
		return rt.Handle(buf, r.WithContext(ctx))
	}); err != nil {
		return err
	}
	buf.FlushTo(w)
	return nil
}

// idempotent mengklaim Idempotency-Key di transaksi request yang sama dengan dokumennya.
// Request kedua dengan kunci yang sama menunggu request pertama selesai, lalu mendapat
// respons yang tersimpan. Bila request pertama gagal, kuncinya ikut di-rollback.
func (a *API) idempotent(w *httpx.BufferedWriter, r *http.Request, rt Route) error {
	ctx := r.Context()
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 100 {
		return httpx.BadRequest("idempotency.key_required", "Header Idempotency-Key wajib diisi (8–100 karakter).")
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return httpx.BadRequest("request.body", "Body request tidak bisa dibaca.")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))

	sess, _ := auth.SessionFrom(ctx)
	q := store.New(db.Conn(ctx, a.deps.Pool))
	_, err = q.ClaimIdempotencyKey(ctx, store.ClaimIdempotencyKeyParams{UserID: sess.UserID, Key: key, RequestHash: sum[:]})
	if errors.Is(err, pgx.ErrNoRows) {
		prev, err := q.GetIdempotencyKey(ctx, store.GetIdempotencyKeyParams{UserID: sess.UserID, Key: key})
		if err != nil {
			return err
		}
		if !bytes.Equal(prev.RequestHash, sum[:]) || prev.Status == nil {
			return httpx.Unprocessable("idempotency.mismatch", "Idempotency-Key ini sudah dipakai untuk permintaan lain.")
		}
		if prev.ContentType != nil {
			w.Header().Set("Content-Type", *prev.ContentType)
		}
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(int(*prev.Status))
		_, err = w.Write(prev.Body)
		return err
	}
	if err != nil {
		return err
	}

	if err := rt.Handle(w, r); err != nil {
		return err
	}
	status := int32(w.Status())
	ct := w.Header().Get("Content-Type")
	return q.SaveIdempotentResponse(ctx, store.SaveIdempotentResponseParams{
		UserID: sess.UserID, Key: key, Status: &status, ContentType: &ct, Body: w.Bytes(),
	})
}

// scope menentukan PT aktif: header X-Company-Id bila ada, selain itu PT yang tersimpan di sesi.
func (a *API) scope(r *http.Request, sess *auth.Session) (rbac.Scope, error) {
	var company uuid.UUID
	if h := r.Header.Get("X-Company-Id"); h != "" {
		id, err := uuid.Parse(h)
		if err != nil {
			return rbac.Scope{}, httpx.BadRequest("company.invalid", "Header X-Company-Id tidak valid.")
		}
		company = id
	} else if sess.ActiveCompanyID != nil {
		company = *sess.ActiveCompanyID
	} else {
		return rbac.Scope{}, httpx.BadRequest("company.required", "Pilih PT aktif terlebih dahulu.")
	}
	role, err := a.deps.Access.Role(r.Context(), sess.UserID, company)
	if errors.Is(err, rbac.ErrNoAccess) {
		return rbac.Scope{}, httpx.Forbidden("company.denied", "Anda tidak punya akses ke PT ini.")
	}
	if err != nil {
		return rbac.Scope{}, err
	}
	return rbac.Scope{CompanyID: company, Role: role}, nil
}

// sameOrigin menolak request pengubah data dari situs lain (lapisan kedua setelah cookie SameSite=Strict).
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

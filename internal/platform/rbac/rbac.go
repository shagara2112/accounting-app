// Package rbac berisi katalog izin, peran bawaan, dan pemeriksaan hak akses per PT.
package rbac

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"lajur/internal/platform/db"
	"lajur/internal/platform/store"
)

var ErrNoAccess = errors.New("pengguna tidak punya akses ke PT ini")

type Perms map[string]struct{}

func NewPerms(list []string) Perms {
	p := make(Perms, len(list))
	for _, v := range list {
		p[v] = struct{}{}
	}
	return p
}

func (p Perms) Has(perm string) bool {
	_, ok := p[perm]
	return ok
}

type Role struct {
	Key   string
	Perms Perms
}

// Resolver menjawab peran pengguna di sebuah PT. Diimplementasikan modul core,
// supaya platform tidak bergantung pada modul (arah dependensi modul → platform).
type Resolver interface {
	Role(ctx context.Context, userID, companyID uuid.UUID) (Role, error)
	DefaultCompany(ctx context.Context, userID uuid.UUID) (*uuid.UUID, error)
}

// Scope adalah PT aktif dan peran pengguna di PT itu untuk request yang sedang berjalan.
type Scope struct {
	CompanyID uuid.UUID
	Role      Role
}

type scopeKey struct{}

func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

func ScopeFrom(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(scopeKey{}).(Scope)
	return s, ok
}

// SyncBuiltinRoles menyamakan peran bawaan di basis data dengan katalog di kode.
func SyncBuiltinRoles(ctx context.Context, q db.Querier) error {
	s := store.New(q)
	for _, r := range Builtin {
		err := s.UpsertBuiltinRole(ctx, store.UpsertBuiltinRoleParams{
			ID: uuid.Must(uuid.NewV7()), Key: r.Key, Name: r.Name, Permissions: r.Permissions,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

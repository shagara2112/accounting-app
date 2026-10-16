// Package rbac berisi katalog izin, peran bawaan, dan pemeriksaan hak akses per PT.
package rbac

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
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
	// Branches membatasi pengguna ke cabang tertentu di PT ini (PLT-12); kosong = semua cabang.
	Branches []uuid.UUID
}

// Restricted: akses pengguna dibatasi ke cabang tertentu.
func (r Role) Restricted() bool { return len(r.Branches) > 0 }

// BranchAllowed: dokumen bercabang branch boleh dilihat atau dibuat pengguna ini. Pengguna yang
// dibatasi hanya boleh dokumen dengan cabang yang diizinkan (bukan dokumen tanpa cabang).
func (r Role) BranchAllowed(branch *uuid.UUID) bool {
	if !r.Restricted() {
		return true
	}
	if branch == nil {
		return false
	}
	for _, b := range r.Branches {
		if b == *branch {
			return true
		}
	}
	return false
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

// BranchesFor mengembalikan batasan cabang pengguna request ini untuk PT companyID (PLT-12).
// Kosong berarti tidak dibatasi: pengguna bebas cabang, atau service dipanggil untuk PT lain
// (mis. dokumen cerminan antar-PT) atau oleh proses latar belakang.
func BranchesFor(ctx context.Context, companyID uuid.UUID) []uuid.UUID {
	s, ok := ScopeFrom(ctx)
	if !ok || s.CompanyID != companyID {
		return []uuid.UUID{}
	}
	if s.Role.Branches == nil {
		return []uuid.UUID{}
	}
	return s.Role.Branches
}

// CheckBranch menolak dokumen di luar cabang yang diizinkan untuk pengguna request ini.
func CheckBranch(ctx context.Context, companyID uuid.UUID, branch *uuid.UUID) error {
	s, ok := ScopeFrom(ctx)
	if !ok || s.CompanyID != companyID || s.Role.BranchAllowed(branch) {
		return nil
	}
	msg := "Akses Anda dibatasi ke cabang tertentu; pilih salah satu cabang Anda."
	return httpx.Forbidden("branch.restricted", msg).WithFields(map[string]string{"branchId": msg})
}

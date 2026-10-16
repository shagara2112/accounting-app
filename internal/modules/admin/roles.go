package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/modules/admin/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/rbac"
)

type Role struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Builtin     bool      `json:"builtin"`
	Permissions []string  `json:"permissions"`
	Assignments int       `json:"assignments"`
	Version     int       `json:"version"`
}

type RoleInput struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	Version     int      `json:"version"`
}

func (s *service) Roles(ctx context.Context) ([]Role, error) {
	rows, err := s.q(ctx).ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Role, 0, len(rows))
	for _, r := range rows {
		out = append(out, Role{
			ID: r.ID, Key: r.Key, Name: r.Name, Builtin: r.Builtin, Permissions: r.Permissions,
			Assignments: int(r.Assignments), Version: int(r.Version),
		})
	}
	return out, nil
}

func (s *service) role(ctx context.Context, id uuid.UUID) (Role, error) {
	list, err := s.Roles(ctx)
	if err != nil {
		return Role{}, err
	}
	for _, r := range list {
		if r.ID == id {
			return r, nil
		}
	}
	return Role{}, httpx.NotFound("role.not_found", "Persona tidak ditemukan.")
}

// checkRole merapikan nama dan izin. Izin hanya boleh dari katalog dan disimpan urut katalog.
func (s *service) checkRole(ctx context.Context, id uuid.UUID, in RoleInput) (string, []string, error) {
	name := strings.TrimSpace(in.Name)
	fields := map[string]string{}
	switch {
	case name == "":
		fields["name"] = "Nama persona wajib diisi."
	case utf8.RuneCountInString(name) > 60:
		fields["name"] = "Nama persona maksimal 60 karakter."
	default:
		taken, err := s.q(ctx).RoleNameTaken(ctx, store.RoleNameTakenParams{Name: name, ID: id})
		if err != nil {
			return "", nil, err
		}
		if taken {
			fields["name"] = "Nama ini sudah dipakai persona lain."
		}
	}
	var perms []string
	for _, p := range rbac.All {
		if slices.Contains(in.Permissions, p) {
			perms = append(perms, p)
		}
	}
	for _, p := range in.Permissions {
		if !slices.Contains(rbac.All, p) {
			fields["permissions"] = fmt.Sprintf("Izin %q tidak ada di katalog.", p)
		}
	}
	if len(perms) == 0 && fields["permissions"] == "" {
		fields["permissions"] = "Pilih minimal satu izin."
	}
	return name, perms, invalid(fields)
}

// roleKey membuat kunci persona kustom dari namanya, mis. "Kasir proyek" → custom_kasir_proyek.
func (s *service) roleKey(ctx context.Context, name string) (string, error) {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "_"):
			b.WriteByte('_')
		}
	}
	base := "custom_" + strings.Trim(b.String(), "_")
	if base == "custom_" {
		base = "custom_persona"
	}
	key := base
	for i := 2; ; i++ {
		exists, err := s.q(ctx).RoleKeyExists(ctx, key)
		if err != nil || !exists {
			return key, err
		}
		key = fmt.Sprintf("%s_%d", base, i)
	}
}

func (s *service) CreateRole(ctx context.Context, in RoleInput, actor audit.Actor) (Role, error) {
	var out Role
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		name, perms, err := s.checkRole(ctx, uuid.Nil, in)
		if err != nil {
			return err
		}
		key, err := s.roleKey(ctx, name)
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		if err := s.q(ctx).InsertRole(ctx, store.InsertRoleParams{ID: id, Key: key, Name: name, Permissions: perms}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), Action: "platform.role.create", ObjectType: "role", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"key": key, "name": name, "permissions": perms},
		}); err != nil {
			return err
		}
		out, err = s.role(ctx, id)
		return err
	})
	return out, err
}

func (s *service) lockRole(ctx context.Context, id uuid.UUID) (store.PlatformRole, error) {
	r, err := s.q(ctx).GetRoleForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, httpx.NotFound("role.not_found", "Persona tidak ditemukan.")
	}
	if err != nil {
		return r, err
	}
	if r.Builtin {
		return r, httpx.Forbidden("role.builtin", "Peran bawaan tidak bisa diubah atau dihapus; buat persona kustom bila butuh izin lain.")
	}
	return r, nil
}

func (s *service) UpdateRole(ctx context.Context, id uuid.UUID, in RoleInput, actor audit.Actor) (Role, error) {
	var out Role
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		if err := q.LockAdmins(ctx); err != nil {
			return err
		}
		before, err := s.lockRole(ctx, id)
		if err != nil {
			return err
		}
		name, perms, err := s.checkRole(ctx, id, in)
		if err != nil {
			return err
		}
		n, err := q.UpdateRole(ctx, store.UpdateRoleParams{ID: id, Version: int32(in.Version), Name: name, Permissions: perms})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Persona ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if err := s.ensureAdminRemains(ctx); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), Action: "platform.role.update", ObjectType: "role", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"name": before.Name, "permissions": before.Permissions},
			After:  map[string]any{"name": name, "permissions": perms},
		}); err != nil {
			return err
		}
		out, err = s.role(ctx, id)
		return err
	})
	return out, err
}

func (s *service) DeleteRole(ctx context.Context, id uuid.UUID, actor audit.Actor) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		before, err := s.lockRole(ctx, id)
		if err != nil {
			return err
		}
		n, err := s.q(ctx).DeleteRole(ctx, id)
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("role.in_use", "Persona ini masih dipakai pengguna. Pindahkan dulu penggunanya ke peran lain.")
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), Action: "platform.role.delete", ObjectType: "role", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"key": before.Key, "name": before.Name, "permissions": before.Permissions},
		})
		return err
	})
}

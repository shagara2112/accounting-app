// Package admin mengelola instalasi: setup wizard pertama kali (PLT-02), pengguna dan aksesnya
// per PT (PLT-05), persona kustom (PLT-12), dan pembuatan PT baru.
//
// Pengelola hanya bisa memberi atau mencabut akses di PT tempat ia sendiri memegang izin
// kelola pengguna, tidak bisa mengubah akses atau status akunnya sendiri, dan tidak bisa
// membuat instalasi kehilangan pengelola pengguna terakhir.
package admin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/admin/internal/store"
	"lajur/internal/modules/core"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// LicenseSetup mengaktifkan lisensi saat setup wizard. Mode local (pengembangan) tidak
// butuh aktivasi; mode remote mengikat license key ke instalasi ini di License Console.
type LicenseSetup interface {
	Remote() bool
	Activate(ctx context.Context, in license.Activation) error
}

// LicenseInput adalah isian lisensi di setup wizard.
type LicenseInput = license.Activation

type Service interface {
	Users(ctx context.Context, actorID uuid.UUID) (UserList, error)
	CreateUser(ctx context.Context, in NewUser, actor audit.Actor) (User, error)
	UpdateUser(ctx context.Context, id uuid.UUID, in UserUpdate, actor audit.Actor) (User, error)
	SetAccess(ctx context.Context, userID, companyID uuid.UUID, roleKey string, branches []uuid.UUID, actor audit.Actor) error
	ResetPassword(ctx context.Context, id uuid.UUID, password string, actor audit.Actor) error
	ResetMFA(ctx context.Context, id uuid.UUID, actor audit.Actor) error
	Unlock(ctx context.Context, id uuid.UUID, actor audit.Actor) error

	Roles(ctx context.Context) ([]Role, error)
	CreateRole(ctx context.Context, in RoleInput, actor audit.Actor) (Role, error)
	UpdateRole(ctx context.Context, id uuid.UUID, in RoleInput, actor audit.Actor) (Role, error)
	DeleteRole(ctx context.Context, id uuid.UUID, actor audit.Actor) error

	Companies(ctx context.Context) ([]Company, error)
	CreateCompany(ctx context.Context, in NewCompany, creatorRole string, actor audit.Actor) (Company, error)

	RemoteLicense() bool
	SetupRequired(ctx context.Context) (bool, error)
	Setup(ctx context.Context, in SetupInput, ip string) (uuid.UUID, error)
}

type Deps struct {
	Pool    *pgxpool.Pool
	Core    core.Service
	Ledger  ledger.Service
	License LicenseSetup
	Now     func() time.Time
}

func New(d Deps) Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &service{d}
}

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

// ---------------------------------------------------------------- pengguna

type Access struct {
	CompanyID   uuid.UUID `json:"companyId"`
	CompanyName string    `json:"companyName"`
	RoleKey     string    `json:"roleKey"`
	RoleName    string    `json:"roleName"`
	// BranchIDs membatasi pengguna ke cabang tertentu di PT ini (PLT-12); kosong = semua.
	BranchIDs []uuid.UUID `json:"branchIds"`
}

type User struct {
	ID          uuid.UUID  `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Active      bool       `json:"active"`
	Locked      bool       `json:"locked"`
	MFAEnabled  bool       `json:"mfaEnabled"`
	MFARequired bool       `json:"mfaRequired"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	Version     int        `json:"version"`
	Access      []Access   `json:"access"`
}

type CompanyChoice struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Manageable bool      `json:"manageable"`
	// Branches: cabang PT yang bisa dipakai membatasi pengguna (PLT-12); hanya untuk PT yang dikelola.
	Branches []core.Dimension `json:"branches"`
}

type RoleChoice struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type UserList struct {
	Items     []User          `json:"items"`
	Companies []CompanyChoice `json:"companies"`
	Roles     []RoleChoice    `json:"roles"`
}

type AccessInput struct {
	CompanyID uuid.UUID   `json:"companyId"`
	RoleKey   string      `json:"roleKey"`
	BranchIDs []uuid.UUID `json:"branchIds"`
}

type NewUser struct {
	Name     string        `json:"name"`
	Email    string        `json:"email"`
	Password string        `json:"password"`
	Access   []AccessInput `json:"access"`
}

type UserUpdate struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Active  bool   `json:"active"`
	Version int    `json:"version"`
}

// manageable: PT tempat pengguna memegang izin kelola pengguna.
func (s *service) manageable(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]bool, error) {
	list, err := s.Core.CompaniesFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]bool{}
	for _, c := range list {
		if slices.Contains(c.Permissions, rbac.UserManage) {
			out[c.ID] = true
		}
	}
	return out, nil
}

// listUsers membaca semua pengguna beserta aksesnya per PT.
func (s *service) listUsers(ctx context.Context) ([]User, error) {
	q := s.q(ctx)
	users, err := q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	access, err := q.ListAccess(ctx)
	if err != nil {
		return nil, err
	}
	byUser := map[uuid.UUID][]Access{}
	privileged := map[uuid.UUID]bool{}
	for _, a := range access {
		branches := a.BranchIds
		if branches == nil {
			branches = []uuid.UUID{}
		}
		byUser[a.UserID] = append(byUser[a.UserID], Access{CompanyID: a.CompanyID, CompanyName: a.CompanyName, RoleKey: a.RoleKey, RoleName: a.RoleName, BranchIDs: branches})
		for _, p := range auth.PrivilegedPerms {
			privileged[a.UserID] = privileged[a.UserID] || slices.Contains(a.Permissions, p)
		}
	}
	now := s.Now()
	out := make([]User, 0, len(users))
	for _, u := range users {
		acc := byUser[u.ID]
		if acc == nil {
			acc = []Access{}
		}
		out = append(out, User{
			ID: u.ID, Email: u.Email, Name: u.Name, Active: u.Active,
			Locked: u.LockedUntil != nil && now.Before(*u.LockedUntil), MFAEnabled: u.TotpEnabledAt != nil,
			MFARequired: privileged[u.ID], LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt, Version: int(u.Version), Access: acc,
		})
	}
	return out, nil
}

func (s *service) user(ctx context.Context, id uuid.UUID) (User, error) {
	list, err := s.listUsers(ctx)
	if err != nil {
		return User{}, err
	}
	for _, u := range list {
		if u.ID == id {
			return u, nil
		}
	}
	return User{}, httpx.NotFound("user.not_found", "Pengguna tidak ditemukan.")
}

func (s *service) Users(ctx context.Context, actorID uuid.UUID) (UserList, error) {
	var out UserList
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		var err error
		if out.Items, err = s.listUsers(ctx); err != nil {
			return err
		}
		mine, err := s.manageable(ctx, actorID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		companies, err := q.ListCompanies(ctx)
		if err != nil {
			return err
		}
		for _, c := range companies {
			choice := CompanyChoice{ID: c.ID, Name: c.Name, Manageable: mine[c.ID], Branches: []core.Dimension{}}
			if choice.Manageable {
				if choice.Branches, err = s.Core.Dimensions(ctx, c.ID, core.DimBranch); err != nil {
					return err
				}
			}
			out.Companies = append(out.Companies, choice)
		}
		roles, err := q.ListRoles(ctx)
		if err != nil {
			return err
		}
		for _, r := range roles {
			out.Roles = append(out.Roles, RoleChoice{Key: r.Key, Name: r.Name})
		}
		return nil
	})
	return out, err
}

func checkProfile(name, email string) (string, string, map[string]string) {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	fields := map[string]string{}
	switch {
	case name == "":
		fields["name"] = "Nama wajib diisi."
	case utf8.RuneCountInString(name) > 100:
		fields["name"] = "Nama maksimal 100 karakter."
	}
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email || len(email) > 200 {
		fields["email"] = "Email tidak valid."
	}
	return name, strings.ToLower(email), fields
}

func invalid(fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	first := slices.Sorted(maps.Keys(fields))[0]
	return httpx.Unprocessable("validation", fields[first]).WithFields(fields)
}

func (s *service) CreateUser(ctx context.Context, in NewUser, actor audit.Actor) (User, error) {
	var out User
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		name, email, fields := checkProfile(in.Name, in.Email)
		if fields["email"] == "" {
			taken, err := q.EmailTaken(ctx, store.EmailTakenParams{Email: email, ID: uuid.Nil})
			if err != nil {
				return err
			}
			if taken {
				fields["email"] = "Email ini sudah dipakai pengguna lain."
			}
		}
		hash, err := auth.HashPassword(in.Password)
		if errors.Is(err, auth.ErrWeakPassword) {
			fields["password"] = "Kata sandi awal minimal 10 karakter."
		} else if err != nil {
			return err
		}
		mine, err := s.manageable(ctx, actor.UserID)
		if err != nil {
			return err
		}
		seen := map[uuid.UUID]bool{}
		for i, a := range in.Access {
			key := fmt.Sprintf("access.%d.companyId", i)
			switch {
			case seen[a.CompanyID]:
				fields[key] = "PT ini dipilih dua kali."
			case !mine[a.CompanyID]:
				fields[key] = "Anda tidak bisa mengelola pengguna di PT ini."
			}
			seen[a.CompanyID] = true
		}
		if err := invalid(fields); err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		if err := q.InsertUser(ctx, store.InsertUserParams{ID: id, Email: email, Name: name, PasswordHash: hash}); err != nil {
			return err
		}
		granted := map[string]string{}
		for i, a := range in.Access {
			role, err := q.GetRoleByKey(ctx, a.RoleKey)
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.Unprocessable("validation", "Peran tidak dikenal.").WithFields(map[string]string{fmt.Sprintf("access.%d.roleKey", i): "Peran tidak dikenal."})
			}
			if err != nil {
				return err
			}
			if err := q.SetAccess(ctx, store.SetAccessParams{UserID: id, CompanyID: a.CompanyID, RoleID: role.ID}); err != nil {
				return err
			}
			granted[a.CompanyID.String()] = role.Key
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), Action: "platform.user.create", ObjectType: "user", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"name": name, "email": email, "access": granted},
		}); err != nil {
			return err
		}
		out, err = s.user(ctx, id)
		return err
	})
	return out, err
}

// ensureAdminRemains menolak perubahan yang membuat tidak ada lagi pengguna aktif yang bisa
// mengelola pengguna. Dipanggil setelah perubahan, di transaksi yang sama.
func (s *service) ensureAdminRemains(ctx context.Context) error {
	n, err := s.q(ctx).CountActiveHolders(ctx, rbac.UserManage)
	if err != nil {
		return err
	}
	if n == 0 {
		return httpx.Conflict("admin.last_admin", "Harus tersisa minimal satu pengguna aktif yang bisa mengelola pengguna.")
	}
	return nil
}

func notSelf(actor audit.Actor, id uuid.UUID, msg string) error {
	if actor.UserID == id {
		return httpx.Forbidden("admin.self", msg)
	}
	return nil
}

func (s *service) UpdateUser(ctx context.Context, id uuid.UUID, in UserUpdate, actor audit.Actor) (User, error) {
	var out User
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		if err := q.LockAdmins(ctx); err != nil {
			return err
		}
		before, err := q.GetUserForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("user.not_found", "Pengguna tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if before.Active && !in.Active {
			if err := notSelf(actor, id, "Anda tidak bisa menonaktifkan akun Anda sendiri."); err != nil {
				return err
			}
		}
		name, email, fields := checkProfile(in.Name, in.Email)
		if fields["email"] == "" {
			taken, err := q.EmailTaken(ctx, store.EmailTakenParams{Email: email, ID: id})
			if err != nil {
				return err
			}
			if taken {
				fields["email"] = "Email ini sudah dipakai pengguna lain."
			}
		}
		if err := invalid(fields); err != nil {
			return err
		}
		n, err := q.UpdateUser(ctx, store.UpdateUserParams{ID: id, Version: int32(in.Version), Name: name, Email: email, Active: in.Active})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Data pengguna ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if !in.Active || email != before.Email {
			// Menonaktifkan pengguna langsung mematikan semua sesinya (TDD).
			if err := q.DeleteUserSessions(ctx, id); err != nil {
				return err
			}
		}
		if !in.Active {
			if err := s.ensureAdminRemains(ctx); err != nil {
				return err
			}
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), Action: "platform.user.update", ObjectType: "user", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"name": before.Name, "email": before.Email, "active": before.Active},
			After:  map[string]any{"name": name, "email": email, "active": in.Active},
		}); err != nil {
			return err
		}
		out, err = s.user(ctx, id)
		return err
	})
	return out, err
}

func (s *service) SetAccess(ctx context.Context, userID, companyID uuid.UUID, roleKey string, branches []uuid.UUID, actor audit.Actor) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if err := notSelf(actor, userID, "Akses Anda sendiri hanya bisa diubah pengelola lain."); err != nil {
			return err
		}
		q := s.q(ctx)
		if err := q.LockAdmins(ctx); err != nil {
			return err
		}
		mine, err := s.manageable(ctx, actor.UserID)
		if err != nil {
			return err
		}
		if !mine[companyID] {
			return httpx.Forbidden("admin.company_denied", "Anda tidak bisa mengelola pengguna di PT ini.")
		}
		if _, err := q.GetUserForUpdate(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("user.not_found", "Pengguna tidak ditemukan.")
		} else if err != nil {
			return err
		}
		var before *string
		if old, err := q.GetAccessRole(ctx, store.GetAccessRoleParams{UserID: userID, CompanyID: companyID}); err == nil {
			before = &old.Key
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if roleKey == "" {
			if _, err := q.RemoveAccess(ctx, store.RemoveAccessParams{UserID: userID, CompanyID: companyID}); err != nil {
				return err
			}
		} else {
			role, err := q.GetRoleByKey(ctx, roleKey)
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.Unprocessable("validation", "Peran tidak dikenal.").WithFields(map[string]string{"roleKey": "Peran tidak dikenal."})
			}
			if err != nil {
				return err
			}
			if len(branches) > 0 {
				n, err := q.CompanyBranchCount(ctx, store.CompanyBranchCountParams{CompanyID: companyID, Ids: branches})
				if err != nil {
					return err
				}
				if int(n) != len(branches) {
					return httpx.Unprocessable("validation", "Ada cabang yang bukan milik PT ini.").WithFields(map[string]string{"branchIds": "Ada cabang yang bukan milik PT ini."})
				}
			}
			if err := q.SetAccess(ctx, store.SetAccessParams{UserID: userID, CompanyID: companyID, RoleID: role.ID, BranchIds: branches}); err != nil {
				return err
			}
		}
		if err := s.ensureAdminRemains(ctx); err != nil {
			return err
		}
		var after *string
		if roleKey != "" {
			after = &roleKey
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "platform.user.access", ObjectType: "user", ObjectID: userID.String(), IP: actor.IP,
			Before: map[string]any{"role": before}, After: map[string]any{"role": after, "branches": len(branches)},
		})
		return err
	})
}

func (s *service) ResetPassword(ctx context.Context, id uuid.UUID, password string, actor audit.Actor) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if err := notSelf(actor, id, "Ganti kata sandi Anda sendiri lewat halaman Profil."); err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.GetUserForUpdate(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("user.not_found", "Pengguna tidak ditemukan.")
		} else if err != nil {
			return err
		}
		hash, err := auth.HashPassword(password)
		if errors.Is(err, auth.ErrWeakPassword) {
			return httpx.Unprocessable("validation", "Kata sandi minimal 10 karakter.").WithFields(map[string]string{"password": "Minimal 10 karakter."})
		}
		if err != nil {
			return err
		}
		if err := q.SetUserPassword(ctx, store.SetUserPasswordParams{ID: id, PasswordHash: hash}); err != nil {
			return err
		}
		if err := q.DeleteUserSessions(ctx, id); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), Action: "platform.user.password_reset", ObjectType: "user", ObjectID: id.String(), IP: actor.IP,
		})
		return err
	})
}

// ResetMFA menghapus 2FA pengguna yang kehilangan ponselnya; ia memasang ulang saat login berikutnya.
func (s *service) ResetMFA(ctx context.Context, id uuid.UUID, actor audit.Actor) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if err := notSelf(actor, id, "Atur 2FA Anda sendiri lewat halaman Profil."); err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.GetUserForUpdate(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("user.not_found", "Pengguna tidak ditemukan.")
		} else if err != nil {
			return err
		}
		if err := q.ResetTOTP(ctx, id); err != nil {
			return err
		}
		if err := q.DeleteUserSessions(ctx, id); err != nil {
			return err
		}
		_, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), Action: "platform.user.mfa_reset", ObjectType: "user", ObjectID: id.String(), IP: actor.IP,
		})
		return err
	})
}

func (s *service) Unlock(ctx context.Context, id uuid.UUID, actor audit.Actor) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		if _, err := q.GetUserForUpdate(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("user.not_found", "Pengguna tidak ditemukan.")
		} else if err != nil {
			return err
		}
		if err := q.UnlockUser(ctx, id); err != nil {
			return err
		}
		_, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), Action: "platform.user.unlock", ObjectType: "user", ObjectID: id.String(), IP: actor.IP,
		})
		return err
	})
}

// Package core adalah modul perusahaan (PT) dan hak akses pengguna per PT.
package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/modules/core/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/rbac"
)

type Company struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	ShortName string    `json:"shortName"`
	Kind      string    `json:"kind"`
	Industry  *string   `json:"industry,omitempty"`
	PKP       bool      `json:"pkp"`
	NPWP      string    `json:"npwp,omitempty"`
	NITKU     string    `json:"nitku,omitempty"`
	Address   string    `json:"address,omitempty"`
	Version   int       `json:"version,omitempty"`
}

type CompanyAccess struct {
	Company
	RoleKey     string   `json:"roleKey"`
	RoleName    string   `json:"roleName"`
	Permissions []string `json:"permissions"`
	// BranchIDs: pengguna dibatasi ke cabang ini (PLT-12); kosong = semua cabang.
	BranchIDs []uuid.UUID `json:"branchIds"`
}

type NewCompany struct {
	Name      string
	ShortName string
	NPWP      *string
	NITKU     *string
	Kind      string // "pt" atau "consolidation"
	Industry  *string
}

// Service adalah satu-satunya pintu modul core bagi modul lain.
type Service interface {
	rbac.Resolver
	CompaniesFor(ctx context.Context, userID uuid.UUID) ([]CompanyAccess, error)
	Companies(ctx context.Context) ([]Company, error)
	// Dimensi cabang dan departemen (GL-09); kind = "cabang" atau "departemen".
	Dimensions(ctx context.Context, companyID uuid.UUID, kind string) ([]Dimension, error)
	SaveDimension(ctx context.Context, companyID uuid.UUID, kind string, id *uuid.UUID, in DimensionInput, actor audit.Actor) (Dimension, error)
	CheckDimensions(ctx context.Context, companyID uuid.UUID, branchID, departmentID *uuid.UUID) error
	Company(ctx context.Context, id uuid.UUID) (Company, error)
	// UpdateTaxProfile mengubah NPWP, NITKU, alamat, dan status PKP PT (TAX-05).
	UpdateTaxProfile(ctx context.Context, id uuid.UUID, in TaxProfile, actor audit.Actor) (Company, error)
	CreateCompany(ctx context.Context, c NewCompany) (Company, error)
	Assign(ctx context.Context, userID, companyID uuid.UUID, roleKey string) error

	Contacts(ctx context.Context, companyID uuid.UUID, f ContactFilter) ([]Contact, error)
	Contact(ctx context.Context, companyID, id uuid.UUID) (Contact, error)
	ContactNames(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error)
	CreateContact(ctx context.Context, companyID uuid.UUID, in ContactInput, actor audit.Actor) (Contact, error)
	UpdateContact(ctx context.Context, companyID, id uuid.UUID, in ContactInput, actor audit.Actor) (Contact, error)
	// LinkedContact mengembalikan kontak di PT ini yang mewakili PT grup lain (ME-03).
	LinkedContact(ctx context.Context, companyID, linkedCompanyID uuid.UUID) (Contact, error)

	// Logo PT untuk kop faktur PDF (JPEG); nil bila belum ada.
	Logo(ctx context.Context, companyID uuid.UUID) ([]byte, error)
	SetLogo(ctx context.Context, companyID uuid.UUID, jpeg []byte, actor audit.Actor) error
}

// AccountCheck memastikan akun ada di PT, akun detail yang aktif, dan berkelas yang diizinkan.
// Diisi buku besar saat perangkaian, karena modul core tidak membaca tabel buku besar.
type AccountCheck func(ctx context.Context, companyID, accountID uuid.UUID, classes []int) error

type Option func(*service)

func WithAccountCheck(fn AccountCheck) Option { return func(s *service) { s.accountCheck = fn } }

func New(pool *pgxpool.Pool, opts ...Option) Service {
	s := &service{pool: pool}
	for _, o := range opts {
		o(s)
	}
	return s
}

type service struct {
	pool         *pgxpool.Pool
	accountCheck AccountCheck
}

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.pool)) }

func (s *service) Role(ctx context.Context, userID, companyID uuid.UUID) (rbac.Role, error) {
	row, err := s.q(ctx).GetAccess(ctx, store.GetAccessParams{UserID: userID, CompanyID: companyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return rbac.Role{}, rbac.ErrNoAccess
	}
	if err != nil {
		return rbac.Role{}, err
	}
	return rbac.Role{Key: row.Key, Perms: rbac.NewPerms(row.Permissions), Branches: row.BranchIds}, nil
}

// DefaultCompany memilih PT pertama yang bisa diakses pengguna, untuk PT aktif saat login.
func (s *service) DefaultCompany(ctx context.Context, userID uuid.UUID) (*uuid.UUID, error) {
	list, err := s.CompaniesFor(ctx, userID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0].ID, nil
}

func (s *service) CompaniesFor(ctx context.Context, userID uuid.UUID) ([]CompanyAccess, error) {
	rows, err := s.q(ctx).ListAccessForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]CompanyAccess, 0, len(rows))
	for _, r := range rows {
		out = append(out, CompanyAccess{
			Company: Company{ID: r.ID, Name: r.Name, ShortName: r.ShortName, Kind: r.Kind, Industry: r.Industry, PKP: r.Pkp},
			RoleKey: r.RoleKey, RoleName: r.RoleName, Permissions: r.Permissions, BranchIDs: nonNil(r.BranchIds),
		})
	}
	return out, nil
}

func (s *service) Companies(ctx context.Context) ([]Company, error) {
	rows, err := s.q(ctx).ListCompanies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Company, 0, len(rows))
	for _, r := range rows {
		out = append(out, Company{ID: r.ID, Name: r.Name, ShortName: r.ShortName, Kind: r.Kind, Industry: r.Industry, PKP: r.Pkp})
	}
	return out, nil
}

func (s *service) Company(ctx context.Context, id uuid.UUID) (Company, error) {
	r, err := s.q(ctx).GetCompany(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Company{}, httpx.NotFound("company.not_found", "PT tidak ditemukan.")
	}
	if err != nil {
		return Company{}, err
	}
	return toCompany(r), nil
}

func toCompany(r store.CoreCompany) Company {
	return Company{
		ID: r.ID, Name: r.Name, ShortName: r.ShortName, Kind: r.Kind, Industry: r.Industry, PKP: r.Pkp,
		NPWP: deref(r.Npwp), NITKU: deref(r.Nitku), Address: r.Address, Version: int(r.Version),
	}
}

func (s *service) CreateCompany(ctx context.Context, c NewCompany) (Company, error) {
	if c.Kind == "" {
		c.Kind = "pt"
	}
	row, err := s.q(ctx).CreateCompany(ctx, store.CreateCompanyParams{
		ID: uuid.Must(uuid.NewV7()), Name: c.Name, ShortName: c.ShortName,
		Npwp: c.NPWP, Nitku: c.NITKU, Kind: c.Kind, Industry: c.Industry,
	})
	if err != nil {
		return Company{}, err
	}
	return Company{ID: row.ID, Name: row.Name, ShortName: row.ShortName, Kind: row.Kind, Industry: row.Industry, PKP: row.Pkp}, nil
}

func (s *service) Assign(ctx context.Context, userID, companyID uuid.UUID, roleKey string) error {
	n, err := s.q(ctx).AssignRoleByKey(ctx, store.AssignRoleByKeyParams{UserID: userID, CompanyID: companyID, RoleKey: roleKey})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("peran %q tidak ada", roleKey)
	}
	return nil
}

func (s *service) Logo(ctx context.Context, companyID uuid.UUID) ([]byte, error) {
	return s.q(ctx).GetCompanyLogo(ctx, companyID)
}

// SetLogo mengganti atau (jpeg nil) menghapus logo PT.
func (s *service) SetLogo(ctx context.Context, companyID uuid.UUID, jpeg []byte, actor audit.Actor) error {
	return db.Run(ctx, s.pool, func(ctx context.Context) error {
		if err := s.q(ctx).SetCompanyLogo(ctx, store.SetCompanyLogoParams{ID: companyID, Logo: jpeg}); err != nil {
			return err
		}
		action := "core.company.logo"
		if jpeg == nil {
			action = "core.company.logo_remove"
		}
		_, err := audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: action, ObjectType: "company", ObjectID: companyID.String(), IP: actor.IP,
			After: map[string]any{"bytes": len(jpeg)},
		})
		return err
	})
}

func nonNil(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

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
	"lajur/internal/platform/db"
	"lajur/internal/platform/rbac"
)

type Company struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	ShortName string    `json:"shortName"`
	Kind      string    `json:"kind"`
	Industry  *string   `json:"industry,omitempty"`
}

type CompanyAccess struct {
	Company
	RoleKey     string   `json:"roleKey"`
	RoleName    string   `json:"roleName"`
	Permissions []string `json:"permissions"`
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
	CreateCompany(ctx context.Context, c NewCompany) (Company, error)
	Assign(ctx context.Context, userID, companyID uuid.UUID, roleKey string) error
}

func New(pool *pgxpool.Pool) Service { return &service{pool: pool} }

type service struct{ pool *pgxpool.Pool }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.pool)) }

func (s *service) Role(ctx context.Context, userID, companyID uuid.UUID) (rbac.Role, error) {
	row, err := s.q(ctx).GetAccess(ctx, store.GetAccessParams{UserID: userID, CompanyID: companyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return rbac.Role{}, rbac.ErrNoAccess
	}
	if err != nil {
		return rbac.Role{}, err
	}
	return rbac.Role{Key: row.Key, Perms: rbac.NewPerms(row.Permissions)}, nil
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
			Company:  Company{ID: r.ID, Name: r.Name, ShortName: r.ShortName, Kind: r.Kind, Industry: r.Industry},
			RoleKey:  r.RoleKey, RoleName: r.RoleName, Permissions: r.Permissions,
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
		out = append(out, Company{ID: r.ID, Name: r.Name, ShortName: r.ShortName, Kind: r.Kind, Industry: r.Industry})
	}
	return out, nil
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
	return Company{ID: row.ID, Name: row.Name, ShortName: row.ShortName, Kind: row.Kind, Industry: row.Industry}, nil
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

package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/modules/core/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
)

// Dimensi analitik cabang dan departemen (GL-09): ditempel di dokumen dan baris jurnal supaya
// laporan bisa dipilah per cabang atau departemen. Tidak bisa dihapus; nonaktifkan bila tidak
// dipakai lagi.

const (
	DimBranch     = "cabang"
	DimDepartment = "departemen"
)

type Dimension struct {
	ID      uuid.UUID `json:"id"`
	Kind    string    `json:"kind"`
	Code    string    `json:"code"`
	Name    string    `json:"name"`
	Active  bool      `json:"active"`
	Version int       `json:"version"`
}

type DimensionInput struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Active  bool   `json:"active"`
	Version int    `json:"version"`
}

var dimCode = regexp.MustCompile(`^[A-Z0-9-]{1,10}$`)

func (in *DimensionInput) validate() error {
	in.Code, in.Name = strings.ToUpper(strings.TrimSpace(in.Code)), strings.TrimSpace(in.Name)
	f := map[string]string{}
	if !dimCode.MatchString(in.Code) {
		f["code"] = "Kode 1–10 huruf besar, angka, atau tanda hubung."
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 100 {
		f["name"] = "Nama wajib diisi (maksimal 100 karakter)."
	}
	if len(f) > 0 {
		msg := f["code"]
		if msg == "" {
			msg = f["name"]
		}
		return httpx.Unprocessable("dimension.invalid", msg).WithFields(f)
	}
	return nil
}

func (s *service) Dimensions(ctx context.Context, companyID uuid.UUID, kind string) ([]Dimension, error) {
	q := s.q(ctx)
	var out []Dimension
	switch kind {
	case DimBranch:
		rows, err := q.ListBranches(ctx, companyID)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, Dimension{ID: r.ID, Kind: kind, Code: r.Code, Name: r.Name, Active: r.Active, Version: int(r.Version)})
		}
	case DimDepartment:
		rows, err := q.ListDepartments(ctx, companyID)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, Dimension{ID: r.ID, Kind: kind, Code: r.Code, Name: r.Name, Active: r.Active, Version: int(r.Version)})
		}
	default:
		return nil, httpx.NotFound("dimension.kind", "Jenis dimensi tidak dikenal.")
	}
	if out == nil {
		out = []Dimension{}
	}
	return out, nil
}

// SaveDimension membuat (id nil) atau mengubah cabang/departemen.
func (s *service) SaveDimension(ctx context.Context, companyID uuid.UUID, kind string, id *uuid.UUID, in DimensionInput, actor audit.Actor) (Dimension, error) {
	if err := in.validate(); err != nil {
		return Dimension{}, err
	}
	var out Dimension
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		q := s.q(ctx)
		var err error
		switch {
		case kind == DimBranch && id == nil:
			var r store.CoreBranch
			r, err = q.InsertBranch(ctx, store.InsertBranchParams{ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, Code: in.Code, Name: in.Name})
			out = Dimension{ID: r.ID, Kind: kind, Code: r.Code, Name: r.Name, Active: r.Active, Version: int(r.Version)}
		case kind == DimBranch:
			var r store.CoreBranch
			r, err = q.UpdateBranch(ctx, store.UpdateBranchParams{CompanyID: companyID, ID: *id, Version: int32(in.Version), Code: in.Code, Name: in.Name, Active: in.Active})
			out = Dimension{ID: r.ID, Kind: kind, Code: r.Code, Name: r.Name, Active: r.Active, Version: int(r.Version)}
		case kind == DimDepartment && id == nil:
			var r store.CoreDepartment
			r, err = q.InsertDepartment(ctx, store.InsertDepartmentParams{ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, Code: in.Code, Name: in.Name})
			out = Dimension{ID: r.ID, Kind: kind, Code: r.Code, Name: r.Name, Active: r.Active, Version: int(r.Version)}
		case kind == DimDepartment:
			var r store.CoreDepartment
			r, err = q.UpdateDepartment(ctx, store.UpdateDepartmentParams{CompanyID: companyID, ID: *id, Version: int32(in.Version), Code: in.Code, Name: in.Name, Active: in.Active})
			out = Dimension{ID: r.ID, Kind: kind, Code: r.Code, Name: r.Name, Active: r.Active, Version: int(r.Version)}
		default:
			return httpx.NotFound("dimension.kind", "Jenis dimensi tidak dikenal.")
		}
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return httpx.Conflict("conflict.version", "Data ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		case db.IsUniqueViolation(err):
			msg := fmt.Sprintf("Kode %s sudah dipakai %s lain.", in.Code, kind)
			return httpx.Conflict("dimension.duplicate", msg).WithFields(map[string]string{"code": msg})
		case err != nil:
			return err
		}
		action := "core." + kind + ".update"
		if id == nil {
			action = "core." + kind + ".create"
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: action, ObjectType: kind, ObjectID: out.ID.String(), IP: actor.IP,
			After: map[string]any{"code": out.Code, "name": out.Name, "active": out.Active},
		})
		return err
	})
	return out, err
}

// CheckDimensions memeriksa cabang dan departemen dokumen: ada di PT ini dan aktif.
func (s *service) CheckDimensions(ctx context.Context, companyID uuid.UUID, branchID, departmentID *uuid.UUID) error {
	check := func(id *uuid.UUID, kind, field, label string) error {
		if id == nil {
			return nil
		}
		list, err := s.Dimensions(ctx, companyID, kind)
		if err != nil {
			return err
		}
		for _, d := range list {
			if d.ID == *id {
				if !d.Active {
					msg := fmt.Sprintf("%s %s nonaktif.", label, d.Code)
					return httpx.Unprocessable("dimension.inactive", msg).WithFields(map[string]string{field: msg})
				}
				return nil
			}
		}
		msg := label + " tidak ditemukan di PT ini."
		return httpx.Unprocessable("dimension.not_found", msg).WithFields(map[string]string{field: msg})
	}
	if err := check(branchID, DimBranch, "branchId", "Cabang"); err != nil {
		return err
	}
	return check(departmentID, DimDepartment, "departmentId", "Departemen")
}

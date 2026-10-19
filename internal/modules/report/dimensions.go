package report

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"lajur/internal/modules/core"
	"lajur/internal/modules/report/internal/store"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const maxBranchColumns = 12

// DimFilter memilah laba rugi per cabang atau departemen (GL-09). Branches membatasi pengguna
// yang aksesnya terbatas pada cabang tertentu (PLT-12); kosong = semua cabang.
type DimFilter struct {
	BranchID      *uuid.UUID
	DepartmentID  *uuid.UUID
	PerBranch     bool // satu kolom per cabang untuk periode pertama
	PerDepartment bool // satu kolom per departemen untuk periode pertama
	Branches      []uuid.UUID
}

func (f DimFilter) active() bool {
	return f.BranchID != nil || f.DepartmentID != nil || f.PerBranch || f.PerDepartment || len(f.Branches) > 0
}

// netDim menjumlahkan mutasi per akun dari baris jurnal untuk rentang tanggal, dikelompokkan
// per cabang (byDept false) atau per departemen (byDept true); uuid.Nil = tanpa dimensi itu.
func (s *service) netDim(ctx context.Context, companyID uuid.UUID, from, to civil.Date, f DimFilter, byDept bool) (map[uuid.UUID]map[uuid.UUID]money.Amount, error) {
	rows, err := s.q(ctx).NetByDimension(ctx, store.NetByDimensionParams{
		CompanyID: companyID, FromDate: from, ToDate: to, BranchID: f.BranchID, DepartmentID: f.DepartmentID,
	})
	if err != nil {
		return nil, err
	}
	allowed := map[uuid.UUID]bool{}
	for _, b := range f.Branches {
		allowed[b] = true
	}
	out := map[uuid.UUID]map[uuid.UUID]money.Amount{} // dimensi → akun → debit − kredit
	for _, r := range rows {
		branch, dept := uuid.Nil, uuid.Nil
		if r.BranchID != nil {
			branch = *r.BranchID
		}
		if r.DepartmentID != nil {
			dept = *r.DepartmentID
		}
		if len(allowed) > 0 && !allowed[branch] {
			continue
		}
		key := branch
		if byDept {
			key = dept
		}
		m, ok := out[key]
		if !ok {
			m = map[uuid.UUID]money.Amount{}
			out[key] = m
		}
		m[r.AccountID] = m[r.AccountID].Add(r.Debit).Sub(r.Credit)
	}
	return out, nil
}

func merge(by map[uuid.UUID]map[uuid.UUID]money.Amount) map[uuid.UUID]money.Amount {
	out := map[uuid.UUID]money.Amount{}
	for _, m := range by {
		for a, v := range m {
			out[a] = out[a].Add(v)
		}
	}
	return out
}

func idText(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// ProfitLossBy adalah laba rugi dengan saringan cabang/departemen, atau satu kolom per cabang
// atau per departemen. Setiap kolom membawa saringannya untuk tautan ke buku besar.
func (s *service) ProfitLossBy(ctx context.Context, companyID uuid.UUID, periods []Period, f DimFilter) (Statement, error) {
	if !f.active() {
		return s.ProfitLoss(ctx, companyID, periods)
	}
	if f.PerBranch && f.PerDepartment {
		return Statement{}, httpx.BadRequest("report.columns", "Pilih kolom per cabang atau per departemen, tidak keduanya.")
	}
	if err := checkColumns(len(periods)); err != nil {
		return Statement{}, err
	}
	c, err := s.chart(ctx, companyID)
	if err != nil {
		return Statement{}, err
	}
	name, err := s.companyName(ctx, companyID)
	if err != nil {
		return Statement{}, err
	}
	b := builder{Chart: c}
	st := Statement{Title: "Laporan laba rugi", Company: name}
	branch, dept := idText(f.BranchID), idText(f.DepartmentID)
	if f.PerBranch || f.PerDepartment {
		p := periods[0]
		if p.To.Before(p.From) {
			return Statement{}, httpx.BadRequest("report.range", "Tanggal akhir periode sebelum tanggal awal.")
		}
		filter, kind, noun := DimFilter{DepartmentID: f.DepartmentID, Branches: f.Branches}, core.DimBranch, "cabang"
		if f.PerDepartment {
			filter, kind, noun = DimFilter{BranchID: f.BranchID, Branches: f.Branches}, core.DimDepartment, "departemen"
		}
		by, err := s.netDim(ctx, companyID, p.From, p.To, filter, f.PerDepartment)
		if err != nil {
			return Statement{}, err
		}
		dims, err := s.Core.Dimensions(ctx, companyID, kind)
		if err != nil {
			return Statement{}, err
		}
		column := func(label, id string) Column {
			col := Column{Label: label, From: p.From, To: p.To, Branch: branch, Department: dept}
			if f.PerDepartment {
				col.Department = id
			} else {
				col.Branch = id
			}
			return col
		}
		for _, d := range dims {
			if m, ok := by[d.ID]; ok {
				b.cols = append(b.cols, m)
				st.Columns = append(st.Columns, column(d.Code+" "+d.Name, d.ID.String()))
			}
		}
		if m, ok := by[uuid.Nil]; ok {
			b.cols = append(b.cols, m)
			st.Columns = append(st.Columns, column("Tanpa "+noun, "none"))
		}
		b.cols = append(b.cols, merge(by))
		st.Columns = append(st.Columns, column("Jumlah "+periodLabel(p), ""))
		if len(b.cols) > maxBranchColumns {
			return Statement{}, httpx.BadRequest("report.columns", fmt.Sprintf(
				"Laba rugi per %s menampilkan paling banyak %d kolom; saring per cabang atau departemen dulu.", noun, maxBranchColumns))
		}
	} else {
		for _, p := range periods {
			if p.To.Before(p.From) {
				return Statement{}, httpx.BadRequest("report.range", "Tanggal akhir periode sebelum tanggal awal.")
			}
			by, err := s.netDim(ctx, companyID, p.From, p.To, f, false)
			if err != nil {
				return Statement{}, err
			}
			b.cols = append(b.cols, merge(by))
			st.Columns = append(st.Columns, Column{Label: periodLabel(p), From: p.From, To: p.To, Branch: branch, Department: dept})
		}
	}
	st.Lines = c.ProfitLossLines(b.cols)
	return st, nil
}

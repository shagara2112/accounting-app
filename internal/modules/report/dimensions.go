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
	BranchID     *uuid.UUID
	DepartmentID *uuid.UUID
	PerBranch    bool // satu kolom per cabang untuk periode pertama
	Branches     []uuid.UUID
}

func (f DimFilter) active() bool {
	return f.BranchID != nil || f.DepartmentID != nil || f.PerBranch || len(f.Branches) > 0
}

// netDim menjumlahkan mutasi per akun dan cabang dari baris jurnal untuk rentang tanggal.
func (s *service) netDim(ctx context.Context, companyID uuid.UUID, from, to civil.Date, f DimFilter) (map[uuid.UUID]map[uuid.UUID]money.Amount, error) {
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
	out := map[uuid.UUID]map[uuid.UUID]money.Amount{} // cabang → akun → debit − kredit
	for _, r := range rows {
		branch := uuid.Nil
		if r.BranchID != nil {
			branch = *r.BranchID
		}
		if len(allowed) > 0 && !allowed[branch] {
			continue
		}
		m, ok := out[branch]
		if !ok {
			m = map[uuid.UUID]money.Amount{}
			out[branch] = m
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

// ProfitLossBy adalah laba rugi dengan saringan cabang/departemen, atau satu kolom per cabang.
func (s *service) ProfitLossBy(ctx context.Context, companyID uuid.UUID, periods []Period, f DimFilter) (Statement, error) {
	if !f.active() {
		return s.ProfitLoss(ctx, companyID, periods)
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
	if f.PerBranch {
		p := periods[0]
		if p.To.Before(p.From) {
			return Statement{}, httpx.BadRequest("report.range", "Tanggal akhir periode sebelum tanggal awal.")
		}
		by, err := s.netDim(ctx, companyID, p.From, p.To, DimFilter{DepartmentID: f.DepartmentID, Branches: f.Branches})
		if err != nil {
			return Statement{}, err
		}
		branches, err := s.Core.Dimensions(ctx, companyID, core.DimBranch)
		if err != nil {
			return Statement{}, err
		}
		for _, br := range branches {
			if m, ok := by[br.ID]; ok {
				b.cols = append(b.cols, m)
				st.Columns = append(st.Columns, Column{Label: br.Code + " " + br.Name, From: p.From, To: p.To})
			}
		}
		if m, ok := by[uuid.Nil]; ok {
			b.cols = append(b.cols, m)
			st.Columns = append(st.Columns, Column{Label: "Tanpa cabang", From: p.From, To: p.To})
		}
		b.cols = append(b.cols, merge(by))
		st.Columns = append(st.Columns, Column{Label: "Jumlah " + periodLabel(p), From: p.From, To: p.To})
		if len(b.cols) > maxBranchColumns {
			return Statement{}, httpx.BadRequest("report.columns", fmt.Sprintf("Laba rugi per cabang menampilkan paling banyak %d kolom; saring per departemen atau pilih cabang.", maxBranchColumns))
		}
	} else {
		for _, p := range periods {
			if p.To.Before(p.From) {
				return Statement{}, httpx.BadRequest("report.range", "Tanggal akhir periode sebelum tanggal awal.")
			}
			by, err := s.netDim(ctx, companyID, p.From, p.To, f)
			if err != nil {
				return Statement{}, err
			}
			b.cols = append(b.cols, merge(by))
			st.Columns = append(st.Columns, Column{Label: periodLabel(p), From: p.From, To: p.To})
		}
	}
	st.Lines = c.ProfitLossLines(b.cols)
	return st, nil
}

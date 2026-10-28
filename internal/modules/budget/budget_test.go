package budget_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/budget"
	"lajur/internal/modules/core"
	"lajur/internal/modules/fixture"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

var day, rp = fixture.Day, fixture.Rp

func code(err error) string {
	var p *httpx.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

func spread(n int64) []money.Amount {
	out := make([]money.Amount, 12)
	for i := range out {
		out[i] = rp(n)
	}
	return out
}

func post(t *testing.T, e *fixture.Env, date string, dept *uuid.UUID, debit, credit string, amount int64) {
	t.Helper()
	if _, err := e.Ledger.Post(e.Ctx, ledger.EntryDraft{
		CompanyID: e.Company, Kind: ledger.General, Date: day(date), Memo: "uji", Actor: e.Actor,
		Lines: []ledger.LineDraft{
			{AccountID: e.Acc[debit], Debit: rp(amount), DepartmentID: dept},
			{AccountID: e.Acc[credit], Credit: rp(amount), DepartmentID: dept},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func row(t *testing.T, c budget.Comparison, accountCode string) budget.Row {
	t.Helper()
	for _, s := range c.Sections {
		for _, r := range s.Rows {
			if r.Code == accountCode {
				return r
			}
		}
	}
	t.Fatalf("akun %s tidak ada di laporan", accountCode)
	return budget.Row{}
}

// BUD-01/02: anggaran per versi dan irisan, laporan anggaran vs realisasi dengan status batas,
// revisi yang menyalin versi lain, dan usulan dari realisasi tahun lalu.
func TestBudgetVersionsAndComparison(t *testing.T) {
	e := fixture.New(t)
	svc := e.Budget
	dept, err := e.Core.SaveDimension(e.Ctx, e.Company, "departemen", nil, core.DimensionInput{Code: "OPS", Name: "Operasional", Active: true}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}

	awal, err := svc.CreateBudget(e.Ctx, e.Company, budget.NewBudget{FiscalYear: 2026, Name: "Awal"}, e.Actor)
	if err != nil || !awal.Active {
		t.Fatalf("versi pertama seharusnya langsung aktif: %+v, %v", awal, err)
	}
	if _, err := svc.CreateBudget(e.Ctx, e.Company, budget.NewBudget{FiscalYear: 2026, Name: "Awal"}, e.Actor); code(err) != "budget.name_taken" {
		t.Fatalf("nama versi kembar seharusnya ditolak: %v", err)
	}

	// Tingkat PT: pendapatan 1 jt/bulan, listrik 500 rb/bulan. Departemen: sewa 200 rb/bulan.
	d, err := svc.SaveSlice(e.Ctx, e.Company, awal.ID, budget.SliceInput{Version: awal.Version, Lines: []budget.LineInput{
		{AccountID: e.Acc["4-1001"], Months: spread(1_000_000)},
		{AccountID: e.Acc["6-2002"], Months: spread(500_000)},
		{AccountID: e.Acc["6-2001"], Months: make([]money.Amount, 12)}, // semua nol: tidak disimpan
	}}, e.Actor)
	if err != nil || len(d.Lines) != 2 || !d.Lines[0].Total.Add(d.Lines[1].Total).Equal(rp(18_000_000)) || d.Months[0] != "2026-01" {
		t.Fatalf("simpan anggaran PT = %+v, %v", d, err)
	}
	d, err = svc.SaveSlice(e.Ctx, e.Company, awal.ID, budget.SliceInput{DepartmentID: &dept.ID, Version: d.Version, Lines: []budget.LineInput{
		{AccountID: e.Acc["6-2001"], Months: spread(200_000)},
	}}, e.Actor)
	if err != nil || len(d.Lines) != 3 {
		t.Fatalf("simpan anggaran departemen = %+v, %v", d, err)
	}
	if _, err := svc.SaveSlice(e.Ctx, e.Company, awal.ID, budget.SliceInput{Version: d.Version - 1}, e.Actor); code(err) != "conflict.version" {
		t.Fatalf("versi basi seharusnya ditolak: %v", err)
	}
	if _, err := svc.SaveSlice(e.Ctx, e.Company, awal.ID, budget.SliceInput{Version: d.Version, Lines: []budget.LineInput{
		{AccountID: e.Acc["1-1111"], Months: spread(1)},
	}}, e.Actor); code(err) != "budget.invalid" {
		t.Fatalf("akun neraca seharusnya ditolak: %v", err)
	}
	neg := spread(1)
	neg[3] = rp(-1)
	if _, err := svc.SaveSlice(e.Ctx, e.Company, awal.ID, budget.SliceInput{Version: d.Version, Lines: []budget.LineInput{
		{AccountID: e.Acc["4-1001"], Months: neg},
	}}, e.Actor); code(err) != "budget.invalid" {
		t.Fatalf("anggaran negatif seharusnya ditolak: %v", err)
	}

	// Realisasi Oktober: pendapatan 10 jt, listrik 5,6 jt (93% dari 6 jt setahun), sewa departemen
	// 2,5 jt (> 2,4 jt setahun), dan beban tanpa anggaran.
	post(t, e, "2026-10-05", nil, "1-1111", "4-1001", 10_000_000)
	post(t, e, "2026-10-06", nil, "6-2002", "1-1111", 5_600_000)
	post(t, e, "2026-10-07", &dept.ID, "6-2001", "1-1111", 2_500_000)
	post(t, e, "2026-10-08", nil, "6-2003", "1-1111", 75_000)

	c, err := svc.Compare(e.Ctx, e.Company, budget.CompareFilter{FiscalYear: 2026, FromMonth: 10, ToMonth: 10})
	if err != nil || c.Budget == nil || c.Budget.ID != awal.ID || c.From != "2026-10" || c.To != "2026-10" {
		t.Fatalf("laporan = %+v, %v", c, err)
	}
	if r := row(t, c, "4-1001"); !r.Budget.Equal(rp(1_000_000)) || !r.Actual.Equal(rp(10_000_000)) || !r.Variance.Equal(rp(9_000_000)) || r.Status != "" {
		t.Fatalf("pendapatan = %+v", r)
	}
	if r := row(t, c, "6-2002"); !r.Budget.Equal(rp(500_000)) || !r.Actual.Equal(rp(5_600_000)) || !r.YearBudget.Equal(rp(6_000_000)) ||
		!r.Remaining.Equal(rp(400_000)) || r.Status != "near" || r.Pct == nil || *r.Pct != 1120 {
		t.Fatalf("listrik = %+v", r)
	}
	if r := row(t, c, "6-2001"); r.Status != "over" || !r.YearBudget.Equal(rp(2_400_000)) {
		t.Fatalf("sewa departemen = %+v", r)
	}
	if r := row(t, c, "6-2003"); r.Status != "unbudgeted" || !r.Budget.IsZero() {
		t.Fatalf("beban tanpa anggaran = %+v", r)
	}
	if c.Near != 1 || c.Over != 2 || !c.Revenue.Actual.Equal(rp(10_000_000)) || !c.Expense.Actual.Equal(rp(8_175_000)) ||
		!c.Profit.Budget.Equal(rp(300_000)) || !c.Profit.Actual.Equal(rp(1_825_000)) {
		t.Fatalf("ringkasan = near %d over %d, %+v %+v %+v", c.Near, c.Over, c.Revenue, c.Expense, c.Profit)
	}
	if m := c.Monthly[9]; m.Month != "2026-10" || !m.ActualRevenue.Equal(rp(10_000_000)) || !m.BudgetExpense.Equal(rp(700_000)) {
		t.Fatalf("bulan Oktober = %+v", m)
	}

	// Irisan departemen: hanya anggaran dan realisasi departemen itu.
	c, err = svc.Compare(e.Ctx, e.Company, budget.CompareFilter{FiscalYear: 2026, FromMonth: 1, ToMonth: 12, Slice: budget.Slice{DepartmentID: &dept.ID}})
	if err != nil || len(c.Sections) != 1 || len(c.Sections[0].Rows) != 1 || !c.Expense.Budget.Equal(rp(2_400_000)) || !c.Expense.Actual.Equal(rp(2_500_000)) {
		t.Fatalf("laporan departemen = %+v, %v", c, err)
	}

	// Revisi menyalin versi awal, lalu diaktifkan; versi aktif tidak bisa dihapus.
	rev, err := svc.CreateBudget(e.Ctx, e.Company, budget.NewBudget{FiscalYear: 2026, Name: "Revisi 1", CopyFrom: &awal.ID}, e.Actor)
	if err != nil || rev.Active || rev.Lines != 3 {
		t.Fatalf("revisi = %+v, %v", rev, err)
	}
	if _, err := svc.ActivateBudget(e.Ctx, e.Company, rev.ID, e.Actor); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteBudget(e.Ctx, e.Company, rev.ID, e.Actor); code(err) != "budget.active" {
		t.Fatalf("versi aktif seharusnya tidak bisa dihapus: %v", err)
	}
	if err := svc.DeleteBudget(e.Ctx, e.Company, awal.ID, e.Actor); err != nil {
		t.Fatalf("versi lama seharusnya bisa dihapus: %v", err)
	}
	list, _ := svc.Budgets(e.Ctx, e.Company)
	if len(list) != 1 || list[0].ID != rev.ID || !list[0].Active {
		t.Fatalf("daftar versi = %+v", list)
	}

	// Usulan 2027 dari realisasi 2026 + 10%.
	lines, err := svc.Suggest(e.Ctx, e.Company, 2027, budget.Slice{}, rp(10))
	if err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]budget.LineInput{}
	for _, l := range lines {
		got[l.AccountID] = l
	}
	if l := got[e.Acc["4-1001"]]; len(l.Months) != 12 || !l.Months[9].Equal(rp(11_000_000)) || !l.Months[0].IsZero() {
		t.Fatalf("usulan pendapatan = %+v", l)
	}
	if l := got[e.Acc["6-2002"]]; !l.Months[9].Equal(rp(6_160_000)) {
		t.Fatalf("usulan listrik = %+v", l)
	}
	if _, ok := got[e.Acc["1-1111"]]; ok {
		t.Fatal("akun neraca tidak boleh diusulkan")
	}
	// Tahun buku tanpa anggaran: laporan tetap jalan dengan realisasi saja.
	c, err = svc.Compare(e.Ctx, e.Company, budget.CompareFilter{FiscalYear: 2025, FromMonth: 1, ToMonth: 12})
	if err != nil || c.Budget != nil || len(c.Sections) != 0 {
		t.Fatalf("tahun tanpa anggaran = %+v, %v", c, err)
	}
}

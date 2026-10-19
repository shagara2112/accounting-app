package report_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/report"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
)

func branch(t *testing.T, e *fixture.Env, code, name string) uuid.UUID {
	t.Helper()
	d, err := e.Core.SaveDimension(e.Ctx, e.Company, core.DimBranch, nil, core.DimensionInput{Code: code, Name: name}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func sell(t *testing.T, e *fixture.Env, b *uuid.UUID, amount int64) sales.InvoiceDetail {
	t.Helper()
	d, err := e.Sales.CreateInvoice(e.Ctx, e.Company, sales.InvoiceInput{CustomerID: e.Customer, Date: day("2026-10-05"), BranchID: b, Lines: []trade.LineInput{{
		Description: "Jasa", AccountID: e.Acc["4-1001"], Qty: money.FromInt(1), UnitPrice: rp(amount), TaxCode: "PPN",
	}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Sales.PostInvoice(e.Ctx, e.Company, d.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// GL-07, GL-09, PLT-12: nomor faktur per cabang dengan format sendiri, laba rugi per cabang,
// dan pengguna yang dibatasi ke satu cabang hanya melihat dan membuat dokumen cabangnya.
func TestBranchesNumberingAndRestriction(t *testing.T) {
	e := fixture.New(t)
	jkt, sby := branch(t, e, "JKT", "Jakarta"), branch(t, e, "SBY", "Surabaya")
	if _, err := e.Core.SaveDimension(e.Ctx, e.Company, core.DimBranch, nil, core.DimensionInput{Code: "JKT", Name: "Lain"}, e.Actor); code(err) != "dimension.duplicate" {
		t.Fatalf("kode cabang ganda seharusnya ditolak: %v", err)
	}
	if _, err := e.Ledger.SaveNumberFormat(e.Ctx, e.Company, ledger.NumberFormat{Series: "FJ", Prefix: "FB", Reset: ledger.ResetYear, Digits: 5}, e.Actor); code(err) != "number.prefix_taken" {
		t.Fatalf("awalan milik seri lain seharusnya ditolak: %v", err)
	}
	if _, err := e.Ledger.SaveNumberFormat(e.Ctx, e.Company, ledger.NumberFormat{Series: "FJ", Prefix: "INV", Reset: ledger.ResetYear, Digits: 5, PerBranch: true}, e.Actor); err != nil {
		t.Fatal(err)
	}
	a, b, c := sell(t, e, &jkt, 10_000_000), sell(t, e, &sby, 4_000_000), sell(t, e, nil, 1_000_000)
	if *a.Number != "INV/JKT/2026/00001" || *b.Number != "INV/SBY/2026/00001" || *c.Number != "INV/2026/00001" {
		t.Fatalf("nomor = %s, %s, %s", *a.Number, *b.Number, *c.Number)
	}
	j, err := e.Ledger.Entry(e.Ctx, e.Company, *a.JournalEntryID)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range j.Lines {
		if l.BranchID == nil || *l.BranchID != jkt || l.BranchCode != "JKT" {
			t.Fatalf("baris jurnal tanpa cabang: %+v", l)
		}
	}

	st, err := e.Report.ProfitLossBy(e.Ctx, e.Company, []report.Period{{From: day("2026-10-01"), To: day("2026-10-31")}}, report.DimFilter{PerBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Columns) != 4 || st.Columns[0].Label != "JKT Jakarta" || st.Columns[2].Label != "Tanpa cabang" {
		t.Fatalf("kolom laba rugi per cabang = %+v", st.Columns)
	}
	rev := line(t, st, "Jumlah pendapatan usaha")
	if !rev.Values[0].Equal(rp(10_000_000)) || !rev.Values[1].Equal(rp(4_000_000)) || !rev.Values[3].Equal(rp(15_000_000)) {
		t.Fatalf("pendapatan per cabang = %v", rev.Values)
	}

	// Pengguna yang dibatasi ke Jakarta.
	role, err := e.Core.Role(e.Ctx, e.Actor.UserID, e.Company)
	if err != nil {
		t.Fatal(err)
	}
	role.Branches = []uuid.UUID{jkt}
	ctx := rbac.WithScope(e.Ctx, rbac.Scope{CompanyID: e.Company, Role: role})
	list, err := e.Sales.Invoices(ctx, e.Company, sales.InvoiceFilter{From: day("2026-10-01"), To: day("2026-10-31")})
	if err != nil || len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("faktur yang terlihat = %+v (%v)", list, err)
	}
	if _, err := e.Sales.Invoice(ctx, e.Company, b.ID); code(err) != "invoice.not_found" {
		t.Fatalf("faktur cabang lain seharusnya tidak terlihat: %v", err)
	}
	if _, err := e.Sales.CreateInvoice(ctx, e.Company, sales.InvoiceInput{CustomerID: e.Customer, Date: day("2026-10-06"), BranchID: &sby, Lines: []trade.LineInput{{
		Description: "Jasa", AccountID: e.Acc["4-1001"], Qty: money.FromInt(1), UnitPrice: rp(1), TaxCode: "PPN",
	}}}, e.Actor); code(err) != "branch.restricted" {
		t.Fatalf("faktur untuk cabang lain seharusnya ditolak: %v", err)
	}
	own, err := e.Report.ProfitLossBy(ctx, e.Company, []report.Period{{From: day("2026-10-01"), To: day("2026-10-31")}}, report.DimFilter{Branches: role.Branches})
	if err != nil {
		t.Fatal(err)
	}
	if got := line(t, own, "Jumlah pendapatan usaha"); !got.Values[0].Equal(rp(10_000_000)) {
		t.Fatalf("laba rugi cabang sendiri = %v", got.Values)
	}
	_ = strings.TrimSpace
}

// GL-08: jurnal akrual dijadikan berulang tiap akhir bulan; jadwal yang terlewat dikejar
// dengan tanggal aslinya dan tidak pernah diposting dua kali.
func TestRecurringJournals(t *testing.T) {
	e := fixture.New(t)
	posted, err := e.Ledger.Post(e.Ctx, ledger.EntryDraft{
		CompanyID: e.Company, Kind: ledger.General, Date: day("2026-08-31"), Memo: "Akrual sewa kantor",
		Lines: []ledger.LineDraft{{AccountID: e.Acc["6-2001"], Debit: rp(5_000_000)}, {AccountID: e.Acc["2-1301"], Credit: rp(5_000_000)}},
		Actor: e.Actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.Ledger.CreateRecurring(e.Ctx, e.Company, ledger.RecurringInput{EntryID: posted.ID, Name: "Sewa kantor", DayOfMonth: 0, StartDate: day("2026-09-01")}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if rc.NextDate.String() != "2026-09-30" || len(rc.Lines) != 2 || !rc.Total.Equal(rp(5_000_000)) {
		t.Fatalf("template = %+v", rc)
	}
	n, err := e.Ledger.RunDueRecurring(e.Ctx, day("2026-11-15"))
	if err != nil || n != 2 {
		t.Fatalf("jadwal terposting = %d (%v)", n, err)
	}
	if n, _ := e.Ledger.RunDueRecurring(e.Ctx, day("2026-11-15")); n != 0 {
		t.Fatalf("jadwal tidak boleh diposting ulang, terposting %d", n)
	}
	rc, _ = e.Ledger.RecurringJournal(e.Ctx, e.Company, rc.ID)
	if rc.NextDate.String() != "2026-11-30" || len(rc.Runs) != 2 || rc.Runs[0].Date.String() != "2026-10-31" {
		t.Fatalf("template setelah jalan = %+v", rc)
	}
	if !e.Net("6-2001", day("2026-10-31")).Equal(rp(15_000_000)) {
		t.Fatalf("beban sewa = %s", e.Net("6-2001", day("2026-10-31")))
	}
}

// GL-09: laba rugi satu kolom per departemen (termasuk Tanpa departemen), kolom membawa saringan
// untuk tautan buku besar, dan buku besar bisa disaring per departemen.
func TestProfitLossPerDepartmentAndLedgerFilter(t *testing.T) {
	e := fixture.New(t)
	dept := func(code, name string) uuid.UUID {
		d, err := e.Core.SaveDimension(e.Ctx, e.Company, core.DimDepartment, nil, core.DimensionInput{Code: code, Name: name}, e.Actor)
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	ops, mkt := dept("OPS", "Operasional"), dept("MKT", "Pemasaran")
	sellTo := func(d *uuid.UUID, amount int64) {
		inv, err := e.Sales.CreateInvoice(e.Ctx, e.Company, sales.InvoiceInput{CustomerID: e.Customer, Date: day("2026-10-05"), DepartmentID: d, Lines: []trade.LineInput{{
			Description: "Jasa", AccountID: e.Acc["4-1001"], Qty: money.FromInt(1), UnitPrice: rp(amount), TaxCode: "PPN",
		}}}, e.Actor)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Sales.PostInvoice(e.Ctx, e.Company, inv.ID, e.Actor); err != nil {
			t.Fatal(err)
		}
	}
	sellTo(&ops, 10_000_000)
	sellTo(&mkt, 4_000_000)
	sellTo(nil, 1_000_000)
	oct := []report.Period{{From: day("2026-10-01"), To: day("2026-10-31")}}

	st, err := e.Report.ProfitLossBy(e.Ctx, e.Company, oct, report.DimFilter{PerDepartment: true})
	if err != nil {
		t.Fatal(err)
	}
	// Kolom urut kode departemen: MKT, OPS, lalu Tanpa departemen dan Jumlah.
	if len(st.Columns) != 4 || st.Columns[1].Label != "OPS Operasional" || st.Columns[2].Label != "Tanpa departemen" {
		t.Fatalf("kolom laba rugi per departemen = %+v", st.Columns)
	}
	if st.Columns[1].Department != ops.String() || st.Columns[2].Department != "none" || st.Columns[3].Department != "" {
		t.Fatalf("saringan kolom = %+v", st.Columns)
	}
	rev := line(t, st, "Jumlah pendapatan usaha")
	if !rev.Values[0].Equal(rp(4_000_000)) || !rev.Values[1].Equal(rp(10_000_000)) || !rev.Values[2].Equal(rp(1_000_000)) || !rev.Values[3].Equal(rp(15_000_000)) {
		t.Fatalf("pendapatan per departemen = %v", rev.Values)
	}

	one, err := e.Report.ProfitLossBy(e.Ctx, e.Company, oct, report.DimFilter{DepartmentID: &mkt})
	if err != nil {
		t.Fatal(err)
	}
	if one.Columns[0].Department != mkt.String() || !line(t, one, "Jumlah pendapatan usaha").Values[0].Equal(rp(4_000_000)) {
		t.Fatalf("laba rugi departemen pemasaran = %+v", one.Columns)
	}
	if _, err := e.Report.ProfitLossBy(e.Ctx, e.Company, oct, report.DimFilter{PerBranch: true, PerDepartment: true}); code(err) != "report.columns" {
		t.Fatalf("kolom per cabang dan per departemen sekaligus seharusnya ditolak: %v", err)
	}

	ledgerOf := func(dim ledger.LedgerDim) ledger.AccountLedger {
		l, err := e.Ledger.AccountLedgerBy(e.Ctx, e.Company, e.Acc["4-1001"], day("2026-10-01"), day("2026-10-31"), dim)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if l := ledgerOf(ledger.LedgerDim{DepartmentID: &ops}); !l.Closing.Equal(rp(10_000_000)) || len(l.Lines) != 1 || l.Lines[0].DepartmentID == nil || *l.Lines[0].DepartmentID != ops {
		t.Fatalf("buku besar OPS = %+v", l)
	}
	if l := ledgerOf(ledger.LedgerDim{NoDepartment: true}); !l.Closing.Equal(rp(1_000_000)) || len(l.Lines) != 1 {
		t.Fatalf("buku besar tanpa departemen = %+v", l)
	}
	if l := ledgerOf(ledger.LedgerDim{}); !l.Closing.Equal(rp(15_000_000)) || len(l.Lines) != 3 {
		t.Fatalf("buku besar semua departemen = %+v", l)
	}
}

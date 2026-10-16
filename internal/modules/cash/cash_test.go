package cash_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"lajur/internal/modules/cash"
	"lajur/internal/modules/core"
	"lajur/internal/modules/fixture"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

// CB-02, CB-03: penerimaan, pengeluaran, dan transfer membentuk jurnal yang benar, dan void
// mengembalikan saldo rekening.
func TestCashTransactions(t *testing.T) {
	e := fixture.New(t)
	in, err := e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{
		Kind: "in", Date: day("2026-10-01"), CashAccountID: e.Bank, Memo: "Setoran modal",
		Lines: []cash.LineInput{{AccountID: e.Acc["3-1001"], Amount: rp(50_000_000)}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if in.Number == nil || *in.Number != "BKM/2026/10/0001" {
		t.Fatalf("nomor penerimaan = %v", in.Number)
	}
	out, err := e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{
		Kind: "out", Date: day("2026-10-31"), CashAccountID: e.Bank, Memo: "Mutasi bank Oktober",
		Lines: []cash.LineInput{
			{AccountID: e.Acc["8-1001"], Amount: rp(25_000), Memo: "Biaya admin"},
			{AccountID: e.Acc["8-1004"], Amount: rp(3_000), Memo: "Pajak jasa giro"},
		},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if out.Number == nil || *out.Number != "BKK/2026/10/0001" || !out.Amount.Equal(rp(28_000)) || len(out.Lines) != 2 {
		t.Fatalf("pengeluaran = %+v", out)
	}
	to := e.Petty
	trf, err := e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{
		Kind: "transfer", Date: day("2026-10-05"), CashAccountID: e.Bank, ToAccountID: &to, Amount: rp(2_000_000),
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	end := day("2026-10-31")
	if !e.Net("1-1111", end).Equal(rp(47_972_000)) || !e.Net("1-1101", end).Equal(rp(2_000_000)) {
		t.Fatalf("saldo bank %s, kas kecil %s", e.Net("1-1111", end), e.Net("1-1101", end))
	}

	accounts, err := e.Cash.Accounts(e.Ctx, e.Company)
	if err != nil || len(accounts) != 2 {
		t.Fatalf("rekening = %+v, %v", accounts, err)
	}

	if _, err := e.Cash.Void(e.Ctx, e.Company, trf.ID, day("2026-10-06"), "Salah rekening tujuan", e.Actor); err != nil {
		t.Fatal(err)
	}
	if !e.Net("1-1101", end).IsZero() {
		t.Fatalf("kas kecil setelah void transfer = %s", e.Net("1-1101", end))
	}
	if _, err := e.Cash.Void(e.Ctx, e.Company, trf.ID, day("2026-10-06"), "Kedua kali", e.Actor); code(err) != "cash.already_void" {
		t.Fatalf("void kedua seharusnya ditolak: %v", err)
	}
	e.VerifyLedger()
}

func TestCashRejectsUnregisteredAndInvalidAccounts(t *testing.T) {
	e := fixture.New(t)
	_, err := e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{
		Kind: "in", Date: day("2026-10-01"), CashAccountID: e.Acc["1-1112"],
		Lines: []cash.LineInput{{AccountID: e.Acc["7-1001"], Amount: rp(1000)}},
	}, e.Actor)
	if code(err) != "cash.not_cash_account" || fixture.Fields(err)["cashAccountId"] == "" {
		t.Fatalf("rekening belum terdaftar seharusnya ditolak: %v", err)
	}
	_, err = e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{
		Kind: "out", Date: day("2026-10-01"), CashAccountID: e.Bank,
		Lines: []cash.LineInput{{AccountID: e.Acc["6-2000"], Amount: rp(1000)}},
	}, e.Actor)
	if fixture.Fields(err)["lines.0.accountId"] == "" {
		t.Fatalf("akun header seharusnya ditolak per baris: %v", err)
	}
	_, err = e.Cash.RegisterAccount(e.Ctx, e.Company, cash.AccountInput{AccountID: e.Acc["4-1001"], Kind: "bank", Active: true}, e.Actor)
	if code(err) != "cash.invalid" {
		t.Fatalf("akun pendapatan tidak boleh jadi rekening bank: %v", err)
	}
}

// Draf: staf mencatat pengeluaran tanpa akun; akuntan melengkapi akunnya lalu memposting.
// Selama draf, tidak ada nomor, jurnal, maupun perubahan saldo bank.
func TestCashDrafts(t *testing.T) {
	e := fixture.New(t)
	staf, akuntan := e.Other, e.Actor
	end := day("2026-10-31")
	in := cash.TransactionInput{
		Kind: "out", Date: day("2026-10-08"), CashAccountID: e.Bank, Memo: "Pengeluaran kasir",
		Lines: []cash.LineInput{{Amount: rp(100_000), Memo: "Bensin operasional"}, {Amount: rp(50_000), Memo: "Fotokopi"}},
	}
	d, err := e.Cash.SaveDraft(e.Ctx, e.Company, nil, in, 0, staf)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "draft" || d.Number != nil || d.JournalEntryID != nil || !d.Amount.Equal(rp(150_000)) || d.Lines[0].AccountID != nil {
		t.Fatalf("draf = %+v", d)
	}
	if !e.Net("1-1111", end).IsZero() {
		t.Fatal("draf tidak boleh mengubah saldo bank")
	}
	if n, amount, err := e.Cash.DraftSummary(e.Ctx, e.Company); err != nil || n != 1 || !amount.Equal(rp(150_000)) {
		t.Fatalf("ringkasan draf = %d %s %v", n, amount, err)
	}
	if _, err := e.Cash.PostDraft(e.Ctx, e.Company, d.ID, akuntan); code(err) != "cash.invalid" || fixture.Fields(err)["lines.0.accountId"] == "" {
		t.Fatalf("draf tanpa akun seharusnya tidak bisa diposting: %v", err)
	}

	// Akuntan (bukan pembuat) melengkapi akun lalu memposting.
	in.Lines[0].AccountID, in.Lines[1].AccountID = e.Acc["6-2005"], e.Acc["6-2004"]
	d, err = e.Cash.SaveDraft(e.Ctx, e.Company, &d.ID, in, d.Version, akuntan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Cash.SaveDraft(e.Ctx, e.Company, &d.ID, in, d.Version-1, akuntan); code(err) != "conflict.version" {
		t.Fatalf("versi lama seharusnya ditolak: %v", err)
	}
	p, err := e.Cash.PostDraft(e.Ctx, e.Company, d.ID, akuntan)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "posted" || p.Number == nil || *p.Number != "BKK/2026/10/0001" || p.JournalEntryID == nil || p.PostedBy == nil {
		t.Fatalf("draf diposting = %+v", p)
	}
	if !e.Net("1-1111", end).Equal(rp(-150_000)) || !e.Net("6-2005", end).Equal(rp(100_000)) {
		t.Fatal("jurnal draf yang diposting salah")
	}
	if _, err := e.Cash.SaveDraft(e.Ctx, e.Company, &d.ID, in, p.Version, akuntan); code(err) != "cash.not_draft" {
		t.Fatalf("transaksi diposting tidak boleh diubah: %v", err)
	}
	if err := e.Cash.DeleteDraft(e.Ctx, e.Company, d.ID, akuntan, true); code(err) != "cash.not_draft" {
		t.Fatalf("transaksi diposting tidak boleh dihapus: %v", err)
	}

	// Draf hanya boleh dihapus pembuatnya (atau Manager/Pemilik).
	d2, err := e.Cash.SaveDraft(e.Ctx, e.Company, nil, cash.TransactionInput{
		Kind: "in", Date: day("2026-10-09"), CashAccountID: e.Petty, Lines: []cash.LineInput{{Amount: rp(20_000)}},
	}, 0, staf)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Cash.DeleteDraft(e.Ctx, e.Company, d2.ID, akuntan, false); code(err) != "cash.not_owner" {
		t.Fatalf("draf orang lain seharusnya tidak bisa dihapus: %v", err)
	}
	list, err := e.Cash.Transactions(e.Ctx, e.Company, cash.Filter{DraftsOnly: true})
	if err != nil || len(list) != 1 || list[0].ID != d2.ID {
		t.Fatalf("daftar draf = %+v (%v)", list, err)
	}

	// Basis data menolak memposting draf yang barisnya belum berakun.
	if _, err := e.Pool.Exec(context.Background(),
		"UPDATE cash.transactions SET status = 'posted', number = 'X', journal_entry_id = $1 WHERE id = $2", *p.JournalEntryID, d2.ID); err == nil {
		t.Fatal("basis data seharusnya menolak posting draf tanpa akun")
	}
	if _, err := e.Pool.Exec(context.Background(),
		"INSERT INTO cash.transaction_lines (id, company_id, transaction_id, line_no, account_id, amount) VALUES ($1, $2, $3, 9, $4, 1)",
		uuid.Must(uuid.NewV7()), e.Company, d.ID, e.Acc["6-2005"]); err == nil {
		t.Fatal("basis data seharusnya menolak baris baru di transaksi yang sudah diposting")
	}
	if err := e.Cash.DeleteDraft(e.Ctx, e.Company, d2.ID, staf, false); err != nil {
		t.Fatal(err)
	}
	e.VerifyLedger()
}

// Revisi klien: departemen dipilih per baris pengeluaran kas, termasuk di draf yang dilengkapi
// akuntan. Baris tanpa departemen memakai departemen kepala transaksi.
func TestCashLineDepartments(t *testing.T) {
	e := fixture.New(t)
	dept := func(code, name string) uuid.UUID {
		d, err := e.Core.SaveDimension(e.Ctx, e.Company, core.DimDepartment, nil, core.DimensionInput{Code: code, Name: name}, e.Actor)
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	ops, mkt, adm := dept("OPS", "Operasional"), dept("MKT", "Pemasaran"), dept("ADM", "Administrasi")
	in := cash.TransactionInput{
		Kind: "out", Date: day("2026-10-10"), CashAccountID: e.Bank, Memo: "Listrik Oktober", DepartmentID: &adm,
		Lines: []cash.LineInput{
			{Amount: rp(600_000), Memo: "Gudang", DepartmentID: &ops},
			{Amount: rp(400_000), Memo: "Showroom", DepartmentID: &mkt},
			{Amount: rp(100_000), Memo: "Kantor"},
		},
	}
	d, err := e.Cash.SaveDraft(e.Ctx, e.Company, nil, in, 0, e.Other)
	if err != nil {
		t.Fatal(err)
	}
	if d.Lines[0].DepartmentID == nil || *d.Lines[0].DepartmentID != ops || d.Lines[2].DepartmentID != nil {
		t.Fatalf("departemen baris draf = %+v", d.Lines)
	}
	for i := range in.Lines {
		in.Lines[i].AccountID = e.Acc["6-2002"]
	}
	if d, err = e.Cash.SaveDraft(e.Ctx, e.Company, &d.ID, in, d.Version, e.Actor); err != nil {
		t.Fatal(err)
	}
	p, err := e.Cash.PostDraft(e.Ctx, e.Company, d.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.Ledger.Entry(e.Ctx, e.Company, *p.JournalEntryID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range j.Lines {
		got[l.Memo] = l.DepartmentCode
	}
	if got["Gudang"] != "OPS" || got["Showroom"] != "MKT" || got["Kantor"] != "ADM" || got["Listrik Oktober"] != "ADM" {
		t.Fatalf("departemen baris jurnal = %v", got)
	}

	// Departemen nonaktif ditolak dengan pesan di baris kas yang benar.
	if _, err := e.Core.SaveDimension(e.Ctx, e.Company, core.DimDepartment, &mkt, core.DimensionInput{Code: "MKT", Name: "Pemasaran", Active: false, Version: 1}, e.Actor); err != nil {
		t.Fatal(err)
	}
	in.DepartmentID = nil
	if _, err := e.Cash.Record(e.Ctx, e.Company, in, e.Actor); code(err) != "cash.invalid" || fixture.Fields(err)["lines.1.departmentId"] == "" {
		t.Fatalf("departemen nonaktif seharusnya ditolak di baris 2: %v", err)
	}
	e.VerifyLedger()
}

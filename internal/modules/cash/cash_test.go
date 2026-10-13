package cash_test

import (
	"testing"

	"lajur/internal/modules/cash"
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
	if in.Number != "BKM/2026/10/0001" {
		t.Fatalf("nomor penerimaan = %s", in.Number)
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
	if out.Number != "BKK/2026/10/0001" || !out.Amount.Equal(rp(28_000)) || len(out.Lines) != 2 {
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

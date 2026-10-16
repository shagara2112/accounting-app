package cash_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"lajur/internal/modules/cash"
	"lajur/internal/modules/fixture"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

func record(t *testing.T, e *fixture.Env, kind, date, memo string, amount int64, counter string) cash.TransactionDetail {
	t.Helper()
	d, err := e.Cash.Record(e.Ctx, e.Company, cash.TransactionInput{
		Kind: kind, Date: day(date), CashAccountID: e.Bank, Memo: memo,
		Lines: []cash.LineInput{{AccountID: e.Acc[counter], Amount: rp(amount)}},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func problem(t *testing.T, err error, code string) {
	t.Helper()
	var p *httpx.Problem
	if !errors.As(err, &p) || p.Code != code {
		t.Fatalf("galat = %v, seharusnya %s", err, code)
	}
}

func lineOf(t *testing.T, d cash.ReconDetail, number string) uuid.UUID {
	t.Helper()
	for _, l := range d.Lines {
		if l.Number == number {
			return l.ID
		}
	}
	t.Fatalf("baris %s tidak ada di rekonsiliasi: %+v", number, d.Lines)
	return uuid.Nil
}

// CB-04, CB-05: centang mutasi sesuai rekening koran, selisih harus nol untuk ditutup, dan
// mutasi yang belum muncul di koran terbawa ke bulan berikutnya.
func TestBankReconciliation(t *testing.T) {
	e := fixture.New(t)
	dep := record(t, e, "in", "2026-10-05", "Setoran modal", 1_000_000, "3-1001")
	chk := record(t, e, "out", "2026-10-10", "Cek sewa kantor", 250_000, "6-2001")
	wrong := record(t, e, "out", "2026-10-12", "Salah catat", 100_000, "6-2001")
	if _, err := e.Cash.Void(e.Ctx, e.Company, wrong.ID, day("2026-10-12"), "Salah rekening", e.Actor); err != nil {
		t.Fatal(err)
	}
	late := record(t, e, "in", "2026-11-03", "Setoran November", 400_000, "3-1001")

	_, err := e.Cash.StartReconciliation(e.Ctx, e.Company, cash.ReconInput{AccountID: e.Petty, StatementDate: day("2026-10-31")}, e.Actor)
	if err != nil {
		t.Fatal(err) // kas kecil pun boleh direkonsiliasi dengan hitungan fisik
	}
	oct, err := e.Cash.StartReconciliation(e.Ctx, e.Company, cash.ReconInput{
		AccountID: e.Bank, StatementDate: day("2026-10-31"), StatementBalance: rp(1_000_000), OpeningBalance: money.Amount{},
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if !oct.FirstRecon || len(oct.Lines) != 2 {
		t.Fatalf("calon baris Oktober (void dan pembaliknya harus tersembunyi) = %+v", oct.Lines)
	}
	_, err = e.Cash.StartReconciliation(e.Ctx, e.Company, cash.ReconInput{AccountID: e.Bank, StatementDate: day("2026-11-30")}, e.Actor)
	problem(t, err, "recon.open_exists")

	_, err = e.Cash.CloseReconciliation(e.Ctx, e.Company, oct.ID, e.Actor)
	problem(t, err, "recon.difference")

	oct, err = e.Cash.SetCleared(e.Ctx, e.Company, oct.ID, []uuid.UUID{lineOf(t, oct, *dep.Number)}, true, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if !oct.Difference.IsZero() || !oct.BookBalance.Equal(rp(750_000)) || !oct.Outstanding.Equal(rp(250_000)) || !oct.InTransit.IsZero() {
		t.Fatalf("rekap Oktober = selisih %s, buku %s, belum cair %s", oct.Difference, oct.BookBalance, oct.Outstanding)
	}
	// Baris rekening lain atau setelah tanggal koran tidak bisa dicentang.
	_, err = e.Cash.SetCleared(e.Ctx, e.Company, oct.ID, []uuid.UUID{uuid.Must(uuid.NewV7())}, true, e.Actor)
	problem(t, err, "recon.line")

	oct, err = e.Cash.CloseReconciliation(e.Ctx, e.Company, oct.ID, e.Actor)
	if err != nil || oct.Status != "closed" {
		t.Fatalf("tutup Oktober = %v %v", oct.Status, err)
	}
	_, err = e.Cash.SetCleared(e.Ctx, e.Company, oct.ID, []uuid.UUID{lineOf(t, oct, *chk.Number)}, true, e.Actor)
	problem(t, err, "recon.closed")
	if _, err := e.Pool.Exec(e.Ctx, "DELETE FROM cash.recon_items WHERE reconciliation_id = $1", oct.ID); err == nil {
		t.Fatal("trigger harus menolak perubahan rekonsiliasi tertutup")
	}

	// November: saldo awal = saldo koran Oktober; cek Oktober terbawa dan kini cair.
	_, err = e.Cash.StartReconciliation(e.Ctx, e.Company, cash.ReconInput{AccountID: e.Bank, StatementDate: day("2026-10-15")}, e.Actor)
	problem(t, err, "validation")
	nov, err := e.Cash.StartReconciliation(e.Ctx, e.Company, cash.ReconInput{
		AccountID: e.Bank, StatementDate: day("2026-11-30"), StatementBalance: rp(1_150_000), OpeningBalance: rp(999),
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if nov.FirstRecon || !nov.OpeningBalance.Equal(rp(1_000_000)) || len(nov.Lines) != 2 {
		t.Fatalf("November = awal %s, baris %+v", nov.OpeningBalance, nov.Lines)
	}
	nov, err = e.Cash.SetCleared(e.Ctx, e.Company, nov.ID, []uuid.UUID{lineOf(t, nov, *chk.Number), lineOf(t, nov, *late.Number)}, true, e.Actor)
	if err != nil || !nov.Difference.IsZero() {
		t.Fatalf("selisih November = %s %v", nov.Difference, err)
	}
	if _, err := e.Cash.CloseReconciliation(e.Ctx, e.Company, nov.ID, e.Actor); err != nil {
		t.Fatal(err)
	}
	_, err = e.Cash.ReopenReconciliation(e.Ctx, e.Company, oct.ID, e.Actor)
	problem(t, err, "recon.not_latest")
	nov, err = e.Cash.ReopenReconciliation(e.Ctx, e.Company, nov.ID, e.Actor)
	if err != nil || nov.Status != "open" || nov.Items != 2 {
		t.Fatalf("buka kembali November = %+v %v", nov.Status, err)
	}

	table := cash.ReconTable(oct)
	if len(table.Rows) < 6 {
		t.Fatalf("laporan rekonsiliasi = %+v", table.Rows)
	}
}

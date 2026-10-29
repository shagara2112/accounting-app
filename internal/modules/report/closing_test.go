package report_test

import (
	"strings"
	"testing"

	"lajur/internal/ledger"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/report"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// hasLine: laporan memuat baris bernama name dengan nilai tidak nol di kolom pertama.
func hasLine(st report.Statement, name string) bool {
	for _, l := range st.Lines {
		if (l.Name == name || l.Code+" "+l.Name == name) && len(l.Values) > 0 && !l.Values[0].IsZero() {
			return true
		}
	}
	return false
}

func balanceSheet(t *testing.T, e *fixture.Env, date string) report.Statement {
	t.Helper()
	st, err := e.Report.BalanceSheet(e.Ctx, e.Company, []civil.Date{day(date)})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Balanced[0] {
		t.Fatalf("neraca %s tidak seimbang", date)
	}
	return st
}

func trialRow(tb report.TrialBalance, name string) (money.Amount, bool) {
	for _, r := range tb.Rows {
		if r.Name == name || r.Code+" "+r.Name == name {
			return r.Debit.Sub(r.Credit), true
		}
	}
	return money.Amount{}, false
}

// GL-10: reklas laba akhir tahun ke laba ditahan bisa diperbarui sesudah revisi dan dibatalkan
// selama bulan terakhir tahun buku belum dikunci final. Laporan tahun itu tidak berubah; mulai
// tahun buku berikutnya laba itu menjadi saldo laba ditahan.
func TestYearEndReclass(t *testing.T) {
	e := fixture.New(t)
	journal(t, e, "2025-01-10", ledger.General, "1-1111", "3-1001", 100_000_000)
	journal(t, e, "2025-06-01", ledger.General, "1-1111", "4-1001", 30_000_000)
	journal(t, e, "2025-07-01", ledger.General, "6-2002", "1-1111", 12_000_000)
	journal(t, e, "2026-03-01", ledger.General, "1-1111", "4-1001", 5_000_000)

	y, err := e.Ledger.YearEnd(e.Ctx, e.Company, 2025)
	if err != nil || y.Status != ledger.YearOpen || !y.Ended || !y.Profit.Equal(rp(18_000_000)) || y.Account == nil || y.Account.Number != "3-2001" {
		t.Fatalf("status 2025 = %+v, %v", y, err)
	}
	if _, err := e.Ledger.CloseYear(e.Ctx, e.Company, 2026, e.Actor); code(err) != "year_end.not_ended" {
		t.Fatalf("tahun buku berjalan seharusnya belum bisa direklas: %v", err)
	}
	if y, _ := e.Ledger.YearEnd(e.Ctx, e.Company, 2024); y.Status != ledger.YearEmpty {
		t.Fatalf("tahun tanpa transaksi = %+v", y)
	}
	if st := balanceSheet(t, e, "2026-06-30"); !line(t, st, "Laba (rugi) tahun-tahun lalu yang belum ditutup").Values[0].Equal(rp(18_000_000)) {
		t.Fatalf("sebelum reklas, laba 2025 tampil sebagai laba tahun lalu yang belum ditutup: %+v", st.Lines)
	}

	y, err = e.Ledger.CloseYear(e.Ctx, e.Company, 2025, e.Actor)
	if err != nil || y.Status != ledger.YearClosed || y.Closing == nil || !strings.HasPrefix(y.Closing.Number, "TB") || !y.Closing.Profit.Equal(rp(18_000_000)) {
		t.Fatalf("reklas 2025 = %+v, %v", y, err)
	}
	first := y.Closing
	if _, err := e.Ledger.CloseYear(e.Ctx, e.Company, 2025, e.Actor); code(err) != "year_end.closed" {
		t.Fatalf("reklas yang masih sesuai tidak perlu diulang: %v", err)
	}
	if _, err := e.Ledger.Void(e.Ctx, e.Company, first.ID, day("2026-01-05"), "salah reklas", e.Actor); code(err) != "journal.void_via_source" {
		t.Fatalf("jurnal reklas hanya dibatalkan lewat tutup buku: %v", err)
	}

	// Laporan tahun 2025 tidak berubah: laba rugi utuh, laba tahun berjalan per 31 Des tetap tampil.
	pl, err := e.Report.ProfitLoss(e.Ctx, e.Company, []report.Period{{From: day("2025-01-01"), To: day("2025-12-31")}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "laba bersih 2025", line(t, pl, "Laba (rugi) bersih").Values[0], 18_000_000)
	st := balanceSheet(t, e, "2025-12-31")
	eq(t, "laba tahun berjalan per 31 Des 2025", line(t, st, "Laba (rugi) tahun berjalan").Values[0], 18_000_000)
	if hasLine(st, "3-2001 Laba ditahan") {
		t.Fatal("reklas 2025 belum masuk laba ditahan per 31 Des 2025")
	}
	ws, err := e.Ledger.Worksheet(e.Ctx, e.Company, day("2025-12-01"))
	if err != nil || !ws.Balanced || !ws.NetIncome.Equal(rp(18_000_000)) {
		t.Fatalf("neraca lajur Desember 2025 = laba %s, seimbang %v, %v", ws.NetIncome, ws.Balanced, err)
	}

	// Mulai 2026 laba 2025 sudah menjadi laba ditahan.
	st = balanceSheet(t, e, "2026-06-30")
	eq(t, "laba ditahan 2026", line(t, st, "3-2001 Laba ditahan").Values[0], 18_000_000)
	eq(t, "laba tahun berjalan 2026", line(t, st, "Laba (rugi) tahun berjalan").Values[0], 5_000_000)
	if hasLine(st, "Laba (rugi) tahun-tahun lalu yang belum ditutup") {
		t.Fatal("sesudah reklas tidak ada laba tahun lalu yang belum ditutup")
	}
	tb, err := e.Report.TrialBalance(e.Ctx, e.Company, day("2026-06-30"))
	if err != nil || !tb.Balanced {
		t.Fatalf("neraca saldo 2026 = %+v, %v", tb, err)
	}
	if v, _ := trialRow(tb, "3-2001 Laba ditahan"); !v.Equal(rp(-18_000_000)) {
		t.Fatalf("laba ditahan di neraca saldo = %s", v)
	}
	if v, _ := trialRow(tb, "4-1001 Pendapatan jasa"); !v.Equal(rp(-5_000_000)) {
		t.Fatalf("pendapatan di neraca saldo 2026 hanya tahun berjalan: %s", v)
	}
	if _, ok := trialRow(tb, "Laba (rugi) tahun-tahun lalu yang belum ditutup"); ok {
		t.Fatal("neraca saldo 2026 tidak punya laba tahun lalu yang belum ditutup")
	}
	if ws, err := e.Ledger.Worksheet(e.Ctx, e.Company, day("2026-01-01")); err != nil || !ws.Balanced {
		t.Fatalf("neraca lajur Januari 2026 seimbang = %v, %v", ws.Balanced, err)
	}

	// Revisi sesudah reklas: koreksi beban 2 jt di Desember 2025 (periode ditutup untuk transaksi).
	if err := e.Ledger.SetPeriodStatus(e.Ctx, e.Company, day("2025-12-01"), "adjusting", "", e.Actor); err != nil {
		t.Fatal(err)
	}
	journal(t, e, "2025-12-31", ledger.Adjustment, "6-2002", "2-1101", 2_000_000)
	y, _ = e.Ledger.YearEnd(e.Ctx, e.Company, 2025)
	if y.Status != ledger.YearChanged || !y.Profit.Equal(rp(16_000_000)) || !y.Closing.Profit.Equal(rp(18_000_000)) {
		t.Fatalf("sesudah revisi = %+v", y)
	}
	eq(t, "selisih revisi sebelum diperbarui", line(t, balanceSheet(t, e, "2026-06-30"), "Laba (rugi) tahun-tahun lalu yang belum ditutup").Values[0], -2_000_000)

	y, err = e.Ledger.CloseYear(e.Ctx, e.Company, 2025, e.Actor)
	if err != nil || y.Status != ledger.YearClosed || y.Closing.ID == first.ID || !y.Closing.Profit.Equal(rp(16_000_000)) {
		t.Fatalf("perbarui reklas = %+v, %v", y, err)
	}
	if old, _ := e.Ledger.Entry(e.Ctx, e.Company, first.ID); old.Status != "void" {
		t.Fatalf("jurnal reklas lama seharusnya void: %+v", old.EntrySummary)
	}
	st = balanceSheet(t, e, "2026-06-30")
	eq(t, "laba ditahan sesudah diperbarui", line(t, st, "3-2001 Laba ditahan").Values[0], 16_000_000)
	if hasLine(st, "Laba (rugi) tahun-tahun lalu yang belum ditutup") {
		t.Fatal("sesudah diperbarui tidak ada sisa laba tahun lalu")
	}

	// Kunci final menahan perubahan reklas; batal reklas wajib beralasan.
	if err := e.Ledger.SetPeriodStatus(e.Ctx, e.Company, day("2025-12-01"), "locked", "", e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ledger.ReopenYear(e.Ctx, e.Company, 2025, "revisi audit", e.Actor); code(err) != "period.locked" {
		t.Fatalf("batal reklas di periode terkunci: %v", err)
	}
	if err := e.Ledger.SetPeriodStatus(e.Ctx, e.Company, day("2025-12-01"), "adjusting", "Revisi auditor", e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ledger.ReopenYear(e.Ctx, e.Company, 2025, "x", e.Actor); code(err) != "journal.void_reason" {
		t.Fatalf("batal reklas tanpa alasan: %v", err)
	}
	y, err = e.Ledger.ReopenYear(e.Ctx, e.Company, 2025, "Menunggu koreksi auditor", e.Actor)
	if err != nil || y.Status != ledger.YearOpen || y.Closing != nil {
		t.Fatalf("batal reklas = %+v, %v", y, err)
	}
	st = balanceSheet(t, e, "2026-06-30")
	eq(t, "kembali sebagai laba tahun lalu", line(t, st, "Laba (rugi) tahun-tahun lalu yang belum ditutup").Values[0], 16_000_000)
	if hasLine(st, "3-2001 Laba ditahan") {
		t.Fatal("sesudah dibatalkan laba ditahan kembali kosong")
	}
	if _, err := e.Ledger.ReopenYear(e.Ctx, e.Company, 2025, "Menunggu koreksi auditor", e.Actor); code(err) != "year_end.not_closed" {
		t.Fatalf("batal reklas yang tidak ada: %v", err)
	}

	// Akun yang dinonaktifkan sesudah reklas (saldonya nol) tidak menghalangi batal dan reklas ulang.
	if _, err := e.Ledger.CloseYear(e.Ctx, e.Company, 2025, e.Actor); err != nil {
		t.Fatal(err)
	}
	accounts, err := e.Ledger.Accounts(e.Ctx, e.Company)
	if err != nil {
		t.Fatal(err)
	}
	var acc ledger.Account
	for _, a := range accounts {
		if a.Code == "6-2002" {
			acc = a
		}
	}
	if _, err := e.Ledger.UpdateAccount(e.Ctx, e.Company, acc.ID, ledger.AccountChange{Name: acc.Name, Active: false, Version: acc.Version}, e.Actor); err != nil {
		t.Fatal(err)
	}
	if y, err := e.Ledger.ReopenYear(e.Ctx, e.Company, 2025, "Revisi sesudah akun dinonaktifkan", e.Actor); err != nil || y.Status != ledger.YearOpen {
		t.Fatalf("batal reklas dengan akun nonaktif = %+v, %v", y, err)
	}
	if y, err := e.Ledger.CloseYear(e.Ctx, e.Company, 2025, e.Actor); err != nil || y.Status != ledger.YearClosed {
		t.Fatalf("reklas dengan akun nonaktif = %+v, %v", y, err)
	}
}

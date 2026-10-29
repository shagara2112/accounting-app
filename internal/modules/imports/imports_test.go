package imports_test

import (
	"strings"
	"testing"

	"lajur/internal/ledger"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/imports"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/money"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

func run(t *testing.T, e *fixture.Env, kind, csv string, o imports.Options) imports.Result {
	t.Helper()
	res, err := e.Imports.Import(e.Ctx, e.Company, kind, kind+".csv", []byte(csv), o, e.Actor)
	if err != nil {
		t.Fatalf("impor %s: %v", kind, err)
	}
	return res
}

func ok(t *testing.T, res imports.Result) {
	t.Helper()
	if !res.Applied || res.Errors > 0 {
		t.Fatalf("impor %s gagal: %+v", res.Kind, res.Issues)
	}
}

// GL-06: migrasi PT retail dari aplikasi lama — kontak, produk, neraca saldo, piutang, utang,
// dan stok awal. Setelah semuanya diimpor, akun ekuitas saldo awal kembali nol.
func TestOpeningMigration(t *testing.T) {
	e := fixture.NewWith(t, "retail")
	cut := imports.Options{Date: day("2026-09-30")}

	// Pratinjau tidak menyimpan apa pun; baris rusak membatalkan seluruh berkas.
	contacts := "Nama*,Jenis*,NPWP,PKP,Termin (hari)\nToko Sumber Rejeki,pelanggan,,tidak,14\nPT Grosir Abadi,pemasok,0102030405060708,ya,30\nPT Pelanggan Uji,pelanggan,,,\n"
	pre := run(t, e, "kontak", contacts, imports.Options{DryRun: true})
	if pre.Applied || pre.Errors > 0 || len(pre.Issues) != 1 || !pre.Issues[0].Warning || pre.Summary[0] != "2 kontak baru" {
		t.Fatalf("pratinjau kontak = %+v", pre)
	}
	bad := run(t, e, "kontak", contacts+"CV Salah,teman,,,\n", imports.Options{})
	if bad.Applied || bad.Errors != 1 || bad.Issues[1].Row != 5 || bad.Issues[1].Column != "Jenis" {
		t.Fatalf("berkas dengan baris rusak = %+v", bad)
	}
	ok(t, run(t, e, "kontak", contacts, imports.Options{}))

	ok(t, run(t, e, "produk", "Kode*,Nama*,Jenis*,Satuan*,Harga jual,Akun pendapatan,Akun HPP/beban,Akun persediaan\n"+
		"BRG-1,Beras 5 kg,barang,karung,75.000,4-1001,5-1001,1-1302\nJSA-1,Ongkos kirim,jasa,kali,10000,4-1002,,\n", imports.Options{}))

	// Neraca saldo lama: kas 10 jt, piutang 3,33 jt (dilewati), persediaan 2 jt (dilewati),
	// utang 1,11 jt (dilewati), modal 14,22 jt.
	tb := run(t, e, "saldo-awal", "Kode akun*,Nama akun,Debit,Kredit\n1-1101,Kas kecil,10.000.000,\n1-1201,Piutang usaha,3.330.000,\n"+
		"1-1302,Persediaan,2.000.000,\n2-1101,Utang usaha,,1.110.000\n3-1001,Modal disetor,,14.220.000\n", cut)
	ok(t, tb)
	if len(tb.Issues) != 3 || !strings.HasPrefix(tb.Summary[0], "Jurnal saldo awal SA/2026/09/0001") {
		t.Fatalf("neraca saldo awal = %+v", tb)
	}
	if again := run(t, e, "saldo-awal", "Kode akun*,Debit,Kredit\n1-1101,1,\n", cut); again.Applied {
		t.Fatal("neraca saldo awal kedua seharusnya ditolak")
	}

	ok(t, run(t, e, "piutang-awal", "Pelanggan*,Nomor faktur*,Tanggal faktur*,Jatuh tempo,Sisa piutang*\n"+
		"Toko Sumber Rejeki,INV-0901,01/09/2026,15/09/2026,2.220.000\nPT Pelanggan Uji,INV-0920,20/09/2026,,1.110.000\n", imports.Options{}))
	ok(t, run(t, e, "utang-awal", "Pemasok*,Nomor faktur pemasok*,Tanggal tagihan*,Sisa utang*\n0102.0304.0506.0708,GA-77,25/09/2026,1110000\n", imports.Options{}))
	ok(t, run(t, e, "stok-awal", "Kode produk*,Kuantitas*,Biaya satuan*\nBRG-1,40,50.000\n", cut))

	st, err := e.Imports.Status(e.Ctx, e.Company)
	if err != nil {
		t.Fatal(err)
	}
	if !st.OpeningAccount.Balance.IsZero() || len(st.OpeningEntries) != 1 {
		t.Fatalf("status saldo awal = %+v", st)
	}
	if !e.Net("1-1201", day("2026-09-30")).Equal(rp(3_330_000)) || !e.Net("2-1101", day("2026-09-30")).Equal(rp(-1_110_000)) ||
		!e.Net("1-1302", day("2026-09-30")).Equal(rp(2_000_000)) {
		t.Fatalf("piutang %s, utang %s, persediaan %s tidak sesuai neraca saldo lama",
			e.Net("1-1201", day("2026-09-30")), e.Net("2-1101", day("2026-09-30")), e.Net("1-1302", day("2026-09-30")))
	}

	// Faktur saldo awal ikut umur piutang dan bisa dilunasi, tetapi bukan penjualan.
	aging, err := e.Sales.Aging(e.Ctx, e.Company, day("2026-10-01"))
	if err != nil || !aging.Total.Equal(rp(3_330_000)) {
		t.Fatalf("umur piutang = %+v (%v)", aging, err)
	}
	open, _ := e.Sales.OpenInvoices(e.Ctx, e.Company, nil)
	if len(open) != 2 || open[0].Kind != sales.KindOpening || !strings.HasPrefix(*open[0].Number, "SAP/2026/09/") {
		t.Fatalf("faktur saldo awal = %+v", open)
	}
	if _, err := e.Sales.CreateReceipt(e.Ctx, e.Company, sales.ReceiptInput{CustomerID: open[0].CustomerID, Date: day("2026-10-02"), CashAccountID: e.Petty,
		Allocations: []sales.AllocationInput{{InvoiceID: open[0].ID, Amount: open[0].Outstanding}}}, e.Actor); err != nil {
		t.Fatal(err)
	}
	if rows, _ := e.Sales.ByCustomer(e.Ctx, e.Company, day("2026-01-01"), day("2026-12-31")); len(rows) != 0 {
		t.Fatalf("faktur saldo awal tidak boleh masuk ringkasan penjualan: %+v", rows)
	}
	e.VerifyLedger()
}

// Halaman Saldo awal: saldo per akun diketik di layar (termasuk pendapatan dan beban sejak awal
// tahun untuk PT yang mulai di tengah tahun), bisa diubah selama periodenya terbuka, dan pindah
// bulan mem-void jurnal lama. Akun piutang, utang, dan persediaan tetap diisi per dokumen.
func TestOpeningForm(t *testing.T) {
	e := fixture.New(t)
	st, err := e.Imports.Opening(e.Ctx, e.Company)
	if err != nil {
		t.Fatal(err)
	}
	if st.EntryID != nil || len(st.Controlled) < 2 {
		t.Fatalf("saldo awal kosong = %+v", st)
	}
	line := func(code string, debit, credit int64) imports.OpeningLine {
		return imports.OpeningLine{AccountID: e.Acc[code], Debit: rp(debit), Credit: rp(credit)}
	}
	save := func(date string, revision int, lines ...imports.OpeningLine) (imports.OpeningState, error) {
		return e.Imports.SaveOpening(e.Ctx, e.Company, imports.OpeningInput{Date: day(date), Lines: lines, Revision: revision}, e.Actor)
	}

	for _, c := range st.Controlled {
		if (c.Kind == "stok-awal") == c.Manual {
			t.Fatalf("hanya piutang dan utang usaha yang boleh diketik langsung: %+v", c)
		}
		if c.Kind != "stok-awal" {
			continue
		}
		// Persediaan tetap per barang lewat impor (kartu stok dan HPP).
		_, err = save("2026-09-30", 0, imports.OpeningLine{AccountID: c.AccountID, Debit: rp(1_000_000)}, line("3-1001", 0, 1_000_000))
		if code(err) != "opening.invalid" || fixture.Fields(err)["lines."+c.AccountID.String()] == "" {
			t.Fatalf("persediaan seharusnya ditolak: %v", err)
		}
	}

	// Mulai Oktober: neraca per 30 September plus pendapatan dan beban Januari–September.
	// Piutang dan utang usaha boleh diketik langsung, tanpa rincian faktur (revisi klien).
	st, err = save("2026-09-30", 0,
		line("1-1111", 250_000_000, 0), line("1-1201", 15_000_000, 0), line("2-1101", 0, 15_000_000),
		line("3-1001", 0, 200_000_000), line("4-1001", 0, 80_000_000), line("6-2001", 30_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if st.EntryID == nil || st.Number != "SA/2026/09/0001" || len(st.Lines) != 6 || !st.OpeningEquity.IsZero() {
		t.Fatalf("saldo awal = %+v", st)
	}
	if !e.Net("1-1201", day("2026-09-30")).Equal(rp(15_000_000)) || !e.Net("2-1101", day("2026-09-30")).Equal(rp(-15_000_000)) {
		t.Fatalf("piutang/utang diketik langsung: %s, %s", e.Net("1-1201", day("2026-09-30")), e.Net("2-1101", day("2026-09-30")))
	}
	if !e.Net("4-1001", day("2026-12-31")).Equal(rp(-80_000_000)) {
		t.Fatalf("pendapatan setahun = %s", e.Net("4-1001", day("2026-12-31")))
	}

	// Koreksi: bank 260 jt; selisihnya sementara ke ekuitas saldo awal. Versi lama ditolak.
	if _, err := save("2026-09-30", st.Revision+1, line("1-1111", 260_000_000, 0)); code(err) != "conflict.version" {
		t.Fatalf("versi basi seharusnya ditolak: %v", err)
	}
	st, err = save("2026-09-30", st.Revision,
		line("1-1111", 260_000_000, 0), line("3-1001", 0, 200_000_000),
		line("4-1001", 0, 80_000_000), line("6-2001", 30_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if st.Number != "SA/2026/09/0001" || st.Revision != 1 || !st.OpeningEquity.Equal(rp(-10_000_000)) {
		t.Fatalf("saldo awal sesudah koreksi = %+v", st)
	}
	if _, err := e.Ledger.Revise(e.Ctx, e.Company, *st.EntryID, ledger.ReviseInput{Date: day("2026-09-30"), Lines: nil, Revision: st.Revision}, e.Actor); code(err) != "journal.edit_opening" {
		t.Fatalf("jurnal saldo awal tidak boleh diubah lewat formulir jurnal: %v", err)
	}

	// Pindah ke 31 Agustus: jurnal September di-void, jurnal baru diposting.
	old := *st.EntryID
	st, err = save("2026-08-31", st.Revision, line("1-1111", 200_000_000, 0), line("3-1001", 0, 200_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if st.Number != "SA/2026/08/0001" || st.Date.String() != "2026-08-31" || *st.EntryID == old {
		t.Fatalf("saldo awal pindah bulan = %+v", st)
	}
	if prev, _ := e.Ledger.Entry(e.Ctx, e.Company, old); prev.Status != "void" {
		t.Fatalf("saldo awal lama seharusnya di-void: %s", prev.Status)
	}
	if !e.Net("1-1111", day("2026-09-30")).Equal(rp(200_000_000)) || !e.Net("4-1001", day("2026-12-31")).IsZero() {
		t.Fatalf("saldo sesudah pindah bulan: bank %s, pendapatan %s", e.Net("1-1111", day("2026-09-30")), e.Net("4-1001", day("2026-12-31")))
	}
	e.VerifyLedger()
}

// Skema kode PT sendiri: kode akun sistem (piutang usaha, PPN keluaran) diganti, posting
// otomatis faktur tetap memakai akun yang sama.
func TestRecodedSystemAccountsKeepPosting(t *testing.T) {
	e := fixture.New(t)
	recode := func(old, code string) {
		list, err := e.Ledger.Accounts(e.Ctx, e.Company)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range list {
			if a.Code == old {
				if _, err := e.Ledger.UpdateAccount(e.Ctx, e.Company, a.ID, ledger.AccountChange{Code: code, Name: a.Name, Active: true, Version: a.Version}, e.Actor); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatalf("akun %s tidak ada", old)
	}
	recode("1-1201", "1-13101")
	recode("2-1201", "2-12101")

	inv, err := e.Sales.CreateInvoice(e.Ctx, e.Company, sales.InvoiceInput{CustomerID: e.Customer, Date: day("2026-10-05"),
		Lines: []trade.LineInput{{Description: "Jasa", AccountID: e.Acc["4-1001"], Qty: money.FromInt(1), UnitPrice: rp(10_000_000), TaxCode: "PPN"}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	posted, err := e.Sales.PostInvoice(e.Ctx, e.Company, inv.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.Ledger.Entry(e.Ctx, e.Company, *posted.JournalEntryID)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, l := range j.Lines {
		codes[l.AccountCode] = true
	}
	if !codes["1-13101"] || !codes["2-12101"] {
		t.Fatalf("jurnal faktur memakai akun %v, seharusnya kode baru 1-13101 dan 2-12101", codes)
	}
	e.VerifyLedger()
}

// Impor jurnal: baris dengan tanggal dan nomor bukti sama menjadi satu jurnal; jurnal yang
// tidak seimbang menandai baris-barisnya dan membatalkan seluruh berkas.
func TestImportJournals(t *testing.T) {
	e := fixture.New(t)
	head := "Tanggal*,Nomor bukti*,Kode akun*,Keterangan,Debit,Kredit\n"
	good := "01/10/2026,BK-1,6-2008,Honor konsultan,2.000.000,\n01/10/2026,BK-1,1-1111,,,2.000.000\n02/10/2026,BK-2,6-2008,Honor,500000,\n02/10/2026,BK-2,1-1101,,,500000\n"
	bad := run(t, e, "jurnal", head+good+"03/10/2026,BK-3,6-2008,Selisih,100,\n03/10/2026,BK-3,1-1101,,,90\n", imports.Options{})
	if bad.Applied || bad.Errors == 0 || bad.Issues[0].Row != 6 {
		t.Fatalf("jurnal tidak seimbang = %+v", bad)
	}
	res := run(t, e, "jurnal", head+good, imports.Options{})
	ok(t, res)
	if res.Summary[0] != "2 jurnal diposting" || !e.Net("6-2008", day("2026-10-31")).Equal(rp(2_500_000)) {
		t.Fatalf("hasil impor jurnal = %+v", res)
	}
}

// IND-07, TAX-12: rekap penjualan harian per tanggal dan toko; harga termasuk PPN dipecah
// menjadi DPP dan PPN digunggung, barang keluar dari stok, dan PPN-nya masuk rekap masa.
func TestImportDailySales(t *testing.T) {
	e := fixture.NewWith(t, "retail")
	ok(t, run(t, e, "produk", "Kode*,Nama*,Jenis*,Satuan*,Akun pendapatan,Akun HPP/beban,Akun persediaan\nBRG-1,Gula 1 kg,barang,pcs,4-1001,5-1001,1-1302\n", imports.Options{}))
	ok(t, run(t, e, "stok-awal", "Kode produk*,Kuantitas*,Biaya satuan*\nBRG-1,100,12000\n", imports.Options{Date: day("2026-09-30")}))

	csv := "Tanggal*,Toko,Kode produk,Kuantitas*,Nilai penjualan*,Dibayar ke*\n" +
		"01/10/2026,Pusat,BRG-1,10,166.500,1-1103\n01/10/2026,Pusat,BRG-1,2,33.300,1-1203\n02/10/2026,Pusat,BRG-1,5,83.250,1-1103\n"
	res := run(t, e, "penjualan-harian", csv, imports.Options{})
	ok(t, res)
	if res.Summary[0] != "2 rekap harian, total Rp 283.050" {
		t.Fatalf("ringkasan = %+v", res.Summary)
	}
	list, err := e.Sales.DailySales(e.Ctx, e.Company, day("2026-10-01"), day("2026-10-31"))
	if err != nil || len(list) != 2 {
		t.Fatalf("rekap = %+v (%v)", list, err)
	}
	first := list[1] // urut tanggal turun
	// 199.800 termasuk PPN 11% efektif → DPP 180.000, PPN 19.800.
	if first.Number != "RPH/2026/10/0001" || !first.DPP.Equal(rp(180_000)) || !first.VAT.Equal(rp(19_800)) {
		t.Fatalf("rekap 1 Oktober = %+v", first)
	}
	if !e.Net("1-1103", day("2026-10-31")).Equal(rp(249_750)) || !e.Net("1-1203", day("2026-10-31")).Equal(rp(33_300)) ||
		!e.Net("5-1001", day("2026-10-31")).Equal(rp(17*12_000)) {
		t.Fatal("kas toko, piutang QRIS, atau HPP salah")
	}
	vat, _ := e.Sales.VATByMonth(e.Ctx, e.Company, day("2026-10-01"), day("2026-11-01"), sales.VATCodeOverrides{})
	if len(vat) != 1 || !vat[0].VAT.Equal(rp(19_800+8_250)) {
		t.Fatalf("PPN keluaran Oktober = %+v", vat)
	}

	// Rekap tanggal dan toko yang sama ditolak; setelah di-void boleh diimpor ulang.
	if again := run(t, e, "penjualan-harian", csv, imports.Options{}); again.Applied || again.Errors == 0 {
		t.Fatalf("rekap ganda seharusnya ditolak: %+v", again)
	}
	if _, err := e.Sales.VoidDailySale(e.Ctx, e.Company, first.ID, day("2026-10-03"), "Salah toko", e.Actor); err != nil {
		t.Fatal(err)
	}
	if !e.Net("5-1001", day("2026-10-31")).Equal(rp(5 * 12_000)) {
		t.Fatalf("HPP setelah void = %s", e.Net("5-1001", day("2026-10-31")))
	}
	rows, _ := e.Sales.ByCustomer(e.Ctx, e.Company, day("2026-10-01"), day("2026-10-31"))
	if len(rows) != 1 || rows[0].ID != nil || !rows[0].DPP.Equal(rp(75_000)) {
		t.Fatalf("ringkasan penjualan eceran = %+v", rows)
	}
	_ = money.Amount{}
	e.VerifyLedger()
}

// TAX-12: rekap PPN digunggung satu masa dari rekap harian yang diposting.
func TestDigunggungRecap(t *testing.T) {
	e := fixture.NewWith(t, "retail")
	ok(t, run(t, e, "penjualan-harian", "Tanggal*,Toko,Akun pendapatan,Kuantitas*,Nilai penjualan*,Dibayar ke*\n"+
		"01/10/2026,A,4-1001,1,111.000,1-1103\n01/10/2026,B,4-1001,1,222.000,1-1103\n", imports.Options{}))
	d, err := e.Coretax.Digunggung(e.Ctx, e.Company, day("2026-10-01"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Month != "2026-10" || len(d.Rows) != 2 || !d.Totals.DPP.Equal(rp(300_000)) || !d.Totals.VAT.Equal(rp(33_000)) {
		t.Fatalf("rekap digunggung = %+v", d)
	}
}

package asset_test

import (
	"testing"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/asset"
	"lajur/internal/modules/fixture"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

var day, rp, code = fixture.Day, fixture.Rp, fixture.Code

func month(s string) civil.Date {
	d, err := civil.ParseMonth(s)
	if err != nil {
		panic(err)
	}
	return d
}

// buy menjurnal perolehan aset dari bank, seperti tagihan atau pengeluaran kas.
func buy(t *testing.T, e *fixture.Env, account string, amount int64, date string) {
	t.Helper()
	if _, err := e.Ledger.Post(e.Ctx, ledger.EntryDraft{
		CompanyID: e.Company, Kind: ledger.General, Date: day(date), Memo: "Pembelian aset", Actor: e.Actor,
		Lines: []ledger.LineDraft{{AccountID: e.Acc[account], Debit: rp(amount)}, {AccountID: e.Acc["1-1111"], Credit: rp(amount)}},
	}); err != nil {
		t.Fatal(err)
	}
}

func category(t *testing.T, cats []asset.Category, name string) uuid.UUID {
	t.Helper()
	for _, c := range cats {
		if c.Name == name {
			return c.ID
		}
	}
	t.Fatalf("kategori %s tidak ada: %+v", name, cats)
	return uuid.Nil
}

// FA-01–03: aset didaftarkan, disusutkan bulanan (termasuk susulan), dijual dengan laba,
// pelepasannya dibatalkan, lalu dihapus dengan rugi. Register selalu cocok dengan buku besar.
func TestFixedAssetLifecycle(t *testing.T) {
	e := fixture.New(t)
	cats, err := e.Asset.DefaultCategories(e.Ctx, e.Company, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	office := category(t, cats, "Peralatan kantor")
	buy(t, e, "1-2104", 24_000_000, "2026-01-10")
	laptop, err := e.Asset.CreateAsset(e.Ctx, e.Company, asset.AssetInput{
		Name: "Laptop tim keuangan", CategoryID: office, AcquiredOn: day("2026-01-10"), Cost: rp(24_000_000),
		Location: "Kantor pusat", Custodian: "Rina",
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if laptop.Code != "AT-0001" || laptop.LifeMonths != 48 || laptop.Method != asset.Straight || laptop.FiscalGroup != "kelompok_1" ||
		laptop.NextAmount.Cmp(rp(500_000)) != 0 || laptop.NextMonth == nil || *laptop.NextMonth != month("2026-01") {
		t.Fatalf("aset baru = %+v", laptop)
	}
	if rec, err := e.Asset.Reconciliation(e.Ctx, e.Company); err != nil || !rec.Matched {
		t.Fatalf("register seharusnya cocok dengan buku besar: %+v %v", rec, err)
	}

	pv, err := e.Asset.PreviewRun(e.Ctx, e.Company, month("2026-03"))
	if err != nil || len(pv.Lines) != 1 || pv.Lines[0].Months != 3 || !pv.Total.Equal(rp(1_500_000)) {
		t.Fatalf("pratinjau Maret = %+v %v", pv, err)
	}
	mar, err := e.Asset.PostRun(e.Ctx, e.Company, month("2026-03"), e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.Ledger.Entry(e.Ctx, e.Company, mar.JournalEntryID)
	if err != nil {
		t.Fatal(err)
	}
	if mar.Number != "PNY/2026/03/0001" || j.Kind != ledger.Adjustment || j.Date.String() != "2026-03-31" || !mar.Total.Equal(rp(1_500_000)) {
		t.Fatalf("jurnal penyusutan = %+v / %+v", mar, j)
	}
	end := day("2026-12-31")
	if !e.Net("6-3004", end).Equal(rp(1_500_000)) || !e.Net("1-2204", end).Equal(rp(-1_500_000)) {
		t.Fatalf("beban %s, akumulasi %s", e.Net("6-3004", end), e.Net("1-2204", end))
	}
	if _, err := e.Asset.PostRun(e.Ctx, e.Company, month("2026-03"), e.Actor); code(err) != "depreciation.exists" {
		t.Fatalf("posting ganda seharusnya ditolak: %v", err)
	}
	apr, err := e.Asset.PostRun(e.Ctx, e.Company, month("2026-04"), e.Actor)
	if err != nil || !apr.Total.Equal(rp(500_000)) {
		t.Fatalf("April = %+v %v", apr, err)
	}

	// Sesudah dijurnal, nilai terkunci; nama dan lokasi tetap boleh diubah.
	d, err := e.Asset.Asset(e.Ctx, e.Company, laptop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Locked || !d.Accumulated.Equal(rp(2_000_000)) || len(d.Posted) != 4 {
		t.Fatalf("aset sesudah penyusutan = %+v", d)
	}
	in := asset.AssetInput{
		Code: d.Code, Name: d.Name, CategoryID: d.CategoryID, AcquiredOn: d.AcquiredOn, Cost: rp(25_000_000), Residual: d.Residual,
		Method: d.Method, LifeMonths: d.LifeMonths, DepreciationStart: d.DepreciationStart, Location: d.Location, Version: d.Version,
	}
	if _, err := e.Asset.UpdateAsset(e.Ctx, e.Company, laptop.ID, in, e.Actor); code(err) != "asset.locked" {
		t.Fatalf("harga perolehan seharusnya terkunci: %v", err)
	}
	in.Cost, in.Location = d.Cost, "Lantai 2"
	if d, err = e.Asset.UpdateAsset(e.Ctx, e.Company, laptop.ID, in, e.Actor); err != nil || d.Location != "Lantai 2" {
		t.Fatalf("lokasi = %+v %v", d.Location, err)
	}

	// Void April: bulan itu disusutkan lagi di jurnal berikutnya.
	if _, err := e.Asset.VoidRun(e.Ctx, e.Company, apr.ID, civil.Date{}, "Salah bulan", e.Actor); err != nil {
		t.Fatal(err)
	}
	if d, _ = e.Asset.Asset(e.Ctx, e.Company, laptop.ID); !d.Accumulated.Equal(rp(1_500_000)) {
		t.Fatalf("akumulasi sesudah void April = %s", d.Accumulated)
	}

	// Dijual 15 Juni: penyusutan April–Juni ikut di jurnal pelepasan; laba Rp 1 juta.
	bank := e.Acc["1-1111"]
	d, err = e.Asset.Dispose(e.Ctx, e.Company, laptop.ID, asset.DisposalInput{
		Date: day("2026-06-15"), Kind: asset.DisposeSale, Proceeds: rp(22_000_000), ProceedsAccountID: &bank,
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	sale := d.Disposals[0]
	if d.Status != asset.StatusDisposed || !sale.Accumulated.Equal(rp(3_000_000)) || !sale.BookValue.Equal(rp(21_000_000)) ||
		!sale.Gain.Equal(rp(1_000_000)) || sale.Number != "PAT/2026/06/0001" {
		t.Fatalf("pelepasan = %+v", sale)
	}
	if !e.Net("1-2104", end).IsZero() || !e.Net("1-2204", end).IsZero() || !e.Net("7-1003", end).Equal(rp(-1_000_000)) ||
		!e.Net("6-3004", end).Equal(rp(3_000_000)) {
		t.Fatalf("aset %s, akumulasi %s, laba %s, beban %s", e.Net("1-2104", end), e.Net("1-2204", end), e.Net("7-1003", end), e.Net("6-3004", end))
	}
	if _, err := e.Asset.PostRun(e.Ctx, e.Company, month("2026-07"), e.Actor); code(err) != "depreciation.nothing_due" {
		t.Fatalf("aset yang dilepas tidak disusutkan lagi: %v", err)
	}
	if _, err := e.Asset.VoidRun(e.Ctx, e.Company, mar.ID, civil.Date{}, "Coba void", e.Actor); code(err) != "depreciation.disposed" {
		t.Fatalf("void penyusutan aset yang sudah dilepas seharusnya ditolak: %v", err)
	}

	// Pelepasan dibatalkan, lalu aset dihapus (rusak) akhir Juni: rugi sebesar nilai buku.
	if d, err = e.Asset.VoidDisposal(e.Ctx, e.Company, sale.ID, civil.Date{}, "Pembeli batal", e.Actor); err != nil || d.Status != asset.StatusActive || !d.Accumulated.Equal(rp(1_500_000)) {
		t.Fatalf("sesudah batal jual = %+v %v", d, err)
	}
	if d, err = e.Asset.Dispose(e.Ctx, e.Company, laptop.ID, asset.DisposalInput{Date: day("2026-06-30"), Kind: asset.DisposeWriteOff, Memo: "Rusak tersiram air"}, e.Actor); err != nil {
		t.Fatal(err)
	}
	if !e.Net("8-1005", end).Equal(rp(21_000_000)) || !e.Net("1-2104", end).IsZero() || !e.Net("1-2204", end).IsZero() {
		t.Fatalf("rugi %s, aset %s, akumulasi %s", e.Net("8-1005", end), e.Net("1-2104", end), e.Net("1-2204", end))
	}

	// Tanah tidak disusutkan; kendaraan baru disusutkan otomatis oleh penjadwal.
	buy(t, e, "1-2101", 500_000_000, "2026-07-01")
	buy(t, e, "1-2103", 96_000_000, "2026-07-01")
	if _, err := e.Asset.CreateAsset(e.Ctx, e.Company, asset.AssetInput{
		Name: "Tanah gudang", CategoryID: category(t, cats, "Tanah"), AcquiredOn: day("2026-07-01"), Cost: rp(500_000_000),
	}, e.Actor); err != nil {
		t.Fatal(err)
	}
	car, err := e.Asset.CreateAsset(e.Ctx, e.Company, asset.AssetInput{
		Name: "Mobil operasional", CategoryID: category(t, cats, "Kendaraan"), AcquiredOn: day("2026-07-01"), Cost: rp(96_000_000),
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := e.Asset.RunDue(e.Ctx, day("2026-08-03")); err != nil || n != 1 {
		t.Fatalf("penjadwal Agustus = %d %v", n, err)
	}
	if n, err := e.Asset.RunDue(e.Ctx, day("2026-08-03")); err != nil || n != 0 {
		t.Fatalf("penjadwal tidak boleh memosting dua kali: %d %v", n, err)
	}
	if c, _ := e.Asset.Asset(e.Ctx, e.Company, car.ID); !c.Accumulated.Equal(rp(1_000_000)) {
		t.Fatalf("akumulasi mobil Juli = %s", c.Accumulated)
	}
	if rec, err := e.Asset.Reconciliation(e.Ctx, e.Company); err != nil || !rec.Matched {
		t.Fatalf("register seharusnya cocok dengan buku besar: %+v %v", rec, err)
	}

	// Penyusutan fiskal 2026: laptop kelompok 1 (dilepas tahun ini) dan mobil kelompok 2.
	fr, err := e.Asset.FiscalReport(e.Ctx, e.Company, 2026)
	if err != nil {
		t.Fatal(err)
	}
	fiscal := map[string]money.Amount{}
	for _, r := range fr.Rows {
		fiscal[r.Code] = r.Fiscal
	}
	if len(fr.Rows) != 3 || !fiscal["AT-0001"].Equal(rp(6_000_000)) || !fiscal[car.Code].Equal(rp(6_000_000)) || !fiscal["AT-0002"].IsZero() {
		t.Fatalf("penyusutan fiskal 2026 = %+v", fr.Rows)
	}
	e.VerifyLedger()
}

// Aset lama dengan akumulasi awal: penyusutan dilanjutkan dari nilai bukunya, dan penghapusan
// aset yang belum punya jurnal tidak meninggalkan apa pun.
func TestOpeningAssetAndDelete(t *testing.T) {
	e := fixture.New(t)
	cats, err := e.Asset.DefaultCategories(e.Ctx, e.Company, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	om := month("2025-12")
	old, err := e.Asset.CreateAsset(e.Ctx, e.Company, asset.AssetInput{
		Code: "KDR-01", Name: "Truk lama", CategoryID: category(t, cats, "Kendaraan"), AcquiredOn: day("2023-01-05"), Cost: rp(96_000_000),
		OpeningAccum: rp(36_000_000), OpeningMonth: &om,
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if !old.Accumulated.Equal(rp(36_000_000)) || old.NextMonth == nil || *old.NextMonth != month("2026-01") || !old.NextAmount.Equal(rp(1_000_000)) {
		t.Fatalf("aset lama = %+v", old)
	}
	if _, err := e.Asset.CreateAsset(e.Ctx, e.Company, asset.AssetInput{
		Code: "kdr-01", Name: "Ganda", CategoryID: old.CategoryID, AcquiredOn: day("2026-01-01"), Cost: rp(1),
	}, e.Actor); code(err) != "asset.invalid" || fixture.Fields(err)["code"] == "" {
		t.Fatalf("kode ganda seharusnya ditolak: %v", err)
	}
	if err := e.Asset.DeleteAsset(e.Ctx, e.Company, old.ID, old.Version, e.Actor); err != nil {
		t.Fatal(err)
	}
	if list, err := e.Asset.Assets(e.Ctx, e.Company); err != nil || len(list) != 0 {
		t.Fatalf("aset tersisa = %+v %v", list, err)
	}
}

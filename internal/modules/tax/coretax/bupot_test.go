package coretax_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"lajur/internal/modules/core"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/tax/coretax"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/money"
)

func postBill(t *testing.T, e *fixture.Env, supplier uuid.UUID, date, price, taxCode, withholding string) purchase.BillDetail {
	t.Helper()
	d, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{SupplierID: supplier, Date: day(date), SupplierInvoiceNo: "INV-" + date, WithholdingCode: withholding,
		Lines: []trade.LineInput{{Description: "Jasa", AccountID: e.Acc["6-2008"], Qty: money.FromInt(1), UnitPrice: money.MustParse(price), TaxCode: taxCode}}}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Purchase.PostBill(e.Ctx, e.Company, d.ID, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func inputItem(t *testing.T, list coretax.InputList, id uuid.UUID) coretax.InputInvoice {
	t.Helper()
	for _, it := range list.Items {
		if it.BillID == id {
			return it
		}
	}
	t.Fatalf("tagihan %s tidak ada di daftar faktur masukan %s", id, list.Month)
	return coretax.InputInvoice{}
}

// TAX-07: masa pengkreditan menggeser PPN masukan di rekap; PPN yang tidak dikreditkan dijurnal
// ke biaya dan menahan void tagihan sampai dikreditkan lagi.
func TestInputVATCreditPeriodAndNonCreditable(t *testing.T) {
	e := setup(t)
	a := postBill(t, e, e.Supplier, "2026-10-08", "10000000", "PPN", "")
	b := postBill(t, e, e.Supplier, "2026-10-09", "2000000", "PPN", "")

	list, err := e.Coretax.Inputs(e.Ctx, e.Company, day("2026-10-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || !list.Credited.Equal(rp(1_320_000)) || list.Unrecorded != 2 {
		t.Fatalf("faktur masukan Oktober = %+v", list)
	}

	if _, err := e.Coretax.UpdateInput(e.Ctx, e.Company, a.ID, coretax.InputUpdate{
		TaxInvoiceNumber: "040.026-00.000000123", TaxInvoiceDate: day("2026-10-08"), CreditPeriod: "2027-02",
	}, e.Actor); code(err) != "tax.input_invalid" || fixture.Fields(err)["creditPeriod"] == "" {
		t.Fatalf("masa pengkreditan lewat tiga masa seharusnya ditolak: %v", err)
	}
	got, err := e.Coretax.UpdateInput(e.Ctx, e.Company, a.ID, coretax.InputUpdate{
		TaxInvoiceNumber: "040.026-00.000000123", TaxInvoiceDate: day("2026-10-08"), CreditPeriod: "2026-12",
	}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaxInvoiceNumber == nil || *got.TaxInvoiceNumber != "04002600000000123" || got.CreditPeriod != "2026-12" || len(got.Problems) != 0 {
		t.Fatalf("faktur masukan = %+v", got)
	}
	if _, err := e.Coretax.UpdateInput(e.Ctx, e.Company, b.ID, coretax.InputUpdate{
		TaxInvoiceNumber: "04002600000000123", TaxInvoiceDate: day("2026-10-09"),
	}, e.Actor); code(err) != "tax.input_invalid" {
		t.Fatalf("nomor faktur pajak ganda seharusnya ditolak: %v", err)
	}

	dec, _ := e.Coretax.Inputs(e.Ctx, e.Company, day("2026-12-01"))
	if len(dec.Items) != 1 || !dec.Credited.Equal(rp(1_100_000)) {
		t.Fatalf("faktur masukan Desember = %+v", dec)
	}

	// Tidak dikreditkan: PPN pindah dari PPN masukan ke akun biaya baris tagihan.
	if _, err := e.Coretax.SetCreditable(e.Ctx, e.Company, b.ID, false, day("2026-10-31"), "x", e.Actor); code(err) != "tax.input_reason" {
		t.Fatalf("alasan wajib diisi: %v", err)
	}
	nc, err := e.Coretax.SetCreditable(e.Ctx, e.Company, b.ID, false, day("2026-10-31"), "Faktur pajak pemasok cacat", e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Creditable || nc.ReclassEntryID == nil || nc.CreditPeriod != "" {
		t.Fatalf("tidak dikreditkan = %+v", nc)
	}
	if v := e.Net("1-1401", day("2026-10-31")); !v.Equal(rp(1_100_000)) {
		t.Fatalf("saldo PPN masukan = %s, seharusnya 1.100.000", v)
	}
	if v := e.Net("6-2008", day("2026-10-31")); !v.Equal(rp(12_220_000)) {
		t.Fatalf("saldo biaya = %s, seharusnya 12.220.000", v)
	}
	e.VerifyLedger()

	rec, err := e.Coretax.VATRecap(e.Ctx, e.Company, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if oct, dec := rec.Months[9], rec.Months[11]; !oct.Input.IsZero() || oct.Bills != 0 || !dec.Input.Equal(rp(1_100_000)) || dec.Bills != 1 {
		t.Fatalf("rekap Oktober = %+v, Desember = %+v", oct, dec)
	}

	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, b.ID, day("2026-11-02"), "Salah input", e.Actor); code(err) != "tax.input_reclass" {
		t.Fatalf("void tagihan yang PPN-nya dijurnal tidak dikreditkan seharusnya ditolak: %v", err)
	}
	if _, err := e.Purchase.CreateDebitNote(e.Ctx, e.Company, purchase.DebitNoteInput{BillID: b.ID, Date: day("2026-11-02"),
		Lines: []purchase.DebitLineInput{{BillLineID: b.Lines[0].ID, Qty: money.FromInt(1)}}}, e.Actor); code(err) != "tax.input_reclass" {
		t.Fatalf("nota debit atas tagihan yang PPN-nya dijurnal tidak dikreditkan seharusnya ditolak: %v", err)
	}

	back, err := e.Coretax.SetCreditable(e.Ctx, e.Company, b.ID, true, day("2026-11-02"), "Faktur pengganti sudah diterima", e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Creditable || back.ReclassEntryID != nil || back.CreditPeriod != "2026-10" {
		t.Fatalf("dikreditkan lagi = %+v", back)
	}
	if v := e.Net("1-1401", day("2026-11-30")); !v.Equal(rp(1_320_000)) {
		t.Fatalf("saldo PPN masukan sesudah dikreditkan lagi = %s", v)
	}
	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, b.ID, day("2026-11-02"), "Salah input", e.Actor); err != nil {
		t.Fatalf("void seharusnya boleh sesudah dikreditkan lagi: %v", err)
	}
	e.VerifyLedger()
}

// TAX-07: daftar pajak masukan Coretax dicocokkan lewat NPWP pemasok dan PPN; nomor faktur yang
// cocok dicatat otomatis, dan faktur yang tidak ada tagihannya dilaporkan.
func TestImportCoretaxInputs(t *testing.T) {
	e := setup(t)
	a := postBill(t, e, e.Supplier, "2026-10-08", "10000000", "PPN", "")
	b := postBill(t, e, e.Supplier, "2026-10-09", "2000000", "PPN", "")
	csv := "NPWP Penjual;Nama Penjual;Nomor Faktur Pajak;Tanggal Faktur Pajak;Harga Jual/DPP;PPN\n" +
		"9876543210987654;CV Pemasok Uji;04002600000000201;08/10/2026;9166667;1100000\n" +
		"9876543210987654;CV Pemasok Uji;04002600000000202;09/10/2026;1833333;219000\n" +
		"0111222333444555;PT Lain;04002600000000203;10/10/2026;1000000;110000\n"
	res, err := e.Coretax.ImportCoretaxInputs(e.Ctx, e.Company, "pajak-masukan.csv", []byte(csv), e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != 3 || len(res.Matched) != 1 || res.Matched[0] != *a.Number || len(res.CoretaxOnly) != 2 {
		t.Fatalf("hasil cocok = %+v", res)
	}
	list, _ := e.Coretax.Inputs(e.Ctx, e.Company, day("2026-10-01"))
	if it := inputItem(t, list, a.ID); it.Match != coretax.MatchOK || it.TaxInvoiceNumber == nil || *it.TaxInvoiceNumber != "04002600000000201" {
		t.Fatalf("tagihan cocok = %+v", it)
	}
	if it := inputItem(t, list, b.ID); it.Match != coretax.MatchUnchecked || it.TaxInvoiceNumber != nil {
		t.Fatalf("PPN berbeda lebih dari Rp1 tidak boleh dicocokkan otomatis: %+v", it)
	}

	// Sesudah nomornya dicatat manual, impor ulang mencocokkan lewat nomor dan menandai selisih.
	if _, err := e.Coretax.UpdateInput(e.Ctx, e.Company, b.ID, coretax.InputUpdate{TaxInvoiceNumber: "04002600000000202", TaxInvoiceDate: day("2026-10-09")}, e.Actor); err != nil {
		t.Fatal(err)
	}
	res, err = e.Coretax.ImportCoretaxInputs(e.Ctx, e.Company, "pajak-masukan.csv", []byte(csv), e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 1 || len(res.Differences) != 1 || !res.Differences[0].CoretaxVAT.Equal(rp(219_000)) {
		t.Fatalf("impor ulang = %+v", res)
	}
	list, _ = e.Coretax.Inputs(e.Ctx, e.Company, day("2026-10-01"))
	if it := inputItem(t, list, b.ID); it.Match != coretax.MatchDiffers {
		t.Fatalf("selisih PPN seharusnya ditandai: %+v", it)
	}
	if _, err := e.Coretax.ImportCoretaxInputs(e.Ctx, e.Company, "x.csv", []byte("a;b\n1;2\n"), e.Actor); code(err) != "tax.input_import" {
		t.Fatalf("berkas tanpa kolom yang dikenal seharusnya ditolak: %v", err)
	}
}

func slipItem(t *testing.T, list coretax.SlipList, id uuid.UUID) coretax.Slip {
	t.Helper()
	for _, it := range list.Items {
		if it.BillID == id {
			return it
		}
	}
	t.Fatalf("tagihan %s tidak ada di daftar bukti potong %s", id, list.Month)
	return coretax.Slip{}
}

// TAX-08: bukti potong PPh 23 diekspor sebagai XML BPPU, nomor yang terbit dicatat, dan
// tagihannya terkunci dari void sampai bukti potongnya ditandai dibatalkan.
func TestWithholdingSlipBPPU(t *testing.T) {
	e := setup(t)
	bill := postBill(t, e, e.Supplier, "2026-10-08", "5000000", "PPN", "PPH23")
	sewa := postBill(t, e, e.Supplier, "2026-10-12", "8000000", "PPN", "PPH42-SEWA")
	month := day("2026-10-01")

	list, err := e.Coretax.Slips(e.Ctx, e.Company, month)
	if err != nil {
		t.Fatal(err)
	}
	it := slipItem(t, list, bill.ID)
	if it.Kind != coretax.BatchBPPU || it.TaxObjectCode != "" || len(it.Problems) == 0 || !it.TaxBase.Equal(rp(5_000_000)) || !it.Amount.Equal(rp(100_000)) {
		t.Fatalf("bukti potong PPh 23 tanpa KOP = %+v", it)
	}
	if s := slipItem(t, list, sewa.ID); s.TaxObjectCode != "28-403-02" || len(s.Problems) != 0 || s.CounterpartTIN != "9876543210987654" {
		t.Fatalf("sewa memakai KOP bawaan = %+v", s)
	}
	if _, err := e.Coretax.ExportSlips(e.Ctx, e.Company, month, []uuid.UUID{bill.ID}, e.Actor); code(err) != "tax.slip_problems" {
		t.Fatalf("ekspor tanpa KOP seharusnya ditolak: %v", err)
	}
	if err := e.Coretax.SetSlipObject(e.Ctx, e.Company, []uuid.UUID{bill.ID}, "28-409-24", e.Actor); err != nil {
		t.Fatal(err)
	}
	list, _ = e.Coretax.Slips(e.Ctx, e.Company, month)
	if it := slipItem(t, list, bill.ID); len(it.Problems) != 1 || !strings.Contains(it.Problems[0], "Tarif") {
		t.Fatalf("KOP bertarif 2,65%% untuk PPh 23 2%% seharusnya ditandai: %+v", it.Problems)
	}
	if _, err := e.Coretax.UpdateSlip(e.Ctx, e.Company, bill.ID, coretax.SlipUpdate{TaxObjectCode: "24-104-07", DocumentNumber: "INV-2026-10-08",
		DocumentDate: day("2026-10-08"), WithholdingDate: day("2026-10-08")}, e.Actor); err != nil {
		t.Fatal(err)
	}

	batches, err := e.Coretax.ExportSlips(e.Ctx, e.Company, month, []uuid.UUID{bill.ID, sewa.ID}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0].Kind != coretax.BatchBPPU || batches[0].InvoiceCount != 2 {
		t.Fatalf("berkas ekspor = %+v", batches)
	}
	saved, err := e.Coretax.Batch(e.Ctx, e.Company, batches[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	xml := string(saved.Content)
	for _, want := range []string{
		"<BpuBulk", "<TIN>0123456789012345</TIN>", "<CounterpartTin>9876543210987654</CounterpartTin>",
		"<TaxObjectCode>24-104-07</TaxObjectCode>", "<TaxBase>5000000</TaxBase>", "<Rate>2</Rate>",
		"<TaxObjectCode>28-403-02</TaxObjectCode>", "<TaxBase>8000000</TaxBase>", "<Rate>10</Rate>",
		"<DocumentNumber>INV-2026-10-08</DocumentNumber>", "<IDPlaceOfBusinessActivity>0123456789012345000000</IDPlaceOfBusinessActivity>",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("XML BPPU tidak berisi %s", want)
		}
	}
	history, err := e.Coretax.Batches(e.Ctx, e.Company, month, []string{coretax.BatchBPPU, coretax.BatchBPNR})
	if err != nil || len(history) != 1 {
		t.Fatalf("riwayat ekspor bukti potong = %+v %v", history, err)
	}
	if faktur, _ := e.Coretax.Batches(e.Ctx, e.Company, month, []string{coretax.BatchFaktur}); len(faktur) != 0 {
		t.Fatalf("riwayat faktur tidak boleh berisi bukti potong: %+v", faktur)
	}

	if err := e.Coretax.ResetSlip(e.Ctx, e.Company, sewa.ID, false, "Draf dihapus di Coretax", e.Actor); err != nil {
		t.Fatal(err)
	}
	if err := e.Coretax.ApproveSlip(e.Ctx, e.Company, bill.ID, "2600abc12", day("2026-10-20"), e.Actor); err != nil {
		t.Fatal(err)
	}
	if err := e.Coretax.ApproveSlip(e.Ctx, e.Company, sewa.ID, "2600ABC12", day("2026-10-20"), e.Actor); code(err) != "tax.slip_number" {
		t.Fatalf("nomor bukti potong ganda seharusnya ditolak: %v", err)
	}
	list, _ = e.Coretax.Slips(e.Ctx, e.Company, month)
	if it := slipItem(t, list, bill.ID); it.Status != coretax.Approved || it.SlipNumber == nil || *it.SlipNumber != "2600ABC12" {
		t.Fatalf("sesudah terbit = %+v", it)
	}
	if s := slipItem(t, list, sewa.ID); s.Status != coretax.Ready {
		t.Fatalf("bukti potong yang dikembalikan = %+v", s)
	}
	if _, err := e.Coretax.UpdateSlip(e.Ctx, e.Company, bill.ID, coretax.SlipUpdate{TaxObjectCode: "24-104-08", DocumentNumber: "X"}, e.Actor); code(err) != "tax.slip_locked" {
		t.Fatalf("bukti potong terbit tidak boleh diubah: %v", err)
	}
	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, bill.ID, day("2026-10-25"), "Salah pemasok", e.Actor); code(err) != "tax.slip_locked" {
		t.Fatalf("void tagihan yang bukti potongnya terbit seharusnya ditolak: %v", err)
	}
	if err := e.Coretax.ResetSlip(e.Ctx, e.Company, bill.ID, true, "Dibatalkan di Coretax", e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.VoidBill(e.Ctx, e.Company, bill.ID, day("2026-10-25"), "Salah pemasok", e.Actor); err != nil {
		t.Fatalf("void seharusnya boleh sesudah bukti potong dibatalkan: %v", err)
	}
}

// TAX-08: PPh 26 hanya untuk pemasok luar negeri dan diekspor sebagai XML BPNR.
func TestWithholdingSlipBPNR(t *testing.T) {
	e := setup(t)
	foreign, err := e.Core.CreateContact(e.Ctx, e.Company, core.ContactInput{Name: "Acme Pte Ltd", IsSupplier: true, CountryCode: "SGP",
		ForeignTIN: "201912345K", Address: "1 Raffles Place, Singapore", PaymentTermsDays: 30}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{SupplierID: foreign.ID, Date: day("2026-10-10"), WithholdingCode: "PPH23",
		Lines: []trade.LineInput{{AccountID: e.Acc["6-2008"], Qty: money.FromInt(1), UnitPrice: rp(10_000_000), TaxCode: "NON-PPN"}}}, e.Actor); code(err) != "bill.invalid" {
		t.Fatalf("PPh 23 untuk pemasok luar negeri seharusnya ditolak: %v", err)
	}
	if _, err := e.Purchase.CreateBill(e.Ctx, e.Company, purchase.BillInput{SupplierID: e.Supplier, Date: day("2026-10-10"), WithholdingCode: "PPH26",
		Lines: []trade.LineInput{{AccountID: e.Acc["6-2008"], Qty: money.FromInt(1), UnitPrice: rp(10_000_000), TaxCode: "NON-PPN"}}}, e.Actor); code(err) != "bill.invalid" {
		t.Fatalf("PPh 26 untuk pemasok dalam negeri seharusnya ditolak: %v", err)
	}
	bill := postBill(t, e, foreign.ID, "2026-10-10", "10000000", "NON-PPN", "PPH26")
	if v := e.Net("2-1206", day("2026-10-31")); !v.Equal(rp(-2_000_000)) {
		t.Fatalf("utang PPh 26 = %s, seharusnya 2.000.000 di kredit", v)
	}

	month := day("2026-10-01")
	list, err := e.Coretax.Slips(e.Ctx, e.Company, month)
	if err != nil {
		t.Fatal(err)
	}
	it := slipItem(t, list, bill.ID)
	if it.Kind != coretax.BatchBPNR || it.CounterpartTIN != "201912345K" || it.Country != "SGP" || !it.Amount.Equal(rp(2_000_000)) {
		t.Fatalf("bukti potong PPh 26 = %+v", it)
	}
	if err := e.Coretax.SetSlipObject(e.Ctx, e.Company, []uuid.UUID{bill.ID}, "27-104-01", e.Actor); err != nil {
		t.Fatal(err)
	}
	batches, err := e.Coretax.ExportSlips(e.Ctx, e.Company, month, []uuid.UUID{bill.ID}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0].Kind != coretax.BatchBPNR {
		t.Fatalf("berkas ekspor = %+v", batches)
	}
	saved, _ := e.Coretax.Batch(e.Ctx, e.Company, batches[0].ID)
	xml := string(saved.Content)
	for _, want := range []string{
		"<BPNRBulk", "<CounterpartTin>201912345K</CounterpartTin>", "<CounterpartName>Acme Pte Ltd</CounterpartName>",
		"<CounterpartCountry>SGP</CounterpartCountry>", "<CounterpartAddress>1 Raffles Place, Singapore</CounterpartAddress>",
		"<TaxObjectCode>27-104-01</TaxObjectCode>", "<GrossIncome>10000000</GrossIncome>", "<Deemed>100</Deemed>", "<Rate>20</Rate>",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("XML BPNR tidak berisi %s", want)
		}
	}
}

func TestWithholdingObjectsSettings(t *testing.T) {
	e := setup(t)
	got, err := e.Coretax.SetWithholdingObjects(e.Ctx, e.Company, map[string]string{"PPH23": "24-104-07", "PPH42-SEWA": ""}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if got["PPH23"] != "24-104-07" || got["PPH42-K4"] != "28-409-23" {
		t.Fatalf("KOP bawaan = %+v", got)
	}
	if _, ok := got["PPH42-SEWA"]; ok {
		t.Fatalf("KOP bawaan yang dikosongkan seharusnya hilang: %+v", got)
	}
	if _, err := e.Coretax.SetWithholdingObjects(e.Ctx, e.Company, map[string]string{"PPH23": "24104"}, e.Actor); code(err) != "tax.objects" {
		t.Fatalf("format KOP salah seharusnya ditolak: %v", err)
	}
}

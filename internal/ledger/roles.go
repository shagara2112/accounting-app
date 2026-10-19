package ledger

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/httpx"
)

// Role adalah akun sistem yang dipakai aturan posting modul (TDD: pemetaan akun per PT,
// dengan akun default dari template bagan akun industri).
type Role string

const (
	Receivable          Role = "piutang_usaha"
	CustomerAdvance     Role = "uang_muka_pelanggan"
	OutputVAT           Role = "ppn_keluaran"
	PrepaidPPh23        Role = "pph23_dibayar_dimuka"
	FinalTaxExpense     Role = "beban_pph_final"
	Payable             Role = "utang_usaha"
	InputVAT            Role = "ppn_masukan"
	WithholdingPPh23    Role = "utang_pph23"
	WithholdingPPhFinal Role = "utang_pph42"
	ProjectAdvance      Role = "uang_muka_proyek"
	RetentionReceivable Role = "piutang_retensi"
	SupplierAdvance     Role = "uang_muka_pembelian"
	OpeningEquity       Role = "ekuitas_saldo_awal"
	AssetGain           Role = "laba_pelepasan_aset"
	AssetLoss           Role = "rugi_pelepasan_aset"
	WithholdingPPh26    Role = "utang_pph26"
	NonCreditableVAT    Role = "beban_ppn_tidak_dikreditkan"
)

type roleInfo struct {
	codes []string // kode default di template bagan akun, urut prioritas
	label string
}

var roles = map[Role]roleInfo{
	Receivable:          {[]string{"1-1201"}, "piutang usaha"},
	CustomerAdvance:     {[]string{"2-1401"}, "uang muka pelanggan"},
	OutputVAT:           {[]string{"2-1201"}, "PPN keluaran"},
	PrepaidPPh23:        {[]string{"1-1403"}, "PPh 23 dibayar di muka"},
	FinalTaxExpense:     {[]string{"9-1003"}, "beban PPh final"},
	Payable:             {[]string{"2-1101"}, "utang usaha"},
	InputVAT:            {[]string{"1-1401"}, "PPN masukan"},
	WithholdingPPh23:    {[]string{"2-1203"}, "utang PPh 23"},
	WithholdingPPhFinal: {[]string{"2-1204"}, "utang PPh 4(2)"},
	// Uang muka proyek konstruksi punya akun sendiri; PT lain memakai uang muka pelanggan.
	ProjectAdvance:      {[]string{"2-1402", "2-1401"}, "uang muka proyek"},
	RetentionReceivable: {[]string{"1-1202"}, "piutang retensi"},
	SupplierAdvance:     {[]string{"1-1503"}, "uang muka pembelian"},
	// Penampung saldo awal: bernilai nol setelah semua saldo awal (neraca saldo, piutang,
	// utang, dan stok per dokumen) selesai diimpor.
	OpeningEquity: {[]string{"3-3001"}, "ekuitas saldo awal"},
	// Pelepasan aset tetap (FA-03). PT lama tanpa akun rugi khusus memakai beban lain-lain.
	AssetGain: {[]string{"7-1003"}, "laba pelepasan aset tetap"},
	AssetLoss: {[]string{"8-1005", "8-1009"}, "rugi pelepasan aset tetap"},
	// PPh 26 pemasok luar negeri (TAX-08).
	WithholdingPPh26: {[]string{"2-1206"}, "utang PPh 26"},
	// PPN masukan tidak dikreditkan atas barang persediaan (TAX-07): akun persediaan hanya
	// berubah lewat kartu stok, jadi PPN-nya dibebankan di sini.
	NonCreditableVAT: {[]string{"6-4003", "6-4002"}, "beban PPN masukan tidak dapat dikreditkan"},
}

// roleCodes adalah semua kode akun sistem; kodenya tidak boleh diganti karena posting otomatis
// mencari akun lewat kode ini.
var roleCodes = func() map[string]bool {
	out := map[string]bool{}
	for _, info := range roles {
		for _, c := range info.codes {
			out[c] = true
		}
	}
	return out
}()

// RoleAccount mencari akun sistem dari kode defaultnya. Akun harus ada, aktif, dan akun detail;
// bila tidak, pesan galat menyebut kode yang perlu dibuat di bagan akun.
func (l *ledger) RoleAccount(ctx context.Context, companyID uuid.UUID, role Role) (Account, error) {
	info, ok := roles[role]
	if !ok {
		return Account{}, fmt.Errorf("akun sistem %q tidak dikenal", role)
	}
	for _, code := range info.codes {
		r, err := l.q(ctx).GetAccountByCode(ctx, store.GetAccountByCodeParams{CompanyID: companyID, Code: code})
		if isNoRows(err) || (err == nil && (!r.Postable || !r.Active)) {
			continue
		}
		if err != nil {
			return Account{}, err
		}
		return toAccount(r), nil
	}
	return Account{}, httpx.Unprocessable("ledger.role_account", fmt.Sprintf(
		"Akun sistem untuk %s belum siap: buat atau aktifkan akun detail %s di bagan akun.", info.label, info.codes[0]))
}

package ledger

import (
	"context"
	"fmt"
	"sort"

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

// systemRoles memetakan akun ke peran akun sistem yang dilayaninya, dengan aturan yang sama
// dengan RoleAccount: pemetaan tersimpan (pinned) didahulukan, selain itu kode bawaan pertama
// yang ada, aktif, dan akun detail.
func systemRoles(rows []store.LedgerAccount, pinned map[Role]uuid.UUID) map[uuid.UUID][]Role {
	byCode := make(map[string]store.LedgerAccount, len(rows))
	for _, r := range rows {
		byCode[r.Code] = r
	}
	out := map[uuid.UUID][]Role{}
	for role, info := range roles {
		if id, ok := pinned[role]; ok {
			out[id] = append(out[id], role)
			continue
		}
		for _, c := range info.codes {
			if r, ok := byCode[c]; ok && r.Postable && r.Active {
				out[r.ID] = append(out[r.ID], role)
				break
			}
		}
	}
	for id := range out {
		sort.Slice(out[id], func(i, j int) bool { return out[id][i] < out[id][j] })
	}
	return out
}

func pinnedRoles(ctx context.Context, q *store.Queries, companyID uuid.UUID) (map[Role]uuid.UUID, error) {
	rows, err := q.RoleAccountMappings(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make(map[Role]uuid.UUID, len(rows))
	for _, r := range rows {
		out[Role(r.Role)] = r.AccountID
	}
	return out, nil
}

// pinRoles mengunci peran akun sistem yang saat ini dilayani akun id ke akun itu, supaya
// posting otomatis tetap memakainya walau kodenya diganti.
func pinRoles(ctx context.Context, q *store.Queries, companyID, id uuid.UUID, actor Actor) ([]string, error) {
	rows, err := q.ListAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	pinned, err := pinnedRoles(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	var labels []string
	for _, role := range systemRoles(rows, pinned)[id] {
		if _, ok := pinned[role]; ok {
			continue
		}
		if err := q.PinRoleAccount(ctx, store.PinRoleAccountParams{CompanyID: companyID, Role: string(role), AccountID: id, PinnedBy: userRef(actor)}); err != nil {
			return nil, err
		}
		labels = append(labels, roles[role].label)
	}
	return labels, nil
}

// RoleLabels mengubah daftar peran menjadi nama yang dibaca pengguna, mis. "piutang usaha".
func roleLabels(list []Role) []string {
	out := make([]string, len(list))
	for i, r := range list {
		out[i] = roles[r].label
	}
	return out
}

// RoleAccount mencari akun sistem dari kode defaultnya. Akun harus ada, aktif, dan akun detail;
// bila tidak, pesan galat menyebut kode yang perlu dibuat di bagan akun.
func (l *ledger) RoleAccount(ctx context.Context, companyID uuid.UUID, role Role) (Account, error) {
	info, ok := roles[role]
	if !ok {
		return Account{}, fmt.Errorf("akun sistem %q tidak dikenal", role)
	}
	// Peran yang sudah dikunci ke akunnya (kode akunnya pernah diganti) tidak lagi dicari lewat kode.
	if id, err := l.q(ctx).RoleAccountMapping(ctx, store.RoleAccountMappingParams{CompanyID: companyID, Role: string(role)}); err == nil {
		r, err := l.q(ctx).GetAccount(ctx, store.GetAccountParams{CompanyID: companyID, ID: id})
		if err != nil {
			return Account{}, err
		}
		if !r.Postable || !r.Active {
			return Account{}, httpx.Unprocessable("ledger.role_account", fmt.Sprintf(
				"Akun sistem untuk %s (%s %s) nonaktif; aktifkan lagi di bagan akun.", info.label, r.Code, r.Name))
		}
		return toAccount(r), nil
	} else if !isNoRows(err) {
		return Account{}, err
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

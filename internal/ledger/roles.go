package ledger

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
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
	// PPN keluaran per jenis faktur; bila tidak dipilih, memakai PPN keluaran.
	OutputVATCollected Role = "ppn_keluaran_dipungut"
	OutputVATOtherDPP  Role = "ppn_keluaran_dpp_lain"
	OutputVATRetail    Role = "ppn_keluaran_digunggung"
)

type roleInfo struct {
	codes   []string // kode default di template bagan akun, urut prioritas
	label   string
	group   string // kelompok di halaman Akun sistem
	purpose string // dipakai untuk apa, dalam kalimat pengguna
	// fallback: peran yang dipakai bila peran ini tidak dipilih dan tidak punya kode bawaan.
	fallback Role
}

var roles = map[Role]roleInfo{
	Receivable:      {[]string{"1-1201"}, "piutang usaha", "Penjualan", "Faktur penjualan kredit, penerimaan pelanggan, dan saldo awal piutang.", ""},
	CustomerAdvance: {[]string{"2-1401"}, "uang muka pelanggan", "Penjualan", "Uang muka yang diterima dari pelanggan sebelum faktur.", ""},
	OutputVAT:       {[]string{"2-1201"}, "PPN keluaran", "Penjualan", "PPN faktur penjualan dan nota retur; juga jenis PPN keluaran di bawah yang tidak dipilih.", ""},
	// Akun PPN keluaran terpisah per jenis faktur pajak, untuk PT yang memisahkannya di bagan akun.
	OutputVATCollected: {nil, "PPN keluaran dipungut pemungut", "Penjualan", "PPN faktur ke pelanggan pemungut PPN (kode 02/03, mis. instansi pemerintah dan BUMN); disetor oleh pelanggan.", OutputVAT},
	OutputVATOtherDPP:  {nil, "PPN keluaran DPP nilai lain", "Penjualan", "PPN faktur dengan DPP nilai lain (kode 04).", OutputVAT},
	OutputVATRetail:    {nil, "PPN keluaran digunggung", "Penjualan", "PPN penjualan harian tanpa faktur pajak per pembeli.", OutputVAT},
	PrepaidPPh23:       {[]string{"1-1403"}, "PPh 23 dibayar di muka", "Penjualan", "PPh 23 yang dipotong pelanggan saat membayar faktur.", ""},
	FinalTaxExpense:    {[]string{"9-1003"}, "beban PPh final", "Penjualan", "PPh final 4(2) yang dipotong pelanggan saat membayar faktur.", ""},
	// Uang muka proyek konstruksi punya akun sendiri; PT lain memakai uang muka pelanggan.
	ProjectAdvance:      {[]string{"2-1402", "2-1401"}, "uang muka proyek", "Penjualan", "Uang muka proyek konstruksi dari pemberi kerja.", ""},
	RetentionReceivable: {[]string{"1-1202"}, "piutang retensi", "Penjualan", "Retensi proyek yang ditahan pemberi kerja.", ""},
	Payable:             {[]string{"2-1101"}, "utang usaha", "Pembelian", "Tagihan pembelian kredit, pembayaran pemasok, dan saldo awal utang.", ""},
	SupplierAdvance:     {[]string{"1-1503"}, "uang muka pembelian", "Pembelian", "Uang muka yang dibayar ke pemasok sebelum tagihan.", ""},
	InputVAT:            {[]string{"1-1401"}, "PPN masukan", "Pembelian", "PPN atas tagihan pembelian dan nota retur pembelian.", ""},
	// PPN masukan tidak dikreditkan atas barang persediaan (TAX-07): akun persediaan hanya
	// berubah lewat kartu stok, jadi PPN-nya dibebankan di sini.
	NonCreditableVAT:    {[]string{"6-4003", "6-4002"}, "beban PPN masukan tidak dapat dikreditkan", "Pembelian", "PPN masukan yang tidak bisa dikreditkan.", ""},
	WithholdingPPh23:    {[]string{"2-1203"}, "utang PPh 23", "Pembelian", "PPh 23 yang dipotong dari tagihan pemasok.", ""},
	WithholdingPPhFinal: {[]string{"2-1204"}, "utang PPh 4(2)", "Pembelian", "PPh 4(2) yang dipotong dari tagihan pemasok.", ""},
	// PPh 26 pemasok luar negeri (TAX-08).
	WithholdingPPh26: {[]string{"2-1206"}, "utang PPh 26", "Pembelian", "PPh 26 yang dipotong dari tagihan pemasok luar negeri.", ""},
	// Penampung saldo awal: bernilai nol setelah semua saldo awal (neraca saldo, piutang,
	// utang, dan stok per dokumen) selesai diimpor.
	OpeningEquity: {[]string{"3-3001"}, "ekuitas saldo awal", "Lainnya", "Penampung selisih saldo awal; nol setelah saldo awal lengkap.", ""},
	// Pelepasan aset tetap (FA-03). PT lama tanpa akun rugi khusus memakai beban lain-lain.
	AssetGain: {[]string{"7-1003"}, "laba pelepasan aset tetap", "Lainnya", "Laba penjualan aset tetap.", ""},
	AssetLoss: {[]string{"8-1005", "8-1009"}, "rugi pelepasan aset tetap", "Lainnya", "Rugi penjualan atau penghapusan aset tetap.", ""},
}

// roleOrder adalah urutan peran di halaman Akun sistem.
var roleOrder = []Role{
	Receivable, CustomerAdvance, OutputVAT, OutputVATCollected, OutputVATOtherDPP, OutputVATRetail, PrepaidPPh23, FinalTaxExpense, ProjectAdvance, RetentionReceivable,
	Payable, SupplierAdvance, InputVAT, NonCreditableVAT, WithholdingPPh23, WithholdingPPhFinal, WithholdingPPh26,
	OpeningEquity, AssetGain, AssetLoss,
}

// roleClasses adalah kelas akun yang boleh melayani peran: akun neraca harus sekelas dengan kode
// bawaannya; pendapatan boleh kelas 4 atau 7, beban boleh kelas 5, 6, 8, atau 9.
func roleClasses(role Role) []int {
	info := roles[role]
	if len(info.codes) == 0 {
		return roleClasses(info.fallback)
	}
	switch c := classOfCode(info.codes[0]); c {
	case 4, 7:
		return []int{4, 7}
	case 5, 6, 8, 9:
		return []int{5, 6, 8, 9}
	default:
		return []int{c}
	}
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
				"Akun sistem untuk %s (%s %s) nonaktif; aktifkan lagi, atau pilih akun lain di Bagan akun → Akun sistem.", info.label, r.Code, r.Name))
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
	if info.fallback != "" {
		return l.RoleAccount(ctx, companyID, info.fallback)
	}
	return Account{}, httpx.Unprocessable("ledger.role_account", fmt.Sprintf(
		"Akun sistem untuk %s belum dipilih: pilih akunnya di Bagan akun → Akun sistem.", info.label))
}

// SystemAccount adalah satu peran akun sistem di halaman Akun sistem beserta akun yang saat ini
// melayaninya.
type SystemAccount struct {
	Role        Role   `json:"role"`
	Label       string `json:"label"`
	Group       string `json:"group"`
	Purpose     string `json:"purpose"`
	DefaultCode string `json:"defaultCode,omitempty"`
	// Classes: kelas akun yang boleh dipilih untuk peran ini.
	Classes   []int      `json:"classes"`
	AccountID *uuid.UUID `json:"accountId,omitempty"`
	// Chosen: akunnya dipilih pengguna (atau kodenya pernah diganti), bukan ditemukan dari kode bawaan.
	Chosen bool `json:"chosen"`
	// Fallback: peran yang akunnya dipakai karena peran ini belum dipilih, mis. PPN keluaran.
	Fallback Role `json:"fallback,omitempty"`
	// Problem: kenapa peran ini belum bisa dipakai posting; kosong berarti siap.
	Problem string `json:"problem,omitempty"`
}

// systemAccounts menyusun status semua peran dengan aturan yang sama dengan RoleAccount.
func systemAccounts(rows []store.LedgerAccount, pinned map[Role]uuid.UUID) []SystemAccount {
	byID := make(map[uuid.UUID]store.LedgerAccount, len(rows))
	byCode := make(map[string]store.LedgerAccount, len(rows))
	for _, r := range rows {
		byID[r.ID], byCode[r.Code] = r, r
	}
	out := make([]SystemAccount, 0, len(roleOrder))
	for _, role := range roleOrder {
		info := roles[role]
		s := SystemAccount{Role: role, Label: info.label, Group: info.group, Purpose: info.purpose, Classes: roleClasses(role)}
		if len(info.codes) > 0 {
			s.DefaultCode = info.codes[0]
		}
		if id, ok := pinned[role]; ok {
			s.AccountID, s.Chosen = &id, true
			if r := byID[id]; !r.Active || !r.Postable {
				s.Problem = fmt.Sprintf("%s %s nonaktif; aktifkan lagi atau pilih akun lain.", r.Code, r.Name)
			}
		} else {
			for _, c := range info.codes {
				if r, ok := byCode[c]; ok && r.Postable && r.Active {
					id := r.ID
					s.AccountID = &id
					break
				}
			}
			switch {
			case s.AccountID == nil && info.fallback != "":
				s.Fallback = info.fallback
			case s.AccountID == nil:
				s.Problem = "Belum dipilih."
			}
		}
		out = append(out, s)
	}
	return out
}

// SystemAccounts mengembalikan semua peran akun sistem PT dan akun yang melayaninya.
func (l *ledger) SystemAccounts(ctx context.Context, companyID uuid.UUID) ([]SystemAccount, error) {
	q := l.q(ctx)
	rows, err := q.ListAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	pinned, err := pinnedRoles(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	return systemAccounts(rows, pinned), nil
}

// SetSystemAccount memilih akun untuk satu peran akun sistem, mis. untuk bagan akun yang disusun
// sendiri tanpa kode bawaan. Akunnya harus akun detail aktif dari kelas yang sesuai. Akun lama yang
// masih bersaldo tidak bisa ditinggalkan, supaya dokumen yang masih terbuka (mis. faktur belum
// lunas) tetap dilunasi di akun yang sama.
func (l *ledger) SetSystemAccount(ctx context.Context, companyID uuid.UUID, role Role, accountID uuid.UUID, actor Actor) ([]SystemAccount, error) {
	info, ok := roles[role]
	if !ok {
		return nil, httpx.NotFound("ledger.role_unknown", "Peran akun sistem tidak dikenal.")
	}
	var out []SystemAccount
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		rows, err := q.ListAccounts(ctx, companyID)
		if err != nil {
			return err
		}
		pinned, err := pinnedRoles(ctx, q, companyID)
		if err != nil {
			return err
		}
		byID := make(map[uuid.UUID]store.LedgerAccount, len(rows))
		for _, r := range rows {
			byID[r.ID] = r
		}
		fail := func(msg string) error {
			return httpx.Unprocessable("ledger.role_account_invalid", msg).WithFields(map[string]string{"accountId": msg})
		}
		a, ok := byID[accountID]
		classes := roleClasses(role)
		switch {
		case !ok:
			return fail("Akun tidak ditemukan di PT ini.")
		case !a.Postable || !a.Active:
			return fail(fmt.Sprintf("%s %s bukan akun detail yang aktif.", a.Code, a.Name))
		case !slices.Contains(classes, int(a.Class)):
			names := make([]string, len(classes))
			for i, c := range classes {
				names[i] = fmt.Sprintf("%d %s", c, ClassOf(c).Name)
			}
			return fail(fmt.Sprintf("Akun %s harus dari kelompok %s.", info.label, strings.Join(names, " atau ")))
		}

		var cur SystemAccount
		for _, s := range systemAccounts(rows, pinned) {
			if s.Role == role {
				cur = s
			}
		}
		if cur.AccountID != nil && *cur.AccountID == accountID && cur.Chosen {
			out = systemAccounts(rows, pinned)
			return nil
		}
		before := map[string]any{"account": ""}
		if cur.AccountID != nil {
			old := byID[*cur.AccountID]
			before["account"] = old.Code
			if old.ID != accountID {
				net, err := q.AccountNetAllTime(ctx, store.AccountNetAllTimeParams{CompanyID: companyID, AccountID: old.ID})
				if err != nil {
					return err
				}
				if !net.IsZero() {
					return fail(fmt.Sprintf(
						"%s %s yang sekarang dipakai untuk %s masih bersaldo %s. Pindahkan saldonya dulu lewat jurnal umum, atau cukup ubah kode akun itu bila hanya penomorannya yang berbeda.",
						old.Code, old.Name, info.label, ClassOf(int(old.Class)).Present(net).IDR()))
				}
			}
		}
		if err := q.SetRoleAccount(ctx, store.SetRoleAccountParams{CompanyID: companyID, Role: string(role), AccountID: accountID, PinnedBy: userRef(actor)}); err != nil {
			return err
		}
		pinned[role] = accountID
		out = systemAccounts(rows, pinned)
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.system_account.set",
			ObjectType: "system_account", ObjectID: string(role), IP: actor.IP,
			Before: before, After: map[string]any{"account": a.Code},
		})
		return err
	})
	return out, err
}

package rbac

// Katalog izin dengan format modul.objek.aksi. Peran (bawaan maupun persona kustom)
// hanya bisa memakai izin dari katalog ini. Mengubah jurnal yang sudah diposting hanya
// mungkin untuk jurnal manual di periode yang belum dikunci (JournalEdit), dengan riwayat
// versi; jurnal dokumen dan periode terkunci tetap dikoreksi lewat void.
const (
	CompanyView   = "core.company.view"
	CompanyEdit   = "core.company.edit" // profil pajak PT: NPWP, NITKU, status PKP
	ContactView   = "core.contact.view"
	ContactCreate = "core.contact.create"
	ContactEdit   = "core.contact.edit" // ubah dan nonaktifkan kontak
	ReportView    = "report.view"
	BudgetManage  = "report.budget.manage" // susun, ubah, dan aktifkan anggaran (BUD-01)
	DataExport    = "data.export"

	LedgerView       = "ledger.view"
	AccountCreate    = "ledger.account.create"
	AccountEdit      = "ledger.account.edit" // ganti nama dan nonaktifkan akun
	JournalCreate    = "ledger.journal.create"
	JournalEdit      = "ledger.journal.edit" // ubah jurnal manual sebelum periodenya dikunci
	JournalVoid      = "ledger.journal.void"
	AdjustmentCreate = "ledger.adjustment.create"
	AdjustmentPost   = "ledger.adjustment.post"
	PeriodManage     = "ledger.period.manage"

	SalesView   = "sales.view"
	SalesCreate = "sales.create"
	SalesVoid   = "sales.void"

	PurchaseView   = "purchase.view"
	PurchaseCreate = "purchase.create"
	PurchaseVoid   = "purchase.void"

	CashView   = "cash.view"
	CashCreate = "cash.create"
	CashVoid   = "cash.void"

	InventoryView   = "inventory.view"
	InventoryCreate = "inventory.create"
	InventoryVoid   = "inventory.void"

	AssetView   = "asset.view"
	AssetCreate = "asset.create" // daftarkan aset, posting penyusutan, dan lepas aset
	AssetVoid   = "asset.void"   // void penyusutan dan pelepasan, hapus aset

	ProjectView   = "project.view"
	ProjectCreate = "project.create"
	ProjectVoid   = "project.void"

	TaxView   = "tax.view"
	TaxCreate = "tax.create"
	TaxExport = "tax.export"

	ConsolView = "consol.view"
	ConsolRun  = "consol.run"

	// Panjar unit: kepala unit mengajukan kebutuhan unitnya, finance menyetujui atau menolak.
	PanjarView    = "panjar.view"        // lihat semua pengajuan, bukan hanya milik sendiri
	PanjarCreate  = "panjar.create"      // ajukan panjar untuk departemen (unit)
	PanjarApprove = "panjar.approve"     // setujui, tolak, dan tandai sudah dibayar
	PanjarUnits   = "panjar.unit.manage" // tetapkan unit yang dikelola setiap kepala unit

	ApprovalManage = "approval.manage"
	AuditView      = "platform.audit.view"
	UserManage     = "platform.user.manage"
	SystemManage   = "platform.system.manage"
)

// All berisi seluruh izin di katalog.
var All = []string{
	CompanyView, CompanyEdit, ContactView, ContactCreate, ContactEdit, ReportView, BudgetManage, DataExport,
	LedgerView, AccountCreate, AccountEdit, JournalCreate, JournalEdit, JournalVoid, AdjustmentCreate, AdjustmentPost, PeriodManage,
	SalesView, SalesCreate, SalesVoid,
	PurchaseView, PurchaseCreate, PurchaseVoid,
	CashView, CashCreate, CashVoid,
	InventoryView, InventoryCreate, InventoryVoid,
	ProjectView, ProjectCreate, ProjectVoid,
	AssetView, AssetCreate, AssetVoid,
	TaxView, TaxCreate, TaxExport,
	ConsolView, ConsolRun,
	PanjarView, PanjarCreate, PanjarApprove, PanjarUnits,
	ApprovalManage, AuditView, UserManage, SystemManage,
}

var (
	viewAll = []string{
		CompanyView, ContactView, ReportView, LedgerView, SalesView, PurchaseView, CashView,
		InventoryView, ProjectView, AssetView, TaxView, ConsolView, PanjarView,
	}
	createAll = []string{
		ContactCreate, AccountCreate, JournalCreate, JournalEdit, AdjustmentCreate, AdjustmentPost, SalesCreate, PurchaseCreate,
		CashCreate, InventoryCreate, ProjectCreate, AssetCreate, TaxCreate, ConsolRun, PanjarCreate,
	}
	// finance memutuskan pengajuan panjar unit: Pemilik, Manager, dan Staf akuntansi.
	finance = []string{PanjarApprove}
	// changeAll = "ubah, hapus, atau void transaksi" di matriks: hanya Pemilik dan Manager.
	changeAll = []string{CompanyEdit, ContactEdit, AccountEdit, JournalVoid, SalesVoid, PurchaseVoid, CashVoid, InventoryVoid, ProjectVoid, AssetVoid}
	control   = []string{ApprovalManage, PeriodManage, AuditView, BudgetManage, PanjarUnits}
	admin     = []string{UserManage, SystemManage}
)

// BuiltinRole adalah peran bawaan sesuai enam persona di PRD.
type BuiltinRole struct {
	Key         string
	Name        string
	Permissions []string
}

// Builtin mengikuti matriks izin di TDD (bagian Autentikasi dan otorisasi).
var Builtin = []BuiltinRole{
	{Key: "pemilik", Name: "Pemilik", Permissions: join(viewAll, createAll, changeAll, control, admin, finance, []string{TaxExport, DataExport})},
	{Key: "manager", Name: "Manager", Permissions: join(viewAll, createAll, changeAll, control, admin, finance, []string{TaxExport, DataExport})},
	{Key: "staf_akuntansi", Name: "Staf akuntansi", Permissions: join(viewAll, createAll, finance, []string{DataExport})},
	{Key: "staf_pajak", Name: "Staf pajak", Permissions: []string{CompanyView, ContactView, TaxView, TaxCreate, TaxExport, DataExport, AssetView}},
	{Key: "auditor", Name: "Auditor", Permissions: join(viewAll, []string{AuditView, DataExport})},
	{Key: "admin", Name: "Admin pelanggan", Permissions: join([]string{CompanyView}, admin)},
	// Kepala unit hanya mengajukan panjar untuk unit yang ditetapkan Manager atau Pemilik dan melihat
	// pengajuan unit itu.
	{Key: "kepala_unit", Name: "Kepala unit", Permissions: []string{CompanyView, PanjarCreate}},
}

func join(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, p := range l {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// CatalogPerm dan CatalogGroup menampilkan katalog izin dengan label untuk layar persona kustom.
type CatalogPerm struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type CatalogGroup struct {
	Name  string        `json:"name"`
	Perms []CatalogPerm `json:"perms"`
}

// Catalog berisi setiap izin di All tepat satu kali (dijaga tes).
var Catalog = []CatalogGroup{
	{"Perusahaan dan kontak", []CatalogPerm{
		{CompanyView, "Lihat profil PT"}, {CompanyEdit, "Ubah profil pajak PT"},
		{ContactView, "Lihat kontak"}, {ContactCreate, "Tambah kontak"}, {ContactEdit, "Ubah dan nonaktifkan kontak"},
	}},
	{"Buku besar", []CatalogPerm{
		{LedgerView, "Lihat jurnal, buku besar, dan bagan akun"}, {AccountCreate, "Tambah akun"},
		{AccountEdit, "Ubah dan nonaktifkan akun"}, {JournalCreate, "Buat jurnal umum"},
		{JournalEdit, "Ubah jurnal manual sebelum periode dikunci"}, {JournalVoid, "Void jurnal"},
		{AdjustmentCreate, "Input jurnal penyesuaian"}, {AdjustmentPost, "Posting jurnal penyesuaian"},
		{PeriodManage, "Kunci dan buka periode"},
	}},
	{"Penjualan", []CatalogPerm{{SalesView, "Lihat faktur dan pelunasan"}, {SalesCreate, "Buat faktur dan pelunasan"}, {SalesVoid, "Void penjualan"}}},
	{"Pembelian", []CatalogPerm{{PurchaseView, "Lihat tagihan dan pembayaran"}, {PurchaseCreate, "Buat tagihan dan pembayaran"}, {PurchaseVoid, "Void pembelian"}}},
	{"Kas dan bank", []CatalogPerm{{CashView, "Lihat kas dan bank"}, {CashCreate, "Catat transaksi kas"}, {CashVoid, "Void kas dan hapus draf orang lain"}}},
	{"Persediaan", []CatalogPerm{{InventoryView, "Lihat produk dan stok"}, {InventoryCreate, "Tambah produk dan penyesuaian stok"}, {InventoryVoid, "Void penyesuaian stok"}}},
	{"Aset tetap", []CatalogPerm{{AssetView, "Lihat aset tetap dan penyusutan"}, {AssetCreate, "Daftarkan aset, posting penyusutan, dan lepas aset"}, {AssetVoid, "Void penyusutan dan pelepasan, hapus aset"}}},
	{"Proyek", []CatalogPerm{{ProjectView, "Lihat proyek"}, {ProjectCreate, "Buat proyek, RAB, dan termin"}, {ProjectVoid, "Batalkan proyek"}}},
	{"Pajak", []CatalogPerm{{TaxView, "Lihat pajak"}, {TaxCreate, "Kelola faktur pajak"}, {TaxExport, "Ekspor XML Coretax dan rekap SPT"}}},
	{"Laporan", []CatalogPerm{
		{ReportView, "Lihat laporan keuangan, anggaran, dan rasio"}, {BudgetManage, "Susun dan ubah anggaran"},
		{DataExport, "Ekspor data ke Excel dan CSV"},
		{ConsolView, "Lihat konsolidasi grup"}, {ConsolRun, "Atur pemetaan konsolidasi"},
	}},
	{"Panjar unit", []CatalogPerm{
		{PanjarView, "Lihat semua pengajuan panjar"}, {PanjarCreate, "Ajukan panjar unit"},
		{PanjarApprove, "Setujui, tolak, dan tandai pembayaran panjar"}, {PanjarUnits, "Tetapkan unit yang dikelola kepala unit"},
	}},
	{"Kontrol dan admin", []CatalogPerm{
		{ApprovalManage, "Approval dan aturan approval"}, {AuditView, "Lihat audit trail"},
		{UserManage, "Kelola pengguna dan persona"}, {SystemManage, "Backup, pembaruan, lisensi, dan PT baru"},
	}},
}

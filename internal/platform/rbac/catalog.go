package rbac

// Katalog izin dengan format modul.objek.aksi. Peran (bawaan maupun persona kustom)
// hanya bisa memakai izin dari katalog ini. Mengubah baris jurnal yang sudah diposting
// sengaja tidak ada di katalog, sehingga tidak bisa diberikan ke siapa pun.
const (
	CompanyView = "core.company.view"
	ReportView  = "report.view"
	DataExport  = "data.export"

	LedgerView       = "ledger.view"
	AccountCreate    = "ledger.account.create"
	AccountEdit      = "ledger.account.edit" // ganti nama dan nonaktifkan akun
	JournalCreate    = "ledger.journal.create"
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

	ProjectView   = "project.view"
	ProjectCreate = "project.create"
	ProjectVoid   = "project.void"

	TaxView   = "tax.view"
	TaxCreate = "tax.create"
	TaxExport = "tax.export"

	ConsolView = "consol.view"
	ConsolRun  = "consol.run"

	ApprovalManage = "approval.manage"
	AuditView      = "platform.audit.view"
	UserManage     = "platform.user.manage"
	SystemManage   = "platform.system.manage"
)

// All berisi seluruh izin di katalog.
var All = []string{
	CompanyView, ReportView, DataExport,
	LedgerView, AccountCreate, AccountEdit, JournalCreate, JournalVoid, AdjustmentCreate, AdjustmentPost, PeriodManage,
	SalesView, SalesCreate, SalesVoid,
	PurchaseView, PurchaseCreate, PurchaseVoid,
	CashView, CashCreate, CashVoid,
	InventoryView, InventoryCreate, InventoryVoid,
	ProjectView, ProjectCreate, ProjectVoid,
	TaxView, TaxCreate, TaxExport,
	ConsolView, ConsolRun,
	ApprovalManage, AuditView, UserManage, SystemManage,
}

var (
	viewAll = []string{
		CompanyView, ReportView, LedgerView, SalesView, PurchaseView, CashView,
		InventoryView, ProjectView, TaxView, ConsolView,
	}
	createAll = []string{
		AccountCreate, JournalCreate, AdjustmentCreate, AdjustmentPost, SalesCreate, PurchaseCreate,
		CashCreate, InventoryCreate, ProjectCreate, TaxCreate, ConsolRun,
	}
	// changeAll = "ubah, hapus, atau void transaksi" di matriks: hanya Pemilik dan Manager.
	changeAll = []string{AccountEdit, JournalVoid, SalesVoid, PurchaseVoid, CashVoid, InventoryVoid, ProjectVoid}
	control = []string{ApprovalManage, PeriodManage, AuditView}
	admin   = []string{UserManage, SystemManage}
)

// BuiltinRole adalah peran bawaan sesuai enam persona di PRD.
type BuiltinRole struct {
	Key         string
	Name        string
	Permissions []string
}

// Builtin mengikuti matriks izin di TDD (bagian Autentikasi dan otorisasi).
var Builtin = []BuiltinRole{
	{Key: "pemilik", Name: "Pemilik", Permissions: join(viewAll, createAll, changeAll, control, admin, []string{TaxExport, DataExport})},
	{Key: "manager", Name: "Manager", Permissions: join(viewAll, createAll, changeAll, control, admin, []string{TaxExport, DataExport})},
	{Key: "staf_akuntansi", Name: "Staf akuntansi", Permissions: join(viewAll, createAll, []string{DataExport})},
	{Key: "staf_pajak", Name: "Staf pajak", Permissions: []string{CompanyView, TaxView, TaxCreate, TaxExport, DataExport}},
	{Key: "auditor", Name: "Auditor", Permissions: join(viewAll, []string{AuditView, DataExport})},
	{Key: "admin", Name: "Admin pelanggan", Permissions: join([]string{CompanyView}, admin)},
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

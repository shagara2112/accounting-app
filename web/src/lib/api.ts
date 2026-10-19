// Klien API Lajur. Semua error server berbentuk problem+json dengan kode bisnis yang stabil.

export class ApiError extends Error {
  status: number
  code: string
  /** Pesan per field, mis. {"lines.2.accountId": "Akun nonaktif."} */
  fields: Record<string, string>

  constructor(status: number, code: string, message: string, fields: Record<string, string> = {}) {
    super(message)
    this.status = status
    this.code = code
    this.fields = fields
  }
}

type RequestOptions = {
  /** Wajib untuk posting dokumen keuangan; kunci yang sama = permintaan yang sama. */
  idempotencyKey?: string
}

export async function api<T>(method: string, path: string, body?: unknown, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (opts.idempotencyKey) headers['Idempotency-Key'] = opts.idempotencyKey
  const res = await fetch('/api/v1' + path, {
    method,
    credentials: 'same-origin',
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    throw new ApiError(res.status, data?.code ?? 'unknown', data?.detail || data?.title || 'Terjadi kesalahan. Coba lagi.', data?.fields ?? {})
  }
  return data as T
}

/** Kunci acak untuk Idempotency-Key. Tidak memakai crypto.randomUUID karena fungsi itu
 *  hanya ada di HTTPS, sedangkan Lajur bisa diakses lewat http di jaringan kantor. */
export function newIdempotencyKey(): string {
  const b = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

export function query(params: Record<string, string | number | undefined | null>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
  }
  const s = q.toString()
  return s ? '?' + s : ''
}

export type CompanyAccess = {
  id: string
  name: string
  shortName: string
  kind: 'pt' | 'consolidation'
  industry?: 'jasa' | 'konstruksi' | 'retail'
  pkp: boolean
  roleKey: string
  roleName: string
  permissions: string[]
  /** PLT-12: pengguna dibatasi ke cabang ini; kosong = semua cabang. */
  branchIds?: string[]
}

export type Me = {
  user: { id: string; name: string; email: string }
  activeCompanyId: string | null
  companies: CompanyAccess[]
  /** Pengguna wajib 2FA tetapi belum memasangnya; aplikasi hanya menampilkan layar pemasangan. */
  mfaSetupRequired?: boolean
}

export type LicenseState = 'ACTIVE' | 'WARNING' | 'EXPIRED' | 'SUSPENDED' | 'UNVERIFIED' | 'INVALID_KEY'

export type LicenseStatus = {
  status: LicenseState
  expiresAt?: string
  daysLeft?: number
  message: string
  /** Versi aplikasi di bawah versi minimum dari License Console: hanya baca sampai diperbarui. */
  updateRequired?: boolean
  minVersion?: string
}

export type AuditItem = {
  seq: number
  at: string
  userId?: string
  action: string
  objectType: string
  objectId?: string
  before?: Record<string, unknown>
  after?: Record<string, unknown>
  ip?: string
}

export type AuditVerify = { ok: boolean; count: number; brokenAt?: number }

// Buku besar ------------------------------------------------------------------------

export type Side = 'debit' | 'kredit'

export type AccountClass = {
  class: number
  name: string
  normal: Side
  statement: 'neraca' | 'laba_rugi'
}

export type Account = {
  id: string
  code: string
  name: string
  class: number
  parentId?: string
  level: number
  postable: boolean
  active: boolean
  version: number
  balance: string
  /** Akun sistem untuk posting otomatis (mis. 1-1401 PPN masukan); kodenya terkunci. */
  system?: boolean
}

export type AccountsResponse = { items: Account[]; classes: AccountClass[]; templates: string[] }

export type Kind = 'umum' | 'penyesuaian' | 'pembalik' | 'penutup'

export type EntrySummary = {
  id: string
  number: string
  kind: Kind
  date: string
  memo: string
  sourceType: string
  sourceId?: string
  total: string
  status: 'posted' | 'void'
  reversalOf?: string
}

export type EntryPage = { items: EntrySummary[]; nextCursor?: string }

export type EntryLine = {
  lineNo: number
  accountId: string
  accountCode: string
  accountName: string
  debit: string
  credit: string
  memo: string
  projectId?: string
  branchId?: string
  departmentId?: string
  branchCode?: string
  departmentCode?: string
}

export type Ref = { id: string; number: string }

export type EntryDetail = EntrySummary & {
  autoReverse: boolean
  postedAt: string
  postedBy?: string
  voidedAt?: string
  voidedBy?: string
  voidReason?: string
  lines: EntryLine[]
  reversalOfRef?: Ref
  reversals: (Ref & { sourceType: string; date: string; status: string })[]
  /** Berapa kali jurnal manual ini diubah; versi lamanya ada di revisions (terbaru dulu). */
  revision: number
  revisedAt?: string
  revisedBy?: string
  revisions: JournalRevision[]
  /** Jurnal manual yang periodenya belum dikunci; editBlock menjelaskan kenapa tidak bisa. */
  editable: boolean
  editBlock?: string
}

/** Versi jurnal yang sudah digantikan. revision 0 = versi saat pertama diposting. */
export type JournalRevision = { revision: number; date: string; memo: string; total: string; lines: EntryLine[]; reason?: string; revisedAt: string; revisedBy?: string }

export type Posted = Ref & { autoReversal?: Ref }
export type Voided = { reversal: Ref; alsoVoided?: Ref[] }

export type LineInput = { accountId: string; debit: string; credit: string; memo: string; projectId?: string; branchId?: string; departmentId?: string }

export type Pair = { debit: string; credit: string }

export type WorksheetRow = {
  accountId?: string
  code: string
  name: string
  class: number
  unadjusted: Pair
  adjustment: Pair
  adjusted: Pair
  profitLoss: Pair
  balanceSheet: Pair
}

type WorksheetTotals = Record<'unadjusted' | 'adjustment' | 'adjusted' | 'profitLoss' | 'balanceSheet', Pair>

export type Worksheet = {
  month: string
  fiscalStart: string
  monthEnd: string
  rows: WorksheetRow[]
  totals: WorksheetTotals
  netIncome: string
  final: WorksheetTotals
  balanced: boolean
}

export type LedgerLine = {
  entryId: string
  number: string
  kind: Kind
  status: string
  date: string
  memo: string
  debit: string
  credit: string
  balance: string
  projectId?: string
}

export type AccountLedger = {
  account: Account
  normal: Side
  from: string
  to: string
  opening: string
  lines: LedgerLine[]
  totalDebit: string
  totalCredit: string
  closing: string
  truncated: boolean
}

export type PeriodStatus = 'open' | 'adjusting' | 'locked'

export type Period = {
  month: string
  status: PeriodStatus
  changedAt?: string
  changedBy?: string
  reason?: string
  entries: number
  adjustments: number
}

export type FiscalYear = { year: number; startMonth: number; periods: Period[] }

export type LedgerSettings = { fiscalYearStartMonth: number; hasEntries: boolean }

// Kontak, pajak, penjualan, pembelian, kas ------------------------------------------------

export type Contact = {
  id: string
  name: string
  isCustomer: boolean
  isSupplier: boolean
  npwp: string
  nik: string
  nitku: string
  pkp: boolean
  /** ISO alfa-3; IDN = dalam negeri. Pemasok luar negeri dipotong PPh 26 (TAX-08). */
  countryCode: string
  /** TIN di negara asal pemasok luar negeri. */
  foreignTin: string
  address: string
  email: string
  phone: string
  paymentTermsDays: number
  bankName: string
  bankAccount: string
  active: boolean
  version: number
  /** Kontak yang mewakili PT lain dalam grup (transaksi antar-PT). */
  linkedCompanyId?: string | null
  linkedCompanyName?: string
  /** Akun beban/aset untuk tagihan cerminan di PT ini. */
  icExpenseAccountId?: string | null
  /** Akun pendapatan untuk faktur cerminan di PT ini. */
  icRevenueAccountId?: string | null
  /** Rekening kas PT ini untuk pelunasan antar-PT yang dicerminkan. */
  icCashAccountId?: string | null
  /** AR-01: limit kredit pelanggan; kosong = tanpa limit. */
  creditLimit?: string | null
  billingContact: string
}

export type ContactInput = Omit<Contact, 'id' | 'linkedCompanyName'>

export type TaxCode = {
  code: string
  kind: 'ppn' | 'pph'
  name: string
  rateBp: number
  dppNum: number
  dppDen: number
  article?: '23' | '4(2)' | '26'
  validFrom: string
  validTo?: string
}

export type TradeLineInput = {
  description: string
  accountId: string
  qty: string
  unitPrice: string
  discountPct: string
  taxCode: string
  productId?: string
  /** Hanya tagihan pembelian: proyek per baris. Faktur penjualan memakai proyek di kepala faktur. */
  projectId?: string
  /** Potongan nominal tetap (potongan uang muka di faktur termin); dibuat server, bukan formulir. */
  discountAmount?: string
}

export type TradeLine = TradeLineInput & {
  /** Hanya baris faktur dan tagihan yang diposting: dirujuk nota kredit/debit. */
  id?: string
  gross: string
  discount: string
  net: string
  otherDpp: string
  vat: string
  accountCode: string
  accountName: string
  /** DPP yang sudah dikembalikan lewat nota kredit (penjualan) atau nota debit (pembelian). */
  creditedNet?: string
  debitedNet?: string
  /** Kuantitas yang sudah diretur (barang kembali ke stok atau ke pemasok). */
  returnedQty?: string
}

export type Totals = { subtotal: string; discount: string; dpp: string; otherDpp: string; vat: string; total: string }

export type DocState = 'draft' | 'open' | 'paid' | 'void'

type DocCommon = {
  id: string
  number: string | null
  status: 'draft' | 'posted' | 'void'
  state: DocState
  date: string
  dueDate: string
  memo: string
  total: string
  paid: string
  outstanding: string
  daysOverdue: number
  /** Dimensi cabang dan departemen (GL-09). */
  branchId?: string
  departmentId?: string
}

type DocDetailCommon = {
  totals: Totals
  lines: TradeLine[]
  journalEntryId?: string
  createdById?: string
  createdBy?: string
  postedBy?: string
  postedAt?: string
  voidedBy?: string
  voidedAt?: string
  voidReason?: string
  version: number
}

export type InvoiceKind = 'biasa' | 'uang_muka' | 'termin' | 'retensi' | 'saldo_awal'

export type Invoice = DocCommon & {
  customerId: string
  customerName: string
  dpp: string
  kind: InvoiceKind
  projectId?: string
  progressPct?: string
  subtotal: string
  discount: string
  retention: string
  /** Total − retensi: yang ditagih sekarang. */
  receivable: string
  /** Nota kredit yang mengurangi sisa faktur. */
  credited: string
}
export type InvoicePayment = { receiptId: string; number: string; date: string; status: string; amount: string; withholding: string; withholdingCode?: string }
export type NoteRef = { number: string; date: string; status: 'posted' | 'void'; total: string; applied: string }
export type InvoiceDetail = Invoice &
  DocDetailCommon & { payments: InvoicePayment[]; creditNotes: (NoteRef & { creditNoteId: string })[]; creditWarning?: string }

export type Bill = DocCommon & { supplierId: string; supplierName: string; supplierInvoiceNo: string; payable: string; debited: string; opening?: boolean }
export type BillPayment = { paymentId: string; number: string; date: string; status: string; amount: string }
export type BillDetail = Bill &
  DocDetailCommon & {
    withholdingCode?: string
    withholding: string
    payments: BillPayment[]
    debitNotes: (NoteRef & { debitNoteId: string })[]
    duplicates: { id: string; number: string | null; status: string }[]
  }

// Nota kredit/retur penjualan (AR-05) dan nota debit/retur pembelian (AP-05).
type NoteCommon = {
  id: string
  number: string
  status: 'posted' | 'void'
  date: string
  memo: string
  dpp: string
  vat: string
  total: string
  applied: string
  excess: string
}
type NoteDetailCommon = {
  totals: Totals
  journalEntryId: string
  createdAt: string
  createdBy?: string
  voidedBy?: string
  voidedAt?: string
  voidReason?: string
}
export type CreditNote = NoteCommon & { customerId: string; customerName: string; invoiceId: string; invoiceNumber: string }
export type CreditNoteDetail = CreditNote & NoteDetailCommon & { lines: (TradeLine & { invoiceLineId: string; restock: boolean })[] }
export type DebitNote = NoteCommon & {
  supplierId: string
  supplierName: string
  billId: string
  billNumber: string
  withholding: string
  payable: string
}
export type DebitNoteDetail = DebitNote & NoteDetailCommon & { supplierInvoiceNo: string; lines: (TradeLine & { billLineId: string })[] }

type SettlementCommon = {
  id: string
  number: string
  /** pelunasan/pembayaran, atau pengembalian uang muka. */
  kind: string
  date: string
  cashAccountId?: string
  cashAccount: string
  amount: string
  advance: string
  fromAdvance: string
  memo: string
  status: 'posted' | 'void'
  journalEntryId: string
}

export type Receipt = SettlementCommon & { customerId: string; customerName: string }
export type ReceiptDetail = Receipt & {
  allocated: string
  withholding: string
  allocations: { invoiceId: string; invoiceNumber: string; amount: string; withholdingCode?: string; withholding: string }[]
  createdBy?: string
  voidedBy?: string
  voidReason?: string
}

export type Payment = SettlementCommon & { supplierId: string; supplierName: string }
export type PaymentDetail = Payment & {
  allocations: { billId: string; billNumber: string; amount: string }[]
  createdBy?: string
  voidedBy?: string
  voidReason?: string
}

export type AgingBucket = 'current' | '1-30' | '31-60' | '61-90' | '90+'

export type Aging = {
  asOf: string
  rows: { contactId: string; contactName: string; buckets: Record<AgingBucket, string>; total: string; documents: number }[]
  totals: Record<AgingBucket, string>
  total: string
}

export type CashAccount = {
  accountId: string
  code: string
  name: string
  kind: 'kas' | 'bank'
  bankName: string
  accountNumber: string
  active: boolean
  balance: string
}

export type CashKind = 'in' | 'out' | 'transfer'

export type CashTransaction = {
  id: string
  /** Kosong selama draf; nomor BKM/BKK/TRF diberikan saat diposting. */
  number: string | null
  kind: CashKind
  date: string
  cashAccountId: string
  cashAccount: string
  toAccountId?: string
  toAccount?: string
  amount: string
  memo: string
  reference: string
  status: 'draft' | 'posted' | 'void'
  journalEntryId?: string
  branchId?: string
  departmentId?: string
}

export type CashTransactionDetail = CashTransaction & {
  /** accountId kosong pada draf yang akunnya belum dilengkapi. */
  lines: { accountId?: string; accountCode: string; accountName: string; amount: string; memo: string; projectId?: string; departmentId?: string }[]
  createdById?: string
  createdBy?: string
  createdAt: string
  postedBy?: string
  postedAt?: string
  voidReason?: string
  version: number
}

// Pajak dan Coretax ---------------------------------------------------------------------------

export type TaxStatus = 'ready' | 'exported' | 'approved' | 'cancelled' | 'replaced'

export type OutputInvoice = {
  invoiceId: string
  number: string
  date: string
  invoiceStatus: 'posted' | 'void'
  buyerId: string
  buyerName: string
  buyerNpwp: string
  dpp: string
  otherDpp: string
  vat: string
  trxCode: string
  status: TaxStatus
  taxInvoiceNumber?: string
  approvedDate?: string
  replacedBy?: string
  note: string
  problems: string[]
}

export type OutputList = { month: string; pkp: boolean; problems: string[]; items: OutputInvoice[] }

export type TaxSettings = { goodsOpt: 'A' | 'B'; goodsCode: string; unitCode: string }

export type ExportBatch = { id: string; period: string; format: string; invoiceCount: number; fileName: string; createdAt: string; createdBy?: string }

export type VATMonth = { month: string; invoices: number; output: string; bills: number; input: string; compensated: string; payable: string; overpaid: string }

export type VATRecap = { year: number; pkp: boolean; months: VATMonth[]; totals: VATMonth }

export type WithheldByCustomer = { receiptId: string; receiptNumber: string; date: string; contactName: string; invoiceNumber: string; dpp: string; code: string; amount: string }
export type WithheldByUs = { billId: string; billNumber: string; date: string; contactName: string; contactNpwp: string; supplierInvoiceNo: string; dpp: string; code: string; amount: string }
export type WithholdingRecap = {
  month: string
  byCustomers: WithheldByCustomer[]
  byUs: WithheldByUs[]
  customerSums: { code: string; count: number; amount: string }[]
  ourSums: { code: string; count: number; amount: string }[]
}

export type CompanyProfile = CompanyAccess & { npwp?: string; nitku?: string; address?: string; version: number }

/** Mengunduh file dari API (mis. XML Coretax). Mengembalikan nama file yang diunduh. */
// Produk dan persediaan -----------------------------------------------------------------------

export type ProductKind = 'barang' | 'jasa'

export type Product = {
  id: string
  code: string
  name: string
  kind: ProductKind
  unit: string
  salePrice: string
  purchasePrice: string
  revenueAccountId?: string | null
  expenseAccountId?: string | null
  inventoryAccountId?: string | null
  active: boolean
  version: number
  stockQty: string
  stockValue: string
  avgCost: string
}

export type ProductInput = Omit<Product, 'id' | 'stockQty' | 'stockValue' | 'avgCost'>

export type StockRow = {
  productId: string
  code: string
  name: string
  unit: string
  active: boolean
  inventoryAccountId: string
  qty: string
  value: string
  avgCost: string
}

export type StockReport = {
  asOf: string
  rows: StockRow[]
  totalValue: string
  accounts: { accountId: string; code: string; name: string; subledger: string; ledger: string; difference: string }[]
}

export type CardLine = {
  date: string
  sourceType: string
  sourceId: string
  number: string
  memo: string
  qtyIn: string
  qtyOut: string
  value: string
  unitCost: string
  qtyAfter: string
  valueAfter: string
  reversal: boolean
}

export type StockCard = {
  product: Product
  from: string
  to: string
  openingQty: string
  openingValue: string
  lines: CardLine[]
  closingQty: string
  closingValue: string
  truncated: boolean
}

export type AdjustmentKind = 'penyesuaian' | 'pemakaian' | 'opname'

export type Adjustment = {
  id: string
  number: string
  kind: AdjustmentKind
  date: string
  accountId: string
  accountCode: string
  accountName: string
  projectId?: string
  memo: string
  status: 'posted' | 'void'
  lineCount: number
}

export type AdjustmentDetail = Adjustment & {
  lines: {
    productId: string
    productCode: string
    productName: string
    unit: string
    systemQty?: string
    countedQty?: string
    qty: string
    unitCost?: string
    value: string
    memo: string
  }[]
  total: string
  createdBy?: string
  createdAt: string
  voidedBy?: string
  voidedAt?: string
  voidDate?: string
  voidReason?: string
}

export type AdjustmentInput = {
  kind: AdjustmentKind
  date: string
  accountId: string
  projectId?: string
  memo: string
  lines: { productId: string; qty: string; countedQty?: string; unitCost?: string; memo: string }[]
}

// Proyek ---------------------------------------------------------------------------------------

export type ProjectStatus = 'berjalan' | 'selesai' | 'batal'

export type Project = {
  id: string
  code: string
  name: string
  customerId?: string | null
  customerName: string
  contractValue: string
  dpPct: string
  retentionPct: string
  pphCode?: string | null
  revenueAccountId?: string | null
  startDate?: string | null
  endDate?: string | null
  status: ProjectStatus
  notes: string
  version: number
}

export type ProjectInput = Omit<Project, 'id' | 'customerName'>

export type ProjectSummary = Project & { budget: string; revenue: string; cost: string; profit: string; costPct?: string }

export type Billing = {
  contractValue: string
  advanceInvoiced: string
  advancePosted: boolean
  advanceDeducted: string
  advanceRemaining: string
  progressPct: string
  progressBilled: string
  unbilled: string
  terms: number
  retentionHeld: string
  retentionReleased: string
  drafts: number
}

export type BudgetLine = { accountId: string; accountCode: string; accountName: string; description: string; amount: string }

export type ProjectDetail = ProjectSummary & { budgetLines: BudgetLine[]; billing: Billing; invoices: Invoice[] }

export type ProjectReportRow = {
  accountId: string
  code: string
  name: string
  class: number
  budget: string
  actual: string
  remaining: string
  usedPct?: string
}

export type ProjectReport = {
  project: Project
  from: string
  to: string
  revenue: ProjectReportRow[]
  costs: ProjectReportRow[]
  totalRevenue: string
  totalBudget: string
  totalCost: string
  profit: string
  margin?: string
  costPct?: string
}

export type ProjectLine = {
  entryId: string
  number: string
  status: string
  sourceType: string
  sourceId?: string
  date: string
  memo: string
  accountId: string
  accountCode: string
  accountName: string
  debit: string
  credit: string
}

export type BillingInput = { kind: Exclude<InvoiceKind, 'biasa'>; date: string; progressPct?: string; taxCode?: string; memo: string }

export async function download(method: 'GET' | 'POST', path: string, body?: unknown): Promise<string> {
  const res = await fetch('/api/v1' + path, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    throw new ApiError(res.status, data?.code ?? 'unknown', data?.detail || data?.title || 'Unduhan gagal.', data?.fields ?? {})
  }
  const name = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'unduhan'
  const url = URL.createObjectURL(await res.blob())
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  return name
}

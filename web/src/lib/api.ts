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
}

export type Me = {
  user: { id: string; name: string; email: string }
  activeCompanyId: string | null
  companies: CompanyAccess[]
}

export type LicenseState = 'ACTIVE' | 'WARNING' | 'EXPIRED' | 'SUSPENDED' | 'UNVERIFIED' | 'INVALID_KEY'

export type LicenseStatus = {
  status: LicenseState
  expiresAt?: string
  daysLeft?: number
  message: string
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
}

export type Posted = Ref & { autoReversal?: Ref }
export type Voided = { reversal: Ref; alsoVoided?: Ref[] }

export type LineInput = { accountId: string; debit: string; credit: string; memo: string }

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
  address: string
  email: string
  phone: string
  paymentTermsDays: number
  bankName: string
  bankAccount: string
  active: boolean
  version: number
}

export type ContactInput = Omit<Contact, 'id'>

export type TaxCode = {
  code: string
  kind: 'ppn' | 'pph'
  name: string
  rateBp: number
  dppNum: number
  dppDen: number
  article?: '23' | '4(2)'
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
}

export type TradeLine = TradeLineInput & {
  gross: string
  discount: string
  net: string
  otherDpp: string
  vat: string
  accountCode: string
  accountName: string
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

export type Invoice = DocCommon & { customerId: string; customerName: string; dpp: string }
export type InvoicePayment = { receiptId: string; number: string; date: string; status: string; amount: string; withholding: string; withholdingCode?: string }
export type InvoiceDetail = Invoice & DocDetailCommon & { payments: InvoicePayment[] }

export type Bill = DocCommon & { supplierId: string; supplierName: string; supplierInvoiceNo: string; payable: string }
export type BillPayment = { paymentId: string; number: string; date: string; status: string; amount: string }
export type BillDetail = Bill &
  DocDetailCommon & {
    withholdingCode?: string
    withholding: string
    payments: BillPayment[]
    duplicates: { id: string; number: string | null; status: string }[]
  }

type SettlementCommon = {
  id: string
  number: string
  date: string
  cashAccountId: string
  cashAccount: string
  amount: string
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
  number: string
  kind: CashKind
  date: string
  cashAccountId: string
  cashAccount: string
  toAccountId?: string
  toAccount?: string
  amount: string
  memo: string
  reference: string
  status: 'posted' | 'void'
  journalEntryId: string
}

export type CashTransactionDetail = CashTransaction & {
  lines: { accountId: string; accountCode: string; accountName: string; amount: string; memo: string }[]
  createdBy?: string
  voidReason?: string
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

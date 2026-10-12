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

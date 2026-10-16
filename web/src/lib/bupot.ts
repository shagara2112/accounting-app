import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { ApiError, api, download, query, type ExportBatch } from './api'
import { useInvalidateCompany } from './invalidate'

// Faktur pajak masukan (TAX-07) dan bukti potong PPh 23/4(2)/26 (TAX-08).

export type InputMatchState = 'belum' | 'cocok' | 'selisih'

export type InputInvoice = {
  billId: string
  number: string
  date: string
  supplierId: string
  supplierName: string
  supplierNpwp: string
  supplierInvoiceNo: string
  dpp: string
  vat: string
  netVat: string
  taxInvoiceNumber?: string
  taxInvoiceDate?: string
  creditable: boolean
  /** YYYY-MM; kosong bila tidak dikreditkan. */
  creditPeriod?: string
  reclassEntryId?: string
  match: InputMatchState
  coretaxVat?: string
  note: string
  problems: string[]
}

export type InputList = { month: string; pkp: boolean; items: InputInvoice[]; credited: string; notCreditable: string; unrecorded: number }

export type InputMatch = {
  rows: number
  matched: string[]
  differences: { bill: string; number: string; lajurVat: string; coretaxVat: string }[]
  coretaxOnly: { line: number; supplier: string; npwp: string; number: string; date: string | null; vat: string }[]
  lajurOnly: string[]
}

export type SlipStatus = 'ready' | 'exported' | 'approved' | 'cancelled'

export type Slip = {
  billId: string
  number: string
  date: string
  supplierId: string
  supplierName: string
  counterpartTin: string
  foreign: boolean
  country: string
  supplierInvoiceNo: string
  code: string
  codeName: string
  kind: 'bppu' | 'bpnr'
  rateBp: number
  taxBase: string
  amount: string
  noted: boolean
  taxObjectCode: string
  taxObjectName: string
  facility: string
  facilityNumber: string
  deemed: number
  document: string
  documentNumber: string
  documentDate: string | null
  withholdingDate: string | null
  status: SlipStatus
  slipNumber?: string
  approvedDate?: string
  changed: boolean
  note: string
  problems: string[]
}

export type SlipList = { month: string; problems: string[]; items: Slip[]; objects: Record<string, string> }

export type SlipUpdate = {
  taxObjectCode: string
  facility: string
  facilityNumber: string
  deemed: number
  document: string
  documentNumber: string
  documentDate?: string
  withholdingDate?: string
  note: string
}

export type TaxObject = { code: string; name: string; rate: number; deemed?: number[] }
export type RefItem = { code: string; name: string }
export type BupotRef = {
  source: string
  bppuObjects: TaxObject[]
  bpnrObjects: TaxObject[]
  bppuFacilities: RefItem[]
  bpnrFacilities: RefItem[]
  documents: RefItem[]
  countries: RefItem[]
}

export type ImportResult = { recorded: string[]; skipped: string[] }

/** Unggah berkas Coretax (Excel/CSV) sebagai multipart. */
async function upload<T>(path: string, file: File, fields: Record<string, string> = {}): Promise<T> {
  const form = new FormData()
  for (const [k, v] of Object.entries(fields)) form.append(k, v)
  form.append('file', file)
  const res = await fetch('/api/v1' + path, { method: 'POST', credentials: 'same-origin', body: form })
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new ApiError(res.status, data?.code ?? 'unknown', data?.detail || data?.title || 'Berkas gagal diunggah.', data?.fields ?? {})
  return data as T
}

/** Referensi KOP, fasilitas, dokumen, dan negara; isinya tetap selama versi Lajur sama. */
export function useBupotRef(enabled = true) {
  return useQuery({ queryKey: ['bupot-ref'], queryFn: () => api<BupotRef>('GET', '/tax/bupot-ref'), enabled, staleTime: Infinity })
}

export function useInputs(companyId: string | null, month: string) {
  return useQuery({
    queryKey: ['tax', companyId, 'inputs', month],
    queryFn: () => api<InputList>('GET', '/tax/input-invoices' + query({ month })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useInputAction(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: async (a:
      | { kind: 'update'; id: string; taxInvoiceNumber: string; taxInvoiceDate: string; creditPeriod: string; note: string }
      | { kind: 'creditable'; id: string; creditable: boolean; date: string; reason: string; idempotencyKey: string }
      | { kind: 'import'; file: File }): Promise<unknown> => {
      switch (a.kind) {
        case 'update':
          return api<InputInvoice>('PUT', `/tax/input-invoices/${a.id}`, {
            taxInvoiceNumber: a.taxInvoiceNumber,
            taxInvoiceDate: a.taxInvoiceDate || undefined,
            creditPeriod: a.creditPeriod,
            note: a.note,
          })
        case 'creditable':
          return api<InputInvoice>('POST', `/tax/input-invoices/${a.id}/creditable`, { creditable: a.creditable, date: a.date || undefined, reason: a.reason }, { idempotencyKey: a.idempotencyKey })
        case 'import':
          return upload<InputMatch>('/tax/input-invoices/import', a.file)
      }
    },
    onSuccess: invalidate,
  })
}

export function useSlips(companyId: string | null, month: string) {
  return useQuery({
    queryKey: ['tax', companyId, 'slips', month],
    queryFn: () => api<SlipList>('GET', '/tax/slips' + query({ month })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useSlipBatches(companyId: string | null, month: string) {
  return useQuery({
    queryKey: ['tax', companyId, 'batches', month, 'bupot'],
    queryFn: async () => (await api<{ items: (ExportBatch & { kind?: string })[] }>('GET', '/tax/exports' + query({ month, kind: 'bupot' }))).items,
    enabled: companyId !== null,
  })
}

export function useSlipAction(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: async (a:
      | { kind: 'update'; id: string; input: SlipUpdate }
      | { kind: 'object'; ids: string[]; taxObjectCode: string }
      | { kind: 'export'; month: string; ids: string[] }
      | { kind: 'approve'; id: string; number: string; date: string }
      | { kind: 'reset'; id: string; note: string }
      | { kind: 'cancel'; id: string; note: string }
      | { kind: 'import'; month: string; file: File }
      | { kind: 'redownload'; batchId: string }): Promise<unknown> => {
      switch (a.kind) {
        case 'update':
          return api<Slip>('PUT', `/tax/slips/${a.id}`, a.input)
        case 'object':
          return api('PUT', '/tax/slips/object', { ids: a.ids, taxObjectCode: a.taxObjectCode })
        case 'export': {
          // Satu ekspor bisa menghasilkan dua berkas: BPPU (dalam negeri) dan BPNR (PPh 26).
          const { items } = await api<{ items: ExportBatch[] }>('POST', '/tax/slips/export', { month: a.month, ids: a.ids })
          const names: string[] = []
          for (const b of items) names.push(await download('GET', `/tax/exports/${b.id}`))
          return names.join(', ')
        }
        case 'approve':
          return api('POST', `/tax/slips/${a.id}/approve`, { number: a.number, date: a.date || undefined })
        case 'reset':
          return api('POST', `/tax/slips/${a.id}/reset`, { note: a.note })
        case 'cancel':
          return api('POST', `/tax/slips/${a.id}/cancel`, { note: a.note })
        case 'import':
          return upload<ImportResult>('/tax/slips/import', a.file, { month: a.month })
        case 'redownload':
          return download('GET', `/tax/exports/${a.batchId}`)
      }
    },
    onSuccess: invalidate,
  })
}

export function useWithholdingObjects(companyId: string | null) {
  return useQuery({
    queryKey: ['tax', companyId, 'withholding-objects'],
    queryFn: async () => (await api<{ objects: Record<string, string> }>('GET', '/tax/withholding-objects')).objects,
    enabled: companyId !== null,
  })
}

export function useSaveWithholdingObjects(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: async (objects: Record<string, string>) => (await api<{ objects: Record<string, string> }>('PUT', '/tax/withholding-objects', { objects })).objects,
    onSuccess: invalidate,
  })
}

/** Tarif persen KOP, mis. 2.65 → "2,65%". */
export function objectRate(rate: number): string {
  return rate.toLocaleString('id-ID', { maximumFractionDigits: 2 }) + '%'
}

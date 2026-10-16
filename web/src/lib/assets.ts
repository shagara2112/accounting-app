// Aset tetap (FA-01–03): register, kategori, penyusutan bulanan, pelepasan, pencocokan dengan
// buku besar, dan daftar penyusutan fiskal. Query key [asset, companyId, ...].
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { api, query } from './api'
import { useInvalidateCompany } from './invalidate'

export type DepMethod = 'garis_lurus' | 'saldo_menurun' | 'tidak_disusutkan'
export type FiscalMethod = 'garis_lurus' | 'saldo_menurun'

export const methodLabel: Record<DepMethod, string> = {
  garis_lurus: 'Garis lurus',
  saldo_menurun: 'Saldo menurun',
  tidak_disusutkan: 'Tidak disusutkan',
}

export type FiscalGroup = { code: string; label: string; years: number; declining: boolean }

export type AssetCategory = {
  id: string
  name: string
  assetAccountId: string
  accumAccountId?: string
  expenseAccountId?: string
  method: DepMethod
  lifeMonths: number
  fiscalGroup: string
  fiscalMethod: FiscalMethod
  active: boolean
  version: number
}

export type CategoryInput = Omit<AssetCategory, 'id' | 'accumAccountId' | 'expenseAccountId'> & { accumAccountId?: string | null; expenseAccountId?: string | null }

export type AssetStatus = 'aktif' | 'dilepas'

export type Asset = {
  id: string
  code: string
  name: string
  categoryId: string
  categoryName: string
  acquiredOn: string
  cost: string
  residual: string
  method: DepMethod
  lifeMonths: number
  depreciationStart: string
  accumulated: string
  bookValue: string
  location: string
  custodian: string
  branchId?: string
  departmentId?: string
  status: AssetStatus
  disposedOn?: string
  version: number
}

export type DepYear = { year: number; amount: string; accum: string; bookValue: string }
export type PostedDep = { month: string; amount: string; number: string; journalEntryId: string; disposal: boolean }

export type DisposalKind = 'jual' | 'faktur' | 'hapus'

export const disposalLabel: Record<DisposalKind, string> = {
  jual: 'Dijual',
  faktur: 'Dijual lewat faktur',
  hapus: 'Dihapus',
}

export type Disposal = {
  id: string
  number: string
  kind: DisposalKind
  date: string
  proceeds: string
  proceedsAccountId?: string
  cost: string
  accumulated: string
  bookValue: string
  gain: string
  memo: string
  journalEntryId: string
  status: 'posted' | 'void'
  createdAt: string
  createdBy?: string
  voidedBy?: string
  voidReason?: string
}

export type AssetDetail = Asset & {
  assetAccountId: string
  accumAccountId?: string
  expenseAccountId?: string
  fiscalGroup: string
  fiscalMethod: FiscalMethod
  openingAccum: string
  openingMonth?: string
  notes: string
  branchCode?: string
  departmentCode?: string
  createdBy?: string
  createdAt: string
  schedule: DepYear[]
  fiscal: DepYear[]
  nextMonth?: string
  nextAmount: string
  posted: PostedDep[]
  disposals: Disposal[]
  /** Nilai, umur, metode, dan akun terkunci karena penyusutannya sudah dijurnal. */
  locked: boolean
}

export type AssetInput = {
  code: string
  name: string
  categoryId: string
  /** Tanggal kosong tidak dikirim, supaya server memberi pesan per field atau memakai bawaannya. */
  acquiredOn?: string
  cost: string
  residual: string
  method: DepMethod | ''
  lifeMonths: number
  depreciationStart?: string
  assetAccountId?: string
  accumAccountId?: string
  expenseAccountId?: string
  fiscalGroup: string
  fiscalMethod: FiscalMethod | ''
  openingAccum: string
  openingMonth?: string
  location: string
  custodian: string
  notes: string
  branchId?: string
  departmentId?: string
  version: number
}

export type SchedulePreview = { monthly: string; schedule: DepYear[]; fiscal: DepYear[] }

export type DepRun = {
  id: string
  month: string
  number: string
  journalEntryId: string
  total: string
  assets: number
  status: 'posted' | 'void'
  postedAt: string
  postedBy?: string
  voidReason?: string
}
export type RunAsset = { assetId: string; code: string; name: string; months: number; firstMonth: string; amount: string }
export type RunDetail = DepRun & { voidedBy?: string; lines: RunAsset[] }
export type RunPreview = { month: string; total: string; lines: RunAsset[]; posted?: DepRun }

export type RecRow = { accountId: string; code: string; name: string; side: 'aset' | 'akumulasi'; register: string; ledger: string; difference: string }
export type Reconciliation = { rows: RecRow[]; matched: boolean }

export type FiscalRow = {
  assetId: string
  code: string
  name: string
  group: string
  groupLabel: string
  method: FiscalMethod
  acquiredOn: string
  cost: string
  openingBook: string
  fiscal: string
  closingBook: string
  commercial: string
  correction: string
  disposed: boolean
}
export type FiscalReport = { year: number; rows: FiscalRow[]; totalFiscal: string; totalCommercial: string; totalCorrection: string }

export function useAssetCategories(companyId: string | null) {
  return useQuery({
    queryKey: ['asset', companyId, 'categories'],
    queryFn: () => api<{ items: AssetCategory[]; fiscalGroups: FiscalGroup[] }>('GET', '/assets/categories'),
    enabled: companyId !== null,
  })
}

export function useSaveCategory(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, input }: { id?: string; input: CategoryInput }) =>
      id ? api<AssetCategory>('PUT', `/assets/categories/${id}`, input) : api<AssetCategory>('POST', '/assets/categories', input),
    onSuccess: invalidate,
  })
}

export function useDefaultCategories(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({ mutationFn: () => api('POST', '/assets/categories/defaults', {}), onSuccess: invalidate })
}

export function useAssets(companyId: string | null) {
  return useQuery({
    queryKey: ['asset', companyId, 'list'],
    queryFn: async () => (await api<{ items: Asset[] }>('GET', '/assets')).items,
    enabled: companyId !== null,
  })
}

export function useAsset(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: ['asset', companyId, 'detail', id],
    queryFn: () => api<AssetDetail>('GET', `/assets/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

/** Aset dari jurnal pelepasannya (sumber jurnal = id pelepasan). */
export function useDisposalAsset(companyId: string | null, disposalId: string | null) {
  return useQuery({
    queryKey: ['asset', companyId, 'disposal', disposalId],
    queryFn: () => api<AssetDetail>('GET', `/assets/disposals/${disposalId}`),
    enabled: companyId !== null && disposalId !== null,
  })
}

export function useSaveAsset(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, input }: { id?: string; input: AssetInput }) =>
      id ? api<AssetDetail>('PUT', `/assets/${id}`, input) : api<AssetDetail>('POST', '/assets', input),
    onSuccess: invalidate,
  })
}

export function useDeleteAsset(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, version }: { id: string; version: number }) => api('DELETE', `/assets/${id}` + query({ version })),
    onSuccess: invalidate,
  })
}

export function useAssetPreview(companyId: string | null, input: AssetInput | null) {
  return useQuery({
    queryKey: ['asset', companyId, 'preview', input],
    queryFn: () => api<SchedulePreview>('POST', '/assets/preview', input),
    enabled: companyId !== null && input !== null,
    placeholderData: keepPreviousData,
  })
}

export type DisposalInput = { date: string; kind: DisposalKind; proceeds: string; proceedsAccountId?: string; memo: string }

export function useDisposeAsset(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, input, idempotencyKey }: { id: string; input: DisposalInput; idempotencyKey: string }) =>
      api<AssetDetail>('POST', `/assets/${id}/dispose`, input, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useVoidDisposal(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, date, reason, idempotencyKey }: { id: string; date: string; reason: string; idempotencyKey: string }) =>
      api<AssetDetail>('POST', `/assets/disposals/${id}/void`, { date, reason }, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useDepRuns(companyId: string | null) {
  return useQuery({
    queryKey: ['asset', companyId, 'runs'],
    queryFn: async () => (await api<{ items: DepRun[] }>('GET', '/assets/depreciation')).items,
    enabled: companyId !== null,
  })
}

export function useDepRun(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: ['asset', companyId, 'run', id],
    queryFn: () => api<RunDetail>('GET', `/assets/depreciation/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

export function useRunPreview(companyId: string | null, month: string) {
  return useQuery({
    queryKey: ['asset', companyId, 'run-preview', month],
    queryFn: () => api<RunPreview>('GET', '/assets/depreciation/preview' + query({ month })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function usePostRun(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ month, idempotencyKey }: { month: string; idempotencyKey: string }) =>
      api<RunDetail>('POST', '/assets/depreciation', { month }, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useVoidRun(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, date, reason, idempotencyKey }: { id: string; date: string; reason: string; idempotencyKey: string }) =>
      api<RunDetail>('POST', `/assets/depreciation/${id}/void`, { date, reason }, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useAssetReconciliation(companyId: string | null) {
  return useQuery({
    queryKey: ['asset', companyId, 'reconciliation'],
    queryFn: () => api<Reconciliation>('GET', '/assets/reconciliation'),
    enabled: companyId !== null,
  })
}

export function useFiscalReport(companyId: string | null, year: number) {
  return useQuery({
    queryKey: ['asset', companyId, 'fiscal', year],
    queryFn: () => api<FiscalReport>('GET', '/assets/fiscal' + query({ year })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

/** Umur dalam bulan sebagai teks, mis. "4 tahun" atau "2 tahun 6 bulan". */
export function lifeText(months: number): string {
  if (months <= 0) return '—'
  const y = Math.floor(months / 12)
  const m = months % 12
  return [y > 0 && `${y} tahun`, m > 0 && `${m} bulan`].filter(Boolean).join(' ')
}

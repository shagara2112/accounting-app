// Anggaran tahunan (BUD-01), anggaran vs realisasi (BUD-02), dan rasio keuangan (RPT-09).
// Query key [budget, companyId, ...] dan [reports, companyId, ratios, ...].
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { api, query } from './api'
import { useInvalidateCompany } from './invalidate'

export type Budget = { id: string; fiscalYear: number; name: string; active: boolean; lines: number; updatedAt: string; version: number }

/** months: 12 nominal mulai bulan pertama tahun buku, menurut saldo normal akun. */
export type BudgetLine = { accountId: string; departmentId?: string; projectId?: string; months: string[]; total: string }
export type BudgetDetail = Budget & { months: string[]; lines: BudgetLine[] }
export type LineInput = { accountId: string; months: string[] }

/** Irisan anggaran: kosong = tingkat PT. */
export type Slice = { departmentId?: string; projectId?: string }

export type CompareRow = {
  accountId?: string
  code?: string
  name: string
  budget: string
  actual: string
  variance: string
  pct?: number
  yearBudget: string
  yearActual: string
  remaining: string
  /** Akun beban: near (≥ nearPct% anggaran setahun), over (> 100%), unbudgeted (tanpa anggaran). */
  status?: 'near' | 'over' | 'unbudgeted'
}
export type CompareSection = { class: number; name: string; expense: boolean; rows: CompareRow[]; total: CompareRow }
export type MonthPoint = { month: string; budgetRevenue: string; actualRevenue: string; budgetExpense: string; actualExpense: string }
export type Comparison = {
  budget?: Budget
  months: string[]
  from: string
  to: string
  sections: CompareSection[]
  revenue: CompareRow
  expense: CompareRow
  profit: CompareRow
  monthly: MonthPoint[]
  near: number
  over: number
  nearPct: number
}

export type RatioRow = { key: string; name: string; formula: string; unit: 'x' | '%' | 'hari'; guide: string; values: (number | null)[]; status?: 'good' | 'watch' | 'bad' }
export type Ratios = { months: string[]; rows: RatioRow[] }

const key = (companyId: string | null, ...rest: unknown[]) => ['budget', companyId, ...rest]

export function useBudgets(companyId: string | null) {
  return useQuery({
    queryKey: key(companyId, 'list'),
    queryFn: async () => (await api<{ items: Budget[] }>('GET', '/budgets')).items,
    enabled: companyId !== null,
  })
}

export function useBudget(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: key(companyId, 'detail', id),
    queryFn: () => api<BudgetDetail>('GET', `/budgets/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

export function useCreateBudget(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: (v: { fiscalYear: number; name: string; copyFrom?: string }) => api<Budget>('POST', '/budgets', v),
    onSuccess: invalidate,
  })
}

export function useRenameBudget(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; name: string; version: number }) => api<Budget>('PUT', `/budgets/${id}`, v),
    onSuccess: invalidate,
  })
}

export function useActivateBudget(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({ mutationFn: (id: string) => api<Budget>('POST', `/budgets/${id}/activate`), onSuccess: invalidate })
}

export function useDeleteBudget(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({ mutationFn: (id: string) => api<void>('DELETE', `/budgets/${id}`), onSuccess: invalidate })
}

export function useSaveSlice(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; version: number; lines: LineInput[] } & Slice) => api<BudgetDetail>('PUT', `/budgets/${id}/lines`, v),
    onSuccess: invalidate,
  })
}

/** Usulan anggaran dari realisasi tahun buku sebelumnya ditambah growth persen. */
export function suggestBudget(year: number, slice: Slice, growth: string) {
  return api<{ lines: LineInput[] }>('GET', '/budgets/suggest' + query({ year, department: slice.departmentId, project: slice.projectId, growth }))
}

export function useCompare(companyId: string | null, f: { year: number; from: number; to: number } & Slice) {
  return useQuery({
    queryKey: key(companyId, 'compare', f),
    queryFn: () => api<Comparison>('GET', '/budgets/compare' + query({ year: f.year, from: f.from, to: f.to, department: f.departmentId, project: f.projectId })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useRatios(companyId: string | null, month: string) {
  return useQuery({
    queryKey: ['reports', companyId, 'ratios', month],
    queryFn: () => api<Ratios>('GET', '/reports/ratios' + query({ month })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

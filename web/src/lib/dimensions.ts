// Dimensi cabang/departemen (GL-09), penomoran dokumen (GL-07), dan jurnal berulang (GL-08).
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Me } from './api'
import { useInvalidateCompany } from './invalidate'
import { activeCompany } from './session'

export type DimKind = 'cabang' | 'departemen'
export type Dimension = { id: string; kind: DimKind; code: string; name: string; active: boolean; version: number }

export function useDimensions(companyId: string | null, kind: DimKind) {
  return useQuery({
    queryKey: ['dimensions', companyId, kind],
    queryFn: async () => (await api<{ items: Dimension[] }>('GET', `/dimensions/${kind}`)).items,
    enabled: companyId !== null,
    staleTime: 60_000,
  })
}

export function useSaveDimension(companyId: string | null, kind: DimKind) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...v }: { id?: string; code: string; name: string; active: boolean; version: number }) =>
      api<Dimension>(id ? 'PUT' : 'POST', id ? `/dimensions/${kind}/${id}` : `/dimensions/${kind}`, v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['dimensions', companyId, kind] }),
  })
}

/** Cabang yang boleh dipakai pengguna (PLT-12): kosong = semua cabang aktif. */
export function allowedBranches(me: Me, branches: Dimension[]): Dimension[] {
  const restrict = activeCompany(me)?.branchIds ?? []
  return branches.filter((b) => b.active && (restrict.length === 0 || restrict.includes(b.id)))
}

export function isRestricted(me: Me): boolean {
  return (activeCompany(me)?.branchIds ?? []).length > 0
}

// Penomoran dokumen -------------------------------------------------------------------

export type NumberFormat = { series: string; label: string; prefix: string; reset: 'month' | 'year' | 'never'; digits: number; perBranch: boolean; custom: boolean; example: string }

export function useNumberFormats(companyId: string | null) {
  return useQuery({
    queryKey: ['ledger', companyId, 'number-formats'],
    queryFn: async () => (await api<{ items: NumberFormat[] }>('GET', '/ledger/number-formats')).items,
    enabled: companyId !== null,
  })
}

export function useSaveNumberFormat(companyId: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ series, ...v }: Pick<NumberFormat, 'series' | 'prefix' | 'reset' | 'digits' | 'perBranch'>) => api<NumberFormat>('PUT', `/ledger/number-formats/${series}`, v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['ledger', companyId, 'number-formats'] }),
  })
}

// Jurnal berulang ---------------------------------------------------------------------

export type RecurringLine = { accountId: string; accountCode: string; accountName: string; debit: string; credit: string; memo: string }
export type Recurring = {
  id: string
  name: string
  kind: 'umum' | 'penyesuaian'
  memo: string
  autoReverse: boolean
  dayOfMonth: number
  nextDate: string
  endDate?: string
  active: boolean
  lastError?: string
  lastRunAt?: string
  total: string
  version: number
  lines?: RecurringLine[]
  runs?: { date: string; entryId: string; number: string; status: string }[]
}
export type RecurringInput = { entryId?: string; name: string; dayOfMonth: number; startDate: string; endDate?: string; active: boolean; version: number }

export function useRecurringList(companyId: string | null) {
  return useQuery({
    queryKey: ['ledger', companyId, 'recurring'],
    queryFn: async () => (await api<{ items: Recurring[] }>('GET', '/ledger/recurring')).items,
    enabled: companyId !== null,
  })
}

export function useRecurring(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: ['ledger', companyId, 'recurring', id],
    queryFn: () => api<Recurring>('GET', `/ledger/recurring/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

export function useSaveRecurring(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, idempotencyKey, ...v }: RecurringInput & { id?: string; idempotencyKey: string }) =>
      id ? api<Recurring>('PUT', `/ledger/recurring/${id}`, v) : api<Recurring>('POST', '/ledger/recurring', v, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useDeleteRecurring(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({ mutationFn: (id: string) => api('DELETE', `/ledger/recurring/${id}`), onSuccess: invalidate })
}

export function useRunRecurring(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, idempotencyKey }: { id: string; idempotencyKey: string }) =>
      api<{ posted: number; recurring: Recurring }>('POST', `/ledger/recurring/${id}/run`, {}, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

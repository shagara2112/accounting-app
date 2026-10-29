// Panjar unit: kepala unit mengajukan kebutuhan unitnya (departemen) sebelum membeli, finance
// menyetujui atau menolak, lalu menandai sudah dibayar. Query key [panjar, companyId, ...].
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { api, query } from './api'
import { useInvalidateCompany } from './invalidate'

export type PanjarStatus = 'diajukan' | 'disetujui' | 'ditolak' | 'dibatalkan' | 'dibayar'

export type PanjarLine = { lineNo: number; description: string; accountId?: string; accountCode?: string; accountName?: string; amount: string }

export type Panjar = {
  id: string
  number: string
  departmentId: string
  departmentCode: string
  departmentName: string
  requestDate: string
  neededDate?: string
  purpose: string
  total: string
  status: PanjarStatus
  approvedAmount?: string
  decidedAt?: string
  decidedByName?: string
  decisionNote?: string
  paidAt?: string
  paidRef?: string
  createdAt: string
  createdBy: string
  createdByName: string
  /** Diajukan oleh pengguna yang sedang masuk. */
  mine: boolean
  version: number
}

export type PanjarDetail = Panjar & { lines: PanjarLine[] }

export type PanjarLineInput = { description: string; accountId?: string; amount: string }
export type PanjarInput = { departmentId: string; neededDate?: string; purpose: string; lines: PanjarLineInput[]; version: number }

/** Posisi anggaran beban unit di tahun buku berjalan. */
export type UnitBudget = {
  departmentId: string
  fiscalYear: number
  to: string
  hasBudget: boolean
  yearBudget: string
  actual: string
  committed: string
  remaining: string
}

/** Pengguna yang dibatasi ke unit tertentu (mis. Kepala unit) dan unit yang dikelolanya. */
export type UnitHead = { userId: string; name: string; email: string; active: boolean; roleName: string; departmentIds: string[] }

/** restricted: pembaca hanya mengelola units (ditetapkan Manager atau Pemilik). */
export type PanjarList = { items: Panjar[]; pending: number; restricted: boolean; units?: string[] }

export const statusLabel: Record<PanjarStatus, string> = {
  diajukan: 'Menunggu keputusan',
  disetujui: 'Disetujui',
  ditolak: 'Ditolak',
  dibatalkan: 'Dibatalkan',
  dibayar: 'Dibayar',
}

const key = (companyId: string | null, ...rest: unknown[]) => ['panjar', companyId, ...rest]

export function usePanjarList(companyId: string | null) {
  return useQuery({
    queryKey: key(companyId, 'list'),
    queryFn: () => api<PanjarList>('GET', '/panjar'),
    enabled: companyId !== null,
  })
}

export function usePanjar(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: key(companyId, 'detail', id),
    queryFn: () => api<PanjarDetail>('GET', `/panjar/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

export function useUnitBudget(companyId: string | null, departmentId: string) {
  return useQuery({
    queryKey: key(companyId, 'unit-budget', departmentId),
    queryFn: () => api<UnitBudget>('GET', '/panjar/unit-budget' + query({ department: departmentId })),
    enabled: companyId !== null && departmentId !== '',
    placeholderData: keepPreviousData,
  })
}

export function useSavePanjar(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, input, idempotencyKey }: { id?: string; input: PanjarInput; idempotencyKey: string }) =>
      id ? api<PanjarDetail>('PUT', `/panjar/${id}`, input) : api<PanjarDetail>('POST', '/panjar', input, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useCancelPanjar(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, version }: { id: string; version: number }) => api<PanjarDetail>('POST', `/panjar/${id}/cancel`, { version }),
    onSuccess: invalidate,
  })
}

/** amount kosong = disetujui sebesar pengajuan. */
export function useDecidePanjar(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; approve: boolean; amount?: string; note: string; version: number }) => api<PanjarDetail>('POST', `/panjar/${id}/decide`, v),
    onSuccess: invalidate,
  })
}

export function usePaidPanjar(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; date: string; ref: string; version: number }) => api<PanjarDetail>('POST', `/panjar/${id}/paid`, v),
    onSuccess: invalidate,
  })
}

export function useUnitHeads(companyId: string | null, enabled: boolean) {
  return useQuery({
    queryKey: key(companyId, 'unit-heads'),
    queryFn: async () => (await api<{ items: UnitHead[] }>('GET', '/panjar/unit-heads')).items,
    enabled: companyId !== null && enabled,
  })
}

export function useSetUnits(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ userId, departmentIds }: { userId: string; departmentIds: string[] }) => api<UnitHead>('PUT', `/panjar/unit-heads/${userId}`, { departmentIds }),
    onSuccess: invalidate,
  })
}

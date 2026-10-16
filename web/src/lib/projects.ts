// Hook data proyek: daftar dengan laba per proyek, detail dengan RAB dan tagihan, laporan
// biaya vs RAB, serta pembuatan draf faktur uang muka, termin, dan pelepasan retensi.
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import {
  api,
  query,
  type BillingInput,
  type InvoiceDetail,
  type ProjectDetail,
  type ProjectInput,
  type ProjectLine,
  type ProjectReport,
  type ProjectStatus,
  type ProjectSummary,
} from './api'
import { useInvalidateCompany } from './invalidate'

export function useProjects(companyId: string | null, f: { status?: string; q?: string } = {}, enabled = true) {
  return useQuery({
    queryKey: ['projects', companyId, 'list', f],
    queryFn: async () => (await api<{ items: ProjectSummary[] }>('GET', '/projects' + query({ status: f.status, q: f.q }))).items,
    enabled: companyId !== null && enabled,
    placeholderData: keepPreviousData,
  })
}

/** Pilihan proyek untuk formulir: proyek yang tidak dibatalkan, ditambah proyek yang sedang
 *  terpilih (supaya draf lama tetap terbaca). */
export function useProjectOptions(companyId: string | null, enabled: boolean, selected: string[] = []) {
  const list = useProjects(companyId, {}, enabled)
  const key = selected.join(',')
  return useMemo(() => {
    const keep = new Set(key.split(','))
    return (list.data ?? [])
      .filter((p) => p.status !== 'batal' || keep.has(p.id))
      .map((p) => ({ id: p.id, label: `${p.code} · ${p.name}`, detail: p.customerName || undefined }))
  }, [list.data, key])
}

/** Peta id proyek → "kode · nama" untuk menampilkan dimensi proyek di jurnal dan dokumen. */
export function useProjectNames(companyId: string | null, enabled: boolean) {
  const list = useProjects(companyId, {}, enabled)
  return useMemo(() => new Map((list.data ?? []).map((p) => [p.id, `${p.code} · ${p.name}`])), [list.data])
}

export function useProject(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: ['projects', companyId, 'detail', id],
    queryFn: () => api<ProjectDetail>('GET', `/projects/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

export function useSaveProject(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, input }: { id?: string; input: ProjectInput }) =>
      id ? api<ProjectDetail>('PUT', `/projects/${id}`, input) : api<ProjectDetail>('POST', '/projects', input),
    onSuccess: invalidate,
  })
}

export function useSaveBudget(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, lines }: { id: string; lines: { accountId: string; description: string; amount: string }[] }) =>
      api<ProjectDetail>('PUT', `/projects/${id}/budget`, { lines }),
    onSuccess: invalidate,
  })
}

export function useProjectReport(companyId: string | null, id: string | null, f: { from?: string; to?: string }) {
  return useQuery({
    queryKey: ['projects', companyId, 'report', id, f],
    queryFn: () => api<ProjectReport>('GET', `/projects/${id}/report` + query(f)),
    enabled: companyId !== null && id !== null,
    placeholderData: keepPreviousData,
  })
}

export function useProjectLines(companyId: string | null, id: string | null, f: { account?: string; from?: string; to?: string }, enabled = true) {
  return useQuery({
    queryKey: ['projects', companyId, 'lines', id, f],
    queryFn: () => api<{ items: ProjectLine[]; truncated: boolean }>('GET', `/projects/${id}/lines` + query(f)),
    enabled: companyId !== null && id !== null && enabled,
    placeholderData: keepPreviousData,
  })
}

export function useCreateBilling(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, input, idempotencyKey }: { id: string; input: BillingInput; idempotencyKey: string }) =>
      api<InvoiceDetail>('POST', `/projects/${id}/billings`, input, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export const projectStatus: Record<ProjectStatus, { label: string; tone: 'accent' | 'good' | 'bad' }> = {
  berjalan: { label: 'Berjalan', tone: 'accent' },
  selesai: { label: 'Selesai', tone: 'good' },
  batal: { label: 'Batal', tone: 'bad' },
}

export const invoiceKindLabel = {
  biasa: 'Faktur',
  uang_muka: 'Uang muka',
  termin: 'Termin',
  retensi: 'Pelepasan retensi',
  saldo_awal: 'Saldo awal',
} as const

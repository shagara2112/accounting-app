// Impor massal Excel/CSV (PLT-07), saldo awal (GL-06), dan rekap penjualan harian (IND-07).
import { useMutation, useQuery } from '@tanstack/react-query'
import { api, ApiError, newIdempotencyKey, type EntrySummary } from './api'
import { useInvalidateCompany } from './invalidate'

export type ImportKind = { kind: string; title: string; perm: string; needDate: boolean }
export type ImportIssue = { row: number; column?: string; message: string; warning?: boolean }
export type ImportResult = { kind: string; rows: number; dryRun: boolean; applied: boolean; errors: number; issues: ImportIssue[]; summary: string[] }
export type ImportStatus = {
  openingAccount: { code: string; name: string; balance: string }
  openingEntries: EntrySummary[]
}

export function useImportKinds(companyId: string | null) {
  return useQuery({
    queryKey: ['imports', companyId, 'kinds'],
    queryFn: async () => (await api<{ items: ImportKind[] }>('GET', '/imports')).items,
    enabled: companyId !== null,
    staleTime: Infinity,
  })
}

export function useImportStatus(companyId: string | null, enabled = true) {
  return useQuery({
    queryKey: ['imports', companyId, 'status'],
    queryFn: () => api<ImportStatus>('GET', '/imports/status'),
    enabled: companyId !== null && enabled,
  })
}

/** Mengirim berkas: dryRun = pratinjau (tidak ada yang disimpan). */
export function useRunImport(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: async ({ kind, file, date, dryRun }: { kind: string; file: File; date: string; dryRun: boolean }) => {
      const form = new FormData()
      if (date) form.append('date', date)
      form.append('dryRun', dryRun ? '1' : '0')
      form.append('file', file)
      const res = await fetch(`/api/v1/imports/${kind}`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Idempotency-Key': newIdempotencyKey() },
        body: form,
      })
      const data = await res.json().catch(() => null)
      if (!res.ok) throw new ApiError(res.status, data?.code ?? 'unknown', data?.detail || 'Impor gagal.', data?.fields ?? {})
      return data as ImportResult
    },
    onSuccess: (r) => {
      if (r.applied) invalidate()
    },
  })
}

// Halaman Saldo awal: saldo per akun diketik di layar, hasilnya satu jurnal saldo awal (seri SA).
export type OpeningLine = { accountId: string; debit: string; credit: string }
export type ControlledAccount = { accountId: string; kind: 'piutang-awal' | 'utang-awal' | 'stok-awal'; reason: string; balance: string }
export type OpeningState = {
  entryId?: string
  number?: string
  date?: string
  revision: number
  lines: OpeningLine[]
  controlled: ControlledAccount[]
  openingEquityId: string
  /** Saldo ekuitas saldo awal per tanggal saldo awal (debit − kredit); nol = lengkap. */
  openingEquity: string
}

export function useOpening(companyId: string | null) {
  return useQuery({ queryKey: ['imports', companyId, 'opening'], queryFn: () => api<OpeningState>('GET', '/imports/opening'), enabled: companyId !== null })
}

export function useSaveOpening(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: (v: { date: string; lines: OpeningLine[]; revision: number; reason: string }) => api<OpeningState>('PUT', '/imports/opening', v),
    onSuccess: invalidate,
  })
}

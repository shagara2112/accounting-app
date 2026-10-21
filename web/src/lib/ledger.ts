import { keepPreviousData, useInfiniteQuery, useMutation, useQuery } from '@tanstack/react-query'
import { useInvalidateCompany } from './invalidate'
import {
  api,
  query,
  type Account,
  type AccountLedger,
  type AccountsResponse,
  type EntryDetail,
  type EntryPage,
  type FiscalYear,
  type LedgerSettings,
  type LineInput,
  type PeriodStatus,
  type Posted,
  type SystemAccount,
  type Voided,
  type Worksheet,
} from './api'

// Semua query buku besar diawali ['ledger', companyId], supaya satu invalidasi menyegarkan
// semua tampilan PT itu setelah posting, void, atau tutup buku.
const key = (companyId: string | null, ...rest: unknown[]) => ['ledger', companyId, ...rest]

// Posting jurnal juga mengubah saldo kas dan laporan modul lain, jadi semua data PT disegarkan.
const useInvalidateLedger = useInvalidateCompany

export function useAccounts(companyId: string | null) {
  return useQuery({
    queryKey: key(companyId, 'accounts'),
    queryFn: () => api<AccountsResponse>('GET', '/ledger/accounts'),
    enabled: companyId !== null,
    staleTime: 30_000,
  })
}

export function useCreateAccount(companyId: string | null) {
  const invalidate = useInvalidateLedger(companyId)
  return useMutation({
    mutationFn: (v: { code: string; name: string; parentId: string | null; postable: boolean }) =>
      api<Account>('POST', '/ledger/accounts', v),
    onSuccess: invalidate,
  })
}

export function useUpdateAccount(companyId: string | null) {
  const invalidate = useInvalidateLedger(companyId)
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; code: string; name: string; active: boolean; version: number; parentId?: string }) =>
      api<Account>('PATCH', `/ledger/accounts/${id}`, v),
    onSuccess: invalidate,
  })
}

/** Peran akun sistem yang dibutuhkan hampir semua PT: posting faktur, tagihan, PPN, dan Saldo awal. */
export const essentialRoles = ['piutang_usaha', 'utang_usaha', 'ppn_keluaran', 'ppn_masukan', 'ekuitas_saldo_awal']

export function useSystemAccounts(companyId: string | null) {
  return useQuery({
    queryKey: key(companyId, 'system-accounts'),
    queryFn: () => api<{ items: SystemAccount[] }>('GET', '/ledger/system-accounts'),
    enabled: companyId !== null,
    staleTime: 30_000,
  })
}

export function useSetSystemAccount(companyId: string | null) {
  const invalidate = useInvalidateLedger(companyId)
  return useMutation({
    mutationFn: ({ role, accountId }: { role: string; accountId: string }) =>
      api<{ items: SystemAccount[] }>('PUT', `/ledger/system-accounts/${role}`, { accountId }),
    onSuccess: invalidate,
  })
}

export function useApplyTemplate(companyId: string | null) {
  const invalidate = useInvalidateLedger(companyId)
  return useMutation({
    mutationFn: (template: string) => api<{ created: number }>('POST', '/ledger/accounts/template', { template }),
    onSuccess: invalidate,
  })
}

export type EntryFilter = { from: string; to: string; kind: string; q: string }

export function useEntries(companyId: string | null, f: EntryFilter) {
  return useInfiniteQuery({
    queryKey: key(companyId, 'entries', f),
    queryFn: ({ pageParam }) =>
      api<EntryPage>('GET', '/journals' + query({ from: f.from, to: f.to, kind: f.kind, q: f.q, cursor: pageParam, limit: 50 })),
    initialPageParam: '',
    getNextPageParam: (last) => last.nextCursor,
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useEntry(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: key(companyId, 'entry', id),
    queryFn: () => api<EntryDetail>('GET', `/journals/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

export type JournalInput = { date: string; memo: string; autoReverse: boolean; lines: LineInput[] }

export function usePostJournal(companyId: string | null) {
  const invalidate = useInvalidateLedger(companyId)
  return useMutation({
    mutationFn: ({ kind, input, idempotencyKey }: { kind: 'umum' | 'penyesuaian'; input: JournalInput; idempotencyKey: string }) =>
      api<Posted>('POST', kind === 'umum' ? '/journals' : '/adjustments', input, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export type ReviseInput = { date: string; memo: string; lines: LineInput[]; reason: string; revision: number }

/** Mengubah jurnal manual yang periodenya belum dikunci; versi lama disimpan di riwayat. */
export function useReviseJournal(companyId: string | null) {
  const invalidate = useInvalidateLedger(companyId)
  return useMutation({
    mutationFn: ({ id, ...input }: ReviseInput & { id: string }) => api<EntryDetail>('PUT', `/journals/${id}`, input),
    onSuccess: invalidate,
  })
}

export function useVoidJournal(companyId: string | null) {
  const invalidate = useInvalidateLedger(companyId)
  return useMutation({
    mutationFn: ({ id, date, reason, idempotencyKey }: { id: string; date: string; reason: string; idempotencyKey: string }) =>
      api<Voided>('POST', `/journals/${id}/void`, { date, reason }, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useWorksheet(companyId: string | null, month: string) {
  return useQuery({
    queryKey: key(companyId, 'worksheet', month),
    queryFn: () => api<Worksheet>('GET', '/ledger/worksheet' + query({ month })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

/** branch/department: id, 'none' (tanpa dimensi), atau kosong (semua). */
export function useAccountLedger(companyId: string | null, accountId: string | null, from: string, to: string, branch = '', department = '') {
  return useQuery({
    queryKey: key(companyId, 'account-ledger', accountId, from, to, branch, department),
    queryFn: () => api<AccountLedger>('GET', `/ledger/accounts/${accountId}/ledger` + query({ from, to, branch, department })),
    enabled: companyId !== null && accountId !== null && from !== '' && to !== '' && from <= to,
    placeholderData: keepPreviousData,
  })
}

export function usePeriods(companyId: string | null, year: number) {
  return useQuery({
    queryKey: key(companyId, 'periods', year),
    queryFn: () => api<FiscalYear>('GET', '/ledger/periods' + query({ year })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useSetPeriodStatus(companyId: string | null) {
  const invalidate = useInvalidateLedger(companyId)
  return useMutation({
    mutationFn: ({ month, status, reason }: { month: string; status: PeriodStatus; reason: string }) =>
      api('POST', `/ledger/periods/${month}/status`, { status, reason }),
    onSuccess: invalidate,
  })
}

export function useLedgerSettings(companyId: string | null) {
  return useQuery({
    queryKey: key(companyId, 'settings'),
    queryFn: () => api<LedgerSettings>('GET', '/ledger/settings'),
    enabled: companyId !== null,
  })
}

export function useUpdateLedgerSettings(companyId: string | null) {
  const invalidate = useInvalidateLedger(companyId)
  return useMutation({
    mutationFn: (fiscalYearStartMonth: number) => api('PUT', '/ledger/settings', { fiscalYearStartMonth }),
    onSuccess: invalidate,
  })
}

/** Urutan pohon bagan akun: setiap induk diikuti anak-anaknya (urut kode), supaya akun yang
 *  dipindah ke induk lain tampil di bawah induk barunya walau kodenya belum disesuaikan. */
export function treeOrder(list: Account[]): Account[] {
  const children = new Map<string, Account[]>()
  const roots: Account[] = []
  for (const a of list) {
    if (!a.parentId) roots.push(a)
    else children.set(a.parentId, [...(children.get(a.parentId) ?? []), a])
  }
  const byCode = (x: Account, y: Account) => x.code.localeCompare(y.code)
  const out: Account[] = []
  const walk = (a: Account) => {
    out.push(a)
    for (const c of (children.get(a.id) ?? []).sort(byCode)) walk(c)
  }
  roots.sort(byCode).forEach(walk)
  // Akun yang induknya tidak ada di daftar (seharusnya tidak terjadi) tetap ditampilkan.
  return out.length === list.length ? out : [...out, ...list.filter((a) => !out.includes(a))]
}

/** Id akun a beserta seluruh turunannya. */
export function subtreeIds(list: Account[], id: string): Set<string> {
  const out = new Set([id])
  let grew = true
  while (grew) {
    grew = false
    for (const a of list) {
      if (a.parentId && out.has(a.parentId) && !out.has(a.id)) {
        out.add(a.id)
        grew = true
      }
    }
  }
  return out
}

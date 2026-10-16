import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, query, type CashAccount, type CashKind, type CashTransaction, type CashTransactionDetail, type Contact, type ContactInput } from './api'
import { useInvalidateCompany } from './invalidate'

export function useCashAccounts(companyId: string | null) {
  return useQuery({
    queryKey: ['cash', companyId, 'accounts'],
    queryFn: async () => (await api<{ items: CashAccount[] }>('GET', '/cash/accounts')).items,
    enabled: companyId !== null,
  })
}

export function useRegisterCashAccount(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ idempotencyKey, ...v }: { accountId: string; kind: 'kas' | 'bank'; bankName: string; accountNumber: string; active: boolean; idempotencyKey: string }) =>
      api<CashAccount>('POST', '/cash/accounts', v, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useCashTransactions(companyId: string | null, f: { from?: string; to?: string; account?: string; drafts?: '1' }) {
  return useQuery({
    queryKey: ['cash', companyId, 'transactions', f],
    queryFn: () => api<{ items: CashTransaction[]; drafts: number; draftAmount: string }>('GET', '/cash/transactions' + query(f)),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useCashTransaction(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: ['cash', companyId, 'transaction', id],
    queryFn: () => api<CashTransactionDetail>('GET', `/cash/transactions/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

export type CashInput = {
  kind: CashKind
  date: string
  cashAccountId: string
  toAccountId?: string
  amount: string
  memo: string
  reference: string
  branchId?: string
  departmentId?: string
  /** accountId boleh kosong (tidak dikirim) untuk draf. */
  lines: { accountId?: string; amount: string; memo: string; projectId?: string; departmentId?: string }[]
}

/** Membuat draf (tanpa id) atau mengubah draf; akun lawan boleh belum diisi. */
export function useSaveCashDraft(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, version, input, idempotencyKey }: { id?: string; version?: number; input: CashInput; idempotencyKey: string }) =>
      id ? api<CashTransactionDetail>('PUT', `/cash/drafts/${id}`, { ...input, version }) : api<CashTransactionDetail>('POST', '/cash/drafts', input, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function usePostCashDraft(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, idempotencyKey }: { id: string; idempotencyKey: string }) => api<CashTransactionDetail>('POST', `/cash/drafts/${id}/post`, {}, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useDeleteCashDraft(companyId: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api('DELETE', `/cash/drafts/${id}`),
    // Detail draf yang sudah dihapus tidak dimuat ulang (pasti 404); laci yang menampilkannya
    // menutup dengan data terakhir.
    onSuccess: (_, id) =>
      qc.invalidateQueries({ predicate: (q) => q.queryKey[1] === companyId && !(q.queryKey[2] === 'transaction' && q.queryKey[3] === id) }),
  })
}

export function useRecordCash(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ input, idempotencyKey }: { input: CashInput; idempotencyKey: string }) =>
      api<CashTransactionDetail>('POST', '/cash/transactions', input, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useVoidCash(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, date, reason, idempotencyKey }: { id: string; date: string; reason: string; idempotencyKey: string }) =>
      api<CashTransactionDetail>('POST', `/cash/transactions/${id}/void`, { date, reason }, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useContacts(companyId: string | null, f: { role?: 'customer' | 'supplier'; q?: string; inactive?: boolean } = {}) {
  return useQuery({
    queryKey: ['contacts', companyId, f],
    queryFn: async () => (await api<{ items: Contact[] }>('GET', '/contacts' + query({ role: f.role, q: f.q, inactive: f.inactive ? 1 : undefined }))).items,
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useSaveContact(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, input, idempotencyKey }: { id?: string; input: ContactInput; idempotencyKey: string }) =>
      id ? api<Contact>('PUT', `/contacts/${id}`, input) : api<Contact>('POST', '/contacts', input, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

// Rekonsiliasi bank (CB-04, CB-05) ------------------------------------------------------

export type Reconciliation = {
  id: string
  accountId: string
  accountCode: string
  accountName: string
  statementDate: string
  statementBalance: string
  openingBalance: string
  status: 'open' | 'closed'
  items: number
  closedAt?: string
  version: number
}

export type ReconLine = { id: string; entryId: string; date: string; number: string; memo: string; debit: string; credit: string; sourceType: string; cleared: boolean }

export type ReconDetail = Reconciliation & {
  lines: ReconLine[]
  cleared: string
  expected: string
  difference: string
  bookBalance: string
  inTransit: string
  outstanding: string
  firstRecon: boolean
}

export function useReconciliations(companyId: string | null, account?: string) {
  return useQuery({
    queryKey: ['cash', companyId, 'recons', account ?? ''],
    queryFn: async () => (await api<{ items: Reconciliation[] }>('GET', '/cash/reconciliations' + query({ account }))).items,
    enabled: companyId !== null,
  })
}

export function useReconciliation(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: ['cash', companyId, 'recon', id],
    queryFn: () => api<ReconDetail>('GET', `/cash/reconciliations/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

function useReconMutation<V>(companyId: string | null, fn: (v: V) => Promise<ReconDetail>) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: (d) => {
      qc.setQueryData(['cash', companyId, 'recon', d.id], d)
      void qc.invalidateQueries({ queryKey: ['cash', companyId, 'recons'] })
    },
  })
}

export type ReconInput = { accountId: string; statementDate: string; statementBalance: string; openingBalance: string; version?: number }

export function useStartRecon(companyId: string | null) {
  return useReconMutation(companyId, ({ key, ...v }: ReconInput & { key: string }) => api<ReconDetail>('POST', '/cash/reconciliations', v, { idempotencyKey: key }))
}

export function useUpdateRecon(companyId: string | null) {
  return useReconMutation(companyId, ({ id, ...v }: ReconInput & { id: string; version: number }) => api<ReconDetail>('PUT', `/cash/reconciliations/${id}`, v))
}

export function useClearLines(companyId: string | null) {
  return useReconMutation(companyId, ({ id, ...v }: { id: string; lineIds: string[]; cleared: boolean }) => api<ReconDetail>('PUT', `/cash/reconciliations/${id}/items`, v))
}

export function useCloseRecon(companyId: string | null) {
  return useReconMutation(companyId, ({ id, key, reopen }: { id: string; key: string; reopen?: boolean }) =>
    api<ReconDetail>('POST', `/cash/reconciliations/${id}/${reopen ? 'reopen' : 'close'}`, {}, { idempotencyKey: key }),
  )
}

export function useDeleteRecon(companyId: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api('DELETE', `/cash/reconciliations/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['cash', companyId, 'recons'] }),
  })
}

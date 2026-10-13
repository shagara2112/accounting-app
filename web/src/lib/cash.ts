import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
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

export function useCashTransactions(companyId: string | null, f: { from: string; to: string; account?: string }) {
  return useQuery({
    queryKey: ['cash', companyId, 'transactions', f],
    queryFn: async () => (await api<{ items: CashTransaction[] }>('GET', '/cash/transactions' + query(f))).items,
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
  lines: { accountId: string; amount: string; memo: string }[]
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

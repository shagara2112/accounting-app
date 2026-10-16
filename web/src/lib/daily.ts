// Rekap penjualan harian retail (IND-07) dan rekap PPN digunggung (TAX-12).
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { api, query } from './api'
import { useInvalidateCompany } from './invalidate'

export type DailySale = {
  id: string
  number: string
  status: 'posted' | 'void'
  date: string
  store: string
  memo: string
  dpp: string
  otherDpp: string
  vat: string
  total: string
}

export type DailySaleDetail = DailySale & {
  lines: { productId?: string; description: string; accountCode: string; accountName: string; qty: string; amount: string; taxCode: string; net: string; vat: string }[]
  payments: { accountId: string; accountCode: string; accountName: string; amount: string }[]
  journalEntryId: string
  createdAt: string
  createdBy?: string
  voidedBy?: string
  voidReason?: string
}

export type Digunggung = {
  month: string
  company: string
  pkp: boolean
  rows: (Omit<DailySale, 'status' | 'memo'> & { id: string })[]
  totals: { dpp: string; otherDpp: string; vat: string; total: string }
}

export function useDailySales(companyId: string | null, f: { from: string; to: string }) {
  return useQuery({
    queryKey: ['sales', companyId, 'daily', f],
    queryFn: async () => (await api<{ items: DailySale[] }>('GET', '/sales/daily' + query(f))).items,
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useDailySale(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: ['sales', companyId, 'daily-sale', id],
    queryFn: () => api<DailySaleDetail>('GET', `/sales/daily/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

export function useVoidDailySale(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, date, reason, idempotencyKey }: { id: string; date: string; reason: string; idempotencyKey: string }) =>
      api<DailySaleDetail>('POST', `/sales/daily/${id}/void`, { date, reason }, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useDigunggung(companyId: string | null, month: string) {
  return useQuery({
    queryKey: ['tax', companyId, 'digunggung', month],
    queryFn: () => api<Digunggung>('GET', '/tax/digunggung' + query({ month })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

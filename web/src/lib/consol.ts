// Hook multi-entitas: pasangan dokumen antar-PT, bagan akun grup dan pemetaannya, serta
// laporan konsolidasi (ME-03, ME-04).
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { api, download, query } from './api'
import { useInvalidateCompany } from './invalidate'
import type { Statement } from './reports'

export type DocType = 'sales.invoice' | 'purchase.bill' | 'sales.credit_note' | 'purchase.debit_note'
export type DocLink = { companyId: string; companyName: string; docType: DocType; docId: string; number: string; mirror: boolean }

export function useDocLink(companyId: string | null, docType: DocType, id: string | null, enabled = true) {
  return useQuery({
    queryKey: ['consol', companyId, 'link', docType, id],
    queryFn: async () => (await api<{ link: DocLink | null }>('GET', `/consol/links/${docType.replace('.', '-')}/${id}`)).link,
    enabled: companyId !== null && id !== null && enabled,
  })
}

export type GroupMember = { companyId: string; accountId: string; code: string; name: string; mapped: boolean }

export type GroupAccount = {
  code: string
  name: string
  parentCode?: string
  postable: boolean
  class: number
  level: number
  stored: boolean
  conflict: boolean
  members: GroupMember[]
}

export type CompanyRef = { id: string; name: string; shortName: string }

export type GroupChart = { companies: CompanyRef[]; accounts: GroupAccount[]; problems: string[] }

export function useGroupChart(companyId: string | null) {
  return useQuery({
    queryKey: ['consol', companyId, 'chart'],
    queryFn: () => api<GroupChart>('GET', '/consol/accounts'),
    enabled: companyId !== null,
  })
}

export function useSaveGroupAccount(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: (v: { code: string; name: string; parentCode?: string }) => api('PUT', '/consol/accounts', v),
    onSuccess: invalidate,
  })
}

export function useSetMapping(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: (v: { accountId: string; groupCode: string }) => api('PUT', '/consol/map', v),
    onSuccess: invalidate,
  })
}

export type Elimination = { kind: string; memo: string; lines: { code: string; name: string; debit: string; credit: string }[] }

export type Difference = { kind: string; seller: string; buyer: string; sellerAmount: string; buyerAmount: string; difference: string; note: string }

export type Consolidation = {
  companies: CompanyRef[]
  asOf: string
  from: string
  to: string
  balanceSheet: Statement
  profitLoss: Statement
  eliminations: Elimination[]
  differences: Difference[]
  problems: string[]
}

export type ConsolParams = { companies: string; date: string; from: string; to: string }

export function useConsolidation(companyId: string | null, p: ConsolParams) {
  return useQuery({
    queryKey: ['consol', companyId, 'report', p],
    queryFn: () => api<Consolidation>('GET', '/consol/report' + query(p)),
    enabled: companyId !== null && p.companies !== '',
    placeholderData: keepPreviousData,
  })
}

export function exportConsolidation(p: ConsolParams & { statement: 'neraca' | 'laba-rugi'; format: 'xlsx' | 'csv' }): Promise<string> {
  return download('GET', '/consol/report/export' + query(p))
}

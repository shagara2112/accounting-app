// Hook laporan keuangan dan dashboard. Laporan dihitung server dari buku besar; browser hanya
// menampilkan dan meminta berkas ekspor.
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { api, download, query, type CashAccount, type Invoice } from './api'

export type ReportLineKind = 'section' | 'group' | 'account' | 'computed' | 'subtotal' | 'total' | 'result'

export type ReportLine = {
  kind: ReportLineKind
  code?: string
  name: string
  depth: number
  accountId?: string
  values?: string[]
}

/** branch/department: saringan kolom (id, atau 'none' untuk tanpa dimensi) untuk tautan buku besar. */
export type ReportColumn = { label: string; from: string; to: string; branch?: string; department?: string }

export type Statement = {
  title: string
  company: string
  columns: ReportColumn[]
  lines: ReportLine[]
  balanced?: boolean[]
}

export type CashFlow = Statement & { opening: string; netChange: string; closing: string; reconciled: boolean; noCash: boolean }

export type TrialBalance = {
  company: string
  date: string
  fiscalStart: string
  rows: { accountId?: string; code: string; name: string; class: number; debit: string; credit: string }[]
  totalDebit: string
  totalCredit: string
  balanced: boolean
}

export type SummaryRow = {
  id?: string
  code?: string
  name: string
  unit?: string
  documents: number
  qty?: string
  dpp: string
  vat: string
  withholding: string
  total: string
}

export type SummaryKind = 'customer' | 'product' | 'supplier'

export type Summary = { title: string; company: string; kind: SummaryKind; from: string; to: string; rows: SummaryRow[]; total: SummaryRow }

export type ContactCard = {
  company: string
  side: 'customer' | 'supplier'
  contactId: string
  contactName: string
  from: string
  to: string
  opening: string
  lines: { kind: string; docId: string; number: string; date: string; memo: string; debit: string; credit: string; withholding: string; balance: string }[]
  totalDebit: string
  totalCredit: string
  closing: string
}

export type Exposure = { total: string; overdue: string; overdueCount: number; dueSoon: string; dueSoonCount: number }
export type PeriodResult = { revenue: string; expense: string; profit: string }

export type Dashboard = {
  today: string
  cash: CashAccount[]
  cashTotal: string
  cashDrafts: number
  draftTotal: string
  receivable: Exposure
  payable: Exposure
  thisMonth: PeriodResult
  lastMonth: PeriodResult
  trend: (PeriodResult & { month: string })[]
  overdue: Invoice[]
}

export type ReportName = 'balance-sheet' | 'profit-loss' | 'cash-flow' | 'trial-balance' | 'summary' | 'contact-card'

type Params = Record<string, string | undefined>

function useReport<T>(name: ReportName, companyId: string | null, params: Params, enabled = true) {
  return useQuery({
    queryKey: ['reports', companyId, name, params],
    queryFn: () => api<T>('GET', `/reports/${name}` + query(params)),
    enabled: companyId !== null && enabled,
    placeholderData: keepPreviousData,
  })
}

export const useBalanceSheet = (companyId: string | null, p: { date: string; compare?: string }) => useReport<Statement>('balance-sheet', companyId, p)
/** branch/department memfilter dimensi (GL-09); per: 'cabang' memecah kolom per cabang. */
export const useProfitLoss = (companyId: string | null, p: { from: string; to: string; compareFrom?: string; compareTo?: string; branch?: string; department?: string; per?: string }) =>
  useReport<Statement>('profit-loss', companyId, p)
export const useCashFlow = (companyId: string | null, p: { from: string; to: string }) => useReport<CashFlow>('cash-flow', companyId, p)
export const useTrialBalance = (companyId: string | null, p: { date: string }) => useReport<TrialBalance>('trial-balance', companyId, p)
export const useSummary = (companyId: string | null, p: { kind: SummaryKind; from: string; to: string }) => useReport<Summary>('summary', companyId, p)
export const useContactCard = (companyId: string | null, p: { side: string; contact: string; from: string; to: string }) =>
  useReport<ContactCard>('contact-card', companyId, p, p.contact !== '')

export function useDashboard(companyId: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ['reports', companyId, 'dashboard'],
    queryFn: () => api<Dashboard>('GET', '/reports/dashboard'),
    enabled: companyId !== null && enabled,
    staleTime: 30_000,
  })
}

/** Mengunduh laporan sebagai Excel atau CSV dengan parameter yang sama dengan tampilan. */
export function exportReport(name: ReportName, format: 'xlsx' | 'csv', params: Params): Promise<string> {
  return download('GET', `/reports/${name}/export` + query({ ...params, format }))
}

// Proyeksi kebutuhan kas per minggu (AP-06) --------------------------------------------

export type ProjectionDoc = { id: string; number: string; contact: string; due: string; amount: string }
export type ProjectionBucket = { label: string; from?: string; to?: string; in: string; out: string; balance: string; invoices: ProjectionDoc[]; bills: ProjectionDoc[] }
export type CashProjection = { asOf: string; cash: string; buckets: ProjectionBucket[]; firstShortfall?: string }

export function useCashProjection(companyId: string | null, date: string, enabled = true) {
  return useQuery({
    queryKey: ['reports', companyId, 'cash-projection', date],
    queryFn: () => api<CashProjection>('GET', '/reports/cash-projection' + query({ date, weeks: 8 })),
    enabled: companyId !== null && enabled,
    placeholderData: keepPreviousData,
  })
}

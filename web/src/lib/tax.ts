import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, download, query, type CompanyProfile, type ExportBatch, type OutputList, type TaxSettings, type VATRecap, type WithholdingRecap } from './api'
import { useInvalidateCompany } from './invalidate'

export function useOutputs(companyId: string | null, month: string) {
  return useQuery({
    queryKey: ['tax', companyId, 'outputs', month],
    queryFn: () => api<OutputList>('GET', '/tax/output-invoices' + query({ month })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useTrxCodes(companyId: string | null) {
  return useQuery({
    queryKey: ['tax', companyId, 'trx-codes'],
    queryFn: async () => (await api<{ items: { code: string; label: string }[] }>('GET', '/tax/trx-codes')).items,
    enabled: companyId !== null,
    staleTime: Infinity,
  })
}

export function useBatches(companyId: string | null, month: string) {
  return useQuery({
    queryKey: ['tax', companyId, 'batches', month],
    queryFn: async () => (await api<{ items: ExportBatch[] }>('GET', '/tax/exports' + query({ month }))).items,
    enabled: companyId !== null,
  })
}

export function useTaxAction(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: (a:
      | { kind: 'trx'; ids: string[]; trxCode: string }
      | { kind: 'export'; month: string; ids: string[] }
      | { kind: 'approve'; id: string; number: string; date: string }
      | { kind: 'cancel'; id: string; note: string }
      | { kind: 'import'; csv: string }
      | { kind: 'redownload'; batchId: string }) => {
      switch (a.kind) {
        case 'trx':
          return api('PUT', '/tax/output-invoices/trx-code', { ids: a.ids, trxCode: a.trxCode })
        case 'export':
          return download('POST', '/tax/output-invoices/export', { month: a.month, ids: a.ids })
        case 'approve':
          return api('POST', `/tax/output-invoices/${a.id}/approve`, { number: a.number, date: a.date || undefined })
        case 'cancel':
          return api('POST', `/tax/output-invoices/${a.id}/cancel`, { note: a.note })
        case 'import':
          return api<{ recorded: string[]; skipped: string[] }>('POST', '/tax/output-invoices/import', { csv: a.csv })
        case 'redownload':
          return download('GET', `/tax/exports/${a.batchId}`)
      }
    },
    onSuccess: invalidate,
  })
}

export function useVATRecap(companyId: string | null, year: number) {
  return useQuery({
    queryKey: ['tax', companyId, 'vat', year],
    queryFn: () => api<VATRecap>('GET', '/tax/vat-recap' + query({ year })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useWithholding(companyId: string | null, month: string) {
  return useQuery({
    queryKey: ['tax', companyId, 'withholding', month],
    queryFn: () => api<WithholdingRecap>('GET', '/tax/withholding' + query({ month })),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useTaxSettings(companyId: string | null) {
  return useQuery({ queryKey: ['tax', companyId, 'settings'], queryFn: () => api<TaxSettings>('GET', '/tax/settings'), enabled: companyId !== null })
}

export function useSaveTaxSettings(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({ mutationFn: (s: TaxSettings) => api<TaxSettings>('PUT', '/tax/settings', s), onSuccess: invalidate })
}

export function useCompanyProfile(companyId: string | null) {
  return useQuery({ queryKey: ['tax', companyId, 'profile'], queryFn: () => api<CompanyProfile>('GET', '/company/profile'), enabled: companyId !== null })
}

export function useSaveCompanyProfile(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (p: { npwp: string; nitku: string; address: string; pkp: boolean; version: number }) => api<CompanyProfile>('PUT', '/company/profile', p),
    // Status PKP juga dipakai formulir faktur lewat /me.
    onSuccess: () => Promise.all([invalidate(), qc.invalidateQueries({ queryKey: ['me'] })]),
  })
}

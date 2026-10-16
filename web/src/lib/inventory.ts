// Hook data produk dan persediaan. Query key mengikuti pola [kelompok, companyId, ...] supaya
// ikut disegarkan setelah faktur, tagihan, atau dokumen persediaan diposting.
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { api, query, type Adjustment, type AdjustmentDetail, type AdjustmentInput, type Product, type ProductInput, type StockCard, type StockReport } from './api'
import { useInvalidateCompany } from './invalidate'

export function useProducts(companyId: string | null, f: { kind?: string; q?: string; all?: boolean } = {}, enabled = true) {
  return useQuery({
    queryKey: ['inventory', companyId, 'products', f],
    queryFn: async () => (await api<{ items: Product[] }>('GET', '/inventory/products' + query({ kind: f.kind, q: f.q, all: f.all ? 1 : undefined }))).items,
    enabled: companyId !== null && enabled,
    placeholderData: keepPreviousData,
  })
}

export function useSaveProduct(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, input }: { id?: string; input: ProductInput }) =>
      id ? api<Product>('PUT', `/inventory/products/${id}`, input) : api<Product>('POST', '/inventory/products', input),
    onSuccess: invalidate,
  })
}

export function useStock(companyId: string | null, date: string) {
  return useQuery({
    queryKey: ['inventory', companyId, 'stock', date],
    queryFn: () => api<StockReport>('GET', '/inventory/stock' + query({ date })),
    enabled: companyId !== null && date !== '',
    placeholderData: keepPreviousData,
  })
}

export function useStockCard(companyId: string | null, productId: string | null, from: string, to: string) {
  return useQuery({
    queryKey: ['inventory', companyId, 'card', productId, from, to],
    queryFn: () => api<StockCard>('GET', `/inventory/products/${productId}/card` + query({ from, to })),
    enabled: companyId !== null && productId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useAdjustments(companyId: string | null, f: { from: string; to: string }) {
  return useQuery({
    queryKey: ['inventory', companyId, 'adjustments', f],
    queryFn: async () => (await api<{ items: Adjustment[] }>('GET', '/inventory/adjustments' + query(f))).items,
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useAdjustment(companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: ['inventory', companyId, 'adjustment', id],
    queryFn: () => api<AdjustmentDetail>('GET', `/inventory/adjustments/${id}`),
    enabled: companyId !== null && id !== null,
  })
}

export function useCreateAdjustment(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ input, idempotencyKey }: { input: AdjustmentInput; idempotencyKey: string }) =>
      api<AdjustmentDetail>('POST', '/inventory/adjustments', input, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

export function useVoidAdjustment(companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: ({ id, date, reason, idempotencyKey }: { id: string; date: string; reason: string; idempotencyKey: string }) =>
      api<AdjustmentDetail>('POST', `/inventory/adjustments/${id}/void`, { date, reason }, { idempotencyKey }),
    onSuccess: invalidate,
  })
}

/** "2,5" atau "1.200": kuantitas tanpa nol di belakang koma. */
export function formatQty(v: string | bigint): string {
  const s = typeof v === 'string' ? v : v.toString()
  const neg = s.startsWith('-')
  const [whole, frac = ''] = (neg ? s.slice(1) : s).split('.')
  const w = whole.replace(/\B(?=(\d{3})+(?!\d))/g, '.')
  const f = frac.replace(/0+$/, '')
  return (neg ? '−' : '') + (f ? `${w},${f}` : w)
}

// Hook data penjualan dan pembelian. Faktur dan tagihan, serta pelunasan dan pembayaran,
// dinormalkan ke satu bentuk supaya satu set layar melayani keduanya.
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import {
  api,
  query,
  type Aging,
  type Bill,
  type BillDetail,
  type Invoice,
  type InvoiceDetail,
  type Payment,
  type PaymentDetail,
  type Receipt,
  type ReceiptDetail,
  type TaxCode,
  type TradeLineInput,
} from './api'
import { useInvalidateCompany } from './invalidate'

export type TradeKind = 'sales' | 'purchase'

export type Doc = Omit<Invoice, 'customerId' | 'customerName' | 'dpp'> & {
  dpp?: string // hanya penjualan
  contactId: string
  contactName: string
  supplierInvoiceNo: string
  payable: string // penjualan: sama dengan total
}

export type DocPayment = { id: string; number: string; date: string; status: string; amount: string; withholding: string; withholdingCode?: string }

export type DocDetail = Doc &
  Pick<InvoiceDetail, 'totals' | 'lines' | 'journalEntryId' | 'createdById' | 'createdBy' | 'postedBy' | 'postedAt' | 'voidedBy' | 'voidedAt' | 'voidReason' | 'version'> & {
    withholdingCode?: string
    withholding: string
    payments: DocPayment[]
    duplicates: BillDetail['duplicates']
  }

export type Settlement = Omit<Receipt, 'customerId' | 'customerName'> & { contactId: string; contactName: string }

export type SettlementDetail = Settlement & {
  allocated: string
  withholding: string
  allocations: { docId: string; docNumber: string; amount: string; withholdingCode?: string; withholding: string }[]
  createdBy?: string
  voidedBy?: string
  voidReason?: string
}

export type DocInput = {
  contactId: string
  supplierInvoiceNo: string
  date: string
  dueDate: string
  memo: string
  withholdingCode: string
  lines: TradeLineInput[]
  version: number
}

export type AllocationInput = { docId: string; amount: string; withholdingCode: string; withholding: string }
export type SettlementInput = { contactId: string; date: string; cashAccountId: string; memo: string; allocations: AllocationInput[] }

const paths = {
  sales: { docs: '/sales/invoices', open: '/sales/open-invoices', settle: '/sales/receipts', aging: '/sales/aging', contact: 'customer' },
  purchase: { docs: '/purchase/bills', open: '/purchase/open-bills', settle: '/purchase/payments', aging: '/purchase/aging', contact: 'supplier' },
}

function toDoc(d: Invoice | Bill): Doc {
  if ('customerId' in d) {
    const { customerId, customerName, ...rest } = d
    return { ...rest, contactId: customerId, contactName: customerName, supplierInvoiceNo: '', payable: d.total }
  }
  const { supplierId, supplierName, ...rest } = d
  return { ...rest, contactId: supplierId, contactName: supplierName }
}

function toDocDetail(d: InvoiceDetail | BillDetail): DocDetail {
  if ('customerId' in d) {
    return {
      ...d,
      ...toDoc(d),
      withholding: '0',
      duplicates: [],
      payments: d.payments.map((p) => ({ id: p.receiptId, number: p.number, date: p.date, status: p.status, amount: p.amount, withholding: p.withholding, withholdingCode: p.withholdingCode })),
    }
  }
  return {
    ...d,
    ...toDoc(d),
    payments: d.payments.map((p) => ({ id: p.paymentId, number: p.number, date: p.date, status: p.status, amount: p.amount, withholding: '0' })),
  }
}

function toSettlement(s: Receipt | Payment): Settlement {
  if ('customerId' in s) {
    const { customerId, customerName, ...rest } = s
    return { ...rest, contactId: customerId, contactName: customerName }
  }
  const { supplierId, supplierName, ...rest } = s
  return { ...rest, contactId: supplierId, contactName: supplierName }
}

function toSettlementDetail(s: ReceiptDetail | PaymentDetail): SettlementDetail {
  if ('customerId' in s) {
    return {
      ...toSettlement(s),
      allocated: s.allocated,
      withholding: s.withholding,
      allocations: s.allocations.map((a) => ({ docId: a.invoiceId, docNumber: a.invoiceNumber, amount: a.amount, withholdingCode: a.withholdingCode, withholding: a.withholding })),
      createdBy: s.createdBy,
      voidedBy: s.voidedBy,
      voidReason: s.voidReason,
    }
  }
  return {
    ...toSettlement(s),
    allocated: s.amount,
    withholding: '0',
    allocations: s.allocations.map((a) => ({ docId: a.billId, docNumber: a.billNumber, amount: a.amount, withholding: '0' })),
    createdBy: s.createdBy,
    voidedBy: s.voidedBy,
    voidReason: s.voidReason,
  }
}

function docBody(kind: TradeKind, v: DocInput) {
  const common = { date: v.date, dueDate: v.dueDate || undefined, memo: v.memo, lines: v.lines, version: v.version }
  return kind === 'sales'
    ? { customerId: v.contactId, ...common }
    : { supplierId: v.contactId, supplierInvoiceNo: v.supplierInvoiceNo, withholdingCode: v.withholdingCode, ...common }
}

function settlementBody(kind: TradeKind, v: SettlementInput) {
  const common = { date: v.date, cashAccountId: v.cashAccountId, memo: v.memo }
  return kind === 'sales'
    ? {
        customerId: v.contactId,
        ...common,
        allocations: v.allocations.map((a) => ({ invoiceId: a.docId, amount: a.amount, withholdingCode: a.withholdingCode, withholding: a.withholding })),
      }
    : { supplierId: v.contactId, ...common, allocations: v.allocations.map((a) => ({ billId: a.docId, amount: a.amount })) }
}

export type DocFilter = { from: string; to: string; state: string; q: string; contactId?: string }

export function useDocs(kind: TradeKind, companyId: string | null, f: DocFilter) {
  const p = paths[kind]
  return useQuery({
    queryKey: [kind, companyId, 'docs', f],
    queryFn: async () => {
      const r = await api<{ items: (Invoice | Bill)[] }>('GET', p.docs + query({ from: f.from, to: f.to, state: f.state, q: f.q, [p.contact]: f.contactId }))
      return r.items.map(toDoc)
    },
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useDoc(kind: TradeKind, companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: [kind, companyId, 'doc', id],
    queryFn: async () => toDocDetail(await api<InvoiceDetail | BillDetail>('GET', `${paths[kind].docs}/${id}`)),
    enabled: companyId !== null && id !== null,
  })
}

export function useOpenDocs(kind: TradeKind, companyId: string | null, contactId: string) {
  const p = paths[kind]
  return useQuery({
    queryKey: [kind, companyId, 'open', contactId],
    queryFn: async () => (await api<{ items: (Invoice | Bill)[] }>('GET', p.open + query({ [p.contact]: contactId }))).items.map(toDoc),
    enabled: companyId !== null && contactId !== '',
  })
}

export function useSaveDoc(kind: TradeKind, companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: async ({ id, input, idempotencyKey }: { id?: string; input: DocInput; idempotencyKey: string }) =>
      toDocDetail(
        id
          ? await api<InvoiceDetail | BillDetail>('PUT', `${paths[kind].docs}/${id}`, docBody(kind, input))
          : await api<InvoiceDetail | BillDetail>('POST', paths[kind].docs, docBody(kind, input), { idempotencyKey }),
      ),
    onSuccess: invalidate,
  })
}

export function useDeleteDoc(kind: TradeKind, companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: (id: string) => api('DELETE', `${paths[kind].docs}/${id}`),
    onSuccess: invalidate,
  })
}

export function usePostDoc(kind: TradeKind, companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: async ({ id, idempotencyKey }: { id: string; idempotencyKey: string }) =>
      toDocDetail(await api<InvoiceDetail | BillDetail>('POST', `${paths[kind].docs}/${id}/post`, {}, { idempotencyKey })),
    onSuccess: invalidate,
  })
}

export function useVoidDoc(kind: TradeKind, companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: async ({ id, date, reason, idempotencyKey }: { id: string; date: string; reason: string; idempotencyKey: string }) =>
      toDocDetail(await api<InvoiceDetail | BillDetail>('POST', `${paths[kind].docs}/${id}/void`, { date, reason }, { idempotencyKey })),
    onSuccess: invalidate,
  })
}

export function useSettlements(kind: TradeKind, companyId: string | null, f: { from: string; to: string }) {
  return useQuery({
    queryKey: [kind, companyId, 'settlements', f],
    queryFn: async () => (await api<{ items: (Receipt | Payment)[] }>('GET', paths[kind].settle + query(f))).items.map(toSettlement),
    enabled: companyId !== null,
    placeholderData: keepPreviousData,
  })
}

export function useSettlement(kind: TradeKind, companyId: string | null, id: string | null) {
  return useQuery({
    queryKey: [kind, companyId, 'settlement', id],
    queryFn: async () => toSettlementDetail(await api<ReceiptDetail | PaymentDetail>('GET', `${paths[kind].settle}/${id}`)),
    enabled: companyId !== null && id !== null,
  })
}

export function useCreateSettlement(kind: TradeKind, companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: async ({ input, idempotencyKey }: { input: SettlementInput; idempotencyKey: string }) =>
      toSettlementDetail(await api<ReceiptDetail | PaymentDetail>('POST', paths[kind].settle, settlementBody(kind, input), { idempotencyKey })),
    onSuccess: invalidate,
  })
}

export function useVoidSettlement(kind: TradeKind, companyId: string | null) {
  const invalidate = useInvalidateCompany(companyId)
  return useMutation({
    mutationFn: async ({ id, date, reason, idempotencyKey }: { id: string; date: string; reason: string; idempotencyKey: string }) =>
      toSettlementDetail(await api<ReceiptDetail | PaymentDetail>('POST', `${paths[kind].settle}/${id}/void`, { date, reason }, { idempotencyKey })),
    onSuccess: invalidate,
  })
}

export function useAging(kind: TradeKind, companyId: string | null, date: string) {
  return useQuery({
    queryKey: [kind, companyId, 'aging', date],
    queryFn: () => api<Aging>('GET', paths[kind].aging + query({ date })),
    enabled: companyId !== null && date !== '',
    placeholderData: keepPreviousData,
  })
}

export function useTaxCodes(companyId: string | null, date: string) {
  return useQuery({
    queryKey: ['tax', companyId, date],
    queryFn: async () => (await api<{ items: TaxCode[] }>('GET', '/tax/codes' + query({ date }))).items,
    enabled: companyId !== null && date !== '',
    staleTime: 5 * 60_000,
    placeholderData: keepPreviousData,
  })
}

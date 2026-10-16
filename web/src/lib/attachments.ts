import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError, api } from './api'

export type AttachmentType =
  | 'journal'
  | 'sales_invoice'
  | 'sales_receipt'
  | 'sales_credit_note'
  | 'purchase_bill'
  | 'purchase_payment'
  | 'purchase_debit_note'
  | 'cash_transaction'
  | 'inventory_adjustment'
  | 'project'
  | 'contact'
  | 'fixed_asset'

export type Attachment = {
  id: string
  objectType: AttachmentType
  objectId: string
  fileName: string
  mime: string
  sizeBytes: number
  uploadedBy?: string
  uploaderId?: string
  uploadedAt: string
}

const key = (companyId: string | null, type: AttachmentType, id: string) => ['attachments', companyId, type, id]

export function useAttachments(companyId: string | null, type: AttachmentType, id: string | null) {
  return useQuery({
    queryKey: key(companyId, type, id ?? ''),
    queryFn: async () => (await api<{ items: Attachment[] }>('GET', `/attachments?objectType=${type}&objectId=${id}`)).items,
    enabled: companyId !== null && id !== null,
  })
}

/** Unggah lewat multipart; objectType dan objectId dikirim sebelum berkas. */
export function useUploadAttachment(companyId: string | null, type: AttachmentType, id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (file: File) => {
      const form = new FormData()
      form.append('objectType', type)
      form.append('objectId', id)
      form.append('file', file)
      const res = await fetch('/api/v1/attachments', { method: 'POST', credentials: 'same-origin', body: form })
      const data = await res.json().catch(() => null)
      if (!res.ok) throw new ApiError(res.status, data?.code ?? 'unknown', data?.detail || 'Lampiran gagal diunggah.', data?.fields ?? {})
      return data as Attachment
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: key(companyId, type, id) }),
  })
}

export function useDeleteAttachment(companyId: string | null, type: AttachmentType, id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (attachmentId: string) => api('DELETE', `/attachments/${attachmentId}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: key(companyId, type, id) }),
  })
}

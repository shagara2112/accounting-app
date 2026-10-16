import { useRef, useState } from 'react'
import { useAttachments, useDeleteAttachment, useUploadAttachment, type AttachmentType } from '../lib/attachments'
import { formatDateTime } from '../lib/dates'
import { Alert, Button, Skeleton } from './ui'

const ACCEPT = 'application/pdf,image/jpeg,image/png,image/webp,image/gif,.xlsx,.docx,.csv'

function size(n: number) {
  return n >= 1 << 20 ? `${(n / (1 << 20)).toLocaleString('id-ID', { maximumFractionDigits: 1 })} MB` : `${Math.max(1, Math.round(n / 1024))} KB`
}

/** Lampiran dokumen (PLT-08): nota, rekening koran, kontrak. PDF dan gambar dibuka di tab baru. */
export function Attachments({
  companyId,
  type,
  id,
  canUpload,
  canDeleteAny,
  userId,
}: {
  companyId: string | null
  type: AttachmentType
  id: string
  canUpload: boolean
  canDeleteAny: boolean
  userId: string
}) {
  const list = useAttachments(companyId, type, id)
  const upload = useUploadAttachment(companyId, type, id)
  const del = useDeleteAttachment(companyId, type, id)
  const input = useRef<HTMLInputElement>(null)
  const [drag, setDrag] = useState(false)
  const items = list.data ?? []

  function send(files: FileList | null) {
    if (!files) return
    for (const f of Array.from(files)) upload.mutate(f)
  }

  if (list.isPending) return <Skeleton className="h-10" />
  if (!canUpload && items.length === 0) return null
  return (
    <section
      className={`flex flex-col gap-2 rounded-[12px] border border-dashed p-3 transition-colors duration-150 ${drag ? 'border-accent bg-accent-soft/40' : 'border-line-2'}`}
      onDragOver={(e) => {
        if (!canUpload) return
        e.preventDefault()
        setDrag(true)
      }}
      onDragLeave={() => setDrag(false)}
      onDrop={(e) => {
        if (!canUpload) return
        e.preventDefault()
        setDrag(false)
        send(e.dataTransfer.files)
      }}
    >
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">Lampiran{items.length > 0 && <span className="ml-1 text-muted">({items.length})</span>}</h3>
        {canUpload && (
          <>
            <Button size="sm" variant="secondary" busy={upload.isPending} onClick={() => input.current?.click()}>
              Tambah lampiran
            </Button>
            <input ref={input} type="file" multiple accept={ACCEPT} className="hidden" onChange={(e) => (send(e.target.files), (e.target.value = ''))} />
          </>
        )}
      </div>
      {items.length === 0 ? (
        <p className="text-[12.5px] text-muted">Seret berkas ke sini atau klik Tambah lampiran. PDF, gambar, Excel, Word, atau CSV, maksimal 10 MB.</p>
      ) : (
        <ul className="flex flex-col">
          {items.map((a) => (
            <li key={a.id} className="flex items-center justify-between gap-3 border-b border-line py-1.5 text-sm last:border-0">
              <a href={`/api/v1/attachments/${a.id}`} target="_blank" rel="noopener" className="min-w-0 truncate font-medium text-accent hover:underline">
                {a.fileName}
              </a>
              <span className="flex shrink-0 items-center gap-2 text-[12px] text-muted">
                {size(a.sizeBytes)} · {a.uploadedBy ?? '—'} · {formatDateTime(a.uploadedAt)}
                {(canDeleteAny || (canUpload && a.uploaderId === userId)) && (
                  <button type="button" onClick={() => del.mutate(a.id)} className="cursor-pointer text-muted hover:text-bad" aria-label={`Hapus ${a.fileName}`}>
                    ✕
                  </button>
                )}
              </span>
            </li>
          ))}
        </ul>
      )}
      {(upload.error || del.error) && <Alert>{(upload.error ?? del.error)?.message}</Alert>}
    </section>
  )
}

import { useState, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { formatDate, formatDateTime, monthOf } from '../lib/dates'
import {
  useDeleteDoc,
  useDoc,
  useEmailInvoice,
  useInvoiceEmailDraft,
  useNote,
  usePostDoc,
  useSettlement,
  useVoidDoc,
  useVoidNote,
  useVoidSettlement,
  type TradeKind,
} from '../lib/docs'
import { docState, tradeText } from '../lib/labels'
import { useDocLink } from '../lib/consol'
import { formatQty, useProducts } from '../lib/inventory'
import { formatAmount, toUnits } from '../lib/money'
import { invoiceKindLabel, useProjectNames } from '../lib/projects'
import { can } from '../lib/session'
import { useToast } from './Toast'
import { Attachments } from './Attachments'
import { Alert, Badge, Button, Dialog, Drawer, Field, Skeleton, TextAreaField } from './ui'
import { VoidForm } from './VoidForm'

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="text-[12.5px] text-muted">{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

function Row({ label, value, strong, muted }: { label: string; value: string; strong?: boolean; muted?: boolean }) {
  return (
    <div className={`flex items-baseline justify-between gap-4 py-1 ${strong ? 'border-t border-line-2 pt-2 font-semibold' : ''} ${muted ? 'text-muted' : ''}`}>
      <span>{label}</span>
      <span className="num">{value}</span>
    </div>
  )
}

/** Detail faktur atau tagihan beserta aksinya: posting draf, terima/bayar, dan void. */
export function DocDrawer({
  me,
  kind,
  id,
  onClose,
  onOpenSettlement,
  onOpenNote,
}: {
  me: Me
  kind: TradeKind
  id: string | null
  onClose: () => void
  onOpenSettlement: (id: string) => void
  onOpenNote: (id: string) => void
}) {
  const t = tradeText[kind]
  const companyId = me.activeCompanyId
  const doc = useDoc(kind, companyId, id)
  const d = doc.data && doc.data.id === id ? doc.data : undefined
  const navigate = useNavigate()
  const toast = useToast()
  const post = usePostDoc(kind, companyId)
  const del = useDeleteDoc(kind, companyId)
  const voidDoc = useVoidDoc(kind, companyId)
  const [mode, setMode] = useState<'view' | 'void' | 'delete'>('view')
  const [postKey] = useState(newIdempotencyKey)
  const [voidKey, setVoidKey] = useState(newIdempotencyKey)
  const projectNames = useProjectNames(companyId, id !== null && can(me, 'project.view'))
  const products = useProducts(companyId, { all: true }, id !== null && can(me, 'inventory.view'))
  const productUnit = new Map((products.data ?? []).map((p) => [p.id, p]))
  const lineProjects = [...new Set((d?.lines ?? []).map((l) => l.projectId).filter((x): x is string => Boolean(x)))]
  const pair = useDocLink(companyId, kind === 'sales' ? 'sales.invoice' : 'purchase.bill', id, d?.status !== 'draft').data

  const canCreate = can(me, t.perm.create)
  const canVoid = can(me, t.perm.void)
  const ownsDraft = d?.createdById === me.user.id
  const activePayments = d?.payments.filter((p) => p.status === 'posted') ?? []
  const activeNotes = d?.notes.filter((n) => n.status === 'posted') ?? []
  // Nota hanya untuk dokumen biasa yang diposting dan masih punya baris yang bisa dikembalikan.
  const notable = d?.status === 'posted' && (!d.kind || d.kind === 'biasa') && !d.opening && d.lines.some((l) => toUnits(l.net) > toUnits(l.notedNet))

  const [emailing, setEmailing] = useState(false)

  function close() {
    setEmailing(false)
    setMode('view')
    post.reset()
    voidDoc.reset()
    del.reset()
    onClose()
  }

  const title = d ? (d.number ?? `Draf ${t.doc_}`) : 'Memuat…'
  let footer: ReactNode = null
  if (d && mode === 'view') {
    if (d.status === 'draft' && canCreate) {
      footer = (
        <>
          {(ownsDraft || canVoid) && (
            <Button variant="ghost" onClick={() => setMode('delete')}>
              Hapus draf
            </Button>
          )}
          {(ownsDraft || canVoid) && (!d.kind || d.kind === 'biasa') && (
            <Button variant="secondary" onClick={() => navigate(`${t.base}/${d.id}/ubah`)}>
              Ubah
            </Button>
          )}
          {kind === 'sales' && (
            <a href={`/api/v1/sales/invoices/${d.id}/pdf`} target="_blank" rel="noopener" className="self-center px-2 text-sm font-semibold text-accent hover:underline">
              Pratinjau PDF
            </a>
          )}
          <Button
            busy={post.isPending}
            onClick={() =>
              post.mutate(
                { id: d.id, idempotencyKey: postKey },
                { onSuccess: (p) => toast(`${p.number} diposting · jurnal seimbang`) },
              )
            }
          >
            Posting
          </Button>
        </>
      )
    } else if (d.status === 'posted') {
      footer = (
        <>
          {kind === 'sales' && (
            <a href={`/api/v1/sales/invoices/${d.id}/pdf`} target="_blank" rel="noopener" className="mr-auto self-center text-sm font-semibold text-accent hover:underline">
              PDF
            </a>
          )}
          {kind === 'sales' && canCreate && (
            <Button variant="secondary" onClick={() => setEmailing(true)}>
              Kirim email
            </Button>
          )}
          {canVoid && (
            <Button
              variant="secondary"
              onClick={() => {
                setVoidKey(newIdempotencyKey())
                voidDoc.reset()
                setMode('void')
              }}
            >
              Void…
            </Button>
          )}
          {canCreate && notable && (
            <Button variant="secondary" onClick={() => navigate(`${t.base}/${t.tabs.notes}/baru?dok=${d.id}`)}>
              {t.note} / retur
            </Button>
          )}
          {canCreate && d.state === 'open' && (
            <Button onClick={() => navigate(`${t.base}/${t.tabs.settle}/baru?kontak=${d.contactId}&dok=${d.id}`)}>{t.settleAction}</Button>
          )}
        </>
      )
    }
  }

  return (
    <Drawer
      open={id !== null}
      onClose={close}
      title={title}
      description={
        d && (
          <span className="flex flex-wrap items-center gap-2">
            <Badge tone={docState[d.state].tone}>{docState[d.state].label}</Badge>
            {d.kind && d.kind !== 'biasa' && <Badge tone="accent">{invoiceKindLabel[d.kind]}</Badge>}
            {d.opening && <Badge tone="accent">Saldo awal</Badge>}
            {d.daysOverdue > 0 && <Badge tone="bad">Lewat {d.daysOverdue} hari</Badge>}
            <span>{d.contactName}</span>
          </span>
        )
      }
      footer={footer}
    >
      {!d && <Skeleton className="h-40" />}
      {d && (
        <>
          <dl className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-x-6 gap-y-3 text-sm">
            <Meta label="Tanggal">{formatDate(d.date)}</Meta>
            <Meta label="Jatuh tempo">{formatDate(d.dueDate)}</Meta>
            {kind === 'purchase' && <Meta label="No. faktur pemasok">{d.supplierInvoiceNo || '—'}</Meta>}
            {d.projectId && (
              <Meta label="Proyek">
                <Link to={`/proyek/${d.projectId}`} className="font-medium text-accent hover:underline">
                  {projectNames.get(d.projectId) ?? 'Buka proyek'}
                </Link>
                {d.progressPct && <span className="block text-[12.5px] text-muted">Progres s.d. {formatAmount(d.progressPct)}%</span>}
              </Meta>
            )}
            {lineProjects.length > 0 && (
              <Meta label="Proyek">
                {lineProjects.map((p) => (
                  <Link key={p} to={`/proyek/${p}`} className="block font-medium text-accent hover:underline">
                    {projectNames.get(p) ?? 'Buka proyek'}
                  </Link>
                ))}
              </Meta>
            )}
            <Meta label="Dibuat oleh">{d.createdBy ?? '—'}</Meta>
            {d.postedAt && <Meta label="Diposting">{`${d.postedBy ?? 'Sistem'} · ${formatDateTime(d.postedAt)}`}</Meta>}
          </dl>
          {d.memo && <p className="text-sm text-ink-2">{d.memo}</p>}
          {pair && (
            <Alert tone="accent">
              Transaksi antar-PT: pasangannya {pair.docType === 'purchase.bill' ? 'tagihan' : 'faktur'} <span className="num font-semibold">{pair.number}</span> di {pair.companyName}
              {pair.mirror ? ', dibuat otomatis dari dokumen ini' : ''}. Void di salah satu sisi ikut mem-void pasangannya.
            </Alert>
          )}
          {d.duplicates.length > 0 && (
            <Alert tone="warn">
              Nomor faktur pemasok ini juga dipakai di {d.duplicates.map((x) => x.number ?? 'draf lain').join(', ')}. Pastikan bukan tagihan ganda.
            </Alert>
          )}
          {d.status === 'void' && d.voidReason && <Alert tone="bad">Di-void oleh {d.voidedBy ?? 'Sistem'}: {d.voidReason}</Alert>}
          {d.status === 'draft' && d.creditWarning && <Alert tone="warn">{d.creditWarning}</Alert>}
          {post.error && <Alert>{post.error.message}</Alert>}

          <div className="overflow-hidden rounded-[12px] border border-line">
            <table className="w-full text-left text-sm">
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="px-3 py-2.5 font-semibold">Uraian</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Qty × harga</th>
                  <th className="px-3 py-2.5 text-right font-semibold">DPP</th>
                  <th className="px-3 py-2.5 text-right font-semibold">PPN</th>
                </tr>
              </thead>
              <tbody>
                {d.lines.map((l, i) => (
                  <tr key={i} className="border-t border-line align-top">
                    <td className="px-3 py-2.5">
                      <span className="block">{l.description || l.accountName}</span>
                      <span className="block text-[12.5px] text-muted">
                        <span className="num">{l.accountCode}</span> {l.accountName}
                      </span>
                    </td>
                    <td className="num px-3 py-2.5 text-right whitespace-nowrap">
                      {formatQty(l.qty)}
                      {l.productId && productUnit.get(l.productId) && ` ${productUnit.get(l.productId)?.unit}`} × {formatAmount(l.unitPrice)}
                      {l.discountAmount ? (
                        <span className="block text-[12.5px] text-muted">potongan uang muka {formatAmount(l.discountAmount)}</span>
                      ) : (
                        toUnits(l.discountPct) > 0n && <span className="block text-[12.5px] text-muted">diskon {formatAmount(l.discountPct)}%</span>
                      )}
                    </td>
                    <td className="num px-3 py-2.5 text-right">{formatAmount(l.net)}</td>
                    <td className="num px-3 py-2.5 text-right">
                      {formatAmount(l.vat)}
                      <span className="block text-[12px] text-muted">{l.taxCode}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="ml-auto w-full max-w-[340px] text-sm">
            {d.kind === 'termin' && <Row label="Nilai progres" value={formatAmount(d.totals.subtotal)} />}
            {toUnits(d.totals.discount) > 0n && <Row label={d.kind === 'termin' ? 'Potongan uang muka' : 'Diskon'} value={`−${formatAmount(d.totals.discount)}`} muted />}
            <Row label="DPP" value={formatAmount(d.totals.dpp)} />
            {d.totals.otherDpp !== d.totals.dpp && <Row label="DPP nilai lain" value={formatAmount(d.totals.otherDpp)} muted />}
            <Row label="PPN" value={formatAmount(d.totals.vat)} />
            <Row label="Total" value={formatAmount(d.total)} strong />
            {d.retention && toUnits(d.retention) > 0n && (
              <>
                <Row label="Retensi ditahan" value={`−${formatAmount(d.retention)}`} muted />
                <Row label="Ditagih sekarang" value={formatAmount(d.payable)} strong />
              </>
            )}
            {kind === 'purchase' && toUnits(d.withholding) > 0n && (
              <>
                <Row label={`PPh dipotong (${d.withholdingCode})`} value={`−${formatAmount(d.withholding)}`} muted />
                <Row label="Dibayar ke pemasok" value={formatAmount(d.payable)} strong />
              </>
            )}
            {d.status === 'posted' && (
              <>
                <Row label={kind === 'sales' ? 'Sudah diterima' : 'Sudah dibayar'} value={formatAmount(d.paid)} muted />
                {toUnits(d.noted) > 0n && <Row label={`${t.note} / retur`} value={formatAmount(d.noted)} muted />}
                <Row label="Sisa" value={formatAmount(d.outstanding)} strong />
              </>
            )}
          </div>

          {d.payments.length > 0 && (
            <div className="flex flex-col gap-2">
              <h3 className="text-sm font-semibold">{t.settle}</h3>
              <ul className="flex flex-col gap-1.5">
                {d.payments.map((p) => (
                  <li key={p.id}>
                    <button
                      type="button"
                      onClick={() => onOpenSettlement(p.id)}
                      className="flex min-h-10 w-full cursor-pointer items-center justify-between gap-3 rounded-[10px] border border-line px-3 text-left text-sm transition-colors hover:bg-hover"
                    >
                      <span className={`num font-semibold ${p.status === 'void' ? 'text-muted line-through' : 'text-accent'}`}>{p.number}</span>
                      <span className="num text-[13px] text-muted">
                        {formatDate(p.date)} · {formatAmount(p.amount)}
                        {toUnits(p.withholding) > 0n && ` (PPh ${formatAmount(p.withholding)})`}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {d.notes.length > 0 && (
            <div className="flex flex-col gap-2">
              <h3 className="text-sm font-semibold">{t.notes}</h3>
              <ul className="flex flex-col gap-1.5">
                {d.notes.map((n) => (
                  <li key={n.id}>
                    <button
                      type="button"
                      onClick={() => onOpenNote(n.id)}
                      className="flex min-h-10 w-full cursor-pointer items-center justify-between gap-3 rounded-[10px] border border-line px-3 text-left text-sm transition-colors hover:bg-hover"
                    >
                      <span className={`num font-semibold ${n.status === 'void' ? 'text-muted line-through' : 'text-accent'}`}>{n.number}</span>
                      <span className="num text-[13px] text-muted">
                        {formatDate(n.date)} · {formatAmount(n.total)}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {d.journalEntryId && (
            <button
              type="button"
              onClick={() => navigate(`/jurnal?bulan=${monthOf(d.date)}&id=${d.journalEntryId}`)}
              className="self-start text-sm font-semibold text-accent hover:underline"
            >
              Lihat jurnal {d.number} →
            </button>
          )}

          {mode === 'delete' && (
            <Alert tone="warn">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <span>Hapus draf ini? Draf belum masuk buku besar, jadi tidak ada jurnal yang terhapus.</span>
                <span className="flex gap-2">
                  <Button size="sm" variant="secondary" onClick={() => setMode('view')}>
                    Batal
                  </Button>
                  <Button
                    size="sm"
                    variant="danger"
                    busy={del.isPending}
                    onClick={() => del.mutate(d.id, { onSuccess: () => (toast('Draf dihapus'), close()) })}
                  >
                    Hapus
                  </Button>
                </span>
              </div>
              {del.error && <span className="mt-2 block">{del.error.message}</span>}
            </Alert>
          )}
          {mode === 'void' &&
            (activePayments.length > 0 || activeNotes.length > 0 ? (
              <Alert tone="warn">
                {activePayments.length > 0
                  ? `${t.doc} ini sudah punya ${t.settle_} ${activePayments.map((p) => p.number).join(', ')}. Void ${t.settle_}nya dulu, baru ${t.doc_} ini.`
                  : `${t.doc} ini sudah punya ${t.note_} ${activeNotes.map((n) => n.number).join(', ')}. Void notanya dulu, baru ${t.doc_} ini.`}
                <Button size="sm" variant="secondary" className="mt-2" onClick={() => setMode('view')}>
                  Tutup
                </Button>
              </Alert>
            ) : (
              <VoidForm
                title={`Void ${d.number}`}
                note={`${t.doc} tidak dihapus; jurnalnya dibalik (seri JV) pada tanggal void dan tercatat di audit trail.`}
                minDate={d.date}
                busy={voidDoc.isPending}
                error={voidDoc.error}
                onCancel={() => setMode('view')}
                onConfirm={(date, reason) =>
                  voidDoc.mutate({ id: d.id, date, reason, idempotencyKey: voidKey }, { onSuccess: (v) => (toast(`${v.number} di-void`), setMode('view')) })
                }
              />
            ))}
          <Attachments
            companyId={companyId}
            type={kind === 'sales' ? 'sales_invoice' : 'purchase_bill'}
            id={d.id}
            canUpload={canCreate}
            canDeleteAny={canVoid}
            userId={me.user.id}
          />
          {kind === 'sales' && emailing && <InvoiceEmailDialog companyId={companyId} id={d.id} onClose={() => setEmailing(false)} />}
        </>
      )}
    </Drawer>
  )
}

/** Detail pelunasan atau pembayaran: faktur yang dialokasikan, PPh, dan void. */
export function SettlementDrawer({ me, kind, id, onClose, onOpenDoc }: { me: Me; kind: TradeKind; id: string | null; onClose: () => void; onOpenDoc: (id: string) => void }) {
  const t = tradeText[kind]
  const companyId = me.activeCompanyId
  const st = useSettlement(kind, companyId, id)
  const s = st.data && st.data.id === id ? st.data : undefined
  const navigate = useNavigate()
  const toast = useToast()
  const voidIt = useVoidSettlement(kind, companyId)
  const [voiding, setVoiding] = useState(false)
  const [key, setKey] = useState(newIdempotencyKey)
  const err = voidIt.error instanceof ApiError ? voidIt.error : null

  function close() {
    setVoiding(false)
    voidIt.reset()
    onClose()
  }

  return (
    <Drawer
      open={id !== null}
      onClose={close}
      title={s?.number ?? 'Memuat…'}
      description={
        s && (
          <span className="flex flex-wrap items-center gap-2">
            {s.status === 'void' ? <Badge tone="bad">Void</Badge> : <Badge tone="good">Diposting</Badge>}
            {s.kind === 'pengembalian' && <Badge tone="accent">Pengembalian uang muka</Badge>}
            <span>{s.contactName}</span>
          </span>
        )
      }
      footer={
        s?.status === 'posted' &&
        can(me, t.perm.void) &&
        !voiding && (
          <Button
            variant="secondary"
            onClick={() => {
              setKey(newIdempotencyKey())
              voidIt.reset()
              setVoiding(true)
            }}
          >
            Void…
          </Button>
        )
      }
    >
      {!s && <Skeleton className="h-40" />}
      {s && (
        <>
          <dl className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-x-6 gap-y-3 text-sm">
            <Meta label="Tanggal">{formatDate(s.date)}</Meta>
            <Meta label="Rekening">{s.cashAccount}</Meta>
            <Meta label={s.kind === 'pengembalian' ? (kind === 'sales' ? 'Uang dikembalikan' : 'Uang diterima kembali') : kind === 'sales' ? 'Uang diterima' : 'Uang dibayar'}>
              <span className="num font-semibold">Rp {formatAmount(s.amount)}</span>
            </Meta>
            {toUnits(s.advance) > 0n && (
              <Meta label="Uang muka baru">
                <span className="num">Rp {formatAmount(s.advance)}</span>
              </Meta>
            )}
            {s.kind !== 'pengembalian' && toUnits(s.fromAdvance) > 0n && (
              <Meta label="Dari saldo uang muka">
                <span className="num">Rp {formatAmount(s.fromAdvance)}</span>
              </Meta>
            )}
            <Meta label="Dicatat oleh">{s.createdBy ?? '—'}</Meta>
          </dl>
          {s.memo && <p className="text-sm text-ink-2">{s.memo}</p>}
          {s.status === 'void' && s.voidReason && <Alert tone="bad">Di-void oleh {s.voidedBy ?? 'Sistem'}: {s.voidReason}</Alert>}

          <div className="overflow-hidden rounded-[12px] border border-line">
            <table className="w-full text-left text-sm">
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="px-3 py-2.5 font-semibold">{t.doc}</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Dilunasi</th>
                  {kind === 'sales' && <th className="px-3 py-2.5 text-right font-semibold">PPh dipotong</th>}
                </tr>
              </thead>
              <tbody>
                {s.allocations.map((a) => (
                  <tr key={a.docId} className="border-t border-line">
                    <td className="px-3 py-2.5">
                      <button type="button" onClick={() => onOpenDoc(a.docId)} className="num cursor-pointer font-semibold text-accent hover:underline">
                        {a.docNumber}
                      </button>
                    </td>
                    <td className="num px-3 py-2.5 text-right">{formatAmount(a.amount)}</td>
                    {kind === 'sales' && (
                      <td className="num px-3 py-2.5 text-right">
                        {toUnits(a.withholding) > 0n ? (
                          <>
                            {formatAmount(a.withholding)}
                            <span className="block text-[12px] text-muted">{a.withholdingCode}</span>
                          </>
                        ) : (
                          '—'
                        )}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <button
            type="button"
            onClick={() => navigate(`/jurnal?bulan=${monthOf(s.date)}&id=${s.journalEntryId}`)}
            className="self-start text-sm font-semibold text-accent hover:underline"
          >
            Lihat jurnal {s.number} →
          </button>
          {voiding && (
            <VoidForm
              title={`Void ${s.number}`}
              note={`Sisa ${t.doc_} yang dilunasi kembali seperti sebelum ${t.settle_} ini; jurnalnya dibalik pada tanggal void.`}
              minDate={s.date}
              busy={voidIt.isPending}
              error={err}
              onCancel={() => setVoiding(false)}
              onConfirm={(date, reason) =>
                voidIt.mutate({ id: s.id, date, reason, idempotencyKey: key }, { onSuccess: (v) => (toast(`${v.number} di-void`), setVoiding(false)) })
              }
            />
          )}
          <Attachments
            companyId={me.activeCompanyId}
            type={kind === 'sales' ? 'sales_receipt' : 'purchase_payment'}
            id={s.id}
            canUpload={can(me, t.perm.create)}
            canDeleteAny={can(me, t.perm.void)}
            userId={me.user.id}
          />
        </>
      )}
    </Drawer>
  )
}

/** Detail nota kredit/retur penjualan atau nota debit/retur pembelian, dan void-nya. */
export function NoteDrawer({ me, kind, id, onClose, onOpenDoc }: { me: Me; kind: TradeKind; id: string | null; onClose: () => void; onOpenDoc: (id: string) => void }) {
  const t = tradeText[kind]
  const companyId = me.activeCompanyId
  const q = useNote(kind, companyId, id)
  const n = q.data && q.data.id === id ? q.data : undefined
  const navigate = useNavigate()
  const toast = useToast()
  const voidIt = useVoidNote(kind, companyId)
  const [voiding, setVoiding] = useState(false)
  const [key, setKey] = useState(newIdempotencyKey)
  const err = voidIt.error instanceof ApiError ? voidIt.error : null
  const pair = useDocLink(companyId, kind === 'sales' ? 'sales.credit_note' : 'purchase.debit_note', id, n !== undefined).data
  const products = useProducts(companyId, { all: true }, id !== null && can(me, 'inventory.view'))
  const goods = new Set((products.data ?? []).filter((p) => p.kind === 'barang').map((p) => p.id))

  function close() {
    setVoiding(false)
    voidIt.reset()
    onClose()
  }

  return (
    <Drawer
      open={id !== null}
      onClose={close}
      title={n?.number ?? 'Memuat…'}
      description={
        n && (
          <span className="flex flex-wrap items-center gap-2">
            {n.status === 'void' ? <Badge tone="bad">Void</Badge> : <Badge tone="good">Diposting</Badge>}
            <Badge tone="accent">{t.note}</Badge>
            <span>{n.contactName}</span>
          </span>
        )
      }
      footer={
        n?.status === 'posted' &&
        can(me, t.perm.void) &&
        !voiding && (
          <Button
            variant="secondary"
            onClick={() => {
              setKey(newIdempotencyKey())
              voidIt.reset()
              setVoiding(true)
            }}
          >
            Void…
          </Button>
        )
      }
    >
      {!n && <Skeleton className="h-40" />}
      {n && (
        <>
          <dl className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-x-6 gap-y-3 text-sm">
            <Meta label="Tanggal">{formatDate(n.date)}</Meta>
            <Meta label={`${t.doc} asal`}>
              <button type="button" onClick={() => onOpenDoc(n.docId)} className="num cursor-pointer font-semibold text-accent hover:underline">
                {n.docNumber}
              </button>
              {n.supplierInvoiceNo && <span className="block text-[12.5px] text-muted">{n.supplierInvoiceNo}</span>}
            </Meta>
            <Meta label="Dicatat oleh">{`${n.createdBy ?? 'Sistem'} · ${formatDateTime(n.createdAt)}`}</Meta>
          </dl>
          {n.memo && <p className="text-sm text-ink-2">Alasan: {n.memo}</p>}
          {pair && (
            <Alert tone="accent">
              Transaksi antar-PT: pasangannya {pair.docType === 'purchase.debit_note' ? 'nota debit' : 'nota kredit'} <span className="num font-semibold">{pair.number}</span> di {pair.companyName}. Void
              di salah satu sisi ikut mem-void pasangannya.
            </Alert>
          )}
          {n.status === 'void' && n.voidReason && <Alert tone="bad">Di-void oleh {n.voidedBy ?? 'Sistem'}: {n.voidReason}</Alert>}

          <div className="overflow-hidden rounded-[12px] border border-line">
            <table className="w-full text-left text-sm">
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="px-3 py-2.5 font-semibold">Uraian</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Qty × harga</th>
                  <th className="px-3 py-2.5 text-right font-semibold">DPP</th>
                  <th className="px-3 py-2.5 text-right font-semibold">PPN</th>
                </tr>
              </thead>
              <tbody>
                {n.lines.map((l, i) => (
                  <tr key={i} className="border-t border-line align-top">
                    <td className="px-3 py-2.5">
                      <span className="block">{l.description || l.accountName}</span>
                      <span className="block text-[12.5px] text-muted">
                        <span className="num">{l.accountCode}</span> {l.accountName}
                      </span>
                      {l.productId && goods.has(l.productId) && (
                        <span className="block text-[12.5px] text-muted">
                          {kind === 'sales' ? (l.restock ? 'Barang kembali ke stok' : 'Barang tidak kembali ke stok') : 'Barang dikembalikan ke pemasok'}
                        </span>
                      )}
                    </td>
                    <td className="num px-3 py-2.5 text-right whitespace-nowrap">
                      {formatQty(l.qty)} × {formatAmount(l.unitPrice)}
                      {toUnits(l.discountPct) > 0n && <span className="block text-[12.5px] text-muted">diskon {formatAmount(l.discountPct)}%</span>}
                    </td>
                    <td className="num px-3 py-2.5 text-right">{formatAmount(l.net)}</td>
                    <td className="num px-3 py-2.5 text-right">
                      {formatAmount(l.vat)}
                      <span className="block text-[12px] text-muted">{l.taxCode}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="ml-auto w-full max-w-[340px] text-sm">
            <Row label="DPP" value={formatAmount(n.totals.dpp)} />
            {n.totals.otherDpp !== n.totals.dpp && <Row label="DPP nilai lain" value={formatAmount(n.totals.otherDpp)} muted />}
            <Row label="PPN" value={formatAmount(n.totals.vat)} />
            <Row label="Total nota" value={formatAmount(n.total)} strong />
            {toUnits(n.withholding) > 0n && <Row label="PPh batal dipotong" value={`−${formatAmount(n.withholding)}`} muted />}
            <Row label={`Mengurangi sisa ${t.doc_}`} value={formatAmount(n.applied)} />
            {toUnits(n.excess) > 0n && <Row label={kind === 'sales' ? 'Uang muka pelanggan' : 'Uang muka pembelian'} value={formatAmount(n.excess)} />}
          </div>

          <button
            type="button"
            onClick={() => navigate(`/jurnal?bulan=${monthOf(n.date)}&id=${n.journalEntryId}`)}
            className="self-start text-sm font-semibold text-accent hover:underline"
          >
            Lihat jurnal {n.number} →
          </button>
          {voiding && (
            <VoidForm
              title={`Void ${n.number}`}
              note={`Sisa ${t.doc_} kembali seperti sebelum nota ini${kind === 'sales' ? ' dan barang returnya keluar lagi dari stok' : ' dan barang returnya masuk lagi ke stok'}; jurnalnya dibalik pada tanggal void.`}
              minDate={n.date}
              busy={voidIt.isPending}
              error={err}
              onCancel={() => setVoiding(false)}
              onConfirm={(date, reason) =>
                voidIt.mutate({ id: n.id, date, reason, idempotencyKey: key }, { onSuccess: (v) => (toast(`${v.number} di-void`), setVoiding(false)) })
              }
            />
          )}
          <Attachments
            companyId={companyId}
            type={kind === 'sales' ? 'sales_credit_note' : 'purchase_debit_note'}
            id={n.id}
            canUpload={can(me, t.perm.create)}
            canDeleteAny={can(me, t.perm.void)}
            userId={me.user.id}
          />
        </>
      )}
    </Drawer>
  )
}

/** Kirim faktur ke pelanggan lewat email, dengan PDF terlampir (AR-03). */
function InvoiceEmailDialog({ companyId, id, onClose }: { companyId: string | null; id: string; onClose: () => void }) {
  const draft = useInvoiceEmailDraft(companyId, id)
  const send = useEmailInvoice()
  const toast = useToast()
  const [key] = useState(newIdempotencyKey)
  const [v, setV] = useState<{ to: string; cc: string; subject: string; message: string } | null>(null)
  const err = send.error instanceof ApiError ? send.error : null
  const form = v ?? (draft.data ? { to: draft.data.to.join(', '), cc: '', subject: draft.data.subject, message: draft.data.message } : null)
  const split = (s: string) => s.split(/[,;\s]+/).filter(Boolean)
  return (
    <Dialog
      open
      onClose={onClose}
      width={560}
      title="Kirim faktur lewat email"
      description="PDF faktur dilampirkan otomatis. Email dikirim lewat server SMTP perusahaan."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button
            busy={send.isPending}
            disabled={!form || split(form.to).length === 0 || !form.subject.trim()}
            onClick={() =>
              form &&
              send.mutate(
                { id, idempotencyKey: key, to: split(form.to), cc: split(form.cc), subject: form.subject, message: form.message },
                {
                  onSuccess: () => {
                    toast('Faktur masuk antrean email')
                    onClose()
                  },
                },
              )
            }
          >
            Kirim
          </Button>
        </>
      }
    >
      {!form ? (
        <Skeleton className="h-40" />
      ) : (
        <div className="flex flex-col gap-4">
          <Field id="em-to" label="Kepada" placeholder="alamat@pelanggan.co.id" hint="Pisahkan beberapa alamat dengan koma." value={form.to} onChange={(e) => setV({ ...form, to: e.target.value })} error={err?.fields.to} />
          <Field id="em-cc" label="Cc (opsional)" value={form.cc} onChange={(e) => setV({ ...form, cc: e.target.value })} />
          <Field id="em-subject" label="Subjek" value={form.subject} onChange={(e) => setV({ ...form, subject: e.target.value })} error={err?.fields.subject} />
          <TextAreaField id="em-message" label="Pesan" rows={8} value={form.message} onChange={(e) => setV({ ...form, message: e.target.value })} />
          {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
        </div>
      )}
    </Dialog>
  )
}

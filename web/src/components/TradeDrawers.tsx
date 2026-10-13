import { useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { formatDate, formatDateTime, monthOf } from '../lib/dates'
import { useDeleteDoc, useDoc, usePostDoc, useSettlement, useVoidDoc, useVoidSettlement, type TradeKind } from '../lib/docs'
import { docState, tradeText } from '../lib/labels'
import { formatAmount, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { useToast } from './Toast'
import { Alert, Badge, Button, Drawer, Skeleton } from './ui'
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
export function DocDrawer({ me, kind, id, onClose, onOpenSettlement }: { me: Me; kind: TradeKind; id: string | null; onClose: () => void; onOpenSettlement: (id: string) => void }) {
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

  const canCreate = can(me, t.perm.create)
  const canVoid = can(me, t.perm.void)
  const ownsDraft = d?.createdById === me.user.id
  const activePayments = d?.payments.filter((p) => p.status === 'posted') ?? []

  function close() {
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
          {(ownsDraft || canVoid) && (
            <Button variant="secondary" onClick={() => navigate(`${t.base}/${d.id}/ubah`)}>
              Ubah
            </Button>
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
            <Meta label="Dibuat oleh">{d.createdBy ?? '—'}</Meta>
            {d.postedAt && <Meta label="Diposting">{`${d.postedBy ?? 'Sistem'} · ${formatDateTime(d.postedAt)}`}</Meta>}
          </dl>
          {d.memo && <p className="text-sm text-ink-2">{d.memo}</p>}
          {d.duplicates.length > 0 && (
            <Alert tone="warn">
              Nomor faktur pemasok ini juga dipakai di {d.duplicates.map((x) => x.number ?? 'draf lain').join(', ')}. Pastikan bukan tagihan ganda.
            </Alert>
          )}
          {d.status === 'void' && d.voidReason && <Alert tone="bad">Di-void oleh {d.voidedBy ?? 'Sistem'}: {d.voidReason}</Alert>}
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
                      {formatAmount(l.qty)} × {formatAmount(l.unitPrice)}
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
            {toUnits(d.totals.discount) > 0n && <Row label="Diskon" value={`−${formatAmount(d.totals.discount)}`} muted />}
            <Row label="DPP" value={formatAmount(d.totals.dpp)} />
            {d.totals.otherDpp !== d.totals.dpp && <Row label="DPP nilai lain" value={formatAmount(d.totals.otherDpp)} muted />}
            <Row label="PPN" value={formatAmount(d.totals.vat)} />
            <Row label="Total" value={formatAmount(d.total)} strong />
            {kind === 'purchase' && toUnits(d.withholding) > 0n && (
              <>
                <Row label={`PPh dipotong (${d.withholdingCode})`} value={`−${formatAmount(d.withholding)}`} muted />
                <Row label="Dibayar ke pemasok" value={formatAmount(d.payable)} strong />
              </>
            )}
            {d.status === 'posted' && (
              <>
                <Row label={kind === 'sales' ? 'Sudah diterima' : 'Sudah dibayar'} value={formatAmount(d.paid)} muted />
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
            (activePayments.length > 0 ? (
              <Alert tone="warn">
                {t.doc} ini sudah punya {t.settle_} {activePayments.map((p) => p.number).join(', ')}. Void {t.settle_}nya dulu, baru {t.doc_} ini.
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
            <Meta label={kind === 'sales' ? 'Uang diterima' : 'Uang dibayar'}>
              <span className="num font-semibold">Rp {formatAmount(s.amount)}</span>
            </Meta>
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
        </>
      )}
    </Drawer>
  )
}

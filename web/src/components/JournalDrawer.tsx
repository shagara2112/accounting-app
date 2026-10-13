import { useState, type ReactNode } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { useNavigate } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { formatDate, formatDateTime, todayISO } from '../lib/dates'
import { useEntry, useVoidJournal } from '../lib/ledger'
import { kindLabel, kindTone, sourceLabel, sourceLink } from '../lib/labels'
import { formatAmount } from '../lib/money'
import { can } from '../lib/session'
import { Alert, Badge, Button, Drawer, Field, Skeleton, TextAreaField, ease } from './ui'
import { useToast } from './Toast'

/** Detail satu jurnal: baris, jurnal terkait, dan void untuk peran yang berizin. */
export function JournalDrawer({ me, id, onClose, onOpen }: { me: Me; id: string | null; onClose: () => void; onOpen: (id: string) => void }) {
  const companyId = me.activeCompanyId
  const entry = useEntry(companyId, id)
  const e = entry.data && entry.data.id === id ? entry.data : undefined
  const [voiding, setVoiding] = useState<{ entryId: string; date: string; reason: string; key: string; attempt: number } | null>(null)
  const voidJournal = useVoidJournal(companyId)
  const toast = useToast()
  const navigate = useNavigate()

  const canVoid = can(me, 'ledger.journal.void') && e?.status === 'posted' && e.sourceType === 'manual'
  const form = voiding && e && voiding.entryId === e.id ? voiding : null
  const error = voidJournal.error instanceof ApiError ? voidJournal.error : null

  function close() {
    setVoiding(null)
    voidJournal.reset()
    onClose()
  }

  function submitVoid() {
    if (!form || !e) return
    setVoiding({ ...form, attempt: form.attempt + 1 })
    voidJournal.mutate(
      { id: e.id, date: form.date, reason: form.reason, idempotencyKey: form.key },
      {
        onSuccess: (v) => {
          const also = v.alsoVoided?.length ? ` · ${v.alsoVoided.map((r) => r.number).join(', ')} ikut batal` : ''
          toast(`${e.number} di-void · pembalik ${v.reversal.number}${also}`)
          setVoiding(null)
        },
      },
    )
  }

  const title = e?.number ?? 'Memuat jurnal…'
  return (
    <Drawer
      open={id !== null}
      onClose={close}
      title={title}
      description={
        e && (
          <span className="flex flex-wrap items-center gap-2">
            <Badge tone={kindTone[e.kind]}>{kindLabel[e.kind]}</Badge>
            {sourceLabel(e.sourceType) && <Badge tone="neutral">{sourceLabel(e.sourceType)}</Badge>}
            {e.status === 'void' ? <Badge tone="bad">Void</Badge> : <Badge tone="good">Diposting</Badge>}
            <span>{formatDate(e.date)}</span>
          </span>
        )
      }
      footer={
        canVoid &&
        (form ? (
          <>
            <Button variant="secondary" onClick={() => setVoiding(null)}>
              Batal
            </Button>
            <Button variant="danger" busy={voidJournal.isPending} disabled={form.reason.trim().length < 5} onClick={submitVoid}>
              Void dan buat jurnal pembalik
            </Button>
          </>
        ) : (
          <Button
            variant="secondary"
            onClick={() => {
              voidJournal.reset()
              setVoiding({ entryId: e.id, date: todayISO() < e.date ? e.date : todayISO(), reason: '', key: newIdempotencyKey(), attempt: 0 })
            }}
          >
            Void jurnal…
          </Button>
        ))
      }
    >
      {!e && (
        <div className="flex flex-col gap-3" aria-busy="true">
          <Skeleton className="h-5 w-2/3" />
          <Skeleton className="h-32" />
        </div>
      )}
      {e && (
        <>
          {e.memo && <p className="text-[15px]">{e.memo}</p>}
          {sourceLink(e.sourceType, e.sourceId) && (
            <button
              type="button"
              onClick={() => {
                close()
                navigate(sourceLink(e.sourceType, e.sourceId)!)
              }}
              className="self-start text-sm font-semibold text-accent hover:underline"
            >
              Buka dokumen sumber: {sourceLabel(e.sourceType).toLowerCase()} {e.number} →
            </button>
          )}

          <dl className="grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-x-6 gap-y-3 text-sm">
            <Meta label="Total">
              <span className="num font-semibold">Rp {formatAmount(e.total)}</span>
            </Meta>
            <Meta label="Diposting oleh">
              {e.postedBy ?? 'Sistem'} · {formatDateTime(e.postedAt)}
            </Meta>
            {e.status === 'void' && (
              <Meta label="Di-void oleh">
                {e.voidedBy ?? 'Sistem'}
                {e.voidedAt && ` · ${formatDateTime(e.voidedAt)}`}
              </Meta>
            )}
          </dl>

          {e.status === 'void' && e.voidReason && <Alert tone="bad">Alasan void: {e.voidReason}</Alert>}
          {e.sourceType === 'auto_reversal' && (
            <Alert tone="neutral">
              Dibuat otomatis dari {e.reversalOfRef?.number ?? 'jurnal penyesuaian'}. Untuk membatalkannya, void jurnal penyesuaian itu; jurnal ini ikut dibatalkan.
            </Alert>
          )}

          <div className="overflow-hidden rounded-[12px] border border-line">
            <table className="w-full text-left text-sm">
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="px-4 py-2.5 font-semibold">Akun</th>
                  <th className="px-4 py-2.5 text-right font-semibold">Debit</th>
                  <th className="px-4 py-2.5 text-right font-semibold">Kredit</th>
                </tr>
              </thead>
              <tbody>
                {e.lines.map((l) => (
                  <tr key={l.lineNo} className="border-t border-line align-top">
                    <td className="px-4 py-2.5">
                      <span className={l.credit !== '0.0000' ? 'pl-5' : ''}>
                        <span className="num font-semibold text-ink-2">{l.accountCode}</span> {l.accountName}
                      </span>
                      {l.memo && <span className={`block text-[13px] text-muted ${l.credit !== '0.0000' ? 'pl-5' : ''}`}>{l.memo}</span>}
                    </td>
                    <td className="num px-4 py-2.5 text-right">{formatAmount(l.debit, { blankZero: true })}</td>
                    <td className="num px-4 py-2.5 text-right">{formatAmount(l.credit, { blankZero: true })}</td>
                  </tr>
                ))}
                <tr className="border-t border-line-2 font-semibold">
                  <td className="px-4 py-2.5">Total</td>
                  <td className="num px-4 py-2.5 text-right">{formatAmount(e.total)}</td>
                  <td className="num px-4 py-2.5 text-right">{formatAmount(e.total)}</td>
                </tr>
              </tbody>
            </table>
          </div>

          {(e.reversalOfRef || e.reversals.length > 0) && (
            <div className="flex flex-col gap-2">
              <h3 className="text-sm font-semibold">Jurnal terkait</h3>
              <ul className="flex flex-col gap-1.5">
                {e.reversalOfRef && (
                  <Related onClick={() => onOpen(e.reversalOfRef!.id)} number={e.reversalOfRef.number} note="jurnal yang dibalik" />
                )}
                {e.reversals.map((r) => (
                  <Related
                    key={r.id}
                    onClick={() => onOpen(r.id)}
                    number={r.number}
                    note={`${sourceLabel(r.sourceType).toLowerCase()} · ${formatDate(r.date)}${r.status === 'void' ? ' · void' : ''}`}
                  />
                ))}
              </ul>
            </div>
          )}

          <AnimatePresence initial={false}>
            {form && (
              <motion.div
                key="void-form"
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, transition: { duration: 0.12 } }}
                transition={{ duration: 0.2, ease }}
                className="flex flex-col gap-4 rounded-[12px] border border-bad/30 bg-bad-soft/40 p-4"
              >
                <div>
                  <h3 className="text-sm font-semibold">Void {e.number}</h3>
                  <p className="mt-1 text-[13px] text-muted">
                    Jurnal asli tidak diubah. Lajur membuat jurnal pembalik (seri JV) bertanggal void, dan keduanya tercatat di audit trail.
                    {e.autoReverse && ' Jurnal pembalik otomatisnya ikut dibatalkan.'}
                  </p>
                </div>
                <Field
                  id="void-date"
                  label="Tanggal void"
                  type="date"
                  min={e.date}
                  value={form.date}
                  error={error?.fields.date}
                  onChange={(ev) => setVoiding({ ...form, date: ev.target.value })}
                />
                <TextAreaField
                  id="void-reason"
                  label="Alasan"
                  data-autofocus
                  placeholder="Mis. salah memilih akun bank"
                  value={form.reason}
                  maxLength={300}
                  error={error?.fields.reason}
                  hint="Minimal 5 karakter; dibaca auditor."
                  onChange={(ev) => setVoiding({ ...form, reason: ev.target.value })}
                />
                {error && !error.fields.date && !error.fields.reason && <Alert shakeKey={form.attempt}>{error.message}</Alert>}
              </motion.div>
            )}
          </AnimatePresence>
        </>
      )}
    </Drawer>
  )
}

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-[12.5px] text-muted">{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

function Related({ number, note, onClick }: { number: string; note: string; onClick: () => void }) {
  return (
    <li>
      <button type="button" onClick={onClick} className="flex min-h-10 w-full cursor-pointer items-center justify-between gap-3 rounded-[10px] border border-line px-3 text-left text-sm transition-colors hover:bg-hover">
        <span className="num font-semibold text-accent">{number}</span>
        <span className="text-[13px] text-muted">{note}</span>
      </button>
    </li>
  )
}

import { motion } from 'motion/react'
import { useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { formatDate, formatDateTime, monthLabel, monthOf, todayISO } from '../lib/dates'
import { useDimensions } from '../lib/dimensions'
import { useAccounts } from '../lib/ledger'
import { formatAmount, fromUnits, parseInput, toUnits } from '../lib/money'
import {
  statusLabel,
  useCancelPanjar,
  useDecidePanjar,
  usePaidPanjar,
  usePanjar,
  usePanjarList,
  useSavePanjar,
  useUnitBudget,
  type Panjar,
  type PanjarDetail,
  type PanjarStatus,
} from '../lib/panjar'
import { can } from '../lib/session'
import { AccountPicker } from '../components/AccountPicker'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Dialog, Drawer, EmptyState, Field, PageHeader, SelectField, Skeleton, TableShell, Tabs, TextAreaField, ease, td, th, type Tone } from '../components/ui'

type Filter = PanjarStatus | 'semua'

const tone: Record<PanjarStatus, Tone> = { diajukan: 'warn', disetujui: 'accent', ditolak: 'bad', dibatalkan: 'neutral', dibayar: 'good' }

const sum = (list: Panjar[], f: (p: Panjar) => string | undefined) => list.reduce((a, p) => a + toUnits(f(p) ?? '0'), 0n)

/**
 * Panjar unit: kepala unit mengajukan kebutuhan unitnya sebelum membeli; finance menyetujui
 * (boleh dengan nominal lebih kecil) atau menolak, lalu menandai sudah dibayar setelah
 * pembayarannya dicatat di Kas dan bank atau Pembelian.
 */
export function PanjarPage({ me }: { me: Me }) {
  const canCreate = can(me, 'panjar.create')
  const canApprove = can(me, 'panjar.approve')
  const reviewer = canApprove || can(me, 'panjar.view')
  const [params, setParams] = useSearchParams()
  const list = usePanjarList(me.activeCompanyId)
  const items = useMemo(() => list.data?.items ?? [], [list.data])
  const filter = (params.get('status') as Filter | null) ?? (canApprove ? 'diajukan' : 'semua')
  const openId = params.get('id')
  const [form, setForm] = useState<{ edit?: PanjarDetail } | null>(null)
  const set = (patch: Record<string, string>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    setParams(next, { replace: true })
  }

  if (!canCreate && !reviewer) return <EmptyState title="Anda tidak punya izin membuka panjar unit" />

  const by = (s: PanjarStatus) => items.filter((p) => p.status === s)
  const month = monthOf(todayISO())
  const paidThisMonth = by('dibayar').filter((p) => p.paidAt && monthOf(p.paidAt) === month)
  const shown = filter === 'semua' ? items : by(filter)
  const filters: { value: Filter; label: string }[] = [
    { value: 'diajukan', label: `Menunggu (${by('diajukan').length})` },
    { value: 'disetujui', label: `Disetujui (${by('disetujui').length})` },
    { value: 'dibayar', label: 'Dibayar' },
    { value: 'ditolak', label: 'Ditolak' },
    { value: 'dibatalkan', label: 'Dibatalkan' },
    { value: 'semua', label: 'Semua' },
  ]

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Panjar unit"
        subtitle={
          reviewer
            ? 'Pengajuan kebutuhan dari kepala unit sebelum membeli. Setujui atau tolak, lalu tandai sudah dibayar setelah pembayarannya dicatat di Kas dan bank atau Pembelian.'
            : 'Ajukan kebutuhan unit Anda sebelum membeli. Finance akan menyetujui atau menolak; uangnya dibayarkan dan dicatat oleh finance.'
        }
        actions={
          canCreate && (
            <Button onClick={() => setForm({})}>
              <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round">
                <path d="M12 5v14M5 12h14" />
              </svg>
              Ajukan panjar
            </Button>
          )
        }
      />

      <div className="grid gap-3 sm:grid-cols-3">
        <Stat label="Menunggu keputusan" count={by('diajukan').length} amount={sum(by('diajukan'), (p) => p.total)} tone="warn" loading={list.isPending} onClick={() => set({ status: 'diajukan' })} />
        <Stat label="Disetujui, belum dibayar" count={by('disetujui').length} amount={sum(by('disetujui'), (p) => p.approvedAmount)} tone="accent" loading={list.isPending} onClick={() => set({ status: 'disetujui' })} />
        <Stat label={`Dibayar ${monthLabel(month)}`} count={paidThisMonth.length} amount={sum(paidThisMonth, (p) => p.approvedAmount)} tone="good" loading={list.isPending} onClick={() => set({ status: 'dibayar' })} />
      </div>

      <Tabs label="Status pengajuan" value={filter} onChange={(v) => set({ status: v === (canApprove ? 'diajukan' : 'semua') ? '' : v })} tabs={filters} />

      {list.error && <Alert>{list.error.message}</Alert>}
      {!list.isPending && shown.length === 0 ? (
        <EmptyState
          title={items.length === 0 ? 'Belum ada pengajuan panjar' : `Tidak ada pengajuan ${filters.find((f) => f.value === filter)?.label.replace(/ \(\d+\)$/, '').toLowerCase()}`}
          body={items.length === 0 && canCreate ? 'Ajukan kebutuhan unit, mis. pembelian suku cadang atau biaya kegiatan, sebelum uangnya dibelanjakan.' : undefined}
          action={items.length === 0 && canCreate ? <Button onClick={() => setForm({})}>Ajukan panjar</Button> : undefined}
        />
      ) : (
        <TableShell minWidth={860}>
          <thead className="bg-hover/60">
            <tr>
              <th className={th}>Nomor</th>
              <th className={th}>Unit</th>
              <th className={th}>Keperluan</th>
              <th className={`${th} text-right`}>Diajukan</th>
              <th className={`${th} text-right`}>Disetujui</th>
              <th className={th}>Status</th>
            </tr>
          </thead>
          <tbody>
            {list.isPending && (
              <tr>
                <td colSpan={6} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            )}
            {shown.map((p) => (
              <tr key={p.id} onClick={() => set({ id: p.id })} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${p.status === 'dibatalkan' ? 'text-muted' : ''}`}>
                <td className={`${td} whitespace-nowrap`}>
                  <span className="num font-semibold text-accent">{p.number}</span>
                  <span className="num block text-[12.5px] text-muted">{formatDate(p.requestDate)}</span>
                </td>
                <td className={td}>
                  <span className="num mr-1.5 text-[12.5px] text-muted">{p.departmentCode}</span>
                  {p.departmentName}
                </td>
                <td className={`${td} max-w-[320px]`}>
                  <span className="line-clamp-2">{p.purpose}</span>
                  {reviewer && <span className="block text-[12.5px] text-muted">oleh {p.mine ? 'Anda' : p.createdByName}</span>}
                </td>
                <td className={`${td} num text-right`}>{formatAmount(p.total)}</td>
                <td className={`${td} num text-right font-semibold`}>{p.approvedAmount ? formatAmount(p.approvedAmount) : <span className="text-muted">—</span>}</td>
                <td className={td}>
                  <Badge tone={tone[p.status]}>{statusLabel[p.status]}</Badge>
                  {p.neededDate && p.status === 'diajukan' && <span className="num block pt-1 text-[12.5px] text-muted">perlu {formatDate(p.neededDate)}</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </TableShell>
      )}

      <PanjarDrawer me={me} id={openId} onClose={() => set({ id: '' })} onEdit={(d) => setForm({ edit: d })} />
      {form && <PanjarForm me={me} edit={form.edit} onClose={() => setForm(null)} onSaved={(id) => (setForm(null), set({ id }))} />}
    </div>
  )
}

function Stat({ label, count, amount, tone: t, loading, onClick }: { label: string; count: number; amount: bigint; tone: Tone; loading: boolean; onClick: () => void }) {
  const dot = { warn: 'bg-warn', accent: 'bg-accent', good: 'bg-good', bad: 'bg-bad', neutral: 'bg-muted' }[t]
  return (
    <motion.button
      type="button"
      onClick={onClick}
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, ease }}
      className="flex cursor-pointer flex-col gap-1 rounded-2xl border border-line bg-surface p-4 text-left shadow-card transition-shadow duration-150 hover:shadow-lift"
    >
      <span className="flex items-center gap-2 text-[13px] font-medium text-muted">
        <span className={`size-2 rounded-full ${dot}`} aria-hidden="true" />
        {label}
      </span>
      {loading ? (
        <Skeleton className="mt-1 h-7 w-32" />
      ) : (
        <span className="flex items-baseline gap-2">
          <span className="num text-[22px] font-bold tracking-tight">Rp {formatAmount(amount)}</span>
          <span className="text-[13px] text-muted">{count} pengajuan</span>
        </span>
      )}
    </motion.button>
  )
}

// Posisi anggaran unit -----------------------------------------------------------------------------

/** Anggaran beban unit setahun, realisasi, panjar disetujui, dan sisa; extra = nominal yang sedang diajukan atau disetujui. */
function UnitBudgetNote({ me, departmentId, extra, extraLabel }: { me: Me; departmentId: string; extra?: bigint | null; extraLabel?: string }) {
  const b = useUnitBudget(me.activeCompanyId, departmentId)
  if (!departmentId) return null
  if (b.isPending) return <Skeleton className="h-16" />
  if (!b.data) return null
  const u = b.data
  if (!u.hasBudget) {
    return <p className="rounded-[10px] bg-hover px-3.5 py-2.5 text-[13px] text-muted">Unit ini belum punya anggaran beban tahun buku {u.fiscalYear}, jadi sisa anggarannya tidak bisa ditampilkan.</p>
  }
  const remaining = toUnits(u.remaining)
  const after = extra != null ? remaining - extra : null
  const rows: [string, string][] = [
    [`Anggaran beban ${u.fiscalYear}`, formatAmount(u.yearBudget)],
    [`Realisasi s.d. ${monthLabel(u.to)}`, formatAmount(u.actual)],
    ['Panjar disetujui, belum dibayar', formatAmount(u.committed)],
  ]
  return (
    <div className="shrink-0 rounded-[12px] border border-line bg-canvas/60 px-4 py-3 text-[13px]">
      <p className="mb-2 font-semibold">Anggaran unit</p>
      <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted">{k}</dt>
            <dd className="num text-right">{v}</dd>
          </div>
        ))}
        <div className="col-span-2 mt-1 grid grid-cols-subgrid border-t border-line pt-1.5">
          <dt className="font-semibold">Sisa anggaran</dt>
          <dd className={`num text-right font-semibold ${remaining < 0n ? 'text-bad' : ''}`}>{formatAmount(remaining)}</dd>
        </div>
        {after !== null && (
          <>
            <dt className="text-muted">{extraLabel}</dt>
            <dd className={`num text-right font-semibold ${after < 0n ? 'text-bad' : 'text-good'}`}>{formatAmount(after)}</dd>
          </>
        )}
      </dl>
      {after !== null && after < 0n && <p className="mt-2 text-warn">Melebihi sisa anggaran unit Rp {formatAmount(-after)}.</p>}
    </div>
  )
}

// Rincian -----------------------------------------------------------------------------------------

type Action = 'approve' | 'reject' | 'paid' | 'cancel'

function PanjarDrawer({ me, id, onClose, onEdit }: { me: Me; id: string | null; onClose: () => void; onEdit: (d: PanjarDetail) => void }) {
  const detail = usePanjar(me.activeCompanyId, id)
  const d = detail.data
  const canApprove = can(me, 'panjar.approve')
  const [action, setAction] = useState<Action | null>(null)
  const footer = d && (
    <>
      {d.mine && d.status === 'diajukan' && (
        <>
          <Button variant="ghost" onClick={() => setAction('cancel')}>
            Batalkan pengajuan
          </Button>
          <Button variant="secondary" onClick={() => onEdit(d)}>
            Ubah
          </Button>
        </>
      )}
      {canApprove && !d.mine && d.status === 'diajukan' && (
        <>
          <Button variant="secondary" onClick={() => setAction('reject')}>
            Tolak
          </Button>
          <Button onClick={() => setAction('approve')}>Setujui</Button>
        </>
      )}
      {canApprove && d.status === 'disetujui' && <Button onClick={() => setAction('paid')}>Tandai sudah dibayar</Button>}
    </>
  )
  return (
    <Drawer
      open={id !== null}
      onClose={onClose}
      title={d?.number ?? 'Pengajuan panjar'}
      description={
        d && (
          <span className="flex flex-wrap items-center gap-2">
            <Badge tone={tone[d.status]}>{statusLabel[d.status]}</Badge>
            <span>
              {d.departmentCode} · {d.departmentName}
            </span>
          </span>
        )
      }
      footer={footer}
    >
      {detail.isPending && <Skeleton className="h-40" />}
      {detail.error && <Alert>{detail.error.message}</Alert>}
      {d && (
        <>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
            <Info label="Diajukan">
              {formatDate(d.requestDate)} oleh {d.mine ? 'Anda' : d.createdByName}
            </Info>
            <Info label="Dibutuhkan">{d.neededDate ? formatDate(d.neededDate) : '—'}</Info>
            <div className="col-span-2">
              <Info label="Keperluan">
                <span className="whitespace-pre-line">{d.purpose}</span>
              </Info>
            </div>
          </dl>

          <div className="shrink-0 overflow-hidden rounded-[12px] border border-line">
            <table className="w-full text-left text-sm">
              <thead className="bg-hover/60">
                <tr>
                  <th className={th}>Rincian kebutuhan</th>
                  <th className={`${th} text-right`}>Nominal</th>
                </tr>
              </thead>
              <tbody>
                {d.lines.map((l) => (
                  <tr key={l.lineNo} className="border-t border-line">
                    <td className={td}>
                      {l.description}
                      {l.accountCode && (
                        <span className="block text-[12.5px] text-muted">
                          <span className="num">{l.accountCode}</span> {l.accountName}
                        </span>
                      )}
                    </td>
                    <td className={`${td} num text-right`}>{formatAmount(l.amount)}</td>
                  </tr>
                ))}
                <tr className="border-t border-line bg-hover/40 font-semibold">
                  <td className={td}>Total diajukan</td>
                  <td className={`${td} num text-right`}>Rp {formatAmount(d.total)}</td>
                </tr>
              </tbody>
            </table>
          </div>

          {d.decidedAt && (
            <Alert tone={d.status === 'ditolak' ? 'bad' : 'good'}>
              <span className="font-semibold">
                {d.status === 'ditolak' ? 'Ditolak' : `Disetujui Rp ${formatAmount(d.approvedAmount ?? '0')}`}
              </span>{' '}
              oleh {d.decidedByName} · {formatDateTime(d.decidedAt)}
              {d.approvedAmount && d.approvedAmount !== d.total && <span className="block">Lebih kecil dari pengajuan Rp {formatAmount(d.total)}.</span>}
              {d.decisionNote && <span className="mt-1 block whitespace-pre-line">“{d.decisionNote}”</span>}
            </Alert>
          )}
          {d.status === 'dibayar' && (
            <Alert tone="accent">
              Dibayar {d.paidAt && formatDate(d.paidAt)} · bukti <span className="num font-semibold">{d.paidRef}</span>
            </Alert>
          )}
          {d.status === 'disetujui' && (
            <p className="text-[13px] text-muted">
              Bayarkan lewat {can(me, 'cash.view') ? <Link to="/kas" className="font-medium text-accent hover:underline">Kas dan bank</Link> : 'Kas dan bank'} atau Pembelian dengan departemen{' '}
              {d.departmentCode} supaya masuk realisasi anggaran unit, lalu tandai sudah dibayar dengan nomor buktinya.
            </p>
          )}
          {d.mine && d.status === 'diajukan' && canApprove && <p className="text-[13px] text-muted">Pengajuan Anda sendiri diputuskan oleh pengguna finance lain.</p>}
          {(d.status === 'diajukan' || d.status === 'disetujui') && <UnitBudgetNote me={me} departmentId={d.departmentId} />}

          {action === 'approve' || action === 'reject' ? (
            <DecideDialog me={me} p={d} approve={action === 'approve'} onClose={() => setAction(null)} />
          ) : action === 'paid' ? (
            <PaidDialog me={me} p={d} onClose={() => setAction(null)} />
          ) : action === 'cancel' ? (
            <CancelDialog me={me} p={d} onClose={() => setAction(null)} />
          ) : null}
        </>
      )}
    </Drawer>
  )
}

function Info({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <dt className="text-[12.5px] font-medium text-muted">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  )
}

function DecideDialog({ me, p, approve, onClose }: { me: Me; p: PanjarDetail; approve: boolean; onClose: () => void }) {
  const toast = useToast()
  const decide = useDecidePanjar(me.activeCompanyId)
  const [amount, setAmount] = useState(formatAmount(p.total))
  const [note, setNote] = useState('')
  const units = parseInput(amount)
  const fields = decide.error instanceof ApiError ? decide.error.fields : {}
  const amountError =
    units === null ? 'Nominal tidak bisa dibaca.' : units <= 0n ? 'Nominal harus lebih dari nol.' : units > toUnits(p.total) ? 'Tidak boleh melebihi nominal pengajuan.' : fields.amount
  function submit(e: FormEvent) {
    e.preventDefault()
    if (approve && (units === null || amountError)) return
    decide.mutate(
      { id: p.id, approve, amount: approve && units !== null ? fromUnits(units) : undefined, note, version: p.version },
      { onSuccess: () => (toast(approve ? `${p.number} disetujui` : `${p.number} ditolak`), onClose()) },
    )
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={approve ? `Setujui ${p.number}` : `Tolak ${p.number}`}
      description={approve ? 'Nominal boleh lebih kecil dari pengajuan. Setelah disetujui, finance membayarkannya lalu menandai sudah dibayar.' : 'Pengaju akan melihat alasan penolakan ini.'}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Batal
          </Button>
          <Button type="submit" form="panjar-decide" variant={approve ? 'primary' : 'danger'} busy={decide.isPending}>
            {approve ? 'Setujui' : 'Tolak pengajuan'}
          </Button>
        </>
      }
    >
      <form id="panjar-decide" onSubmit={submit} className="flex flex-col gap-4">
        {approve && (
          <Field id="panjar-amount" label="Nominal disetujui" inputMode="decimal" className="num text-right" value={amount} onChange={(e) => setAmount(e.target.value)} error={amountError} data-autofocus />
        )}
        <TextAreaField
          id="panjar-note"
          label={approve ? 'Catatan (opsional)' : 'Alasan penolakan'}
          value={note}
          maxLength={300}
          onChange={(e) => setNote(e.target.value)}
          error={fields.note}
          {...(approve ? {} : { 'data-autofocus': true })}
        />
        {approve && <UnitBudgetNote me={me} departmentId={p.departmentId} extra={units !== null && units > 0n ? units : null} extraLabel="Sisa setelah disetujui" />}
        {decide.error && !fields.amount && !fields.note && <Alert shakeKey={decide.failureCount}>{decide.error.message}</Alert>}
      </form>
    </Dialog>
  )
}

function PaidDialog({ me, p, onClose }: { me: Me; p: PanjarDetail; onClose: () => void }) {
  const toast = useToast()
  const paid = usePaidPanjar(me.activeCompanyId)
  const [date, setDate] = useState(todayISO())
  const [ref, setRef] = useState('')
  const fields = paid.error instanceof ApiError ? paid.error.fields : {}
  function submit(e: FormEvent) {
    e.preventDefault()
    paid.mutate({ id: p.id, date, ref, version: p.version }, { onSuccess: () => (toast(`${p.number} ditandai dibayar`), onClose()) })
  }
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Tandai ${p.number} sudah dibayar`}
      description={`Catat dulu pembayaran Rp ${formatAmount(p.approvedAmount ?? p.total)} di Kas dan bank (kas keluar) atau Pembelian dengan departemen ${p.departmentCode}, lalu tulis nomor buktinya di sini.`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Batal
          </Button>
          <Button type="submit" form="panjar-paid" busy={paid.isPending}>
            Tandai dibayar
          </Button>
        </>
      }
    >
      <form id="panjar-paid" onSubmit={submit} className="grid gap-4 sm:grid-cols-[160px_1fr]">
        <Field id="panjar-paid-date" label="Tanggal bayar" type="date" value={date} max={todayISO()} onChange={(e) => setDate(e.target.value)} error={fields.date} />
        <Field id="panjar-paid-ref" label="Nomor bukti pembayaran" placeholder="mis. KK/2026/10/0003" value={ref} maxLength={60} onChange={(e) => setRef(e.target.value)} error={fields.ref} data-autofocus />
        {paid.error && !fields.date && !fields.ref && (
          <div className="sm:col-span-2">
            <Alert shakeKey={paid.failureCount}>{paid.error.message}</Alert>
          </div>
        )}
      </form>
    </Dialog>
  )
}

function CancelDialog({ me, p, onClose }: { me: Me; p: PanjarDetail; onClose: () => void }) {
  const toast = useToast()
  const cancel = useCancelPanjar(me.activeCompanyId)
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Batalkan ${p.number}?`}
      description="Pengajuan yang dibatalkan tetap tercatat dengan status dibatalkan dan tidak bisa diajukan ulang. Buat pengajuan baru bila masih dibutuhkan."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Kembali
          </Button>
          <Button
            variant="danger"
            busy={cancel.isPending}
            onClick={() => cancel.mutate({ id: p.id, version: p.version }, { onSuccess: () => (toast(`${p.number} dibatalkan`), onClose()) })}
          >
            Batalkan pengajuan
          </Button>
        </>
      }
    >
      {cancel.error && <Alert>{cancel.error.message}</Alert>}
    </Dialog>
  )
}

// Formulir pengajuan -------------------------------------------------------------------------------

type Row = { key: number; description: string; accountId: string; amount: string }

let rowKey = 0
const blankRow = (): Row => ({ key: ++rowKey, description: '', accountId: '', amount: '' })

function PanjarForm({ me, edit, onClose, onSaved }: { me: Me; edit?: PanjarDetail; onClose: () => void; onSaved: (id: string) => void }) {
  const toast = useToast()
  const save = useSavePanjar(me.activeCompanyId)
  const departments = (useDimensions(me.activeCompanyId, 'departemen').data ?? []).filter((d) => d.active || d.id === edit?.departmentId)
  const withAccounts = can(me, 'ledger.view')
  const [departmentId, setDepartmentId] = useState(edit?.departmentId ?? '')
  const [neededDate, setNeededDate] = useState(edit?.neededDate ?? '')
  const [purpose, setPurpose] = useState(edit?.purpose ?? '')
  const [rows, setRows] = useState<Row[]>(() =>
    edit ? edit.lines.map((l) => ({ key: ++rowKey, description: l.description, accountId: l.accountId ?? '', amount: formatAmount(l.amount) })) : [blankRow()],
  )
  const [key] = useState(newIdempotencyKey)
  const [shake, setShake] = useState(0)
  const fields = save.error instanceof ApiError ? save.error.fields : {}
  const amounts = rows.map((r) => parseInput(r.amount))
  const total = amounts.reduce<bigint>((a, u) => a + (u ?? 0n), 0n)
  const dept = departmentId || (departments.length === 1 ? departments[0].id : '')

  const patch = (k: number, v: Partial<Row>) => setRows((rs) => rs.map((r) => (r.key === k ? { ...r, ...v } : r)))

  function submit(e: FormEvent) {
    e.preventDefault()
    if (amounts.some((u) => u === null)) {
      setShake((s) => s + 1)
      return
    }
    save.mutate(
      {
        id: edit?.id,
        idempotencyKey: key,
        input: {
          departmentId: dept,
          neededDate: neededDate || undefined,
          purpose,
          version: edit?.version ?? 0,
          lines: rows.map((r, i) => ({ description: r.description, accountId: r.accountId || undefined, amount: fromUnits(amounts[i] ?? 0n) })),
        },
      },
      { onSuccess: (d) => (toast(edit ? `${d.number} diubah` : `${d.number} diajukan · Rp ${formatAmount(d.total)}`), onSaved(d.id)) },
    )
  }

  const fieldErr = Object.entries(fields).find(([k]) => k.startsWith('lines'))?.[1]
  return (
    <Dialog
      open
      onClose={onClose}
      width={760}
      title={edit ? `Ubah ${edit.number}` : 'Ajukan panjar unit'}
      description="Tulis kebutuhan unit yang akan dibeli atau dibayar. Pengajuan bisa diubah atau dibatalkan selama belum diputuskan finance."
      footer={
        <>
          <span className="mr-auto self-center text-sm text-muted">
            Total <span className="num ml-1 text-base font-bold text-ink">Rp {formatAmount(total)}</span>
          </span>
          <Button variant="ghost" onClick={onClose}>
            Batal
          </Button>
          <Button type="submit" form="panjar-form" busy={save.isPending}>
            {edit ? 'Simpan perubahan' : 'Ajukan'}
          </Button>
        </>
      }
    >
      <form id="panjar-form" onSubmit={submit} className="flex flex-col gap-4">
        {departments.length === 0 ? (
          <Alert tone="warn">
            Belum ada departemen aktif di PT ini. {can(me, 'core.company.edit') ? <Link to="/cabang" className="font-semibold underline">Tambahkan departemen</Link> : 'Minta admin menambahkan departemen'} untuk setiap unit dulu.
          </Alert>
        ) : (
          <div className="grid gap-4 sm:grid-cols-[1fr_180px]">
            <SelectField id="panjar-dept" label="Unit (departemen)" value={dept} onChange={(e) => setDepartmentId(e.target.value)} error={fields.departmentId}>
              {dept === '' && <option value="">Pilih unit</option>}
              {departments.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.code} · {d.name}
                </option>
              ))}
            </SelectField>
            <Field id="panjar-needed" label="Dibutuhkan tanggal" hint="Opsional" type="date" min={todayISO()} value={neededDate} onChange={(e) => setNeededDate(e.target.value)} error={fields.neededDate} />
          </div>
        )}
        <TextAreaField id="panjar-purpose" label="Keperluan" rows={2} maxLength={300} placeholder="mis. Perbaikan genset kantor unit teknik" value={purpose} onChange={(e) => setPurpose(e.target.value)} error={fields.purpose} />

        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1.5 text-sm font-medium">Rincian kebutuhan</legend>
          {rows.map((r, i) => (
            <div key={r.key} className={`grid gap-2 ${withAccounts ? 'sm:grid-cols-[1fr_200px_150px_40px]' : 'sm:grid-cols-[1fr_170px_40px]'}`}>
              <Field
                id={`panjar-desc-${r.key}`}
                label={`Rincian ${i + 1}`}
                placeholder="mis. Suku cadang genset"
                maxLength={200}
                value={r.description}
                onChange={(e) => patch(r.key, { description: e.target.value })}
                error={fields[`lines.${i}.description`]}
              />
              {withAccounts && <AccountCell me={me} id={`panjar-acc-${r.key}`} value={r.accountId} onChange={(v) => patch(r.key, { accountId: v })} error={fields[`lines.${i}.accountId`]} />}
              <Field
                id={`panjar-amt-${r.key}`}
                label="Nominal"
                inputMode="decimal"
                className="num text-right"
                placeholder="0"
                value={r.amount}
                onChange={(e) => patch(r.key, { amount: e.target.value })}
                onBlur={() => amounts[i] !== null && r.amount !== '' && patch(r.key, { amount: formatAmount(amounts[i]!) })}
                error={amounts[i] === null ? 'Tidak bisa dibaca' : fields[`lines.${i}.amount`]}
              />
              <button
                type="button"
                aria-label={`Hapus rincian ${i + 1}`}
                disabled={rows.length === 1}
                onClick={() => setRows((rs) => rs.filter((x) => x.key !== r.key))}
                className="flex size-10 cursor-pointer items-center justify-center self-end rounded-[10px] text-muted transition-colors hover:bg-hover hover:text-bad disabled:cursor-not-allowed disabled:opacity-40 sm:mb-0.5"
              >
                <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                  <path d="M6 6l12 12M18 6L6 18" />
                </svg>
              </button>
            </div>
          ))}
          {rows.length < 50 && (
            <Button type="button" variant="ghost" size="sm" className="self-start" onClick={() => setRows((rs) => [...rs, blankRow()])}>
              + Tambah rincian
            </Button>
          )}
        </fieldset>

        <UnitBudgetNote me={me} departmentId={dept} extra={total > 0n ? total : null} extraLabel="Sisa bila pengajuan ini disetujui" />
        {save.error && (fieldErr || !Object.keys(fields).length) && <Alert shakeKey={`${save.failureCount}-${shake}`}>{fieldErr ?? save.error.message}</Alert>}
      </form>
    </Dialog>
  )
}

/** Akun perkiraan per baris, hanya untuk pengguna yang boleh melihat bagan akun. */
function AccountCell({ me, id, value, onChange, error }: { me: Me; id: string; value: string; onChange: (v: string) => void; error?: string }) {
  const accounts = useAccounts(me.activeCompanyId).data?.items ?? []
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium">
        Akun <span className="font-normal text-muted">(opsional)</span>
      </label>
      <AccountPicker id={id} accounts={accounts} value={value} onChange={onChange} invalid={!!error} placeholder="Kode atau nama" />
      {error && <p className="text-[12.5px] text-bad">{error}</p>}
    </div>
  )
}

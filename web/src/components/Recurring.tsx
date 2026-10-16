import { useState, type ReactNode } from 'react'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { addMonths, formatDate, formatDateTime, monthEnd, monthLabel, monthOf, monthStart, todayISO } from '../lib/dates'
import { useDeleteRecurring, useRecurring, useRecurringList, useRunRecurring, useSaveRecurring, type Recurring } from '../lib/dimensions'
import { kindLabel, kindTone } from '../lib/labels'
import { formatAmount } from '../lib/money'
import { can } from '../lib/session'
import { useToast } from './Toast'
import { Alert, Badge, Button, Checkbox, Dialog, Drawer, EmptyState, Field, SelectField, Skeleton, TableShell, td, th } from './ui'

const scheduleLabel = (day: number) => (day === 0 ? 'Tiap akhir bulan' : `Tiap tanggal ${day}`)
const finished = (r: Recurring) => Boolean(r.endDate && r.nextDate > r.endDate)

type Source = { entryId: string; number: string; memo: string; date: string }

/** Membuat jurnal berulang dari jurnal manual (GL-08), atau mengubah jadwalnya. */
export function RecurringDialog({
  companyId,
  source,
  recurring,
  onClose,
  onSaved,
}: {
  companyId: string | null
  source?: Source
  recurring?: Recurring
  onClose: () => void
  onSaved?: (r: Recurring) => void
}) {
  const save = useSaveRecurring(companyId)
  const toast = useToast()
  const [key] = useState(newIdempotencyKey)
  const [v, setV] = useState(() => {
    if (recurring) {
      return { name: recurring.name, day: recurring.dayOfMonth, start: monthOf(recurring.nextDate), end: recurring.endDate ? monthOf(recurring.endDate) : '', active: recurring.active }
    }
    const d = Number(source!.date.slice(8, 10))
    const day = source!.date === monthEnd(monthOf(source!.date)) ? 0 : Math.min(d, 28)
    return { name: source!.memo || `Berulang dari ${source!.number}`, day, start: addMonths(monthOf(source!.date), 1), end: '', active: true }
  })
  const err = save.error instanceof ApiError ? save.error : null
  const first = v.start ? (v.day === 0 ? monthEnd(v.start) : `${v.start}-${String(v.day).padStart(2, '0')}`) : ''
  const lateCount = first && first <= todayISO() ? countUntil(first, v.day, v.end ? monthEnd(v.end) : todayISO()) : 0

  function submit() {
    save.mutate(
      {
        id: recurring?.id,
        entryId: source?.entryId,
        name: v.name,
        dayOfMonth: v.day,
        startDate: monthStart(v.start),
        endDate: v.end ? monthEnd(v.end) : undefined,
        active: v.active,
        version: recurring?.version ?? 0,
        idempotencyKey: key,
      },
      {
        onSuccess: (r) => {
          toast(recurring ? `${r.name} disimpan` : `${r.name} dijadwalkan · pertama ${formatDate(r.nextDate)}`)
          onSaved?.(r)
          onClose()
        },
      },
    )
  }

  return (
    <Dialog
      open
      onClose={onClose}
      title={recurring ? `Ubah ${recurring.name}` : `Jadikan ${source!.number} berulang`}
      description={
        recurring
          ? 'Baris jurnal tetap sama dengan saat template dibuat. Untuk mengubah akun atau nominal, hapus template lalu buat lagi dari jurnal baru.'
          : 'Baris jurnal ini disalin menjadi template dan diposting otomatis setiap bulan pada tanggal jadwalnya.'
      }
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button busy={save.isPending} disabled={!v.name.trim() || !v.start} onClick={submit}>
            {recurring ? 'Simpan' : 'Jadwalkan'}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field id="rec-nama" label="Nama" hint="Mis. akrual sewa kantor atau penyusutan bulanan." value={v.name} maxLength={200} onChange={(e) => setV({ ...v, name: e.target.value })} error={err?.fields.name} />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <SelectField id="rec-tanggal" label="Tanggal posting" value={String(v.day)} onChange={(e) => setV({ ...v, day: Number(e.target.value) })} error={err?.fields.dayOfMonth}>
            {Array.from({ length: 28 }, (_, i) => (
              <option key={i + 1} value={i + 1}>
                Tanggal {i + 1}
              </option>
            ))}
            <option value={0}>Akhir bulan</option>
          </SelectField>
          <Field
            id="rec-mulai"
            type="month"
            label={recurring ? 'Jadwal berikutnya di bulan' : 'Mulai bulan'}
            value={v.start}
            onChange={(e) => setV({ ...v, start: e.target.value })}
            error={err?.fields.startDate}
          />
        </div>
        <Field
          id="rec-selesai"
          type="month"
          label="Berakhir setelah bulan (opsional)"
          hint="Kosongkan bila berjalan terus sampai dinonaktifkan."
          value={v.end}
          min={v.start || undefined}
          onChange={(e) => setV({ ...v, end: e.target.value })}
          error={err?.fields.endDate}
        />
        {recurring && <Checkbox checked={v.active} onChange={(x) => setV({ ...v, active: x })} label="Aktif" hint="Template nonaktif tidak diposting oleh penjadwal." />}
        {first && (
          <p className="text-sm text-muted">
            Jadwal pertama <span className="font-semibold text-ink">{formatDate(first)}</span>
            {lateCount > 0 && ` — sudah lewat; ${lateCount} jurnal yang tertinggal diposting dengan tanggal aslinya pada putaran penjadwal berikutnya (paling lambat satu jam), atau lewat Jalankan sekarang.`}
          </p>
        )}
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

function countUntil(first: string, day: number, until: string): number {
  let n = 0
  for (let m = monthOf(first); n < 120; m = addMonths(m, 1)) {
    const d = day === 0 ? monthEnd(m) : `${m}-${String(day).padStart(2, '0')}`
    if (d > until) break
    n++
  }
  return n
}

/** Daftar template jurnal berulang untuk tab Berulang di halaman Jurnal. */
export function RecurringList({ me, openId, onOpen, onOpenEntry }: { me: Me; openId: string | null; onOpen: (id: string | null) => void; onOpenEntry: (id: string) => void }) {
  const list = useRecurringList(me.activeCompanyId)
  const items = list.data ?? []
  const today = todayISO()
  return (
    <>
      <TableShell minWidth={760}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Nama</th>
            <th className={th}>Jadwal</th>
            <th className={th}>Berikutnya</th>
            <th className={`${th} text-right`}>Total</th>
            <th className={th}>Status</th>
          </tr>
        </thead>
        <tbody>
          {list.isPending && (
            <tr>
              <td colSpan={5} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {items.map((r) => (
            <tr key={r.id} onClick={() => onOpen(r.id)} className="cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover">
              <td className={td}>
                <span className="font-medium">{r.name}</span>
                <span className="mt-0.5 flex items-center gap-1.5">
                  <Badge tone={kindTone[r.kind]}>{kindLabel[r.kind]}</Badge>
                  {r.autoReverse && <span className="text-xs text-muted">dibalik otomatis</span>}
                </span>
              </td>
              <td className={td}>{scheduleLabel(r.dayOfMonth)}</td>
              <td className={`${td} num whitespace-nowrap`}>
                {finished(r) ? <span className="text-muted">—</span> : formatDate(r.nextDate)}
                {!finished(r) && r.active && r.nextDate <= today && <span className="block text-xs text-warn">jatuh tempo</span>}
              </td>
              <td className={`${td} num text-right`}>{formatAmount(r.total)}</td>
              <td className={td}>
                {r.lastError ? (
                  <Badge tone="bad">Gagal</Badge>
                ) : finished(r) ? (
                  <Badge tone="neutral">Selesai</Badge>
                ) : r.active ? (
                  <Badge tone="good">Aktif</Badge>
                ) : (
                  <Badge tone="neutral">Nonaktif</Badge>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && (
        <EmptyState
          title="Belum ada jurnal berulang"
          body="Buka jurnal manual yang sudah diposting, mis. akrual sewa atau penyusutan, lalu pilih Jadikan berulang."
        />
      )}
      {list.isError && <p className="text-sm text-bad">{list.error.message}</p>}
      <RecurringDrawer me={me} id={openId} onClose={() => onOpen(null)} onOpenEntry={onOpenEntry} />
    </>
  )
}

function RecurringDrawer({ me, id, onClose, onOpenEntry }: { me: Me; id: string | null; onClose: () => void; onOpenEntry: (id: string) => void }) {
  const companyId = me.activeCompanyId
  const q = useRecurring(companyId, id)
  const r = q.data && q.data.id === id ? q.data : undefined
  const run = useRunRecurring(companyId)
  const del = useDeleteRecurring(companyId)
  const toast = useToast()
  const [editing, setEditing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [runKey, setRunKey] = useState(newIdempotencyKey)
  const canEdit = can(me, 'ledger.journal.create')
  const due = r && r.active && !finished(r) && r.nextDate <= todayISO()
  const error = run.error ?? del.error

  function close() {
    setConfirmDelete(false)
    run.reset()
    del.reset()
    onClose()
  }

  return (
    <>
      <Drawer
        open={id !== null}
        onClose={close}
        title={r?.name ?? 'Memuat jurnal berulang…'}
        description={
          r && (
            <span className="flex flex-wrap items-center gap-2">
              <Badge tone={kindTone[r.kind]}>{kindLabel[r.kind]}</Badge>
              {r.active ? <Badge tone="good">Aktif</Badge> : <Badge tone="neutral">Nonaktif</Badge>}
              <span>{scheduleLabel(r.dayOfMonth)}</span>
            </span>
          )
        }
        footer={
          r &&
          canEdit &&
          (confirmDelete ? (
            <>
              <Button variant="secondary" onClick={() => setConfirmDelete(false)}>
                Batal
              </Button>
              <Button
                variant="danger"
                busy={del.isPending}
                onClick={() =>
                  del.mutate(r.id, {
                    onSuccess: () => {
                      toast(`${r.name} dihapus`)
                      close()
                    },
                  })
                }
              >
                Hapus template
              </Button>
            </>
          ) : (
            <>
              <Button variant="secondary" onClick={() => setConfirmDelete(true)}>
                Hapus…
              </Button>
              <Button variant="secondary" onClick={() => setEditing(true)}>
                Ubah jadwal
              </Button>
              {due && (
                <Button
                  busy={run.isPending}
                  onClick={() =>
                    run.mutate(
                      { id: r.id, idempotencyKey: runKey },
                      {
                        onSuccess: (x) => {
                          setRunKey(newIdempotencyKey())
                          toast(x.posted > 0 ? `${x.posted} jurnal diposting` : 'Tidak ada jadwal yang jatuh tempo')
                        },
                      },
                    )
                  }
                >
                  Jalankan sekarang
                </Button>
              )}
            </>
          ))
        }
      >
        {!r && (
          <div className="flex flex-col gap-3" aria-busy="true">
            <Skeleton className="h-5 w-2/3" />
            <Skeleton className="h-32" />
          </div>
        )}
        {r && (
          <>
            {r.memo && <p className="text-[15px]">{r.memo}</p>}
            {confirmDelete && <Alert tone="warn">Template dihapus; jurnal yang sudah diposting darinya tetap ada dan bisa di-void satu per satu.</Alert>}
            {r.lastError && <Alert>Gagal terakhir: {r.lastError}</Alert>}
            {error && <Alert>{error.message}</Alert>}
            <dl className="grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-x-6 gap-y-3 text-sm">
              <Meta label="Berikutnya">{finished(r) ? 'Selesai' : formatDate(r.nextDate)}</Meta>
              <Meta label="Berakhir">{r.endDate ? monthLabel(monthOf(r.endDate)) : 'Tanpa batas'}</Meta>
              <Meta label="Total per jurnal">
                <span className="num font-semibold">Rp {formatAmount(r.total)}</span>
              </Meta>
              {r.lastRunAt && <Meta label="Terakhir jalan">{formatDateTime(r.lastRunAt)}</Meta>}
            </dl>
            {r.autoReverse && <p className="text-sm text-muted">Jurnal penyesuaian ini dibalik otomatis di awal bulan berikutnya.</p>}

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
                  {(r.lines ?? []).map((l, i) => (
                    <tr key={i} className="border-t border-line align-top">
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
                </tbody>
              </table>
            </div>

            <div className="flex flex-col gap-2">
              <h3 className="text-sm font-semibold">Riwayat posting</h3>
              {(r.runs ?? []).length === 0 ? (
                <p className="text-sm text-muted">Belum pernah diposting.</p>
              ) : (
                <ul className="flex flex-col gap-1.5">
                  {r.runs!.map((x) => (
                    <li key={x.entryId}>
                      <button
                        type="button"
                        onClick={() => {
                          close()
                          onOpenEntry(x.entryId)
                        }}
                        className="flex w-full items-center justify-between gap-3 rounded-[10px] px-3 py-2 text-left text-sm transition-colors duration-150 hover:bg-hover"
                      >
                        <span className={`num font-semibold ${x.status === 'void' ? 'text-muted line-through' : 'text-accent'}`}>{x.number}</span>
                        <span className="text-muted">
                          {formatDate(x.date)}
                          {x.status === 'void' && ' · void'}
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </>
        )}
      </Drawer>
      {editing && r && <RecurringDialog companyId={companyId} recurring={r} onClose={() => setEditing(false)} />}
    </>
  )
}

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <dt className="text-[12.5px] text-muted">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  )
}

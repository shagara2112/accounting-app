import { motion } from 'motion/react'
import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiError, type Account, type Me } from '../lib/api'
import {
  suggestBudget,
  useActivateBudget,
  useBudget,
  useBudgets,
  useCompare,
  useCreateBudget,
  useDeleteBudget,
  useRenameBudget,
  useSaveSlice,
  type Budget,
  type BudgetDetail,
  type CompareRow,
  type Comparison,
  type MonthPoint,
  type Slice,
} from '../lib/budget'
import { monthLabel, todayISO } from '../lib/dates'
import { useDimensions } from '../lib/dimensions'
import { useAccounts, useLedgerSettings } from '../lib/ledger'
import { formatAmount, fromUnits, parseInput, toUnits } from '../lib/money'
import { useProjects } from '../lib/projects'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Dialog, EmptyState, Field, PageHeader, Segmented, SelectField, Skeleton, TableShell, Tabs, ease, td, th } from '../components/ui'

type Tab = 'realisasi' | 'susun'

const shortMonth = ['Jan', 'Feb', 'Mar', 'Apr', 'Mei', 'Jun', 'Jul', 'Agu', 'Sep', 'Okt', 'Nov', 'Des']

/** Label bulan pendek; tahun dua digit ikut bila tahun buku tidak mulai Januari. */
function monthShort(ym: string, withYear: boolean) {
  const [y, m] = ym.split('-').map(Number)
  return withYear ? `${shortMonth[m - 1]} ${String(y).slice(2)}` : shortMonth[m - 1]
}

function currentFiscalYear(startMonth: number) {
  const [y, m] = todayISO().split('-').map(Number)
  return m >= startMonth ? y : y - 1
}

/** Bulan ke-berapa tahun buku year untuk hari ini, dibatasi 1–12. */
function currentMonthIndex(year: number, startMonth: number) {
  const [y, m] = todayISO().split('-').map(Number)
  return Math.min(12, Math.max(1, (y - year) * 12 + m - startMonth + 1))
}

function sliceOf(v: string): Slice {
  if (v.startsWith('d:')) return { departmentId: v.slice(2) }
  if (v.startsWith('p:')) return { projectId: v.slice(2) }
  return {}
}

/** Anggaran tahunan (BUD-01) dan anggaran vs realisasi (BUD-02). */
export function BudgetPage({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams()
  const settings = useLedgerSettings(me.activeCompanyId)
  const startMonth = settings.data?.fiscalYearStartMonth ?? 1
  const tab: Tab = params.get('tab') === 'susun' ? 'susun' : 'realisasi'
  const year = Number(params.get('tahun')) || currentFiscalYear(startMonth)
  const set = (patch: Record<string, string>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    setParams(next, { replace: true })
  }
  if (!can(me, 'report.view')) return <EmptyState title="Anda tidak punya izin melihat laporan" />
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Anggaran"
        subtitle="Susun anggaran pendapatan dan beban per tahun buku, lalu pantau realisasinya dari buku besar. Akun beban yang mendekati atau melewati anggaran setahun langsung ditandai."
        actions={
          <div className="flex items-center gap-1 rounded-[10px] border border-line-2 bg-surface p-1">
            <Button size="sm" variant="ghost" aria-label="Tahun buku sebelumnya" onClick={() => set({ tahun: String(year - 1) })}>
              ‹
            </Button>
            <span className="px-2 text-sm font-semibold">Tahun buku {startMonth === 1 ? year : `${year}/${String(year + 1).slice(2)}`}</span>
            <Button size="sm" variant="ghost" aria-label="Tahun buku berikutnya" onClick={() => set({ tahun: String(year + 1) })}>
              ›
            </Button>
          </div>
        }
      />
      <Tabs
        label="Anggaran"
        value={tab}
        onChange={(v) => set({ tab: v === 'realisasi' ? '' : v })}
        tabs={[
          { value: 'realisasi', label: 'Anggaran vs realisasi' },
          { value: 'susun', label: 'Susun anggaran' },
        ]}
      />
      {tab === 'realisasi' ? (
        <CompareTab key={year} me={me} year={year} startMonth={startMonth} onEdit={() => set({ tab: 'susun' })} />
      ) : (
        <EditorTab key={year} me={me} year={year} startMonth={startMonth} />
      )}
    </div>
  )
}

/** Pilihan irisan: seluruh PT (atau tingkat PT di penyusun), satu departemen, atau satu proyek. */
function SliceSelect({ me, id, value, onChange, allLabel, disabled }: { me: Me; id: string; value: string; onChange: (v: string) => void; allLabel: string; disabled?: boolean }) {
  const departments = useDimensions(me.activeCompanyId, 'departemen').data ?? []
  const projects = useProjects(me.activeCompanyId, {}, can(me, 'project.view')).data ?? []
  return (
    <SelectField id={id} label="Departemen atau proyek" value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
      <option value="">{allLabel}</option>
      {departments.length > 0 && (
        <optgroup label="Departemen">
          {departments.map((d) => (
            <option key={d.id} value={`d:${d.id}`}>
              {d.code} · {d.name}
            </option>
          ))}
        </optgroup>
      )}
      {projects.length > 0 && (
        <optgroup label="Proyek">
          {projects.map((p) => (
            <option key={p.id} value={`p:${p.id}`}>
              {p.code} · {p.name}
            </option>
          ))}
        </optgroup>
      )}
    </SelectField>
  )
}

// Anggaran vs realisasi ---------------------------------------------------------------------------

function CompareTab({ me, year, startMonth, onEdit }: { me: Me; year: number; startMonth: number; onEdit: () => void }) {
  const [from, setFrom] = useState(1)
  const [to, setTo] = useState(() => currentMonthIndex(year, startMonth))
  const [slice, setSlice] = useState('')
  const [chart, setChart] = useState<'beban' | 'pendapatan'>('beban')
  const c = useCompare(me.activeCompanyId, { year, from, to, ...sliceOf(slice) })
  const data = c.data
  const months = data?.months ?? Array.from({ length: 12 }, (_, i) => {
    const m = ((startMonth - 1 + i) % 12) + 1
    const y = year + Math.floor((startMonth - 1 + i) / 12)
    return `${y}-${String(m).padStart(2, '0')}`
  })
  const withYear = startMonth !== 1
  return (
    <div className="flex flex-col gap-5">
      <div className="grid grid-cols-[repeat(auto-fit,minmax(200px,1fr))] gap-4 lg:max-w-[920px]">
        <SelectField id="bv-dari" label="Dari bulan" value={from} onChange={(e) => setFrom(Math.min(Number(e.target.value), to))}>
          {months.map((m, i) => (
            <option key={m} value={i + 1}>
              {monthLabel(m)}
            </option>
          ))}
        </SelectField>
        <SelectField id="bv-sampai" label="Sampai bulan" value={to} onChange={(e) => setTo(Math.max(Number(e.target.value), from))}>
          {months.map((m, i) => (
            <option key={m} value={i + 1}>
              {monthLabel(m)}
            </option>
          ))}
        </SelectField>
        <SliceSelect me={me} id="bv-irisan" value={slice} onChange={setSlice} allLabel="Seluruh PT" />
      </div>

      {c.isPending && <Skeleton className="h-72 rounded-2xl" />}
      {c.isError && <Alert>{c.error.message}</Alert>}
      {data && (
        <div className={`flex flex-col gap-5 transition-opacity duration-150 ${c.isPlaceholderData ? 'opacity-60' : ''}`}>
          {!data.budget && (
            <Alert tone="warn">
              Tahun buku ini belum punya anggaran aktif; yang tampil hanya realisasi.{' '}
              {can(me, 'report.budget.manage') && (
                <button type="button" onClick={onEdit} className="cursor-pointer font-semibold underline">
                  Susun anggaran
                </button>
              )}
            </Alert>
          )}
          <div className="grid grid-cols-[repeat(auto-fit,minmax(250px,1fr))] gap-4">
            <SummaryCard index={0} title="Pendapatan" row={data.revenue} kind="revenue" />
            <SummaryCard index={1} title="Beban" row={data.expense} kind="expense" />
            <SummaryCard index={2} title="Laba" row={data.profit} kind="profit" />
          </div>
          {(data.near > 0 || data.over > 0) && (
            <Alert tone={data.over > 0 ? 'bad' : 'warn'}>
              {data.over > 0 && `${data.over} akun beban melebihi anggaran setahun atau belum dianggarkan. `}
              {data.near > 0 && `${data.near} akun beban sudah mencapai ${data.nearPct}% anggaran setahun. `}
              Lihat kolom Status di tabel.
            </Alert>
          )}
          <section className="flex flex-col gap-3 rounded-2xl border border-line bg-surface p-5 shadow-card">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h2 className="text-base font-semibold tracking-tight">Per bulan</h2>
                <p className="mt-1 text-[13px] text-muted">Batang = realisasi, kotak terang = anggaran. Bulan di luar periode dipudarkan.</p>
              </div>
              <Segmented
                label="Grafik"
                value={chart}
                onChange={setChart}
                options={[
                  { value: 'beban', label: 'Beban' },
                  { value: 'pendapatan', label: 'Pendapatan' },
                ]}
              />
            </div>
            <MonthlyChart points={data.monthly} expense={chart === 'beban'} from={from} to={to} withYear={withYear} />
          </section>
          <CompareTable data={data} />
        </div>
      )}
    </div>
  )
}

function SummaryCard({ index, title, row, kind }: { index: number; title: string; row: CompareRow; kind: 'revenue' | 'expense' | 'profit' }) {
  const pct = row.pct ?? null
  const bar = pct === null ? 0 : Math.min(100, Math.max(0, pct))
  const color = kind === 'expense' ? (pct !== null && pct > 100 ? 'bg-bad' : pct !== null && pct >= 90 ? 'bg-warn' : 'bg-accent') : kind === 'revenue' && pct !== null && pct >= 100 ? 'bg-good' : 'bg-accent'
  const neg = toUnits(row.actual) < 0n
  return (
    <motion.section
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease, delay: index * 0.06 }}
      className="flex flex-col gap-2 rounded-2xl border border-line bg-surface p-5 shadow-card"
    >
      <span className="text-[13px] font-medium text-muted">{title} · realisasi</span>
      <span className={`num text-[24px] leading-tight font-bold tracking-[-0.02em] ${neg ? 'text-bad' : ''}`}>Rp {formatAmount(row.actual)}</span>
      <span className="text-[13px] text-muted">
        Anggaran periode <span className="num font-semibold text-ink">Rp {formatAmount(row.budget)}</span>
        {pct !== null && <span className="num ml-1">({pct.toLocaleString('id-ID', { maximumFractionDigits: 0 })}%)</span>}
      </span>
      {kind !== 'profit' && (
        <div className="mt-1 h-2 overflow-hidden rounded-full bg-hover">
          <motion.div className={`h-full rounded-full ${color}`} initial={{ width: 0 }} animate={{ width: `${bar}%` }} transition={{ duration: 0.7, ease, delay: 0.2 + index * 0.06 }} />
        </div>
      )}
      {kind === 'expense' && (
        <span className="text-[12.5px] text-muted">
          Sisa anggaran setahun <span className="num font-semibold text-ink">Rp {formatAmount(row.remaining)}</span>
        </span>
      )}
    </motion.section>
  )
}

function MonthlyChart({ points, expense, from, to, withYear }: { points: MonthPoint[]; expense: boolean; from: number; to: number; withYear: boolean }) {
  const units = (v: string) => Number(toUnits(v) / 10000n)
  const pairs = points.map((p) => (expense ? [units(p.budgetExpense), units(p.actualExpense)] : [units(p.budgetRevenue), units(p.actualRevenue)]))
  const peak = Math.max(1, ...pairs.flat())
  const W = 720
  const H = 180
  const step = W / 12
  const h = (v: number) => (Math.max(0, v) / peak) * (H - 24)
  return (
    <svg viewBox={`0 0 ${W} ${H + 22}`} className="block w-full" role="img" aria-label={expense ? 'Beban per bulan: anggaran dan realisasi' : 'Pendapatan per bulan: anggaran dan realisasi'}>
      {pairs.map(([b, a], i) => {
        const x = step * i + step / 2
        const inside = i + 1 >= from && i + 1 <= to
        const over = expense && b > 0 && a > b
        return (
          <g key={points[i].month} opacity={inside ? 1 : 0.35}>
            <title>{`${monthLabel(points[i].month)} · anggaran ${b.toLocaleString('id-ID')} · realisasi ${a.toLocaleString('id-ID')}`}</title>
            <rect x={x - 20} y={H - h(b)} width="40" height={h(b)} rx="6" fill="var(--color-accent-soft)" stroke="var(--color-line-2)" />
            <motion.rect
              x={x - 9}
              width="18"
              rx="4"
              fill={over ? 'var(--color-bad)' : expense ? 'var(--color-slate)' : 'var(--color-accent)'}
              initial={{ y: H, height: 0 }}
              animate={{ y: H - h(a), height: h(a) }}
              transition={{ duration: 0.6, ease, delay: 0.1 + i * 0.03 }}
            />
            <text x={x} y={H + 16} textAnchor="middle" fontSize="12" fill="var(--color-muted)">
              {monthShort(points[i].month, withYear)}
            </text>
          </g>
        )
      })}
      <line x1="0" x2={W} y1={H} y2={H} stroke="var(--color-line)" />
    </svg>
  )
}

const statusBadge = {
  near: <Badge tone="warn">Mendekati batas</Badge>,
  over: <Badge tone="bad">Melebihi</Badge>,
  unbudgeted: <Badge tone="bad">Tanpa anggaran</Badge>,
}

function CompareTable({ data }: { data: Comparison }) {
  const cell = `${td} num text-right whitespace-nowrap`
  const variance = (r: CompareRow, expense: boolean) => {
    const v = toUnits(r.variance)
    const tone = expense ? (v > 0n ? 'text-bad' : '') : v < 0n ? 'text-warn' : ''
    return <span className={tone}>{formatAmount(r.variance, { blankZero: true })}</span>
  }
  const pctCell = (r: CompareRow) =>
    r.pct === undefined || r.pct === null ? '' : `${r.pct.toLocaleString('id-ID', { maximumFractionDigits: 0 })}%`
  const line = (r: CompareRow, expense: boolean, strong = false) => (
    <>
      <td className={cell}>{formatAmount(r.budget, { blankZero: !strong })}</td>
      <td className={cell}>{formatAmount(r.actual, { blankZero: !strong })}</td>
      <td className={cell}>{variance(r, expense)}</td>
      <td className={cell}>{pctCell(r)}</td>
      <td className={cell}>{formatAmount(r.yearBudget, { blankZero: !strong })}</td>
      <td className={cell}>{formatAmount(r.remaining, { blankZero: !strong })}</td>
    </>
  )
  if (data.sections.length === 0) return <EmptyState title="Belum ada anggaran maupun realisasi" body="Pilih tahun buku lain, atau susun anggarannya dulu." />
  return (
    <TableShell minWidth={1000}>
      <thead className="bg-hover/60">
        <tr>
          <th className={th}>Akun</th>
          <th className={`${th} text-right`}>Anggaran</th>
          <th className={`${th} text-right`}>Realisasi</th>
          <th className={`${th} text-right`}>Selisih</th>
          <th className={`${th} text-right`}>%</th>
          <th className={`${th} text-right`}>Anggaran setahun</th>
          <th className={`${th} text-right`}>Sisa setahun</th>
          <th className={th}>Status</th>
        </tr>
      </thead>
      <tbody>
        {data.sections.map((s) => [
          <tr key={`h-${s.class}`} className="border-t border-line bg-hover/50">
            <td colSpan={8} className={`${td} text-xs font-semibold tracking-wide text-muted uppercase`}>
              {s.class} · {s.name}
            </td>
          </tr>,
          ...s.rows.map((r) => (
            <tr key={r.accountId} className="border-t border-line transition-colors hover:bg-hover/40">
              <td className={td}>
                <Link to={`/buku-besar?akun=${r.accountId}`} className="hover:text-accent">
                  <span className="num mr-2 text-[12.5px] text-muted">{r.code}</span>
                  {r.name}
                </Link>
              </td>
              {line(r, s.expense)}
              <td className={td}>{r.status && statusBadge[r.status]}</td>
            </tr>
          )),
          <tr key={`t-${s.class}`} className="border-t border-line font-semibold">
            <td className={td}>{s.total.name}</td>
            {line(s.total, s.expense, true)}
            <td className={td} />
          </tr>,
        ])}
        {[
          { r: data.revenue, expense: false },
          { r: data.expense, expense: true },
          { r: data.profit, expense: false },
        ].map(({ r, expense }) => (
          <tr key={r.name} className="border-t border-line-2 bg-hover/40 font-semibold">
            <td className={td}>Jumlah {r.name.toLowerCase()}</td>
            {line(r, expense, true)}
            <td className={td} />
          </tr>
        ))}
      </tbody>
    </TableShell>
  )
}

// Susun anggaran ----------------------------------------------------------------------------------

function EditorTab({ me, year, startMonth }: { me: Me; year: number; startMonth: number }) {
  const companyId = me.activeCompanyId
  const canEdit = can(me, 'report.budget.manage')
  const budgets = useBudgets(companyId)
  const create = useCreateBudget(companyId)
  const activate = useActivateBudget(companyId)
  const remove = useDeleteBudget(companyId)
  const toast = useToast()
  const versions = (budgets.data ?? []).filter((b) => b.fiscalYear === year)
  const [selected, setSelected] = useState<string | null>(null)
  const current = versions.find((v) => v.id === selected) ?? versions.find((v) => v.active) ?? versions[0] ?? null
  const detail = useBudget(companyId, current?.id ?? null)
  const accounts = useAccounts(companyId)
  const [slice, setSlice] = useState('')
  const [dialog, setDialog] = useState<'baru' | 'nama' | 'hapus' | null>(null)
  const [dirty, setDirty] = useState(false)

  function guard(fn: () => void) {
    if (!dirty || window.confirm('Perubahan anggaran belum disimpan. Buang perubahan itu?')) {
      setDirty(false)
      fn()
    }
  }

  if (budgets.isPending || accounts.isPending) return <Skeleton className="h-96 rounded-2xl" />
  if (versions.length === 0 || !current) {
    return (
      <EmptyState
        title={`Belum ada anggaran tahun buku ${year}`}
        body={canEdit ? 'Mulai dengan versi Awal. Isinya bisa diambil dari realisasi tahun lalu ditambah persen kenaikan.' : 'Minta Pemilik atau Manager menyusun anggarannya.'}
        action={
          canEdit && (
            <Button busy={create.isPending} onClick={() => create.mutate({ fiscalYear: year, name: 'Awal' }, { onSuccess: (b) => (setSelected(b.id), toast(`Anggaran ${year} dibuat`)) })}>
              Buat anggaran {year}
            </Button>
          )
        }
      />
    )
  }
  const err = (create.error ?? activate.error ?? remove.error) as ApiError | null
  return (
    <div className="flex flex-col gap-5 pb-28">
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-[260px]">
          <SelectField id="ang-versi" label="Versi" value={current.id} onChange={(e) => guard(() => setSelected(e.target.value))}>
            {versions.map((v) => (
              <option key={v.id} value={v.id}>
                {v.name}
                {v.active ? ' (aktif)' : ''}
              </option>
            ))}
          </SelectField>
        </div>
        <div className="w-[320px]">
          <SliceSelect me={me} id="ang-irisan" value={slice} onChange={(v) => guard(() => setSlice(v))} allLabel="Tingkat PT (tanpa departemen/proyek)" />
        </div>
        <div className="flex flex-wrap items-center gap-2 pb-0.5">
          {current.active ? <Badge tone="good">Dipakai laporan</Badge> : <Badge tone="neutral">Tidak aktif</Badge>}
          {canEdit && !current.active && (
            <Button variant="secondary" size="sm" busy={activate.isPending} onClick={() => activate.mutate(current.id, { onSuccess: () => toast(`${current.name} dipakai laporan anggaran vs realisasi`) })}>
              Aktifkan
            </Button>
          )}
          {canEdit && (
            <>
              <Button variant="secondary" size="sm" onClick={() => setDialog('baru')}>
                Versi baru…
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setDialog('nama')}>
                Ganti nama…
              </Button>
              {!current.active && (
                <Button variant="ghost" size="sm" className="text-bad" onClick={() => setDialog('hapus')}>
                  Hapus…
                </Button>
              )}
            </>
          )}
        </div>
      </div>
      {err && <Alert>{err.message}</Alert>}
      {detail.isPending && <Skeleton className="h-96 rounded-2xl" />}
      {detail.data && accounts.data && (
        <BudgetGrid
          key={`${detail.data.id}-${slice}-${detail.data.version}`}
          me={me}
          detail={detail.data}
          slice={sliceOf(slice)}
          accounts={accounts.data.items}
          classes={accounts.data.classes.map((c) => c.name)}
          canEdit={canEdit}
          withYear={startMonth !== 1}
          onDirty={setDirty}
        />
      )}
      <VersionDialogs me={me} year={year} versions={versions} current={current} open={dialog} onClose={() => setDialog(null)} onCreated={(id) => setSelected(id)} />
    </div>
  )
}

function VersionDialogs({ me, year, versions, current, open, onClose, onCreated }: { me: Me; year: number; versions: Budget[]; current: Budget; open: 'baru' | 'nama' | 'hapus' | null; onClose: () => void; onCreated: (id: string) => void }) {
  const create = useCreateBudget(me.activeCompanyId)
  const rename = useRenameBudget(me.activeCompanyId)
  const remove = useDeleteBudget(me.activeCompanyId)
  const toast = useToast()
  const [name, setName] = useState('')
  const [copyFrom, setCopyFrom] = useState('')
  const [shownFor, setShownFor] = useState<string | null>(null)
  if (open && shownFor !== open) {
    setShownFor(open)
    setName(open === 'nama' ? current.name : `Revisi ${versions.length}`)
    setCopyFrom(current.id)
    create.reset()
    rename.reset()
    remove.reset()
  }
  if (!open && shownFor) setShownFor(null)
  const err = (open === 'baru' ? create.error : open === 'nama' ? rename.error : remove.error) as ApiError | null
  const close = () => onClose()
  return (
    <>
      <Dialog
        open={open === 'baru'}
        onClose={close}
        title={`Versi anggaran baru ${year}`}
        description="Mis. revisi pertengahan tahun. Versi baru tidak langsung dipakai laporan; aktifkan setelah selesai disusun."
        footer={
          <>
            <Button variant="secondary" onClick={close}>
              Batal
            </Button>
            <Button
              busy={create.isPending}
              disabled={!name.trim()}
              onClick={() =>
                create.mutate(
                  { fiscalYear: year, name: name.trim(), copyFrom: copyFrom || undefined },
                  { onSuccess: (b) => (toast(`Versi ${b.name} dibuat`), onCreated(b.id), close()) },
                )
              }
            >
              Buat versi
            </Button>
          </>
        }
      >
        <Field id="versi-nama" label="Nama versi" maxLength={60} value={name} onChange={(e) => setName(e.target.value)} error={err?.fields.name} />
        <SelectField id="versi-salin" label="Isi awal" value={copyFrom} onChange={(e) => setCopyFrom(e.target.value)}>
          <option value="">Kosong</option>
          {versions.map((v) => (
            <option key={v.id} value={v.id}>
              Salin dari {v.name}
            </option>
          ))}
        </SelectField>
        {err && !err.fields.name && <Alert>{err.message}</Alert>}
      </Dialog>
      <Dialog
        open={open === 'nama'}
        onClose={close}
        title="Ganti nama versi"
        footer={
          <>
            <Button variant="secondary" onClick={close}>
              Batal
            </Button>
            <Button
              busy={rename.isPending}
              disabled={!name.trim()}
              onClick={() => rename.mutate({ id: current.id, name: name.trim(), version: current.version }, { onSuccess: () => (toast('Nama versi disimpan'), close()) })}
            >
              Simpan
            </Button>
          </>
        }
      >
        <Field id="versi-ganti" label="Nama versi" maxLength={60} value={name} onChange={(e) => setName(e.target.value)} error={err?.fields.name} />
        {err && !err.fields.name && <Alert>{err.message}</Alert>}
      </Dialog>
      <Dialog
        open={open === 'hapus'}
        onClose={close}
        title={`Hapus versi ${current.name}?`}
        description="Semua baris anggaran di versi ini ikut terhapus. Versi lain dan realisasi tidak berubah."
        footer={
          <>
            <Button variant="secondary" onClick={close}>
              Batal
            </Button>
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate(current.id, { onSuccess: () => (toast(`Versi ${current.name} dihapus`), onCreated(''), close()) })}>
              Hapus versi
            </Button>
          </>
        }
      >
        {err && <Alert>{err.message}</Alert>}
      </Dialog>
    </>
  )
}

type Amounts = Record<string, string[]>

const blank = () => Array.from({ length: 12 }, () => '')

/** Bagi rata nominal setahun ke 12 bulan dalam rupiah bulat; sisa pembagian masuk bulan terakhir. */
function spread(total: bigint): string[] {
  const rupiah = total / 10000n
  const base = rupiah / 12n
  const rest = rupiah - base * 12n
  return Array.from({ length: 12 }, (_, i) => {
    const v = (i === 11 ? base + rest : base) * 10000n
    return v === 0n ? '' : formatAmount(v)
  })
}

function rowTotal(months: string[]): bigint | null {
  let t = 0n
  for (const m of months) {
    const u = parseInput(m)
    if (u === null) return null
    t += u
  }
  return t
}

const cellInput =
  'h-9 w-full rounded-[8px] border bg-surface px-2 text-right text-[13px] text-ink tabular-nums transition-[border-color,box-shadow] duration-150 placeholder:text-line-2 hover:border-muted focus:border-accent focus:shadow-[0_0_0_3px_rgb(51_85_224/0.14)] focus-visible:outline-none disabled:opacity-60'

function BudgetGrid({ me, detail, slice, accounts, classes, canEdit, withYear, onDirty }: { me: Me; detail: BudgetDetail; slice: Slice; accounts: Account[]; classes: string[]; canEdit: boolean; withYear: boolean; onDirty: (v: boolean) => void }) {
  const save = useSaveSlice(me.activeCompanyId)
  const toast = useToast()
  const initial = useMemo(() => {
    const out: Amounts = {}
    for (const l of detail.lines) {
      if ((l.departmentId ?? '') === (slice.departmentId ?? '') && (l.projectId ?? '') === (slice.projectId ?? '')) {
        out[l.accountId] = l.months.map((m) => formatAmount(m, { blankZero: true }))
      }
    }
    return out
  }, [detail, slice])
  const [amounts, setAmounts] = useState<Amounts>(initial)
  const [search, setSearch] = useState('')
  const [filledOnly, setFilledOnly] = useState(false)
  const [growth, setGrowth] = useState('0')
  const [loading, setLoading] = useState(false)
  const [notice, setNotice] = useState('')
  const dirty = JSON.stringify(amounts) !== JSON.stringify(initial)

  function update(next: Amounts) {
    setAmounts(next)
    onDirty(JSON.stringify(next) !== JSON.stringify(initial))
  }
  const setMonth = (id: string, i: number, v: string) => update({ ...amounts, [id]: (amounts[id] ?? blank()).map((m, k) => (k === i ? v : m)) })
  const filled = (id: string) => (amounts[id] ?? []).some((m) => m.trim() && parseInput(m) !== 0n)

  const rows = accounts
    .filter((a) => a.postable && a.class >= 4 && (a.active || filled(a.id)))
    .sort((a, b) => a.code.localeCompare(b.code))
  const q = search.trim().toLowerCase()
  const visible = rows.filter((a) => (!q || a.code.toLowerCase().includes(q) || a.name.toLowerCase().includes(q)) && (!filledOnly || filled(a.id)))

  let revenue = 0n
  let expense = 0n
  let unreadable = false
  for (const a of rows) {
    const t = rowTotal(amounts[a.id] ?? blank())
    if (t === null) unreadable = true
    else if (a.class === 4 || a.class === 7) revenue += t
    else expense += t
  }

  async function fillFromLastYear() {
    const g = growth.trim() === '' ? '0' : growth.trim().replace(',', '.')
    if (Object.keys(amounts).some(filled) && !window.confirm('Angka yang sudah diisi untuk akun yang punya realisasi tahun lalu akan diganti. Lanjutkan?')) return
    setLoading(true)
    setNotice('')
    try {
      const res = await suggestBudget(detail.fiscalYear, slice, g)
      const next = { ...amounts }
      for (const l of res.lines) next[l.accountId] = l.months.map((m) => formatAmount(m, { blankZero: true }))
      update(next)
      setNotice(res.lines.length ? `${res.lines.length} akun diisi dari realisasi tahun buku ${detail.fiscalYear - 1}${g !== '0' ? ` + ${g}%` : ''}. Periksa lalu simpan.` : `Tidak ada realisasi di tahun buku ${detail.fiscalYear - 1} untuk irisan ini.`)
    } catch (e) {
      setNotice(e instanceof Error ? e.message : 'Gagal mengambil realisasi tahun lalu.')
    } finally {
      setLoading(false)
    }
  }

  function submit() {
    const lines = Object.entries(amounts)
      .filter(([id]) => filled(id))
      .map(([accountId, months]) => ({ accountId, months: months.map((m) => fromUnits(parseInput(m) ?? 0n)) }))
    save.mutate(
      { id: detail.id, version: detail.version, lines, ...slice },
      { onSuccess: () => (onDirty(false), toast('Anggaran disimpan')) },
    )
  }

  const err = save.error instanceof ApiError ? save.error : null
  let lastClass = 0
  return (
    <>
      <div className="flex flex-wrap items-end gap-3">
        <input
          type="search"
          aria-label="Cari akun"
          placeholder="Cari kode atau nama akun"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="min-h-11 w-full max-w-[280px] rounded-[10px] border border-line-2 bg-surface px-3 text-[15px] focus:border-accent focus:shadow-[0_0_0_4px_rgb(51_85_224/0.14)] focus-visible:outline-none"
        />
        <label className="flex min-h-11 cursor-pointer items-center gap-2 text-sm">
          <input type="checkbox" checked={filledOnly} onChange={(e) => setFilledOnly(e.target.checked)} className="size-[18px] cursor-pointer accent-[var(--color-accent)]" />
          Hanya yang terisi
        </label>
        {canEdit && (
          <div className="ml-auto flex flex-wrap items-end gap-2">
            <div className="w-[120px]">
              <Field id="ang-naik" label="Kenaikan (%)" inputMode="decimal" className="num text-right" value={growth} onChange={(e) => setGrowth(e.target.value)} />
            </div>
            <Button variant="secondary" busy={loading} onClick={fillFromLastYear}>
              Isi dari realisasi {detail.fiscalYear - 1}
            </Button>
          </div>
        )}
      </div>
      {notice && <Alert tone="good">{notice}</Alert>}
      <p className="text-[13px] text-muted">
        Isi kolom <b>Setahun</b> untuk membaginya rata ke 12 bulan, atau isi per bulan. Nominal menurut saldo normal akun: pendapatan dan beban sama-sama positif.
      </p>
      <div className="overflow-hidden rounded-2xl border border-line bg-surface shadow-card">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm" style={{ minWidth: 1720 }}>
            <thead className="bg-hover/60 text-[12.5px] text-muted">
              <tr>
                <th className="sticky left-0 z-10 w-[280px] bg-surface px-4 py-3 font-semibold">Akun</th>
                <th className="w-[150px] px-2 py-3 text-right font-semibold">Setahun</th>
                {detail.months.map((m) => (
                  <th key={m} className="w-[105px] px-1.5 py-3 text-right font-semibold">
                    {monthShort(m, withYear)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {visible.map((a) => {
                const header = a.class !== lastClass ? classes[a.class - 1] : null
                lastClass = a.class
                const months = amounts[a.id] ?? blank()
                const total = rowTotal(months)
                const rowErr = err?.fields[`lines.${a.id}`]
                return [
                  header && (
                    <tr key={`k-${a.class}`} className="border-t border-line bg-hover/50">
                      <td colSpan={14} className="sticky left-0 px-4 py-2 text-xs font-semibold tracking-wide text-muted uppercase">
                        {a.class} · {header}
                      </td>
                    </tr>
                  ),
                  <tr key={a.id} className="border-t border-line">
                    <td className="sticky left-0 z-10 bg-surface px-4 py-1.5">
                      <span className="num mr-2 text-[12.5px] text-muted">{a.code}</span>
                      {a.name}
                      {rowErr && <span className="block text-[12.5px] text-bad">{rowErr}</span>}
                    </td>
                    <td className="px-2 py-1.5">
                      <input
                        aria-label={`Setahun ${a.code} ${a.name}`}
                        inputMode="decimal"
                        disabled={!canEdit}
                        placeholder="0"
                        defaultValue={total ? formatAmount(total) : ''}
                        key={`${total}`}
                        onBlur={(e) => {
                          const u = parseInput(e.target.value)
                          if (u === null || u === total) return
                          update({ ...amounts, [a.id]: spread(u) })
                        }}
                        className={`${cellInput} border-line-2 font-semibold ${total === null ? 'border-bad' : ''}`}
                      />
                    </td>
                    {months.map((m, i) => (
                      <td key={i} className="px-1.5 py-1.5">
                        <input
                          aria-label={`${monthLabel(detail.months[i])} ${a.code} ${a.name}`}
                          inputMode="decimal"
                          disabled={!canEdit}
                          value={m}
                          onChange={(e) => setMonth(a.id, i, e.target.value)}
                          onBlur={(e) => {
                            const u = parseInput(e.target.value)
                            if (u !== null) setMonth(a.id, i, u === 0n ? '' : formatAmount(u))
                          }}
                          className={`${cellInput} ${parseInput(m) === null ? 'border-bad' : 'border-line'}`}
                        />
                      </td>
                    ))}
                  </tr>,
                ]
              })}
              {visible.length === 0 && (
                <tr>
                  <td colSpan={14} className="px-4 py-6 text-center text-sm text-muted">
                    {rows.length === 0 ? (
                      <>
                        Belum ada akun pendapatan atau beban. <Link to="/akun" className="font-semibold text-accent underline">Buka Bagan akun</Link>
                      </>
                    ) : (
                      'Tidak ada akun yang cocok.'
                    )}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      <div className="fixed inset-x-0 bottom-0 z-10 border-t border-line bg-surface/95 px-4 py-3 backdrop-blur sm:px-8 lg:left-[244px] print:hidden">
        <div className="mx-auto flex max-w-[1920px] flex-wrap items-center gap-x-8 gap-y-2">
          <Total label="Anggaran pendapatan" value={revenue} />
          <Total label="Anggaran beban" value={expense} />
          <Total label="Rencana laba" value={revenue - expense} strong />
          <span className="text-[12.5px] text-muted">{detail.name}{detail.active ? ' · aktif' : ''} · {slice.departmentId ? 'per departemen' : slice.projectId ? 'per proyek' : 'tingkat PT'}</span>
          {canEdit && (
            <Button className="ml-auto" busy={save.isPending} disabled={!dirty || unreadable} onClick={submit}>
              Simpan anggaran
            </Button>
          )}
        </div>
      </div>
    </>
  )
}

function Total({ label, value, strong }: { label: string; value: bigint; strong?: boolean }) {
  return (
    <span className="flex flex-col">
      <span className="text-[12.5px] text-muted">{label}</span>
      <span className={`num ${strong ? 'font-bold' : 'font-semibold'} ${value < 0n ? 'text-bad' : ''}`}>{formatAmount(value)}</span>
    </span>
  )
}

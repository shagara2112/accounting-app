import { useMemo, useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type BillingInput, type Me, type ProjectDetail as Detail, type ProjectReportRow } from '../lib/api'
import { addMonths, formatDate, monthOf, todayISO } from '../lib/dates'
import { useTaxCodes } from '../lib/docs'
import { docState, sourceLabel } from '../lib/labels'
import { useAccounts } from '../lib/ledger'
import { formatAmount, fromUnits, parseInput, toUnits } from '../lib/money'
import { invoiceKindLabel, projectStatus, useCreateBilling, useProject, useProjectLines, useProjectReport, useSaveBudget } from '../lib/projects'
import { can } from '../lib/session'
import { computeLine, rateLabel } from '../lib/trade'
import { AccountPicker } from '../components/AccountPicker'
import { ProjectDialog } from '../components/ProjectDialog'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Dialog, EmptyState, Field, Segmented, SelectField, Skeleton, TableShell, Tabs, inputClass, td, th } from '../components/ui'
import { BudgetBar } from './Projects'

type Tab = 'tagihan' | 'rab' | 'rincian'

/** Satu proyek: tagihan uang muka, termin, dan retensi (AR-08); RAB vs aktual dan laba (IND-03). */
export function ProjectDetailPage({ me }: { me: Me }) {
  const { id = '' } = useParams()
  const companyId = me.activeCompanyId
  const project = useProject(companyId, id)
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Tab) ?? 'tagihan'
  const [editing, setEditing] = useState(false)
  const [billing, setBilling] = useState<BillingInput['kind'] | null>(null)

  if (project.isPending) return <Skeleton className="h-96" />
  if (project.isError) return <EmptyState title="Proyek tidak ditemukan" body={project.error.message} />
  const p = project.data
  const profit = toUnits(p.profit)
  const margin = toUnits(p.revenue) > 0n ? Number((profit * 10000n) / toUnits(p.revenue)) / 100 : null

  function setTab(t: Tab) {
    const next = new URLSearchParams(params)
    if (t === 'tagihan') next.delete('tab')
    else next.set('tab', t)
    next.delete('akun')
    setParams(next, { replace: true })
  }

  return (
    <div className="flex flex-col gap-5">
      <div>
        <Link to="/proyek" className="text-sm font-medium text-muted hover:text-ink">
          ← Proyek
        </Link>
        <div className="mt-1 flex flex-wrap items-end justify-between gap-4">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-3">
              <h1 className="text-[26px] font-bold tracking-tight">{p.name}</h1>
              <Badge tone={projectStatus[p.status].tone}>{projectStatus[p.status].label}</Badge>
            </div>
            <p className="mt-1.5 text-sm text-muted">
              <span className="num">{p.code}</span> · {p.customerName || 'Proyek internal'}
              {p.startDate && ` · ${formatDate(p.startDate)}`}
              {p.endDate && ` s.d. ${formatDate(p.endDate)}`}
              {p.pphCode && ` · ${p.pphCode}`}
            </p>
          </div>
          <div className="flex flex-wrap gap-2.5">
            {can(me, 'project.create') && (
              <Button variant="secondary" onClick={() => setEditing(true)}>
                Ubah proyek
              </Button>
            )}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-[repeat(auto-fit,minmax(200px,1fr))] gap-4">
        <Metric label="Nilai kontrak" value={formatAmount(p.contractValue)} note={toUnits(p.dpPct) > 0n || toUnits(p.retentionPct) > 0n ? `UM ${formatAmount(p.dpPct)}% · retensi ${formatAmount(p.retentionPct)}%` : undefined} />
        <Metric label="Pendapatan diakui" value={formatAmount(p.revenue)} note={`Progres ditagih ${formatAmount(p.billing.progressPct)}%`} />
        <Metric label="Biaya aktual" value={formatAmount(p.cost)}>
          <BudgetBar cost={p.cost} budget={p.budget} pct={p.costPct} />
        </Metric>
        <Metric label="Laba proyek" value={formatAmount(p.profit)} tone={profit < 0n ? 'bad' : profit > 0n ? 'good' : undefined} note={margin !== null ? `Margin ${margin.toLocaleString('id-ID')}%` : undefined} />
      </div>

      <Tabs
        label="Bagian proyek"
        value={tab}
        onChange={setTab}
        tabs={[
          { value: 'tagihan', label: 'Tagihan' },
          { value: 'rab', label: 'RAB vs aktual' },
          { value: 'rincian', label: 'Rincian jurnal' },
        ]}
      />
      {tab === 'tagihan' && <BillingTab me={me} p={p} onBill={setBilling} />}
      {tab === 'rab' && <BudgetTab me={me} p={p} />}
      {tab === 'rincian' && <LinesTab me={me} p={p} />}

      <ProjectDialog me={me} editing={editing ? p : null} onClose={() => setEditing(false)} />
      <BillingDialog me={me} p={p} kind={billing} onClose={() => setBilling(null)} />
    </div>
  )
}

function Metric({ label, value, note, tone, children }: { label: string; value: string; note?: string; tone?: 'good' | 'bad'; children?: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 rounded-[14px] border border-line bg-surface p-4">
      <span className="text-[13px] text-muted">{label}</span>
      {!children && <span className={`num text-xl font-semibold ${tone === 'bad' ? 'text-bad' : tone === 'good' ? 'text-good' : ''}`}>{value}</span>}
      {children}
      {note && <span className="text-[12.5px] text-muted">{note}</span>}
    </div>
  )
}

function BillingTab({ me, p, onBill }: { me: Me; p: Detail; onBill: (k: BillingInput['kind']) => void }) {
  const navigate = useNavigate()
  const b = p.billing
  const canBill = can(me, 'sales.create') && p.status !== 'batal'
  const progress = Math.min(Number(b.progressPct), 100)
  const hasAdvance = toUnits(p.dpPct) > 0n
  return (
    <>
      <Card
        title="Posisi tagihan"
        subtitle="Termin = progres kumulatif × nilai kontrak − termin sebelumnya. Uang muka dipotong proporsional, retensi ditahan sebagai piutang retensi sampai dilepas."
        actions={
          canBill && (
            <div className="flex flex-wrap gap-2">
              {hasAdvance && toUnits(b.advanceInvoiced) === 0n && (
                <Button variant="secondary" onClick={() => onBill('uang_muka')}>
                  Faktur uang muka
                </Button>
              )}
              {toUnits(b.retentionHeld) > 0n && (
                <Button variant="secondary" onClick={() => onBill('retensi')}>
                  Lepas retensi
                </Button>
              )}
              {p.status === 'berjalan' && toUnits(b.unbilled) > 0n && <Button onClick={() => onBill('termin')}>Faktur termin</Button>}
            </div>
          )
        }
      >
        <div className="flex flex-col gap-2">
          <div className="flex items-baseline justify-between text-sm">
            <span>Progres ditagih</span>
            <span className="num font-semibold">{formatAmount(b.progressPct)}%</span>
          </div>
          <span className="h-2 overflow-hidden rounded-full bg-hover" aria-hidden="true">
            <span className="block h-full rounded-full bg-accent transition-[width] duration-500" style={{ width: `${progress}%` }} />
          </span>
        </div>
        <dl className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-6 gap-y-3 pt-2 text-sm">
          <Fact label="Nilai progres ditagih" value={formatAmount(b.progressBilled)} />
          <Fact label="Belum ditagih" value={formatAmount(b.unbilled)} />
          {hasAdvance && <Fact label="Uang muka difakturkan" value={formatAmount(b.advanceInvoiced)} note={b.advanceInvoiced !== '0.0000' && !b.advancePosted ? 'masih draf' : undefined} />}
          {hasAdvance && <Fact label="Sisa uang muka" value={formatAmount(b.advanceRemaining)} note={`dipotong ${formatAmount(b.advanceDeducted)}`} />}
          <Fact label="Retensi tertahan" value={formatAmount(b.retentionHeld)} note={toUnits(b.retentionReleased) > 0n ? `dilepas ${formatAmount(b.retentionReleased)}` : undefined} />
        </dl>
        {b.drafts > 0 && <Alert tone="warn">Ada {b.drafts} draf faktur proyek yang belum diposting. Draf ikut dihitung supaya termin tidak tertagih dua kali.</Alert>}
      </Card>

      <TableShell minWidth={860}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Tanggal</th>
            <th className={th}>Nomor</th>
            <th className={th}>Jenis</th>
            <th className={`${th} text-right`}>Nilai</th>
            <th className={`${th} text-right`}>Total + PPN</th>
            <th className={`${th} text-right`}>Sisa ditagih</th>
            <th className={th}>Status</th>
          </tr>
        </thead>
        <tbody>
          {p.invoices.map((inv) => (
            <tr key={inv.id} onClick={() => navigate(`/penjualan?bulan=${monthOf(inv.date)}&id=${inv.id}`)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${inv.status === 'void' ? 'text-muted' : ''}`}>
              <td className={`${td} num whitespace-nowrap`}>{formatDate(inv.date)}</td>
              <td className={`${td} num font-semibold ${inv.status === 'void' ? 'line-through' : 'text-accent'}`}>{inv.number ?? 'Draf'}</td>
              <td className={td}>
                {invoiceKindLabel[inv.kind]}
                {inv.progressPct && <span className="text-muted"> s.d. {formatAmount(inv.progressPct)}%</span>}
              </td>
              <td className={`${td} num text-right`}>{formatAmount(inv.kind === 'termin' ? inv.subtotal : inv.dpp)}</td>
              <td className={`${td} num text-right`}>{formatAmount(inv.total)}</td>
              <td className={`${td} num text-right`}>{inv.status === 'posted' ? formatAmount(inv.outstanding) : '—'}</td>
              <td className={td}>
                <Badge tone={docState[inv.state].tone}>{docState[inv.state].label}</Badge>
              </td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {p.invoices.length === 0 && <EmptyState title="Belum ada faktur untuk proyek ini" body="Faktur biasa juga bisa diberi proyek dari formulir penjualan." />}
    </>
  )
}

function Fact({ label, value, note }: { label: string; value: string; note?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="text-[12.5px] text-muted">{label}</dt>
      <dd className="num font-semibold">
        {value}
        {note && <span className="block text-[12px] font-normal text-muted">{note}</span>}
      </dd>
    </div>
  )
}

/** Membuat draf faktur uang muka, termin, atau pelepasan retensi; pratinjau dihitung di browser,
 *  angka finalnya dihitung server dari data proyek. */
function BillingDialog({ me, p, kind, onClose }: { me: Me; p: Detail; kind: BillingInput['kind'] | null; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const navigate = useNavigate()
  const toast = useToast()
  const create = useCreateBilling(companyId)
  const [shownFor, setShownFor] = useState<BillingInput['kind'] | null>(null)
  const [date, setDate] = useState(todayISO())
  const [pct, setPct] = useState('')
  const [taxCode, setTaxCode] = useState('')
  const [memo, setMemo] = useState('')
  const [key, setKey] = useState(newIdempotencyKey)
  const [attempt, setAttempt] = useState(0)
  const codes = useTaxCodes(kind ? companyId : null, date || todayISO())
  const ppn = (codes.data ?? []).filter((c) => c.kind === 'ppn')

  if (kind && shownFor !== kind) {
    setShownFor(kind)
    setDate(todayISO())
    setPct('')
    setTaxCode('')
    setMemo('')
    setKey(newIdempotencyKey())
    create.reset()
  }
  const current = kind ?? shownFor
  const err = create.error instanceof ApiError ? create.error : null
  const b = p.billing
  const contract = toUnits(p.contractValue)
  const code = taxCode || (ppn.some((c) => c.code === 'PPN') ? 'PPN' : 'NON-PPN')

  // Pratinjau perhitungan yang sama dengan server (lihat project.CreateBilling).
  const preview = useMemo(() => {
    const hundred = 100n * 10000n
    const round = (n: bigint, d: bigint) => ((n * 2n) / d + 1n) / 2n
    const pctOf = (a: bigint, rate: bigint) => round(a * rate, hundred * 10000n) * 10000n
    if (current === 'uang_muka') {
      const gross = pctOf(contract, toUnits(p.dpPct))
      return { gross, deduction: 0n, retention: 0n }
    }
    if (current === 'retensi') return { gross: toUnits(b.retentionHeld), deduction: 0n, retention: 0n }
    const rate = parseInput(pct)
    if (rate === null || rate <= toUnits(b.progressPct) || rate > hundred) return null
    const gross = pctOf(contract, rate) - toUnits(b.progressBilled)
    if (gross <= 0n) return null
    let deduction = 0n
    const remaining = toUnits(b.advanceRemaining)
    if (remaining > 0n) {
      deduction = rate === hundred ? remaining : pctOf(gross, toUnits(p.dpPct))
      if (deduction > remaining) deduction = remaining
      if (deduction > gross) deduction = gross
    }
    let retention = pctOf(gross, toUnits(p.retentionPct))
    if (retention > gross - deduction) retention = gross - deduction
    return { gross, deduction, retention }
  }, [current, pct, contract, p.dpPct, p.retentionPct, b])

  const vatCalc = preview ? computeLine({ qty: '1', unitPrice: fromUnits(preview.gross - preview.deduction), discountPct: '0', taxCode: current === 'retensi' ? 'NON-PPN' : code }, ppn) : null
  const vat = vatCalc && !vatCalc.invalid ? vatCalc.vat : 0n
  const title = current === 'uang_muka' ? 'Faktur uang muka' : current === 'retensi' ? 'Pelepasan retensi' : `Faktur termin ${b.terms + 1}`

  function submit() {
    if (!current) return
    setAttempt((n) => n + 1)
    const input: BillingInput = { kind: current, date, memo: memo.trim() }
    if (current === 'termin') input.progressPct = fromUnits(parseInput(pct) ?? 0n)
    if (current !== 'retensi' && taxCode) input.taxCode = taxCode
    create.mutate(
      { id: p.id, input, idempotencyKey: key },
      {
        onSuccess: (inv) => {
          toast(`Draf ${invoiceKindLabel[inv.kind].toLowerCase()} dibuat · periksa lalu posting`)
          onClose()
          navigate(`/penjualan?bulan=${monthOf(inv.date)}&id=${inv.id}`)
        },
      },
    )
  }

  return (
    <Dialog
      open={kind !== null}
      onClose={onClose}
      width={560}
      title={title}
      description={`${p.code} · ${p.customerName}`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button busy={create.isPending} disabled={!preview || !date} onClick={submit}>
            Buat draf faktur
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
        <Field id="tg-tanggal" label="Tanggal faktur" type="date" data-autofocus value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} />
        {current === 'termin' && (
          <Field
            id="tg-progres"
            label="Progres kumulatif %"
            inputMode="decimal"
            className="num text-right"
            placeholder={`lebih dari ${formatAmount(b.progressPct)}`}
            value={pct}
            onChange={(e) => setPct(e.target.value)}
            error={err?.fields.progressPct}
            hint="Dari berita acara progres pekerjaan"
          />
        )}
        {current !== 'retensi' && (
          <SelectField id="tg-pajak" label="Pajak" value={code} onChange={(e) => setTaxCode(e.target.value)}>
            {ppn.map((c) => (
              <option key={c.code} value={c.code} disabled={!me.companies.find((x) => x.id === me.activeCompanyId)?.pkp && c.rateBp > 0}>
                {c.code === 'NON-PPN' ? 'Tanpa PPN' : `${c.code} ${rateLabel(c.rateBp)}${c.dppNum !== c.dppDen ? ` × ${c.dppNum}/${c.dppDen}` : ''}`}
              </option>
            ))}
          </SelectField>
        )}
      </div>
      <Field id="tg-catatan" label="Catatan di faktur" value={memo} maxLength={500} onChange={(e) => setMemo(e.target.value)} error={err?.fields.memo} />
      {preview ? (
        <dl className="rounded-[12px] border border-line bg-canvas/60 p-4 text-sm">
          <PreviewRow label={current === 'termin' ? 'Nilai progres termin ini' : current === 'retensi' ? 'Retensi dilepas' : 'Uang muka'} value={preview.gross} />
          {preview.deduction > 0n && <PreviewRow label="Potongan uang muka" value={-preview.deduction} muted />}
          <PreviewRow label="DPP" value={preview.gross - preview.deduction} />
          <PreviewRow label="PPN" value={vat} muted />
          <PreviewRow label="Total faktur" value={preview.gross - preview.deduction + vat} strong />
          {preview.retention > 0n && (
            <>
              <PreviewRow label="Retensi ditahan" value={-preview.retention} muted />
              <PreviewRow label="Ditagih sekarang" value={preview.gross - preview.deduction + vat - preview.retention} strong />
            </>
          )}
          {current === 'retensi' && <p className="pt-2 text-[12.5px] text-muted">Tanpa PPN; PPN-nya sudah dipungut di faktur termin.</p>}
        </dl>
      ) : (
        current === 'termin' && <p className="text-sm text-muted">Isi progres kumulatif di atas {formatAmount(b.progressPct)}% untuk melihat nilai termin.</p>
      )}
      {err && Object.keys(err.fields).length === 0 && <Alert shakeKey={attempt}>{err.message}</Alert>}
    </Dialog>
  )
}

function PreviewRow({ label, value, strong, muted }: { label: string; value: bigint; strong?: boolean; muted?: boolean }) {
  return (
    <div className={`flex items-baseline justify-between gap-4 py-1 ${strong ? 'border-t border-line-2 pt-2 font-semibold' : ''} ${muted ? 'text-muted' : ''}`}>
      <dt>{label}</dt>
      <dd className="num">{formatAmount(value)}</dd>
    </div>
  )
}

const periods = [
  { value: 'semua', label: 'Sepanjang proyek' },
  { value: 'bulan', label: 'Bulan ini' },
  { value: 'tahun', label: 'Tahun ini' },
] as const

function range(v: (typeof periods)[number]['value']): { from?: string; to?: string } {
  const m = monthOf(todayISO())
  if (v === 'bulan') return { from: m, to: m }
  if (v === 'tahun') return { from: `${m.slice(0, 4)}-01`, to: m }
  return {}
}

function BudgetTab({ me, p }: { me: Me; p: Detail }) {
  const [period, setPeriod] = useState<(typeof periods)[number]['value']>('semua')
  const report = useProjectReport(me.activeCompanyId, p.id, range(period))
  const [editing, setEditing] = useState(false)
  const [, setParams] = useSearchParams()
  const r = report.data
  const openLines = (accountId: string) => setParams({ tab: 'rincian', akun: accountId }, { replace: true })

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Segmented label="Periode" value={period} onChange={setPeriod} options={periods.map((x) => ({ value: x.value, label: x.label }))} />
        {can(me, 'project.create') && (
          <Button variant="secondary" onClick={() => setEditing(true)}>
            Ubah RAB
          </Button>
        )}
      </div>
      {!r && <Skeleton className="h-60" />}
      {r && (
        <TableShell minWidth={860}>
          <thead className="bg-hover/60">
            <tr>
              <th className={th}>Akun</th>
              <th className={`${th} text-right`}>RAB</th>
              <th className={`${th} text-right`}>Aktual</th>
              <th className={`${th} text-right`}>Sisa RAB</th>
              <th className={`${th} w-[180px]`}>Terpakai</th>
            </tr>
          </thead>
          <tbody>
            <SectionRow label="Pendapatan" />
            {r.revenue.map((row) => (
              <ReportLine key={row.accountId} row={row} revenue onOpen={openLines} />
            ))}
            {r.revenue.length === 0 && <EmptyRow text="Belum ada pendapatan pada periode ini." />}
            <TotalRow label="Total pendapatan" actual={r.totalRevenue} />
            <SectionRow label="Biaya" />
            {r.costs.map((row) => (
              <ReportLine key={row.accountId} row={row} onOpen={openLines} />
            ))}
            {r.costs.length === 0 && <EmptyRow text="Belum ada RAB atau biaya." />}
            <TotalRow label="Total biaya" budget={r.totalBudget} actual={r.totalCost} pct={r.costPct} />
            <tr className="border-t-2 border-line-2 text-base font-semibold">
              <td className={td}>Laba proyek</td>
              <td className={td} />
              <td className={`${td} num text-right ${toUnits(r.profit) < 0n ? 'text-bad' : 'text-good'}`}>{formatAmount(r.profit)}</td>
              <td className={`${td} text-right text-sm font-normal text-muted`} colSpan={2}>
                {r.margin && `margin ${Number(r.margin).toLocaleString('id-ID')}%`}
              </td>
            </tr>
          </tbody>
        </TableShell>
      )}
      <BudgetDialog me={me} p={p} open={editing} onClose={() => setEditing(false)} />
    </>
  )
}

function SectionRow({ label }: { label: string }) {
  return (
    <tr className="border-t border-line bg-canvas/60">
      <td colSpan={5} className="px-4 py-2 text-[12.5px] font-semibold tracking-wide text-muted uppercase">
        {label}
      </td>
    </tr>
  )
}

function EmptyRow({ text }: { text: string }) {
  return (
    <tr className="border-t border-line">
      <td colSpan={5} className={`${td} text-muted`}>
        {text}
      </td>
    </tr>
  )
}

function ReportLine({ row, revenue, onOpen }: { row: ProjectReportRow; revenue?: boolean; onOpen: (id: string) => void }) {
  const over = !revenue && toUnits(row.remaining) < 0n
  return (
    <tr onClick={() => onOpen(row.accountId)} className="cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover">
      <td className={td}>
        <span className="num font-semibold text-ink-2">{row.code}</span> {row.name}
      </td>
      <td className={`${td} num text-right`}>{revenue ? '' : formatAmount(row.budget, { blankZero: true }) || '—'}</td>
      <td className={`${td} num text-right`}>{formatAmount(row.actual)}</td>
      <td className={`${td} num text-right ${over ? 'font-semibold text-bad' : ''}`}>{revenue || toUnits(row.budget) === 0n ? '' : formatAmount(row.remaining)}</td>
      <td className={td}>{!revenue && row.usedPct && <PctBar pct={row.usedPct} />}</td>
    </tr>
  )
}

function TotalRow({ label, budget, actual, pct }: { label: string; budget?: string; actual: string; pct?: string }) {
  return (
    <tr className="border-t border-line-2 font-semibold">
      <td className={td}>{label}</td>
      <td className={`${td} num text-right`}>{budget ? formatAmount(budget) : ''}</td>
      <td className={`${td} num text-right`}>{formatAmount(actual)}</td>
      <td className={`${td} num text-right`}>{budget && toUnits(budget) > 0n ? formatAmount(toUnits(budget) - toUnits(actual)) : ''}</td>
      <td className={td}>{pct && <PctBar pct={pct} />}</td>
    </tr>
  )
}

function PctBar({ pct }: { pct: string }) {
  const used = Number(pct)
  const tone = used > 100 ? 'bg-bad' : used >= 90 ? 'bg-warn' : 'bg-good'
  return (
    <span className="flex items-center gap-2">
      <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-hover" aria-hidden="true">
        <span className={`block h-full rounded-full ${tone}`} style={{ width: `${Math.min(used, 100)}%` }} />
      </span>
      <span className={`num w-12 text-right text-[12px] ${used > 100 ? 'font-semibold text-bad' : 'text-muted'}`}>{used.toLocaleString('id-ID', { maximumFractionDigits: 1 })}%</span>
    </span>
  )
}

type BudgetRow = { key: string; accountId: string; description: string; amount: string }

function BudgetDialog({ me, p, open, onClose }: { me: Me; p: Detail; open: boolean; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const accounts = useAccounts(companyId)
  const save = useSaveBudget(companyId)
  const toast = useToast()
  const [rows, setRows] = useState<BudgetRow[]>([])
  const [shown, setShown] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const cost = useMemo(() => (accounts.data?.items ?? []).filter((a) => [5, 6, 8, 9].includes(a.class)), [accounts.data])

  if (open && !shown) {
    setShown(true)
    setRows(
      p.budgetLines.length > 0
        ? p.budgetLines.map((l) => ({ key: newIdempotencyKey(), accountId: l.accountId, description: l.description, amount: formatAmount(l.amount) }))
        : [{ key: newIdempotencyKey(), accountId: '', description: '', amount: '' }],
    )
    save.reset()
  }
  const err = save.error instanceof ApiError ? save.error : null
  const total = rows.reduce((s, r) => s + (parseInput(r.amount) ?? 0n), 0n)
  const set = (key: string, patch: Partial<BudgetRow>) => setRows((list) => list.map((r) => (r.key === key ? { ...r, ...patch } : r)))

  function close() {
    setShown(false)
    onClose()
  }

  function submit() {
    setAttempt((n) => n + 1)
    const lines = rows.filter((r) => r.accountId || r.amount).map((r) => ({ accountId: r.accountId, description: r.description.trim(), amount: fromUnits(parseInput(r.amount) ?? 0n) }))
    save.mutate({ id: p.id, lines }, { onSuccess: () => (toast('RAB disimpan'), close()) })
  }

  return (
    <Dialog
      open={open}
      onClose={close}
      width={760}
      title={`RAB ${p.code}`}
      description="Satu pos = satu akun biaya, sehingga biaya aktualnya terbaca langsung dari buku besar."
      footer={
        <>
          <span className="num mr-auto self-center text-sm text-muted">Total RAB {formatAmount(total)}</span>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button busy={save.isPending} onClick={submit}>
            Simpan RAB
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-2">
        {rows.map((r, i) => (
          <div key={r.key} className="grid grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_150px_40px] items-start gap-2 max-sm:grid-cols-1">
            <div>
              <AccountPicker id={`rab-${r.key}`} label={`Akun pos ${i + 1}`} accounts={cost} value={r.accountId} onChange={(a) => set(r.key, { accountId: a })} invalid={Boolean(err?.fields[`lines.${i}.accountId`])} />
              {err?.fields[`lines.${i}.accountId`] && <p className="mt-1 text-[13px] text-bad">{err.fields[`lines.${i}.accountId`]}</p>}
            </div>
            <input aria-label={`Uraian pos ${i + 1}`} placeholder="Uraian" value={r.description} maxLength={200} onChange={(e) => set(r.key, { description: e.target.value })} className={`${inputClass} border-line-2`} />
            <input
              aria-label={`Nilai pos ${i + 1}`}
              inputMode="decimal"
              placeholder="0"
              value={r.amount}
              onChange={(e) => set(r.key, { amount: e.target.value })}
              onBlur={(e) => {
                const u = parseInput(e.target.value)
                if (u !== null && e.target.value) set(r.key, { amount: formatAmount(u) })
              }}
              className={`${inputClass} num text-right ${err?.fields[`lines.${i}.amount`] || parseInput(r.amount) === null ? 'border-bad' : 'border-line-2'}`}
            />
            <button
              type="button"
              aria-label={`Hapus pos ${i + 1}`}
              onClick={() => setRows((list) => list.filter((x) => x.key !== r.key))}
              className="flex size-11 cursor-pointer items-center justify-center rounded-[10px] text-muted hover:bg-hover hover:text-bad"
            >
              ×
            </button>
          </div>
        ))}
        <Button size="sm" variant="ghost" className="self-start" onClick={() => setRows((list) => [...list, { key: newIdempotencyKey(), accountId: '', description: '', amount: '' }])}>
          + Tambah pos
        </Button>
      </div>
      {err && <Alert shakeKey={attempt}>{err.message}</Alert>}
    </Dialog>
  )
}

function LinesTab({ me, p }: { me: Me; p: Detail }) {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const account = params.get('akun') ?? ''
  const [months, setMonths] = useState<'semua' | '3'>('semua')
  const f = months === '3' ? { from: addMonths(monthOf(todayISO()), -2), to: monthOf(todayISO()) } : {}
  const report = useProjectReport(me.activeCompanyId, p.id, {})
  const lines = useProjectLines(me.activeCompanyId, p.id, { account: account || undefined, ...f })
  const options = [...(report.data?.revenue ?? []), ...(report.data?.costs ?? [])].filter((r) => toUnits(r.actual) !== 0n)
  const items = lines.data?.items ?? []

  return (
    <>
      <div className="flex flex-wrap items-end gap-3">
        <SelectField
          id="rincian-akun"
          label="Akun"
          value={account}
          onChange={(e) => {
            const next = new URLSearchParams(params)
            if (e.target.value) next.set('akun', e.target.value)
            else next.delete('akun')
            setParams(next, { replace: true })
          }}
          className="min-w-[280px]"
        >
          <option value="">Semua akun</option>
          {options.map((o) => (
            <option key={o.accountId} value={o.accountId}>
              {o.code} {o.name}
            </option>
          ))}
        </SelectField>
        <Segmented
          label="Rentang"
          value={months}
          onChange={setMonths}
          options={[
            { value: 'semua', label: 'Sepanjang proyek' },
            { value: '3', label: '3 bulan terakhir' },
          ]}
        />
      </div>
      <TableShell minWidth={860}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Tanggal</th>
            <th className={th}>Jurnal</th>
            <th className={th}>Akun</th>
            <th className={th}>Keterangan</th>
            <th className={`${th} text-right`}>Debit</th>
            <th className={`${th} text-right`}>Kredit</th>
          </tr>
        </thead>
        <tbody>
          {lines.isPending && (
            <tr>
              <td colSpan={6} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {items.map((l, i) => (
            <tr
              key={`${l.entryId}-${i}`}
              onClick={() => navigate(`/jurnal?bulan=${monthOf(l.date)}&id=${l.entryId}`)}
              className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${l.status === 'void' ? 'text-muted' : ''}`}
            >
              <td className={`${td} num whitespace-nowrap`}>{formatDate(l.date)}</td>
              <td className={td}>
                <span className={`num font-semibold ${l.status === 'void' ? 'line-through' : 'text-accent'}`}>{l.number}</span>
                {l.sourceType !== 'manual' && <span className="block text-[12px] text-muted">{sourceLabel(l.sourceType)}</span>}
              </td>
              <td className={td}>
                <span className="num text-ink-2">{l.accountCode}</span> {l.accountName}
              </td>
              <td className={`${td} max-w-[280px] truncate text-ink-2`}>{l.memo}</td>
              <td className={`${td} num text-right`}>{formatAmount(l.debit, { blankZero: true })}</td>
              <td className={`${td} num text-right`}>{formatAmount(l.credit, { blankZero: true })}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {lines.isSuccess && items.length === 0 && <EmptyState title="Belum ada jurnal berproyek" body="Pilih proyek di baris tagihan, kas, jurnal, atau di kepala faktur supaya biaya dan pendapatannya masuk ke sini." />}
      {lines.data?.truncated && <Alert tone="warn">Hanya 2.000 baris pertama yang ditampilkan; pilih satu akun untuk mempersempit.</Alert>}
    </>
  )
}

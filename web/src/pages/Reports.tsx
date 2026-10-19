import { useState, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import type { Me } from '../lib/api'
import { useContacts } from '../lib/cash'
import { allowedBranches, isRestricted, useDimensions } from '../lib/dimensions'
import { addDays, addMonths, daysBetween, formatDate, lastYear, monthEnd, monthOf, monthStart, todayISO } from '../lib/dates'
import { useLedgerSettings } from '../lib/ledger'
import { formatAmount, toUnits } from '../lib/money'
import {
  exportReport,
  useBalanceSheet,
  useCashFlow,
  useContactCard,
  useProfitLoss,
  useSummary,
  useTrialBalance,
  type ReportColumn,
  type ReportLine,
  type ReportName,
  type Statement,
  type SummaryKind,
} from '../lib/reports'
import { activeCompany, can } from '../lib/session'
import { formatQty } from '../lib/inventory'
import { SearchSelect } from '../components/SearchSelect'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, EmptyState, Field, PageHeader, Segmented, SelectField, Skeleton, TableShell, Tabs, td, th } from '../components/ui'

type Tab = 'neraca' | 'laba-rugi' | 'arus-kas' | 'neraca-saldo' | 'ringkasan' | 'kartu'

const tabs: { value: Tab; label: string }[] = [
  { value: 'neraca', label: 'Neraca' },
  { value: 'laba-rugi', label: 'Laba rugi' },
  { value: 'arus-kas', label: 'Arus kas' },
  { value: 'neraca-saldo', label: 'Neraca saldo' },
  { value: 'ringkasan', label: 'Penjualan dan pembelian' },
  { value: 'kartu', label: 'Kartu kontak' },
]

/** Laporan keuangan (RPT-01–RPT-05): semua angka dihitung dari buku besar dan bisa ditelusuri
 *  sampai jurnalnya. Ekspor Excel/CSV dan cetak ke PDF dari browser. */
export function Reports({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams()
  // Pengguna yang dibatasi ke cabang tertentu (PLT-12) hanya melihat laba rugi cabangnya.
  const restricted = isRestricted(me)
  const tab = restricted ? 'laba-rugi' : ((params.get('tab') as Tab) ?? 'neraca')
  return (
    <div className="flex flex-col gap-5">
      <div className="print:hidden">
        <PageHeader
          title="Laporan"
          subtitle={
            restricted
              ? 'Akses Anda dibatasi ke cabang tertentu, sehingga laporan yang tersedia adalah laba rugi cabang Anda.'
              : 'Dihitung langsung dari buku besar, sehingga angka yang sama muncul di semua laporan. Klik baris akun untuk membuka buku besarnya.'
          }
        />
      </div>
      {!restricted && (
        <div className="print:hidden">
          <Tabs label="Jenis laporan" value={tab} onChange={(v) => setParams(v === 'neraca' ? {} : { tab: v }, { replace: true })} tabs={tabs} />
        </div>
      )}
      {tab === 'neraca' && <BalanceSheetTab me={me} />}
      {tab === 'laba-rugi' && <ProfitLossTab me={me} />}
      {tab === 'arus-kas' && <CashFlowTab me={me} />}
      {tab === 'neraca-saldo' && <TrialBalanceTab me={me} />}
      {tab === 'ringkasan' && <SummaryTab me={me} />}
      {tab === 'kartu' && <CardTab me={me} />}
    </div>
  )
}

// Periode ---------------------------------------------------------------------------------------

type Preset = 'bulan' | 'bulan-lalu' | 'tahun' | 'kustom'
type Range = { from: string; to: string }

function useFiscalStart(me: Me): (date: string) => string {
  const settings = useLedgerSettings(me.activeCompanyId)
  const start = settings.data?.fiscalYearStartMonth ?? 1
  return (date: string) => {
    const [y, m] = date.split('-').map(Number)
    const year = m >= start ? y : y - 1
    return `${year}-${String(start).padStart(2, '0')}-01`
  }
}

function presetRange(p: Preset, fiscal: (d: string) => string, custom: Range): Range {
  const today = todayISO()
  const m = monthOf(today)
  switch (p) {
    case 'bulan':
      return { from: monthStart(m), to: today }
    case 'bulan-lalu':
      return { from: monthStart(addMonths(m, -1)), to: monthEnd(addMonths(m, -1)) }
    case 'tahun':
      return { from: fiscal(today), to: today }
    default:
      return custom
  }
}

type Compare = '' | 'sebelumnya' | 'tahun-lalu'

function compareRange(r: Range, c: Compare): Range | null {
  if (!c) return null
  const sameMonth = monthOf(r.from) === monthOf(r.to)
  // Tahun lalu, atau rentang beberapa bulan dari awal bulan (mis. tahun buku berjalan): periode
  // yang sama setahun sebelumnya.
  if (c === 'tahun-lalu' || (!sameMonth && r.from.endsWith('-01'))) return { from: lastYear(r.from), to: lastYear(r.to) }
  // Satu bulan (penuh atau s.d. hari ini): bulan sebelumnya sepanjang hari yang sama.
  if (sameMonth && r.from.endsWith('-01')) {
    const prev = addMonths(monthOf(r.from), -1)
    if (r.to === monthEnd(monthOf(r.to))) return { from: monthStart(prev), to: monthEnd(prev) }
    const end = addDays(monthStart(prev), daysBetween(r.from, r.to))
    return { from: monthStart(prev), to: end > monthEnd(prev) ? monthEnd(prev) : end }
  }
  const len = daysBetween(r.from, r.to)
  return { from: addDays(r.from, -len - 1), to: addDays(r.from, -1) }
}

function PeriodPicker({ preset, onPreset, custom, onCustom, allowYear = true }: { preset: Preset; onPreset: (p: Preset) => void; custom: Range; onCustom: (r: Range) => void; allowYear?: boolean }) {
  return (
    <div className="flex flex-wrap items-end gap-3">
      <Segmented
        label="Periode"
        value={preset}
        onChange={onPreset}
        options={[
          { value: 'bulan', label: 'Bulan ini' },
          { value: 'bulan-lalu', label: 'Bulan lalu' },
          ...(allowYear ? [{ value: 'tahun' as const, label: 'Tahun buku berjalan' }] : []),
          { value: 'kustom', label: 'Pilih tanggal' },
        ]}
      />
      {preset === 'kustom' && (
        <>
          <Field id="periode-dari" label="Dari" type="date" value={custom.from} max={custom.to} onChange={(e) => onCustom({ ...custom, from: e.target.value })} />
          <Field id="periode-sampai" label="Sampai" type="date" value={custom.to} min={custom.from} onChange={(e) => onCustom({ ...custom, to: e.target.value })} />
        </>
      )}
    </div>
  )
}

function rangeText(r: Range): string {
  return r.from === r.to ? formatDate(r.from) : `${formatDate(r.from)} – ${formatDate(r.to)}`
}

// Kerangka laporan: judul cetak, tombol ekspor, dan isi ---------------------------------------

function ReportFrame({ me, title, period, name, params, controls, children }: { me: Me; title: string; period: string; name: ReportName; params: Record<string, string | undefined>; controls: ReactNode; children: ReactNode }) {
  const toast = useToast()
  const [busy, setBusy] = useState<'' | 'xlsx' | 'csv'>('')
  const [error, setError] = useState<string | null>(null)
  const canExport = can(me, 'data.export')

  async function run(format: 'xlsx' | 'csv') {
    setBusy(format)
    setError(null)
    try {
      const file = await exportReport(name, format, params)
      toast(`${file} diunduh`)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Ekspor gagal.')
    } finally {
      setBusy('')
    }
  }

  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-4 print:hidden">
        {controls}
        <div className="flex flex-wrap gap-2">
          {canExport && (
            <>
              <Button variant="secondary" size="sm" busy={busy === 'xlsx'} disabled={busy !== ''} onClick={() => void run('xlsx')}>
                Excel
              </Button>
              <Button variant="secondary" size="sm" busy={busy === 'csv'} disabled={busy !== ''} onClick={() => void run('csv')}>
                CSV
              </Button>
            </>
          )}
          <Button variant="secondary" size="sm" onClick={() => window.print()}>
            Cetak / PDF
          </Button>
        </div>
      </div>
      {error && <Alert>{error}</Alert>}
      <div className="hidden print:block">
        <p className="text-sm">{activeCompany(me)?.name}</p>
        <h1 className="text-xl font-bold">{title}</h1>
        <p className="text-sm">{period}</p>
      </div>
      {children}
    </>
  )
}

// Laporan bertingkat (neraca, laba rugi, arus kas) ----------------------------------------------

const rowStyle: Record<ReportLine['kind'], string> = {
  section: 'bg-canvas/70 text-[12.5px] font-semibold tracking-wide text-muted uppercase',
  group: 'font-medium text-ink-2',
  account: '',
  computed: 'text-ink-2',
  subtotal: 'font-semibold',
  total: 'border-t border-line-2 font-semibold',
  result: 'border-t-2 border-line-2 bg-hover/40 text-[15px] font-bold',
}

export function StatementTable({ st, diff, onAccount }: { st: Statement; diff?: boolean; onAccount?: (id: string, column: number) => void }) {
  const showDiff = diff && st.columns.length === 2
  // Kolom per cabang/departemen: klik nama akun membuka kolom Jumlah; klik angka membuka kolomnya.
  const perDim = st.columns.some((c) => c.branch !== st.columns[0].branch || c.department !== st.columns[0].department)
  const nameColumn = perDim ? st.columns.length - 1 : 0
  return (
    <TableShell minWidth={st.columns.length > 1 ? 760 : 560}>
      <thead className="bg-hover/60">
        <tr>
          <th className={th}>Pos</th>
          {st.columns.map((c) => (
            <th key={c.label} className={`${th} text-right whitespace-nowrap`}>
              {c.label}
            </th>
          ))}
          {showDiff && <th className={`${th} text-right`}>Perubahan</th>}
        </tr>
      </thead>
      <tbody>
        {st.lines.map((l, i) => {
          const indent = l.kind === 'section' ? 0 : l.kind === 'computed' ? 1 : l.kind === 'total' || l.kind === 'result' ? 0 : l.depth + (l.kind === 'subtotal' ? 0 : 1)
          const clickable = l.kind === 'account' && l.accountId && onAccount
          const values = l.values ?? []
          const change = showDiff && values.length === 2 ? toUnits(values[0]) - toUnits(values[1]) : null
          return (
            <tr
              key={i}
              onClick={clickable ? () => onAccount(l.accountId!, nameColumn) : undefined}
              className={`border-t border-line ${rowStyle[l.kind]} ${clickable ? 'cursor-pointer transition-colors duration-150 hover:bg-hover' : ''}`}
            >
              <td className={`${td} ${l.kind === 'section' ? 'py-2' : ''}`} style={{ paddingLeft: 16 + indent * 18 }}>
                {l.kind === 'account' && <span className="num mr-2 text-[12.5px] text-muted">{l.code}</span>}
                {l.name}
              </td>
              {st.columns.map((col, c) => (
                <td
                  key={c}
                  title={clickable ? `Buku besar ${l.code} · ${col.label}` : undefined}
                  onClick={
                    clickable
                      ? (e) => {
                          e.stopPropagation()
                          onAccount(l.accountId!, c)
                        }
                      : undefined
                  }
                  className={`${td} num text-right whitespace-nowrap ${values[c] && toUnits(values[c]) < 0n ? 'text-bad' : ''} ${clickable ? 'hover:text-accent hover:underline' : ''}`}
                >
                  {values[c] !== undefined ? formatAmount(values[c], { parens: true }) : ''}
                </td>
              ))}
              {showDiff && <td className={`${td} num text-right whitespace-nowrap text-muted`}>{change !== null ? formatAmount(change, { parens: true }) : ''}</td>}
            </tr>
          )
        })}
      </tbody>
    </TableShell>
  )
}

/** Tautan ke buku besar akun untuk periode dan saringan cabang/departemen satu kolom laporan. */
function useLedgerLink(columns: ReportColumn[] | undefined) {
  const navigate = useNavigate()
  return (accountId: string, column: number) => {
    const c = columns?.[column]
    if (!c) return
    const dims = `${c.branch ? `&cabang=${c.branch}` : ''}${c.department ? `&departemen=${c.department}` : ''}`
    navigate(`/buku-besar?akun=${accountId}&dari=${c.from}&sampai=${c.to}${dims}`)
  }
}

function BalanceSheetTab({ me }: { me: Me }) {
  const [date, setDate] = useState(todayISO())
  const [compare, setCompare] = useState<'' | 'bulan' | 'tahun' | 'kustom'>('bulan')
  const [customCompare, setCustomCompare] = useState(() => monthEnd(addMonths(monthOf(todayISO()), -1)))
  const fiscal = useFiscalStart(me)
  const compareDate =
    compare === 'bulan' ? monthEnd(addMonths(monthOf(date), -1)) : compare === 'tahun' ? addDays(fiscal(date), -1) : compare === 'kustom' ? customCompare : undefined
  const params = { date, compare: compareDate }
  const report = useBalanceSheet(me.activeCompanyId, params)
  const st = report.data
  const onAccount = useLedgerLink(st?.columns)
  return (
    <ReportFrame
      me={me}
      title="Laporan posisi keuangan"
      period={`Per ${formatDate(date)}${compareDate ? ` dibanding ${formatDate(compareDate)}` : ''}`}
      name="balance-sheet"
      params={params}
      controls={
        <div className="flex flex-wrap items-end gap-3">
          <Field id="neraca-tanggal" label="Per tanggal" type="date" value={date} onChange={(e) => setDate(e.target.value || todayISO())} />
          <SelectField id="neraca-banding" label="Dibanding" value={compare} onChange={(e) => setCompare(e.target.value as typeof compare)}>
            <option value="">Tanpa pembanding</option>
            <option value="bulan">Akhir bulan lalu</option>
            <option value="tahun">Akhir tahun buku lalu</option>
            <option value="kustom">Tanggal lain…</option>
          </SelectField>
          {compare === 'kustom' && <Field id="neraca-banding-tanggal" label="Tanggal pembanding" type="date" value={customCompare} onChange={(e) => setCustomCompare(e.target.value)} />}
        </div>
      }
    >
      {!st && <Skeleton className="h-96" />}
      {st?.balanced?.some((b) => !b) && <Alert>Neraca tidak seimbang. Jalankan pemeriksaan saldo di menu Audit, atau hubungi dukungan Lajur.</Alert>}
      {st && <StatementTable st={st} diff onAccount={(id) => onAccount(id, 0)} />}
    </ReportFrame>
  )
}

function ProfitLossTab({ me }: { me: Me }) {
  const fiscal = useFiscalStart(me)
  const [preset, setPreset] = useState<Preset>('bulan')
  const [custom, setCustom] = useState<Range>({ from: monthStart(monthOf(todayISO())), to: todayISO() })
  const [compare, setCompare] = useState<Compare>('sebelumnya')
  const [branch, setBranch] = useState('')
  const [department, setDepartment] = useState('')
  const branches = allowedBranches(me, useDimensions(me.activeCompanyId, 'cabang').data ?? [])
  const departments = (useDimensions(me.activeCompanyId, 'departemen').data ?? []).filter((d) => d.active)
  const restricted = isRestricted(me)
  const perBranch = branch === 'per'
  const perDept = department === 'per'
  const per = perBranch || perDept
  const range = presetRange(preset, fiscal, custom)
  const cmp = per ? null : compareRange(range, compare)
  const params = {
    from: range.from,
    to: range.to,
    compareFrom: cmp?.from,
    compareTo: cmp?.to,
    branch: perBranch ? undefined : branch || undefined,
    department: perDept ? undefined : department || undefined,
    per: perBranch ? 'cabang' : perDept ? 'departemen' : undefined,
  }
  const report = useProfitLoss(me.activeCompanyId, params)
  const st = report.data
  const onAccount = useLedgerLink(st?.columns)
  const dimText = [
    perBranch ? 'per cabang' : branch && branches.find((b) => b.id === branch)?.name,
    perDept ? 'per departemen' : department && `departemen ${departments.find((d) => d.id === department)?.name ?? ''}`,
  ].filter(Boolean)
  return (
    <ReportFrame
      me={me}
      title="Laporan laba rugi"
      period={`${rangeText(range)}${cmp ? ` dibanding ${rangeText(cmp)}` : ''}${dimText.length ? ` · ${dimText.join(', ')}` : ''}`}
      name="profit-loss"
      params={params}
      controls={
        <div className="flex flex-wrap items-end gap-3">
          <PeriodPicker preset={preset} onPreset={setPreset} custom={custom} onCustom={setCustom} />
          {!per && (
            <SelectField id="lr-banding" label="Dibanding" value={compare} onChange={(e) => setCompare(e.target.value as Compare)}>
              <option value="">Tanpa pembanding</option>
              <option value="sebelumnya">Periode sebelumnya</option>
              <option value="tahun-lalu">Periode sama tahun lalu</option>
            </SelectField>
          )}
          {branches.length > 0 && (
            <SelectField id="lr-cabang" label="Cabang" value={branch} onChange={(e) => setBranch(e.target.value)}>
              <option value="">{restricted ? 'Semua cabang saya' : 'Semua cabang'}</option>
              <option value="per" disabled={perDept}>
                Per cabang (kolom)
              </option>
              {branches.map((b) => (
                <option key={b.id} value={b.id}>
                  {b.code} · {b.name}
                </option>
              ))}
            </SelectField>
          )}
          {departments.length > 0 && (
            <SelectField id="lr-departemen" label="Departemen" value={department} onChange={(e) => setDepartment(e.target.value)}>
              <option value="">Semua departemen</option>
              <option value="per" disabled={perBranch}>
                Per departemen (kolom)
              </option>
              {departments.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.code} · {d.name}
                </option>
              ))}
            </SelectField>
          )}
        </div>
      }
    >
      {!st && <Skeleton className="h-96" />}
      {st && <StatementTable st={st} diff={!per} onAccount={restricted ? undefined : onAccount} />}
      {st && (branch || department) && (
        <p className="text-[13px] text-muted print:hidden">
          {per
            ? `Jurnal tanpa ${perDept ? 'departemen' : 'cabang'}, mis. penyusutan atau jurnal umum tanpa ${perDept ? 'departemen' : 'cabang'}, dikumpulkan di kolom Tanpa ${perDept ? 'departemen' : 'cabang'}. Klik angka untuk melihat rincian jurnalnya di buku besar.`
            : 'Hanya jurnal dengan cabang atau departemen sesuai filter yang dihitung; jurnal tanpa dimensi, mis. penyusutan atau jurnal umum, tidak ikut. Klik angka untuk melihat rincian jurnalnya di buku besar.'}
        </p>
      )}
    </ReportFrame>
  )
}

function CashFlowTab({ me }: { me: Me }) {
  const fiscal = useFiscalStart(me)
  const [preset, setPreset] = useState<Preset>('bulan')
  const [custom, setCustom] = useState<Range>({ from: monthStart(monthOf(todayISO())), to: todayISO() })
  const range = presetRange(preset, fiscal, custom)
  const report = useCashFlow(me.activeCompanyId, range)
  const cf = report.data
  return (
    <ReportFrame
      me={me}
      title="Laporan arus kas (metode tidak langsung)"
      period={rangeText(range)}
      name="cash-flow"
      params={range}
      controls={<PeriodPicker preset={preset} onPreset={setPreset} custom={custom} onCustom={setCustom} />}
    >
      {!cf && <Skeleton className="h-96" />}
      {cf?.noCash && (
        <Alert tone="warn">
          Belum ada rekening kas atau bank yang terdaftar, sehingga kas awal dan akhir nol. Daftarkan di menu{' '}
          <Link to="/kas" className="font-semibold underline">
            Kas dan bank
          </Link>
          .
        </Alert>
      )}
      {cf && !cf.reconciled && <Alert>Arus kas tidak cocok dengan saldo kas. Hubungi dukungan Lajur.</Alert>}
      {cf && <StatementTable st={cf} />}
      {cf && (
        <p className="text-[13px] text-muted print:hidden">
          Dimulai dari laba bersih, lalu setiap perubahan akun neraca selain kas dibalik: aset lancar dan liabilitas jangka pendek masuk aktivitas operasi, aset tidak lancar ke investasi, liabilitas jangka
          panjang dan ekuitas ke pendanaan. Akun akumulasi penyusutan ditambahkan kembali sebagai pos nonkas.
        </p>
      )}
    </ReportFrame>
  )
}

function TrialBalanceTab({ me }: { me: Me }) {
  const [date, setDate] = useState(todayISO())
  const report = useTrialBalance(me.activeCompanyId, { date })
  const tb = report.data
  const navigate = useNavigate()
  return (
    <ReportFrame
      me={me}
      title="Neraca saldo"
      period={`Per ${formatDate(date)}`}
      name="trial-balance"
      params={{ date }}
      controls={<Field id="ns-tanggal" label="Per tanggal" type="date" value={date} onChange={(e) => setDate(e.target.value || todayISO())} />}
    >
      {!tb && <Skeleton className="h-96" />}
      {tb && (
        <>
          <p className="text-[13px] text-muted">
            Akun neraca dihitung sejak awal; akun laba rugi sejak awal tahun buku ({formatDate(tb.fiscalStart)}).{' '}
            <Badge tone={tb.balanced ? 'good' : 'bad'}>{tb.balanced ? 'Seimbang' : 'Tidak seimbang'}</Badge>
          </p>
          <TableShell minWidth={640}>
            <thead className="bg-hover/60">
              <tr>
                <th className={th}>Akun</th>
                <th className={`${th} text-right`}>Debit</th>
                <th className={`${th} text-right`}>Kredit</th>
              </tr>
            </thead>
            <tbody>
              {tb.rows.map((r, i) => (
                <tr
                  key={r.accountId ?? `x${i}`}
                  onClick={r.accountId ? () => navigate(`/buku-besar?akun=${r.accountId}&dari=${r.class >= 4 ? tb.fiscalStart : '2000-01-01'}&sampai=${tb.date}`) : undefined}
                  className={`border-t border-line ${r.accountId ? 'cursor-pointer transition-colors duration-150 hover:bg-hover' : 'text-ink-2'}`}
                >
                  <td className={td}>
                    <span className="num mr-2 text-[12.5px] text-muted">{r.code}</span>
                    {r.name}
                  </td>
                  <td className={`${td} num text-right`}>{formatAmount(r.debit, { blankZero: true })}</td>
                  <td className={`${td} num text-right`}>{formatAmount(r.credit, { blankZero: true })}</td>
                </tr>
              ))}
              <tr className="border-t-2 border-line-2 font-semibold">
                <td className={td}>Jumlah</td>
                <td className={`${td} num text-right`}>{formatAmount(tb.totalDebit)}</td>
                <td className={`${td} num text-right`}>{formatAmount(tb.totalCredit)}</td>
              </tr>
            </tbody>
          </TableShell>
        </>
      )}
    </ReportFrame>
  )
}

function SummaryTab({ me }: { me: Me }) {
  const fiscal = useFiscalStart(me)
  const [kind, setKind] = useState<SummaryKind>('customer')
  const [preset, setPreset] = useState<Preset>('bulan')
  const [custom, setCustom] = useState<Range>({ from: monthStart(monthOf(todayISO())), to: todayISO() })
  const range = presetRange(preset, fiscal, custom)
  const params = { kind, ...range }
  const report = useSummary(me.activeCompanyId, params)
  const s = report.data && report.data.kind === kind ? report.data : undefined
  const product = kind === 'product'
  const supplier = kind === 'supplier'
  return (
    <ReportFrame
      me={me}
      title={s?.title ?? 'Ringkasan'}
      period={rangeText(range)}
      name="summary"
      params={params}
      controls={
        <div className="flex flex-wrap items-end gap-3">
          <Segmented
            label="Ringkasan"
            value={kind}
            onChange={setKind}
            options={[
              { value: 'customer', label: 'Per pelanggan' },
              { value: 'product', label: 'Per produk' },
              { value: 'supplier', label: 'Pembelian per pemasok' },
            ]}
          />
          <PeriodPicker preset={preset} onPreset={setPreset} custom={custom} onCustom={setCustom} />
        </div>
      }
    >
      {!s && <Skeleton className="h-60" />}
      {s && (
        <TableShell minWidth={760}>
          <thead className="bg-hover/60">
            <tr>
              <th className={th}>{product ? 'Produk' : supplier ? 'Pemasok' : 'Pelanggan'}</th>
              {product && <th className={`${th} text-right`}>Kuantitas</th>}
              <th className={`${th} text-right`}>{supplier ? 'Tagihan' : 'Faktur'}</th>
              <th className={`${th} text-right`}>DPP</th>
              <th className={`${th} text-right`}>PPN</th>
              {supplier && <th className={`${th} text-right`}>PPh dipotong</th>}
              {!product && <th className={`${th} text-right`}>Total</th>}
            </tr>
          </thead>
          <tbody>
            {s.rows.map((r, i) => (
              <tr key={r.id ?? `x${i}`} className="border-t border-line">
                <td className={td}>
                  {r.code && <span className="num mr-2 text-[12.5px] text-muted">{r.code}</span>}
                  {r.name}
                </td>
                {product && <td className={`${td} num text-right whitespace-nowrap`}>{r.qty ? `${formatQty(r.qty)} ${r.unit ?? ''}` : '—'}</td>}
                <td className={`${td} num text-right`}>{r.documents}</td>
                <td className={`${td} num text-right`}>{formatAmount(r.dpp)}</td>
                <td className={`${td} num text-right`}>{formatAmount(r.vat)}</td>
                {supplier && <td className={`${td} num text-right`}>{formatAmount(r.withholding, { blankZero: true })}</td>}
                {!product && <td className={`${td} num text-right`}>{formatAmount(r.total)}</td>}
              </tr>
            ))}
            <tr className="border-t-2 border-line-2 font-semibold">
              <td className={td}>Jumlah</td>
              {product && <td className={td} />}
              <td className={`${td} num text-right`}>{product ? '' : s.total.documents}</td>
              <td className={`${td} num text-right`}>{formatAmount(s.total.dpp)}</td>
              <td className={`${td} num text-right`}>{formatAmount(s.total.vat)}</td>
              {supplier && <td className={`${td} num text-right`}>{formatAmount(s.total.withholding, { blankZero: true })}</td>}
              {!product && <td className={`${td} num text-right`}>{formatAmount(s.total.total)}</td>}
            </tr>
          </tbody>
        </TableShell>
      )}
      {s && s.rows.length === 0 && <EmptyState title="Belum ada dokumen yang diposting pada periode ini" />}
    </ReportFrame>
  )
}

function CardTab({ me }: { me: Me }) {
  const fiscal = useFiscalStart(me)
  const navigate = useNavigate()
  const [side, setSide] = useState<'customer' | 'supplier'>('customer')
  const [contact, setContact] = useState('')
  const [preset, setPreset] = useState<Preset>('tahun')
  const [custom, setCustom] = useState<Range>({ from: monthStart(monthOf(todayISO())), to: todayISO() })
  const contacts = useContacts(me.activeCompanyId, { role: side, inactive: true })
  const range = presetRange(preset, fiscal, custom)
  const params = { side, contact, ...range }
  const report = useContactCard(me.activeCompanyId, params)
  const card = report.data && report.data.contactId === contact && report.data.side === side ? report.data : undefined
  const base = side === 'customer' ? '/penjualan' : '/pembelian'
  const docLink = (kind: string, id: string, date: string) =>
    kind === 'faktur' || kind === 'tagihan'
      ? `${base}?bulan=${monthOf(date)}&id=${id}`
      : kind === 'nota_kredit' || kind === 'nota_debit'
        ? `${base}?tab=retur&nid=${id}`
        : `${base}?tab=${side === 'customer' ? 'pelunasan' : 'pembayaran'}&pid=${id}`
  return (
    <ReportFrame
      me={me}
      title={side === 'customer' ? 'Kartu piutang pelanggan' : 'Kartu utang pemasok'}
      period={`${card?.contactName ?? ''} · ${rangeText(range)}`}
      name="contact-card"
      params={params}
      controls={
        <div className="flex flex-wrap items-end gap-3">
          <Segmented
            label="Jenis kartu"
            value={side}
            onChange={(v) => {
              setSide(v)
              setContact('')
            }}
            options={[
              { value: 'customer', label: 'Pelanggan' },
              { value: 'supplier', label: 'Pemasok' },
            ]}
          />
          <div className="flex w-[280px] flex-col gap-1.5">
            <label htmlFor="kartu-kontak" className="text-sm font-medium text-ink-2">
              {side === 'customer' ? 'Pelanggan' : 'Pemasok'}
            </label>
            <SearchSelect id="kartu-kontak" options={(contacts.data ?? []).map((c) => ({ id: c.id, label: c.name }))} value={contact} onChange={setContact} placeholder="Cari kontak" />
          </div>
          <PeriodPicker preset={preset} onPreset={setPreset} custom={custom} onCustom={setCustom} />
        </div>
      }
    >
      {!contact && <EmptyState title="Pilih kontak" body="Kartu menampilkan setiap faktur atau tagihan dan pelunasannya, dengan saldo berjalan." />}
      {contact && !card && <Skeleton className="h-60" />}
      {card && (
        <TableShell minWidth={820}>
          <thead className="bg-hover/60">
            <tr>
              <th className={th}>Tanggal</th>
              <th className={th}>Dokumen</th>
              <th className={th}>Keterangan</th>
              <th className={`${th} text-right`}>Debit</th>
              <th className={`${th} text-right`}>Kredit</th>
              <th className={`${th} text-right`}>Saldo</th>
            </tr>
          </thead>
          <tbody>
            <tr className="border-t border-line text-ink-2">
              <td className={`${td} num`}>{formatDate(card.from)}</td>
              <td className={td} colSpan={4}>
                Saldo awal
              </td>
              <td className={`${td} num text-right`}>{formatAmount(card.opening)}</td>
            </tr>
            {card.lines.map((l) => (
              <tr key={`${l.kind}-${l.docId}`} onClick={() => navigate(docLink(l.kind, l.docId, l.date))} className="cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover">
                <td className={`${td} num whitespace-nowrap`}>{formatDate(l.date)}</td>
                <td className={td}>
                  <span className="num font-semibold text-accent">{l.number}</span>
                  <span className="block text-[12px] text-muted capitalize">{l.kind.replace('_', ' ')}</span>
                </td>
                <td className={`${td} max-w-[280px] truncate text-ink-2`}>
                  {l.memo || '—'}
                  {toUnits(l.withholding) > 0n && <span className="block text-[12px] text-muted">PPh {formatAmount(l.withholding)}</span>}
                </td>
                <td className={`${td} num text-right`}>{formatAmount(l.debit, { blankZero: true })}</td>
                <td className={`${td} num text-right`}>{formatAmount(l.credit, { blankZero: true })}</td>
                <td className={`${td} num text-right`}>{formatAmount(l.balance)}</td>
              </tr>
            ))}
            <tr className="border-t-2 border-line-2 font-semibold">
              <td className={`${td} num`}>{formatDate(card.to)}</td>
              <td className={td} colSpan={2}>
                Saldo akhir
              </td>
              <td className={`${td} num text-right`}>{formatAmount(card.totalDebit)}</td>
              <td className={`${td} num text-right`}>{formatAmount(card.totalCredit)}</td>
              <td className={`${td} num text-right`}>{formatAmount(card.closing)}</td>
            </tr>
          </tbody>
        </TableShell>
      )}
    </ReportFrame>
  )
}

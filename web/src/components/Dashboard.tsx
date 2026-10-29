import { animate, motion, useReducedMotion } from 'motion/react'
import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import type { Me } from '../lib/api'
import { formatDate, monthLabel } from '../lib/dates'
import { formatAmount, toUnits } from '../lib/money'
import { useDashboard, type Exposure } from '../lib/reports'
import { can } from '../lib/session'
import { Badge, Card, Skeleton, ease } from './ui'

const icon = {
  cash: 'M3 7a2 2 0 0 1 2-2h13a1 1 0 0 1 1 1v3M3 7v11a2 2 0 0 0 2 2h14a1 1 0 0 0 1-1v-4M3 7l0 0M21 11h-4a2 2 0 0 0 0 4h4z',
  in: 'M17 7L7 17M7 9v8h8',
  out: 'M7 17L17 7M9 7h8v8',
  profit: 'M3 17l6-6 4 4 8-8M14 7h7v7',
}

/** Dashboard (RPT-07): kas dan bank, piutang dan utang jatuh tempo, laba bulan ini dibanding
 *  bulan lalu, tren enam bulan, dan faktur yang paling lama lewat jatuh tempo. */
export function Dashboard({ me }: { me: Me }) {
  const d = useDashboard(me.activeCompanyId, can(me, 'report.view'))
  if (d.isPending)
    return (
      <div className="grid grid-cols-[repeat(auto-fit,minmax(230px,1fr))] gap-4">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-36 rounded-2xl" />
        ))}
      </div>
    )
  if (d.isError) return null
  const x = d.data
  const profit = toUnits(x.thisMonth.profit)
  const last = toUnits(x.lastMonth.profit)
  const delta = last !== 0n ? Number(((profit - last) * 1000n) / (last < 0n ? -last : last)) / 10 : null

  return (
    <div className="flex flex-col gap-5">
      <div className="grid grid-cols-[repeat(auto-fit,minmax(230px,1fr))] gap-4">
        <Kpi index={0} label="Kas dan bank" value={x.cashTotal} to={x.cashDrafts > 0 ? '/kas?draf=1' : '/kas'} icon={icon.cash} tint="teal">
          {x.cashDrafts > 0 && (
            <Badge tone="warn">
              {x.cashDrafts} draf belum diposting · {formatAmount(x.draftTotal)}
            </Badge>
          )}
          <ul className="flex flex-col gap-1 text-[12.5px] text-muted">
            {x.cash.slice(0, 3).map((a) => (
              <li key={a.accountId} className="flex justify-between gap-2">
                <span className="truncate">{a.name}</span>
                <span className="num text-ink-2">{formatAmount(a.balance)}</span>
              </li>
            ))}
          </ul>
        </Kpi>
        <Kpi index={1} label="Piutang belum lunas" value={x.receivable.total} to="/penjualan?tab=umur" icon={icon.in} tint="accent">
          <Due e={x.receivable} />
        </Kpi>
        <Kpi index={2} label="Utang belum dibayar" value={x.payable.total} to="/pembelian?tab=umur" icon={icon.out} tint="warn">
          <Due e={x.payable} />
        </Kpi>
        <Kpi
          index={3}
          label={`Laba ${monthLabel(x.today.slice(0, 7))}`}
          value={x.thisMonth.profit}
          to="/laporan?tab=laba-rugi"
          tone={profit < 0n ? 'bad' : undefined}
          icon={icon.profit}
          tint={profit < 0n ? 'bad' : 'good'}
        >
          <span className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-[12.5px] text-muted">
            {delta !== null && (
              <span className={`num rounded-full px-2 py-0.5 text-[12px] font-semibold ${delta >= 0 ? 'bg-good-soft text-good' : 'bg-bad-soft text-bad'}`}>
                {delta >= 0 ? '▲' : '▼'} {Math.abs(delta).toLocaleString('id-ID')}%
              </span>
            )}
            <span>
              vs bulan lalu <span className="num">{formatAmount(x.lastMonth.profit)}</span> · s.d. {formatDate(x.today)}
            </span>
          </span>
        </Kpi>
      </div>
      <div className="flex flex-wrap gap-5">
        <motion.div
          className="min-w-0 flex-[2_1_440px]"
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.4, ease, delay: 0.2 }}
        >
          <Card title="Pendapatan dan beban 6 bulan" subtitle="Dari buku besar, tanpa jurnal penutup. Arahkan kursor ke bulan untuk rinciannya." className="h-full">
            <Trend data={x.trend} />
          </Card>
        </motion.div>
        <motion.div
          className="min-w-0 flex-[1_1_300px]"
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.4, ease, delay: 0.28 }}
        >
          <Card
            title="Faktur lewat jatuh tempo"
            subtitle={x.overdue.length ? 'Yang paling lama lebih dulu.' : undefined}
            className="h-full"
            actions={
              x.overdue.length > 0 && (
                <Link to="/penjualan?tab=umur" className="text-[13px] font-semibold text-accent hover:underline">
                  Umur piutang
                </Link>
              )
            }
          >
            {x.overdue.length === 0 ? (
              <div className="flex flex-1 flex-col items-center justify-center gap-2 py-8 text-center">
                <span className="flex size-11 items-center justify-center rounded-full bg-good-soft text-good">
                  <svg width="22" height="22" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M5 12l5 5L20 7" />
                  </svg>
                </span>
                <p className="text-sm font-semibold">Semua faktur tepat waktu</p>
                <p className="text-[13px] text-muted">Tidak ada faktur yang lewat jatuh tempo.</p>
              </div>
            ) : (
              <ul className="flex flex-col">
                {x.overdue.map((inv, i) => (
                  <motion.li
                    key={inv.id}
                    className="border-b border-line last:border-0"
                    initial={{ opacity: 0, x: 8 }}
                    animate={{ opacity: 1, x: 0 }}
                    transition={{ duration: 0.3, ease, delay: 0.35 + i * 0.05 }}
                  >
                    <Link to={`/penjualan?bulan=${inv.date.slice(0, 7)}&id=${inv.id}`} className="-mx-2 flex items-center gap-3 rounded-xl px-2 py-2.5 text-sm transition-colors hover:bg-hover">
                      <span aria-hidden="true" className="flex size-9 shrink-0 items-center justify-center rounded-full bg-accent-soft text-[12px] font-bold text-accent">
                        {initials(inv.customerName)}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-semibold">{inv.customerName}</span>
                        <span className="num block text-[12.5px] text-muted">{inv.number}</span>
                      </span>
                      <span className="shrink-0 text-right">
                        <span className="num block font-semibold">{formatAmount(inv.outstanding)}</span>
                        <Badge tone="bad">{inv.daysOverdue} hari</Badge>
                      </span>
                    </Link>
                  </motion.li>
                ))}
              </ul>
            )}
          </Card>
        </motion.div>
      </div>
    </div>
  )
}

/** Inisial nama untuk avatar, mis. "PT Contoh Mandiri" → "CM" (tanpa PT/CV). */
function initials(name: string) {
  const words = name
    .replace(/^(PT|CV|UD|Toko)\.?\s+/i, '')
    .split(/\s+/)
    .filter(Boolean)
  return ((words[0]?.[0] ?? '') + (words[1]?.[0] ?? '')).toUpperCase() || '?'
}

const tints = {
  teal: 'bg-teal/12 text-teal',
  accent: 'bg-accent-soft text-accent',
  warn: 'bg-warn-soft text-warn',
  good: 'bg-good-soft text-good',
  bad: 'bg-bad-soft text-bad',
}

function Kpi({
  index,
  label,
  value,
  to,
  tone,
  icon: path,
  tint,
  children,
}: {
  index: number
  label: string
  value: string
  to: string
  tone?: 'bad'
  icon: string
  tint: keyof typeof tints
  children?: ReactNode
}) {
  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.4, ease, delay: index * 0.06 }}>
      <Link
        to={to}
        className="group flex h-full flex-col gap-2 rounded-2xl border border-line bg-surface p-5 shadow-card transition-all duration-200 ease-lajur hover:-translate-y-0.5 hover:border-line-2 hover:shadow-lift"
      >
        <span className="flex items-center gap-2.5">
          <span className={`flex size-9 items-center justify-center rounded-xl ${tints[tint]}`}>
            <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
              <path d={path} />
            </svg>
          </span>
          <span className="text-[13px] font-medium text-muted">{label}</span>
        </span>
        <span className={`num text-[24px] leading-tight font-bold tracking-[-0.02em] ${tone === 'bad' ? 'text-bad' : ''}`}>
          Rp <CountUp value={value} />
        </span>
        {children}
        <span className="mt-auto flex items-center gap-1 pt-1 text-[12.5px] font-semibold text-accent opacity-0 transition-opacity duration-200 group-hover:opacity-100 group-focus-visible:opacity-100">
          Lihat rincian
          <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M5 12h14M13 6l6 6-6 6" />
          </svg>
        </span>
      </Link>
    </motion.div>
  )
}

/** Nominal yang menghitung naik saat pertama tampil; angka akhirnya selalu nilai persisnya. */
export function CountUp({ value }: { value: string }) {
  const reduce = useReducedMotion()
  const target = Number(toUnits(value)) / 10000
  const [shown, setShown] = useState<number | null>(reduce ? null : 0)
  useEffect(() => {
    if (reduce) return
    const controls = animate(0, target, { duration: 0.8, ease: [0.2, 0, 0, 1], onUpdate: setShown, onComplete: () => setShown(null) })
    return () => controls.stop()
  }, [target, reduce])
  if (shown === null) return <>{formatAmount(value)}</>
  const n = Math.round(shown)
  return <>{(n < 0 ? '−' : '') + Math.abs(n).toLocaleString('id-ID')}</>
}

function Due({ e }: { e: Exposure }) {
  return (
    <span className="flex flex-wrap gap-1.5 text-[12.5px]">
      {e.overdueCount > 0 ? (
        <Badge tone="bad">
          {e.overdueCount} lewat tempo · {formatAmount(e.overdue)}
        </Badge>
      ) : (
        <span className="text-muted">Tidak ada yang lewat tempo</span>
      )}
      {e.dueSoonCount > 0 && (
        <Badge tone="warn">
          {e.dueSoonCount} jatuh tempo 7 hari · {formatAmount(e.dueSoon)}
        </Badge>
      )}
    </span>
  )
}

const shortMonth = ['Jan', 'Feb', 'Mar', 'Apr', 'Mei', 'Jun', 'Jul', 'Agu', 'Sep', 'Okt', 'Nov', 'Des']

type Month = { month: string; revenue: string; expense: string; profit: string }

/** Batang pendapatan dan beban per bulan dengan garis laba; tooltip saat kursor di atas bulan,
 *  tabel tersembunyi untuk pembaca layar. */
function Trend({ data }: { data: Month[] }) {
  const reduce = useReducedMotion()
  const [active, setActive] = useState<number | null>(null)
  const W = 640
  const H = 220
  const top = 12
  const bottom = 28
  const left = 52
  const plot = H - top - bottom
  const num = (v: string) => Number(toUnits(v)) / 10000
  const values = data.flatMap((m) => [num(m.revenue), num(m.expense), num(m.profit)])
  const hi = niceCeil(Math.max(1, ...values))
  const lo = Math.min(0, ...values.map((v) => v)) < 0 ? -niceCeil(-Math.min(...values)) : 0
  const y = (v: number) => top + ((hi - v) / (hi - lo)) * plot
  const step = (W - left) / data.length
  const cx = (i: number) => left + step * i + step / 2
  const barW = Math.min(18, step / 4)
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => lo + (hi - lo) * f)
  const points = data.map((m, i) => [cx(i), y(num(m.profit))] as const)
  const line = points.map(([px, py], i) => `${i === 0 ? 'M' : 'L'}${px.toFixed(1)} ${py.toFixed(1)}`).join(' ')
  const a = active !== null ? data[active] : null

  return (
    <div>
      <div className="flex flex-wrap items-center gap-4 pb-2 text-[12.5px] text-muted" aria-hidden="true">
        <span className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-[3px] bg-accent" /> Pendapatan
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-[3px] bg-slate" /> Beban
        </span>
        <span className="flex items-center gap-1.5">
          <span className="h-[3px] w-3.5 rounded-full bg-teal" /> Laba
        </span>
      </div>
      <div className="relative" onMouseLeave={() => setActive(null)} aria-hidden="true">
        <svg viewBox={`0 0 ${W} ${H}`} className="block w-full overflow-visible">
          <defs>
            <linearGradient id="dash-rev" x1="0" x2="0" y1="0" y2="1">
              <stop offset="0" stopColor="var(--color-sky)" />
              <stop offset="1" stopColor="var(--color-accent)" />
            </linearGradient>
          </defs>
          {ticks.map((t, i) => (
            <g key={i}>
              <line x1={left} x2={W} y1={y(t)} y2={y(t)} stroke="var(--color-line)" strokeDasharray={t === 0 ? undefined : '3 5'} />
              <text x={left - 10} y={y(t) + 4} textAnchor="end" fontSize="11" fill="var(--color-muted)">
                {compactNumber(t)}
              </text>
            </g>
          ))}
          {data.map((m, i) => {
            const [, mm] = m.month.split('-').map(Number)
            const rev = Math.max(0, num(m.revenue))
            const exp = Math.max(0, num(m.expense))
            return (
              <g key={m.month}>
                {active === i && <rect x={cx(i) - step / 2 + 4} y={top - 4} width={step - 8} height={plot + 8} rx="10" fill="var(--color-hover)" />}
                <motion.rect
                  x={cx(i) - barW - 2}
                  width={barW}
                  rx="4"
                  fill="url(#dash-rev)"
                  initial={reduce ? false : { y: y(0), height: 0 }}
                  animate={{ y: y(rev), height: y(0) - y(rev) }}
                  transition={{ duration: 0.6, ease, delay: 0.2 + i * 0.05 }}
                />
                <motion.rect
                  x={cx(i) + 2}
                  width={barW}
                  rx="4"
                  fill="var(--color-slate)"
                  initial={reduce ? false : { y: y(0), height: 0 }}
                  animate={{ y: y(exp), height: y(0) - y(exp) }}
                  transition={{ duration: 0.6, ease, delay: 0.25 + i * 0.05 }}
                />
                <text x={cx(i)} y={H - 8} textAnchor="middle" fontSize="12" fontWeight={active === i ? 700 : 500} fill={active === i ? 'var(--color-ink)' : 'var(--color-muted)'}>
                  {shortMonth[mm - 1]}
                </text>
                <rect x={cx(i) - step / 2} y={0} width={step} height={H} fill="transparent" onMouseEnter={() => setActive(i)} />
              </g>
            )
          })}
          <motion.path
            d={line}
            fill="none"
            stroke="var(--color-teal)"
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeLinejoin="round"
            initial={reduce ? false : { pathLength: 0 }}
            animate={{ pathLength: 1 }}
            transition={{ duration: 0.8, ease: 'easeInOut', delay: 0.6 }}
            pointerEvents="none"
          />
          {points.map(([px, py], i) => (
            <motion.circle
              key={i}
              cx={px}
              cy={py}
              r={active === i ? 5.5 : 3.5}
              fill="var(--color-surface)"
              stroke="var(--color-teal)"
              strokeWidth="2.5"
              initial={reduce ? false : { opacity: 0 }}
              animate={{ opacity: 1 }}
              transition={{ duration: 0.2, delay: 0.7 + i * 0.1 }}
              pointerEvents="none"
            />
          ))}
        </svg>
        {a && active !== null && (
          <motion.div
            key={active}
            initial={{ opacity: 0, y: 4 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.15, ease }}
            className="pointer-events-none absolute top-0 z-10 w-[190px] -translate-x-1/2 rounded-xl border border-line bg-surface p-3 text-[12.5px] shadow-lift"
            style={{ left: `${Math.min(84, Math.max(16, (cx(active) / W) * 100))}%` }}
          >
            <p className="mb-1.5 font-semibold text-ink">{monthLabel(a.month)}</p>
            <TipRow color="bg-accent" label="Pendapatan" value={a.revenue} />
            <TipRow color="bg-slate" label="Beban" value={a.expense} />
            <div className="mt-1.5 border-t border-line pt-1.5">
              <TipRow color="bg-teal" label="Laba" value={a.profit} strong />
            </div>
          </motion.div>
        )}
      </div>
      <table className="sr-only">
        <caption>Pendapatan, beban, dan laba per bulan</caption>
        <thead>
          <tr>
            <th>Bulan</th>
            <th>Pendapatan</th>
            <th>Beban</th>
            <th>Laba</th>
          </tr>
        </thead>
        <tbody>
          {data.map((m) => (
            <tr key={m.month}>
              <td>{monthLabel(m.month)}</td>
              <td>{formatAmount(m.revenue)}</td>
              <td>{formatAmount(m.expense)}</td>
              <td>{formatAmount(m.profit)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function TipRow({ color, label, value, strong }: { color: string; label: string; value: string; strong?: boolean }) {
  const neg = toUnits(value) < 0n
  return (
    <p className="flex items-center justify-between gap-3 py-0.5">
      <span className="flex items-center gap-1.5 text-muted">
        <span className={`size-2 rounded-full ${color}`} />
        {label}
      </span>
      <span className={`num ${strong ? 'font-semibold' : ''} ${neg ? 'text-bad' : 'text-ink'}`}>{formatAmount(value)}</span>
    </p>
  )
}

/** Batas atas sumbu yang bulat: 1, 2, 2,5, 5 × 10ⁿ. */
function niceCeil(v: number) {
  const p = 10 ** Math.floor(Math.log10(v))
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p
  return 10 * p
}

/** Nominal ringkas untuk sumbu grafik: 1,2 M, 350 jt, 12 rb. */
function compactNumber(n: number): string {
  const abs = Math.abs(n)
  const fmt = (x: number) => x.toLocaleString('id-ID', { maximumFractionDigits: 1 })
  if (abs >= 1e9) return `${fmt(n / 1e9)} M`
  if (abs >= 1e6) return `${fmt(n / 1e6)} jt`
  if (abs >= 1e3) return `${fmt(n / 1e3)} rb`
  return fmt(n)
}

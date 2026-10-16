import { motion } from 'motion/react'
import { Link } from 'react-router'
import type { Me } from '../lib/api'
import { formatDate, monthLabel } from '../lib/dates'
import { formatAmount, toUnits } from '../lib/money'
import { useDashboard, type Exposure } from '../lib/reports'
import { can } from '../lib/session'
import { Badge, Card, Skeleton, ease } from './ui'

/** Dashboard (RPT-07): kas dan bank, piutang dan utang jatuh tempo, laba bulan ini dibanding
 *  bulan lalu, tren enam bulan, dan faktur yang paling lama lewat jatuh tempo. */
export function Dashboard({ me }: { me: Me }) {
  const d = useDashboard(me.activeCompanyId, can(me, 'report.view'))
  if (d.isPending) return <Skeleton className="h-64" />
  if (d.isError) return null
  const x = d.data
  const profit = toUnits(x.thisMonth.profit)
  const last = toUnits(x.lastMonth.profit)
  const delta = last !== 0n ? Number(((profit - last) * 1000n) / (last < 0n ? -last : last)) / 10 : null

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
        <Kpi index={0} label="Kas dan bank" value={x.cashTotal} to={x.cashDrafts > 0 ? '/kas?draf=1' : '/kas'}>
          {x.cashDrafts > 0 && (
            <Badge tone="warn">
              {x.cashDrafts} draf belum diposting · {formatAmount(x.draftTotal)}
            </Badge>
          )}
          <ul className="flex flex-col gap-0.5 text-[12.5px] text-muted">
            {x.cash.slice(0, 3).map((a) => (
              <li key={a.accountId} className="flex justify-between gap-2">
                <span className="truncate">{a.name}</span>
                <span className="num">{formatAmount(a.balance)}</span>
              </li>
            ))}
          </ul>
        </Kpi>
        <Kpi index={1} label="Piutang belum lunas" value={x.receivable.total} to="/penjualan?tab=umur">
          <Due e={x.receivable} />
        </Kpi>
        <Kpi index={2} label="Utang belum dibayar" value={x.payable.total} to="/pembelian?tab=umur">
          <Due e={x.payable} />
        </Kpi>
        <Kpi index={3} label={`Laba ${monthLabel(x.today.slice(0, 7))}`} value={x.thisMonth.profit} to="/laporan?tab=laba-rugi" tone={profit < 0n ? 'bad' : undefined}>
          <span className="text-[12.5px] text-muted">
            s.d. {formatDate(x.today)} · bulan lalu {formatAmount(x.lastMonth.profit)}
            {delta !== null && (
              <span className={`ml-1 font-semibold ${delta >= 0 ? 'text-good' : 'text-bad'}`}>
                {delta >= 0 ? '▲' : '▼'} {Math.abs(delta).toLocaleString('id-ID')}%
              </span>
            )}
          </span>
        </Kpi>
      </div>
      <div className="flex flex-wrap gap-4">
        <div className="min-w-0 flex-[2_1_420px]">
          <Card title="Pendapatan dan beban 6 bulan" subtitle="Dari buku besar, tanpa jurnal penutup.">
            <Trend data={x.trend} />
          </Card>
        </div>
        <div className="min-w-0 flex-[1_1_300px]">
          <Card title="Faktur lewat jatuh tempo" subtitle={x.overdue.length ? 'Yang paling lama lebih dulu.' : undefined}>
            {x.overdue.length === 0 ? (
              <p className="text-sm text-muted">Tidak ada faktur yang lewat jatuh tempo.</p>
            ) : (
              <ul className="flex flex-col">
                {x.overdue.map((inv) => (
                  <li key={inv.id} className="border-b border-line last:border-0">
                    <Link to={`/penjualan?bulan=${inv.date.slice(0, 7)}&id=${inv.id}`} className="flex items-center justify-between gap-3 py-2 text-sm hover:text-accent">
                      <span className="min-w-0">
                        <span className="num block font-semibold">{inv.number}</span>
                        <span className="block truncate text-[12.5px] text-muted">{inv.customerName}</span>
                      </span>
                      <span className="shrink-0 text-right">
                        <span className="num block">{formatAmount(inv.outstanding)}</span>
                        <Badge tone="bad">{inv.daysOverdue} hari</Badge>
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>
      </div>
    </div>
  )
}

function Kpi({ index, label, value, to, tone, children }: { index: number; label: string; value: string; to: string; tone?: 'bad'; children?: React.ReactNode }) {
  return (
    <motion.div initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.25, ease, delay: index * 0.04 }}>
      <Link to={to} className="flex h-full flex-col gap-1.5 rounded-[14px] border border-line bg-surface p-4 transition-colors duration-150 hover:border-line-2 hover:bg-hover/40">
        <span className="text-[13px] text-muted">{label}</span>
        <span className={`num text-[22px] font-semibold tracking-tight ${tone === 'bad' ? 'text-bad' : ''}`}>Rp {formatAmount(value)}</span>
        {children}
      </Link>
    </motion.div>
  )
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

/** Batang pendapatan dan beban per bulan; tabel tersembunyi untuk pembaca layar. */
function Trend({ data }: { data: { month: string; revenue: string; expense: string; profit: string }[] }) {
  const values = data.flatMap((m) => [toUnits(m.revenue), toUnits(m.expense)])
  const peak = values.reduce((a, b) => (b > a ? b : a), 1n)
  const height = (v: string) => {
    const u = toUnits(v)
    return u <= 0n ? 0 : Math.max(2, Number((u * 1000n) / peak) / 10)
  }
  return (
    <div>
      <div className="flex items-center gap-4 pb-3 text-[12.5px] text-muted" aria-hidden="true">
        <span className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-sm bg-accent" /> Pendapatan
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-sm bg-line-2" /> Beban
        </span>
      </div>
      <div className="flex h-44 items-end gap-3" aria-hidden="true">
        {data.map((m, i) => {
          const [, mm] = m.month.split('-').map(Number)
          const profit = toUnits(m.profit)
          return (
            <div key={m.month} className="flex min-w-0 flex-1 flex-col items-center gap-1.5">
              <div className="flex h-36 w-full items-end justify-center gap-1">
                <motion.span
                  className="w-[38%] max-w-6 rounded-t-[4px] bg-accent"
                  initial={{ height: 0 }}
                  animate={{ height: `${height(m.revenue)}%` }}
                  transition={{ duration: 0.4, ease, delay: i * 0.03 }}
                  title={`Pendapatan ${formatAmount(m.revenue)}`}
                />
                <motion.span
                  className="w-[38%] max-w-6 rounded-t-[4px] bg-line-2"
                  initial={{ height: 0 }}
                  animate={{ height: `${height(m.expense)}%` }}
                  transition={{ duration: 0.4, ease, delay: i * 0.03 + 0.02 }}
                  title={`Beban ${formatAmount(m.expense)}`}
                />
              </div>
              <span className="text-[12px] text-muted">{shortMonth[mm - 1]}</span>
              <span className={`num text-[11.5px] ${profit < 0n ? 'text-bad' : 'text-ink-2'}`}>{compact(m.profit)}</span>
            </div>
          )
        })}
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

/** Nominal ringkas untuk label grafik: 1,2 M, 350 jt, 12 rb. */
function compact(v: string): string {
  const n = Number(toUnits(v) / 10000n)
  const abs = Math.abs(n)
  const fmt = (x: number) => x.toLocaleString('id-ID', { maximumFractionDigits: 1 })
  if (abs >= 1e9) return `${fmt(n / 1e9)} M`
  if (abs >= 1e6) return `${fmt(n / 1e6)} jt`
  if (abs >= 1e3) return `${fmt(n / 1e3)} rb`
  return fmt(n)
}

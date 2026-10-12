import { useNavigate, useSearchParams } from 'react-router'
import type { Me, Pair, WorksheetRow } from '../lib/api'
import { formatDate, monthLabel, monthOf, todayISO } from '../lib/dates'
import { useWorksheet } from '../lib/ledger'
import { formatAmount, toUnits } from '../lib/money'
import { MonthNav } from '../components/MonthNav'
import { Badge, EmptyState, PageHeader, Skeleton } from '../components/ui'

const groups = [
  { key: 'unadjusted', label: 'Neraca saldo' },
  { key: 'adjustment', label: 'Penyesuaian' },
  { key: 'adjusted', label: 'NS disesuaikan' },
  { key: 'profitLoss', label: 'Laba rugi' },
  { key: 'balanceSheet', label: 'Neraca' },
] as const

type GroupKey = (typeof groups)[number]['key']

const cell = 'num px-3 py-2 text-right whitespace-nowrap'
const sticky = 'sticky left-0 z-[1] bg-surface'

export function Worksheet({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const ws = useWorksheet(me.activeCompanyId, month)
  const navigate = useNavigate()
  const w = ws.data

  const net = w ? toUnits(w.netIncome) : 0n

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Neraca lajur"
        aside={w && (w.balanced ? <Badge tone="good">Seimbang</Badge> : <Badge tone="bad">Tidak seimbang</Badge>)}
        subtitle={
          w
            ? `Saldo per ${formatDate(w.monthEnd)}. Kolom penyesuaian berisi jurnal penyesuaian bertanggal ${monthLabel(month)}; laba rugi dihitung sejak ${formatDate(w.fiscalStart)}.`
            : 'Neraca saldo, penyesuaian, neraca saldo disesuaikan, laba rugi, dan neraca.'
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <MonthNav value={month} onChange={(m) => setParams({ bulan: m }, { replace: true })} />
      </div>

      {ws.isPending && <Skeleton className="h-72" />}
      {ws.isError && <p className="text-sm text-bad">{ws.error.message}</p>}
      {w && w.rows.length === 0 && <EmptyState title={`Belum ada saldo sampai ${monthLabel(month)}`} body="Neraca lajur terisi setelah ada jurnal yang diposting." />}
      {w && w.rows.length > 0 && (
        <div className={`overflow-hidden rounded-[14px] border border-line bg-surface transition-opacity duration-150 ${ws.isPlaceholderData ? 'opacity-60' : ''}`}>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[1180px] border-separate border-spacing-0 text-left text-[13px]">
              <thead className="text-[12px] text-muted">
                <tr>
                  <th rowSpan={2} className={`${sticky} min-w-[260px] border-b border-line bg-hover px-4 py-2.5 align-bottom font-semibold`}>
                    Akun
                  </th>
                  {groups.map((g) => (
                    <th key={g.key} colSpan={2} className="border-b border-l border-line bg-hover px-3 py-2 text-center font-semibold text-ink-2">
                      {g.label}
                    </th>
                  ))}
                </tr>
                <tr>
                  {groups.map((g) => (
                    <Sides key={g.key} />
                  ))}
                </tr>
              </thead>
              <tbody>
                {w.rows.map((r) => (
                  <Row key={r.accountId ?? r.code} row={r} onOpen={r.accountId ? () => navigate(`/buku-besar?akun=${r.accountId}&dari=${w.fiscalStart}&sampai=${w.monthEnd}`) : undefined} />
                ))}
                <TotalRow label="Jumlah" values={w.totals} />
                <tr className="text-ink-2">
                  <td className={`${sticky} border-t border-line px-4 py-2 font-semibold`}>{net >= 0n ? 'Laba bersih' : 'Rugi bersih'}</td>
                  {groups.map((g) => {
                    const pl = g.key === 'profitLoss'
                    const bs = g.key === 'balanceSheet'
                    const amount = net >= 0n ? w.netIncome : w.netIncome.slice(1)
                    return (
                      <Pairs
                        key={g.key}
                        pair={{
                          debit: (pl && net >= 0n) || (bs && net < 0n) ? amount : '0',
                          credit: (pl && net < 0n) || (bs && net >= 0n) ? amount : '0',
                        }}
                        className="border-t border-line font-semibold"
                      />
                    )
                  })}
                </tr>
                <TotalRow label="Jumlah akhir" values={w.final} strong />
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}

function Sides() {
  return (
    <>
      <th className="border-b border-l border-line bg-hover px-3 py-1.5 text-right font-semibold">Debit</th>
      <th className="border-b border-line bg-hover px-3 py-1.5 text-right font-semibold">Kredit</th>
    </>
  )
}

function Pairs({ pair, className = '' }: { pair: Pair; className?: string }) {
  return (
    <>
      <td className={`${cell} border-l border-line ${className}`}>{formatAmount(pair.debit, { blankZero: true })}</td>
      <td className={`${cell} ${className}`}>{formatAmount(pair.credit, { blankZero: true })}</td>
    </>
  )
}

function Row({ row, onOpen }: { row: WorksheetRow; onOpen?: () => void }) {
  return (
    <tr onClick={onOpen} className={`group ${onOpen ? 'cursor-pointer' : ''}`}>
      <td className={`${sticky} border-t border-line px-4 py-2 transition-colors duration-150 group-hover:bg-hover`}>
        {row.accountId ? (
          <>
            <span className="num mr-2 font-semibold text-ink-2">{row.code}</span>
            {row.name}
          </>
        ) : (
          <span className="text-muted italic">{row.name}</span>
        )}
      </td>
      {groups.map((g) => (
        <Pairs key={g.key} pair={row[g.key as GroupKey]} className="border-t border-line transition-colors duration-150 group-hover:bg-hover" />
      ))}
    </tr>
  )
}

function TotalRow({ label, values, strong }: { label: string; values: Record<GroupKey, Pair>; strong?: boolean }) {
  const border = strong ? 'border-t border-b-[3px] border-b-double border-line-2' : 'border-t border-line-2'
  return (
    <tr className="font-semibold">
      <td className={`${sticky} ${border} px-4 py-2.5`}>{label}</td>
      {groups.map((g) => (
        <Pairs key={g.key} pair={values[g.key]} className={`${border} bg-hover/40`} />
      ))}
    </tr>
  )
}

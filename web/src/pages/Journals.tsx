import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import type { Kind, Me } from '../lib/api'
import { formatDate, monthEnd, monthLabel, monthOf, monthStart, todayISO } from '../lib/dates'
import { useDebounced } from '../lib/hooks'
import { useEntries } from '../lib/ledger'
import { kindLabel, kindTone, sourceLabel } from '../lib/labels'
import { formatAmount } from '../lib/money'
import { can } from '../lib/session'
import { JournalDrawer } from '../components/JournalDrawer'
import { MonthNav } from '../components/MonthNav'
import { RecurringList } from '../components/Recurring'
import { Badge, Button, EmptyState, PageHeader, Segmented, Skeleton, TableShell, Tabs, inputClass, td, th } from '../components/ui'

const kinds: { value: '' | Kind; label: string }[] = [
  { value: '', label: 'Semua' },
  { value: 'umum', label: 'Umum' },
  { value: 'penyesuaian', label: 'Penyesuaian' },
  { value: 'pembalik', label: 'Pembalik' },
]

export function Journals({ me }: { me: Me }) {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const kind = (params.get('jenis') ?? '') as '' | Kind
  const openId = params.get('id')
  const flashId = params.get('baru')
  const tab = params.get('tab') === 'berulang' ? 'berulang' : 'jurnal'
  const [search, setSearch] = useState(params.get('q') ?? '')
  const q = useDebounced(search.trim(), 250)

  const entries = useEntries(me.activeCompanyId, { from: monthStart(month), to: monthEnd(month), kind, q })
  const items = entries.data?.pages.flatMap((p) => p.items) ?? []

  function update(next: Record<string, string | null>) {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v)
      else p.delete(k)
    }
    setParams(p, { replace: true })
  }

  const canGeneral = can(me, 'ledger.journal.create')
  const canAdjust = can(me, 'ledger.adjustment.post')

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Jurnal"
        subtitle="Semua jurnal yang sudah diposting, termasuk pembalik otomatis dan pembatalan. Jurnal manual bisa diubah selama periodenya belum dikunci, dengan riwayat versi; selebihnya koreksi lewat void."
        actions={
          <>
            {canAdjust && (
              <Button variant="secondary" onClick={() => navigate('/jurnal/baru?jenis=penyesuaian')}>
                Jurnal penyesuaian
              </Button>
            )}
            {canGeneral && <Button onClick={() => navigate('/jurnal/baru?jenis=umum')}>Jurnal umum</Button>}
          </>
        }
      />

      <Tabs
        label="Daftar jurnal"
        value={tab}
        onChange={(t) => update({ tab: t === 'berulang' ? t : null, r: null, id: null })}
        tabs={[
          { value: 'jurnal', label: 'Jurnal' },
          { value: 'berulang', label: 'Berulang' },
        ]}
      />

      {tab === 'berulang' ? (
        <RecurringList me={me} openId={params.get('r')} onOpen={(r) => update({ r })} onOpenEntry={(id) => update({ tab: null, r: null, id })} />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <MonthNav value={month} onChange={(m) => update({ bulan: m, baru: null })} />
            <Segmented label="Jenis jurnal" value={kind} onChange={(k) => update({ jenis: k || null })} options={kinds} />
            <input
              type="search"
              aria-label="Cari nomor atau keterangan"
              placeholder="Cari nomor atau keterangan"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value)
                update({ q: e.target.value.trim() || null })
              }}
              className={`${inputClass} border-line-2 max-w-[280px] flex-1`}
            />
          </div>

          <TableShell minWidth={760}>
            <thead className="bg-hover/60">
              <tr>
                <th className={th}>Tanggal</th>
                <th className={th}>Nomor</th>
                <th className={th}>Jenis</th>
                <th className={th}>Keterangan</th>
                <th className={`${th} text-right`}>Total</th>
              </tr>
            </thead>
            <tbody>
              {entries.isPending &&
                [0, 1, 2, 3, 4].map((i) => (
                  <tr key={i} className="border-t border-line">
                    <td colSpan={5} className={td}>
                      <Skeleton className="h-5" />
                    </td>
                  </tr>
                ))}
              {items.map((e) => {
                const isVoid = e.status === 'void'
                return (
                  <tr
                    key={e.id}
                    onClick={() => update({ id: e.id })}
                    className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${e.id === flashId ? 'flash' : ''} ${isVoid ? 'text-muted' : ''}`}
                  >
                    <td className={`${td} num whitespace-nowrap`}>{formatDate(e.date)}</td>
                    <td className={td}>
                      <button
                        type="button"
                        onClick={(ev) => {
                          ev.stopPropagation()
                          update({ id: e.id })
                        }}
                        className={`num cursor-pointer font-semibold whitespace-nowrap ${isVoid ? 'line-through' : 'text-accent'} hover:underline`}
                      >
                        {e.number}
                      </button>
                    </td>
                    <td className={td}>
                      <span className="flex flex-wrap items-center gap-1.5">
                        <Badge tone={kindTone[e.kind]}>{kindLabel[e.kind]}</Badge>
                        {isVoid && <Badge tone="bad">Void</Badge>}
                        {sourceLabel(e.sourceType) && <span className="text-xs text-muted">{sourceLabel(e.sourceType)}</span>}
                      </span>
                    </td>
                    <td className={`${td} max-w-[320px] truncate`}>{e.memo || <span className="text-muted">—</span>}</td>
                    <td className={`${td} num text-right whitespace-nowrap ${isVoid ? 'line-through' : ''}`}>{formatAmount(e.total)}</td>
                  </tr>
                )
              })}
            </tbody>
          </TableShell>

          {entries.isSuccess && items.length === 0 && (
            <EmptyState
              title={q ? 'Tidak ada jurnal yang cocok' : `Belum ada jurnal di ${monthLabel(month)}`}
              body={q ? 'Coba kata kunci lain, atau pilih bulan lain.' : 'Jurnal dari modul lain nanti juga muncul di sini, tertaut ke dokumen sumbernya.'}
              action={canGeneral && !q && <Button onClick={() => navigate('/jurnal/baru?jenis=umum')}>Buat jurnal umum</Button>}
            />
          )}
          {entries.hasNextPage && (
            <div className="flex justify-center">
              <Button variant="secondary" busy={entries.isFetchingNextPage} onClick={() => entries.fetchNextPage()}>
                Muat lebih banyak
              </Button>
            </div>
          )}
          {entries.isError && <p className="text-sm text-bad">{entries.error.message}</p>}
        </>
      )}

      <JournalDrawer me={me} id={openId} onClose={() => update({ id: null })} onOpen={(id) => update({ id })} />
    </div>
  )
}

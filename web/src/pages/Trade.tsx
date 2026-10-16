import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import type { AgingBucket, Me } from '../lib/api'
import { formatDate, monthEnd, monthLabel, monthOf, monthStart, todayISO } from '../lib/dates'
import { useAging, useDocs, useNotes, useSettlements, type TradeKind } from '../lib/docs'
import { useDebounced } from '../lib/hooks'
import { docState, tradeText } from '../lib/labels'
import { formatAmount, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { isRestricted } from '../lib/dimensions'
import { MonthNav } from '../components/MonthNav'
import { DocDrawer, NoteDrawer, SettlementDrawer } from '../components/TradeDrawers'
import { DailySaleDrawer } from '../components/DailySaleDrawer'
import { useCashProjection } from '../lib/reports'
import { useDailySales } from '../lib/daily'
import { Alert, Badge, Button, Card, EmptyState, Field, PageHeader, Segmented, Skeleton, TableShell, Tabs, inputClass, td, th } from '../components/ui'

const states = [
  { value: '', label: 'Semua' },
  { value: 'draft', label: 'Draf' },
  { value: 'open', label: 'Belum lunas' },
  { value: 'paid', label: 'Lunas' },
  { value: 'void', label: 'Void' },
]

const buckets: { key: AgingBucket; label: string }[] = [
  { key: 'current', label: 'Belum jatuh tempo' },
  { key: '1-30', label: '1–30 hari' },
  { key: '31-60', label: '31–60 hari' },
  { key: '61-90', label: '61–90 hari' },
  { key: '90+', label: '> 90 hari' },
]

/** Penjualan atau pembelian: daftar dokumen, pelunasan/pembayaran, dan umur piutang/utang. */
export function TradePage({ me, kind }: { me: Me; kind: TradeKind }) {
  const t = tradeText[kind]
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? t.tabs.docs
  const docId = params.get('id')
  const settlementId = params.get('pid')
  const noteId = params.get('nid')
  const dailyId = params.get('hid')

  function update(next: Record<string, string | null>) {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v)
      else p.delete(k)
    }
    setParams(p, { replace: true })
  }

  const canCreate = can(me, t.perm.create)
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title={t.title}
        subtitle={
          kind === 'sales'
            ? 'Faktur memposting jurnal piutang, pendapatan, dan PPN keluaran. Pelunasan bisa sebagian, banyak faktur sekaligus, dan dengan PPh yang dipotong pelanggan.'
            : 'Tagihan memposting jurnal beban atau aset, PPN masukan, utang usaha, dan utang PPh yang dipotong. Pembayaran bisa sebagian atau banyak tagihan sekaligus.'
        }
        actions={
          canCreate && (
            <>
              <Button variant="secondary" onClick={() => navigate(`${t.base}/${t.tabs.settle}/baru`)}>
                {t.settleAction}
              </Button>
              <Button onClick={() => navigate(`${t.base}/baru`)}>{t.doc} baru</Button>
            </>
          )
        }
      />
      <Tabs
        label={t.title}
        value={tab}
        onChange={(v) => update({ tab: v === t.tabs.docs ? null : v })}
        tabs={[
          { value: t.tabs.docs, label: kind === 'sales' ? 'Faktur' : 'Tagihan' },
          { value: t.tabs.settle, label: t.settle },
          { value: t.tabs.notes, label: t.notes },
          ...(kind === 'sales' ? [{ value: 'harian', label: 'Penjualan harian' }] : []),
          { value: t.tabs.aging, label: t.aging },
        ]}
      />
      {tab === t.tabs.docs && <DocList me={me} kind={kind} onOpen={(id) => update({ id })} />}
      {tab === t.tabs.settle && <SettlementList me={me} kind={kind} onOpen={(id) => update({ pid: id })} />}
      {tab === t.tabs.notes && <NoteList me={me} kind={kind} onOpen={(id) => update({ nid: id })} />}
      {tab === 'harian' && kind === 'sales' && <DailyList me={me} onOpen={(id) => update({ hid: id })} />}
      {tab === t.tabs.aging && <AgingTable me={me} kind={kind} onContact={(id) => update({ tab: null, kontak: id, status: 'open' })} />}

      <DocDrawer
        me={me}
        kind={kind}
        id={docId}
        onClose={() => update({ id: null })}
        onOpenSettlement={(id) => update({ id: null, pid: id })}
        onOpenNote={(id) => update({ id: null, nid: id })}
      />
      <SettlementDrawer me={me} kind={kind} id={settlementId} onClose={() => update({ pid: null })} onOpenDoc={(id) => update({ pid: null, id })} />
      <NoteDrawer me={me} kind={kind} id={noteId} onClose={() => update({ nid: null })} onOpenDoc={(id) => update({ nid: null, id })} />
      {kind === 'sales' && <DailySaleDrawer me={me} id={dailyId} onClose={() => update({ hid: null })} />}
    </div>
  )
}

function DocList({ me, kind, onOpen }: { me: Me; kind: TradeKind; onOpen: (id: string) => void }) {
  const t = tradeText[kind]
  const [params, setParams] = useSearchParams()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const state = params.get('status') ?? ''
  const contactId = params.get('kontak') ?? undefined
  const [search, setSearch] = useState('')
  const q = useDebounced(search.trim())
  // Filter kontak (dari umur piutang) menampilkan semua bulan.
  const range = contactId ? { from: '2000-01-01', to: '2100-12-31' } : { from: monthStart(month), to: monthEnd(month) }
  const docs = useDocs(kind, me.activeCompanyId, { ...range, state, q, contactId })
  const flash = params.get('baru')

  function set(k: string, v: string | null) {
    const p = new URLSearchParams(params)
    if (v) p.set(k, v)
    else p.delete(k)
    setParams(p, { replace: true })
  }

  const items = docs.data ?? []
  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        {!contactId && <MonthNav value={month} onChange={(m) => set('bulan', m)} />}
        {contactId && (
          <Badge tone="accent">
            {items[0]?.contactName ?? t.contact} · semua bulan
            <button type="button" aria-label="Hapus filter kontak" onClick={() => set('kontak', null)} className="ml-1 cursor-pointer font-bold">
              ×
            </button>
          </Badge>
        )}
        <Segmented label="Status" value={state} onChange={(v) => set('status', v || null)} options={states} />
        <input type="search" aria-label="Cari nomor atau catatan" placeholder="Cari nomor atau catatan" value={search} onChange={(e) => setSearch(e.target.value)} className={`${inputClass} max-w-[260px] flex-1 border-line-2`} />
      </div>
      <TableShell minWidth={860}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Tanggal</th>
            <th className={th}>Nomor</th>
            <th className={th}>{t.contact}</th>
            <th className={th}>Jatuh tempo</th>
            <th className={`${th} text-right`}>Total</th>
            <th className={`${th} text-right`}>Sisa</th>
            <th className={th}>Status</th>
          </tr>
        </thead>
        <tbody>
          {docs.isPending &&
            [0, 1, 2, 3].map((i) => (
              <tr key={i} className="border-t border-line">
                <td colSpan={7} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            ))}
          {items.map((d) => (
            <tr key={d.id} onClick={() => onOpen(d.id)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${d.id === flash ? 'flash' : ''} ${d.state === 'void' ? 'text-muted' : ''}`}>
              <td className={`${td} num whitespace-nowrap`}>{formatDate(d.date)}</td>
              <td className={td}>
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation()
                    onOpen(d.id)
                  }}
                  className={`num cursor-pointer font-semibold whitespace-nowrap hover:underline ${d.number ? (d.state === 'void' ? 'line-through' : 'text-accent') : 'text-muted italic'}`}
                >
                  {d.number ?? 'Draf'}
                </button>
                {d.supplierInvoiceNo && <span className="block text-xs text-muted">{d.supplierInvoiceNo}</span>}
              </td>
              <td className={`${td} max-w-[240px] truncate`}>{d.contactName}</td>
              <td className={`${td} num whitespace-nowrap`}>
                {formatDate(d.dueDate)}
                {d.daysOverdue > 0 && <span className="ml-2 text-xs font-semibold text-bad">+{d.daysOverdue} hr</span>}
              </td>
              <td className={`${td} num text-right whitespace-nowrap`}>{formatAmount(d.total)}</td>
              <td className={`${td} num text-right whitespace-nowrap`}>{d.status === 'posted' ? formatAmount(d.outstanding, { blankZero: true }) || '—' : ''}</td>
              <td className={td}>
                <Badge tone={docState[d.state].tone}>{docState[d.state].label}</Badge>
              </td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {docs.isSuccess && items.length === 0 && (
        <EmptyState
          title={contactId ? `Tidak ada ${t.doc_} untuk kontak ini` : `Belum ada ${t.doc_} di ${monthLabel(month)}`}
          body={state ? 'Coba status lain.' : `${t.doc} draf belum masuk buku besar; jurnal baru dibuat saat diposting.`}
        />
      )}
    </>
  )
}

function SettlementList({ me, kind, onOpen }: { me: Me; kind: TradeKind; onOpen: (id: string) => void }) {
  const t = tradeText[kind]
  const [params, setParams] = useSearchParams()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const list = useSettlements(kind, me.activeCompanyId, { from: monthStart(month), to: monthEnd(month) })
  const flash = params.get('baru')
  const items = list.data ?? []
  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <MonthNav
          value={month}
          onChange={(m) => {
            const p = new URLSearchParams(params)
            p.set('bulan', m)
            setParams(p, { replace: true })
          }}
        />
      </div>
      <TableShell minWidth={760}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Tanggal</th>
            <th className={th}>Nomor</th>
            <th className={th}>{t.contact}</th>
            <th className={th}>Rekening</th>
            <th className={`${th} text-right`}>{kind === 'sales' ? 'Diterima' : 'Dibayar'}</th>
            <th className={th}>Status</th>
          </tr>
        </thead>
        <tbody>
          {list.isPending &&
            [0, 1, 2].map((i) => (
              <tr key={i} className="border-t border-line">
                <td colSpan={6} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            ))}
          {items.map((s) => (
            <tr key={s.id} onClick={() => onOpen(s.id)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${s.id === flash ? 'flash' : ''} ${s.status === 'void' ? 'text-muted' : ''}`}>
              <td className={`${td} num whitespace-nowrap`}>{formatDate(s.date)}</td>
              <td className={`${td} num font-semibold whitespace-nowrap ${s.status === 'void' ? 'line-through' : 'text-accent'}`}>
                {s.number}
                {(s.kind === 'pengembalian' || toUnits(s.advance) > 0n) && (
                  <span className="block text-xs font-normal text-muted">{s.kind === 'pengembalian' ? 'Pengembalian uang muka' : 'Uang muka'}</span>
                )}
              </td>
              <td className={`${td} max-w-[240px] truncate`}>{s.contactName}</td>
              <td className={td}>{s.cashAccount}</td>
              <td className={`${td} num text-right`}>{formatAmount(s.amount)}</td>
              <td className={td}>{s.status === 'void' ? <Badge tone="bad">Void</Badge> : <Badge tone="good">Diposting</Badge>}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && <EmptyState title={`Belum ada ${t.settle_} di ${monthLabel(month)}`} />}
    </>
  )
}

function NoteList({ me, kind, onOpen }: { me: Me; kind: TradeKind; onOpen: (id: string) => void }) {
  const t = tradeText[kind]
  const [params, setParams] = useSearchParams()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const list = useNotes(kind, me.activeCompanyId, { from: monthStart(month), to: monthEnd(month) })
  const items = list.data ?? []
  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <MonthNav
          value={month}
          onChange={(m) => {
            const p = new URLSearchParams(params)
            p.set('bulan', m)
            setParams(p, { replace: true })
          }}
        />
        <p className="text-sm text-muted">
          Buat {t.note_} dari {t.doc_} yang diposting: buka {t.doc_}nya, lalu pilih “{t.note} / retur”.
        </p>
      </div>
      <TableShell minWidth={820}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Tanggal</th>
            <th className={th}>Nomor</th>
            <th className={th}>{t.contact}</th>
            <th className={th}>{t.doc} asal</th>
            <th className={`${th} text-right`}>Total</th>
            <th className={th}>Status</th>
          </tr>
        </thead>
        <tbody>
          {list.isPending &&
            [0, 1, 2].map((i) => (
              <tr key={i} className="border-t border-line">
                <td colSpan={6} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            ))}
          {items.map((n) => (
            <tr key={n.id} onClick={() => onOpen(n.id)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${n.status === 'void' ? 'text-muted' : ''}`}>
              <td className={`${td} num whitespace-nowrap`}>{formatDate(n.date)}</td>
              <td className={`${td} num font-semibold whitespace-nowrap ${n.status === 'void' ? 'line-through' : 'text-accent'}`}>{n.number}</td>
              <td className={`${td} max-w-[240px] truncate`}>{n.contactName}</td>
              <td className={`${td} num`}>{n.docNumber}</td>
              <td className={`${td} num text-right`}>{formatAmount(n.total)}</td>
              <td className={td}>{n.status === 'void' ? <Badge tone="bad">Void</Badge> : <Badge tone="good">Diposting</Badge>}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && <EmptyState title={`Belum ada ${t.note_} di ${monthLabel(month)}`} />}
    </>
  )
}

/** Rekap penjualan harian retail (IND-07), diisi lewat impor Excel atau CSV. */
function DailyList({ me, onOpen }: { me: Me; onOpen: (id: string) => void }) {
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const list = useDailySales(me.activeCompanyId, { from: monthStart(month), to: monthEnd(month) })
  const items = list.data ?? []
  const posted = items.filter((d) => d.status === 'posted')
  const sum = (k: 'dpp' | 'vat' | 'total') => posted.reduce((a, d) => a + toUnits(d[k]), 0n)
  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <MonthNav
          value={month}
          onChange={(m) => {
            const p = new URLSearchParams(params)
            p.set('bulan', m)
            setParams(p, { replace: true })
          }}
        />
        {can(me, 'sales.create') && (
          <Button variant="secondary" size="sm" onClick={() => navigate('/impor?jenis=penjualan-harian')}>
            Impor rekap harian
          </Button>
        )}
        {posted.length > 0 && (
          <p className="text-sm text-muted">
            {posted.length} rekap · penjualan <span className="num font-semibold text-ink">Rp {formatAmount(sum('total'))}</span> · PPN digunggung{' '}
            <span className="num font-semibold text-ink">Rp {formatAmount(sum('vat'))}</span>
          </p>
        )}
      </div>
      <TableShell minWidth={760}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Tanggal</th>
            <th className={th}>Nomor</th>
            <th className={th}>Toko</th>
            <th className={`${th} text-right`}>DPP</th>
            <th className={`${th} text-right`}>PPN</th>
            <th className={`${th} text-right`}>Total</th>
            <th className={th}>Status</th>
          </tr>
        </thead>
        <tbody>
          {list.isPending &&
            [0, 1, 2].map((i) => (
              <tr key={i} className="border-t border-line">
                <td colSpan={7} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            ))}
          {items.map((d) => (
            <tr key={d.id} onClick={() => onOpen(d.id)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${d.status === 'void' ? 'text-muted' : ''}`}>
              <td className={`${td} num whitespace-nowrap`}>{formatDate(d.date)}</td>
              <td className={`${td} num font-semibold whitespace-nowrap ${d.status === 'void' ? 'line-through' : 'text-accent'}`}>{d.number}</td>
              <td className={td}>{d.store || '—'}</td>
              <td className={`${td} num text-right`}>{formatAmount(d.dpp)}</td>
              <td className={`${td} num text-right`}>{formatAmount(d.vat)}</td>
              <td className={`${td} num text-right`}>{formatAmount(d.total)}</td>
              <td className={td}>{d.status === 'void' ? <Badge tone="bad">Void</Badge> : <Badge tone="good">Diposting</Badge>}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && (
        <EmptyState title={`Belum ada rekap penjualan harian di ${monthLabel(month)}`} body="Impor rekap dari kasir atau spreadsheet toko; satu rekap per tanggal dan toko." />
      )}
    </>
  )
}

function AgingTable({ me, kind, onContact }: { me: Me; kind: TradeKind; onContact: (id: string) => void }) {
  const t = tradeText[kind]
  const [date, setDate] = useState(todayISO())
  const aging = useAging(kind, me.activeCompanyId, date)
  const a = aging.data
  return (
    <>
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-[200px]">
          <Field id="umur-per" label="Per tanggal" type="date" value={date} onChange={(e) => e.target.value && setDate(e.target.value)} />
        </div>
        {a && (
          <p className="pb-3 text-sm text-muted">
            Total {kind === 'sales' ? 'piutang' : 'utang'} belum lunas: <span className="num font-semibold text-ink">Rp {formatAmount(a.total)}</span>
          </p>
        )}
      </div>
      <div className={`transition-opacity duration-150 ${aging.isPlaceholderData ? 'opacity-60' : ''}`}>
        <TableShell minWidth={900}>
          <thead className="bg-hover/60">
            <tr>
              <th className={th}>{t.contact}</th>
              {buckets.map((b) => (
                <th key={b.key} className={`${th} text-right`}>
                  {b.label}
                </th>
              ))}
              <th className={`${th} text-right`}>Total</th>
            </tr>
          </thead>
          <tbody>
            {aging.isPending && (
              <tr>
                <td colSpan={7} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            )}
            {a?.rows.map((r) => (
              <tr key={r.contactId} onClick={() => onContact(r.contactId)} className="cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover">
                <td className={td}>
                  <span className="font-medium">{r.contactName}</span>
                  <span className="block text-xs text-muted">{r.documents} dokumen</span>
                </td>
                {buckets.map((b) => (
                  <td key={b.key} className={`${td} num text-right ${b.key === '90+' && r.buckets[b.key] !== '0.0000' ? 'font-semibold text-bad' : ''}`}>
                    {formatAmount(r.buckets[b.key], { blankZero: true })}
                  </td>
                ))}
                <td className={`${td} num text-right font-semibold`}>{formatAmount(r.total)}</td>
              </tr>
            ))}
            {a && a.rows.length > 0 && (
              <tr className="border-t border-line-2 bg-hover/40 font-semibold">
                <td className={td}>Jumlah</td>
                {buckets.map((b) => (
                  <td key={b.key} className={`${td} num text-right`}>
                    {formatAmount(a.totals[b.key], { blankZero: true })}
                  </td>
                ))}
                <td className={`${td} num text-right`}>{formatAmount(a.total)}</td>
              </tr>
            )}
          </tbody>
        </TableShell>
      </div>
      {a && a.rows.length === 0 && <EmptyState title={`Tidak ada ${kind === 'sales' ? 'piutang' : 'utang'} yang belum lunas per ${formatDate(date)}`} />}
      {kind === 'purchase' && can(me, 'report.view') && !isRestricted(me) && <CashProjectionCard me={me} date={date} />}
    </>
  )
}

/** AP-06: saldo kas proyeksi per minggu dari piutang dan utang yang jatuh tempo. */
function CashProjectionCard({ me, date }: { me: Me; date: string }) {
  const q = useCashProjection(me.activeCompanyId, date)
  const p = q.data
  return (
    <Card
      title="Proyeksi kebutuhan kas"
      subtitle="Saldo kas dan bank hari ini, ditambah piutang dan dikurangi utang yang jatuh tempo per minggu. Dokumen lewat jatuh tempo dianggap diterima atau dibayar segera."
    >
      {!p && <Skeleton className="h-32" />}
      {p && (
        <>
          <p className="text-sm">
            Saldo kas dan bank: <span className="num font-semibold">Rp {formatAmount(p.cash)}</span>
          </p>
          {p.firstShortfall && <Alert tone="warn">Saldo proyeksi negatif mulai {p.firstShortfall.toLowerCase()}. Siapkan dana tambahan atau jadwalkan ulang pembayaran.</Alert>}
          <TableShell minWidth={640}>
            <thead className="bg-hover/60">
              <tr>
                <th className={th}>Periode</th>
                <th className={`${th} text-right`}>Piutang jatuh tempo</th>
                <th className={`${th} text-right`}>Utang jatuh tempo</th>
                <th className={`${th} text-right`}>Saldo proyeksi</th>
              </tr>
            </thead>
            <tbody>
              {p.buckets.map((b) => (
                <tr key={b.label} className="border-t border-line">
                  <td className={td}>
                    <span className="font-medium">{b.label}</span>
                    {b.from && (
                      <span className="block text-xs text-muted">
                        {formatDate(b.from)}
                        {b.to ? ` – ${formatDate(b.to)}` : ' dan seterusnya'}
                      </span>
                    )}
                  </td>
                  <td className={`${td} num text-right`}>
                    {formatAmount(b.in, { blankZero: true })}
                    {b.invoices.length > 0 && <span className="block text-xs text-muted">{b.invoices.length} faktur</span>}
                  </td>
                  <td className={`${td} num text-right`}>
                    {formatAmount(b.out, { blankZero: true })}
                    {b.bills.length > 0 && <span className="block text-xs text-muted">{b.bills.length} tagihan</span>}
                  </td>
                  <td className={`${td} num text-right font-semibold ${toUnits(b.balance) < 0n ? 'text-bad' : ''}`}>{formatAmount(b.balance, { parens: true })}</td>
                </tr>
              ))}
            </tbody>
          </TableShell>
        </>
      )}
    </Card>
  )
}

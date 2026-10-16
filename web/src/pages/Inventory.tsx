import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { newIdempotencyKey, type AdjustmentKind, type Me } from '../lib/api'
import { formatDate, formatDateTime, monthEnd, monthOf, monthStart, todayISO } from '../lib/dates'
import { formatQty, useAdjustment, useAdjustments, useStock, useStockCard, useVoidAdjustment } from '../lib/inventory'
import { sourceLink } from '../lib/labels'
import { formatAmount, toUnits } from '../lib/money'
import { useProjectNames } from '../lib/projects'
import { can } from '../lib/session'
import { MonthNav } from '../components/MonthNav'
import { useToast } from '../components/Toast'
import { VoidForm } from '../components/VoidForm'
import { Alert, Badge, Button, Card, Drawer, EmptyState, Field, PageHeader, Skeleton, TableShell, Tabs, td, th } from '../components/ui'

export const adjustmentKind: Record<AdjustmentKind, { label: string; tone: 'accent' | 'warn' | 'neutral'; help: string }> = {
  penyesuaian: { label: 'Penyesuaian', tone: 'neutral', help: 'Tambah atau kurangi stok, mis. saldo awal, barang rusak, atau hilang.' },
  pemakaian: { label: 'Pemakaian', tone: 'warn', help: 'Barang keluar untuk dipakai sendiri, mis. material ke proyek.' },
  opname: { label: 'Stock opname', tone: 'accent', help: 'Hitung fisik; selisih terhadap stok sistem dijurnal otomatis.' },
}

/** Persediaan: posisi stok per tanggal dengan pencocokan ke buku besar, kartu stok, dan dokumen persediaan (INV-03, INV-04). */
export function InventoryPage({ me }: { me: Me }) {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'stok'
  const productId = params.get('produk')
  const docId = params.get('doc')
  const canCreate = can(me, 'inventory.create')

  function update(next: Record<string, string | null>) {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v)
      else p.delete(k)
    }
    setParams(p, { replace: true })
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Persediaan"
        subtitle="Stok satu gudang dengan HPP rata-rata tertimbang. Penjualan barang membukukan HPP otomatis; transaksi bertanggal mundur menghitung ulang HPP sesudahnya dan menjurnal koreksinya."
        actions={
          canCreate && (
            <>
              <Button variant="secondary" onClick={() => navigate('/persediaan/baru?jenis=pemakaian')}>
                Pemakaian
              </Button>
              <Button variant="secondary" onClick={() => navigate('/persediaan/baru?jenis=penyesuaian')}>
                Penyesuaian
              </Button>
              <Button onClick={() => navigate('/persediaan/baru?jenis=opname')}>Stock opname</Button>
            </>
          )
        }
      />
      <Tabs
        label="Persediaan"
        value={tab}
        onChange={(v) => update({ tab: v === 'stok' ? null : v })}
        tabs={[
          { value: 'stok', label: 'Posisi stok' },
          { value: 'dokumen', label: 'Dokumen persediaan' },
        ]}
      />
      {tab === 'stok' && <StockTab me={me} onOpen={(id) => update({ produk: id })} />}
      {tab === 'dokumen' && <AdjustmentList me={me} onOpen={(id) => update({ doc: id })} />}
      <StockCardDrawer me={me} productId={productId} onClose={() => update({ produk: null })} />
      <AdjustmentDrawer me={me} id={docId} onClose={() => update({ doc: null })} />
    </div>
  )
}

function StockTab({ me, onOpen }: { me: Me; onOpen: (id: string) => void }) {
  const [date, setDate] = useState(todayISO())
  const stock = useStock(me.activeCompanyId, date)
  const r = stock.data
  const mismatched = r?.accounts.filter((a) => toUnits(a.difference) !== 0n) ?? []
  return (
    <>
      <div className="flex flex-wrap items-end gap-4">
        <Field id="stok-tanggal" label="Posisi per tanggal" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="max-w-[200px]" />
        {r && (
          <p className="pb-3 text-sm text-muted">
            Nilai persediaan <span className="num font-semibold text-ink">Rp {formatAmount(r.totalValue)}</span>
          </p>
        )}
      </div>
      {r && r.accounts.length > 0 && (
        <Card title="Cocok dengan buku besar?" subtitle="Nilai kartu stok dibandingkan saldo akun persediaan pada tanggal yang sama.">
          <ul className="flex flex-col">
            {r.accounts.map((a) => {
              const ok = toUnits(a.difference) === 0n
              return (
                <li key={a.accountId} className="flex flex-wrap items-center justify-between gap-3 border-b border-line py-2 text-sm last:border-0">
                  <span>
                    <span className="num font-semibold text-ink-2">{a.code}</span> {a.name}
                  </span>
                  <span className="num flex items-center gap-3 text-[13px]">
                    <span className="text-muted">Kartu stok {formatAmount(a.subledger)}</span>
                    <span className="text-muted">Buku besar {formatAmount(a.ledger)}</span>
                    <Badge tone={ok ? 'good' : 'bad'}>{ok ? 'Cocok' : `Selisih ${formatAmount(a.difference)}`}</Badge>
                  </span>
                </li>
              )
            })}
          </ul>
          {mismatched.length > 0 && (
            <Alert tone="warn">
              Ada jurnal manual ke akun persediaan. Nilai persediaan sebaiknya hanya berubah lewat tagihan, faktur, dan dokumen persediaan supaya sama dengan kartu stok.
            </Alert>
          )}
        </Card>
      )}
      <TableShell minWidth={760}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Barang</th>
            <th className={`${th} text-right`}>Stok</th>
            <th className={`${th} text-right`}>HPP rata-rata</th>
            <th className={`${th} text-right`}>Nilai</th>
          </tr>
        </thead>
        <tbody>
          {stock.isPending && (
            <tr>
              <td colSpan={4} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {r?.rows.map((row) => (
            <tr key={row.productId} onClick={() => onOpen(row.productId)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${row.active ? '' : 'text-muted'}`}>
              <td className={td}>
                <span className="num block text-[12.5px] text-muted">{row.code}</span>
                <span className="font-medium">{row.name}</span>
              </td>
              <td className={`${td} num text-right whitespace-nowrap ${toUnits(row.qty) === 0n ? 'text-muted' : ''}`}>
                {formatQty(row.qty)} {row.unit}
              </td>
              <td className={`${td} num text-right`}>{formatAmount(row.avgCost)}</td>
              <td className={`${td} num text-right`}>{formatAmount(row.value)}</td>
            </tr>
          ))}
          {r && r.rows.length > 0 && (
            <tr className="border-t border-line-2 font-semibold">
              <td className={td} colSpan={3}>
                Total
              </td>
              <td className={`${td} num text-right`}>{formatAmount(r.totalValue)}</td>
            </tr>
          )}
        </tbody>
      </TableShell>
      {r && r.rows.length === 0 && (
        <EmptyState
          title="Belum ada barang"
          body="Tambahkan produk berjenis barang di menu Produk, lalu isi saldo awalnya lewat dokumen penyesuaian."
          action={
            <Link to="/produk" className="text-sm font-semibold text-accent">
              Buka Produk
            </Link>
          }
        />
      )}
    </>
  )
}

function StockCardDrawer({ me, productId, onClose }: { me: Me; productId: string | null; onClose: () => void }) {
  const [from, setFrom] = useState(() => monthStart(monthOf(todayISO())))
  const [to, setTo] = useState(todayISO())
  const card = useStockCard(me.activeCompanyId, productId, from, to)
  const c = card.data && card.data.product.id === productId ? card.data : undefined
  const unit = c?.product.unit ?? ''
  return (
    <Drawer
      open={productId !== null}
      onClose={onClose}
      title={c ? `${c.product.code} · ${c.product.name}` : 'Kartu stok'}
      description={c && `Kartu stok · HPP rata-rata saat ini ${formatAmount(c.product.avgCost)} per ${unit}`}
    >
      <div className="grid grid-cols-2 gap-4">
        <Field id="kartu-dari" label="Dari" type="date" value={from} max={to} onChange={(e) => setFrom(e.target.value)} />
        <Field id="kartu-sampai" label="Sampai" type="date" value={to} min={from} onChange={(e) => setTo(e.target.value)} />
      </div>
      {!c && <Skeleton className="h-40" />}
      {c && (
        <div className="overflow-hidden rounded-[12px] border border-line">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[560px] text-left text-sm">
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="px-3 py-2.5 font-semibold">Dokumen</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Masuk</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Keluar</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Nilai</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Saldo</th>
                </tr>
              </thead>
              <tbody>
                <tr className="border-t border-line text-muted">
                  <td className="px-3 py-2.5">Saldo awal {formatDate(c.from)}</td>
                  <td colSpan={3} />
                  <td className="num px-3 py-2.5 text-right">
                    {formatQty(c.openingQty)}
                    <span className="block text-[12px]">{formatAmount(c.openingValue)}</span>
                  </td>
                </tr>
                {c.lines.map((l, i) => {
                  const link = docLink(l.sourceType, l.sourceId)
                  return (
                    <tr key={i} className="border-t border-line align-top">
                      <td className="px-3 py-2.5">
                        <span className="num text-[12.5px] text-muted">{formatDate(l.date)}</span>{' '}
                        {link ? (
                          <Link to={link} className={`num font-semibold ${l.reversal ? 'text-muted line-through' : 'text-accent'} hover:underline`}>
                            {l.number}
                          </Link>
                        ) : (
                          <span className="num font-semibold">{l.number}</span>
                        )}
                        <span className="block text-[12.5px] text-muted">{l.memo}</span>
                      </td>
                      <td className="num px-3 py-2.5 text-right">{toUnits(l.qtyIn) > 0n ? formatQty(l.qtyIn) : ''}</td>
                      <td className="num px-3 py-2.5 text-right">{toUnits(l.qtyOut) > 0n ? formatQty(l.qtyOut) : ''}</td>
                      <td className="num px-3 py-2.5 text-right">
                        {formatAmount(l.value)}
                        <span className="block text-[12px] text-muted">@ {formatAmount(l.unitCost)}</span>
                      </td>
                      <td className="num px-3 py-2.5 text-right">
                        {formatQty(l.qtyAfter)}
                        <span className="block text-[12px] text-muted">{formatAmount(l.valueAfter)}</span>
                      </td>
                    </tr>
                  )
                })}
                <tr className="border-t border-line-2 font-semibold">
                  <td className="px-3 py-2.5">Saldo akhir {formatDate(c.to)}</td>
                  <td colSpan={3} />
                  <td className="num px-3 py-2.5 text-right">
                    {formatQty(c.closingQty)} {unit}
                    <span className="block text-[12px] font-normal text-muted">{formatAmount(c.closingValue)}</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      )}
      {c?.truncated && <Alert tone="warn">Hanya 2.000 pergerakan pertama yang ditampilkan; persempit rentang tanggal.</Alert>}
    </Drawer>
  )
}

/** Halaman dokumen sumber pergerakan stok. */
function docLink(sourceType: string, id: string): string | null {
  switch (sourceType) {
    case 'inventory.adjustment':
      return `/persediaan?tab=dokumen&doc=${id}`
    default:
      return sourceLink(sourceType, id)
  }
}

function AdjustmentList({ me, onOpen }: { me: Me; onOpen: (id: string) => void }) {
  const [params, setParams] = useSearchParams()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const list = useAdjustments(me.activeCompanyId, { from: monthStart(month), to: monthEnd(month) })
  const projectNames = useProjectNames(me.activeCompanyId, can(me, 'project.view'))
  const items = list.data ?? []

  function setMonth(m: string) {
    const p = new URLSearchParams(params)
    p.set('bulan', m)
    setParams(p, { replace: true })
  }

  return (
    <>
      <div>
        <MonthNav value={month} onChange={setMonth} />
      </div>
      <TableShell minWidth={820}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Tanggal</th>
            <th className={th}>Nomor</th>
            <th className={th}>Jenis</th>
            <th className={th}>Akun lawan</th>
            <th className={th}>Keterangan</th>
            <th className={`${th} text-right`}>Barang</th>
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
          {items.map((a) => (
            <tr key={a.id} onClick={() => onOpen(a.id)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${a.status === 'void' ? 'text-muted' : ''}`}>
              <td className={`${td} num whitespace-nowrap`}>{formatDate(a.date)}</td>
              <td className={`${td} num font-semibold ${a.status === 'void' ? 'line-through' : 'text-accent'}`}>{a.number}</td>
              <td className={td}>
                <Badge tone={a.status === 'void' ? 'bad' : adjustmentKind[a.kind].tone}>{a.status === 'void' ? 'Void' : adjustmentKind[a.kind].label}</Badge>
              </td>
              <td className={td}>
                <span className="num text-ink-2">{a.accountCode}</span> {a.accountName}
              </td>
              <td className={`${td} max-w-[260px] truncate text-ink-2`}>
                {a.memo || '—'}
                {a.projectId && <span className="block truncate text-[12.5px] text-accent">{projectNames.get(a.projectId)}</span>}
              </td>
              <td className={`${td} num text-right`}>{a.lineCount}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && <EmptyState title="Belum ada dokumen persediaan bulan ini" body="Penyesuaian, pemakaian barang, dan stock opname muncul di sini." />}
    </>
  )
}

function AdjustmentDrawer({ me, id, onClose }: { me: Me; id: string | null; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const q = useAdjustment(companyId, id)
  const a = q.data && q.data.id === id ? q.data : undefined
  const voidIt = useVoidAdjustment(companyId)
  const projectNames = useProjectNames(companyId, Boolean(a?.projectId) && can(me, 'project.view'))
  const toast = useToast()
  const navigate = useNavigate()
  const [voiding, setVoiding] = useState(false)
  const [key, setKey] = useState(newIdempotencyKey)
  const counted = a?.kind === 'opname'

  function close() {
    setVoiding(false)
    voidIt.reset()
    onClose()
  }

  return (
    <Drawer
      open={id !== null}
      onClose={close}
      title={a?.number ?? 'Memuat…'}
      description={
        a && (
          <span className="flex flex-wrap items-center gap-2">
            <Badge tone={a.status === 'void' ? 'bad' : adjustmentKind[a.kind].tone}>{a.status === 'void' ? 'Void' : adjustmentKind[a.kind].label}</Badge>
            <span>{formatDate(a.date)}</span>
          </span>
        )
      }
      footer={
        a?.status === 'posted' &&
        can(me, 'inventory.void') &&
        !voiding && (
          <Button
            variant="secondary"
            onClick={() => {
              setKey(newIdempotencyKey())
              voidIt.reset()
              setVoiding(true)
            }}
          >
            Void…
          </Button>
        )
      }
    >
      {!a && <Skeleton className="h-40" />}
      {a && (
        <>
          <dl className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-x-6 gap-y-3 text-sm">
            <div>
              <dt className="text-[12.5px] text-muted">Akun lawan</dt>
              <dd>
                <span className="num">{a.accountCode}</span> {a.accountName}
              </dd>
            </div>
            {a.projectId && (
              <div>
                <dt className="text-[12.5px] text-muted">Proyek</dt>
                <dd>
                  <Link to={`/proyek/${a.projectId}`} className="font-medium text-accent hover:underline">
                    {projectNames.get(a.projectId) ?? 'Buka proyek'}
                  </Link>
                </dd>
              </div>
            )}
            <div>
              <dt className="text-[12.5px] text-muted">Dicatat</dt>
              <dd>{`${a.createdBy ?? 'Sistem'} · ${formatDateTime(a.createdAt)}`}</dd>
            </div>
          </dl>
          {a.memo && <p className="text-sm text-ink-2">{a.memo}</p>}
          {a.status === 'void' && (
            <Alert tone="bad">
              Di-void oleh {a.voidedBy ?? 'Sistem'} per {a.voidDate ? formatDate(a.voidDate) : '—'}: {a.voidReason}
            </Alert>
          )}
          <div className="overflow-hidden rounded-[12px] border border-line">
            <table className="w-full text-left text-sm">
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="px-3 py-2.5 font-semibold">Barang</th>
                  {counted && <th className="px-3 py-2.5 text-right font-semibold">Sistem → fisik</th>}
                  <th className="px-3 py-2.5 text-right font-semibold">Selisih</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Nilai</th>
                </tr>
              </thead>
              <tbody>
                {a.lines.map((l) => (
                  <tr key={l.productId} className="border-t border-line align-top">
                    <td className="px-3 py-2.5">
                      <span className="num text-[12.5px] text-muted">{l.productCode}</span> {l.productName}
                      {l.memo && <span className="block text-[12.5px] text-muted">{l.memo}</span>}
                    </td>
                    {counted && (
                      <td className="num px-3 py-2.5 text-right whitespace-nowrap">
                        {formatQty(l.systemQty ?? '0')} → {formatQty(l.countedQty ?? '0')}
                      </td>
                    )}
                    <td className={`num px-3 py-2.5 text-right whitespace-nowrap ${toUnits(l.qty) < 0n ? 'text-bad' : toUnits(l.qty) > 0n ? 'text-good' : 'text-muted'}`}>
                      {toUnits(l.qty) > 0n ? '+' : ''}
                      {formatQty(l.qty)} {l.unit}
                      {l.unitCost && <span className="block text-[12px] text-muted">@ {formatAmount(l.unitCost)}</span>}
                    </td>
                    <td className="num px-3 py-2.5 text-right">{formatAmount(l.value)}</td>
                  </tr>
                ))}
                <tr className="border-t border-line-2 font-semibold">
                  <td className="px-3 py-2.5" colSpan={counted ? 3 : 2}>
                    Perubahan nilai persediaan
                  </td>
                  <td className="num px-3 py-2.5 text-right">{formatAmount(a.total)}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <button type="button" onClick={() => navigate(`/jurnal?bulan=${monthOf(a.date)}&q=${encodeURIComponent(a.number)}`)} className="self-start text-sm font-semibold text-accent hover:underline">
            Lihat jurnal {a.number} →
          </button>
          {voiding && (
            <VoidForm
              title={`Void ${a.number}`}
              note="Dokumen tidak dihapus; barang dikembalikan dengan nilai yang sama persis pada tanggal void, dan jurnalnya dibalik."
              minDate={a.date}
              busy={voidIt.isPending}
              error={voidIt.error}
              onCancel={() => setVoiding(false)}
              onConfirm={(date, reason) =>
                voidIt.mutate({ id: a.id, date, reason, idempotencyKey: key }, { onSuccess: (v) => (toast(`${v.number} di-void`), setVoiding(false)) })
              }
            />
          )}
        </>
      )}
    </Drawer>
  )
}

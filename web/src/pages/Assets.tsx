import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import {
  lifeText,
  methodLabel,
  useAssetCategories,
  useAssetReconciliation,
  useAssets,
  useDefaultCategories,
  useDepRun,
  useDepRuns,
  useDisposalAsset,
  useFiscalReport,
  usePostRun,
  useRunPreview,
  useSaveCategory,
  useVoidRun,
  type AssetCategory,
  type DepMethod,
  type FiscalGroup,
  type FiscalMethod,
} from '../lib/assets'
import { addMonths, formatDate, formatDateTime, monthLabel, monthOf, todayISO } from '../lib/dates'
import { useAccounts } from '../lib/ledger'
import { formatAmount, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { AccountPicker } from '../components/AccountPicker'
import { AssetDrawer } from '../components/AssetDrawer'
import { MonthNav } from '../components/MonthNav'
import { useToast } from '../components/Toast'
import { VoidForm } from '../components/VoidForm'
import { Alert, Badge, Button, Card, Checkbox, Dialog, Drawer, EmptyState, Field, PageHeader, Segmented, SelectField, Skeleton, TableShell, Tabs, td, th } from '../components/ui'

type Tab = 'daftar' | 'penyusutan' | 'fiskal' | 'kategori'

/** Aset tetap (FA-01–03): register, penyusutan bulanan, daftar penyusutan fiskal, dan kategori. */
export function AssetsPage({ me }: { me: Me }) {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Tab) ?? 'daftar'
  const companyId = me.activeCompanyId
  const disposalId = params.get('pelepasan')
  const fromDisposal = useDisposalAsset(companyId, disposalId)

  function update(next: Record<string, string | null>) {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v)
      else p.delete(k)
    }
    setParams(p, { replace: true })
  }

  // Tautan dari jurnal pelepasan membawa id pelepasan; buka asetnya.
  const disposalAsset = disposalId ? fromDisposal.data?.id : undefined
  useEffect(() => {
    if (disposalAsset) update({ pelepasan: null, tab: null, id: disposalAsset })
  }, [disposalAsset])

  if (!can(me, 'asset.view')) return <EmptyState title="Anda tidak punya izin melihat aset tetap" />
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Aset tetap"
        subtitle="Register aset dengan penyusutan bulanan otomatis. Perolehan aset dicatat lewat tagihan, kas, atau jurnal ke akun aset; register dicocokkan dengan saldo akun itu."
        actions={can(me, 'asset.create') && <Button onClick={() => navigate('/aset/baru')}>Aset baru</Button>}
      />
      <Tabs
        label="Aset tetap"
        value={tab}
        onChange={(v) => update({ tab: v === 'daftar' ? null : v, id: null, run: null })}
        tabs={[
          { value: 'daftar', label: 'Daftar aset' },
          { value: 'penyusutan', label: 'Penyusutan' },
          { value: 'fiskal', label: 'Penyusutan fiskal' },
          { value: 'kategori', label: 'Kategori' },
        ]}
      />
      {tab === 'daftar' && <AssetList me={me} onOpen={(id) => update({ id })} onCategories={() => update({ tab: 'kategori' })} />}
      {tab === 'penyusutan' && <Depreciation me={me} runId={params.get('run')} onRun={(run) => update({ run })} />}
      {tab === 'fiskal' && <FiscalTab me={me} onOpen={(id) => update({ tab: null, id })} />}
      {tab === 'kategori' && <Categories me={me} />}
      <AssetDrawer me={me} id={params.get('id')} onClose={() => update({ id: null })} />
    </div>
  )
}

// Daftar aset --------------------------------------------------------------------------

function AssetList({ me, onOpen, onCategories }: { me: Me; onOpen: (id: string) => void; onCategories: () => void }) {
  const companyId = me.activeCompanyId
  const list = useAssets(companyId)
  const cats = useAssetCategories(companyId)
  const rec = useAssetReconciliation(companyId)
  const navigate = useNavigate()
  const [status, setStatus] = useState<'aktif' | 'dilepas' | ''>('aktif')
  const items = (list.data ?? []).filter((a) => !status || a.status === status)
  const sum = (f: 'cost' | 'accumulated' | 'bookValue') => items.reduce((s, a) => s + toUnits(a[f]), 0n)
  const r = rec.data

  if (cats.isSuccess && cats.data.items.length === 0) {
    return (
      <EmptyState
        title="Belum ada kategori aset"
        body="Kategori menentukan akun, metode, dan umur penyusutan bawaan. Mulai dengan kategori bawaan dari bagan akun (tanah, bangunan, kendaraan, peralatan), lalu sesuaikan bila perlu."
        action={can(me, 'asset.create') && <Button onClick={onCategories}>Atur kategori</Button>}
      />
    )
  }
  return (
    <>
      {r && r.rows.length > 0 && (
        <Card title="Cocok dengan buku besar?" subtitle="Total register dibandingkan saldo akun aset dan akumulasi penyusutan saat ini.">
          <ul className="flex flex-col">
            {r.rows.map((row) => {
              const ok = toUnits(row.difference) === 0n
              return (
                <li key={`${row.accountId}-${row.side}`} className="flex flex-wrap items-center justify-between gap-3 border-b border-line py-2 text-sm last:border-0">
                  <span>
                    <span className="num font-semibold text-ink-2">{row.code}</span> {row.name}
                  </span>
                  <span className="num flex items-center gap-3 text-[13px]">
                    <span className="text-muted">Register {formatAmount(row.register)}</span>
                    <span className="text-muted">Buku besar {formatAmount(row.ledger)}</span>
                    <Badge tone={ok ? 'good' : 'bad'}>{ok ? 'Cocok' : `Selisih ${formatAmount(row.difference)}`}</Badge>
                  </span>
                </li>
              )
            })}
          </ul>
          {!r.matched && (
            <Alert tone="warn">
              Selisih biasanya berarti ada aset yang sudah dibeli (lewat tagihan, kas, jurnal, atau saldo awal) tetapi belum didaftarkan di sini, atau penyusutan yang dijurnal manual di luar modul aset.
            </Alert>
          )}
        </Card>
      )}
      <div className="flex flex-wrap items-center gap-3">
        <Segmented
          label="Status aset"
          value={status}
          onChange={setStatus}
          options={[
            { value: 'aktif', label: 'Aktif' },
            { value: 'dilepas', label: 'Dilepas' },
            { value: '', label: 'Semua' },
          ]}
        />
      </div>
      <TableShell minWidth={900}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Aset</th>
            <th className={th}>Diperoleh</th>
            <th className={th}>Metode</th>
            <th className={`${th} text-right`}>Harga perolehan</th>
            <th className={`${th} text-right`}>Akumulasi</th>
            <th className={`${th} text-right`}>Nilai buku</th>
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
            <tr key={a.id} onClick={() => onOpen(a.id)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${a.status === 'dilepas' ? 'text-muted' : ''}`}>
              <td className={td}>
                <span className="num mr-2 text-[12.5px] text-muted">{a.code}</span>
                <span className="font-medium">{a.name}</span>
                <span className="block text-[12.5px] text-muted">
                  {a.categoryName}
                  {a.location && ` · ${a.location}`}
                  {a.status === 'dilepas' && a.disposedOn && ` · dilepas ${formatDate(a.disposedOn)}`}
                </span>
              </td>
              <td className={`${td} num whitespace-nowrap`}>{formatDate(a.acquiredOn)}</td>
              <td className={td}>
                {methodLabel[a.method]}
                {a.method !== 'tidak_disusutkan' && <span className="block text-[12.5px] text-muted">{lifeText(a.lifeMonths)}</span>}
              </td>
              <td className={`${td} num text-right`}>{formatAmount(a.cost)}</td>
              <td className={`${td} num text-right`}>{formatAmount(a.accumulated, { blankZero: true })}</td>
              <td className={`${td} num text-right font-semibold`}>{formatAmount(a.bookValue)}</td>
            </tr>
          ))}
          {items.length > 1 && (
            <tr className="border-t border-line-2 font-semibold">
              <td className={td} colSpan={3}>
                Jumlah {items.length} aset
              </td>
              <td className={`${td} num text-right`}>{formatAmount(sum('cost'))}</td>
              <td className={`${td} num text-right`}>{formatAmount(sum('accumulated'))}</td>
              <td className={`${td} num text-right`}>{formatAmount(sum('bookValue'))}</td>
            </tr>
          )}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && (
        <EmptyState
          title={status === 'dilepas' ? 'Belum ada aset yang dilepas' : 'Belum ada aset'}
          body="Daftarkan aset yang sudah dibeli, termasuk aset lama beserta akumulasi penyusutannya, supaya penyusutannya dijurnal otomatis setiap bulan."
          action={can(me, 'asset.create') && status !== 'dilepas' && <Button onClick={() => navigate('/aset/baru')}>Daftarkan aset</Button>}
        />
      )}
    </>
  )
}

// Penyusutan bulanan --------------------------------------------------------------------

function Depreciation({ me, runId, onRun }: { me: Me; runId: string | null; onRun: (id: string | null) => void }) {
  const companyId = me.activeCompanyId
  const thisMonth = monthOf(todayISO())
  const [month, setMonth] = useState(() => addMonths(thisMonth, -1))
  const preview = useRunPreview(companyId, month)
  const runs = useDepRuns(companyId)
  const post = usePostRun(companyId)
  const toast = useToast()
  const [key, setKey] = useState(newIdempotencyKey)
  const p = preview.data
  const err = post.error instanceof ApiError ? post.error : null
  const future = month > thisMonth

  return (
    <>
      <p className="max-w-3xl text-sm text-muted">
        Penyusutan bulan lalu diposting otomatis mulai tanggal 1 sebagai jurnal penyesuaian (seri PNY). Anda juga bisa memostingnya sendiri, mis. saat tutup buku sebelum tanggal 1. Bulan yang
        terlewat ikut disusutkan di jurnal berikutnya.
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <MonthNav value={month} onChange={setMonth} />
      </div>
      <Card
        title={`Penyusutan ${monthLabel(month)}`}
        subtitle={
          p?.posted
            ? `Sudah diposting: ${p.posted.number} · Rp ${formatAmount(p.posted.total)}`
            : future
              ? 'Bulan depan belum bisa diposting.'
              : 'Aset yang penyusutannya jatuh tempo s.d. bulan ini dan belum dijurnal.'
        }
        actions={
          p?.posted ? (
            <Button variant="secondary" onClick={() => onRun(p.posted!.id)}>
              Lihat jurnal
            </Button>
          ) : (
            can(me, 'asset.create') &&
            !future &&
            p &&
            p.lines.length > 0 && (
              <Button
                busy={post.isPending}
                onClick={() =>
                  post.mutate(
                    { month, idempotencyKey: key },
                    {
                      onSuccess: (r) => {
                        toast(`${r.number} diposting · Rp ${formatAmount(r.total)}`)
                        setKey(newIdempotencyKey())
                      },
                    },
                  )
                }
              >
                Posting penyusutan {monthLabel(month)}
              </Button>
            )
          )
        }
      >
        {!p && <Skeleton className="h-16" />}
        {p && p.lines.length === 0 && !p.posted && <p className="text-sm text-muted">Tidak ada penyusutan yang jatuh tempo.</p>}
        {p && p.lines.length > 0 && (
          <ul className="flex flex-col text-sm">
            {p.lines.map((l) => (
              <li key={l.assetId} className="flex items-center justify-between gap-3 border-b border-line py-2 last:border-0">
                <span>
                  <span className="num mr-2 text-[12.5px] text-muted">{l.code}</span>
                  {l.name}
                  {l.months > 1 && <span className="ml-2 text-[12.5px] text-warn">{l.months} bulan, sejak {monthLabel(monthOf(l.firstMonth))}</span>}
                </span>
                <span className="num">{formatAmount(l.amount)}</span>
              </li>
            ))}
            <li className="flex items-center justify-between gap-3 border-t border-line-2 pt-2 font-semibold">
              <span>Jumlah</span>
              <span className="num">{formatAmount(p.total)}</span>
            </li>
          </ul>
        )}
        {err && <Alert>{err.message}</Alert>}
      </Card>

      <TableShell minWidth={640}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Bulan</th>
            <th className={th}>Nomor</th>
            <th className={`${th} text-right`}>Aset</th>
            <th className={`${th} text-right`}>Total</th>
            <th className={th}>Diposting</th>
          </tr>
        </thead>
        <tbody>
          {runs.isPending && (
            <tr>
              <td colSpan={5} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {(runs.data ?? []).map((r) => (
            <tr key={r.id} onClick={() => onRun(r.id)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${r.status === 'void' ? 'text-muted' : ''}`}>
              <td className={td}>{monthLabel(monthOf(r.month))}</td>
              <td className={td}>
                <span className={`num font-semibold ${r.status === 'void' ? 'line-through' : 'text-accent'}`}>{r.number}</span>
                {r.status === 'void' && (
                  <Badge tone="bad" className="ml-2">
                    Void
                  </Badge>
                )}
              </td>
              <td className={`${td} num text-right`}>{r.assets}</td>
              <td className={`${td} num text-right`}>{formatAmount(r.total)}</td>
              <td className={`${td} text-[13px] text-muted`}>
                {r.postedBy ?? 'Penjadwal'} · {formatDateTime(r.postedAt)}
              </td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {runs.isSuccess && runs.data.length === 0 && <EmptyState title="Belum ada jurnal penyusutan" body="Jurnal penyusutan bulanan akan muncul di sini." />}
      <RunDrawer me={me} id={runId} onClose={() => onRun(null)} />
    </>
  )
}

function RunDrawer({ me, id, onClose }: { me: Me; id: string | null; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const q = useDepRun(companyId, id)
  const r = q.data && q.data.id === id ? q.data : undefined
  const voidRun = useVoidRun(companyId)
  const navigate = useNavigate()
  const toast = useToast()
  const [voiding, setVoiding] = useState(false)
  const [key, setKey] = useState(newIdempotencyKey)
  return (
    <Drawer
      open={id !== null}
      onClose={() => (setVoiding(false), voidRun.reset(), onClose())}
      title={r?.number ?? 'Memuat…'}
      description={
        r && (
          <span className="flex items-center gap-2">
            <span>Penyusutan {monthLabel(monthOf(r.month))}</span>
            {r.status === 'void' ? <Badge tone="bad">Void</Badge> : <Badge tone="good">Diposting</Badge>}
          </span>
        )
      }
      footer={
        r &&
        r.status === 'posted' &&
        can(me, 'asset.void') &&
        !voiding && (
          <Button variant="secondary" onClick={() => (voidRun.reset(), setVoiding(true))}>
            Void jurnal…
          </Button>
        )
      }
    >
      {!r && <Skeleton className="h-32" />}
      {r && (
        <>
          {r.status === 'void' && r.voidReason && (
            <Alert tone="bad">
              Di-void oleh {r.voidedBy ?? 'Sistem'}: {r.voidReason}
            </Alert>
          )}
          <div className="overflow-hidden rounded-[12px] border border-line">
            <table className="w-full text-left text-sm">
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="px-3 py-2.5 font-semibold">Aset</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Bulan</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Penyusutan</th>
                </tr>
              </thead>
              <tbody>
                {r.lines.map((l) => (
                  <tr key={l.assetId} className="border-t border-line">
                    <td className="px-3 py-2.5">
                      <span className="num mr-2 text-[12.5px] text-muted">{l.code}</span>
                      {l.name}
                    </td>
                    <td className="num px-3 py-2.5 text-right">{l.months}</td>
                    <td className="num px-3 py-2.5 text-right">{formatAmount(l.amount)}</td>
                  </tr>
                ))}
                <tr className="border-t border-line-2 font-semibold">
                  <td className="px-3 py-2.5" colSpan={2}>
                    Jumlah
                  </td>
                  <td className="num px-3 py-2.5 text-right">{formatAmount(r.total)}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <button type="button" onClick={() => navigate(`/jurnal?bulan=${monthOf(r.month)}&id=${r.journalEntryId}`)} className="self-start text-sm font-semibold text-accent hover:underline">
            Lihat jurnal {r.number} →
          </button>
          {voiding && (
            <VoidForm
              title={`Void ${r.number}`}
              note="Jurnalnya dibalik; bulan-bulan ini kembali belum dijurnal dan ikut jurnal penyusutan berikutnya."
              minDate={r.month.slice(0, 7) + '-01'}
              busy={voidRun.isPending}
              error={voidRun.error}
              onCancel={() => setVoiding(false)}
              onConfirm={(date, reason) =>
                voidRun.mutate({ id: r.id, date, reason, idempotencyKey: key }, { onSuccess: () => (toast(`${r.number} di-void`), setVoiding(false), setKey(newIdempotencyKey())) })
              }
            />
          )}
        </>
      )}
    </Drawer>
  )
}

// Penyusutan fiskal ---------------------------------------------------------------------

function FiscalTab({ me, onOpen }: { me: Me; onOpen: (id: string) => void }) {
  const thisYear = Number(todayISO().slice(0, 4))
  const [year, setYear] = useState(thisYear)
  const report = useFiscalReport(me.activeCompanyId, year)
  const r = report.data
  const years = Array.from({ length: 8 }, (_, i) => thisYear + 1 - i)
  return (
    <>
      <p className="max-w-3xl text-sm text-muted">
        Penyusutan fiskal per kelompok harta (UU PPh Pasal 11) sebagai daftar penyusutan lampiran SPT Tahunan Badan. Koreksi = penyusutan komersial − fiskal; angka positif berarti koreksi fiskal
        positif. Penyusutan komersial di sini dari jadwal aset.
      </p>
      <div className="flex flex-wrap items-end gap-3">
        <SelectField id="fiskal-tahun" label="Tahun pajak" value={String(year)} onChange={(e) => setYear(Number(e.target.value))} className="max-w-[160px]">
          {years.map((y) => (
            <option key={y} value={y}>
              {y}
            </option>
          ))}
        </SelectField>
      </div>
      <TableShell minWidth={1100}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Aset</th>
            <th className={th}>Kelompok</th>
            <th className={th}>Diperoleh</th>
            <th className={`${th} text-right`}>Harga perolehan</th>
            <th className={`${th} text-right`}>Nilai sisa awal</th>
            <th className={`${th} text-right`}>Penyusutan fiskal</th>
            <th className={`${th} text-right`}>Nilai sisa akhir</th>
            <th className={`${th} text-right`}>Komersial</th>
            <th className={`${th} text-right`}>Koreksi</th>
          </tr>
        </thead>
        <tbody>
          {!r && (
            <tr>
              <td colSpan={9} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {r?.rows.map((x) => (
            <tr key={x.assetId} onClick={() => onOpen(x.assetId)} className="cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover">
              <td className={td}>
                <span className="num mr-2 text-[12.5px] text-muted">{x.code}</span>
                {x.name}
                {x.disposed && <span className="block text-[12.5px] text-muted">dilepas</span>}
              </td>
              <td className={td}>
                {x.groupLabel}
                <span className="block text-[12.5px] text-muted">{methodLabel[x.method]}</span>
              </td>
              <td className={`${td} num whitespace-nowrap`}>{formatDate(x.acquiredOn)}</td>
              <td className={`${td} num text-right`}>{formatAmount(x.cost)}</td>
              <td className={`${td} num text-right`}>{formatAmount(x.openingBook)}</td>
              <td className={`${td} num text-right font-semibold`}>{formatAmount(x.fiscal, { blankZero: true })}</td>
              <td className={`${td} num text-right`}>{formatAmount(x.closingBook)}</td>
              <td className={`${td} num text-right`}>{formatAmount(x.commercial, { blankZero: true })}</td>
              <td className={`${td} num text-right ${toUnits(x.correction) !== 0n ? 'font-semibold' : 'text-muted'}`}>{formatAmount(x.correction, { parens: true })}</td>
            </tr>
          ))}
          {r && r.rows.length > 0 && (
            <tr className="border-t border-line-2 font-semibold">
              <td className={td} colSpan={5}>
                Jumlah
              </td>
              <td className={`${td} num text-right`}>{formatAmount(r.totalFiscal)}</td>
              <td className={td} />
              <td className={`${td} num text-right`}>{formatAmount(r.totalCommercial)}</td>
              <td className={`${td} num text-right`}>{formatAmount(r.totalCorrection, { parens: true })}</td>
            </tr>
          )}
        </tbody>
      </TableShell>
      {r && r.rows.length === 0 && <EmptyState title={`Belum ada aset untuk tahun ${year}`} />}
    </>
  )
}

// Kategori ------------------------------------------------------------------------------

function Categories({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const q = useAssetCategories(companyId)
  const defaults = useDefaultCategories(companyId)
  const accounts = useAccounts(companyId).data?.items ?? []
  const toast = useToast()
  const [editing, setEditing] = useState<AssetCategory | 'new' | null>(null)
  const canEdit = can(me, 'asset.create')
  const items = q.data?.items ?? []
  const groups = q.data?.fiscalGroups ?? []
  const account = (id?: string) => accounts.find((a) => a.id === id)
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-2xl text-sm text-muted">Kategori mengisi akun, metode, umur, dan kelompok fiskal bawaan saat mendaftarkan aset. Setiap aset tetap bisa diatur sendiri.</p>
        {canEdit && (
          <div className="flex gap-2">
            <Button variant="secondary" busy={defaults.isPending} onClick={() => defaults.mutate(undefined, { onSuccess: () => toast('Kategori bawaan dibuat') })}>
              Buat kategori bawaan
            </Button>
            <Button onClick={() => setEditing('new')}>Kategori baru</Button>
          </div>
        )}
      </div>
      {defaults.error && <Alert>{defaults.error.message}</Alert>}
      <TableShell minWidth={860}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Kategori</th>
            <th className={th}>Metode</th>
            <th className={th}>Kelompok fiskal</th>
            <th className={th}>Akun aset</th>
            <th className={th}>Akun beban penyusutan</th>
          </tr>
        </thead>
        <tbody>
          {q.isPending && (
            <tr>
              <td colSpan={5} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {items.map((c) => (
            <tr key={c.id} onClick={() => canEdit && setEditing(c)} className={`border-t border-line ${canEdit ? 'cursor-pointer transition-colors duration-150 hover:bg-hover' : ''} ${c.active ? '' : 'text-muted'}`}>
              <td className={td}>
                <span className="font-medium">{c.name}</span>
                {!c.active && (
                  <Badge tone="neutral" className="ml-2">
                    Nonaktif
                  </Badge>
                )}
              </td>
              <td className={td}>
                {methodLabel[c.method]}
                {c.method !== 'tidak_disusutkan' && <span className="block text-[12.5px] text-muted">{lifeText(c.lifeMonths)}</span>}
              </td>
              <td className={td}>{groups.find((g) => g.code === c.fiscalGroup)?.label ?? c.fiscalGroup}</td>
              <td className={td}>
                <span className="num text-ink-2">{account(c.assetAccountId)?.code}</span> {account(c.assetAccountId)?.name}
              </td>
              <td className={td}>
                {c.expenseAccountId ? (
                  <>
                    <span className="num text-ink-2">{account(c.expenseAccountId)?.code}</span> {account(c.expenseAccountId)?.name}
                  </>
                ) : (
                  <span className="text-muted">—</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {q.isSuccess && items.length === 0 && (
        <EmptyState
          title="Belum ada kategori"
          body="Buat kategori bawaan dari akun aset tetap di bagan akun (1-21xx), atau buat kategori sendiri."
          action={
            canEdit && (
              <Button busy={defaults.isPending} onClick={() => defaults.mutate(undefined, { onSuccess: () => toast('Kategori bawaan dibuat') })}>
                Buat kategori bawaan
              </Button>
            )
          }
        />
      )}
      {editing && <CategoryDialog me={me} c={editing === 'new' ? null : editing} groups={groups} onClose={() => setEditing(null)} />}
    </>
  )
}

function CategoryDialog({ me, c, groups, onClose }: { me: Me; c: AssetCategory | null; groups: FiscalGroup[]; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const save = useSaveCategory(companyId)
  const accounts = useAccounts(companyId).data?.items ?? []
  const toast = useToast()
  const [v, setV] = useState({
    name: c?.name ?? '',
    assetAccountId: c?.assetAccountId ?? '',
    accumAccountId: c?.accumAccountId ?? '',
    expenseAccountId: c?.expenseAccountId ?? '',
    method: (c?.method ?? 'garis_lurus') as DepMethod,
    years: c ? String(c.lifeMonths / 12) : '4',
    fiscalGroup: c?.fiscalGroup ?? 'kelompok_1',
    fiscalMethod: (c?.fiscalMethod ?? 'garis_lurus') as FiscalMethod,
    active: c?.active ?? true,
  })
  const err = save.error instanceof ApiError ? save.error : null
  const group = groups.find((g) => g.code === v.fiscalGroup)
  const depreciable = v.method !== 'tidak_disusutkan'
  const assets = accounts.filter((a) => a.class === 1)
  const expenses = accounts.filter((a) => a.class === 5 || a.class === 6 || a.class === 8)

  function submit() {
    save.mutate(
      {
        id: c?.id,
        input: {
          name: v.name,
          assetAccountId: v.assetAccountId,
          accumAccountId: depreciable ? v.accumAccountId || null : null,
          expenseAccountId: depreciable ? v.expenseAccountId || null : null,
          method: v.method,
          lifeMonths: depreciable ? Math.round(Number(v.years.replace(',', '.')) * 12) || 0 : 0,
          fiscalGroup: v.fiscalGroup,
          fiscalMethod: group && !group.declining ? 'garis_lurus' : v.fiscalMethod,
          active: v.active,
          version: c?.version ?? 0,
        },
      },
      { onSuccess: (x) => (toast(`Kategori ${x.name} disimpan`), onClose()) },
    )
  }

  const picker = (id: string, label: string, value: string, list: typeof accounts, onChange: (id: string) => void, field: string) => (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-ink-2">
        {label}
      </label>
      <AccountPicker id={id} accounts={list} value={value} onChange={onChange} invalid={Boolean(err?.fields[field])} />
      {err?.fields[field] && <p className="text-[13px] text-bad">{err.fields[field]}</p>}
    </div>
  )

  return (
    <Dialog
      open
      onClose={onClose}
      width={560}
      title={c ? `Ubah kategori ${c.name}` : 'Kategori baru'}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button busy={save.isPending} disabled={!v.name.trim() || !v.assetAccountId} onClick={submit}>
            Simpan
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field id="kat-nama" label="Nama kategori" value={v.name} maxLength={100} onChange={(e) => setV({ ...v, name: e.target.value })} error={err?.fields.name} />
        {picker('kat-aset', 'Akun aset', v.assetAccountId, assets, (x) => setV({ ...v, assetAccountId: x }), 'assetAccountId')}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <SelectField id="kat-metode" label="Metode penyusutan" value={v.method} onChange={(e) => setV({ ...v, method: e.target.value as DepMethod })} error={err?.fields.method}>
            <option value="garis_lurus">Garis lurus</option>
            <option value="saldo_menurun">Saldo menurun</option>
            <option value="tidak_disusutkan">Tidak disusutkan (mis. tanah)</option>
          </SelectField>
          {depreciable && (
            <Field id="kat-umur" label="Umur manfaat (tahun)" inputMode="decimal" value={v.years} onChange={(e) => setV({ ...v, years: e.target.value })} error={err?.fields.lifeMonths} />
          )}
        </div>
        {depreciable && picker('kat-akum', 'Akun akumulasi penyusutan', v.accumAccountId, assets, (x) => setV({ ...v, accumAccountId: x }), 'accumAccountId')}
        {depreciable && picker('kat-beban', 'Akun beban penyusutan', v.expenseAccountId, expenses, (x) => setV({ ...v, expenseAccountId: x }), 'expenseAccountId')}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <SelectField id="kat-fiskal" label="Kelompok harta fiskal" value={v.fiscalGroup} onChange={(e) => setV({ ...v, fiscalGroup: e.target.value })} error={err?.fields.fiscalGroup}>
            {groups.map((g) => (
              <option key={g.code} value={g.code}>
                {g.label}
              </option>
            ))}
          </SelectField>
          <SelectField
            id="kat-fiskal-metode"
            label="Metode fiskal"
            value={group && !group.declining ? 'garis_lurus' : v.fiscalMethod}
            disabled={group && !group.declining}
            onChange={(e) => setV({ ...v, fiscalMethod: e.target.value as FiscalMethod })}
            error={err?.fields.fiscalMethod}
          >
            <option value="garis_lurus">Garis lurus</option>
            <option value="saldo_menurun">Saldo menurun</option>
          </SelectField>
        </div>
        {c && <Checkbox checked={v.active} onChange={(x) => setV({ ...v, active: x })} label="Aktif" hint="Kategori nonaktif tidak bisa dipilih untuk aset baru." />}
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

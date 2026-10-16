import { useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type AdjustmentKind, type Me } from '../lib/api'
import { formatDate, todayISO } from '../lib/dates'
import { formatQty, useCreateAdjustment, useProducts, useStock } from '../lib/inventory'
import { useAccounts } from '../lib/ledger'
import { formatAmount, fromUnits, parseInput, toUnits } from '../lib/money'
import { useProjectOptions } from '../lib/projects'
import { can } from '../lib/session'
import { AccountPicker } from '../components/AccountPicker'
import { SearchSelect } from '../components/SearchSelect'
import { useToast } from '../components/Toast'
import { Alert, Button, Card, EmptyState, Field, Segmented, inputClass } from '../components/ui'
import { adjustmentKind } from './Inventory'

type Row = { key: string; productId: string; qty: string; counted: string; unitCost: string; memo: string }

const blank = (productId = ''): Row => ({ key: newIdempotencyKey(), productId, qty: '', counted: '', unitCost: '', memo: '' })

// Akun lawan default per jenis dokumen (kode template bagan akun).
const defaultAccount: Record<AdjustmentKind, string[]> = {
  penyesuaian: ['5-1003', '3-3001'],
  opname: ['5-1003'],
  pemakaian: ['5-1001'],
}

/** Dokumen persediaan baru: penyesuaian, pemakaian barang, atau stock opname (INV-04). */
export function AdjustmentForm({ me }: { me: Me }) {
  const [params] = useSearchParams()
  const raw = params.get('jenis')
  const kind: AdjustmentKind = raw === 'opname' || raw === 'pemakaian' ? raw : 'penyesuaian'
  return <AdjustmentFormFor key={kind} me={me} kind={kind} />
}

function AdjustmentFormFor({ me, kind }: { me: Me; kind: AdjustmentKind }) {
  const companyId = me.activeCompanyId
  const navigate = useNavigate()
  const toast = useToast()
  const accounts = useAccounts(companyId)
  const products = useProducts(companyId, { kind: 'barang' })
  const create = useCreateAdjustment(companyId)
  const [date, setDate] = useState(todayISO())
  const stock = useStock(companyId, date)
  const [accountId, setAccountId] = useState<string | null>(null)
  const [projectId, setProjectId] = useState('')
  const [memo, setMemo] = useState('')
  const [rows, setRows] = useState<Row[]>(() => [blank()])
  const [key] = useState(newIdempotencyKey)
  const [attempt, setAttempt] = useState(0)
  const projectOptions = useProjectOptions(companyId, can(me, 'project.view'), [projectId])

  const allAccounts = useMemo(() => accounts.data?.items ?? [], [accounts.data])
  const productList = useMemo(() => products.data ?? [], [products.data])
  const inventoryAccounts = new Set(productList.map((p) => p.inventoryAccountId))
  const counterAccounts = allAccounts.filter((a) => !inventoryAccounts.has(a.id))
  const fallback = defaultAccount[kind].map((c) => allAccounts.find((a) => a.code === c && a.postable)?.id).find(Boolean) ?? ''
  const counter = accountId ?? fallback
  const levels = useMemo(() => new Map((stock.data?.rows ?? []).map((r) => [r.productId, r])), [stock.data])
  const productOptions = useMemo(
    () => productList.map((p) => ({ id: p.id, label: `${p.code} · ${p.name}`, detail: `Stok ${formatQty(levels.get(p.id)?.qty ?? p.stockQty)} ${p.unit}` })),
    [productList, levels],
  )
  const byId = new Map(productList.map((p) => [p.id, p]))
  const err = create.error instanceof ApiError ? create.error : null
  const lineErr = (i: number, f: string) => err?.fields[`lines.${i}.${f}`]

  function setRow(k: string, patch: Partial<Row>) {
    setRows((list) => list.map((r) => (r.key === k ? { ...r, ...patch } : r)))
  }

  /** Stock opname: isi semua barang aktif sekaligus, hasil hitung dikosongkan. */
  function loadAll() {
    const chosen = new Set(rows.map((r) => r.productId))
    const extra = productList.filter((p) => p.active && !chosen.has(p.id)).map((p) => blank(p.id))
    setRows((list) => [...list.filter((r) => r.productId), ...extra])
  }

  // Selisih per baris, untuk pratinjau sebelum disimpan.
  function diffOf(r: Row): bigint | null {
    if (kind === 'opname') {
      const c = r.counted.trim() === '' ? null : parseInput(r.counted)
      if (c === null) return null
      return c - toUnits(levels.get(r.productId)?.qty ?? '0')
    }
    const q = parseInput(r.qty)
    if (q === null) return null
    return kind === 'pemakaian' ? -q : q
  }

  function submit() {
    setAttempt((n) => n + 1)
    const lines = rows
      .filter((r) => r.productId)
      .map((r) => {
        const cost = r.unitCost.trim() ? parseInput(r.unitCost) : null
        return {
          productId: r.productId,
          qty: kind === 'opname' ? '0' : fromUnits(parseInput(r.qty) ?? 0n),
          countedQty: kind === 'opname' && r.counted.trim() !== '' ? fromUnits(parseInput(r.counted) ?? 0n) : undefined,
          unitCost: cost !== null ? fromUnits(cost) : undefined,
          memo: r.memo.trim(),
        }
      })
    create.mutate(
      { idempotencyKey: key, input: { kind, date, accountId: counter, projectId: projectId || undefined, memo: memo.trim(), lines } },
      {
        onSuccess: (a) => {
          toast(`${a.number} diposting · nilai persediaan ${formatAmount(a.total)}`)
          navigate(`/persediaan?tab=dokumen&doc=${a.id}`)
        },
      },
    )
  }

  if (!can(me, 'inventory.create')) return <EmptyState title="Anda tidak punya izin membuat dokumen persediaan" />
  if (products.isSuccess && productList.length === 0)
    return (
      <EmptyState
        title="Belum ada barang"
        body="Dokumen persediaan butuh produk berjenis barang."
        action={
          <Link to="/produk" className="text-sm font-semibold text-accent">
            Buka Produk
          </Link>
        }
      />
    )

  const opname = kind === 'opname'
  return (
    <div className="flex flex-col gap-5">
      <div>
        <Link to="/persediaan?tab=dokumen" className="text-sm font-medium text-muted hover:text-ink">
          ← Persediaan
        </Link>
        <h1 className="mt-1 text-[26px] font-bold tracking-tight">{adjustmentKind[kind].label} baru</h1>
        <p className="mt-1.5 max-w-2xl text-sm text-muted">
          {adjustmentKind[kind].help} Langsung diposting saat disimpan; barang keluar dinilai dengan HPP rata-rata pada tanggalnya.
        </p>
      </div>
      <Segmented
        label="Jenis dokumen"
        value={kind}
        onChange={(k) => navigate(`/persediaan/baru?jenis=${k}`, { replace: true })}
        options={[
          { value: 'penyesuaian', label: 'Penyesuaian' },
          { value: 'pemakaian', label: 'Pemakaian' },
          { value: 'opname', label: 'Stock opname' },
        ]}
      />

      <Card>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
          <Field id="ps-tanggal" label="Tanggal" type="date" value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} hint={opname ? 'Stok sistem dihitung per akhir tanggal ini' : undefined} />
          <div className="flex min-w-0 flex-col gap-1.5">
            <label htmlFor="ps-akun" className="text-sm font-medium text-ink-2">
              {kind === 'pemakaian' ? 'Dibebankan ke akun' : 'Akun selisih'}
            </label>
            <AccountPicker id="ps-akun" accounts={counterAccounts} value={counter} onChange={setAccountId} invalid={Boolean(err?.fields.accountId)} />
            {err?.fields.accountId ? (
              <p className="text-[13px] text-bad">{err.fields.accountId}</p>
            ) : (
              kind === 'penyesuaian' && <p className="text-[13px] text-muted">Saldo awal: pilih 3-3001 Ekuitas saldo awal.</p>
            )}
          </div>
          {projectOptions.length > 0 && (
            <div className="flex min-w-0 flex-col gap-1.5">
              <label htmlFor="ps-proyek" className="text-sm font-medium text-ink-2">
                Proyek <span className="font-normal text-muted">(opsional)</span>
              </label>
              <SearchSelect id="ps-proyek" options={projectOptions} value={projectId} onChange={setProjectId} placeholder="Tanpa proyek" clearLabel="Tanpa proyek" />
              <p className="text-[13px] text-muted">Biayanya masuk ke laba proyek ini.</p>
            </div>
          )}
        </div>
        <Field id="ps-keterangan" label="Keterangan" value={memo} maxLength={500} onChange={(e) => setMemo(e.target.value)} error={err?.fields.memo} />
      </Card>

      <div className="overflow-hidden rounded-[14px] border border-line bg-surface">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[860px] text-left text-sm">
            <thead className="bg-hover/60 text-[12.5px] text-muted">
              <tr>
                <th className="px-2 py-3 pl-4 font-semibold">Barang</th>
                <th className="w-[130px] px-2 py-3 text-right font-semibold">Stok sistem</th>
                <th className="w-[150px] px-2 py-3 text-right font-semibold">{opname ? 'Hasil hitung' : kind === 'pemakaian' ? 'Jumlah dipakai' : 'Selisih (+/−)'}</th>
                {opname && <th className="w-[110px] px-2 py-3 text-right font-semibold">Selisih</th>}
                {kind !== 'pemakaian' && <th className="w-[150px] px-2 py-3 text-right font-semibold">HPP satuan masuk</th>}
                <th className="px-2 py-3 font-semibold">Keterangan</th>
                <th className="w-12 px-2 py-3">
                  <span className="sr-only">Hapus</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => {
                const p = byId.get(r.productId)
                const level = levels.get(r.productId)
                const diff = diffOf(r)
                const incoming = diff !== null && diff > 0n
                return (
                  <tr key={r.key} className="border-t border-line align-top">
                    <td className="min-w-[240px] px-2 py-2 pl-4">
                      <SearchSelect
                        label={`Barang baris ${i + 1}`}
                        options={productOptions}
                        value={r.productId}
                        onChange={(id) => setRow(r.key, { productId: id })}
                        placeholder="Cari barang"
                        invalid={Boolean(lineErr(i, 'productId'))}
                      />
                      {lineErr(i, 'productId') && <p className="mt-1 text-[13px] text-bad">{lineErr(i, 'productId')}</p>}
                    </td>
                    <td className="num px-2 pt-5 text-right text-ink-2">{p ? `${formatQty(level?.qty ?? '0')} ${p.unit}` : '—'}</td>
                    <td className="px-2 py-2">
                      <input
                        aria-label={`${opname ? 'Hasil hitung' : 'Jumlah'} baris ${i + 1}`}
                        inputMode="decimal"
                        placeholder={opname ? 'hitung fisik' : '0'}
                        value={opname ? r.counted : r.qty}
                        onChange={(e) => setRow(r.key, opname ? { counted: e.target.value } : { qty: e.target.value })}
                        className={`${inputClass} num text-right ${lineErr(i, opname ? 'countedQty' : 'qty') || (r.productId && diff === null && (opname ? r.counted : r.qty)) ? 'border-bad' : 'border-line-2'}`}
                      />
                      {lineErr(i, opname ? 'countedQty' : 'qty') && <p className="mt-1 text-[13px] text-bad">{lineErr(i, opname ? 'countedQty' : 'qty')}</p>}
                    </td>
                    {opname && (
                      <td className={`num px-2 pt-5 text-right ${diff === null ? 'text-muted' : diff < 0n ? 'text-bad' : diff > 0n ? 'text-good' : 'text-muted'}`}>
                        {diff === null ? '—' : diff === 0n ? 'cocok' : `${diff > 0n ? '+' : ''}${formatQty(fromUnits(diff))}`}
                      </td>
                    )}
                    {kind !== 'pemakaian' && (
                      <td className="px-2 py-2">
                        <input
                          aria-label={`HPP satuan baris ${i + 1}`}
                          inputMode="decimal"
                          disabled={!incoming}
                          placeholder={incoming ? (level && toUnits(level.qty) > 0n ? formatAmount(level.avgCost) : p ? formatAmount(p.purchasePrice) : '') : '—'}
                          value={incoming ? r.unitCost : ''}
                          onChange={(e) => setRow(r.key, { unitCost: e.target.value })}
                          className={`${inputClass} num text-right disabled:bg-hover/50 ${lineErr(i, 'unitCost') ? 'border-bad' : 'border-line-2'}`}
                        />
                        {lineErr(i, 'unitCost') && <p className="mt-1 text-[13px] text-bad">{lineErr(i, 'unitCost')}</p>}
                      </td>
                    )}
                    <td className="min-w-[160px] px-2 py-2">
                      <input aria-label={`Keterangan baris ${i + 1}`} value={r.memo} maxLength={250} onChange={(e) => setRow(r.key, { memo: e.target.value })} className={`${inputClass} border-line-2`} />
                    </td>
                    <td className="px-2 py-2">
                      <button
                        type="button"
                        aria-label={`Hapus baris ${i + 1}`}
                        disabled={rows.length <= 1}
                        onClick={() => setRows((list) => list.filter((x) => x.key !== r.key))}
                        className="flex size-11 cursor-pointer items-center justify-center rounded-[10px] text-muted transition-colors hover:bg-hover hover:text-bad disabled:cursor-not-allowed disabled:opacity-30"
                      >
                        ×
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
        <div className="flex flex-wrap items-center gap-3 border-t border-line px-4 py-3">
          <Button variant="ghost" size="sm" onClick={() => setRows((list) => [...list, blank()])}>
            + Tambah barang
          </Button>
          {opname && (
            <Button variant="ghost" size="sm" onClick={loadAll}>
              Isi semua barang
            </Button>
          )}
          <span className="ml-auto text-[13px] text-muted">HPP satuan masuk kosong = HPP rata-rata pada tanggal itu.</span>
        </div>
      </div>

      {err && <Alert shakeKey={attempt}>{err.message}</Alert>}

      <div className="sticky bottom-0 z-10 -mx-4 flex flex-wrap items-center justify-end gap-2.5 border-t border-line bg-surface/95 px-4 py-4 backdrop-blur sm:-mx-8 sm:px-8">
        <span className="mr-auto text-sm text-muted">{date && `Tanggal ${formatDate(date)}`}</span>
        <Button variant="secondary" onClick={() => navigate('/persediaan?tab=dokumen')}>
          Batal
        </Button>
        <Button busy={create.isPending} disabled={!counter || !rows.some((r) => r.productId)} onClick={submit}>
          Simpan & posting
        </Button>
      </div>
    </div>
  )
}

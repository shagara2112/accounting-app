import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router'
import { ApiError, type Account, type Me, type Product, type ProductInput, type ProductKind } from '../lib/api'
import { useDebounced } from '../lib/hooks'
import { formatQty, useProducts, useSaveProduct } from '../lib/inventory'
import { useAccounts } from '../lib/ledger'
import { formatAmount, fromUnits, parseInput } from '../lib/money'
import { activeCompany, can } from '../lib/session'
import { AccountPicker } from '../components/AccountPicker'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Checkbox, Dialog, EmptyState, Field, PageHeader, Segmented, Skeleton, TableShell, inputClass, td, th } from '../components/ui'

/** Master produk dan jasa (INV-01): dipakai di faktur dan tagihan supaya harga, akun, dan stok terisi otomatis. */
export function Products({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const navigate = useNavigate()
  const [kind, setKind] = useState<'' | ProductKind>('')
  const [search, setSearch] = useState('')
  const [inactive, setInactive] = useState(false)
  const q = useDebounced(search.trim())
  const list = useProducts(companyId, { kind: kind || undefined, q, all: inactive })
  const [editing, setEditing] = useState<Product | 'new' | null>(null)
  const canCreate = can(me, 'inventory.create')
  const items = list.data ?? []

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Produk dan jasa"
        subtitle="Barang dilacak stoknya dengan HPP rata-rata tertimbang; jasa hanya membawa harga dan akun default. Pilih produk di faktur atau tagihan supaya harga, akun, dan stok terisi otomatis."
        actions={canCreate && <Button onClick={() => setEditing('new')}>Produk baru</Button>}
      />
      <div className="flex flex-wrap items-center gap-3">
        <Segmented
          label="Jenis"
          value={kind}
          onChange={setKind}
          options={[
            { value: '', label: 'Semua' },
            { value: 'barang', label: 'Barang' },
            { value: 'jasa', label: 'Jasa' },
          ]}
        />
        <input type="search" aria-label="Cari kode atau nama" placeholder="Cari kode atau nama" value={search} onChange={(e) => setSearch(e.target.value)} className={`${inputClass} max-w-[280px] flex-1 border-line-2`} />
        <Checkbox checked={inactive} onChange={setInactive} label="Tampilkan nonaktif" />
      </div>
      <TableShell minWidth={900}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Produk</th>
            <th className={th}>Jenis</th>
            <th className={`${th} text-right`}>Harga jual</th>
            <th className={`${th} text-right`}>Harga beli</th>
            <th className={`${th} text-right`}>Stok</th>
            <th className={`${th} text-right`}>HPP rata-rata</th>
            <th className={`${th} text-right`}>Nilai stok</th>
          </tr>
        </thead>
        <tbody>
          {list.isPending && (
            <tr>
              <td colSpan={7} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {items.map((p) => (
            <tr
              key={p.id}
              onClick={canCreate ? () => setEditing(p) : undefined}
              className={`border-t border-line ${canCreate ? 'cursor-pointer transition-colors duration-150 hover:bg-hover' : ''} ${p.active ? '' : 'text-muted'}`}
            >
              <td className={td}>
                <span className="num block text-[12.5px] text-muted">{p.code}</span>
                <span className="font-medium">{p.name}</span>
                {!p.active && (
                  <Badge tone="neutral" className="ml-2">
                    Nonaktif
                  </Badge>
                )}
              </td>
              <td className={td}>
                <Badge tone={p.kind === 'barang' ? 'accent' : 'neutral'}>{p.kind === 'barang' ? 'Barang' : 'Jasa'}</Badge>
              </td>
              <td className={`${td} num text-right`}>{formatAmount(p.salePrice, { blankZero: true }) || '—'}</td>
              <td className={`${td} num text-right`}>{formatAmount(p.purchasePrice, { blankZero: true }) || '—'}</td>
              <td className={`${td} num text-right whitespace-nowrap`}>
                {p.kind === 'barang' ? (
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation()
                      navigate(`/persediaan?produk=${p.id}`)
                    }}
                    className="cursor-pointer font-semibold text-accent hover:underline"
                    title="Buka kartu stok"
                  >
                    {formatQty(p.stockQty)} {p.unit}
                  </button>
                ) : (
                  '—'
                )}
              </td>
              <td className={`${td} num text-right`}>{p.kind === 'barang' ? formatAmount(p.avgCost) : '—'}</td>
              <td className={`${td} num text-right`}>{p.kind === 'barang' ? formatAmount(p.stockValue) : '—'}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && (
        <EmptyState
          title={q ? 'Tidak ada produk yang cocok' : 'Belum ada produk'}
          body="Tambahkan barang dagang, material, atau jasa yang sering ditagih. Faktur dan tagihan tetap bisa dibuat tanpa produk."
          action={canCreate && !q && <Button onClick={() => setEditing('new')}>Produk baru</Button>}
        />
      )}
      <ProductDialog me={me} editing={editing} onClose={() => setEditing(null)} />
    </div>
  )
}

type Form = {
  code: string
  name: string
  kind: ProductKind
  unit: string
  salePrice: string
  purchasePrice: string
  revenueAccountId: string
  expenseAccountId: string
  inventoryAccountId: string
  active: boolean
  version: number
}

function toForm(p: Product): Form {
  return {
    code: p.code,
    name: p.name,
    kind: p.kind,
    unit: p.unit,
    salePrice: formatAmount(p.salePrice, { blankZero: true }),
    purchasePrice: formatAmount(p.purchasePrice, { blankZero: true }),
    revenueAccountId: p.revenueAccountId ?? '',
    expenseAccountId: p.expenseAccountId ?? '',
    inventoryAccountId: p.inventoryAccountId ?? '',
    active: p.active,
    version: p.version,
  }
}

/** Akun default dari bagan akun template industri PT. */
function defaults(accounts: Account[], industry?: string): Form {
  const id = (code: string) => accounts.find((a) => a.code === code && a.postable)?.id ?? ''
  return {
    code: '',
    name: '',
    kind: industry === 'jasa' ? 'jasa' : 'barang',
    unit: industry === 'jasa' ? 'jam' : 'pcs',
    salePrice: '',
    purchasePrice: '',
    revenueAccountId: id('4-1001'),
    expenseAccountId: id('5-1001'),
    inventoryAccountId: id(industry === 'konstruksi' ? '1-1301' : '1-1302'),
    active: true,
    version: 0,
  }
}

function ProductDialog({ me, editing, onClose }: { me: Me; editing: Product | 'new' | null; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const accounts = useAccounts(companyId)
  const save = useSaveProduct(companyId)
  const toast = useToast()
  const [shownFor, setShownFor] = useState<Product | 'new' | null>(null)
  const [form, setForm] = useState<Form | null>(null)
  const [attempt, setAttempt] = useState(0)
  const all = useMemo(() => accounts.data?.items ?? [], [accounts.data])

  if (editing && shownFor !== editing && accounts.data) {
    setShownFor(editing)
    setForm(editing === 'new' ? defaults(all, activeCompany(me)?.industry) : toForm(editing))
    save.reset()
  }
  const isNew = (editing ?? shownFor) === 'new'
  const err = save.error instanceof ApiError ? save.error : null
  const set = (patch: Partial<Form>) => setForm((f) => (f ? { ...f, ...patch } : f))
  const byClass = (classes: number[]) => all.filter((a) => classes.includes(a.class))
  const price = (v: string) => parseInput(v)

  function close() {
    setShownFor(null)
    onClose()
  }

  function submit() {
    if (!form) return
    setAttempt((n) => n + 1)
    const input: ProductInput = {
      code: form.code.trim(),
      name: form.name.trim(),
      kind: form.kind,
      unit: form.unit.trim(),
      salePrice: fromUnits(price(form.salePrice) ?? 0n),
      purchasePrice: fromUnits(price(form.purchasePrice) ?? 0n),
      revenueAccountId: form.revenueAccountId || null,
      expenseAccountId: form.expenseAccountId || null,
      inventoryAccountId: form.kind === 'barang' ? form.inventoryAccountId || null : null,
      active: form.active,
      version: form.version,
    }
    save.mutate(
      { id: shownFor && shownFor !== 'new' ? shownFor.id : undefined, input },
      { onSuccess: (p) => (toast(`${p.code} ${p.name} disimpan`), close()) },
    )
  }

  const goods = form?.kind === 'barang'
  const hasStock = shownFor && shownFor !== 'new' && shownFor.kind === 'barang' && shownFor.stockQty !== '0.0000'
  return (
    <Dialog
      open={editing !== null}
      onClose={close}
      width={640}
      title={isNew ? 'Produk baru' : `Ubah ${form?.code ?? ''}`}
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button busy={save.isPending} disabled={!form?.code.trim() || !form?.name.trim()} onClick={submit}>
            Simpan
          </Button>
        </>
      }
    >
      {!form ? (
        <Skeleton className="h-60" />
      ) : (
        <>
          <Segmented
            label="Jenis produk"
            value={form.kind}
            onChange={(k) => set({ kind: k })}
            options={[
              { value: 'barang', label: 'Barang (dilacak stoknya)' },
              { value: 'jasa', label: 'Jasa' },
            ]}
          />
          {hasStock && <p className="-mt-2 text-[13px] text-muted">Produk ini sudah punya pergerakan stok; jenis dan akun persediaannya tidak bisa diubah.</p>}
          <div className="grid grid-cols-[160px_1fr] gap-4 max-sm:grid-cols-1">
            <Field id="p-kode" label="Kode" data-autofocus className="num" value={form.code} maxLength={40} onChange={(e) => set({ code: e.target.value })} error={err?.fields.code} />
            <Field id="p-nama" label="Nama" value={form.name} maxLength={200} onChange={(e) => set({ name: e.target.value })} error={err?.fields.name} />
          </div>
          <div className="grid grid-cols-3 gap-4 max-sm:grid-cols-1">
            <Field id="p-satuan" label="Satuan" placeholder="pcs, kg, sak, jam" value={form.unit} maxLength={20} onChange={(e) => set({ unit: e.target.value })} error={err?.fields.unit} />
            <Field
              id="p-jual"
              label="Harga jual"
              inputMode="decimal"
              className="num text-right"
              placeholder="0"
              value={form.salePrice}
              onChange={(e) => set({ salePrice: e.target.value })}
              error={err?.fields.salePrice ?? (price(form.salePrice) === null ? 'Nominal tidak terbaca.' : undefined)}
            />
            <Field
              id="p-beli"
              label="Harga beli"
              inputMode="decimal"
              className="num text-right"
              placeholder="0"
              value={form.purchasePrice}
              onChange={(e) => set({ purchasePrice: e.target.value })}
              error={err?.fields.purchasePrice ?? (price(form.purchasePrice) === null ? 'Nominal tidak terbaca.' : undefined)}
              hint={goods ? 'Dipakai sebagai harga pokok bila stok masih kosong' : undefined}
            />
          </div>
          <AccountField id="p-pendapatan" label="Akun pendapatan" accounts={byClass([4, 7])} value={form.revenueAccountId} onChange={(v) => set({ revenueAccountId: v })} error={err?.fields.revenueAccountId} />
          {goods ? (
            <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
              <AccountField id="p-persediaan" label="Akun persediaan" accounts={byClass([1])} value={form.inventoryAccountId} onChange={(v) => set({ inventoryAccountId: v })} error={err?.fields.inventoryAccountId} />
              <AccountField id="p-hpp" label="Akun HPP" accounts={byClass([5, 6, 8])} value={form.expenseAccountId} onChange={(v) => set({ expenseAccountId: v })} error={err?.fields.expenseAccountId} />
            </div>
          ) : (
            <AccountField id="p-beban" label="Akun beban saat dibeli (opsional)" accounts={byClass([1, 5, 6, 8])} value={form.expenseAccountId} onChange={(v) => set({ expenseAccountId: v })} error={err?.fields.expenseAccountId} />
          )}
          {!isNew && <Checkbox checked={form.active} onChange={(v) => set({ active: v })} label="Produk aktif" hint="Produk nonaktif tidak bisa dipilih di faktur, tagihan, atau dokumen persediaan baru." />}
          {err && Object.keys(err.fields).length === 0 && <Alert shakeKey={attempt}>{err.message}</Alert>}
        </>
      )}
    </Dialog>
  )
}

function AccountField({ id, label, accounts, value, onChange, error }: { id: string; label: string; accounts: Account[]; value: string; onChange: (v: string) => void; error?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-ink-2">
        {label}
      </label>
      <AccountPicker id={id} accounts={accounts} value={value} onChange={onChange} invalid={Boolean(error)} />
      {error && <p className="text-[13px] text-bad">{error}</p>}
    </div>
  )
}

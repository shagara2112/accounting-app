import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { ApiError, type Me } from '../lib/api'
import {
  lifeText,
  useAsset,
  useAssetCategories,
  useAssetPreview,
  useSaveAsset,
  type AssetCategory,
  type AssetDetail,
  type AssetInput,
  type DepMethod,
  type FiscalMethod,
} from '../lib/assets'
import { monthLabel, monthOf, todayISO } from '../lib/dates'
import { useDebounced } from '../lib/hooks'
import { useAccounts } from '../lib/ledger'
import { formatAmount, fromUnits, parseInput } from '../lib/money'
import { can } from '../lib/session'
import { AccountPicker } from '../components/AccountPicker'
import { DimensionFields, type DimValue } from '../components/DimensionFields'
import { useToast } from '../components/Toast'
import { Alert, Button, Card, Checkbox, EmptyState, Field, SelectField, Skeleton, TextAreaField } from '../components/ui'

/** Mendaftarkan atau mengubah aset tetap (FA-01). */
export function AssetForm({ me }: { me: Me }) {
  const { id } = useParams()
  const asset = useAsset(me.activeCompanyId, id ?? null)
  if (!can(me, 'asset.create')) return <EmptyState title="Anda tidak punya izin mendaftarkan aset" />
  if (!id) return <AssetFormFor me={me} />
  if (asset.isError) return <EmptyState title="Aset tidak ditemukan" body={asset.error.message} />
  if (!asset.data || asset.data.id !== id) return <Skeleton className="h-96" />
  return <AssetFormFor key={`${id}-${asset.data.version}`} me={me} existing={asset.data} />
}

const amountText = (s?: string) => (s && parseInput(s) !== 0n ? formatAmount(s) : '')

function AssetFormFor({ me, existing }: { me: Me; existing?: AssetDetail }) {
  const companyId = me.activeCompanyId
  const navigate = useNavigate()
  const toast = useToast()
  const cats = useAssetCategories(companyId)
  const accounts = useAccounts(companyId).data?.items ?? []
  const save = useSaveAsset(companyId)
  const locked = existing?.locked ?? false
  const [v, setV] = useState({
    code: existing?.code ?? '',
    name: existing?.name ?? '',
    categoryId: existing?.categoryId ?? '',
    acquiredOn: existing?.acquiredOn ?? todayISO(),
    cost: amountText(existing?.cost),
    residual: amountText(existing?.residual),
    method: (existing?.method ?? 'garis_lurus') as DepMethod,
    years: existing ? String(existing.lifeMonths / 12) : '',
    start: existing ? monthOf(existing.depreciationStart) : '',
    assetAccountId: existing?.assetAccountId ?? '',
    accumAccountId: existing?.accumAccountId ?? '',
    expenseAccountId: existing?.expenseAccountId ?? '',
    fiscalGroup: existing?.fiscalGroup ?? '',
    fiscalMethod: (existing?.fiscalMethod ?? 'garis_lurus') as FiscalMethod,
    openingAccum: amountText(existing?.openingAccum),
    openingMonth: existing?.openingMonth ? monthOf(existing.openingMonth) : '',
    location: existing?.location ?? '',
    custodian: existing?.custodian ?? '',
    notes: existing?.notes ?? '',
  })
  const [dims, setDims] = useState<DimValue>({ branchId: existing?.branchId ?? '', departmentId: existing?.departmentId ?? '' })
  const [showOpening, setShowOpening] = useState(Boolean(existing?.openingMonth))
  const err = save.error instanceof ApiError ? save.error : null
  const categories = (cats.data?.items ?? []).filter((c) => c.active || c.id === v.categoryId)
  const groups = cats.data?.fiscalGroups ?? []
  const group = groups.find((g) => g.code === v.fiscalGroup)
  const depreciable = v.method !== 'tidak_disusutkan'

  function chooseCategory(c: AssetCategory | undefined) {
    if (!c) return setV({ ...v, categoryId: '' })
    setV({
      ...v,
      categoryId: c.id,
      method: c.method,
      years: c.method === 'tidak_disusutkan' ? '' : String(c.lifeMonths / 12),
      assetAccountId: c.assetAccountId,
      accumAccountId: c.accumAccountId ?? '',
      expenseAccountId: c.expenseAccountId ?? '',
      fiscalGroup: c.fiscalGroup,
      fiscalMethod: c.fiscalMethod,
    })
  }

  const cost = parseInput(v.cost)
  const input: AssetInput = {
    code: v.code.trim(),
    name: v.name.trim(),
    categoryId: v.categoryId,
    acquiredOn: v.acquiredOn || undefined,
    cost: fromUnits(cost ?? 0n),
    residual: fromUnits(parseInput(v.residual) ?? 0n),
    method: v.method,
    lifeMonths: depreciable ? Math.round(Number(v.years.replace(',', '.')) * 12) || 0 : 0,
    depreciationStart: v.start ? `${v.start}-01` : undefined,
    assetAccountId: v.assetAccountId || undefined,
    accumAccountId: depreciable ? v.accumAccountId || undefined : undefined,
    expenseAccountId: depreciable ? v.expenseAccountId || undefined : undefined,
    fiscalGroup: v.fiscalGroup,
    fiscalMethod: group && !group.declining ? 'garis_lurus' : v.fiscalMethod,
    openingAccum: showOpening ? fromUnits(parseInput(v.openingAccum) ?? 0n) : '0',
    openingMonth: showOpening && v.openingMonth ? `${v.openingMonth}-01` : undefined,
    location: v.location.trim(),
    custodian: v.custodian.trim(),
    notes: v.notes.trim(),
    branchId: dims.branchId || undefined,
    departmentId: dims.departmentId || undefined,
    version: existing?.version ?? 0,
  }
  const debounced = useDebounced(input, 400)
  const previewReady = debounced.categoryId !== '' && Boolean(debounced.acquiredOn) && (parseInput(debounced.cost) ?? 0n) > 0n
  const preview = useAssetPreview(companyId, previewReady ? debounced : null)
  const p = previewReady ? preview.data : undefined

  function submit(e?: FormEvent) {
    e?.preventDefault()
    save.mutate(
      { id: existing?.id, input },
      {
        onSuccess: (x) => {
          toast(existing ? `${x.code} disimpan` : `${x.code} didaftarkan`)
          navigate(`/aset?id=${x.id}`)
        },
      },
    )
  }

  if (cats.isSuccess && cats.data.items.length === 0) {
    return (
      <EmptyState
        title="Belum ada kategori aset"
        body="Buat kategori dulu (atau kategori bawaan dari bagan akun), karena kategori mengisi akun dan metode penyusutan aset."
        action={<Button onClick={() => navigate('/aset?tab=kategori')}>Atur kategori</Button>}
      />
    )
  }

  const assetAccounts = accounts.filter((a) => a.class === 1)
  const expenseAccounts = accounts.filter((a) => a.class === 5 || a.class === 6 || a.class === 8)
  const picker = (id: string, label: string, value: string, list: typeof accounts, onChange: (x: string) => void, field: string) => (
    <div className="flex min-w-0 flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-ink-2">
        {label}
      </label>
      {locked ? (
        <p className="min-h-11 rounded-[10px] border border-line bg-hover/40 px-3 py-2.5 text-[15px] text-ink-2">
          {list.find((a) => a.id === value)?.code} {list.find((a) => a.id === value)?.name}
        </p>
      ) : (
        <AccountPicker id={id} accounts={list} value={value} onChange={onChange} invalid={Boolean(err?.fields[field])} />
      )}
      {err?.fields[field] && <p className="text-[13px] text-bad">{err.fields[field]}</p>}
    </div>
  )

  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-5">
      <div>
        <Link to={existing ? `/aset?id=${existing.id}` : '/aset'} className="text-sm font-medium text-muted hover:text-ink">
          ← Aset tetap
        </Link>
        <h1 className="mt-1 text-[26px] font-bold tracking-tight">{existing ? `Ubah ${existing.code} ${existing.name}` : 'Aset baru'}</h1>
        <p className="mt-1.5 max-w-2xl text-sm text-muted">
          Mendaftarkan aset tidak membuat jurnal perolehan: aset dibeli lewat tagihan, kas, jurnal, atau saldo awal ke akun aset. Register ini mencatat rinciannya dan menjurnal penyusutannya setiap
          bulan.
        </p>
      </div>

      <Card title="Identitas">
        <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
          <SelectField
            id="aset-kategori"
            label="Kategori"
            value={v.categoryId}
            disabled={locked}
            onChange={(e) => chooseCategory(categories.find((c) => c.id === e.target.value))}
            error={err?.fields.categoryId}
          >
            <option value="">Pilih kategori</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </SelectField>
          <Field id="aset-kode" label="Kode" placeholder="Otomatis, mis. AT-0001" value={v.code} maxLength={30} onChange={(e) => setV({ ...v, code: e.target.value })} error={err?.fields.code} />
          <div className="sm:col-span-2">
            <Field id="aset-nama" label="Nama aset" placeholder="Mis. Laptop tim keuangan" value={v.name} maxLength={200} onChange={(e) => setV({ ...v, name: e.target.value })} error={err?.fields.name} />
          </div>
          <Field id="aset-lokasi" label="Lokasi (opsional)" value={v.location} maxLength={200} onChange={(e) => setV({ ...v, location: e.target.value })} error={err?.fields.location} />
          <Field id="aset-pj" label="Penanggung jawab (opsional)" value={v.custodian} maxLength={200} onChange={(e) => setV({ ...v, custodian: e.target.value })} />
          <DimensionFields me={me} value={dims} onChange={setDims} errors={err?.fields} />
        </div>
        <TextAreaField id="aset-catatan" label="Catatan (opsional)" rows={2} maxLength={1000} value={v.notes} onChange={(e) => setV({ ...v, notes: e.target.value })} error={err?.fields.notes} />
      </Card>

      <Card title="Nilai dan penyusutan" subtitle={locked ? 'Penyusutan aset ini sudah dijurnal, sehingga nilai, umur, metode, dan akunnya terkunci. Void jurnal penyusutannya dulu bila perlu koreksi.' : undefined}>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(200px,1fr))] gap-4">
          <Field id="aset-tanggal" label="Tanggal perolehan" type="date" disabled={locked} value={v.acquiredOn} onChange={(e) => setV({ ...v, acquiredOn: e.target.value })} error={err?.fields.acquiredOn} />
          <Field
            id="aset-harga"
            label="Harga perolehan"
            inputMode="decimal"
            className="num text-right"
            disabled={locked}
            value={v.cost}
            onChange={(e) => setV({ ...v, cost: e.target.value })}
            onBlur={() => cost !== null && cost > 0n && setV({ ...v, cost: formatAmount(cost) })}
            error={err?.fields.cost}
          />
          <SelectField id="aset-metode" label="Metode" disabled={locked} value={v.method} onChange={(e) => setV({ ...v, method: e.target.value as DepMethod })} error={err?.fields.method}>
            <option value="garis_lurus">Garis lurus</option>
            <option value="saldo_menurun">Saldo menurun</option>
            <option value="tidak_disusutkan">Tidak disusutkan</option>
          </SelectField>
          {depreciable && (
            <>
              <Field
                id="aset-umur"
                label="Umur manfaat (tahun)"
                inputMode="decimal"
                disabled={locked}
                value={v.years}
                onChange={(e) => setV({ ...v, years: e.target.value })}
                error={err?.fields.lifeMonths}
              />
              <Field
                id="aset-sisa"
                label="Nilai sisa (opsional)"
                inputMode="decimal"
                className="num text-right"
                disabled={locked}
                value={v.residual}
                onChange={(e) => setV({ ...v, residual: e.target.value })}
                error={err?.fields.residual}
              />
              <Field
                id="aset-mulai"
                label="Mulai disusutkan"
                type="month"
                disabled={locked}
                value={v.start || monthOf(v.acquiredOn || todayISO())}
                onChange={(e) => setV({ ...v, start: e.target.value })}
                error={err?.fields.depreciationStart}
                hint="Bawaan: bulan perolehan."
              />
            </>
          )}
        </div>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(260px,1fr))] gap-4">
          {picker('aset-akun', 'Akun aset', v.assetAccountId, assetAccounts, (x) => setV({ ...v, assetAccountId: x }), 'assetAccountId')}
          {depreciable && picker('aset-akum', 'Akun akumulasi penyusutan', v.accumAccountId, assetAccounts, (x) => setV({ ...v, accumAccountId: x }), 'accumAccountId')}
          {depreciable && picker('aset-beban', 'Akun beban penyusutan', v.expenseAccountId, expenseAccounts, (x) => setV({ ...v, expenseAccountId: x }), 'expenseAccountId')}
        </div>
        {depreciable && !locked && (
          <div className="flex flex-col gap-3 rounded-[12px] border border-line p-3">
            <Checkbox
              checked={showOpening}
              onChange={setShowOpening}
              label="Aset lama yang sudah disusutkan sebelum memakai Lajur"
              hint="Isi akumulasi penyusutannya; jadwal dilanjutkan dari nilai buku sisanya."
            />
            {showOpening && (
              <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
                <Field
                  id="aset-akum-awal"
                  label="Akumulasi penyusutan"
                  inputMode="decimal"
                  className="num text-right"
                  value={v.openingAccum}
                  onChange={(e) => setV({ ...v, openingAccum: e.target.value })}
                  error={err?.fields.openingAccum}
                />
                <Field
                  id="aset-akum-bulan"
                  label="Sampai dengan bulan"
                  type="month"
                  value={v.openingMonth}
                  onChange={(e) => setV({ ...v, openingMonth: e.target.value })}
                  error={err?.fields.openingMonth}
                  hint="Penyusutan dilanjutkan mulai bulan sesudahnya."
                />
              </div>
            )}
          </div>
        )}
        {locked && existing && Number(existing.openingAccum) > 0 && existing.openingMonth && (
          <p className="text-[13px] text-muted">
            Akumulasi awal Rp {formatAmount(existing.openingAccum)} s.d. {monthLabel(monthOf(existing.openingMonth))}.
          </p>
        )}
      </Card>

      <Card title="Penyusutan fiskal" subtitle="Untuk koreksi fiskal dan lampiran SPT Tahunan; tidak dijurnal.">
        <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
          <SelectField id="aset-kelompok" label="Kelompok harta" value={v.fiscalGroup} onChange={(e) => setV({ ...v, fiscalGroup: e.target.value })} error={err?.fields.fiscalGroup}>
            <option value="">Pilih kelompok</option>
            {groups.map((g) => (
              <option key={g.code} value={g.code}>
                {g.label}
              </option>
            ))}
          </SelectField>
          <SelectField
            id="aset-fiskal-metode"
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
      </Card>

      {p && (p.schedule.length > 0 || p.fiscal.length > 0) && (
        <Card
          title="Pratinjau jadwal"
          subtitle={
            p.schedule.length > 0
              ? `Penyusutan bulan pertama Rp ${formatAmount(p.monthly)}${input.lifeMonths > 0 ? ` · umur ${lifeText(input.lifeMonths)}` : ''}`
              : 'Aset ini tidak disusutkan secara komersial.'
          }
        >
          <div className="overflow-x-auto rounded-[12px] border border-line">
            <table className="w-full min-w-[480px] text-left text-sm">
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="px-3 py-2.5 font-semibold">Tahun</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Komersial</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Nilai buku</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Fiskal</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Nilai sisa fiskal</th>
                </tr>
              </thead>
              <tbody>
                {[...new Set([...p.schedule.map((y) => y.year), ...p.fiscal.map((y) => y.year)])]
                  .sort((a, b) => a - b)
                  .map((y) => {
                    const c = p.schedule.find((s) => s.year === y)
                    const f = p.fiscal.find((s) => s.year === y)
                    return (
                      <tr key={y} className="border-t border-line">
                        <td className="num px-3 py-2">{y}</td>
                        <td className="num px-3 py-2 text-right">{c ? formatAmount(c.amount) : '—'}</td>
                        <td className="num px-3 py-2 text-right text-muted">{c ? formatAmount(c.bookValue) : ''}</td>
                        <td className="num px-3 py-2 text-right">{f ? formatAmount(f.amount) : '—'}</td>
                        <td className="num px-3 py-2 text-right text-muted">{f ? formatAmount(f.bookValue) : ''}</td>
                      </tr>
                    )
                  })}
              </tbody>
            </table>
          </div>
        </Card>
      )}

      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}

      <div className="sticky bottom-0 z-10 -mx-4 flex justify-end gap-2.5 border-t border-line bg-surface/95 px-4 py-4 backdrop-blur sm:-mx-8 sm:px-8">
        <Button variant="secondary" onClick={() => navigate(existing ? `/aset?id=${existing.id}` : '/aset')}>
          Batal
        </Button>
        <Button type="submit" busy={save.isPending} disabled={!v.name.trim() || !v.categoryId}>
          {existing ? 'Simpan perubahan' : 'Daftarkan aset'}
        </Button>
      </div>
    </form>
  )
}

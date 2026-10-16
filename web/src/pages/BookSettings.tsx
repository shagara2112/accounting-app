import { useState } from 'react'
import { useSearchParams } from 'react-router'
import { ApiError, type Me } from '../lib/api'
import { useDimensions, useNumberFormats, useSaveDimension, useSaveNumberFormat, type DimKind, type Dimension, type NumberFormat } from '../lib/dimensions'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Checkbox, Dialog, EmptyState, Field, PageHeader, SelectField, Skeleton, TableShell, Tabs, td, th } from '../components/ui'

const resetLabel: Record<NumberFormat['reset'], string> = { month: 'Tiap bulan', year: 'Tiap tahun', never: 'Tidak pernah' }

/** Dimensi cabang dan departemen (GL-09) serta format nomor dokumen (GL-07). */
export function BookSettings({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'cabang'
  if (!can(me, 'ledger.view')) return <EmptyState title="Anda tidak punya izin melihat pengaturan buku" />
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Cabang, departemen, dan penomoran"
        subtitle="Cabang dan departemen ditempel di faktur, tagihan, dan transaksi kas, lalu diwariskan ke setiap baris jurnal, sehingga laba rugi bisa dipilah per cabang. Penomoran dokumen mengatur awalan dan siklus nomor tiap jenis dokumen."
      />
      <Tabs
        label="Pengaturan buku"
        value={tab}
        onChange={(v) => setParams(v === 'cabang' ? {} : { tab: v }, { replace: true })}
        tabs={[
          { value: 'cabang', label: 'Cabang' },
          { value: 'departemen', label: 'Departemen' },
          { value: 'nomor', label: 'Penomoran dokumen' },
        ]}
      />
      {tab === 'cabang' && <DimensionList me={me} kind="cabang" />}
      {tab === 'departemen' && <DimensionList me={me} kind="departemen" />}
      {tab === 'nomor' && <NumberFormats me={me} />}
    </div>
  )
}

function DimensionList({ me, kind }: { me: Me; kind: DimKind }) {
  const list = useDimensions(me.activeCompanyId, kind)
  const [editing, setEditing] = useState<Dimension | 'new' | null>(null)
  const canEdit = can(me, 'core.company.edit')
  const items = list.data ?? []
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted">
          {kind === 'cabang'
            ? 'Pengguna bisa dibatasi ke cabang tertentu di menu Pengguna dan akses.'
            : 'Departemen memilah biaya per bagian, mis. operasional, pemasaran, atau administrasi.'}
        </p>
        {canEdit && <Button onClick={() => setEditing('new')}>{kind === 'cabang' ? 'Cabang baru' : 'Departemen baru'}</Button>}
      </div>
      <TableShell minWidth={480}>
        <thead className="bg-hover/60">
          <tr>
            <th className={`${th} w-32`}>Kode</th>
            <th className={th}>Nama</th>
            <th className={`${th} w-28`}>Status</th>
          </tr>
        </thead>
        <tbody>
          {list.isPending && (
            <tr>
              <td colSpan={3} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {items.map((d) => (
            <tr key={d.id} onClick={() => canEdit && setEditing(d)} className={`border-t border-line ${canEdit ? 'cursor-pointer transition-colors duration-150 hover:bg-hover' : ''}`}>
              <td className={`${td} num font-semibold`}>{d.code}</td>
              <td className={td}>{d.name}</td>
              <td className={td}>{d.active ? <Badge tone="good">Aktif</Badge> : <Badge tone="neutral">Nonaktif</Badge>}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && (
        <EmptyState title={kind === 'cabang' ? 'Belum ada cabang' : 'Belum ada departemen'} body="Tanpa dimensi, semua transaksi tercatat untuk PT secara keseluruhan." />
      )}
      {editing && <DimensionDialog companyId={me.activeCompanyId} kind={kind} d={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
    </>
  )
}

function DimensionDialog({ companyId, kind, d, onClose }: { companyId: string | null; kind: DimKind; d: Dimension | null; onClose: () => void }) {
  const save = useSaveDimension(companyId, kind)
  const toast = useToast()
  const [v, setV] = useState({ code: d?.code ?? '', name: d?.name ?? '', active: d?.active ?? true })
  const err = save.error instanceof ApiError ? save.error : null
  const label = kind === 'cabang' ? 'cabang' : 'departemen'
  return (
    <Dialog
      open
      onClose={onClose}
      title={d ? `Ubah ${label} ${d.code}` : `${label[0].toUpperCase()}${label.slice(1)} baru`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button
            busy={save.isPending}
            disabled={!v.code.trim() || !v.name.trim()}
            onClick={() => save.mutate({ id: d?.id, ...v, version: d?.version ?? 0 }, { onSuccess: (x) => (toast(`${x.code} disimpan`), onClose()) })}
          >
            Simpan
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field id="dim-kode" label="Kode" hint="Huruf besar, angka, atau tanda hubung; muncul di nomor dokumen bila penomoran per cabang." value={v.code} maxLength={10} onChange={(e) => setV({ ...v, code: e.target.value.toUpperCase() })} error={err?.fields.code} />
        <Field id="dim-nama" label="Nama" value={v.name} maxLength={100} onChange={(e) => setV({ ...v, name: e.target.value })} error={err?.fields.name} />
        {d && <Checkbox checked={v.active} onChange={(x) => setV({ ...v, active: x })} label="Aktif" hint="Dimensi nonaktif tidak bisa dipilih di dokumen baru." />}
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

function NumberFormats({ me }: { me: Me }) {
  const list = useNumberFormats(me.activeCompanyId)
  const [editing, setEditing] = useState<NumberFormat | null>(null)
  const canEdit = can(me, 'ledger.period.manage')
  return (
    <>
      <p className="max-w-3xl text-sm text-muted">
        Mengubah format tidak mengubah nomor dokumen yang sudah ada. Urutan nomor dilanjutkan per seri dan periode, sehingga tidak pernah ada nomor ganda.
      </p>
      <TableShell minWidth={720}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Dokumen</th>
            <th className={th}>Awalan</th>
            <th className={th}>Nomor diulang</th>
            <th className={th}>Per cabang</th>
            <th className={th}>Contoh</th>
          </tr>
        </thead>
        <tbody>
          {list.isPending && (
            <tr>
              <td colSpan={5} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {(list.data ?? []).map((f) => (
            <tr key={f.series} onClick={() => canEdit && setEditing(f)} className={`border-t border-line ${canEdit ? 'cursor-pointer transition-colors duration-150 hover:bg-hover' : ''}`}>
              <td className={td}>
                <span className="font-medium">{f.label}</span>
                <span className="num block text-xs text-muted">Seri {f.series}</span>
              </td>
              <td className={`${td} num`}>{f.prefix}</td>
              <td className={td}>{resetLabel[f.reset]}</td>
              <td className={td}>{f.perBranch ? 'Ya' : '—'}</td>
              <td className={`${td} num`}>
                {f.example}
                {!f.custom && <span className="ml-2 text-xs text-muted">(bawaan)</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {editing && <NumberFormatDialog companyId={me.activeCompanyId} f={editing} onClose={() => setEditing(null)} />}
    </>
  )
}

function NumberFormatDialog({ companyId, f, onClose }: { companyId: string | null; f: NumberFormat; onClose: () => void }) {
  const save = useSaveNumberFormat(companyId)
  const toast = useToast()
  const [v, setV] = useState({ prefix: f.prefix, reset: f.reset, digits: f.digits, perBranch: f.perBranch })
  const err = save.error instanceof ApiError ? save.error : null
  const now = new Date()
  const parts = [v.prefix || '…', ...(v.perBranch ? ['JKT'] : []), ...(v.reset !== 'never' ? [String(now.getFullYear())] : []), ...(v.reset === 'month' ? [String(now.getMonth() + 1).padStart(2, '0')] : []), '1'.padStart(v.digits, '0')]
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Penomoran ${f.label.toLowerCase()}`}
      description={`Contoh nomor berikutnya: ${parts.join('/')}`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button busy={save.isPending} onClick={() => save.mutate({ series: f.series, ...v }, { onSuccess: () => (toast('Format nomor disimpan'), onClose()) })}>
            Simpan
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field id="nf-awalan" label="Awalan" hint="Huruf, angka, atau tanda hubung; tidak boleh sama dengan seri lain." value={v.prefix} maxLength={12} onChange={(e) => setV({ ...v, prefix: e.target.value })} error={err?.fields.prefix} />
        <SelectField id="nf-ulang" label="Nomor diulang" value={v.reset} onChange={(e) => setV({ ...v, reset: e.target.value as NumberFormat['reset'] })}>
          <option value="month">Tiap bulan (…/2026/10/0001)</option>
          <option value="year">Tiap tahun (…/2026/0001)</option>
          <option value="never">Tidak pernah (…/0001)</option>
        </SelectField>
        <SelectField id="nf-digit" label="Jumlah digit urut" value={String(v.digits)} onChange={(e) => setV({ ...v, digits: Number(e.target.value) })}>
          {[3, 4, 5, 6, 7, 8].map((n) => (
            <option key={n} value={n}>
              {n} digit
            </option>
          ))}
        </SelectField>
        <Checkbox checked={v.perBranch} onChange={(x) => setV({ ...v, perBranch: x })} label="Nomor terpisah per cabang" hint="Kode cabang masuk ke nomor dan tiap cabang punya urutan sendiri." />
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

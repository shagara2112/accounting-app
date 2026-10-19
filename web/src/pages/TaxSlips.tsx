import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiError, type Me, type TaxCode } from '../lib/api'
import {
  objectRate,
  useBupotRef,
  useSaveWithholdingObjects,
  useSlipAction,
  useSlipBatches,
  useSlips,
  useWithholdingObjects,
  type BupotRef,
  type ImportResult,
  type Slip,
  type SlipStatus,
  type TaxObject,
} from '../lib/bupot'
import { formatDate, formatDateTime, monthLabel, monthOf, todayISO } from '../lib/dates'
import { useTaxCodes } from '../lib/docs'
import { formatAmount, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { MonthNav } from '../components/MonthNav'
import { SearchSelect } from '../components/SearchSelect'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Dialog, EmptyState, Field, Segmented, SelectField, Skeleton, TableShell, TextAreaField, td, th, type Tone } from '../components/ui'

const statusView: Record<SlipStatus, { label: string; tone: Tone }> = {
  ready: { label: 'Siap ekspor', tone: 'neutral' },
  exported: { label: 'Diekspor', tone: 'accent' },
  approved: { label: 'Terbit', tone: 'good' },
  cancelled: { label: 'Dibatalkan', tone: 'bad' },
}

function objectOptions(list: TaxObject[]) {
  return list.map((o) => ({ id: o.code, label: `${o.code} · ${o.name}`, detail: objectRate(o.rate) }))
}

function objectsFor(ref: BupotRef | undefined, kind: Slip['kind']) {
  return (kind === 'bpnr' ? ref?.bpnrObjects : ref?.bppuObjects) ?? []
}

/** Bukti potong PPh 23, 4(2), dan 26 (TAX-08): lengkapi kode objek pajak, ekspor XML BPPU/BPNR,
 *  unggah di Coretax, lalu catat nomor bukti potong yang terbit. */
export function SlipsTab({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const [params, setParams] = useSearchParams()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const slips = useSlips(companyId, month)
  const batches = useSlipBatches(companyId, month)
  const ref = useBupotRef().data
  const action = useSlipAction(companyId)
  const toast = useToast()
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [kop, setKop] = useState('')
  const [open, setOpen] = useState<Slip | null>(null)
  const [importing, setImporting] = useState(false)
  const canExport = can(me, 'tax.export')
  const canEdit = can(me, 'tax.create')

  const data = slips.data
  const items = data?.items ?? []
  const pending = (s: Slip) => s.status === 'ready' || s.status === 'exported'
  const selectable = (s: Slip) => pending(s) && toUnits(s.amount) > 0n
  const chosen = items.filter((s) => selected.has(s.billId) && selectable(s))
  const kinds = new Set(chosen.map((s) => s.kind))
  const total = items.filter((s) => s.status !== 'cancelled').reduce((n, s) => n + toUnits(s.amount), 0n)
  const counts = items.reduce<Record<string, number>>((m, s) => ({ ...m, [s.status]: (m[s.status] ?? 0) + 1 }), {})
  const exportable = chosen.filter((s) => s.problems.length === 0)

  function toggle(id: string, on: boolean) {
    setSelected((s) => {
      const n = new Set(s)
      if (on) n.add(id)
      else n.delete(id)
      return n
    })
  }

  function run(a: Parameters<typeof action.mutate>[0], done: string) {
    action.mutate(a, {
      onSuccess: (r) => {
        toast(typeof r === 'string' && r ? `${done}: ${r}` : done)
        setSelected(new Set())
        setKop('')
      },
      onError: (e) => toast(e.message, 'bad'),
    })
  }

  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <MonthNav
          label="Masa pajak"
          value={month}
          onChange={(m) => {
            setSelected(new Set())
            setParams({ tab: 'bupot', bulan: m }, { replace: true })
          }}
        />
        {data && (
          <span className="flex flex-wrap items-center gap-2 text-sm">
            {(Object.keys(statusView) as SlipStatus[]).map(
              (s) =>
                counts[s] && (
                  <Badge key={s} tone={statusView[s].tone}>
                    {statusView[s].label} {counts[s]}
                  </Badge>
                ),
            )}
            <span className="text-muted">
              PPh dipotong masa ini <span className="num font-semibold text-ink">Rp {formatAmount(total)}</span>
            </span>
          </span>
        )}
      </div>
      {data?.problems.map((p) => (
        <Alert key={p} tone="warn">
          {p}
        </Alert>
      ))}
      <p className="text-sm text-muted">
        Satu tagihan yang memotong PPh = satu bukti potong, dengan nilai bersih sesudah nota debit. Pemasok dalam negeri diekspor sebagai XML BPPU (PPh 23 dan 4(2)), pemasok luar negeri sebagai XML BPNR (PPh
        26). Kode objek pajak bawaan per kode PPh diatur di tab Pengaturan.
      </p>

      {(canExport || canEdit) && items.length > 0 && (
        <div className="flex flex-wrap items-center gap-2.5 rounded-[12px] border border-line bg-surface px-4 py-3">
          <span className="text-sm text-muted">{chosen.length ? `${chosen.length} bukti potong dipilih` : 'Centang bukti potong untuk diekspor atau diberi kode objek pajak'}</span>
          {canEdit && (
            <>
              <div className="ml-auto w-[320px] max-sm:w-full">
                <SearchSelect
                  label="Kode objek pajak"
                  options={kinds.size === 1 ? objectOptions(objectsFor(ref, [...kinds][0])) : []}
                  value={kop}
                  onChange={setKop}
                  placeholder={kinds.size > 1 ? 'Pilih BPPU atau BPNR saja' : 'Kode objek pajak…'}
                  empty={kinds.size === 0 ? 'Centang bukti potong dulu.' : 'Tidak ada yang cocok.'}
                />
              </div>
              <Button size="sm" variant="secondary" disabled={!kop || chosen.length === 0 || kinds.size !== 1} busy={action.isPending && action.variables?.kind === 'object'} onClick={() => run({ kind: 'object', ids: chosen.map((s) => s.billId), taxObjectCode: kop }, 'Kode objek pajak dipasang')}>
                Terapkan
              </Button>
              <Button size="sm" variant="secondary" onClick={() => setImporting(true)}>
                Impor nomor bukti potong
              </Button>
            </>
          )}
          {canExport && (
            <Button
              size="sm"
              disabled={exportable.length === 0 || exportable.length !== chosen.length}
              busy={action.isPending && action.variables?.kind === 'export'}
              onClick={() => run({ kind: 'export', month, ids: chosen.map((s) => s.billId) }, 'XML diunduh')}
            >
              Ekspor XML ({chosen.length})
            </Button>
          )}
        </div>
      )}

      <div className={`transition-opacity duration-150 ${slips.isPlaceholderData ? 'opacity-60' : ''}`}>
        <TableShell minWidth={1080}>
          <thead className="bg-hover/60">
            <tr>
              <th className={`${th} w-10`}>
                <input
                  type="checkbox"
                  aria-label="Pilih semua yang bisa diekspor"
                  checked={items.some(selectable) && items.filter(selectable).every((s) => selected.has(s.billId))}
                  onChange={(e) => setSelected(e.target.checked ? new Set(items.filter(selectable).map((s) => s.billId)) : new Set())}
                  className="size-[18px] cursor-pointer accent-[var(--color-accent)]"
                />
              </th>
              <th className={th}>Tagihan</th>
              <th className={th}>Pemasok</th>
              <th className={th}>PPh</th>
              <th className={th}>Kode objek pajak</th>
              <th className={`${th} text-right`}>DPP</th>
              <th className={`${th} text-right`}>Dipotong</th>
              <th className={th}>Status</th>
            </tr>
          </thead>
          <tbody>
            {slips.isPending && (
              <tr>
                <td colSpan={8} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            )}
            {items.map((s) => (
              <tr key={s.billId} onClick={() => setOpen(s)} className="cursor-pointer border-t border-line align-top transition-colors duration-150 hover:bg-hover">
                <td className={td} onClick={(e) => e.stopPropagation()}>
                  <input
                    type="checkbox"
                    aria-label={`Pilih ${s.number}`}
                    disabled={!selectable(s)}
                    checked={selected.has(s.billId)}
                    onChange={(e) => toggle(s.billId, e.target.checked)}
                    className="size-[18px] cursor-pointer accent-[var(--color-accent)] disabled:cursor-not-allowed disabled:opacity-30"
                  />
                </td>
                <td className={td}>
                  <span className="num font-semibold">{s.number}</span>
                  <span className="block text-xs text-muted">{formatDate(s.date)}</span>
                </td>
                <td className={`${td} max-w-[240px]`}>
                  <span className="block truncate">{s.supplierName}</span>
                  <span className="num block text-xs text-muted">{s.counterpartTin ? `${s.foreign ? `TIN ${s.country}` : 'NPWP'} ${s.counterpartTin}` : 'Tanpa NPWP/TIN'}</span>
                  {s.problems.map((p) => (
                    <span key={p} className="block text-xs font-medium text-bad">
                      {p}
                    </span>
                  ))}
                </td>
                <td className={td}>
                  <span className="num">{s.code}</span>
                  <span className="block text-xs text-muted">{s.kind === 'bpnr' ? 'BPNR' : 'BPPU'}</span>
                </td>
                <td className={`${td} max-w-[260px]`}>
                  {s.taxObjectCode ? (
                    <>
                      <span className="num">{s.taxObjectCode}</span>
                      <span className="block truncate text-xs text-muted" title={s.taxObjectName}>
                        {s.taxObjectName}
                      </span>
                    </>
                  ) : (
                    <span className="text-muted">—</span>
                  )}
                </td>
                <td className={`${td} num text-right`}>{formatAmount(s.taxBase)}</td>
                <td className={`${td} num text-right`}>
                  {formatAmount(s.amount)}
                  {s.noted && <span className="block text-xs text-muted">sesudah nota debit</span>}
                </td>
                <td className={td}>
                  <Badge tone={statusView[s.status].tone}>{statusView[s.status].label}</Badge>
                  {s.slipNumber && <span className="num block text-xs text-muted">{s.slipNumber}</span>}
                  {s.changed && <span className="block text-xs font-medium text-warn">Nilai berubah sesudah diekspor</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </TableShell>
      </div>
      {slips.isSuccess && items.length === 0 && (
        <EmptyState title={`Tidak ada PPh dipotong di ${monthLabel(month)}`} body="Tagihan pembelian yang diposting dengan potongan PPh 23, 4(2), atau 26 otomatis muncul di sini." />
      )}

      {(batches.data?.length ?? 0) > 0 && (
        <Card title="Riwayat ekspor bukti potong" subtitle="File disimpan supaya bisa diunduh ulang dan diperiksa auditor.">
          <ul className="flex flex-col">
            {batches.data!.map((b) => (
              <li key={b.id} className="flex flex-wrap items-center justify-between gap-3 border-b border-line py-2 text-sm last:border-0">
                <span>
                  <span className="num font-medium">{b.fileName}</span>
                  <span className="block text-xs text-muted">
                    {b.invoiceCount} bukti potong · {b.createdBy ?? 'Sistem'} · {formatDateTime(b.createdAt)} · {b.format}
                  </span>
                </span>
                {canExport && (
                  <Button size="sm" variant="ghost" onClick={() => run({ kind: 'redownload', batchId: b.id }, 'XML diunduh')}>
                    Unduh lagi
                  </Button>
                )}
              </li>
            ))}
          </ul>
        </Card>
      )}

      <SlipDialog me={me} item={open} onClose={() => setOpen(null)} />
      <ImportSlipsDialog me={me} month={month} open={importing} onClose={() => setImporting(false)} />
    </>
  )
}

function SlipDialog({ me, item, onClose }: { me: Me; item: Slip | null; onClose: () => void }) {
  const action = useSlipAction(me.activeCompanyId)
  const ref = useBupotRef().data
  const toast = useToast()
  const [shownFor, setShownFor] = useState<Slip | null>(null)
  const [mode, setMode] = useState<'isian' | 'terbit' | 'batal'>('isian')
  const [form, setForm] = useState({ taxObjectCode: '', facility: 'N/A', facilityNumber: '', deemed: 100, document: 'CommercialInvoice', documentNumber: '', documentDate: '', withholdingDate: '', note: '' })
  const [number, setNumber] = useState('')
  const [date, setDate] = useState('')
  const [note, setNote] = useState('')

  if (item && shownFor?.billId !== item.billId) {
    setShownFor(item)
    setMode(item.status === 'approved' ? 'batal' : 'isian')
    setForm({
      taxObjectCode: item.taxObjectCode,
      facility: item.facility,
      facilityNumber: item.facilityNumber,
      deemed: item.deemed,
      document: item.document,
      documentNumber: item.documentNumber,
      documentDate: item.documentDate ?? item.date,
      withholdingDate: item.withholdingDate ?? item.date,
      note: item.note,
    })
    setNumber('')
    setDate(todayISO())
    setNote('')
    action.reset()
  }
  const s = item ?? shownFor
  if (!s) return null
  const canEdit = can(me, 'tax.create')
  const editable = canEdit && (s.status === 'ready' || s.status === 'exported')
  const err = action.error instanceof ApiError ? action.error : null
  const set = (patch: Partial<typeof form>) => setForm((f) => ({ ...f, ...patch }))
  const objects = objectsFor(ref, s.kind)
  const object = objects.find((o) => o.code === form.taxObjectCode)
  const facilities = (s.kind === 'bpnr' ? ref?.bpnrFacilities : ref?.bppuFacilities) ?? []
  const close = () => (setShownFor(null), onClose())
  const done = (msg: string) => ({ onSuccess: () => (toast(msg), close()) })

  return (
    <Dialog
      open={item !== null}
      onClose={close}
      width={640}
      title={`Bukti potong ${s.number}`}
      description={`${s.supplierName} · ${s.codeName} · DPP ${formatAmount(s.taxBase)} · PPh ${formatAmount(s.amount)}`}
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Tutup
          </Button>
          {editable && mode === 'isian' && (
            <Button busy={action.isPending} onClick={() => action.mutate({ kind: 'update', id: s.billId, input: { ...form, documentDate: form.documentDate || undefined, withholdingDate: form.withholdingDate || undefined } }, done('Bukti potong disimpan'))}>
              Simpan
            </Button>
          )}
          {editable && mode === 'terbit' && (
            <Button busy={action.isPending} disabled={number.trim().length < 3} onClick={() => action.mutate({ kind: 'approve', id: s.billId, number, date }, done(`Nomor bukti potong ${s.number} dicatat`))}>
              Catat nomor
            </Button>
          )}
          {canEdit && mode === 'batal' && (s.status === 'exported' || s.status === 'approved') && (
            <Button
              variant="danger"
              busy={action.isPending}
              disabled={s.status === 'approved' && note.trim().length < 5}
              onClick={() => action.mutate({ kind: s.status === 'approved' ? 'cancel' : 'reset', id: s.billId, note }, done(s.status === 'approved' ? 'Bukti potong ditandai dibatalkan' : 'Dikembalikan ke siap ekspor'))}
            >
              {s.status === 'approved' ? 'Tandai dibatalkan' : 'Kembalikan ke siap ekspor'}
            </Button>
          )}
        </>
      }
    >
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge tone={statusView[s.status].tone}>{statusView[s.status].label}</Badge>
        <Badge tone="neutral">{s.kind === 'bpnr' ? 'BPNR · PPh 26' : 'BPPU'}</Badge>
        {s.slipNumber && <span className="num">Nomor {s.slipNumber}</span>}
        {s.approvedDate && <span className="text-muted">terbit {formatDate(s.approvedDate)}</span>}
        <Link to={`/pembelian?id=${s.billId}`} className="font-semibold text-accent hover:underline">
          Buka tagihan →
        </Link>
      </div>
      {s.changed && <Alert tone="warn">PPh tagihan ini berubah sesudah diekspor (nota debit). Perbarui bukti potongnya di Coretax lewat pembetulan.</Alert>}
      {s.problems.map((p) => (
        <Alert key={p} tone="warn">
          {p}
        </Alert>
      ))}
      {canEdit && s.status !== 'cancelled' && (
        <Segmented
          label="Aksi"
          value={mode}
          onChange={(v) => (setMode(v), action.reset())}
          options={
            s.status === 'approved'
              ? [{ value: 'batal' as const, label: 'Batalkan bukti potong' }]
              : [
                  { value: 'isian' as const, label: 'Isian' },
                  { value: 'terbit' as const, label: 'Catat nomor terbit' },
                  ...(s.status === 'exported' ? [{ value: 'batal' as const, label: 'Batalkan ekspor' }] : []),
                ]
          }
        />
      )}
      {mode === 'isian' && (
        <>
          <div className="flex flex-col gap-1.5">
            <span className="text-[13px] font-semibold text-ink-2">Kode objek pajak</span>
            {editable ? (
              <SearchSelect label="Kode objek pajak" options={objectOptions(objects)} value={form.taxObjectCode} onChange={(v) => set({ taxObjectCode: v, deemed: objects.find((o) => o.code === v)?.deemed?.[0] ?? 100 })} invalid={!!err?.fields.taxObjectCode} />
            ) : (
              <span className="text-sm">{s.taxObjectCode ? `${s.taxObjectCode} · ${s.taxObjectName}` : '—'}</span>
            )}
            {object && (
              <span className="text-xs text-muted">
                Tarif {objectRate(object.rate)}; PPh tagihan {objectRate(s.rateBp / 100)}.
              </span>
            )}
            {err?.fields.taxObjectCode && <span className="text-[13px] text-bad">{err.fields.taxObjectCode}</span>}
          </div>
          <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
            <SelectField id="bp-fasilitas" label="Fasilitas" disabled={!editable} value={form.facility} onChange={(e) => set({ facility: e.target.value })}>
              {facilities.map((f) => (
                <option key={f.code} value={f.code}>
                  {f.name}
                </option>
              ))}
            </SelectField>
            {(s.kind === 'bpnr' || form.facility !== 'N/A') && (
              <Field
                id="bp-no-fasilitas"
                label={form.facility === 'COD' ? 'Nomor SKD' : 'Nomor dokumen fasilitas'}
                disabled={!editable}
                value={form.facilityNumber}
                maxLength={60}
                onChange={(e) => set({ facilityNumber: e.target.value })}
                error={err?.fields.facilityNumber}
              />
            )}
            {s.kind === 'bpnr' && (
              <SelectField id="bp-norma" label="Norma penghasilan neto" disabled={!editable} value={String(form.deemed)} onChange={(e) => set({ deemed: Number(e.target.value) })} error={err?.fields.deemed}>
                {(object?.deemed?.length ? object.deemed : [100]).map((d) => (
                  <option key={d} value={d}>
                    {d}%
                  </option>
                ))}
              </SelectField>
            )}
            <SelectField id="bp-dokumen" label="Dokumen referensi" disabled={!editable} value={form.document} onChange={(e) => set({ document: e.target.value })}>
              {(ref?.documents ?? []).map((d) => (
                <option key={d.code} value={d.code}>
                  {d.name}
                </option>
              ))}
            </SelectField>
            <Field id="bp-no-dokumen" label="Nomor dokumen" disabled={!editable} value={form.documentNumber} maxLength={60} onChange={(e) => set({ documentNumber: e.target.value })} error={err?.fields.documentNumber} />
            <Field id="bp-tgl-dokumen" label="Tanggal dokumen" type="date" disabled={!editable} value={form.documentDate} onChange={(e) => set({ documentDate: e.target.value })} />
            <Field
              id="bp-tgl-potong"
              label="Tanggal pemotongan"
              type="date"
              disabled={!editable}
              value={form.withholdingDate}
              onChange={(e) => set({ withholdingDate: e.target.value })}
              error={err?.fields.withholdingDate}
              hint={`Harus di masa ${monthLabel(monthOf(s.date))}.`}
            />
          </div>
          <TextAreaField id="bp-catatan" label="Catatan" rows={2} maxLength={300} disabled={!editable} value={form.note} onChange={(e) => set({ note: e.target.value })} />
        </>
      )}
      {mode === 'terbit' && (
        <>
          <p className="text-sm text-muted">Sesudah XML diunggah dan bukti potong diterbitkan di Coretax, catat nomornya di sini.</p>
          <div className="grid grid-cols-[1fr_180px] gap-4 max-sm:grid-cols-1">
            <Field id="bp-nomor" label="Nomor bukti potong" className="num" data-autofocus value={number} onChange={(e) => setNumber(e.target.value)} error={err?.fields.number} />
            <Field id="bp-terbit" label="Tanggal terbit" type="date" min={s.date} value={date} onChange={(e) => setDate(e.target.value)} />
          </div>
        </>
      )}
      {mode === 'batal' && (
        <TextAreaField
          id="bp-batal"
          label={s.status === 'approved' ? 'Alasan pembatalan di Coretax' : 'Alasan (opsional)'}
          hint={s.status === 'approved' ? 'Batalkan dulu bukti potongnya di Coretax. Setelah ditandai dibatalkan, tagihannya boleh di-void.' : 'Mis. draf bukti potong dihapus di Coretax sebelum diterbitkan.'}
          rows={2}
          maxLength={300}
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      )}
      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
    </Dialog>
  )
}

function ImportSlipsDialog({ me, month, open, onClose }: { me: Me; month: string; open: boolean; onClose: () => void }) {
  const action = useSlipAction(me.activeCompanyId)
  const toast = useToast()
  const [file, setFile] = useState<File | null>(null)
  const [result, setResult] = useState<ImportResult | null>(null)

  function close() {
    setFile(null)
    setResult(null)
    action.reset()
    onClose()
  }

  return (
    <Dialog
      open={open}
      onClose={close}
      width={640}
      title={`Impor nomor bukti potong ${monthLabel(month)}`}
      description="Unduh daftar bukti potong dari Coretax (Excel atau CSV), lalu unggah di sini. Nomor bukti potong dicocokkan lewat nomor dokumen referensinya."
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            {result ? 'Selesai' : 'Batal'}
          </Button>
          {!result && (
            <Button
              busy={action.isPending}
              disabled={!file}
              onClick={() =>
                file &&
                action.mutate(
                  { kind: 'import', month, file },
                  {
                    onSuccess: (r) => {
                      const res = r as ImportResult
                      setResult(res)
                      toast(`${res.recorded.length} nomor bukti potong dicatat`)
                    },
                  },
                )
              }
            >
              Impor
            </Button>
          )}
        </>
      }
    >
      {!result ? (
        <>
          <input
            type="file"
            accept=".xlsx,.csv,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
            aria-label="Pilih berkas daftar bukti potong"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            className="text-sm file:mr-3 file:cursor-pointer file:rounded-[8px] file:border file:border-line-2 file:bg-surface file:px-3 file:py-2 file:text-sm file:font-semibold"
          />
          <p className="text-[13px] text-muted">Kolom yang dibaca dari judulnya: Nomor Bukti Potong, Nomor Dokumen (referensi), dan Tanggal (opsional).</p>
          {action.error && <Alert>{action.error.message}</Alert>}
        </>
      ) : (
        <>
          <Alert tone="good">
            {result.recorded.length} bukti potong tercatat terbit{result.recorded.length > 0 && `: ${result.recorded.join(', ')}`}.
          </Alert>
          {result.skipped.length > 0 && (
            <Alert tone="warn">
              <span className="font-semibold">{result.skipped.length} baris dilewati:</span>
              <ul className="mt-1 list-disc pl-5">
                {result.skipped.map((x) => (
                  <li key={x}>{x}</li>
                ))}
              </ul>
            </Alert>
          )}
        </>
      )}
    </Dialog>
  )
}

/** KOP bawaan per kode PPh, dipakai bukti potong baru (tab Pengaturan). */
export function WithholdingObjectsCard({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const objects = useWithholdingObjects(companyId)
  const ref = useBupotRef().data
  const codes = useTaxCodes(companyId, todayISO()).data ?? []
  if (!objects.data || !ref) return <Skeleton className="h-48" />
  const pph = codes.filter((c) => c.kind === 'pph')
  return <ObjectsForm key={JSON.stringify(objects.data)} me={me} codes={pph} initial={objects.data} refData={ref} />
}

function ObjectsForm({ me, codes, initial, refData }: { me: Me; codes: TaxCode[]; initial: Record<string, string>; refData: BupotRef }) {
  const save = useSaveWithholdingObjects(me.activeCompanyId)
  const toast = useToast()
  const editable = can(me, 'tax.create')
  const [map, setMap] = useState(initial)
  const err = save.error instanceof ApiError ? save.error : null
  return (
    <Card title="Kode objek pajak bawaan" subtitle="Dipasang otomatis ke bukti potong baru per kode PPh tagihan; tetap bisa diganti per bukti potong. PPh 23 dan 26 bergantung pada jenis jasanya.">
      {codes.map((c) => {
        const list = c.article === '26' ? refData.bpnrObjects : refData.bppuObjects
        return (
          <div key={c.code} className="flex flex-col gap-1.5">
            <span className="text-[13px] font-semibold text-ink-2">{c.name}</span>
            {editable ? (
              <SearchSelect label={c.name} options={objectOptions(list)} value={map[c.code] ?? ''} onChange={(v) => setMap((m) => ({ ...m, [c.code]: v }))} clearLabel="Tanpa kode bawaan" />
            ) : (
              <span className="num text-sm">{map[c.code] || '—'}</span>
            )}
          </div>
        )
      })}
      {err && <Alert>{err.message}</Alert>}
      {editable && (
        <Button className="self-start" busy={save.isPending} onClick={() => save.mutate(Object.fromEntries(codes.map((c) => [c.code, map[c.code] ?? ''])), { onSuccess: () => toast('Kode objek pajak bawaan disimpan') })}>
          Simpan kode bawaan
        </Button>
      )}
    </Card>
  )
}

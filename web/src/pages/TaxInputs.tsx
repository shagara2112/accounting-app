import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { useInputAction, useInputs, type InputInvoice, type InputMatch, type InputMatchState } from '../lib/bupot'
import { addMonths, formatDate, monthLabel, monthOf, todayISO } from '../lib/dates'
import { formatAmount, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { MonthNav } from '../components/MonthNav'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Dialog, EmptyState, Field, Segmented, SelectField, Skeleton, TableShell, TextAreaField, td, th, type Tone } from '../components/ui'

const matchView: Record<InputMatchState, { label: string; tone: Tone }> = {
  belum: { label: 'Belum dicocokkan', tone: 'neutral' },
  cocok: { label: 'Cocok', tone: 'good' },
  selisih: { label: 'Selisih', tone: 'bad' },
}

/** Faktur pajak masukan (TAX-07): nomor faktur pemasok, masa pengkreditan, dan pencocokan
 *  dengan daftar pajak masukan Coretax. */
export function InputsTab({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const [params, setParams] = useSearchParams()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const inputs = useInputs(companyId, month)
  const [open, setOpen] = useState<InputInvoice | null>(null)
  const [importing, setImporting] = useState(false)
  const canEdit = can(me, 'tax.create')
  const data = inputs.data
  const items = data?.items ?? []

  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <MonthNav label="Masa pajak" value={month} onChange={(m) => setParams({ tab: 'masukan', bulan: m }, { replace: true })} />
        {data && (
          <span className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted">
            <span>
              Dikreditkan masa ini <span className="num font-semibold text-ink">Rp {formatAmount(data.credited)}</span>
            </span>
            {toUnits(data.notCreditable) > 0n && (
              <span>
                Tidak dikreditkan <span className="num font-semibold text-ink">Rp {formatAmount(data.notCreditable)}</span>
              </span>
            )}
            {data.unrecorded > 0 && <Badge tone="warn">{data.unrecorded} tanpa nomor faktur pajak</Badge>}
          </span>
        )}
        {canEdit && data?.pkp && (
          <Button size="sm" variant="secondary" className="ml-auto" onClick={() => setImporting(true)}>
            Cocokkan dengan Coretax
          </Button>
        )}
      </div>
      {data && !data.pkp && <Alert tone="warn">PT ini bukan PKP: PPN masukan tidak bisa dikreditkan dan sudah menjadi bagian biaya tagihan.</Alert>}
      <p className="text-sm text-muted">
        Tagihan ber-PPN otomatis dikreditkan pada masa tagihannya. Catat nomor faktur pajak pemasok, geser masa pengkreditan (paling lama tiga masa sesudah masa faktur), atau tandai tidak dikreditkan bila
        fakturnya cacat atau untuk keperluan yang tidak boleh dikreditkan; PPN-nya lalu dijurnal ke biaya.
      </p>

      <div className={`transition-opacity duration-150 ${inputs.isPlaceholderData ? 'opacity-60' : ''}`}>
        <TableShell minWidth={980}>
          <thead className="bg-hover/60">
            <tr>
              <th className={th}>Tagihan</th>
              <th className={th}>Pemasok</th>
              <th className={`${th} text-right`}>DPP</th>
              <th className={`${th} text-right`}>PPN</th>
              <th className={th}>Faktur pajak</th>
              <th className={th}>Masa kredit</th>
              <th className={th}>Coretax</th>
            </tr>
          </thead>
          <tbody>
            {inputs.isPending && (
              <tr>
                <td colSpan={7} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            )}
            {items.map((it) => (
              <tr key={it.billId} onClick={() => setOpen(it)} className="cursor-pointer border-t border-line align-top transition-colors duration-150 hover:bg-hover">
                <td className={td}>
                  <span className="num font-semibold">{it.number}</span>
                  <span className="block text-xs text-muted">{formatDate(it.date)}</span>
                </td>
                <td className={`${td} max-w-[260px]`}>
                  <span className="block truncate">{it.supplierName}</span>
                  <span className="num block text-xs text-muted">{it.supplierNpwp || 'Tanpa NPWP'}</span>
                  {it.problems.map((p) => (
                    <span key={p} className="block text-xs font-medium text-bad">
                      {p}
                    </span>
                  ))}
                </td>
                <td className={`${td} num text-right`}>{formatAmount(it.dpp)}</td>
                <td className={`${td} num text-right`}>
                  {formatAmount(it.netVat)}
                  {it.netVat !== it.vat && <span className="block text-xs text-muted">tagihan {formatAmount(it.vat)}</span>}
                </td>
                <td className={`${td} num text-[13px]`}>
                  {it.taxInvoiceNumber ?? '—'}
                  {it.taxInvoiceDate && <span className="block text-xs text-muted">{formatDate(it.taxInvoiceDate)}</span>}
                </td>
                <td className={td}>
                  {it.creditable ? (
                    <span className={it.creditPeriod === month ? 'font-medium' : 'text-muted'}>{monthLabel(it.creditPeriod!)}</span>
                  ) : (
                    <Badge tone="warn">Tidak dikreditkan</Badge>
                  )}
                </td>
                <td className={td}>
                  <Badge tone={matchView[it.match].tone}>{matchView[it.match].label}</Badge>
                  {it.match === 'selisih' && it.coretaxVat && <span className="num block text-xs text-muted">Coretax {formatAmount(it.coretaxVat)}</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </TableShell>
      </div>
      {inputs.isSuccess && items.length === 0 && data?.pkp && (
        <EmptyState title={`Tidak ada PPN masukan di ${monthLabel(month)}`} body="Tagihan pembelian ber-PPN yang diposting, atau yang dikreditkan pada masa ini, muncul di sini." />
      )}

      <InputDialog me={me} item={open} onClose={() => setOpen(null)} />
      <ImportInputsDialog me={me} open={importing} onClose={() => setImporting(false)} />
    </>
  )
}

/** Masa pengkreditan yang dibolehkan: dari masa tagihan (atau masa faktur bila lebih akhir)
 *  sampai tiga masa sesudah masa faktur pajak. */
function creditMonths(billDate: string, taxInvoiceDate: string): string[] {
  const bill = monthOf(billDate)
  const base = taxInvoiceDate ? monthOf(taxInvoiceDate) : bill
  const lo = base > bill ? base : bill
  const out: string[] = []
  for (let m = lo; m <= addMonths(base, 3); m = addMonths(m, 1)) out.push(m)
  return out
}

function InputDialog({ me, item, onClose }: { me: Me; item: InputInvoice | null; onClose: () => void }) {
  const action = useInputAction(me.activeCompanyId)
  const toast = useToast()
  const [shownFor, setShownFor] = useState<InputInvoice | null>(null)
  const [mode, setMode] = useState<'faktur' | 'kredit'>('faktur')
  const [number, setNumber] = useState('')
  const [date, setDate] = useState('')
  const [period, setPeriod] = useState('')
  const [note, setNote] = useState('')
  const [reason, setReason] = useState('')
  const [reclassDate, setReclassDate] = useState('')
  const [key, setKey] = useState(newIdempotencyKey)

  if (item && shownFor?.billId !== item.billId) {
    setShownFor(item)
    setMode('faktur')
    setNumber(item.taxInvoiceNumber ?? '')
    setDate(item.taxInvoiceDate ?? item.date)
    setPeriod(item.creditable && item.creditPeriod !== monthOf(item.date) ? (item.creditPeriod ?? '') : '')
    setNote(item.note)
    setReason('')
    setReclassDate(todayISO() < item.date ? item.date : todayISO())
    setKey(newIdempotencyKey())
    action.reset()
  }
  const it = item ?? shownFor
  if (!it) return null
  const canEdit = can(me, 'tax.create')
  const err = action.error instanceof ApiError ? action.error : null
  const months = creditMonths(it.date, number.trim() ? date : '')
  const close = () => (setShownFor(null), onClose())

  return (
    <Dialog
      open={item !== null}
      onClose={close}
      width={580}
      title={`PPN masukan ${it.number}`}
      description={`${it.supplierName} · DPP ${formatAmount(it.dpp)} · PPN ${formatAmount(it.netVat)}${it.supplierInvoiceNo ? ` · faktur pemasok ${it.supplierInvoiceNo}` : ''}`}
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Tutup
          </Button>
          {canEdit && mode === 'faktur' && (
            <Button
              busy={action.isPending}
              onClick={() =>
                action.mutate(
                  { kind: 'update', id: it.billId, taxInvoiceNumber: number, taxInvoiceDate: number.trim() ? date : '', creditPeriod: it.creditable ? period : '', note },
                  { onSuccess: () => (toast(`Faktur masukan ${it.number} disimpan`), close()) },
                )
              }
            >
              Simpan
            </Button>
          )}
          {canEdit && mode === 'kredit' && (
            <Button
              variant={it.creditable ? 'danger' : 'primary'}
              busy={action.isPending}
              disabled={reason.trim().length < 5}
              onClick={() =>
                action.mutate(
                  { kind: 'creditable', id: it.billId, creditable: !it.creditable, date: reclassDate, reason, idempotencyKey: key },
                  { onSuccess: () => (toast(it.creditable ? 'PPN masukan ditandai tidak dikreditkan dan dijurnal ke biaya' : 'PPN masukan dikreditkan lagi'), close()) },
                )
              }
            >
              {it.creditable ? 'Tandai tidak dikreditkan' : 'Kreditkan lagi'}
            </Button>
          )}
        </>
      }
    >
      <div className="flex flex-wrap items-center gap-2 text-sm">
        {it.creditable ? <Badge tone="good">Dikreditkan {monthLabel(it.creditPeriod!)}</Badge> : <Badge tone="warn">Tidak dikreditkan</Badge>}
        <Badge tone={matchView[it.match].tone}>{matchView[it.match].label}</Badge>
        <Link to={`/pembelian?id=${it.billId}`} className="font-semibold text-accent hover:underline">
          Buka tagihan →
        </Link>
      </div>
      {it.problems.map((p) => (
        <Alert key={p} tone="warn">
          {p}
        </Alert>
      ))}
      {canEdit && (
        <Segmented
          label="Aksi"
          value={mode}
          onChange={(v) => (setMode(v), action.reset())}
          options={[
            { value: 'faktur', label: 'Faktur pajak' },
            { value: 'kredit', label: it.creditable ? 'Tidak dikreditkan' : 'Kreditkan lagi' },
          ]}
        />
      )}
      {mode === 'faktur' && (
        <>
          <div className="grid grid-cols-[1fr_180px] gap-4 max-sm:grid-cols-1">
            <Field
              id="fm-nomor"
              label="Nomor faktur pajak pemasok"
              className="num"
              data-autofocus
              disabled={!canEdit}
              placeholder="mis. 04002600000000123"
              value={number}
              onChange={(e) => setNumber(e.target.value)}
              error={err?.fields.taxInvoiceNumber}
            />
            <Field id="fm-tanggal" label="Tanggal faktur" type="date" disabled={!canEdit || !number.trim()} value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.taxInvoiceDate} />
          </div>
          {it.creditable && (
            <SelectField
              id="fm-masa"
              label="Masa pengkreditan"
              disabled={!canEdit}
              value={period}
              onChange={(e) => setPeriod(e.target.value)}
              error={err?.fields.creditPeriod}
              hint="Boleh digeser paling lama tiga masa sesudah masa faktur pajak (UU PPN Pasal 9 ayat 9), mis. bila faktur diterima terlambat."
            >
              <option value="">Masa tagihan ({monthLabel(monthOf(it.date))})</option>
              {months
                .filter((m) => m !== monthOf(it.date))
                .map((m) => (
                  <option key={m} value={m}>
                    {monthLabel(m)}
                  </option>
                ))}
            </SelectField>
          )}
          <TextAreaField id="fm-catatan" label="Catatan" rows={2} maxLength={300} disabled={!canEdit} value={note} onChange={(e) => setNote(e.target.value)} error={err?.fields.note} />
        </>
      )}
      {mode === 'kredit' && (
        <>
          <p className="text-sm text-muted">
            {it.creditable
              ? `PPN ${formatAmount(it.netVat)} dipindah dari akun PPN masukan ke akun biaya baris tagihan (barang persediaan ke beban PPN tidak dapat dikreditkan) lewat jurnal bertanggal di bawah, dan tidak masuk rekap PPN.`
              : 'Jurnal PPN tidak dikreditkan di-void pada tanggal di bawah, dan PPN-nya kembali dikreditkan pada masa tagihan.'}
          </p>
          <Field id="fm-tgl-jurnal" label="Tanggal jurnal" type="date" min={it.date} value={reclassDate} onChange={(e) => setReclassDate(e.target.value)} error={err?.fields.date} />
          <TextAreaField id="fm-alasan" label="Alasan" rows={2} maxLength={300} value={reason} onChange={(e) => setReason(e.target.value)} error={err?.fields.reason} placeholder="mis. Faktur pajak pemasok cacat; pengganti belum diterima" />
        </>
      )}
      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
    </Dialog>
  )
}

function ImportInputsDialog({ me, open, onClose }: { me: Me; open: boolean; onClose: () => void }) {
  const action = useInputAction(me.activeCompanyId)
  const toast = useToast()
  const [file, setFile] = useState<File | null>(null)
  const [result, setResult] = useState<InputMatch | null>(null)

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
      width={680}
      title="Cocokkan dengan daftar pajak masukan Coretax"
      description="Unduh daftar faktur pajak masukan dari Coretax (Excel atau CSV), lalu unggah di sini. Faktur dicocokkan lewat nomor yang sudah dicatat, atau NPWP pemasok dan PPN yang sama; nomor faktur yang cocok dicatat otomatis."
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
                  { kind: 'import', file },
                  {
                    onSuccess: (r) => {
                      const res = r as InputMatch
                      setResult(res)
                      toast(`${res.matched.length} faktur masukan cocok`)
                    },
                  },
                )
              }
            >
              Cocokkan
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
            aria-label="Pilih berkas daftar pajak masukan"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            className="text-sm file:mr-3 file:cursor-pointer file:rounded-[8px] file:border file:border-line-2 file:bg-surface file:px-3 file:py-2 file:text-sm file:font-semibold"
          />
          <p className="text-[13px] text-muted">Kolom yang dibaca dari judulnya: NPWP penjual, nama penjual, nomor faktur pajak, tanggal faktur, dan PPN.</p>
          {action.error && <Alert>{action.error.message}</Alert>}
        </>
      ) : (
        <>
          <Alert tone="good">
            {result.rows} faktur di berkas · {result.matched.length} cocok{result.matched.length > 0 && `: ${result.matched.join(', ')}`}.
          </Alert>
          {result.differences.length > 0 && (
            <Alert tone="bad">
              <span className="font-semibold">{result.differences.length} faktur berbeda PPN-nya:</span>
              <ul className="mt-1 list-disc pl-5">
                {result.differences.map((d) => (
                  <li key={d.number}>
                    {d.bill} · faktur <span className="num">{d.number}</span> · Lajur {formatAmount(d.lajurVat)}, Coretax {formatAmount(d.coretaxVat)}
                  </li>
                ))}
              </ul>
            </Alert>
          )}
          {result.coretaxOnly.length > 0 && (
            <Alert tone="warn">
              <span className="font-semibold">{result.coretaxOnly.length} faktur di Coretax belum ada tagihannya.</span> Catat tagihannya di menu Pembelian, atau periksa NPWP pemasoknya.
              <ul className="mt-1 list-disc pl-5">
                {result.coretaxOnly.map((c) => (
                  <li key={c.line}>
                    {c.supplier || c.npwp} · <span className="num">{c.number}</span>
                    {c.date && ` · ${formatDate(c.date)}`} · PPN {formatAmount(c.vat)}
                  </li>
                ))}
              </ul>
            </Alert>
          )}
          {result.lajurOnly.length > 0 && (
            <Alert tone="neutral">
              <span className="font-semibold">{result.lajurOnly.length} tagihan di masa yang sama belum ada fakturnya di Coretax:</span> {result.lajurOnly.join(', ')}. Tanyakan faktur pajaknya ke pemasok.
            </Alert>
          )}
        </>
      )}
    </Dialog>
  )
}

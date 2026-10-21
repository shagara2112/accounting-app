import { useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useDigunggung } from '../lib/daily'
import { ApiError, download, type CompanyProfile, type Me, type OutputInvoice, type TaxSettings, type TaxStatus } from '../lib/api'
import { formatDate, formatDateTime, monthLabel, monthOf, todayISO } from '../lib/dates'
import { formatAmount, toUnits } from '../lib/money'
import { can } from '../lib/session'
import {
  useBatches,
  useCompanyProfile,
  useLogoVersion,
  useSetLogo,
  useOutputs,
  useSaveCompanyProfile,
  useSaveTaxSettings,
  useTaxAction,
  useTaxSettings,
  useTrxCodes,
  useVATRecap,
  useWithholding,
} from '../lib/tax'
import { InputsTab } from './TaxInputs'
import { SlipsTab, WithholdingObjectsCard } from './TaxSlips'
import { MonthNav } from '../components/MonthNav'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Checkbox, Dialog, EmptyState, Field, PageHeader, Segmented, Skeleton, TableShell, Tabs, TextAreaField, td, th, type Tone } from '../components/ui'

const statusView: Record<TaxStatus, { label: string; tone: Tone }> = {
  ready: { label: 'Siap ekspor', tone: 'neutral' },
  exported: { label: 'Diekspor', tone: 'accent' },
  approved: { label: 'Terbit', tone: 'good' },
  cancelled: { label: 'Dibatalkan', tone: 'bad' },
  replaced: { label: 'Diganti', tone: 'warn' },
}

export function TaxPage({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'faktur'
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Pajak"
        subtitle="Faktur pajak keluaran dan bukti potong diekspor ke XML lalu diunggah manual ke Coretax; nomor yang terbit dicatat kembali di sini. Faktur masukan dicocokkan dengan Coretax. Rekap PPN dan PPh dihitung dari faktur dan tagihan yang diposting."
      />
      <Tabs
        label="Pajak"
        value={tab}
        onChange={(v) => setParams(v === 'faktur' ? {} : { tab: v }, { replace: true })}
        tabs={[
          { value: 'faktur', label: 'Faktur keluaran' },
          { value: 'masukan', label: 'Faktur masukan' },
          { value: 'bupot', label: 'Bukti potong' },
          { value: 'ppn', label: 'Rekap PPN' },
          { value: 'pph', label: 'PPh dipotong' },
          { value: 'digunggung', label: 'PPN digunggung' },
          { value: 'pengaturan', label: 'Pengaturan' },
        ]}
      />
      {tab === 'faktur' && <Outputs me={me} />}
      {tab === 'masukan' && <InputsTab me={me} />}
      {tab === 'bupot' && <SlipsTab me={me} />}
      {tab === 'ppn' && <VATRecapTab me={me} />}
      {tab === 'pph' && <WithholdingTab me={me} />}
      {tab === 'digunggung' && <DigunggungTab me={me} />}
      {tab === 'pengaturan' && <SettingsTab me={me} />}
    </div>
  )
}

function Outputs({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const [params, setParams] = useSearchParams()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const outputs = useOutputs(companyId, month)
  const batches = useBatches(companyId, month)
  const codes = useTrxCodes(companyId).data ?? []
  const action = useTaxAction(companyId)
  const toast = useToast()
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [trx, setTrx] = useState('')
  const [open, setOpen] = useState<OutputInvoice | null>(null)
  const [importing, setImporting] = useState(false)
  const canExport = can(me, 'tax.export')
  const canEdit = can(me, 'tax.create')

  const data = outputs.data
  const items = data?.items ?? []
  const selectable = (o: OutputInvoice) => (o.status === 'ready' || o.status === 'exported') && o.invoiceStatus === 'posted' && o.problems.length === 0
  const chosen = items.filter((o) => selected.has(o.invoiceId) && selectable(o))
  const vatTotal = items.filter((o) => o.status !== 'cancelled' && o.invoiceStatus === 'posted').reduce((s, o) => s + toUnits(o.vat), 0n)
  const counts = items.reduce<Record<string, number>>((m, o) => ({ ...m, [o.status]: (m[o.status] ?? 0) + 1 }), {})

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
        toast(typeof r === 'string' ? `${done}: ${r}` : done)
        setSelected(new Set())
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
            setParams({ bulan: m }, { replace: true })
          }}
        />
        {data && (
          <span className="flex flex-wrap gap-2 text-sm">
            {(Object.keys(statusView) as TaxStatus[]).map(
              (s) =>
                counts[s] && (
                  <Badge key={s} tone={statusView[s].tone}>
                    {statusView[s].label} {counts[s]}
                  </Badge>
                ),
            )}
            <span className="text-muted">
              PPN keluaran masa ini <span className="num font-semibold text-ink">Rp {formatAmount(vatTotal)}</span>
            </span>
          </span>
        )}
      </div>
      {data?.problems.map((p) => (
        <Alert key={p} tone="warn">
          {p}
        </Alert>
      ))}

      {(canExport || canEdit) && items.length > 0 && (
        <div className="flex flex-wrap items-center gap-2.5 rounded-[12px] border border-line bg-surface px-4 py-3">
          <span className="text-sm text-muted">{chosen.length ? `${chosen.length} faktur dipilih` : 'Centang faktur untuk diekspor atau diubah kode transaksinya'}</span>
          {canEdit && (
            <>
              <select aria-label="Kode transaksi" value={trx} onChange={(e) => setTrx(e.target.value)} className="ml-auto min-h-9 max-w-[280px] cursor-pointer rounded-[8px] border border-line-2 bg-surface px-2 text-[13px]">
                <option value="">Ubah kode transaksi…</option>
                {codes.map((c) => (
                  <option key={c.code} value={c.code}>
                    {c.label}
                  </option>
                ))}
              </select>
              <Button size="sm" variant="secondary" disabled={!trx || chosen.length === 0} busy={action.isPending && action.variables?.kind === 'trx'} onClick={() => run({ kind: 'trx', ids: chosen.map((o) => o.invoiceId), trxCode: trx }, 'Kode transaksi diubah')}>
                Terapkan
              </Button>
              <Button size="sm" variant="secondary" onClick={() => setImporting(true)}>
                Impor nomor faktur
              </Button>
            </>
          )}
          {canExport && (
            <Button size="sm" disabled={chosen.length === 0} busy={action.isPending && action.variables?.kind === 'export'} onClick={() => run({ kind: 'export', month, ids: chosen.map((o) => o.invoiceId) }, 'XML diunduh')}>
              Ekspor XML ({chosen.length})
            </Button>
          )}
        </div>
      )}

      <TableShell minWidth={980}>
        <thead className="bg-hover/60">
          <tr>
            <th className={`${th} w-10`}>
              <input
                type="checkbox"
                aria-label="Pilih semua yang bisa diekspor"
                checked={items.some(selectable) && items.filter(selectable).every((o) => selected.has(o.invoiceId))}
                onChange={(e) => setSelected(e.target.checked ? new Set(items.filter(selectable).map((o) => o.invoiceId)) : new Set())}
                className="size-[18px] cursor-pointer accent-[var(--color-accent)]"
              />
            </th>
            <th className={th}>Faktur</th>
            <th className={th}>Pembeli</th>
            <th className={`${th} text-right`}>DPP</th>
            <th className={`${th} text-right`}>PPN</th>
            <th className={th}>Kode</th>
            <th className={th}>Status</th>
            <th className={th}>Nomor faktur pajak</th>
          </tr>
        </thead>
        <tbody>
          {outputs.isPending && (
            <tr>
              <td colSpan={8} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {items.map((o) => (
            <tr key={o.invoiceId} onClick={() => setOpen(o)} className="cursor-pointer border-t border-line align-top transition-colors duration-150 hover:bg-hover">
              <td className={td} onClick={(e) => e.stopPropagation()}>
                <input
                  type="checkbox"
                  aria-label={`Pilih ${o.number}`}
                  disabled={!selectable(o)}
                  checked={selected.has(o.invoiceId)}
                  onChange={(e) => toggle(o.invoiceId, e.target.checked)}
                  className="size-[18px] cursor-pointer accent-[var(--color-accent)] disabled:cursor-not-allowed disabled:opacity-30"
                />
              </td>
              <td className={td}>
                <span className={`num font-semibold ${o.invoiceStatus === 'void' ? 'text-muted line-through' : ''}`}>{o.number}</span>
                <span className="block text-xs text-muted">{formatDate(o.date)}</span>
              </td>
              <td className={`${td} max-w-[240px]`}>
                <span className="block truncate">{o.buyerName}</span>
                <span className="num block text-xs text-muted">{o.buyerNpwp || '—'}</span>
                {o.problems.map((p) => (
                  <span key={p} className="block text-xs font-medium text-bad">
                    {p}
                  </span>
                ))}
              </td>
              <td className={`${td} num text-right`}>{formatAmount(o.dpp)}</td>
              <td className={`${td} num text-right`}>{formatAmount(o.vat)}</td>
              <td className={`${td} num`}>{o.trxCode}</td>
              <td className={td}>
                <Badge tone={statusView[o.status].tone}>{statusView[o.status].label}</Badge>
              </td>
              <td className={`${td} num text-[13px]`}>{o.taxInvoiceNumber ?? '—'}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {outputs.isSuccess && items.length === 0 && data?.pkp && (
        <EmptyState title={`Tidak ada faktur ber-PPN di ${monthLabel(month)}`} body="Faktur penjualan yang diposting dengan PPN otomatis muncul di sini." />
      )}

      {(batches.data?.length ?? 0) > 0 && (
        <Card title="Riwayat ekspor" subtitle="File disimpan supaya bisa diunduh ulang dan diperiksa auditor.">
          <ul className="flex flex-col">
            {batches.data!.map((b) => (
              <li key={b.id} className="flex flex-wrap items-center justify-between gap-3 border-b border-line py-2 text-sm last:border-0">
                <span>
                  <span className="num font-medium">{b.fileName}</span>
                  <span className="block text-xs text-muted">
                    {b.invoiceCount} faktur · {b.createdBy ?? 'Sistem'} · {formatDateTime(b.createdAt)} · {b.format}
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

      <OutputDialog me={me} item={open} onClose={() => setOpen(null)} />
      <ImportDialog me={me} open={importing} onClose={() => setImporting(false)} />
    </>
  )
}

function OutputDialog({ me, item, onClose }: { me: Me; item: OutputInvoice | null; onClose: () => void }) {
  const action = useTaxAction(me.activeCompanyId)
  const toast = useToast()
  const [shownFor, setShownFor] = useState<OutputInvoice | null>(null)
  const [mode, setMode] = useState<'approve' | 'cancel'>('approve')
  const [number, setNumber] = useState('')
  const [date, setDate] = useState('')
  const [note, setNote] = useState('')

  if (item && shownFor?.invoiceId !== item.invoiceId) {
    setShownFor(item)
    setMode(item.status === 'approved' ? 'cancel' : 'approve')
    setNumber('')
    setDate(todayISO())
    setNote('')
    action.reset()
  }
  const o = item ?? shownFor
  if (!o) return null
  const canEdit = can(me, 'tax.create')
  const err = action.error instanceof ApiError ? action.error : null
  const open = o.status === 'ready' || o.status === 'exported'

  return (
    <Dialog
      open={item !== null}
      onClose={() => (setShownFor(null), onClose())}
      width={560}
      title={`Faktur pajak ${o.number}`}
      description={`${o.buyerName} · DPP ${formatAmount(o.dpp)} · PPN ${formatAmount(o.vat)} · kode ${o.trxCode}`}
      footer={
        canEdit &&
        (open || o.status === 'approved') && (
          <>
            <Button variant="secondary" onClick={onClose}>
              Tutup
            </Button>
            {mode === 'approve' ? (
              <Button
                busy={action.isPending}
                disabled={number.replace(/\D/g, '').length < 13}
                onClick={() => action.mutate({ kind: 'approve', id: o.invoiceId, number, date }, { onSuccess: () => (toast(`Nomor faktur pajak ${o.number} dicatat`), onClose()) })}
              >
                Catat nomor
              </Button>
            ) : (
              <Button
                variant="danger"
                busy={action.isPending}
                disabled={note.trim().length < 5}
                onClick={() => action.mutate({ kind: 'cancel', id: o.invoiceId, note }, { onSuccess: () => (toast(open ? 'Dikembalikan ke status siap ekspor' : 'Faktur pajak ditandai dibatalkan'), onClose()) })}
              >
                {open ? 'Kembalikan ke siap ekspor' : 'Tandai dibatalkan'}
              </Button>
            )}
          </>
        )
      }
    >
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={statusView[o.status].tone}>{statusView[o.status].label}</Badge>
        {o.taxInvoiceNumber && <span className="num text-sm">Nomor {o.taxInvoiceNumber}</span>}
        {o.approvedDate && <span className="text-sm text-muted">terbit {formatDate(o.approvedDate)}</span>}
      </div>
      {o.note && <p className="text-sm text-muted">Catatan: {o.note}</p>}
      {o.problems.map((p) => (
        <Alert key={p} tone="warn">
          {p}
        </Alert>
      ))}
      {canEdit && open && (
        <Segmented
          label="Aksi"
          value={mode}
          onChange={setMode}
          options={[
            { value: 'approve', label: 'Catat nomor terbit' },
            { value: 'cancel', label: 'Batalkan ekspor' },
          ]}
        />
      )}
      {canEdit && open && mode === 'approve' && (
        <>
          <Field id="nsfp" label="Nomor faktur pajak dari Coretax" data-autofocus className="num" placeholder="mis. 04002600000000017" value={number} onChange={(e) => setNumber(e.target.value)} error={err?.fields.number} />
          <Field id="nsfp-date" label="Tanggal terbit" type="date" min={o.date} value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} />
        </>
      )}
      {canEdit && (mode === 'cancel' || o.status === 'approved') && (open || o.status === 'approved') && (
        <TextAreaField
          id="tax-note"
          label={o.status === 'approved' ? 'Alasan pembatalan di Coretax' : 'Alasan'}
          hint={o.status === 'approved' ? 'Batalkan dulu faktur pajaknya di Coretax. Setelah ditandai dibatalkan, faktur penjualan boleh di-void.' : undefined}
          value={note}
          maxLength={300}
          onChange={(e) => setNote(e.target.value)}
          error={err?.fields.note}
        />
      )}
      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
    </Dialog>
  )
}

function ImportDialog({ me, open, onClose }: { me: Me; open: boolean; onClose: () => void }) {
  const action = useTaxAction(me.activeCompanyId)
  const toast = useToast()
  const [csv, setCsv] = useState('')
  const [result, setResult] = useState<{ recorded: string[]; skipped: string[] } | null>(null)

  function close() {
    setCsv('')
    setResult(null)
    action.reset()
    onClose()
  }

  return (
    <Dialog
      open={open}
      onClose={close}
      width={640}
      title="Impor nomor faktur pajak"
      description="Unduh daftar faktur keluaran dari Coretax, simpan sebagai CSV, lalu pilih file atau tempel isinya. Kolom yang dibaca: Referensi (nomor faktur Lajur, mis. FJ/2026/10/0001), Nomor Faktur Pajak, dan Tanggal (opsional)."
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            {result ? 'Selesai' : 'Batal'}
          </Button>
          {!result && (
            <Button
              busy={action.isPending}
              disabled={!csv.trim()}
              onClick={() =>
                action.mutate(
                  { kind: 'import', csv },
                  {
                    onSuccess: (r) => {
                      const res = r as { recorded: string[]; skipped: string[] }
                      setResult(res)
                      toast(`${res.recorded.length} nomor faktur pajak dicatat`)
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
            accept=".csv,text/csv,text/plain"
            aria-label="Pilih file CSV"
            onChange={async (e) => {
              const f = e.target.files?.[0]
              if (f) setCsv(await f.text())
            }}
            className="text-sm file:mr-3 file:cursor-pointer file:rounded-[8px] file:border file:border-line-2 file:bg-surface file:px-3 file:py-2 file:text-sm file:font-semibold"
          />
          <TextAreaField id="csv" label="Isi CSV" rows={8} className="num text-[13px]" placeholder={'Referensi;Nomor Faktur Pajak;Tanggal\nFJ/2026/10/0001;04002600000000017;07/10/2026'} value={csv} onChange={(e) => setCsv(e.target.value)} />
          {action.error && <Alert>{action.error.message}</Alert>}
        </>
      ) : (
        <>
          <Alert tone="good">{result.recorded.length} faktur tercatat terbit{result.recorded.length > 0 && `: ${result.recorded.join(', ')}`}.</Alert>
          {result.skipped.length > 0 && (
            <Alert tone="warn">
              <span className="font-semibold">{result.skipped.length} baris dilewati:</span>
              <ul className="mt-1 list-disc pl-5">
                {result.skipped.map((s) => (
                  <li key={s}>{s}</li>
                ))}
              </ul>
            </Alert>
          )}
        </>
      )}
    </Dialog>
  )
}

function VATRecapTab({ me }: { me: Me }) {
  const [year, setYear] = useState(Number(todayISO().slice(0, 4)))
  const recap = useVATRecap(me.activeCompanyId, year)
  const r = recap.data
  const cell = `${td} num text-right`
  // Kolom PPN dipungut pemungut (WAPU, kode 02/03) hanya bila tahun ini ada.
  const collected = r ? toUnits(r.totals.collected) > 0n : false
  return (
    <>
      <div className="flex items-center gap-1 self-start rounded-[10px] border border-line-2 bg-surface p-1">
        <Button size="sm" variant="ghost" aria-label="Tahun sebelumnya" onClick={() => setYear(year - 1)}>
          ‹
        </Button>
        <span className="px-2 text-sm font-semibold">Tahun {year}</span>
        <Button size="sm" variant="ghost" aria-label="Tahun berikutnya" onClick={() => setYear(year + 1)}>
          ›
        </Button>
      </div>
      {r && !r.pkp && <Alert tone="warn">PT ini bukan PKP: tidak memungut PPN dan PPN masukan tidak bisa dikreditkan.</Alert>}
      <p className="text-sm text-muted">
        Kurang bayar disetor paling lambat akhir bulan berikutnya. Lebih bayar otomatis dikompensasikan ke masa berikutnya; restitusi belum didukung. PPN faktur ke pemungut PPN (kode 02/03) disetor oleh pembeli, jadi tidak ikut kurang bayar.
      </p>
      <div className={`transition-opacity duration-150 ${recap.isPlaceholderData ? 'opacity-60' : ''}`}>
        <TableShell minWidth={860}>
          <thead className="bg-hover/60">
            <tr>
              <th className={th}>Masa</th>
              <th className={`${th} text-right`}>PPN keluaran</th>
              {collected && <th className={`${th} text-right`}>Dipungut pemungut</th>}
              <th className={`${th} text-right`}>PPN masukan</th>
              <th className={`${th} text-right`}>Kompensasi masa lalu</th>
              <th className={`${th} text-right`}>Kurang bayar</th>
              <th className={`${th} text-right`}>Lebih bayar</th>
            </tr>
          </thead>
          <tbody>
            {recap.isPending && (
              <tr>
                <td colSpan={collected ? 7 : 6} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            )}
            {r?.months.map((m) => (
              <tr key={m.month} className="border-t border-line">
                <td className={td}>{monthLabel(m.month)}</td>
                <td className={cell}>
                  {formatAmount(m.output, { blankZero: true })}
                  {m.invoices > 0 && <span className="block text-xs text-muted">{m.invoices} faktur</span>}
                </td>
                {collected && <td className={cell}>{toUnits(m.collected) > 0n ? `−${formatAmount(m.collected)}` : ''}</td>}
                <td className={cell}>
                  {formatAmount(m.input, { blankZero: true })}
                  {m.bills > 0 && <span className="block text-xs text-muted">{m.bills} tagihan</span>}
                </td>
                <td className={cell}>{formatAmount(m.compensated, { blankZero: true })}</td>
                <td className={`${cell} ${toUnits(m.payable) > 0n ? 'font-semibold text-bad' : ''}`}>{formatAmount(m.payable, { blankZero: true })}</td>
                <td className={`${cell} ${toUnits(m.overpaid) > 0n ? 'font-semibold text-good' : ''}`}>{formatAmount(m.overpaid, { blankZero: true })}</td>
              </tr>
            ))}
            {r && (
              <tr className="border-t border-line-2 bg-hover/40 font-semibold">
                <td className={td}>Jumlah {year}</td>
                <td className={cell}>{formatAmount(r.totals.output)}</td>
                {collected && <td className={cell}>−{formatAmount(r.totals.collected)}</td>}
                <td className={cell}>{formatAmount(r.totals.input)}</td>
                <td className={cell} />
                <td className={cell}>{formatAmount(r.totals.payable)}</td>
                <td className={cell}>{formatAmount(r.totals.overpaid, { blankZero: true })}</td>
              </tr>
            )}
          </tbody>
        </TableShell>
      </div>
    </>
  )
}

/** Rekap PPN digunggung (TAX-12): penjualan eceran per hari dan toko dari rekap harian. */
function DigunggungTab({ me }: { me: Me }) {
  const [month, setMonth] = useState(monthOf(todayISO()))
  const q = useDigunggung(me.activeCompanyId, month)
  const d = q.data
  const toast = useToast()
  const canExport = can(me, 'tax.export')
  const save = (format: string) => void download('GET', `/tax/digunggung/export?month=${month}${format === 'csv' ? '&format=csv' : ''}`).catch((e: Error) => toast(e.message))
  return (
    <>
      <MonthNav label="Masa pajak" value={month} onChange={setMonth} />
      <Card
        title={`PPN digunggung ${monthLabel(month)}`}
        subtitle="Penjualan eceran ke konsumen akhir tanpa faktur pajak per pembeli. Jumlahnya dilaporkan di SPT Masa PPN; sumbernya rekap penjualan harian di menu Penjualan."
        actions={
          canExport &&
          d &&
          d.rows.length > 0 && (
            <span className="flex gap-2">
              <Button variant="secondary" size="sm" onClick={() => save('xlsx')}>
                Ekspor Excel
              </Button>
              <Button variant="ghost" size="sm" onClick={() => save('csv')}>
                CSV
              </Button>
            </span>
          )
        }
      >
        {d && !d.pkp && <Alert tone="warn">PT ini bukan PKP, sehingga tidak memungut PPN.</Alert>}
        {!d && <Skeleton className="h-24" />}
        {d && d.rows.length === 0 && (
          <p className="text-sm text-muted">
            Belum ada rekap penjualan harian di {monthLabel(month)}.{' '}
            <Link to="/impor?jenis=penjualan-harian" className="font-semibold text-accent hover:underline">
              Impor rekap harian
            </Link>
          </p>
        )}
        {d && d.rows.length > 0 && (
          <TableShell minWidth={680}>
            <thead className="bg-hover/60">
              <tr>
                <th className={th}>Tanggal</th>
                <th className={th}>Rekap</th>
                <th className={th}>Toko</th>
                <th className={`${th} text-right`}>DPP</th>
                <th className={`${th} text-right`}>PPN</th>
                <th className={`${th} text-right`}>Total</th>
              </tr>
            </thead>
            <tbody>
              {d.rows.map((r) => (
                <tr key={r.id} className="border-t border-line">
                  <td className={`${td} num whitespace-nowrap`}>{formatDate(r.date)}</td>
                  <td className={td}>
                    <Link to={`/penjualan?tab=harian&bulan=${month}&hid=${r.id}`} className="num font-semibold text-accent hover:underline">
                      {r.number}
                    </Link>
                  </td>
                  <td className={td}>{r.store || '—'}</td>
                  <td className={`${td} num text-right`}>{formatAmount(r.dpp)}</td>
                  <td className={`${td} num text-right`}>{formatAmount(r.vat)}</td>
                  <td className={`${td} num text-right`}>{formatAmount(r.total)}</td>
                </tr>
              ))}
              <tr className="border-t border-line-2 bg-hover/40 font-semibold">
                <td className={td} colSpan={3}>
                  Jumlah masa ini
                </td>
                <td className={`${td} num text-right`}>{formatAmount(d.totals.dpp)}</td>
                <td className={`${td} num text-right`}>{formatAmount(d.totals.vat)}</td>
                <td className={`${td} num text-right`}>{formatAmount(d.totals.total)}</td>
              </tr>
            </tbody>
          </TableShell>
        )}
      </Card>
    </>
  )
}

function WithholdingTab({ me }: { me: Me }) {
  const [month, setMonth] = useState(monthOf(todayISO()))
  const w = useWithholding(me.activeCompanyId, month).data
  return (
    <>
      <MonthNav label="Masa pajak" value={month} onChange={setMonth} />
      <Card
        title="PPh yang kita potong dari pemasok"
        subtitle="Disetor dan dilaporkan lewat SPT Masa PPh Unifikasi; bukti potongnya diekspor dari tab Bukti potong. Dasar: tagihan yang diposting di masa ini."
        actions={
          <span className="flex flex-wrap gap-2">
            {w?.ourSums.map((s) => (
              <Badge key={s.code} tone="warn">
                {s.code} · Rp {formatAmount(s.amount)}
              </Badge>
            ))}
          </span>
        }
      >
        {w && w.byUs.length === 0 ? (
          <p className="text-sm text-muted">Tidak ada PPh yang dipotong di {monthLabel(month)}.</p>
        ) : (
          <WithheldTable
            rows={(w?.byUs ?? []).map((r) => ({ key: r.billId, date: r.date, doc: r.billNumber, name: r.contactName, sub: r.contactNpwp ? `NPWP ${r.contactNpwp}` : 'Belum ada NPWP', dpp: r.dpp, code: r.code, amount: r.amount }))}
          />
        )}
      </Card>
      <Card
        title="PPh yang dipotong pelanggan"
        subtitle="PPh 23 menjadi kredit pajak (PPh 23 dibayar di muka); PPh final 4(2) dibebankan. Minta bukti potongnya dari pelanggan. Dasar: pelunasan di masa ini."
        actions={
          <span className="flex flex-wrap gap-2">
            {w?.customerSums.map((s) => (
              <Badge key={s.code} tone="accent">
                {s.code} · Rp {formatAmount(s.amount)}
              </Badge>
            ))}
          </span>
        }
      >
        {w && w.byCustomers.length === 0 ? (
          <p className="text-sm text-muted">Tidak ada potongan PPh oleh pelanggan di {monthLabel(month)}.</p>
        ) : (
          <WithheldTable
            rows={(w?.byCustomers ?? []).map((r) => ({ key: r.receiptId + r.invoiceNumber, date: r.date, doc: `${r.receiptNumber} · ${r.invoiceNumber}`, name: r.contactName, sub: '', dpp: r.dpp, code: r.code, amount: r.amount }))}
          />
        )}
      </Card>
    </>
  )
}

function WithheldTable({ rows }: { rows: { key: string; date: string; doc: string; name: string; sub: string; dpp: string; code: string; amount: string }[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[720px] text-left text-sm">
        <thead className="text-[12.5px] text-muted">
          <tr>
            <th className="py-2 pr-3 font-semibold">Tanggal</th>
            <th className="py-2 pr-3 font-semibold">Dokumen</th>
            <th className="py-2 pr-3 font-semibold">Pihak</th>
            <th className="py-2 pr-3 text-right font-semibold">DPP faktur</th>
            <th className="py-2 pr-3 font-semibold">Kode</th>
            <th className="py-2 text-right font-semibold">PPh</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key} className="border-t border-line">
              <td className="num py-2 pr-3">{formatDate(r.date)}</td>
              <td className="num py-2 pr-3">{r.doc}</td>
              <td className="py-2 pr-3">
                {r.name}
                {r.sub && <span className="num block text-xs text-muted">{r.sub}</span>}
              </td>
              <td className="num py-2 pr-3 text-right">{formatAmount(r.dpp)}</td>
              <td className="num py-2 pr-3">{r.code}</td>
              <td className="num py-2 text-right font-semibold">{formatAmount(r.amount)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function SettingsTab({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const profile = useCompanyProfile(companyId)
  const settings = useTaxSettings(companyId)
  if (profile.isPending || settings.isPending) return <Skeleton className="h-64" />
  if (!profile.data || !settings.data) return <Alert>{(profile.error ?? settings.error)?.message}</Alert>
  return (
    <div className="grid grid-cols-[repeat(auto-fit,minmax(340px,1fr))] gap-4">
      <ProfileCard key={`p-${profile.data.version}`} me={me} profile={profile.data} />
      <LogoCard me={me} />
      <CoretaxCard key={`s-${settings.data.unitCode}-${settings.data.goodsCode}-${settings.data.goodsOpt}`} me={me} initial={settings.data} />
      <WithholdingObjectsCard me={me} />
    </div>
  )
}

function ProfileCard({ me, profile }: { me: Me; profile: CompanyProfile }) {
  const save = useSaveCompanyProfile(me.activeCompanyId)
  const toast = useToast()
  const editable = can(me, 'core.company.edit')
  const [npwp, setNpwp] = useState(profile.npwp ?? '')
  const [nitku, setNitku] = useState(profile.nitku ?? '')
  const [address, setAddress] = useState(profile.address ?? '')
  const [pkp, setPkp] = useState(profile.pkp)
  const err = save.error instanceof ApiError ? save.error : null
  return (
    <Card title={`Profil pajak ${profile.name}`} subtitle="Dicetak di faktur pajak dan dipakai sebagai TIN serta SellerIDTKU di XML Coretax.">
      <Field id="pt-npwp" label="NPWP" className="num" disabled={!editable} value={npwp} onChange={(e) => setNpwp(e.target.value)} error={err?.fields.npwp} />
      <Field id="pt-nitku" label="NITKU" className="num" disabled={!editable} placeholder="22 digit; kosong = NPWP + 000000" value={nitku} onChange={(e) => setNitku(e.target.value)} error={err?.fields.nitku} />
      <TextAreaField id="pt-alamat" label="Alamat" rows={2} disabled={!editable} value={address} onChange={(e) => setAddress(e.target.value)} />
      <Checkbox checked={pkp} disabled={!editable} onChange={setPkp} label="Pengusaha Kena Pajak (PKP)" hint="PT non-PKP tidak boleh memungut PPN dan tidak bisa mengkreditkan PPN masukan." />
      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      {editable ? (
        <Button className="self-start" busy={save.isPending} onClick={() => save.mutate({ npwp, nitku, address, pkp, version: profile.version }, { onSuccess: () => toast('Profil pajak PT disimpan') })}>
          Simpan profil
        </Button>
      ) : (
        <p className="text-[13px] text-muted">Hanya Pemilik dan Manager yang bisa mengubah profil pajak PT.</p>
      )}
    </Card>
  )
}

/** Logo PT di kop faktur PDF (AR-03). */
function LogoCard({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const version = useLogoVersion(companyId)
  const set = useSetLogo(companyId)
  const toast = useToast()
  const [missing, setMissing] = useState(false)
  const editable = can(me, 'core.company.edit')
  const input = useRef<HTMLInputElement>(null)
  return (
    <Card title="Logo di faktur" subtitle="Tampil di kop faktur PDF yang dikirim ke pelanggan. PNG atau JPEG; diperkecil otomatis.">
      <div className="flex flex-wrap items-center gap-4">
        <div className="flex h-20 w-44 items-center justify-center rounded-[10px] border border-line bg-white p-2">
          {!missing ? (
            <img
              key={version.data}
              src={`/api/v1/company/logo?v=${version.data ?? 0}`}
              alt="Logo PT"
              className="max-h-full max-w-full object-contain"
              onError={() => setMissing(true)}
              onLoad={() => setMissing(false)}
            />
          ) : (
            <span className="text-[12.5px] text-muted">Belum ada logo</span>
          )}
        </div>
        {editable && (
          <div className="flex flex-col gap-2">
            <Button variant="secondary" size="sm" busy={set.isPending} onClick={() => input.current?.click()}>
              {missing ? 'Unggah logo' : 'Ganti logo'}
            </Button>
            {!missing && (
              <Button variant="ghost" size="sm" onClick={() => set.mutate(null, { onSuccess: () => (setMissing(true), toast('Logo dihapus')) })}>
                Hapus logo
              </Button>
            )}
            <input
              ref={input}
              type="file"
              accept="image/png,image/jpeg"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0]
                e.target.value = ''
                if (f) set.mutate(f, { onSuccess: () => (setMissing(false), toast('Logo disimpan')) })
              }}
            />
          </div>
        )}
      </div>
      {set.error && <Alert>{set.error.message}</Alert>}
    </Card>
  )
}

function CoretaxCard({ me, initial }: { me: Me; initial: TaxSettings }) {
  const save = useSaveTaxSettings(me.activeCompanyId)
  const toast = useToast()
  const editable = can(me, 'tax.create')
  const [s, setS] = useState(initial)
  const err = save.error instanceof ApiError ? save.error : null
  return (
    <Card title="Isian XML Coretax" subtitle="Dipakai untuk setiap baris faktur saat diekspor. Cocokkan dengan daftar referensi DJP, lalu uji dengan satu file kecil sebelum mengekspor banyak faktur.">
      <Segmented
        label="Jenis penyerahan"
        value={s.goodsOpt}
        onChange={(v) => editable && setS({ ...s, goodsOpt: v })}
        options={[
          { value: 'B', label: 'B · Jasa' },
          { value: 'A', label: 'A · Barang' },
        ]}
      />
      <Field id="kode-bj" label="Kode barang/jasa" className="num" disabled={!editable} value={s.goodsCode} maxLength={6} onChange={(e) => setS({ ...s, goodsCode: e.target.value })} error={err?.fields.goodsCode} hint="6 digit; 000000 bila tidak dipakai." />
      <Field
        id="kode-satuan"
        label="Kode satuan"
        className="num"
        disabled={!editable}
        value={s.unitCode}
        maxLength={7}
        onChange={(e) => setS({ ...s, unitCode: e.target.value })}
        error={err?.fields.unitCode}
        hint="Format UM.0000, sesuai daftar kode satuan di referensi XML DJP."
      />
      {editable && (
        <Button className="self-start" busy={save.isPending} onClick={() => save.mutate(s, { onSuccess: () => toast('Isian XML Coretax disimpan') })}>
          Simpan isian
        </Button>
      )}
    </Card>
  )
}

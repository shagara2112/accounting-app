import { useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiError, type Me } from '../lib/api'
import { formatDate, monthOf } from '../lib/dates'
import { useImportKinds, useImportStatus, useRunImport, type ImportResult } from '../lib/imports'
import { formatAmount, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, EmptyState, Field, PageHeader, SelectField, Skeleton, TableShell, td, th } from '../components/ui'

// Urutan migrasi yang disarankan; jenis lain menyusul.
const steps: Record<string, string> = {
  kontak: 'Pelanggan dan pemasok, termasuk NPWP dan termin.',
  produk: 'Barang dan jasa beserta akun pendapatan, HPP, dan persediaannya.',
  'saldo-awal': 'Neraca saldo dari aplikasi lama pada tanggal mulai memakai Lajur.',
  'piutang-awal': 'Faktur yang belum lunas, per pelanggan, untuk umur piutang dan pelunasan.',
  'utang-awal': 'Tagihan pemasok yang belum dibayar.',
  'stok-awal': 'Kuantitas dan biaya satuan per barang pada tanggal mulai.',
  jurnal: 'Jurnal umum dari aplikasi lama atau spreadsheet, dikelompokkan per nomor bukti.',
  'penjualan-harian': 'Rekap penjualan eceran per hari dan toko (IND-07); PPN-nya PPN digunggung.',
}

/** Impor massal dari Excel atau CSV (PLT-07) dan migrasi saldo awal (GL-06). */
export function ImportsPage({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const [params, setParams] = useSearchParams()
  const list = useImportKinds(companyId)
  const kinds = (list.data ?? []).filter((k) => can(me, k.perm))
  const status = useImportStatus(companyId, can(me, 'ledger.view'))
  const selected = kinds.find((k) => k.kind === params.get('jenis')) ?? kinds[0]

  if (list.isSuccess && kinds.length === 0) return <EmptyState title="Anda tidak punya izin mengimpor data" />
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Impor data"
        subtitle="Pindahkan data dari Excel atau aplikasi lama. Setiap berkas diperiksa dengan aturan yang sama seperti input manual, lalu diimpor seluruhnya atau tidak sama sekali."
      />
      {status.data && <OpeningStatus status={status.data} />}
      <div className="grid gap-5 lg:grid-cols-[280px_1fr]">
        <div className="lg:hidden">
          <SelectField id="jenis-impor" label="Jenis impor" value={selected?.kind ?? ''} onChange={(e) => setParams({ jenis: e.target.value }, { replace: true })}>
            {kinds.map((k, i) => (
              <option key={k.kind} value={k.kind}>
                {i + 1}. {k.title}
              </option>
            ))}
          </SelectField>
        </div>
        <nav aria-label="Jenis impor" className="hidden flex-col gap-1.5 lg:flex">
          {kinds.map((k, i) => (
            <button
              key={k.kind}
              type="button"
              onClick={() => setParams({ jenis: k.kind }, { replace: true })}
              className={`flex cursor-pointer flex-col gap-0.5 rounded-[12px] border px-3.5 py-2.5 text-left transition-colors duration-150 ${
                selected?.kind === k.kind ? 'border-accent bg-accent-soft/50' : 'border-line bg-surface hover:bg-hover'
              }`}
            >
              <span className="text-sm font-semibold">
                <span className="num mr-1.5 text-muted">{i + 1}.</span>
                {k.title}
              </span>
              <span className="text-[12.5px] text-muted">{steps[k.kind]}</span>
            </button>
          ))}
        </nav>
        {selected ? <ImportPanel key={selected.kind} companyId={companyId} kind={selected.kind} title={selected.title} needDate={selected.needDate} /> : <Skeleton className="h-64" />}
      </div>
    </div>
  )
}

function OpeningStatus({ status }: { status: NonNullable<ReturnType<typeof useImportStatus>['data']> }) {
  const bal = toUnits(status.openingAccount.balance)
  const entry = status.openingEntries[0]
  if (!entry && bal === 0n) return null
  return (
    <Alert tone={bal === 0n ? 'good' : 'warn'}>
      {entry && (
        <>
          Saldo awal per {formatDate(entry.date)} sudah diimpor (
          <Link to={`/jurnal?bulan=${monthOf(entry.date)}&id=${entry.id}`} className="num font-semibold underline">
            {entry.number}
          </Link>
          ).{' '}
        </>
      )}
      {bal === 0n
        ? `Akun ${status.openingAccount.code} ${status.openingAccount.name} bersaldo nol: saldo awal lengkap dan seimbang.`
        : `Akun ${status.openingAccount.code} ${status.openingAccount.name} masih bersaldo Rp ${formatAmount(bal < 0n ? -bal : bal)}. Saldo ini menjadi nol setelah piutang, utang, dan stok awal selesai diimpor sesuai neraca saldo lama.`}
    </Alert>
  )
}

function ImportPanel({ companyId, kind, title, needDate }: { companyId: string | null; kind: string; title: string; needDate: boolean }) {
  const run = useRunImport(companyId)
  const toast = useToast()
  const input = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [date, setDate] = useState('')
  const [result, setResult] = useState<ImportResult | null>(null)
  const err = run.error instanceof ApiError ? run.error : null

  function go(dryRun: boolean) {
    if (!file) return
    run.mutate(
      { kind, file, date, dryRun },
      {
        onSuccess: (r) => {
          setResult(r)
          if (r.applied) {
            toast(`Impor ${title.toLowerCase()} selesai`)
            setFile(null)
            if (input.current) input.current.value = ''
          }
        },
        onError: () => setResult(null),
      },
    )
  }

  return (
    <Card title={title} subtitle={steps[kind]}>
      <ol className="flex list-decimal flex-col gap-1 pl-5 text-sm text-ink-2">
        <li>
          Unduh{' '}
          <a href={`/api/v1/imports/${kind}/template`} className="font-semibold text-accent hover:underline">
            template Excel
          </a>
          , isi baris datanya (petunjuk ada di bagian atas template), lalu simpan sebagai .xlsx atau CSV.
        </li>
        <li>Pilih berkasnya dan periksa dulu. Pemeriksaan tidak menyimpan apa pun.</li>
        <li>Bila tidak ada galat, impor. Satu baris salah membatalkan seluruh berkas, jadi data tidak pernah masuk setengah.</li>
      </ol>
      <div className="flex flex-wrap items-end gap-3">
        {needDate && (
          <div className="w-[200px]">
            <Field id="impor-tanggal" label="Tanggal saldo awal" type="date" value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} />
          </div>
        )}
        <div className="flex min-w-0 flex-col gap-1.5">
          <span className="text-sm font-medium text-ink-2">Berkas</span>
          <label className="flex min-h-11 cursor-pointer items-center gap-2 rounded-[10px] border border-dashed border-line-2 px-4 text-sm transition-colors hover:bg-hover">
            <input
              ref={input}
              type="file"
              accept=".xlsx,.csv"
              className="sr-only"
              onChange={(e) => {
                setFile(e.target.files?.[0] ?? null)
                setResult(null)
                run.reset()
              }}
            />
            <span className="font-semibold text-accent">Pilih berkas</span>
            <span className="max-w-[260px] truncate text-muted">{file ? file.name : '.xlsx atau .csv, maks. 5 MB'}</span>
          </label>
        </div>
        <Button variant="secondary" disabled={!file || (needDate && !date)} busy={run.isPending && run.variables?.dryRun} onClick={() => go(true)}>
          Periksa berkas
        </Button>
        <Button disabled={!file || (needDate && !date) || (result !== null && result.errors > 0)} busy={run.isPending && !run.variables?.dryRun} onClick={() => go(false)}>
          Impor
        </Button>
      </div>
      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      {result && <ResultView r={result} />}
    </Card>
  )
}

function ResultView({ r }: { r: ImportResult }) {
  const warnings = r.issues.filter((i) => i.warning).length
  return (
    <div className="flex flex-col gap-3">
      <Alert tone={r.errors > 0 ? 'bad' : r.applied ? 'good' : 'accent'}>
        {r.errors > 0
          ? `${r.errors} galat di ${r.rows} baris; berkas belum diimpor. Perbaiki baris yang ditandai lalu periksa lagi.`
          : r.applied
            ? `Berhasil diimpor: ${r.summary.join('; ')}.`
            : `Berkas siap diimpor: ${r.summary.join('; ')}.`}
        {warnings > 0 && ` ${warnings} baris dilewati atau diberi catatan.`}
      </Alert>
      {r.issues.length > 0 && (
        <TableShell minWidth={560}>
          <thead className="bg-hover/60">
            <tr>
              <th className={`${th} w-20`}>Baris</th>
              <th className={`${th} w-40`}>Kolom</th>
              <th className={th}>Keterangan</th>
            </tr>
          </thead>
          <tbody>
            {r.issues.map((i, n) => (
              <tr key={n} className="border-t border-line align-top">
                <td className={`${td} num`}>{i.row}</td>
                <td className={td}>{i.column || '—'}</td>
                <td className={td}>
                  {i.warning ? (
                    <Badge tone="warn" className="mr-2">
                      Catatan
                    </Badge>
                  ) : (
                    <Badge tone="bad" className="mr-2">
                      Galat
                    </Badge>
                  )}
                  {i.message}
                </td>
              </tr>
            ))}
          </tbody>
        </TableShell>
      )}
    </div>
  )
}

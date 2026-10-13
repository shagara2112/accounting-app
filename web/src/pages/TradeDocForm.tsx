import { useMemo, useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { Link, useNavigate, useParams } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { formatDate, monthOf, todayISO } from '../lib/dates'
import { useContacts } from '../lib/cash'
import { useDoc, usePostDoc, useSaveDoc, useTaxCodes, type DocDetail, type DocInput, type TradeKind } from '../lib/docs'
import { useAccounts } from '../lib/ledger'
import { tradeText } from '../lib/labels'
import { formatAmount, fromUnits, parseInput } from '../lib/money'
import { activeCompany, can } from '../lib/session'
import { computeLine, computeTotals, rateLabel, withholding } from '../lib/trade'
import { AccountPicker } from '../components/AccountPicker'
import { SearchSelect } from '../components/SearchSelect'
import { useToast } from '../components/Toast'
import { Alert, Button, Card, EmptyState, Field, Skeleton, ease, inputClass } from '../components/ui'

type Row = { key: string; accountId: string; description: string; qty: string; unitPrice: string; discountPct: string; taxCode: string }

const blankRow = (taxCode: string, accountId = ''): Row => ({ key: newIdempotencyKey(), accountId, description: '', qty: '1', unitPrice: '', discountPct: '', taxCode })

/** Halaman faktur atau tagihan: baru, atau mengubah draf (route …/:id/ubah). */
export function TradeDocForm({ me, kind }: { me: Me; kind: TradeKind }) {
  const { id } = useParams()
  const doc = useDoc(kind, me.activeCompanyId, id ?? null)
  if (!id) return <DocForm key="new" me={me} kind={kind} />
  if (doc.isPending) return <Skeleton className="h-96" />
  if (doc.isError) return <EmptyState title="Dokumen tidak ditemukan" body={doc.error.message} />
  if (doc.data.status !== 'draft') return <EmptyState title="Dokumen sudah diposting" body="Dokumen yang sudah diposting tidak bisa diubah; koreksi lewat void." />
  return <DocForm key={doc.data.id} me={me} kind={kind} existing={doc.data} />
}

function DocForm({ me, kind, existing }: { me: Me; kind: TradeKind; existing?: DocDetail }) {
  const t = tradeText[kind]
  const companyId = me.activeCompanyId
  const company = activeCompany(me)
  const navigate = useNavigate()
  const toast = useToast()
  const contacts = useContacts(companyId, { role: t.contactRole })
  const accounts = useAccounts(companyId)
  const save = useSaveDoc(kind, companyId)
  const post = usePostDoc(kind, companyId)

  const defaultTax = kind === 'sales' && !company?.pkp ? 'NON-PPN' : 'PPN'
  const defaultAccount = kind === 'sales' ? (accounts.data?.items.find((a) => a.code === '4-1001')?.id ?? '') : ''
  const [contactId, setContactId] = useState(existing?.contactId ?? '')
  const [date, setDate] = useState(existing?.date ?? todayISO())
  const [dueDate, setDueDate] = useState(existing?.dueDate ?? '')
  const [dueTouched, setDueTouched] = useState(Boolean(existing))
  const [memo, setMemo] = useState(existing?.memo ?? '')
  const [supplierNo, setSupplierNo] = useState(existing?.supplierInvoiceNo ?? '')
  const [whCode, setWhCode] = useState(existing?.withholdingCode ?? '')
  const [rows, setRows] = useState<Row[]>(() =>
    existing
      ? existing.lines.map((l) => ({
          key: newIdempotencyKey(),
          accountId: l.accountId,
          description: l.description,
          qty: formatAmount(l.qty),
          unitPrice: formatAmount(l.unitPrice),
          discountPct: l.discountPct === '0.0000' ? '' : formatAmount(l.discountPct),
          taxCode: l.taxCode,
        }))
      : [blankRow(defaultTax)],
  )
  const [keys] = useState(() => ({ save: newIdempotencyKey(), post: newIdempotencyKey() }))
  const [attempt, setAttempt] = useState(0)

  const taxCodes = useTaxCodes(companyId, date || todayISO())
  const codes = taxCodes.data ?? []
  const ppnCodes = codes.filter((c) => c.kind === 'ppn')
  const pphCodes = codes.filter((c) => c.kind === 'pph')
  const contact = contacts.data?.find((c) => c.id === contactId)

  // Akun baris yang boleh dipakai: pendapatan untuk faktur; aset dan beban untuk tagihan.
  const lineAccounts = useMemo(() => (accounts.data?.items ?? []).filter((a) => t.accountClasses.includes(a.class)), [accounts.data, t.accountClasses])

  const calcs = rows.map((r) => (r.accountId || r.unitPrice ? computeLine({ qty: r.qty || '0', unitPrice: r.unitPrice || '0', discountPct: r.discountPct || '0', taxCode: r.taxCode }, ppnCodes) : null))
  const totals = computeTotals(calcs.filter((c) => c && !c.invalid) as NonNullable<(typeof calcs)[number]>[])
  const wh = withholding(totals.dpp, pphCodes.find((c) => c.code === whCode))
  const autoDue = contact && date ? addDays(date, contact.paymentTermsDays) : ''
  const effectiveDue = dueTouched ? dueDate : autoDue
  const err = (save.error ?? post.error) instanceof ApiError ? ((save.error ?? post.error) as ApiError) : null

  function setRow(key: string, patch: Partial<Row>) {
    setRows((list) => list.map((r) => (r.key === key ? { ...r, ...patch } : r)))
  }

  function input(): DocInput {
    return {
      contactId,
      supplierInvoiceNo: supplierNo.trim(),
      date,
      dueDate: effectiveDue,
      memo: memo.trim(),
      withholdingCode: whCode,
      version: existing?.version ?? 0,
      lines: rows
        .filter((r) => r.accountId || r.unitPrice || r.description)
        .map((r) => ({
          accountId: r.accountId,
          description: r.description.trim(),
          qty: fromUnits(parseInput(r.qty) ?? 0n),
          unitPrice: fromUnits(parseInput(r.unitPrice) ?? 0n),
          discountPct: fromUnits(parseInput(r.discountPct) ?? 0n),
          taxCode: r.taxCode,
        })),
    }
  }

  async function submit(andPost: boolean) {
    setAttempt((n) => n + 1)
    save.reset()
    post.reset()
    try {
      const saved = await save.mutateAsync({ id: existing?.id, input: input(), idempotencyKey: keys.save })
      if (!andPost) {
        toast(`Draf ${t.doc_} disimpan`)
        navigate(`${t.base}?bulan=${monthOf(saved.date)}&id=${saved.id}&baru=${saved.id}`)
        return
      }
      const posted = await post.mutateAsync({ id: saved.id, idempotencyKey: keys.post })
      toast(`${posted.number} diposting · jurnal seimbang`)
      navigate(`${t.base}?bulan=${monthOf(posted.date)}&baru=${posted.id}`)
    } catch {
      // Pesan galat ditampilkan dari save.error atau post.error.
    }
  }

  if (!can(me, t.perm.create)) return <EmptyState title={`Anda tidak punya izin membuat ${t.doc_}`} />

  const lineError = (i: number, field: string) => err?.fields[`lines.${i}.${field}`]
  const busy = save.isPending || post.isPending
  return (
    <form
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        void submit(false)
      }}
      className="flex flex-col gap-5"
    >
      <div>
        <Link to={t.base} className="text-sm font-medium text-muted hover:text-ink">
          ← {t.title}
        </Link>
        <h1 className="mt-1 text-[26px] font-bold tracking-tight">{existing ? `Ubah draf ${t.doc_}` : `${t.doc} baru`}</h1>
        <p className="mt-1.5 max-w-2xl text-sm text-muted">
          Disimpan sebagai draf dulu; nomor {kind === 'sales' ? 'FJ' : 'FB'} dan jurnal baru dibuat saat diposting. PPN dihitung per baris dengan tarif yang berlaku pada tanggal {t.doc_}.
        </p>
      </div>

      <Card>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
          <div className="flex min-w-0 flex-col gap-1.5">
            <label htmlFor="kontak" className="text-sm font-medium text-ink-2">
              {t.contact}
            </label>
            <SearchSelect
              id="kontak"
              options={(contacts.data ?? []).filter((c) => c.active || c.id === contactId).map((c) => ({ id: c.id, label: c.name, detail: c.npwp ? `NPWP ${c.npwp}` : undefined }))}
              value={contactId}
              onChange={setContactId}
              placeholder={`Cari ${t.contact.toLowerCase()}`}
              invalid={Boolean(err?.fields.customerId || err?.fields.supplierId)}
              empty={`Belum ada ${t.contact.toLowerCase()}. Tambahkan di menu Kontak.`}
            />
            {(err?.fields.customerId || err?.fields.supplierId) && <p className="text-[13px] text-bad">{err?.fields.customerId ?? err?.fields.supplierId}</p>}
          </div>
          <Field id="tanggal" label="Tanggal" type="date" value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} />
          <Field
            id="jatuh-tempo"
            label="Jatuh tempo"
            type="date"
            min={date}
            value={effectiveDue}
            onChange={(e) => {
              setDueTouched(true)
              setDueDate(e.target.value)
            }}
            error={err?.fields.dueDate}
            hint={!dueTouched && contact ? `Otomatis dari termin ${contact.paymentTermsDays} hari` : undefined}
          />
          {kind === 'purchase' && (
            <Field id="no-pemasok" label="No. faktur pemasok" value={supplierNo} maxLength={60} onChange={(e) => setSupplierNo(e.target.value)} error={err?.fields.supplierInvoiceNo} />
          )}
        </div>
        <Field id="catatan" label="Catatan" value={memo} maxLength={500} onChange={(e) => setMemo(e.target.value)} error={err?.fields.memo} />
      </Card>

      <div className="overflow-hidden rounded-[14px] border border-line bg-surface">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[1040px] text-left text-sm">
            <thead className="bg-hover/60 text-[12.5px] text-muted">
              <tr>
                <th className="px-2 py-3 pl-4 font-semibold">Akun</th>
                <th className="px-2 py-3 font-semibold">Uraian</th>
                <th className="w-[90px] px-2 py-3 text-right font-semibold">Qty</th>
                <th className="w-[150px] px-2 py-3 text-right font-semibold">Harga satuan</th>
                <th className="w-[80px] px-2 py-3 text-right font-semibold">Diskon %</th>
                <th className="w-[150px] px-2 py-3 font-semibold">Pajak</th>
                <th className="w-[130px] px-2 py-3 text-right font-semibold">DPP</th>
                <th className="w-12 px-2 py-3">
                  <span className="sr-only">Hapus</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <AnimatePresence initial={false}>
                {rows.map((r, i) => {
                  const c = calcs[i]
                  const num = (field: 'qty' | 'unitPrice' | 'discountPct') =>
                    `${inputClass} num text-right ${c?.invalid === field || lineError(i, field) ? 'border-bad' : 'border-line-2'}`
                  return (
                    <motion.tr
                      key={r.key}
                      layout="position"
                      initial={{ opacity: 0, y: -6 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0, transition: { duration: 0.12 } }}
                      transition={{ duration: 0.18, ease }}
                      className="border-t border-line align-top"
                    >
                      <td className="min-w-[220px] px-2 py-2 pl-4">
                        {accounts.isPending ? (
                          <Skeleton className="h-11" />
                        ) : (
                          <AccountPicker
                            id={`akun-${r.key}`}
                            label={`Akun baris ${i + 1}`}
                            accounts={lineAccounts}
                            value={r.accountId}
                            invalid={Boolean(lineError(i, 'accountId'))}
                            onChange={(a) => setRow(r.key, { accountId: a })}
                            placeholder={t.accountHint}
                          />
                        )}
                        {lineError(i, 'accountId') && <p className="mt-1 text-[13px] text-bad">{lineError(i, 'accountId')}</p>}
                      </td>
                      <td className="min-w-[180px] px-2 py-2">
                        <input aria-label={`Uraian baris ${i + 1}`} value={r.description} maxLength={300} onChange={(e) => setRow(r.key, { description: e.target.value })} className={`${inputClass} border-line-2`} />
                      </td>
                      <td className="px-2 py-2">
                        <input aria-label={`Qty baris ${i + 1}`} inputMode="decimal" value={r.qty} onChange={(e) => setRow(r.key, { qty: e.target.value })} className={num('qty')} />
                      </td>
                      <td className="px-2 py-2">
                        <input
                          aria-label={`Harga satuan baris ${i + 1}`}
                          inputMode="decimal"
                          placeholder="0"
                          value={r.unitPrice}
                          onChange={(e) => setRow(r.key, { unitPrice: e.target.value })}
                          onBlur={(e) => {
                            const u = parseInput(e.target.value)
                            if (u !== null && e.target.value) setRow(r.key, { unitPrice: formatAmount(u) })
                          }}
                          className={num('unitPrice')}
                        />
                      </td>
                      <td className="px-2 py-2">
                        <input aria-label={`Diskon baris ${i + 1}`} inputMode="decimal" placeholder="0" value={r.discountPct} onChange={(e) => setRow(r.key, { discountPct: e.target.value })} className={num('discountPct')} />
                      </td>
                      <td className="px-2 py-2">
                        <select
                          aria-label={`Pajak baris ${i + 1}`}
                          value={r.taxCode}
                          onChange={(e) => setRow(r.key, { taxCode: e.target.value })}
                          className={`${inputClass} cursor-pointer ${lineError(i, 'taxCode') || c?.invalid === 'taxCode' ? 'border-bad' : 'border-line-2'}`}
                        >
                          {ppnCodes.map((code) => (
                            <option key={code.code} value={code.code} disabled={kind === 'sales' && !company?.pkp && code.rateBp > 0}>
                              {code.code === 'NON-PPN' ? 'Tanpa PPN' : `${code.code} ${rateLabel(code.rateBp)}${code.dppNum !== code.dppDen ? ` × ${code.dppNum}/${code.dppDen}` : ''}`}
                            </option>
                          ))}
                        </select>
                        {lineError(i, 'taxCode') && <p className="mt-1 text-[13px] text-bad">{lineError(i, 'taxCode')}</p>}
                      </td>
                      <td className="num px-2 pt-5 text-right">
                        {c && !c.invalid ? formatAmount(c.net) : '—'}
                        {c && !c.invalid && c.vat > 0n && <span className="block text-[12px] text-muted">PPN {formatAmount(c.vat)}</span>}
                      </td>
                      <td className="px-2 py-2">
                        <button
                          type="button"
                          aria-label={`Hapus baris ${i + 1}`}
                          disabled={rows.length <= 1}
                          onClick={() => setRows((list) => list.filter((x) => x.key !== r.key))}
                          className="flex size-11 cursor-pointer items-center justify-center rounded-[10px] text-muted transition-colors hover:bg-hover hover:text-bad disabled:cursor-not-allowed disabled:opacity-30"
                        >
                          <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                            <path d="M6 6l12 12M18 6L6 18" />
                          </svg>
                        </button>
                      </td>
                    </motion.tr>
                  )
                })}
              </AnimatePresence>
            </tbody>
          </table>
        </div>
        <div className="flex flex-wrap items-start justify-between gap-6 border-t border-line px-4 py-4">
          <Button variant="ghost" size="sm" onClick={() => setRows((list) => [...list, blankRow(list[list.length - 1]?.taxCode ?? defaultTax, defaultAccount)])}>
            + Tambah baris
          </Button>
          <dl className="w-full max-w-[380px] text-sm">
            <Total label="Subtotal" value={totals.subtotal} />
            {totals.discount > 0n && <Total label="Diskon" value={-totals.discount} />}
            <Total label="DPP" value={totals.dpp} />
            {totals.otherDpp !== totals.dpp && <Total label="DPP nilai lain" value={totals.otherDpp} muted />}
            <Total label="PPN" value={totals.vat} />
            <Total label="Total" value={totals.total} strong />
            {kind === 'purchase' && (
              <>
                <div className="flex items-center justify-between gap-3 py-1.5">
                  <label htmlFor="pph" className="text-ink-2">
                    PPh dipotong
                  </label>
                  <select id="pph" value={whCode} onChange={(e) => setWhCode(e.target.value)} className="min-h-9 max-w-[220px] cursor-pointer rounded-[8px] border border-line-2 bg-surface px-2 text-[13px]">
                    <option value="">Tidak ada</option>
                    {pphCodes.map((c) => (
                      <option key={c.code} value={c.code}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </div>
                {wh > 0n && <Total label="PPh (dari DPP)" value={-wh} muted />}
                <Total label="Dibayar ke pemasok" value={totals.total - wh} strong />
              </>
            )}
          </dl>
        </div>
      </div>

      {err && <Alert shakeKey={attempt}>{err.message}</Alert>}

      <div className="sticky bottom-0 z-10 -mx-8 flex flex-wrap items-center justify-end gap-2.5 border-t border-line bg-surface/95 px-8 py-4 backdrop-blur">
        <span className="mr-auto text-sm text-muted">
          {date && `Tanggal ${formatDate(date)}`}
          {effectiveDue && ` · jatuh tempo ${formatDate(effectiveDue)}`}
        </span>
        <Button variant="secondary" onClick={() => navigate(t.base)}>
          Batal
        </Button>
        <Button variant="secondary" type="submit" busy={save.isPending && !post.isPending} disabled={busy}>
          Simpan draf
        </Button>
        <Button busy={post.isPending} disabled={busy || totals.total <= 0n || !contactId} onClick={() => void submit(true)}>
          Simpan & posting
        </Button>
      </div>
    </form>
  )
}

function Total({ label, value, strong, muted }: { label: string; value: bigint; strong?: boolean; muted?: boolean }) {
  return (
    <div className={`flex items-baseline justify-between gap-4 py-1 ${strong ? 'border-t border-line-2 pt-2 text-base font-semibold' : ''} ${muted ? 'text-muted' : ''}`}>
      <dt>{label}</dt>
      <dd className="num">{formatAmount(value)}</dd>
    </div>
  )
}

function addDays(iso: string, days: number): string {
  const [y, m, d] = iso.split('-').map(Number)
  const dt = new Date(y, m - 1, d + days)
  return `${dt.getFullYear()}-${String(dt.getMonth() + 1).padStart(2, '0')}-${String(dt.getDate()).padStart(2, '0')}`
}

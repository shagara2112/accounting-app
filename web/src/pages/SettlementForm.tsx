import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { formatDate, monthOf, todayISO } from '../lib/dates'
import { useCashAccounts, useContacts } from '../lib/cash'
import { useAdvanceBalance, useCreateSettlement, useOpenDocs, useRefundAdvance, useTaxCodes, type Doc, type TradeKind } from '../lib/docs'
import { tradeText } from '../lib/labels'
import { formatAmount, formatRp, fromUnits, parseInput, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { withholding } from '../lib/trade'
import { SearchSelect } from '../components/SearchSelect'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Dialog, EmptyState, Field, SelectField, Skeleton, inputClass } from '../components/ui'

/** vat: PPN dipungut pelanggan pemungut (WAPU), hanya penjualan. */
type Alloc = { checked: boolean; amount: string; whCode: string; wh: string; vat: string }

/** Pelunasan piutang atau pembayaran utang: satu transaksi untuk satu atau banyak dokumen. */
export function SettlementForm({ me, kind }: { me: Me; kind: TradeKind }) {
  const t = tradeText[kind]
  const companyId = me.activeCompanyId
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const toast = useToast()
  const contacts = useContacts(companyId, { role: t.contactRole })
  const cashAccounts = useCashAccounts(companyId)
  const create = useCreateSettlement(kind, companyId)

  const [contactId, setContactId] = useState(params.get('kontak') ?? '')
  const preselect = params.get('dok')
  const [date, setDate] = useState(todayISO())
  const [cashAccountId, setCashAccountId] = useState('')
  const [memo, setMemo] = useState('')
  const [allocs, setAllocs] = useState<Record<string, Alloc>>({})
  const [advance, setAdvance] = useState('')
  const [fromAdvance, setFromAdvance] = useState('')
  const [refunding, setRefunding] = useState(false)
  const [key] = useState(newIdempotencyKey)
  const [attempt, setAttempt] = useState(0)

  const open = useOpenDocs(kind, companyId, contactId)
  const balance = toUnits(useAdvanceBalance(kind, companyId, contactId).data ?? '0')
  const pph = (useTaxCodes(companyId, date || todayISO()).data ?? []).filter((c) => c.kind === 'pph')
  const docs = open.data ?? []
  const activeCash = (cashAccounts.data ?? []).filter((a) => a.active)
  // Kolom PPN dipungut untuk pelanggan pemungut PPN (WAPU) atau faktur yang diposting saat pelanggannya pemungut.
  const collector = kind === 'sales' && (Boolean(contacts.data?.find((c) => c.id === contactId)?.vatCollector) || docs.some((d) => d.vatCollector))

  // PPN faktur pemungut yang belum dipungut; diisi otomatis saat faktur dicentang.
  const vatLeft = (d: Doc) => (d.vatCollector ? toUnits(d.vat ?? '0') - toUnits(d.vatCollected ?? '0') : 0n)
  // Dokumen yang dipilih dari drawer langsung tercentang penuh.
  const alloc = (d: Doc): Alloc =>
    allocs[d.id] ?? { checked: d.id === preselect, amount: formatAmount(d.outstanding), whCode: '', wh: '', vat: vatLeft(d) > 0n ? formatAmount(vatLeft(d)) : '' }

  function set(d: Doc, patch: Partial<Alloc>) {
    setAllocs((m) => ({ ...m, [d.id]: { ...alloc(d), ...patch } }))
  }

  const chosen = docs.filter((d) => alloc(d).checked)
  let allocated = 0n
  let withheld = 0n
  let collected = 0n
  let unreadable = false
  for (const d of chosen) {
    const a = alloc(d)
    const amt = parseInput(a.amount)
    const wh = a.whCode ? parseInput(a.wh) : 0n
    const vat = collector && a.vat.trim() ? parseInput(a.vat) : 0n
    if (amt === null || wh === null || vat === null) unreadable = true
    allocated += amt ?? 0n
    withheld += wh ?? 0n
    collected += vat ?? 0n
  }
  const advanceU = advance.trim() ? parseInput(advance) : 0n
  const fromAdvanceU = fromAdvance.trim() ? parseInput(fromAdvance) : 0n
  if (advanceU === null || fromAdvanceU === null) unreadable = true
  const cash = allocated - withheld - collected + (advanceU ?? 0n) - (fromAdvanceU ?? 0n)
  const err = create.error instanceof ApiError ? create.error : null

  function submit() {
    setAttempt((n) => n + 1)
    create.mutate(
      {
        idempotencyKey: key,
        input: {
          contactId,
          date,
          cashAccountId,
          memo: memo.trim(),
          advance: fromUnits(advanceU ?? 0n),
          fromAdvance: fromUnits(fromAdvanceU ?? 0n),
          allocations: chosen.map((d) => {
            const a = alloc(d)
            return {
              docId: d.id,
              amount: fromUnits(parseInput(a.amount) ?? 0n),
              withholdingCode: a.whCode,
              withholding: fromUnits(a.whCode ? (parseInput(a.wh) ?? 0n) : 0n),
              vatCollected: fromUnits(collector && a.vat.trim() ? (parseInput(a.vat) ?? 0n) : 0n),
            }
          }),
        },
      },
      {
        onSuccess: (s) => {
          toast(`${s.number} diposting · ${formatRp(s.amount)} ${kind === 'sales' ? 'diterima' : 'dibayar'}`)
          navigate(`${t.base}?tab=${t.tabs.settle}&bulan=${monthOf(s.date)}&baru=${s.id}`)
        },
      },
    )
  }

  if (!can(me, t.perm.create)) return <EmptyState title={`Anda tidak punya izin mencatat ${t.settle_}`} />

  return (
    <div className="flex flex-col gap-5">
      <div>
        <Link to={`${t.base}?tab=${t.tabs.settle}`} className="text-sm font-medium text-muted hover:text-ink">
          ← {t.title}
        </Link>
        <h1 className="mt-1 text-[26px] font-bold tracking-tight">{t.settleAction}</h1>
        <p className="mt-1.5 max-w-2xl text-sm text-muted">
          {kind === 'sales'
            ? 'Pilih faktur yang dilunasi; boleh sebagian. Bila pelanggan memotong PPh 23 atau PPh final 4(2), isi potongannya: piutang tetap tertutup penuh, potongannya dicatat sebagai PPh dibayar di muka atau beban PPh final. Pelanggan pemungut PPN (WAPU) menyetor sendiri PPN fakturnya: isi PPN dipungut, piutangnya ikut tertutup tanpa uang masuk.'
            : 'Pilih tagihan yang dibayar; boleh sebagian. PPh yang dipotong sudah dicatat saat tagihan diposting, jadi yang dibayar adalah sisa utang ke pemasok.'}
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
              options={(contacts.data ?? []).map((c) => ({ id: c.id, label: c.name, detail: c.npwp ? `NPWP ${c.npwp}` : undefined }))}
              value={contactId}
              onChange={(id) => {
                setContactId(id)
                setAllocs({})
              }}
              placeholder={`Cari ${t.contact.toLowerCase()}`}
              invalid={Boolean(err?.fields.customerId || err?.fields.supplierId)}
            />
          </div>
          <Field id="tanggal" label="Tanggal" type="date" value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} />
          <SelectField id="rekening" label={kind === 'sales' ? 'Masuk ke rekening' : 'Dibayar dari rekening'} value={cashAccountId} onChange={(e) => setCashAccountId(e.target.value)} error={err?.fields.cashAccountId}>
            <option value="">Pilih rekening</option>
            {activeCash.map((a) => (
              <option key={a.accountId} value={a.accountId}>
                {a.name}
                {a.accountNumber ? ` · ${a.accountNumber}` : ''}
              </option>
            ))}
          </SelectField>
        </div>
        <Field id="catatan" label="Catatan" value={memo} maxLength={500} onChange={(e) => setMemo(e.target.value)} />
      </Card>

      {contactId && (
        <Card
          title="Uang muka"
          subtitle={
            kind === 'sales'
              ? 'Uang muka pelanggan dicatat sebagai kewajiban sampai dipakai melunasi faktur atau dikembalikan.'
              : 'Uang muka ke pemasok dicatat sebagai aset sampai dipakai melunasi tagihan atau dikembalikan pemasok.'
          }
          actions={
            balance > 0n && (
              <Button variant="ghost" size="sm" onClick={() => setRefunding(true)}>
                {kind === 'sales' ? 'Kembalikan uang muka…' : 'Catat pengembalian…'}
              </Button>
            )
          }
        >
          <p className="text-sm">
            Saldo uang muka {kind === 'sales' ? 'dari' : 'ke'} {t.contact.toLowerCase()} ini: <span className="num font-semibold">Rp {formatAmount(balance)}</span>
          </p>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
            {balance > 0n && (
              <Field
                id="pakai-um"
                label={`Pakai saldo uang muka`}
                inputMode="decimal"
                className="num text-right"
                hint={`Paling banyak Rp ${formatAmount(balance)}; mengurangi uang yang ${kind === 'sales' ? 'diterima' : 'dibayar'}.`}
                value={fromAdvance}
                onChange={(e) => setFromAdvance(e.target.value)}
                error={err?.fields.fromAdvance}
              />
            )}
            <Field
              id="um-baru"
              label={kind === 'sales' ? 'Uang muka diterima (tanpa faktur)' : 'Uang muka dibayar (tanpa tagihan)'}
              inputMode="decimal"
              className="num text-right"
              hint="Boleh tanpa memilih dokumen."
              value={advance}
              onChange={(e) => setAdvance(e.target.value)}
              error={err?.fields.advance}
            />
          </div>
        </Card>
      )}
      {refunding && <RefundDialog kind={kind} companyId={companyId} contactId={contactId} balance={balance} onClose={() => setRefunding(false)} />}

      {!contactId && <EmptyState title={`Pilih ${t.contact.toLowerCase()} dulu`} body={`${t.doc} yang belum lunas akan muncul di sini.`} />}
      {contactId && open.isPending && <Skeleton className="h-40" />}
      {contactId && open.isSuccess && docs.length === 0 && <EmptyState title={`Tidak ada ${t.doc_} yang belum lunas`} />}
      {docs.length > 0 && (
        <div className="overflow-hidden rounded-[14px] border border-line bg-surface">
          <div className="overflow-x-auto">
            <table className={`w-full ${collector ? 'min-w-[1040px]' : 'min-w-[880px]'} text-left text-sm`}>
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="w-12 px-4 py-3">
                    <span className="sr-only">Pilih</span>
                  </th>
                  <th className="px-2 py-3 font-semibold">{t.doc}</th>
                  <th className="px-2 py-3 font-semibold">Jatuh tempo</th>
                  <th className="px-2 py-3 text-right font-semibold">Sisa</th>
                  <th className="w-[170px] px-2 py-3 text-right font-semibold">{kind === 'sales' ? 'Dilunasi' : 'Dibayar'}</th>
                  {kind === 'sales' && <th className="w-[300px] px-2 py-3 font-semibold">PPh dipotong pelanggan</th>}
                  {collector && <th className="w-[150px] px-2 py-3 text-right font-semibold">PPN dipungut</th>}
                </tr>
              </thead>
              <tbody>
                {docs.map((d) => {
                  const a = alloc(d)
                  // Indeks galat dari server mengikuti urutan dokumen yang dicentang.
                  const n = chosen.indexOf(d)
                  const amtErr = err?.fields[`allocations.${n}.amount`] ?? err?.fields[`allocations.${n}.invoiceId`] ?? err?.fields[`allocations.${n}.billId`]
                  const whErr = err?.fields[`allocations.${n}.withholding`] ?? err?.fields[`allocations.${n}.withholdingCode`]
                  const vatErr = err?.fields[`allocations.${n}.vatCollected`]
                  return (
                    <tr key={d.id} className={`border-t border-line align-top transition-colors duration-150 ${a.checked ? 'bg-accent-soft/40' : ''}`}>
                      <td className="px-4 pt-4">
                        <input
                          type="checkbox"
                          aria-label={`Pilih ${d.number}`}
                          checked={a.checked}
                          onChange={(e) => set(d, { checked: e.target.checked })}
                          className="size-[18px] cursor-pointer accent-[var(--color-accent)]"
                        />
                      </td>
                      <td className="px-2 py-3">
                        <span className="num font-semibold">{d.number}</span>
                        <span className="block text-xs text-muted">
                          {formatDate(d.date)} · total {formatAmount(d.payable)}
                        </span>
                      </td>
                      <td className="num px-2 py-3 whitespace-nowrap">
                        {formatDate(d.dueDate)}
                        {d.daysOverdue > 0 && (
                          <Badge tone="bad" className="ml-2">
                            +{d.daysOverdue} hr
                          </Badge>
                        )}
                      </td>
                      <td className="num px-2 py-3 text-right">{formatAmount(d.outstanding)}</td>
                      <td className="px-2 py-2">
                        <input
                          aria-label={`Nominal ${d.number}`}
                          inputMode="decimal"
                          disabled={!a.checked}
                          value={a.amount}
                          onChange={(e) => set(d, { amount: e.target.value })}
                          onBlur={(e) => {
                            const u = parseInput(e.target.value)
                            if (u !== null) set(d, { amount: formatAmount(u) })
                          }}
                          className={`${inputClass} num text-right ${amtErr || parseInput(a.amount) === null ? 'border-bad' : 'border-line-2'}`}
                        />
                        {amtErr && <p className="mt-1 text-[13px] text-bad">{amtErr}</p>}
                      </td>
                      {kind === 'sales' && (
                        <td className="px-2 py-2">
                          <div className="flex gap-2">
                            <select
                              aria-label={`Kode PPh ${d.number}`}
                              disabled={!a.checked}
                              value={a.whCode}
                              onChange={(e) => {
                                const code = pph.find((c) => c.code === e.target.value)
                                // Saran: tarif × DPP, sebanding dengan porsi yang dilunasi.
                                const paidPart = parseInput(a.amount) ?? 0n
                                const total = toUnits(d.total)
                                const base = d.dpp && total > 0n ? (toUnits(d.dpp) * paidPart) / total : 0n
                                set(d, { whCode: e.target.value, wh: code ? formatAmount(withholding(base, code)) : '' })
                              }}
                              className={`${inputClass} min-w-0 flex-1 cursor-pointer border-line-2 text-[13px]`}
                            >
                              <option value="">Tidak ada</option>
                              {pph.map((c) => (
                                <option key={c.code} value={c.code}>
                                  {c.name}
                                </option>
                              ))}
                            </select>
                            <div className="w-[120px] shrink-0">
                              <input
                                aria-label={`Nominal PPh ${d.number}`}
                                inputMode="decimal"
                                disabled={!a.checked || !a.whCode}
                                value={a.wh}
                                onChange={(e) => set(d, { wh: e.target.value })}
                                className={`${inputClass} num text-right ${whErr ? 'border-bad' : 'border-line-2'}`}
                              />
                            </div>
                          </div>
                          {whErr && <p className="mt-1 text-[13px] text-bad">{whErr}</p>}
                        </td>
                      )}
                      {collector && (
                        <td className="px-2 py-2">
                          <input
                            aria-label={`PPN dipungut ${d.number}`}
                            inputMode="decimal"
                            disabled={!a.checked}
                            placeholder="0"
                            value={a.vat}
                            onChange={(e) => set(d, { vat: e.target.value })}
                            onBlur={(e) => {
                              const u = e.target.value.trim() ? parseInput(e.target.value) : null
                              if (u !== null) set(d, { vat: formatAmount(u) })
                            }}
                            className={`${inputClass} num text-right ${vatErr || (a.vat.trim() && parseInput(a.vat) === null) ? 'border-bad' : 'border-line-2'}`}
                          />
                          {vatErr ? (
                            <p className="mt-1 text-[13px] text-bad">{vatErr}</p>
                          ) : (
                            toUnits(d.vat ?? '0') > 0n && <p className="mt-1 text-right text-[12px] text-muted">PPN faktur {formatAmount(d.vat ?? '0')}</p>
                          )}
                        </td>
                      )}
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {err && <Alert shakeKey={attempt}>{err.message}</Alert>}

      <div className="sticky bottom-0 z-10 -mx-4 flex flex-wrap items-center gap-x-8 gap-y-3 border-t border-line bg-surface/95 px-4 py-4 backdrop-blur sm:-mx-8 sm:px-8">
        <Sum label={kind === 'sales' ? 'Piutang dilunasi' : 'Utang dibayar'} value={allocated} />
        {kind === 'sales' && <Sum label="PPh dipotong" value={withheld} />}
        {collector && <Sum label="PPN dipungut" value={collected} />}
        <Sum label={kind === 'sales' ? 'Uang diterima' : 'Uang keluar'} value={cash} strong />
        <div className="ml-auto flex gap-2.5">
          <Button variant="secondary" onClick={() => navigate(t.base)}>
            Batal
          </Button>
          <Button
            busy={create.isPending}
            disabled={(chosen.length === 0 && (advanceU ?? 0n) <= 0n) || (cash > 0n && !cashAccountId) || unreadable || cash < 0n || (cash === 0n && (fromAdvanceU ?? 0n) === 0n && collected === 0n)}
            onClick={submit}
          >
            Posting {t.settle_}
          </Button>
        </div>
      </div>
    </div>
  )
}

function Sum({ label, value, strong }: { label: string; value: bigint; strong?: boolean }) {
  return (
    <div className="flex flex-col">
      <span className="text-[12.5px] text-muted">{label}</span>
      <span className={`num font-semibold ${strong ? 'text-xl' : 'text-lg'}`}>{formatAmount(value)}</span>
    </div>
  )
}

/** Pengembalian uang muka: ke pelanggan (kas keluar) atau dari pemasok (kas masuk). */
function RefundDialog({ kind, companyId, contactId, balance, onClose }: { kind: TradeKind; companyId: string | null; contactId: string; balance: bigint; onClose: () => void }) {
  const refund = useRefundAdvance(kind, companyId)
  const toast = useToast()
  const accounts = (useCashAccounts(companyId).data ?? []).filter((a) => a.active)
  const [key] = useState(newIdempotencyKey)
  const [v, setV] = useState({ date: todayISO(), cashAccountId: '', amount: formatAmount(balance), memo: '' })
  const amount = parseInput(v.amount)
  const err = refund.error instanceof ApiError ? refund.error : null
  return (
    <Dialog
      open
      onClose={onClose}
      title={kind === 'sales' ? 'Kembalikan uang muka pelanggan' : 'Catat pengembalian uang muka dari pemasok'}
      description={`Saldo uang muka Rp ${formatAmount(balance)}.`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button
            busy={refund.isPending}
            disabled={!v.cashAccountId || amount === null || amount <= 0n}
            onClick={() =>
              refund.mutate(
                { contactId, date: v.date, cashAccountId: v.cashAccountId, amount: fromUnits(amount ?? 0n), memo: v.memo, idempotencyKey: key },
                { onSuccess: (r) => (toast(`${r.number} diposting`), onClose()) },
              )
            }
          >
            Posting
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field id="ref-tanggal" label="Tanggal" type="date" value={v.date} onChange={(e) => setV({ ...v, date: e.target.value })} error={err?.fields.date} />
        <SelectField id="ref-rekening" label={kind === 'sales' ? 'Dibayar dari rekening' : 'Masuk ke rekening'} value={v.cashAccountId} onChange={(e) => setV({ ...v, cashAccountId: e.target.value })} error={err?.fields.cashAccountId}>
          <option value="">Pilih rekening</option>
          {accounts.map((a) => (
            <option key={a.accountId} value={a.accountId}>
              {a.name}
            </option>
          ))}
        </SelectField>
        <Field id="ref-nominal" label="Nominal" inputMode="decimal" className="num text-right" value={v.amount} onChange={(e) => setV({ ...v, amount: e.target.value })} error={err?.fields.amount} />
        <Field id="ref-catatan" label="Catatan" value={v.memo} maxLength={500} onChange={(e) => setV({ ...v, memo: e.target.value })} />
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

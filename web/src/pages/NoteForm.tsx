import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { formatDate, monthOf, todayISO } from '../lib/dates'
import { useCreateNote, useDoc, useTaxCodes, type DocDetail, type TradeKind } from '../lib/docs'
import { formatQty, useProducts } from '../lib/inventory'
import { tradeText } from '../lib/labels'
import { formatAmount, formatRp, fromUnits, parseInput, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { computeLine, computeTotals, withholding, type LineCalc } from '../lib/trade'
import { useToast } from '../components/Toast'
import { Alert, Button, Card, EmptyState, Field, Skeleton, inputClass } from '../components/ui'

type Pick = { checked: boolean; qty: string; price: string; restock: boolean }
type DocLine = DocDetail['lines'][number]

/** Nota kredit/retur penjualan (AR-05) atau nota debit/retur pembelian (AP-05) atas satu dokumen. */
export function NoteForm({ me, kind }: { me: Me; kind: TradeKind }) {
  const t = tradeText[kind]
  const companyId = me.activeCompanyId
  const [params] = useSearchParams()
  const docId = params.get('dok')
  const doc = useDoc(kind, companyId, docId)
  const d = doc.data && doc.data.id === docId ? doc.data : undefined
  if (!can(me, t.perm.create)) return <EmptyState title={`Anda tidak punya izin membuat ${t.note_}`} />
  if (!docId) return <EmptyState title={`Pilih ${t.doc_} dulu`} body={`Buka ${t.doc_} yang diposting, lalu pilih "Buat ${t.note_} / retur".`} />
  if (doc.isError) return <Alert>{doc.error.message}</Alert>
  if (!d) return <Skeleton className="h-96" />
  return <NoteFormBody me={me} kind={kind} d={d} />
}

function NoteFormBody({ me, kind, d }: { me: Me; kind: TradeKind; d: DocDetail }) {
  const t = tradeText[kind]
  const companyId = me.activeCompanyId
  const navigate = useNavigate()
  const toast = useToast()
  const create = useCreateNote(kind, companyId)
  const codes = useTaxCodes(companyId, d.date).data ?? []
  const products = useProducts(companyId, { all: true }, can(me, 'inventory.view'))
  const goods = new Set((products.data ?? []).filter((p) => p.kind === 'barang').map((p) => p.id))
  const unit = new Map((products.data ?? []).map((p) => [p.id, p.unit]))
  const [date, setDate] = useState(todayISO() < d.date ? d.date : todayISO())
  const [memo, setMemo] = useState('')
  const [picks, setPicks] = useState<Record<string, Pick>>({})
  const [key] = useState(newIdempotencyKey)
  const [attempt, setAttempt] = useState(0)
  const err = create.error instanceof ApiError ? create.error : null

  const isGoods = (l: DocLine) => Boolean(l.productId && goods.has(l.productId))
  const leftNet = (l: DocLine) => toUnits(l.net) - toUnits(l.notedNet)
  const leftQty = (l: DocLine) => toUnits(l.qty) - toUnits(l.returnedQty ?? '0')
  // Retur barang memakai sisa kuantitas yang belum diretur; baris lain mulai dari kuantitas penuh.
  const pick = (l: DocLine): Pick =>
    picks[l.id!] ?? {
      checked: false,
      qty: formatQty(fromUnits(isGoods(l) && leftQty(l) > 0n ? leftQty(l) : toUnits(l.qty))),
      price: formatAmount(l.unitPrice),
      restock: isGoods(l),
    }
  function set(l: DocLine, patch: Partial<Pick>) {
    setPicks((m) => ({ ...m, [l.id!]: { ...pick(l), ...patch } }))
  }

  const lines = d.lines.filter((l) => l.id)
  const chosen = lines.filter((l) => pick(l).checked)
  const calcs = new Map<string, LineCalc>(
    chosen.map((l) => {
      const p = pick(l)
      return [l.id!, computeLine({ qty: p.qty, unitPrice: p.price, discountPct: l.discountPct, taxCode: l.taxCode }, codes)]
    }),
  )
  const totals = computeTotals([...calcs.values()])
  const whCode = codes.find((c) => c.code === d.withholdingCode)
  const wh = kind === 'purchase' ? withholding(totals.dpp, whCode) : 0n
  const reduces = totals.total - wh
  const outstanding = toUnits(d.outstanding)
  const applied = reduces < outstanding ? reduces : outstanding > 0n ? outstanding : 0n
  const excess = reduces - applied
  const invalid = chosen.some((l) => calcs.get(l.id!)?.invalid)

  function submit() {
    setAttempt((n) => n + 1)
    create.mutate(
      {
        idempotencyKey: key,
        input: {
          docId: d.id,
          date,
          memo: memo.trim(),
          lines: chosen.map((l) => {
            const p = pick(l)
            const price = parseInput(p.price) ?? 0n
            return {
              docLineId: l.id!,
              qty: fromUnits(parseInput(p.qty) ?? 0n),
              unitPrice: price === toUnits(l.unitPrice) ? undefined : fromUnits(price),
              restock: kind === 'sales' && isGoods(l) && p.restock,
            }
          }),
        },
      },
      {
        onSuccess: (n) => {
          toast(`${n.number} diposting · ${formatRp(n.total)}`)
          navigate(`${t.base}?tab=${t.tabs.notes}&bulan=${monthOf(n.date)}&nid=${n.id}`)
        },
      },
    )
  }

  const blocked =
    d.status !== 'posted'
      ? `${t.doc} ini belum diposting atau sudah di-void.`
      : d.kind && d.kind !== 'biasa'
        ? `${t.doc} ini dibuat dari halaman proyek; koreksinya lewat void dan ${t.doc_} baru dari proyek.`
        : null

  return (
    <div className="flex flex-col gap-5">
      <div>
        <Link to={`${t.base}?id=${d.id}`} className="text-sm font-medium text-muted hover:text-ink">
          ← {d.number}
        </Link>
        <h1 className="mt-1 text-[26px] font-bold tracking-tight">{kind === 'sales' ? 'Nota kredit / retur penjualan' : 'Nota debit / retur pembelian'}</h1>
        <p className="mt-1.5 max-w-2xl text-sm text-muted">
          {kind === 'sales'
            ? `Atas faktur ${d.number} untuk ${d.contactName}. Nilai nota mengurangi pendapatan, PPN keluaran, dan sisa piutang faktur; bila faktur sudah lunas, kelebihannya menjadi uang muka pelanggan. Barang yang dicentang kembali ke stok dengan HPP saat dijual.`
            : `Atas tagihan ${d.number} dari ${d.contactName}. Nilai nota mengurangi biaya atau persediaan, PPN masukan, dan sisa utang tagihan; bila tagihan sudah lunas, kelebihannya menjadi uang muka pembelian. Barang persediaan keluar dari stok dengan harga di tagihan.`}
        </p>
      </div>
      {blocked ? (
        <Alert tone="warn">{blocked}</Alert>
      ) : (
        <>
          <Card>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
              <Field id="nota-tanggal" label="Tanggal nota" type="date" min={d.date} value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} />
              <Field id="nota-alasan" label="Alasan" placeholder="mis. barang cacat, potongan harga" value={memo} maxLength={500} onChange={(e) => setMemo(e.target.value)} error={err?.fields.memo} />
            </div>
            <p className="text-[13px] text-muted">
              Tarif dan kode pajak mengikuti {t.doc_} asal tanggal {formatDate(d.date)}. Sisa {t.doc_} saat ini Rp {formatAmount(d.outstanding)}.
            </p>
          </Card>

          <div className="overflow-hidden rounded-[14px] border border-line bg-surface">
            <div className="overflow-x-auto">
              <table className="w-full min-w-[860px] text-left text-sm">
                <thead className="bg-hover/60 text-[12.5px] text-muted">
                  <tr>
                    <th className="w-12 px-4 py-3">
                      <span className="sr-only">Pilih</span>
                    </th>
                    <th className="px-2 py-3 font-semibold">Baris {t.doc_}</th>
                    <th className="w-[130px] px-2 py-3 text-right font-semibold">Kuantitas</th>
                    <th className="w-[170px] px-2 py-3 text-right font-semibold">Harga satuan</th>
                    <th className="px-2 py-3 text-right font-semibold">DPP nota</th>
                    <th className="px-2 py-3 text-right font-semibold">PPN</th>
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l) => {
                    const p = pick(l)
                    const n = chosen.indexOf(l)
                    const c = calcs.get(l.id!)
                    const left = leftNet(l)
                    const done = left <= 0n || (kind === 'purchase' && isGoods(l) && leftQty(l) <= 0n)
                    const qtyErr = n >= 0 ? (err?.fields[`lines.${n}.qty`] ?? err?.fields[`lines.${n}.invoiceLineId`] ?? err?.fields[`lines.${n}.billLineId`]) : undefined
                    const priceErr = n >= 0 ? err?.fields[`lines.${n}.unitPrice`] : undefined
                    const restockErr = n >= 0 ? err?.fields[`lines.${n}.restock`] : undefined
                    return (
                      <tr key={l.id} className={`border-t border-line align-top transition-colors duration-150 ${p.checked ? 'bg-accent-soft/40' : ''} ${done ? 'text-muted' : ''}`}>
                        <td className="px-4 pt-4">
                          <input
                            type="checkbox"
                            aria-label={`Pilih ${l.description || l.accountName}`}
                            checked={p.checked}
                            disabled={done}
                            onChange={(e) => set(l, { checked: e.target.checked })}
                            className="size-[18px] cursor-pointer accent-[var(--color-accent)] disabled:cursor-not-allowed"
                          />
                        </td>
                        <td className="px-2 py-3">
                          <span className="block font-medium">{l.description || l.accountName}</span>
                          <span className="block text-[12.5px] text-muted">
                            {formatQty(l.qty)}
                            {l.productId && unit.get(l.productId) ? ` ${unit.get(l.productId)}` : ''} × {formatAmount(l.unitPrice)}
                            {toUnits(l.discountPct) > 0n && ` · diskon ${formatAmount(l.discountPct)}%`} · DPP {formatAmount(l.net)} · {l.taxCode}
                          </span>
                          {toUnits(l.notedNet) > 0n && (
                            <span className="block text-[12.5px] text-muted">
                              Sudah di{kind === 'sales' ? 'kreditkan' : 'kembalikan'} {formatAmount(l.notedNet)}
                              {toUnits(l.returnedQty ?? '0') > 0n && ` · diretur ${formatQty(l.returnedQty!)}`}
                              {done && ' · tidak ada sisa'}
                            </span>
                          )}
                          {kind === 'sales' && isGoods(l) && (
                            <label className="mt-1.5 flex w-fit cursor-pointer items-center gap-2 text-[13px]">
                              <input
                                type="checkbox"
                                checked={p.restock}
                                disabled={!p.checked}
                                onChange={(e) => set(l, { restock: e.target.checked })}
                                className="size-4 cursor-pointer accent-[var(--color-accent)]"
                              />
                              Barang kembali ke stok
                            </label>
                          )}
                          {kind === 'purchase' && isGoods(l) && p.checked && <span className="mt-1 block text-[13px] text-muted">Barang keluar dari stok, dikembalikan ke pemasok.</span>}
                          {restockErr && <p className="mt-1 text-[13px] text-bad">{restockErr}</p>}
                        </td>
                        <td className="px-2 py-2">
                          <input
                            aria-label={`Kuantitas ${l.description}`}
                            inputMode="decimal"
                            disabled={!p.checked}
                            value={p.qty}
                            onChange={(e) => set(l, { qty: e.target.value })}
                            className={`${inputClass} num text-right ${qtyErr || c?.invalid === 'qty' ? 'border-bad' : 'border-line-2'}`}
                          />
                          {qtyErr && <p className="mt-1 text-[13px] text-bad">{qtyErr}</p>}
                        </td>
                        <td className="px-2 py-2">
                          <input
                            aria-label={`Harga ${l.description}`}
                            inputMode="decimal"
                            disabled={!p.checked}
                            value={p.price}
                            onChange={(e) => set(l, { price: e.target.value })}
                            onBlur={(e) => {
                              const u = parseInput(e.target.value)
                              if (u !== null) set(l, { price: formatAmount(u) })
                            }}
                            className={`${inputClass} num text-right ${priceErr || c?.invalid === 'unitPrice' ? 'border-bad' : 'border-line-2'}`}
                          />
                          {priceErr && <p className="mt-1 text-[13px] text-bad">{priceErr}</p>}
                        </td>
                        <td className="num px-2 py-3 text-right">{c && !c.invalid ? formatAmount(c.net) : '—'}</td>
                        <td className="num px-2 py-3 text-right">{c && !c.invalid ? formatAmount(c.vat) : '—'}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </div>

          {err && <Alert shakeKey={attempt}>{err.message}</Alert>}

          <div className="sticky bottom-0 z-10 -mx-4 flex flex-wrap items-center gap-x-8 gap-y-3 border-t border-line bg-surface/95 px-4 py-4 backdrop-blur sm:-mx-8 sm:px-8">
            <Sum label="DPP" value={totals.dpp} />
            <Sum label="PPN" value={totals.vat} />
            {wh > 0n && <Sum label={`PPh batal dipotong`} value={wh} />}
            <Sum label={`Mengurangi sisa ${t.doc_}`} value={applied} strong />
            {excess > 0n && <Sum label={kind === 'sales' ? 'Menjadi uang muka pelanggan' : 'Menjadi uang muka pembelian'} value={excess} />}
            <div className="ml-auto flex gap-2.5">
              <Button variant="secondary" onClick={() => navigate(`${t.base}?id=${d.id}`)}>
                Batal
              </Button>
              <Button busy={create.isPending} disabled={chosen.length === 0 || invalid || totals.total <= 0n} onClick={submit}>
                Posting {t.note_}
              </Button>
            </div>
          </div>
        </>
      )}
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

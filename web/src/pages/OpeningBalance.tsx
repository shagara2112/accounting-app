import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { ApiError, type Account, type Me } from '../lib/api'
import { addMonths, formatDate, monthEnd, monthOf, todayISO } from '../lib/dates'
import { useOpening, useSaveOpening, type ControlledAccount, type OpeningState } from '../lib/imports'
import { useAccounts } from '../lib/ledger'
import { formatAmount, fromUnits, parseInput, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Checkbox, EmptyState, Field, PageHeader, Skeleton, TextAreaField, inputClass } from '../components/ui'

type Amounts = Record<string, { debit: string; credit: string }>

const importLabel: Record<ControlledAccount['kind'], string> = {
  'piutang-awal': 'Impor saldo awal piutang',
  'utang-awal': 'Impor saldo awal utang',
  'stok-awal': 'Impor saldo awal stok',
}

/** Saldo awal per akun diketik di layar (GL-06). Hasilnya satu jurnal saldo awal (seri SA) yang
 *  bisa dikoreksi selama periodenya belum dikunci. */
export function OpeningBalancePage({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const opening = useOpening(companyId)
  const accounts = useAccounts(companyId)
  if (!can(me, 'ledger.view')) return <EmptyState title="Anda tidak punya izin melihat buku besar" />
  if (opening.isPending || accounts.isPending) return <Skeleton className="h-96" />
  if (!opening.data || !accounts.data) {
    const err = opening.error ?? accounts.error
    return (
      <div className="flex flex-col gap-5">
        <PageHeader title="Saldo awal" />
        <Alert>
          {err?.message}{' '}
          {err instanceof ApiError && err.code === 'ledger.role_account' && (
            <Link to="/akun/sistem" className="font-semibold underline">
              Buka Akun sistem
            </Link>
          )}
        </Alert>
      </div>
    )
  }
  return (
    <OpeningForm
      key={`${opening.data.entryId ?? 'baru'}-${opening.data.revision}`}
      me={me}
      state={opening.data}
      accounts={accounts.data.items}
      classes={accounts.data.classes.map((c) => c.name)}
    />
  )
}

function OpeningForm({ me, state, accounts, classes }: { me: Me; state: OpeningState; accounts: Account[]; classes: string[] }) {
  const save = useSaveOpening(me.activeCompanyId)
  const toast = useToast()
  const canEdit = can(me, 'ledger.journal.create')
  const [date, setDate] = useState(state.date ?? monthEnd(addMonths(monthOf(todayISO()), -1)))
  const [amounts, setAmounts] = useState<Amounts>(() =>
    Object.fromEntries(state.lines.map((l) => [l.accountId, { debit: formatAmount(l.debit, { blankZero: true }), credit: formatAmount(l.credit, { blankZero: true }) }])),
  )
  const [search, setSearch] = useState('')
  const [filledOnly, setFilledOnly] = useState(false)
  const [reason, setReason] = useState('')
  const [attempt, setAttempt] = useState(0)
  const controlled = useMemo(() => new Map(state.controlled.map((c) => [c.accountId, c])), [state.controlled])

  // Akun detail yang aktif, ditambah akun nonaktif yang sudah punya saldo awal.
  const rows = accounts.filter((a) => a.postable && (a.active || amounts[a.id]) && a.id !== state.openingEquityId)
  const q = search.trim().toLowerCase()
  const filled = (id: string) => Boolean(amounts[id]?.debit.trim() || amounts[id]?.credit.trim())
  const visible = rows.filter((a) => (!q || a.code.toLowerCase().includes(q) || a.name.toLowerCase().includes(q)) && (!filledOnly || filled(a.id) || controlled.has(a.id)))
  const equity = accounts.find((a) => a.id === state.openingEquityId)

  let debit = 0n
  let credit = 0n
  let unreadable = false
  // Akun yang hanya bisa diisi lewat impor (persediaan) tidak ikut dijumlah.
  const locked = (id: string) => controlled.has(id) && !controlled.get(id)!.manual
  for (const [id, v] of Object.entries(amounts)) {
    if (locked(id)) continue
    const d = parseInput(v.debit)
    const c = parseInput(v.credit)
    if (d === null || c === null) unreadable = true
    debit += d ?? 0n
    credit += c ?? 0n
  }
  const diff = debit - credit
  const err = save.error instanceof ApiError ? save.error : null

  function setAmount(id: string, side: 'debit' | 'credit', value: string) {
    setAmounts((m) => {
      const cur = m[id] ?? { debit: '', credit: '' }
      const next = { ...cur, [side]: value }
      if (value.trim()) next[side === 'debit' ? 'credit' : 'debit'] = ''
      return { ...m, [id]: next }
    })
  }

  function submit() {
    setAttempt((n) => n + 1)
    const lines = Object.entries(amounts)
      .filter(([id]) => !locked(id))
      .map(([accountId, v]) => ({ accountId, debit: fromUnits(parseInput(v.debit) ?? 0n), credit: fromUnits(parseInput(v.credit) ?? 0n) }))
      .filter((l) => toUnits(l.debit) !== 0n || toUnits(l.credit) !== 0n)
    save.mutate(
      { date, lines, revision: state.revision, reason: reason.trim() },
      { onSuccess: (s) => toast(`Saldo awal disimpan sebagai ${s.number}${toUnits(s.openingEquity) === 0n ? ' · seimbang' : ''}`) },
    )
  }

  let lastClass = 0
  return (
    <div className="flex flex-col gap-5 pb-28">
      <PageHeader
        title="Saldo awal"
        subtitle="Saldo setiap akun pada tanggal mulai memakai Lajur, biasanya akhir bulan sebelumnya. Angka ini menjadi saldo awal di Buku besar dan laporan sesudah tanggal itu."
        actions={
          state.number && (
            <Link to={`/jurnal?bulan=${monthOf(state.date!)}&id=${state.entryId}`} className="text-sm font-semibold text-accent hover:underline">
              Lihat jurnal {state.number} →
            </Link>
          )
        }
      />

      <div className="flex flex-wrap items-end gap-4">
        <div className="w-[220px]">
          <Field id="sa-tanggal" label="Tanggal saldo awal" type="date" disabled={!canEdit} value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} />
        </div>
        <input
          type="search"
          aria-label="Cari akun"
          placeholder="Cari kode atau nama akun"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className={`${inputClass} max-w-[280px] flex-1 border-line-2`}
        />
        <Checkbox checked={filledOnly} onChange={setFilledOnly} label="Hanya akun yang terisi" />
      </div>

      <Alert tone="neutral">
        Mulai di tengah tahun? Isi juga saldo akun <b>pendapatan dan beban sejak awal tahun buku</b>, supaya laba rugi setahun tetap lengkap. Piutang usaha dan utang usaha boleh diketik langsung, atau diisi
        per faktur dan tagihan lewat <Link to="/impor" className="font-semibold underline">Impor data</Link> supaya umur piutang dan utang ikut benar. Persediaan diisi per barang lewat impor supaya kartu stok
        benar.
      </Alert>

      <div className="overflow-hidden rounded-[14px] border border-line bg-surface">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-left text-sm">
            <thead className="bg-hover/60 text-[12.5px] text-muted">
              <tr>
                <th className="px-4 py-3 font-semibold">Akun</th>
                <th className="w-[200px] px-2 py-3 text-right font-semibold">Debit</th>
                <th className="w-[200px] px-4 py-3 text-right font-semibold">Kredit</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((a) => {
                const header = a.class !== lastClass ? classes[a.class - 1] : null
                lastClass = a.class
                const ctl = controlled.get(a.id)
                const v = amounts[a.id] ?? { debit: '', credit: '' }
                const rowErr = err?.fields[`lines.${a.id}`]
                const bad = (s: string) => parseInput(s) === null
                return [
                  header && (
                    <tr key={`k-${a.class}`} className="border-t border-line bg-hover/50">
                      <td colSpan={3} className="px-4 py-2 text-xs font-semibold tracking-wide text-muted uppercase">
                        {a.class} · {header}
                      </td>
                    </tr>
                  ),
                  <tr key={a.id} className="border-t border-line align-top">
                    <td className="px-4 py-2.5">
                      <span className="num mr-2 text-[12.5px] text-muted">{a.code}</span>
                      {a.name}
                      {!a.active && <Badge tone="neutral">Nonaktif</Badge>}
                      {ctl && (
                        <span className={`block text-xs ${ctl.manual ? 'text-warn' : 'text-muted'}`}>
                          {ctl.reason}{' '}
                          <Link to={`/impor?jenis=${ctl.kind}`} className="font-semibold text-accent hover:underline">
                            {importLabel[ctl.kind]} →
                          </Link>
                        </span>
                      )}
                      {ctl?.manual && toUnits(ctl.balance) !== 0n && state.date && (
                        <span className="block text-xs text-muted">
                          Sudah diimpor per dokumen: <span className="num font-semibold text-ink">{formatAmount(ctl.balance, { parens: true })}</span> per {formatDate(state.date)}. Yang
                          diketik di sini menambah saldo itu; jangan diisi dua kali.
                        </span>
                      )}
                      {rowErr && <span className="block text-[13px] text-bad">{rowErr}</span>}
                    </td>
                    {ctl && !ctl.manual ? (
                      <td colSpan={2} className="num px-4 py-2.5 text-right text-ink-2">
                        {state.entryId && state.date ? `${formatAmount(ctl.balance, { parens: true })} per ${formatDate(state.date)}` : '—'}
                      </td>
                    ) : (
                      (['debit', 'credit'] as const).map((side) => (
                        <td key={side} className={side === 'debit' ? 'px-2 py-1.5' : 'px-4 py-1.5 pl-2'}>
                          <input
                            aria-label={`${side === 'debit' ? 'Debit' : 'Kredit'} ${a.code} ${a.name}`}
                            inputMode="decimal"
                            autoComplete="off"
                            placeholder="0"
                            disabled={!canEdit}
                            value={v[side]}
                            onChange={(e) => setAmount(a.id, side, e.target.value)}
                            onBlur={(e) => {
                              const u = parseInput(e.target.value)
                              if (u !== null) setAmount(a.id, side, u === 0n ? '' : formatAmount(u))
                            }}
                            className={`${inputClass} num text-right ${bad(v[side]) || rowErr ? 'border-bad' : 'border-line-2'}`}
                          />
                        </td>
                      ))
                    )}
                  </tr>,
                ]
              })}
              {visible.length === 0 && (
                <tr>
                  <td colSpan={3} className="px-4 py-6 text-center text-sm text-muted">
                    Tidak ada akun yang cocok.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {canEdit && state.entryId && (
        <TextAreaField id="sa-alasan" label="Alasan perubahan (opsional)" rows={2} maxLength={300} value={reason} onChange={(e) => setReason(e.target.value)} error={err?.fields.reason} />
      )}
      {err && Object.keys(err.fields).length === 0 && <Alert shakeKey={attempt}>{err.message}</Alert>}

      <div className="fixed inset-x-0 bottom-0 z-10 border-t border-line bg-surface/95 px-4 py-3 backdrop-blur sm:px-8 lg:left-[244px] print:hidden">
        <div className="mx-auto flex max-w-[1920px] flex-wrap items-center gap-x-6 gap-y-2">
          <Total label="Total debit" value={debit} />
          <Total label="Total kredit" value={credit} />
          <span className="flex flex-col text-[12.5px] text-muted">
            {debit + credit === 0n ? (
              <span>Belum ada saldo yang diisi.</span>
            ) : diff === 0n ? (
              <span className="font-semibold text-good">Seimbang</span>
            ) : (
              <>
                <span>
                  Selisih <span className="num font-semibold text-ink">Rp {formatAmount(diff < 0n ? -diff : diff)}</span> ke {equity ? `${equity.code} ${equity.name}` : 'ekuitas saldo awal'}
                </span>
                <span>Menjadi nol setelah semua saldo awal terisi dan seimbang.</span>
              </>
            )}
            {state.entryId && state.date && (
              <span>
                Ekuitas saldo awal per {formatDate(state.date)}: <span className="num font-semibold text-ink">{formatAmount(state.openingEquity, { parens: true })}</span>
                {toUnits(state.openingEquity) === 0n && ' · lengkap'}
              </span>
            )}
          </span>
          {canEdit && (
            <Button className="ml-auto" busy={save.isPending} disabled={unreadable || !date || debit + credit === 0n} onClick={submit}>
              {state.entryId ? 'Simpan perubahan' : 'Simpan saldo awal'}
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

function Total({ label, value }: { label: string; value: bigint }) {
  return (
    <span className="flex flex-col">
      <span className="text-[12.5px] text-muted">{label}</span>
      <span className="num font-semibold">{formatAmount(value)}</span>
    </span>
  )
}

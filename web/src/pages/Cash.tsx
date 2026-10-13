import { useState } from 'react'
import { motion } from 'motion/react'
import { useNavigate, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type CashKind, type Me } from '../lib/api'
import { useCashAccounts, useCashTransaction, useCashTransactions, useRecordCash, useRegisterCashAccount, useVoidCash } from '../lib/cash'
import { formatDate, monthEnd, monthLabel, monthOf, monthStart, todayISO } from '../lib/dates'
import { useAccounts } from '../lib/ledger'
import { formatAmount, formatRp, fromUnits, parseInput } from '../lib/money'
import { can } from '../lib/session'
import { AccountPicker } from '../components/AccountPicker'
import { MonthNav } from '../components/MonthNav'
import { useToast } from '../components/Toast'
import { VoidForm } from '../components/VoidForm'
import { Alert, Badge, Button, Dialog, Drawer, EmptyState, Field, PageHeader, Segmented, SelectField, Skeleton, TableShell, ease, inputClass, td, th } from '../components/ui'

const kindLabel: Record<CashKind, string> = { in: 'Penerimaan', out: 'Pengeluaran', transfer: 'Transfer' }
const kindTone = { in: 'good', out: 'warn', transfer: 'accent' } as const

export function CashPage({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const month = params.get('bulan') ?? monthOf(todayISO())
  const account = params.get('rekening') ?? undefined
  const openId = params.get('id')
  const accounts = useCashAccounts(companyId)
  const list = useCashTransactions(companyId, { from: monthStart(month), to: monthEnd(month), account })
  const [recording, setRecording] = useState<CashKind | null>(null)
  const [registering, setRegistering] = useState(false)
  const canCreate = can(me, 'cash.create')

  function update(next: Record<string, string | null>) {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v)
      else p.delete(k)
    }
    setParams(p, { replace: true })
  }

  const items = list.data ?? []
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Kas dan bank"
        subtitle="Saldo rekening adalah saldo akun buku besarnya, sehingga pelunasan dan pembayaran dari penjualan dan pembelian ikut terhitung. Transaksi bank dicatat manual."
        actions={
          canCreate && (
            <>
              <Button variant="secondary" onClick={() => setRecording('transfer')}>
                Transfer
              </Button>
              <Button variant="secondary" onClick={() => setRecording('out')}>
                Pengeluaran
              </Button>
              <Button onClick={() => setRecording('in')}>Penerimaan</Button>
            </>
          )
        }
      />

      <div className="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-3">
        {accounts.isPending && [0, 1, 2].map((i) => <Skeleton key={i} className="h-28" />)}
        {accounts.data?.map((a, i) => {
          const active = a.accountId === account
          return (
            <motion.button
              key={a.accountId}
              type="button"
              initial={{ opacity: 0, y: 6 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.2, delay: Math.min(i, 8) * 0.03, ease }}
              onClick={() => update({ rekening: active ? null : a.accountId })}
              aria-pressed={active}
              className={`flex cursor-pointer flex-col gap-1 rounded-[14px] border p-4 text-left transition-colors duration-150 ${active ? 'border-accent bg-accent-soft' : 'border-line bg-surface hover:bg-hover'} ${a.active ? '' : 'opacity-60'}`}
            >
              <span className="flex items-center justify-between gap-2 text-[12.5px] text-muted">
                <span className="num">{a.code}</span>
                <Badge tone={a.kind === 'bank' ? 'accent' : 'neutral'}>{a.kind === 'bank' ? 'Bank' : 'Kas'}</Badge>
              </span>
              <span className="font-semibold">{a.name}</span>
              <span className="num text-xs text-muted">{a.bankName ? `${a.bankName} · ${a.accountNumber}` : ' '}</span>
              <span className="num mt-1 text-xl font-semibold tracking-tight">{formatAmount(a.balance, { parens: true })}</span>
            </motion.button>
          )
        })}
        {canCreate && (
          <button
            type="button"
            onClick={() => setRegistering(true)}
            className="flex min-h-28 cursor-pointer flex-col items-center justify-center gap-1 rounded-[14px] border border-dashed border-line-2 text-sm font-semibold text-muted transition-colors hover:border-muted hover:text-ink"
          >
            + Daftarkan rekening
          </button>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <MonthNav value={month} onChange={(m) => update({ bulan: m })} />
        {account && (
          <>
            <Badge tone="accent">
              {accounts.data?.find((a) => a.accountId === account)?.name}
              <button type="button" aria-label="Hapus filter rekening" onClick={() => update({ rekening: null })} className="ml-1 cursor-pointer font-bold">
                ×
              </button>
            </Badge>
            <Button size="sm" variant="ghost" onClick={() => navigate(`/buku-besar?akun=${account}&dari=${monthStart(month)}&sampai=${monthEnd(month)}`)}>
              Lihat semua mutasi di buku besar →
            </Button>
          </>
        )}
      </div>

      <TableShell minWidth={820}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Tanggal</th>
            <th className={th}>Nomor</th>
            <th className={th}>Jenis</th>
            <th className={th}>Rekening</th>
            <th className={th}>Keterangan</th>
            <th className={`${th} text-right`}>Jumlah</th>
          </tr>
        </thead>
        <tbody>
          {list.isPending && (
            <tr>
              <td colSpan={6} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {items.map((x) => (
            <tr key={x.id} onClick={() => update({ id: x.id })} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${x.status === 'void' ? 'text-muted' : ''}`}>
              <td className={`${td} num whitespace-nowrap`}>{formatDate(x.date)}</td>
              <td className={`${td} num font-semibold whitespace-nowrap ${x.status === 'void' ? 'line-through' : 'text-accent'}`}>{x.number}</td>
              <td className={td}>
                <span className="flex items-center gap-1.5">
                  <Badge tone={kindTone[x.kind]}>{kindLabel[x.kind]}</Badge>
                  {x.status === 'void' && <Badge tone="bad">Void</Badge>}
                </span>
              </td>
              <td className={td}>{x.kind === 'transfer' ? `${x.cashAccount} → ${x.toAccount}` : x.cashAccount}</td>
              <td className={`${td} max-w-[260px] truncate`}>{x.memo || x.reference || '—'}</td>
              <td className={`${td} num text-right whitespace-nowrap ${x.kind === 'out' ? 'text-bad' : x.kind === 'in' ? 'text-good' : ''}`}>
                {x.kind === 'out' ? '−' : x.kind === 'in' ? '+' : ''}
                {formatAmount(x.amount)}
              </td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && (
        <EmptyState title={`Belum ada transaksi kas manual di ${monthLabel(month)}`} body="Pelunasan faktur dan pembayaran tagihan tercatat di Penjualan dan Pembelian; semuanya tetap terlihat di buku besar rekening." />
      )}

      <RecordDialog me={me} kind={recording} onClose={() => setRecording(null)} onKind={setRecording} defaultAccount={account} />
      <RegisterDialog me={me} open={registering} onClose={() => setRegistering(false)} />
      <TransactionDrawer me={me} id={openId} onClose={() => update({ id: null })} />
    </div>
  )
}

type Line = { key: string; accountId: string; amount: string; memo: string }
const blankLine = (): Line => ({ key: newIdempotencyKey(), accountId: '', amount: '', memo: '' })

function RecordDialog({ me, kind, onClose, onKind, defaultAccount }: { me: Me; kind: CashKind | null; onClose: () => void; onKind: (k: CashKind) => void; defaultAccount?: string }) {
  const companyId = me.activeCompanyId
  const accounts = useAccounts(companyId)
  const cashAccounts = (useCashAccounts(companyId).data ?? []).filter((a) => a.active)
  const record = useRecordCash(companyId)
  const toast = useToast()
  const [shownFor, setShownFor] = useState<CashKind | null>(null)
  const [date, setDate] = useState(todayISO())
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [amount, setAmount] = useState('')
  const [memo, setMemo] = useState('')
  const [reference, setReference] = useState('')
  const [lines, setLines] = useState<Line[]>([blankLine()])
  const [key, setKey] = useState(newIdempotencyKey)
  const [attempt, setAttempt] = useState(0)

  if (kind && shownFor === null) {
    setShownFor(kind)
    setDate(todayISO())
    setFrom(defaultAccount ?? cashAccounts[0]?.accountId ?? '')
    setTo('')
    setAmount('')
    setMemo('')
    setReference('')
    setLines([blankLine()])
    setKey(newIdempotencyKey())
    record.reset()
  }
  const current = kind ?? shownFor
  const err = record.error instanceof ApiError ? record.error : null
  const counter = (accounts.data?.items ?? []).filter((a) => !cashAccounts.some((c) => c.accountId === a.id))
  const total = current === 'transfer' ? (parseInput(amount) ?? 0n) : lines.reduce((s, l) => s + (parseInput(l.amount) ?? 0n), 0n)

  function close() {
    setShownFor(null)
    onClose()
  }

  function submit() {
    if (!current) return
    setAttempt((n) => n + 1)
    record.mutate(
      {
        idempotencyKey: key,
        input: {
          kind: current,
          date,
          cashAccountId: from,
          toAccountId: current === 'transfer' ? to : undefined,
          amount: fromUnits(current === 'transfer' ? total : 0n),
          memo: memo.trim(),
          reference: reference.trim(),
          lines:
            current === 'transfer'
              ? []
              : lines.filter((l) => l.accountId || l.amount).map((l) => ({ accountId: l.accountId, amount: fromUnits(parseInput(l.amount) ?? 0n), memo: l.memo.trim() })),
        },
      },
      {
        onSuccess: (x) => {
          toast(`${x.number} diposting · ${formatRp(x.amount)}`)
          close()
        },
      },
    )
  }

  return (
    <Dialog
      open={kind !== null}
      onClose={close}
      width={640}
      title={current ? kindLabel[current] : ''}
      description={
        current === 'in'
          ? 'Uang masuk ke rekening, mis. bunga jasa giro, setoran modal, atau pendapatan lain. Seri BKM.'
          : current === 'out'
            ? 'Uang keluar dari rekening, mis. biaya admin bank, pajak bunga, atau biaya kecil. Seri BKK.'
            : 'Pindah dana antar rekening kas dan bank milik PT ini. Seri TRF.'
      }
      footer={
        <>
          <span className="mr-auto self-center text-sm text-muted">
            Total <span className="num font-semibold text-ink">{formatRp(total)}</span>
          </span>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button busy={record.isPending} disabled={total <= 0n || !from || (current === 'transfer' && !to)} onClick={submit}>
            Posting
          </Button>
        </>
      }
    >
      {current && (
        <Segmented
          label="Jenis transaksi"
          value={current}
          onChange={(k) => {
            setShownFor(k)
            onKind(k)
          }}
          options={[
            { value: 'in', label: 'Penerimaan' },
            { value: 'out', label: 'Pengeluaran' },
            { value: 'transfer', label: 'Transfer' },
          ]}
        />
      )}
      <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
        <Field id="kas-tanggal" label="Tanggal" type="date" value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} />
        <SelectField id="kas-rekening" label={current === 'transfer' ? 'Dari rekening' : 'Rekening'} value={from} onChange={(e) => setFrom(e.target.value)} error={err?.fields.cashAccountId}>
          <option value="">Pilih rekening</option>
          {cashAccounts.map((a) => (
            <option key={a.accountId} value={a.accountId}>
              {a.name}
            </option>
          ))}
        </SelectField>
      </div>
      {current === 'transfer' ? (
        <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
          <SelectField id="kas-tujuan" label="Ke rekening" value={to} onChange={(e) => setTo(e.target.value)} error={err?.fields.toAccountId}>
            <option value="">Pilih rekening tujuan</option>
            {cashAccounts
              .filter((a) => a.accountId !== from)
              .map((a) => (
                <option key={a.accountId} value={a.accountId}>
                  {a.name}
                </option>
              ))}
          </SelectField>
          <Field id="kas-jumlah" label="Jumlah" inputMode="decimal" className="num text-right" value={amount} onChange={(e) => setAmount(e.target.value)} error={err?.fields.amount} />
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          <span className="text-sm font-medium text-ink-2">{current === 'in' ? 'Diterima dari akun' : 'Dibebankan ke akun'}</span>
          {lines.map((l, i) => (
            <div key={l.key} className="grid grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_130px_40px] items-start gap-2 max-sm:grid-cols-1">
              <div>
                <AccountPicker
                  id={`kas-akun-${l.key}`}
                  label={`Akun baris ${i + 1}`}
                  accounts={counter}
                  value={l.accountId}
                  invalid={Boolean(err?.fields[`lines.${i}.accountId`])}
                  onChange={(a) => setLines((list) => list.map((x) => (x.key === l.key ? { ...x, accountId: a } : x)))}
                />
                {err?.fields[`lines.${i}.accountId`] && <p className="mt-1 text-[13px] text-bad">{err.fields[`lines.${i}.accountId`]}</p>}
              </div>
              <input aria-label={`Keterangan baris ${i + 1}`} placeholder="Keterangan" value={l.memo} onChange={(e) => setLines((list) => list.map((x) => (x.key === l.key ? { ...x, memo: e.target.value } : x)))} className={`${inputClass} border-line-2`} />
              <input
                aria-label={`Jumlah baris ${i + 1}`}
                inputMode="decimal"
                placeholder="0"
                value={l.amount}
                onChange={(e) => setLines((list) => list.map((x) => (x.key === l.key ? { ...x, amount: e.target.value } : x)))}
                onBlur={(e) => {
                  const u = parseInput(e.target.value)
                  if (u !== null && e.target.value) setLines((list) => list.map((x) => (x.key === l.key ? { ...x, amount: formatAmount(u) } : x)))
                }}
                className={`${inputClass} num text-right ${err?.fields[`lines.${i}.amount`] || parseInput(l.amount) === null ? 'border-bad' : 'border-line-2'}`}
              />
              <button
                type="button"
                aria-label={`Hapus baris ${i + 1}`}
                disabled={lines.length <= 1}
                onClick={() => setLines((list) => list.filter((x) => x.key !== l.key))}
                className="flex size-11 cursor-pointer items-center justify-center rounded-[10px] text-muted hover:bg-hover hover:text-bad disabled:opacity-30"
              >
                ×
              </button>
            </div>
          ))}
          <Button size="sm" variant="ghost" className="self-start" onClick={() => setLines((list) => [...list, blankLine()])}>
            + Tambah baris
          </Button>
        </div>
      )}
      <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
        <Field id="kas-keterangan" label="Keterangan" value={memo} maxLength={500} onChange={(e) => setMemo(e.target.value)} />
        <Field id="kas-referensi" label="Referensi bank (opsional)" value={reference} maxLength={100} onChange={(e) => setReference(e.target.value)} />
      </div>
      {err && <Alert shakeKey={attempt}>{err.message}</Alert>}
    </Dialog>
  )
}

function RegisterDialog({ me, open, onClose }: { me: Me; open: boolean; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const accounts = useAccounts(companyId)
  const register = useRegisterCashAccount(companyId)
  const toast = useToast()
  const [accountId, setAccountId] = useState('')
  const [kind, setKind] = useState<'kas' | 'bank'>('bank')
  const [bankName, setBankName] = useState('')
  const [number, setNumber] = useState('')
  const [key] = useState(newIdempotencyKey)
  const assets = (accounts.data?.items ?? []).filter((a) => a.class === 1)
  const err = register.error instanceof ApiError ? register.error : null

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Daftarkan rekening kas atau bank"
      description="Pilih akun aset yang dipakai sebagai rekening, mis. 1-1113 Bank BRI. Buat dulu akunnya di Bagan akun bila belum ada."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button
            busy={register.isPending}
            disabled={!accountId}
            onClick={() =>
              register.mutate(
                { accountId, kind, bankName: bankName.trim(), accountNumber: number.trim(), active: true, idempotencyKey: key },
                { onSuccess: (a) => (toast(`${a.name} didaftarkan sebagai rekening ${a.kind}`), onClose()) },
              )
            }
          >
            Daftarkan
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-1.5">
        <label htmlFor="rek-akun" className="text-sm font-medium text-ink-2">
          Akun
        </label>
        <AccountPicker id="rek-akun" accounts={assets} value={accountId} onChange={setAccountId} invalid={Boolean(err?.fields.accountId)} />
        {err?.fields.accountId && <p className="text-[13px] text-bad">{err.fields.accountId}</p>}
      </div>
      <Segmented
        label="Jenis rekening"
        value={kind}
        onChange={setKind}
        options={[
          { value: 'bank', label: 'Bank' },
          { value: 'kas', label: 'Kas' },
        ]}
      />
      {kind === 'bank' && (
        <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
          <Field id="rek-bank" label="Nama bank" value={bankName} onChange={(e) => setBankName(e.target.value)} />
          <Field id="rek-nomor" label="Nomor rekening" value={number} onChange={(e) => setNumber(e.target.value)} />
        </div>
      )}
      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
    </Dialog>
  )
}

function TransactionDrawer({ me, id, onClose }: { me: Me; id: string | null; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const q = useCashTransaction(companyId, id)
  const x = q.data && q.data.id === id ? q.data : undefined
  const voidIt = useVoidCash(companyId)
  const toast = useToast()
  const navigate = useNavigate()
  const [voiding, setVoiding] = useState(false)
  const [key, setKey] = useState(newIdempotencyKey)

  return (
    <Drawer
      open={id !== null}
      onClose={() => {
        setVoiding(false)
        voidIt.reset()
        onClose()
      }}
      title={x?.number ?? 'Memuat…'}
      description={
        x && (
          <span className="flex items-center gap-2">
            <Badge tone={kindTone[x.kind]}>{kindLabel[x.kind]}</Badge>
            {x.status === 'void' ? <Badge tone="bad">Void</Badge> : <Badge tone="good">Diposting</Badge>}
            <span>{formatDate(x.date)}</span>
          </span>
        )
      }
      footer={
        x?.status === 'posted' &&
        can(me, 'cash.void') &&
        !voiding && (
          <Button
            variant="secondary"
            onClick={() => {
              setKey(newIdempotencyKey())
              voidIt.reset()
              setVoiding(true)
            }}
          >
            Void…
          </Button>
        )
      }
    >
      {!x && <Skeleton className="h-32" />}
      {x && (
        <>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
            <div>
              <dt className="text-[12.5px] text-muted">Rekening</dt>
              <dd>{x.kind === 'transfer' ? `${x.cashAccount} → ${x.toAccount}` : x.cashAccount}</dd>
            </div>
            <div>
              <dt className="text-[12.5px] text-muted">Jumlah</dt>
              <dd className="num font-semibold">Rp {formatAmount(x.amount)}</dd>
            </div>
            {x.reference && (
              <div>
                <dt className="text-[12.5px] text-muted">Referensi</dt>
                <dd>{x.reference}</dd>
              </div>
            )}
            <div>
              <dt className="text-[12.5px] text-muted">Dicatat oleh</dt>
              <dd>{x.createdBy ?? '—'}</dd>
            </div>
          </dl>
          {x.memo && <p className="text-sm text-ink-2">{x.memo}</p>}
          {x.status === 'void' && x.voidReason && <Alert tone="bad">Alasan void: {x.voidReason}</Alert>}
          {x.lines.length > 0 && (
            <div className="overflow-hidden rounded-[12px] border border-line">
              <table className="w-full text-left text-sm">
                <tbody>
                  {x.lines.map((l, i) => (
                    <tr key={i} className="border-t border-line first:border-0">
                      <td className="px-3 py-2.5">
                        <span className="num font-semibold text-ink-2">{l.accountCode}</span> {l.accountName}
                        {l.memo && <span className="block text-[12.5px] text-muted">{l.memo}</span>}
                      </td>
                      <td className="num px-3 py-2.5 text-right">{formatAmount(l.amount)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <button type="button" onClick={() => navigate(`/jurnal?bulan=${monthOf(x.date)}&id=${x.journalEntryId}`)} className="self-start text-sm font-semibold text-accent hover:underline">
            Lihat jurnal {x.number} →
          </button>
          {voiding && (
            <VoidForm
              title={`Void ${x.number}`}
              note="Transaksi tidak dihapus; jurnalnya dibalik pada tanggal void dan saldo rekening kembali."
              minDate={x.date}
              busy={voidIt.isPending}
              error={voidIt.error}
              onCancel={() => setVoiding(false)}
              onConfirm={(date, reason) =>
                voidIt.mutate({ id: x.id, date, reason, idempotencyKey: key }, { onSuccess: (v) => (toast(`${v.number} di-void`), setVoiding(false)) })
              }
            />
          )}
        </>
      )}
    </Drawer>
  )
}


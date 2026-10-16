import { useMemo, useState, type FormEvent, type KeyboardEvent } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type EntryDetail, type Me } from '../lib/api'
import { addMonths, formatDate, monthEnd, monthLabel, monthOf, monthStart, todayISO } from '../lib/dates'
import { useAccounts, useEntry, usePeriods, usePostJournal, useReviseJournal } from '../lib/ledger'
import { periodStatus } from '../lib/labels'
import { formatAmount, formatRp, fromUnits, parseInput } from '../lib/money'
import { useProjectOptions } from '../lib/projects'
import { can } from '../lib/session'
import { AccountPicker } from '../components/AccountPicker'
import { SearchSelect } from '../components/SearchSelect'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Checkbox, EmptyState, Field, Skeleton, ease, inputClass } from '../components/ui'

// branchId dan departmentId hanya dibawa dari jurnal yang sedang diubah, supaya dimensinya tidak hilang.
type Row = { key: string; accountId: string; memo: string; debit: string; credit: string; projectId: string; branchId?: string; departmentId?: string }
type RowErrors = Record<string, { account?: string; amount?: string; memo?: string }>

const blankRow = (): Row => ({ key: newIdempotencyKey(), accountId: '', memo: '', debit: '', credit: '', projectId: '' })
const isEmpty = (r: Row) => !r.accountId && !r.memo.trim() && !r.debit.trim() && !r.credit.trim()

export function JournalForm({ me }: { me: Me }) {
  const [params] = useSearchParams()
  const kind = params.get('jenis') === 'penyesuaian' ? 'penyesuaian' : 'umum'
  // Form baru setiap kali jenis berganti, supaya tanggal default dan isian tidak terbawa.
  return <JournalFormFor key={kind} me={me} kind={kind} />
}

/** Mengubah jurnal manual yang sudah diposting selama periodenya belum dikunci (revisi klien). */
export function JournalEdit({ me }: { me: Me }) {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const entry = useEntry(me.activeCompanyId, id)
  const e = entry.data && entry.data.id === id ? entry.data : undefined
  if (entry.isError) return <EmptyState title="Jurnal tidak ditemukan" body={entry.error.message} />
  if (!e) return <Skeleton className="h-96" />
  if (!e.editable || (e.kind !== 'umum' && e.kind !== 'penyesuaian')) {
    return (
      <EmptyState
        title={`${e.number} tidak bisa diubah`}
        body={e.editBlock ?? 'Hanya jurnal manual yang belum di-void dan periodenya belum dikunci yang bisa diubah. Jurnal dari dokumen dikoreksi lewat dokumennya.'}
        action={
          <Button variant="secondary" onClick={() => navigate(`/jurnal?bulan=${monthOf(e.date)}&id=${e.id}`)}>
            Kembali ke jurnal
          </Button>
        }
      />
    )
  }
  return <JournalFormFor key={`${e.id}-${e.revision}`} me={me} kind={e.kind} existing={e} />
}

function JournalFormFor({ me, kind, existing }: { me: Me; kind: 'umum' | 'penyesuaian'; existing?: EntryDetail }) {
  const adjusting = kind === 'penyesuaian'
  const editing = existing !== undefined
  const companyId = me.activeCompanyId
  const navigate = useNavigate()
  const toast = useToast()

  const accounts = useAccounts(companyId)
  const post = usePostJournal(companyId)
  const revise = useReviseJournal(companyId)
  // Penyesuaian biasanya untuk bulan lalu, bertanggal akhir bulan.
  const [date, setDate] = useState(() => existing?.date ?? (adjusting ? monthEnd(addMonths(monthOf(todayISO()), -1)) : todayISO()))
  const [memo, setMemo] = useState(existing?.memo ?? '')
  const [autoReverse, setAutoReverse] = useState(existing?.autoReverse ?? false)
  const [reason, setReason] = useState('')
  const [rows, setRows] = useState<Row[]>(() =>
    existing
      ? existing.lines.map((l) => ({
          key: newIdempotencyKey(),
          accountId: l.accountId,
          memo: l.memo,
          debit: formatAmount(l.debit, { blankZero: true }),
          credit: formatAmount(l.credit, { blankZero: true }),
          projectId: l.projectId ?? '',
          branchId: l.branchId,
          departmentId: l.departmentId,
        }))
      : [blankRow(), blankRow()],
  )
  const [idempotencyKey] = useState(newIdempotencyKey)
  const [attempt, setAttempt] = useState(0)
  const [rowErrors, setRowErrors] = useState<RowErrors>({})
  const projectOptions = useProjectOptions(companyId, can(me, 'project.view'), rows.map((r) => r.projectId))

  const year = Number(date.slice(0, 4)) || new Date().getFullYear()
  const periods = usePeriods(companyId, year)
  const period = periods.data?.periods.find((p) => p.month === monthOf(date))

  const totals = useMemo(() => {
    let debit = 0n
    let credit = 0n
    let unreadable = false
    for (const r of rows) {
      const d = parseInput(r.debit)
      const c = parseInput(r.credit)
      if (d === null || c === null) unreadable = true
      debit += d ?? 0n
      credit += c ?? 0n
    }
    return { debit, credit, diff: debit - credit, unreadable }
  }, [rows])

  const filled = rows.filter((r) => !isEmpty(r))
  const complete = filled.every((r) => {
    const d = parseInput(r.debit) ?? 0n
    const c = parseInput(r.credit) ?? 0n
    return r.accountId !== '' && (d > 0n) !== (c > 0n)
  })
  const balanced = totals.diff === 0n && totals.debit > 0n && !totals.unreadable
  const ready = balanced && complete && filled.length >= 2 && date !== ''
  const blockedByPeriod = period && (period.status === 'locked' || (period.status === 'adjusting' && !adjusting))

  function setRow(key: string, patch: Partial<Row>) {
    setRows((list) => list.map((r) => (r.key === key ? { ...r, ...patch } : r)))
    setRowErrors((errs) => {
      if (!errs[key]) return errs
      const next = { ...errs }
      delete next[key]
      return next
    })
  }

  function addRow(focus = true) {
    const row = blankRow()
    setRows((list) => [...list, row])
    if (focus) requestAnimationFrame(() => document.getElementById(`akun-${row.key}`)?.focus())
  }

  /** Mengisi selisih ke baris terakhir yang belum bernominal, atau ke baris baru. */
  function fillDifference() {
    const amount = fromUnits(totals.diff < 0n ? -totals.diff : totals.diff)
    const side = totals.diff > 0n ? 'credit' : 'debit'
    const target = [...rows].reverse().find((r) => !r.debit.trim() && !r.credit.trim())
    const value = formatAmount(amount)
    if (target) {
      setRow(target.key, { [side]: value })
      requestAnimationFrame(() => document.getElementById(`akun-${target.key}`)?.focus())
    } else {
      const row = { ...blankRow(), [side]: value }
      setRows((list) => [...list, row])
      requestAnimationFrame(() => document.getElementById(`akun-${row.key}`)?.focus())
    }
  }

  function submit(e?: FormEvent) {
    e?.preventDefault()
    if (!ready || post.isPending || revise.isPending) return
    setAttempt((n) => n + 1)
    const submitted = rows.filter((r) => !isEmpty(r))
    const lines = submitted.map((r) => ({
      accountId: r.accountId,
      debit: fromUnits(parseInput(r.debit) ?? 0n),
      credit: fromUnits(parseInput(r.credit) ?? 0n),
      memo: r.memo.trim(),
      projectId: r.projectId || undefined,
      branchId: r.branchId,
      departmentId: r.departmentId,
    }))
    const onError = (err: Error) => {
      if (!(err instanceof ApiError)) return
      const errs: RowErrors = {}
      for (const [field, msg] of Object.entries(err.fields)) {
        const m = /^lines\.(\d+)\.(accountId|amount|memo)$/.exec(field)
        const row = m && submitted[Number(m[1])]
        if (!row) continue
        const slot = m[2] === 'accountId' ? 'account' : m[2]
        errs[row.key] = { ...errs[row.key], [slot]: msg }
      }
      setRowErrors(errs)
    }
    if (existing) {
      revise.mutate(
        { id: existing.id, date, memo: memo.trim(), lines, reason: reason.trim(), revision: existing.revision },
        {
          onSuccess: (x) => {
            toast(`${x.number} diubah · versi lama tersimpan di riwayat`)
            navigate(`/jurnal?bulan=${monthOf(x.date)}&id=${x.id}&baru=${x.id}`)
          },
          onError,
        },
      )
      return
    }
    post.mutate(
      { kind, idempotencyKey, input: { date, memo: memo.trim(), autoReverse: adjusting && autoReverse, lines } },
      {
        onSuccess: (p) => {
          toast(`${p.number} diposting · jurnal seimbang${p.autoReversal ? ` · pembalik ${p.autoReversal.number}` : ''}`)
          navigate(`/jurnal?bulan=${monthOf(date)}&baru=${p.id}`)
        },
        onError,
      },
    )
  }

  function onFormKeyDown(e: KeyboardEvent) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      submit()
    }
  }

  const allowed = editing
    ? can(me, 'ledger.journal.edit') && (!adjusting || can(me, 'ledger.adjustment.post'))
    : can(me, adjusting ? 'ledger.adjustment.post' : 'ledger.journal.create')
  if (!allowed) {
    return <EmptyState title={`Anda tidak punya izin ${editing ? 'mengubah' : 'membuat'} jurnal ini`} body="Peran Anda di PT ini tidak mengizinkannya." />
  }
  const accountList = accounts.data?.items ?? []
  const failure = editing ? revise.error : post.error
  const serverError = failure instanceof ApiError ? failure : null
  const nextMonthStart = `${addMonths(monthOf(date || todayISO()), 1)}-01`

  return (
    <form onSubmit={submit} onKeyDown={onFormKeyDown} className="flex flex-col gap-5" noValidate>
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <Link to={existing ? `/jurnal?bulan=${monthOf(existing.date)}&id=${existing.id}` : '/jurnal'} className="text-sm font-medium text-muted hover:text-ink">
            ← Jurnal
          </Link>
          <h1 className="mt-1 text-[26px] font-bold tracking-tight">
            {existing ? `Ubah ${existing.number}` : adjusting ? 'Jurnal penyesuaian baru' : 'Jurnal umum baru'}
          </h1>
          <p className="mt-1.5 max-w-2xl text-sm text-muted">
            {existing
              ? 'Nomor jurnal tetap. Isi sebelumnya disimpan di riwayat perubahan dan audit trail, sehingga perubahan ini tetap bisa ditelusuri.'
              : adjusting
                ? 'Seri JP, biasanya bertanggal akhir bulan. Boleh masuk saat periode sudah ditutup untuk transaksi, selama belum dikunci final.'
                : 'Seri JU. Setelah diposting, jurnal masih bisa diubah selama periodenya belum dikunci; setiap perubahan tercatat di riwayat.'}
          </p>
        </div>
      </div>

      <Card>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4">
          <Field
            id="tanggal"
            label="Tanggal"
            type="date"
            required
            value={date}
            min={existing ? monthStart(monthOf(existing.date)) : undefined}
            max={existing ? monthEnd(monthOf(existing.date)) : undefined}
            onChange={(e) => setDate(e.target.value)}
            error={serverError?.fields.date}
            hint={
              existing ? (
                `Hanya dalam ${monthLabel(monthOf(existing.date))}, sesuai nomornya.`
              ) : period && period.status !== 'open' ? (
                <span className={blockedByPeriod ? 'text-bad' : 'text-warn'}>
                  {monthLabel(period.month)}: {periodStatus[period.status].help}
                </span>
              ) : undefined
            }
          />
          <div className="sm:col-span-2">
            <Field
              id="keterangan"
              label="Keterangan"
              placeholder={adjusting ? 'Mis. Penyusutan peralatan kantor' : 'Mis. Pembayaran sewa gudang'}
              value={memo}
              maxLength={500}
              onChange={(e) => setMemo(e.target.value)}
              error={serverError?.fields.memo}
            />
          </div>
        </div>
        {adjusting && !existing && (
          <Checkbox
            checked={autoReverse}
            onChange={setAutoReverse}
            label={`Balik otomatis pada ${formatDate(nextMonthStart)}`}
            hint="Untuk akrual: Lajur langsung membuat jurnal pembalik (seri JB) di awal bulan berikutnya, sehingga transaksi aslinya bisa dicatat normal."
          />
        )}
        {existing?.autoReverse && <p className="text-sm text-muted">Jurnal pembalik otomatisnya di awal bulan berikutnya ikut berubah mengikuti isi baru.</p>}
        {existing && (
          <Field
            id="alasan"
            label="Alasan perubahan (opsional)"
            placeholder="Mis. salah akun, nominal salah ketik"
            value={reason}
            maxLength={300}
            onChange={(e) => setReason(e.target.value)}
            error={serverError?.fields.reason}
          />
        )}
      </Card>

      <div className="overflow-hidden rounded-[14px] border border-line bg-surface">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[820px] text-left text-sm">
            <thead className="bg-hover/60 text-[12.5px] text-muted">
              <tr>
                <th className="w-10 px-3 py-3 text-center font-semibold">#</th>
                <th className="px-2 py-3 font-semibold">Akun</th>
                <th className="px-2 py-3 font-semibold">Keterangan baris</th>
                <th className="w-[170px] px-2 py-3 text-right font-semibold">Debit</th>
                <th className="w-[170px] px-2 py-3 text-right font-semibold">Kredit</th>
                <th className="w-12 px-2 py-3">
                  <span className="sr-only">Hapus</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <AnimatePresence initial={false}>
                {rows.map((r, i) => {
                  const err = rowErrors[r.key]
                  const debitBad = parseInput(r.debit) === null
                  const creditBad = parseInput(r.credit) === null
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
                      <td className="num px-3 pt-5 text-center text-[13px] text-muted">{i + 1}</td>
                      <td className="min-w-[260px] px-2 py-2">
                        {accounts.isPending ? (
                          <Skeleton className="h-11" />
                        ) : (
                          <AccountPicker
                            id={`akun-${r.key}`}
                            label={`Akun baris ${i + 1}`}
                            accounts={accountList}
                            value={r.accountId}
                            invalid={Boolean(err?.account)}
                            onChange={(id) => setRow(r.key, { accountId: id })}
                            onCommit={() => document.getElementById(`debit-${r.key}`)?.focus()}
                          />
                        )}
                        {err?.account && <p className="mt-1 text-[13px] text-bad">{err.account}</p>}
                        {projectOptions.length > 0 && (
                          <div className="mt-1.5">
                            <SearchSelect
                              label={`Proyek baris ${i + 1}`}
                              options={projectOptions}
                              value={r.projectId}
                              onChange={(p) => setRow(r.key, { projectId: p })}
                              placeholder="Proyek (opsional)"
                              clearLabel="Tanpa proyek"
                            />
                          </div>
                        )}
                      </td>
                      <td className="min-w-[180px] px-2 py-2">
                        <input
                          aria-label={`Keterangan baris ${i + 1}`}
                          value={r.memo}
                          maxLength={250}
                          onChange={(e) => setRow(r.key, { memo: e.target.value })}
                          className={`${inputClass} ${err?.memo ? 'border-bad' : 'border-line-2'}`}
                        />
                      </td>
                      {(['debit', 'credit'] as const).map((side) => {
                        const bad = side === 'debit' ? debitBad : creditBad
                        return (
                          <td key={side} className="px-2 py-2">
                            <input
                              id={`${side}-${r.key}`}
                              aria-label={`${side === 'debit' ? 'Debit' : 'Kredit'} baris ${i + 1}`}
                              aria-invalid={bad || Boolean(err?.amount) || undefined}
                              inputMode="decimal"
                              autoComplete="off"
                              placeholder="0"
                              value={r[side]}
                              onChange={(e) => {
                                const v = e.target.value
                                const other = side === 'debit' ? 'credit' : 'debit'
                                setRow(r.key, v.trim() ? { [side]: v, [other]: '' } : { [side]: v })
                              }}
                              onBlur={(e) => {
                                const u = parseInput(e.target.value)
                                if (u !== null) setRow(r.key, { [side]: u === 0n ? '' : formatAmount(u) })
                              }}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter' && !e.metaKey && !e.ctrlKey) {
                                  e.preventDefault()
                                  if (i === rows.length - 1) addRow()
                                  else document.getElementById(`akun-${rows[i + 1].key}`)?.focus()
                                }
                              }}
                              className={`${inputClass} num text-right ${bad || err?.amount ? 'border-bad' : 'border-line-2'}`}
                            />
                            {side === 'debit' && err?.amount && <p className="mt-1 text-[13px] text-bad">{err.amount}</p>}
                          </td>
                        )
                      })}
                      <td className="px-2 py-2">
                        <button
                          type="button"
                          aria-label={`Hapus baris ${i + 1}`}
                          disabled={rows.length <= 2}
                          onClick={() => setRows((list) => list.filter((x) => x.key !== r.key))}
                          className="flex size-11 cursor-pointer items-center justify-center rounded-[10px] text-muted transition-colors hover:bg-hover hover:text-bad disabled:cursor-not-allowed disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-muted"
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
        <div className="flex flex-wrap items-center gap-2 border-t border-line px-3 py-2.5">
          <Button variant="ghost" size="sm" onClick={() => addRow()}>
            + Tambah baris
          </Button>
          {totals.diff !== 0n && !totals.unreadable && (
            <Button variant="ghost" size="sm" onClick={fillDifference}>
              Isi selisih {formatRp(totals.diff < 0n ? -totals.diff : totals.diff)} ke {totals.diff > 0n ? 'kredit' : 'debit'}
            </Button>
          )}
          <span className="ml-auto hidden text-xs text-muted sm:inline">Enter: baris berikutnya · Ctrl/⌘ + Enter: posting</span>
        </div>
      </div>

      {serverError && Object.keys(rowErrors).length === 0 && !serverError.fields.date && <Alert shakeKey={attempt}>{serverError.message}</Alert>}

      <div className="sticky bottom-0 z-10 -mx-4 border-t border-line bg-surface/95 px-4 py-4 backdrop-blur sm:-mx-8 sm:px-8">
        <div className="flex flex-wrap items-center gap-x-8 gap-y-3">
          <Total label="Total debit" value={totals.debit} />
          <Total label="Total kredit" value={totals.credit} />
          <BalanceBadge diff={totals.diff} empty={totals.debit === 0n && totals.credit === 0n} unreadable={totals.unreadable} />
          <div className="ml-auto flex gap-2.5">
            <Button variant="secondary" onClick={() => navigate(existing ? `/jurnal?bulan=${monthOf(existing.date)}&id=${existing.id}` : '/jurnal')}>
              Batal
            </Button>
            <Button type="submit" busy={post.isPending || revise.isPending} disabled={!ready}>
              {existing ? 'Simpan perubahan' : 'Posting jurnal'}
            </Button>
          </div>
        </div>
      </div>
    </form>
  )
}

function Total({ label, value }: { label: string; value: bigint }) {
  return (
    <div className="flex flex-col">
      <span className="text-[12.5px] text-muted">{label}</span>
      <span className="num text-lg font-semibold">{formatAmount(value)}</span>
    </div>
  )
}

/** Indikator selisih: berubah hijau dengan transisi warna saat jurnal seimbang (TDD, 150 ms). */
function BalanceBadge({ diff, empty, unreadable }: { diff: bigint; empty: boolean; unreadable: boolean }) {
  if (unreadable) return <Badge tone="bad">Ada nominal yang tidak terbaca</Badge>
  if (empty) return <Badge tone="neutral">Belum ada nominal</Badge>
  const ok = diff === 0n
  return (
    <span aria-live="polite">
      <Badge tone={ok ? 'good' : 'bad'}>
        <span className="size-1.5 rounded-full bg-current" />
        {ok ? 'Seimbang · selisih Rp 0' : `Selisih ${formatRp(diff < 0n ? -diff : diff)}`}
      </Badge>
    </span>
  )
}

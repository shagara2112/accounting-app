import { useState } from 'react'
import { motion } from 'motion/react'
import { useNavigate, useSearchParams } from 'react-router'
import { ApiError, newIdempotencyKey, type CashKind, type CashTransactionDetail, type Me } from '../lib/api'
import {
  useCashAccounts,
  useCashTransaction,
  useCashTransactions,
  useDeleteCashDraft,
  usePostCashDraft,
  useRecordCash,
  useRegisterCashAccount,
  useSaveCashDraft,
  useVoidCash,
  type CashInput,
} from '../lib/cash'
import { formatDate, monthEnd, monthLabel, monthOf, monthStart, todayISO } from '../lib/dates'
import { useAccounts } from '../lib/ledger'
import { formatAmount, formatRp, fromUnits, parseInput } from '../lib/money'
import { useProjectNames, useProjectOptions } from '../lib/projects'
import { can } from '../lib/session'
import { isRestricted, useDimensions } from '../lib/dimensions'
import { AccountPicker } from '../components/AccountPicker'
import { DimensionFields, type DimValue } from '../components/DimensionFields'
import { SearchSelect } from '../components/SearchSelect'
import { MonthNav } from '../components/MonthNav'
import { useToast } from '../components/Toast'
import { VoidForm } from '../components/VoidForm'
import { Attachments } from '../components/Attachments'
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
  const draftsOnly = params.get('draf') === '1'
  const list = useCashTransactions(companyId, draftsOnly ? { drafts: '1' } : { from: monthStart(month), to: monthEnd(month), account })
  const [recording, setRecording] = useState<Opening | null>(null)
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

  const items = list.data?.items ?? []
  const drafts = list.data?.drafts ?? 0
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Kas dan bank"
        subtitle="Saldo rekening adalah saldo akun buku besarnya, sehingga pelunasan dan pembayaran dari penjualan dan pembelian ikut terhitung. Transaksi bank dicatat manual."
        actions={
          <>
            {!isRestricted(me) && (
              <Button variant="ghost" onClick={() => navigate(account ? `/kas/rekonsiliasi?rekening=${account}` : '/kas/rekonsiliasi')}>
                Rekonsiliasi bank
              </Button>
            )}
            {canCreate && (
            <>
              <Button variant="secondary" onClick={() => setRecording({ kind: 'transfer' })}>
                Transfer
              </Button>
              <Button variant="secondary" onClick={() => setRecording({ kind: 'out' })}>
                Pengeluaran
              </Button>
              <Button onClick={() => setRecording({ kind: 'in' })}>Penerimaan</Button>
            </>
            )}
          </>
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

      {drafts > 0 && !draftsOnly && (
        <Alert tone="warn">
          Ada {drafts} draf transaksi kas senilai Rp {formatAmount(list.data?.draftAmount ?? '0')} yang belum diposting, sehingga belum masuk saldo rekening.{' '}
          <button type="button" onClick={() => update({ draf: '1', id: null })} className="cursor-pointer font-semibold underline">
            Lihat draf
          </button>
        </Alert>
      )}
      <div className="flex flex-wrap items-center gap-3">
        {draftsOnly ? (
          <Badge tone="warn">
            Semua draf belum diposting
            <button type="button" aria-label="Tampilkan semua transaksi" onClick={() => update({ draf: null })} className="ml-1 cursor-pointer font-bold">
              ×
            </button>
          </Badge>
        ) : (
          <MonthNav value={month} onChange={(m) => update({ bulan: m })} />
        )}
        {account && !draftsOnly && (
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
              <td className={`${td} num font-semibold whitespace-nowrap ${x.status === 'void' ? 'line-through' : x.status === 'draft' ? 'text-muted' : 'text-accent'}`}>{x.number ?? 'Draf'}</td>
              <td className={td}>
                <span className="flex items-center gap-1.5">
                  <Badge tone={kindTone[x.kind]}>{kindLabel[x.kind]}</Badge>
                  {x.status === 'void' && <Badge tone="bad">Void</Badge>}
                  {x.status === 'draft' && <Badge tone="warn">Draf</Badge>}
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
        <EmptyState
          title={draftsOnly ? 'Tidak ada draf transaksi kas' : `Belum ada transaksi kas manual di ${monthLabel(month)}`}
          body={draftsOnly ? undefined : 'Pelunasan faktur dan pembayaran tagihan tercatat di Penjualan dan Pembelian; semuanya tetap terlihat di buku besar rekening.'}
        />
      )}

      <RecordDialog me={me} open={recording} onClose={() => setRecording(null)} onKind={(k) => setRecording((r) => r && { ...r, kind: k })} defaultAccount={account} />
      <RegisterDialog me={me} open={registering} onClose={() => setRegistering(false)} />
      <TransactionDrawer
        me={me}
        id={openId}
        onClose={() => update({ id: null })}
        onEditDraft={(d) => {
          update({ id: null })
          setRecording({ kind: d.kind, draft: d })
        }}
      />
    </div>
  )
}

type Line = { key: string; accountId: string; amount: string; memo: string; projectId: string; departmentId: string }
const blankLine = (projectId = ''): Line => ({ key: newIdempotencyKey(), accountId: '', amount: '', memo: '', projectId, departmentId: '' })

/** Dialog transaksi kas baru, atau melengkapi draf (open.draft). */
type Opening = { kind: CashKind; draft?: CashTransactionDetail }

function RecordDialog({ me, open, onClose, onKind, defaultAccount }: { me: Me; open: Opening | null; onClose: () => void; onKind: (k: CashKind) => void; defaultAccount?: string }) {
  const companyId = me.activeCompanyId
  const kind = open?.kind ?? null
  const draft = open?.draft
  const accounts = useAccounts(companyId)
  const cashAccounts = (useCashAccounts(companyId).data ?? []).filter((a) => a.active)
  const record = useRecordCash(companyId)
  const saveDraft = useSaveCashDraft(companyId)
  const postDraft = usePostCashDraft(companyId)
  const toast = useToast()
  const [shownFor, setShownFor] = useState<CashKind | null>(null)
  const [date, setDate] = useState(todayISO())
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [amount, setAmount] = useState('')
  const [memo, setMemo] = useState('')
  const [reference, setReference] = useState('')
  const [dims, setDims] = useState<DimValue>({ branchId: '', departmentId: '' })
  const [lines, setLines] = useState<Line[]>([blankLine()])
  const [key, setKey] = useState(newIdempotencyKey)
  const [postKey, setPostKey] = useState(newIdempotencyKey)
  // Versi draf terakhir yang disimpan dialog ini: bila "Simpan & posting" gagal di tahap posting,
  // drafnya sudah tersimpan dengan versi baru dan percobaan berikutnya harus memakai versi itu.
  const [version, setVersion] = useState<number>()
  const [attempt, setAttempt] = useState(0)
  const projectOptions = useProjectOptions(companyId, kind !== null && can(me, 'project.view'), lines.map((l) => l.projectId))
  const departments = (useDimensions(companyId, 'departemen').data ?? []).filter((d) => d.active || lines.some((l) => l.departmentId === d.id))

  if (kind && shownFor === null) {
    setShownFor(kind)
    setDate(draft?.date ?? todayISO())
    setFrom(draft?.cashAccountId ?? defaultAccount ?? cashAccounts[0]?.accountId ?? '')
    setTo(draft?.toAccountId ?? '')
    setAmount(draft && draft.kind === 'transfer' ? formatAmount(draft.amount) : '')
    setMemo(draft?.memo ?? '')
    setReference(draft?.reference ?? '')
    setDims({ branchId: draft?.branchId ?? '', departmentId: '' })
    setLines(
      draft && draft.lines.length > 0
        ? draft.lines.map((l) => ({
            key: newIdempotencyKey(),
            accountId: l.accountId ?? '',
            amount: formatAmount(l.amount),
            memo: l.memo,
            projectId: l.projectId ?? '',
            // Departemen kepala dari draf lama dipindah ke baris supaya terlihat dan bisa diubah.
            departmentId: l.departmentId ?? draft.departmentId ?? '',
          }))
        : [blankLine()],
    )
    setKey(newIdempotencyKey())
    setPostKey(newIdempotencyKey())
    setVersion(draft?.version)
    record.reset()
    saveDraft.reset()
    postDraft.reset()
  }
  const current = kind ?? shownFor
  const failure = record.error ?? saveDraft.error ?? postDraft.error
  const err = failure instanceof ApiError ? failure : null
  const busy = record.isPending || saveDraft.isPending || postDraft.isPending
  const counter = (accounts.data?.items ?? []).filter((a) => !cashAccounts.some((c) => c.accountId === a.id))
  const total = current === 'transfer' ? (parseInput(amount) ?? 0n) : lines.reduce((s, l) => s + (parseInput(l.amount) ?? 0n), 0n)

  function close() {
    setShownFor(null)
    onClose()
  }

  function input(kind: CashKind): CashInput {
    return {
      kind,
      date,
      cashAccountId: from,
      toAccountId: kind === 'transfer' ? to : undefined,
      amount: fromUnits(kind === 'transfer' ? total : 0n),
      memo: memo.trim(),
      reference: reference.trim(),
      branchId: dims.branchId || undefined,
      departmentId: dims.departmentId || undefined,
      lines:
        kind === 'transfer'
          ? []
          : lines
              .filter((l) => l.accountId || l.amount || l.memo.trim())
              .map((l) => ({
                accountId: l.accountId || undefined,
                amount: fromUnits(parseInput(l.amount) ?? 0n),
                memo: l.memo.trim(),
                projectId: l.projectId || undefined,
                departmentId: l.departmentId || undefined,
              })),
    }
  }

  // post = false: simpan sebagai draf (akun boleh kosong). post = true: posting sekarang;
  // draf yang sedang dilengkapi disimpan dulu, lalu diposting.
  async function submit(post: boolean) {
    if (!current) return
    setAttempt((n) => n + 1)
    record.reset()
    saveDraft.reset()
    postDraft.reset()
    try {
      if (post && !draft) {
        const x = await record.mutateAsync({ idempotencyKey: key, input: input(current) })
        toast(`${x.number} diposting · ${formatRp(x.amount)}`)
      } else {
        const saved = await saveDraft.mutateAsync({ id: draft?.id, version, input: input(current), idempotencyKey: key })
        setVersion(saved.version)
        if (post) {
          const x = await postDraft.mutateAsync({ id: saved.id, idempotencyKey: postKey })
          toast(`${x.number} diposting · ${formatRp(x.amount)}`)
        } else {
          toast('Draf disimpan · belum masuk saldo rekening')
        }
      }
      close()
    } catch {
      // Pesan galat ditampilkan dari error mutasi.
    }
  }

  return (
    <Dialog
      open={kind !== null}
      onClose={close}
      width={640}
      title={current ? (draft ? `Lengkapi draf ${kindLabel[current].toLowerCase()}` : kindLabel[current]) : ''}
      description={
        <>
          {current === 'in'
            ? 'Uang masuk ke rekening, mis. bunga jasa giro, setoran modal, atau pendapatan lain. Seri BKM.'
            : current === 'out'
              ? 'Uang keluar dari rekening, mis. biaya admin bank, pajak bunga, atau biaya kecil. Seri BKK.'
              : 'Pindah dana antar rekening kas dan bank milik PT ini. Seri TRF.'}
          {current !== 'transfer' && ' Belum tahu akunnya? Simpan sebagai draf; akuntan bisa melengkapinya lalu memposting.'}
        </>
      }
      footer={
        <>
          <span className="mr-auto self-center text-sm text-muted">
            Total <span className="num font-semibold text-ink">{formatRp(total)}</span>
          </span>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button variant="secondary" busy={saveDraft.isPending && !postDraft.isPending} disabled={busy || total <= 0n || !from || (current === 'transfer' && !to)} onClick={() => void submit(false)}>
            Simpan draf
          </Button>
          <Button busy={record.isPending || postDraft.isPending} disabled={busy || total <= 0n || !from || (current === 'transfer' && !to)} onClick={() => void submit(true)}>
            {draft ? 'Simpan & posting' : 'Posting'}
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
          <span className="text-sm font-medium text-ink-2">
            {current === 'in' ? 'Diterima dari akun' : 'Dibebankan ke akun'}
            <span className="font-normal text-muted"> · boleh dikosongkan bila disimpan sebagai draf</span>
          </span>
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
                {projectOptions.length > 0 && (
                  <div className="mt-1.5">
                    <SearchSelect
                      label={`Proyek baris ${i + 1}`}
                      options={projectOptions}
                      value={l.projectId}
                      onChange={(p) => setLines((list) => list.map((x) => (x.key === l.key ? { ...x, projectId: p } : x)))}
                      placeholder="Proyek (opsional)"
                      clearLabel="Tanpa proyek"
                    />
                  </div>
                )}
              </div>
              <div>
                <input aria-label={`Keterangan baris ${i + 1}`} placeholder="Keterangan" value={l.memo} onChange={(e) => setLines((list) => list.map((x) => (x.key === l.key ? { ...x, memo: e.target.value } : x)))} className={`${inputClass} border-line-2`} />
                {departments.length > 0 && (
                  <select
                    aria-label={`Departemen baris ${i + 1}`}
                    value={l.departmentId}
                    onChange={(e) => setLines((list) => list.map((x) => (x.key === l.key ? { ...x, departmentId: e.target.value } : x)))}
                    className={`${inputClass} mt-1.5 ${err?.fields[`lines.${i}.departmentId`] ? 'border-bad' : 'border-line-2'} ${l.departmentId ? '' : 'text-muted'}`}
                  >
                    <option value="">Departemen (opsional)</option>
                    {departments.map((d) => (
                      <option key={d.id} value={d.id}>
                        {d.code} · {d.name}
                      </option>
                    ))}
                  </select>
                )}
                {err?.fields[`lines.${i}.departmentId`] && <p className="mt-1 text-[13px] text-bad">{err.fields[`lines.${i}.departmentId`]}</p>}
              </div>
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
          <Button size="sm" variant="ghost" className="self-start" onClick={() => setLines((list) => [...list, blankLine(list[list.length - 1]?.projectId)])}>
            + Tambah baris
          </Button>
        </div>
      )}
      <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
        <Field id="kas-keterangan" label="Keterangan" value={memo} maxLength={500} onChange={(e) => setMemo(e.target.value)} />
        <Field id="kas-referensi" label="Referensi bank (opsional)" value={reference} maxLength={100} onChange={(e) => setReference(e.target.value)} />
        <DimensionFields me={me} value={dims} onChange={setDims} errors={err?.fields} withDepartment={false} />
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

function TransactionDrawer({ me, id, onClose, onEditDraft }: { me: Me; id: string | null; onClose: () => void; onEditDraft: (d: CashTransactionDetail) => void }) {
  const companyId = me.activeCompanyId
  const q = useCashTransaction(companyId, id)
  const x = q.data && q.data.id === id ? q.data : undefined
  const voidIt = useVoidCash(companyId)
  const del = useDeleteCashDraft(companyId)
  const missing = x?.lines.filter((l) => !l.accountId).length ?? 0
  const toast = useToast()
  const navigate = useNavigate()
  const [voiding, setVoiding] = useState(false)
  const [key, setKey] = useState(newIdempotencyKey)
  const projectNames = useProjectNames(companyId, id !== null && can(me, 'project.view'))
  const departmentNames = new Map((useDimensions(companyId, 'departemen').data ?? []).map((d) => [d.id, `${d.code} · ${d.name}`]))

  return (
    <Drawer
      open={id !== null}
      onClose={() => {
        setVoiding(false)
        voidIt.reset()
        del.reset()
        onClose()
      }}
      title={x ? (x.number ?? `Draf ${kindLabel[x.kind].toLowerCase()}`) : 'Memuat…'}
      description={
        x && (
          <span className="flex items-center gap-2">
            <Badge tone={kindTone[x.kind]}>{kindLabel[x.kind]}</Badge>
            {x.status === 'void' ? <Badge tone="bad">Void</Badge> : x.status === 'draft' ? <Badge tone="warn">Draf</Badge> : <Badge tone="good">Diposting</Badge>}
            <span>{formatDate(x.date)}</span>
          </span>
        )
      }
      footer={
        x?.status === 'draft' ? (
          <>
            {(x.createdById === me.user.id || can(me, 'cash.void')) && (
              <Button variant="ghost" busy={del.isPending} onClick={() => del.mutate(x.id, { onSuccess: () => (toast('Draf dihapus'), onClose()) })}>
                Hapus draf
              </Button>
            )}
            {can(me, 'cash.create') && <Button onClick={() => onEditDraft(x)}>{missing > 0 ? 'Lengkapi & posting' : 'Periksa & posting'}</Button>}
          </>
        ) : x?.status === 'posted' &&
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
          {x.status === 'draft' && (
            <Alert tone="warn">
              Draf belum masuk buku besar dan belum mengubah saldo rekening.{missing > 0 && ` ${missing} baris belum punya akun lawan.`}
            </Alert>
          )}
          {del.error && <Alert>{del.error.message}</Alert>}
          {x.status === 'void' && x.voidReason && <Alert tone="bad">Alasan void: {x.voidReason}</Alert>}
          {x.lines.length > 0 && (
            <div className="overflow-hidden rounded-[12px] border border-line">
              <table className="w-full text-left text-sm">
                <tbody>
                  {x.lines.map((l, i) => (
                    <tr key={i} className="border-t border-line first:border-0">
                      <td className="px-3 py-2.5">
                        {l.accountId ? (
                          <>
                            <span className="num font-semibold text-ink-2">{l.accountCode}</span> {l.accountName}
                          </>
                        ) : (
                          <span className="font-medium text-warn">Akun belum dipilih</span>
                        )}
                        {l.memo && <span className="block text-[12.5px] text-muted">{l.memo}</span>}
                        {l.projectId && <span className="block text-[12.5px] text-accent">Proyek {projectNames.get(l.projectId) ?? ''}</span>}
                        {(l.departmentId ?? x.departmentId) && <span className="block text-[12.5px] text-ink-2">Departemen {departmentNames.get((l.departmentId ?? x.departmentId)!) ?? ''}</span>}
                      </td>
                      <td className="num px-3 py-2.5 text-right">{formatAmount(l.amount)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {x.journalEntryId && (
            <button type="button" onClick={() => navigate(`/jurnal?bulan=${monthOf(x.date)}&id=${x.journalEntryId}`)} className="self-start text-sm font-semibold text-accent hover:underline">
              Lihat jurnal {x.number} →
            </button>
          )}
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
          <Attachments companyId={companyId} type="cash_transaction" id={x.id} canUpload={can(me, 'cash.create')} canDeleteAny={can(me, 'cash.void')} userId={me.user.id} />
        </>
      )}
    </Drawer>
  )
}


import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { ApiError, download, newIdempotencyKey, type Me } from '../lib/api'
import {
  useCashAccounts,
  useClearLines,
  useCloseRecon,
  useDeleteRecon,
  useReconciliation,
  useReconciliations,
  useStartRecon,
  useUpdateRecon,
  type ReconDetail,
} from '../lib/cash'
import { formatDate, todayISO } from '../lib/dates'
import { formatAmount, fromUnits, parseInput, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Checkbox, Dialog, EmptyState, Field, PageHeader, SelectField, Skeleton, TableShell, td, th } from '../components/ui'

/** Daftar rekonsiliasi per rekening (CB-04). */
export function ReconList({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const [params, setParams] = useSearchParams()
  const accounts = useCashAccounts(companyId)
  const account = params.get('rekening') ?? accounts.data?.find((a) => a.kind === 'bank')?.accountId ?? accounts.data?.[0]?.accountId
  const list = useReconciliations(companyId, account)
  const navigate = useNavigate()
  const [starting, setStarting] = useState(false)
  if (!can(me, 'cash.view')) return <EmptyState title="Anda tidak punya izin melihat kas dan bank" />
  const items = list.data ?? []
  const open = items.find((r) => r.status === 'open')

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Rekonsiliasi bank"
        subtitle="Cocokkan mutasi di buku dengan rekening koran. Mutasi yang belum muncul di koran terbawa ke rekonsiliasi berikutnya."
        actions={
          can(me, 'cash.create') &&
          account && (
            <Button disabled={!!open} onClick={() => setStarting(true)}>
              Rekonsiliasi baru
            </Button>
          )
        }
      />
      <div className="flex flex-wrap items-end gap-3">
        <SelectField id="rk-rekening" label="Rekening" className="min-w-[260px]" value={account ?? ''} onChange={(e) => setParams({ rekening: e.target.value }, { replace: true })}>
          {accounts.data?.map((a) => (
            <option key={a.accountId} value={a.accountId}>
              {a.code} {a.name}
              {a.bankName ? ` · ${a.bankName} ${a.accountNumber}` : ''}
            </option>
          ))}
        </SelectField>
        <Link to="/kas" className="pb-2.5 text-sm font-semibold text-accent hover:underline">
          ← Kembali ke kas dan bank
        </Link>
      </div>
      {open && <Alert tone="warn">Rekonsiliasi per {formatDate(open.statementDate)} masih terbuka. Selesaikan atau hapus dulu sebelum membuat yang baru.</Alert>}
      <TableShell minWidth={640}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Rekening koran per</th>
            <th className={`${th} text-right`}>Saldo awal</th>
            <th className={`${th} text-right`}>Saldo koran</th>
            <th className={`${th} text-right`}>Dicentang</th>
            <th className={th}>Status</th>
          </tr>
        </thead>
        <tbody>
          {list.isPending && (
            <tr>
              <td colSpan={5} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {!list.isPending && items.length === 0 && (
            <tr>
              <td colSpan={5} className={`${td} text-muted`}>
                Belum ada rekonsiliasi untuk rekening ini.
              </td>
            </tr>
          )}
          {items.map((r) => (
            <tr key={r.id} onClick={() => navigate(`/kas/rekonsiliasi/${r.id}`)} className="cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover">
              <td className={`${td} font-medium`}>{formatDate(r.statementDate)}</td>
              <td className={`${td} num text-right`}>{formatAmount(r.openingBalance, { parens: true })}</td>
              <td className={`${td} num text-right`}>{formatAmount(r.statementBalance, { parens: true })}</td>
              <td className={`${td} num text-right`}>{r.items} mutasi</td>
              <td className={td}>{r.status === 'closed' ? <Badge tone="good">Ditutup</Badge> : <Badge tone="warn">Terbuka</Badge>}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {starting && account && <StartDialog companyId={companyId} accountId={account} first={!items.some((r) => r.status === 'closed')} onClose={() => setStarting(false)} />}
    </div>
  )
}

function StartDialog({ companyId, accountId, first, onClose }: { companyId: string | null; accountId: string; first: boolean; onClose: () => void }) {
  const start = useStartRecon(companyId)
  const navigate = useNavigate()
  const [key] = useState(newIdempotencyKey)
  const [v, setV] = useState({ statementDate: todayISO(), statementBalance: '', openingBalance: '0' })
  const err = start.error instanceof ApiError ? start.error : null
  const bal = parseInput(v.statementBalance)
  const open = parseInput(v.openingBalance)
  return (
    <Dialog
      open
      onClose={onClose}
      title="Rekonsiliasi baru"
      description="Isi tanggal dan saldo akhir sesuai rekening koran."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button
            busy={start.isPending}
            disabled={bal === null || (first && open === null)}
            onClick={() =>
              start.mutate(
                { key, accountId, statementDate: v.statementDate, statementBalance: fromUnits(bal ?? 0n), openingBalance: fromUnits(open ?? 0n) },
                { onSuccess: (d) => navigate(`/kas/rekonsiliasi/${d.id}`) },
              )
            }
          >
            Mulai
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field id="rk-tanggal" label="Tanggal rekening koran" type="date" value={v.statementDate} onChange={(e) => setV({ ...v, statementDate: e.target.value })} error={err?.fields.statementDate} />
        <Field id="rk-saldo" label="Saldo akhir rekening koran" inputMode="decimal" className="num text-right" value={v.statementBalance} onChange={(e) => setV({ ...v, statementBalance: e.target.value })} />
        {first && (
          <Field
            id="rk-awal"
            label="Saldo awal rekening koran"
            hint="Rekonsiliasi pertama rekening ini: saldo koran sebelum mutasi pertama yang akan dicocokkan."
            inputMode="decimal"
            className="num text-right"
            value={v.openingBalance}
            onChange={(e) => setV({ ...v, openingBalance: e.target.value })}
          />
        )}
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

/** Rekonsiliasi satu rekening koran: centang mutasi, lihat selisih, tutup bila nol. */
export function ReconPage({ me }: { me: Me }) {
  const { id = '' } = useParams()
  const companyId = me.activeCompanyId
  const q = useReconciliation(companyId, id)
  if (q.isPending) return <Skeleton className="h-96" />
  if (q.isError) return <Alert>{q.error.message}</Alert>
  return <ReconView key={q.data.version} me={me} d={q.data} />
}

function ReconView({ me, d }: { me: Me; d: ReconDetail }) {
  const companyId = me.activeCompanyId
  const clear = useClearLines(companyId)
  const close = useCloseRecon(companyId)
  const update = useUpdateRecon(companyId)
  const del = useDeleteRecon(companyId)
  const toast = useToast()
  const navigate = useNavigate()
  const [onlyOpen, setOnlyOpen] = useState(false)
  const [editing, setEditing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [closeKey, setCloseKey] = useState(newIdempotencyKey)
  const editable = d.status === 'open' && can(me, 'cash.create')
  const lines = useMemo(() => (onlyOpen ? d.lines.filter((l) => !l.cleared) : d.lines), [d.lines, onlyOpen])
  const diff = toUnits(d.difference)
  const adjusted = toUnits(d.bookBalance) - toUnits(d.inTransit) + toUnits(d.outstanding)
  const pending = lines.filter((l) => !l.cleared)
  const busy = clear.isPending

  function toggle(ids: string[], cleared: boolean) {
    clear.mutate({ id: d.id, lineIds: ids, cleared })
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title={`Rekonsiliasi ${d.accountCode} ${d.accountName}`}
        subtitle={`Rekening koran per ${formatDate(d.statementDate)}`}
        aside={d.status === 'closed' ? <Badge tone="good">Ditutup</Badge> : <Badge tone="warn">Terbuka</Badge>}
        actions={
          <>
            <Button variant="ghost" onClick={() => void download('GET', `/cash/reconciliations/${d.id}/export`).catch((e: Error) => toast(e.message))}>
              Ekspor Excel
            </Button>
            {editable && (
              <Button variant="secondary" onClick={() => setEditing(true)}>
                Ubah saldo koran
              </Button>
            )}
            {editable && (
              <Button
                busy={close.isPending}
                disabled={diff !== 0n}
                onClick={() => close.mutate({ id: d.id, key: closeKey }, { onSuccess: () => (setCloseKey(newIdempotencyKey()), toast('Rekonsiliasi ditutup')) })}
              >
                Tutup rekonsiliasi
              </Button>
            )}
            {d.status === 'closed' && can(me, 'cash.void') && (
              <Button
                variant="secondary"
                busy={close.isPending}
                onClick={() => close.mutate({ id: d.id, key: closeKey, reopen: true }, { onSuccess: () => (setCloseKey(newIdempotencyKey()), toast('Rekonsiliasi dibuka kembali')) })}
              >
                Buka kembali
              </Button>
            )}
          </>
        }
      />
      <Link to={`/kas/rekonsiliasi?rekening=${d.accountId}`} className="-mt-3 text-sm font-semibold text-accent hover:underline">
        ← Semua rekonsiliasi rekening ini
      </Link>

      <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-3">
        <Summary label="Saldo awal koran" value={d.openingBalance} />
        <Summary label="Mutasi dicentang" value={d.cleared} />
        <Summary label="Saldo akhir koran" value={d.statementBalance} />
        <Summary label="Selisih" value={d.difference} tone={diff === 0n ? 'good' : 'bad'} hint={diff === 0n ? 'Cocok' : 'Belum cocok'} />
      </div>
      {(close.error || clear.error || update.error || del.error) && <Alert>{(close.error ?? clear.error ?? update.error ?? del.error)?.message}</Alert>}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <Checkbox checked={onlyOpen} onChange={setOnlyOpen} label="Hanya yang belum dicentang" />
        {editable && pending.length > 0 && (
          <Button variant="secondary" size="sm" busy={busy} onClick={() => toggle(pending.map((l) => l.id), true)}>
            Centang semua yang tampil ({pending.length})
          </Button>
        )}
      </div>
      <TableShell minWidth={760}>
        <thead className="bg-hover/60">
          <tr>
            <th className={`${th} w-10`} />
            <th className={th}>Tanggal</th>
            <th className={th}>Nomor</th>
            <th className={th}>Keterangan</th>
            <th className={`${th} text-right`}>Masuk</th>
            <th className={`${th} text-right`}>Keluar</th>
          </tr>
        </thead>
        <tbody>
          {lines.length === 0 && (
            <tr>
              <td colSpan={6} className={`${td} text-muted`}>
                {onlyOpen ? 'Semua mutasi sudah dicentang.' : 'Tidak ada mutasi sampai tanggal rekening koran ini.'}
              </td>
            </tr>
          )}
          {lines.map((l) => (
            <tr key={l.id} className={`border-t border-line ${l.cleared ? 'bg-good-soft/40' : ''}`}>
              <td className={td}>
                <input
                  type="checkbox"
                  aria-label={`Centang ${l.number}`}
                  className="size-4 cursor-pointer accent-[var(--color-accent)]"
                  checked={l.cleared}
                  disabled={!editable || busy}
                  onChange={(e) => toggle([l.id], e.target.checked)}
                />
              </td>
              <td className={`${td} whitespace-nowrap`}>{formatDate(l.date)}</td>
              <td className={`${td} num whitespace-nowrap`}>
                <Link to={`/jurnal?bulan=${l.date.slice(0, 7)}&id=${l.entryId}`} className="text-accent hover:underline">
                  {l.number}
                </Link>
              </td>
              <td className={`${td} max-w-[320px] truncate text-ink-2`}>{l.memo || '—'}</td>
              <td className={`${td} num text-right`}>{toUnits(l.debit) > 0n ? formatAmount(l.debit) : ''}</td>
              <td className={`${td} num text-right`}>{toUnits(l.credit) > 0n ? formatAmount(l.credit) : ''}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>

      <Card title="Rekap rekonsiliasi" subtitle="Saldo buku disesuaikan dengan mutasi yang belum muncul di rekening koran (CB-05).">
        <dl className="flex max-w-lg flex-col gap-1.5 text-sm">
          <Row label="Saldo menurut buku" value={d.bookBalance} />
          <Row label="Dikurangi setoran dalam perjalanan" value={fromUnits(-toUnits(d.inTransit))} />
          <Row label="Ditambah penarikan belum dicairkan" value={d.outstanding} />
          <Row label="Saldo buku disesuaikan" value={fromUnits(adjusted)} bold />
          <Row label="Saldo menurut rekening koran" value={d.statementBalance} bold />
        </dl>
      </Card>

      {editable &&
        (confirmDelete ? (
          <div className="flex flex-wrap items-center gap-3 text-sm">
            <span className="text-ink-2">Hapus rekonsiliasi ini? Semua centangnya ikut hilang.</span>
            <Button
              variant="danger"
              size="sm"
              busy={del.isPending}
              onClick={() => del.mutate(d.id, { onSuccess: () => (toast('Rekonsiliasi dihapus'), navigate(`/kas/rekonsiliasi?rekening=${d.accountId}`)) })}
            >
              Hapus
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(false)}>
              Batal
            </Button>
          </div>
        ) : (
          <button type="button" className="w-fit cursor-pointer text-sm text-muted hover:text-bad" onClick={() => setConfirmDelete(true)}>
            Hapus rekonsiliasi terbuka ini
          </button>
        ))}
      {editing && <EditDialog companyId={companyId} d={d} onClose={() => setEditing(false)} />}
    </div>
  )
}

function Summary({ label, value, tone, hint }: { label: string; value: string; tone?: 'good' | 'bad'; hint?: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-[14px] border border-line bg-surface p-4">
      <span className="text-[12.5px] text-muted">{label}</span>
      <span className={`num text-xl font-semibold tracking-tight ${tone === 'good' ? 'text-good' : tone === 'bad' ? 'text-bad' : ''}`}>{formatAmount(value, { parens: true })}</span>
      {hint && <span className="text-[12px] text-muted">{hint}</span>}
    </div>
  )
}

function Row({ label, value, bold }: { label: string; value: string; bold?: boolean }) {
  return (
    <div className={`flex justify-between gap-4 ${bold ? 'border-t border-line pt-1.5 font-semibold' : ''}`}>
      <dt>{label}</dt>
      <dd className="num">{formatAmount(value, { parens: true })}</dd>
    </div>
  )
}

function EditDialog({ companyId, d, onClose }: { companyId: string | null; d: ReconDetail; onClose: () => void }) {
  const update = useUpdateRecon(companyId)
  const [v, setV] = useState({ statementDate: d.statementDate, statementBalance: formatAmount(d.statementBalance), openingBalance: formatAmount(d.openingBalance) })
  const err = update.error instanceof ApiError ? update.error : null
  const bal = parseInput(v.statementBalance)
  const open = parseInput(v.openingBalance)
  return (
    <Dialog
      open
      onClose={onClose}
      title="Ubah saldo rekening koran"
      description="Mutasi yang sudah dicentang tetap; yang tanggalnya setelah tanggal koran baru dilepas."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button
            busy={update.isPending}
            disabled={bal === null || open === null}
            onClick={() =>
              update.mutate(
                { id: d.id, version: d.version, accountId: d.accountId, statementDate: v.statementDate, statementBalance: fromUnits(bal ?? 0n), openingBalance: fromUnits(open ?? 0n) },
                { onSuccess: onClose },
              )
            }
          >
            Simpan
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field id="rke-tanggal" label="Tanggal rekening koran" type="date" value={v.statementDate} onChange={(e) => setV({ ...v, statementDate: e.target.value })} error={err?.fields.statementDate} />
        <Field id="rke-saldo" label="Saldo akhir rekening koran" inputMode="decimal" className="num text-right" value={v.statementBalance} onChange={(e) => setV({ ...v, statementBalance: e.target.value })} />
        {d.firstRecon && (
          <Field id="rke-awal" label="Saldo awal rekening koran" inputMode="decimal" className="num text-right" value={v.openingBalance} onChange={(e) => setV({ ...v, openingBalance: e.target.value })} />
        )}
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

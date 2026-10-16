import { useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import {
  disposalLabel,
  lifeText,
  methodLabel,
  useAsset,
  useAssetCategories,
  useDeleteAsset,
  useDisposeAsset,
  useVoidDisposal,
  type AssetDetail,
  type DisposalKind,
} from '../lib/assets'
import { formatDate, monthLabel, monthOf, todayISO } from '../lib/dates'
import { useAccounts } from '../lib/ledger'
import { formatAmount, fromUnits, parseInput, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { AccountPicker } from './AccountPicker'
import { Attachments } from './Attachments'
import { useToast } from './Toast'
import { VoidForm } from './VoidForm'
import { Alert, Badge, Button, Dialog, Drawer, Field, Segmented, Skeleton, TextAreaField } from './ui'

/** Detail aset tetap: nilai, jadwal penyusutan komersial dan fiskal, riwayat jurnal, pelepasan, dan lampiran. */
export function AssetDrawer({ me, id, onClose }: { me: Me; id: string | null; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const q = useAsset(companyId, id)
  const a = q.data && q.data.id === id ? q.data : undefined
  const navigate = useNavigate()
  const toast = useToast()
  const del = useDeleteAsset(companyId)
  const voidDisposal = useVoidDisposal(companyId)
  const [disposing, setDisposing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [voiding, setVoiding] = useState<string | null>(null)
  const [voidKey, setVoidKey] = useState(newIdempotencyKey)
  const groups = useAssetCategories(companyId).data?.fiscalGroups ?? []
  const canCreate = can(me, 'asset.create')
  const canVoid = can(me, 'asset.void')

  function close() {
    setConfirmDelete(false)
    setVoiding(null)
    del.reset()
    voidDisposal.reset()
    onClose()
  }

  const active = a?.status === 'aktif'
  const deletable = a && !a.locked && a.disposals.length === 0
  return (
    <>
      <Drawer
        open={id !== null}
        onClose={close}
        title={a?.name ?? 'Memuat aset…'}
        description={
          a && (
            <span className="flex flex-wrap items-center gap-2">
              <span className="num font-semibold text-ink-2">{a.code}</span>
              <span>{a.categoryName}</span>
              {active ? <Badge tone="good">Aktif</Badge> : <Badge tone="neutral">Dilepas {a.disposedOn && formatDate(a.disposedOn)}</Badge>}
            </span>
          )
        }
        footer={
          a &&
          (confirmDelete ? (
            <>
              <Button variant="secondary" onClick={() => setConfirmDelete(false)}>
                Batal
              </Button>
              <Button
                variant="danger"
                busy={del.isPending}
                onClick={() => del.mutate({ id: a.id, version: a.version }, { onSuccess: () => (toast(`${a.code} dihapus`), close()) })}
              >
                Hapus aset
              </Button>
            </>
          ) : (
            <>
              {canVoid && deletable && (
                <Button variant="secondary" onClick={() => setConfirmDelete(true)}>
                  Hapus…
                </Button>
              )}
              {canCreate && (
                <Button variant="secondary" onClick={() => navigate(`/aset/${a.id}/ubah`)}>
                  Ubah
                </Button>
              )}
              {canCreate && active && <Button onClick={() => setDisposing(true)}>Lepas aset…</Button>}
            </>
          ))
        }
      >
        {!a && (
          <div className="flex flex-col gap-3" aria-busy="true">
            <Skeleton className="h-5 w-2/3" />
            <Skeleton className="h-32" />
          </div>
        )}
        {a && (
          <>
            {confirmDelete && <Alert tone="warn">Aset ini belum punya jurnal, sehingga bisa dihapus dari register. Jurnal perolehannya tidak ikut berubah.</Alert>}
            {(del.error || voidDisposal.error) && <Alert>{(del.error ?? voidDisposal.error)?.message}</Alert>}
            <dl className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-x-6 gap-y-3 text-sm">
              <Meta label="Harga perolehan">
                <span className="num font-semibold">Rp {formatAmount(a.cost)}</span>
              </Meta>
              <Meta label="Akumulasi penyusutan">
                <span className="num">Rp {formatAmount(a.accumulated)}</span>
              </Meta>
              <Meta label="Nilai buku">
                <span className="num font-semibold">Rp {formatAmount(a.bookValue)}</span>
              </Meta>
              <Meta label="Diperoleh">{formatDate(a.acquiredOn)}</Meta>
              <Meta label="Metode">
                {methodLabel[a.method]}
                {a.method !== 'tidak_disusutkan' && ` · ${lifeText(a.lifeMonths)}`}
              </Meta>
              {a.method !== 'tidak_disusutkan' && <Meta label="Mulai disusutkan">{monthLabel(monthOf(a.depreciationStart))}</Meta>}
              {a.nextMonth && (
                <Meta label="Penyusutan berikutnya">
                  <span className="num">Rp {formatAmount(a.nextAmount)}</span> · {monthLabel(monthOf(a.nextMonth))}
                </Meta>
              )}
              {toUnits(a.residual) > 0n && (
                <Meta label="Nilai sisa">
                  <span className="num">Rp {formatAmount(a.residual)}</span>
                </Meta>
              )}
              <Meta label="Kelompok fiskal">
                {groups.find((g) => g.code === a.fiscalGroup)?.label ?? a.fiscalGroup} · {methodLabel[a.fiscalMethod]}
              </Meta>
              {a.location && <Meta label="Lokasi">{a.location}</Meta>}
              {a.custodian && <Meta label="Penanggung jawab">{a.custodian}</Meta>}
              {(a.branchCode || a.departmentCode) && <Meta label="Cabang · departemen">{[a.branchCode, a.departmentCode].filter(Boolean).join(' · ')}</Meta>}
            </dl>
            {toUnits(a.openingAccum) > 0n && a.openingMonth && (
              <p className="text-[13px] text-muted">
                Aset lama: akumulasi Rp {formatAmount(a.openingAccum)} s.d. {monthLabel(monthOf(a.openingMonth))} sudah tercatat sebelum memakai Lajur.
              </p>
            )}
            {a.notes && <p className="text-sm text-ink-2">{a.notes}</p>}

            {a.disposals.map((d) => (
              <div key={d.id} className={`flex flex-col gap-2 rounded-[12px] border p-3 text-sm ${d.status === 'void' ? 'border-line text-muted' : 'border-line-2'}`}>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="flex items-center gap-2">
                    <span className={`num font-semibold ${d.status === 'void' ? 'line-through' : ''}`}>{d.number}</span>
                    <Badge tone={d.status === 'void' ? 'bad' : 'neutral'}>{d.status === 'void' ? 'Void' : disposalLabel[d.kind]}</Badge>
                    <span>{formatDate(d.date)}</span>
                  </span>
                  <button type="button" onClick={() => navigate(`/jurnal?bulan=${monthOf(d.date)}&id=${d.journalEntryId}`)} className="text-[13px] font-semibold text-accent hover:underline">
                    Jurnal →
                  </button>
                </div>
                <p className="num text-[13px]">
                  Nilai buku Rp {formatAmount(d.bookValue)}
                  {d.kind === 'jual' && ` · harga jual Rp ${formatAmount(d.proceeds)} · ${toUnits(d.gain) >= 0n ? 'laba' : 'rugi'} Rp ${formatAmount(toUnits(d.gain) < 0n ? -toUnits(d.gain) : toUnits(d.gain))}`}
                  {d.kind === 'faktur' && ' dibebankan ke akun laba pelepasan; harga jualnya masuk lewat faktur penjualan.'}
                  {d.kind === 'hapus' && ' dibebankan sebagai rugi pelepasan.'}
                </p>
                {d.memo && <p className="text-[13px] text-ink-2">{d.memo}</p>}
                {d.status === 'void' && d.voidReason && <p className="text-[13px]">Di-void oleh {d.voidedBy ?? 'Sistem'}: {d.voidReason}</p>}
                {d.status === 'posted' && canVoid && voiding !== d.id && (
                  <Button variant="secondary" size="sm" className="self-start" onClick={() => (voidDisposal.reset(), setVoiding(d.id))}>
                    Batalkan pelepasan…
                  </Button>
                )}
                {voiding === d.id && (
                  <VoidForm
                    title={`Batalkan ${d.number}`}
                    note="Jurnal pelepasan dibalik, penyusutan susulannya kembali belum dijurnal, dan aset aktif lagi."
                    minDate={d.date}
                    busy={voidDisposal.isPending}
                    error={voidDisposal.error}
                    onCancel={() => setVoiding(null)}
                    onConfirm={(date, reason) =>
                      voidDisposal.mutate(
                        { id: d.id, date, reason, idempotencyKey: voidKey },
                        { onSuccess: () => (toast(`${d.number} dibatalkan`), setVoiding(null), setVoidKey(newIdempotencyKey())) },
                      )
                    }
                  />
                )}
              </div>
            ))}

            <Schedule a={a} />

            {a.posted.length > 0 && (
              <div className="flex flex-col gap-2">
                <h3 className="text-sm font-semibold">Penyusutan yang sudah dijurnal</h3>
                <ul className="flex max-h-60 flex-col overflow-y-auto rounded-[12px] border border-line text-sm">
                  {[...a.posted].reverse().map((p) => (
                    <li key={p.month} className="flex items-center justify-between gap-3 border-b border-line px-3 py-2 last:border-0">
                      <span>{monthLabel(monthOf(p.month))}</span>
                      <span className="flex items-center gap-3">
                        <button type="button" onClick={() => navigate(`/jurnal?bulan=${monthOf(p.month)}&id=${p.journalEntryId}`)} className="num text-[13px] font-semibold text-accent hover:underline">
                          {p.number}
                        </button>
                        <span className="num w-28 text-right">{formatAmount(p.amount)}</span>
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <Attachments companyId={companyId} type="fixed_asset" id={a.id} canUpload={canCreate} canDeleteAny={canVoid} userId={me.user.id} />
          </>
        )}
      </Drawer>
      {disposing && a && <DisposeDialog me={me} a={a} onClose={() => setDisposing(false)} />}
    </>
  )
}

/** Jadwal penyusutan per tahun: komersial (dijurnal) berdampingan dengan fiskal (koreksi fiskal). */
function Schedule({ a }: { a: AssetDetail }) {
  const years = [...new Set([...a.schedule.map((y) => y.year), ...a.fiscal.map((y) => y.year)])].sort((x, y) => x - y)
  if (years.length === 0) return null
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-sm font-semibold">Jadwal penyusutan per tahun</h3>
      <div className="overflow-x-auto rounded-[12px] border border-line">
        <table className="w-full min-w-[480px] text-left text-sm">
          <thead className="bg-hover/60 text-[12.5px] text-muted">
            <tr>
              <th className="px-3 py-2.5 font-semibold">Tahun</th>
              <th className="px-3 py-2.5 text-right font-semibold">Komersial</th>
              <th className="px-3 py-2.5 text-right font-semibold">Nilai buku</th>
              <th className="px-3 py-2.5 text-right font-semibold">Fiskal</th>
              <th className="px-3 py-2.5 text-right font-semibold">Nilai sisa fiskal</th>
            </tr>
          </thead>
          <tbody>
            {years.map((y) => {
              const c = a.schedule.find((s) => s.year === y)
              const f = a.fiscal.find((s) => s.year === y)
              return (
                <tr key={y} className="border-t border-line">
                  <td className="num px-3 py-2">{y}</td>
                  <td className="num px-3 py-2 text-right">{c ? formatAmount(c.amount) : '—'}</td>
                  <td className="num px-3 py-2 text-right text-muted">{c ? formatAmount(c.bookValue) : ''}</td>
                  <td className="num px-3 py-2 text-right">{f ? formatAmount(f.amount) : '—'}</td>
                  <td className="num px-3 py-2 text-right text-muted">{f ? formatAmount(f.bookValue) : ''}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      <p className="text-[12.5px] text-muted">Komersial dari bulan mulai disusutkan (sesudah akumulasi awal bila ada); fiskal per tahun pajak sesuai kelompok harta.</p>
    </div>
  )
}

const kinds: { value: DisposalKind; label: string }[] = [
  { value: 'jual', label: 'Dijual' },
  { value: 'faktur', label: 'Lewat faktur' },
  { value: 'hapus', label: 'Dihapus' },
]

function DisposeDialog({ me, a, onClose }: { me: Me; a: AssetDetail; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const dispose = useDisposeAsset(companyId)
  const accounts = (useAccounts(companyId).data?.items ?? []).filter((x) => x.class === 1 && x.id !== a.assetAccountId && x.id !== a.accumAccountId)
  const toast = useToast()
  const [key] = useState(newIdempotencyKey)
  const [kind, setKind] = useState<DisposalKind>('jual')
  const [date, setDate] = useState(todayISO() < a.acquiredOn ? a.acquiredOn : todayISO())
  const [proceeds, setProceeds] = useState('')
  const [account, setAccount] = useState('')
  const [memo, setMemo] = useState('')
  const err = dispose.error instanceof ApiError ? dispose.error : null
  const amount = parseInput(proceeds)
  const ready = date !== '' && (kind !== 'jual' || (amount !== null && amount > 0n && account !== ''))

  function submit() {
    dispose.mutate(
      {
        id: a.id,
        idempotencyKey: key,
        input: { date, kind, proceeds: kind === 'jual' ? fromUnits(amount ?? 0n) : '0', proceedsAccountId: kind === 'jual' ? account : undefined, memo: memo.trim() },
      },
      {
        onSuccess: (x) => {
          const d = x.disposals.find((v) => v.status === 'posted')
          toast(`${a.code} dilepas${d ? ` · ${d.number}` : ''}`)
          onClose()
        },
      },
    )
  }

  return (
    <Dialog
      open
      onClose={onClose}
      width={520}
      title={`Lepas ${a.code} ${a.name}`}
      description="Penyusutan yang belum dijurnal s.d. bulan pelepasan ikut dijurnal, lalu harga perolehan dan akumulasinya dikeluarkan dari buku besar."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button busy={dispose.isPending} disabled={!ready} onClick={submit}>
            Posting pelepasan
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Segmented label="Jenis pelepasan" value={kind} onChange={setKind} options={kinds} />
        <p className="text-[13px] text-muted">
          {kind === 'jual' && 'Uang hasil penjualan diterima langsung, mis. ke rekening bank. Selisih dengan nilai buku dicatat sebagai laba atau rugi.'}
          {kind === 'faktur' &&
            'Untuk penjualan yang perlu faktur (mis. terutang PPN): buat faktur penjualan dengan akun pendapatan 7-1003 Laba penjualan aset tetap. Nilai buku aset dibebankan ke akun yang sama, sehingga laba atau ruginya terlihat di satu akun.'}
          {kind === 'hapus' && 'Aset rusak, hilang, atau dibuang. Seluruh nilai bukunya dicatat sebagai rugi pelepasan.'}
        </p>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field id="lepas-tanggal" label="Tanggal pelepasan" type="date" min={a.acquiredOn} value={date} onChange={(e) => setDate(e.target.value)} error={err?.fields.date} />
          {kind === 'jual' && (
            <Field
              id="lepas-harga"
              label="Harga jual (sebelum PPN)"
              inputMode="decimal"
              className="num text-right"
              value={proceeds}
              onChange={(e) => setProceeds(e.target.value)}
              onBlur={() => amount !== null && amount > 0n && setProceeds(formatAmount(amount))}
              error={err?.fields.proceeds}
            />
          )}
        </div>
        {kind === 'jual' && (
          <div className="flex flex-col gap-1.5">
            <label htmlFor="lepas-akun" className="text-sm font-medium text-ink-2">
              Diterima di akun
            </label>
            <AccountPicker id="lepas-akun" accounts={accounts} value={account} onChange={setAccount} invalid={Boolean(err?.fields.proceedsAccountId)} placeholder="Mis. bank atau piutang lain-lain" />
            {err?.fields.proceedsAccountId && <p className="text-[13px] text-bad">{err.fields.proceedsAccountId}</p>}
          </div>
        )}
        <TextAreaField id="lepas-catatan" label="Keterangan (opsional)" rows={2} maxLength={300} value={memo} onChange={(e) => setMemo(e.target.value)} />
        <p className="num text-[13px] text-muted">
          Nilai buku saat ini Rp {formatAmount(a.bookValue)}
          {a.nextMonth && ', sebelum penyusutan susulan s.d. bulan pelepasan'}.
        </p>
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-[12.5px] text-muted">{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

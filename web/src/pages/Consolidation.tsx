import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'
import { ApiError, type Me } from '../lib/api'
import { exportConsolidation, useConsolidation, useGroupChart, useSaveGroupAccount, useSetMapping, type GroupAccount } from '../lib/consol'
import { formatDate, todayISO } from '../lib/dates'
import { useDebounced } from '../lib/hooks'
import { formatAmount, toUnits } from '../lib/money'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Checkbox, Dialog, EmptyState, Field, PageHeader, Segmented, SelectField, Skeleton, TableShell, Tabs, inputClass, td, th } from '../components/ui'
import { StatementTable } from './Reports'

type Tab = 'laporan' | 'pemetaan'

/** Konsolidasi grup (ME-04): laporan gabungan semua PT dengan eliminasi antar-PT, dan pemetaan
 *  bagan akun tiap PT ke bagan akun grup. */
export function Consolidation({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Tab) ?? 'laporan'
  return (
    <div className="flex flex-col gap-5">
      <div className="print:hidden">
        <PageHeader
          title="Konsolidasi grup"
          subtitle="Jumlah semua PT ditambah eliminasi antar-PT: piutang dengan utang, dan penjualan dengan pembelian sesama PT grup. Eliminasi hanya ada di laporan ini; buku setiap PT tetap bersih."
        />
      </div>
      <div className="print:hidden">
        <Tabs
          label="Konsolidasi"
          value={tab}
          onChange={(v) => setParams(v === 'laporan' ? {} : { tab: v }, { replace: true })}
          tabs={[
            { value: 'laporan', label: 'Laporan konsolidasi' },
            { value: 'pemetaan', label: 'Pemetaan akun grup' },
          ]}
        />
      </div>
      {tab === 'laporan' ? <ReportTab me={me} onMapping={() => setParams({ tab: 'pemetaan' }, { replace: true })} /> : <MappingTab me={me} />}
    </div>
  )
}

function ReportTab({ me, onMapping }: { me: Me; onMapping: () => void }) {
  const toast = useToast()
  const eligible = me.companies.filter((c) => c.kind === 'pt' && c.permissions.includes('consol.view'))
  const [chosen, setChosen] = useState<string[]>(() => eligible.map((c) => c.id))
  const [date, setDate] = useState(todayISO())
  const [from, setFrom] = useState(`${todayISO().slice(0, 4)}-01-01`)
  const [statement, setStatement] = useState<'neraca' | 'laba-rugi'>('neraca')
  const [busy, setBusy] = useState(false)
  const [exportError, setExportError] = useState<string | null>(null)
  const params = { companies: chosen.join(','), date, from, to: date }
  const report = useConsolidation(me.activeCompanyId, params)
  const c = report.data
  const st = c ? (statement === 'neraca' ? c.balanceSheet : c.profitLoss) : undefined

  async function run(format: 'xlsx' | 'csv') {
    setBusy(true)
    setExportError(null)
    try {
      toast(`${await exportConsolidation({ ...params, statement, format })} diunduh`)
    } catch (e) {
      setExportError(e instanceof Error ? e.message : 'Ekspor gagal.')
    } finally {
      setBusy(false)
    }
  }

  if (eligible.length === 0) return <EmptyState title="Anda belum punya izin konsolidasi di PT mana pun" />
  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-4 print:hidden">
        <div className="flex flex-wrap items-end gap-4">
          <fieldset className="flex flex-col gap-1.5">
            <legend className="mb-1.5 text-sm font-medium text-ink-2">PT yang dikonsolidasi</legend>
            <div className="flex flex-wrap gap-x-5">
              {eligible.map((co) => (
                <Checkbox
                  key={co.id}
                  checked={chosen.includes(co.id)}
                  onChange={(v) => setChosen((list) => (v ? [...list, co.id] : list.filter((x) => x !== co.id)))}
                  label={co.shortName}
                />
              ))}
            </div>
          </fieldset>
          <Field id="ks-tanggal" label="Neraca per" type="date" value={date} onChange={(e) => setDate(e.target.value || todayISO())} />
          <Field id="ks-dari" label="Laba rugi sejak" type="date" value={from} max={date} onChange={(e) => setFrom(e.target.value)} />
          <Segmented
            label="Laporan"
            value={statement}
            onChange={setStatement}
            options={[
              { value: 'neraca', label: 'Neraca' },
              { value: 'laba-rugi', label: 'Laba rugi' },
            ]}
          />
        </div>
        <div className="flex flex-wrap gap-2">
          {can(me, 'data.export') && (
            <>
              <Button variant="secondary" size="sm" busy={busy} disabled={busy || !c || c.problems.length > 0} onClick={() => void run('xlsx')}>
                Excel
              </Button>
              <Button variant="secondary" size="sm" disabled={busy || !c || c.problems.length > 0} onClick={() => void run('csv')}>
                CSV
              </Button>
            </>
          )}
          <Button variant="secondary" size="sm" onClick={() => window.print()}>
            Cetak / PDF
          </Button>
        </div>
      </div>
      {exportError && <Alert>{exportError}</Alert>}
      <div className="hidden print:block">
        <h1 className="text-xl font-bold">{st?.title}</h1>
        <p className="text-sm">{statement === 'neraca' ? `Per ${formatDate(date)}` : `${formatDate(from)} – ${formatDate(date)}`}</p>
      </div>

      {chosen.length === 0 && <EmptyState title="Pilih minimal satu PT" />}
      {chosen.length > 0 && !c && <Skeleton className="h-96" />}
      {c && c.problems.length > 0 && (
        <Alert tone="warn">
          <p className="font-semibold">Pemetaan akun grup belum lengkap, jadi laporan belum bisa disusun.</p>
          <ul className="mt-1 list-disc pl-5">
            {c.problems.slice(0, 6).map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
          <button type="button" onClick={onMapping} className="mt-2 font-semibold underline">
            Buka pemetaan akun
          </button>
        </Alert>
      )}
      {c && st && c.problems.length === 0 && (
        <>
          {st.balanced?.some((b) => !b) && <Alert>Ada kolom neraca yang tidak seimbang; periksa eliminasi dan pemetaan akun.</Alert>}
          <StatementTable st={st} />
          <div className="flex flex-wrap gap-4 print:block">
            <div className="min-w-0 flex-[1_1_420px]">
              <Card title="Jurnal eliminasi" subtitle="Dihitung dari dokumen antar-PT; tidak masuk buku besar PT mana pun.">
                {c.eliminations.length === 0 ? (
                  <p className="text-sm text-muted">Tidak ada transaksi antar-PT pada periode ini.</p>
                ) : (
                  <div className="flex flex-col gap-3">
                    {c.eliminations.map((e, i) => (
                      <div key={i} className="rounded-[10px] border border-line">
                        <p className="border-b border-line bg-hover/50 px-3 py-2 text-[13px] font-semibold">{e.memo}</p>
                        <table className="w-full text-sm">
                          <tbody>
                            {e.lines.map((l, j) => (
                              <tr key={j} className="border-t border-line first:border-0">
                                <td className={`px-3 py-1.5 ${toUnits(l.credit) > 0n ? 'pl-8' : ''}`}>
                                  <span className="num mr-2 text-[12.5px] text-muted">{l.code}</span>
                                  {l.name}
                                </td>
                                <td className="num px-3 py-1.5 text-right">{formatAmount(l.debit, { blankZero: true })}</td>
                                <td className="num px-3 py-1.5 text-right">{formatAmount(l.credit, { blankZero: true })}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    ))}
                  </div>
                )}
              </Card>
            </div>
            <div className="min-w-0 flex-[1_1_320px]">
              <Card title="Selisih antar-PT" subtitle="Saldo yang tidak berpasangan dan perlu dibereskan.">
                {c.differences.length === 0 ? (
                  <p className="text-sm text-good">Semua saldo antar-PT berpasangan.</p>
                ) : (
                  <ul className="flex flex-col gap-2">
                    {c.differences.map((d, i) => (
                      <li key={i} className="rounded-[10px] border border-line p-3 text-sm">
                        <p className="font-semibold">
                          {d.seller} → {d.buyer} <Badge tone="warn">{d.kind === 'piutang_utang' ? 'piutang–utang' : d.kind === 'pendapatan_beban' ? 'pendapatan–beban' : 'tautan'}</Badge>
                        </p>
                        {d.kind !== 'tautan' && (
                          <p className="num text-[13px] text-ink-2">
                            {d.seller} {formatAmount(d.sellerAmount)} · {d.buyer} {formatAmount(d.buyerAmount)} · selisih {formatAmount(d.difference)}
                          </p>
                        )}
                        <p className="text-[13px] text-muted">{d.note}</p>
                      </li>
                    ))}
                  </ul>
                )}
              </Card>
            </div>
          </div>
        </>
      )}
    </>
  )
}

function MappingTab({ me }: { me: Me }) {
  const chart = useGroupChart(me.activeCompanyId)
  const setMapping = useSetMapping(me.activeCompanyId)
  const toast = useToast()
  const [onlyConflicts, setOnlyConflicts] = useState<'beda' | 'semua'>('beda')
  const [search, setSearch] = useState('')
  const q = useDebounced(search.trim().toLowerCase())
  const [editing, setEditing] = useState<GroupAccount | 'new' | null>(null)
  const canEdit = can(me, 'consol.run')
  const data = chart.data
  const short = useMemo(() => new Map((data?.companies ?? []).map((c) => [c.id, c.shortName])), [data])
  const postable = (data?.accounts ?? []).filter((a) => a.postable)

  if (chart.isPending) return <Skeleton className="h-96" />
  if (!data) return null
  const rows = data.accounts.filter(
    (a) => a.postable && (onlyConflicts === 'semua' || a.conflict) && (!q || a.code.includes(q) || a.name.toLowerCase().includes(q) || a.members.some((m) => m.name.toLowerCase().includes(q))),
  )
  const conflicts = data.accounts.filter((a) => a.conflict).length

  return (
    <>
      <p className="max-w-3xl text-sm text-muted">
        Akun PT masuk ke akun grup berkode sama. Bila satu kode berarti hal berbeda di tiap PT (mis. 4-1001 pendapatan jasa di satu PT dan penjualan barang di PT lain), buat akun grup baru lalu petakan akun
        PT aktif ke sana. Pemetaan hanya bisa diubah untuk PT yang sedang aktif.
      </p>
      {data.problems.length > 0 && (
        <Alert tone="warn">
          <ul className="list-disc pl-5">
            {data.problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </Alert>
      )}
      <div className="flex flex-wrap items-center gap-3">
        <Segmented
          label="Tampilkan"
          value={onlyConflicts}
          onChange={setOnlyConflicts}
          options={[
            { value: 'beda', label: `Beda nama (${conflicts})` },
            { value: 'semua', label: 'Semua akun detail' },
          ]}
        />
        <input type="search" aria-label="Cari kode atau nama akun" placeholder="Cari kode atau nama akun" value={search} onChange={(e) => setSearch(e.target.value)} className={`${inputClass} max-w-[260px] flex-1 border-line-2`} />
        {canEdit && (
          <Button variant="secondary" className="ml-auto" onClick={() => setEditing('new')}>
            Akun grup baru
          </Button>
        )}
      </div>
      <TableShell minWidth={860}>
        <thead className="bg-hover/60">
          <tr>
            <th className={`${th} w-[34%]`}>Akun grup</th>
            <th className={th}>Akun PT yang masuk</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((g) => (
            <tr key={g.code} className="border-t border-line align-top">
              <td className={td}>
                <span className="num mr-2 font-semibold text-ink-2">{g.code}</span>
                {g.name}
                <span className="mt-1 flex flex-wrap gap-1.5">
                  {g.conflict && <Badge tone="warn">Beda nama</Badge>}
                  {!g.stored && <Badge tone="neutral">Dari bagan akun PT</Badge>}
                  {canEdit && (
                    <button type="button" onClick={() => setEditing(g)} className="cursor-pointer text-[12.5px] font-semibold text-accent hover:underline">
                      Ganti nama
                    </button>
                  )}
                </span>
              </td>
              <td className={td}>
                {g.members.length === 0 && <span className="text-muted">Belum ada</span>}
                <ul className="flex flex-col gap-1.5">
                  {g.members.map((m) => {
                    const own = m.companyId === me.activeCompanyId
                    return (
                      <li key={`${m.companyId}-${m.accountId}`} className="flex flex-wrap items-center gap-2">
                        <Badge tone={own ? 'accent' : 'neutral'}>{short.get(m.companyId)}</Badge>
                        <span>
                          <span className="num text-[12.5px] text-muted">{m.code}</span> {m.name}
                          {m.mapped && <span className="text-[12px] text-muted"> · dipetakan manual</span>}
                        </span>
                        {own && canEdit && (
                          <select
                            aria-label={`Petakan ${m.code} ${m.name}`}
                            value={g.code}
                            disabled={setMapping.isPending}
                            onChange={(e) =>
                              setMapping.mutate(
                                { accountId: m.accountId, groupCode: e.target.value === m.code ? '' : e.target.value },
                                { onSuccess: () => toast(`${m.code} dipetakan ke ${e.target.value}`) },
                              )
                            }
                            className="min-h-9 max-w-[260px] cursor-pointer rounded-[8px] border border-line-2 bg-surface px-2 text-[13px]"
                          >
                            {postable
                              .filter((x) => x.class === g.class)
                              .map((x) => (
                                <option key={x.code} value={x.code}>
                                  {x.code} {x.name}
                                </option>
                              ))}
                          </select>
                        )}
                      </li>
                    )
                  })}
                </ul>
              </td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {setMapping.error instanceof ApiError && <Alert>{setMapping.error.message}</Alert>}
      {rows.length === 0 && <EmptyState title={onlyConflicts === 'beda' ? 'Tidak ada akun berkode sama yang berbeda nama' : 'Tidak ada akun yang cocok'} />}
      <GroupAccountDialog me={me} editing={editing} accounts={data.accounts} onClose={() => setEditing(null)} />
    </>
  )
}

function GroupAccountDialog({ me, editing, accounts, onClose }: { me: Me; editing: GroupAccount | 'new' | null; accounts: GroupAccount[]; onClose: () => void }) {
  const save = useSaveGroupAccount(me.activeCompanyId)
  const toast = useToast()
  const [shownFor, setShownFor] = useState<GroupAccount | 'new' | null>(null)
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [parent, setParent] = useState('')
  if (editing && shownFor !== editing) {
    setShownFor(editing)
    setCode(editing === 'new' ? '' : editing.code)
    setName(editing === 'new' ? '' : editing.name)
    setParent(editing === 'new' ? '' : (editing.parentCode ?? ''))
    save.reset()
  }
  const isNew = (editing ?? shownFor) === 'new'
  const err = save.error instanceof ApiError ? save.error : null
  const headers = accounts.filter((a) => !a.postable && a.level >= 2)
  function close() {
    setShownFor(null)
    onClose()
  }
  return (
    <Dialog
      open={editing !== null}
      onClose={close}
      title={isNew ? 'Akun grup baru' : `Ganti nama ${code}`}
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button
            busy={save.isPending}
            disabled={!code.trim() || !name.trim()}
            onClick={() => save.mutate({ code: code.trim(), name: name.trim(), parentCode: isNew ? parent || undefined : undefined }, { onSuccess: () => (toast(`Akun grup ${code} disimpan`), close()) })}
          >
            Simpan
          </Button>
        </>
      }
    >
      {isNew && (
        <SelectField id="ga-induk" label="Di bawah akun header" value={parent} onChange={(e) => setParent(e.target.value)} error={err?.fields.parentCode}>
          <option value="">Pilih akun header</option>
          {headers.map((h) => (
            <option key={h.code} value={h.code}>
              {h.code} {h.name}
            </option>
          ))}
        </SelectField>
      )}
      <Field id="ga-kode" label="Kode" className="num" data-autofocus={isNew || undefined} disabled={!isNew} placeholder="mis. 4-1011" value={code} onChange={(e) => setCode(e.target.value)} error={err?.fields.code} />
      <Field id="ga-nama" label="Nama" data-autofocus={!isNew || undefined} value={name} maxLength={200} onChange={(e) => setName(e.target.value)} error={err?.fields.name} />
      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
    </Dialog>
  )
}

import { useSearchParams } from 'react-router'
import type { Me } from '../lib/api'
import { formatDate, monthEnd, monthOf, monthStart, todayISO } from '../lib/dates'
import { useAccountLedger, useAccounts } from '../lib/ledger'
import { kindLabel, kindTone } from '../lib/labels'
import { formatAmount } from '../lib/money'
import { AccountPicker } from '../components/AccountPicker'
import { JournalDrawer } from '../components/JournalDrawer'
import { Alert, Badge, EmptyState, Field, PageHeader, Skeleton, TableShell, td, th } from '../components/ui'

export function AccountLedgerPage({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const [params, setParams] = useSearchParams()
  const thisMonth = monthOf(todayISO())
  const accountId = params.get('akun')
  const from = params.get('dari') ?? monthStart(thisMonth)
  const to = params.get('sampai') ?? monthEnd(thisMonth)
  const openId = params.get('id')

  const accounts = useAccounts(companyId)
  const ledger = useAccountLedger(companyId, accountId, from, to)
  const data = ledger.data && ledger.data.account.id === accountId ? ledger.data : undefined

  function update(next: Record<string, string | null>) {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v)
      else p.delete(k)
    }
    setParams(p, { replace: true })
  }

  const normal = data?.normal === 'kredit' ? 'kredit' : 'debit'

  return (
    <div className="flex flex-col gap-5">
      <PageHeader title="Buku besar" subtitle="Mutasi dan saldo berjalan satu akun. Klik nomor jurnal untuk melihat detail dan dokumen sumbernya." />

      <div className="grid grid-cols-[minmax(260px,2fr)_repeat(2,minmax(150px,1fr))] gap-3 max-md:grid-cols-1">
        <div className="flex min-w-0 flex-col gap-1.5">
          <label htmlFor="bb-akun" className="text-sm font-medium text-ink-2">
            Akun
          </label>
          {accounts.isPending ? (
            <Skeleton className="h-11" />
          ) : (
            <AccountPicker id="bb-akun" accounts={accounts.data?.items ?? []} value={accountId ?? ''} onChange={(id) => update({ akun: id, id: null })} />
          )}
        </div>
        <Field id="bb-dari" label="Dari" type="date" value={from} max={to} onChange={(e) => e.target.value && update({ dari: e.target.value })} />
        <Field id="bb-sampai" label="Sampai" type="date" value={to} min={from} onChange={(e) => e.target.value && update({ sampai: e.target.value })} />
      </div>

      {!accountId && <EmptyState title="Pilih akun" body="Ketik kode atau nama akun untuk melihat mutasi dan saldonya." />}
      {accountId && !data && ledger.isFetching && <Skeleton className="h-64" />}
      {ledger.isError && <Alert>{ledger.error.message}</Alert>}

      {data && (
        <>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-3">
            <Stat label="Saldo awal" value={data.opening} note={formatDate(data.from)} />
            <Stat label="Mutasi debit" value={data.totalDebit} />
            <Stat label="Mutasi kredit" value={data.totalCredit} />
            <Stat label="Saldo akhir" value={data.closing} note={`${formatDate(data.to)} · saldo normal ${normal}`} strong />
          </div>
          {data.truncated && <Alert tone="warn">Hanya 5.000 baris pertama yang ditampilkan; persempit rentang tanggal. Saldo akhir tetap dihitung dari semua baris.</Alert>}

          <div className={`transition-opacity duration-150 ${ledger.isPlaceholderData ? 'opacity-60' : ''}`}>
            <TableShell minWidth={820}>
              <thead className="bg-hover/60">
                <tr>
                  <th className={th}>Tanggal</th>
                  <th className={th}>Nomor</th>
                  <th className={th}>Keterangan</th>
                  <th className={`${th} text-right`}>Debit</th>
                  <th className={`${th} text-right`}>Kredit</th>
                  <th className={`${th} text-right`}>Saldo</th>
                </tr>
              </thead>
              <tbody>
                <tr className="border-t border-line bg-hover/40 text-ink-2">
                  <td className={`${td} num`}>{formatDate(data.from)}</td>
                  <td className={td} colSpan={4}>
                    Saldo awal
                  </td>
                  <td className={`${td} num text-right font-semibold`}>{formatAmount(data.opening, { parens: true })}</td>
                </tr>
                {data.lines.map((l, i) => (
                  <tr key={`${l.entryId}-${i}`} className={`border-t border-line ${l.status === 'void' ? 'text-muted' : ''}`}>
                    <td className={`${td} num whitespace-nowrap`}>{formatDate(l.date)}</td>
                    <td className={td}>
                      <span className="flex items-center gap-2">
                        <button type="button" onClick={() => update({ id: l.entryId })} className="num cursor-pointer font-semibold whitespace-nowrap text-accent hover:underline">
                          {l.number}
                        </button>
                        {l.kind !== 'umum' && <Badge tone={kindTone[l.kind]}>{kindLabel[l.kind]}</Badge>}
                        {l.status === 'void' && <Badge tone="bad">Void</Badge>}
                      </span>
                    </td>
                    <td className={`${td} max-w-[280px] truncate`}>{l.memo || '—'}</td>
                    <td className={`${td} num text-right`}>{formatAmount(l.debit, { blankZero: true })}</td>
                    <td className={`${td} num text-right`}>{formatAmount(l.credit, { blankZero: true })}</td>
                    <td className={`${td} num text-right`}>{formatAmount(l.balance, { parens: true })}</td>
                  </tr>
                ))}
                <tr className="border-t border-line-2 font-semibold">
                  <td className={`${td} num`}>{formatDate(data.to)}</td>
                  <td className={td} colSpan={2}>
                    Saldo akhir
                  </td>
                  <td className={`${td} num text-right`}>{formatAmount(data.totalDebit)}</td>
                  <td className={`${td} num text-right`}>{formatAmount(data.totalCredit)}</td>
                  <td className={`${td} num text-right`}>{formatAmount(data.closing, { parens: true })}</td>
                </tr>
              </tbody>
            </TableShell>
          </div>
          {data.lines.length === 0 && <p className="text-sm text-muted">Tidak ada mutasi di rentang ini.</p>}
        </>
      )}

      <JournalDrawer me={me} id={openId} onClose={() => update({ id: null })} onOpen={(id) => update({ id })} />
    </div>
  )
}

function Stat({ label, value, note, strong }: { label: string; value: string; note?: string; strong?: boolean }) {
  return (
    <div className={`flex flex-col gap-1 rounded-[14px] border p-4 ${strong ? 'border-accent/40 bg-accent-soft/50' : 'border-line bg-surface'}`}>
      <span className="text-[12.5px] text-muted">{label}</span>
      <span className="num text-xl font-semibold tracking-tight">{formatAmount(value, { parens: true })}</span>
      {note && <span className="text-xs text-muted">{note}</span>}
    </div>
  )
}

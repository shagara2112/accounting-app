import { useState } from 'react'
import { useNavigate } from 'react-router'
import type { Me } from '../lib/api'
import { formatDate } from '../lib/dates'
import { useDebounced } from '../lib/hooks'
import { formatAmount, toUnits } from '../lib/money'
import { projectStatus, useProjects } from '../lib/projects'
import { can } from '../lib/session'
import { ProjectDialog } from '../components/ProjectDialog'
import { Badge, Button, EmptyState, PageHeader, Segmented, Skeleton, TableShell, inputClass, td, th } from '../components/ui'

/** Daftar proyek dengan laba per proyek dan pemakaian RAB (IND-03, IND-05). */
export function Projects({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const navigate = useNavigate()
  const [status, setStatus] = useState('berjalan')
  const [search, setSearch] = useState('')
  const q = useDebounced(search.trim())
  const list = useProjects(companyId, { status: status || undefined, q })
  const [creating, setCreating] = useState(false)
  const canCreate = can(me, 'project.create')
  const items = list.data ?? []
  const totals = items.reduce((t, p) => ({ revenue: t.revenue + toUnits(p.revenue), cost: t.cost + toUnits(p.cost), profit: t.profit + toUnits(p.profit) }), { revenue: 0n, cost: 0n, profit: 0n })

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Proyek"
        subtitle="Biaya dan pendapatan dibaca dari baris jurnal yang diberi proyek: tagihan, kas, jurnal, pemakaian material, dan faktur. Angka di sini selalu sama dengan buku besar."
        actions={canCreate && <Button onClick={() => setCreating(true)}>Proyek baru</Button>}
      />
      <div className="flex flex-wrap items-center gap-3">
        <Segmented
          label="Status"
          value={status}
          onChange={setStatus}
          options={[
            { value: 'berjalan', label: 'Berjalan' },
            { value: 'selesai', label: 'Selesai' },
            { value: 'batal', label: 'Batal' },
            { value: '', label: 'Semua' },
          ]}
        />
        <input type="search" aria-label="Cari kode atau nama proyek" placeholder="Cari kode atau nama proyek" value={search} onChange={(e) => setSearch(e.target.value)} className={`${inputClass} max-w-[280px] flex-1 border-line-2`} />
      </div>
      <TableShell minWidth={980}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Proyek</th>
            <th className={th}>Status</th>
            <th className={`${th} text-right`}>Nilai kontrak</th>
            <th className={`${th} text-right`}>Pendapatan</th>
            <th className={`${th} min-w-[200px]`}>Biaya vs RAB</th>
            <th className={`${th} text-right`}>Laba</th>
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
          {items.map((p) => {
            const profit = toUnits(p.profit)
            return (
              <tr key={p.id} onClick={() => navigate(`/proyek/${p.id}`)} className="cursor-pointer border-t border-line align-top transition-colors duration-150 hover:bg-hover">
                <td className={td}>
                  <span className="num block text-[12.5px] text-muted">{p.code}</span>
                  <span className="font-medium">{p.name}</span>
                  <span className="block text-[12.5px] text-muted">
                    {p.customerName || 'Proyek internal'}
                    {p.startDate && ` · mulai ${formatDate(p.startDate)}`}
                  </span>
                </td>
                <td className={td}>
                  <Badge tone={projectStatus[p.status].tone}>{projectStatus[p.status].label}</Badge>
                </td>
                <td className={`${td} num text-right`}>{formatAmount(p.contractValue, { blankZero: true }) || '—'}</td>
                <td className={`${td} num text-right`}>{formatAmount(p.revenue)}</td>
                <td className={td}>
                  <BudgetBar cost={p.cost} budget={p.budget} pct={p.costPct} />
                </td>
                <td className={`${td} num text-right font-semibold ${profit < 0n ? 'text-bad' : profit > 0n ? 'text-good' : ''}`}>{formatAmount(p.profit)}</td>
              </tr>
            )
          })}
          {items.length > 1 && (
            <tr className="border-t border-line-2 font-semibold">
              <td className={td} colSpan={3}>
                Total {items.length} proyek
              </td>
              <td className={`${td} num text-right`}>{formatAmount(totals.revenue)}</td>
              <td className={`${td} num`}>{formatAmount(totals.cost)}</td>
              <td className={`${td} num text-right ${totals.profit < 0n ? 'text-bad' : ''}`}>{formatAmount(totals.profit)}</td>
            </tr>
          )}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && (
        <EmptyState
          title={q ? 'Tidak ada proyek yang cocok' : 'Belum ada proyek di status ini'}
          body="Buat proyek untuk melacak biaya dan laba per kontrak atau per klien, menyusun RAB, dan menagih termin."
          action={canCreate && !q && <Button onClick={() => setCreating(true)}>Proyek baru</Button>}
        />
      )}
      <ProjectDialog me={me} editing={creating ? 'new' : null} onClose={() => setCreating(false)} onSaved={(p) => navigate(`/proyek/${p.id}`)} />
    </div>
  )
}

/** Biaya aktual terhadap RAB: hijau di bawah 90%, kuning sampai 100%, merah bila melampaui. */
export function BudgetBar({ cost, budget, pct }: { cost: string; budget: string; pct?: string }) {
  const used = pct ? Number(pct) : null
  const tone = used === null ? 'bg-line-2' : used > 100 ? 'bg-bad' : used >= 90 ? 'bg-warn' : 'bg-good'
  return (
    <div className="flex flex-col gap-1">
      <span className="num text-[13px]">
        {formatAmount(cost)}
        <span className="text-muted"> / {toUnits(budget) > 0n ? formatAmount(budget) : 'tanpa RAB'}</span>
      </span>
      {used !== null && (
        <span className="flex items-center gap-2">
          <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-hover" aria-hidden="true">
            <span className={`block h-full rounded-full ${tone}`} style={{ width: `${Math.min(used, 100)}%` }} />
          </span>
          <span className={`num text-[12px] ${used > 100 ? 'font-semibold text-bad' : 'text-muted'}`}>{used.toLocaleString('id-ID', { maximumFractionDigits: 1 })}%</span>
        </span>
      )}
    </div>
  )
}

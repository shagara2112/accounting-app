import { motion } from 'motion/react'
import type { AuditItem, Me, PeriodStatus } from '../lib/api'
import { monthLabel, monthName } from '../lib/dates'
import { periodStatus } from '../lib/labels'
import { formatAmount } from '../lib/money'
import { useAudit, useAuditVerify } from '../lib/queries'
import { Badge, Skeleton, type Tone } from '../components/ui'

const labels: Record<string, string> = {
  'auth.login': 'Masuk',
  'auth.login_failed': 'Gagal masuk',
  'auth.logout': 'Keluar',
  'ledger.journal.post': 'Posting jurnal',
  'ledger.journal.void': 'Void jurnal',
  'ledger.account.create': 'Akun dibuat',
  'ledger.account.update': 'Akun diubah',
  'ledger.coa.template': 'Template bagan akun',
  'ledger.period.status': 'Status periode',
  'ledger.settings.update': 'Tahun buku diubah',
}

const tone: Record<string, Tone> = {
  'auth.login_failed': 'bad',
  'auth.login': 'accent',
  'ledger.journal.post': 'good',
  'ledger.journal.void': 'bad',
  'ledger.period.status': 'warn',
}

const objects: Record<string, string> = { user: 'Pengguna', journal_entry: 'Jurnal', account: 'Akun', period: 'Periode', company: 'PT' }

const str = (v: unknown) => (typeof v === 'string' || typeof v === 'number' ? String(v) : '')

/** Ringkasan satu kejadian dari isi sebelum dan sesudahnya. */
function summary(it: AuditItem): string {
  const a = it.after ?? {}
  const b = it.before ?? {}
  switch (it.action) {
    case 'ledger.journal.post':
      return `${str(a.number)} · Rp ${formatAmount(str(a.total) || '0')}`
    case 'ledger.journal.void':
      return `${str(a.number)} → pembalik ${str(a.reversal)} · “${str(a.reason)}”`
    case 'ledger.account.create':
      return `${str(a.code)} ${str(a.name)}`
    case 'ledger.account.update': {
      const changes = []
      if (a.name !== b.name) changes.push(`nama “${str(b.name)}” → “${str(a.name)}”`)
      if (a.active !== b.active) changes.push(a.active ? 'diaktifkan' : 'dinonaktifkan')
      return `${str(a.code)} · ${changes.join(', ') || 'tanpa perubahan'}`
    }
    case 'ledger.coa.template':
      return `Template ${str(a.template)} · ${str(a.accounts)} akun`
    case 'ledger.period.status': {
      const from = periodStatus[b.status as PeriodStatus]?.label ?? str(b.status)
      const to = periodStatus[a.status as PeriodStatus]?.label ?? str(a.status)
      return `${it.objectId ? monthLabel(it.objectId) : ''}: ${from} → ${to}${a.reason ? ` · “${str(a.reason)}”` : ''}`
    }
    case 'ledger.settings.update':
      return `Tahun buku mulai ${monthName(Number(a.fiscalYearStartMonth))}`
    default:
      return objects[it.objectType] ?? it.objectType
  }
}

export function Audit({ me }: { me: Me }) {
  const list = useAudit(me.activeCompanyId)
  const verify = useAuditVerify(me.activeCompanyId)

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-[26px] font-bold tracking-tight">Audit trail</h1>
          <p className="mt-1.5 text-sm text-muted">Setiap kejadian tercatat berantai; perubahan diam-diam pada catatan lama langsung terdeteksi.</p>
        </div>
        {verify.data &&
          (verify.data.ok ? (
            <Badge tone="good">Rantai utuh · {verify.data.count.toLocaleString('id-ID')} catatan</Badge>
          ) : (
            <Badge tone="bad">Rantai rusak mulai catatan #{verify.data.brokenAt}</Badge>
          ))}
      </div>

      <div className="overflow-hidden rounded-[14px] border border-line bg-surface">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[860px] text-left text-sm">
            <thead className="bg-hover/60 text-[12.5px] text-muted">
              <tr>
                <th className="px-5 py-3 font-semibold">Waktu</th>
                <th className="px-5 py-3 font-semibold">Kejadian</th>
                <th className="px-5 py-3 font-semibold">Rincian</th>
                <th className="px-5 py-3 font-semibold">Alamat IP</th>
                <th className="px-5 py-3 text-right font-semibold">No.</th>
              </tr>
            </thead>
            <tbody>
              {list.isPending &&
                [0, 1, 2, 3].map((i) => (
                  <tr key={i}>
                    <td colSpan={5} className="px-5 py-3">
                      <Skeleton className="h-5" />
                    </td>
                  </tr>
                ))}
              {list.data?.items.map((it, i) => (
                <motion.tr
                  key={it.seq}
                  initial={{ opacity: 0, y: 4 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={{ duration: 0.2, delay: Math.min(i, 12) * 0.02, ease: [0.2, 0, 0, 1] }}
                  className="border-t border-line transition-colors duration-150 hover:bg-hover"
                >
                  <td className="num px-5 py-3 text-ink-2">
                    {new Date(it.at).toLocaleString('id-ID', { dateStyle: 'medium', timeStyle: 'medium' })}
                  </td>
                  <td className="px-5 py-3">
                    <Badge tone={tone[it.action] ?? 'neutral'}>{labels[it.action] ?? it.action}</Badge>
                  </td>
                  <td className="max-w-[420px] truncate px-5 py-3 text-ink-2" title={summary(it)}>
                    {summary(it)}
                  </td>
                  <td className="num px-5 py-3 text-ink-2">{it.ip ?? '—'}</td>
                  <td className="num px-5 py-3 text-right text-muted">{it.seq}</td>
                </motion.tr>
              ))}
            </tbody>
          </table>
        </div>
        {list.data?.items.length === 0 && <p className="px-5 py-10 text-center text-sm text-muted">Belum ada catatan.</p>}
      </div>
    </div>
  )
}

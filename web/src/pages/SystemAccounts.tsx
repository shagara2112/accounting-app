import { Fragment, useState } from 'react'
import { useNavigate } from 'react-router'
import type { Me, SystemAccount } from '../lib/api'
import { essentialRoles, useAccounts, useSetSystemAccount, useSystemAccounts } from '../lib/ledger'
import { can } from '../lib/session'
import { AccountPicker } from '../components/AccountPicker'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, EmptyState, PageHeader, Skeleton, TableShell, td, th } from '../components/ui'

const sentence = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

/** Akun sistem: akun yang dipakai posting otomatis (faktur, tagihan, pajak, saldo awal). PT yang
 *  memakai template sudah terisi dari kode bawaannya; PT dengan skema kode sendiri memilihnya di sini. */
export function SystemAccountsPage({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const roles = useSystemAccounts(companyId)
  const accounts = useAccounts(companyId)
  const set = useSetSystemAccount(companyId)
  const toast = useToast()
  const navigate = useNavigate()
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [failed, setFailed] = useState<{ role: string; message: string } | null>(null)
  const canEdit = can(me, 'ledger.account.edit')
  if (!can(me, 'ledger.view')) return <EmptyState title="Anda tidak punya izin melihat buku besar" />

  const items = roles.data?.items ?? []
  const all = accounts.data?.items ?? []
  const byId = new Map(all.map((a) => [a.id, a]))
  const groups = [...new Set(items.map((r) => r.group))]
  const missing = items.filter((r) => r.problem && essentialRoles.includes(r.role))

  function choose(r: SystemAccount, accountId: string) {
    if (!accountId || accountId === r.accountId) return
    const a = byId.get(accountId)
    setFailed(null)
    setDraft((d) => ({ ...d, [r.role]: accountId }))
    set.mutate(
      { role: r.role, accountId },
      {
        onSuccess: () => toast(`${sentence(r.label)} sekarang memakai ${a?.code} ${a?.name}`),
        onError: (e) => setFailed({ role: r.role, message: e.message }),
        onSettled: () =>
          setDraft((d) => {
            const { [r.role]: _, ...rest } = d
            return rest
          }),
      },
    )
  }

  function status(r: SystemAccount) {
    if (!r.problem) return r.chosen ? <Badge tone="good">Dipilih</Badge> : <Badge tone="neutral">Kode bawaan</Badge>
    if (r.accountId) return <Badge tone="bad">Nonaktif</Badge>
    return <Badge tone={essentialRoles.includes(r.role) ? 'warn' : 'neutral'}>Belum dipilih</Badge>
  }

  function hint(r: SystemAccount) {
    if (r.problem && r.accountId) return r.problem
    if (r.problem) return `Kode bawaan ${r.defaultCode} tidak ada di bagan akun; pilih akunnya.`
    if (!r.chosen) return `Ditemukan dari kode bawaan ${r.defaultCode}.`
    return ''
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Akun sistem"
        subtitle="Akun yang dipakai posting otomatis: faktur, tagihan, pembayaran, pajak, aset tetap, dan saldo awal. PT yang memakai template sudah terisi dari kode bawaannya; bila bagan akun memakai skema kode sendiri, pilih akunnya di sini."
        actions={
          <Button variant="secondary" onClick={() => navigate('/akun')}>
            Bagan akun
          </Button>
        }
      />
      {missing.length > 0 && (
        <Alert tone="warn">
          Akun untuk {missing.map((r) => r.label).join(', ')} belum dipilih. Faktur, tagihan, dan halaman Saldo awal membutuhkannya. Peran lain yang tidak dipakai PT ini (mis. piutang retensi untuk PT non-konstruksi) boleh dibiarkan.
        </Alert>
      )}

      <TableShell minWidth={760}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Peran</th>
            <th className={`${th} w-[340px]`}>Akun</th>
            <th className={`${th} w-[130px]`}>Status</th>
          </tr>
        </thead>
        <tbody>
          {(roles.isPending || accounts.isPending) &&
            [0, 1, 2, 3, 4, 5].map((i) => (
              <tr key={i} className="border-t border-line">
                <td colSpan={3} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            ))}
          {groups.map((g) => (
            <Fragment key={g}>
              <tr className="border-t border-line bg-hover/50">
                <td colSpan={3} className={`${td} font-semibold`}>
                  {g}
                </td>
              </tr>
              {items
                .filter((r) => r.group === g)
                .map((r) => {
                  const current = r.accountId ? byId.get(r.accountId) : undefined
                  const options = all.filter((a) => r.classes.includes(a.class))
                  const error = failed?.role === r.role ? failed.message : ''
                  return (
                    <tr key={r.role} className="border-t border-line align-top">
                      <td className={td}>
                        <p className="font-medium">{sentence(r.label)}</p>
                        <p className="mt-0.5 text-[13px] text-muted">{r.purpose}</p>
                      </td>
                      <td className={td}>
                        {canEdit ? (
                          <AccountPicker
                            accounts={options}
                            value={draft[r.role] ?? r.accountId ?? ''}
                            onChange={(id) => choose(r, id)}
                            invalid={Boolean(error)}
                            label={`Akun untuk ${r.label}`}
                            placeholder="Pilih akun"
                          />
                        ) : (
                          <p className="num">{current ? `${current.code} ${current.name}` : '—'}</p>
                        )}
                        {error ? <p className="mt-1 text-[13px] text-bad">{error}</p> : hint(r) && <p className="mt-1 text-[13px] text-muted">{hint(r)}</p>}
                      </td>
                      <td className={td}>{status(r)}</td>
                    </tr>
                  )
                })}
            </Fragment>
          ))}
        </tbody>
      </TableShell>
      {(roles.error || accounts.error) && <Alert>{(roles.error ?? accounts.error)?.message}</Alert>}
    </div>
  )
}

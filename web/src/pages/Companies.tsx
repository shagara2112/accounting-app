import { useState } from 'react'
import { ApiError, type Me } from '../lib/api'
import { useAdminCompanies, useCreateCompany, type Industry } from '../lib/admin'
import { formatDate } from '../lib/dates'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Dialog, EmptyState, Field, PageHeader, SelectField, Skeleton, TableShell, td, th } from '../components/ui'

/** PT dalam grup. PT baru langsung punya bagan akun dari template industrinya. */
export function Companies({ me }: { me: Me }) {
  const list = useAdminCompanies()
  const [creating, setCreating] = useState(false)
  if (!can(me, 'platform.system.manage')) return <EmptyState title="Anda tidak punya izin mengelola PT" />
  const industries = list.data?.industries ?? []
  const label = (k?: string) => industries.find((i) => i.key === k)?.label ?? 'Tanpa template'

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="PT dalam grup"
        subtitle="Semua PT memakai satu instalasi dan satu lisensi. Profil pajak (NPWP, PKP) diubah di halaman Pajak tiap PT."
        actions={<Button onClick={() => setCreating(true)}>PT baru</Button>}
      />
      <TableShell minWidth={720}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>PT</th>
            <th className={th}>Industri</th>
            <th className={th}>NPWP</th>
            <th className={`${th} text-right`}>Pengguna</th>
            <th className={th}>Dibuat</th>
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
          {list.data?.items.map((c) => (
            <tr key={c.id} className="border-t border-line">
              <td className={td}>
                <span className="block font-medium">{c.name}</span>
                <span className="block text-[12.5px] text-muted">{c.shortName}</span>
              </td>
              <td className={td}>{c.industry ? <Badge tone="neutral">{label(c.industry)}</Badge> : <span className="text-muted">—</span>}</td>
              <td className={`${td} num`}>{c.npwp ?? '—'}</td>
              <td className={`${td} num text-right`}>{c.users}</td>
              <td className={`${td} whitespace-nowrap text-ink-2`}>{formatDate(c.createdAt)}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.data && <NewCompanyDialog open={creating} onClose={() => setCreating(false)} industries={industries} />}
    </div>
  )
}

function NewCompanyDialog({ open, onClose, industries }: { open: boolean; onClose: () => void; industries: Industry[] }) {
  const create = useCreateCompany()
  const toast = useToast()
  const blank = { name: '', shortName: '', industry: (industries[0]?.key ?? '') as string, npwp: '' }
  const [v, setV] = useState(blank)
  const err = create.error instanceof ApiError ? create.error : null
  function close() {
    setV(blank)
    create.reset()
    onClose()
  }
  return (
    <Dialog
      open={open}
      onClose={close}
      width={520}
      title="PT baru"
      description="Anda mendapat peran yang sama dengan peran Anda di PT aktif. Pengguna lain diberi akses lewat halaman Pengguna."
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button
            busy={create.isPending}
            disabled={!v.name.trim() || !v.shortName.trim()}
            onClick={() =>
              create.mutate(v, {
                onSuccess: (c) => {
                  toast(`${c.name} dibuat dengan bagan akunnya`)
                  close()
                },
              })
            }
          >
            Buat PT
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field id="ptb-nama" label="Nama PT" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} error={err?.fields.name} />
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="ptb-singkat" label="Nama singkat" value={v.shortName} onChange={(e) => setV({ ...v, shortName: e.target.value })} error={err?.fields.shortName} />
          <SelectField id="ptb-industri" label="Industri dan template akun" value={v.industry} onChange={(e) => setV({ ...v, industry: e.target.value })} error={err?.fields.industry}>
            {industries.map((i) => (
              <option key={i.key} value={i.key}>
                {i.label}
              </option>
            ))}
            <option value="">Tanpa template (sembilan akun kelompok)</option>
          </SelectField>
        </div>
        <Field id="ptb-npwp" label="NPWP (opsional)" inputMode="numeric" className="num" value={v.npwp} onChange={(e) => setV({ ...v, npwp: e.target.value })} error={err?.fields.npwp} />
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

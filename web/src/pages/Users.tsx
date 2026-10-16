import { useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router'
import { ApiError, type Me } from '../lib/api'
import {
  useAdminUsers,
  useCreateUser,
  useDeleteRole,
  useResetMfa,
  useResetPassword,
  useRoles,
  useSaveRole,
  useSetAccess,
  useUnlockUser,
  useUpdateUser,
  type AdminRole,
  type AdminUser,
  type PermCatalog,
  type UserList,
} from '../lib/admin'
import { formatDateTime } from '../lib/dates'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Checkbox, Dialog, Drawer, EmptyState, Field, PageHeader, SelectField, Skeleton, TableShell, Tabs, td, th } from '../components/ui'

type Tab = 'pengguna' | 'persona'

/** Pengguna dan aksesnya per PT, serta persona kustom (PLT-05, PLT-12). */
export function Users({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams()
  const tab: Tab = params.get('tab') === 'persona' ? 'persona' : 'pengguna'
  if (!can(me, 'platform.user.manage')) return <EmptyState title="Anda tidak punya izin mengelola pengguna" />
  return (
    <div className="flex flex-col gap-5">
      <PageHeader title="Pengguna dan akses" subtitle="Satu orang bisa punya peran berbeda di tiap PT. Akses hanya bisa diatur di PT tempat Anda mengelola pengguna." />
      <Tabs
        label="Pengguna atau persona"
        value={tab}
        onChange={(v) => setParams(v === 'pengguna' ? {} : { tab: v }, { replace: true })}
        tabs={[
          { value: 'pengguna', label: 'Pengguna' },
          { value: 'persona', label: 'Persona' },
        ]}
      />
      {tab === 'pengguna' ? <UserTab me={me} /> : <RoleTab />}
    </div>
  )
}

function UserTab({ me }: { me: Me }) {
  const list = useAdminUsers()
  const [creating, setCreating] = useState(false)
  const [openId, setOpenId] = useState<string | null>(null)
  const data = list.data
  const open = data?.items.find((u) => u.id === openId) ?? null

  return (
    <>
      <div className="flex justify-end">
        <Button onClick={() => setCreating(true)}>Pengguna baru</Button>
      </div>
      <TableShell minWidth={860}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Nama</th>
            <th className={th}>Akses per PT</th>
            <th className={th}>2FA</th>
            <th className={th}>Status</th>
            <th className={th}>Login terakhir</th>
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
          {data?.items.map((u) => (
            <tr key={u.id} onClick={() => setOpenId(u.id)} className={`cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover ${u.active ? '' : 'text-muted'}`}>
              <td className={td}>
                <span className="block font-medium">
                  {u.name}
                  {u.id === me.user.id && <span className="ml-1.5 text-[12.5px] font-normal text-muted">(Anda)</span>}
                </span>
                <span className="block text-[12.5px] text-muted">{u.email}</span>
              </td>
              <td className={td}>
                <span className="flex flex-wrap gap-1.5">
                  {u.access.length === 0 && <span className="text-muted">Belum ada akses</span>}
                  {u.access.map((a) => {
                    const codes = a.branchIds.map((id) => data.companies.find((c) => c.id === a.companyId)?.branches.find((b) => b.id === id)?.code).filter(Boolean)
                    return (
                      <Badge key={a.companyId} tone="neutral">
                        {a.companyName} · {a.roleName}
                        {a.branchIds.length > 0 && ` · cabang ${codes.length > 0 ? codes.join(', ') : a.branchIds.length}`}
                      </Badge>
                    )
                  })}
                </span>
              </td>
              <td className={td}>
                <MfaBadge u={u} />
              </td>
              <td className={td}>
                <StatusBadge u={u} />
              </td>
              <td className={`${td} whitespace-nowrap text-ink-2`}>{u.lastLoginAt ? formatDateTime(u.lastLoginAt) : 'Belum pernah'}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {data && <NewUserDialog open={creating} onClose={() => setCreating(false)} data={data} />}
      {data && <UserDrawer user={open} data={data} self={open?.id === me.user.id} onClose={() => setOpenId(null)} />}
    </>
  )
}

function MfaBadge({ u }: { u: AdminUser }) {
  if (u.mfaEnabled) return <Badge tone="good">Aktif</Badge>
  if (u.mfaRequired) return <Badge tone="warn">Wajib, belum dipasang</Badge>
  return <span className="text-muted">—</span>
}

function StatusBadge({ u }: { u: AdminUser }) {
  if (!u.active) return <Badge tone="neutral">Nonaktif</Badge>
  if (u.locked) return <Badge tone="bad">Terkunci</Badge>
  return <Badge tone="good">Aktif</Badge>
}

function NewUserDialog({ open, onClose, data }: { open: boolean; onClose: () => void; data: UserList }) {
  const create = useCreateUser()
  const toast = useToast()
  const manageable = data.companies.filter((c) => c.manageable)
  const [v, setV] = useState({ name: '', email: '', password: '' })
  const [access, setAccess] = useState<Record<string, string>>({})
  const err = create.error instanceof ApiError ? create.error : null

  function close() {
    setV({ name: '', email: '', password: '' })
    setAccess({})
    create.reset()
    onClose()
  }

  function submit(e?: FormEvent) {
    e?.preventDefault()
    create.mutate(
      { ...v, access: Object.entries(access).filter(([, r]) => r).map(([companyId, roleKey]) => ({ companyId, roleKey })) },
      {
        onSuccess: (u) => {
          toast(`${u.name} ditambahkan`)
          close()
        },
      },
    )
  }

  return (
    <Dialog
      open={open}
      onClose={close}
      width={560}
      title="Pengguna baru"
      description="Berikan kata sandi awal ke orangnya secara langsung; ia bisa menggantinya di halaman Profil."
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button busy={create.isPending} disabled={!v.name.trim() || !v.email.trim() || v.password.length < 10} onClick={() => submit()}>
            Simpan
          </Button>
        </>
      }
    >
      <form onSubmit={submit} noValidate className="flex flex-col gap-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="pg-nama" label="Nama" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} error={err?.fields.name} />
          <Field id="pg-email" label="Email" type="email" value={v.email} onChange={(e) => setV({ ...v, email: e.target.value })} error={err?.fields.email} />
        </div>
        <Field id="pg-sandi" label="Kata sandi awal" type="password" autoComplete="new-password" hint="Minimal 10 karakter." value={v.password} onChange={(e) => setV({ ...v, password: e.target.value })} error={err?.fields.password} />
        <fieldset className="flex flex-col gap-3">
          <legend className="mb-1 text-sm font-semibold">Akses per PT</legend>
          {manageable.map((c, i) => (
            <SelectField
              key={c.id}
              id={`pg-akses-${c.id}`}
              label={c.name}
              value={access[c.id] ?? ''}
              onChange={(e) => setAccess({ ...access, [c.id]: e.target.value })}
              error={err?.fields[`access.${i}.companyId`] ?? err?.fields[`access.${i}.roleKey`]}
            >
              <option value="">Tanpa akses</option>
              {data.roles.map((r) => (
                <option key={r.key} value={r.key}>
                  {r.name}
                </option>
              ))}
            </SelectField>
          ))}
        </fieldset>
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </form>
    </Dialog>
  )
}

function UserDrawer({ user, data, self, onClose }: { user: AdminUser | null; data: UserList; self: boolean; onClose: () => void }) {
  return (
    <Drawer open={user !== null} onClose={onClose} title={user?.name ?? ''} description={user?.email}>
      {user && <UserDetail key={`${user.id}-${user.version}`} user={user} data={data} self={self} />}
    </Drawer>
  )
}

function UserDetail({ user, data, self }: { user: AdminUser; data: UserList; self: boolean }) {
  const update = useUpdateUser()
  const setAccess = useSetAccess()
  const unlock = useUnlockUser()
  const resetMfa = useResetMfa()
  const toast = useToast()
  const [v, setV] = useState({ name: user.name, email: user.email, active: user.active })
  const [password, setPassword] = useState(false)
  const [confirmMfa, setConfirmMfa] = useState(false)
  const err = update.error instanceof ApiError ? update.error : null
  const changed = v.name !== user.name || v.email !== user.email || v.active !== user.active
  const access = (companyId: string) => user.access.find((a) => a.companyId === companyId)

  return (
    <div className="flex flex-col gap-6">
      <section className="flex flex-col gap-4">
        <Field id="ub-nama" label="Nama" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} error={err?.fields.name} />
        <Field id="ub-email" label="Email" type="email" value={v.email} onChange={(e) => setV({ ...v, email: e.target.value })} error={err?.fields.email} />
        <Checkbox
          checked={v.active}
          onChange={(active) => setV({ ...v, active })}
          disabled={self}
          label="Akun aktif"
          hint={self ? 'Anda tidak bisa menonaktifkan akun sendiri.' : 'Menonaktifkan akun langsung mengakhiri semua sesinya.'}
        />
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
        <Button
          className="w-fit"
          disabled={!changed}
          busy={update.isPending}
          onClick={() => update.mutate({ id: user.id, ...v, version: user.version }, { onSuccess: () => toast('Data pengguna disimpan') })}
        >
          Simpan perubahan
        </Button>
      </section>

      <section className="flex flex-col gap-3">
        <h3 className="text-sm font-semibold">Akses per PT</h3>
        {self && <p className="text-sm text-muted">Akses Anda sendiri hanya bisa diubah pengelola lain.</p>}
        {data.companies.map((c) => {
          const a = access(c.id)
          const limit = a?.branchIds ?? []
          const branches = c.branches.filter((b) => b.active || limit.includes(b.id))
          const locked = self || !c.manageable || setAccess.isPending
          const save = (roleKey: string, branchIds: string[]) =>
            setAccess.mutate({ userId: user.id, companyId: c.id, roleKey, branchIds }, { onSuccess: () => toast(`Akses di ${c.name} diperbarui`) })
          return (
            <div key={c.id} className="flex flex-col gap-2">
              <SelectField
                id={`ub-akses-${c.id}`}
                label={c.name}
                value={a?.roleKey ?? ''}
                disabled={locked}
                hint={!c.manageable ? 'Anda tidak mengelola pengguna di PT ini.' : undefined}
                onChange={(e) => save(e.target.value, limit)}
              >
                <option value="">Tanpa akses</option>
                {data.roles.map((r) => (
                  <option key={r.key} value={r.key}>
                    {r.name}
                  </option>
                ))}
              </SelectField>
              {a && branches.length > 0 && (
                <div className="flex flex-col gap-1.5">
                  <p className="text-[13px] text-muted">
                    {limit.length === 0
                      ? 'Semua cabang. Pilih cabang untuk membatasi pengguna ke dokumen dan laba rugi cabang itu saja.'
                      : `Dibatasi ke ${limit.length} cabang: hanya dokumen dan laba rugi cabang ini; buku besar, pajak, dan laporan seluruh PT tertutup.`}
                  </p>
                  <div className="flex flex-wrap gap-1.5" role="group" aria-label={`Batasi cabang di ${c.name}`}>
                    {branches.map((b) => {
                      const on = limit.includes(b.id)
                      return (
                        <button
                          key={b.id}
                          type="button"
                          aria-pressed={on}
                          disabled={locked}
                          onClick={() => save(a.roleKey, on ? limit.filter((x) => x !== b.id) : [...limit, b.id])}
                          className={`cursor-pointer rounded-full border px-3 py-1 text-[13px] font-medium transition-colors duration-150 disabled:cursor-default disabled:opacity-60 ${on ? 'border-accent bg-accent-soft text-accent' : 'border-line-2 text-ink-2 hover:bg-hover'}`}
                        >
                          {b.code} · {b.name}
                        </button>
                      )
                    })}
                  </div>
                </div>
              )}
            </div>
          )
        })}
        {setAccess.error && <Alert>{setAccess.error.message}</Alert>}
      </section>

      {!self && (
        <section className="flex flex-col gap-3">
          <h3 className="text-sm font-semibold">Keamanan</h3>
          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" size="sm" onClick={() => setPassword(true)}>
              Atur ulang kata sandi
            </Button>
            {user.mfaEnabled && (
              <Button variant="secondary" size="sm" onClick={() => setConfirmMfa(true)}>
                Atur ulang 2FA
              </Button>
            )}
            {user.locked && (
              <Button variant="secondary" size="sm" busy={unlock.isPending} onClick={() => unlock.mutate(user.id, { onSuccess: () => toast('Akun dibuka') })}>
                Buka kunci akun
              </Button>
            )}
          </div>
          {(resetMfa.error || unlock.error) && <Alert>{(resetMfa.error ?? unlock.error)?.message}</Alert>}
        </section>
      )}

      <ResetPasswordDialog open={password} user={user} onClose={() => setPassword(false)} />
      <Dialog
        open={confirmMfa}
        onClose={() => setConfirmMfa(false)}
        title="Atur ulang 2FA?"
        description={`${user.name} perlu memasang 2FA lagi saat login berikutnya. Pakai ini bila ponselnya hilang atau diganti.`}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmMfa(false)}>
              Batal
            </Button>
            <Button
              variant="danger"
              busy={resetMfa.isPending}
              onClick={() =>
                resetMfa.mutate(user.id, {
                  onSuccess: () => {
                    toast('2FA diatur ulang')
                    setConfirmMfa(false)
                  },
                })
              }
            >
              Atur ulang
            </Button>
          </>
        }
      >
        <p className="text-sm text-ink-2">Semua sesi {user.name} akan diakhiri.</p>
      </Dialog>
    </div>
  )
}

function ResetPasswordDialog({ open, user, onClose }: { open: boolean; user: AdminUser; onClose: () => void }) {
  const reset = useResetPassword()
  const toast = useToast()
  const [password, setPassword] = useState('')
  const err = reset.error instanceof ApiError ? reset.error : null
  function close() {
    setPassword('')
    reset.reset()
    onClose()
  }
  return (
    <Dialog
      open={open}
      onClose={close}
      title="Atur ulang kata sandi"
      description={`Kata sandi baru untuk ${user.name}. Semua sesinya diakhiri; berikan kata sandi ini secara langsung.`}
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button
            busy={reset.isPending}
            disabled={password.length < 10}
            onClick={() =>
              reset.mutate(
                { id: user.id, password },
                {
                  onSuccess: () => {
                    toast('Kata sandi diatur ulang')
                    close()
                  },
                },
              )
            }
          >
            Simpan
          </Button>
        </>
      }
    >
      <Field id="reset-sandi" label="Kata sandi baru" type="password" autoComplete="new-password" hint="Minimal 10 karakter." value={password} onChange={(e) => setPassword(e.target.value)} error={err?.fields.password} />
      {err && !err.fields.password && <Alert>{err.message}</Alert>}
    </Dialog>
  )
}

// ---------------------------------------------------------------- persona

function RoleTab() {
  const roles = useRoles()
  const [editing, setEditing] = useState<AdminRole | 'new' | null>(null)
  const data = roles.data
  return (
    <>
      <div className="flex justify-end">
        <Button onClick={() => setEditing('new')}>Persona baru</Button>
      </div>
      <TableShell minWidth={640}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Persona</th>
            <th className={th}>Jenis</th>
            <th className={`${th} text-right`}>Izin</th>
            <th className={`${th} text-right`}>Dipakai</th>
          </tr>
        </thead>
        <tbody>
          {roles.isPending && (
            <tr>
              <td colSpan={4} className={td}>
                <Skeleton className="h-5" />
              </td>
            </tr>
          )}
          {data?.items.map((r) => (
            <tr key={r.id} onClick={() => setEditing(r)} className="cursor-pointer border-t border-line transition-colors duration-150 hover:bg-hover">
              <td className={`${td} font-medium`}>{r.name}</td>
              <td className={td}>{r.builtin ? <Badge tone="neutral">Bawaan</Badge> : <Badge tone="accent">Kustom</Badge>}</td>
              <td className={`${td} num text-right`}>{r.permissions.length}</td>
              <td className={`${td} num text-right`}>{r.assignments} akses</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {data && <RoleDialog role={editing} catalog={data.catalog} onClose={() => setEditing(null)} />}
    </>
  )
}

function RoleDialog({ role, catalog, onClose }: { role: AdminRole | 'new' | null; catalog: PermCatalog; onClose: () => void }) {
  const r = role === 'new' ? null : role
  return (
    <Dialog
      open={role !== null}
      onClose={onClose}
      width={640}
      title={role === 'new' ? 'Persona baru' : (r?.name ?? '')}
      description={r?.builtin ? 'Peran bawaan sesuai matriks izin dan tidak bisa diubah. Buat persona kustom bila butuh kombinasi lain.' : 'Pilih izin dari katalog. Mengubah baris jurnal yang sudah diposting tidak pernah bisa diberikan.'}
    >
      {role !== null && <RoleForm key={r?.id ?? 'new'} role={r} catalog={catalog} onClose={onClose} />}
    </Dialog>
  )
}

function RoleForm({ role, catalog, onClose }: { role: AdminRole | null; catalog: PermCatalog; onClose: () => void }) {
  const save = useSaveRole()
  const del = useDeleteRole()
  const toast = useToast()
  const [name, setName] = useState(role?.name ?? '')
  const [perms, setPerms] = useState<Set<string>>(new Set(role?.permissions ?? []))
  const readOnly = role?.builtin ?? false
  const err = save.error instanceof ApiError ? save.error : null

  function toggle(key: string, on: boolean) {
    const next = new Set(perms)
    if (on) next.add(key)
    else next.delete(key)
    setPerms(next)
  }

  return (
    <div className="flex flex-col gap-5">
      {!readOnly && <Field id="persona-nama" label="Nama persona" placeholder="mis. Kasir proyek" value={name} onChange={(e) => setName(e.target.value)} error={err?.fields.name} />}
      <div className="grid gap-5 sm:grid-cols-2">
        {catalog.map((g) => (
          <fieldset key={g.name} className="flex flex-col gap-2">
            <legend className="mb-1 text-[12.5px] font-semibold tracking-wide text-muted uppercase">{g.name}</legend>
            {g.perms.map((p) => (
              <Checkbox key={p.key} checked={perms.has(p.key)} onChange={(on) => toggle(p.key, on)} disabled={readOnly} label={p.label} />
            ))}
          </fieldset>
        ))}
      </div>
      {err?.fields.permissions && <Alert>{err.fields.permissions}</Alert>}
      {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      {del.error && <Alert>{del.error.message}</Alert>}
      {!readOnly && (
        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-line pt-4">
          {role ? (
            <Button
              variant="ghost"
              busy={del.isPending}
              onClick={() =>
                del.mutate(role.id, {
                  onSuccess: () => {
                    toast('Persona dihapus')
                    onClose()
                  },
                })
              }
            >
              Hapus persona
            </Button>
          ) : (
            <span />
          )}
          <Button
            busy={save.isPending}
            disabled={!name.trim() || perms.size === 0}
            onClick={() =>
              save.mutate(
                { id: role?.id, name, permissions: [...perms], version: role?.version ?? 0 },
                {
                  onSuccess: () => {
                    toast(role ? 'Persona disimpan' : 'Persona dibuat')
                    onClose()
                  },
                },
              )
            }
          >
            {role ? 'Simpan persona' : 'Buat persona'}
          </Button>
        </div>
      )}
    </div>
  )
}

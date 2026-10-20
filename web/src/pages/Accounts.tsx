import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router'
import { ApiError, type Account, type AccountClass, type Me } from '../lib/api'
import { subtreeIds, treeOrder, useAccounts, useApplyTemplate, useCreateAccount, useUpdateAccount } from '../lib/ledger'
import { formatAmount } from '../lib/money'
import { activeCompany, can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Checkbox, Dialog, Field, PageHeader, SelectField, Skeleton, TableShell, inputClass, td, th } from '../components/ui'

const templateInfo: Record<string, { title: string; body: string }> = {
  jasa: { title: 'Jasa', body: 'Pendapatan jasa dan retainer, beban tenaga ahli, pendapatan diterima di muka.' },
  konstruksi: { title: 'Konstruksi', body: 'Biaya proyek per jenis, piutang dan utang retensi, uang muka proyek, alat berat, PPh final 4(2).' },
  retail: { title: 'Retail', body: 'Persediaan barang dagang, HPP, kas toko, piutang kartu dan QRIS, retur penjualan.' },
  kosong: { title: 'Mulai kosong', body: 'Hanya sembilan akun kelompok berkode 1–9; akun lain Anda buat sendiri.' },
}

export function Accounts({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const accounts = useAccounts(companyId)
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Account | null>(null)

  const list = useMemo(() => treeOrder(accounts.data?.items ?? []), [accounts.data])
  const classes = accounts.data?.classes ?? []
  const q = search.trim().toLowerCase()
  const visible = q ? list.filter((a) => a.code.toLowerCase().startsWith(q) || a.name.toLowerCase().includes(q)) : list
  const canCreate = can(me, 'ledger.account.create')
  const canEdit = can(me, 'ledger.account.edit')

  if (accounts.isSuccess && list.length === 0) {
    return <TemplatePicker me={me} canCreate={canCreate} />
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Bagan akun"
        subtitle="Angka kepala kode menentukan kelompok, saldo normal, dan letaknya di laporan. Saldo per hari ini; akun laba rugi sejak awal tahun buku."
        actions={canCreate && <Button onClick={() => setCreating(true)}>Akun baru</Button>}
      />
      <input
        type="search"
        aria-label="Cari kode atau nama akun"
        placeholder="Cari kode atau nama akun"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        className={`${inputClass} border-line-2 max-w-[320px]`}
      />

      <TableShell minWidth={680}>
        <thead className="bg-hover/60">
          <tr>
            <th className={`${th} w-[120px]`}>Kode</th>
            <th className={th}>Nama akun</th>
            <th className={`${th} w-[120px]`}>Saldo normal</th>
            <th className={`${th} w-[180px] text-right`}>Saldo</th>
            {canEdit && (
              <th className={`${th} w-[64px]`}>
                <span className="sr-only">Ubah</span>
              </th>
            )}
          </tr>
        </thead>
        <tbody>
          {accounts.isPending &&
            [0, 1, 2, 3, 4, 5].map((i) => (
              <tr key={i} className="border-t border-line">
                <td colSpan={5} className={td}>
                  <Skeleton className="h-5" />
                </td>
              </tr>
            ))}
          {visible.map((a) => {
            const cls = classes[a.class - 1]
            const group = a.level === 1
            return (
              <tr
                key={a.id}
                onClick={a.postable ? () => navigate(`/buku-besar?akun=${a.id}`) : undefined}
                className={`border-t border-line ${group ? 'bg-hover/50' : ''} ${a.postable ? 'cursor-pointer transition-colors duration-150 hover:bg-hover' : ''} ${a.active ? '' : 'text-muted'}`}
              >
                <td className={`${td} num ${a.postable ? '' : 'font-semibold'}`}>{a.code}</td>
                <td className={td}>
                  <span className="flex items-center gap-2" style={{ paddingLeft: q ? 0 : (a.level - 1) * 18 }}>
                    <span className={a.postable ? '' : 'font-semibold'}>{a.name}</span>
                    {!a.active && <Badge tone="neutral">Nonaktif</Badge>}
                    {a.system && (
                      <span title={`Dipakai posting otomatis: ${a.systemRoles?.join(', ')}`}>
                        <Badge tone="accent">Akun sistem</Badge>
                      </span>
                    )}
                  </span>
                </td>
                <td className={`${td} text-[13px] text-muted`}>{group && cls ? `${cls.normal === 'debit' ? 'Debit' : 'Kredit'} · ${cls.statement === 'neraca' ? 'Neraca' : 'Laba rugi'}` : ''}</td>
                <td className={`${td} num text-right ${a.postable ? '' : 'font-semibold text-ink-2'}`}>{formatAmount(a.balance, { parens: true, blankZero: !group })}</td>
                {canEdit && (
                  <td className={`${td} py-1`}>
                    <button
                      type="button"
                      aria-label={`Ubah ${a.code} ${a.name}`}
                      onClick={(e) => {
                        e.stopPropagation()
                        setEditing(a)
                      }}
                      className="flex size-9 cursor-pointer items-center justify-center rounded-[8px] text-muted transition-colors hover:bg-surface hover:text-ink"
                    >
                      <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z" />
                      </svg>
                    </button>
                  </td>
                )}
              </tr>
            )
          })}
        </tbody>
      </TableShell>
      {q && visible.length === 0 && <p className="text-sm text-muted">Tidak ada akun yang cocok dengan “{search}”.</p>}

      <NewAccountDialog me={me} open={creating} onClose={() => setCreating(false)} accounts={list} classes={classes} />
      <EditAccountDialog me={me} account={editing} onClose={() => setEditing(null)} />
    </div>
  )
}

function TemplatePicker({ me, canCreate }: { me: Me; canCreate: boolean }) {
  const industry = activeCompany(me)?.industry
  const [choice, setChoice] = useState<string>(industry ?? 'jasa')
  const apply = useApplyTemplate(me.activeCompanyId)
  const toast = useToast()
  return (
    <div className="flex flex-col gap-5">
      <PageHeader title="Bagan akun" subtitle="PT ini belum punya bagan akun. Mulai dari template standar SAK EP sesuai industri; semua PT dalam grup memakai kode yang sama sehingga konsolidasi mudah." />
      {!canCreate ? (
        <Alert tone="neutral">Bagan akun belum diisi. Minta Staf akuntansi, Manager, atau Pemilik memilih template.</Alert>
      ) : (
        <Card title="Pilih template" subtitle="Template bisa ditambah atau diubah namanya setelah dipasang.">
          <div role="radiogroup" aria-label="Template bagan akun" className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-3">
            {Object.entries(templateInfo).map(([key, t]) => {
              const active = key === choice
              return (
                <button
                  key={key}
                  type="button"
                  role="radio"
                  aria-checked={active}
                  onClick={() => setChoice(key)}
                  className={`flex cursor-pointer flex-col gap-1.5 rounded-[12px] border p-4 text-left transition-all duration-150 ${
                    active ? 'border-accent bg-accent-soft shadow-sm' : 'border-line-2 hover:border-muted hover:bg-hover'
                  }`}
                >
                  <span className="flex items-center gap-2 font-semibold">
                    {t.title}
                    {key === industry && <Badge tone="accent">Industri PT ini</Badge>}
                  </span>
                  <span className="text-[13px] text-muted">{t.body}</span>
                </button>
              )
            })}
          </div>
          {apply.isError && <Alert>{apply.error.message}</Alert>}
          <div>
            <Button
              busy={apply.isPending}
              onClick={() => apply.mutate(choice, { onSuccess: (r) => toast(`${r.created} akun dibuat dari template ${templateInfo[choice].title.toLowerCase()}`) })}
            >
              Pakai template {templateInfo[choice].title.toLowerCase()}
            </Button>
          </div>
        </Card>
      )}
    </div>
  )
}

function NewAccountDialog({ me, open, onClose, accounts, classes }: { me: Me; open: boolean; onClose: () => void; accounts: Account[]; classes: AccountClass[] }) {
  const create = useCreateAccount(me.activeCompanyId)
  const toast = useToast()
  const [parentId, setParentId] = useState('')
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [postable, setPostable] = useState(true)
  const [attempt, setAttempt] = useState(0)

  const parents = useMemo(() => treeOrder(accounts).filter((a) => !a.postable && a.active && a.level < 5), [accounts])
  const parent = accounts.find((a) => a.id === parentId)
  const head = /^[1-9]/.test(code) ? Number(code[0]) : 0
  const cls = head ? classes[head - 1] : undefined
  const mismatch = parent && head && head !== parent.class
  const err = create.error instanceof ApiError ? create.error : null

  function reset() {
    setParentId('')
    setCode('')
    setName('')
    setPostable(true)
    create.reset()
    onClose()
  }

  function submit() {
    setAttempt((n) => n + 1)
    create.mutate(
      { code: code.trim(), name: name.trim(), parentId: parentId || null, postable },
      {
        onSuccess: (a) => {
          toast(`Akun ${a.code} ${a.name} dibuat`)
          reset()
        },
      },
    )
  }

  return (
    <Dialog
      open={open}
      onClose={reset}
      title="Akun baru"
      description="Kode berkepala sama dengan induknya, mis. di bawah 1-1100 Kas dan setara kas: 1-1113."
      footer={
        <>
          <Button variant="secondary" onClick={reset}>
            Batal
          </Button>
          <Button busy={create.isPending} disabled={!code.trim() || !name.trim() || Boolean(mismatch)} onClick={submit}>
            Simpan akun
          </Button>
        </>
      }
    >
      <SelectField
        id="akun-induk"
        label="Induk"
        value={parentId}
        error={err?.fields.parentId}
        onChange={(e) => {
          setParentId(e.target.value)
          const p = accounts.find((a) => a.id === e.target.value)
          if (p && !code) setCode(p.code.length === 1 ? `${p.code}-` : p.code.slice(0, -2))
        }}
      >
        <option value="">— Tanpa induk (akun kelompok 1–9) —</option>
        {parents.map((a) => (
          <option key={a.id} value={a.id}>
            {'  '.repeat(a.level - 1)}
            {a.code} · {a.name}
          </option>
        ))}
      </SelectField>
      <Field
        id="akun-kode"
        label="Kode"
        data-autofocus
        value={code}
        placeholder="mis. 1-1113"
        className="num"
        onChange={(e) => setCode(e.target.value)}
        error={mismatch ? `Induk ${parent.code} berkepala ${parent.class}; kode harus diawali ${parent.class}.` : err?.fields.code}
        hint={cls && `${cls.name} · saldo normal ${cls.normal} · masuk ${cls.statement === 'neraca' ? 'neraca' : 'laba rugi'}`}
      />
      <Field id="akun-nama" label="Nama akun" value={name} maxLength={120} onChange={(e) => setName(e.target.value)} error={err?.fields.name} />
      <Checkbox
        checked={postable}
        onChange={setPostable}
        disabled={!parentId}
        label="Akun detail (bisa dipakai di jurnal)"
        hint="Matikan untuk akun header yang hanya mengelompokkan akun di bawahnya. Jenis akun tidak bisa diubah setelah disimpan."
      />
      {err && Object.keys(err.fields).length === 0 && <Alert shakeKey={attempt}>{err.message}</Alert>}
    </Dialog>
  )
}

function EditAccountDialog({ me, account, onClose }: { me: Me; account: Account | null; onClose: () => void }) {
  const update = useUpdateAccount(me.activeCompanyId)
  const all = useAccounts(me.activeCompanyId).data?.items
  const toast = useToast()
  const [parentId, setParentId] = useState('')
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [active, setActive] = useState(true)
  const [shownFor, setShownFor] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)

  if (account && shownFor !== account.id) {
    setShownFor(account.id)
    setParentId(account.parentId ?? '')
    setCode(account.code)
    setName(account.name)
    setActive(account.active)
    update.reset()
  }

  function close() {
    setShownFor(null)
    onClose()
  }

  const err = update.error instanceof ApiError ? update.error : null
  // Induk yang boleh dipilih: akun header aktif di kelas yang sama, bukan akun ini atau turunannya.
  const parents = useMemo(() => {
    if (!account || !all) return []
    const own = subtreeIds(all, account.id)
    return treeOrder(all).filter((a) => !a.postable && a.class === account.class && !own.has(a.id) && (a.active || a.id === account.parentId) && a.level < 5)
  }, [account, all])
  return (
    <Dialog
      open={account !== null}
      onClose={close}
      title={account ? `Ubah ${account.code}` : 'Ubah akun'}
      description="Jenis akun (header atau detail) tidak bisa diubah. Induk dan kode bisa dikoreksi selama angka kepalanya sama; laporan dan riwayat jurnal ikut memakai pengelompokan dan kode baru."
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button
            busy={update.isPending}
            disabled={!name.trim() || !code.trim()}
            onClick={() => {
              if (!account) return
              setAttempt((n) => n + 1)
              update.mutate(
                { id: account.id, code: code.trim(), name: name.trim(), active, version: account.version, parentId: parentId && parentId !== account.parentId ? parentId : undefined },
                {
                  onSuccess: (a) => {
                    toast(`Akun ${a.code} disimpan`)
                    close()
                  },
                },
              )
            }}
          >
            Simpan
          </Button>
        </>
      }
    >
      <SelectField
        id="ubah-induk"
        label="Induk"
        value={parentId}
        disabled={!account || account.level === 1}
        onChange={(e) => setParentId(e.target.value)}
        error={err?.fields.parentId}
        hint={
          account?.level === 1
            ? 'Akun kelompok tidak punya induk.'
            : 'Pindahkan ke kelompok lain di kelas yang sama untuk memperbaiki pengelompokan. Akun header ikut membawa akun-akun di bawahnya.'
        }
      >
        {account?.level === 1 && <option value="">— Tanpa induk —</option>}
        {parents.map((a) => (
          <option key={a.id} value={a.id}>
            {'\u00a0\u00a0'.repeat(a.level - 1)}
            {a.code} · {a.name}
          </option>
        ))}
      </SelectField>
      <Field
        id="ubah-kode"
        label="Kode akun"
        className="num"
        value={code}
        maxLength={20}
        disabled={!account || account.level === 1}
        onChange={(e) => setCode(e.target.value)}
        error={err?.fields.code}
        hint={
          account?.level === 1
            ? 'Kode akun kelompok tidak bisa diubah.'
            : account?.system
              ? `Akun sistem untuk ${account.systemRoles?.join(', ') ?? 'posting otomatis'}. Kodenya boleh diganti sesuai skema kode PT (angka kepala ${account.class} tetap); posting otomatis tetap memakai akun ini.`
              : `Untuk koreksi salah ketik atau skema kode PT. Angka kepala ${account?.class ?? ''} harus tetap supaya letaknya di laporan tidak berubah.`
        }
      />
      <Field id="ubah-nama" label="Nama akun" data-autofocus value={name} maxLength={120} onChange={(e) => setName(e.target.value)} error={err?.fields.name} />
      <Checkbox
        checked={active}
        onChange={setActive}
        label="Akun aktif"
        hint="Akun nonaktif tidak bisa dipakai di jurnal baru. Akun yang masih bersaldo tidak bisa dinonaktifkan."
      />
      {err && !err.fields.name && !err.fields.code && !err.fields.parentId && <Alert shakeKey={attempt}>{err.message}</Alert>}
    </Dialog>
  )
}

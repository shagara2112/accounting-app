import { useState } from 'react'
import { ApiError, newIdempotencyKey, type Account, type Contact, type ContactInput, type Me } from '../lib/api'
import { useCashAccounts, useContacts, useSaveContact } from '../lib/cash'
import { formatAmount, fromUnits, parseInput } from '../lib/money'
import { useDebounced } from '../lib/hooks'
import { useAccounts } from '../lib/ledger'
import { can } from '../lib/session'
import { AccountPicker } from '../components/AccountPicker'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Checkbox, Dialog, EmptyState, Field, PageHeader, Segmented, SelectField, Skeleton, TableShell, TextAreaField, inputClass, td, th } from '../components/ui'

type Role = '' | 'customer' | 'supplier'

const empty: ContactInput = {
  name: '',
  isCustomer: true,
  isSupplier: false,
  npwp: '',
  nik: '',
  nitku: '',
  pkp: false,
  address: '',
  email: '',
  phone: '',
  paymentTermsDays: 30,
  bankName: '',
  bankAccount: '',
  active: true,
  version: 0,
  linkedCompanyId: null,
  icExpenseAccountId: null,
  icRevenueAccountId: null,
  icCashAccountId: null,
  creditLimit: null,
  billingContact: '',
}

/** Isian formulir dari kontak tersimpan; id dan nama PT grup bukan bagian isian. */
function toInput(c: Contact): ContactInput {
  const { id: _id, linkedCompanyName: _name, ...rest } = c
  return { ...rest, creditLimit: c.creditLimit ? formatAmount(c.creditLimit) : null }
}

/** Limit kredit di formulir ditulis gaya Indonesia; kosong = tanpa limit. */
function limitBody(v: string | null | undefined): string | null {
  if (!v || !v.trim()) return null
  const u = parseInput(v)
  return u === null ? v : fromUnits(u)
}

/** "0123456789012345" → "0123 4567 8901 2345" supaya mudah dicocokkan dengan kartu NPWP. */
function groupDigits(s: string) {
  return s.replace(/(\d{4})(?=\d)/g, '$1 ')
}

export function Contacts({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const [role, setRole] = useState<Role>('')
  const [search, setSearch] = useState('')
  const [inactive, setInactive] = useState(false)
  const q = useDebounced(search.trim())
  const list = useContacts(companyId, { role: role || undefined, q, inactive })
  const [editing, setEditing] = useState<Contact | 'new' | null>(null)
  const canCreate = can(me, 'core.contact.create')
  const canEdit = can(me, 'core.contact.edit')
  const items = list.data ?? []

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Kontak"
        subtitle="Pelanggan dan pemasok PT ini. NPWP 16 digit dan NITKU dipakai untuk faktur pajak dan bukti potong."
        actions={canCreate && <Button onClick={() => setEditing('new')}>Kontak baru</Button>}
      />
      <div className="flex flex-wrap items-center gap-3">
        <Segmented
          label="Peran"
          value={role}
          onChange={setRole}
          options={[
            { value: '', label: 'Semua' },
            { value: 'customer', label: 'Pelanggan' },
            { value: 'supplier', label: 'Pemasok' },
          ]}
        />
        <input type="search" aria-label="Cari nama atau NPWP" placeholder="Cari nama atau NPWP" value={search} onChange={(e) => setSearch(e.target.value)} className={`${inputClass} max-w-[280px] flex-1 border-line-2`} />
        <Checkbox checked={inactive} onChange={setInactive} label="Tampilkan nonaktif" />
      </div>
      <TableShell minWidth={820}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Nama</th>
            <th className={th}>Peran</th>
            <th className={th}>NPWP</th>
            <th className={`${th} text-right`}>Termin</th>
            <th className={th}>Kontak</th>
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
          {items.map((c) => (
            <tr
              key={c.id}
              onClick={canEdit ? () => setEditing(c) : undefined}
              className={`border-t border-line ${canEdit ? 'cursor-pointer transition-colors duration-150 hover:bg-hover' : ''} ${c.active ? '' : 'text-muted'}`}
            >
              <td className={td}>
                <span className="font-medium">{c.name}</span>
                {!c.active && (
                  <Badge tone="neutral" className="ml-2">
                    Nonaktif
                  </Badge>
                )}
              </td>
              <td className={td}>
                <span className="flex gap-1.5">
                  {c.isCustomer && <Badge tone="accent">Pelanggan</Badge>}
                  {c.isSupplier && <Badge tone="warn">Pemasok</Badge>}
                  {c.pkp && <Badge tone="good">PKP</Badge>}
                  {c.linkedCompanyId && <Badge tone="neutral">PT grup</Badge>}
                </span>
              </td>
              <td className={`${td} num whitespace-nowrap`}>{c.npwp ? groupDigits(c.npwp) : c.nik ? `NIK ${groupDigits(c.nik)}` : '—'}</td>
              <td className={`${td} num text-right`}>{c.paymentTermsDays} hari</td>
              <td className={`${td} max-w-[240px] truncate text-ink-2`}>{[c.phone, c.email].filter(Boolean).join(' · ') || '—'}</td>
            </tr>
          ))}
        </tbody>
      </TableShell>
      {list.isSuccess && items.length === 0 && (
        <EmptyState
          title={q ? 'Tidak ada kontak yang cocok' : 'Belum ada kontak'}
          body="Tambahkan pelanggan sebelum membuat faktur, dan pemasok sebelum mencatat tagihan."
          action={canCreate && !q && <Button onClick={() => setEditing('new')}>Kontak baru</Button>}
        />
      )}
      <ContactDialog me={me} editing={editing} onClose={() => setEditing(null)} />
    </div>
  )
}

function ContactDialog({ me, editing, onClose }: { me: Me; editing: Contact | 'new' | null; onClose: () => void }) {
  const save = useSaveContact(me.activeCompanyId)
  const accounts = useAccounts(editing !== null ? me.activeCompanyId : null)
  const all = accounts.data?.items ?? []
  const toast = useToast()
  const [form, setForm] = useState<ContactInput>(empty)
  const [shownFor, setShownFor] = useState<Contact | 'new' | null>(null)
  const [key, setKey] = useState(newIdempotencyKey)
  const [attempt, setAttempt] = useState(0)

  if (editing && shownFor !== editing) {
    setShownFor(editing)
    setForm(editing === 'new' ? empty : toInput(editing))
    setKey(newIdempotencyKey())
    save.reset()
  }
  const isNew = (editing ?? shownFor) === 'new'
  const err = save.error instanceof ApiError ? save.error : null
  const set = (patch: Partial<ContactInput>) => setForm((f) => ({ ...f, ...patch }))

  function close() {
    setShownFor(null)
    onClose()
  }

  return (
    <Dialog
      open={editing !== null}
      onClose={close}
      width={640}
      title={isNew ? 'Kontak baru' : `Ubah ${form.name}`}
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button
            busy={save.isPending}
            disabled={!form.name.trim()}
            onClick={() => {
              setAttempt((n) => n + 1)
              save.mutate(
                { id: shownFor && shownFor !== 'new' ? shownFor.id : undefined, input: { ...form, creditLimit: limitBody(form.creditLimit) }, idempotencyKey: key },
                { onSuccess: (c) => (toast(`${c.name} disimpan`), close()) },
              )
            }}
          >
            Simpan
          </Button>
        </>
      }
    >
      <Field id="k-nama" label="Nama" data-autofocus value={form.name} maxLength={200} onChange={(e) => set({ name: e.target.value })} error={err?.fields.name} />
      <div className="flex flex-wrap gap-x-6">
        <Checkbox checked={form.isCustomer} onChange={(v) => set({ isCustomer: v })} label="Pelanggan" />
        <Checkbox checked={form.isSupplier} onChange={(v) => set({ isSupplier: v })} label="Pemasok" />
        <Checkbox checked={form.pkp} onChange={(v) => set({ pkp: v })} label="Pengusaha Kena Pajak (PKP)" />
      </div>
      {err?.fields.role && <p className="text-[13px] text-bad">{err.fields.role}</p>}
      <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
        <Field id="k-npwp" label="NPWP" inputMode="numeric" className="num" placeholder="16 digit" value={form.npwp} onChange={(e) => set({ npwp: e.target.value })} error={err?.fields.npwp} />
        <Field id="k-nik" label="NIK (tanpa NPWP)" inputMode="numeric" className="num" placeholder="16 digit" value={form.nik} onChange={(e) => set({ nik: e.target.value })} error={err?.fields.nik} />
        <Field id="k-nitku" label="NITKU" inputMode="numeric" className="num" placeholder="22 digit, diawali NPWP" value={form.nitku} onChange={(e) => set({ nitku: e.target.value })} error={err?.fields.nitku} />
        <Field
          id="k-termin"
          label="Termin pembayaran (hari)"
          type="number"
          min={0}
          max={365}
          value={form.paymentTermsDays}
          onChange={(e) => set({ paymentTermsDays: Number(e.target.value) })}
          error={err?.fields.paymentTermsDays}
        />
        <Field id="k-email" label="Email penagihan" type="email" value={form.email} onChange={(e) => set({ email: e.target.value })} error={err?.fields.email} />
        <Field id="k-telepon" label="Telepon" value={form.phone} onChange={(e) => set({ phone: e.target.value })} />
      </div>
      <TextAreaField id="k-alamat" label="Alamat" rows={2} value={form.address} onChange={(e) => set({ address: e.target.value })} />
      {form.isCustomer && (
        <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
          <Field
            id="k-limit"
            label="Limit kredit"
            inputMode="decimal"
            className="num text-right"
            placeholder="Kosong = tanpa limit"
            hint="Faktur yang membuat piutang melewati limit diberi peringatan sebelum diposting."
            value={form.creditLimit ?? ''}
            onChange={(e) => set({ creditLimit: e.target.value })}
            onBlur={(e) => {
              const u = e.target.value.trim() ? parseInput(e.target.value) : null
              if (u !== null) set({ creditLimit: formatAmount(u) })
            }}
            error={err?.fields.creditLimit}
          />
          <Field id="k-penagihan" label="Kontak penagihan" placeholder="mis. Ibu Sari, bagian keuangan" value={form.billingContact} maxLength={200} onChange={(e) => set({ billingContact: e.target.value })} error={err?.fields.billingContact} />
        </div>
      )}
      {form.isSupplier && (
        <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
          <Field id="k-bank" label="Bank pemasok" value={form.bankName} onChange={(e) => set({ bankName: e.target.value })} />
          <Field id="k-rekening" label="Nomor rekening" value={form.bankAccount} onChange={(e) => set({ bankAccount: e.target.value })} />
        </div>
      )}
      <GroupLink me={me} form={form} set={set} linkedName={shownFor && shownFor !== 'new' ? shownFor.linkedCompanyName : undefined} accounts={all} fields={err?.fields ?? {}} />
      {!isNew && <Checkbox checked={form.active} onChange={(v) => set({ active: v })} label="Kontak aktif" hint="Kontak nonaktif tidak bisa dipilih di faktur atau tagihan baru." />}
      {err && Object.keys(err.fields).length === 0 && <Alert shakeKey={attempt}>{err.message}</Alert>}
    </Dialog>
  )
}

/** Tautan ke PT lain dalam grup (ME-03): faktur ke kontak ini otomatis menjadi tagihan di PT
 *  itu, dan sebaliknya. Akun default dipakai untuk dokumen cerminan yang dibuat di PT ini. */
function GroupLink({
  me,
  form,
  set,
  linkedName,
  accounts,
  fields,
}: {
  me: Me
  form: ContactInput
  set: (patch: Partial<ContactInput>) => void
  linkedName?: string
  accounts: Account[]
  fields: Record<string, string>
}) {
  const others = me.companies.filter((c) => c.id !== me.activeCompanyId && c.kind === 'pt')
  const cashAccounts = (useCashAccounts(me.activeCompanyId).data ?? []).filter((a) => a.active)
  const unknown = form.linkedCompanyId && !others.some((c) => c.id === form.linkedCompanyId)
  return (
    <div className="flex flex-col gap-3 rounded-[12px] border border-line p-4">
      <SelectField
        id="k-grup"
        label="Mewakili PT dalam grup"
        value={form.linkedCompanyId ?? ''}
        onChange={(e) => set({ linkedCompanyId: e.target.value || null, ...(e.target.value ? {} : { icExpenseAccountId: null, icRevenueAccountId: null, icCashAccountId: null }) })}
        error={fields.linkedCompanyId}
        hint="Faktur ke kontak ini otomatis menjadi tagihan di PT itu (dan sebaliknya); void di satu sisi ikut mem-void pasangannya."
      >
        <option value="">Bukan PT grup</option>
        {others.map((c) => (
          <option key={c.id} value={c.id}>
            {c.name}
          </option>
        ))}
        {unknown && <option value={form.linkedCompanyId ?? ''}>{linkedName ?? 'PT grup lain'}</option>}
      </SelectField>
      {form.linkedCompanyId && (
        <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
          <div className="flex min-w-0 flex-col gap-1.5">
            <label htmlFor="k-ic-beban" className="text-sm font-medium text-ink-2">
              Akun tagihan cerminan
            </label>
            <AccountPicker
              id="k-ic-beban"
              accounts={accounts.filter((a) => [1, 5, 6, 8].includes(a.class))}
              value={form.icExpenseAccountId ?? ''}
              onChange={(v) => set({ icExpenseAccountId: v || null })}
              invalid={Boolean(fields.icExpenseAccountId)}
            />
            <p className={`text-[13px] ${fields.icExpenseAccountId ? 'text-bad' : 'text-muted'}`}>{fields.icExpenseAccountId ?? 'Beban atau aset saat PT itu menagih PT ini.'}</p>
          </div>
          <div className="flex min-w-0 flex-col gap-1.5">
            <label htmlFor="k-ic-pendapatan" className="text-sm font-medium text-ink-2">
              Akun faktur cerminan
            </label>
            <AccountPicker
              id="k-ic-pendapatan"
              accounts={accounts.filter((a) => [4, 7].includes(a.class))}
              value={form.icRevenueAccountId ?? ''}
              onChange={(v) => set({ icRevenueAccountId: v || null })}
              invalid={Boolean(fields.icRevenueAccountId)}
            />
            <p className={`text-[13px] ${fields.icRevenueAccountId ? 'text-bad' : 'text-muted'}`}>{fields.icRevenueAccountId ?? 'Pendapatan saat PT itu mencatat tagihan dari PT ini.'}</p>
          </div>
          <SelectField
            id="k-ic-kas"
            label="Rekening pelunasan antar-PT"
            value={form.icCashAccountId ?? ''}
            onChange={(e) => set({ icCashAccountId: e.target.value || null })}
            error={fields.icCashAccountId}
            hint="Pelunasan di PT itu otomatis tercatat di PT ini lewat rekening ini."
          >
            <option value="">Pilih rekening</option>
            {cashAccounts.map((a) => (
              <option key={a.accountId} value={a.accountId}>
                {a.code} {a.name}
              </option>
            ))}
          </SelectField>
        </div>
      )}
    </div>
  )
}

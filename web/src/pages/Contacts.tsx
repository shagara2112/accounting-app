import { useState } from 'react'
import { ApiError, newIdempotencyKey, type Contact, type ContactInput, type Me } from '../lib/api'
import { useContacts, useSaveContact } from '../lib/cash'
import { useDebounced } from '../lib/hooks'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Checkbox, Dialog, EmptyState, Field, PageHeader, Segmented, Skeleton, TableShell, TextAreaField, inputClass, td, th } from '../components/ui'

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
  const toast = useToast()
  const [form, setForm] = useState<ContactInput>(empty)
  const [shownFor, setShownFor] = useState<Contact | 'new' | null>(null)
  const [key, setKey] = useState(newIdempotencyKey)
  const [attempt, setAttempt] = useState(0)

  if (editing && shownFor !== editing) {
    setShownFor(editing)
    setForm(editing === 'new' ? empty : { ...editing })
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
                { id: shownFor && shownFor !== 'new' ? shownFor.id : undefined, input: form, idempotencyKey: key },
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
      {form.isSupplier && (
        <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
          <Field id="k-bank" label="Bank pemasok" value={form.bankName} onChange={(e) => set({ bankName: e.target.value })} />
          <Field id="k-rekening" label="Nomor rekening" value={form.bankAccount} onChange={(e) => set({ bankAccount: e.target.value })} />
        </div>
      )}
      {!isNew && <Checkbox checked={form.active} onChange={(v) => set({ active: v })} label="Kontak aktif" hint="Kontak nonaktif tidak bisa dipilih di faktur atau tagihan baru." />}
      {err && Object.keys(err.fields).length === 0 && <Alert shakeKey={attempt}>{err.message}</Alert>}
    </Dialog>
  )
}

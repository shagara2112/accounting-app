import { useMemo, useState } from 'react'
import { ApiError, type Me, type ProjectDetail, type ProjectInput, type ProjectStatus } from '../lib/api'
import { useContacts } from '../lib/cash'
import { useTaxCodes } from '../lib/docs'
import { todayISO } from '../lib/dates'
import { useAccounts } from '../lib/ledger'
import { formatAmount, fromUnits, parseInput } from '../lib/money'
import { useSaveProject } from '../lib/projects'
import { AccountPicker } from './AccountPicker'
import { SearchSelect } from './SearchSelect'
import { useToast } from './Toast'
import { Alert, Button, Dialog, Field, Segmented, SelectField, Skeleton, TextAreaField } from './ui'

type Form = {
  code: string
  name: string
  customerId: string
  contractValue: string
  dpPct: string
  retentionPct: string
  pphCode: string
  revenueAccountId: string
  startDate: string
  endDate: string
  status: ProjectStatus
  notes: string
  version: number
}

function toForm(p: ProjectDetail): Form {
  return {
    code: p.code,
    name: p.name,
    customerId: p.customerId ?? '',
    contractValue: formatAmount(p.contractValue, { blankZero: true }),
    dpPct: formatAmount(p.dpPct, { blankZero: true }),
    retentionPct: formatAmount(p.retentionPct, { blankZero: true }),
    pphCode: p.pphCode ?? '',
    revenueAccountId: p.revenueAccountId ?? '',
    startDate: p.startDate ?? '',
    endDate: p.endDate ?? '',
    status: p.status,
    notes: p.notes,
    version: p.version,
  }
}

/** Data proyek (IND-01): pemberi kerja, nilai kontrak, uang muka, retensi, dan PPh final 4(2). */
export function ProjectDialog({ me, editing, onClose, onSaved }: { me: Me; editing: ProjectDetail | 'new' | null; onClose: () => void; onSaved?: (p: ProjectDetail) => void }) {
  const companyId = me.activeCompanyId
  const open = editing !== null
  const accounts = useAccounts(companyId)
  const customers = useContacts(companyId, { role: 'customer' })
  const codes = useTaxCodes(open ? companyId : null, todayISO())
  const save = useSaveProject(companyId)
  const toast = useToast()
  const [shownFor, setShownFor] = useState<ProjectDetail | 'new' | null>(null)
  const [form, setForm] = useState<Form | null>(null)
  const [attempt, setAttempt] = useState(0)
  const revenue = useMemo(() => (accounts.data?.items ?? []).filter((a) => a.class === 4 || a.class === 7), [accounts.data])

  if (editing && shownFor !== editing && accounts.data) {
    setShownFor(editing)
    setForm(
      editing === 'new'
        ? {
            code: '',
            name: '',
            customerId: '',
            contractValue: '',
            dpPct: '',
            retentionPct: '',
            pphCode: '',
            revenueAccountId: revenue.find((a) => a.code === '4-1001')?.id ?? '',
            startDate: todayISO(),
            endDate: '',
            status: 'berjalan',
            notes: '',
            version: 0,
          }
        : toForm(editing),
    )
    save.reset()
  }
  const isNew = (editing ?? shownFor) === 'new'
  const err = save.error instanceof ApiError ? save.error : null
  const set = (patch: Partial<Form>) => setForm((f) => (f ? { ...f, ...patch } : f))
  const pph = (codes.data ?? []).filter((c) => c.kind === 'pph')

  function close() {
    setShownFor(null)
    onClose()
  }

  function submit() {
    if (!form) return
    setAttempt((n) => n + 1)
    const input: ProjectInput = {
      code: form.code.trim(),
      name: form.name.trim(),
      customerId: form.customerId || null,
      contractValue: fromUnits(parseInput(form.contractValue) ?? 0n),
      dpPct: fromUnits(parseInput(form.dpPct) ?? 0n),
      retentionPct: fromUnits(parseInput(form.retentionPct) ?? 0n),
      pphCode: form.pphCode || null,
      revenueAccountId: form.revenueAccountId || null,
      startDate: form.startDate || null,
      endDate: form.endDate || null,
      status: form.status,
      notes: form.notes.trim(),
      version: form.version,
    }
    save.mutate(
      { id: shownFor && shownFor !== 'new' ? shownFor.id : undefined, input },
      {
        onSuccess: (p) => {
          toast(`${p.code} ${p.name} disimpan`)
          close()
          onSaved?.(p)
        },
      },
    )
  }

  const unreadable = (v: string) => (parseInput(v) === null ? 'Angka tidak terbaca.' : undefined)
  return (
    <Dialog
      open={open}
      onClose={close}
      width={680}
      title={isNew ? 'Proyek baru' : `Ubah ${form?.code ?? ''}`}
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button busy={save.isPending} disabled={!form?.code.trim() || !form?.name.trim()} onClick={submit}>
            Simpan
          </Button>
        </>
      }
    >
      {!form ? (
        <Skeleton className="h-72" />
      ) : (
        <>
          <div className="grid grid-cols-[180px_1fr] gap-4 max-sm:grid-cols-1">
            <Field id="pr-kode" label="Kode" data-autofocus className="num" placeholder="PRJ-2026-01" value={form.code} maxLength={30} onChange={(e) => set({ code: e.target.value })} error={err?.fields.code} />
            <Field id="pr-nama" label="Nama proyek" value={form.name} maxLength={200} onChange={(e) => set({ name: e.target.value })} error={err?.fields.name} />
          </div>
          <div className="flex min-w-0 flex-col gap-1.5">
            <label htmlFor="pr-pelanggan" className="text-sm font-medium text-ink-2">
              Pemberi kerja
            </label>
            <SearchSelect
              id="pr-pelanggan"
              options={(customers.data ?? []).filter((c) => c.active || c.id === form.customerId).map((c) => ({ id: c.id, label: c.name, detail: c.npwp ? `NPWP ${c.npwp}` : undefined }))}
              value={form.customerId}
              onChange={(v) => set({ customerId: v })}
              placeholder="Cari pelanggan"
              clearLabel="Tanpa pemberi kerja (proyek internal)"
              invalid={Boolean(err?.fields.customerId)}
            />
            {err?.fields.customerId ? <p className="text-[13px] text-bad">{err.fields.customerId}</p> : <p className="text-[13px] text-muted">Wajib untuk faktur uang muka dan termin.</p>}
          </div>
          <div className="grid grid-cols-3 gap-4 max-sm:grid-cols-1">
            <Field
              id="pr-kontrak"
              label="Nilai kontrak (sebelum PPN)"
              inputMode="decimal"
              className="num text-right"
              placeholder="0"
              value={form.contractValue}
              onChange={(e) => set({ contractValue: e.target.value })}
              onBlur={() => {
                const u = parseInput(form.contractValue)
                if (u !== null && form.contractValue) set({ contractValue: formatAmount(u) })
              }}
              error={err?.fields.contractValue ?? unreadable(form.contractValue)}
            />
            <Field id="pr-um" label="Uang muka %" inputMode="decimal" className="num text-right" placeholder="0" value={form.dpPct} onChange={(e) => set({ dpPct: e.target.value })} error={err?.fields.dpPct ?? unreadable(form.dpPct)} />
            <Field id="pr-retensi" label="Retensi %" inputMode="decimal" className="num text-right" placeholder="0" value={form.retentionPct} onChange={(e) => set({ retentionPct: e.target.value })} error={err?.fields.retentionPct ?? unreadable(form.retentionPct)} />
          </div>
          <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
            <div className="flex min-w-0 flex-col gap-1.5">
              <label htmlFor="pr-akun" className="text-sm font-medium text-ink-2">
                Akun pendapatan termin
              </label>
              <AccountPicker id="pr-akun" accounts={revenue} value={form.revenueAccountId} onChange={(v) => set({ revenueAccountId: v })} invalid={Boolean(err?.fields.revenueAccountId)} />
              {err?.fields.revenueAccountId && <p className="text-[13px] text-bad">{err.fields.revenueAccountId}</p>}
            </div>
            <SelectField id="pr-pph" label="PPh yang dipotong pemberi kerja" value={form.pphCode} onChange={(e) => set({ pphCode: e.target.value })} error={err?.fields.pphCode}>
              <option value="">Tidak ada</option>
              {pph.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name}
                </option>
              ))}
            </SelectField>
          </div>
          <div className="grid grid-cols-2 gap-4 max-sm:grid-cols-1">
            <Field id="pr-mulai" label="Mulai" type="date" value={form.startDate} onChange={(e) => set({ startDate: e.target.value })} />
            <Field id="pr-selesai" label="Target selesai" type="date" min={form.startDate} value={form.endDate} onChange={(e) => set({ endDate: e.target.value })} error={err?.fields.endDate} />
          </div>
          {!isNew && (
            <div className="flex flex-col gap-1.5">
              <span className="text-sm font-medium text-ink-2">Status</span>
              <Segmented
                label="Status proyek"
                value={form.status}
                onChange={(v) => set({ status: v })}
                options={[
                  { value: 'berjalan', label: 'Berjalan' },
                  { value: 'selesai', label: 'Selesai' },
                  { value: 'batal', label: 'Batal' },
                ]}
              />
              {form.status === 'batal' && <p className="text-[13px] text-muted">Proyek batal tidak bisa dipakai di transaksi baru; transaksi lamanya tetap bisa di-void.</p>}
            </div>
          )}
          <TextAreaField id="pr-catatan" label="Catatan" rows={2} value={form.notes} maxLength={1000} onChange={(e) => set({ notes: e.target.value })} error={err?.fields.notes} />
          {err && Object.keys(err.fields).length === 0 && <Alert shakeKey={attempt}>{err.message}</Alert>}
        </>
      )}
    </Dialog>
  )
}

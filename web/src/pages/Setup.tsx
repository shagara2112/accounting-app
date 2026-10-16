import { useState, type FormEvent } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { ApiError } from '../lib/api'
import { useSetup, type SetupInput, type SetupStatus } from '../lib/admin'
import { Alert, Button, Field, SelectField, ease } from '../components/ui'
import { AuthFrame } from './Login'

type Step = 'license' | 'company' | 'admin'

const fieldStep: Record<string, Step> = { license: 'license', company: 'company', admin: 'admin' }

/** Setup wizard pertama kali (PLT-02): lisensi, PT pertama dengan bagan akunnya, dan akun Pemilik. */
export function Setup({ status }: { status: SetupStatus }) {
  const steps: Step[] = status.remoteLicense ? ['license', 'company', 'admin'] : ['company', 'admin']
  const [step, setStep] = useState<Step>(steps[0])
  const [v, setV] = useState<SetupInput & { confirm: string }>({
    license: { consoleUrl: '', clientId: '', licenseKey: '' },
    company: { name: '', shortName: '', industry: status.industries[0]?.key ?? '', npwp: '' },
    admin: { name: '', email: '', password: '' },
    confirm: '',
  })
  const setup = useSetup()
  const err = setup.error instanceof ApiError ? setup.error : null
  const [attempt, setAttempt] = useState(0)
  const index = steps.indexOf(step)
  const f = (k: string) => err?.fields[k]

  function next(e: FormEvent) {
    e.preventDefault()
    if (index < steps.length - 1) {
      setStep(steps[index + 1])
      return
    }
    setAttempt((n) => n + 1)
    const { confirm: _c, license, ...rest } = v
    setup.mutate(status.remoteLicense ? { ...rest, license } : rest, {
      // Galat per field dibawa kembali ke langkah tempat field itu berada.
      onError: (e) => {
        if (!(e instanceof ApiError)) return
        const first = Object.keys(e.fields)[0]
        const s = first && fieldStep[first.split('.')[0]]
        if (s && steps.includes(s)) setStep(s)
      },
    })
  }

  const company = v.company
  const admin = v.admin
  const mismatch = v.confirm !== '' && v.confirm !== admin.password
  const canNext =
    step === 'license'
      ? !!(v.license?.consoleUrl && v.license.clientId && v.license.licenseKey)
      : step === 'company'
        ? !!(company.name.trim() && company.shortName.trim())
        : !!(admin.name.trim() && admin.email.trim() && admin.password.length >= 10 && v.confirm === admin.password)

  return (
    <AuthFrame wide>
      <form onSubmit={next} noValidate className="flex flex-col gap-5 rounded-[14px] border border-line bg-surface p-6 shadow-sm">
        <div>
          <p className="text-[12.5px] font-semibold tracking-wide text-muted uppercase">
            Menyiapkan Lajur · langkah {index + 1} dari {steps.length}
          </p>
          <h1 className="mt-1 text-xl font-bold tracking-tight">
            {step === 'license' ? 'Aktifkan lisensi' : step === 'company' ? 'PT pertama' : 'Akun Pemilik'}
          </h1>
          <p className="mt-1 text-sm text-muted">
            {step === 'license'
              ? 'Isi Client ID, license key, dan alamat License Console dari pemilik aplikasi. License key hanya dipakai di server ini.'
              : step === 'company'
                ? 'PT lain dalam grup bisa ditambahkan nanti. Bagan akun standar SAK EP dipasang sesuai industrinya.'
                : 'Akun ini memegang semua akses dan bisa membuat akun lain. Setelah ini Anda diminta memasang 2FA.'}
          </p>
        </div>
        <div className="h-1 overflow-hidden rounded-full bg-hover" aria-hidden="true">
          <motion.div className="h-full rounded-full bg-accent" animate={{ width: `${((index + 1) / steps.length) * 100}%` }} transition={{ duration: 0.25, ease }} />
        </div>

        <AnimatePresence mode="wait" initial={false}>
          <motion.div key={step} initial={{ opacity: 0, x: 12 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -12 }} transition={{ duration: 0.18, ease }} className="flex flex-col gap-4">
            {step === 'license' && v.license && (
              <>
                <Field
                  id="console-url"
                  label="Alamat License Console"
                  placeholder="https://script.google.com/macros/s/…/exec"
                  value={v.license.consoleUrl}
                  onChange={(e) => setV({ ...v, license: { ...v.license!, consoleUrl: e.target.value } })}
                  error={f('license.consoleUrl')}
                />
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field id="client-id" label="Client ID" value={v.license.clientId} onChange={(e) => setV({ ...v, license: { ...v.license!, clientId: e.target.value } })} error={f('license.clientId')} />
                  <Field
                    id="license-key"
                    label="License key"
                    placeholder="LIC-XXXX-XXXX-XXXX-XXXX"
                    className="num"
                    value={v.license.licenseKey}
                    onChange={(e) => setV({ ...v, license: { ...v.license!, licenseKey: e.target.value } })}
                    error={f('license.licenseKey')}
                  />
                </div>
              </>
            )}
            {step === 'company' && (
              <>
                <Field id="pt-nama" label="Nama PT" placeholder="PT Contoh Sejahtera" value={company.name} onChange={(e) => setV({ ...v, company: { ...company, name: e.target.value } })} error={f('company.name')} />
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field
                    id="pt-singkat"
                    label="Nama singkat"
                    hint="Tampil di pilihan PT, mis. Konstruksi."
                    value={company.shortName}
                    onChange={(e) => setV({ ...v, company: { ...company, shortName: e.target.value } })}
                    error={f('company.shortName')}
                  />
                  <SelectField id="pt-industri" label="Industri" value={company.industry} onChange={(e) => setV({ ...v, company: { ...company, industry: e.target.value } })} error={f('company.industry')}>
                    {status.industries.map((i) => (
                      <option key={i.key} value={i.key}>
                        {i.label}
                      </option>
                    ))}
                  </SelectField>
                </div>
                <Field id="pt-npwp" label="NPWP (opsional)" inputMode="numeric" className="num" value={company.npwp} onChange={(e) => setV({ ...v, company: { ...company, npwp: e.target.value } })} error={f('company.npwp')} />
              </>
            )}
            {step === 'admin' && (
              <>
                <Field id="admin-nama" label="Nama lengkap" autoComplete="name" value={admin.name} onChange={(e) => setV({ ...v, admin: { ...admin, name: e.target.value } })} error={f('admin.name')} />
                <Field id="admin-email" label="Email" type="email" autoComplete="username" value={admin.email} onChange={(e) => setV({ ...v, admin: { ...admin, email: e.target.value } })} error={f('admin.email')} />
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field
                    id="admin-sandi"
                    label="Kata sandi"
                    type="password"
                    autoComplete="new-password"
                    hint="Minimal 10 karakter."
                    value={admin.password}
                    onChange={(e) => setV({ ...v, admin: { ...admin, password: e.target.value } })}
                    error={f('admin.password')}
                  />
                  <Field
                    id="admin-ulang"
                    label="Ulangi kata sandi"
                    type="password"
                    autoComplete="new-password"
                    value={v.confirm}
                    onChange={(e) => setV({ ...v, confirm: e.target.value })}
                    error={mismatch ? 'Belum sama dengan kata sandi di sebelahnya.' : undefined}
                  />
                </div>
              </>
            )}
          </motion.div>
        </AnimatePresence>

        {err && Object.keys(err.fields).length === 0 && <Alert shakeKey={attempt}>{err.message}</Alert>}
        <div className="flex items-center justify-between gap-3">
          {index > 0 ? (
            <Button type="button" variant="secondary" onClick={() => setStep(steps[index - 1])}>
              Kembali
            </Button>
          ) : (
            <span />
          )}
          <Button type="submit" busy={setup.isPending} disabled={!canNext}>
            {index < steps.length - 1 ? 'Lanjut' : 'Selesai dan masuk'}
          </Button>
        </div>
      </form>
    </AuthFrame>
  )
}

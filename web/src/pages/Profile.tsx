import { useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, type Me } from '../lib/api'
import { useChangePassword, useMfaStatus, useTotpDisable } from '../lib/admin'
import { formatDate } from '../lib/dates'
import { useLogout } from '../lib/queries'
import { activeCompany } from '../lib/session'
import { MfaEnroll } from '../components/MfaEnroll'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Dialog, Field, PageHeader, Skeleton } from '../components/ui'
import { AuthFrame } from './Login'

/** Layar pengganti aplikasi selama pengguna wajib 2FA belum memasangnya. */
export function MfaRequired({ me }: { me: Me }) {
  const qc = useQueryClient()
  const logout = useLogout()
  return (
    <AuthFrame wide>
      <div className="flex flex-col gap-5 rounded-[14px] border border-line bg-surface p-6 shadow-sm">
        <div>
          <h1 className="text-xl font-bold tracking-tight">Pasang 2FA dulu</h1>
          <p className="mt-1 text-sm text-muted">
            Halo {me.user.name}. Peran Anda bisa mengelola pengguna atau sistem, jadi akun ini wajib dilindungi kode dari aplikasi autentikator di ponsel.
          </p>
        </div>
        <MfaEnroll onDone={() => qc.invalidateQueries()} />
        <button type="button" onClick={() => logout.mutate()} className="w-fit cursor-pointer text-sm text-muted hover:text-ink">
          Keluar
        </button>
      </div>
    </AuthFrame>
  )
}

export function Profile({ me }: { me: Me }) {
  const company = activeCompany(me)
  return (
    <div className="flex flex-col gap-5">
      <PageHeader title="Profil" subtitle="Kata sandi dan 2FA akun Anda. Nama dan email diubah oleh pengelola pengguna." />
      <Card title={me.user.name} subtitle={me.user.email}>
        <ul className="flex flex-col gap-1.5 text-sm">
          {me.companies.map((c) => (
            <li key={c.id} className="flex flex-wrap items-center gap-2">
              <span className={c.id === company?.id ? 'font-semibold' : ''}>{c.name}</span>
              <Badge tone="neutral">{c.roleName}</Badge>
            </li>
          ))}
        </ul>
      </Card>
      <div className="grid gap-5 lg:grid-cols-2">
        <PasswordCard />
        <MfaCard />
      </div>
    </div>
  )
}

function PasswordCard() {
  const change = useChangePassword()
  const toast = useToast()
  const [v, setV] = useState({ current: '', new: '', confirm: '' })
  const err = change.error instanceof ApiError ? change.error : null
  const mismatch = v.confirm !== '' && v.confirm !== v.new

  function submit(e: FormEvent) {
    e.preventDefault()
    change.mutate(
      { current: v.current, new: v.new },
      {
        onSuccess: () => {
          setV({ current: '', new: '', confirm: '' })
          toast('Kata sandi diganti · sesi di perangkat lain diakhiri')
        },
      },
    )
  }

  return (
    <Card title="Kata sandi" subtitle="Minimal 10 karakter. Mengganti kata sandi mengakhiri sesi Anda di perangkat lain.">
      <form onSubmit={submit} noValidate className="flex flex-col gap-4">
        <Field id="sandi-lama" label="Kata sandi sekarang" type="password" autoComplete="current-password" value={v.current} onChange={(e) => setV({ ...v, current: e.target.value })} error={err?.fields.current} />
        <Field id="sandi-baru" label="Kata sandi baru" type="password" autoComplete="new-password" value={v.new} onChange={(e) => setV({ ...v, new: e.target.value })} error={err?.fields.new} />
        <Field
          id="sandi-ulang"
          label="Ulangi kata sandi baru"
          type="password"
          autoComplete="new-password"
          value={v.confirm}
          onChange={(e) => setV({ ...v, confirm: e.target.value })}
          error={mismatch ? 'Belum sama dengan kata sandi baru.' : undefined}
        />
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
        <Button type="submit" className="w-fit" busy={change.isPending} disabled={!v.current || v.new.length < 10 || v.confirm !== v.new}>
          Ganti kata sandi
        </Button>
      </form>
    </Card>
  )
}

function MfaCard() {
  const status = useMfaStatus()
  const [enrolling, setEnrolling] = useState(false)
  const [disabling, setDisabling] = useState(false)
  const toast = useToast()
  const s = status.data

  return (
    <Card
      title="Verifikasi dua langkah (2FA)"
      subtitle="Setelah kata sandi, Lajur meminta kode 6 digit dari aplikasi autentikator di ponsel Anda."
      actions={s && <Badge tone={s.enabled ? 'good' : s.required ? 'bad' : 'neutral'}>{s.enabled ? 'Aktif' : 'Belum aktif'}</Badge>}
    >
      {!s ? (
        <Skeleton className="h-16" />
      ) : s.enabled ? (
        <div className="flex flex-col gap-3 text-sm">
          <p className="text-ink-2">
            Aktif sejak {s.enabledAt ? formatDate(s.enabledAt.slice(0, 10)) : '—'}.
            {s.required && ' Peran Anda wajib memakai 2FA. Bila ponsel hilang, minta pengelola pengguna mengatur ulang 2FA Anda.'}
          </p>
          {!s.required && (
            <Button variant="secondary" className="w-fit" onClick={() => setDisabling(true)}>
              Matikan 2FA
            </Button>
          )}
        </div>
      ) : enrolling ? (
        <MfaEnroll
          onDone={() => {
            setEnrolling(false)
            toast('2FA aktif')
          }}
        />
      ) : (
        <Button className="w-fit" onClick={() => setEnrolling(true)}>
          Pasang 2FA
        </Button>
      )}
      <DisableDialog open={disabling} onClose={() => setDisabling(false)} />
    </Card>
  )
}

function DisableDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const disable = useTotpDisable()
  const toast = useToast()
  const [password, setPassword] = useState('')
  const err = disable.error instanceof ApiError ? disable.error : null
  return (
    <Dialog
      open={open}
      onClose={() => {
        setPassword('')
        disable.reset()
        onClose()
      }}
      title="Matikan 2FA"
      description="Masuk ke Lajur cukup dengan kata sandi lagi. Konfirmasi dengan kata sandi Anda."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button
            variant="danger"
            busy={disable.isPending}
            disabled={!password}
            onClick={() =>
              disable.mutate(password, {
                onSuccess: () => {
                  toast('2FA dimatikan')
                  setPassword('')
                  onClose()
                },
              })
            }
          >
            Matikan 2FA
          </Button>
        </>
      }
    >
      <Field id="konfirmasi-sandi" label="Kata sandi" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} error={err?.fields.password} />
      {err && !err.fields.password && <Alert>{err.message}</Alert>}
    </Dialog>
  )
}

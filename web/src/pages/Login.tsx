import { useState, type FormEvent, type ReactNode } from 'react'
import { motion } from 'motion/react'
import { Button, Field } from '../components/ui'
import { Logo } from '../components/Logo'
import { useLogin } from '../lib/queries'
import { useLoginTotp } from '../lib/admin'

export function Login() {
  const login = useLogin()
  const totp = useLoginTotp()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [challenge, setChallenge] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)

  function submit(e: FormEvent) {
    e.preventDefault()
    setAttempt((n) => n + 1)
    login.mutate(
      { email, password },
      {
        onSuccess: (res) => {
          if (res.totpRequired && res.challenge) {
            setChallenge(res.challenge)
            setCode('')
          }
        },
      },
    )
  }

  function submitCode(e: FormEvent) {
    e.preventDefault()
    if (!challenge) return
    setAttempt((n) => n + 1)
    totp.mutate(
      { challenge, code },
      {
        // Challenge habis (5 menit atau 5 kode salah): kembali ke kata sandi.
        onError: (err) => {
          if ('code' in err && err.code === 'auth.challenge_expired') {
            setChallenge(null)
            setPassword('')
          }
        },
      },
    )
  }

  return (
    <AuthFrame>
      {challenge ? (
        <form onSubmit={submitCode} noValidate className="flex flex-col gap-5 rounded-[14px] border border-line bg-surface p-6 shadow-sm">
          <div>
            <h1 className="text-xl font-bold tracking-tight">Kode autentikator</h1>
            <p className="mt-1 text-sm text-muted">Buka aplikasi autentikator di ponsel Anda, lalu masukkan 6 digit kode untuk Lajur.</p>
          </div>
          <Field
            id="kode"
            label="Kode 6 digit"
            inputMode="numeric"
            autoComplete="one-time-code"
            autoFocus
            maxLength={7}
            className="num tracking-[0.25em]"
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
          {totp.isError && <ErrorLine attempt={attempt}>{totp.error.message}</ErrorLine>}
          <Button type="submit" busy={totp.isPending} disabled={code.replace(/\D/g, '').length !== 6}>
            Masuk
          </Button>
          <button type="button" onClick={() => setChallenge(null)} className="cursor-pointer text-sm text-muted hover:text-ink">
            Kembali ke kata sandi
          </button>
        </form>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-5 rounded-[14px] border border-line bg-surface p-6 shadow-sm">
          <div>
            <h1 className="text-xl font-bold tracking-tight">Masuk</h1>
            <p className="mt-1 text-sm text-muted">Gunakan akun yang dibuat Admin perusahaan Anda.</p>
          </div>
          <Field id="email" label="Email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
          <Field id="password" label="Kata sandi" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          {login.isError && <ErrorLine attempt={attempt}>{login.error.message}</ErrorLine>}
          <Button type="submit" busy={login.isPending}>
            Masuk
          </Button>
        </form>
      )}
    </AuthFrame>
  )
}

/** Kerangka halaman sebelum masuk: login, setup wizard, dan pemasangan 2FA wajib. */
export function AuthFrame({ children, wide }: { children: ReactNode; wide?: boolean }) {
  return (
    <main className="flex min-h-screen items-center justify-center px-4 py-10">
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.25, ease: [0.2, 0, 0, 1] }}
        className={`w-full ${wide ? 'max-w-xl' : 'max-w-sm'}`}
      >
        <div className="mb-8 flex items-center gap-2.5">
          <Logo />
          <span className="text-xl font-bold tracking-tight">Lajur</span>
        </div>
        {children}
      </motion.div>
    </main>
  )
}

function ErrorLine({ attempt, children }: { attempt: number; children: ReactNode }) {
  return (
    <motion.p
      key={attempt}
      role="alert"
      initial={{ opacity: 0, x: 0 }}
      animate={{ opacity: 1, x: [0, -6, 6, -4, 4, 0] }}
      transition={{ duration: 0.2 }}
      className="rounded-[10px] bg-bad-soft px-3 py-2.5 text-sm text-bad"
    >
      {children}
    </motion.p>
  )
}

import { useState, type FormEvent } from 'react'
import { motion } from 'motion/react'
import { Button, Field } from '../components/ui'
import { Logo } from '../components/Logo'
import { useLogin } from '../lib/queries'

export function Login() {
  const login = useLogin()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [attempt, setAttempt] = useState(0)

  function submit(e: FormEvent) {
    e.preventDefault()
    setAttempt((n) => n + 1)
    login.mutate({ email, password })
  }

  return (
    <main className="flex min-h-screen items-center justify-center px-4 py-10">
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.25, ease: [0.2, 0, 0, 1] }}
        className="w-full max-w-sm"
      >
        <div className="mb-8 flex items-center gap-2.5">
          <Logo />
          <span className="text-xl font-bold tracking-tight">Lajur</span>
        </div>
        <form onSubmit={submit} className="flex flex-col gap-5 rounded-[14px] border border-line bg-surface p-6 shadow-sm">
          <div>
            <h1 className="text-xl font-bold tracking-tight">Masuk</h1>
            <p className="mt-1 text-sm text-muted">Gunakan akun yang dibuat Admin perusahaan Anda.</p>
          </div>
          <Field id="email" label="Email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
          <Field id="password" label="Kata sandi" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          {login.isError && (
            <motion.p
              key={attempt}
              role="alert"
              initial={{ opacity: 0, x: 0 }}
              animate={{ opacity: 1, x: [0, -6, 6, -4, 4, 0] }}
              transition={{ duration: 0.2 }}
              className="rounded-[10px] bg-bad-soft px-3 py-2.5 text-sm text-bad"
            >
              {login.error.message}
            </motion.p>
          )}
          <Button type="submit" busy={login.isPending}>
            Masuk
          </Button>
        </form>
      </motion.div>
    </main>
  )
}

import { useState, type FormEvent, type ReactNode } from 'react'
import { motion, useReducedMotion } from 'motion/react'
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
        <form onSubmit={submitCode} noValidate className="flex flex-col gap-5">
          <FormHeading title="Kode autentikator" body="Buka aplikasi autentikator di ponsel Anda, lalu masukkan 6 digit kode untuk Lajur." />
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
          <Button type="submit" busy={totp.isPending} disabled={code.replace(/\D/g, '').length !== 6} className="min-h-12">
            Masuk
          </Button>
          <button type="button" onClick={() => setChallenge(null)} className="cursor-pointer text-sm text-muted hover:text-ink">
            Kembali ke kata sandi
          </button>
        </form>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-5">
          <FormHeading title="Selamat datang kembali" body="Masuk dengan akun yang dibuat Admin perusahaan Anda." />
          <Field id="email" label="Email" type="email" autoComplete="username" required placeholder="nama@perusahaan.co.id" value={email} onChange={(e) => setEmail(e.target.value)} />
          <Field id="password" label="Kata sandi" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          {login.isError && <ErrorLine attempt={attempt}>{login.error.message}</ErrorLine>}
          <Button type="submit" busy={login.isPending} className="min-h-12">
            Masuk
            {!login.isPending && (
              <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M5 12h14M13 6l6 6-6 6" />
              </svg>
            )}
          </Button>
        </form>
      )}
    </AuthFrame>
  )
}

function FormHeading({ title, body }: { title: string; body: string }) {
  return (
    <div className="mb-1">
      <h1 className="text-[26px] leading-tight font-bold tracking-[-0.02em]">{title}</h1>
      <p className="mt-2 text-sm text-muted">{body}</p>
    </div>
  )
}

/** Kerangka halaman sebelum masuk: login, setup wizard, dan pemasangan 2FA wajib. Layar lebar
 *  menampilkan panel merek dengan ilustrasi produk di kiri; layar sempit hanya formulirnya. */
export function AuthFrame({ children, wide }: { children: ReactNode; wide?: boolean }) {
  return (
    <main className="flex min-h-screen bg-canvas">
      <BrandPanel />
      <div className="flex min-w-0 flex-1 items-center justify-center px-4 py-10 sm:px-10">
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.45, ease: [0.2, 0, 0, 1] }}
          className={`w-full ${wide ? 'max-w-xl' : 'max-w-[400px]'}`}
        >
          <div className="mb-10 flex items-center gap-2.5 lg:hidden">
            <Logo size={34} />
            <span className="text-xl font-bold tracking-tight">Lajur</span>
          </div>
          {children}
          <p className="mt-10 flex items-center gap-2 text-xs text-muted">
            <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 3l8 4v5c0 4.5-3.4 8.3-8 9-4.6-.7-8-4.5-8-9V7zM9 12l2 2 4-4" />
            </svg>
            Data transaksi tersimpan di server perusahaan Anda sendiri.
          </p>
        </motion.div>
      </div>
    </main>
  )
}

const features = [
  { title: 'Multi-PT dan konsolidasi', body: 'Semua PT dalam grup di satu aplikasi.', icon: 'M3 21h18M5 21V8l4-3v16M9 21V5l6 4v12M15 21V11l4-2v12' },
  { title: 'Siap Coretax', body: 'Ekspor XML faktur pajak dan bukti potong.', icon: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M9 15l2 2 4-4' },
  { title: 'Di server Anda', body: 'Berjalan on-premise di jaringan kantor.', icon: 'M2 7h20v5H2zM2 12h20v5H2zM6 9.5h.01M6 14.5h.01' },
]

function BrandPanel() {
  return (
    <aside className="night-grid relative hidden w-[52%] max-w-[780px] shrink-0 flex-col justify-between gap-10 overflow-hidden px-12 py-11 text-night-ink lg:flex xl:px-16">
      <div className="flex items-center gap-2.5">
        <Logo size={34} />
        <span className="text-xl font-bold tracking-tight text-white">Lajur</span>
      </div>
      <div className="flex flex-col gap-10">
        <motion.div initial={{ opacity: 0, y: 14 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.5, ease: [0.2, 0, 0, 1], delay: 0.05 }}>
          <p className="max-w-[520px] text-[38px] leading-[1.12] font-bold tracking-[-0.025em] text-white xl:text-[44px]">
            Pembukuan multi-PT yang rapi, dari faktur sampai laporan pajak.
          </p>
          <p className="mt-4 max-w-[480px] text-[15px] leading-relaxed text-night-muted">
            Penjualan, pembelian, kas, persediaan, aset, dan pajak tercatat di satu buku besar, langsung jadi neraca dan laba rugi.
          </p>
        </motion.div>
        <HeroArt />
      </div>
      <ul className="grid grid-cols-3 gap-6">
        {features.map((f, i) => (
          <motion.li
            key={f.title}
            initial={{ opacity: 0, y: 10 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.4, ease: [0.2, 0, 0, 1], delay: 0.7 + i * 0.08 }}
            className="flex flex-col gap-2"
          >
            <span className="flex size-9 items-center justify-center rounded-lg border border-night-line bg-white/5 text-sky">
              <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <path d={f.icon} />
              </svg>
            </span>
            <span className="text-sm font-semibold text-white">{f.title}</span>
            <span className="text-[13px] leading-snug text-night-muted">{f.body}</span>
          </motion.li>
        ))}
      </ul>
    </aside>
  )
}

const bars = [
  { rev: 52, exp: 38 },
  { rev: 61, exp: 44 },
  { rev: 57, exp: 41 },
  { rev: 72, exp: 47 },
  { rev: 80, exp: 52 },
  { rev: 94, exp: 58 },
]

/** Ilustrasi produk: kartu laba rugi dengan grafik yang tumbuh, faktur lunas, dan ekspor
 *  Coretax yang mengambang. Hanya dekorasi; disembunyikan dari pembaca layar. */
function HeroArt() {
  const reduce = useReducedMotion()
  const pop = (delay: number) => ({
    initial: { opacity: 0, y: 18, scale: 0.97 },
    animate: { opacity: 1, y: 0, scale: 1 },
    transition: { duration: 0.55, ease: [0.2, 0, 0, 1] as const, delay },
  })
  // Garis laba melewati puncak selisih pendapatan dan beban tiap bulan.
  const w = 300
  const h = 120
  const step = w / bars.length
  const points = bars.map((b, i) => [step * i + step / 2, h - (b.rev - b.exp) * 2.6 - 6] as const)
  const line = points.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(1)} ${y.toFixed(1)}`).join(' ')
  return (
    <div aria-hidden="true" className="relative h-[350px] w-full max-w-[560px] select-none">
      <motion.div {...pop(0.2)} className="absolute top-[78px] left-0 w-[360px] rounded-2xl border border-line bg-surface p-5 text-ink shadow-2xl">
        <div className="flex items-center justify-between">
          <span className="text-[13px] font-medium text-muted">Laba rugi</span>
          <span className="rounded-full bg-accent-soft px-2.5 py-1 text-[11px] font-semibold text-accent">6 bulan</span>
        </div>
        <div className="mt-1.5 flex items-baseline gap-2.5">
          <span className="num text-[26px] font-bold tracking-tight">Rp 486,2 jt</span>
          <span className="rounded-full bg-good-soft px-2 py-0.5 text-[11.5px] font-semibold text-good">+12,4%</span>
        </div>
        <svg viewBox={`0 0 ${w} ${h + 18}`} className="mt-3 w-full overflow-visible">
          {[0.25, 0.5, 0.75].map((f) => (
            <line key={f} x1="0" x2={w} y1={h * f} y2={h * f} stroke="var(--color-line)" strokeDasharray="3 4" />
          ))}
          {bars.map((b, i) => {
            const x = step * i + step / 2
            return (
              <g key={i}>
                <motion.rect
                  x={x - 15}
                  width="13"
                  rx="3.5"
                  fill="var(--color-accent)"
                  initial={reduce ? false : { height: 0, y: h }}
                  animate={{ height: b.rev, y: h - b.rev }}
                  transition={{ duration: 0.6, ease: [0.2, 0, 0, 1], delay: 0.45 + i * 0.07 }}
                />
                <motion.rect
                  x={x + 2}
                  width="13"
                  rx="3.5"
                  fill="var(--color-slate)"
                  initial={reduce ? false : { height: 0, y: h }}
                  animate={{ height: b.exp, y: h - b.exp }}
                  transition={{ duration: 0.6, ease: [0.2, 0, 0, 1], delay: 0.5 + i * 0.07 }}
                />
                <text x={x} y={h + 15} textAnchor="middle" fontSize="10" fill="var(--color-muted)">
                  {['Mei', 'Jun', 'Jul', 'Agu', 'Sep', 'Okt'][i]}
                </text>
              </g>
            )
          })}
          <motion.path
            d={line}
            fill="none"
            stroke="var(--color-teal)"
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeLinejoin="round"
            initial={reduce ? false : { pathLength: 0 }}
            animate={{ pathLength: 1 }}
            transition={{ duration: 1.1, ease: 'easeInOut', delay: 1 }}
          />
          <motion.circle
            cx={points[points.length - 1][0]}
            cy={points[points.length - 1][1]}
            r="4.5"
            fill="var(--color-surface)"
            stroke="var(--color-teal)"
            strokeWidth="2.5"
            initial={reduce ? false : { scale: 0 }}
            animate={{ scale: 1 }}
            transition={{ duration: 0.3, delay: 2 }}
          />
        </svg>
      </motion.div>

      <motion.div {...pop(0.55)} className="absolute top-0 right-0 w-[230px]">
        <div className="float-slow rounded-xl border border-line bg-surface p-3.5 text-ink shadow-xl">
          <div className="flex items-center gap-2.5">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-good-soft text-good">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M5 12l5 5L20 7" />
              </svg>
            </span>
            <span className="min-w-0">
              <span className="num block text-[13px] font-semibold">FJ/2026/10/0012</span>
              <span className="block truncate text-[12px] text-muted">Pelunasan diterima</span>
            </span>
          </div>
          <div className="mt-3 flex items-center justify-between border-t border-line pt-2.5">
            <span className="num text-sm font-semibold">Rp 11.100.000</span>
            <span className="rounded-full bg-good-soft px-2 py-0.5 text-[11px] font-semibold text-good">Lunas</span>
          </div>
        </div>
      </motion.div>

      <motion.div {...pop(0.8)} className="absolute right-4 bottom-2 w-[250px]">
        <div className="float-slower rounded-xl border border-line bg-surface p-3.5 text-ink shadow-xl">
          <div className="flex items-center gap-2.5">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-accent-soft text-accent">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M12 18v-6M9 15l3 3 3-3" />
              </svg>
            </span>
            <span className="min-w-0">
              <span className="block text-[13px] font-semibold">XML Coretax siap</span>
              <span className="block text-[12px] text-muted">24 faktur keluaran · Oktober</span>
            </span>
          </div>
          <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-hover">
            <motion.div
              className="h-full rounded-full bg-accent"
              initial={reduce ? false : { width: '0%' }}
              animate={{ width: '100%' }}
              transition={{ duration: 1.4, ease: [0.2, 0, 0, 1], delay: 1.2 }}
            />
          </div>
        </div>
      </motion.div>

      <motion.div {...pop(1.05)} className="absolute top-[150px] right-2 hidden xl:block">
        <div className="float-slow flex items-center gap-2 rounded-full border border-night-line bg-night-2 px-3.5 py-2 text-[12.5px] font-semibold text-white shadow-lg">
          <span className="flex size-5 items-center justify-center rounded-full bg-teal text-night">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
              <path d="M5 12l5 5L20 7" />
            </svg>
          </span>
          Neraca seimbang
        </div>
      </motion.div>
    </div>
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

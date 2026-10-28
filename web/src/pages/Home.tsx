import { motion } from 'motion/react'
import { Link } from 'react-router'
import type { Me } from '../lib/api'
import { activeCompany, can } from '../lib/session'
import { allowedBranches, isRestricted, useDimensions } from '../lib/dimensions'
import { Dashboard } from '../components/Dashboard'
import { Card, ease } from '../components/ui'

// Ringkasan izin dalam bahasa sehari-hari, mengikuti matriks enam persona di TDD.
const abilities: [string, string][] = [
  ['ledger.journal.create', 'Membuat transaksi dan jurnal'],
  ['ledger.journal.edit', 'Mengubah jurnal sebelum periode dikunci'],
  ['ledger.account.create', 'Menambah akun di bagan akun'],
  ['ledger.account.edit', 'Mengubah nama dan menonaktifkan akun'],
  ['ledger.adjustment.post', 'Menginput dan memposting jurnal penyesuaian'],
  ['ledger.journal.void', 'Membatalkan (void) transaksi'],
  ['ledger.period.manage', 'Mengunci dan membuka periode'],
  ['approval.manage', 'Menyetujui transaksi'],
  ['panjar.create', 'Mengajukan panjar unit'],
  ['panjar.approve', 'Menyetujui panjar unit'],
  ['tax.export', 'Mengekspor XML Coretax'],
  ['platform.audit.view', 'Melihat audit trail'],
  ['data.export', 'Mengekspor data'],
  ['platform.user.manage', 'Mengelola pengguna dan persona'],
  ['platform.system.manage', 'Mengelola backup, pembaruan, dan lisensi'],
]

// Pintasan pekerjaan harian; hanya yang diizinkan peran pengguna di PT aktif yang tampil.
const shortcuts: { perm: string; to: string; label: string; hint: string; icon: string }[] = [
  { perm: 'sales.create', to: '/penjualan/baru', label: 'Faktur penjualan', hint: 'Tagih pelanggan', icon: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M8 13h8M8 17h5' },
  { perm: 'sales.create', to: '/penjualan/pelunasan/baru', label: 'Terima pelunasan', hint: 'Pembayaran pelanggan', icon: 'M12 2v14M6 10l6 6 6-6M4 22h16' },
  { perm: 'purchase.create', to: '/pembelian/baru', label: 'Tagihan pembelian', hint: 'Catat tagihan pemasok', icon: 'M6 2L3 6v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6l-3-4zM3 6h18M16 10a4 4 0 0 1-8 0' },
  { perm: 'purchase.create', to: '/pembelian/pembayaran/baru', label: 'Bayar pemasok', hint: 'Pelunasan tagihan', icon: 'M12 22V8M6 14l6-6 6 6M4 2h16' },
  { perm: 'cash.create', to: '/kas', label: 'Kas dan bank', hint: 'Masuk, keluar, transfer', icon: 'M2 7h20v12H2zM2 11h20M6 15h4' },
  { perm: 'panjar.create', to: '/panjar', label: 'Panjar unit', hint: 'Ajukan kebutuhan unit', icon: 'M9 2h6a1 1 0 0 1 1 1v2H8V3a1 1 0 0 1 1-1zM8 4H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2h-2M9 14l2 2 4-4' },
  { perm: 'ledger.journal.create', to: '/jurnal/baru', label: 'Jurnal umum', hint: 'Transaksi di luar modul', icon: 'M2 4h6a4 4 0 0 1 4 4v12a3 3 0 0 0-3-3H2zM22 4h-6a4 4 0 0 0-4 4v12a3 3 0 0 1 3-3h7z' },
  { perm: 'report.view', to: '/laporan', label: 'Laporan keuangan', hint: 'Neraca, laba rugi, arus kas', icon: 'M3 3v18h18M7 15l4-4 3 3 6-6M15 8h5v5' },
  { perm: 'tax.view', to: '/pajak', label: 'Pajak', hint: 'Faktur pajak dan Coretax', icon: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M9 15l6-6M9.5 9.5h.01M14.5 14.5h.01' },
]

function greeting(hour: number) {
  if (hour >= 4 && hour < 11) return 'Selamat pagi'
  if (hour >= 11 && hour < 15) return 'Selamat siang'
  if (hour >= 15 && hour < 19) return 'Selamat sore'
  return 'Selamat malam'
}

export function Home({ me }: { me: Me }) {
  const company = activeCompany(me)
  const branches = useDimensions(me.activeCompanyId, 'cabang').data ?? []
  const myBranches = isRestricted(me) ? allowedBranches(me, branches) : []
  const perms = new Set(company?.permissions ?? [])
  const firstName = me.user.name.split(' ')[0]
  const now = new Date()
  const today = now.toLocaleDateString('id-ID', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' })
  const quick = shortcuts.filter((s) => perms.has(s.perm))
  const allowed = abilities.filter(([perm]) => perms.has(perm))

  return (
    <div className="flex flex-col gap-6">
      <motion.section
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.45, ease }}
        className="night-grid relative overflow-hidden rounded-3xl px-6 py-7 text-night-ink sm:px-8 sm:py-8"
      >
        <div className="relative z-10 flex max-w-[620px] flex-col gap-4">
          <span className="text-[13px] font-medium text-night-muted">{today}</span>
          <div>
            <h1 className="text-[30px] leading-tight font-bold tracking-[-0.02em] text-white sm:text-[34px]">
              {greeting(now.getHours())}, {firstName}
            </h1>
            <p className="mt-2 text-[15px] text-night-muted">
              {company ? (
                <>
                  Anda masuk sebagai <span className="font-semibold text-white">{company.roleName}</span> di{' '}
                  <span className="font-semibold text-white">{company.name}</span>.
                </>
              ) : (
                'Akun Anda belum diberi akses ke PT mana pun. Hubungi Admin perusahaan.'
              )}
              {myBranches.length > 0 && ` Akses Anda dibatasi ke cabang ${myBranches.map((b) => b.name).join(', ')}.`}
            </p>
          </div>
          <div className="flex flex-wrap gap-2.5 pt-1">
            {quick.slice(0, 2).map((s, i) => (
              <Link
                key={s.to}
                to={s.to}
                className={`flex min-h-11 items-center gap-2 rounded-xl px-4 text-sm font-semibold transition-all duration-150 hover:-translate-y-px ${
                  i === 0 ? 'bg-white text-night shadow-lg hover:shadow-xl' : 'border border-white/15 bg-white/8 text-white hover:bg-white/14'
                }`}
              >
                <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
                  <path d={s.icon} />
                </svg>
                {s.label}
              </Link>
            ))}
            <Link
              to="/panduan"
              className="flex min-h-11 items-center gap-2 rounded-xl border border-white/15 bg-white/8 px-4 text-sm font-semibold text-white transition-all duration-150 hover:-translate-y-px hover:bg-white/14"
            >
              Panduan penggunaan
            </Link>
          </div>
        </div>
        <HeroDecor />
      </motion.section>

      {can(me, 'report.view') && !isRestricted(me) && <Dashboard me={me} />}

      <div className="flex flex-wrap gap-5">
        {quick.length > 0 && (
          <motion.div className="min-w-0 flex-[2_1_440px]" initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.4, ease, delay: 0.3 }}>
            <Card title="Pintasan" subtitle="Pekerjaan yang paling sering dilakukan di PT ini." className="h-full">
              <ul className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-3">
                {quick.map((s) => (
                  <li key={s.to}>
                    <Link
                      to={s.to}
                      className="group flex h-full items-center gap-3 rounded-xl border border-line p-3 transition-all duration-200 ease-lajur hover:-translate-y-0.5 hover:border-accent/30 hover:bg-accent-soft/40 hover:shadow-card"
                    >
                      <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-accent-soft text-accent transition-colors duration-200 group-hover:bg-accent group-hover:text-on-accent">
                        <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                          <path d={s.icon} />
                        </svg>
                      </span>
                      <span className="min-w-0">
                        <span className="block text-sm font-semibold">{s.label}</span>
                        <span className="block text-[12.5px] leading-snug text-muted">{s.hint}</span>
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            </Card>
          </motion.div>
        )}
        <motion.div className="min-w-0 flex-[1_1_300px]" initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.4, ease, delay: 0.36 }}>
          <Card title="Akses Anda di PT ini" subtitle={`${allowed.length} dari ${abilities.length} izin; ditentukan oleh peran Anda dan bisa berbeda di setiap PT.`} className="h-full">
            <ul className="flex flex-col gap-0.5">
              {abilities.map(([perm, label]) => {
                const yes = perms.has(perm)
                return (
                  <li key={perm} className="flex items-center gap-2.5 py-1 text-[13.5px]">
                    <span className={`flex size-[18px] shrink-0 items-center justify-center rounded-full ${yes ? 'bg-good-soft text-good' : 'bg-hover text-muted'}`}>
                      <svg width="11" height="11" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                        <path d={yes ? 'M5 12l5 5L20 7' : 'M7 7l10 10M17 7L7 17'} />
                      </svg>
                    </span>
                    <span className={yes ? 'text-ink' : 'text-muted'}>
                      {label}
                      {!yes && <span className="sr-only"> (tidak diizinkan)</span>}
                    </span>
                  </li>
                )
              })}
            </ul>
          </Card>
        </motion.div>
      </div>
    </div>
  )
}

/** Hiasan banner Beranda: kartu buku besar dan grafik kecil yang mengambang. */
function HeroDecor() {
  return (
    <div aria-hidden="true" className="pointer-events-none absolute top-1/2 right-6 hidden -translate-y-1/2 lg:block xl:right-12">
      <div className="relative h-[180px] w-[300px]">
        <div className="float-slower absolute top-2 right-0 w-[210px] rounded-2xl border border-white/12 bg-white/6 p-4 backdrop-blur-sm">
          <div className="mb-3 flex items-center justify-between">
            <span className="h-2 w-16 rounded-full bg-white/30" />
            <span className="h-2 w-8 rounded-full bg-teal/70" />
          </div>
          <div className="flex h-16 items-end gap-2">
            {[38, 52, 46, 64, 58, 80].map((h, i) => (
              <motion.span
                key={i}
                className="flex-1 rounded-t-[4px] bg-gradient-to-t from-accent to-sky"
                initial={{ height: 0 }}
                animate={{ height: `${h}%` }}
                transition={{ duration: 0.7, ease, delay: 0.4 + i * 0.07 }}
              />
            ))}
          </div>
        </div>
        <div className="float-slow absolute bottom-0 left-0 w-[180px] rounded-2xl border border-white/12 bg-night-2/90 p-4 shadow-2xl">
          <div className="flex items-center gap-2.5">
            <span className="flex size-8 items-center justify-center rounded-full bg-teal text-night">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round">
                <path d="M5 12l5 5L20 7" />
              </svg>
            </span>
            <span className="flex flex-col gap-1.5">
              <span className="h-2 w-20 rounded-full bg-white/40" />
              <span className="h-2 w-12 rounded-full bg-white/20" />
            </span>
          </div>
          <div className="mt-3 flex flex-col gap-1.5">
            <span className="h-1.5 w-full rounded-full bg-white/12" />
            <span className="h-1.5 w-4/5 rounded-full bg-white/12" />
          </div>
        </div>
      </div>
    </div>
  )
}

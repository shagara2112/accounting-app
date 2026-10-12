import { AnimatePresence, motion } from 'motion/react'
import { NavLink, useLocation, useOutlet } from 'react-router'
import type { LicenseStatus, Me } from '../lib/api'
import { formatDate } from '../lib/dates'
import { useLicense, useLogout, useSwitchCompany } from '../lib/queries'
import { activeCompany, can } from '../lib/session'
import { Badge, Button, ease } from './ui'
import { Logo } from './Logo'

// Modul yang dijadwalkan di rencana implementasi TDD; ditampilkan agar arah produk terlihat.
const upcoming = ['Penjualan', 'Pembelian', 'Kas dan bank', 'Pajak', 'Proyek', 'Persediaan', 'Laporan', 'Konsolidasi']

const icons = {
  home: 'M3 3h7v9H3zM14 3h7v5h-7zM14 12h7v9h-7zM3 16h7v5H3z',
  journal: 'M2 4h6a4 4 0 0 1 4 4v12a3 3 0 0 0-3-3H2zM22 4h-6a4 4 0 0 0-4 4v12a3 3 0 0 1 3-3h7z',
  ledger: 'M4 19.5A2.5 2.5 0 0 1 6.5 17H20V3H6.5A2.5 2.5 0 0 0 4 5.5zM4 19.5A2.5 2.5 0 0 0 6.5 22H20v-5M8 7h8M8 11h6',
  worksheet: 'M3 3h18v18H3zM3 9h18M9 3v18M15 3v18',
  accounts: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
  periods: 'M8 2v4M16 2v4M3 8h18M5 4h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2zM9 15l2 2 4-4',
  audit: 'M12 3l8 4v5c0 4.5-3.4 8.3-8 9-4.6-.7-8-4.5-8-9V7zM9 12l2 2 4-4',
}

export function Shell({ me }: { me: Me }) {
  const location = useLocation()
  const outlet = useOutlet()
  const company = activeCompany(me)
  const canAudit = can(me, 'platform.audit.view')
  const canLedger = can(me, 'ledger.view')
  const logout = useLogout()

  return (
    <div className="flex min-h-screen flex-wrap">
      <nav aria-label="Menu utama" className="flex max-w-full flex-[1_1_232px] flex-col gap-1 border-r border-line bg-surface px-3.5 py-5">
        <div className="flex items-center gap-2.5 px-2.5 pb-5">
          <Logo />
          <span className="text-lg font-bold tracking-tight">Lajur</span>
        </div>
        <NavItem to="/" label="Beranda" icon={icons.home} end />
        {canLedger && (
          <>
            <NavHeading>Akuntansi</NavHeading>
            <NavItem to="/jurnal" label="Jurnal" icon={icons.journal} />
            <NavItem to="/buku-besar" label="Buku besar" icon={icons.ledger} />
            <NavItem to="/neraca-lajur" label="Neraca lajur" icon={icons.worksheet} />
            <NavItem to="/akun" label="Bagan akun" icon={icons.accounts} />
            <NavItem to="/periode" label="Periode" icon={icons.periods} />
          </>
        )}
        {canAudit && (
          <>
            <NavHeading>Kontrol</NavHeading>
            <NavItem to="/audit" label="Audit trail" icon={icons.audit} />
          </>
        )}
        <NavHeading>Segera hadir</NavHeading>
        {upcoming.map((m) => (
          <span key={m} className="flex min-h-9 items-center px-3 text-sm text-muted">
            {m}
          </span>
        ))}
        <div className="mt-auto flex flex-col gap-2 border-t border-line px-2.5 pt-4">
          <div className="flex flex-col">
            <span className="text-sm font-semibold">{me.user.name}</span>
            <span className="text-xs text-muted">{company ? `${company.roleName} · ${company.shortName}` : 'Belum punya akses PT'}</span>
          </div>
          <Button variant="secondary" busy={logout.isPending} onClick={() => logout.mutate()}>
            Keluar
          </Button>
        </div>
      </nav>

      <div className="flex min-w-0 flex-[999_1_560px] flex-col">
        <header className="flex flex-wrap items-center justify-between gap-4 border-b border-line bg-surface px-8 py-4">
          <div className="flex min-w-0 flex-col">
            <span className="text-xs font-medium text-muted">PT aktif</span>
            <div className="relative h-7 overflow-hidden text-lg font-semibold">
              <AnimatePresence mode="wait" initial={false}>
                <motion.span
                  key={company?.id ?? 'none'}
                  className="block"
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.2, ease }}
                >
                  {company?.name ?? '—'}
                </motion.span>
              </AnimatePresence>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <LicenseChip />
            <CompanySwitcher me={me} />
          </div>
        </header>
        <main className="w-full max-w-[1240px] px-8 pt-7 pb-14">
          {/* Halaman baru langsung tampil lalu memudar masuk; tidak menunggu animasi keluar
              halaman lama, supaya perpindahan tidak pernah menahan pengguna (TDD). */}
          <motion.div key={location.pathname} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.2, ease }}>
            {outlet}
          </motion.div>
        </main>
      </div>
    </div>
  )
}

function NavHeading({ children }: { children: string }) {
  return <p className="mt-4 mb-0.5 px-3 text-xs font-semibold tracking-wide text-muted uppercase">{children}</p>
}

function NavItem({ to, label, icon, end }: { to: string; label: string; icon: string; end?: boolean }) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        `flex min-h-11 items-center gap-2.5 rounded-[10px] px-3 text-sm transition-colors duration-150 ${
          isActive ? 'bg-accent-soft font-semibold text-accent' : 'font-medium text-ink-2 hover:bg-hover'
        }`
      }
    >
      <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <path d={icon} />
      </svg>
      {label}
    </NavLink>
  )
}

function CompanySwitcher({ me }: { me: Me }) {
  const switchCompany = useSwitchCompany()
  if (me.companies.length < 2) return null
  return (
    <div className="flex items-center gap-2">
      <label htmlFor="pt-aktif" className="text-[13px] text-muted">
        Ganti PT
      </label>
      <select
        id="pt-aktif"
        value={me.activeCompanyId ?? ''}
        disabled={switchCompany.isPending}
        onChange={(e) => switchCompany.mutate(e.target.value)}
        className="min-h-11 cursor-pointer rounded-[10px] border border-line-2 bg-surface px-3 text-sm text-ink transition-colors hover:border-muted"
      >
        {me.companies.map((c) => (
          <option key={c.id} value={c.id}>
            {c.name}
          </option>
        ))}
      </select>
    </div>
  )
}

function licenseView(s: LicenseStatus): { tone: 'good' | 'warn' | 'bad'; text: string } {
  switch (s.status) {
    case 'ACTIVE':
      return { tone: 'good', text: s.expiresAt ? `Lisensi aktif s.d. ${formatDate(s.expiresAt)}` : 'Lisensi aktif' }
    case 'WARNING':
      return { tone: 'warn', text: `Lisensi berakhir ${s.daysLeft ?? 0} hari lagi` }
    case 'UNVERIFIED':
      return { tone: 'warn', text: 'Lisensi belum terverifikasi' }
    case 'EXPIRED':
      return { tone: 'bad', text: 'Lisensi berakhir · hanya baca' }
    default:
      return { tone: 'bad', text: 'Lisensi dikunci' }
  }
}

function LicenseChip() {
  const license = useLicense()
  if (!license.data) return null
  const v = licenseView(license.data)
  return (
    <span title={license.data.message}>
      <Badge tone={v.tone}>
        <span className="size-1.5 rounded-full bg-current" />
        {v.text}
      </Badge>
    </span>
  )
}

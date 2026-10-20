import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useState } from 'react'
import { Link, NavLink, useLocation, useNavigationType, useOutlet } from 'react-router'
import type { LicenseStatus, Me } from '../lib/api'
import { formatDate } from '../lib/dates'
import { topicForPath, visibleTopics } from '../lib/guide'
import { useLicense, useLogout, useSwitchCompany } from '../lib/queries'
import { useBackupStatus } from '../lib/system'
import { activeCompany, can } from '../lib/session'
import { isRestricted } from '../lib/dimensions'
import { Badge, Button, ease } from './ui'
import { Logo } from './Logo'

const icons = {
  home: 'M3 3h7v9H3zM14 3h7v5h-7zM14 12h7v9h-7zM3 16h7v5H3z',
  guide: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM9.1 9a3 3 0 0 1 5.8 1c0 2-3 3-3 3M12 17h.01',
  journal: 'M2 4h6a4 4 0 0 1 4 4v12a3 3 0 0 0-3-3H2zM22 4h-6a4 4 0 0 0-4 4v12a3 3 0 0 1 3-3h7z',
  ledger: 'M4 19.5A2.5 2.5 0 0 1 6.5 17H20V3H6.5A2.5 2.5 0 0 0 4 5.5zM4 19.5A2.5 2.5 0 0 0 6.5 22H20v-5M8 7h8M8 11h6',
  worksheet: 'M3 3h18v18H3zM3 9h18M9 3v18M15 3v18',
  accounts: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
  opening: 'M4 22V4M4 4h13l-2.5 4L17 12H4',
  periods: 'M8 2v4M16 2v4M3 8h18M5 4h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2zM9 15l2 2 4-4',
  audit: 'M12 3l8 4v5c0 4.5-3.4 8.3-8 9-4.6-.7-8-4.5-8-9V7zM9 12l2 2 4-4',
  sales: 'M3 17l6-6 4 4 8-8M14 7h7v7',
  purchase: 'M6 2L3 6v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6l-3-4zM3 6h18M16 10a4 4 0 0 1-8 0',
  cash: 'M2 7h20v12H2zM2 11h20M6 15h4',
  contacts: 'M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM23 21v-2a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8',
  tax: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M9 15l6-6M9.5 9.5h.01M14.5 14.5h.01',
  project: 'M2 20h20M5 20V9l7-5 7 5v11M9 20v-6h6v6',
  inventory: 'M21 8l-9-5-9 5v8l9 5 9-5zM3 8l9 5 9-5M12 13v8',
  products: 'M20.6 13.4l-7.2 7.2a2 2 0 0 1-2.8 0L3 13V3h10l7.6 7.6a2 2 0 0 1 0 2.8zM7.5 7.5h.01',
  assets: 'M3 21h18M5 21V8l7-5 7 5v13M9 21v-6h6v6M9 10h.01M15 10h.01',
  reports: 'M3 3v18h18M7 15l4-4 3 3 6-6M15 8h5v5',
  consol: 'M3 21h18M5 21V8l4-3v16M9 21V5l6 4v12M15 21V11l4-2v12',
  imports: 'M12 3v12M7 10l5 5 5-5M5 21h14',
  branches: 'M6 3v12M18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM18 9a9 9 0 0 1-9 9',
  users: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM19 8v6M22 11h-6',
  companies: 'M3 21h18M6 21V4h8v17M14 9h4v12M9 8h2M9 12h2M9 16h2',
  system: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z',
}

export function Shell({ me }: { me: Me }) {
  const location = useLocation()
  const outlet = useOutlet()
  const company = activeCompany(me)
  const canAudit = can(me, 'platform.audit.view')
  // Pengguna yang dibatasi ke cabang tertentu (PLT-12) tidak membuka tampilan seluruh PT.
  const restricted = isRestricted(me)
  const canLedger = can(me, 'ledger.view') && !restricted
  const trade = [
    can(me, 'sales.view') && { to: '/penjualan', label: 'Penjualan', icon: icons.sales },
    can(me, 'purchase.view') && { to: '/pembelian', label: 'Pembelian', icon: icons.purchase },
    can(me, 'cash.view') && { to: '/kas', label: 'Kas dan bank', icon: icons.cash },
    can(me, 'tax.view') && !restricted && { to: '/pajak', label: 'Pajak', icon: icons.tax },
    can(me, 'core.contact.view') && { to: '/kontak', label: 'Kontak', icon: icons.contacts },
  ].filter((x) => x !== false)
  const operations = [
    can(me, 'project.view') && { to: '/proyek', label: 'Proyek', icon: icons.project },
    can(me, 'inventory.view') && !restricted && { to: '/persediaan', label: 'Persediaan', icon: icons.inventory },
    can(me, 'inventory.view') && { to: '/produk', label: 'Produk dan jasa', icon: icons.products },
    can(me, 'asset.view') && !restricted && { to: '/aset', label: 'Aset tetap', icon: icons.assets },
  ].filter((x) => x !== false)
  const logout = useLogout()
  // Di layar sempit menu dilipat di bawah bar atas dan tertutup lagi setiap pindah halaman.
  const [menuOpen, setMenuOpen] = useState(false)
  const [path, setPath] = useState(location.pathname)
  if (path !== location.pathname) {
    setPath(location.pathname)
    setMenuOpen(false)
  }
  // Halaman baru mulai dari atas; tombol kembali browser memakai posisi gulir bawaannya.
  const navType = useNavigationType()
  useEffect(() => {
    if (navType !== 'POP') window.scrollTo(0, 0)
  }, [location.pathname, navType])

  return (
    <div className="flex min-h-screen flex-col lg:flex-row">
      <nav aria-label="Menu utama" className="flex flex-col border-b border-line bg-surface px-3.5 py-3 lg:w-[232px] lg:shrink-0 lg:border-r lg:border-b-0 lg:py-5 print:hidden">
        <div className="flex items-center gap-2.5 px-2.5 lg:pb-5">
          <Logo />
          <span className="text-lg font-bold tracking-tight">Lajur</span>
          <button
            type="button"
            aria-expanded={menuOpen}
            aria-controls="menu-utama"
            onClick={() => setMenuOpen((o) => !o)}
            className="ml-auto flex min-h-11 cursor-pointer items-center gap-2 rounded-[10px] border border-line-2 px-3 text-sm font-medium text-ink-2 transition-colors hover:bg-hover lg:hidden"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
              <path d={menuOpen ? 'M6 6l12 12M18 6L6 18' : 'M4 7h16M4 12h16M4 17h16'} />
            </svg>
            Menu
          </button>
        </div>
        <div id="menu-utama" className={`${menuOpen ? 'flex' : 'hidden'} flex-1 flex-col gap-1 pt-2 lg:flex lg:pt-0`}>
          <NavItem to="/" label="Beranda" icon={icons.home} end />
          <NavItem to="/panduan" label="Panduan" icon={icons.guide} />
          {trade.length > 0 && (
            <>
              <NavHeading>Transaksi</NavHeading>
              {trade.map((n) => (
                <NavItem key={n.to} to={n.to} label={n.label} icon={n.icon} />
              ))}
            </>
          )}
          {operations.length > 0 && (
            <>
              <NavHeading>Operasional</NavHeading>
              {operations.map((n) => (
                <NavItem key={n.to} to={n.to} label={n.label} icon={n.icon} />
              ))}
            </>
          )}
          {(can(me, 'report.view') || (can(me, 'consol.view') && !restricted)) && (
            <>
              <NavHeading>Laporan</NavHeading>
              {can(me, 'report.view') && <NavItem to="/laporan" label={restricted ? 'Laba rugi cabang' : 'Laporan keuangan'} icon={icons.reports} />}
              {can(me, 'consol.view') && !restricted && <NavItem to="/konsolidasi" label="Konsolidasi grup" icon={icons.consol} />}
            </>
          )}
          {canLedger && (
            <>
              <NavHeading>Akuntansi</NavHeading>
              <NavItem to="/jurnal" label="Jurnal" icon={icons.journal} />
              <NavItem to="/buku-besar" label="Buku besar" icon={icons.ledger} />
              <NavItem to="/neraca-lajur" label="Neraca lajur" icon={icons.worksheet} />
              <NavItem to="/akun" label="Bagan akun" icon={icons.accounts} />
              <NavItem to="/saldo-awal" label="Saldo awal" icon={icons.opening} />
              <NavItem to="/periode" label="Periode" icon={icons.periods} />
              <NavItem to="/cabang" label="Cabang dan penomoran" icon={icons.branches} />
              <NavItem to="/impor" label="Impor data" icon={icons.imports} />
            </>
          )}
          {canAudit && (
            <>
              <NavHeading>Kontrol</NavHeading>
              <NavItem to="/audit" label="Audit trail" icon={icons.audit} />
            </>
          )}
          {(can(me, 'platform.user.manage') || can(me, 'platform.system.manage')) && (
            <>
              <NavHeading>Admin</NavHeading>
              {can(me, 'platform.user.manage') && <NavItem to="/pengguna" label="Pengguna dan akses" icon={icons.users} />}
              {can(me, 'platform.system.manage') && <NavItem to="/pt" label="PT dalam grup" icon={icons.companies} />}
              {can(me, 'platform.system.manage') && <NavItem to="/sistem" label="Sistem dan lisensi" icon={icons.system} />}
            </>
          )}
          <div className="mt-auto flex flex-col gap-2 border-t border-line px-2.5 pt-4">
            <NavLink to="/profil" className="-mx-2 flex flex-col rounded-[10px] px-2 py-1 transition-colors duration-150 hover:bg-hover">
              <span className="text-sm font-semibold">{me.user.name}</span>
              <span className="text-xs text-muted">{company ? `${company.roleName} · ${company.shortName}` : 'Belum punya akses PT'}</span>
            </NavLink>
            <Button variant="secondary" busy={logout.isPending} onClick={() => logout.mutate()}>
              Keluar
            </Button>
          </div>
        </div>
      </nav>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex flex-wrap items-center justify-between gap-4 border-b border-line bg-surface px-4 py-4 sm:px-8 print:hidden">
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
            <HelpLink me={me} />
            <LicenseChip />
            <CompanySwitcher me={me} />
          </div>
        </header>
        <LicenseBanner canManage={can(me, 'platform.system.manage')} />
        {can(me, 'platform.system.manage') && <BackupBanner />}
        <main className="w-full max-w-[1240px] px-4 pt-7 pb-14 sm:px-8 print:max-w-none print:p-0">
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

/** Tombol Panduan di bar atas: membuka panduan untuk halaman yang sedang dibuka. */
function HelpLink({ me }: { me: Me }) {
  const location = useLocation()
  if (location.pathname === '/panduan') return null
  const topic = topicForPath(visibleTopics(me), location.pathname, location.search)
  return (
    <Link
      to={topic ? `/panduan?topik=${topic.id}` : '/panduan'}
      title={topic ? `Panduan: ${topic.title}` : 'Panduan'}
      className="flex min-h-11 items-center gap-2 rounded-[10px] border border-line-2 px-3 text-sm font-medium text-ink-2 transition-colors duration-150 hover:bg-hover"
    >
      <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <path d={icons.guide} />
      </svg>
      Panduan
    </Link>
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

/** Banner di atas halaman bila lisensi tidak aktif penuh (LIC-08): segera berakhir, berakhir,
 *  belum terverifikasi, ditangguhkan, key tidak valid, atau versi wajib diperbarui. */
function LicenseBanner({ canManage }: { canManage: boolean }) {
  const license = useLicense()
  const s = license.data
  if (!s || (s.status === 'ACTIVE' && !s.updateRequired)) return null
  const tone = s.status === 'WARNING' || s.status === 'UNVERIFIED' ? 'bg-warn-soft text-warn' : 'bg-bad-soft text-bad'
  return (
    <div role="status" className={`flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 text-sm sm:px-8 print:hidden ${tone}`}>
      <span className="font-medium">{s.message}</span>
      {canManage && (
        <NavLink to="/sistem" className="font-semibold underline underline-offset-2">
          {s.status === 'WARNING' || s.status === 'EXPIRED' ? 'Perpanjang lisensi' : 'Lihat status lisensi'}
        </NavLink>
      )}
    </div>
  )
}

/** Banner backup untuk admin (PLT-09): belum dikonfigurasi, gagal, atau tidak ada backup sukses 48 jam. */
function BackupBanner() {
  const q = useBackupStatus(true)
  const s = q.data
  if (!s) return null
  const failed = s.lastFailed && (!s.lastOk || s.lastFailed.startedAt > s.lastOk)
  const message = !s.configured
    ? 'Backup belum dikonfigurasi: kunci publik backup belum dipasang di server.'
    : s.overdue
      ? 'Belum ada backup sukses dalam 48 jam terakhir.'
      : failed
        ? 'Backup terakhir gagal.'
        : null
  if (!message) return null
  return (
    <div role="status" className="flex flex-wrap items-center gap-x-3 gap-y-1 bg-warn-soft px-4 py-2.5 text-sm text-warn sm:px-8 print:hidden">
      <span className="font-medium">{message}</span>
      <NavLink to="/sistem?tab=backup" className="font-semibold underline underline-offset-2">
        Lihat backup
      </NavLink>
    </div>
  )
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

import { Link } from 'react-router'
import type { Me } from '../lib/api'
import { activeCompany, can } from '../lib/session'
import { allowedBranches, isRestricted, useDimensions } from '../lib/dimensions'
import { Dashboard } from '../components/Dashboard'
import { Card } from '../components/ui'

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
  ['tax.export', 'Mengekspor XML Coretax'],
  ['platform.audit.view', 'Melihat audit trail'],
  ['data.export', 'Mengekspor data'],
  ['platform.user.manage', 'Mengelola pengguna dan persona'],
  ['platform.system.manage', 'Mengelola backup, pembaruan, dan lisensi'],
]

// Pintasan pekerjaan harian; hanya yang diizinkan peran pengguna di PT aktif yang tampil.
const shortcuts: { perm: string; to: string; label: string; hint: string }[] = [
  { perm: 'sales.create', to: '/penjualan/baru', label: 'Faktur penjualan baru', hint: 'Tagih pelanggan' },
  { perm: 'sales.create', to: '/penjualan/pelunasan/baru', label: 'Terima pelunasan', hint: 'Pembayaran dari pelanggan' },
  { perm: 'purchase.create', to: '/pembelian/baru', label: 'Tagihan pembelian baru', hint: 'Catat tagihan pemasok' },
  { perm: 'purchase.create', to: '/pembelian/pembayaran/baru', label: 'Bayar pemasok', hint: 'Pelunasan tagihan' },
  { perm: 'cash.create', to: '/kas', label: 'Catat kas dan bank', hint: 'Penerimaan, pengeluaran, transfer' },
  { perm: 'ledger.journal.create', to: '/jurnal/baru', label: 'Jurnal umum', hint: 'Transaksi di luar modul' },
  { perm: 'report.view', to: '/laporan', label: 'Laporan keuangan', hint: 'Neraca, laba rugi, arus kas' },
  { perm: 'tax.view', to: '/pajak', label: 'Pajak', hint: 'Faktur pajak dan ekspor Coretax' },
]

export function Home({ me }: { me: Me }) {
  const company = activeCompany(me)
  const branches = useDimensions(me.activeCompanyId, 'cabang').data ?? []
  const myBranches = isRestricted(me) ? allowedBranches(me, branches) : []
  const perms = new Set(company?.permissions ?? [])
  const firstName = me.user.name.split(' ')[0]

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-[26px] font-bold tracking-tight">Selamat datang, {firstName}</h1>
        <p className="mt-1.5 text-sm text-muted">
          {company ? `Anda masuk sebagai ${company.roleName} di ${company.name}.` : 'Akun Anda belum diberi akses ke PT mana pun. Hubungi Admin perusahaan.'}
          {myBranches.length > 0 && ` Akses Anda dibatasi ke cabang ${myBranches.map((b) => b.name).join(', ')}.`}{' '}
          <Link to="/panduan" className="font-semibold text-accent hover:underline">
            Buka panduan penggunaan →
          </Link>
        </p>
      </div>
      {can(me, 'report.view') && !isRestricted(me) && <Dashboard me={me} />}
      <div className="flex flex-wrap gap-4">
        <div className="min-w-0 flex-[1_1_320px]">
          <Card title="Yang bisa Anda lakukan di PT ini" subtitle="Ditentukan oleh peran Anda; bisa berbeda di setiap PT.">
            <ul className="flex flex-col">
              {abilities.map(([perm, label]) => {
                const yes = perms.has(perm)
                return (
                  <li key={perm} className="flex items-center gap-2.5 border-b border-line py-2 text-sm last:border-0">
                    <span className={`size-2 rounded-full ${yes ? 'bg-good' : 'bg-line-2'}`} />
                    <span className={yes ? 'text-ink' : 'text-muted line-through decoration-line-2'}>{label}</span>
                  </li>
                )
              })}
            </ul>
          </Card>
        </div>
        {shortcuts.some((s) => perms.has(s.perm)) && (
          <div className="min-w-0 flex-[1_1_320px]">
            <Card title="Pintasan" subtitle="Pekerjaan yang paling sering dilakukan di PT ini.">
              <ul className="flex flex-col">
                {shortcuts
                  .filter((s) => perms.has(s.perm))
                  .map((s) => (
                    <li key={s.to} className="border-b border-line last:border-0">
                      <Link to={s.to} className="flex items-center justify-between gap-3 py-2 text-sm transition-colors duration-150 hover:text-accent">
                        <span className="font-medium">{s.label}</span>
                        <span className="text-[12.5px] text-muted">{s.hint}</span>
                      </Link>
                    </li>
                  ))}
              </ul>
            </Card>
          </div>
        )}
      </div>
    </div>
  )
}

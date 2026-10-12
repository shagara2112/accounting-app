import type { Me } from '../lib/api'
import { activeCompany } from '../lib/session'
import { Card } from '../components/ui'

// Ringkasan izin dalam bahasa sehari-hari, mengikuti matriks enam persona di TDD.
const abilities: [string, string][] = [
  ['ledger.journal.create', 'Membuat transaksi dan jurnal'],
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

// Rencana implementasi di TDD.
const roadmap: [string, string][] = [
  ['Fondasi platform', 'Selesai'],
  ['Buku besar, kode akun 1–9, jurnal penyesuaian', 'Selesai'],
  ['Penjualan, pembelian, kas dan bank', 'Apr 2027'],
  ['Pajak dan ekspor Coretax', 'Apr 2027'],
  ['Multi-entitas dan konsolidasi', 'Mei 2027'],
  ['Proyek dan persediaan', 'Mei 2027'],
  ['Lisensi, installer, dan pembaruan', 'Mei 2027'],
]

export function Home({ me }: { me: Me }) {
  const company = activeCompany(me)
  const perms = new Set(company?.permissions ?? [])
  const firstName = me.user.name.split(' ')[0]

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-[26px] font-bold tracking-tight">Selamat datang, {firstName}</h1>
        <p className="mt-1.5 text-sm text-muted">
          {company ? `Anda masuk sebagai ${company.roleName} di ${company.name}.` : 'Akun Anda belum diberi akses ke PT mana pun. Hubungi Admin perusahaan.'}
        </p>
      </div>
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
        <div className="min-w-0 flex-[1_1_320px]">
          <Card title="Sedang dibangun" subtitle="Modul MVP sesuai rencana implementasi; GA Agustus 2027.">
            <ul className="flex flex-col">
              {roadmap.map(([name, when]) => (
                <li key={name} className="flex items-center justify-between gap-3 border-b border-line py-2 text-sm last:border-0">
                  <span>{name}</span>
                  <span className={`shrink-0 text-xs font-semibold ${when === 'Selesai' ? 'text-good' : 'text-muted'}`}>{when}</span>
                </li>
              ))}
            </ul>
          </Card>
        </div>
      </div>
    </div>
  )
}

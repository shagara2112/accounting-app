// Tanggal akuntansi adalah tanggal kalender ("2026-10-31"), bukan titik waktu. Semua fungsi
// di sini bekerja dengan string dan bagian tanggal lokal, sehingga tidak ada pergeseran hari
// karena zona waktu.

const monthNames = ['Januari', 'Februari', 'Maret', 'April', 'Mei', 'Juni', 'Juli', 'Agustus', 'September', 'Oktober', 'November', 'Desember']
const shortMonths = ['Jan', 'Feb', 'Mar', 'Apr', 'Mei', 'Jun', 'Jul', 'Agu', 'Sep', 'Okt', 'Nov', 'Des']

const pad = (n: number) => String(n).padStart(2, '0')

export function todayISO(): string {
  const d = new Date()
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** "2026-10-31" → "2026-10" */
export const monthOf = (iso: string) => iso.slice(0, 7)

export function monthStart(ym: string): string {
  return `${ym}-01`
}

export function monthEnd(ym: string): string {
  const [y, m] = ym.split('-').map(Number)
  return `${ym}-${pad(new Date(y, m, 0).getDate())}`
}

export function addMonths(ym: string, n: number): string {
  const [y, m] = ym.split('-').map(Number)
  const total = y * 12 + (m - 1) + n
  return `${Math.floor(total / 12)}-${pad((total % 12) + 1)}`
}

/** "2026-10" → "Oktober 2026" */
export function monthLabel(ym: string): string {
  const [y, m] = ym.split('-').map(Number)
  return `${monthNames[m - 1]} ${y}`
}

export const monthName = (m: number) => monthNames[m - 1]

/** "2026-10-31" → "31 Okt 2026"; waktu ISO lengkap juga diterima. */
export function formatDate(iso: string): string {
  if (iso.length === 10) {
    const [y, m, d] = iso.split('-').map(Number)
    return `${d} ${shortMonths[m - 1]} ${y}`
  }
  return new Date(iso).toLocaleDateString('id-ID', { day: 'numeric', month: 'short', year: 'numeric' })
}

export function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString('id-ID', { dateStyle: 'medium', timeStyle: 'short' })
}

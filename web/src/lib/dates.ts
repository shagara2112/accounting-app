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

/** Tanggal ditambah n hari. */
export function addDays(iso: string, n: number): string {
  const [y, m, d] = iso.split('-').map(Number)
  const dt = new Date(y, m - 1, d + n)
  return `${dt.getFullYear()}-${pad(dt.getMonth() + 1)}-${pad(dt.getDate())}`
}

/** Tanggal yang sama setahun sebelumnya; 29 Februari menjadi 28 Februari. */
export function lastYear(iso: string): string {
  const [y, m, d] = iso.split('-').map(Number)
  const end = new Date(y - 1, m, 0).getDate()
  return `${y - 1}-${pad(m)}-${pad(Math.min(d, end))}`
}

/** Jumlah hari dari a ke b (b − a). */
export function daysBetween(a: string, b: string): number {
  const [y1, m1, d1] = a.split('-').map(Number)
  const [y2, m2, d2] = b.split('-').map(Number)
  return Math.round((Date.UTC(y2, m2 - 1, d2) - Date.UTC(y1, m1 - 1, d1)) / 86_400_000)
}

// Nominal dari API berupa string desimal 4 angka, mis. "1250000.0000". Di browser nominal
// dihitung sebagai BigInt berskala 10^4, sama seperti di server, supaya debit = kredit
// tidak pernah meleset karena pembulatan floating point.

const SCALE = 10000n

export function toUnits(s: string): bigint {
  const neg = s.startsWith('-')
  const [whole, frac = ''] = (neg ? s.slice(1) : s).split('.')
  const u = BigInt(whole || '0') * SCALE + BigInt((frac + '0000').slice(0, 4))
  return neg ? -u : u
}

/** Kebalikan toUnits: string yang dikirim ke API. */
export function fromUnits(u: bigint): string {
  const neg = u < 0n
  const a = neg ? -u : u
  return (neg ? '-' : '') + (a / SCALE).toString() + '.' + (a % SCALE).toString().padStart(4, '0')
}

type FormatOptions = { blankZero?: boolean; parens?: boolean }

/** "1.250.000" atau "1.250.000,50". Negatif ditulis "−1.250.000", atau "(1.250.000)" dengan parens. */
export function formatAmount(value: bigint | string, opts: FormatOptions = {}): string {
  const v = typeof value === 'string' ? toUnits(value) : value
  if (v === 0n && opts.blankZero) return ''
  const neg = v < 0n
  const a = neg ? -v : v
  const whole = (a / SCALE).toString().replace(/\B(?=(\d{3})+(?!\d))/g, '.')
  let frac = (a % SCALE).toString().padStart(4, '0').replace(/0+$/, '')
  if (frac.length === 1) frac += '0'
  const s = frac ? `${whole},${frac}` : whole
  if (!neg) return s
  return opts.parens ? `(${s})` : `−${s}`
}

export function formatRp(value: bigint | string): string {
  const s = formatAmount(value)
  return s.startsWith('−') ? `−Rp ${s.slice(1)}` : `Rp ${s}`
}

/**
 * Membaca ketikan pengguna dengan kebiasaan Indonesia: titik pemisah ribuan, koma desimal.
 * "1.250.000", "1250000", "1.250.000,5", dan "Rp 1.250.000" semuanya diterima.
 * Mengembalikan null bila tidak bisa dibaca.
 */
export function parseInput(raw: string): bigint | null {
  let s = raw.replace(/\s|rp/gi, '')
  if (s === '') return 0n
  if (s.includes(',')) {
    s = s.replace(/\./g, '').replace(',', '.')
  } else if (/^\d{1,3}(\.\d{3})+$/.test(s)) {
    s = s.replace(/\./g, '')
  }
  if (!/^\d+(\.\d{1,4})?$/.test(s) || s.split('.')[0].length > 15) return null
  return toUnits(s)
}

// Perhitungan baris faktur dan tagihan di browser, sama persis dengan paket Go `trade` dan `tax`:
// qty × harga dan diskon dibulatkan ke rupiah (≥ 0,5 ke atas), PPN dan PPh dibulatkan ke bawah.
// Server tetap menghitung ulang saat menyimpan; ini hanya supaya total terlihat selagi mengetik.
import type { TaxCode } from './api'
import { parseInput } from './money'

const SCALE = 10000n

type Mode = 'halfUp' | 'down'

function divRound(n: bigint, d: bigint, mode: Mode): bigint {
  const q = n / d // pembagian BigInt dipotong ke arah nol
  const r = n % d
  if (mode === 'down' || r === 0n) return q
  const twice = (r < 0n ? -r : r) * 2n
  if (twice >= (d < 0n ? -d : d)) return n < 0n !== d < 0n ? q - 1n : q + 1n
  return q
}

/** a (berskala 10^4) × num/den, dibulatkan ke `places` desimal; hasil tetap berskala 10^4. */
function mulFrac(a: bigint, num: bigint, den: bigint, places: number, mode: Mode): bigint {
  const step = 10n ** BigInt(4 - places)
  return divRound(a * num, den * step, mode) * step
}

function mul(a: bigint, b: bigint, places: number, mode: Mode): bigint {
  const step = 10n ** BigInt(4 - places)
  return divRound(a * b, SCALE * step, mode) * step
}

export type LineValues = { qty: string; unitPrice: string; discountPct: string; taxCode: string }

export type LineCalc = {
  gross: bigint
  discount: bigint
  net: bigint
  otherDpp: bigint
  vat: bigint
  /** Isian yang tidak terbaca atau di luar batas; baris tidak ikut dihitung. */
  invalid?: 'qty' | 'unitPrice' | 'discountPct' | 'taxCode'
}

const zero: LineCalc = { gross: 0n, discount: 0n, net: 0n, otherDpp: 0n, vat: 0n }

export function computeLine(l: LineValues, codes: TaxCode[]): LineCalc {
  const qty = parseInput(l.qty)
  const price = parseInput(l.unitPrice)
  const pct = parseInput(l.discountPct)
  if (qty === null || qty <= 0n) return { ...zero, invalid: 'qty' }
  if (price === null) return { ...zero, invalid: 'unitPrice' }
  if (pct === null || pct > 100n * SCALE || pct % 100n !== 0n) return { ...zero, invalid: 'discountPct' }
  const code = codes.find((c) => c.code === l.taxCode && c.kind === 'ppn')
  if (!code) return { ...zero, invalid: 'taxCode' }

  const gross = mul(qty, price, 0, 'halfUp')
  const discount = mul(gross, mulFrac(pct, 1n, 100n, 4, 'halfUp'), 0, 'halfUp')
  const net = gross - discount
  if (code.rateBp === 0) return { gross, discount, net, otherDpp: net, vat: 0n }
  const num = BigInt(code.dppNum)
  const den = BigInt(code.dppDen)
  return {
    gross,
    discount,
    net,
    otherDpp: mulFrac(net, num, den, 2, 'halfUp'),
    vat: mulFrac(net, num * BigInt(code.rateBp), den * 10000n, 0, 'down'),
  }
}

export type TotalsCalc = { subtotal: bigint; discount: bigint; dpp: bigint; otherDpp: bigint; vat: bigint; total: bigint }

export function computeTotals(lines: LineCalc[]): TotalsCalc {
  const t = { subtotal: 0n, discount: 0n, dpp: 0n, otherDpp: 0n, vat: 0n, total: 0n }
  for (const l of lines) {
    t.subtotal += l.gross
    t.discount += l.discount
    t.dpp += l.net
    t.otherDpp += l.otherDpp
    t.vat += l.vat
  }
  t.total = t.dpp + t.vat
  return t
}

/** PPh yang dipotong: tarif × dasar, dibulatkan ke bawah ke rupiah. */
export function withholding(base: bigint, code?: TaxCode): bigint {
  if (!code || code.kind !== 'pph') return 0n
  return mulFrac(base, BigInt(code.rateBp), 10000n, 0, 'down')
}

/** "12%" atau "1,75%" dari basis poin. */
export function rateLabel(bp: number): string {
  return (bp / 100).toLocaleString('id-ID', { maximumFractionDigits: 2 }) + '%'
}

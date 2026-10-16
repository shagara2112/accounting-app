// Encoder QR kecil untuk tautan otpauth:// 2FA: mode byte, koreksi galat level M, versi 1–10
// (sampai 213 byte). Mengikuti ISO/IEC 18004 dengan susunan seperti pustaka QR Nayuki.
// Ditulis sendiri supaya Lajur tetap tanpa dependensi luar dan berjalan tanpa internet.

/** [jumlah blok, codeword data per blok pendek, codeword EC per blok] untuk level M. */
const BLOCKS_M: [number, number, number][] = [
  [1, 16, 10], [1, 28, 16], [1, 44, 26], [2, 32, 18], [2, 43, 24],
  [4, 27, 16], [4, 31, 18], [4, 38, 22], [5, 36, 22], [5, 43, 26],
]
/** Total codeword per versi (data + EC). */
const TOTAL = [26, 44, 70, 100, 134, 172, 196, 242, 292, 346]
const ALIGN = [[], [6, 18], [6, 22], [6, 26], [6, 30], [6, 34], [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50]]

function gfMul(x: number, y: number): number {
  let z = 0
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d)
    z ^= ((y >>> i) & 1) * x
  }
  return z
}

function rsDivisor(degree: number): number[] {
  const r = new Array<number>(degree).fill(0)
  r[degree - 1] = 1
  let root = 1
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < r.length; j++) {
      r[j] = gfMul(r[j], root)
      if (j + 1 < r.length) r[j] ^= r[j + 1]
    }
    root = gfMul(root, 0x02)
  }
  return r
}

function rsRemainder(data: number[], divisor: number[]): number[] {
  const r = divisor.map(() => 0)
  for (const b of data) {
    const factor = b ^ (r.shift() as number)
    r.push(0)
    divisor.forEach((c, i) => (r[i] ^= gfMul(c, factor)))
  }
  return r
}

/** Data codeword dari teks: mode byte, panjang, isi, terminator, dan pengisi 0xEC/0x11. */
function encodeData(bytes: Uint8Array, version: number, capacity: number): number[] {
  const bits: number[] = []
  const put = (v: number, n: number) => {
    for (let i = n - 1; i >= 0; i--) bits.push((v >>> i) & 1)
  }
  put(0b0100, 4)
  put(bytes.length, version <= 9 ? 8 : 16)
  bytes.forEach((b) => put(b, 8))
  put(0, Math.min(4, capacity * 8 - bits.length))
  put(0, (8 - (bits.length % 8)) % 8)
  const out: number[] = []
  for (let i = 0; i < bits.length; i += 8) out.push(bits.slice(i, i + 8).reduce((a, b) => (a << 1) | b, 0))
  for (let pad = 0xec; out.length < capacity; pad ^= 0xec ^ 0x11) out.push(pad)
  return out
}

function interleave(data: number[], version: number): number[] {
  const [numBlocks, , ecLen] = BLOCKS_M[version - 1]
  const raw = TOTAL[version - 1]
  const numShort = numBlocks - (raw % numBlocks)
  const shortLen = Math.floor(raw / numBlocks)
  const divisor = rsDivisor(ecLen)
  const blocks: number[][] = []
  for (let i = 0, k = 0; i < numBlocks; i++) {
    const dat = data.slice(k, k + shortLen - ecLen + (i < numShort ? 0 : 1))
    k += dat.length
    const ecc = rsRemainder(dat, divisor)
    if (i < numShort) dat.push(0)
    blocks.push(dat.concat(ecc))
  }
  const out: number[] = []
  for (let i = 0; i < blocks[0].length; i++) {
    blocks.forEach((b, j) => {
      if (i !== shortLen - ecLen || j >= numShort) out.push(b[i])
    })
  }
  return out
}

/** Matriks QR (true = modul gelap), atau null bila teks terlalu panjang. */
export function qrMatrix(text: string): boolean[][] | null {
  const bytes = new TextEncoder().encode(text)
  let version = 0
  for (let v = 1; v <= 10; v++) {
    const [blocks, , ec] = BLOCKS_M[v - 1]
    const capacity = TOTAL[v - 1] - blocks * ec
    const overhead = v <= 9 ? 12 : 20
    if (bytes.length * 8 + overhead <= capacity * 8) {
      version = v
      break
    }
  }
  if (!version) return null
  const [blocks, , ec] = BLOCKS_M[version - 1]
  const codewords = interleave(encodeData(bytes, version, TOTAL[version - 1] - blocks * ec), version)

  const size = version * 4 + 17
  const mod = Array.from({ length: size }, () => new Array<boolean>(size).fill(false))
  const fn = Array.from({ length: size }, () => new Array<boolean>(size).fill(false))
  const set = (x: number, y: number, dark: boolean) => {
    mod[y][x] = dark
    fn[y][x] = true
  }

  for (let i = 0; i < size; i++) {
    set(6, i, i % 2 === 0)
    set(i, 6, i % 2 === 0)
  }
  for (const [cx, cy] of [[3, 3], [size - 4, 3], [3, size - 4]]) {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const d = Math.max(Math.abs(dx), Math.abs(dy))
        const x = cx + dx
        const y = cy + dy
        if (x >= 0 && x < size && y >= 0 && y < size) set(x, y, d !== 2 && d !== 4)
      }
    }
  }
  const pos = ALIGN[version - 1]
  pos.forEach((ay, i) =>
    pos.forEach((ax, j) => {
      const last = pos.length - 1
      if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) return
      for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) set(ax + dx, ay + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1)
    }),
  )

  const drawFormat = (mask: number) => {
    const data = (0 << 3) | mask // level M = 00
    let rem = data
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
    const bits = ((data << 10) | rem) ^ 0x5412
    const bit = (i: number) => ((bits >>> i) & 1) !== 0
    for (let i = 0; i <= 5; i++) set(8, i, bit(i))
    set(8, 7, bit(6))
    set(8, 8, bit(7))
    set(7, 8, bit(8))
    for (let i = 9; i < 15; i++) set(14 - i, 8, bit(i))
    for (let i = 0; i < 8; i++) set(size - 1 - i, 8, bit(i))
    for (let i = 8; i < 15; i++) set(8, size - 15 + i, bit(i))
    set(8, size - 8, true)
  }
  drawFormat(0)
  if (version >= 7) {
    let rem = version
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25)
    const bits = (version << 12) | rem
    for (let i = 0; i < 18; i++) {
      const dark = ((bits >>> i) & 1) !== 0
      const a = size - 11 + (i % 3)
      const b = Math.floor(i / 3)
      set(a, b, dark)
      set(b, a, dark)
    }
  }

  let bitIndex = 0
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5
    for (let vert = 0; vert < size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j
        const upward = ((right + 1) & 2) === 0
        const y = upward ? size - 1 - vert : vert
        if (!fn[y][x] && bitIndex < codewords.length * 8) {
          mod[y][x] = ((codewords[bitIndex >>> 3] >>> (7 - (bitIndex & 7))) & 1) !== 0
          bitIndex++
        }
      }
    }
  }

  const masks: ((x: number, y: number) => boolean)[] = [
    (x, y) => (x + y) % 2 === 0,
    (_, y) => y % 2 === 0,
    (x) => x % 3 === 0,
    (x, y) => (x + y) % 3 === 0,
    (x, y) => (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0,
    (x, y) => ((x * y) % 2) + ((x * y) % 3) === 0,
    (x, y) => (((x * y) % 2) + ((x * y) % 3)) % 2 === 0,
    (x, y) => (((x + y) % 2) + ((x * y) % 3)) % 2 === 0,
  ]
  const applyMask = (m: number) => {
    for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (!fn[y][x] && masks[m](x, y)) mod[y][x] = !mod[y][x]
  }
  let best = 0
  let bestScore = Infinity
  for (let m = 0; m < 8; m++) {
    applyMask(m)
    drawFormat(m)
    const score = penalty(mod)
    if (score < bestScore) {
      best = m
      bestScore = score
    }
    applyMask(m)
  }
  applyMask(best)
  drawFormat(best)
  return mod
}

/** Skor penalti ISO 18004 (aturan N1–N4); mask dengan skor terendah dipakai. */
function penalty(m: boolean[][]): number {
  const n = m.length
  let score = 0
  const lines: boolean[][] = []
  for (let i = 0; i < n; i++) {
    lines.push(m[i])
    lines.push(m.map((row) => row[i]))
  }
  for (const line of lines) {
    let run = 1
    for (let i = 1; i <= n; i++) {
      if (i < n && line[i] === line[i - 1]) run++
      else {
        if (run >= 5) score += 3 + (run - 5)
        run = 1
      }
    }
    const s = line.map((d) => (d ? '1' : '0')).join('')
    for (const pat of ['10111010000', '00001011101']) {
      for (let i = s.indexOf(pat); i !== -1; i = s.indexOf(pat, i + 1)) score += 40
    }
  }
  let dark = 0
  for (let y = 0; y < n; y++) {
    for (let x = 0; x < n; x++) {
      if (m[y][x]) dark++
      if (x + 1 < n && y + 1 < n && m[y][x] === m[y][x + 1] && m[y][x] === m[y + 1][x] && m[y][x] === m[y + 1][x + 1]) score += 3
    }
  }
  score += Math.floor(Math.abs(dark * 20 - n * n * 10) / (n * n)) * 10
  return score
}

/** Path SVG modul gelap, dengan zona tenang 4 modul. */
export function qrPath(matrix: boolean[][]): { d: string; size: number } {
  let d = ''
  matrix.forEach((row, y) =>
    row.forEach((dark, x) => {
      if (dark) d += `M${x + 4} ${y + 4}h1v1h-1z`
    }),
  )
  return { d, size: matrix.length + 8 }
}

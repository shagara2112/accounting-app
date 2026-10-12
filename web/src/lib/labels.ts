import type { Kind, PeriodStatus } from './api'
import type { Tone } from '../components/ui'

export const kindLabel: Record<Kind, string> = {
  umum: 'Umum',
  penyesuaian: 'Penyesuaian',
  pembalik: 'Pembalik',
  penutup: 'Penutup',
}

export const kindTone: Record<Kind, Tone> = {
  umum: 'neutral',
  penyesuaian: 'accent',
  pembalik: 'warn',
  penutup: 'neutral',
}

/** Asal jurnal selain input manual, mis. "Pembatalan" untuk pembalik karena void. */
export function sourceLabel(source: string): string {
  switch (source) {
    case 'manual':
      return ''
    case 'void':
      return 'Pembatalan'
    case 'auto_reversal':
      return 'Balik otomatis'
    default:
      return source
  }
}

export const periodStatus: Record<PeriodStatus, { label: string; tone: Tone; help: string }> = {
  open: { label: 'Terbuka', tone: 'neutral', help: 'Semua jenis jurnal boleh masuk.' },
  adjusting: { label: 'Penyesuaian', tone: 'warn', help: 'Ditutup untuk transaksi; hanya jurnal penyesuaian yang boleh masuk.' },
  locked: { label: 'Terkunci', tone: 'good', help: 'Dikunci final; tidak ada jurnal yang bisa masuk.' },
}

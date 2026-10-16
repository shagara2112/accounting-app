import type { DocState, Kind, PeriodStatus } from './api'
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

const sources: Record<string, string> = {
  manual: '',
  void: 'Pembatalan',
  auto_reversal: 'Balik otomatis',
  'sales.invoice': 'Faktur penjualan',
  'sales.receipt': 'Pelunasan piutang',
  'sales.credit_note': 'Nota kredit',
  'purchase.bill': 'Tagihan pemasok',
  'purchase.payment': 'Pembayaran utang',
  'purchase.debit_note': 'Nota debit',
  'cash.transaction': 'Kas dan bank',
  'inventory.cost': 'HPP penjualan',
  'inventory.return': 'HPP retur penjualan',
  'inventory.daily': 'HPP penjualan harian',
  'sales.daily': 'Penjualan harian',
  opening: 'Saldo awal',
  'inventory.adjustment': 'Dokumen persediaan',
  'asset.depreciation': 'Penyusutan aset',
  'asset.disposal': 'Pelepasan aset',
  'tax.input_vat': 'PPN masukan tidak dikreditkan',
}

/** Asal jurnal selain input manual, mis. "Pembatalan" untuk pembalik karena void. */
export function sourceLabel(source: string): string {
  return sources[source] ?? source
}

/** Halaman dokumen sumber sebuah jurnal, untuk drill-down dari buku besar (GL-04). */
export function sourceLink(source: string, id?: string): string | null {
  if (!id) return null
  switch (source) {
    case 'sales.invoice':
      return `/penjualan?id=${id}`
    case 'sales.receipt':
      return `/penjualan?tab=pelunasan&pid=${id}`
    case 'purchase.bill':
    case 'tax.input_vat':
      return `/pembelian?id=${id}`
    case 'purchase.payment':
      return `/pembelian?tab=pembayaran&pid=${id}`
    case 'sales.credit_note':
    case 'inventory.return':
      return `/penjualan?tab=retur&nid=${id}`
    case 'purchase.debit_note':
      return `/pembelian?tab=retur&nid=${id}`
    case 'sales.daily':
    case 'inventory.daily':
      return `/penjualan?tab=harian&hid=${id}`
    case 'cash.transaction':
      return `/kas?id=${id}`
    case 'inventory.cost':
      return `/penjualan?id=${id}`
    case 'inventory.adjustment':
      return `/persediaan?tab=dokumen&doc=${id}`
    case 'asset.depreciation':
      return `/aset?tab=penyusutan&run=${id}`
    case 'asset.disposal':
      return `/aset?pelepasan=${id}`
    default:
      return null
  }
}

export const docState: Record<DocState, { label: string; tone: Tone }> = {
  draft: { label: 'Draf', tone: 'neutral' },
  open: { label: 'Belum lunas', tone: 'warn' },
  paid: { label: 'Lunas', tone: 'good' },
  void: { label: 'Void', tone: 'bad' },
}

export const tradeText = {
  sales: {
    title: 'Penjualan',
    doc: 'Faktur',
    doc_: 'faktur',
    contact: 'Pelanggan',
    contactRole: 'customer' as const,
    settle: 'Pelunasan',
    settle_: 'pelunasan',
    settleAction: 'Terima pembayaran',
    note: 'Nota kredit',
    note_: 'nota kredit',
    notes: 'Retur & nota kredit',
    aging: 'Umur piutang',
    base: '/penjualan',
    tabs: { docs: 'faktur', settle: 'pelunasan', notes: 'retur', aging: 'umur' },
    perm: { view: 'sales.view', create: 'sales.create', void: 'sales.void' },
    accountClasses: [4, 7],
    accountHint: 'akun pendapatan (kepala 4 atau 7)',
  },
  purchase: {
    title: 'Pembelian',
    doc: 'Tagihan',
    doc_: 'tagihan',
    contact: 'Pemasok',
    contactRole: 'supplier' as const,
    settle: 'Pembayaran',
    settle_: 'pembayaran',
    settleAction: 'Bayar tagihan',
    note: 'Nota debit',
    note_: 'nota debit',
    notes: 'Retur & nota debit',
    aging: 'Umur utang',
    base: '/pembelian',
    tabs: { docs: 'tagihan', settle: 'pembayaran', notes: 'retur', aging: 'umur' },
    perm: { view: 'purchase.view', create: 'purchase.create', void: 'purchase.void' },
    accountClasses: [1, 5, 6, 8],
    accountHint: 'akun aset, beban pokok, atau beban',
  },
}

export const periodStatus: Record<PeriodStatus, { label: string; tone: Tone; help: string }> = {
  open: { label: 'Terbuka', tone: 'neutral', help: 'Semua jenis jurnal boleh masuk.' },
  adjusting: { label: 'Penyesuaian', tone: 'warn', help: 'Ditutup untuk transaksi; hanya jurnal penyesuaian yang boleh masuk.' },
  locked: { label: 'Terkunci', tone: 'good', help: 'Dikunci final; tidak ada jurnal yang bisa masuk.' },
}

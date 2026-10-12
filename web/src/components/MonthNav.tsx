import { addMonths, monthLabel } from '../lib/dates'

const chevron = (d: string) => (
  <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d={d} />
  </svg>
)

/** Pindah bulan dengan tombol ‹ dan ›; label bulan dibacakan pembaca layar saat berubah. */
export function MonthNav({ value, onChange, label = 'Bulan' }: { value: string; onChange: (ym: string) => void; label?: string }) {
  const btn = 'flex size-9 cursor-pointer items-center justify-center rounded-[8px] text-ink-2 transition-colors hover:bg-hover'
  return (
    <div role="group" aria-label={label} className="inline-flex items-center gap-1 rounded-[10px] border border-line-2 bg-surface p-1">
      <button type="button" className={btn} onClick={() => onChange(addMonths(value, -1))} aria-label={`Bulan sebelumnya, ${monthLabel(addMonths(value, -1))}`}>
        {chevron('M15 6l-6 6 6 6')}
      </button>
      <span aria-live="polite" className="min-w-[132px] px-1 text-center text-sm font-semibold">
        {monthLabel(value)}
      </span>
      <button type="button" className={btn} onClick={() => onChange(addMonths(value, 1))} aria-label={`Bulan berikutnya, ${monthLabel(addMonths(value, 1))}`}>
        {chevron('M9 6l6 6-6 6')}
      </button>
    </div>
  )
}

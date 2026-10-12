import { Children, useEffect, useId, useRef, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type RefObject, type SelectHTMLAttributes, type TextareaHTMLAttributes } from 'react'
import { createPortal } from 'react-dom'
import { AnimatePresence, motion } from 'motion/react'

export const ease = [0.2, 0, 0, 1] as const

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'md' | 'sm'
  busy?: boolean
}

const variants = {
  primary: 'bg-accent text-on-accent hover:brightness-110 hover:shadow-md disabled:opacity-60',
  secondary: 'bg-surface text-ink border border-line-2 hover:bg-hover disabled:opacity-60',
  ghost: 'text-ink-2 hover:bg-hover disabled:opacity-60',
  danger: 'bg-bad text-white hover:brightness-110 hover:shadow-md disabled:opacity-60',
}

export function Button({ variant = 'primary', size = 'md', busy, className = '', children, disabled, ...rest }: ButtonProps) {
  return (
    <button
      type="button"
      {...rest}
      disabled={disabled || busy}
      className={`inline-flex cursor-pointer items-center justify-center gap-2 rounded-[10px] font-semibold whitespace-nowrap transition-all duration-150 ease-lajur disabled:cursor-not-allowed ${
        size === 'md' ? 'min-h-11 px-4 text-sm' : 'min-h-9 px-3 text-[13px]'
      } ${variants[variant]} ${className}`}
    >
      {busy && <Spinner />}
      {children}
    </button>
  )
}

export function Spinner() {
  return (
    <span
      aria-hidden="true"
      className="inline-block size-3.5 animate-spin rounded-full border-2 border-current border-t-transparent opacity-80"
    />
  )
}

/** Kelas dasar input; dipakai juga oleh input di dalam tabel jurnal. */
export const inputClass =
  'min-h-11 w-full rounded-[10px] border bg-surface px-3 text-[15px] text-ink transition-colors duration-150 placeholder:text-muted hover:border-muted disabled:opacity-60'

function fieldBorder(error?: string) {
  return error ? 'border-bad' : 'border-line-2'
}

function FieldShell({ id, label, hint, error, children }: { id: string; label: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-ink-2">
        {label}
      </label>
      {children}
      {error ? (
        <motion.p
          key={error}
          id={`${id}-msg`}
          initial={{ opacity: 0, x: 0 }}
          animate={{ opacity: 1, x: [0, -5, 5, -3, 3, 0] }}
          transition={{ duration: 0.2 }}
          className="text-[13px] text-bad"
        >
          {error}
        </motion.p>
      ) : (
        hint && (
          <p id={`${id}-msg`} className="text-[13px] text-muted">
            {hint}
          </p>
        )
      )}
    </div>
  )
}

type FieldProps = InputHTMLAttributes<HTMLInputElement> & { label: string; id: string; hint?: ReactNode; error?: string }

export function Field({ label, id, hint, error, className = '', ...rest }: FieldProps) {
  return (
    <FieldShell id={id} label={label} hint={hint} error={error}>
      <input
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={error || hint ? `${id}-msg` : undefined}
        {...rest}
        className={`${inputClass} ${fieldBorder(error)} ${className}`}
      />
    </FieldShell>
  )
}

type SelectFieldProps = SelectHTMLAttributes<HTMLSelectElement> & { label: string; id: string; hint?: ReactNode; error?: string }

export function SelectField({ label, id, hint, error, className = '', children, ...rest }: SelectFieldProps) {
  return (
    <FieldShell id={id} label={label} hint={hint} error={error}>
      <select
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={error || hint ? `${id}-msg` : undefined}
        {...rest}
        className={`${inputClass} ${fieldBorder(error)} cursor-pointer ${className}`}
      >
        {children}
      </select>
    </FieldShell>
  )
}

type TextAreaProps = TextareaHTMLAttributes<HTMLTextAreaElement> & { label: string; id: string; hint?: ReactNode; error?: string }

export function TextAreaField({ label, id, hint, error, className = '', ...rest }: TextAreaProps) {
  return (
    <FieldShell id={id} label={label} hint={hint} error={error}>
      <textarea
        id={id}
        rows={3}
        aria-invalid={error ? true : undefined}
        aria-describedby={error || hint ? `${id}-msg` : undefined}
        {...rest}
        className={`${inputClass} ${fieldBorder(error)} py-2.5 ${className}`}
      />
    </FieldShell>
  )
}

export function Checkbox({ label, hint, checked, onChange, disabled }: { label: ReactNode; hint?: ReactNode; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  const id = useId()
  return (
    <label htmlFor={id} className="flex cursor-pointer items-start gap-3 py-1">
      <input
        id={id}
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5 size-[18px] shrink-0 cursor-pointer accent-[var(--color-accent)]"
      />
      <span className="flex flex-col gap-0.5">
        <span className="text-sm font-medium">{label}</span>
        {hint && <span className="text-[13px] text-muted">{hint}</span>}
      </span>
    </label>
  )
}

export function Skeleton({ className = '' }: { className?: string }) {
  return <div aria-hidden="true" className={`skeleton ${className}`} />
}

const tones = {
  good: 'bg-good-soft text-good',
  warn: 'bg-warn-soft text-warn',
  bad: 'bg-bad-soft text-bad',
  accent: 'bg-accent-soft text-accent',
  neutral: 'bg-hover text-ink-2',
}

export type Tone = keyof typeof tones

export function Badge({ tone, children, className = '' }: { tone: Tone; children: ReactNode; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold whitespace-nowrap transition-colors duration-150 ${tones[tone]} ${className}`}>
      {children}
    </span>
  )
}

export function Alert({ tone = 'bad', children, shakeKey }: { tone?: Tone; children: ReactNode; shakeKey?: unknown }) {
  return (
    <motion.div
      key={String(shakeKey ?? '')}
      role={tone === 'bad' ? 'alert' : 'status'}
      initial={{ opacity: 0, x: 0 }}
      animate={{ opacity: 1, x: tone === 'bad' ? [0, -6, 6, -4, 4, 0] : 0 }}
      transition={{ duration: 0.2 }}
      className={`rounded-[10px] px-3.5 py-2.5 text-sm ${tones[tone]}`}
    >
      {children}
    </motion.div>
  )
}

export function Card({ title, subtitle, actions, children, className = '' }: { title?: string; subtitle?: string; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`flex flex-col gap-3 rounded-[14px] border border-line bg-surface p-5 ${className}`}>
      {(title || actions) && (
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            {title && <h2 className="text-base font-semibold">{title}</h2>}
            {subtitle && <p className="mt-1 text-[13px] text-muted">{subtitle}</p>}
          </div>
          {actions}
        </div>
      )}
      {children}
    </section>
  )
}

export function PageHeader({ title, subtitle, actions, aside }: { title: string; subtitle?: ReactNode; actions?: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="text-[26px] font-bold tracking-tight">{title}</h1>
          {aside}
        </div>
        {subtitle && <p className="mt-1.5 max-w-2xl text-sm text-muted">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2.5">{actions}</div>}
    </div>
  )
}

export function EmptyState({ title, body, action }: { title: string; body?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-14 text-center">
      <svg width="44" height="44" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="var(--color-line-2)" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round">
        <path d="M4 4h16v16H4zM4 9h16M9 9v11" />
      </svg>
      <p className="text-base font-semibold">{title}</p>
      {body && <p className="max-w-md text-sm text-muted">{body}</p>}
      {action}
    </div>
  )
}

/** Pilihan tunggal berbentuk tombol bersebelahan, mis. filter jenis jurnal. */
export function Segmented<T extends string>({ label, value, onChange, options }: { label: string; value: T; onChange: (v: T) => void; options: { value: T; label: string }[] }) {
  const id = useId()
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-[10px] border border-line-2 bg-surface p-1">
      {options.map((o) => {
        const active = o.value === value
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(o.value)}
            className={`relative min-h-9 cursor-pointer rounded-[7px] px-3 text-[13px] font-semibold transition-colors duration-150 ${active ? 'text-accent' : 'text-ink-2 hover:text-ink'}`}
          >
            {active && (
              <motion.span layoutId={`seg-${id}`} className="absolute inset-0 rounded-[7px] bg-accent-soft" transition={{ duration: 0.2, ease }} />
            )}
            <span className="relative">{o.label}</span>
          </button>
        )
      })}
    </div>
  )
}

const focusable = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/** Fokus masuk ke panel saat dibuka, Tab berputar di dalamnya, Esc menutup, dan fokus
 *  kembali ke tombol pembuka saat ditutup. */
function useModal(panel: RefObject<HTMLDivElement | null>, onClose: () => void) {
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const first = panel.current?.querySelector<HTMLElement>('[data-autofocus]') ?? panel.current?.querySelector<HTMLElement>(focusable)
    first?.focus()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.stopPropagation()
        close.current()
      }
      if (e.key === 'Tab' && panel.current) {
        const items = Array.from(panel.current.querySelectorAll<HTMLElement>(focusable))
        if (items.length === 0) return
        const [head, tail] = [items[0], items[items.length - 1]]
        if (e.shiftKey && document.activeElement === head) {
          e.preventDefault()
          tail.focus()
        } else if (!e.shiftKey && document.activeElement === tail) {
          e.preventDefault()
          head.focus()
        }
      }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.body.style.overflow = prevOverflow
      opener?.focus?.()
    }
  }, [panel])
}

type ModalProps = { open: boolean; onClose: () => void; title: string; description?: ReactNode; children: ReactNode; footer?: ReactNode }

export function Dialog({ open, onClose, width = 480, ...rest }: ModalProps & { width?: number }) {
  return createPortal(
    <AnimatePresence>{open && <DialogPanel key="dialog" onClose={onClose} width={width} {...rest} />}</AnimatePresence>,
    document.body,
  )
}

function DialogPanel({ onClose, title, description, children, footer, width }: Omit<ModalProps, 'open'> & { width: number }) {
  const panel = useRef<HTMLDivElement>(null)
  const titleId = useId()
  useModal(panel, onClose)
  return (
    <motion.div className="fixed inset-0 z-40 flex items-center justify-center p-4" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0, transition: { duration: 0.12 } }} transition={{ duration: 0.2 }}>
      <div className="absolute inset-0 bg-[#0b0f17]/40" onClick={onClose} aria-hidden="true" />
      <motion.div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        style={{ maxWidth: width }}
        initial={{ opacity: 0, scale: 0.98 }}
        animate={{ opacity: 1, scale: 1 }}
        exit={{ opacity: 0, scale: 0.98, transition: { duration: 0.12 } }}
        transition={{ duration: 0.2, ease }}
        className="relative flex max-h-[calc(100vh-32px)] w-full flex-col overflow-hidden rounded-[14px] border border-line bg-surface shadow-xl"
      >
        <div className="flex flex-col gap-1 px-6 pt-5">
          <h2 id={titleId} className="text-lg font-semibold">
            {title}
          </h2>
          {description && <p className="text-sm text-muted">{description}</p>}
        </div>
        {Children.toArray(children).length > 0 ? <div className="flex flex-col gap-4 overflow-y-auto px-6 py-5">{children}</div> : <div className="h-5" />}
        {footer && <div className="flex flex-wrap justify-end gap-2.5 border-t border-line bg-canvas/50 px-6 py-4">{footer}</div>}
      </motion.div>
    </motion.div>
  )
}

export function Drawer({ open, onClose, ...rest }: ModalProps) {
  return createPortal(
    <AnimatePresence>{open && <DrawerPanel key="drawer" onClose={onClose} {...rest} />}</AnimatePresence>,
    document.body,
  )
}

function DrawerPanel({ onClose, title, description, children, footer }: Omit<ModalProps, 'open'>) {
  const panel = useRef<HTMLDivElement>(null)
  const titleId = useId()
  useModal(panel, onClose)
  return (
    <motion.div className="fixed inset-0 z-40" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0, transition: { duration: 0.15 } }} transition={{ duration: 0.2 }}>
      <div className="absolute inset-0 bg-[#0b0f17]/30" onClick={onClose} aria-hidden="true" />
      <motion.div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        initial={{ x: 40, opacity: 0 }}
        animate={{ x: 0, opacity: 1 }}
        exit={{ x: 40, opacity: 0, transition: { duration: 0.15 } }}
        transition={{ duration: 0.25, ease }}
        className="absolute inset-y-0 right-0 flex w-full max-w-[640px] flex-col border-l border-line bg-surface shadow-2xl"
      >
        <div className="flex items-start justify-between gap-4 border-b border-line px-6 py-4">
          <div className="min-w-0">
            <h2 id={titleId} className="num truncate text-lg font-semibold">
              {title}
            </h2>
            {description && <div className="mt-1 text-sm text-muted">{description}</div>}
          </div>
          <button type="button" onClick={onClose} aria-label="Tutup" className="-mr-2 flex size-10 shrink-0 cursor-pointer items-center justify-center rounded-[10px] text-ink-2 transition-colors hover:bg-hover">
            <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>
        <div className="flex flex-1 flex-col gap-5 overflow-y-auto px-6 py-5">{children}</div>
        {footer && <div className="flex flex-wrap justify-end gap-2.5 border-t border-line px-6 py-4">{footer}</div>}
      </motion.div>
    </motion.div>
  )
}

/** Tabel standar: header lengket, baris 40 px, angka rata kanan diatur per sel. */
export function TableShell({ children, minWidth = 720 }: { children: ReactNode; minWidth?: number }) {
  return (
    <div className="overflow-hidden rounded-[14px] border border-line bg-surface">
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm" style={{ minWidth }}>
          {children}
        </table>
      </div>
    </div>
  )
}

export const th = 'px-4 py-3 text-[12.5px] font-semibold text-muted'
export const td = 'px-4 py-2.5'

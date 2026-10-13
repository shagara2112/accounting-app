import { useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { AnimatePresence, motion } from 'motion/react'
import { ease, inputClass } from './ui'

export type Option = { id: string; label: string; detail?: string }

const MAX = 60
const LIST_HEIGHT = 288

function usePosition(input: RefObject<HTMLInputElement | null>, open: boolean) {
  const [style, setStyle] = useState<CSSProperties>({})
  useLayoutEffect(() => {
    if (!open) return
    function place() {
      const r = input.current?.getBoundingClientRect()
      if (!r) return
      const width = Math.max(r.width, 280)
      const left = Math.min(r.left, window.innerWidth - width - 8)
      const below = window.innerHeight - r.bottom
      setStyle(below < LIST_HEIGHT + 16 && r.top > below ? { position: 'fixed', left, width, bottom: window.innerHeight - r.top + 4 } : { position: 'fixed', left, width, top: r.bottom + 4 })
    }
    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    return () => {
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place, true)
    }
  }, [input, open])
  return style
}

/** Pilihan yang bisa dicari dengan mengetik; panah atas/bawah dan Enter untuk memilih. */
export function SearchSelect({
  id,
  options,
  value,
  onChange,
  placeholder = 'Ketik untuk mencari',
  invalid,
  label,
  empty = 'Tidak ada yang cocok.',
}: {
  id?: string
  options: Option[]
  value: string
  onChange: (id: string) => void
  placeholder?: string
  invalid?: boolean
  label?: string
  empty?: string
}) {
  const listId = useId()
  const input = useRef<HTMLInputElement>(null)
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [active, setActive] = useState(0)
  const style = usePosition(input, open)
  const selected = options.find((o) => o.id === value)

  const shown = useMemo(() => {
    const q = text.trim().toLowerCase()
    const list = q ? options.filter((o) => o.label.toLowerCase().includes(q) || o.detail?.toLowerCase().includes(q)) : options
    return list.slice(0, MAX)
  }, [options, text])

  function choose(o: Option | undefined) {
    if (o) onChange(o.id)
    setOpen(false)
    setText('')
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      setOpen(true)
      const n = shown.length
      if (n) setActive((i) => (e.key === 'ArrowDown' ? (i + 1) % n : (i - 1 + n) % n))
    } else if (e.key === 'Enter' && open) {
      e.preventDefault()
      choose(shown[active])
    } else if (e.key === 'Escape' && open) {
      e.stopPropagation()
      setOpen(false)
      setText('')
    }
  }

  return (
    <div className="relative min-w-0">
      <input
        ref={input}
        id={id}
        role="combobox"
        aria-label={label}
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={open && shown[active] ? `${listId}-${shown[active].id}` : undefined}
        aria-invalid={invalid || undefined}
        autoComplete="off"
        placeholder={placeholder}
        value={open ? text : (selected?.label ?? '')}
        onFocus={() => {
          setOpen(true)
          setText('')
          setActive(Math.max(0, options.findIndex((o) => o.id === value)))
        }}
        onBlur={() => {
          setOpen(false)
          setText('')
        }}
        onChange={(e) => {
          setText(e.target.value)
          setActive(0)
          setOpen(true)
        }}
        onKeyDown={onKeyDown}
        className={`${inputClass} ${invalid ? 'border-bad' : 'border-line-2'} truncate`}
      />
      {createPortal(
        <AnimatePresence>
          {open && (
            <motion.ul
              id={listId}
              role="listbox"
              style={{ ...style, maxHeight: LIST_HEIGHT }}
              initial={{ opacity: 0, y: -4 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, transition: { duration: 0.1 } }}
              transition={{ duration: 0.15, ease }}
              className="z-50 overflow-y-auto rounded-[12px] border border-line bg-surface py-1 shadow-xl"
            >
              {shown.length === 0 && <li className="px-3 py-2.5 text-sm text-muted">{empty}</li>}
              {shown.map((o, i) => (
                <li
                  key={o.id}
                  id={`${listId}-${o.id}`}
                  role="option"
                  aria-selected={o.id === value}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    choose(o)
                  }}
                  onMouseEnter={() => setActive(i)}
                  className={`flex cursor-pointer flex-col px-3 py-2 text-sm ${i === active ? 'bg-hover' : ''}`}
                >
                  <span className="truncate">{o.label}</span>
                  {o.detail && <span className="num truncate text-xs text-muted">{o.detail}</span>}
                </li>
              ))}
            </motion.ul>
          )}
        </AnimatePresence>,
        document.body,
      )}
    </div>
  )
}

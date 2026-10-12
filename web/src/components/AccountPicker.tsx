import { useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { AnimatePresence, motion } from 'motion/react'
import type { Account } from '../lib/api'
import { ease, inputClass } from './ui'

type Props = {
  id?: string
  accounts: Account[]
  value: string
  onChange: (accountId: string) => void
  invalid?: boolean
  placeholder?: string
  label?: string // untuk pembaca layar bila tidak ada <label> terlihat
  className?: string
  onCommit?: () => void // dipanggil setelah akun dipilih dengan keyboard
}

const MAX_RESULTS = 60
const LIST_HEIGHT = 288

/** Posisi daftar mengikuti input. Daftar dirender di body (portal) supaya tidak terpotong
 *  oleh tabel yang bisa digulir, dan membuka ke atas bila ruang di bawah tidak cukup. */
function useListPosition(input: RefObject<HTMLInputElement | null>, open: boolean) {
  const [style, setStyle] = useState<CSSProperties>({})
  useLayoutEffect(() => {
    if (!open) return
    function place() {
      const r = input.current?.getBoundingClientRect()
      if (!r) return
      const below = window.innerHeight - r.bottom
      const width = Math.max(r.width, 320)
      const left = Math.min(r.left, window.innerWidth - width - 8)
      setStyle(
        below < LIST_HEIGHT + 16 && r.top > below
          ? { position: 'fixed', left, width, bottom: window.innerHeight - r.top + 4 }
          : { position: 'fixed', left, width, top: r.bottom + 4 },
      )
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

/** Pilih akun dengan mengetik kode atau nama. Panah atas/bawah memilih, Enter atau Tab
 *  mengambil, Esc membatalkan. Hanya akun detail yang aktif yang ditawarkan. */
export function AccountPicker({ id, accounts, value, onChange, invalid, placeholder = 'Ketik kode atau nama akun', label, className = '', onCommit }: Props) {
  const listId = useId()
  const input = useRef<HTMLInputElement>(null)
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [active, setActive] = useState(0)
  const listStyle = useListPosition(input, open)

  const selected = accounts.find((a) => a.id === value)
  const options = useMemo(() => {
    const q = text.trim().toLowerCase()
    const usable = accounts.filter((a) => a.postable && (a.active || a.id === value))
    if (!q) return usable.slice(0, MAX_RESULTS)
    const byCode = usable.filter((a) => a.code.toLowerCase().startsWith(q))
    const byName = usable.filter((a) => !a.code.toLowerCase().startsWith(q) && a.name.toLowerCase().includes(q))
    return [...byCode, ...byName].slice(0, MAX_RESULTS)
  }, [accounts, text, value])

  function choose(a: Account | undefined, viaKeyboard: boolean) {
    if (a) onChange(a.id)
    setOpen(false)
    setText('')
    if (a && viaKeyboard) onCommit?.()
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      setOpen(true)
      setActive((i) => {
        const n = options.length
        if (n === 0) return 0
        return e.key === 'ArrowDown' ? (i + 1) % n : (i - 1 + n) % n
      })
    } else if (e.key === 'Enter' && open) {
      e.preventDefault()
      choose(options[active], true)
    } else if (e.key === 'Tab' && open && text.trim() !== '' && options[active]) {
      choose(options[active], false)
    } else if (e.key === 'Escape' && open) {
      e.stopPropagation()
      setOpen(false)
      setText('')
    }
  }

  const shown = open ? text : selected ? `${selected.code} · ${selected.name}` : ''

  return (
    <div className={`relative min-w-0 ${className}`}>
      <input
        ref={input}
        id={id}
        role="combobox"
        aria-label={label}
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={open && options[active] ? `${listId}-${options[active].id}` : undefined}
        aria-invalid={invalid || undefined}
        autoComplete="off"
        spellCheck={false}
        placeholder={placeholder}
        value={shown}
        onFocus={() => {
          setOpen(true)
          setText('')
          setActive(Math.max(0, options.findIndex((a) => a.id === value)))
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
              style={{ ...listStyle, maxHeight: LIST_HEIGHT }}
              initial={{ opacity: 0, y: -4 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, transition: { duration: 0.1 } }}
              transition={{ duration: 0.15, ease }}
              className="z-50 overflow-y-auto rounded-[12px] border border-line bg-surface py-1 shadow-xl"
            >
              {options.length === 0 && <li className="px-3 py-2.5 text-sm text-muted">Tidak ada akun detail yang cocok.</li>}
              {options.map((a, i) => (
                <li
                  key={a.id}
                  id={`${listId}-${a.id}`}
                  role="option"
                  aria-selected={a.id === value}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    choose(a, false)
                  }}
                  onMouseEnter={() => setActive(i)}
                  className={`flex cursor-pointer items-baseline gap-3 px-3 py-2 text-sm ${i === active ? 'bg-hover' : ''}`}
                >
                  <span className="num w-[68px] shrink-0 font-semibold text-ink-2">{a.code}</span>
                  <span className="min-w-0 flex-1 truncate">{a.name}</span>
                  {a.id === value && <span className="text-xs font-semibold text-accent">dipilih</span>}
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

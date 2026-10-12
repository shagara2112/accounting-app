import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { ease } from './ui'

type Tone = 'good' | 'bad' | 'neutral'
type Item = { id: number; message: string; tone: Tone }

const ToastContext = createContext<(message: string, tone?: Tone) => void>(() => {})

/** Toast singkat setelah simpan atau posting (TDD: meluncur dengan ikon centang, 200 ms). */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Item[]>([])
  const push = useCallback((message: string, tone: Tone = 'good') => {
    const id = Date.now() + Math.random()
    setItems((list) => [...list.slice(-2), { id, message, tone }])
    setTimeout(() => setItems((list) => list.filter((t) => t.id !== id)), 4000)
  }, [])

  return (
    <ToastContext.Provider value={push}>
      {children}
      <div aria-live="polite" className="pointer-events-none fixed right-5 bottom-5 left-5 z-50 flex flex-col items-end gap-2">
        <AnimatePresence initial={false}>
          {items.map((t) => (
            <motion.div
              key={t.id}
              layout
              initial={{ opacity: 0, y: 16 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: 8, transition: { duration: 0.15 } }}
              transition={{ duration: 0.2, ease }}
              className="pointer-events-auto flex max-w-md items-center gap-2.5 rounded-[12px] bg-ink px-4 py-3 text-sm font-medium text-canvas shadow-lg"
            >
              <span className={`flex size-5 shrink-0 items-center justify-center rounded-full ${t.tone === 'bad' ? 'bg-bad' : t.tone === 'good' ? 'bg-good' : 'bg-muted'}`}>
                <svg width="12" height="12" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="white" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                  <path d={t.tone === 'bad' ? 'M6 6l12 12M18 6L6 18' : 'M5 12l5 5L20 7'} />
                </svg>
              </span>
              {t.message}
            </motion.div>
          ))}
        </AnimatePresence>
      </div>
    </ToastContext.Provider>
  )
}

export const useToast = () => useContext(ToastContext)

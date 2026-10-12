import { useEffect, useState } from 'react'

/** Nilai yang baru berubah setelah pengguna berhenti mengetik selama delay ms. */
export function useDebounced<T>(value: T, delay = 250): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), delay)
    return () => clearTimeout(t)
  }, [value, delay])
  return v
}

import { useEffect, useState, type FormEvent } from 'react'
import { ApiError } from '../lib/api'
import { useTotpEnable, useTotpSetup } from '../lib/admin'
import { qrMatrix, qrPath } from '../lib/qr'
import { Alert, Button, Field, Skeleton } from './ui'

/** Pemasangan 2FA: pindai QR (atau ketik kunci), lalu konfirmasi dengan kode pertama. */
export function MfaEnroll({ onDone }: { onDone: () => void }) {
  const setup = useTotpSetup()
  const enable = useTotpEnable()
  const [code, setCode] = useState('')
  const [attempt, setAttempt] = useState(0)

  // Kunci baru dibuat sekali saat komponen dibuka; membuka ulang membuat kunci baru.
  const start = setup.mutate
  useEffect(() => start(), [start])

  function submit(e: FormEvent) {
    e.preventDefault()
    setAttempt((n) => n + 1)
    enable.mutate(code, { onSuccess: onDone })
  }

  if (setup.isError) return <Alert>{setup.error.message}</Alert>
  if (!setup.data) return <Skeleton className="h-64" />
  const qr = qrMatrix(setup.data.uri)
  const path = qr ? qrPath(qr) : null
  const err = enable.error instanceof ApiError ? enable.error : null

  return (
    <div className="flex flex-col gap-5">
      <ol className="flex list-decimal flex-col gap-1.5 pl-5 text-sm text-ink-2">
        <li>Pasang aplikasi autentikator di ponsel, mis. Google Authenticator, Microsoft Authenticator, atau Authy.</li>
        <li>Pindai kode QR di bawah dari aplikasi itu.</li>
        <li>Masukkan 6 digit kode yang muncul di aplikasi.</li>
      </ol>
      <div className="flex flex-wrap items-center gap-5">
        {path && (
          <svg viewBox={`0 0 ${path.size} ${path.size}`} width={184} height={184} role="img" aria-label="Kode QR untuk aplikasi autentikator" className="shrink-0 rounded-[10px] border border-line bg-white" shapeRendering="crispEdges">
            <path d={path.d} fill="#111" />
          </svg>
        )}
        <div className="flex min-w-0 flex-1 flex-col gap-2 text-sm">
          <span className="text-muted">Tidak bisa memindai? Ketik kunci ini di aplikasi:</span>
          <code className="num rounded-[10px] bg-hover px-3 py-2 text-[13.5px] tracking-wide break-all select-all">{setup.data.key}</code>
          <a href={setup.data.uri} className="w-fit text-accent underline-offset-2 hover:underline">
            Buka di aplikasi autentikator (dari ponsel)
          </a>
        </div>
      </div>
      <form onSubmit={submit} noValidate className="flex flex-wrap items-end gap-3">
        <Field
          id="kode-2fa"
          label="Kode 6 digit"
          inputMode="numeric"
          autoComplete="one-time-code"
          maxLength={7}
          className="num w-40 tracking-[0.2em]"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          error={err?.fields.code}
        />
        <Button type="submit" busy={enable.isPending} disabled={code.replace(/\D/g, '').length !== 6}>
          Aktifkan 2FA
        </Button>
      </form>
      {err && !err.fields.code && <Alert shakeKey={attempt}>{err.message}</Alert>}
    </div>
  )
}

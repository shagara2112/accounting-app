import { useState } from 'react'
import { motion } from 'motion/react'
import { ApiError } from '../lib/api'
import { todayISO } from '../lib/dates'
import { Alert, Button, Field, TextAreaField, ease } from './ui'

/** Formulir void yang sama untuk semua dokumen: tanggal void dan alasan yang dibaca auditor.
 *  Dokumen asli tidak diubah; jurnalnya dibalik pada tanggal void. */
export function VoidForm({
  title,
  note,
  minDate,
  busy,
  error,
  onCancel,
  onConfirm,
}: {
  title: string
  note: string
  minDate: string
  busy: boolean
  error: unknown
  onCancel: () => void
  onConfirm: (date: string, reason: string) => void
}) {
  const [date, setDate] = useState(() => (todayISO() < minDate ? minDate : todayISO()))
  const [reason, setReason] = useState('')
  const [attempt, setAttempt] = useState(0)
  const err = error instanceof ApiError ? error : null

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.2, ease }}
      className="flex flex-col gap-4 rounded-[12px] border border-bad/30 bg-bad-soft/40 p-4"
    >
      <div>
        <h3 className="text-sm font-semibold">{title}</h3>
        <p className="mt-1 text-[13px] text-muted">{note}</p>
      </div>
      <Field id="void-date" label="Tanggal void" type="date" min={minDate} value={date} error={err?.fields.date} onChange={(e) => setDate(e.target.value)} />
      <TextAreaField
        id="void-reason"
        label="Alasan"
        data-autofocus
        value={reason}
        maxLength={300}
        placeholder="Mis. salah memilih pelanggan"
        hint="Minimal 5 karakter; dibaca auditor."
        error={err?.fields.reason}
        onChange={(e) => setReason(e.target.value)}
      />
      {err && !err.fields.date && !err.fields.reason && <Alert shakeKey={attempt}>{err.message}</Alert>}
      <div className="flex justify-end gap-2.5">
        <Button variant="secondary" onClick={onCancel}>
          Batal
        </Button>
        <Button
          variant="danger"
          busy={busy}
          disabled={reason.trim().length < 5}
          onClick={() => {
            setAttempt((n) => n + 1)
            onConfirm(date, reason.trim())
          }}
        >
          Void
        </Button>
      </div>
    </motion.div>
  )
}

import { useState } from 'react'
import { useNavigate } from 'react-router'
import { ApiError, newIdempotencyKey, type Me } from '../lib/api'
import { useDailySale, useVoidDailySale } from '../lib/daily'
import { formatDate, formatDateTime, monthOf } from '../lib/dates'
import { formatQty } from '../lib/inventory'
import { formatAmount } from '../lib/money'
import { can } from '../lib/session'
import { useToast } from './Toast'
import { Alert, Badge, Button, Drawer, Skeleton } from './ui'
import { VoidForm } from './VoidForm'

/** Detail rekap penjualan harian retail (IND-07): baris barang, penerimaan, dan void. */
export function DailySaleDrawer({ me, id, onClose }: { me: Me; id: string | null; onClose: () => void }) {
  const companyId = me.activeCompanyId
  const q = useDailySale(companyId, id)
  const d = q.data && q.data.id === id ? q.data : undefined
  const navigate = useNavigate()
  const toast = useToast()
  const voidIt = useVoidDailySale(companyId)
  const [voiding, setVoiding] = useState(false)
  const [key, setKey] = useState(newIdempotencyKey)
  const err = voidIt.error instanceof ApiError ? voidIt.error : null

  function close() {
    setVoiding(false)
    voidIt.reset()
    onClose()
  }

  return (
    <Drawer
      open={id !== null}
      onClose={close}
      title={d?.number ?? 'Memuat…'}
      description={
        d && (
          <span className="flex flex-wrap items-center gap-2">
            {d.status === 'void' ? <Badge tone="bad">Void</Badge> : <Badge tone="good">Diposting</Badge>}
            <span>
              {d.store || 'Toko'} · {formatDate(d.date)}
            </span>
          </span>
        )
      }
      footer={
        d?.status === 'posted' &&
        can(me, 'sales.void') &&
        !voiding && (
          <Button
            variant="secondary"
            onClick={() => {
              setKey(newIdempotencyKey())
              voidIt.reset()
              setVoiding(true)
            }}
          >
            Void…
          </Button>
        )
      }
    >
      {!d && <Skeleton className="h-40" />}
      {d && (
        <>
          <p className="text-sm text-muted">
            Dicatat {d.createdBy ?? 'Sistem'} · {formatDateTime(d.createdAt)}. Harga termasuk PPN; PPN-nya dilaporkan sebagai PPN digunggung.
          </p>
          {d.status === 'void' && d.voidReason && <Alert tone="bad">Di-void oleh {d.voidedBy ?? 'Sistem'}: {d.voidReason}</Alert>}
          <div className="overflow-hidden rounded-[12px] border border-line">
            <table className="w-full text-left text-sm">
              <thead className="bg-hover/60 text-[12.5px] text-muted">
                <tr>
                  <th className="px-3 py-2.5 font-semibold">Barang</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Qty</th>
                  <th className="px-3 py-2.5 text-right font-semibold">Penjualan</th>
                  <th className="px-3 py-2.5 text-right font-semibold">PPN</th>
                </tr>
              </thead>
              <tbody>
                {d.lines.map((l, i) => (
                  <tr key={i} className="border-t border-line align-top">
                    <td className="px-3 py-2.5">
                      <span className="block">{l.description || l.accountName}</span>
                      <span className="block text-[12.5px] text-muted">
                        <span className="num">{l.accountCode}</span> {l.accountName} · {l.taxCode}
                      </span>
                    </td>
                    <td className="num px-3 py-2.5 text-right">{formatQty(l.qty)}</td>
                    <td className="num px-3 py-2.5 text-right">{formatAmount(l.amount)}</td>
                    <td className="num px-3 py-2.5 text-right">{formatAmount(l.vat)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="ml-auto w-full max-w-[340px] text-sm">
            {[
              ['DPP', d.dpp],
              ['PPN digunggung', d.vat],
            ].map(([k, v]) => (
              <div key={k} className="flex justify-between gap-4 py-1">
                <span>{k}</span>
                <span className="num">{formatAmount(v)}</span>
              </div>
            ))}
            <div className="flex justify-between gap-4 border-t border-line-2 py-1 pt-2 font-semibold">
              <span>Total diterima</span>
              <span className="num">{formatAmount(d.total)}</span>
            </div>
            {d.payments.map((p) => (
              <div key={p.accountId} className="flex justify-between gap-4 py-0.5 text-[13px] text-muted">
                <span>
                  {p.accountCode} {p.accountName}
                </span>
                <span className="num">{formatAmount(p.amount)}</span>
              </div>
            ))}
          </div>
          <button
            type="button"
            onClick={() => navigate(`/jurnal?bulan=${monthOf(d.date)}&id=${d.journalEntryId}`)}
            className="self-start text-sm font-semibold text-accent hover:underline"
          >
            Lihat jurnal {d.number} →
          </button>
          {voiding && (
            <VoidForm
              title={`Void ${d.number}`}
              note="Jurnalnya dibalik pada tanggal void dan barangnya kembali ke stok. Setelah itu rekap tanggal dan toko yang sama boleh diimpor ulang."
              minDate={d.date}
              busy={voidIt.isPending}
              error={err}
              onCancel={() => setVoiding(false)}
              onConfirm={(date, reason) =>
                voidIt.mutate({ id: d.id, date, reason, idempotencyKey: key }, { onSuccess: (v) => (toast(`${v.number} di-void`), setVoiding(false)) })
              }
            />
          )}
        </>
      )}
    </Drawer>
  )
}

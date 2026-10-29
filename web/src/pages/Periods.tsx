import { useState, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiError, type Me, type Period, type PeriodStatus } from '../lib/api'
import { formatDate, formatDateTime, monthLabel, monthName, monthOf, todayISO } from '../lib/dates'
import { useCloseYear, useLedgerSettings, usePeriods, useReopenYear, useSetPeriodStatus, useUpdateLedgerSettings, useYearEnd, type YearEnd } from '../lib/ledger'
import { formatAmount } from '../lib/money'
import { periodStatus } from '../lib/labels'
import { can } from '../lib/session'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Dialog, SelectField, Skeleton, TableShell, TextAreaField, td, th, type Tone } from '../components/ui'

const order: PeriodStatus[] = ['open', 'adjusting', 'locked']

type Pending = { period: Period; to: PeriodStatus }

export function Periods({ me }: { me: Me }) {
  const companyId = me.activeCompanyId
  const [params, setParams] = useSearchParams()
  const year = Number(params.get('tahun')) || Number(todayISO().slice(0, 4))
  const periods = usePeriods(companyId, year)
  const settings = useLedgerSettings(companyId)
  const setStatus = useSetPeriodStatus(companyId)
  const toast = useToast()
  const manage = can(me, 'ledger.period.manage')
  const [pending, setPending] = useState<Pending | null>(null)

  const fy = periods.data
  const startMonth = fy?.startMonth ?? 1
  const yearLabel = startMonth === 1 ? `Tahun buku ${year}` : `Tahun buku ${monthName(startMonth)} ${year} – ${monthName(((startMonth + 10) % 12) + 1)} ${year + 1}`

  function move(p: Period, to: PeriodStatus) {
    // Maju (menutup) langsung jalan; mundur (membuka kembali) dan kunci final minta konfirmasi.
    if (to === 'adjusting' && p.status === 'open') {
      setStatus.mutate(
        { month: p.month, status: to, reason: '' },
        { onSuccess: () => toast(`${monthLabel(p.month)} ditutup untuk transaksi`), onError: (e) => toast(e.message, 'bad') },
      )
      return
    }
    setPending({ period: p, to })
  }

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-[26px] font-bold tracking-tight">Periode dan tutup buku</h1>
        <p className="mt-1.5 max-w-2xl text-sm text-muted">
          Tutup buku dua tahap: periode ditutup dulu untuk transaksi supaya Staf akuntansi bisa memasukkan jurnal penyesuaian, lalu Manager menguncinya secara final. Membuka kembali wajib disertai alasan dan tercatat di audit trail.
        </p>
      </div>

      <ol className="flex flex-wrap items-stretch gap-2" aria-label="Tahap tutup buku">
        {order.map((s, i) => (
          <li key={s} className="flex items-center gap-2">
            <div className="flex min-h-[64px] max-w-[260px] flex-col justify-center gap-1 rounded-[12px] border border-line bg-surface px-4 py-2.5">
              <Badge tone={periodStatus[s].tone} className="self-start">
                {i + 1}. {periodStatus[s].label}
              </Badge>
              <span className="text-[13px] text-muted">{periodStatus[s].help}</span>
            </div>
            {i < order.length - 1 && (
              <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="var(--color-line-2)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M5 12h14M13 6l6 6-6 6" />
              </svg>
            )}
          </li>
        ))}
      </ol>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="inline-flex items-center gap-1 rounded-[10px] border border-line-2 bg-surface p-1" role="group" aria-label="Tahun buku">
          <YearButton label="Tahun sebelumnya" d="M15 6l-6 6 6 6" onClick={() => setParams({ tahun: String(year - 1) }, { replace: true })} />
          <span aria-live="polite" className="px-2 text-sm font-semibold">
            {yearLabel}
          </span>
          <YearButton label="Tahun berikutnya" d="M9 6l6 6-6 6" onClick={() => setParams({ tahun: String(year + 1) }, { replace: true })} />
        </div>
        {settings.data && <FiscalStart me={me} startMonth={settings.data.fiscalYearStartMonth} locked={settings.data.hasEntries} />}
      </div>

      <div className={`transition-opacity duration-150 ${periods.isPlaceholderData ? 'opacity-60' : ''}`}>
        <TableShell minWidth={820}>
          <thead className="bg-hover/60">
            <tr>
              <th className={th}>Bulan</th>
              <th className={th}>Status</th>
              <th className={`${th} text-right`}>Jurnal</th>
              <th className={`${th} text-right`}>Penyesuaian</th>
              <th className={th}>Perubahan terakhir</th>
              {manage && (
                <th className={`${th} text-right`}>
                  <span className="sr-only">Aksi</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {periods.isPending &&
              Array.from({ length: 6 }, (_, i) => (
                <tr key={i} className="border-t border-line">
                  <td colSpan={6} className={td}>
                    <Skeleton className="h-5" />
                  </td>
                </tr>
              ))}
            {fy?.periods.map((p) => (
              <tr key={p.month} className="border-t border-line">
                <td className={`${td} font-semibold`}>{monthLabel(p.month)}</td>
                <td className={td}>
                  <Badge tone={periodStatus[p.status].tone}>
                    {p.status === 'locked' && (
                      <svg width="11" height="11" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round">
                        <path d="M6 11h12v10H6zM8 11V7a4 4 0 0 1 8 0v4" />
                      </svg>
                    )}
                    {periodStatus[p.status].label}
                  </Badge>
                </td>
                <td className={`${td} num text-right`}>{p.entries || '—'}</td>
                <td className={`${td} num text-right`}>{p.adjustments || '—'}</td>
                <td className={`${td} text-[13px] text-muted`}>
                  {p.changedAt ? (
                    <>
                      {p.changedBy ?? 'Sistem'} · {formatDateTime(p.changedAt)}
                      {p.reason && <span className="block text-ink-2">“{p.reason}”</span>}
                    </>
                  ) : (
                    '—'
                  )}
                </td>
                {manage && (
                  <td className={`${td} py-1.5 text-right`}>
                    <span className="inline-flex flex-wrap justify-end gap-1.5">
                      {p.status === 'open' && (
                        <Button size="sm" variant="secondary" busy={setStatus.isPending && setStatus.variables?.month === p.month} onClick={() => move(p, 'adjusting')}>
                          Tutup untuk transaksi
                        </Button>
                      )}
                      {p.status === 'adjusting' && (
                        <>
                          <Button size="sm" variant="ghost" onClick={() => move(p, 'open')}>
                            Buka kembali
                          </Button>
                          <Button size="sm" onClick={() => move(p, 'locked')}>
                            Kunci final
                          </Button>
                        </>
                      )}
                      {p.status === 'locked' && (
                        <Button size="sm" variant="ghost" onClick={() => move(p, 'adjusting')}>
                          Buka kembali
                        </Button>
                      )}
                    </span>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </TableShell>
      </div>
      {!manage && <p className="text-sm text-muted">Hanya Pemilik dan Manager yang bisa menutup, mengunci, dan membuka periode.</p>}

      <YearEndCard key={year} me={me} year={year} label={startMonth === 1 ? String(year) : `${year}/${String(year + 1).slice(2)}`} />

      <StatusDialog me={me} pending={pending} onClose={() => setPending(null)} />
    </div>
  )
}

function StatusDialog({ me, pending, onClose }: { me: Me; pending: Pending | null; onClose: () => void }) {
  const setStatus = useSetPeriodStatus(me.activeCompanyId)
  const toast = useToast()
  const [reason, setReason] = useState('')
  const [to, setTo] = useState<PeriodStatus>('adjusting')
  const [shownFor, setShownFor] = useState<Pending | null>(null)
  const [attempt, setAttempt] = useState(0)

  if (pending && shownFor !== pending) {
    setShownFor(pending)
    setReason('')
    setTo(pending.to)
    setStatus.reset()
  }
  // shownFor tetap diisi setelah ditutup, supaya isi dialog tidak hilang selama animasi keluar.
  const current = pending ?? shownFor
  if (!current) return null

  const { period } = current
  const reopening = order.indexOf(to) < order.indexOf(period.status)
  const label = monthLabel(period.month)
  const err = setStatus.error instanceof ApiError ? setStatus.error : null

  function submit() {
    setAttempt((n) => n + 1)
    setStatus.mutate(
      { month: period.month, status: to, reason: reason.trim() },
      {
        onSuccess: () => {
          toast(reopening ? `${label} dibuka kembali (${periodStatus[to].label.toLowerCase()})` : `${label} dikunci final`)
          onClose()
        },
      },
    )
  }

  return (
    <Dialog
      open={pending !== null}
      onClose={onClose}
      title={reopening ? `Buka kembali ${label}?` : `Kunci final ${label}?`}
      description={
        reopening
          ? 'Alasan wajib diisi dan tercatat di audit trail. Kunci kembali setelah koreksi selesai.'
          : 'Setelah dikunci, tidak ada jurnal apa pun yang bisa masuk ke periode ini sampai dibuka kembali dengan alasan.'
      }
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Batal
          </Button>
          <Button busy={setStatus.isPending} disabled={reopening && reason.trim().length < 5} onClick={submit}>
            {reopening ? 'Buka kembali' : 'Kunci final'}
          </Button>
        </>
      }
    >
      {reopening && period.status === 'locked' && (
        <SelectField id="periode-ke" label="Buka ke tahap" value={to} onChange={(e) => setTo(e.target.value as PeriodStatus)}>
          <option value="adjusting">Penyesuaian: hanya jurnal penyesuaian</option>
          <option value="open">Terbuka: semua jurnal</option>
        </SelectField>
      )}
      {reopening && (
        <TextAreaField
          id="periode-alasan"
          label="Alasan"
          data-autofocus
          value={reason}
          maxLength={300}
          placeholder="Mis. koreksi penyusutan dari auditor"
          onChange={(e) => setReason(e.target.value)}
          error={err?.fields.reason}
        />
      )}
      {err && !err.fields.reason && <Alert shakeKey={attempt}>{err.message}</Alert>}
    </Dialog>
  )
}

function FiscalStart({ me, startMonth, locked }: { me: Me; startMonth: number; locked: boolean }) {
  const update = useUpdateLedgerSettings(me.activeCompanyId)
  const toast = useToast()
  const editable = can(me, 'ledger.period.manage') && !locked
  if (!editable) {
    return (
      <p className="text-sm text-muted">
        Tahun buku mulai <span className="font-semibold text-ink">{monthName(startMonth)}</span>
        {locked && ' · tidak bisa diubah setelah ada jurnal'}
      </p>
    )
  }
  return (
    <div className="flex items-center gap-2">
      <label htmlFor="awal-tahun-buku" className="text-sm text-muted">
        Tahun buku mulai
      </label>
      <select
        id="awal-tahun-buku"
        value={startMonth}
        disabled={update.isPending}
        onChange={(e) => update.mutate(Number(e.target.value), { onSuccess: () => toast('Awal tahun buku disimpan'), onError: (er) => toast(er.message, 'bad') })}
        className="min-h-11 cursor-pointer rounded-[10px] border border-line-2 bg-surface px-3 text-sm"
      >
        {Array.from({ length: 12 }, (_, i) => (
          <option key={i + 1} value={i + 1}>
            {monthName(i + 1)}
          </option>
        ))}
      </select>
    </div>
  )
}

function YearButton({ label, d, onClick }: { label: string; d: string; onClick: () => void }) {
  return (
    <button type="button" aria-label={label} onClick={onClick} className="flex size-9 cursor-pointer items-center justify-center rounded-[8px] text-ink-2 transition-colors hover:bg-hover">
      <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d={d} />
      </svg>
    </button>
  )
}

const yearStatus: Record<YearEnd['status'], { label: string; tone: Tone }> = {
  kosong: { label: 'Tidak ada laba rugi', tone: 'neutral' },
  belum: { label: 'Belum direklas', tone: 'warn' },
  sudah: { label: 'Sudah direklas', tone: 'good' },
  berubah: { label: 'Perlu diperbarui', tone: 'bad' },
}

/**
 * Reklas laba (rugi) akhir tahun ke laba ditahan (GL-10). Jurnal penutupnya bertanggal akhir tahun
 * buku dan bisa diperbarui atau dibatalkan selama bulan terakhirnya belum dikunci final.
 */
function YearEndCard({ me, year, label }: { me: Me; year: number; label: string }) {
  const companyId = me.activeCompanyId
  const ye = useYearEnd(companyId, year)
  const close = useCloseYear(companyId)
  const toast = useToast()
  const manage = can(me, 'ledger.period.manage')
  const [reopen, setReopen] = useState(false)
  const y = ye.data
  const locked = y?.periodStatus === 'locked'
  const lastMonth = y ? monthLabel(monthOf(y.to)) : ''
  const blocked = !y ? 'loading' : !y.ended ? `Bisa dilakukan setelah tahun buku berakhir (${formatDate(y.to)}).` : locked ? `Periode ${lastMonth} sudah dikunci final; buka dulu periode itu untuk mengubah reklas.` : ''

  function run() {
    close.mutate(year, {
      onSuccess: (r) => toast(`Laba ${label} direklas ke laba ditahan · ${r.closing?.number ?? ''}`),
      onError: (e) => toast(e.message, 'bad'),
    })
  }

  return (
    <Card
      title={`Reklas laba akhir tahun ${label}`}
      subtitle="Memindahkan laba (rugi) tahun buku ke laba ditahan lewat jurnal penutup bertanggal akhir tahun buku. Laporan tahun itu tidak berubah; mulai tahun berikutnya laba itu tampil sebagai laba ditahan. Selama bulan terakhirnya belum dikunci final, revisi tetap bisa masuk dan reklas tinggal diperbarui atau dibatalkan."
    >
      {ye.isPending && <Skeleton className="h-16" />}
      {ye.error && <Alert>{ye.error.message}</Alert>}
      {y && (
        <div className={`flex flex-col gap-3 transition-opacity duration-150 ${ye.isPlaceholderData ? 'opacity-60' : ''}`}>
          <div className="flex flex-wrap items-end justify-between gap-4">
            <dl className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm sm:grid-cols-3">
              <Info label={`Laba (rugi) ${label}`}>
                <span className={`num text-base font-semibold ${y.profit.startsWith('-') ? 'text-bad' : ''}`}>Rp {formatAmount(y.profit)}</span>
              </Info>
              <Info label="Status">
                <Badge tone={yearStatus[y.status].tone}>{yearStatus[y.status].label}</Badge>
              </Info>
              <Info label="Ke akun">{y.account ? `${y.account.number} ${y.accountName}` : '—'}</Info>
            </dl>
            {manage && y.status !== 'kosong' && (
              <div className="flex flex-wrap gap-2">
                {y.closing && (
                  <Button variant="ghost" disabled={locked} onClick={() => setReopen(true)}>
                    Batalkan reklas
                  </Button>
                )}
                {y.status !== 'sudah' && (
                  <Button busy={close.isPending} disabled={!!blocked || !!y.accountProblem} onClick={run}>
                    {y.status === 'berubah' ? 'Perbarui reklas' : 'Reklas ke laba ditahan'}
                  </Button>
                )}
              </div>
            )}
          </div>

          {y.closing && (
            <p className="text-[13px] text-muted">
              Jurnal{' '}
              <Link to={`/jurnal?bulan=${monthOf(y.to)}&id=${y.closing.id}`} className="num font-semibold text-accent hover:underline">
                {y.closing.number}
              </Link>{' '}
              tanggal {formatDate(y.to)} mereklas Rp {formatAmount(y.closing.profit)}
              {y.closing.postedByName && ` · oleh ${y.closing.postedByName}`} · {formatDateTime(y.closing.postedAt)}
            </p>
          )}
          {y.status === 'berubah' && y.closing && (
            <Alert tone="warn">
              Buku {label} berubah sesudah reklas: laba sekarang Rp {formatAmount(y.profit)}, yang sudah direklas Rp {formatAmount(y.closing.profit)}.
              {manage ? ' Tekan Perbarui reklas supaya laba ditahan sesuai.' : ' Minta Manager memperbarui reklas.'}
            </Alert>
          )}
          {y.status === 'kosong' && <p className="text-[13px] text-muted">Belum ada pendapatan atau beban di tahun buku ini.</p>}
          {y.status !== 'kosong' && y.status !== 'sudah' && blocked && blocked !== 'loading' && <p className="text-[13px] text-muted">{blocked}</p>}
          {y.status === 'sudah' && locked && <p className="text-[13px] text-muted">Periode {lastMonth} sudah dikunci final, jadi reklas ini sudah final.</p>}
          {y.status !== 'kosong' && y.accountProblem && (
            <Alert tone="warn">
              {y.accountProblem}{' '}
              <Link to="/akun/sistem" className="font-semibold underline">
                Buka Akun sistem
              </Link>
            </Alert>
          )}
        </div>
      )}
      {reopen && y && <ReopenDialog me={me} year={year} label={label} onClose={() => setReopen(false)} />}
    </Card>
  )
}

function Info({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <dt className="text-[12.5px] font-medium text-muted">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  )
}

function ReopenDialog({ me, year, label, onClose }: { me: Me; year: number; label: string; onClose: () => void }) {
  const reopen = useReopenYear(me.activeCompanyId)
  const toast = useToast()
  const [reason, setReason] = useState('')
  const fields = reopen.error instanceof ApiError ? reopen.error.fields : {}
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Batalkan reklas ${label}?`}
      description="Jurnal reklas di-void dengan jurnal pembalik di tanggal yang sama. Laba tahun itu kembali tampil sebagai laba tahun lalu yang belum ditutup sampai direklas lagi."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Kembali
          </Button>
          <Button
            variant="danger"
            busy={reopen.isPending}
            onClick={() => reopen.mutate({ year, reason }, { onSuccess: () => (toast(`Reklas ${label} dibatalkan`), onClose()) })}
          >
            Batalkan reklas
          </Button>
        </>
      }
    >
      <TextAreaField id="reopen-reason" label="Alasan" placeholder="mis. Menunggu koreksi auditor" value={reason} maxLength={300} onChange={(e) => setReason(e.target.value)} error={fields.reason} data-autofocus />
      {reopen.error && !fields.reason && <Alert shakeKey={reopen.failureCount}>{reopen.error.message}</Alert>}
    </Dialog>
  )
}

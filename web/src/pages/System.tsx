import { useEffect, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { ApiError, type Me } from '../lib/api'
import { formatDate, formatDateTime } from '../lib/dates'
import { useLicense } from '../lib/queries'
import { can } from '../lib/session'
import {
  healthVersion,
  useApplyUpdate,
  useBackups,
  useLicenseDetail,
  useMail,
  useSaveMail,
  useTestMail,
  useUpdateCheck,
  useUpdateState,
  usePaymentInfo,
  useRecheckLicense,
  useReplaceLicense,
  useRunBackup,
  useSaveBackupSettings,
  useSubmitPayment,
  type BackupRecord,
  type BackupSettings,
  type LicenseDetail,
  type MailSettings,
  type PaymentInfo,
} from '../lib/system'
import { useToast } from '../components/Toast'
import { Alert, Badge, Button, Card, Checkbox, Dialog, EmptyState, Field, PageHeader, SelectField, Skeleton, TableShell, Tabs, TextAreaField, td, th } from '../components/ui'

const licenseTone = { ACTIVE: 'good', WARNING: 'warn', UNVERIFIED: 'warn', EXPIRED: 'bad', SUSPENDED: 'bad', INVALID_KEY: 'bad' } as const
const licenseLabel = { ACTIVE: 'Aktif', WARNING: 'Segera berakhir', UNVERIFIED: 'Belum terverifikasi', EXPIRED: 'Berakhir', SUSPENDED: 'Ditangguhkan', INVALID_KEY: 'Key tidak valid' } as const

type Tab = 'lisensi' | 'backup' | 'email' | 'pembaruan'

/** Sistem: lisensi dan perpanjangannya, serta backup. Tetap terbuka walau lisensi berakhir. */
export function SystemPage({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams()
  const raw = params.get('tab')
  const tab: Tab = raw === 'backup' || raw === 'email' || raw === 'pembaruan' ? raw : 'lisensi'
  if (!can(me, 'platform.system.manage')) return <EmptyState title="Anda tidak punya izin mengelola sistem" />
  return (
    <div className="flex flex-col gap-5">
      <PageHeader title="Sistem" subtitle="Lisensi, backup, email, dan pembaruan untuk seluruh grup. Halaman ini tetap bisa dibuka walau lisensi berakhir atau ditangguhkan." />
      <Tabs
        label="Bagian sistem"
        value={tab}
        onChange={(v) => setParams(v === 'lisensi' ? {} : { tab: v }, { replace: true })}
        tabs={[
          { value: 'lisensi', label: 'Lisensi' },
          { value: 'backup', label: 'Backup' },
          { value: 'email', label: 'Email' },
          { value: 'pembaruan', label: 'Pembaruan' },
        ]}
      />
      {tab === 'lisensi' ? <LicenseSection /> : tab === 'backup' ? <BackupSection /> : tab === 'email' ? <MailSection me={me} /> : <UpdateSection />}
    </div>
  )
}

function LicenseSection() {
  const status = useLicense()
  const info = useLicenseDetail(true)
  const recheck = useRecheckLicense()
  const toast = useToast()
  const [replacing, setReplacing] = useState(false)
  const st = status.data
  const remote = info.data?.mode === 'remote' ? info.data.detail : null

  return (
    <>
      <Card
        title="Lisensi"
        subtitle={st?.message}
        actions={st && <Badge tone={st.updateRequired ? 'bad' : licenseTone[st.status]}>{st.updateRequired ? 'Wajib perbarui' : licenseLabel[st.status]}</Badge>}
      >
        {!info.data ? (
          <Skeleton className="h-24" />
        ) : !remote ? (
          <p className="text-sm text-ink-2">
            Server ini memakai lisensi pengembangan (mode <span className="num">local</span>) dan tidak menghubungi License Console. Server pelanggan selalu memakai mode remote.
          </p>
        ) : (
          <div className="flex flex-col gap-4">
            <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2">
              <Item label="Berlaku sampai">{remote.expiresAt ? formatDate(remote.expiresAt) : '—'}{st?.daysLeft != null && st.daysLeft >= 0 && <span className="text-muted"> · sisa {st.daysLeft} hari</span>}</Item>
              <Item label="Client ID">{remote.clientId}</Item>
              <Item label="Check-in terakhir">{remote.checkedAt ? formatDateTime(remote.checkedAt) : 'Belum pernah'}</Item>
              <Item label="Token berlaku s.d.">{remote.tokenValidTo ? formatDateTime(remote.tokenValidTo) : '—'}</Item>
              <Item label="Versi Lajur">
                <span className="num">{remote.version}</span>
                {remote.minVersion && <span className="text-muted"> · minimum {remote.minVersion}</span>}
              </Item>
              <Item label="ID instalasi">
                <span className="num" title={remote.installationId}>
                  {remote.installationId.slice(0, 12)}…
                </span>
              </Item>
              <Item label="License Console" wide>
                <span className="break-all text-ink-2">{remote.consoleUrl}</span>
              </Item>
            </dl>
            {remote.errorMessage && (
              <Alert tone="warn">
                Check-in terakhir ({remote.attemptedAt ? formatDateTime(remote.attemptedAt) : '—'}) gagal: {remote.errorMessage}
              </Alert>
            )}
            {recheck.error && <Alert>{recheck.error.message}</Alert>}
            <div className="flex flex-wrap gap-2">
              <Button
                variant="secondary"
                busy={recheck.isPending}
                onClick={() =>
                  recheck.mutate(undefined, {
                    onSuccess: (r) => (r.ok ? toast('Lisensi diperiksa ulang') : toast(r.message ?? 'Check-in gagal')),
                  })
                }
              >
                Cek ulang lisensi
              </Button>
              <Button variant="ghost" onClick={() => setReplacing(true)}>
                Ganti license key
              </Button>
            </div>
          </div>
        )}
      </Card>
      {remote && <Renewal />}
      {remote && <ReplaceDialog open={replacing} detail={remote} onClose={() => setReplacing(false)} />}
    </>
  )
}

function Item({ label, children, wide }: { label: string; children: ReactNode; wide?: boolean }) {
  return (
    <div className={wide ? 'sm:col-span-2' : ''}>
      <dt className="text-[12.5px] text-muted">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  )
}

const rupiah = (n: number) => 'Rp ' + n.toLocaleString('id-ID')

const requestTone = { PENDING: 'warn', APPROVED: 'good', REJECTED: 'bad', CANCELLED: 'neutral' } as const
const requestLabel = { PENDING: 'Menunggu verifikasi', APPROVED: 'Disetujui', REJECTED: 'Ditolak', CANCELLED: 'Dibatalkan' } as const

/** Perpanjangan (LIC-07): bayar lewat tautan paket, lalu unggah bukti bayar untuk diverifikasi pemilik aplikasi. */
function Renewal() {
  const info = usePaymentInfo(true)
  const [paying, setPaying] = useState(false)
  if (info.isPending) return <Skeleton className="h-32" />
  if (info.isError) return <Alert tone="warn">Paket perpanjangan tidak bisa dimuat: {info.error.message}</Alert>
  const p = info.data
  return (
    <Card title="Perpanjang lisensi" subtitle="Pilih paket dan bayar lewat tautannya, lalu unggah bukti bayar. Setelah disetujui, tekan Cek ulang lisensi.">
      <div className="flex flex-col gap-4">
        {p.latest && (
          <Alert tone={requestTone[p.latest.status]}>
            Bukti bayar {p.latest.packageName} ({rupiah(p.latest.amount)}, {formatDateTime(p.latest.submittedAt)}): {requestLabel[p.latest.status]}
            {p.latest.status === 'APPROVED' && p.latest.newExpiresAt && ` · berlaku sampai ${formatDate(p.latest.newExpiresAt)}`}
            {p.latest.status === 'REJECTED' && p.latest.reviewReason && ` · ${p.latest.reviewReason}`}
          </Alert>
        )}
        {!p.enabled ? (
          <p className="text-sm text-ink-2">
            Belum ada paket perpanjangan online. Hubungi pemilik aplikasi{p.contactWhatsApp && <> lewat WhatsApp {p.contactWhatsApp}</>}.
          </p>
        ) : (
          <>
            <div className="grid gap-3 sm:grid-cols-[repeat(auto-fit,minmax(180px,1fr))]">
              {p.packages.map((pkg) => (
                <div key={pkg.id} className="flex flex-col gap-2 rounded-[12px] border border-line p-4">
                  <span className="text-sm font-semibold">{pkg.name}</span>
                  <span className="num text-lg font-semibold">{rupiah(pkg.price)}</span>
                  <span className="text-[12.5px] text-muted">{pkg.months} bulan</span>
                  <a href={pkg.link} target="_blank" rel="noopener noreferrer" className="mt-1 w-fit text-sm font-semibold text-accent underline-offset-2 hover:underline">
                    Bayar paket ini ↗
                  </a>
                </div>
              ))}
            </div>
            {p.instructions && <p className="text-sm whitespace-pre-line text-ink-2">{p.instructions}</p>}
            <Button className="w-fit" onClick={() => setPaying(true)} disabled={p.latest?.status === 'PENDING'}>
              Unggah bukti bayar
            </Button>
          </>
        )}
      </div>
      <PaymentDialog open={paying} info={p} onClose={() => setPaying(false)} />
    </Card>
  )
}

const MAX_PROOF = 6 * 1024 * 1024

function PaymentDialog({ open, info, onClose }: { open: boolean; info: PaymentInfo; onClose: () => void }) {
  const submit = useSubmitPayment()
  const toast = useToast()
  const blank = { packageId: info.packages[0]?.id ?? '', amount: String(info.packages[0]?.price ?? ''), payerName: '', reference: '', note: '' }
  const [v, setV] = useState(blank)
  const [file, setFile] = useState<File | null>(null)
  const [fileError, setFileError] = useState('')
  const err = submit.error instanceof ApiError ? submit.error : null

  function close() {
    setV(blank)
    setFile(null)
    setFileError('')
    submit.reset()
    onClose()
  }

  function pick(f: File | null) {
    setFileError('')
    if (f && !['image/jpeg', 'image/png', 'image/webp', 'application/pdf'].includes(f.type)) setFileError('Bukti bayar harus gambar JPG/PNG/WEBP atau PDF.')
    else if (f && f.size > MAX_PROOF) setFileError('Ukuran bukti bayar maksimal 6 MB.')
    setFile(f)
  }

  async function send() {
    if (!file) return
    const buf = new Uint8Array(await file.arrayBuffer())
    let bin = ''
    for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000))
    submit.mutate(
      { ...v, amount: Number(v.amount.replace(/\D/g, '')), fileBase64: btoa(bin), mimeType: file.type },
      {
        onSuccess: () => {
          toast('Bukti bayar terkirim · menunggu verifikasi')
          close()
        },
      },
    )
  }

  return (
    <Dialog
      open={open}
      onClose={close}
      width={520}
      title="Unggah bukti bayar"
      description="Pemilik aplikasi memverifikasi bukti bayar, lalu memperpanjang lisensi sesuai paket."
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button busy={submit.isPending} disabled={!file || !!fileError || !v.payerName.trim() || !v.packageId || !Number(v.amount.replace(/\D/g, ''))} onClick={() => void send()}>
            Kirim
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <SelectField
          id="pay-paket"
          label="Paket"
          value={v.packageId}
          onChange={(e) => setV({ ...v, packageId: e.target.value, amount: String(info.packages.find((p) => p.id === e.target.value)?.price ?? v.amount) })}
        >
          {info.packages.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name} · {rupiah(p.price)}
            </option>
          ))}
        </SelectField>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="pay-nominal" label="Nominal dibayar" inputMode="numeric" className="num text-right" value={v.amount} onChange={(e) => setV({ ...v, amount: e.target.value })} />
          <Field id="pay-nama" label="Nama pembayar" value={v.payerName} onChange={(e) => setV({ ...v, payerName: e.target.value })} />
        </div>
        <Field id="pay-ref" label="No. referensi / ID order (opsional)" value={v.reference} onChange={(e) => setV({ ...v, reference: e.target.value })} />
        <TextAreaField id="pay-catatan" label="Catatan (opsional)" rows={2} value={v.note} onChange={(e) => setV({ ...v, note: e.target.value })} />
        <div className="flex flex-col gap-1.5">
          <label htmlFor="pay-bukti" className="text-sm font-medium">
            Bukti bayar
          </label>
          <input id="pay-bukti" type="file" accept="image/jpeg,image/png,image/webp,application/pdf" onChange={(e) => pick(e.target.files?.[0] ?? null)} className="text-sm file:mr-3 file:cursor-pointer file:rounded-[10px] file:border file:border-line-2 file:bg-surface file:px-3 file:py-2 file:text-sm" />
          {fileError && <p className="text-[13px] text-bad">{fileError}</p>}
        </div>
        {err && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

function ReplaceDialog({ open, detail, onClose }: { open: boolean; detail: LicenseDetail; onClose: () => void }) {
  const replace = useReplaceLicense()
  const toast = useToast()
  const blank = { consoleUrl: detail.consoleUrl ?? '', clientId: detail.clientId ?? '', licenseKey: '' }
  const [v, setV] = useState(blank)
  const err = replace.error instanceof ApiError ? replace.error : null
  function close() {
    setV(blank)
    replace.reset()
    onClose()
  }
  return (
    <Dialog
      open={open}
      onClose={close}
      width={520}
      title="Ganti license key"
      description="Dipakai setelah pemilik aplikasi mereset license key atau memindahkan lisensi ke server ini."
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            Batal
          </Button>
          <Button
            busy={replace.isPending}
            disabled={!v.consoleUrl || !v.clientId || !v.licenseKey}
            onClick={() =>
              replace.mutate(v, {
                onSuccess: () => {
                  toast('Lisensi diaktifkan dengan key baru')
                  close()
                },
              })
            }
          >
            Aktifkan
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field id="ganti-url" label="Alamat License Console" value={v.consoleUrl} onChange={(e) => setV({ ...v, consoleUrl: e.target.value })} error={err?.fields['license.consoleUrl']} />
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="ganti-client" label="Client ID" value={v.clientId} onChange={(e) => setV({ ...v, clientId: e.target.value })} error={err?.fields['license.clientId']} />
          <Field id="ganti-key" label="License key baru" className="num" value={v.licenseKey} onChange={(e) => setV({ ...v, licenseKey: e.target.value })} error={err?.fields['license.licenseKey']} />
        </div>
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
      </div>
    </Dialog>
  )
}

// ---------------------------------------------------------------- backup

const kindLabel = { scheduled: 'Terjadwal', manual: 'Manual', pre_update: 'Sebelum pembaruan' } as const
const copyLabel: Record<string, string> = { local: 'Server', dir: 'Folder lain', s3: 'S3' }

function size(n?: number) {
  if (!n) return '—'
  if (n >= 1 << 30) return `${(n / (1 << 30)).toLocaleString('id-ID', { maximumFractionDigits: 1 })} GB`
  if (n >= 1 << 20) return `${(n / (1 << 20)).toLocaleString('id-ID', { maximumFractionDigits: 1 })} MB`
  return `${Math.max(1, Math.round(n / 1024))} KB`
}

function BackupSection() {
  const q = useBackups(true)
  const run = useRunBackup()
  const toast = useToast()
  if (q.isPending) return <Skeleton className="h-48" />
  if (q.isError) return <Alert>{q.error.message}</Alert>
  const { status: st, settings, items } = q.data
  const running = st.running || items.some((b) => b.status === 'running')

  return (
    <>
      <Card
        title="Backup"
        subtitle="Backup harian berisi basis data dan lampiran, dienkripsi dengan kunci publik. Hanya Pemilik yang memegang kunci privat untuk membukanya."
        actions={st.configured && <Badge tone={st.overdue ? 'bad' : 'good'}>{st.overdue ? 'Terlambat' : 'Berjalan'}</Badge>}
      >
        {!st.configured ? (
          <Alert tone="warn">
            Kunci publik backup belum dipasang. Pemasang mengisi <span className="num">LAJUR_BACKUP_PUBLIC_KEY</span> di /opt/lajur/.env (dibuat oleh skrip instal atau{' '}
            <span className="num">lajur backup-keygen</span>), lalu menjalankan ulang aplikasi.
          </Alert>
        ) : (
          <div className="flex flex-col gap-4">
            <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
              <Item label="Backup sukses terakhir">{st.lastOk ? formatDateTime(st.lastOk) : 'Belum pernah'}</Item>
              <Item label="Backup berikutnya">{new Date(st.nextRun).toLocaleString('id-ID', { dateStyle: 'medium', timeStyle: 'short', timeZone: st.timezone })} ({st.timezone})</Item>
              <Item label="Sidik kunci">
                <span className="num">{st.keyId}</span>
              </Item>
            </dl>
            {st.overdue && <Alert>Belum ada backup sukses dalam 48 jam terakhir. Periksa galat di bawah atau jalankan backup sekarang.</Alert>}
            {st.lastFailed && (
              <Alert tone="warn">
                Backup {formatDateTime(st.lastFailed.startedAt)} gagal: {st.lastFailed.error}
              </Alert>
            )}
            {st.restoreTest && (
              <p className="text-sm text-ink-2">
                <Badge tone={st.restoreTest.ok ? 'good' : 'bad'}>{st.restoreTest.ok ? 'Uji pulih berhasil' : 'Uji pulih gagal'}</Badge> {formatDateTime(st.restoreTest.testedAt)} · {st.restoreTest.detail}
              </p>
            )}
            {run.error && <Alert>{run.error.message}</Alert>}
            <Button className="w-fit" busy={running || run.isPending} onClick={() => run.mutate(undefined, { onSuccess: () => toast('Backup dimulai · berjalan di latar belakang') })}>
              {running ? 'Backup sedang berjalan…' : 'Backup sekarang'}
            </Button>
          </div>
        )}
      </Card>

      <TableShell minWidth={760}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Waktu</th>
            <th className={th}>Jenis</th>
            <th className={`${th} text-right`}>Ukuran</th>
            <th className={th}>Salinan</th>
            <th className={th}>Status</th>
            <th className={th} />
          </tr>
        </thead>
        <tbody>
          {items.length === 0 && (
            <tr>
              <td colSpan={6} className={`${td} text-muted`}>
                Belum ada backup.
              </td>
            </tr>
          )}
          {items.map((b) => (
            <BackupRow key={b.id} b={b} />
          ))}
        </tbody>
      </TableShell>

      <BackupSettingsCard key={settings.version} settings={settings} timezone={st.timezone} />
      <Card title="Memulihkan backup" subtitle="Dilakukan di server oleh pemasang, dengan kunci privat dari Pemilik.">
        <ol className="flex list-decimal flex-col gap-1.5 pl-5 text-sm text-ink-2">
          <li>Unduh file backup (.lajurbk) dari tabel di atas, atau ambil dari folder lain/S3.</li>
          <li>
            Hentikan aplikasi: <span className="num">docker compose stop app</span>
          </li>
          <li>
            Pulihkan: <span className="num">docker compose run --rm app restore /var/lib/lajur/backups/NAMA-FILE.lajurbk</span>, lalu tempel kunci privat saat diminta.
          </li>
          <li>
            Jalankan lagi: <span className="num">docker compose start app</span>
          </li>
        </ol>
      </Card>
    </>
  )
}

function BackupRow({ b }: { b: BackupRecord }) {
  return (
    <tr className="border-t border-line">
      <td className={`${td} whitespace-nowrap`}>{formatDateTime(b.startedAt)}</td>
      <td className={td}>{kindLabel[b.kind]}</td>
      <td className={`${td} num text-right`}>{size(b.sizeBytes)}</td>
      <td className={td}>
        <span className="flex flex-wrap gap-1.5">
          {Object.entries(b.copies).map(([k, v]) => (
            <span key={k} title={v === 'ok' ? undefined : v}>
              <Badge tone={v === 'ok' ? 'neutral' : 'bad'}>
                {copyLabel[k] ?? k}
                {v === 'ok' ? '' : ' gagal'}
              </Badge>
            </span>
          ))}
        </span>
      </td>
      <td className={td}>
        {b.status === 'ok' ? <Badge tone="good">{b.pruned ? 'Berhasil · dihapus retensi' : 'Berhasil'}</Badge> : b.status === 'running' ? <Badge tone="warn">Berjalan</Badge> : <span title={b.error}><Badge tone="bad">Gagal</Badge></span>}
      </td>
      <td className={`${td} text-right`}>
        {b.status === 'ok' && !b.pruned && (
          <a href={`/api/v1/system/backups/${b.id}/download`} className="text-sm font-semibold text-accent underline-offset-2 hover:underline">
            Unduh
          </a>
        )}
      </td>
    </tr>
  )
}

function BackupSettingsCard({ settings, timezone }: { settings: BackupSettings; timezone: string }) {
  const save = useSaveBackupSettings()
  const toast = useToast()
  const [v, setV] = useState<BackupSettings>({ ...settings, s3Secret: '' })
  const err = save.error instanceof ApiError ? save.error : null
  const f = (k: string) => err?.fields[k]
  const changed = JSON.stringify({ ...v, s3Secret: '' }) !== JSON.stringify({ ...settings, s3Secret: '' }) || !!v.s3Secret
  return (
    <Card title="Pengaturan backup" subtitle="Folder lain bisa berupa NAS atau disk kedua yang di-mount di server. Penyimpanan S3-compatible: AWS S3, Wasabi, Cloudflare R2, MinIO, dan sejenisnya.">
      <div className="flex flex-col gap-4">
        <div className="grid gap-4 sm:grid-cols-3">
          <SelectField id="bk-jam" label={`Jam backup harian (${timezone})`} value={v.hour} onChange={(e) => setV({ ...v, hour: Number(e.target.value) })} error={f('hour')}>
            {Array.from({ length: 24 }, (_, h) => (
              <option key={h} value={h}>
                {String(h).padStart(2, '0')}.00
              </option>
            ))}
          </SelectField>
          <Field id="bk-simpan" label="Simpan salinan terakhir" type="number" min={3} max={365} value={v.keep} onChange={(e) => setV({ ...v, keep: Number(e.target.value) })} error={f('keep')} />
          <Field id="bk-folder" label="Folder lain (opsional)" placeholder="/mnt/nas/lajur" value={v.copyDir} onChange={(e) => setV({ ...v, copyDir: e.target.value })} error={f('copyDir')} />
        </div>
        <fieldset className="grid gap-4 sm:grid-cols-2">
          <legend className="mb-2 text-sm font-semibold">Salinan ke S3 (opsional)</legend>
          <Field id="bk-s3-endpoint" label="Alamat S3" placeholder="https://s3.ap-southeast-1.wasabisys.com" value={v.s3Endpoint} onChange={(e) => setV({ ...v, s3Endpoint: e.target.value })} error={f('s3Endpoint')} />
          <Field id="bk-s3-bucket" label="Bucket" value={v.s3Bucket} onChange={(e) => setV({ ...v, s3Bucket: e.target.value })} error={f('s3Bucket')} />
          <Field id="bk-s3-region" label="Region" placeholder="ap-southeast-1" value={v.s3Region} onChange={(e) => setV({ ...v, s3Region: e.target.value })} />
          <Field id="bk-s3-prefix" label="Folder di bucket" placeholder="lajur/" value={v.s3Prefix} onChange={(e) => setV({ ...v, s3Prefix: e.target.value })} />
          <Field id="bk-s3-key" label="Access key" value={v.s3AccessKey} onChange={(e) => setV({ ...v, s3AccessKey: e.target.value })} error={f('s3AccessKey')} />
          <Field
            id="bk-s3-secret"
            label="Secret key"
            type="password"
            autoComplete="off"
            placeholder={settings.s3HasSecret ? 'Tersimpan · isi untuk mengganti' : ''}
            value={v.s3Secret ?? ''}
            onChange={(e) => setV({ ...v, s3Secret: e.target.value })}
          />
        </fieldset>
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
        <Button className="w-fit" disabled={!changed} busy={save.isPending} onClick={() => save.mutate(v, { onSuccess: () => toast('Pengaturan backup disimpan') })}>
          Simpan pengaturan
        </Button>
      </div>
    </Card>
  )
}

// ---------------------------------------------------------------- pembaruan

const updateLabel = { idle: '', running: 'Sedang memperbarui', succeeded: 'Berhasil', failed: 'Gagal', rolled_back: 'Dikembalikan ke versi lama' } as const

function UpdateSection() {
  const check = useUpdateCheck(true)
  const apply = useApplyUpdate()
  const [confirming, setConfirming] = useState(false)
  const [watching, setWatching] = useState(false)
  const state = useUpdateState(true, watching)
  const [live, setLive] = useState<string | null>(null)
  const st = state.data
  const target = st?.to ?? check.data?.latest?.version

  // Selama dan sesudah pembaruan, pantau versi yang menjawab /healthz.
  useEffect(() => {
    if (!watching) return
    const id = setInterval(() => void healthVersion().then(setLive), 2000)
    return () => clearInterval(id)
  }, [watching])
  useEffect(() => {
    if (watching && st && st.status !== 'running' && st.status !== 'idle') setWatching(false)
  }, [watching, st])

  if (check.isPending) return <Skeleton className="h-40" />
  if (check.isError) return <Alert>{check.error.message}</Alert>
  const c = check.data
  const running = st?.status === 'running' || watching
  const blocked = !c.licenseOk ? 'Pembaruan hanya tersedia untuk lisensi yang aktif.' : !c.backupOk ? 'Backup belum dikonfigurasi; pembaruan selalu didahului backup.' : !c.reachable && c.latest?.minFrom ? `Perbarui dulu ke versi ${c.latest.minFrom}.` : null

  return (
    <>
      <Card title="Pembaruan" subtitle="Pembaruan diunduh dari update server dengan tanda tangan rilis, didahului backup otomatis, dan kembali ke versi lama bila versi baru gagal berjalan.">
        <div className="flex flex-col gap-4">
          <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
            <Item label="Versi terpasang">
              <span className="num">{live ?? c.current}</span>
            </Item>
            <Item label="Versi terbaru">{c.latest ? <span className="num">{c.latest.version}</span> : '—'}</Item>
            <Item label="Dirilis">{c.latest ? formatDate(c.latest.publishedAt) : '—'}</Item>
          </dl>
          {!c.configured && <Alert tone="warn">Pembaruan otomatis belum dikonfigurasi di server ini (LAJUR_UPDATE_URL dan container updater).</Alert>}
          {c.error && <Alert tone="warn">Update server tidak bisa diperiksa: {c.error}</Alert>}
          {c.latest && c.latest.notes && (
            <div className="rounded-[12px] border border-line p-4">
              <p className="mb-1.5 text-[12.5px] font-semibold tracking-wide text-muted uppercase">Catatan rilis {c.latest.version}</p>
              <p className="text-sm whitespace-pre-line text-ink-2">{c.latest.notes}</p>
            </div>
          )}
          {c.configured && !c.available && !c.error && <p className="text-sm text-ink-2">Lajur sudah versi terbaru.</p>}
          {c.available && blocked && <Alert tone="warn">{blocked}</Alert>}
          {apply.error && <Alert>{apply.error.message}</Alert>}
          {c.available && (
            <Button className="w-fit" disabled={!!blocked || running} busy={running} onClick={() => setConfirming(true)}>
              {running ? 'Sedang memperbarui…' : `Perbarui ke ${c.latest?.version}`}
            </Button>
          )}
        </div>
      </Card>

      {st && st.status !== 'idle' && (
        <Card title={`${updateLabel[st.status]}${st.from && st.to ? ` · ${st.from} → ${st.to}` : ''}`} subtitle={st.step}>
          <div className="flex flex-col gap-3">
            {watching && live && live === target && <Alert tone="good">Versi {target} sudah berjalan. Muat ulang halaman untuk memakai versi baru.</Alert>}
            {watching && !live && <Alert tone="warn">Aplikasi sedang dimulai ulang… halaman ini menunggu sampai siap.</Alert>}
            {st.error && <Alert tone={st.status === 'rolled_back' ? 'warn' : 'bad'}>{st.error}</Alert>}
            <ol className="flex flex-col gap-1 text-sm">
              {st.log.map((l, i) => (
                <li key={i} className="flex gap-3">
                  <span className="num shrink-0 text-muted">{new Date(l.at).toLocaleTimeString('id-ID')}</span>
                  <span>{l.message}</span>
                </li>
              ))}
            </ol>
            {st.status === 'succeeded' && (
              <Button variant="secondary" className="w-fit" onClick={() => window.location.reload()}>
                Muat ulang halaman
              </Button>
            )}
          </div>
        </Card>
      )}

      <Dialog
        open={confirming}
        onClose={() => setConfirming(false)}
        title={`Perbarui ke ${c.latest?.version}?`}
        description="Lajur membuat backup, mengunduh versi baru, lalu memulai ulang aplikasi (sekitar 1–3 menit). Minta pengguna lain menyimpan pekerjaannya dulu."
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirming(false)}>
              Batal
            </Button>
            <Button
              busy={apply.isPending}
              onClick={() =>
                apply.mutate(undefined, {
                  onSuccess: () => {
                    setConfirming(false)
                    setWatching(true)
                  },
                })
              }
            >
              Perbarui sekarang
            </Button>
          </>
        }
      >
        <p className="text-sm text-ink-2">Bila versi baru gagal berjalan, updater otomatis kembali ke versi {c.current}.</p>
      </Dialog>
    </>
  )
}

// ---------------------------------------------------------------- email

const outboxKind = { invoice: 'Faktur', backup_alert: 'Peringatan backup', license_alert: 'Peringatan lisensi', test: 'Uji' } as const
const outboxStatus = { queued: ['warn', 'Antre'], sent: ['good', 'Terkirim'], failed: ['bad', 'Gagal'] } as const

function MailSection({ me }: { me: Me }) {
  const q = useMail(true)
  if (q.isPending) return <Skeleton className="h-48" />
  if (q.isError) return <Alert>{q.error.message}</Alert>
  return (
    <>
      <MailSettingsCard key={q.data.settings.version} me={me} settings={q.data.settings} />
      <TableShell minWidth={720}>
        <thead className="bg-hover/60">
          <tr>
            <th className={th}>Waktu</th>
            <th className={th}>Jenis</th>
            <th className={th}>Kepada</th>
            <th className={th}>Subjek</th>
            <th className={th}>Status</th>
          </tr>
        </thead>
        <tbody>
          {q.data.outbox.length === 0 && (
            <tr>
              <td colSpan={5} className={`${td} text-muted`}>
                Belum ada email yang dikirim.
              </td>
            </tr>
          )}
          {q.data.outbox.map((m) => (
            <tr key={m.id} className="border-t border-line">
              <td className={`${td} whitespace-nowrap`}>{formatDateTime(m.createdAt)}</td>
              <td className={td}>{outboxKind[m.kind] ?? m.kind}</td>
              <td className={`${td} max-w-[200px] truncate`}>{m.to.join(', ')}</td>
              <td className={`${td} max-w-[260px] truncate`}>{m.subject}</td>
              <td className={td}>
                <span title={m.error}>
                  <Badge tone={outboxStatus[m.status][0]}>{outboxStatus[m.status][1]}</Badge>
                </span>
                {m.status === 'queued' && m.attempts > 0 && <span className="ml-2 text-[12px] text-muted">dicoba {m.attempts}×</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </TableShell>
    </>
  )
}

function MailSettingsCard({ me, settings }: { me: Me; settings: MailSettings }) {
  const save = useSaveMail()
  const test = useTestMail()
  const toast = useToast()
  const [v, setV] = useState<MailSettings>({ ...settings, password: '' })
  const [to, setTo] = useState(me.user.email)
  const err = save.error instanceof ApiError ? save.error : null
  const f = (k: string) => err?.fields[k]
  return (
    <Card title="Server email (SMTP)" subtitle="Dipakai untuk mengirim faktur ke pelanggan serta peringatan backup dan lisensi ke admin. Gunakan akun email perusahaan, mis. Google Workspace atau email hosting.">
      <div className="flex flex-col gap-4">
        <Checkbox checked={v.enabled} onChange={(enabled) => setV({ ...v, enabled })} label="Aktifkan pengiriman email" />
        <div className="grid gap-4 sm:grid-cols-3">
          <Field id="smtp-host" label="Server SMTP" placeholder="smtp.gmail.com" value={v.host} onChange={(e) => setV({ ...v, host: e.target.value })} error={f('host')} />
          <Field id="smtp-port" label="Port" type="number" value={v.port} onChange={(e) => setV({ ...v, port: Number(e.target.value) })} error={f('port')} />
          <SelectField id="smtp-security" label="Keamanan" value={v.security} onChange={(e) => setV({ ...v, security: e.target.value as MailSettings['security'] })} error={f('security')}>
            <option value="starttls">STARTTLS (port 587)</option>
            <option value="tls">SSL/TLS (port 465)</option>
            <option value="none">Tanpa enkripsi (relay di kantor)</option>
          </SelectField>
          <Field id="smtp-user" label="Username" autoComplete="off" value={v.username} onChange={(e) => setV({ ...v, username: e.target.value })} />
          <Field
            id="smtp-pass"
            label="Password"
            type="password"
            autoComplete="new-password"
            placeholder={settings.hasPassword ? 'Tersimpan · isi untuk mengganti' : ''}
            value={v.password ?? ''}
            onChange={(e) => setV({ ...v, password: e.target.value })}
          />
          <Field id="smtp-from" label="Alamat pengirim" placeholder="faktur@perusahaan.co.id" value={v.from} onChange={(e) => setV({ ...v, from: e.target.value })} error={f('from')} />
          <Field id="smtp-name" label="Nama pengirim" value={v.fromName} onChange={(e) => setV({ ...v, fromName: e.target.value })} />
        </div>
        {err && Object.keys(err.fields).length === 0 && <Alert>{err.message}</Alert>}
        <Button className="w-fit" busy={save.isPending} onClick={() => save.mutate(v, { onSuccess: () => toast('Pengaturan email disimpan') })}>
          Simpan pengaturan
        </Button>
        <div className="flex flex-wrap items-end gap-3 border-t border-line pt-4">
          <Field id="smtp-test" label="Kirim email uji ke" className="min-w-[260px]" value={to} onChange={(e) => setTo(e.target.value)} />
          <Button variant="secondary" busy={test.isPending} disabled={!settings.enabled} onClick={() => test.mutate(to, { onSuccess: (r) => toast(`Email uji terkirim ke ${r.to}`) })}>
            Kirim email uji
          </Button>
        </div>
        {!settings.enabled && <p className="text-[12.5px] text-muted">Simpan dan aktifkan pengaturan dulu sebelum mengirim email uji.</p>}
        {test.error && <Alert>{test.error.message}</Alert>}
      </div>
    </Card>
  )
}

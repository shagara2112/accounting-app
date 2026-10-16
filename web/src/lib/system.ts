import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'

export type LicenseDetail = {
  configured: boolean
  consoleUrl?: string
  clientId?: string
  installationId: string
  version: string
  checkedAt?: string
  attemptedAt?: string
  errorCode?: string
  errorMessage?: string
  tokenValidTo?: string
  expiresAt?: string
  minVersion?: string
}

export type LicenseInfo = { mode: 'local' } | { mode: 'remote'; detail: LicenseDetail }

export function useLicenseDetail(enabled: boolean) {
  return useQuery({ queryKey: ['system', 'license'], queryFn: () => api<LicenseInfo>('GET', '/system/license/detail'), enabled })
}

function useSystemMutation<V, R = unknown>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['system'] })
      void qc.invalidateQueries({ queryKey: ['license'] })
    },
  })
}

export function useRecheckLicense() {
  return useSystemMutation(() => api<{ ok: boolean; message?: string; detail: LicenseDetail }>('POST', '/system/license/recheck'))
}

export function useReplaceLicense() {
  return useSystemMutation((v: { consoleUrl: string; clientId: string; licenseKey: string }) => api('PUT', '/system/license', v))
}

export type PaymentPackage = { id: string; name: string; months: number; price: number; link: string }
export type PaymentRequest = {
  id: string
  status: 'PENDING' | 'APPROVED' | 'REJECTED' | 'CANCELLED'
  packageName: string
  months: number
  amount: number
  reference?: string
  submittedAt: string
  reviewReason?: string
  reviewedAt?: string
  newExpiresAt?: string
}
export type PaymentInfo = {
  enabled: boolean
  instructions: string
  packages: PaymentPackage[]
  latest: PaymentRequest | null
  license: { expiresAt: string; status: string; daysLeft?: number }
  contactWhatsApp: string
}

export function usePaymentInfo(enabled: boolean) {
  return useQuery({ queryKey: ['system', 'payment'], queryFn: () => api<PaymentInfo>('GET', '/system/license/payment'), enabled, retry: false })
}

export type PaymentInput = { packageId: string; amount: number; payerName: string; reference: string; note: string; fileBase64: string; mimeType: string }

export function useSubmitPayment() {
  return useSystemMutation((v: PaymentInput) => api<{ ok: boolean; request: PaymentRequest }>('POST', '/system/license/payment', v))
}

// Backup ----------------------------------------------------------------------------

export type BackupRecord = {
  id: string
  kind: 'scheduled' | 'manual' | 'pre_update'
  status: 'running' | 'ok' | 'failed'
  startedAt: string
  finishedAt?: string
  fileName?: string
  sizeBytes?: number
  sha256?: string
  keyId?: string
  copies: Record<string, string>
  error?: string
  pruned: boolean
}

export type BackupStatus = {
  configured: boolean
  keyId?: string
  running: boolean
  lastOk?: string
  lastFailed?: BackupRecord
  overdue: boolean
  nextRun: string
  timezone: string
  restoreTest?: { testedAt: string; ok: boolean; detail: string }
}

export type BackupSettings = {
  hour: number
  keep: number
  copyDir: string
  s3Endpoint: string
  s3Region: string
  s3Bucket: string
  s3Prefix: string
  s3AccessKey: string
  s3Secret?: string
  s3HasSecret: boolean
  version: number
}

export function useBackups(enabled: boolean) {
  return useQuery({
    queryKey: ['system', 'backups'],
    queryFn: () => api<{ status: BackupStatus; settings: BackupSettings; items: BackupRecord[] }>('GET', '/system/backups'),
    enabled,
    // Selama backup berjalan, status dipantau tiap 3 detik.
    refetchInterval: (q) => (q.state.data?.status.running || q.state.data?.items.some((b) => b.status === 'running') ? 3000 : false),
  })
}

export function useBackupStatus(enabled: boolean) {
  return useQuery({ queryKey: ['system', 'backup-status'], queryFn: () => api<BackupStatus>('GET', '/system/backups/status'), enabled, staleTime: 60_000 })
}

export function useRunBackup() {
  return useSystemMutation(() => api('POST', '/system/backups'))
}

export function useSaveBackupSettings() {
  return useSystemMutation((v: BackupSettings) => api<BackupSettings>('PUT', '/system/backups/settings', v))
}

// Pembaruan --------------------------------------------------------------------------

export type UpdateCheck = {
  current: string
  configured: boolean
  licenseOk: boolean
  backupOk: boolean
  latest?: { version: string; channel: string; notes: string; publishedAt: string; minFrom?: string }
  available: boolean
  reachable: boolean
  error?: string
}

export type UpdateState = {
  status: 'idle' | 'running' | 'succeeded' | 'failed' | 'rolled_back'
  from?: string
  to?: string
  step?: string
  error?: string
  startedAt?: string
  finishedAt?: string
  log: { at: string; message: string }[]
}

export function useUpdateCheck(enabled: boolean) {
  return useQuery({ queryKey: ['system', 'updates'], queryFn: () => api<UpdateCheck>('GET', '/system/updates'), enabled, staleTime: 5 * 60_000 })
}

export function useApplyUpdate() {
  return useSystemMutation(() => api<{ ok: boolean; version: string }>('POST', '/system/updates/apply'))
}

/** Status updater; selama berjalan dipantau tiap 2 detik. Saat app dimulai ulang, request
 *  gagal sebentar dan tetap dicoba lagi. */
export function useUpdateState(enabled: boolean, watching: boolean) {
  return useQuery({
    queryKey: ['system', 'update-state'],
    queryFn: () => api<UpdateState>('GET', '/system/updates/status'),
    enabled,
    retry: watching ? 1000 : 0,
    retryDelay: 2000,
    refetchInterval: (q) => (watching || q.state.data?.status === 'running' ? 2000 : false),
  })
}

/** Versi yang sedang menjawab /healthz (tanpa sesi), untuk tahu kapan versi baru sudah hidup. */
export async function healthVersion(): Promise<string | null> {
  try {
    const r = await fetch('/healthz', { cache: 'no-store' })
    if (!r.ok) return null
    return ((await r.json()) as { version?: string }).version ?? null
  } catch {
    return null
  }
}

// Email ------------------------------------------------------------------------------

export type MailSettings = {
  enabled: boolean
  host: string
  port: number
  security: 'starttls' | 'tls' | 'none'
  username: string
  password?: string
  hasPassword: boolean
  from: string
  fromName: string
  version: number
}

export type OutboxItem = {
  id: string
  kind: 'invoice' | 'backup_alert' | 'license_alert' | 'test'
  to: string[]
  subject: string
  attachment?: string
  status: 'queued' | 'sent' | 'failed'
  attempts: number
  error?: string
  createdAt: string
  sentAt?: string
}

export function useMail(enabled: boolean) {
  return useQuery({
    queryKey: ['system', 'mail'],
    queryFn: () => api<{ settings: MailSettings; outbox: OutboxItem[] }>('GET', '/system/mail'),
    enabled,
    refetchInterval: (q) => (q.state.data?.outbox.some((m) => m.status === 'queued') ? 10_000 : false),
  })
}

export function useSaveMail() {
  return useSystemMutation((v: MailSettings) => api<MailSettings>('PUT', '/system/mail', v))
}

export function useTestMail() {
  return useMutation({ mutationFn: (to: string) => api<{ ok: boolean; to: string }>('POST', '/system/mail/test', { to }) })
}

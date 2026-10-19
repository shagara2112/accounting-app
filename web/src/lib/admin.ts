import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'
import type { Dimension } from './dimensions'

// Setup wizard --------------------------------------------------------------------

export type Industry = { key: 'jasa' | 'konstruksi' | 'retail'; label: string }

export type SetupStatus = { required: boolean; remoteLicense: boolean; industries: Industry[] }

export type SetupInput = {
  license?: { consoleUrl: string; clientId: string; licenseKey: string }
  company: { name: string; shortName: string; industry: string; npwp: string }
  admin: { name: string; email: string; password: string }
}

export function useSetupStatus(enabled: boolean) {
  return useQuery({ queryKey: ['setup'], queryFn: () => api<SetupStatus>('GET', '/setup'), enabled, retry: false })
}

export function useSetup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: SetupInput) => api<{ ok: boolean; mfaSetupRequired: boolean }>('POST', '/setup', v),
    onSuccess: () => qc.invalidateQueries(),
  })
}

// Login dua tahap dan 2FA ------------------------------------------------------------

export type LoginResult = { ok?: boolean; mfaSetupRequired?: boolean; totpRequired?: boolean; challenge?: string }

export function useLoginTotp() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { challenge: string; code: string }) => api<LoginResult>('POST', '/auth/totp', v),
    onSuccess: () => qc.invalidateQueries(),
  })
}

export type MfaStatus = { enabled: boolean; enabledAt?: string; required: boolean; setupPending: boolean }

export function useMfaStatus() {
  return useQuery({ queryKey: ['mfa'], queryFn: () => api<MfaStatus>('GET', '/auth/mfa') })
}

export function useTotpSetup() {
  return useMutation({ mutationFn: () => api<{ key: string; uri: string }>('POST', '/auth/totp/setup') })
}

export function useTotpEnable() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (code: string) => api('POST', '/auth/totp/enable', { code }),
    onSuccess: () => qc.invalidateQueries(),
  })
}

export function useTotpDisable() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (password: string) => api('POST', '/auth/totp/disable', { password }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['mfa'] }),
  })
}

export function useChangePassword() {
  return useMutation({ mutationFn: (v: { current: string; new: string }) => api('POST', '/auth/password', v) })
}

// Pengguna, persona, dan PT ---------------------------------------------------------

/** branchIds: pengguna dibatasi ke cabang ini (PLT-12); kosong = semua cabang. */
export type UserAccess = { companyId: string; companyName: string; roleKey: string; roleName: string; branchIds: string[] }

export type AdminUser = {
  id: string
  email: string
  name: string
  active: boolean
  locked: boolean
  mfaEnabled: boolean
  mfaRequired: boolean
  lastLoginAt?: string
  createdAt: string
  version: number
  access: UserAccess[]
}

export type UserList = {
  items: AdminUser[]
  companies: { id: string; name: string; manageable: boolean; branches: Dimension[] }[]
  roles: { key: string; name: string }[]
}

const usersKey = ['admin', 'users']

export function useAdminUsers() {
  return useQuery({ queryKey: usersKey, queryFn: () => api<UserList>('GET', '/admin/users') })
}

function useAdminMutation<V, R = unknown>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient()
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: ['admin'] }) })
}

export function useCreateUser() {
  return useAdminMutation((v: { name: string; email: string; password: string; access: { companyId: string; roleKey: string }[] }) =>
    api<AdminUser>('POST', '/admin/users', v),
  )
}

export function useUpdateUser() {
  return useAdminMutation(({ id, ...v }: { id: string; name: string; email: string; active: boolean; version: number }) =>
    api<AdminUser>('PUT', `/admin/users/${id}`, v),
  )
}

export function useSetAccess() {
  return useAdminMutation(({ userId, ...v }: { userId: string; companyId: string; roleKey: string; branchIds?: string[] }) => api('PUT', `/admin/users/${userId}/access`, v))
}

export function useResetPassword() {
  return useAdminMutation(({ id, password }: { id: string; password: string }) => api('POST', `/admin/users/${id}/password`, { password }))
}

export function useResetMfa() {
  return useAdminMutation((id: string) => api('POST', `/admin/users/${id}/reset-2fa`))
}

export function useUnlockUser() {
  return useAdminMutation((id: string) => api('POST', `/admin/users/${id}/unlock`))
}

export type AdminRole = { id: string; key: string; name: string; builtin: boolean; permissions: string[]; assignments: number; version: number }
export type PermCatalog = { name: string; perms: { key: string; label: string }[] }[]

export function useRoles() {
  return useQuery({ queryKey: ['admin', 'roles'], queryFn: () => api<{ items: AdminRole[]; catalog: PermCatalog }>('GET', '/admin/roles') })
}

export function useSaveRole() {
  return useAdminMutation(({ id, ...v }: { id?: string; name: string; permissions: string[]; version: number }) =>
    id ? api<AdminRole>('PUT', `/admin/roles/${id}`, v) : api<AdminRole>('POST', '/admin/roles', v),
  )
}

export function useDeleteRole() {
  return useAdminMutation((id: string) => api('DELETE', `/admin/roles/${id}`))
}

export type AdminCompany = { id: string; name: string; shortName: string; industry?: string; npwp?: string; active: boolean; users: number; createdAt: string }

export function useAdminCompanies() {
  return useQuery({ queryKey: ['admin', 'companies'], queryFn: () => api<{ items: AdminCompany[]; industries: Industry[] }>('GET', '/admin/companies') })
}

export function useRenameCompany() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; name: string; shortName: string }) => api<AdminCompany>('PUT', `/admin/companies/${id}`, v),
    // Nama PT tampil di bar atas, pilihan PT (/me), dan daftar admin.
    onSuccess: () => qc.invalidateQueries(),
  })
}

export function useCreateCompany() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { name: string; shortName: string; industry: string; npwp: string }) => api<AdminCompany>('POST', '/admin/companies', v),
    // PT baru muncul di pilihan PT (/me) dan daftar admin.
    onSuccess: () => qc.invalidateQueries(),
  })
}

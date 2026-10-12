import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type AuditItem, type AuditVerify, type LicenseStatus, type Me } from './api'

export function useMe() {
  return useQuery({ queryKey: ['me'], queryFn: () => api<Me>('GET', '/me'), retry: false, staleTime: 30_000 })
}

export function useLicense() {
  return useQuery({ queryKey: ['license'], queryFn: () => api<LicenseStatus>('GET', '/system/license'), staleTime: 60_000 })
}

export function useLogin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { email: string; password: string }) => api('POST', '/auth/login', v),
    onSuccess: () => qc.invalidateQueries(),
  })
}

export function useLogout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api('POST', '/auth/logout'),
    onSuccess: () => qc.resetQueries(),
  })
}

export function useSwitchCompany() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (companyId: string) => api('POST', '/session/company', { companyId }),
    onSuccess: () => qc.invalidateQueries(),
  })
}

// companyId ikut di queryKey supaya data PT lain tidak pernah tertukar setelah berpindah PT.
export function useAudit(companyId: string | null) {
  return useQuery({
    queryKey: ['audit', companyId],
    queryFn: () => api<{ items: AuditItem[] }>('GET', '/audit?limit=100'),
    enabled: companyId !== null,
  })
}

export function useAuditVerify(companyId: string | null) {
  return useQuery({
    queryKey: ['audit-verify', companyId],
    queryFn: () => api<AuditVerify>('GET', '/audit/verify'),
    enabled: companyId !== null,
  })
}

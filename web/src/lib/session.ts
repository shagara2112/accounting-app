import type { CompanyAccess, Me } from './api'

export function activeCompany(me: Me): CompanyAccess | undefined {
  return me.companies.find((c) => c.id === me.activeCompanyId) ?? me.companies[0]
}

/** Izin pengguna di PT aktif; server tetap memeriksa ulang setiap request. */
export function can(me: Me, perm: string): boolean {
  return activeCompany(me)?.permissions.includes(perm) ?? false
}

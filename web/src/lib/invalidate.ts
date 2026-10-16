import { useQueryClient } from '@tanstack/react-query'

/** Menyegarkan semua data milik satu PT setelah dokumen diposting atau di-void. Satu faktur
 *  mengubah buku besar, saldo kas, umur piutang, dan audit trail sekaligus, jadi semuanya
 *  disegarkan bersama. Semua query key berbentuk [kelompok, companyId, ...]. */
export function useInvalidateCompany(companyId: string | null) {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ predicate: (q) => q.queryKey[1] === companyId })
}

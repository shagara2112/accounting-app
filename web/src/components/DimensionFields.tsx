import { useEffect } from 'react'
import type { Me } from '../lib/api'
import { allowedBranches, isRestricted, useDimensions } from '../lib/dimensions'
import { SelectField } from './ui'

export type DimValue = { branchId: string; departmentId: string }

/**
 * Pilihan cabang dan departemen (GL-09) di kepala dokumen. Tidak tampil bila PT belum punya
 * dimensi. Pengguna yang dibatasi ke cabang tertentu (PLT-12) wajib memilih salah satu cabangnya.
 */
export function DimensionFields({
  me,
  value,
  onChange,
  errors,
  withDepartment = true,
}: {
  me: Me
  value: DimValue
  onChange: (v: DimValue) => void
  errors?: Record<string, string>
  /** false bila departemen dipilih per baris, mis. di transaksi kas. */
  withDepartment?: boolean
}) {
  const companyId = me.activeCompanyId
  const branchList = useDimensions(companyId, 'cabang').data ?? []
  const deptList = useDimensions(companyId, 'departemen').data ?? []
  const restricted = isRestricted(me)
  const branches = allowedBranches(me, branchList)
  const current = branchList.find((b) => b.id === value.branchId)
  if (current && !branches.includes(current)) branches.unshift(current)
  const depts = deptList.filter((d) => d.active || d.id === value.departmentId)

  // Pengguna terbatas dengan satu cabang langsung terisi cabangnya.
  const only = restricted && !value.branchId && branches.length === 1 ? branches[0].id : null
  useEffect(() => {
    if (only) onChange({ ...value, branchId: only })
  }, [only])

  return (
    <>
      {branches.length > 0 && (
        <SelectField
          id="dim-cabang"
          label={restricted ? 'Cabang' : 'Cabang (opsional)'}
          value={value.branchId}
          onChange={(e) => onChange({ ...value, branchId: e.target.value })}
          error={errors?.branchId}
        >
          {(!restricted || !value.branchId) && <option value="">{restricted ? 'Pilih cabang' : 'Tanpa cabang'}</option>}
          {branches.map((b) => (
            <option key={b.id} value={b.id}>
              {b.code} · {b.name}
            </option>
          ))}
        </SelectField>
      )}
      {withDepartment && depts.length > 0 && (
        <SelectField
          id="dim-departemen"
          label="Departemen (opsional)"
          value={value.departmentId}
          onChange={(e) => onChange({ ...value, departmentId: e.target.value })}
          error={errors?.departmentId}
        >
          <option value="">Tanpa departemen</option>
          {depts.map((d) => (
            <option key={d.id} value={d.id}>
              {d.code} · {d.name}
            </option>
          ))}
        </SelectField>
      )}
    </>
  )
}

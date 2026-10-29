/** Logo Lajur: tiga lajur buku besar yang naik, di kotak warna aksen. */
export function Logo({ size = 30 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="9" fill="var(--color-accent)" />
      <rect x="8" y="15" width="4" height="10" rx="2" fill="var(--color-on-accent)" opacity="0.6" />
      <rect x="14" y="10.5" width="4" height="14.5" rx="2" fill="var(--color-on-accent)" opacity="0.8" />
      <rect x="20" y="6" width="4" height="19" rx="2" fill="var(--color-on-accent)" />
    </svg>
  )
}

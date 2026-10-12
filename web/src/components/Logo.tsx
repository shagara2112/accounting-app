export function Logo({ size = 30 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 30 30" aria-hidden="true">
      <rect width="30" height="30" rx="9" fill="var(--color-accent)" />
      <path d="M10 9v12M15 9v12M20 9v12" stroke="var(--color-on-accent)" strokeWidth="2.2" strokeLinecap="round" />
    </svg>
  )
}

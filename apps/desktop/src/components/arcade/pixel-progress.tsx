import { useArcade } from "./arcade-store"

/**
 * Arcade's block progress bar: filled 8px segments for a known value, a
 * stepping runner while the end is unknown. Renders nothing when Arcade is off.
 */
export function PixelProgress({ value, label }: { value?: number; label: string }) {
  const arcade = useArcade()
  if (!arcade) return null
  const known = value !== undefined
  const pct = known ? Math.max(0, Math.min(100, value)) : undefined
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={known ? 0 : undefined}
      aria-valuemax={known ? 100 : undefined}
      aria-valuenow={pct === undefined ? undefined : Math.round(pct)}
      className="arcade-progress"
    >
      {known ? (
        <span className="arcade-progress-fill" style={{ width: `${pct}%` }} />
      ) : (
        <span className="arcade-anim arcade-progress-runner" />
      )}
    </div>
  )
}

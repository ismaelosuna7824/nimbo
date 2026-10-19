import { useEffect, useState } from "react"
import { AppLogo } from "@/components/app-logo"
import { useArcadeStore } from "./arcade-store"

const SPARKLE_COLORS = ["var(--arcade-violet)", "var(--arcade-cyan)", "var(--arcade-pink)", "var(--arcade-lime)", "var(--arcade-amber)"]

/** Twelve sparkles on a ring, alternating far/near so the burst reads as pixels, not a circle. */
const SPARKLES = Array.from({ length: 12 }, (_, i) => {
  const angle = (i / 12) * Math.PI * 2
  const reach = i % 2 === 0 ? 76 : 52
  return {
    dx: Math.round((Math.cos(angle) * reach) / 4) * 4,
    dy: Math.round((Math.sin(angle) * reach) / 4) * 4,
    size: i % 3 === 0 ? 8 : 4,
    color: SPARKLE_COLORS[i % SPARKLE_COLORS.length],
  }
})

const DURATION_MS = 2600

/**
 * The mascot hops and throws pixel sparkles after a successful deploy. Only drawn
 * while Arcade is on; reduced motion keeps the still frame (see arcade.css).
 */
export function ArcadeCelebration() {
  const arcade = useArcadeStore((s) => s.arcade)
  const celebration = useArcadeStore((s) => s.celebration)
  // The celebration that already finished playing; a newer one is on screen.
  const [done, setDone] = useState(0)

  useEffect(() => {
    if (celebration === 0) return
    const timer = setTimeout(() => setDone(celebration), DURATION_MS)
    return () => clearTimeout(timer)
  }, [celebration])

  if (!arcade || celebration === done) return null
  return (
    <div key={celebration} className="arcade-celebration" aria-hidden="true">
      {SPARKLES.map((s, i) => (
        <span
          key={i}
          className="arcade-anim arcade-sparkle"
          style={{ "--dx": `${s.dx}px`, "--dy": `${s.dy}px`, width: s.size, height: s.size, background: s.color } as React.CSSProperties}
        />
      ))}
      <AppLogo className="arcade-anim arcade-mascot" style={{ width: 72, height: 72 }} />
      <span className="arcade-anim arcade-banner">Deployed!</span>
    </div>
  )
}

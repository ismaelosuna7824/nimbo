import { AppLogo } from "@/components/app-logo"
import type { WorkflowIcon } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { DEFAULT_TILE_COLOR, tileColorById } from "./colors"
import { glyphById } from "./glyphs"
import { PixelGlyph } from "./pixel-glyph"

/**
 * Glyph size inside a tile: a multiple of 8 CSS px when that leaves sensible
 * padding, so each glyph pixel maps to whole device pixels on 2× screens.
 */
function glyphSize(size: number): number {
  const snapped = Math.floor((size * 0.85) / 8) * 8
  return snapped >= size * 0.6 ? snapped : Math.round(size * 0.72)
}

/**
 * A workflow's pixel-art icon on its tinted tile; the Nimbo mascot for the
 * explicit mascot choice (MASCOT_GLYPH), a missing icon or a glyph this
 * build doesn't know.
 */
export function WorkflowIconTile({ icon, size = 32, className }: { icon?: WorkflowIcon | null; size?: number; className?: string }) {
  const glyph = icon ? glyphById(icon.glyph) : undefined
  if (!icon || !glyph) return <AppLogo className={className} style={{ width: size, height: size }} />
  const color = tileColorById(icon.color) ?? DEFAULT_TILE_COLOR
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-flex shrink-0 items-center justify-center border select-none",
        "shadow-[inset_0_1px_0_rgb(255_255_255/0.1),0_1px_2px_rgb(0_0_0/0.25)]",
        "dark:shadow-[inset_0_1px_0_rgb(255_255_255/0.08),0_0_8px_-1px_var(--tile-glow)]",
        className,
      )}
      style={
        {
          width: size,
          height: size,
          borderRadius: Math.round(size * 0.22),
          borderColor: `${color.base}b3`,
          backgroundImage: `linear-gradient(160deg, ${color.bgFrom}, ${color.bgTo})`,
          "--tile-glow": `${color.base}80`,
        } as React.CSSProperties
      }
    >
      <PixelGlyph glyph={glyph} color={color} size={glyphSize(size)} />
    </span>
  )
}

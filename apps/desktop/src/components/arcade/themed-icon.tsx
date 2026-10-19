import type { LucideIcon, LucideProps } from "lucide-react"
import { DEFAULT_TILE_COLOR, tileColorById } from "@/components/workflow-icon/colors"
import { glyphById } from "@/components/workflow-icon/glyphs"
import { PixelGlyph } from "@/components/workflow-icon/pixel-glyph"
import { cn } from "@/lib/utils"
import { useArcade } from "./arcade-store"
import { PIXEL_ICONS, type PixelIconName } from "./pixel-icons"

type ThemedIconProps = LucideProps & {
  /** Drawn whenever Arcade is off. */
  icon: LucideIcon
} & (
    | { /** One-color Arcade icon that follows the text color. */ pixel: PixelIconName; glyph?: never; tint?: never }
    | {
        /** Full-color sprite from the workflow glyphs (glyphs.ts), for headline actions. */
        glyph: string
        /** Tile color id for the sprite's tinted tones. */
        tint?: string
        pixel?: never
      }
  )

/** A lucide icon, swapped for a pixel icon while the Arcade style is on. */
export function ThemedIcon({ icon: Icon, pixel, glyph, tint, className, ...props }: ThemedIconProps) {
  const arcade = useArcade()
  if (!arcade) return <Icon className={className} {...props} />
  const def = glyph ? glyphById(glyph) : PIXEL_ICONS[pixel!]
  if (!def) return <Icon className={className} {...props} />
  return (
    <PixelGlyph
      glyph={def}
      color={(tint && tileColorById(tint)) || DEFAULT_TILE_COLOR}
      size={16}
      className={cn("pixel-icon shrink-0", className)}
    />
  )
}

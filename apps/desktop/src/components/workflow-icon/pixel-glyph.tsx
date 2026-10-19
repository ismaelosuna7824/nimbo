import type { TileColor } from "./colors"
import type { PixelGlyphDef } from "./glyphs"

interface PixelRect {
  x: number
  y: number
  w: number
  h: number
  ch: string
}

const rectCache = new WeakMap<PixelGlyphDef, PixelRect[]>()

/**
 * Splits a glyph into rectangles: horizontal runs of one character, merged
 * downwards while the run below has the same span and character.
 */
function rectsOf(glyph: PixelGlyphDef): PixelRect[] {
  const cached = rectCache.get(glyph)
  if (cached) return cached
  const done: PixelRect[] = []
  let open = new Map<string, PixelRect>()
  glyph.rows.forEach((row, y) => {
    const next = new Map<string, PixelRect>()
    for (let x = 0; x < row.length; ) {
      const ch = row[x]
      let end = x + 1
      while (end < row.length && row[end] === ch) end++
      if (ch !== ".") {
        const key = `${x}:${end}:${ch}`
        const above = open.get(key)
        if (above) {
          above.h++
          open.delete(key)
          next.set(key, above)
        } else {
          next.set(key, { x, y, w: end - x, h: 1, ch })
        }
      }
      x = end
    }
    for (const r of open.values()) done.push(r)
    open = next
  })
  for (const r of open.values()) done.push(r)
  rectCache.set(glyph, done)
  return done
}

function fillOf(glyph: PixelGlyphDef, ch: string, color: TileColor): string {
  const v = glyph.palette[ch]
  switch (v) {
    case "ink":
    case "light":
    case "base":
    case "dark":
      return color[v]
    default:
      return v ?? "transparent"
  }
}

/** A 16×16 glyph as crisp SVG rectangles, sized in CSS pixels. */
export function PixelGlyph({ glyph, color, size, className }: { glyph: PixelGlyphDef; color: TileColor; size: number; className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      className={className}
      width={size}
      height={size}
      // Inline size wins over menu/command item rules that resize nested svgs.
      style={{ width: size, height: size }}
      shapeRendering="crispEdges"
      aria-hidden="true"
      focusable="false"
    >
      {rectsOf(glyph).map((r) => (
        <rect key={`${r.x}:${r.y}`} x={r.x} y={r.y} width={r.w} height={r.h} fill={fillOf(glyph, r.ch, color)} />
      ))}
    </svg>
  )
}

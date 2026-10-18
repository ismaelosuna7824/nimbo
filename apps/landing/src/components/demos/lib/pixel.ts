// Build-time helpers that turn the app's 16×16 pixel glyphs into SVG rects
// (same run-merging as apps/desktop/src/components/workflow-icon/pixel-glyph.tsx).
import { DEFAULT_TILE_COLOR, tileColorById, type TileColor } from "./colors";
import { glyphById, type PixelGlyphDef } from "./glyphs";

export type GlyphId =
  | "cloud"
  | "bolt"
  | "database"
  | "envelope"
  | "bucket"
  | "globe"
  | "shield"
  | "key"
  | "gear"
  | "server"
  | "chip"
  | "network"
  | "flow"
  | "broadcast"
  | "clock"
  | "rocket"
  | "robot"
  | "star"
  | "fire"
  | "leaf"
  | "diamond"
  | "potion";

export type TileColorId = "violet" | "cyan" | "pink" | "lime" | "amber" | "red" | "blue" | "slate";

export interface PixelRect {
  x: number;
  y: number;
  w: number;
  h: number;
  fill: string;
}

function fillOf(glyph: PixelGlyphDef, ch: string, color: TileColor): string {
  const v = glyph.palette[ch];
  switch (v) {
    case "ink":
    case "light":
    case "base":
    case "dark":
      return color[v];
    default:
      return v ?? "transparent";
  }
}

/** Horizontal runs of one character, merged downwards while the run below matches. */
export function glyphRects(id: GlyphId, colorId: TileColorId = "violet"): PixelRect[] {
  const glyph = glyphById(id);
  if (!glyph) throw new Error(`Unknown glyph ${id}`);
  const color = tileColorById(colorId) ?? DEFAULT_TILE_COLOR;
  const done: Array<Omit<PixelRect, "fill"> & { ch: string }> = [];
  let open = new Map<string, Omit<PixelRect, "fill"> & { ch: string }>();
  glyph.rows.forEach((row, y) => {
    const next = new Map<string, Omit<PixelRect, "fill"> & { ch: string }>();
    for (let x = 0; x < row.length; ) {
      const ch = row[x]!;
      let end = x + 1;
      while (end < row.length && row[end] === ch) end++;
      if (ch !== ".") {
        const key = `${x}:${end}:${ch}`;
        const above = open.get(key);
        if (above) {
          above.h++;
          open.delete(key);
          next.set(key, above);
        } else {
          next.set(key, { x, y, w: end - x, h: 1, ch });
        }
      }
      x = end;
    }
    for (const r of open.values()) done.push(r);
    open = next;
  });
  for (const r of open.values()) done.push(r);
  return done.map(({ ch, ...r }) => ({ ...r, fill: fillOf(glyph, ch, color) }));
}

export function tileColor(colorId: TileColorId = "violet"): TileColor {
  return tileColorById(colorId) ?? DEFAULT_TILE_COLOR;
}

import type { PixelGlyphDef } from "@/components/workflow-icon/glyphs"

/**
 * One-color chrome icons for the Arcade style, drawn on an 8×8 grid and
 * doubled to the 16×16 glyph grid, so every art pixel is a 2×2 block that
 * stays crisp at 16 CSS px. "X" is drawn in currentColor.
 */
const ART = {
  search: [".XXX....", "X...X...", "X...X...", "X...X...", ".XXXX...", "....XX..", ".....XX.", "......XX"],
  check: ["XXXXXXXX", "X......X", "X.....XX", "X....X.X", "XX..X..X", "X.XX...X", "X......X", "XXXXXXXX"],
  list: ["........", "XX.XXXXX", "........", "XX.XXXXX", "........", "XX.XXXXX", "........", "........"],
  trash: ["..XXXX..", "XXXXXXXX", ".X....X.", ".X.XX.X.", ".X.XX.X.", ".X.XX.X.", ".X....X.", "..XXXX.."],
  warning: ["...XX...", "..XXXX..", "..X..X..", ".XX..XX.", ".XXXXXX.", "XXX..XXX", "XXXXXXXX", "........"],
  upload: ["...XX...", "..XXXX..", ".XXXXXX.", "...XX...", "...XX...", "X......X", "X......X", "XXXXXXXX"],
  download: ["...XX...", "...XX...", ".XXXXXX.", "..XXXX..", "...XX...", "X......X", "X......X", "XXXXXXXX"],
  sun: ["...XX...", ".X....X.", "...XX...", "X.XXXX.X", "X.XXXX.X", "...XX...", ".X....X.", "...XX..."],
  moon: ["..XXXX..", ".XXX....", "XXX.....", "XX......", "XX......", "XXX....X", ".XXXXXXX", "..XXXXX."],
  gear: ["...XX...", ".XXXXXX.", ".XX..XX.", "XX....XX", "XX....XX", ".XX..XX.", ".XXXXXX.", "...XX..."],
  panel: ["XXXXXXXX", "X...XXXX", "X...XXXX", "X...XXXX", "X...XXXX", "X...XXXX", "X...XXXX", "XXXXXXXX"],
  code: ["........", "..X..X..", ".X....X.", "X......X", ".X....X.", "..X..X..", "........", "........"],
  sliders: ["..X.....", "XXXXXXXX", "..X.....", "........", ".....X..", "XXXXXXXX", ".....X..", "........"],
  rocket: ["...XX...", "..XXXX..", "..X..X..", "..XXXX..", "..XXXX..", ".XXXXXX.", "XX.XX.XX", "...XX..."],
  plug: ["..X..X..", "..X..X..", ".XXXXXX.", ".XXXXXX.", "..XXXX..", "...XX...", "...XX...", "...XX..."],
  gauge: ["........", "..XXXX..", ".X....X.", "X....X.X", "X...X..X", "X..XX..X", "XXXXXXXX", "........"],
  star: ["...XX...", "...XX...", "XXXXXXXX", ".XXXXXX.", "..XXXX..", ".XXXXXX.", ".XX..XX.", "X......X"],
  zap: ["....XX..", "...XX...", "..XX....", ".XXXXXX.", "....XX..", "...XX...", "..XX....", ".X......"],
  plus: ["........", "...XX...", "...XX...", ".XXXXXX.", ".XXXXXX.", "...XX...", "...XX...", "........"],
  close: ["........", ".X....X.", "..X..X..", "...XX...", "...XX...", "..X..X..", ".X....X.", "........"],
} satisfies Record<string, readonly string[]>

export type PixelIconName = keyof typeof ART

export const PIXEL_ICONS: Record<PixelIconName, PixelGlyphDef> = Object.fromEntries(
  Object.entries(ART).map(([id, rows]): [string, PixelGlyphDef] => [
    id,
    {
      id,
      label: id,
      keywords: "",
      palette: { X: "currentColor" },
      rows: rows.flatMap((row) => {
        const wide = row.replace(/./g, "$&$&")
        return [wide, wide]
      }),
    },
  ]),
) as Record<PixelIconName, PixelGlyphDef>

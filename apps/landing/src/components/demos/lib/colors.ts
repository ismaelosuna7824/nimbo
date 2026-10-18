// Copied from apps/desktop/src/components/workflow-icon/colors.ts (build-time only).
/**
 * Tile colors for workflow icons. Each tints the glyph tones (ink outline,
 * light/base/dark fills) and the dark tile gradient; the tile stays dark in
 * both app themes, like the Nimbo mascot tile.
 */
export interface TileColor {
  id: string
  label: string
  ink: string
  light: string
  base: string
  dark: string
  /** Tile gradient, top to bottom. */
  bgFrom: string
  bgTo: string
}

export const TILE_COLORS: readonly TileColor[] = [
  { id: "violet", label: "Violet", ink: "#1a0b33", light: "#dcc4ff", base: "#a66bff", dark: "#6a2fd0", bgFrom: "#2e1856", bgTo: "#120a24" },
  { id: "cyan", label: "Cyan", ink: "#04202a", light: "#b8f6ff", base: "#22d3ee", dark: "#0b7d96", bgFrom: "#0c3a48", bgTo: "#04161d" },
  { id: "pink", label: "Pink", ink: "#2e0820", light: "#ffc6e8", base: "#ff4fb8", dark: "#b0187a", bgFrom: "#4d1338", bgTo: "#1a0614" },
  { id: "lime", label: "Lime", ink: "#132104", light: "#e6ffad", base: "#a3e635", dark: "#5a8a0e", bgFrom: "#28420c", bgTo: "#0c1604" },
  { id: "amber", label: "Amber", ink: "#2b1a02", light: "#ffe8a6", base: "#fbbf24", dark: "#b06a05", bgFrom: "#4d3006", bgTo: "#1a1002" },
  { id: "red", label: "Red", ink: "#2e060b", light: "#ffbcbc", base: "#ff4d5e", dark: "#b0172b", bgFrom: "#521019", bgTo: "#1c0408" },
  { id: "blue", label: "Blue", ink: "#07142e", light: "#c0d6ff", base: "#4d8dff", dark: "#1d4fc2", bgFrom: "#14295e", bgTo: "#060d22" },
  { id: "slate", label: "Slate", ink: "#0f141c", light: "#e2e8f0", base: "#94a3b8", dark: "#4b5a70", bgFrom: "#2c3545", bgTo: "#0e1118" },
]

export const DEFAULT_TILE_COLOR = TILE_COLORS[0]

const BY_ID: Record<string, TileColor> = Object.fromEntries(TILE_COLORS.map((c) => [c.id, c]))

/** The color with this ID, or undefined for unknown IDs. */
export function tileColorById(id: string): TileColor | undefined {
  return Object.hasOwn(BY_ID, id) ? BY_ID[id] : undefined
}

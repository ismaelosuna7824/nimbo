// Colors, fonts and text fitting of the Architecture diagram (a light document whatever the app theme).
import { CARD_W, FONT_FAMILY, wrapParts } from "./geometry"
import type { GroupKind, SubnetTier } from "./model"

export const INK = "#0f172a"
export const MUTED = "#475569"
export const SUBTLE = "#64748b"
export const LINE = "#d5dbe3"
export const ACCENT = "#f97316"

export const TITLE_FONT = `600 13px ${FONT_FAMILY}`
export const BODY_FONT = `400 11px ${FONT_FAMILY}`
export const GROUP_FONT = `600 12px ${FONT_FAMILY}`
export const GROUP_SUB_FONT = `400 11px ${FONT_FAMILY}`

/** Left edge of a card's text (after the icon) and the width it may use. */
export const CARD_TEXT_X = 64
export const CARD_TEXT_W = CARD_W - CARD_TEXT_X - 12

interface GroupStyle {
  fill: string
  stroke: string
  dash?: string
}

/** Light grey nested containers: translucent fills darken with depth; subnets carry their tier's tint. */
export function groupStyle(kind: GroupKind, tier?: SubnetTier): GroupStyle {
  switch (kind) {
    case "cloud":
      return { fill: "#ffffff", stroke: "#b9c3ce" }
    case "account":
      return { fill: "rgba(15,23,42,0.025)", stroke: "#c3ccd6", dash: "6 4" }
    case "region":
      return { fill: "rgba(15,23,42,0.025)", stroke: "#9fb0c0", dash: "6 4" }
    case "vpc":
      return { fill: "rgba(15,23,42,0.035)", stroke: "#b8c2cd" }
    case "az":
      return { fill: "none", stroke: "#a3b1bf", dash: "4 4" }
    case "subnet":
      return tier === "public"
        ? { fill: "rgba(34,160,80,0.08)", stroke: "#9ccfaa" }
        : tier === "private"
          ? { fill: "rgba(37,99,200,0.07)", stroke: "#a9c1e3" }
          : { fill: "rgba(15,23,42,0.04)", stroke: "#c3cbd4" }
    case "external":
      return { fill: "rgba(15,23,42,0.02)", stroke: "#b9c3ce", dash: "6 4" }
    default:
      return { fill: "rgba(15,23,42,0.035)", stroke: "rgba(15,23,42,0.06)" }
  }
}

/** A card's description parts ("orders · postgres 16 · SG db: in 5432 from SG app") on up to two lines. */
export function cardLines(parts: string[]) {
  return wrapParts(parts.filter(Boolean), BODY_FONT, CARD_TEXT_W, 2)
}

export interface LegendEntry {
  kind: "data" | "control" | "public" | "private"
  label: string
}

export const LEGEND_ROW = 22

export function legendHeight(entries: LegendEntry[]) {
  return 40 + entries.length * LEGEND_ROW
}

export interface CardText {
  title: string
  lines: string[]
  /** Full text for tooltips. */
  tooltip: string
}

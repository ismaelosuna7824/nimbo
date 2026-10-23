// SVG building blocks of the Architecture diagram. The canvas renders them inside React Flow nodes and
// edges, and the image export composes the same elements into one standalone SVG, so both look alike.
import type { ReactNode } from "react"
import { BORDER_CARD_H, BORDER_CARD_W, CARD_H, CARD_W, FONT_FAMILY, fitText, LEGEND_W } from "./geometry"
import type { FlowKind, GroupKind, NetworkScope, SubnetTier } from "./model"
import {
  ACCENT,
  CARD_TEXT_W,
  CARD_TEXT_X,
  GROUP_FONT,
  GROUP_SUB_FONT,
  groupStyle,
  INK,
  legendHeight,
  LEGEND_ROW,
  LINE,
  MUTED,
  SUBTLE,
  TITLE_FONT,
  type CardText,
  type LegendEntry,
} from "./styles"

/** Shared <defs>: arrowheads and the card shadow. Rendered once per SVG document. */
export function DiagramDefs() {
  return (
    <defs>
      {(["data", "control"] as const).map((k) => (
        <marker
          key={k}
          id={`arch-arrow-${k}`}
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="7"
          markerHeight="7"
          orient="auto-start-reverse"
          markerUnits="userSpaceOnUse"
        >
          <path d="M 0 0 L 10 5 L 0 10 z" fill={k === "data" ? "#334155" : "#94a3b8"} />
        </marker>
      ))}
      <marker id="arch-arrow-selected" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse" markerUnits="userSpaceOnUse">
        <path d="M 0 0 L 10 5 L 0 10 z" fill={ACCENT} />
      </marker>
      <filter id="arch-card-shadow" x="-10%" y="-10%" width="120%" height="140%">
        <feDropShadow dx="0" dy="1" stdDeviation="1.5" floodColor="#0f172a" floodOpacity="0.08" />
      </filter>
    </defs>
  )
}

export function GroupShape({
  width,
  height,
  kind,
  tier,
  scope,
  label,
  sublabel,
  icon,
  iconWidth = 18,
  selected,
}: {
  width: number
  height: number
  kind: GroupKind
  tier?: SubnetTier
  scope?: NetworkScope
  label: string
  sublabel?: readonly string[]
  icon?: string
  /** Width of the label icon (wordmark logos are wider than tall). */
  iconWidth?: number
  selected?: boolean
}) {
  const s = groupStyle(kind, tier, scope)
  const textX = icon ? 18 + iconWidth : 14
  const max = Math.max(40, width - textX - 12)
  const logical = kind === "logical" || kind === "other"
  return (
    <g>
      <rect
        x={0.5}
        y={0.5}
        width={Math.max(0, width - 1)}
        height={Math.max(0, height - 1)}
        rx={kind === "cloud" || kind === "docker-host" || kind === "k8s-cluster" ? 12 : 10}
        fill={s.fill}
        stroke={selected ? ACCENT : s.stroke}
        strokeWidth={selected ? 2 : 1.25}
        strokeDasharray={selected ? undefined : s.dash}
      />
      {icon && <image href={icon} x={12} y={10} width={iconWidth} height={18} preserveAspectRatio="xMinYMid meet" />}
      <text
        x={textX}
        y={23}
        fill={logical ? SUBTLE : "#334155"}
        fontFamily={FONT_FAMILY}
        fontSize={logical ? 11 : 12}
        fontWeight={600}
        letterSpacing={logical ? 0.6 : 0}
      >
        {/* A wordmark logo (wider than tall) already spells the name. */}
        {icon && iconWidth > 18 && kind === "cloud" ? null : fitText(logical ? label.toUpperCase() : label, logical ? `600 11px ${FONT_FAMILY}` : GROUP_FONT, max)}
      </text>
      {sublabel?.map((line, i) => (
        <text key={i} x={textX} y={39 + i * 14} fill={SUBTLE} fontFamily={FONT_FAMILY} fontSize={11}>
          {fitText(line, GROUP_SUB_FONT, max)}
        </text>
      ))}
    </g>
  )
}

export function CardShape({
  icon,
  text,
  selected,
  existing,
  onBorder,
}: {
  icon: string
  text: CardText
  selected?: boolean
  existing?: boolean
  /** The small variant drawn on a group's border: icon and title only. */
  onBorder?: boolean
}) {
  const width = onBorder ? BORDER_CARD_W : CARD_W
  const height = onBorder ? BORDER_CARD_H : CARD_H
  const iconSize = onBorder ? 24 : 38
  const iconX = onBorder ? 9 : 14
  const textX = onBorder ? iconX + iconSize + 8 : CARD_TEXT_X
  const max = onBorder ? width - textX - 10 : CARD_TEXT_W
  const lines = text.lines.slice(0, 2)
  const titleY = onBorder || lines.length === 0 ? height / 2 + 4.5 : lines.length === 1 ? 34 : 28
  return (
    <g>
      <title>{text.tooltip}</title>
      <rect
        x={0.5}
        y={0.5}
        width={width - 1}
        height={height - 1}
        rx={8}
        fill="#ffffff"
        stroke={selected ? ACCENT : LINE}
        strokeWidth={selected ? 2 : 1}
        strokeDasharray={existing && !selected ? "4 3" : undefined}
        filter="url(#arch-card-shadow)"
      />
      <image href={icon} x={iconX} y={(height - iconSize) / 2} width={iconSize} height={iconSize} preserveAspectRatio="xMidYMid meet" />
      <text x={textX} y={titleY} fill={INK} fontFamily={FONT_FAMILY} fontSize={13} fontWeight={600}>
        {fitText(text.title, TITLE_FONT, max)}
      </text>
      {lines.map((l, i) => (
        <text key={i} x={textX} y={titleY + 17 + i * 15} fill={MUTED} fontFamily={FONT_FAMILY} fontSize={11}>
          {l}
        </text>
      ))}
    </g>
  )
}

export function ActorShape({
  width,
  icon,
  label,
  sublabel,
  selected,
}: {
  width: number
  icon: string
  label: string
  sublabel?: string
  selected?: boolean
}) {
  return (
    <g>
      <title>{sublabel ? `${label} — ${sublabel}` : label}</title>
      {selected && <rect x={0.5} y={0.5} width={width - 1} height={95} rx={10} fill="none" stroke={ACCENT} strokeWidth={2} />}
      <image href={icon} x={(width - 44) / 2} y={8} width={44} height={44} preserveAspectRatio="xMidYMid meet" />
      <text x={width / 2} y={70} textAnchor="middle" fill={INK} fontFamily={FONT_FAMILY} fontSize={12} fontWeight={600}>
        {fitText(label, `600 12px ${FONT_FAMILY}`, width - 8)}
      </text>
      {sublabel && (
        <text x={width / 2} y={85} textAnchor="middle" fill={SUBTLE} fontFamily={FONT_FAMILY} fontSize={10.5}>
          {fitText(sublabel, `400 10.5px ${FONT_FAMILY}`, width - 4)}
        </text>
      )}
    </g>
  )
}

export function EdgeShape({ path, kind, selected }: { path: string; kind: FlowKind; selected?: boolean }) {
  return (
    <path
      d={path}
      fill="none"
      stroke={selected ? ACCENT : kind === "data" ? "#334155" : "#94a3b8"}
      strokeWidth={selected ? 2 : kind === "data" ? 1.5 : 1.25}
      strokeDasharray={kind === "control" ? "5 4" : undefined}
      strokeLinejoin="round"
      markerEnd={`url(#arch-arrow-${selected ? "selected" : kind})`}
    />
  )
}

export function HeaderShape({ title, subtitle, width }: { title: string; subtitle?: string; width: number }) {
  return (
    <g>
      <text x={0} y={26} fill={INK} fontFamily={FONT_FAMILY} fontSize={24} fontWeight={700}>
        {fitText(title, `700 24px ${FONT_FAMILY}`, Math.max(200, width))}
      </text>
      {subtitle && (
        <text x={0} y={50} fill={SUBTLE} fontFamily={FONT_FAMILY} fontSize={13}>
          {fitText(subtitle, `400 13px ${FONT_FAMILY}`, Math.max(200, width))}
        </text>
      )}
    </g>
  )
}

export function LegendShape({ entries }: { entries: LegendEntry[] }) {
  const h = legendHeight(entries)
  const row = (e: LegendEntry, i: number): ReactNode => {
    const y = 44 + i * LEGEND_ROW
    let sample: ReactNode
    if (e.kind === "data" || e.kind === "control") {
      sample = <EdgeShape path={`M 14 ${y - 4} L 50 ${y - 4}`} kind={e.kind} />
    } else {
      const s =
        e.kind === "internal-network" || e.kind === "external-network"
          ? groupStyle("docker-network", undefined, e.kind === "internal-network" ? "internal" : "external")
          : groupStyle("subnet", e.kind)
      sample = <rect x={16} y={y - 12} width={32} height={16} rx={4} fill={s.fill} stroke={s.stroke} strokeDasharray={s.dash} />
    }
    return (
      <g key={e.kind}>
        {sample}
        <text x={62} y={y} fill={MUTED} fontFamily={FONT_FAMILY} fontSize={11}>
          {e.label}
        </text>
      </g>
    )
  }
  return (
    <g>
      <rect x={0.5} y={0.5} width={LEGEND_W - 1} height={h - 1} rx={8} fill="#ffffff" stroke={LINE} />
      <text x={14} y={22} fill="#334155" fontFamily={FONT_FAMILY} fontSize={11} fontWeight={600} letterSpacing={0.6}>
        LEGEND
      </text>
      {entries.map(row)}
    </g>
  )
}

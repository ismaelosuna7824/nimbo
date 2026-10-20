import type { Workflow } from "@/lib/api/types"
import { typeLabel } from "./derive"
import { HEADER_GAP, HEADER_H, LEGEND_GAP, LEGEND_W, type Rect } from "./geometry"
import { actorIcon, archIconFor, groupIcon, providerLogo, type ArchGroupKind } from "./icons"
import type { Diagram } from "./layout"
import { PROVIDER_SHORT, type ArchActor, type ArchCard, type ArchGroup, type ArchModel } from "./model"
import { cardLines, legendHeight, type CardText, type LegendEntry } from "./styles"

/** Service names (icon short names) that more than one resource type of the model shares. */
export function sharedServiceNames(cards: ArchCard[]) {
  const types = new Map<string, Set<string>>()
  for (const c of cards) {
    const short = archIconFor(c.provider ?? "aws", c.type).short
    types.set(short, (types.get(short) ?? new Set()).add(c.type))
  }
  return new Set([...types].filter(([, t]) => t.size > 1).map(([short]) => short))
}

/**
 * Icon, title and description lines of a card. Cards of a service that several types share (Pub/Sub
 * topic and subscription) are titled with their resource type.
 */
export function cardScene(card: ArchCard, labelOf: (type: string) => string | undefined, shared: Set<string>) {
  const icon = archIconFor(card.provider ?? "aws", card.type)
  const service = !card.provider || icon.generic || shared.has(icon.short) ? labelOf(card.type) || typeLabel(card.type) : icon.short
  const title = card.label || service
  // A data source named "existing" is not "existing · existing".
  const parts = [...new Set([card.label ? service : "", card.name, ...card.details, ...(card.security ?? []), card.folded ?? ""].filter(Boolean))]
  const text: CardText = {
    title,
    lines: card.onBorder ? [] : cardLines(parts),
    tooltip: [title, parts.join("\n"), card.nodeId].join("\n"),
  }
  return { icon: icon.src, text }
}

const GROUP_ICON_KIND: Partial<Record<ArchGroup["kind"], ArchGroupKind>> = {
  account: "account",
  region: "region",
  vpc: "vpc",
  az: "az",
}

/** The Google Cloud logo is a wordmark (about 4.5:1); other label icons are square. */
export function groupIconWidth(g: ArchGroup) {
  return g.kind === "cloud" && g.provider === "gcp" ? 80 : 18
}

/** Label icon of a container: provider logo, region/VPC/subnet icons; none for logical groups. */
export function groupIconOf(g: ArchGroup): string | undefined {
  if (!g.provider) return undefined
  if (g.kind === "cloud") return providerLogo(g.provider)
  if (g.kind === "account" && g.provider === "azure") return groupIcon(g.provider, "resourceGroup")
  if (g.kind === "subnet") return groupIcon(g.provider, g.tier === "public" ? "subnet-public" : g.tier === "private" ? "subnet-private" : "subnet")
  const kind = GROUP_ICON_KIND[g.kind]
  return kind ? groupIcon(g.provider, kind) : undefined
}

export function actorIconOf(a: ArchActor) {
  return actorIcon(a.kind === "users" ? "users" : a.kind === "internet" ? "internet" : "service").src
}

/** "AWS · us-east-1 · dev" from the clouds and regions of the design and the active environment. */
export function defaultSubtitle(model: ArchModel, wf: Pick<Workflow, "activeEnvironment">) {
  return [
    model.providers.map((p) => PROVIDER_SHORT[p]).join(" + "),
    model.regions.join(", "),
    wf.activeEnvironment ?? "",
  ]
    .filter(Boolean)
    .join(" · ")
}

export function legendEntries(model: ArchModel, diagram: Diagram): LegendEntry[] {
  const out: LegendEntry[] = [{ kind: "data", label: "Data & requests" }]
  if (diagram.edges.some((e) => e.kind === "control")) out.push({ kind: "control", label: "Identity, keys & telemetry" })
  if (model.groups.some((g) => g.kind === "subnet" && g.tier === "public")) out.push({ kind: "public", label: "Public subnet" })
  if (model.groups.some((g) => g.kind === "subnet" && g.tier === "private")) out.push({ kind: "private", label: "Private subnet" })
  return out
}

export interface Decorations {
  title: string
  subtitle: string
  legend: LegendEntry[]
  header: Rect
  legendRect: Rect
}

/** Title block above the diagram (left) and legend below it (right). */
export function decorations(model: ArchModel, diagram: Diagram, wf: Pick<Workflow, "name" | "activeEnvironment">): Decorations {
  const b = diagram.bounds
  const legend = legendEntries(model, diagram)
  return {
    title: wf.name,
    subtitle: defaultSubtitle(model, wf),
    legend,
    header: { x: b.x, y: b.y - HEADER_H - HEADER_GAP, width: Math.max(b.width, 320), height: HEADER_H },
    legendRect: { x: b.x + Math.max(b.width, LEGEND_W) - LEGEND_W, y: b.y + b.height + LEGEND_GAP, width: LEGEND_W, height: legendHeight(legend) },
  }
}

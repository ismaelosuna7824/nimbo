import { ALL_CLOUDS, type Cloud, type EdgePattern, type Template } from "@/lib/api/types"

export type Kind = "terraform" | "docker" | "kubernetes"

/** Workflow kind → the dialog's kind ("" Terraform). */
export function dialogKind(kind: string | undefined): Kind {
  return kind === "docker" || kind === "kubernetes" ? kind : "terraform"
}

/** The Edge patterns group, in the order the dialog lists them. */
export const EDGE_PATTERNS: { id: EdgePattern; label: string }[] = [
  { id: "reverse-proxy", label: "Reverse proxy" },
  { id: "api-gateway", label: "API gateway" },
  { id: "load-balancer", label: "Load balancer" },
]

export function patternLabel(p: EdgePattern | undefined): string | undefined {
  return EDGE_PATTERNS.find((e) => e.id === p)?.label
}

/** "all" lists every template; "edge-patterns" only the Edge patterns group. */
export type TemplateFilter = "all" | "edge-patterns"

export interface TemplateSections {
  /** The general starters (none while filtering on Edge patterns). */
  general: Template[]
  /** Edge patterns: reverse proxy, API gateway, load balancer, then by cloud. */
  edge: Template[]
}

/**
 * Splits the templates of a kind into the general starters and the Edge
 * patterns group. level filters by difficulty ("all" keeps every one);
 * cloud narrows the Edge patterns of Terraform to one provider.
 */
export function templateSections(
  templates: Template[],
  { kind, level, filter, cloud }: { kind: Kind; level: string; filter: TemplateFilter; cloud: Cloud | "all" },
): TemplateSections {
  const ofKind = templates.filter((t) => dialogKind(t.kind) === kind && (level === "all" || t.difficulty === level))
  const order = (t: Template) => EDGE_PATTERNS.findIndex((p) => p.id === t.pattern) * ALL_CLOUDS.length + (t.cloud ? ALL_CLOUDS.indexOf(t.cloud) : 0)
  const edge = ofKind
    .filter((t) => t.category === "edge-patterns" && (kind !== "terraform" || cloud === "all" || t.cloud === cloud))
    .sort((a, b) => order(a) - order(b))
  const general = filter === "all" ? ofKind.filter((t) => t.category !== "edge-patterns") : []
  return { general, edge }
}

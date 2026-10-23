// The Architecture diagram of a Docker workspace: the compose graph drawn as the machine running
// it (Docker host), the compose project, its networks and one card per service. Volumes, secrets,
// configs and Dockerfile stages are lines on their service's card, not cards of their own.
import { techIcon, techIconFor } from "@/components/tech-icons"
import type { ComposeEdge, ComposeMount, ComposeNode, ComposeServiceStatus, ComposeStatus, ServiceFacts, Workflow } from "@/lib/api/types"
import type { ArchCard, ArchEdge, ArchGroup, ArchModel } from "./model"
import { cardLines, type CardText, type LegendEntry } from "./styles"

export const DOCKER_HOST_GROUP = "group:docker-host"
export const COMPOSE_PROJECT_GROUP = "group:compose-project"
export const USERS_ACTOR = "actor:users"

/** Compose puts services that name no network on the project's implicit `default` network. */
const DEFAULT_NETWORK = "default"

export const networkGroupId = (name: string) => `group:network:${name}`

/** Service → service connections of the graph that are requests (depends_on, links, env URLs). */
const REQUEST_EDGES: Partial<Record<ComposeEdge["kind"], true>> = { depends_on: true, links: true, inferred: true }

type DockerWorkflow = Pick<Workflow, "name" | "compose">

/**
 * The architecture of a Docker workspace: Docker host → compose project → one group per network
 * holding the services attached to it (a service on several networks sits in its first one, with a
 * dashed line to each other network), the Users reaching every service that publishes ports, and
 * service → service requests (api → db for `api.depends_on: [db]`, the reverse of the graph's
 * "source is used by target"). `status` adds the containers' state and the host ports actually bound.
 */
export function deriveDockerArchitecture(wf: DockerWorkflow, status?: ComposeStatus | null): ArchModel {
  const model: ArchModel = { groups: [], cards: [], actors: [], edges: [], providers: [], regions: [] }
  const compose = wf.compose
  const nodes = compose?.graph.nodes ?? []
  const graphEdges = compose?.graph.edges ?? []
  const services = nodes.filter((n) => n.kind === "service" && n.service)
  if (!compose || services.length === 0) return model

  const byId = new Map(nodes.map((n) => [n.id, n]))
  model.groups.push(
    { id: DOCKER_HOST_GROUP, kind: "docker-host", label: "Docker host" },
    { id: COMPOSE_PROJECT_GROUP, parent: DOCKER_HOST_GROUP, kind: "compose-project", label: compose.name || wf.name, sublabel: [compose.project] },
  )

  // Networks in the order services first use them, so the layout reads like the compose file.
  const networksOf = new Map(services.map((s) => [s.id, s.service!.networks.length ? s.service!.networks : [DEFAULT_NETWORK]]))
  const networks = [...new Set([...networksOf.values()].flat())]
  for (const name of networks) model.groups.push(networkGroup(name, networkNode(nodes, byId, name)))

  for (const s of services) {
    const [first, ...others] = networksOf.get(s.id)!
    model.cards.push({
      id: s.id,
      parent: networkGroupId(first),
      nodeId: s.id,
      type: "compose_service",
      kind: "resource",
      category: "containers",
      name: s.name,
      details: serviceFacts(s, nodes, byId, graphEdges, status),
      existing: s.inactive || undefined,
    })
    for (const other of others) model.edges.push({ id: `${s.id}->${networkGroupId(other)}`, source: s.id, target: networkGroupId(other), kind: "control" })
  }

  const published = services.filter((s) => s.service!.ports.some((p) => p.published !== false))
  if (published.length) {
    model.actors.push({ id: USERS_ACTOR, kind: "users", label: "Users", sublabel: "published ports" })
    for (const s of published) model.edges.push({ id: `${USERS_ACTOR}->${s.id}`, source: USERS_ACTOR, target: s.id, kind: "data" })
  }

  const serviceIds = new Set(services.map((s) => s.id))
  const seen = new Set<string>()
  for (const e of graphEdges) {
    if (!REQUEST_EDGES[e.kind] || e.source === e.target || !serviceIds.has(e.source) || !serviceIds.has(e.target)) continue
    // The graph points from the used service to its user; requests go the other way.
    const id = `${e.target}->${e.source}`
    if (seen.has(id)) continue
    seen.add(id)
    model.edges.push({ id, source: e.target, target: e.source, kind: "data" } satisfies ArchEdge)
  }
  return model
}

/** The graph node of a network the services name (absent for an implicit `default`). */
function networkNode(nodes: ComposeNode[], byId: Map<string, ComposeNode>, name: string) {
  const isNetwork = (n: ComposeNode | undefined) => !!n && (n.kind === "network" || (n.kind === "external" && n.externalKind === "network"))
  const direct = byId.get(`network:${name}`)
  return isNetwork(direct) ? direct : nodes.find((n) => isNetwork(n) && n.name === name)
}

function networkGroup(name: string, node: ComposeNode | undefined): ArchGroup {
  const external = !!node && (node.external || node.kind === "external")
  const internal = !external && !!node?.network?.internal
  // The Docker name differs from the compose key when `name:` sets it (always for external ones).
  const realName = node?.network?.name && node.network.name !== name ? node.network.name : ""
  const facts = external ? ["external", realName] : [node?.network?.driver || "bridge", internal ? "internal" : "", realName]
  return {
    id: networkGroupId(name),
    parent: COMPOSE_PROJECT_GROUP,
    kind: "docker-network",
    label: name,
    sublabel: [facts.filter(Boolean).join(" · ")],
    scope: external ? "external" : internal ? "internal" : undefined,
    nodeId: node?.id,
  }
}

/**
 * A service card's lines, most telling first (cards show two lines; the tooltip has them all):
 * image or build, replicas, container state, published ports, inactive profiles, Dockerfile stages,
 * mounts, secrets and configs.
 */
function serviceFacts(node: ComposeNode, nodes: ComposeNode[], byId: Map<string, ComposeNode>, edges: ComposeEdge[], status: ComposeStatus | null | undefined) {
  const svc = node.service!
  const live = status?.services.find((s) => s.nodeId === node.id || s.name === node.name)
  const used = (kind: "secret" | "config") =>
    [...new Set(edges.filter((e) => e.kind === kind && e.target === node.id).map((e) => byId.get(e.source)?.name ?? e.source.replace(/^[^:]*:/, "")))].map(
      (name) => `${kind} ${name}`,
    )
  return [
    sourceFact(svc),
    svc.replicas && svc.replicas > 1 ? `×${svc.replicas}` : "",
    live ? [live.state === "absent" ? "not running" : live.state, live.health ?? ""].filter(Boolean).join(" · ") : "",
    ...portFacts(node, live, status),
    node.inactive ? `profile ${(svc.profiles ?? []).join(", ")} · inactive` : "",
    stagesFact(node, nodes, svc),
    ...svc.volumes.map((m) => mountFact(m, byId)),
    ...used("secret"),
    ...used("config"),
  ].filter(Boolean)
}

/** "postgres:16", or "build ./api (Dockerfile.prod, target runtime)" for a built service. */
function sourceFact(svc: ServiceFacts) {
  if (!svc.build) return svc.image?.ref ?? ""
  const { context, dockerfile, target } = svc.build
  const ctx = context === "." || /^(\.|\/|~|[a-z]+:\/\/)/.test(context) ? context : `./${context}`
  const trimmed = context.replace(/^\.\/?/, "").replace(/\/$/, "")
  // The Dockerfile path is relative to the project; the default one says nothing.
  const custom = dockerfile && dockerfile !== "Dockerfile" && dockerfile !== `${trimmed}/Dockerfile` ? dockerfile : ""
  const extra = [custom, target ? `target ${target}` : ""].filter(Boolean).join(", ")
  return `build ${ctx}${extra ? ` (${extra})` : ""}`
}

/** "8080→80/tcp" per published port, with the host port actually bound when the project runs. */
function portFacts(node: ComposeNode, live: ComposeServiceStatus | undefined, status: ComposeStatus | null | undefined) {
  return node.service!.ports.flatMap((p) => {
    if (p.published === false) return []
    const proto = p.protocol || "tcp"
    const container = Number(p.container)
    const bound =
      live?.containers.flatMap((c) => c.ports).find((x) => x.container === container && x.protocol === proto)?.host ??
      status?.remaps.find((r) => r.service === node.name && r.containerPort === container && r.protocol === proto)?.port
    const host = bound !== undefined ? String(bound) : p.host
    const ip = p.hostIp && p.hostIp !== "0.0.0.0" && p.hostIp !== "::" ? `${p.hostIp}:` : ""
    if (!host) return [`${p.container}/${proto} on a random host port`]
    const fact = `${ip}${host}→${p.container}/${proto}`
    // A busy fixed host port is moved to a free one on `up` (remapPorts).
    return [p.host && p.host !== host ? `${fact} (remapped from ${p.host})` : fact]
  })
}

/** "3-stage build: deps → builder → runtime", or "from node:20-alpine" for a single-stage build. */
function stagesFact(node: ComposeNode, nodes: ComposeNode[], svc: ServiceFacts) {
  const stages = nodes
    .filter((n) => n.kind === "build-stage" && n.parent === node.id && n.stage)
    .sort((a, b) => a.stage!.index - b.stage!.index)
    .map((n) => n.stage!)
  if (stages.length > 1) return `${stages.length}-stage build: ${stages.map((s) => s.name || s.from).join(" → ")}`
  if (stages.length === 1) return `from ${stages[0].from}`
  if (!svc.build) return ""
  const bases = svc.baseImages
  return bases.length > 1 ? `${bases.length}-stage build` : bases.length === 1 ? `from ${bases[0]}` : ""
}

/** "vol db-data → /var/lib/postgresql/data", "bind ./src → /app (ro)", "tmpfs /tmp". */
function mountFact(m: ComposeMount, byId: Map<string, ComposeNode>) {
  const ro = m.readOnly ? " (ro)" : ""
  const type = m.type ?? (m.source && /^(\.|\/|~)/.test(m.source) ? "bind" : "volume")
  if (type === "tmpfs") return `tmpfs ${m.target}`
  if (type === "bind") return `bind ${m.source ?? ""} → ${m.target}${ro}`
  if (!m.source) return `anonymous vol → ${m.target}${ro}`
  const vol = byId.get(`volume:${m.source}`)
  const external = !!vol && (vol.external || vol.kind === "external")
  return `vol ${m.source}${external ? " (external)" : ""} → ${m.target}${ro}`
}

/** Icon and text of a service card: the technology the service runs (image, Dockerfile stages, stack). */
export function dockerCardScene(card: ArchCard, node: ComposeNode | undefined): { icon: string; text: CardText } {
  const svc = node?.service
  const icon = techIconFor({ image: svc?.image?.ref, baseImages: svc?.baseImages, stack: svc?.stack, kind: "service" })
  return {
    icon: icon.src,
    text: { title: card.name, lines: cardLines(card.details), tooltip: [card.name, card.details.join("\n"), card.nodeId].join("\n") },
  }
}

/** Label icon of a container: Docker on the host, a network icon on networks, none on the project. */
export function dockerGroupIconOf(g: ArchGroup): string | undefined {
  if (g.kind === "docker-host") return techIcon("docker").src
  if (g.kind === "docker-network") return techIcon("network").src
  return undefined
}

/** "Docker Compose · nimbo-dk-…" */
export function dockerSubtitle(wf: DockerWorkflow) {
  return ["Docker Compose", wf.compose?.project ?? ""].filter(Boolean).join(" · ")
}

export function dockerLegend(model: ArchModel): LegendEntry[] {
  const out: LegendEntry[] = [{ kind: "data", label: "Requests & dependencies" }]
  if (model.edges.some((e) => e.kind === "control")) out.push({ kind: "control", label: "Also attached to network" })
  if (model.groups.some((g) => g.scope === "internal")) out.push({ kind: "internal-network", label: "Internal network (no outside access)" })
  if (model.groups.some((g) => g.scope === "external")) out.push({ kind: "external-network", label: "External network" })
  return out
}

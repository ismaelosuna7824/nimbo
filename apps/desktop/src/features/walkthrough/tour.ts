// The guided tour of the Build canvas: the design as drawn, from the roots (what nothing depends on) to the
// leaves, one layer of dependencies at a time — a resource group, then what is built on it, then what is
// built on those… — with a caption saying what each resource is, why it is here and what uses it next.
// Pure: tour-graph.ts turns each kind of canvas into a TourGraph.
import type { Chip, Where } from "./describe"

/** What a resource is for, which decides why the caption says it is here. */
export type TourKind =
  | "account"
  | "network"
  | "subnet"
  | "dns"
  | "security"
  | "identity"
  | "secrets"
  | "monitoring"
  | "platform"
  | "database"
  | "cache"
  | "storage"
  | "messaging"
  | "compute"
  | "front"
  | "service"
  | "config"
  | "volume"
  | "namespace"
  | "other"

export interface TourNode {
  id: string
  /** "main", "api", "db-data". */
  name: string
  /** "Resource Group", "PostgreSQL container", "Deployment". */
  title: string
  kind: TourKind
  /** The catalog's one-line description, when it has one. */
  about?: string
  where?: Where
  /** Random names, passwords, lookups, associations and access policies: shown briefly with the step they serve. */
  plumbing?: boolean
  /** A container drawn around others (a VPC, a network, a namespace). */
  frame?: boolean
  /** Top-left on the canvas: the tour reads each layer top to bottom. */
  x: number
  y: number
}

/** `to` depends on `from` (is built on it); `edges` are the canvas edges between them. */
export interface TourDep {
  from: string
  to: string
  edges: string[]
}

export interface TourGraph {
  nodes: TourNode[]
  deps: TourDep[]
}

export interface TourStep {
  /** Siblings: nodes built on the same resource, read top to bottom. */
  nodes: string[]
  /** Dependency layer: 0 for the roots. */
  layer: number
  /** What they are built on (shown in the previous steps), for the caption. */
  parents: string[]
  /** Plumbing created with them ("also created here"). */
  also: string[]
  /** The node of `nodes` each plumbing item serves. */
  hostOf: Record<string, string>
  /** Canvas edges into each node and plumbing item, from what it is built on (they light up). */
  edgesInto: Record<string, string[]>
}

/** A sibling group never holds more cards than this; larger ones are split top to bottom. */
export const MAX_FOCUS = 12
/** Groups this small are toured card by card, each a step of its own; larger ones get an overview, then sub-steps. */
export const SMALL_GROUP = 3
/** Cards whose left edges are closer than this read as one column. */
const COLUMN = 160

/** As the canvas reads: column by column (cards a little off a column still belong to it), then top to bottom. */
function readingOrder(byId: Map<string, TourNode>) {
  const column = (id: string) => Math.round(byId.get(id)!.x / COLUMN)
  return (a: string, b: string) => column(a) - column(b) || byId.get(a)!.y - byId.get(b)!.y || (a < b ? -1 : 1)
}

/**
 * The tour's sibling groups. Layers are the longest dependency chains from the roots (as the automatic layout places
 * them, left to right). In a layer, siblings — nodes built on the same real resource, their deepest dependency —
 * form a group (at most MAX_FOCUS cards), groups read top to bottom. Plumbing is no step of its own: it goes with
 * the nearest real resource it serves. Cycles are cut where they close. See tourStops for the steps shown.
 */
export function buildTour(g: TourGraph): TourStep[] {
  const byId = new Map(g.nodes.map((n) => [n.id, n]))
  const depsOf = new Map<string, TourDep[]>()
  for (const d of g.deps) {
    if (d.from === d.to || !byId.has(d.from) || !byId.has(d.to)) continue
    depsOf.set(d.to, [...(depsOf.get(d.to) ?? []), d])
  }
  const level = new Map<string, number>()
  const visiting = new Set<string>()
  const levelOf = (id: string): number => {
    const known = level.get(id)
    if (known !== undefined) return known
    if (visiting.has(id)) return 0
    visiting.add(id)
    const l = Math.max(-1, ...(depsOf.get(id) ?? []).map((d) => levelOf(d.from))) + 1
    visiting.delete(id)
    level.set(id, l)
    return l
  }
  for (const n of g.nodes) levelOf(n.id)

  const topDown = readingOrder(byId)
  /** The dependency a node is built on most directly: the deepest one, a real resource before plumbing, the topmost. */
  const primaryOf = (id: string): string | undefined =>
    (depsOf.get(id) ?? [])
      .map((d) => d.from)
      .filter((f) => (level.get(f) ?? 0) < (level.get(id) ?? 0))
      .sort((a, b) => (level.get(b) ?? 0) - (level.get(a) ?? 0) || Number(!!byId.get(a)!.plumbing) - Number(!!byId.get(b)!.plumbing) || topDown(a, b))[0]

  const real = g.nodes.filter((n) => !n.plumbing)
  // Everything plumbing: tour it all the same.
  const shown = real.length ? real : g.nodes
  const steps: TourStep[] = []
  const stepOf = new Map<string, number>()
  const layers = [...new Set(shown.map((n) => level.get(n.id)!))].sort((a, b) => a - b)
  for (const layer of layers) {
    const members = shown.filter((n) => level.get(n.id) === layer).map((n) => n.id)
    // Siblings: built on the same real resource (through plumbing, such as a secret built on a queue's access rule);
    // the roots together.
    const groups = new Map<string, string[]>()
    for (const id of members) {
      let p = primaryOf(id)
      while (p && byId.get(p)!.plumbing) p = primaryOf(p)
      groups.set(p ?? "", [...(groups.get(p ?? "") ?? []), id])
    }
    const ordered = [...groups].map(([p, ids]) => [p, ids.sort(topDown)] as const).sort((a, b) => topDown(a[1][0], b[1][0]))
    for (const [parent, ids] of ordered) {
      for (let i = 0; i < ids.length; i += MAX_FOCUS) {
        const chunk = ids.slice(i, i + MAX_FOCUS)
        for (const id of chunk) stepOf.set(id, steps.length)
        steps.push({ nodes: chunk, layer, parents: parent ? [parent] : [], also: [], hostOf: {}, edgesInto: Object.fromEntries(chunk.map((id) => [id, (depsOf.get(id) ?? []).flatMap((d) => d.edges)])) })
      }
    }
  }
  // Plumbing joins the nearest real resource it is built on (roots: the first card).
  if (real.length) {
    for (const n of [...g.nodes].filter((x) => x.plumbing).sort((a, b) => level.get(a.id)! - level.get(b.id)! || topDown(a.id, b.id))) {
      let p = primaryOf(n.id)
      while (p && (!stepOf.has(p) || byId.get(p)!.plumbing)) p = primaryOf(p)
      const index = p !== undefined ? stepOf.get(p)! : 0
      const s = steps[index]
      s.also.push(n.id)
      s.hostOf[n.id] = p ?? s.nodes[0]
      s.edgesInto[n.id] = (depsOf.get(n.id) ?? []).flatMap((d) => d.edges)
    }
  }
  return steps
}

/** What the tour shows at once: one card, or a sibling group's overview before its cards one by one. */
export interface TourStop {
  /** "2", or "2b" for the second card of step 2's group. */
  label: string
  /** Step number (1-based); the step count is the tour's length. */
  step: number
  /** Cards in focus (they glow). */
  nodes: string[]
  /** The sibling group they belong to (the focus itself outside groups). */
  group: string[]
  parents: string[]
  also: string[]
  edges: string[]
}

/**
 * The stops of a tour: groups of up to SMALL_GROUP cards card by card, each a step; larger groups as a step with an
 * overview of the whole group, then sub-steps (2a, 2b…) for its cards, so the camera can show readable cards.
 */
export function tourStops(steps: TourStep[]): TourStop[] {
  const out: TourStop[] = []
  let n = 0
  const card = (s: TourStep, id: string, label: string, group: string[]): TourStop => {
    const also = s.also.filter((p) => s.hostOf[p] === id)
    return { label, step: n, nodes: [id], group, parents: s.parents, also, edges: [id, ...also].flatMap((x) => s.edgesInto[x] ?? []) }
  }
  for (const s of steps) {
    if (s.nodes.length <= SMALL_GROUP) {
      for (const id of s.nodes) {
        n++
        out.push(card(s, id, `${n}`, [id]))
      }
      continue
    }
    n++
    out.push({ label: `${n}`, step: n, nodes: s.nodes, group: s.nodes, parents: s.parents, also: s.also, edges: Object.values(s.edgesInto).flat() })
    s.nodes.forEach((id, i) => out.push(card(s, id, `${n}${String.fromCharCode(97 + i)}`, s.nodes)))
  }
  return out
}

/** "a, b and c", "a, b, c and 4 more". */
export function listOf(names: string[], max = 3): string {
  if (names.length === 0) return ""
  if (names.length === 1) return names[0]
  if (names.length <= max) return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`
  return `${names.slice(0, max).join(", ")} and ${names.length - max} more`
}

export interface TourLine {
  id: string
  label: string
  /** Why it is in this design. */
  why: string
}

export interface TourCaption {
  /** "Resource Group main", or "Built on Virtual Network main" for a group's overview. */
  title: string
  /** A sub-step's place in its group: "2 of 8 built on Resource Group main". */
  context?: string
  /** One line per resource in focus (the panel shows a few, the rest on demand). */
  lines: TourLine[]
  /** The catalog description of a single resource. */
  about?: string
  /** "→ used by Subnet apps and Subnet db"; "The end of the tour…" on the last stop. */
  next: string
  where?: Where
  also: Chip[]
}

/** The caption of `stops[index]`. */
export function tourCaption(g: TourGraph, stops: TourStop[], index: number): TourCaption {
  const byId = new Map(g.nodes.map((n) => [n.id, n]))
  const label = (id: string) => {
    const n = byId.get(id)!
    return n.name && n.name !== n.title ? `${n.title} ${n.name}` : n.title
  }
  const dependents = new Map<string, string[]>()
  const dependencies = new Map<string, string[]>()
  for (const d of g.deps) {
    if (d.from === d.to || !byId.has(d.from) || !byId.has(d.to)) continue
    if (!dependents.get(d.from)?.includes(d.to)) dependents.set(d.from, [...(dependents.get(d.from) ?? []), d.to])
    if (!dependencies.get(d.to)?.includes(d.from)) dependencies.set(d.to, [...(dependencies.get(d.to) ?? []), d.from])
  }
  const realUsers = (id: string) => (dependents.get(id) ?? []).filter((u) => !byId.get(u)!.plumbing).sort(readingOrder(byId))
  const reach = (id: string) => {
    const seen = new Set<string>()
    const stack = [id]
    while (stack.length) {
      for (const u of dependents.get(stack.pop()!) ?? []) {
        if (seen.has(u)) continue
        seen.add(u)
        stack.push(u)
      }
    }
    return seen
  }
  const real = g.nodes.filter((n) => !n.plumbing)
  const step = stops[index]

  const whyOf = (id: string): string => {
    const n = byId.get(id)!
    // Through plumbing when nothing real is built on it directly: an NSG serves the subnet its association names.
    const direct = realUsers(id)
    const linked = direct.length
      ? direct
      : [...new Set((dependents.get(id) ?? []).filter((p) => byId.get(p)!.plumbing).flatMap((p) => [...realUsers(p), ...(dependencies.get(p) ?? []).filter((d) => d !== id && !byId.get(d)!.plumbing && !byId.get(d)!.frame)]))].sort(readingOrder(byId))
    const users = linked.map(label)
    const them = listOf(users)
    // "Subnet apps lives in", "Subnet apps and Subnet db live in".
    const verb = (one: string, many: string) => (users.length === 1 ? one : many)
    // What it sends to or stands in front of; the frame it is drawn in is not one.
    const uses = listOf((dependencies.get(id) ?? []).filter((d) => !byId.get(d)!.plumbing && !byId.get(d)!.frame && byId.get(d)!.kind !== "account").map(label))
    switch (n.kind) {
      case "account": {
        const held = [...reach(id)].filter((u) => !byId.get(u)!.plumbing).length
        return held >= real.length - 1 ? `Holds every resource of this design (${held}).` : `Holds ${held} resources of this design.`
      }
      case "network":
        return them ? `The private network ${them} ${verb("lives", "live")} in.` : "The private network of this design."
      case "subnet":
        return them ? `A slice of the network set apart for ${them}.` : "A slice of the network."
      case "dns":
        return them ? `Lets ${them} be found by name inside the network.` : "Names resources inside the network."
      case "security":
        return them ? `Firewall rules: decides which traffic may reach ${them}.` : "Firewall rules for the network."
      case "identity":
        return them ? `The identity ${them} ${verb("uses", "use")} to reach other services without passwords.` : "An identity services use instead of passwords."
      case "secrets":
        return them ? `Keeps the passwords and connection strings ${them} ${verb("reads", "read")}.` : "Keeps passwords and keys out of the code."
      case "monitoring":
        return them ? `Collects the logs and metrics of ${them}.` : "Collects logs and metrics."
      case "platform":
        return them ? `The platform ${them} ${verb("runs", "run")} on.` : "The platform the apps run on."
      case "database":
        return them ? `Stores the data ${them} ${verb("reads and writes", "read and write")}.` : "Stores the application's data."
      case "cache":
        return them ? `Keeps hot data in memory for ${them}.` : "Keeps hot data in memory."
      case "storage":
        return them ? `Stores files and objects for ${them}.` : "Stores files and objects."
      case "messaging":
        return them ? `Carries messages between the services; ${them} ${verb("is", "are")} built on it.` : "Carries messages between the services."
      case "compute":
        return them ? `Runs your code; ${them} ${verb("is", "are")} built on it.` : uses ? `Runs your code, using ${uses}.` : "Runs your code: this is where requests are handled."
      case "front":
        return uses ? `The way in from outside: it sends requests on to ${uses}.` : "The way in from outside."
      case "service":
        return uses ? `A stable address in front of ${uses}.` : "A stable address in front of the pods."
      case "config":
        return them ? `Settings ${them} ${verb("reads", "read")} when ${verb("it starts", "they start")}.` : "Settings the apps read when they start."
      case "volume":
        return them ? `A disk that keeps the data of ${them} when containers are replaced.` : "A disk that outlives containers."
      case "namespace":
        return them ? `Groups ${them} so they can be managed together.` : "Groups the objects of this app."
      default:
        return them ? `Created first so ${them} can use it.` : uses ? `Built on ${uses}.` : "Part of this design."
    }
  }

  const single = step.nodes.length === 1
  const parent = step.parents.length ? listOf(step.parents.map(label), 2) : ""
  const title = single ? label(step.nodes[0]) : parent ? `Built on ${parent}` : `${step.nodes.length} starting points`
  const lines = step.nodes.map((id) => ({ id, label: label(id), why: whyOf(id) }))
  const position = single && step.group.length > 1 ? step.group.indexOf(step.nodes[0]) + 1 : 0
  const context = position ? `${position} of ${step.group.length} ${parent ? `built on ${parent}` : "starting points"}` : undefined
  const after = [...new Set(step.nodes.flatMap(realUsers))].filter((u) => !step.nodes.includes(u))
  const next = after.length
    ? `→ used by ${listOf(after.map(label), 4)}`
    : index === stops.length - 1
      ? `The end of the tour: nothing else is built on ${single ? "it" : "these"}.`
      : `Nothing else is built on ${single ? "it" : "these"}.`
  const wheres = step.nodes.map((id) => byId.get(id)!.where).filter((w): w is Where => !!w)
  const sameWhere = wheres.length === step.nodes.length && wheres.every((w) => w.tone === wheres[0].tone) ? wheres[0] : undefined
  return {
    title,
    ...(context ? { context } : {}),
    lines,
    ...(single && byId.get(step.nodes[0])!.about ? { about: byId.get(step.nodes[0])!.about } : {}),
    next,
    ...(sameWhere ? { where: single ? sameWhere : { ...sameWhere, text: `All ${step.nodes.length}: ${sameWhere.text.charAt(0).toLowerCase()}${sameWhere.text.slice(1)}` } } : {}),
    also: step.also.map((id) => ({ label: label(id), nodeId: id })),
  }
}

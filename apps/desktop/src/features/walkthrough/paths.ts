// The paths a request takes through a design, derived from the Architecture view's data-flow model
// (features/architecture): it starts at an entry point (Users or the Internet reaching a front door),
// follows the solid data-flow arrows in their direction and ends where nothing goes on (a database,
// a bucket, a consumer). Queues, topics and event buses are asynchronous: the request returns there
// and the message goes on to the services that consume it later. Pure and deterministic.
import type { ArchEdge, ArchModel } from "@/features/architecture/model"

/** Choice at a fork that plays every way out, one after another. */
export const SHOW_ALL = "*"
/** A walk never grows beyond this many steps (large designs with many forks shown at once). */
export const MAX_STEPS = 40

/** What the walk needs to know about an item of the model (see describe.ts). */
export interface HopTraits {
  /** A queue, topic or event bus: the request returns here, the message continues later. */
  async: boolean
  /** Identity, keys, DNS, telemetry…: never a hop, shown as "also involved" on the steps it helps. */
  supporting: boolean
}

export type TraitsOf = (id: string) => HopTraits

/** Where a walk starts: an actor reaching a front door, or (without actors) an item the flow starts at. */
export interface Entry {
  /** The front door's item ID (unique among the entries of a model). */
  target: string
  /** Users or Internet; absent for a flow start picked when the design has no entry point. */
  actor?: string
}

export interface Step {
  /** Item of the model the request is at: a card, a group (a packed kind, a subnet) or an actor. */
  at: string
  /** Where it came from (absent on the first step). */
  from?: string
  /** The model edge travelled; drawn from `at` to `from` when `reversed` (a consumer pulling from its queue). */
  edge?: ArchEdge
  reversed?: boolean
  /** `at` is asynchronous (a queue, topic or event bus). */
  async?: boolean
  /** An asynchronous hop came before: this part runs later, on its own. */
  afterAsync?: boolean
  /** The ways out when the flow splits here (item IDs, in the model's order). */
  branches?: string[]
  /** The first step of another way out of an earlier fork (shown after the previous way ended). */
  resumes?: boolean
  /** Shown already on an earlier way: the walk does not follow it again. */
  joins?: boolean
  /** Nothing goes on from here. */
  end?: boolean
}

export interface Walk {
  entry: Entry
  steps: Step[]
  /** The walk stops at a fork whose way is not chosen yet: the step index and the ways out. */
  pending?: { step: number; options: string[] }
  /** Cut at MAX_STEPS. */
  truncated?: boolean
}

const isData = (e: ArchEdge) => e.kind === "data"

/**
 * Where requests come in: every data-flow arrow from the Users actor (from the Internet when no Users
 * reach the design), one entry per front door, in the model's order.
 */
export function walkEntries(model: ArchModel): Entry[] {
  const actors = new Map(model.actors.map((a) => [a.id, a]))
  const from = (kind: "users" | "internet") => {
    const seen = new Set<string>()
    return model.edges.flatMap((e) => {
      if (!isData(e) || actors.get(e.source)?.kind !== kind || seen.has(e.target) || actors.has(e.target)) return []
      seen.add(e.target)
      return [{ target: e.target, actor: e.source }]
    })
  }
  const users = from("users")
  return users.length ? users : from("internet")
}

/**
 * Items a flow starts at when no actor reaches the design (a topic fanning out, a queue feeding a
 * worker): they send data on, and nothing but supporting pieces sends data to them.
 */
export function flowStarts(model: ArchModel, traits: TraitsOf): Entry[] {
  const actors = new Set(model.actors.map((a) => a.id))
  const fed = new Set(model.edges.filter((e) => isData(e) && !actors.has(e.source) && !traits(e.source).supporting).map((e) => e.target))
  const starts = new Set<string>()
  for (const e of model.edges) {
    if (!isData(e) || actors.has(e.source) || fed.has(e.source) || traits(e.source).supporting || traits(e.target).supporting) continue
    starts.add(e.source)
  }
  return [...starts].map((target) => ({ target }))
}

interface Hop {
  to: string
  edge: ArchEdge
  reversed: boolean
}

/**
 * The walk from `entry`: depth first along the data flow, never back onto the path it came by (cycles),
 * never into supporting pieces. At a fork it takes `choices[fork]` (a next item, or SHOW_ALL for every
 * way in turn) and stops with `pending` when there is no choice yet. With every way shown, an item an
 * earlier way reached already is a last step ("joins").
 */
export function buildWalk(model: ArchModel, entry: Entry, choices: Record<string, string>, traits: TraitsOf): Walk {
  const actors = new Set(model.actors.map((a) => a.id))
  const out = new Map<string, ArchEdge[]>()
  const into = new Map<string, ArchEdge[]>()
  for (const e of model.edges) {
    if (!isData(e)) continue
    out.set(e.source, [...(out.get(e.source) ?? []), e])
    into.set(e.target, [...(into.get(e.target) ?? []), e])
  }
  // Cards of a packed kind group ("ECS services (6)") share the group's arrows.
  const groups = new Map(model.groups.map((g) => [g.id, g]))
  const packedParent = new Map(model.cards.flatMap((c) => (c.parent && groups.get(c.parent)?.packed ? [[c.id, c.parent] as const] : [])))

  const nextHops = (at: string, path: Set<string>): Hop[] => {
    const hops: Hop[] = []
    const add = (to: string, edge: ArchEdge, reversed: boolean) => {
      if (path.has(to) || traits(to).supporting || hops.some((h) => h.to === to)) return
      hops.push({ to, edge, reversed })
    }
    const parent = packedParent.get(at)
    for (const e of [...(out.get(at) ?? []), ...(parent ? (out.get(parent) ?? []) : [])]) add(e.target, e, false)
    // Consumers pull from a queue: their arrow points at it, the message goes to them.
    if (traits(at).async) for (const e of into.get(at) ?? []) if (!actors.has(e.source)) add(e.source, e, true)
    return hops
  }

  const steps: Step[] = []
  const shown = new Set<string>()
  let pending: Walk["pending"]
  let truncated = false

  /** Adds `step` and what follows it; false stops the whole walk (a fork to choose, the size limit). */
  const visit = (step: Step, path: string[]): boolean => {
    steps.push(step)
    if (shown.has(step.at)) {
      step.joins = true
      return true
    }
    shown.add(step.at)
    if (steps.length >= MAX_STEPS) {
      truncated = true
      step.end = true
      return false
    }
    const onPath = [...path, step.at]
    const hops = nextHops(step.at, new Set(onPath))
    if (hops.length === 0) {
      step.end = true
      return true
    }
    const afterAsync = !!(step.afterAsync || step.async)
    const next = (h: Hop, resumes: boolean): Step => ({
      at: h.to,
      from: step.at,
      edge: h.edge,
      ...(h.reversed ? { reversed: true } : {}),
      ...(traits(h.to).async ? { async: true } : {}),
      ...(afterAsync ? { afterAsync: true } : {}),
      ...(resumes ? { resumes: true } : {}),
    })
    if (hops.length === 1) return visit(next(hops[0], false), onPath)
    step.branches = hops.map((h) => h.to)
    const choice = choices[step.at]
    if (choice === SHOW_ALL) {
      for (let i = 0; i < hops.length; i++) if (!visit(next(hops[i], i > 0), onPath)) return false
      return true
    }
    const picked = hops.find((h) => h.to === choice)
    if (!picked) {
      pending = { step: steps.length - 1, options: step.branches }
      return false
    }
    return visit(next(picked, false), onPath)
  }

  if (entry.actor) {
    steps.push({ at: entry.actor })
    const edge = model.edges.find((e) => isData(e) && e.source === entry.actor && e.target === entry.target)
    visit({ at: entry.target, from: entry.actor, edge, ...(traits(entry.target).async ? { async: true } : {}) }, [entry.actor])
  } else {
    visit({ at: entry.target, ...(traits(entry.target).async ? { async: true } : {}) }, [])
  }
  return { entry, steps, ...(pending ? { pending } : {}), ...(truncated ? { truncated } : {}) }
}

/** What the end of a walk tells: components passed (actors aside) and how many of them ran later, after a queue. */
export function walkSummary(model: ArchModel, walk: Walk): { components: number; async: number } {
  const actors = new Set(model.actors.map((a) => a.id))
  const items = walk.steps.filter((s) => !actors.has(s.at))
  return {
    components: new Set(items.map((s) => s.at)).size,
    async: new Set(items.filter((s) => s.afterAsync).map((s) => s.at)).size,
  }
}

import { describe, expect, test } from "bun:test"
import type { ArchActor, ArchCard, ArchEdge, ArchGroup, ArchModel } from "@/features/architecture/model"
import { buildWalk, flowStarts, leadsSomewhere, MAX_STEPS, SHOW_ALL, walkEntries, walkSummary, type HopTraits } from "./paths"

const card = (id: string, parent?: string): ArchCard => ({ id, nodeId: id, type: "t", kind: "resource", category: "compute", name: id, details: [], ...(parent ? { parent } : {}) })
const data = (source: string, target: string): ArchEdge => ({ id: `flow:${source}->${target}`, source, target, kind: "data" })
const control = (source: string, target: string): ArchEdge => ({ id: `flow:${source}->${target}`, source, target, kind: "control" })
const users: ArchActor = { id: "actor:users", kind: "users", label: "Users" }
const internet: ArchActor = { id: "actor:internet", kind: "internet", label: "Internet" }
const model = (cards: string[], edges: ArchEdge[], actors: ArchActor[] = [users], groups: ArchGroup[] = []): ArchModel => ({
  groups,
  cards: cards.map((id) => card(id)),
  actors,
  edges,
  providers: [],
  regions: [],
})
const traitsFrom =
  (async: string[] = [], supporting: string[] = [], plumbing: string[] = []) =>
  (id: string): HopTraits => ({ async: async.includes(id), supporting: supporting.includes(id), plumbing: plumbing.includes(id) })
const at = (steps: { at: string }[]) => steps.map((s) => s.at)

describe("walkEntries", () => {
  test("one entry per front door the Users reach", () => {
    const m = model(["api", "web", "db"], [data("actor:users", "api"), data("actor:users", "web"), data("api", "db")])
    expect(walkEntries(m)).toEqual([
      { target: "api", actor: "actor:users" },
      { target: "web", actor: "actor:users" },
    ])
  })

  test("the Internet only when no Users reach the design", () => {
    const m = model(["igw", "nat"], [data("actor:internet", "igw"), data("nat", "actor:internet")], [internet])
    expect(walkEntries(m)).toEqual([{ target: "igw", actor: "actor:internet" }])
    const both = model(["igw", "api"], [data("actor:internet", "igw"), data("actor:users", "api")], [users, internet])
    expect(walkEntries(both)).toEqual([{ target: "api", actor: "actor:users" }])
  })

  test("no entry point: none, and the flow starts are offered instead", () => {
    const m = model(["topic", "queue", "worker", "key"], [data("topic", "queue"), data("queue", "worker"), control("topic", "key")], [])
    expect(walkEntries(m)).toEqual([])
    expect(flowStarts(m, traitsFrom())).toEqual([{ target: "topic" }])
  })
})

describe("buildWalk", () => {
  test("follows the data flow to the end, past dashed lines and supporting pieces", () => {
    const m = model(["api", "db", "role", "dns"], [data("actor:users", "api"), data("api", "db"), control("api", "role"), data("db", "dns")])
    const walk = buildWalk(m, walkEntries(m)[0], {}, traitsFrom([], ["dns", "role"]))
    expect(at(walk.steps)).toEqual(["actor:users", "api", "db"])
    expect(walk.steps[1]).toMatchObject({ from: "actor:users", edge: { id: "flow:actor:users->api" } })
    expect(walk.steps[2]).toMatchObject({ from: "api", end: true })
    expect(walk.pending).toBeUndefined()
    expect(walkSummary(m, walk)).toEqual({ components: 2, async: 0, ends: ["db"] })
  })

  test("stops at a fork until a way is chosen", () => {
    const m = model(["api", "db", "cache"], [data("actor:users", "api"), data("api", "cache"), data("api", "db")])
    const entry = walkEntries(m)[0]
    const open = buildWalk(m, entry, {}, traitsFrom())
    expect(at(open.steps)).toEqual(["actor:users", "api"])
    expect(open.steps[1].branches).toEqual(["cache", "db"])
    expect(open.pending).toEqual({ step: 1, options: ["cache", "db"] })

    const chosen = buildWalk(m, entry, { api: "db" }, traitsFrom())
    expect(at(chosen.steps)).toEqual(["actor:users", "api", "db"])
    expect(chosen.pending).toBeUndefined()
  })

  test("show all plays every way in turn; an item reached before joins instead of repeating", () => {
    const m = model(["ingress", "frontend", "backend", "redis"], [
      data("actor:users", "ingress"),
      data("ingress", "backend"),
      data("ingress", "frontend"),
      data("backend", "redis"),
      data("frontend", "backend"),
    ])
    const walk = buildWalk(m, walkEntries(m)[0], { ingress: SHOW_ALL }, traitsFrom())
    expect(at(walk.steps)).toEqual(["actor:users", "ingress", "backend", "redis", "frontend", "backend"])
    expect(walk.steps[4]).toMatchObject({ from: "ingress", resumes: true })
    expect(walk.steps[5]).toMatchObject({ from: "frontend", joins: true })
    expect(walkSummary(m, walk).components).toBe(4)
  })

  test("a queue is asynchronous and goes on to the consumers pulling from it", () => {
    // The notifier's arrow points at the queue it reads; the message goes the other way.
    const m = model(["api", "queue", "notifier", "db"], [data("actor:users", "api"), data("api", "queue"), data("notifier", "queue"), data("notifier", "db")])
    const walk = buildWalk(m, walkEntries(m)[0], {}, traitsFrom(["queue"]))
    expect(at(walk.steps)).toEqual(["actor:users", "api", "queue", "notifier", "db"])
    expect(walk.steps[2]).toMatchObject({ async: true })
    expect(walk.steps[3]).toMatchObject({ from: "queue", reversed: true, afterAsync: true, edge: { id: "flow:notifier->queue" } })
    expect(walk.steps[4]).toMatchObject({ afterAsync: true, end: true })
    expect(walkSummary(m, walk)).toEqual({ components: 4, async: 2, ends: ["db"] })
  })

  test("network plumbing is never a step: the next component says the request passed through it", () => {
    // Internet → internet gateway → public subnet → load balancer → nodes; the NAT on the side leads nowhere.
    const m = model(["igw", "public", "lb", "nodes", "nat"], [data("actor:internet", "igw"), data("igw", "public"), data("public", "lb"), data("lb", "nodes"), data("igw", "nat")], [internet])
    const traits = traitsFrom([], [], ["igw", "public", "nat"])
    const walk = buildWalk(m, walkEntries(m)[0], {}, traits)
    expect(at(walk.steps)).toEqual(["actor:internet", "lb", "nodes"])
    expect(walk.steps[1]).toMatchObject({ from: "actor:internet", through: ["igw", "public"] })
    expect(walk.steps[2].through).toBeUndefined()
    expect(walkSummary(m, walk)).toEqual({ components: 2, async: 0, ends: ["nodes"] })
  })

  test("an entry that only reaches plumbing leads nowhere", () => {
    const m = model(["igw", "public"], [data("actor:internet", "igw"), data("igw", "public")], [internet])
    const traits = traitsFrom([], [], ["igw", "public"])
    expect(leadsSomewhere(m, walkEntries(m)[0], traits)).toBe(false)
    expect(at(buildWalk(m, walkEntries(m)[0], {}, traits).steps)).toEqual(["actor:internet"])
    const served = model(["igw", "web"], [data("actor:internet", "igw"), data("igw", "web")], [internet])
    expect(leadsSomewhere(served, walkEntries(served)[0], traits)).toBe(true)
  })

  test("never goes back onto its own path (cycles)", () => {
    const m = model(["a", "b", "c"], [data("actor:users", "a"), data("a", "b"), data("b", "a"), data("b", "c"), data("c", "a")])
    const walk = buildWalk(m, walkEntries(m)[0], {}, traitsFrom())
    expect(at(walk.steps)).toEqual(["actor:users", "a", "b", "c"])
    expect(walk.steps[3].end).toBe(true)
  })

  test("starts at a flow start without an actor", () => {
    const m = model(["topic", "queue"], [data("topic", "queue")], [])
    const walk = buildWalk(m, { target: "topic" }, {}, traitsFrom(["topic", "queue"]))
    expect(at(walk.steps)).toEqual(["topic", "queue"])
    expect(walk.steps[0]).toMatchObject({ async: true })
    expect(walk.steps[1]).toMatchObject({ afterAsync: true, end: true })
  })

  test("cards of a packed kind group share the group's arrows", () => {
    const g: ArchGroup = { id: "group:kind:x", kind: "logical", label: "ECS services (3)", packed: true }
    const m: ArchModel = { ...model([], [data("actor:users", "lb"), data("lb", "s1"), data("group:kind:x", "queue")], [users], [g]), cards: [card("lb"), card("s1", g.id), card("s2", g.id), card("s3", g.id), card("queue")] }
    const walk = buildWalk(m, walkEntries(m)[0], {}, traitsFrom())
    expect(at(walk.steps)).toEqual(["actor:users", "lb", "s1", "queue"])
  })

  test("long walks are cut at MAX_STEPS", () => {
    const ids = Array.from({ length: MAX_STEPS + 10 }, (_, i) => `s${i}`)
    const m = model(ids, [data("actor:users", "s0"), ...ids.slice(1).map((id, i) => data(ids[i], id))])
    const walk = buildWalk(m, walkEntries(m)[0], {}, traitsFrom())
    expect(walk.steps).toHaveLength(MAX_STEPS)
    expect(walk.truncated).toBe(true)
  })
})

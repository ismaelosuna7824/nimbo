import { describe, expect, test } from "bun:test"
import type { ComposeEdge, ComposeGraph, ComposeNode, ComposeStatus, ServiceFacts } from "@/lib/api/types"
import {
  buildCanvas,
  DEFAULT_NETWORK,
  FRAME_HEADER,
  FRAME_PAD,
  placeNew,
  relativePosition,
  SERVICE_H,
  SERVICE_W,
  STAGES_H,
  type CanvasItem,
} from "./model"

const facts = (over: Partial<ServiceFacts> = {}): ServiceFacts => ({
  baseImages: [],
  ports: [],
  expose: [],
  environment: [],
  volumes: [],
  networks: [],
  dependsOn: [],
  ...over,
})

const service = (name: string, over: Partial<ServiceFacts> = {}, node: Partial<ComposeNode> = {}): ComposeNode => ({
  id: `service:${name}`,
  kind: "service",
  name,
  service: facts(over),
  ...node,
})

const network = (name: string, node: Partial<ComposeNode> = {}): ComposeNode => ({ id: `network:${name}`, kind: "network", name, ...node })
const volume = (name: string, node: Partial<ComposeNode> = {}): ComposeNode => ({ id: `volume:${name}`, kind: "volume", name, ...node })

/** A graph edge the way the engine stores it: the source is used by the target. */
const edge = (kind: ComposeEdge["kind"], source: string, target: string, over: Partial<ComposeEdge> = {}): ComposeEdge => ({
  id: `${kind}:${source}->${target}`,
  kind,
  source,
  target,
  ...over,
})

const byId = (items: CanvasItem[]) => new Map(items.map((i) => [i.id, i]))

type Rect = { x: number; y: number; w: number; h: number }
const intersects = (a: Rect, b: Rect) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h

describe("buildCanvas: network frames", () => {
  test("services without networks sit in the implicit default network frame", () => {
    const c = buildCanvas({ nodes: [service("api"), service("db")], edges: [] })
    const items = byId(c.items)
    expect(items.get(DEFAULT_NETWORK)?.kind).toBe("frame")
    expect(items.get("service:api")?.parent).toBe(DEFAULT_NETWORK)
    expect(items.get("service:db")?.parent).toBe(DEFAULT_NETWORK)
  })

  test("no default frame when every service declares its networks", () => {
    const c = buildCanvas({ nodes: [network("back"), service("api", { networks: ["back"] })], edges: [] })
    expect(c.items.some((i) => i.id === DEFAULT_NETWORK)).toBe(false)
    expect(byId(c.items).get("service:api")?.parent).toBe("network:back")
  })

  test("a service in several networks is drawn in its first one; the others are edges", () => {
    const nodes = [network("front"), network("back"), service("api", { networks: ["front", "back"] })]
    const edges = [edge("network", "network:front", "service:api"), edge("network", "network:back", "service:api")]
    const c = buildCanvas({ nodes, edges })
    expect(byId(c.items).get("service:api")?.parent).toBe("network:front")
    // The frame it sits in is the membership; only the second network gets an edge.
    expect(c.edges.map((e) => [e.source, e.target])).toEqual([["service:api", "network:back"]])
  })

  test("an external network is a frame flagged external", () => {
    const ext: ComposeNode = { id: "network:shared", kind: "external", externalKind: "network", name: "shared" }
    const c = buildCanvas({ nodes: [ext, service("api", { networks: ["shared"] })], edges: [] })
    const frame = byId(c.items).get("network:shared")
    expect(frame).toMatchObject({ kind: "frame", external: true })
    expect(byId(c.items).get("service:api")?.parent).toBe("network:shared")
  })

  test("frames wrap their members with header and padding", () => {
    const nodes = [
      service("api", {}, { position: { x: 100, y: 200 } }),
      service("db", {}, { position: { x: 500, y: 260 } }),
    ]
    const frame = byId(buildCanvas({ nodes, edges: [] }).items).get(DEFAULT_NETWORK)!
    expect(frame).toMatchObject({
      x: 100 - FRAME_PAD,
      y: 200 - FRAME_HEADER,
      w: 500 + SERVICE_W - 100 + 2 * FRAME_PAD,
      h: 260 + SERVICE_H - 200 + FRAME_HEADER + FRAME_PAD,
    })
  })

  test("build stages are folded into their service card, in Dockerfile order", () => {
    const stage = (name: string, index: number): ComposeNode => ({
      id: `stage:api:${name}`,
      kind: "build-stage",
      name,
      parent: "service:api",
      stage: { index, name, from: "node", dockerfile: "Dockerfile", line: index + 1 },
    })
    const c = buildCanvas({ nodes: [service("api"), stage("run", 1), stage("deps", 0)], edges: [edge("copy_from", "stage:api:deps", "stage:api:run")] })
    const api = byId(c.items).get("service:api")!
    expect(api.stages?.map((s) => s.name)).toEqual(["deps", "run"])
    expect(api.h).toBe(SERVICE_H + STAGES_H)
    expect(c.items.some((i) => i.id.startsWith("stage:"))).toBe(false)
    expect(c.edges).toEqual([])
  })
})

describe("buildCanvas: edges", () => {
  test("depends_on points from the user to what it uses, labelled with the condition", () => {
    const g: ComposeGraph = {
      nodes: [service("api"), service("db")],
      edges: [edge("depends_on", "service:db", "service:api", { condition: "service_healthy" })],
    }
    expect(buildCanvas(g).edges).toEqual([
      expect.objectContaining({ source: "service:api", target: "service:db", kind: "depends_on", label: "healthy" }),
    ])
  })

  test("a depends_on without a condition reads 'started'", () => {
    const g: ComposeGraph = { nodes: [service("api"), service("db")], edges: [edge("depends_on", "service:db", "service:api")] }
    expect(buildCanvas(g).edges[0].label).toBe("started")
  })

  test("inferred connections point at the referenced service and show the env key", () => {
    const g: ComposeGraph = {
      nodes: [service("api"), service("db")],
      edges: [edge("inferred", "service:db", "service:api", { envKey: "DATABASE_URL" })],
    }
    expect(buildCanvas(g).edges[0]).toMatchObject({ source: "service:api", target: "service:db", kind: "inferred", label: "DATABASE_URL" })
  })

  test("mounts show the target path and read-only", () => {
    const g: ComposeGraph = {
      nodes: [service("db"), volume("data")],
      edges: [edge("volume", "volume:data", "service:db", { path: "/var/lib/postgresql/data", readOnly: true })],
    }
    expect(buildCanvas(g).edges[0]).toMatchObject({ source: "service:db", target: "volume:data", label: "/var/lib/postgresql/data (ro)" })
  })

  test("edges to nodes that are not drawn are dropped", () => {
    const g: ComposeGraph = { nodes: [service("api")], edges: [edge("depends_on", "service:gone", "service:api")] }
    expect(buildCanvas(g).edges).toEqual([])
  })

  test("an edge is live only when both services run", () => {
    const g: ComposeGraph = {
      nodes: [service("api"), service("db"), service("cache")],
      edges: [edge("depends_on", "service:db", "service:api"), edge("depends_on", "service:cache", "service:api")],
    }
    const status: ComposeStatus = {
      workflowId: "w1",
      project: "nimbo-dk-w1",
      state: "running",
      remaps: [],
      updatedAt: "2026-10-22T10:00:00Z",
      services: [
        { name: "api", nodeId: "service:api", state: "running", containers: [] },
        { name: "db", nodeId: "service:db", state: "running", containers: [] },
        { name: "cache", nodeId: "service:cache", state: "stopped", containers: [] },
      ],
    }
    const live = Object.fromEntries(buildCanvas(g, status).edges.map((e) => [e.target, e.live]))
    expect(live).toEqual({ "service:db": true, "service:cache": false })
  })
})

describe("placeNew", () => {
  const placedApi = service("api", {}, { position: { x: 0, y: 0 } })
  const placedDb = service("db", {}, { position: { x: 400, y: 0 } })

  test("a new service joins its frame below the members already there", () => {
    const c = buildCanvas({ nodes: [placedApi, placedDb, service("cache")], edges: [] })
    const pos = placeNew(c.items, c.edges).get("service:cache")!
    expect(pos.x).toBe(0)
    expect(pos.y).toBeGreaterThanOrEqual(SERVICE_H)
  })

  test("new cards never overlap placed cards or each other", () => {
    const nodes = [placedApi, placedDb, service("a"), service("b"), service("c"), volume("v1"), volume("v2")]
    const c = buildCanvas({ nodes, edges: [] })
    placeNew(c.items, c.edges)
    const cards = c.items.filter((i) => i.kind !== "frame")
    for (const a of cards) for (const b of cards) if (a !== b) expect(intersects(a, b)).toBe(false)
  })

  test("a volume used by a placed service lines up to the right of that service's frame", () => {
    const nodes = [placedApi, placedDb, volume("data")]
    const c = buildCanvas({ nodes, edges: [edge("volume", "volume:data", "service:db")] })
    const pos = placeNew(c.items, c.edges).get("volume:data")!
    const frame = byId(c.items).get(DEFAULT_NETWORK)!
    expect(pos.x).toBeGreaterThanOrEqual(frame.x + frame.w)
    expect(pos.y).toBe(0)
  })

  test("a second volume joins the column of the existing volumes without covering them", () => {
    const nodes = [placedApi, placedDb, volume("old", { position: { x: 900, y: 0 } }), volume("data")]
    const c = buildCanvas({ nodes, edges: [edge("volume", "volume:data", "service:db")] })
    const pos = placeNew(c.items, c.edges).get("volume:data")!
    const old = byId(c.items).get("volume:old")!
    expect(pos.x).toBe(900)
    expect(intersects({ ...pos, w: old.w, h: old.h }, old)).toBe(false)
  })

  test("an unrelated top-level card starts a row below the drawing, outside every frame", () => {
    const nodes = [placedApi, placedDb, volume("loose")]
    const c = buildCanvas({ nodes, edges: [] })
    placeNew(c.items, c.edges)
    const items = byId(c.items)
    const v = items.get("volume:loose")!
    const frame = items.get(DEFAULT_NETWORK)!
    expect(v.y).toBeGreaterThan(frame.y + frame.h)
  })

  test("an empty network frame is placed like a card; a frame with members is not", () => {
    const nodes = [placedApi, network("empty")]
    const c = buildCanvas({ nodes, edges: [] })
    const out = placeNew(c.items, c.edges)
    expect(out.has("network:empty")).toBe(true)
    expect(out.has(DEFAULT_NETWORK)).toBe(false)
  })

  test("placed items keep their positions", () => {
    const c = buildCanvas({ nodes: [placedApi, placedDb, service("x")], edges: [] })
    const out = placeNew(c.items, c.edges)
    expect(out.has("service:api")).toBe(false)
    expect(byId(c.items).get("service:db")).toMatchObject({ x: 400, y: 0 })
  })

  test("the first service of an empty canvas gets a spot", () => {
    const c = buildCanvas({ nodes: [service("only")], edges: [] })
    expect(placeNew(c.items, c.edges).get("service:only")).toBeDefined()
  })
})

describe("relativePosition", () => {
  test("members are positioned relative to their frame; top-level items absolutely", () => {
    const c = buildCanvas({ nodes: [service("api", {}, { position: { x: 100, y: 100 } }), volume("v", { position: { x: 700, y: 50 } })], edges: [] })
    const items = byId(c.items)
    expect(relativePosition(items.get("service:api")!, items)).toEqual({ x: FRAME_PAD, y: FRAME_HEADER })
    expect(relativePosition(items.get("volume:v")!, items)).toEqual({ x: 700, y: 50 })
  })
})

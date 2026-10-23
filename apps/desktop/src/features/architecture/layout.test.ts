import { describe, expect, test } from "bun:test"
import { containerLinks } from "./layout"
import type { ArchCard, ArchModel } from "./model"

const card = (id: string, parent: string): ArchCard => ({ id, parent, nodeId: id, provider: "gcp", type: id, kind: "resource", category: "networking", name: id, details: [] })

describe("containerLinks", () => {
  // Users → DNS → load balancer (Global) → Cloud Run (region); Cloud Run → its service account (Global).
  const model: ArchModel = {
    groups: [
      { id: "cloud", kind: "cloud", label: "Google Cloud" },
      { id: "project", parent: "cloud", kind: "account", label: "Project" },
      { id: "global", parent: "project", kind: "region", label: "Global" },
      { id: "region", parent: "project", kind: "region", label: "Region · us-central1" },
    ],
    cards: [card("dns", "global"), card("lb", "global"), card("sa", "global"), card("run", "region")],
    actors: [{ id: "actor:users", kind: "users", label: "Users" }],
    edges: [
      { id: "e1", source: "actor:users", target: "dns", kind: "data" },
      { id: "e2", source: "dns", target: "lb", kind: "data" },
      { id: "e3", source: "lb", target: "run", kind: "data" },
      { id: "e4", source: "run", target: "sa", kind: "control" },
    ],
    providers: ["gcp"],
    regions: ["us-central1"],
  }
  const links = containerLinks(model)

  test("the group Users enter through comes first, between them and the region", () => {
    expect([...links("project").first]).toEqual(["global"])
    // At the top, Users are laid out with the cloud: nothing to put first.
    expect([...links("").first]).toEqual([])
  })

  test("groups are laid out along the data, not back along dashed lines", () => {
    expect(links("project").edges).toEqual([{ id: "stand-in:global->region", sources: ["global"], targets: ["region"] }])
    // Edges between direct children stay as they are.
    expect(links("global").edges).toEqual([{ id: "e2", sources: ["dns"], targets: ["lb"] }])
  })

  test("a child the Users reach that a sibling also feeds is not put first", () => {
    // Compose: web and redis both publish ports, web calls redis (ELK rejects a FIRST node with an incoming edge).
    const compose: ArchModel = {
      groups: [{ id: "net", kind: "docker-network", label: "default" }],
      cards: [card("web", "net"), card("redis", "net"), card("adminer", "net")],
      actors: [{ id: "actor:users", kind: "users", label: "Users" }],
      edges: [
        { id: "u1", source: "actor:users", target: "web", kind: "data" },
        { id: "u2", source: "actor:users", target: "redis", kind: "data" },
        { id: "u3", source: "actor:users", target: "adminer", kind: "data" },
        { id: "d1", source: "web", target: "redis", kind: "data" },
      ],
      providers: [],
      regions: [],
    }
    expect([...containerLinks(compose)("net").first].sort()).toEqual(["adminer", "web"])
  })
})

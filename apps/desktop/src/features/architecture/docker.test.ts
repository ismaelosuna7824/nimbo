import { describe, expect, test } from "bun:test"
import { techIcon } from "@/components/tech-icons"
import type { ComposeEdge, ComposeModel, ComposeNode, ComposeStatus, ServiceFacts } from "@/lib/api/types"
import {
  COMPOSE_PROJECT_GROUP,
  DOCKER_HOST_GROUP,
  deriveDockerArchitecture,
  dockerCardScene,
  dockerGroupIconOf,
  dockerLegend,
  dockerSubtitle,
  networkGroupId,
  USERS_ACTOR,
} from "./docker"
import type { ArchModel } from "./model"

const service = (name: string, facts: Partial<ServiceFacts> = {}, extra: Partial<ComposeNode> = {}): ComposeNode => ({
  id: `service:${name}`,
  kind: "service",
  name,
  service: { baseImages: [], ports: [], expose: [], environment: [], volumes: [], networks: [], dependsOn: [], ...facts },
  ...extra,
})
const network = (name: string, facts: ComposeNode["network"] = {}, extra: Partial<ComposeNode> = {}): ComposeNode => ({
  id: `network:${name}`,
  kind: "network",
  name,
  network: facts,
  ...extra,
})
/** `target` uses `source` (the graph's direction). */
const edge = (source: string, target: string, kind: ComposeEdge["kind"], extra: Partial<ComposeEdge> = {}): ComposeEdge => ({
  id: `${source}->${target}:${kind}`,
  source,
  target,
  kind,
  ...extra,
})
const wf = (nodes: ComposeNode[], edges: ComposeEdge[] = [], compose: Partial<ComposeModel> = {}) => ({
  name: "Shop",
  compose: { files: ["compose.yaml"], project: "nimbo-dk-abc", graph: { nodes, edges }, ...compose },
})
const card = (m: ArchModel, name: string) => m.cards.find((c) => c.id === `service:${name}`)!

describe("deriveDockerArchitecture", () => {
  test("nests services in their network, the compose project and the Docker host", () => {
    const m = deriveDockerArchitecture(
      wf([service("api", { networks: ["backend"] }), service("db", { networks: ["backend"] }), network("backend", { driver: "bridge" })], [], { name: "shop" }),
    )
    expect(m.groups.map((g) => [g.id, g.kind, g.parent])).toEqual([
      [DOCKER_HOST_GROUP, "docker-host", undefined],
      [COMPOSE_PROJECT_GROUP, "compose-project", DOCKER_HOST_GROUP],
      [networkGroupId("backend"), "docker-network", COMPOSE_PROJECT_GROUP],
    ])
    expect(m.groups[0].label).toBe("Docker host")
    expect(m.groups[1]).toMatchObject({ label: "shop", sublabel: ["nimbo-dk-abc"] })
    expect(m.groups[2]).toMatchObject({ label: "backend", sublabel: ["bridge"], nodeId: "network:backend" })
    expect(card(m, "api")).toMatchObject({ parent: networkGroupId("backend"), nodeId: "service:api", name: "api" })
    expect(m.providers).toEqual([])
    expect(m.regions).toEqual([])
  })

  test("titles the project with the workflow name when compose has no name", () => {
    const m = deriveDockerArchitecture(wf([service("web")]))
    expect(m.groups.find((g) => g.id === COMPOSE_PROJECT_GROUP)!.label).toBe("Shop")
  })

  test("puts services without networks on the implicit default network, shown only when used", () => {
    const m = deriveDockerArchitecture(wf([service("web"), service("db", { networks: ["data"] }), network("data")]))
    expect(card(m, "web").parent).toBe(networkGroupId("default"))
    expect(m.groups.find((g) => g.id === networkGroupId("default"))).toMatchObject({ label: "default", sublabel: ["bridge"], nodeId: undefined })

    const only = deriveDockerArchitecture(wf([service("db", { networks: ["data"] }), network("data")]))
    expect(only.groups.map((g) => g.id)).not.toContain(networkGroupId("default"))
  })

  test("draws nothing for a project without services", () => {
    const m = deriveDockerArchitecture(wf([network("data")]))
    expect(m).toEqual({ groups: [], cards: [], actors: [], edges: [], providers: [], regions: [] })
  })

  test("places a service on several networks in its first one, with a line to the others", () => {
    const m = deriveDockerArchitecture(
      wf([service("api", { networks: ["front", "back"] }), service("db", { networks: ["back"] }), network("front"), network("back")]),
    )
    expect(card(m, "api").parent).toBe(networkGroupId("front"))
    expect(m.edges).toContainEqual({ id: `service:api->${networkGroupId("back")}`, source: "service:api", target: networkGroupId("back"), kind: "control" })
    expect(m.edges.some((e) => e.target === networkGroupId("front"))).toBe(false)
  })

  test("the Users reach every service that publishes ports", () => {
    const m = deriveDockerArchitecture(
      wf([
        service("web", { ports: [{ host: "8080", container: "80", protocol: "tcp", published: true }] }),
        service("db", { expose: ["5432"] }),
        service("admin", { ports: [{ host: "127.0.0.1", container: "9000", published: false }] }),
      ]),
    )
    expect(m.actors).toEqual([{ id: USERS_ACTOR, kind: "users", label: "Users", sublabel: "published ports" }])
    expect(m.edges.filter((e) => e.source === USERS_ACTOR)).toEqual([{ id: `${USERS_ACTOR}->service:web`, source: USERS_ACTOR, target: "service:web", kind: "data" }])
    expect(card(m, "web").details).toContain("8080→80/tcp")

    expect(deriveDockerArchitecture(wf([service("db")])).actors).toEqual([])
  })

  test("requests go from the user to the used service (the graph's reverse), once per pair", () => {
    const m = deriveDockerArchitecture(
      wf(
        [service("api"), service("db"), service("cache"), service("legacy"), network("default")],
        [
          edge("service:db", "service:api", "depends_on", { condition: "service_healthy" }),
          edge("service:db", "service:api", "inferred", { envKey: "DATABASE_URL" }),
          edge("service:cache", "service:api", "inferred", { envKey: "REDIS_URL" }),
          edge("service:api", "service:legacy", "links"),
          edge("network:default", "service:api", "network"),
        ],
      ),
    )
    const requests = m.edges.filter((e) => e.source.startsWith("service:") && e.target.startsWith("service:"))
    expect(requests).toEqual([
      { id: "service:api->service:db", source: "service:api", target: "service:db", kind: "data" },
      { id: "service:api->service:cache", source: "service:api", target: "service:cache", kind: "data" },
      { id: "service:legacy->service:api", source: "service:legacy", target: "service:api", kind: "data" },
    ])
    expect(m.edges.some((e) => e.source === "network:default" || e.target === "network:default")).toBe(false)
  })

  test("folds volumes, secrets, configs and Dockerfile stages into the service card", () => {
    const m = deriveDockerArchitecture(
      wf(
        [
          service("db", {
            image: { ref: "postgres:16", repo: "postgres", tag: "16" },
            volumes: [
              { type: "volume", source: "db-data", target: "/var/lib/postgresql/data" },
              { type: "volume", source: "shared", target: "/shared", readOnly: true },
              { type: "bind", source: "./init", target: "/docker-entrypoint-initdb.d", readOnly: true },
              { type: "tmpfs", target: "/tmp" },
            ],
          }),
          service("api", { build: { context: "api", dockerfile: "api/Dockerfile", target: "runtime" }, baseImages: ["node:20", "node:20-slim"] }),
          { id: "stage:api:deps", kind: "build-stage", name: "deps", parent: "service:api", stage: { index: 0, name: "deps", from: "node:20", dockerfile: "api/Dockerfile", line: 1 } },
          { id: "stage:api:runtime", kind: "build-stage", name: "runtime", parent: "service:api", stage: { index: 2, name: "runtime", from: "node:20-slim", dockerfile: "api/Dockerfile", line: 9 } },
          { id: "stage:api:builder", kind: "build-stage", name: "builder", parent: "service:api", stage: { index: 1, name: "builder", from: "deps", fromStage: "deps", dockerfile: "api/Dockerfile", line: 5 } },
          service("worker", { build: { context: ".", dockerfile: "worker.Dockerfile" }, baseImages: ["python:3.12"] }),
          { id: "volume:db-data", kind: "volume", name: "db-data" },
          { id: "volume:shared", kind: "external", externalKind: "volume", name: "shared", external: true },
          { id: "secret:db_password", kind: "secret", name: "db_password" },
          { id: "config:pg_conf", kind: "config", name: "pg_conf" },
        ],
        [
          edge("volume:db-data", "service:db", "volume", { path: "/var/lib/postgresql/data" }),
          edge("secret:db_password", "service:db", "secret"),
          edge("config:pg_conf", "service:db", "config"),
          edge("stage:api:runtime", "service:api", "build"),
        ],
      ),
    )
    expect(card(m, "db").details).toEqual([
      "postgres:16",
      "vol db-data → /var/lib/postgresql/data",
      "vol shared (external) → /shared (ro)",
      "bind ./init → /docker-entrypoint-initdb.d (ro)",
      "tmpfs /tmp",
      "secret db_password",
      "config pg_conf",
    ])
    expect(card(m, "api").details).toEqual(["build ./api (target runtime)", "3-stage build: deps → builder → runtime"])
    expect(card(m, "worker").details).toEqual(["build . (worker.Dockerfile)", "from python:3.12"])
    // Stages, volumes, secrets and configs are lines, not cards.
    expect(m.cards.map((c) => c.id)).toEqual(["service:db", "service:api", "service:worker"])
  })

  test("shows external and internal networks as such", () => {
    const m = deriveDockerArchitecture(
      wf([
        service("proxy", { networks: ["edge"] }),
        service("db", { networks: ["private"] }),
        { id: "network:edge", kind: "external", externalKind: "network", name: "edge", external: true, network: { name: "traefik_edge" } },
        network("private", { driver: "bridge", internal: true }),
      ]),
    )
    expect(m.groups.find((g) => g.id === networkGroupId("edge"))).toMatchObject({ kind: "docker-network", scope: "external", sublabel: ["external · traefik_edge"] })
    expect(m.groups.find((g) => g.id === networkGroupId("private"))).toMatchObject({ kind: "docker-network", scope: "internal", sublabel: ["bridge · internal"] })
    expect(dockerLegend(m).map((e) => e.kind)).toEqual(["data", "internal-network", "external-network"])
  })

  test("adds the containers' state and the host ports actually bound", () => {
    const nodes = [
      service("web", { image: { ref: "nginx:1.27", repo: "nginx" }, replicas: 3, ports: [{ host: "8080", container: "80", protocol: "tcp", published: true }] }),
      service("db", { image: { ref: "postgres:16", repo: "postgres" }, ports: [{ host: "5432", container: "5432", published: true }] }),
      service("debug", { image: { ref: "busybox", repo: "busybox" }, profiles: ["debug"] }, { inactive: true }),
    ]
    const status: ComposeStatus = {
      workflowId: "abc",
      project: "nimbo-dk-abc",
      state: "running",
      services: [
        {
          name: "web",
          nodeId: "service:web",
          state: "running",
          health: "healthy",
          containers: [
            {
              id: "1",
              name: "nimbo-dk-abc-web-1",
              state: "running",
              health: "healthy",
              exitCode: 0,
              restarts: 0,
              image: "nginx:1.27",
              imageId: "sha256:1",
              ports: [{ container: 80, protocol: "tcp", hostIp: "0.0.0.0", host: 8081 }],
            },
          ],
        },
        { name: "db", nodeId: "service:db", state: "absent", containers: [] },
      ],
      remaps: [{ service: "db", containerPort: 5432, protocol: "tcp", hostPort: 5432, port: 5433 }],
      updatedAt: "2026-10-22T00:00:00Z",
    }
    const m = deriveDockerArchitecture(wf(nodes), status)
    expect(card(m, "web").details).toEqual(["nginx:1.27", "×3", "running · healthy", "8081→80/tcp (remapped from 8080)"])
    expect(card(m, "db").details).toEqual(["postgres:16", "not running", "5433→5432/tcp (remapped from 5432)"])
    expect(card(m, "debug")).toMatchObject({ existing: true, details: ["busybox", "profile debug · inactive"] })

    // Without status: the compose file's ports, no state.
    expect(card(deriveDockerArchitecture(wf(nodes)), "web").details).toEqual(["nginx:1.27", "×3", "8080→80/tcp"])
  })
})

describe("docker scene", () => {
  test("cards use the service's technology icon and its facts", () => {
    const node = service("db", { image: { ref: "postgres:16", repo: "postgres" } })
    const m = deriveDockerArchitecture(wf([node]))
    const scene = dockerCardScene(m.cards[0], node)
    expect(scene.icon).toBe(techIcon("postgres").src)
    expect(scene.text.title).toBe("db")
    expect(scene.text.lines.join(" ")).toContain("postgres:16")
    expect(scene.text.tooltip).toContain("service:db")
  })

  test("group icons and subtitle", () => {
    const m = deriveDockerArchitecture(wf([service("web")]))
    const icons = new Map(m.groups.map((g) => [g.kind, dockerGroupIconOf(g)]))
    expect(icons.get("docker-host")).toBe(techIcon("docker").src)
    expect(icons.get("compose-project")).toBeUndefined()
    expect(icons.get("docker-network")).toBe(techIcon("network").src)
    expect(dockerSubtitle(wf([]))).toBe("Docker Compose · nimbo-dk-abc")
  })
})

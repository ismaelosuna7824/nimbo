import { describe, expect, test } from "bun:test"
import type { ComposeGraph, ComposeNode, KubeGraph, KubeNode, ResourceSchema, WorkflowNode } from "@/lib/api/types"
import { buildTour, listOf, MAX_FOCUS, tourCaption, tourStops, type TourGraph, type TourNode } from "./tour"
import { dockerTourGraph, kubeTourGraph, notCreated, terraformTourGraph, terraformTourKind, type CanvasCard, type CanvasLink } from "./tour-graph"

const node = (id: string): WorkflowNode => {
  const data = id.startsWith("data.")
  const [type, name] = (data ? id.slice(5) : id).split(".")
  return { id, kind: data ? "data" : "resource", type, name, position: { x: 0, y: 0 }, positioned: true, body: { attrs: [], blocks: [] } }
}
/** [referenced, referrer] pairs as canvas edges (a Terraform edge points from the referenced resource). */
const links = (pairs: [string, string][]): CanvasLink[] => pairs.map(([source, target]) => ({ id: `${source}>${target}`, source, target }))
const LABELS: Record<string, string> = {
  azurerm_resource_group: "Resource Group",
  azurerm_virtual_network: "Virtual Network",
  azurerm_subnet: "Subnet",
  azurerm_network_security_group: "Network Security Group",
  azurerm_private_dns_zone: "Private DNS Zone",
  azurerm_log_analytics_workspace: "Log Analytics Workspace",
  azurerm_postgresql_flexible_server: "PostgreSQL Flexible Server",
  azurerm_container_app_environment: "Container App Environment",
  azurerm_container_app: "Container App",
  azurerm_key_vault: "Key Vault",
  azurerm_key_vault_secret: "Key Vault Secret",
  azurerm_servicebus_namespace: "Service Bus Namespace",
  azurerm_servicebus_queue: "Service Bus Queue",
  azurerm_user_assigned_identity: "User Assigned Identity",
}
const schemaOf = (n: WorkflowNode) => (LABELS[n.type] ? ({ label: LABELS[n.type], flociSupported: n.type !== "azurerm_log_analytics_workspace" } as ResourceSchema) : undefined)

// The user's "Azure Orders API" (its Build canvas as laid out: x is the dependency column, y top to bottom).
const AZURE: [string, number, number][] = [
  ["azurerm_resource_group.main", 12, 552],
  ["random_string.suffix", 350, 253],
  ["random_password.postgres", 688, 180],
  ["data.azurerm_client_config.current", 688, 296],
  ["azurerm_private_dns_zone.postgres", 350, 34],
  ["azurerm_virtual_network.main", 350, 489],
  ["azurerm_network_security_group.apps", 350, 616],
  ["azurerm_network_security_group.db", 352, 720],
  ["azurerm_log_analytics_workspace.main", 350, 826],
  ["azurerm_servicebus_namespace.main", 688, 420],
  ["azurerm_key_vault.main", 1026, 229],
  ["azurerm_user_assigned_identity.apps", 1364, 694],
  ["azurerm_subnet.db", 688, 544],
  ["azurerm_subnet.apps", 688, 668],
  ["azurerm_private_dns_zone_virtual_network_link.postgres", 688, 56],
  ["azurerm_subnet_network_security_group_association.db", 1026, 544],
  ["azurerm_subnet_network_security_group_association.apps", 1026, 668],
  ["azurerm_servicebus_queue.order_created", 1026, 420],
  ["azurerm_key_vault_access_policy.apps", 1702, 484],
  ["azurerm_postgresql_flexible_server.main", 1026, 62],
  ["azurerm_container_app_environment.main", 1026, 773],
  ["azurerm_key_vault_secret.database_url", 1702, 112],
  ["azurerm_container_app.api", 2040, 525],
  ["azurerm_container_app.notifier", 2040, 709],
]
const RG = "azurerm_resource_group.main"
const AZURE_LINKS = links([
  ...AZURE.filter(([id]) => /^azurerm_(?!resource_group|key_vault_access|key_vault_secret|subnet_network)/.test(id) && id !== RG).map(([id]) => [RG, id] as [string, string]),
  ["random_string.suffix", "azurerm_key_vault.main"],
  ["random_string.suffix", "azurerm_postgresql_flexible_server.main"],
  ["random_password.postgres", "azurerm_postgresql_flexible_server.main"],
  ["random_password.postgres", "azurerm_key_vault_secret.database_url"],
  ["data.azurerm_client_config.current", "azurerm_key_vault.main"],
  ["data.azurerm_client_config.current", "azurerm_key_vault_access_policy.apps"],
  ["azurerm_virtual_network.main", "azurerm_subnet.db"],
  ["azurerm_virtual_network.main", "azurerm_subnet.apps"],
  ["azurerm_virtual_network.main", "azurerm_private_dns_zone_virtual_network_link.postgres"],
  ["azurerm_private_dns_zone.postgres", "azurerm_private_dns_zone_virtual_network_link.postgres"],
  ["azurerm_private_dns_zone.postgres", "azurerm_postgresql_flexible_server.main"],
  ["azurerm_private_dns_zone_virtual_network_link.postgres", "azurerm_postgresql_flexible_server.main"],
  ["azurerm_subnet.db", "azurerm_postgresql_flexible_server.main"],
  ["azurerm_subnet.db", "azurerm_subnet_network_security_group_association.db"],
  ["azurerm_network_security_group.db", "azurerm_subnet_network_security_group_association.db"],
  ["azurerm_subnet.apps", "azurerm_subnet_network_security_group_association.apps"],
  ["azurerm_network_security_group.apps", "azurerm_subnet_network_security_group_association.apps"],
  ["azurerm_subnet.apps", "azurerm_container_app_environment.main"],
  ["azurerm_log_analytics_workspace.main", "azurerm_container_app_environment.main"],
  ["azurerm_servicebus_namespace.main", "azurerm_servicebus_queue.order_created"],
  ["azurerm_key_vault.main", "azurerm_key_vault_access_policy.apps"],
  ["azurerm_user_assigned_identity.apps", "azurerm_key_vault_access_policy.apps"],
  ["azurerm_key_vault.main", "azurerm_key_vault_secret.database_url"],
  ["azurerm_postgresql_flexible_server.main", "azurerm_key_vault_secret.database_url"],
  ...["azurerm_container_app.api", "azurerm_container_app.notifier"].flatMap((app) => [
    ["azurerm_container_app_environment.main", app] as [string, string],
    ["azurerm_user_assigned_identity.apps", app] as [string, string],
    ["azurerm_key_vault_access_policy.apps", app] as [string, string],
    ["azurerm_servicebus_queue.order_created", app] as [string, string],
  ]),
  ["azurerm_key_vault_secret.database_url", "azurerm_container_app.api"],
])

describe("tour: the Azure Orders API", () => {
  const g = terraformTourGraph({
    nodes: AZURE.map(([id]) => node(id)),
    links: AZURE_LINKS,
    cards: AZURE.map(([id, x, y]) => ({ id, x, y })),
    schemaOf,
    deployed: new Set(["azurerm_resource_group.main"]),
    absent: { ids: new Set(["azurerm_container_app.api", "azurerm_container_app.notifier"]), env: "local" },
  })
  const steps = buildTour(g)
  const stops = tourStops(steps)

  test("starts at the resource group, then the column built on it, and ends with the Container Apps", () => {
    expect(steps[0].nodes).toEqual([RG])
    // Random names, passwords and the client config come along with the first step, briefly.
    expect(steps[0].also).toEqual(["random_string.suffix", "random_password.postgres", "data.azurerm_client_config.current"])
    // Its children, top to bottom like the canvas: one step, glowing together.
    expect(steps[1].nodes).toEqual([
      "azurerm_private_dns_zone.postgres",
      "azurerm_virtual_network.main",
      "azurerm_network_security_group.apps",
      "azurerm_network_security_group.db",
      "azurerm_log_analytics_workspace.main",
      "azurerm_servicebus_namespace.main",
      "azurerm_key_vault.main",
      "azurerm_user_assigned_identity.apps",
    ])
    expect(steps[1].parents).toEqual([RG])
    expect(steps[1].edgesInto["azurerm_virtual_network.main"]).toEqual([`${RG}>azurerm_virtual_network.main`])
    // The subnets are siblings (built on the VNet), the queue has its own step; associations and links go along.
    const subnets = steps.find((s) => s.nodes.includes("azurerm_subnet.db"))!
    expect(subnets.nodes).toEqual(["azurerm_subnet.db", "azurerm_subnet.apps"])
    expect(subnets.also).toEqual(expect.arrayContaining(["azurerm_subnet_network_security_group_association.db", "azurerm_subnet_network_security_group_association.apps"]))
    expect(steps.flatMap((s) => s.also)).toContain("azurerm_key_vault_access_policy.apps")
    expect(steps.at(-1)!.nodes).toEqual(["azurerm_container_app.api"])
    // Every card is shown exactly once.
    expect(steps.flatMap((s) => [...s.nodes, ...s.also]).sort()).toEqual(AZURE.map(([id]) => id).sort())
  })

  test("stops: small groups card by card, a large group as an overview then its cards one by one", () => {
    expect(stops.slice(0, 4).map((s) => [s.label, s.nodes.length])).toEqual([
      ["1", 1],
      ["2", 8],
      ["2a", 1],
      ["2b", 1],
    ])
    expect(stops[3]).toMatchObject({ nodes: ["azurerm_virtual_network.main"], group: steps[1].nodes, parents: [RG], edges: [`${RG}>azurerm_virtual_network.main`] })
    // The subnets (2 siblings) are two steps of their own; their associations go with the subnet they guard.
    const db = stops.find((s) => s.nodes[0] === "azurerm_subnet.db")!
    const apps = stops.find((s) => s.nodes[0] === "azurerm_subnet.apps")!
    expect(apps.step).toBe(db.step + 1)
    expect(db.also).toEqual(["azurerm_subnet_network_security_group_association.db"])
    expect(apps.also).toEqual(["azurerm_subnet_network_security_group_association.apps"])
    // The random suffix and the client config go with the resource group, the first card.
    expect(stops[0].also).toEqual(steps[0].also)
    expect(stops.at(-1)!.nodes).toEqual(["azurerm_container_app.api"])
  })

  test("captions say what each resource is for in this design and what is built on it next", () => {
    const rg = tourCaption(g, stops, 0)
    expect(rg).toMatchObject({ title: "Resource Group main", where: { tone: "deployed" } })
    expect(rg.context).toBeUndefined()
    expect(rg.lines[0].why).toBe("Holds every resource of this design (16).")
    expect(rg.next).toStartWith("→ used by ")
    expect(rg.also.map((c) => c.label)).toEqual(["Random string suffix", "Random password postgres", "Client config current"])

    const column = tourCaption(g, stops, 1)
    expect(column.title).toBe("Built on Resource Group main")
    expect(column.lines).toHaveLength(8)
    expect(column.lines.find((l) => l.label === "Virtual Network main")!.why).toBe("The private network Subnet db and Subnet apps live in.")
    expect(column.lines.find((l) => l.label === "Private DNS Zone postgres")!.why).toBe("Lets PostgreSQL Flexible Server main be found by name inside the network.")
    // Nothing real builds on an NSG directly: its association names the subnet it guards.
    expect(column.lines.find((l) => l.label === "Network Security Group apps")!.why).toBe("Firewall rules: decides which traffic may reach Subnet apps.")

    const vnet = tourCaption(g, stops, 3)
    expect(vnet).toMatchObject({ title: "Virtual Network main", context: "2 of 8 built on Resource Group main", next: "→ used by Subnet db and Subnet apps" })

    const subnet = tourCaption(g, stops, stops.findIndex((s) => s.nodes[0] === "azurerm_subnet.db"))
    expect(subnet).toMatchObject({ title: "Subnet db", next: "→ used by PostgreSQL Flexible Server main" })
    expect(subnet.context).toBeUndefined()

    const last = tourCaption(g, stops, stops.length - 1)
    expect(last.lines[0].why).toStartWith("Runs your code, using Container App Environment main, ")
    expect(last).toMatchObject({ title: "Container App api", next: "The end of the tour: nothing else is built on it.", where: { tone: "absent", text: 'Not created in environment "local"' } })
  })
})

describe("tour: AWS serverless API", () => {
  const AWS: [string, number, number][] = [
    ["aws_dynamodb_table.items", 0, 0],
    ["aws_iam_role.lambda", 0, 120],
    ["aws_apigatewayv2_api.http", 0, 240],
    ["aws_cloudwatch_log_group.api", 0, 360],
    ["data.archive_file.api", 0, 480],
    ["aws_iam_role_policy.items_access", 300, 0],
    ["aws_lambda_function.api", 300, 200],
    ["aws_apigatewayv2_stage.default", 300, 400],
    ["aws_apigatewayv2_integration.api", 600, 100],
    ["aws_lambda_permission.api", 600, 300],
    ["aws_apigatewayv2_route.proxy", 900, 100],
  ]
  const g = terraformTourGraph({
    nodes: AWS.map(([id]) => node(id)),
    links: links([
      ["aws_dynamodb_table.items", "aws_iam_role_policy.items_access"],
      ["aws_iam_role.lambda", "aws_iam_role_policy.items_access"],
      ["aws_dynamodb_table.items", "aws_lambda_function.api"],
      ["aws_iam_role.lambda", "aws_lambda_function.api"],
      ["data.archive_file.api", "aws_lambda_function.api"],
      ["aws_apigatewayv2_api.http", "aws_apigatewayv2_stage.default"],
      ["aws_apigatewayv2_api.http", "aws_apigatewayv2_integration.api"],
      ["aws_lambda_function.api", "aws_apigatewayv2_integration.api"],
      ["aws_apigatewayv2_api.http", "aws_lambda_permission.api"],
      ["aws_lambda_function.api", "aws_lambda_permission.api"],
      ["aws_lambda_function.api", "aws_cloudwatch_log_group.api"],
      ["aws_apigatewayv2_integration.api", "aws_apigatewayv2_route.proxy"],
      ["aws_apigatewayv2_api.http", "aws_apigatewayv2_route.proxy"],
    ]),
    cards: AWS.map(([id, x, y]) => ({ id, x, y })),
    schemaOf: () => undefined,
    deployed: new Set(),
  })
  const steps = buildTour(g)

  test("the foundations first (table, role, API), the function on them, the API's wiring last", () => {
    expect(steps[0].nodes).toEqual(["aws_dynamodb_table.items", "aws_iam_role.lambda", "aws_apigatewayv2_api.http"])
    expect(steps[0].also).toContain("data.archive_file.api")
    expect(steps.findIndex((s) => s.nodes.includes("aws_lambda_function.api"))).toBeGreaterThan(0)
    expect(steps.at(-1)!.nodes).toEqual(["aws_apigatewayv2_route.proxy"])
    expect(steps.flatMap((s) => s.also)).toEqual(expect.arrayContaining(["aws_iam_role_policy.items_access", "aws_lambda_permission.api"]))
    // Three starting points: a step each.
    const stops = tourStops(steps)
    expect(stops.slice(0, 3).map((s) => [s.label, s.nodes])).toEqual([
      ["1", ["aws_dynamodb_table.items"]],
      ["2", ["aws_iam_role.lambda"]],
      ["3", ["aws_apigatewayv2_api.http"]],
    ])
    expect(stops[0].also).toContain("data.archive_file.api")
    expect(tourCaption(g, stops, 1).lines[0].why).toBe("The identity Lambda function api uses to reach other services without passwords.")
  })
})

const svc = (name: string, image: string): ComposeNode => ({
  id: `service:${name}`,
  kind: "service",
  name,
  service: { image: { ref: image, repo: image.split(":")[0] }, baseImages: [], ports: [], expose: [], environment: [], volumes: [], networks: [], dependsOn: [] },
})

describe("tour: Docker", () => {
  test("network and volume, then the data services, then the app, then the proxy", () => {
    const graph: ComposeGraph = {
      nodes: [svc("proxy", "nginx:1.27"), svc("api", "node:22"), svc("db", "postgres:16"), svc("cache", "redis:7"), { id: "volume:pgdata", kind: "volume", name: "pgdata" }],
      edges: [],
    }
    const cards: CanvasCard[] = [
      { id: "network:default", x: 0, y: 0, frame: true },
      { id: "volume:pgdata", x: 0, y: 400 },
      { id: "service:db", x: 300, y: 50, parent: "network:default" },
      { id: "service:cache", x: 300, y: 200, parent: "network:default" },
      { id: "service:api", x: 600, y: 100, parent: "network:default" },
      { id: "service:proxy", x: 900, y: 100, parent: "network:default" },
    ]
    // The canvas draws the user → what it uses.
    const canvasLinks: CanvasLink[] = [
      { id: "e1", source: "service:db", target: "volume:pgdata" },
      { id: "e2", source: "service:api", target: "service:db" },
      { id: "e3", source: "service:api", target: "service:cache" },
      { id: "e4", source: "service:proxy", target: "service:api" },
    ]
    const g = dockerTourGraph({ graph, links: canvasLinks, cards })
    const steps = buildTour(g)
    expect(steps.map((s) => s.nodes)).toEqual([["network:default", "volume:pgdata"], ["service:db", "service:cache"], ["service:api"], ["service:proxy"]])
    const stops = tourStops(steps)
    expect(stops.map((s) => s.label)).toEqual(["1", "2", "3", "4", "5", "6"])
    expect(tourCaption(g, stops, 0).lines[0].why).toBe("The private network PostgreSQL container db, Redis container cache, Node.js container api and 1 more live in.")
    expect(tourCaption(g, stops, 1).lines[0].why).toBe("A disk that keeps the data of PostgreSQL container db when containers are replaced.")
    expect(tourCaption(g, stops, 5)).toMatchObject({ title: "NGINX container proxy", where: { tone: "local", text: "Runs locally in Docker" } })
    expect(tourCaption(g, stops, 5).lines[0].why).toBe("The way in from outside: it sends requests on to Node.js container api.")
  })
})

const kube = (id: string, extra: Partial<KubeNode> = {}): KubeNode => {
  const [kind, namespace, name] = id.split("/")
  return { id, kind, apiVersion: "v1", name, namespace, category: "workload", source: 0, sourceKind: "manifests", sourcePath: "k8s", editable: true, ...extra } as KubeNode
}

describe("tour: Kubernetes", () => {
  test("namespace, then config, secrets and claims, then workloads, then services, then the ingress", () => {
    const graph: KubeGraph = {
      nodes: [
        kube("Namespace/notes/notes"),
        kube("ConfigMap/notes/web"),
        kube("PersistentVolumeClaim/notes/data"),
        kube("StatefulSet/notes/postgres", { workload: { containers: [{ name: "pg", image: "postgres:17" }] } as never }),
        kube("Deployment/notes/web", { workload: { containers: [{ name: "web", image: "ghcr.io/x/notes:1" }] } as never }),
        kube("Service/notes/web", { service: { type: "ClusterIP", ports: [] } as never }),
        kube("Ingress/notes/web"),
      ],
      edges: [],
    }
    const frame = "Namespace/notes/notes"
    const cards: CanvasCard[] = [
      { id: frame, x: 0, y: 0, frame: true },
      ...["ConfigMap/notes/web", "PersistentVolumeClaim/notes/data", "StatefulSet/notes/postgres", "Deployment/notes/web", "Service/notes/web", "Ingress/notes/web"].map((id, i) => ({ id, x: 100 * i, y: 50 * i, parent: frame })),
    ]
    const canvasLinks: CanvasLink[] = [
      { id: "pvc", source: "StatefulSet/notes/postgres", target: "PersistentVolumeClaim/notes/data" },
      { id: "cm", source: "Deployment/notes/web", target: "ConfigMap/notes/web" },
      { id: "env", source: "Deployment/notes/web", target: "StatefulSet/notes/postgres" },
      { id: "sel", source: "Service/notes/web", target: "Deployment/notes/web" },
      { id: "route", source: "Ingress/notes/web", target: "Service/notes/web" },
    ]
    const g = kubeTourGraph({ graph, links: canvasLinks, cards })
    const steps = buildTour(g)
    expect(steps.map((s) => s.nodes)).toEqual([
      [frame],
      ["ConfigMap/notes/web", "PersistentVolumeClaim/notes/data"],
      ["StatefulSet/notes/postgres"],
      ["Deployment/notes/web"],
      ["Service/notes/web"],
      ["Ingress/notes/web"],
    ])
    expect(steps[4].edgesInto["Service/notes/web"]).toEqual(["sel"])
    const stops = tourStops(steps)
    expect(tourCaption(g, stops, 5).lines[0].why).toBe("A stable address in front of Deployment web.")
    expect(tourCaption(g, stops, 3).title).toBe("StatefulSet (PostgreSQL) postgres")
  })
})

describe("buildTour", () => {
  const n = (id: string, y = 0, extra: Partial<TourNode> = {}): TourNode => ({ id, name: id, title: "Thing", kind: "other", x: 0, y, ...extra })

  test("cycles are cut where they close", () => {
    const g: TourGraph = {
      nodes: [n("a"), n("b", 1), n("c", 2)],
      deps: [
        { from: "a", to: "b", edges: [] },
        { from: "b", to: "c", edges: [] },
        { from: "c", to: "a", edges: [] },
      ],
    }
    expect(buildTour(g).flatMap((s) => s.nodes).sort()).toEqual(["a", "b", "c"])
  })

  test("large sibling groups are split top to bottom", () => {
    const kids = Array.from({ length: MAX_FOCUS + 2 }, (_, i) => n(`k${i}`, i))
    const g: TourGraph = { nodes: [n("root"), ...kids], deps: kids.map((k) => ({ from: "root", to: k.id, edges: [] })) }
    expect(buildTour(g).map((s) => s.nodes.length)).toEqual([1, MAX_FOCUS, 2])
  })

  test("a design of plumbing only is toured all the same", () => {
    const g: TourGraph = { nodes: [n("random", 0, { plumbing: true })], deps: [] }
    expect(buildTour(g)).toEqual([{ nodes: ["random"], layer: 0, parents: [], also: [], hostOf: {}, edgesInto: { random: [] } }])
  })

  test("listOf", () => {
    expect(listOf(["a"])).toBe("a")
    expect(listOf(["a", "b"])).toBe("a and b")
    expect(listOf(["a", "b", "c", "d", "e"])).toBe("a, b, c and 2 more")
  })

  test("terraformTourKind", () => {
    expect(terraformTourKind("azurerm_resource_group")).toBe("account")
    expect(terraformTourKind("aws_vpc")).toBe("network")
    expect(terraformTourKind("azurerm_subnet")).toBe("subnet")
    expect(terraformTourKind("aws_security_group")).toBe("security")
    expect(terraformTourKind("azurerm_container_app_environment")).toBe("platform")
    expect(terraformTourKind("aws_sqs_queue")).toBe("messaging")
    expect(terraformTourKind("aws_lambda_function")).toBe("compute")
    expect(terraformTourKind("aws_lb")).toBe("front")
  })

  test("notCreated: blocks the active environment counts to zero", () => {
    const wf = {
      nodes: [{ ...node("azurerm_container_app.api"), body: { attrs: [{ name: "count", value: { expr: "var.sandbox ? 0 : 1" } }], blocks: [] } }, node("azurerm_resource_group.main")],
      edges: [],
      variables: [{ name: "sandbox", body: { attrs: [{ name: "default", value: { expr: "false", literal: false } }], blocks: [] } }],
      locals: [],
      providers: [],
      environments: [{ name: "local", values: { sandbox: { expr: "true", literal: true } }, envVars: [] }],
      activeEnvironment: "local",
    }
    expect([...notCreated(wf)]).toEqual(["azurerm_container_app.api"])
    expect([...notCreated({ ...wf, activeEnvironment: undefined })]).toEqual([])
  })
})

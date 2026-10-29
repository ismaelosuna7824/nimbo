import { describe, expect, test } from "bun:test"
import { deriveArchitecture, type DeriveWorkflow } from "@/features/architecture/derive"
import { deriveDockerArchitecture } from "@/features/architecture/docker"
import { deriveKubernetesArchitecture } from "@/features/architecture/kubernetes"
import type { Body, ComposeGraph, ComposeNode, KubeGraph, KubeNode, ResourceSchema, WorkflowEdge, WorkflowNode } from "@/lib/api/types"
import { captionOf, noEntryText, summaryText } from "./captions"
import { OPERATOR_ACTOR, operatorEntries, withClusterOperators } from "./clusters"
import { describeDocker, describeKubernetes, describeTerraform, firstSentence, referenceText } from "./describe"
import { buildWalk, leadsSomewhere, SHOW_ALL, walkEntries, walkSummary } from "./paths"

// Terraform designs written as HCL expressions; references (edges) are derived from the expressions the way
// the daemon does, with indexed block paths ("template[0].container[0].env[1].value").
type Blocks = [type: string, attrs: Record<string, string>, blocks?: Blocks][]
const toBody = (attrs: Record<string, string>, blocks: Blocks = []): Body => ({
  attrs: Object.entries(attrs).map(([name, expr]) => {
    const lit = /^"(.*)"$/.exec(expr)
    return { name, value: lit ? { literal: lit[1] } : { expr } }
  }),
  blocks: blocks.map(([type, a, b]) => ({ type, body: toBody(a, b) })),
})
const res = (address: string, attrs: Record<string, string> = {}, blocks: Blocks = []): WorkflowNode => {
  const data = address.startsWith("data.")
  const [type, name] = (data ? address.slice(5) : address).split(".")
  return { id: address, kind: data ? "data" : "resource", type, name, position: { x: 0, y: 0 }, positioned: true, body: toBody(attrs, blocks) }
}
const REF = /(?<![\w.])((?:data\.)?[a-z][a-z0-9]*_[\w-]*\.[A-Za-z_][\w-]*)(?:\.(\w+))?/g
function edgesOf(nodes: WorkflowNode[]): WorkflowEdge[] {
  const ids = new Set(nodes.map((n) => n.id))
  const out: WorkflowEdge[] = []
  const walk = (n: WorkflowNode, b: Body, prefix: string) => {
    for (const a of b.attrs) {
      for (const m of a.value.expr?.matchAll(REF) ?? []) {
        if (!ids.has(m[1]) || m[1] === n.id) continue
        const attribute = prefix + a.name
        out.push({ id: `${m[1]}>${n.id}:${attribute}`, source: m[1], target: n.id, kind: "reference", attribute, ...(m[2] ? { sourceAttr: m[2] } : {}) })
      }
    }
    const seen: Record<string, number> = {}
    for (const blk of b.blocks) {
      const i = seen[blk.type] ?? 0
      seen[blk.type] = i + 1
      walk(n, blk.body, `${prefix}${blk.type}[${i}].`)
    }
  }
  for (const n of nodes) walk(n, n.body, "")
  return out
}
const design = (nodes: WorkflowNode[]): DeriveWorkflow => ({ nodes, edges: edgesOf(nodes), variables: [], locals: [], providers: [{ name: "azurerm", body: toBody({}) }], environments: [] })
const RG = { resource_group_name: "azurerm_resource_group.main.name", location: "azurerm_resource_group.main.location" }

// The user's example: an Azure Container Apps API with PostgreSQL Flexible Server in a private VNet, a
// Service Bus queue a notifier app consumes, Key Vault and Log Analytics.
const azureNodes = [
  res("azurerm_resource_group.main", { name: '"rg-orders"', location: '"westeurope"' }),
  res("azurerm_virtual_network.main", { ...RG, address_space: '["10.20.0.0/16"]' }),
  res("azurerm_subnet.apps", { resource_group_name: "azurerm_resource_group.main.name", virtual_network_name: "azurerm_virtual_network.main.name", address_prefixes: '["10.20.0.0/23"]' }),
  res("azurerm_subnet.db", { resource_group_name: "azurerm_resource_group.main.name", virtual_network_name: "azurerm_virtual_network.main.name", address_prefixes: '["10.20.4.0/24"]' }),
  res("azurerm_private_dns_zone.postgres", { resource_group_name: "azurerm_resource_group.main.name", name: '"orders.postgres.database.azure.com"' }),
  res("azurerm_log_analytics_workspace.main", { ...RG, sku: '"PerGB2018"' }),
  res("azurerm_container_app_environment.main", { ...RG, log_analytics_workspace_id: "azurerm_log_analytics_workspace.main.id", infrastructure_subnet_id: "azurerm_subnet.apps.id" }),
  res("azurerm_user_assigned_identity.api", { ...RG }),
  res("azurerm_key_vault.main", { ...RG, sku_name: '"standard"' }),
  res("azurerm_key_vault_access_policy.api", { key_vault_id: "azurerm_key_vault.main.id", object_id: "azurerm_user_assigned_identity.api.principal_id" }),
  res("azurerm_postgresql_flexible_server.main", {
    ...RG,
    version: '"16"',
    delegated_subnet_id: "azurerm_subnet.db.id",
    private_dns_zone_id: "azurerm_private_dns_zone.postgres.id",
  }),
  res("azurerm_servicebus_namespace.main", { ...RG, sku: '"Standard"' }),
  res("azurerm_servicebus_queue.orders", { name: '"orders"', namespace_id: "azurerm_servicebus_namespace.main.id" }),
  res(
    "azurerm_container_app.api",
    { resource_group_name: "azurerm_resource_group.main.name", container_app_environment_id: "azurerm_container_app_environment.main.id" },
    [
      ["identity", { identity_ids: "[azurerm_user_assigned_identity.api.id]" }],
      ["ingress", { external_enabled: "true", target_port: "8080" }],
      [
        "template",
        {},
        [
          [
            "container",
            { name: '"api"' },
            [
              ["env", { name: '"DATABASE_HOST"', value: "azurerm_postgresql_flexible_server.main.fqdn" }],
              ["env", { name: '"QUEUE_NAME"', value: "azurerm_servicebus_queue.orders.name" }],
              ["env", { name: '"KEY_VAULT_URI"', value: "azurerm_key_vault.main.vault_uri" }],
            ],
          ],
        ],
      ],
    ],
  ),
  res("azurerm_container_app.notifier", { resource_group_name: "azurerm_resource_group.main.name", container_app_environment_id: "azurerm_container_app_environment.main.id" }, [
    ["template", {}, [["container", { name: '"notifier"' }, [["env", { name: '"QUEUE_NAME"', value: "azurerm_servicebus_queue.orders.name" }]]]]],
  ]),
]

const SCHEMAS: Record<string, Partial<ResourceSchema>> = {
  azurerm_container_app: { label: "Container App", flociSupported: true },
  azurerm_postgresql_flexible_server: { label: "PostgreSQL Flexible Server", flociSupported: true, description: "Managed PostgreSQL. Private by default." },
  azurerm_servicebus_namespace: { label: "Service Bus Namespace", flociSupported: true },
  azurerm_key_vault: { label: "Key Vault", flociSupported: true },
  azurerm_user_assigned_identity: { label: "User Assigned Identity", flociSupported: true },
  azurerm_container_app_environment: { label: "Container App Environment", flociSupported: true },
  azurerm_log_analytics_workspace: { label: "Log Analytics Workspace", flociSupported: false },
  azurerm_private_dns_zone: { label: "Private DNS Zone", flociSupported: true },
  azurerm_key_vault_secret: { label: "Key Vault Secret", flociSupported: false },
}

describe("Terraform: the Azure Container Apps example", () => {
  const wf = design(azureNodes)
  const model = deriveArchitecture(wf)
  const d = describeTerraform({
    model,
    nodes: wf.nodes,
    edges: wf.edges,
    schemaOf: (n) => SCHEMAS[n.type] as ResourceSchema | undefined,
    deployed: new Set(["azurerm_container_app.api"]),
  })
  const traits = (id: string) => d.info.get(id)!
  const entries = walkEntries(model)

  test("the API is the entry; Postgres and the queue are the ways out; the notifier consumes later", () => {
    expect(entries).toEqual([{ target: "azurerm_container_app.api", actor: "actor:users" }])
    const open = buildWalk(model, entries[0], {}, traits)
    expect(open.pending?.options.sort()).toEqual(["azurerm_postgresql_flexible_server.main", "azurerm_servicebus_namespace.main"])
    const all = buildWalk(model, entries[0], { "azurerm_container_app.api": SHOW_ALL }, traits)
    const ids = all.steps.map((s) => s.at)
    expect(ids).toContain("azurerm_container_app.notifier")
    expect(ids).not.toContain("azurerm_private_dns_zone.postgres")
    expect(ids).not.toContain("azurerm_key_vault.main")
    expect(all.steps.find((s) => s.at === "azurerm_servicebus_namespace.main")).toMatchObject({ async: true })
    expect(all.steps.find((s) => s.at === "azurerm_container_app.notifier")).toMatchObject({ afterAsync: true, reversed: true, end: true })
    expect(summaryText(walkSummary(model, all), d)).toBe(
      "The request passed through 4 components; 1 continued asynchronously. It ended at PostgreSQL Flexible Server main and Container App notifier.",
    )
  })

  test("captions: friendly type, role, connection, local support and the supporting pieces", () => {
    const walk = buildWalk(model, entries[0], { "azurerm_container_app.api": "azurerm_postgresql_flexible_server.main" }, traits)
    const api = captionOf(walk, 1, d)
    expect(api).toMatchObject({ name: "api", type: "Azure Container App", where: { tone: "deployed" } })
    expect(api.text).toStartWith("It receives the request and runs your code to handle it.")
    expect(api.facts).toEqual(["external ingress"])
    const also = api.also.map((c) => c.label)
    expect(also).toEqual(expect.arrayContaining(["Key Vault main", "User Assigned Identity api", "Container App Environment main", "Log Analytics Workspace main"]))

    const db = captionOf(walk, 2, d)
    expect(db).toMatchObject({ type: "Azure PostgreSQL Flexible Server", about: "Managed PostgreSQL.", via: "environment variable DATABASE_HOST" })
    expect(db.text).toBe("It stores the data and answers the query. The answer travels back the way it came.")
    expect(db.also.map((c) => c.label)).toContain("Private DNS Zone postgres")
  })

  test("the queue explains the asynchronous hand-off and the consumer picks the message up", () => {
    const walk = buildWalk(model, entries[0], { "azurerm_container_app.api": "azurerm_servicebus_namespace.main" }, traits)
    const queue = captionOf(walk, 2, d)
    expect(queue.async).toBe(true)
    expect(queue.via).toBe("environment variable QUEUE_NAME")
    const notifier = captionOf(walk, 3, d)
    expect(notifier.text).toStartWith("Later, it picks the message up from main")
    expect(notifier.via).toBe("environment variable QUEUE_NAME")
    expect(notifier.where).toEqual({ tone: "local", text: "Runs locally in the sandbox" })
  })
})

// The user's "Azure Orders API": the Container Apps, Key Vault and Service Bus have `count = local.managed`, which is
// 0 in the sandbox environment "local" (sandbox_mode = true). The database URL reaches the API through a Key Vault
// secret built from the server's FQDN.
describe("Terraform: a front door the active environment skips (count = 0)", () => {
  const managed = { count: "local.managed" }
  const nodes = [
    res("azurerm_resource_group.main", { name: '"rg-orders"', location: '"westeurope"' }),
    res("azurerm_virtual_network.main", { ...RG, address_space: '["10.20.0.0/16"]' }),
    res("azurerm_subnet.apps", { resource_group_name: "azurerm_resource_group.main.name", virtual_network_name: "azurerm_virtual_network.main.name" }),
    res("azurerm_subnet.db", { resource_group_name: "azurerm_resource_group.main.name", virtual_network_name: "azurerm_virtual_network.main.name" }),
    res("random_password.postgres", { length: "24" }),
    res("azurerm_postgresql_flexible_server.main", { ...RG, delegated_subnet_id: "azurerm_subnet.db.id", administrator_password: "random_password.postgres.result" }),
    res("azurerm_key_vault.main", { ...managed, ...RG, sku_name: '"standard"' }),
    res("azurerm_key_vault_secret.database_url", {
      ...managed,
      name: '"database-url"',
      key_vault_id: "azurerm_key_vault.main[0].id",
      value: 'join("", ["postgres://u:", random_password.postgres.result, "@", azurerm_postgresql_flexible_server.main.fqdn, ":5432/orders"])',
    }),
    res("azurerm_container_app_environment.main", { ...RG, infrastructure_subnet_id: "azurerm_subnet.apps.id" }),
    res(
      "azurerm_container_app.api",
      { ...managed, resource_group_name: "azurerm_resource_group.main.name", container_app_environment_id: "azurerm_container_app_environment.main.id" },
      [
        ["secret", { name: '"database-url"', key_vault_secret_id: "azurerm_key_vault_secret.database_url[0].versionless_id" }],
        ["ingress", { external_enabled: "true", target_port: "8080" }],
        ["template", {}, [["container", { name: '"api"' }, [["env", { name: '"DATABASE_URL"', secret_name: '"database-url"' }]]]]],
      ],
    ),
  ]
  const wf: DeriveWorkflow = {
    ...design(nodes),
    variables: [{ name: "sandbox_mode", body: toBody({ type: "bool", default: "false" }) }],
    locals: [{ name: "managed", value: { expr: "var.sandbox_mode ? 0 : 1" } }],
    environments: [{ name: "local", values: { sandbox_mode: { expr: "true", literal: true } }, envVars: [] }],
    activeEnvironment: "local",
  }

  test("the active environment draws no front door; the whole design has the API as its entry", () => {
    expect(walkEntries(deriveArchitecture(wf))).toEqual([])
    const model = deriveArchitecture(wf, { everyEnvironment: true })
    expect(walkEntries(model)).toEqual([{ target: "azurerm_container_app.api", actor: "actor:users" }])
    const absent = { ids: new Set(["azurerm_container_app.api", "azurerm_key_vault.main"]), env: "local" }
    const d = describeTerraform({ model, nodes: wf.nodes, edges: wf.edges, schemaOf: (n) => SCHEMAS[n.type] as ResourceSchema | undefined, deployed: new Set(), absent })
    const walk = buildWalk(model, walkEntries(model)[0], {}, (id) => d.info.get(id)!)
    // The connection string in Key Vault is how the API reaches the database.
    expect(walk.steps.map((s) => s.at)).toEqual(["actor:users", "azurerm_container_app.api", "azurerm_postgresql_flexible_server.main"])
    expect(captionOf(walk, 1, d).where).toEqual({ tone: "absent", text: 'Not created in environment "local" (count = 0)' })
    expect(captionOf(walk, 2, d).via).toBe("the Key Vault Secret database-url")
  })
})

describe("Terraform: AWS serverless", () => {
  const nodes = [
    res("aws_apigatewayv2_api.http", { protocol_type: '"HTTP"' }),
    res("aws_apigatewayv2_integration.api", { api_id: "aws_apigatewayv2_api.http.id", integration_uri: "aws_lambda_function.api.invoke_arn" }),
    res("aws_apigatewayv2_route.proxy", { api_id: "aws_apigatewayv2_api.http.id", target: '"integrations/${aws_apigatewayv2_integration.api.id}"' }),
    res("aws_iam_role.lambda", {}),
    res("aws_dynamodb_table.items", { billing_mode: '"PAY_PER_REQUEST"' }),
    res("aws_lambda_function.api", { role: "aws_iam_role.lambda.arn", runtime: '"nodejs22.x"' }, [["environment", { variables: "{ TABLE_NAME = aws_dynamodb_table.items.name }" }]]),
    res("aws_lambda_permission.api", { function_name: "aws_lambda_function.api.function_name", source_arn: '"${aws_apigatewayv2_api.http.execution_arn}/*/*"' }),
  ]
  const wf: DeriveWorkflow = { ...design(nodes), providers: [{ name: "aws", body: toBody({ region: '"us-east-1"' }) }] }
  const model = deriveArchitecture(wf)
  const d = describeTerraform({ model, nodes: wf.nodes, edges: wf.edges, schemaOf: () => undefined, deployed: new Set() })
  const walk = buildWalk(model, walkEntries(model)[0], {}, (id) => d.info.get(id)!)

  test("API Gateway → Lambda → DynamoDB, connected through the integration and an env variable", () => {
    expect(walk.steps.map((s) => s.at)).toEqual(["actor:users", "aws_apigatewayv2_api.http", "aws_lambda_function.api", "aws_dynamodb_table.items"])
    expect(captionOf(walk, 1, d).text).toBe("It is the front door: it receives the request from the internet and routes it. It passes the request on to api (Lambda function).")
    expect(captionOf(walk, 2, d).via).toBe("the API Gateway integration")
    expect(captionOf(walk, 3, d).via).toBe("environment variable TABLE_NAME")
    expect(captionOf(walk, 2, d).also.map((c) => c.label)).toContain("IAM role lambda")
  })
})

describe("helpers", () => {
  test("firstSentence", () => {
    expect(firstSentence("Run code without servers. Starts with a handler.")).toBe("Run code without servers.")
    expect(firstSentence("")).toBeUndefined()
    expect(firstSentence("No period")).toBe("No period")
  })

  test("referenceText falls back to the attribute", () => {
    expect(referenceText({ source: "a.b", attribute: "default_action[0].target_group_arn", sourceAttr: "arn" }, undefined)).toBe("default_action.target_group_arn → arn")
    expect(referenceText({ source: "aws_sqs_queue.jobs", attribute: "event_source_arn" }, undefined)).toBe("an event source mapping (it polls for messages)")
  })
})

const svc = (name: string, image: string, extra: Partial<NonNullable<ComposeNode["service"]>> = {}): ComposeNode => ({
  id: `service:${name}`,
  kind: "service",
  name,
  service: { image: { ref: image, repo: image.split(":")[0] }, baseImages: [], ports: [], expose: [], environment: [], volumes: [], networks: [], dependsOn: [], ...extra },
})

describe("Docker", () => {
  test("producer → broker (asynchronous) → worker, with env keys and volumes", () => {
    const graph: ComposeGraph = {
      nodes: [
        svc("producer", "node:22-alpine", { ports: [{ host: "3000", container: "3000", published: true }] }),
        svc("broker", "rabbitmq:3-management"),
        svc("worker", "python:3.12-slim"),
        { id: "volume:mq", kind: "volume", name: "mq" },
      ],
      edges: [
        { id: "e1", source: "service:broker", target: "service:producer", kind: "inferred", envKey: "AMQP_URL" },
        { id: "e2", source: "service:broker", target: "service:worker", kind: "depends_on", condition: "service_healthy" },
        { id: "e3", source: "volume:mq", target: "service:broker", kind: "volume", path: "/var/lib/rabbitmq" },
      ],
    }
    const model = deriveDockerArchitecture({ name: "mq", compose: { name: "mq", project: "mq", graph } as never })
    const d = describeDocker(model, graph)
    const walk = buildWalk(model, walkEntries(model)[0], {}, (id) => d.info.get(id)!)
    expect(walk.steps.map((s) => s.at)).toEqual(["actor:users", "service:producer", "service:broker", "service:worker"])
    const broker = captionOf(walk, 2, d)
    expect(broker).toMatchObject({ type: "RabbitMQ container", async: true, via: "environment variable AMQP_URL", where: { tone: "local", text: "Runs locally in Docker" } })
    expect(broker.also).toEqual([{ label: "volume mq", detail: "mounted at /var/lib/rabbitmq", nodeId: "volume:mq" }])
    expect(captionOf(walk, 3, d)).toMatchObject({ via: "depends_on (starts after it is healthy)" })
    expect(captionOf(walk, 3, d).text).toStartWith("Later, it picks the message up from broker")
  })
})

const kube = (id: string, extra: Partial<KubeNode> = {}): KubeNode => {
  const [kind, namespace, name] = id.split("/")
  return { id, kind, apiVersion: "v1", name, namespace, category: "workload", source: 0, sourceKind: "manifests", sourcePath: "k8s", editable: true, ...extra } as KubeNode
}

describe("Kubernetes", () => {
  test("Ingress → Deployment through its Service, then Redis", () => {
    const graph: KubeGraph = {
      nodes: [
        kube("Ingress/shop/shop"),
        kube("Service/shop/backend", { service: { type: "ClusterIP", ports: [] } as never }),
        kube("Service/shop/redis", { service: { type: "ClusterIP", ports: [] } as never }),
        kube("Deployment/shop/backend", { workload: { containers: [{ name: "app", image: "ghcr.io/stefanprodan/podinfo:6" }] } as never }),
        kube("Deployment/shop/redis", { workload: { containers: [{ name: "redis", image: "redis:7" }] } as never }),
        kube("ConfigMap/shop/settings"),
      ],
      edges: [
        { id: "r", source: "Ingress/shop/shop", target: "Service/shop/backend", kind: "routes", label: "/api" },
        { id: "s1", source: "Service/shop/backend", target: "Deployment/shop/backend", kind: "selects" },
        { id: "s2", source: "Service/shop/redis", target: "Deployment/shop/redis", kind: "selects" },
        { id: "env", source: "Deployment/shop/backend", target: "Service/shop/redis", kind: "env", envKey: "REDIS_URL" },
        { id: "cm", source: "Deployment/shop/backend", target: "ConfigMap/shop/settings", kind: "envFrom" },
      ],
    }
    const model = deriveKubernetesArchitecture({ id: "k", name: "k", kubernetes: { graph } as never })
    const d = describeKubernetes(model, graph)
    const walk = buildWalk(model, walkEntries(model)[0], {}, (id) => d.info.get(id)!)
    expect(walk.steps.map((s) => s.at)).toEqual(["actor:users", "Ingress/shop/shop", "Deployment/shop/backend", "Deployment/shop/redis"])
    expect(captionOf(walk, 1, d).type).toBe("Kubernetes Ingress")
    const backend = captionOf(walk, 2, d)
    expect(backend.via).toBe("route /api → Service backend")
    expect(backend.also.map((c) => c.label)).toEqual(expect.arrayContaining(["ConfigMap settings", "Service backend"]))
    const redis = captionOf(walk, 3, d)
    expect(redis).toMatchObject({ type: "Kubernetes Deployment (Redis)", via: "environment variable REDIS_URL → Service redis" })
    expect(redis.text).toStartWith("It answers from memory")
  })
})

// The user's EKS cluster: nodes in a private subnet that goes out through a NAT gateway in the public subnet, an
// internet gateway, and nothing that exposes a workload.
describe("Terraform: managed Kubernetes clusters", () => {
  const VPC = { vpc_id: "aws_vpc.main.id" }
  const eksNodes = (cluster: Record<string, string> = {}) => [
    res("aws_vpc.main", { cidr_block: '"10.0.0.0/16"' }),
    res("aws_subnet.nodes", { ...VPC, cidr_block: '"10.0.1.0/24"' }),
    res("aws_subnet.public", { ...VPC, cidr_block: '"10.0.0.0/24"', map_public_ip_on_launch: "true" }),
    res("aws_internet_gateway.main", VPC),
    res("aws_eip.nat", { domain: '"vpc"' }),
    res("aws_nat_gateway.main", { allocation_id: "aws_eip.nat.id", subnet_id: "aws_subnet.public.id" }),
    res("aws_route_table.public", VPC, [["route", { cidr_block: '"0.0.0.0/0"', gateway_id: "aws_internet_gateway.main.id" }]]),
    res("aws_route_table.nodes", VPC, [["route", { cidr_block: '"0.0.0.0/0"', nat_gateway_id: "aws_nat_gateway.main.id" }]]),
    res("aws_route_table_association.public", { subnet_id: "aws_subnet.public.id", route_table_id: "aws_route_table.public.id" }),
    res("aws_route_table_association.nodes", { subnet_id: "aws_subnet.nodes.id", route_table_id: "aws_route_table.nodes.id" }),
    res("aws_iam_role.cluster", { name: '"eks-cluster"' }),
    res("aws_iam_role.nodes", { name: '"eks-nodes"' }),
    res("aws_eks_cluster.main", { name: '"main"', role_arn: "aws_iam_role.cluster.arn" }, [
      ["vpc_config", { subnet_ids: "aws_subnet.nodes[*].id", endpoint_private_access: "true", ...cluster }],
    ]),
    res("aws_eks_node_group.workers", { cluster_name: "aws_eks_cluster.main.name", node_role_arn: "aws_iam_role.nodes.arn", subnet_ids: "aws_subnet.nodes[*].id" }),
  ]
  const AWS_SCHEMAS: Record<string, Partial<ResourceSchema>> = {
    aws_eks_cluster: { label: "EKS Cluster", flociSupported: false },
    aws_eks_node_group: { label: "EKS Node Group", flociSupported: false },
    aws_nat_gateway: { label: "NAT Gateway", flociSupported: true },
    aws_internet_gateway: { label: "Internet Gateway", flociSupported: true },
    aws_lb: { label: "Load Balancer", flociSupported: true },
  }
  const walkthrough = (nodes: WorkflowNode[], provider = "aws") => {
    const wf: DeriveWorkflow = { ...design(nodes), providers: [{ name: provider, body: toBody({}) }] }
    const model = withClusterOperators(deriveArchitecture(wf), wf)
    const schemas = { ...SCHEMAS, ...AWS_SCHEMAS }
    const d = describeTerraform({ model, nodes: wf.nodes, edges: wf.edges, schemaOf: (n) => schemas[n.type] as ResourceSchema | undefined, deployed: new Set() })
    const traits = (id: string) => d.info.get(id)!
    return { model, d, traits, users: walkEntries(model).filter((e) => leadsSomewhere(model, e, traits)), operator: operatorEntries(model) }
  }

  test("no load balancer: nothing receives requests from the internet, and the panel says why", () => {
    const { model, d, users } = walkthrough(eksNodes())
    // The internet reaches the internet gateway, and the gateway only empty subnets: not a way in.
    expect(walkEntries(model)).toEqual([{ target: "aws_internet_gateway.main", actor: "actor:internet" }])
    expect(users).toEqual([])
    expect(noEntryText(model, d, "terraform")).toBe(
      "Nothing in this design receives requests from the internet yet: the EKS nodes run in private subnets and there is no load balancer or Ingress in front of them. Add an ALB/NLB (or an Ingress with the AWS Load Balancer Controller) to expose a workload.",
    )
  })

  test("a public API endpoint: the operator's kubectl reaches the control plane, which runs the work on the nodes", () => {
    const { model, d, traits, operator } = walkthrough(eksNodes())
    expect(operator).toEqual([{ actor: OPERATOR_ACTOR, target: "aws_eks_cluster.main" }])
    const walk = buildWalk(model, operator[0], {}, traits)
    expect(walk.steps.map((s) => s.at)).toEqual([OPERATOR_ACTOR, "aws_eks_cluster.main", "aws_eks_node_group.workers"])
    expect(captionOf(walk, 0, d)).toMatchObject({ name: "Operator", type: "kubectl" })
    expect(captionOf(walk, 1, d).text).toBe("The Kubernetes control plane: its API checks who is calling and records the change. It schedules the work on workers (EKS Node Group).")
    const nodes = captionOf(walk, 2, d)
    expect(nodes.text).toStartWith("The worker nodes run your pods")
    expect(nodes.egress).toBe("In private subnet nodes, outbound through NAT Gateway main → Internet Gateway main")
    expect(summaryText(walkSummary(model, walk), d)).toBe("The request passed through 2 components. It ended at EKS Node Group workers.")
  })

  test("a private API endpoint offers no operator path", () => {
    expect(walkthrough(eksNodes({ endpoint_public_access: "false" })).operator).toEqual([])
  })

  test("a load balancer in front of the node group: the users' request goes through it to the nodes", () => {
    const { model, traits, users } = walkthrough([
      ...eksNodes(),
      res("aws_lb.web", { load_balancer_type: '"application"', subnets: "[aws_subnet.public.id]" }),
      res("aws_lb_target_group.web", { port: "80", protocol: '"HTTP"', ...VPC }),
      res("aws_lb_listener.http", { load_balancer_arn: "aws_lb.web.arn", port: "80" }, [["default_action", { type: '"forward"', target_group_arn: "aws_lb_target_group.web.arn" }]]),
      res("aws_autoscaling_attachment.workers", {
        autoscaling_group_name: "aws_eks_node_group.workers.resources[0].autoscaling_groups[0].name",
        lb_target_group_arn: "aws_lb_target_group.web.arn",
      }),
    ])
    expect(users).toEqual([{ target: "aws_lb.web", actor: "actor:users" }])
    expect(buildWalk(model, users[0], {}, traits).steps.map((s) => s.at)).toEqual(["actor:users", "aws_lb.web", "aws_eks_node_group.workers"])
  })

  test("an Ingress in the design: the users' request goes through it to the workload", () => {
    const { model, traits, users } = walkthrough([
      ...eksNodes(),
      res("kubernetes_deployment_v1.web", { wait_for_rollout: "true" }),
      res("kubernetes_service_v1.web", {}, [["spec", { selector: "kubernetes_deployment_v1.web.metadata[0].labels" }]]),
      res("kubernetes_ingress_v1.web", {}, [["spec", { ingress_class_name: '"alb"', service_name: "kubernetes_service_v1.web.metadata[0].name" }]]),
    ])
    expect(users).toEqual([{ target: "kubernetes_ingress_v1.web", actor: "actor:users" }])
    const steps = buildWalk(model, users[0], {}, traits).steps.map((s) => s.at)
    expect(steps).toEqual(["actor:users", "kubernetes_ingress_v1.web", "kubernetes_service_v1.web", "kubernetes_deployment_v1.web"])
  })

  test("AKS: a cluster that is not private offers the operator path; a private one does not", () => {
    const aks = (attrs: Record<string, string>) => [
      res("azurerm_resource_group.main", { name: '"rg"', location: '"westeurope"' }),
      res("azurerm_kubernetes_cluster.main", { ...RG, dns_prefix: '"main"', ...attrs }, [["default_node_pool", { name: '"system"', vm_size: '"Standard_D2_v2"' }]]),
    ]
    const open = walkthrough(aks({}), "azurerm")
    expect(open.operator).toEqual([{ actor: OPERATOR_ACTOR, target: "azurerm_kubernetes_cluster.main" }])
    expect(open.users).toEqual([])
    expect(noEntryText(open.model, open.d, "terraform")).toContain("Add an Application Gateway or a public Load Balancer")
    expect(walkthrough(aks({ private_cluster_enabled: "true" }), "azurerm").operator).toEqual([])
  })
})

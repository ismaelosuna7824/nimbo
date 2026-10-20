import { describe, expect, test } from "bun:test"
import type { Body, ModuleTree, NamedBlock, Value, Workflow, WorkflowNode } from "@/lib/api/types"
import { deriveArchitecture, type DeriveWorkflow } from "./derive"
import { moduleNameOf, moduleServiceType } from "./modules"

// A synthetic project shaped like a module-heavy import: ECS services, task definitions, a load
// balancer with one target group per service, SQS queues with DLQs, Lambdas and a static site, all
// local modules; plus a registry module that was never downloaded.
const lit = (literal: unknown): Value => ({ literal })
const ref = (expr: string): Value => ({ expr })
const body = (attrs: Record<string, Value>, blocks: Body["blocks"] = []): Body => ({
  attrs: Object.entries(attrs).map(([name, value]) => ({ name, value })),
  blocks,
})
const block = (type: string, attrs: Record<string, Value>): Body["blocks"][number] => ({ type, body: body(attrs) })
const res = (address: string, attrs: Record<string, Value> = {}, blocks: Body["blocks"] = []): WorkflowNode => {
  const data = address.startsWith("data.")
  const [type, name] = (data ? address.slice(5) : address).split(".")
  return { id: address, kind: data ? "data" : "resource", type, name, position: { x: 0, y: 0 }, positioned: false, body: body(attrs, blocks) }
}
const call = (name: string, attrs: Record<string, Value>): WorkflowNode => ({
  id: `module.${name}`,
  kind: "module",
  type: "module",
  name,
  position: { x: 0, y: 0 },
  positioned: true,
  body: body(attrs),
})
const named = (name: string, attrs: Record<string, Value>): NamedBlock => ({ name, body: body(attrs) })
const module = (nodes: WorkflowNode[], inputs: string[], outputs: Record<string, string> = {}): Workflow => ({
  id: "w",
  name: "m",
  createdAt: "",
  updatedAt: "",
  nodes,
  variables: inputs.map((v) => named(v, {})),
  outputs: Object.entries(outputs).map(([k, v]) => named(k, { value: ref(v) })),
  locals: [],
  providers: [],
  other: [],
  environments: [],
  edges: [],
  clouds: [],
  diagnostics: [],
})

const MODULES: ModuleTree["modules"] = {
  "modules/ecs_service": module(
    [
      res("aws_cloudwatch_log_group.ecs", { retention_in_days: ref("var.log_days") }),
      res("aws_ecs_task_definition.app", {
        cpu: ref("var.task_cpu"),
        memory: ref("var.task_memory"),
        container_definitions: ref('jsonencode([{ environment = [{ name = "QUEUE", value = var.queue_url }], logConfiguration = { group = aws_cloudwatch_log_group.ecs.name } }])'),
      }),
      res("aws_ecs_service.app", { launch_type: lit("FARGATE"), desired_count: ref("var.desired_count"), task_definition: ref("aws_ecs_task_definition.app.arn") }, [
        block("network_configuration", { subnets: ref("var.subnet_ids") }),
        block("load_balancer", { target_group_arn: ref("var.target_group_arn") }),
      ]),
      res("aws_appautoscaling_target.ecs", { resource_id: ref('"service/${aws_ecs_service.app.name}"') }),
    ],
    ["task_cpu", "task_memory", "desired_count", "subnet_ids", "target_group_arn", "queue_url", "log_days"],
    { service_name: "aws_ecs_service.app.name" },
  ),
  "modules/loadbalancer": module(
    [
      res("aws_lb.main", { internal: lit(true), load_balancer_type: lit("application"), subnets: ref("var.subnet_ids") }),
      res("aws_lb_target_group.services", { for_each: ref("var.services") }),
      res("aws_lb_listener.http", { load_balancer_arn: ref("aws_lb.main.arn") }, [block("default_action", { target_group_arn: ref('aws_lb_target_group.services["billing"].arn') })]),
    ],
    ["subnet_ids", "services"],
    { target_group_arns: "{ for k, tg in aws_lb_target_group.services : k => tg.arn }", lb_arn: "aws_lb.main.arn" },
  ),
  "modules/networking": module(
    [
      res("aws_vpc.main", { cidr_block: lit("10.0.0.0/16") }),
      res("aws_subnet.private_a", { vpc_id: ref("aws_vpc.main.id"), cidr_block: lit("10.0.10.0/24") }),
    ],
    [],
    { private_subnet_ids: "[aws_subnet.private_a.id]" },
  ),
  "modules/sqs_queue": module(
    [res("aws_sqs_queue.dlq"), res("aws_sqs_queue.main", { redrive_policy: ref("jsonencode({ deadLetterTargetArn = aws_sqs_queue.dlq.arn })") })],
    [],
    { queue_arn: "aws_sqs_queue.main.arn", queue_url: "aws_sqs_queue.main.url" },
  ),
  "modules/lambda_function": module(
    [
      res("aws_iam_role.lambda"),
      res("aws_cloudwatch_log_group.lambda"),
      res("aws_lambda_function.this", { runtime: ref("var.runtime"), memory_size: lit(256), role: ref("aws_iam_role.lambda.arn") }),
    ],
    ["runtime"],
    { function_arn: "aws_lambda_function.this.arn" },
  ),
  "modules/static_site": module(
    [
      res("aws_s3_bucket.site"),
      res("aws_cloudfront_origin_access_control.site"),
      res("aws_cloudfront_distribution.this", { web_acl_id: ref("var.waf_arn") }, [
        block("origin", { domain_name: ref("aws_s3_bucket.site.bucket_regional_domain_name"), origin_access_control_id: ref("aws_cloudfront_origin_access_control.site.id") }),
        block("origin", { domain_name: ref("var.api_origin") }),
      ]),
    ],
    ["waf_arn", "api_origin"],
  ),
  ".terraform/modules/cache": module([res("aws_elasticache_replication_group.this", { engine: lit("redis") })], []),
}
const local = (dir: string) => ({ source: `./${dir}`, local: true, dir })
const ROOT: DeriveWorkflow = {
  nodes: [
    call("networking", { source: lit("./modules/networking") }),
    call("loadbalancer", { source: lit("./modules/loadbalancer"), subnet_ids: ref("module.networking.private_subnet_ids"), services: ref("var.services") }),
    ...["billing", "sso", "worker"].map((s) =>
      call(`ecs_service_${s}`, {
        source: lit("./modules/ecs_service"),
        task_cpu: ref(`var.${s}_cpu`),
        task_memory: lit(1024),
        desired_count: lit(2),
        subnet_ids: ref("module.networking.private_subnet_ids"),
        target_group_arn: ref(`module.loadbalancer.target_group_arns["${s}"]`),
        queue_url: ref("module.sqs_jobs.queue_url"),
      }),
    ),
    call("sqs_jobs", { source: lit("./modules/sqs_queue") }),
    call("lambda_dispatcher", { source: lit("./modules/lambda_function"), runtime: lit("nodejs22.x"), count: lit(3) }),
    call("lambda_unknown", { source: lit("./modules/lambda_function"), count: ref("var.enabled ? 1 : 0") }),
    call("static_site", { source: lit("./modules/static_site"), api_origin: ref("module.loadbalancer.lb_arn") }),
    call("cache", { source: lit("registry.terraform.io/acme/cache/aws"), version: lit("1.0.0") }),
    call("db", { source: lit("terraform-aws-modules/rds/aws"), version: lit("6.1.0") }),
    res("aws_lambda_event_source_mapping.jobs", {
      event_source_arn: ref("module.sqs_jobs.queue_arn"),
      function_name: ref("module.lambda_dispatcher[0].function_arn"),
    }),
  ],
  edges: [],
  variables: [
    named("services", { default: lit({ billing: {}, sso: {}, worker: {} }) }),
    named("billing_cpu", { default: lit(512) }),
    named("sso_cpu", { default: lit(256) }),
  ],
  locals: [],
  providers: [named("aws", { region: lit("us-east-1") })],
  environments: [],
}
const TREE: ModuleTree = {
  calls: {
    "module.networking": local("modules/networking"),
    "module.loadbalancer": local("modules/loadbalancer"),
    "module.ecs_service_billing": local("modules/ecs_service"),
    "module.ecs_service_sso": local("modules/ecs_service"),
    "module.ecs_service_worker": local("modules/ecs_service"),
    "module.sqs_jobs": local("modules/sqs_queue"),
    "module.lambda_dispatcher": local("modules/lambda_function"),
    "module.lambda_unknown": local("modules/lambda_function"),
    "module.static_site": local("modules/static_site"),
    "module.cache": { source: "registry.terraform.io/acme/cache/aws", version: "1.0.0", local: false, dir: ".terraform/modules/cache" },
    "module.db": { source: "terraform-aws-modules/rds/aws", version: "6.1.0", local: false },
  },
  modules: MODULES,
}

describe("module expansion", () => {
  const m = deriveArchitecture(ROOT, { modules: TREE })
  const card = (id: string) => m.cards.find((c) => c.id === id)!

  test("one card per service, titled after the call, its helpers folded in", () => {
    const billing = card("module.ecs_service_billing.aws_ecs_service.app")
    expect(billing).toMatchObject({ label: "Billing", name: "", nodeId: "module.ecs_service_billing" })
    // Inputs resolve: desired_count literal, cpu from a root variable, memory from the call.
    expect(billing.details).toEqual(["FARGATE", "2 tasks", "512 CPU", "1 GB", "autoscaling"])
    expect(billing.folded).toContain("CloudWatch log group")
    expect(m.cards.some((c) => c.type === "aws_ecs_task_definition" || c.type === "aws_appautoscaling_target")).toBe(false)
    // The queue is the card, its DLQ a fact; the ECS module's helpers are not cards.
    expect(card("module.sqs_jobs.aws_sqs_queue.main")).toMatchObject({ label: "Jobs", details: ["standard", "DLQ"] })
    expect(m.cards.some((c) => c.id === "module.sqs_jobs.aws_sqs_queue.dlq")).toBe(false)
    // Two services in one module keep two cards; the access control folds into the distribution.
    const site = m.cards.filter((c) => c.nodeId === "module.static_site").map((c) => [c.type, c.label])
    expect(site.sort()).toEqual([
      ["aws_cloudfront_distribution", "Static site"],
      ["aws_s3_bucket", "Static site"],
    ])
    // Generic module cards are gone for readable modules.
    expect(m.cards.filter((c) => c.kind === "module").map((c) => c.id)).toEqual(["module.db"])
  })

  test("count and for_each multiply; unknown counts print nothing", () => {
    expect(card("module.lambda_dispatcher.aws_lambda_function.this").details).toEqual(["×3", "nodejs22.x", "256 MB"])
    expect(card("module.lambda_unknown.aws_lambda_function.this").details).toEqual(["256 MB"])
    expect(m.cards.flatMap((c) => c.details).some((d) => /N|\+|existing/.test(d))).toBe(false)
  })

  test("references follow module outputs and inputs across modules", () => {
    const flows = m.edges.map((e) => `${e.source} -> ${e.target}`)
    // LB → each service through target_group_arns["…"] (the target group is drawn inside the LB): one arrow to the sub-group.
    expect(flows).toContain("module.loadbalancer.aws_lb.main -> group:kind:group:subnet:module.networking.aws_subnet.private_a:aws_ecs_service")
    // A root helper wiring two modules' outputs: SQS → Lambda.
    expect(flows).toContain("module.sqs_jobs.aws_sqs_queue.main -> module.lambda_dispatcher.aws_lambda_function.this")
    // CloudFront → S3 inside its module, CloudFront → LB through an input.
    expect(flows).toContain("module.static_site.aws_cloudfront_distribution.this -> module.static_site.aws_s3_bucket.site")
    expect(flows).toContain("module.static_site.aws_cloudfront_distribution.this -> module.loadbalancer.aws_lb.main")
    // Subnets reached through module.networking's output place the services and the LB in the VPC.
    const subnet = m.groups.find((g) => g.kind === "subnet")!
    expect(card("module.loadbalancer.aws_lb.main").parent).toBe(subnet.id)
  })

  test("three or more cards of one kind form a labelled, packed sub-group", () => {
    const g = m.groups.find((x) => x.label === "ECS services (3)")!
    expect(g).toMatchObject({ kind: "logical", packed: true })
    expect(m.cards.filter((c) => c.parent === g.id).map((c) => c.label)).toEqual(["Billing", "SSO", "Worker"])
  })

  test("downloaded registry modules expand; others are one card typed from their source", () => {
    expect(card("module.cache.aws_elasticache_replication_group.this")).toMatchObject({ label: "Cache", nodeId: "module.cache" })
    expect(card("module.db")).toMatchObject({ type: "aws_db_instance", label: "DB", details: ["module · terraform-aws-modules/rds/aws", "6.1.0"] })
    expect(m.groups.find((g) => g.id === card("module.db").parent)?.label).toBe("Data")
  })

  test("names and service types of module sources", () => {
    expect(moduleNameOf("./modules/ecs_service")).toBe("ecs_service")
    expect(moduleNameOf("terraform-aws-modules/vpc/aws")).toBe("vpc")
    expect(moduleNameOf("registry.terraform.io/terraform-aws-modules/s3-bucket/aws")).toBe("s3-bucket")
    expect(moduleNameOf("git::https://github.com/acme/terraform-aws-eks.git?ref=v1")).toBe("eks")
    expect(moduleNameOf("github.com/acme/infra//modules/queue")).toBe("queue")
    expect(moduleNameOf("github.com/oracle-devrel/terraform-oci-arch-adb")).toBe("arch-adb")
    expect(moduleServiceType("terraform-aws-modules/vpc/aws", "aws")).toBe("aws_vpc")
    expect(moduleServiceType("terraform-aws-modules/security-group/aws", "aws")).toBe("aws_security_group")
    expect(moduleServiceType("terraform-google-modules/kubernetes-engine/google", "gcp")).toBe("google_container_cluster")
    expect(moduleServiceType("Azure/aks/azurerm", "azure")).toBe("azurerm_kubernetes_cluster")
    expect(moduleServiceType("../compliance/vanta", "aws")).toBeUndefined()
    // Azure Verified Modules, terraform-google-modules and oracle-terraform-modules names.
    expect(moduleServiceType("Azure/avm-res-web-site/azurerm", "azure")).toBe("azurerm_linux_web_app")
    expect(moduleServiceType("Azure/avm-res-network-privatednszone/azurerm", "azure")).toBe("azurerm_private_dns_zone")
    expect(moduleServiceType("Azure/avm-res-containerservice-managedcluster/azurerm", "azure")).toBe("azurerm_kubernetes_cluster")
    expect(moduleServiceType("Azure/avm-res-documentdb-databaseaccount/azurerm", "azure")).toBe("azurerm_cosmosdb_account")
    expect(moduleServiceType("terraform-google-modules/cloud-nat/google", "gcp")).toBe("google_compute_router")
    expect(moduleServiceType("terraform-google-modules/vm/google//modules/mig", "gcp")).toBe("google_compute_region_instance_group_manager")
    expect(moduleServiceType("terraform-google-modules/memorystore/google", "gcp")).toBe("google_redis_instance")
    expect(moduleServiceType("oracle-terraform-modules/drg/oci", "oci")).toBe("oci_core_drg")
    expect(moduleServiceType("github.com/oracle-devrel/terraform-oci-arch-adb", "oci")).toBe("oci_database_autonomous_database")
    // Helpers that create no service are not drawn.
    const helpers = deriveArchitecture({
      ...ROOT,
      nodes: [
        call("naming", { source: lit("Azure/avm-utl-naming/azure") }),
        call("apis", { source: lit("terraform-google-modules/project-factory/google//modules/project_services") }),
      ],
      edges: [],
    })
    expect(helpers.cards).toEqual([])
  })

  test("module conventions: conditional counts, object defaults, outputs and for_each inputs", () => {
    // terraform-*-modules style: `count = var.create ? 1 : 0`, an object input with optional
    // defaults (Azure Verified Modules' resource_types), a for_each read through another module's
    // output, and a call with for_each whose instances agree on an input.
    const tree: ModuleTree = {
      calls: { "module.vcn": { source: "./modules/vcn", local: true, dir: "modules/vcn" }, "module.site": { source: "./modules/site", local: true, dir: "modules/site" } },
      modules: {
        "modules/vcn": {
          ...module(
            [
              res("oci_core_vcn.vcn", { cidr_blocks: ref("var.cidrs") }),
              res("oci_core_nat_gateway.nat", { count: ref("var.create_nat ? 1 : 0"), vcn_id: ref("oci_core_vcn.vcn.id") }),
              res("oci_core_internet_gateway.igw", { count: ref("var.create_igw ? 1 : 0"), vcn_id: ref("oci_core_vcn.vcn.id") }),
            ],
            ["cidrs", "create_nat", "create_igw"],
            { gateways: '{ for k, v in { nat = var.create_nat } : k => v if v }' },
          ),
          variables: [named("cidrs", {}), named("create_nat", { default: lit(false) }), named("create_igw", {})],
        },
        "modules/site": {
          ...module([res("azapi_resource.this", { type: ref("var.types.sites"), for_each: ref("var.gateways") })], ["types", "gateways"]),
          variables: [named("types", { type: ref('object({ sites = optional(string, "Microsoft.Web/sites@2024-04-01") })'), default: ref("{}") }), named("gateways", {})],
        },
      },
    }
    const m = deriveArchitecture(
      {
        ...ROOT,
        nodes: [
          call("vcn", {
            source: lit("./modules/vcn"),
            for_each: ref('{ a = { nat = true }, b = { nat = true } }'),
            create_nat: ref('each.value["nat"]'),
            create_igw: ref("false"),
            cidrs: ref('["10.0.0.0/16"]'),
          }),
          call("site", { source: lit("./modules/site"), gateways: ref("module.vcn.gateways") }),
        ],
        edges: [],
        providers: [{ name: "oci", body: body({}) }],
      },
      { modules: tree },
    )
    const types = m.cards.map((c) => c.type).sort()
    // Both instances create the NAT gateway, neither the internet gateway; the site exists once
    // per gateway the VCN module outputs, typed from its object input's default.
    expect(types).toEqual(["azurerm_linux_web_app", "oci_core_nat_gateway"])
    expect(m.groups.find((g) => g.kind === "vpc")?.label).toBe("VCN · VCN ×2")
  })

  test("without module content the workflow is drawn as before", () => {
    const plain = deriveArchitecture(ROOT)
    expect(plain.cards.find((c) => c.id === "module.ecs_service_billing")).toMatchObject({ kind: "module", type: "aws_ecs_service", label: "ECS service billing" })
  })
})

import { describe, expect, test } from "bun:test"
import type { Body, Value, WorkflowEdge, WorkflowNode } from "@/lib/api/types"
import { deriveArchitecture, humanize, typeLabel, type DeriveWorkflow } from "./derive"

const lit = (literal: unknown): Value => ({ literal })
const ref = (expr: string): Value => ({ expr })
const body = (attrs: Record<string, Value>, blocks: Body["blocks"] = []): Body => ({
  attrs: Object.entries(attrs).map(([name, value]) => ({ name, value })),
  blocks,
})
const node = (address: string, attrs: Record<string, Value> = {}, label?: string, blocks: Body["blocks"] = []): WorkflowNode => {
  const data = address.startsWith("data.")
  const [type, name] = (data ? address.slice(5) : address).split(".")
  return { id: address, kind: data ? "data" : "resource", type, name, label, position: { x: 0, y: 0 }, positioned: true, body: body(attrs, blocks) }
}
const block = (type: string, attrs: Record<string, Value>): Body["blocks"][number] => ({ type, body: body(attrs) })
/** `target` references `source` through `attribute`. */
const edge = (source: string, target: string, attribute: string): WorkflowEdge => ({
  id: `${source}->${target}:${attribute}`,
  source,
  target,
  kind: "reference",
  attribute,
})
const wf = (nodes: WorkflowNode[], edges: WorkflowEdge[], region = "us-east-1"): DeriveWorkflow => ({
  nodes,
  edges,
  variables: [],
  locals: [],
  providers: [{ name: "aws", body: body({ region: lit(region) }) }],
  environments: [],
})

describe("deriveArchitecture", () => {
  test("places resources in their subnet, VPC and region", () => {
    const m = deriveArchitecture(
      wf(
        [
          node("aws_vpc.main", { cidr_block: lit("10.0.0.0/16") }),
          node("aws_subnet.private", { cidr_block: lit("10.0.1.0/24"), availability_zone: lit("us-east-1a") }),
          node("aws_subnet.public", { cidr_block: lit("10.0.0.0/24"), map_public_ip_on_launch: lit(true) }),
          node("aws_instance.app", { instance_type: lit("t3.micro") }),
          node("aws_security_group.app"),
          node("aws_dynamodb_table.items", { billing_mode: lit("PAY_PER_REQUEST") }),
        ],
        [
          edge("aws_vpc.main", "aws_subnet.private", "vpc_id"),
          edge("aws_vpc.main", "aws_subnet.public", "vpc_id"),
          edge("aws_vpc.main", "aws_security_group.app", "vpc_id"),
          edge("aws_subnet.private", "aws_instance.app", "subnet_id"),
          edge("aws_security_group.app", "aws_instance.app", "vpc_security_group_ids"),
        ],
      ),
    )
    const card = (id: string) => m.cards.find((c) => c.id === id)!
    const group = (id: string) => m.groups.find((g) => g.id === id)!
    expect(card("aws_instance.app").parent).toBe("group:subnet:aws_subnet.private")
    expect(card("aws_instance.app").details).toContain("t3.micro")
    expect(group("group:subnet:aws_subnet.private")).toMatchObject({ label: "Private subnet", tier: "private", parent: "group:az:aws_vpc.main:us-east-1a" })
    expect(group("group:subnet:aws_subnet.public")).toMatchObject({ label: "Public subnet", tier: "public", parent: "group:vpc:aws_vpc.main" })
    expect(group("group:vpc:aws_vpc.main")).toMatchObject({ parent: "group:region:aws:us-east-1", sublabel: ["10.0.0.0/16"] })
    // The security group is a line on the instance, not a card.
    expect(m.cards.map((c) => c.id)).not.toContain("aws_security_group.app")
    expect(card("aws_instance.app").security).toEqual(["SG app"])
    expect(card("aws_dynamodb_table.items").parent).toBe("group:logical:group:region:aws:us-east-1:data")
    expect(card("aws_dynamodb_table.items").details).toContain("on-demand")
    // Containment is drawn by nesting, never as arrows.
    expect(m.edges.some((e) => e.source === "aws_vpc.main" || e.target === "aws_vpc.main")).toBe(false)
    expect(m.regions).toEqual(["us-east-1"])
  })

  test("resources spanning subnets share one tier group", () => {
    const m = deriveArchitecture(
      wf(
        [
          node("aws_vpc.main"),
          node("aws_subnet.a", { availability_zone: lit("us-east-1a") }, "private a"),
          node("aws_subnet.b", { availability_zone: lit("us-east-1b") }, "private b"),
          node("aws_db_subnet_group.db"),
          node("aws_db_instance.db", { engine: lit("postgres"), engine_version: lit("16"), multi_az: lit(true) }),
        ],
        [
          edge("aws_vpc.main", "aws_subnet.a", "vpc_id"),
          edge("aws_vpc.main", "aws_subnet.b", "vpc_id"),
          edge("aws_subnet.a", "aws_db_subnet_group.db", "subnet_ids"),
          edge("aws_subnet.b", "aws_db_subnet_group.db", "subnet_ids"),
          edge("aws_db_subnet_group.db", "aws_db_instance.db", "db_subnet_group_name"),
        ],
      ),
    )
    const db = m.cards.find((c) => c.id === "aws_db_instance.db")!
    expect(db.parent).toBe("group:subnet:aws_subnet.a")
    expect(db.details).toEqual(["postgres 16", "Multi-AZ"])
    expect(m.groups.filter((g) => g.kind === "subnet")).toHaveLength(1)
    expect(m.groups.find((g) => g.kind === "subnet")!.sublabel).toEqual(["us-east-1a, us-east-1b"])
    // The subnet group helper is folded into the database, not a card.
    expect(m.cards.map((c) => c.id)).not.toContain("aws_db_subnet_group.db")
    expect(db.folded).toBe("+ DB subnet group")
  })

  test("folds helpers into their card and derives data flow through them", () => {
    const m = deriveArchitecture(
      wf(
        [
          node("aws_apigatewayv2_api.http", { protocol_type: lit("HTTP") }),
          node("aws_apigatewayv2_integration.api"),
          node("aws_apigatewayv2_route.proxy"),
          node("aws_lambda_function.api", { runtime: lit("nodejs22.x"), memory_size: lit(256) }),
          node("aws_lambda_permission.api"),
          node("aws_sqs_queue.jobs"),
          node("aws_lambda_event_source_mapping.jobs"),
          node("aws_dynamodb_table.items"),
          node("aws_iam_role.lambda"),
          node("aws_iam_role_policy.items"),
          node("aws_iam_role_policy.logs"),
          node("aws_cloudwatch_log_group.api", { retention_in_days: lit(14) }),
          node("data.archive_file.api"),
        ],
        [
          edge("aws_apigatewayv2_api.http", "aws_apigatewayv2_integration.api", "api_id"),
          edge("aws_lambda_function.api", "aws_apigatewayv2_integration.api", "integration_uri"),
          edge("aws_apigatewayv2_api.http", "aws_apigatewayv2_route.proxy", "api_id"),
          edge("aws_apigatewayv2_integration.api", "aws_apigatewayv2_route.proxy", "target"),
          edge("aws_lambda_function.api", "aws_lambda_permission.api", "function_name"),
          edge("aws_apigatewayv2_api.http", "aws_lambda_permission.api", "source_arn"),
          edge("aws_sqs_queue.jobs", "aws_lambda_event_source_mapping.jobs", "event_source_arn"),
          edge("aws_lambda_function.api", "aws_lambda_event_source_mapping.jobs", "function_name"),
          edge("aws_iam_role.lambda", "aws_lambda_function.api", "role"),
          edge("aws_iam_role.lambda", "aws_iam_role_policy.items", "role"),
          edge("aws_iam_role.lambda", "aws_iam_role_policy.logs", "role"),
          edge("aws_dynamodb_table.items", "aws_iam_role_policy.items", "policy"),
          edge("aws_sqs_queue.jobs", "aws_iam_role_policy.items", "policy"),
          edge("aws_cloudwatch_log_group.api", "aws_iam_role_policy.logs", "policy"),
          edge("aws_lambda_function.api", "aws_cloudwatch_log_group.api", "name"),
          edge("data.archive_file.api", "aws_lambda_function.api", "filename"),
        ],
      ),
    )
    expect(m.cards.map((c) => c.id).sort()).toEqual([
      "aws_apigatewayv2_api.http",
      "aws_cloudwatch_log_group.api",
      "aws_dynamodb_table.items",
      "aws_iam_role.lambda",
      "aws_lambda_function.api",
      "aws_sqs_queue.jobs",
    ])
    const card = (id: string) => m.cards.find((c) => c.id === id)!
    expect(card("aws_iam_role.lambda").folded).toBe("+ 2 IAM role policies")
    expect(card("aws_apigatewayv2_api.http").folded).toBe("+ API Gateway integration, API Gateway route")
    expect(card("aws_lambda_function.api").details).toEqual(["nodejs22.x", "256 MB"])
    const flows = m.edges.map((e) => `${e.source} -> ${e.target} ${e.kind}`).sort()
    expect(flows).toEqual([
      "actor:users -> aws_apigatewayv2_api.http data",
      "aws_apigatewayv2_api.http -> aws_lambda_function.api data",
      "aws_lambda_function.api -> aws_cloudwatch_log_group.api control",
      "aws_lambda_function.api -> aws_dynamodb_table.items data",
      "aws_lambda_function.api -> aws_iam_role.lambda control",
      // The event source mapping wins over the IAM permission's reverse direction.
      "aws_sqs_queue.jobs -> aws_lambda_function.api data",
    ])
    // Global IAM sits in the cloud, outside the region.
    expect(card("aws_iam_role.lambda").parent).toBe("group:logical:group:cloud:aws:security")
  })

  test("infers external actors from public entry points and gateways", () => {
    const none = deriveArchitecture(wf([node("aws_sqs_queue.q")], []))
    expect(none.actors).toEqual([])

    const site = deriveArchitecture(
      wf([node("aws_s3_bucket.site"), node("aws_s3_bucket_website_configuration.site")], [edge("aws_s3_bucket.site", "aws_s3_bucket_website_configuration.site", "bucket")]),
    )
    expect(site.actors.map((a) => a.id)).toEqual(["actor:users"])
    expect(site.cards[0].details).toContain("static website")
    expect(site.edges.map((e) => e.id)).toEqual(["flow:actor:users->aws_s3_bucket.site"])

    const internalLb = deriveArchitecture(wf([node("aws_lb.internal", { internal: lit(true) })], []))
    expect(internalLb.actors).toEqual([])

    const vpc = deriveArchitecture(
      wf(
        [
          node("aws_vpc.main"),
          node("aws_internet_gateway.gw"),
          node("aws_subnet.web"),
          node("aws_route_table.public", {}),
          node("aws_route.out", { destination_cidr_block: lit("0.0.0.0/0"), gateway_id: ref("aws_internet_gateway.gw.id") }),
          node("aws_route_table_association.web"),
        ],
        [
          edge("aws_vpc.main", "aws_internet_gateway.gw", "vpc_id"),
          edge("aws_vpc.main", "aws_subnet.web", "vpc_id"),
          edge("aws_vpc.main", "aws_route_table.public", "vpc_id"),
          edge("aws_route_table.public", "aws_route.out", "route_table_id"),
          edge("aws_internet_gateway.gw", "aws_route.out", "gateway_id"),
          edge("aws_route_table.public", "aws_route_table_association.web", "route_table_id"),
          edge("aws_subnet.web", "aws_route_table_association.web", "subnet_id"),
        ],
      ),
    )
    expect(vpc.actors.map((a) => [a.id, a.above])).toEqual([["actor:internet", "aws_internet_gateway.gw"]])
    expect(vpc.cards.find((c) => c.id === "aws_internet_gateway.gw")).toMatchObject({ parent: "group:vpc:aws_vpc.main", onBorder: true })
    expect(vpc.groups.find((g) => g.id === "group:subnet:aws_subnet.web")).toMatchObject({ tier: "public", sublabel: ["routes: 0.0.0.0/0 → IGW"] })
    expect(vpc.cards.map((c) => c.id)).toEqual(["aws_internet_gateway.gw"])
    expect(vpc.edges.map((e) => e.id).sort()).toEqual(["flow:actor:internet->aws_internet_gateway.gw", "flow:aws_internet_gateway.gw->group:subnet:aws_subnet.web"])

    const conn = deriveArchitecture(wf([node("aws_sqs_queue.q")], []), { connections: [{ id: "stripe-1a2b3c", name: "Stripe", kind: "http" }] })
    expect(conn.actors).toEqual([{ id: "conn:stripe-1a2b3c", parent: "group:external", kind: "service", label: "Stripe", sublabel: "HTTP API" }])
  })

  test("resolves variables, for_each counts and Azure resource groups", () => {
    const azure: DeriveWorkflow = {
      nodes: [
        node("azurerm_resource_group.main", { name: lit("rg-demo"), location: ref("var.location") }),
        node("azurerm_storage_account.main", { account_tier: lit("Standard"), account_replication_type: lit("LRS") }),
        node("azurerm_storage_queue.jobs"),
        node("azurerm_storage_container.uploads"),
      ],
      edges: [
        edge("azurerm_resource_group.main", "azurerm_storage_account.main", "resource_group_name"),
        edge("azurerm_storage_account.main", "azurerm_storage_queue.jobs", "storage_account_id"),
        edge("azurerm_storage_account.main", "azurerm_storage_container.uploads", "storage_account_id"),
      ],
      variables: [{ name: "location", body: body({ default: lit("eastus") }) }],
      locals: [],
      providers: [{ name: "azurerm", body: body({}) }],
      environments: [],
    }
    const m = deriveArchitecture(azure)
    // Resource group → region (its location) → services.
    const rg = m.groups.find((g) => g.kind === "account")!
    expect(rg).toMatchObject({ label: "Resource group · rg-demo" })
    expect(m.groups.find((g) => g.kind === "region")).toMatchObject({ label: "Region · eastus", parent: rg.id })
    // The queue and the container are drawn inside their storage account.
    const account = m.cards.find((c) => c.id === "azurerm_storage_account.main")!
    expect(m.cards.map((c) => c.id)).toEqual(["azurerm_storage_account.main"])
    expect(account.details).toContain("Standard LRS")
    expect(account.folded).toBe("+ Storage container, Storage queue")
    expect(m.edges).toEqual([])

    const each = deriveArchitecture({
      ...wf([node("aws_sqs_queue.consumer", { for_each: ref("toset(var.consumers)") })], []),
      variables: [{ name: "consumers", body: body({ default: lit(["billing", "shipping"]) }) }],
    })
    expect(each.cards[0].details[0]).toBe("×2")
  })

  test("security groups fold into every card using them; unused ones go to the VPC", () => {
    const m = deriveArchitecture(
      wf(
        [
          node("aws_vpc.main", { cidr_block: lit("10.0.0.0/16") }),
          node("aws_subnet.a"),
          node("aws_security_group.alb", { name: lit("alb") }, undefined, [
            block("ingress", { from_port: lit(443), to_port: lit(443), protocol: lit("tcp"), cidr_blocks: lit(["0.0.0.0/0"]) }),
          ]),
          node("aws_security_group.app", { name: lit("app") }),
          node("aws_vpc_security_group_ingress_rule.app_from_alb", {
            security_group_id: ref("aws_security_group.app.id"),
            referenced_security_group_id: ref("aws_security_group.alb.id"),
            from_port: lit(8080),
            to_port: lit(8080),
            ip_protocol: lit("tcp"),
          }),
          node("aws_security_group.legacy", { name: lit("legacy") }),
          node("aws_lb.web"),
          node("aws_instance.bastion"),
          node("aws_ecs_service.api"),
        ],
        [
          edge("aws_vpc.main", "aws_subnet.a", "vpc_id"),
          edge("aws_vpc.main", "aws_security_group.alb", "vpc_id"),
          edge("aws_vpc.main", "aws_security_group.app", "vpc_id"),
          edge("aws_vpc.main", "aws_security_group.legacy", "vpc_id"),
          edge("aws_security_group.app", "aws_vpc_security_group_ingress_rule.app_from_alb", "security_group_id"),
          edge("aws_security_group.alb", "aws_vpc_security_group_ingress_rule.app_from_alb", "referenced_security_group_id"),
          edge("aws_security_group.alb", "aws_lb.web", "security_groups"),
          edge("aws_security_group.alb", "aws_instance.bastion", "vpc_security_group_ids"),
          edge("aws_security_group.app", "aws_ecs_service.api", "network_configuration.security_groups"),
          edge("aws_subnet.a", "aws_ecs_service.api", "network_configuration.subnets"),
        ],
      ),
    )
    const card = (id: string) => m.cards.find((c) => c.id === id)!
    expect(m.cards.map((c) => c.id).sort()).toEqual(["aws_ecs_service.api", "aws_instance.bastion", "aws_lb.web"])
    // One security group on several cards.
    expect(card("aws_lb.web").security).toEqual(["SG alb: in 443 from anywhere"])
    expect(card("aws_instance.bastion").security).toEqual(["SG alb: in 443 from anywhere"])
    // Rule resources fold into their group's summary.
    expect(card("aws_ecs_service.api").security).toEqual(["SG app: in 8080 from SG alb"])
    expect(m.groups.find((g) => g.kind === "vpc")!.sublabel).toEqual(["10.0.0.0/16", "unused security group: legacy"])
    // Security groups draw no arrows: only Users → the internet-facing load balancer remains.
    expect(m.edges.map((e) => e.id)).toEqual(["flow:actor:users->aws_lb.web"])
  })

  test("route tables fold into their subnets; Elastic IPs into their NAT gateway", () => {
    const m = deriveArchitecture(
      wf(
        [
          node("aws_vpc.main"),
          node("aws_internet_gateway.igw"),
          node("aws_subnet.public", { availability_zone: lit("us-east-1a") }),
          node("aws_subnet.private", { availability_zone: lit("us-east-1a") }),
          node("aws_eip.nat", { domain: lit("vpc") }),
          node("aws_nat_gateway.nat"),
          node("aws_route_table.public"),
          node("aws_route.public", { destination_cidr_block: lit("0.0.0.0/0"), gateway_id: ref("aws_internet_gateway.igw.id") }),
          node("aws_route_table_association.public"),
          node("aws_route_table.private", {}, undefined, [block("route", { cidr_block: lit("0.0.0.0/0"), nat_gateway_id: ref("aws_nat_gateway.nat.id") })]),
          node("aws_route_table_association.private"),
        ],
        [
          edge("aws_vpc.main", "aws_internet_gateway.igw", "vpc_id"),
          edge("aws_vpc.main", "aws_subnet.public", "vpc_id"),
          edge("aws_vpc.main", "aws_subnet.private", "vpc_id"),
          edge("aws_eip.nat", "aws_nat_gateway.nat", "allocation_id"),
          edge("aws_subnet.public", "aws_nat_gateway.nat", "subnet_id"),
          edge("aws_vpc.main", "aws_route_table.public", "vpc_id"),
          edge("aws_route_table.public", "aws_route.public", "route_table_id"),
          edge("aws_internet_gateway.igw", "aws_route.public", "gateway_id"),
          edge("aws_route_table.public", "aws_route_table_association.public", "route_table_id"),
          edge("aws_subnet.public", "aws_route_table_association.public", "subnet_id"),
          edge("aws_vpc.main", "aws_route_table.private", "vpc_id"),
          edge("aws_nat_gateway.nat", "aws_route_table.private", "route.nat_gateway_id"),
          edge("aws_route_table.private", "aws_route_table_association.private", "route_table_id"),
          edge("aws_subnet.private", "aws_route_table_association.private", "subnet_id"),
        ],
      ),
    )
    const group = (id: string) => m.groups.find((g) => g.id === id)!
    expect(m.cards.map((c) => c.id).sort()).toEqual(["aws_internet_gateway.igw", "aws_nat_gateway.nat"])
    expect(group("group:subnet:aws_subnet.public")).toMatchObject({ tier: "public", sublabel: ["routes: 0.0.0.0/0 → IGW"] })
    expect(group("group:subnet:aws_subnet.private")).toMatchObject({ tier: "private", sublabel: ["routes: 0.0.0.0/0 → NAT"] })
    const nat = m.cards.find((c) => c.id === "aws_nat_gateway.nat")!
    expect(nat.parent).toBe("group:subnet:aws_subnet.public")
    expect(nat.details).toContain("Elastic IP")
    // The private subnet's way out is its routes line, not an arrow to the NAT gateway.
    expect(m.edges.map((e) => e.id).sort()).toEqual(["flow:actor:internet->aws_internet_gateway.igw", "flow:aws_internet_gateway.igw->group:subnet:aws_subnet.public"])
  })

  test("target groups fold into their load balancer, task definitions into their service", () => {
    const m = deriveArchitecture(
      wf(
        [
          node("aws_lb.web", { load_balancer_type: lit("application") }),
          node("aws_lb_target_group.app", { port: lit(8080) }),
          node("aws_lb_listener.https"),
          node("aws_ecs_task_definition.api", { cpu: lit("512"), memory: lit("1024") }),
          node("aws_ecs_service.api", { launch_type: lit("FARGATE"), desired_count: lit(3) }),
          node("aws_db_instance.orders", { engine: lit("postgres") }),
          node("aws_iam_role.task"),
        ],
        [
          edge("aws_lb.web", "aws_lb_listener.https", "load_balancer_arn"),
          edge("aws_lb_target_group.app", "aws_lb_listener.https", "default_action.target_group_arn"),
          edge("aws_lb_target_group.app", "aws_ecs_service.api", "load_balancer.target_group_arn"),
          edge("aws_ecs_task_definition.api", "aws_ecs_service.api", "task_definition"),
          edge("aws_db_instance.orders", "aws_ecs_task_definition.api", "container_definitions"),
          edge("aws_iam_role.task", "aws_ecs_task_definition.api", "task_role_arn"),
        ],
      ),
    )
    const card = (id: string) => m.cards.find((c) => c.id === id)!
    expect(m.cards.map((c) => c.id).sort()).toEqual(["aws_db_instance.orders", "aws_ecs_service.api", "aws_iam_role.task", "aws_lb.web"])
    expect(card("aws_lb.web").folded).toBe("+ LB listener, LB target group")
    expect(card("aws_ecs_service.api").details).toEqual(["FARGATE", "3 tasks", "512 CPU", "1 GB"])
    expect(card("aws_ecs_service.api").folded).toBe("+ ECS task definition")
    // The load balancer still feeds the service; the task definition's references are the service's.
    expect(m.edges.map((e) => `${e.source} -> ${e.target} ${e.kind}`).sort()).toEqual([
      "actor:users -> aws_lb.web data",
      "aws_ecs_service.api -> aws_db_instance.orders data",
      "aws_ecs_service.api -> aws_iam_role.task control",
      "aws_lb.web -> aws_ecs_service.api data",
    ])
  })

  test("Users reach an entry point only through the entry points in front of it", () => {
    const m = deriveArchitecture(
      wf(
        [node("aws_route53_zone.main"), node("aws_route53_record.shop"), node("aws_cloudfront_distribution.cdn"), node("aws_lb.web")],
        [
          edge("aws_route53_zone.main", "aws_route53_record.shop", "zone_id"),
          edge("aws_cloudfront_distribution.cdn", "aws_route53_record.shop", "alias.name"),
          edge("aws_lb.web", "aws_cloudfront_distribution.cdn", "origin.domain_name"),
        ],
      ),
    )
    expect(m.edges.map((e) => `${e.source} -> ${e.target}`).sort()).toEqual([
      "actor:users -> aws_route53_zone.main",
      "aws_cloudfront_distribution.cdn -> aws_lb.web",
      "aws_route53_zone.main -> aws_cloudfront_distribution.cdn",
    ])
  })

  test("labels read naturally", () => {
    expect(humanize("test_web")).toBe("Test web")
    expect(humanize("api")).toBe("API")
    expect(typeLabel("aws_iam_role_policy")).toBe("IAM role policy")
    expect(typeLabel("aws_apigatewayv2_route")).toBe("API Gateway route")
  })
})

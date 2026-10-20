// Module calls in the Architecture diagram: local modules (and registry/remote ones `terraform init`
// downloaded) are expanded into their resources, one copy per call, so the diagram shows the services
// a module-heavy project really runs. Kept pure for unit tests.
import type { Body, ModuleCallSource, ModuleTree, Value, WorkflowEdge, WorkflowNode } from "@/lib/api/types"
import type { DeriveWorkflow } from "./derive"
import type { ArchProvider } from "./icons/classify"

/** The root-level module call an expanded resource comes from. */
export interface ModuleInstance {
  /** Node ID of the call in the workflow ("module.ecs_service_billing"): selecting its cards opens it. */
  callId: string
  /** Name of the call ("ecs_service_billing"). */
  name: string
  /** Name of the module itself, from its source ("ecs_service" for ./modules/ecs_service). */
  moduleName: string
}

export interface ExpandedWorkflow {
  wf: DeriveWorkflow
  /** Expanded resource → the root-level call it comes from. */
  instanceOf: Map<string, ModuleInstance>
  /** Expanded resource → the module calls it sits in, outermost first (their count/for_each multiply it). */
  calls: Map<string, WorkflowNode[]>
}

/** Attribute of an expanded module input naming its call's for_each (the input reads `each`). */
export const EACH_ATTR = "__for_each"

/** Nested calls deeper than this are not expanded (modules calling each other in a cycle). */
const MAX_DEPTH = 8

/** One module instance (or the root): its blocks by address and what its expressions refer to. */
interface Scope {
  /** Address prefix of its resources: "" for the root, "module.app." inside module.app. */
  prefix: string
  /** Prefix of its renamed variables and locals ("__m3_"; "" for the root). */
  tag: string
  nodes: Map<string, WorkflowNode>
  locals: Map<string, Value | undefined>
  outputs: Map<string, Value | undefined>
  /** The call's arguments by input name (expressions of the parent scope). */
  args: Map<string, Value>
  parent?: Scope
  /** Expanded module calls by call name. */
  children: Map<string, Scope>
}

/** A traversal: data source, module output, input variable, local value or resource (then an attribute). */
const TRAVERSAL =
  /(?<![\w.])(?:data\.([A-Za-z_][\w-]*)\.([A-Za-z_][\w-]*)|module\.([A-Za-z_][\w-]*)(?:\[[^\]]*\])?(?:\.([A-Za-z_][\w-]*))?|(var|local)\.([A-Za-z_][\w-]*)|([a-z][a-z0-9]*_[\w-]*)\.([A-Za-z_][\w-]*))/g
const SCOPED_NAME = /(?<![\w.])(var|local)\.([A-Za-z_][\w-]*)/g
/** A module output read by its caller ("module.network.private_subnets"). */
const MODULE_OUTPUT = /(?<![\w.])module\.([A-Za-z_][\w-]*)(?:\[[^\]]*\])?\.([A-Za-z_][\w-]*)/g

/** `var.x` and `local.x` of a module instance renamed to its own (`var.__m3_x`), so one resolver serves every instance. */
function renameValue(v: Value, tag: string): Value {
  return v.expr ? { ...v, expr: v.expr.replace(SCOPED_NAME, (_, kind: string, name: string) => `${kind}.${tag}${name}`) } : v
}

function mapBody(b: Body, fn: (v: Value) => Value): Body {
  return {
    ...b,
    attrs: b.attrs.map((a) => ({ ...a, value: fn(a.value) })),
    blocks: b.blocks.map((blk) => ({ ...blk, body: mapBody(blk.body, fn) })),
  }
}

/** Calls `fn` for every attribute of a body with its path as Terraform edges name it ("network_configuration[0].subnets"). */
function walkAttrs(b: Body, fn: (path: string, value: Value, top: boolean) => void, prefix = "") {
  for (const a of b.attrs) fn(prefix + a.name, a.value, prefix === "")
  const counts: Record<string, number> = {}
  for (const blk of b.blocks) {
    const i = counts[blk.type] ?? 0
    counts[blk.type] = i + 1
    walkAttrs(blk.body, fn, `${prefix}${blk.type}[${i}].`)
  }
}

/** "./modules/ecs_service" → "ecs_service"; "terraform-aws-modules/vpc/aws" → "vpc"; git URLs → their repository. */
export function moduleNameOf(source: string) {
  const s = source.replace(/^git::|^[a-z]+::/, "").replace(/\?.*$/, "").replace(/\/+$/, "")
  const sub = s.split("//")
  if (sub.length > 1 && sub[sub.length - 1] && !/^[a-z]+:$/.test(sub[sub.length - 2])) {
    const tail = sub[sub.length - 1].split("/").filter(Boolean)
    if (tail.length) return tail[tail.length - 1]
  }
  const parts = s.split("/").filter(Boolean)
  // Registry address: [host/]namespace/name/provider.
  // ("github.com/org/repo" is a repository, not a registry address.)
  if (!s.startsWith(".") && !s.includes(":") && ((parts.length === 3 && !parts[0].includes(".")) || (parts.length === 4 && parts[0].includes(".")))) return parts[parts.length - 2]
  return (parts[parts.length - 1] ?? "").replace(/\.git$/, "").replace(/^terraform-(aws|google|azurerm|oci)-/, "")
}

/**
 * The workflow with every readable module call replaced by its resources ("module.app.aws_ecs_service.this"),
 * their inputs bound to the call's arguments and references followed across module boundaries (a
 * call's argument `module.lb.target_group_arn` reaches the target group inside module.lb). Without
 * readable calls the workflow is returned as is.
 */
export function expandModules(wf: DeriveWorkflow, tree: ModuleTree | undefined): ExpandedWorkflow {
  const contentOf = (call: ModuleCallSource | undefined) => (call?.dir ? tree?.modules[call.dir] : undefined)
  if (!tree || !wf.nodes.some((n) => n.kind === "module" && contentOf(tree.calls[n.id]))) {
    return { wf, instanceOf: new Map(), calls: new Map() }
  }
  const nodes: WorkflowNode[] = []
  const variables = [...wf.variables]
  const locals = [...wf.locals]
  const instanceOf = new Map<string, ModuleInstance>()
  const calls = new Map<string, WorkflowNode[]>()
  /** Every drawn block with its scope and original (not renamed) body, for references. */
  const placed: { id: string; node: WorkflowNode; scope: Scope }[] = []
  /** The scope the expressions of an expanded block, variable or local are written in. */
  const scopeOf = new Map<object, Scope>()
  let tags = 0

  const build = (
    blocks: WorkflowNode[],
    scope: Scope,
    tag: string,
    path: string,
    instance: ModuleInstance | undefined,
    chain: WorkflowNode[],
    depth: number,
  ) => {
    for (const n of blocks) {
      const id = scope.prefix + n.id
      const node: WorkflowNode = tag ? { ...n, id, body: mapBody(n.body, (v) => renameValue(v, tag)) } : n
      const call = n.kind === "module" ? tree.calls[path + n.id] : undefined
      const content = depth < MAX_DEPTH ? contentOf(call) : undefined
      if (call && content) {
        const inner = `__m${++tags}_`
        const renamedArgs = new Map(node.body.attrs.map((a) => [a.name, a.value]))
        // An argument reading `each.value` of the call's for_each is known when every instance agrees.
        const forEach = renamedArgs.get("for_each")
        for (const v of content.variables ?? []) {
          const arg = renamedArgs.get(v.name)
          const value = arg ?? v.body.attrs.find((a) => a.name === "default")?.value
          // The type keeps the defaults of optional object attributes.
          const type = v.body.attrs.find((a) => a.name === "type")
          const each = arg?.expr && forEach && /\beach\./.test(arg.expr) ? [{ name: EACH_ATTR, value: forEach }] : []
          variables.push({ name: inner + v.name, body: { attrs: [...(value ? [{ name: "default", value }] : []), ...(type ? [type] : []), ...each], blocks: [] } })
          scopeOf.set(variables[variables.length - 1], scope)
        }
        const child: Scope = {
          prefix: `${id}.`,
          tag: inner,
          nodes: new Map(content.nodes.map((x) => [x.id, x])),
          locals: new Map((content.locals ?? []).map((l) => [l.name, l.value])),
          outputs: new Map((content.outputs ?? []).map((o) => [o.name, o.body.attrs.find((a) => a.name === "value")?.value])),
          args: new Map(n.body.attrs.map((a) => [a.name, a.value])),
          parent: scope,
          children: new Map(),
        }
        scope.children.set(n.name, child)
        for (const l of content.locals ?? []) {
          locals.push({ name: inner + l.name, value: renameValue(l.value, inner) })
          scopeOf.set(locals[locals.length - 1], child)
        }
        // Outputs are locals of the instance, so the caller's `module.<name>.<output>` resolves.
        for (const [name, v] of child.outputs) {
          if (!v) continue
          locals.push({ name: `${inner}__out_${name}`, value: renameValue(v, inner) })
          scopeOf.set(locals[locals.length - 1], child)
        }
        const inst = instance ?? { callId: id, name: n.name, moduleName: moduleNameOf(call.source) }
        build(content.nodes, child, inner, `${path}${n.id}.`, inst, [...chain, node], depth + 1)
        continue
      }
      nodes.push(node)
      scopeOf.set(node, scope)
      placed.push({ id, node: n, scope })
      if (instance) {
        instanceOf.set(id, instance)
        calls.set(id, chain)
      }
    }
  }
  const root: Scope = {
    prefix: "",
    tag: "",
    nodes: new Map(wf.nodes.map((n) => [n.id, n])),
    locals: new Map(wf.locals.map((l) => [l.name, l.value])),
    outputs: new Map(),
    args: new Map(),
    children: new Map(),
  }
  build(wf.nodes, root, "", "", undefined, [], 1)
  // `module.<name>.<output>` of an expanded call reads the output's local.
  const outputs = (scope: Scope) => (v: Value): Value =>
    v.expr
      ? {
          ...v,
          expr: v.expr.replace(MODULE_OUTPUT, (m, name: string, output: string) => {
            const child = scope.children.get(name)
            return child?.outputs.get(output) ? `local.${child.tag}__out_${output}` : m
          }),
        }
      : v
  const read = (x: object) => outputs(scopeOf.get(x) ?? root)
  const drawn = nodes.map((n) => ({ ...n, body: mapBody(n.body, read(n)) }))
  const vars = variables.map((v) => ({ ...v, body: mapBody(v.body, read(v)) }))
  const locs = locals.map((l) => ({ ...l, value: read(l)(l.value) }))

  // References of an expression: the drawn blocks it reaches through locals, inputs and module outputs.
  const refsOf = (expr: string, scope: Scope, seen: Set<string>): { id: string; attr?: string }[] => {
    const out: { id: string; attr?: string }[] = []
    const follow = (key: string, v: Value | undefined, at: Scope | undefined) => {
      if (!v?.expr || !at || seen.has(key)) return
      seen.add(key)
      out.push(...refsOf(v.expr, at, seen))
    }
    for (const m of expr.matchAll(TRAVERSAL)) {
      const [, dataType, dataName, mod, output, kind, name, type, resName] = m
      if (dataType) {
        if (scope.nodes.has(`data.${dataType}.${dataName}`)) out.push({ id: `${scope.prefix}data.${dataType}.${dataName}` })
      } else if (mod) {
        const child = scope.children.get(mod)
        if (child) {
          if (output) follow(`${child.prefix}output.${output}`, child.outputs.get(output), child)
        } else if (scope.nodes.has(`module.${mod}`)) {
          out.push({ id: `${scope.prefix}module.${mod}`, attr: output })
        }
      } else if (kind === "var") {
        follow(`${scope.prefix}var.${name}`, scope.args.get(name), scope.parent)
      } else if (kind === "local") {
        follow(`${scope.prefix}local.${name}`, scope.locals.get(name), scope)
      } else if (type && scope.nodes.get(`${type}.${resName}`)?.kind === "resource") {
        out.push({ id: `${scope.prefix}${type}.${resName}` })
      }
    }
    return out
  }
  const edges = new Map<string, WorkflowEdge>()
  for (const p of placed) {
    walkAttrs(p.node.body, (path, value, top) => {
      if (!value.expr) return
      const kind = top && path === "depends_on" ? "depends_on" : "reference"
      for (const t of refsOf(value.expr, p.scope, new Set())) {
        if (t.id === p.id) continue
        const id = `e:${t.id}>${p.id}:${path}:${t.attr ?? ""}`
        edges.set(id, { id, source: t.id, target: p.id, kind, attribute: path, ...(t.attr ? { sourceAttr: t.attr } : {}) })
      }
    })
  }
  return { wf: { ...wf, nodes: drawn, edges: [...edges.values()], variables: vars, locals: locs }, instanceOf, calls }
}

// ------------------------------------------------------------ unreadable modules

/** Module name keywords → the resource type standing for the module, per provider (first match wins). */
const MODULE_SERVICES: readonly (readonly [RegExp, Partial<Record<ArchProvider, string>>])[] = [
  // Helpers that create no service (naming, regions, shared interfaces, enabled APIs): not drawn.
  [/(^|[-_])(naming|regions|interfaces|labels|utils?|tags)$|project[-_]?(services|factory)|gcloud/, { azure: "azapi_deployment", gcp: "google_project_service", oci: "oci_identity_tag", aws: "aws_default_tags" }],
  // Azure Verified Modules ("avm-res-<provider>-<type>") and other names the generic rules miss.
  [/privatednszone|private[-_]dns/, { azure: "azurerm_private_dns_zone", gcp: "google_dns_managed_zone" }],
  [/privateendpoint|private[-_]endpoint/, { azure: "azurerm_private_endpoint" }],
  [/publicip/, { azure: "azurerm_public_ip" }],
  [/natgateway|cloud[-_]nat|cloud[-_]router/, { azure: "azurerm_nat_gateway", gcp: "google_compute_router", oci: "oci_core_nat_gateway" }],
  [/applicationgateway|app[-_]gateway/, { azure: "azurerm_application_gateway" }],
  [/bastion/, { azure: "azurerm_bastion_host", oci: "oci_bastion_bastion", gcp: "google_compute_instance", aws: "aws_instance" }],
  [/routetable|route[-_]table/, { azure: "azurerm_route_table", aws: "aws_route_table", oci: "oci_core_route_table" }],
  [/web[-_]site|webapp|web[-_]app|app[-_]service/, { azure: "azurerm_linux_web_app" }],
  [/serverfarm|service[-_]plan/, { azure: "azurerm_service_plan" }],
  [/containerapp|container[-_]app|managedenvironment/, { azure: "azurerm_container_app" }],
  [/containerservice|managedcluster/, { azure: "azurerm_kubernetes_cluster" }],
  [/documentdb|cosmos/, { azure: "azurerm_cosmosdb_account" }],
  [/apimanagement/, { azure: "azurerm_api_management" }],
  [/operationalinsights/, { azure: "azurerm_log_analytics_workspace" }],
  [/insights[-_]component|app[-_]insights/, { azure: "azurerm_application_insights" }],
  [/(^|[-_])sql([-_]|$)|sql[-_]server/, { azure: "azurerm_mssql_server", gcp: "google_sql_database_instance", aws: "aws_db_instance" }],
  [/(^|[-_])(adb|autonomous)/, { oci: "oci_database_autonomous_database" }],
  [/mysql/, { oci: "oci_mysql_mysql_db_system", azure: "azurerm_mysql_flexible_server", gcp: "google_sql_database_instance", aws: "aws_db_instance" }],
  [/memorystore/, { gcp: "google_redis_instance" }],
  [/(^|[-_])drg([-_]|$)/, { oci: "oci_core_drg" }],
  [/(^|[-_])mig([-_]|$)|instance[-_]group/, { gcp: "google_compute_region_instance_group_manager" }],
  [/instance[-_]template/, { gcp: "google_compute_instance_template" }],
  [/(^|[-_])address([-_]|$)/, { gcp: "google_compute_address" }],
  [/streaming|stream[-_]pool/, { oci: "oci_streaming_stream" }],
  [/security[-_]?group|firewall|nsg/, { aws: "aws_security_group", gcp: "google_compute_firewall", azure: "azurerm_network_security_group", oci: "oci_core_network_security_group" }],
  [/(^|[-_])(vpc|network|networking|vnet|vcn)([-_]|$)/, { aws: "aws_vpc", gcp: "google_compute_network", azure: "azurerm_virtual_network", oci: "oci_core_vcn" }],
  [/eks|gke|aks|oke|kubernetes/, { aws: "aws_eks_cluster", gcp: "google_container_cluster", azure: "azurerm_kubernetes_cluster", oci: "oci_containerengine_cluster" }],
  [/aurora|rds|sql[-_]?db|postgres|mysql|database|(^|[-_])db([-_]|$)/, { aws: "aws_db_instance", gcp: "google_sql_database_instance", azure: "azurerm_postgresql_flexible_server", oci: "oci_database_autonomous_database" }],
  [/dynamodb/, { aws: "aws_dynamodb_table" }],
  [/cosmos/, { azure: "azurerm_cosmosdb_account" }],
  [/firestore/, { gcp: "google_firestore_database" }],
  [/elasticache|redis|memcache|memory[-_]?db/, { aws: "aws_elasticache_replication_group", gcp: "google_redis_instance", azure: "azurerm_redis_cache" }],
  [/cloud[-_]?run/, { gcp: "google_cloud_run_v2_service" }],
  [/lambda|function/, { aws: "aws_lambda_function", gcp: "google_cloudfunctions2_function", azure: "azurerm_linux_function_app", oci: "oci_functions_function" }],
  [/ecs/, { aws: "aws_ecs_service" }],
  [/ecr|registry|artifact/, { aws: "aws_ecr_repository", gcp: "google_artifact_registry_repository", azure: "azurerm_container_registry", oci: "oci_artifacts_container_repository" }],
  [/cloudfront|cdn|front[-_]?door/, { aws: "aws_cloudfront_distribution", azure: "azurerm_cdn_frontdoor_profile", gcp: "google_compute_backend_bucket" }],
  [/alb|elb|load[-_]?balanc|(^|[-_])lb([-_]|$)/, { aws: "aws_lb", gcp: "google_compute_global_forwarding_rule", azure: "azurerm_lb", oci: "oci_load_balancer_load_balancer" }],
  [/api[-_]?gateway|apim|api[-_]management/, { aws: "aws_apigatewayv2_api", gcp: "google_api_gateway_gateway", azure: "azurerm_api_management", oci: "oci_apigateway_gateway" }],
  [/waf/, { aws: "aws_wafv2_web_acl", azure: "azurerm_web_application_firewall_policy" }],
  [/acm|certificate/, { aws: "aws_acm_certificate" }],
  [/route53|dns/, { aws: "aws_route53_zone", gcp: "google_dns_managed_zone", azure: "azurerm_dns_zone", oci: "oci_dns_zone" }],
  [/sqs|queue|service[-_]?bus/, { aws: "aws_sqs_queue", azure: "azurerm_servicebus_namespace", oci: "oci_queue_queue" }],
  [/sns|pub[-_]?sub|topic|notification/, { aws: "aws_sns_topic", gcp: "google_pubsub_topic", azure: "azurerm_eventgrid_topic", oci: "oci_ons_notification_topic" }],
  [/eventbridge|event[-_]?grid|scheduler/, { aws: "aws_cloudwatch_event_rule", azure: "azurerm_eventgrid_topic", gcp: "google_cloud_scheduler_job" }],
  [/step[-_]?functions|sfn|workflows?/, { aws: "aws_sfn_state_machine", gcp: "google_workflows_workflow", azure: "azurerm_logic_app_workflow" }],
  [/s3|bucket|storage|object[-_]?store/, { aws: "aws_s3_bucket", gcp: "google_storage_bucket", azure: "azurerm_storage_account", oci: "oci_objectstorage_bucket" }],
  [/efs|filestore|file[-_]?share/, { aws: "aws_efs_file_system", gcp: "google_filestore_instance", azure: "azurerm_storage_share", oci: "oci_file_storage_file_system" }],
  [/kinesis/, { aws: "aws_kinesis_stream" }],
  [/msk|kafka|event[-_]?hub/, { aws: "aws_msk_cluster", azure: "azurerm_eventhub_namespace" }],
  [/opensearch|elasticsearch/, { aws: "aws_opensearch_domain" }],
  [/bigquery/, { gcp: "google_bigquery_dataset" }],
  [/cognito/, { aws: "aws_cognito_user_pool" }],
  [/secret/, { aws: "aws_secretsmanager_secret", gcp: "google_secret_manager_secret", azure: "azurerm_key_vault", oci: "oci_vault_secret" }],
  [/kms|key[-_]?vault|vault/, { aws: "aws_kms_key", gcp: "google_kms_key_ring", azure: "azurerm_key_vault", oci: "oci_kms_vault" }],
  [/iam|role|identity|oidc|service[-_]?account/, { aws: "aws_iam_role", gcp: "google_service_account", azure: "azurerm_user_assigned_identity", oci: "oci_identity_policy" }],
  [/cloudwatch|monitoring|alarm|logs?([-_]|$)|log[-_]?analytics/, { aws: "aws_cloudwatch_metric_alarm", gcp: "google_monitoring_alert_policy", azure: "azurerm_log_analytics_workspace" }],
  [/ec2|instance|(^|[-_])vm([-_]|$)|compute|autoscaling|asg/, { aws: "aws_instance", gcp: "google_compute_instance", azure: "azurerm_linux_virtual_machine", oci: "oci_core_instance" }],
]

/** The resource type a module stands for, from its name ("terraform-aws-modules/rds/aws" → aws_db_instance). */
export function moduleServiceType(source: string, provider: ArchProvider | undefined): string | undefined {
  if (!provider) return undefined
  const name = moduleNameOf(source).toLowerCase()
  for (const [re, types] of MODULE_SERVICES) if (re.test(name) && types[provider]) return types[provider]
  return undefined
}

// Wire contract mirrored from backend/internal/model/model.go. Change both together.

export type Cloud = "aws" | "azure" | "gcp" | "oci"
export const ALL_CLOUDS: Cloud[] = ["aws", "azure", "gcp", "oci"]

export type NodeKind = "resource" | "data" | "module"

/** One HCL expression. `expr` is canonical HCL source; `literal` is its JSON form when constant. */
export interface Value {
  expr?: string
  literal?: unknown
}

export interface Attr {
  name: string
  value: Value
  comments?: Comments
  /** Items of `workflow.locals` / `workflow.terraform` only: the source block (BlockPart) they are written in. */
  file?: string
  order?: number
}

export interface Block {
  type: string
  labels?: string[]
  body: Body
  /** `workflow.other` blocks: see WorkflowNode.file/order. Items of `workflow.terraform`: their source block (BlockPart). */
  file?: string
  order?: number
}

/**
 * One imported `locals` or `terraform` block. Export writes one block per part with the items whose
 * file/order match; items without a part go to the recommended layout.
 */
export interface BlockPart {
  file: string
  order?: number
  comments?: Comments
}

/** `comments` belong to the block that owns the body (resource, variable, nested block, ...). */
export interface Body {
  attrs: Attr[]
  blocks: Block[]
  comments?: Comments
}

/**
 * Source comments kept verbatim with their markers ("# x", "// y", "/* z *\/") so export writes
 * them back. In lead/tail/after "" is a blank line between comment groups. Keep them when editing.
 */
export interface Comments {
  /** Comment lines directly above (for the first block of a file, the file header). */
  lead?: string[]
  /** Same-line comment: after an attribute's value, or after a block's "{". */
  line?: string
  /** Block: comment lines after its last attribute/nested block, before "}". */
  tail?: string[]
  /** Block: same-line comment after "}". */
  end?: string
  /** Top-level block: comment lines after it at the end of its file. */
  after?: string[]
}

export interface Position {
  x: number
  y: number
}

export interface WorkflowNode {
  id: string
  kind: NodeKind
  type: string
  name: string
  label?: string
  parent?: string
  position: Position
  positioned: boolean
  body: Body
  /**
   * Root .tf file the block is written to: recorded by import (the project keeps its layout) or chosen
   * by the user. Absent: Nimbo's recommended layout (`workflow.nodeFiles` has the resulting file).
   */
  file?: string
  /** 1-based position in `file` as imported; clear it when changing `file`. */
  order?: number
}

export type EdgeKind = "reference" | "depends_on"

export interface WorkflowEdge {
  id: string
  source: string
  target: string
  kind: EdgeKind
  attribute: string
  sourceAttr?: string
}

export interface NamedBlock {
  name: string
  body: Body
  /** See WorkflowNode.file/order. */
  file?: string
  order?: number
}

export interface EnvVar {
  key: string
  value: string
  secret?: boolean
}

export interface Environment {
  name: string
  values: Record<string, Value>
  envVars: EnvVar[]
  /** Root variable file it was imported from ("terraform.tfvars"); exported back there. Absent: environments/<name>.tfvars. */
  file?: string
}

/** GET /workflows/{id}/environments (docker and Kubernetes): environments are files on top of the base. */
export interface EnvironmentsView {
  kind: WorkflowKind
  /** "" = the base. */
  active: string
  environments: EnvironmentInfo[]
  sources: EnvironmentSource[]
  /** How environments map to files for this workflow. */
  note?: string
  /** Docker: what every environment builds on (the compose files, then .env when it exists). */
  baseFiles?: string[]
}

export interface EnvironmentInfo {
  name: string
  active: boolean
  prodLike: boolean
  /** What it renders on top of the base: "overlays/prod", "chart/values-prod.yaml", "compose.prod.yaml", ".env.prod". */
  files: string[]
  /** Docker: the environment's env file variables. */
  variables?: EnvironmentVariable[]
  changes?: EnvironmentChange[]
}

/** A deployable unit and whether it takes environments (Kustomize and Helm do, plain manifests don't). */
export interface EnvironmentSource {
  label: string
  kind: string
  path: string
  supported: boolean
  note?: string
}

export interface EnvironmentVariable {
  key: string
  /** Absent when secret. */
  value?: string
  secret: boolean
  /** The inherited base value (absent: none, or secret). */
  base?: string
  /** The base .env sets it too (secrets included). */
  inBase: boolean
  /** Set in this environment's own file. */
  overridden: boolean
  file: string
}

export interface EnvironmentChange {
  target: string
  field: string
  base: string
  value: string
}

/** GET /workflows/{id}/environments/compare: every environment rendered on its own, per workload/service. */
export interface EnvCompare {
  /** The base (name "") first. */
  columns: { name: string; label: string }[]
  groups: EnvCompareGroup[]
  /** Render errors by environment ("" = base). */
  errors?: Record<string, string>
}

export interface EnvCompareGroup {
  id: string
  label: string
  kind: string
  rows: EnvCompareRow[]
}

export interface EnvCompareRow {
  key: string
  label: string
  /** Aligned with EnvCompare.columns ("" = not set / not rendered). */
  values: string[]
  differs: boolean
}

/** GET /workflows/{id}/environments/{env}/settings: the common knobs of one workload/service. */
export interface EnvSettings {
  environment: string
  target: string
  targets: { id: string; label: string; kind: string }[]
  /** Where this environment's edits go. */
  file?: string
  settings: EnvSetting[]
  note?: string
}

export interface EnvSetting {
  /** Opaque: sent back to PATCH …/settings. */
  key: string
  label: string
  group: string
  type: "int" | "string" | "cpu" | "memory"
  /** Rendered in this environment ("" = not set). */
  value: string
  /** Rendered in the base. */
  base?: string
  /** This environment's own file sets it. */
  overridden: boolean
  editable: boolean
  reason?: string
}

export type Severity = "error" | "warning" | "info"

/**
 * A problem found by import, normalization, schema validation, the best-practice checks
 * (codes "bp_*", info/warning only, never block deploys) or a Terraform run.
 */
export interface Diagnostic {
  severity: Severity
  code: string
  message: string
  /** Explanation; for best-practice findings, why it matters. */
  detail?: string
  /** Suggested change (best-practice findings). */
  fix?: string
  /** Documentation of the practice (docker findings). */
  docs?: string
  nodeId?: string
  attribute?: string
  /** Code diagnostics (Terraform syntax errors): the file and 1-based line they point at. */
  file?: string
  line?: number
}

/** Terraform best-practice findings carry codes with this prefix. */
export const BEST_PRACTICE_PREFIX = "bp_"
/** Docker checks carry codes with this prefix; their warnings and info are best practices (errors are problems docker compose refuses). */
export const DOCKER_PREFIX = "dk_"
/** Kubernetes checks carry codes with this prefix; warnings and info are best practices (errors: invalid YAML/schema, render failures). */
export const KUBERNETES_PREFIX = "k8s_"

export function isBestPractice(d: Diagnostic): boolean {
  return (
    d.code.startsWith(BEST_PRACTICE_PREFIX) ||
    ((d.code.startsWith(DOCKER_PREFIX) || d.code.startsWith(KUBERNETES_PREFIX)) && d.severity !== "error")
  )
}

/** Network findings (configuration review) carry codes with this prefix; they never block deploys. */
export const NETWORK_PREFIX = "net_"

export function isNetworkFinding(d: Diagnostic): boolean {
  return d.code.startsWith(NETWORK_PREFIX)
}

export interface Workflow {
  id: string
  name: string
  description?: string
  createdAt: string
  updatedAt: string
  /** Hidden from recents; runs and load tests are refused until restored. */
  archived?: boolean
  /** The Learn challenge this workflow was started from (Challenge tab). */
  challenge?: string
  /** Pixel-art icon, random on creation; glyph "nimbo" is the explicit mascot. Absent only on not-yet-migrated data. */
  icon?: WorkflowIcon
  nodes: WorkflowNode[]
  variables: NamedBlock[]
  outputs: NamedBlock[]
  locals: Attr[]
  /** Comments of the `locals` block of the items without a part (new content). */
  localsComments?: Comments
  /** Imported `locals` blocks. */
  localsBlocks?: BlockPart[]
  providers: NamedBlock[]
  /** Items of every terraform block; `comments` belong to the block of the items without a part. */
  terraform?: Body
  /** Imported `terraform` blocks. */
  terraformBlocks?: BlockPart[]
  other: Block[]
  environments: Environment[]
  activeEnvironment?: string
  /**
   * Directory structure of the export. "single": one root module, environments as variable files (one shared state).
   * "environments": modules/<service> plus one root (and state) per environment under environments/<env>.
   * Absent: automatic, "environments" with two or more environments.
   */
  layout?: WorkflowLayout
  /** Service module name of the environments layout (modules/<service>); absent: the workflow name in snake_case. */
  service?: string
  /**
   * "docker": a Docker Compose workspace (files are the source of truth; `compose` holds the derived graph).
   * "kubernetes": manifests / Kustomize / Helm (files are the source of truth; `kubernetes` holds the derived graph).
   * Absent/"" = Terraform.
   */
  kind?: WorkflowKind
  /** Docker workspaces only. */
  compose?: ComposeModel
  /** Kubernetes workspaces only. */
  kubernetes?: KubernetesModel
  // derived, read-only
  edges: WorkflowEdge[]
  clouds: Cloud[]
  diagnostics: Diagnostic[]
  /** Node ID -> root .tf file the export writes it to (its `file`, or the recommended layout). */
  nodeFiles?: Record<string, string>
}

export type WorkflowLayout = "single" | "environments"

export interface WorkflowSummary {
  id: string
  name: string
  description?: string
  updatedAt: string
  /** Docker workspaces: services. */
  nodeCount: number
  kind?: WorkflowKind
  clouds: Cloud[]
  archived?: boolean
  icon?: WorkflowIcon
}

/** Glyph and tile color IDs (src/components/workflow-icon; backend model.WorkflowIconGlyphs/Colors); "nimbo" and unknown glyphs render as the mascot. */
export interface WorkflowIcon {
  glyph: string
  color: string
}

// ---------------------------------------------------------------- catalog

export interface ProviderInfo {
  cloud: Cloud
  name: string
  source: string
  version: string
  label: string
  emulator: string
  emulatorPort: number
  resourceCount: number
  dataCount: number
}

export interface ResourceSummary {
  kind: NodeKind
  type: string
  cloud: Cloud
  service?: string
  category: string
  label: string
  icon: string
  popular?: boolean
  flociSupported: boolean
}

/** cty type JSON: "string" | "number" | "bool" | "dynamic" | ["list", T] | ["set", T] | ["map", T] | ["object", {k: T}] | ["tuple", T[]] */
export type CtyType = string | [string, unknown]

export interface SchemaAttribute {
  name: string
  type: CtyType
  required?: boolean
  optional?: boolean
  computed?: boolean
  sensitive?: boolean
  deprecated?: boolean
  description?: string
  default?: unknown
  enum?: string[]
  basic?: boolean
}

export interface SchemaNestedBlock {
  name: string
  nesting: "single" | "list" | "set" | "map" | "group"
  minItems?: number
  maxItems?: number
  block: SchemaBlock
}

export interface SchemaBlock {
  attributes: SchemaAttribute[]
  blocks: SchemaNestedBlock[]
}

export interface ResourceSchema extends ResourceSummary {
  provider: string
  description?: string
  docsUrl?: string
  block: SchemaBlock
}

// ---------------------------------------------------------------- code intelligence

/** Schemas the code editor completes from: resource and data types plus provider configuration blocks. */
export type SchemaKind = "resource" | "data" | "provider"

/** One entry of the compact index of every resource/data type (GET /catalog/types). */
export interface TypeInfo {
  kind: "resource" | "data"
  type: string
  /** Absent for utility providers (archive, random). */
  cloud?: Cloud
  label: string
  /** Runs in the local emulator (or needs none). */
  local?: boolean
}

export interface FunctionParameter {
  name: string
  type: CtyType
  description?: string
  nullable?: boolean
}

/** A Terraform built-in function (GET /catalog/functions). */
export interface FunctionSignature {
  name: string
  description?: string
  returnType: CtyType
  parameters: FunctionParameter[]
  variadicParameter?: FunctionParameter
}

/** A module `terraform init` downloaded into the sandbox: manifest key ("vpc", "app.db"), source and .tf files. */
export interface DownloadedModule {
  key: string
  source: string
  version?: string
  files: Record<string, string>
}

/** Every Terraform source the editor resolves names in: the workflow's .tf files by path and downloaded modules. */
export interface CodeWorkspace {
  files: Record<string, string>
  modules: DownloadedModule[]
}

/** A problem of a file being edited; lines and columns are 1-based, the end exclusive. */
export interface CodeProblem {
  severity: Severity
  code: string
  message: string
  detail?: string
  line: number
  column: number
  endLine: number
  endColumn: number
  /** k8s_* problems: field path inside the object ("spec.template.spec.containers[0].image"). */
  attribute?: string
  /** The diagnostic's fix text. */
  fix?: string
}

export interface ConnectionRecipe {
  id: string
  label: string
  description?: string
  sourceType: string
  targetType: string
  curated: boolean
}

export interface Template {
  id: string
  name: string
  description: string
  /** "" for Docker templates. */
  cloud: Cloud | ""
  kind?: WorkflowKind
  difficulty: "beginner" | "intermediate" | "advanced"
  tags: string[]
  icon?: WorkflowIcon
  /** "edge-patterns" groups the template in the New workflow dialog; absent for the general starters. */
  category?: TemplateCategory
  /** The edge pattern an "edge-patterns" template shows. */
  pattern?: EdgePattern
}

export type TemplateCategory = "edge-patterns"
export type EdgePattern = "reverse-proxy" | "api-gateway" | "load-balancer"

// ---------------------------------------------------------------- sandbox & runs

export type SandboxState = "absent" | "starting" | "running" | "stopped" | "error"

export interface EmulatorStatus {
  cloud: Cloud
  container: string
  image: string
  port: number
  hostPort: number
  url: string
  state: SandboxState
  version?: string
  services?: Record<string, string>
}

export interface SandboxStatus {
  workflowId: string
  state: SandboxState
  network?: string
  emulators: EmulatorStatus[]
  message?: string
}

export type RunAction = "validate" | "plan" | "apply" | "destroy" | ComposeRunAction | KubeClusterRunAction
/** Kubernetes workspaces: lifecycle of the workflow's local cluster (up/down/restart are shared with docker). */
export type KubeClusterRunAction = "cluster-stop" | "cluster-start" | "cluster-delete"
export type RunStatus = "queued" | "running" | "succeeded" | "failed" | "canceled"

export interface LogLine {
  t: string
  stream: "stdout" | "stderr" | "system"
  text: string
}

export interface PlanSummary {
  add: number
  change: number
  destroy: number
}

export interface Run {
  id: string
  workflowId: string
  action: RunAction
  environment?: string
  status: RunStatus
  createdAt: string
  startedAt?: string
  endedAt?: string
  exitCode: number
  plan?: PlanSummary
  error?: string
  log?: LogLine[]
  /** Kubernetes restart: the workload node id. */
  workload?: string
  removeVolumes?: boolean
  deleteCluster?: boolean
}

export interface DeployedResource {
  address: string
  mode: "managed" | "data"
  type: string
  name: string
  index?: unknown
  nodeId?: string
  values: Record<string, unknown>
}

export interface DeployedState {
  workflowId: string
  resources: DeployedResource[]
  outputs: Record<string, unknown>
  updatedAt: string
}

// ---------------------------------------------------------------- endpoints (how to use what is deployed)

export type AccessKind =
  | "http-api"
  | "function-url"
  | "function"
  | "queue"
  | "topic"
  | "bucket"
  | "table"
  | "stream"
  | "event-bus"
  | "state-machine"
  | "secret"
  | "parameter"
  | "user-pool"
  | "database"
  | "cache"
  | "web-service"
  | "key"
  | "kubernetes"
  | "instance"
  | "generic"

export interface AccessField {
  label: string
  value: string
  secret?: boolean
  hint?: string
}

/** How SDKs and CLIs reach one running emulator. */
export interface SandboxAccess {
  cloud: Cloud
  url: string
  region?: string
  credentials: AccessField[]
  /** Shell exports pointing tools at the emulator. */
  envSnippet: string
  notes: string[]
}

export interface HTTPEndpoint {
  method: string
  path: string
  url: string
  description?: string
  /** Greedy route (e.g. ANY /{proxy+}): `url` has a sample path the user can change. */
  proxy?: boolean
}

export interface Snippet {
  /** curl | AWS CLI | JavaScript | Python | ... */
  label: string
  /** bash | javascript | python | json */
  language: string
  code: string
}

export type TryInput = "http" | "json" | "text" | "none"

export interface TryAction {
  id: string
  label: string
  description?: string
  input: TryInput
  default?: string
  /** The action changes data in the sandbox. */
  writes?: boolean
}

export interface ResourceAccess {
  nodeId?: string
  address: string
  type: string
  label: string
  kind: AccessKind
  /** One plain-language sentence. */
  summary: string
  fields: AccessField[]
  endpoints?: HTTPEndpoint[]
  snippets: Snippet[]
  actions: TryAction[]
  warning?: string
  /** Plumbing (roles, permissions, triggers, routing, settings) listed apart from what you call. */
  supporting?: boolean
}

// ---------------------------------------------------------------------------
// Network checks (configuration review and reachability analysis)

/** "deployed": values from the sandbox's Terraform state; "design": the workflow's expressions. */
export type NetworkBasis = "deployed" | "design"

/** GET /workflows/{id}/network. Addresses are instance addresses; nodeId is their canvas node. */
export interface NetworkSummary {
  workflowId: string
  basis: NetworkBasis
  basisReason: string
  networks: NetNetwork[]
  subnets: NetSubnet[]
  routeTables: NetRouteTable[]
  gateways: NetGateway[]
  securityGroups: NetSecurityGroup[]
  placements: NetPlacement[]
  /** The "net_*" diagnostics of the same analysis. */
  findings: Diagnostic[]
  counts: NetFindingCounts
  /** What was skipped because its inputs are unknown. */
  notes: string[]
}

export interface NetFindingCounts {
  errors: number
  warnings: number
  info: number
}

/** A VPC (AWS), virtual network (Azure) or VPC network (GCP). */
export interface NetNetwork {
  nodeId?: string
  address: string
  cloud: Cloud
  name: string
  /** Empty when unknown or not applicable (GCP). */
  cidrs: string[]
  id?: string
}

export type SubnetClassification = "public" | "private" | "isolated" | "unknown"

export interface NetSubnet {
  nodeId?: string
  address: string
  name: string
  /** Address of its VPC/VNet. */
  network?: string
  cidr?: string
  /** Availability zone. */
  zone?: string
  classification: SubnetClassification
  /** Plain-language reason for the classification. */
  why: string
  /** Address ("main route table of aws_vpc.main" when implicit). */
  routeTable?: string
  nacl?: string
  mapPublicIp?: boolean
  id?: string
}

export interface NetRouteTable {
  nodeId?: string
  address: string
  network?: string
  main?: boolean
  subnets: string[]
  routes: NetRoute[]
}

/** targetKind: local | internet-gateway | egress-only-gateway | nat-gateway | vpc-endpoint | peering | transit-gateway | instance | network-interface | vpn-gateway | unknown */
export interface NetRoute {
  destination: string
  targetKind: string
  target?: string
  nodeId?: string
}

/** kind uses the NetRoute.targetKind names. */
export interface NetGateway {
  nodeId?: string
  address: string
  kind: string
  network?: string
  /** NAT gateways. */
  subnet?: string
  /** VPC endpoints: "s3", "dynamodb", ... */
  service?: string
  detail?: string
}

export interface NetSecurityGroup {
  nodeId?: string
  address: string
  network?: string
  ingress: NetRule[]
  egress: NetRule[]
  /** e.g. "Allows 80, 443 from anywhere; all outbound" */
  summary: string
}

/** One allow rule. ports: "80", "1024-65535" or "all"; peer: a CIDR, "anywhere", a security group address or "itself". */
export interface NetRule {
  /** tcp | udp | icmp | all */
  protocol: string
  ports: string
  peer: string
  description?: string
  nodeId?: string
}

export type PlacementKind =
  | "instance"
  | "function"
  | "database"
  | "cache"
  | "load-balancer"
  | "container-service"
  | "kubernetes"
  | "interface"

/** Where a workload sits in the network. */
export interface NetPlacement {
  nodeId?: string
  address: string
  kind: PlacementKind
  network?: string
  subnets: string[]
  zones: string[]
  securityGroups: string[]
  public: boolean
  publicWhy: string
  /** Listening ports when known. */
  ports?: number[]
  /** false: a Lambda outside any VPC. */
  inVpc: boolean
}

/** nodeId: node ID or address. */
export interface NetEndpoint {
  kind: "internet" | "node"
  nodeId?: string
}

export type NetProtocol = "tcp" | "udp" | "icmp"

/** POST /workflows/{id}/network/reachability. port is ignored for icmp; live also runs real connections in the sandbox. */
export interface ReachabilityRequest {
  source: NetEndpoint
  destination: NetEndpoint
  protocol: NetProtocol
  port: number
  live?: boolean
}

export type HopVerdict = "allow" | "deny" | "unknown"

/** layer: endpoint | address | route | gateway | nacl | security-group | listener | target | service */
export interface ReachabilityHop {
  component: string
  layer: string
  nodeId?: string
  verdict: HopVerdict
  reason: string
  /** Live results: "live" when the emulator enforces this layer, "analysis" when only the analyzer checked it. */
  enforcement?: "live" | "analysis"
}

export interface ReachabilityResult {
  reachable: boolean
  verdict: "reachable" | "unreachable" | "unknown"
  basis: NetworkBasis
  summary: string
  path: ReachabilityHop[]
  blockers: string[]
  suggestions: string[]
  live?: LiveCheck
}

export interface LiveCheck {
  probes: LiveProbe[]
  /** Layers the probes exercise in the emulator. */
  verified: string[]
  /** Layers the emulator does not enforce (the analyzer's verdict stands). */
  analysisOnly: string[]
  /** Why nothing could run. */
  unavailable?: string
  note?: string
}

export interface LiveProbe {
  name: string
  command: string
  connected: boolean
  /** "HTTP 200", "connection refused", "timed out", ... */
  result: string
  output?: string
  latencyMs: number
  predicted: boolean
  matches: boolean
}

/** POST /workflows/{id}/network/test-instance: the subnet (node ID or address). */
export interface TestInstanceRequest {
  subnet: string
}

/** GET /workflows/{id}/endpoints; refetch when the deployed state changes. */
export interface EndpointsInfo {
  workflowId: string
  /** Empty while the sandbox is not running. */
  sandbox: SandboxAccess[]
  resources: ResourceAccess[]
  updatedAt: string
}

export interface TryRequest {
  /** Node ID of the resource, or its address when it has no node. */
  nodeId: string
  action: string
  input?: string
  /** http actions: method, full sandbox URL (or path relative to the endpoint) and headers. */
  method?: string
  path?: string
  headers?: Record<string, string>
}

export interface TryResult {
  ok: boolean
  /** e.g. "200 OK" for HTTP, "sent" / "3 messages" for AWS actions. */
  status?: string
  latencyMs: number
  contentType?: string
  body?: string
  headers?: Record<string, string>
  error?: string
}

// ---------------------------------------------------------------- load testing

export type LoadTargetKind = "http" | "aws" | "sql" | "redis" | "mongo"

export interface HTTPTarget {
  method: string
  url: string
  headers?: Record<string, string>
  body?: string
}

export interface AWSTarget {
  operation: string
  resource: string
  payload?: string
  key?: string
}

/**
 * With connectionId the target hits that saved connection (a service outside the sandbox):
 * HTTP urls may be paths relative to its baseUrl; AWS operations hit the real account.
 * sql/redis/mongo targets always need connectionId.
 */
export interface LoadTarget {
  kind: LoadTargetKind
  nodeId?: string
  connectionId?: string
  label: string
  http?: HTTPTarget
  aws?: AWSTarget
  sql?: SQLTarget
  redis?: RedisTarget
  mongo?: MongoTarget
}

/** Args are templates bound as driver parameters ($1.. / ?), never interpolated into query. */
export interface SQLTarget {
  query: string
  args?: string[]
  mode: "query" | "exec"
}

/** e.g. "SET bench:{{seq}} {{uuid}}"; split on whitespace, double quotes group. */
export interface RedisTarget {
  command: string
}

export interface MongoTarget {
  database: string
  collection?: string
  operation: "insertOne" | "findOne" | "find" | "updateOne" | "deleteOne" | "runCommand"
  /** JSON template: document (insertOne), filter (find*, updateOne, deleteOne), command (runCommand). */
  document?: string
  update?: string
}

export type LoadMode = "rps" | "concurrency"

/** Ramps linearly from the previous stage's target (0 for the first) over durationSec; durationSec 0 is an instant jump. */
export interface LoadStage {
  durationSec: number
  target: number
}

export interface LoadProfile {
  preset: "smoke" | "load" | "stress" | "spike" | "soak" | "autoscale" | "custom"
  label?: string
  description?: string
  mode: LoadMode
  stages: LoadStage[]
}

export interface Threshold {
  metric: "p50_ms" | "p90_ms" | "p95_ms" | "p99_ms" | "max_ms" | "error_rate" | "rps_min"
  op: "<" | "<=" | ">" | ">="
  value: number
}

/** confirmExternal must be true for targets outside the local sandbox, else the API answers 428. */
export interface LoadTestSpec {
  name: string
  target: LoadTarget
  profile: LoadProfile
  thresholds: Threshold[]
  timeoutMs: number
  maxWorkers?: number
  confirmExternal?: boolean
  /** Docker/Kubernetes workflow runs: a real, reversible incident injected during the run. */
  scenario?: ScenarioSpec
}

export type ScenarioId = "kill_replica" | "dependency_down" | "dependency_latency" | "cache_flush" | "traffic_spike"
export type ScenarioVerdict = "survived" | "degraded" | "failed"

/** target: graph node id ("service:db", "Deployment/ns/web"); unused by traffic_spike. atSec: seconds after load starts. */
export interface ScenarioSpec {
  id: ScenarioId
  target: string
  atSec: number
  durationSec: number
  /** dependency_latency: delayMs, jitterMs, lossPct; traffic_spike: factor (numbers as strings). */
  params?: Record<string, string>
}

export interface ScenarioCheck {
  name: string
  passed: boolean
  detail: string
}

/** Times share LoadSample.t's axis. verdict is "" while the run is in progress. */
export interface ScenarioResult {
  id: ScenarioId
  target: string
  targetName?: string
  injectedAtSec: number
  revertedAtSec?: number
  recoveredAtSec?: number
  verdict: ScenarioVerdict | ""
  message: string
  checks: ScenarioCheck[]
  detail?: string
}

export interface ScenarioTarget {
  id: string
  name: string
  detail: string
}

export interface ScenarioParam {
  name: string
  label: string
  default: number
  min: number
  max: number
  unit?: string
}

export interface ScenarioOption {
  id: ScenarioId
  title: string
  description: string
  needsTarget: boolean
  targets: ScenarioTarget[]
  params?: ScenarioParam[]
  defaultDurationSec: number
}

/** GET /workflows/{id}/scenarios. kind "" for Terraform workflows. */
export interface ScenarioCatalog {
  kind: "docker" | "kubernetes" | ""
  environment?: string
  scenarios: ScenarioOption[]
  unavailable: { id: ScenarioId; title: string; reason: string }[]
}

export interface LatencyStats {
  min: number
  mean: number
  stddev: number
  p50: number
  p90: number
  p95: number
  p99: number
  max: number
}

export interface LoadSample {
  t: number
  target: number
  rps: number
  errors: number
  inFlight: number
  p50: number
  p95: number
  p99: number
  max: number
}

export interface LoadSummary {
  requests: number
  errors: number
  errorRate: number
  rps: number
  peakRps: number
  bytesIn: number
  latency: LatencyStats
  statusCodes: Record<string, number>
  errorKinds: Record<string, number>
  stability: number
  breakingPoint?: number
}

export interface ThresholdResult extends Threshold {
  actual: number
  passed: boolean
}

export type LoadTestStatus = "running" | "passed" | "failed" | "stopped" | "error"

/** Exactly one of workflowId / connectionId is set (the owner of the run). */
export interface LoadTestRun {
  id: string
  workflowId?: string
  connectionId?: string
  /** Kubernetes workflows: the active environment when the run started ("" / absent: base). */
  environment?: string
  /** Traffic left the local sandbox. */
  external?: boolean
  spec: LoadTestSpec
  status: LoadTestStatus
  startedAt: string
  endedAt?: string
  timeline: LoadSample[]
  summary?: LoadSummary
  thresholds?: ThresholdResult[]
  error?: string
  /** Runs against a docker or Kubernetes workflow's service: container usage during the run (set once it ends). */
  resources?: LoadResources
  /** The injected incident: times, then (once the run ends) the verdict and its checks. */
  scenario?: ScenarioResult
}

export type LoadVerdictKind = "cpu-bound" | "single-core" | "memory-bound" | "oom" | "restarted" | "ok"

export interface LoadServiceResources {
  service: string
  nodeId: string
  /** The service the load test targeted. */
  target?: boolean
  cpuLimit: number
  cpuLimited: boolean
  memLimit: number
  memLimited: boolean
  startupMs?: number
  /** t aligned with LoadTestRun.timeline[].t (0 = baseline). */
  timeline: { t: number; cpuPercent: number; memBytes: number; containers: number }[]
  peakCpuPercent: number
  /** Ratio: 1 = the CPU limit is saturated. */
  peakCpuOfLimit: number
  peakMemBytes: number
  /** Ratio: 1 = the memory limit is reached. */
  peakMemOfLimit: number
  restarts?: number
  oomKilled?: boolean
}

export interface LoadVerdict {
  service: string
  kind: LoadVerdictKind
  bottleneck: boolean
  message: string
  atRps?: number
  atT?: number
}

/** Load test ↔ container usage correlation. */
export interface LoadResources {
  /** Targeted service. */
  service?: string
  services: LoadServiceResources[]
  verdicts: LoadVerdict[]
  /** Headline, e.g. "api reached 98% of its 0.25 CPU limit at 80 req/s — CPU-bound; raise limits or add replicas". */
  verdict: string
  note?: string
  /** Kubernetes runs: replicas and HPA status of the workloads in scope, and the autoscaling verdict. */
  scaling?: LoadScaling
}

export type ScalingKind = "scaled" | "late" | "maxed" | "no_hpa" | "no_metrics" | "not_needed" | "too_short"

/** One second of a workload's replicas; t shares LoadSample.t's axis. */
export interface ScalingPoint {
  t: number
  /** spec.replicas: what the HPA asked for. */
  desired: number
  ready: number
  /** HPA's average CPU utilization (% of requests, refreshed ~every 15 s). */
  cpuUtilization?: number
}

export interface HPAPolicy {
  name: string
  min: number
  max: number
  /** Average CPU utilization target (% of requests); absent when the HPA does not scale on CPU. */
  targetCpu?: number
}

export interface WorkloadScaling {
  nodeId: string
  workload: string
  target?: boolean
  hpa?: HPAPolicy
  timeline: ScalingPoint[]
  kind: ScalingKind
  message: string
  scaleUpAt?: number
  readyAt?: number
  degradedAt?: number
  degradedFor?: number
  peakReplicas: number
  peakCpuUtilization?: number
}

export interface LoadScaling {
  workloads: WorkloadScaling[]
  /** Headline sentence. */
  verdict: string
  note?: string
}

// ---------------------------------------------------------------- capacity planning

export interface CapacityRequest {
  /** Peak requests per second to plan for. */
  targetRps: number
  /** Optional p95 objective (ms): caps each replica at the highest rate served within it. */
  p95Ms?: number
  /** "" / absent: the active environment. */
  environment?: string
  /** Absent: the newest finished run of the environment with container usage. */
  runId?: string
}

export type CapacityConfidence = "high" | "medium" | "low"
/** app scales out; dependency (database/cache/broker) is projected; idle did not follow the traffic. */
export type CapacityRole = "app" | "dependency" | "idle"

export interface CapacityApply {
  target: string
  values: Record<string, string>
  /** "Replicas: 1 → 3". */
  changes: string[]
  skipped?: string[]
}

export interface CapacityService {
  nodeId: string
  service: string
  role: CapacityRole
  target?: boolean
  measuredReplicas: number
  measuredRps: number
  /** Cores per replica at idle / over the busiest healthy seconds. */
  idleCpu: number
  peakCpu: number
  cpuPerRps: number
  peakMemBytes: number
  currentCpuLimit?: number
  currentMemLimit?: number
  currentReplicas?: number
  currentHpa?: HPAPolicy
  cpuRequest: string
  cpuLimit: string
  memRequest: string
  memLimit: string
  cpuCeiling: number
  perReplicaRps?: number
  replicasRaw: number
  replicas: number
  /** Kubernetes apps only. */
  hpa?: HPAPolicy
  projectedCpu: number
  totalCpu: number
  totalMemGiB: number
  /** What bounds a replica's rate. */
  limit?: "cpu" | "latency" | "none"
  note: string
  caveat?: string
  apply?: CapacityApply
}

export interface NodeOption {
  shape: string
  arch: "arm64" | "x86_64"
  vcpu: number
  memGiB: number
  allocCpu: number
  allocMemGiB: number
  hourlyUsd: number
  nodes: number
  monthlyUsd: number
  /** Fractions of the nodes' allocatable capacity requested. */
  cpuUsed: number
  memUsed: number
}

export interface CloudEstimate {
  cloud: "aws" | "azure" | "gcp" | "oci"
  region: string
  recommended: NodeOption
  /** Every shape that fits, cheapest first. */
  options: NodeOption[]
}

export interface CapacityPlan {
  workflowId: string
  kind: "docker" | "kubernetes"
  environment?: string
  createdAt: string
  run: {
    id: string
    name: string
    startedAt: string
    durationSec: number
    healthySec: number
    peakRps: number
    breakingPoint?: number
    stability: number
    errorRate: number
    p95Ms: number
  }
  targetRps: number
  p95Ms?: number
  /** Highest rate served before the breaking point. */
  measuredRps: number
  /** targetRps / measuredRps. */
  extrapolation: number
  confidence: CapacityConfidence
  confidenceReasons: string[]
  cpuCeiling: number
  headroomRule: string
  services: CapacityService[]
  totalCpu: number
  totalMemGiB: number
  clouds: CloudEstimate[]
  prices: { asOf: string; regions: Record<string, string>; note: string; sources: string[] }
  assumptions: string[]
  caveats: string[]
  applicable: boolean
  applyNote?: string
}

// ---------------------------------------------------------------- connections

export type ConnectionKind = "http" | "postgres" | "mysql" | "redis" | "mongodb" | "aws"

export interface HTTPAuth {
  type: "none" | "bearer" | "basic" | "api_key"
  /** secret */
  token?: string
  username?: string
  /** secret */
  password?: string
  /** api_key header name, default X-API-Key */
  header?: string
}

export type ConnectionSecret = "password" | "uri" | "auth.token" | "auth.password"

/**
 * A saved, already-deployed service. Secrets are never returned: they come back blank and are
 * listed in secretsSet. On update a blank secret keeps the stored value; clearSecrets removes them.
 */
export interface Connection {
  id: string
  name: string
  kind: ConnectionKind
  description?: string
  // http
  baseUrl?: string
  headers?: Record<string, string>
  auth?: HTTPAuth
  insecureSkipVerify?: boolean
  // databases: uri (secret) or discrete fields
  uri?: string
  host?: string
  port?: number
  username?: string
  password?: string
  /** redis: DB number as string */
  database?: string
  tls?: boolean
  // aws
  profile?: string
  region?: string
  secretsSet?: ConnectionSecret[]
  clearSecrets?: ConnectionSecret[]
  createdAt: string
  updatedAt: string
}

export interface ConnectionCheck {
  ok: boolean
  latencyMs: number
  message: string
  detail?: string
}

// ---------------------------------------------------------------- setup (onboarding)

export type SetupCheckStatus = "ok" | "missing" | "warning" | "error"

/** One way to resolve a failed check. */
export interface SetupFix {
  kind: "link" | "action" | "command"
  label: string
  url?: string
  /** id for POST /setup/actions/{action} */
  action?: string
  /** Shell command the user can copy (for "action" fixes: the manual equivalent). */
  command?: string
}

export interface SetupCheck {
  /** wsl (Windows only) | docker-cli | docker-daemon | docker-permissions (Linux only) | images | docker-socket | docker-memory | disk | internet | docker-compose (compose v2 plugin, for Docker workspaces) */
  id: string
  title: string
  status: SetupCheckStatus
  required: boolean
  /** One line, e.g. "Docker 29.8 via OrbStack". */
  summary: string
  /** Beginner explanation. */
  detail: string
  fixes?: SetupFix[]
}

/** GET /setup */
export interface SetupStatus {
  /** Every required check is ok. */
  ready: boolean
  platform: "darwin" | "linux" | "windows"
  /** Go GOARCH: "arm64" | "amd64" | … */
  arch: string
  /** e.g. "macOS 26.0", "Ubuntu 24.04 LTS", "Windows 11" */
  osName: string
  /** Linux os-release ID (ubuntu, fedora, arch, …); "" elsewhere. */
  distro: string
  packageManager: "brew" | "apt" | "dnf" | "pacman" | "zypper" | "winget" | "choco" | ""
  checks: SetupCheck[]
}

/** Data of a setup.progress event. current/total count the action's units (images for pull-images). */
export interface SetupProgress {
  action: string
  image?: string
  step: "start" | "pull" | "skip" | "image-done" | "waiting" | "done"
  current: number
  total: number
  message: string
  /** Layer progress of `image` while step is "pull". */
  layersDone?: number
  layersTotal?: number
  /** Last event of the action. */
  done: boolean
  error?: string
}

/** A new workflow; origin "mcp" when an AI agent created it through the MCP server. */
export interface WorkflowCreated {
  id: string
  name: string
  kind?: WorkflowKind
  origin: "mcp" | "app"
}

// ---------------------------------------------------------------- events

export type ServerEvent =
  | { type: "workflow.updated"; workflowId: string; data: Workflow }
  | { type: "workflow.deleted"; workflowId: string; data: { id: string } }
  | { type: "workflow.created"; workflowId?: string; data: WorkflowCreated }
  | { type: "sandbox.status"; workflowId: string; data: SandboxStatus }
  | { type: "run.status"; workflowId: string; data: Run }
  | { type: "run.log"; workflowId: string; data: { runId: string; line: LogLine } }
  | { type: "state.updated"; workflowId: string; data: DeployedState }
  | { type: "loadtest.sample"; workflowId: string; data: { runId: string; sample: LoadSample } }
  | { type: "loadtest.status"; workflowId: string; data: LoadTestRun }
  /** Docker workspaces: container state changed (and after every run). */
  | { type: "compose.status"; workflowId: string; data: ComposeStatus }
  /** Docker workspaces: one `docker stats` sample per service (about 1/s while containers run). */
  | { type: "compose.stats"; workflowId: string; data: ComposeStatsEvent }
  /** Docker workspaces: metrics of a build/up run that built at least one image. */
  | { type: "compose.build"; workflowId: string; data: ComposeBuild }
  /** Kubernetes workspaces: cluster, rollout, pod or event change (and after every run). */
  | { type: "kube.status"; workflowId: string; data: KubeStatus }
  /** Kubernetes workspaces: one usage sample per workload (`service` = workload node id), about 1/s while pods run. */
  | { type: "kube.stats"; workflowId: string; data: ComposeStatsEvent }
  /** Global (no workflowId): a saved connection was created, updated or deleted. */
  | { type: "connections.updated"; workflowId?: string; data: { id: string } }
  /** Global (no workflowId): progress of a POST /setup/actions/{action}. */
  | { type: "setup.progress"; workflowId?: string; data: SetupProgress }

export interface HealthInfo {
  version: string
  home: string
  docker: boolean
  dockerError?: string
}

/** App preferences (GET/PUT /settings). Sandbox fields apply to sandboxes started afterwards. */
export interface Settings {
  defaultMode: "guided" | "pro"
  /** docker CLI binary (name on PATH or absolute path) */
  docker: string
  /** Emulator image per cloud. Sending "" restores the default. */
  images: Record<Cloud, string>
  terraformImage: string
  /** Region of new AWS provider blocks and of Terraform runs in the sandbox. */
  defaultAWSRegion: string
  /** Best-practice checks (bp_* Terraform, dk_* Docker advice): on by default, off as a whole or per rule; opt-in rules on demand. */
  bestPractices: BestPracticeSettings
}

export interface BestPracticeSettings {
  disabled?: boolean
  /** Rule codes turned off, e.g. "bp_missing_tags". */
  disabledRules: string[]
  /** Opt-in rules (defaultOff) turned on, e.g. "dk_hardening". */
  enabledRules?: string[]
}

/** One best-practice check (GET /best-practices/rules). */
export interface BestPracticeRule {
  code: string
  title: string
  category:
    | "inputs"
    | "versions"
    | "hardcoding"
    | "tagging"
    | "naming"
    | "security"
    | "structure"
    | "docker-build"
    | "docker-compose"
    | "docker-security"
    | "kubernetes"
    | "kubernetes-reliability"
    | "kubernetes-security"
    | "kubernetes-networking"
    | "kubernetes-hygiene"
  /** Default severity (a few findings of a rule may be lower). */
  severity: Severity
  description: string
  /** Documentation of the practice. */
  docs?: string
  /** Opt-in: only runs when listed in enabledRules. */
  defaultOff?: boolean
}

/** An AI agent app Nimbo can register its MCP server with (GET /agents). */
export interface AgentClient {
  id: string
  name: string
  /** App, CLI or config folder found on this machine. */
  detected: boolean
  /** A "nimbo" MCP server entry exists in its config. */
  configured: boolean
  configPath: string
  /** Manual setup: config fragment or command. */
  snippet: string
  note?: string
}

/** GET /mcp/command: how agents launch Nimbo's MCP server. */
export interface McpCommand {
  command: string
  args: string[]
  /** Why one-click install is unavailable (temporary build). */
  problem?: string
}

export interface McpToolInfo {
  name: string
  title: string
  description: string
  group: string
}

export interface McpPromptInfo {
  name: string
  title: string
  description: string
  arguments: string[] | null
}

/** GET /mcp/catalog: everything agents can do through Nimbo. */
export interface McpCatalog {
  tools: McpToolInfo[]
  prompts: McpPromptInfo[]
}

export interface ApiErrorBody {
  error: string
  diagnostics?: Diagnostic[]
  /** Machine-readable error code, e.g. "confirmation_required" (HTTP 428). */
  code?: string
}

// ---------------------------------------------------------------------------
// Folder import

/** One importable Terraform root module found by POST /import/scan. */
export interface ImportProject {
  /** Slash path relative to the scanned folder; "." is the folder itself. */
  path: string
  /** Suggested workflow name. */
  name: string
  resources: number
  dataSources: number
  /** Module calls in the project root. */
  modules: number
  providers: string[]
  clouds: Cloud[]
  /** Relative to `path`; each becomes an environment. */
  tfvarsFiles: string[]
  /** A remote backend is configured; the sandbox runs with local state instead. */
  hasBackend: boolean
  diagnostics: Diagnostic[]
  /**
   * "environments": environments/<env>/ roots that only call modules/<service>, imported as ONE workflow with one
   * environment per root (`environments`); `path` is the folder holding environments/ and modules/.
   */
  layout: WorkflowLayout
  environments?: string[]
  /** "docker": a compose project (or a lone Dockerfile); "kubernetes": manifests, a Kustomize tree or a Helm chart. */
  kind?: WorkflowKind
  /** Compose files in -f order; empty = a lone Dockerfile (compose.yaml is generated). */
  composeFiles?: string[]
  services?: number
  dockerfiles?: string[]
  /** Kubernetes projects: what would be deployed (manifests dirs, kustomizations, charts). */
  kubeSources?: KubeSource[]
  /** Kubernetes projects: objects rendered without an environment. */
  objects?: number
  /**
   * Folders and files outside `path` the compose files reference (build contexts, Dockerfiles, env_file, bind mounts,
   * config/secret files, include/extends files), imported too: the workflow keeps the layout from their common
   * ancestor so the compose files stay byte-identical. Paths relative to `path` ("../services/api").
   */
  outside?: ImportOutside[]
  /** A group of sibling Dockerfile folders ("dockerfiles:<parent>"): the folders, one service each of a generated compose.yaml. */
  members?: string[]
  /** Size of what a docker workflow imports. */
  bytes?: number
  fileCount?: number
}

export interface ImportOutside {
  path: string
  /** "build context", "Dockerfile", "env_file", "bind mount", "config", "secret", "compose file" */
  kinds: string[]
  dir: boolean
  bytes: number
  files: number
}

export interface ImportScan {
  /** Absolute folder (dir input) or the uploaded folder's name (files input). */
  root: string
  projects: ImportProject[]
  /** Directories not offered as projects, with the reason ("modules/net: local module used by envs/dev"). */
  skipped: string[]
}

/** Source of a scan/import: a folder on the daemon's machine or uploaded files (relative path → content). */
export type ImportSource = { dir: string; files?: never } | { files: Record<string, string>; dir?: never }

export type ImportRequest = ImportSource & {
  /** Scan paths to import, one workflow each; omitted = the folder root only. */
  projects?: string[]
  /** Workflow name when exactly one project is imported. */
  name?: string
  /** Workflow name per project path. */
  names?: Record<string, string>
}

/** Read-only content of a module call (GET /workflows/{id}/modules/{nodeId}). */
export interface ModuleView {
  source: string
  local: boolean
  /** Local modules inside the workflow files: their folder, e.g. "modules/network". */
  dir?: string
  /** Local modules: the module as a workflow (nodes unpositioned). */
  workflow?: Workflow
  /** Registry/remote modules or unreadable sources: why there is no diagram. */
  message?: string
}

/** Every module call of a workflow with its content, nested calls included (GET /workflows/{id}/modules). */
export interface ModuleTree {
  /** Call path ("module.app", "module.app.module.db") → where its content comes from. */
  calls: Record<string, ModuleCallSource>
  /** Module directories read, as workflows (nodes unpositioned), by ModuleCallSource.dir. */
  modules: Record<string, Workflow>
}

export interface ModuleCallSource {
  source: string
  version?: string
  local: boolean
  /**
   * Set when the content is readable: a local module in the workflow files ("modules/network") or a
   * registry/remote module `terraform init` downloaded into the sandbox (".terraform/modules/vpc").
   */
  dir?: string
}

/** Body of POST /workflows/{id}/modules/extract (and .../extract/preview). */
export interface ModuleExtractRequest {
  /** Node IDs or addresses to move into the module. */
  nodeIds: string[]
  /** snake_case: module.<name> and modules/<name>. */
  name: string
}

/** One input variable or output of a module. */
export interface ModuleIO {
  name: string
  /** Inputs: Terraform type constraint. */
  type?: string
  description: string
  /** Inputs: the expression the call passes; outputs: the expression inside the module. */
  value: string
  sensitive?: boolean
}

/** A `moved { from = … to = … }` block. */
export interface MovedAddress {
  from: string
  to: string
}

/** What extracting nodes into a local module creates (POST …/modules/extract/preview). */
export interface ModuleExtraction {
  name: string
  /** "./modules/<name>" */
  source: string
  inputs: ModuleIO[]
  outputs: ModuleIO[]
  /** Root moved blocks so deployed resources move into the module instead of being recreated. */
  moved: MovedAddress[]
  /** Provider configurations passed through `providers` (only with non-default aliases). */
  providers: string[]
  /** Root addresses the module call depends on. */
  dependsOn: string[]
  /** Created files, relative to the workflow root. */
  files: string[]
}

/** "scaffold": README.md, .gitignore, terraform.tfvars.example generated next to the Terraform (unless the workflow has its own). */
export type WorkflowFileKind = "terraform" | "tfvars" | "env" | "module" | "scaffold" | "asset" | "compose" | "dockerfile"
export type FileLanguage =
  | "hcl"
  | "json"
  | "javascript"
  | "typescript"
  | "python"
  | "go"
  | "yaml"
  | "markdown"
  | "shell"
  | "dockerfile"
  | "java"
  | "kotlin"
  | "csharp"
  | "rust"
  | "php"
  | "ruby"
  | "sql"
  | "toml"
  | "ini"
  | "properties"
  | "xml"
  | "html"
  | "css"
  | "nginx"
  | "makefile"
  | "protobuf"
  | "text"

/**
 * One file an export writes (GET /workflows/{id}/files). Generated files are rendered from the workflow; the
 * others are assets stored with it. Editable files (code and assets) change through PUT /terraform.
 */
export interface WorkflowFile {
  /** Slash-separated, relative to the export root. */
  path: string
  size: number
  kind: WorkflowFileKind
  generated: boolean
  editable: boolean
  /** Why the file is not editable (absent when it is). */
  readOnlyReason?: string
  /** Can be renamed, moved or deleted from the Code tab. */
  movable: boolean
  binary: boolean
  language: FileLanguage
}

export interface WorkflowFileContent extends WorkflowFile {
  content: string
}

/**
 * PUT /workflows/{id}/terraform with changes: moves run first, then each change sets a file's content (created when
 * new) or deletes it (null). Paths are export paths. Terraform code is re-imported, every other file is stored.
 */
export interface FileChangesRequest {
  changes?: Record<string, string | null>
  moves?: { from: string; to: string }[]
  /** Format the HCL written (terraform fmt). */
  format?: boolean
}

/** A new local module, not saved yet (POST /workflows/{id}/modules/scaffold). */
export interface ModuleScaffold {
  name: string
  /** modules/<name> */
  dir: string
  /** Export path → content: versions.tf, variables.tf, outputs.tf, main.tf, README.md. */
  files: Record<string, string>
  /** The module block calling it and the code file it belongs in. */
  call: { path: string; block: string }
}

// ---------------------------------------------------------------------------
// Docker workspaces (docker compose + Dockerfiles)

export type WorkflowKind = "" | "docker" | "kubernetes"

export function isDocker(wf: Pick<Workflow, "kind"> | null | undefined): boolean {
  return wf?.kind === "docker"
}

export function isKubernetes(wf: Pick<Workflow, "kind"> | null | undefined): boolean {
  return wf?.kind === "kubernetes"
}

export interface ComposeModel {
  /** Compose files in `-f` order. */
  files: string[]
  /** Compose project name runs use ("nimbo-dk-<id>"). */
  project: string
  /** Active profiles. */
  profiles?: string[]
  /** Top-level `name:` (display only). */
  name?: string
  /** Files reached through include: / extends.file. */
  included?: string[]
  dockerfiles?: string[]
  /** ${VAR} references with no value in .env and no default. */
  unresolved?: string[]
  graph: ComposeGraph
  /** Environment the graph was derived for (the active one when its override file exists; absent = base). */
  environment?: string
  /** Environments: compose.<env>.yaml override files next to the first compose file, plus .env.<env>. */
  environments?: ComposeEnvironment[]
}

/** A docker environment: `docker compose -f compose.yaml [-f <file>] --env-file .env --env-file <envFile>`; either file alone is enough. */
export interface ComposeEnvironment {
  name: string
  /** The override file (compose.prod.yaml); absent when the environment only changes variables. */
  file?: string
  /** Its env file as named: .env.<env> or <env>.env. */
  envFile?: string
  /** file then envFile: what the environment adds to the base. */
  files: string[]
}

export interface ComposeGraph {
  nodes: ComposeNode[]
  edges: ComposeEdge[]
}

export type ComposeNodeKind = "service" | "build-stage" | "network" | "volume" | "config" | "secret" | "external"

export interface ComposeNode {
  /** Stable: "service:web", "network:backend", "volume:db-data", "stage:web:builder", "secret:x". */
  id: string
  kind: ComposeNodeKind
  name: string
  /** build-stage → its service node. */
  parent?: string
  /** Saved canvas position; absent = automatic layout. */
  position?: Position
  /** Where it is declared (workflow file path, 1-based line). */
  file?: string
  line?: number
  external?: boolean
  /** What an "external" node is (extends targets are "service"). */
  externalKind?: "network" | "volume" | "config" | "secret" | "service"
  /** A service whose profiles are not active. */
  inactive?: boolean
  service?: ServiceFacts
  stage?: StageFacts
  network?: { driver?: string; internal?: boolean; name?: string }
  volume?: { driver?: string; name?: string }
  /** configs/secrets. */
  resource?: { file?: string; environment?: string; content?: boolean }
}

export interface ComposePortMapping {
  host?: string
  container: string
  protocol?: string
  hostIp?: string
  published?: boolean
}

export interface ComposeHealthcheck {
  test: string
  interval?: string
  timeout?: string
  retries?: number
  startPeriod?: string
  startInterval?: string
  disabled?: boolean
}

export type DependsOnCondition = "service_started" | "service_healthy" | "service_completed_successfully"

export interface ComposeEnvVar {
  key: string
  /** Absent when redacted (secret) or taken from the shell/.env. */
  value?: string
  /** The value as written in the compose file when it differs from `value` (`${VAR}`, `$$`): what editors show and send back. */
  raw?: string
  /** Sent back without a value: keeps the stored value. */
  secret?: boolean
  fromFile?: string
  unresolved?: boolean
}

export interface ComposeMount {
  source?: string
  target: string
  type?: "volume" | "bind" | "tmpfs"
  readOnly?: boolean
}

export interface ComposeDependency {
  service: string
  condition?: DependsOnCondition | string
  required?: boolean
  restart?: boolean
}

export interface ServiceFacts {
  image?: { ref: string; repo: string; tag?: string; digest?: string }
  build?: { context: string; dockerfile: string; target?: string; args?: Record<string, string> }
  /** FROM of every Dockerfile stage. */
  baseImages: string[]
  /** Frameworks/runtimes found in the build context (node, express, nextjs, go, spring…), for icons. */
  stack?: string[]
  ports: ComposePortMapping[]
  expose: string[]
  command?: string
  entrypoint?: string
  healthcheck?: ComposeHealthcheck
  healthcheckFrom?: "compose" | "dockerfile"
  restart?: string
  replicas?: number
  resources?: { cpus?: string; memory?: string }
  profiles?: string[]
  environment: ComposeEnvVar[]
  envFiles?: string[]
  volumes: ComposeMount[]
  networks: string[]
  dependsOn: ComposeDependency[]
  links?: string[]
  containerName?: string
  hostname?: string
  user?: string
  workingDir?: string
  platform?: string
  /** "svc" or "file#svc". */
  extends?: string
  /** Attributes the active environment's override file sets ("image", "ports", "environment", "deploy.replicas", "deploy.resources"…): edits of them go there. */
  overrides?: string[]
}

export interface StageFacts {
  index: number
  name?: string
  from: string
  /** FROM another stage of the same Dockerfile. */
  fromStage?: string
  platform?: string
  dockerfile: string
  line: number
  final?: boolean
  expose?: string[]
  user?: string
  workdir?: string
  cmd?: string
  entrypoint?: string
}

/** No "bind": bind mounts have no node (see ServiceFacts.volumes). */
export type ComposeEdgeKind =
  | "depends_on"
  | "links"
  | "network"
  | "volume"
  | "config"
  | "secret"
  | "build"
  | "copy_from"
  | "extends"
  | "inferred"

/** Like Terraform edges, the source is used by the target: db → api for `api.depends_on: [db]`, network → service. */
export interface ComposeEdge {
  id: string
  source: string
  target: string
  kind: ComposeEdgeKind
  label?: string
  condition?: string
  /** Mount target path. */
  path?: string
  readOnly?: boolean
  /** Inferred connections: the env key whose value names the source (DATABASE_URL). */
  envKey?: string
}

export type DockerImageCategory =
  | "web"
  | "gateway"
  | "language"
  | "database"
  | "cache"
  | "search"
  | "messaging"
  | "storage"
  | "observability"
  | "auth"
  | "workflow"
  | "ai"
  | "devtools"
  | "apps"
  | "cms"
  | "network"
  | "tools"

/** A file template of GET /catalog/file-templates (Code tab "New from template…"). */
export interface FileTemplate {
  id: string
  kind: "docker" | "kubernetes"
  /** Menu group: "Dockerfile", ".dockerignore", "Environment", "Compose", "Config", "Kustomize", "Helm", "Manifest". */
  group: string
  label: string
  description: string
  /** Default name of the new file (for a snippet, the file it goes into). */
  fileName: string
  stack?: string
  /** Inserted into an existing file (a compose service under `services:`) instead of creating one. */
  snippet?: boolean
  content: string
}

/** A curated image of GET /catalog/docker/images. */
export interface DockerImage {
  id: string
  image: string
  tag: string
  label: string
  description: string
  category: DockerImageCategory
  /** Tech icon ID (src/components/tech-icons). */
  icon: string
  ports: number[]
  env?: { key: string; value: string; secret?: boolean; description?: string }[]
  healthcheck?: ComposeHealthcheck
  volumes?: { target: string; name?: string }[]
  /** Env values another service sets to reach this one ({{host}}, {{port}}, {{env.KEY}} placeholders). */
  connect?: { key: string; template: string }[]
  command?: string
  /** Replaces the image's entrypoint (e.g. "sh -c" chaining a setup step before command). */
  entrypoint?: string
}

export interface AddServiceRequest {
  name?: string
  image?: string
  /** DockerImage.id: image, ports, env, healthcheck and volumes from the catalog. */
  catalogId?: string
  /**
   * Builds from `context`. A context without a Dockerfile gets a buildable starter (and the folder): node/python/go
   * = a tiny HTTP server on 8080, static = nginx serving index.html on 80, alpine (default) = a container that keeps running.
   */
  build?: { context: string; dockerfile?: string; target?: string; starter?: DockerStarter }
  position?: Position
}

export type DockerStarter = "node" | "python" | "go" | "static" | "alpine"

/** PATCH /workflows/{id}/compose/services/{name}: set fields; null removes the key; lists replace the whole list. */
export interface ServicePatch {
  name?: string
  image?: string | null
  build?: { context: string; dockerfile?: string; target?: string } | null
  ports?: ComposePortMapping[] | null
  environment?: ComposeEnvVar[] | null
  volumes?: ComposeMount[] | null
  dependsOn?: ComposeDependency[] | null
  healthcheck?: ComposeHealthcheck | null
  restart?: string | null
  replicas?: number | null
  resources?: { cpus?: string; memory?: string } | null
  networks?: string[] | null
  command?: string | string[] | null
  entrypoint?: string | string[] | null
  profiles?: string[] | null
  user?: string | null
  containerName?: string | null
  expose?: string[] | null
  envFiles?: string[] | null
  /** Attributes (as in ServiceFacts.overrides) the active environment's override file stops setting: the base values apply again. */
  resetOverrides?: string[]
}

export interface ComposeNetworkRequest {
  name: string
  driver?: string
  external?: boolean
  internal?: boolean
}

export interface ComposeVolumeRequest {
  name: string
  driver?: string
  external?: boolean
}

export type ComposeConnectKind = "depends_on" | "network" | "volume" | "env"

/** One way to connect two compose nodes (GET /workflows/{id}/compose/connect). */
export interface ComposeConnectRecipe {
  id: string
  kind: ComposeConnectKind
  label: string
  description?: string
  condition?: DependsOnCondition
  network?: string
  env?: { key: string; value: string }[]
  /** Volume recipes: the mount path when the catalog knows it; absent = ask the user and send `target`. */
  target?: string
}

export interface ComposeConnectRequest {
  from: string
  to: string
  kind: ComposeConnectKind
  condition?: DependsOnCondition
  network?: string
  recipeId?: string
  /** Volume mounts: path inside the container. */
  target?: string
}

// Runtime (docker compose up/down of a docker workspace)

export type ComposeRunAction = "up" | "down" | "build" | "pull" | "restart"

export interface ComposeRunRequest {
  action: ComposeRunAction
  /** up/build/pull/restart: only this service. */
  service?: string
  /** down: also delete named volumes (data). */
  removeVolumes?: boolean
  /** up: move busy fixed host ports to free ones (an override file; the user's compose stays untouched). */
  remapPorts?: boolean
  timeoutSec?: number
}

export interface PortConflict {
  service: string
  containerPort: number
  protocol: "tcp" | "udp"
  hostIp?: string
  hostPort: number
  /** Size of a published range (hostPort is its first port); absent for one port. */
  count?: number
  /** Process holding the port, when known. */
  holder?: string
  /** Free port (first of `count`) remapPorts would use. */
  suggestedPort: number
}

export interface PortRemap {
  service: string
  containerPort: number
  protocol: "tcp" | "udp"
  /** From the compose file. */
  hostPort: number
  /** Size of a moved range (hostPort/port are its first ports); absent for one port. */
  count?: number
  /** Actually published. */
  port: number
}

export type ComposeState = "absent" | "starting" | "running" | "stopped" | "error"
export type ContainerHealth = "healthy" | "unhealthy" | "starting"

export interface ComposePort {
  container: number
  protocol: "tcp" | "udp"
  hostIp: string
  host: number
  /** http://127.0.0.1:<host>/ for TCP ports reachable from this machine. */
  url?: string
}

export interface ComposeContainer {
  id: string
  name: string
  state: "created" | "running" | "restarting" | "paused" | "exited" | "dead" | "removing"
  health?: ContainerHealth
  exitCode: number
  restarts: number
  image: string
  imageId: string
  startedAt?: string
  finishedAt?: string
  oomKilled?: boolean
  /** Time from start until ready, in ms. */
  startupMs?: number
  /** CPU limit in cores; 0/absent = none. */
  cpuLimit?: number
  /** Memory limit in bytes; 0/absent = none. */
  memLimit?: number
  ports: ComposePort[]
}

export interface ComposeServiceStatus {
  name: string
  nodeId: string
  state: ComposeState
  health?: ContainerHealth
  containers: ComposeContainer[]
  /** Time from start until ready, in ms. */
  startupMs?: number
}

export interface ComposeStatus {
  workflowId: string
  project: string
  state: ComposeState
  /** Every service of the compose files (absent ones too), by name. */
  services: ComposeServiceStatus[]
  remaps: PortRemap[]
  message?: string
  updatedAt: string
  /** OOM kills, restart loops, failed exits and unhealthy containers. */
  problems?: ComposeProblem[]
}

export interface ComposeProblem {
  service: string
  nodeId: string
  container: string
  kind: "oom" | "restarts" | "exit" | "unhealthy"
  severity: "error" | "warning"
  message: string
  fix?: string
  exitCode?: number
  restarts?: number
}

/** One `docker stats` sample of a service (containers summed). % of limit = cpuPercent / cpuLimit. */
export interface ServiceUsage {
  service: string
  /** ISO time. */
  t: string
  /** Running containers summed. */
  containers: number
  /** docker stats %: 100 = one core. */
  cpuPercent: number
  /** Cores: sum of limits, or host CPUs when unlimited. */
  cpuLimit: number
  cpuLimited: boolean
  memBytes: number
  memLimit: number
  memLimited: boolean
  /** Cumulative counters. */
  netRxBytes: number
  netTxBytes: number
  blockReadBytes: number
  blockWriteBytes: number
  pids: number
}

/** GET /workflows/{id}/compose/stats (also starts the `docker stats` stream when containers run). */
export interface ComposeStats {
  workflowId: string
  streaming: boolean
  hostCpus: number
  hostMemory: number
  intervalMs: number
  services: { service: string; nodeId: string; current?: ServiceUsage; history: ServiceUsage[] /* last 120 s, oldest first */ }[]
  message?: string
  updatedAt: string
}

export interface ComposeStatsEvent {
  workflowId: string
  services: ServiceUsage[]
}

export interface BuildStep {
  name: string
  stage?: string
  instruction: string
  internal?: boolean
  cached: boolean
  durationMs: number
  error?: boolean
}

export interface BuildFinding {
  code: "image_too_big" | "base_dominates" | "slow_uncached_step"
  severity: "error" | "warning" | "info"
  message: string
  fix?: string
  /** dk_* rule ids (GET /best-practices/rules). */
  rules?: string[]
}

export interface ServiceBuildStats {
  service: string
  nodeId: string
  image?: string
  imageId?: string
  /** node | go | python | java | dotnet | rust | ruby | php | static */
  stack?: string
  durationMs: number
  cacheHits: number
  cacheMisses: number
  steps: BuildStep[]
  sizeBytes: number
  baseImage?: string
  /** 0 = unknown. */
  baseSizeBytes: number
  /** Biggest first (top 8). */
  layers: { createdBy: string; sizeBytes: number; base: boolean }[]
  failed?: boolean
  /** This build − the previous one. */
  delta?: { prevRunId: string; durationMs: number; sizeBytes: number; cacheHits: number; cacheMisses: number }
  findings: BuildFinding[]
}

/** Metrics of a build/up run that built at least one image. */
export interface ComposeBuild {
  runId: string
  action: "build" | "up"
  at: string
  durationMs: number
  succeeded: boolean
  services: ServiceBuildStats[]
}

export interface ComposeLogLine {
  service: string
  container: string
  line: LogLine
}

// ---------------------------------------------------------------------------
// Kubernetes workspaces (manifests, Kustomize, Helm; mirror of backend/internal/model/kube.go)

export type KubeSourceKind = "manifests" | "kustomize" | "helm"

/** One deployable unit of a Kubernetes workflow, in apply order. */
export interface KubeSource {
  kind: KubeSourceKind
  /** Manifest file/dir, kustomization dir (base) or chart dir, relative to the workflow files. */
  path: string
  /** Helm release name (default: the chart name). */
  release?: string
  /** Namespace of objects that declare none (default "default"). */
  namespace?: string
  /** Helm values files layered on the chart's values.yaml for every environment. */
  valuesFiles?: string[]
  /** Environment → Kustomize overlay dir / Helm values file (explicit; conventions fill envPaths). */
  environments?: Record<string, string>
  // derived, read-only
  envPaths?: Record<string, string>
  chart?: KubeChart
  objects: number
  error?: string
}

export interface KubeChart {
  name: string
  version: string
  appVersion?: string
  description?: string
  icon?: string
  dependencies?: KubeChartDependency[]
}

export interface KubeChartDependency {
  name: string
  version?: string
  repository?: string
  alias?: string
  condition?: string
  vendored: boolean
  enabled: boolean
}

/** An image built from the workflow's files and loaded into the local cluster before `up`. */
export interface KubeBuild {
  image: string
  context: string
  dockerfile?: string
  target?: string
  args?: Record<string, string>
}

export interface KubernetesModel {
  sources: KubeSource[]
  builds?: KubeBuild[]
  // derived, read-only
  /** Environment the graph was rendered for ("" = base). */
  environment?: string
  files?: string[]
  dockerfiles?: string[]
  graph: KubeGraph
}

export interface KubeGraph {
  nodes: KubeNode[]
  edges: KubeEdge[]
  /** Third-party charts vendored in Helm sources: the canvas collapses each into one release node. */
  charts?: KubeChartGroup[]
}

/** A chart vendored from a repository (palette "Add chart", monitoring), as one canvas node. */
export interface KubeChartGroup {
  /** "chart:<vendored subchart path>" ("chart:charts/monitoring/charts/kube-prometheus-stack"). */
  id: string
  source: number
  sourcePath: string
  /** The vendored chart's name, version and appVersion. */
  chart: string
  version?: string
  appVersion?: string
  repository?: string
  release: string
  namespace: string
  /** Values files in merge order (the source chart's values.yaml first). */
  valuesFiles?: string[]
  objects: number
  /** Saved position of the collapsed node. */
  position?: Position
}

export type KubeCategory = "workload" | "network" | "config" | "storage" | "autoscaling" | "rbac" | "cluster" | "custom"

/** One rendered object. id = "<Kind>/<namespace>/<name>" ("<Kind>/<name>" cluster-scoped). */
export interface KubeNode {
  id: string
  kind: string
  apiVersion: string
  name: string
  namespace?: string
  category: KubeCategory
  position?: Position
  source: number
  sourceKind: KubeSourceKind
  sourcePath: string
  file?: string
  line?: number
  /** Canvas/inspector edits can be written back (Helm objects: field by field, see kubernetesFields). */
  editable: boolean
  labels?: Record<string, string>
  /** Image repositories (icon hints for techIconFor). */
  stack?: string[]
  workload?: KubeWorkload
  service?: KubeServiceFacts
  routes?: KubeRoute[]
  scaler?: KubeScaler
  disruption?: KubeDisruption
  storage?: KubeStorage
  /** ConfigMap/Secret keys (values never sent). */
  data?: string[]
  custom?: boolean
  /** KubeChartGroup id of the vendored chart that renders it (absent for the workflow's own objects). */
  chart?: string
}

export interface KubeWorkload {
  replicas?: number
  selector?: Record<string, string>
  podLabels?: Record<string, string>
  serviceAccount?: string
  schedule?: string
  serviceName?: string
  containers: KubeContainer[]
  initContainers?: KubeContainer[]
  volumes?: KubeVolume[]
  volumeClaimTemplates?: KubeClaimTemplate[]
}

export interface KubeContainer {
  name: string
  image: string
  imageRef?: { ref: string; repo: string; tag?: string; digest?: string }
  pullPolicy?: string
  command?: string[]
  args?: string[]
  ports: KubeContainerPort[]
  probes: KubeProbes
  resources: KubeResources
  env: KubeEnv[]
  envFrom: KubeEnvFrom[]
  volumeMounts: KubeVolumeMount[]
}

export interface KubeContainerPort {
  name?: string
  containerPort: number
  protocol?: string
}

export interface KubeProbes {
  liveness?: KubeProbe
  readiness?: KubeProbe
  startup?: KubeProbe
}

export interface KubeProbe {
  type: "http" | "tcp" | "exec" | "grpc"
  path?: string
  /** Number or port name. */
  port?: string
  command?: string
  initialDelaySeconds?: number
  periodSeconds?: number
  timeoutSeconds?: number
  failureThreshold?: number
}

export interface KubeQuantities {
  cpu?: string
  memory?: string
}

export interface KubeResources {
  requests: KubeQuantities
  limits: KubeQuantities
}

export interface KubeEnv {
  name: string
  /** Absent for secret-looking names (secret set) and valueFrom. */
  value?: string
  secret?: boolean
  from?: KubeRef
}

/** valueFrom: ConfigMap | Secret (name, key), Field (key = fieldPath), Resource (key = resource). */
export interface KubeRef {
  kind: "ConfigMap" | "Secret" | "Field" | "Resource"
  name?: string
  key?: string
}

export interface KubeEnvFrom {
  kind: "ConfigMap" | "Secret"
  name: string
  prefix?: string
}

export interface KubeVolumeMount {
  name: string
  mountPath: string
  subPath?: string
  readOnly?: boolean
}

export interface KubeVolume {
  name: string
  kind: "configMap" | "secret" | "persistentVolumeClaim" | "emptyDir" | "hostPath" | "projected" | "other"
  /** ConfigMap/Secret/claim name or host path. */
  source?: string
}

export interface KubeClaimTemplate {
  name: string
  storageClass?: string
  size?: string
}

export interface KubeServiceFacts {
  type: "ClusterIP" | "NodePort" | "LoadBalancer" | "ExternalName" | string
  headless?: boolean
  ports: KubeServicePort[]
  selector?: Record<string, string>
  externalName?: string
  /** The selector selects no rendered pod template. */
  unmatched?: boolean
}

export interface KubeServicePort {
  name?: string
  port: number
  targetPort?: string
  nodePort?: number
  protocol?: string
}

export interface KubeRoute {
  host?: string
  path?: string
  service: string
  port?: string
  tls?: boolean
  class?: string
}

export interface KubeScaler {
  /** Node id of scaleTargetRef. */
  target: string
  minReplicas: number
  maxReplicas: number
  cpu?: number
  memory?: number
}

export interface KubeDisruption {
  minAvailable?: string
  maxUnavailable?: string
  selector?: Record<string, string>
}

export interface KubeStorage {
  storageClass?: string
  size?: string
  accessModes?: string[]
  provisioner?: string
  default?: boolean
}

/** Source refers to target: the Service selects the workload, the workload uses the ConfigMap, the HPA scales it. */
export type KubeEdgeKind =
  | "selects"
  | "routes"
  | "env"
  | "envFrom"
  | "volume"
  | "pvc"
  | "storageclass"
  | "scales"
  | "disrupts"
  | "serviceaccount"
  | "binds"
  | "netpol"
  | "governs"
  | "inferred"

export interface KubeEdge {
  id: string
  source: string
  target: string
  kind: KubeEdgeKind
  label?: string
  envKey?: string
}

/** Where one field of a rendered object is written (GET /workflows/{id}/kubernetes/fields). */
export interface KubeField {
  /** "spec.replicas", "spec.template.spec.containers[name=web].image", "[0]" indexes. */
  path: string
  value: unknown
  editable: boolean
  /** Why it is read-only (computed by a template expression, several values keys…). */
  reason?: string
  file?: string
  line?: number
  via?: "manifest" | "patch" | "kustomization" | "values" | "template"
  valuesKeys?: string[]
}

export interface KubeFields {
  nodeId: string
  fields: KubeField[]
}

export interface KubeRendered {
  environment: string
  sources: { index: number; kind: KubeSourceKind; path: string; yaml: string }[]
  diagnostics: Diagnostic[]
}

export interface KubeObjectPatch {
  node: string
  set?: Record<string, unknown>
  unset?: string[]
}

export interface KubeConnectRecipe {
  id: string
  kind: "expose" | "selects" | "route" | "envFrom" | "volume" | "env" | "serviceaccount" | "scale" | string
  label: string
  description: string
}

export interface KubeConnectRequest {
  from: string
  to: string
  recipeId?: string
  kind?: string
  port?: number
  path?: string
  host?: string
  key?: string
}

/** GET /catalog/kubernetes/schema: the Kubernetes swagger definitions (OpenAPI v2) the k8s_schema rule uses. */
export interface KubeSchema {
  version: string
  /** "apps/v1/Deployment"… kinds that live in a namespace. */
  namespaced: string[]
  definitions: Record<string, Record<string, unknown>>
}

/** A palette entry of Kubernetes workspaces (GET /catalog/kubernetes). */
export interface KubeCatalogItem {
  /** "deployment-service", "image:<docker catalog id>", "chart:<name>". */
  id: string
  group: "starter" | "image" | "chart"
  label: string
  description: string
  category: string
  /** "kubernetes" for starters, else a tech icon id. */
  icon: string
  /** Object kinds the item adds. */
  kinds: string[]
  image?: string
  workload?: "Deployment" | "StatefulSet"
  chart?: { repository: string; name: string; version: string; appVersion?: string }
}

// Kubernetes runtime (mirror of backend/internal/model/kuberun.go): one local k3s cluster per workflow.

/** Same values as ComposeState. */
export type KubeState = "absent" | "starting" | "running" | "stopped" | "error"
export type KubeClusterState = "absent" | "starting" | "running" | "stopped" | "error"

export interface KubeRunRequest {
  action: "up" | "down" | "restart" | "cluster-stop" | "cluster-start" | "cluster-delete"
  /** restart: workload node id ("" = every Deployment/StatefulSet/DaemonSet). */
  workload?: string
  /** down: also delete PVCs (data!). */
  removeVolumes?: boolean
  /** down: also delete the cluster. */
  deleteCluster?: boolean
  /** up: environment to render (default: the active one). */
  environment?: string
  timeoutSec?: number
}

/** GET /workflows/{id}/kubernetes/status; SSE "kube.status". */
export interface KubeStatus {
  workflowId: string
  cluster: KubeClusterStatus
  /** Worst workload state (absent without workloads). */
  state: KubeState
  workloads: KubeWorkloadStatus[]
  forwards: KubeForward[]
  ingresses: KubeIngressURL[]
  /** Newest first. */
  events: KubeEvent[]
  problems: KubeProblem[]
  message?: string
  updatedAt: string
}

export interface KubeClusterStatus {
  name: string
  state: KubeClusterState
  image: string
  version?: string
  apiPort?: number
  ingressPort?: number
  /** Nimbo's kubeconfig for this cluster (kubectl --kubeconfig <it>). */
  kubeconfig?: string
  ingressClass?: string
  storageClass?: string
  message?: string
}

export interface KubeWorkloadStatus {
  nodeId: string
  kind: string
  namespace: string
  name: string
  state: KubeState
  desired: number
  ready: number
  updated: number
  available: number
  succeeded?: number
  failed?: number
  /** One line ("2 of 3 updated pods are ready"). */
  rollout?: string
  images: string[]
  pods: KubePod[]
  startupMs?: number
  lastSchedule?: string
}

export interface KubePod {
  name: string
  namespace: string
  phase: "Pending" | "Running" | "Succeeded" | "Failed" | "Unknown" | string
  ready: boolean
  restarts: number
  /** CrashLoopBackOff, OOMKilled, ImagePullBackOff, Unschedulable… */
  reason?: string
  message?: string
  node?: string
  ip?: string
  startedAt?: string
  readyAt?: string
  containers: KubePodContainer[]
  cpuLimit?: number
  memLimit?: number
}

export interface KubePodContainer {
  name: string
  image: string
  init?: boolean
  ready: boolean
  state: "waiting" | "running" | "terminated" | string
  reason?: string
  message?: string
  exitCode?: number
  restarts: number
  lastReason?: string
  lastExitCode?: number
  oomKilled?: boolean
}

/** Daemon-managed port-forward 127.0.0.1:hostPort → Service port. */
export interface KubeForward {
  nodeId: string
  namespace: string
  service: string
  port: number
  portName?: string
  protocol: string
  hostPort: number
  url?: string
  ready: boolean
  message?: string
}

export interface KubeIngressURL {
  nodeId: string
  namespace: string
  ingress: string
  host?: string
  path: string
  service: string
  port?: string
  url: string
  /** http://127.0.0.1:<ingress port><path> (send the Host header). */
  address: string
  serviceNodeId: string
  class?: string
}

export interface KubeEvent {
  type: "Normal" | "Warning" | string
  reason: string
  message: string
  nodeId?: string
  kind: string
  namespace?: string
  name: string
  count: number
  lastSeen: string
}

export interface KubeProblem {
  nodeId: string
  namespace: string
  pod?: string
  container?: string
  kind: "oom" | "crashloop" | "restarts" | "image-pull" | "unschedulable" | "probe" | "exit" | "job-failed" | "config" | string
  severity: "error" | "warning"
  message: string
  fix?: string
  exitCode?: number
  restarts?: number
}

export interface KubeLogLine {
  nodeId?: string
  namespace: string
  pod: string
  container: string
  line: LogLine
}

// Convert a docker workflow to Kubernetes (POST /workflows/{id}/convert/kubernetes).

export type ConvertKubernetesFormat = "manifests" | "kustomize" | "helm"

export interface ConvertKubernetesRequest {
  format: ConvertKubernetesFormat
  /** Application name (DNS label): labels, chart/release name, Ingress hosts; default from the workflow name. */
  name?: string
  /** Default: name. */
  namespace?: string
  /** Preview: the report and generated files, nothing created. */
  dryRun?: boolean
  /** Size requests/limits from the newest usable load test (default true when there is one). */
  sizeFromLoadTest?: boolean
  /** Size from this load test run instead of the newest. */
  loadTestRunId?: string
  /** Prod HorizontalPodAutoscalers (Kustomize/Helm). Defaults: enabled, 5 max replicas, 70% CPU. */
  autoscaling?: { enabled?: boolean; maxReplicas?: number; targetCpu?: number }
}

export type KubeSizingSource = "load_test" | "compose" | "default"

/** The load test run a conversion sizes from. */
export interface KubeConvertLoadTest {
  runId: string
  name?: string
  /** Compose service the test hit. */
  target?: string
  startedAt: string
  /** Seconds of load. */
  seconds: number
  rps: number
  peakRps: number
  /** Compose services it measured that still exist. */
  services: string[]
  /** False when sizing from it was turned off. */
  used: boolean
}

export interface KubeResourceQty {
  cpu: string
  memory: string
}

/** A service's requests/limits and where they come from. */
export interface KubeConvertSizing {
  service: string
  source: KubeSizingSource
  requests: KubeResourceQty
  limits: KubeResourceQty
  /** Per-container usage the load test recorded (cores, bytes). */
  measured?: { cpuP90: number; cpuPeak: number; memPeak: number }
  /** The service hit its Docker limit during the test: the measurement is a lower bound. */
  censored?: "cpu" | "memory" | "single-core"
  /** Where the numbers come from, one line. */
  detail: string
}

/** A HorizontalPodAutoscaler the conversion wrote. */
export interface KubeConvertHPA {
  service: string
  environment: string
  minReplicas: number
  maxReplicas: number
  /** averageUtilization, % of the CPU request. */
  targetCpu: number
}

export interface KubeConvertReport {
  format: ConvertKubernetesFormat
  name: string
  namespace: string
  sources: KubeSource[]
  builds: KubeBuild[]
  environments: string[]
  objects: { kind: string; name: string; file: string; service?: string }[]
  /** One compose feature and what it became. */
  mappings: { service?: string; from: string; to: string }[]
  /** What couldn't be mapped (or only approximately): why and what to do. */
  unmapped: { service?: string; feature: string; detail: string; severity: "warning" | "info"; file?: string; line?: number }[]
  /** The load test it can size from (absent when the workflow has no usable run). */
  loadTest?: KubeConvertLoadTest
  sizing: KubeConvertSizing[]
  autoscaling: KubeConvertHPA[]
}

export interface ConvertKubernetesResponse {
  /** The new Kubernetes workflow (not set for a dry run). */
  workflow?: Workflow
  report: KubeConvertReport
  /** Dry run: the generated files by path. */
  files?: Record<string, string>
}

// ---------------------------------------------------------------------------
// Monitoring (GET/POST /workflows/{id}/monitoring)

export interface MonitoringComponent {
  name: string
  /** Pinned image or chart. */
  image: string
  purpose: string
}

export interface MonitoringAlert {
  name: string
  severity: "warning" | "critical"
  /** Workload ("shop/Deployment/api") or compose service. */
  target: string
  summary: string
}

export interface MonitoringScrape {
  target: string
  port: string
  path: string
  via: string
}

export interface MonitoringLink {
  name: "Grafana" | "Prometheus" | "Alertmanager"
  url?: string
  /** Deployed and reachable at url. */
  running: boolean
}

export interface MonitoringCredentials {
  user: string
  password?: string
  /** Where the password is stored. */
  source: string
}

export interface MonitoringSummary {
  kind: "docker" | "kubernetes"
  /** The workflow has the generated monitoring (adding again updates it). */
  enabled: boolean
  components: MonitoringComponent[]
  files: string[]
  alerts: MonitoringAlert[]
  scrapes: MonitoringScrape[]
  dashboards: string[]
  /** Per workload/service: where its HTTP alerts' request metrics come from (empty: no request metrics, no HTTP alert). */
  http: MonitoringHTTP[]
  thresholds: MonitoringThresholds
  links: MonitoringLink[]
  credentials?: MonitoringCredentials
  /** Requested (Kubernetes) or reserved (docker) CPU, memory and disk. */
  cost: { cpuMillis: number; memoryBytes: number; diskBytes: number }
  notes?: string[]
}

export type MonitoringHTTPSource = "traefik" | "ingress-nginx" | "otel"

export interface MonitoringHTTP {
  target: string
  sources: MonitoringHTTPSource[]
}

export interface MonitoringThresholds {
  /** 5xx share of requests that fires the error alert (5m). */
  errorPercent: number
  /** p95 latency that fires the latency alert (10m). */
  latencyP95Ms: number
  /** Below this traffic neither HTTP alert fires. */
  minRequestsPerSecond: number
}

export interface MonitoringRequest {
  /** Zero/omitted fields keep the current thresholds (else the defaults). */
  thresholds?: Partial<MonitoringThresholds>
}

export interface MonitoringResult {
  workflow: Workflow
  summary: MonitoringSummary
}

// ---------------------------------------------------------------------------
// Production readiness (GET /workflows/{id}/readiness, docker and Kubernetes)

export type ReadinessStatus = "ready" | "needs_work" | "blocked"

/**
 * info passes with suggestions; todo is not verified yet (no load test, scenario run or capacity plan): it is left
 * out of the score and lowers the confidence; skip does not apply.
 */
export type ReadinessItemStatus = "pass" | "info" | "warn" | "fail" | "todo" | "skip"

export type ReadinessActionKind = "open_problem" | "run_load_test" | "open_load_test" | "add_monitoring" | "run_scenario" | "plan_capacity"

export interface ReadinessAction {
  kind: ReadinessActionKind
  label: string
  /** The load test run (open_load_test). */
  runId?: string
  /** The incident to run and its target node (run_scenario). */
  scenario?: string
  target?: string
}

export interface ReadinessItem {
  id: string
  title: string
  status: ReadinessItemStatus
  message: string
  /** The rules (k8s_*, dk_*) the item is made of. */
  codes?: string[]
  diagnostics?: Diagnostic[]
  action?: ReadinessAction
}

export type ReadinessCategory = "reliability" | "performance" | "resilience" | "monitoring" | "security" | "hygiene"

export interface ReadinessGroup {
  id: ReadinessCategory
  title: string
  /** Share of the environment score (all categories add up to 100). */
  weight: number
  /** Mean of the scored items (pass/info 100, warn 50, fail 0); null when nothing has evidence yet. */
  score: number | null
  /** Items not verified yet. */
  untested: number
  items: ReadinessItem[]
}

export type ReadinessCellId = "rules" | "health" | "resources" | "monitoring" | "load" | "resilience" | "capacity"

export interface ReadinessCell {
  id: ReadinessCellId
  status: ReadinessItemStatus
  message: string
  diagnostics?: Diagnostic[]
  action?: ReadinessAction
}

/** One compose service or workload of an environment, a cell per column (always all of them, in order). */
export interface ReadinessComponent {
  nodeId: string
  name: string
  /** "service" (compose) or the workload kind. */
  kind: string
  role: "app" | "data"
  /** Worst cell. */
  status: ReadinessItemStatus
  score: number | null
  cells: ReadinessCell[]
}

export interface EnvironmentReadiness {
  /** "" is the base (checked as production when the workflow has no environments). */
  name: string
  productionLike: boolean
  status: ReadinessStatus
  /** 0-100: the weighted mean of the category scores that have evidence. */
  score: number
  /** 0-100: the weighted share of checks verified (100 = nothing left untested). */
  confidence: number
  summary: string
  groups: ReadinessGroup[]
  components: ReadinessComponent[]
  /** The latest load test the evidence comes from. */
  loadTestRunId?: string
}

export interface WorkflowReadiness {
  workflowId: string
  kind: "docker" | "kubernetes"
  activeEnvironment: string
  environments: EnvironmentReadiness[]
}

// ---------------------------------------------------------------- learn (roadmaps + challenges)

export type LearnProviderId = "aws" | "azure" | "gcp" | "oci" | "docker" | "kubernetes" | "global"
export type LearnLevel = "beginner" | "intermediate" | "advanced" | "expert"
export type LearnRuntime = "local" | "emulated" | "design"
export type LearnStatus = "not_started" | "in_progress" | "completed"

/** An official documentation page. */
export interface LearnSource {
  title: string
  url: string
}

export type ChallengeCheckType = "validate" | "rules_clean" | "has" | "deploy" | "http" | "load" | "scenario" | "readiness"

/** One graded goal; the parameter object named like `type` holds its settings. */
export interface ChallengeCheck {
  id: string
  label: string
  type: ChallengeCheckType
  environment?: string
  rules_clean?: Record<string, unknown>
  has?: Record<string, unknown>
  http?: Record<string, unknown>
  load?: Record<string, unknown>
  scenario?: Record<string, unknown>
  readiness?: Record<string, unknown>
}

/** The workflow a challenge starts from: a template, or inline files of a kind. */
export interface ChallengeStarter {
  templateId?: string
  kind?: "terraform" | "docker" | "kubernetes"
  cloud?: Cloud
  files?: Record<string, string>
}

export interface Challenge {
  id: string
  provider: LearnProviderId
  level: LearnLevel
  title: string
  summary: string
  minutes: number
  tags: string[]
  prerequisites?: string[]
  runtime: LearnRuntime
  /** Markdown. */
  problem: string
  requirements: string[]
  /** Revealed one at a time; the last is closest to the answer. */
  hints: string[]
  /** Markdown. */
  solution: string
  solutionFiles?: Record<string, string>
  starter: ChallengeStarter
  checks: ChallengeCheck[]
  /** Global challenges: how to solve it on each provider (markdown). */
  adaptations?: Partial<Record<LearnProviderId, string>>
  sources: LearnSource[]
}

export type ChallengeRunStatus = "pending" | "running" | "passed" | "failed" | "error" | "canceled"

/** One measurement of a check: actual vs expected, e.g. "312 ms" vs "< 100 ms". */
export interface ChallengeCheckResult {
  label: string
  passed: boolean
  actual: string
  expected: string
}

/** A check's outcome; runId is its load test (load, scenario) or deploy run. */
export interface ChallengeGoalResult {
  id: string
  label: string
  type: ChallengeCheckType
  status: ChallengeRunStatus
  passed: boolean
  detail: string
  runId?: string
  checks: ChallengeCheckResult[]
}

/** One "Check my solution" (running or finished). */
export interface ChallengeResult {
  challenge: string
  status: ChallengeRunStatus
  environment?: string
  /** What runs now ("Deploying your changes", a check label). */
  phase?: string
  startedAt: string
  endedAt?: string
  deployRunId?: string
  goals: ChallengeGoalResult[]
  passed: boolean
  score: number
  error?: string
}

/** GET /workflows/{id}/challenge. */
export interface ChallengeView {
  challenge: Challenge
  last?: ChallengeResult
}

/** The user's progress on a challenge (kept across content updates). */
export interface ChallengeProgress {
  challengeId: string
  status: LearnStatus
  attempts: number
  completedGoals: string[]
  workflowId?: string
  startedAt?: string
  updatedAt?: string
  completedAt?: string
}

export interface ChallengeSummary {
  id: string
  provider: LearnProviderId
  level: LearnLevel
  title: string
  summary: string
  minutes: number
  tags: string[]
  runtime: LearnRuntime
  prerequisites: string[]
  checks: number
  progress: ChallengeProgress
}

export interface LearnPrerequisite {
  id: string
  title: string
  completed: boolean
  missing?: boolean
}

export interface ChallengeDetail {
  challenge: Challenge
  progress: ChallengeProgress
  prerequisites: LearnPrerequisite[]
  /** Roadmap steps linking to it ("<level>: <step title>"). */
  steps: string[]
}

export interface RoadmapStep {
  title: string
  description: string
  challenges?: string[]
  docs: LearnSource[]
}

export interface RoadmapLevel {
  level: LearnLevel
  title: string
  goals: string
  steps: RoadmapStep[]
}

export interface Roadmap {
  id: string
  provider: LearnProviderId
  title: string
  summary: string
  levels: RoadmapLevel[]
  sources: LearnSource[]
}

export interface LearnLevelCount {
  level: LearnLevel
  challenges: number
  completed: number
}

export interface LearnProvider {
  id: LearnProviderId
  title: string
  hasRoadmap: boolean
  challenges: number
  completed: number
  levels: LearnLevelCount[]
}

/** Content migrations: applied, not applied (after a failure) and load errors. */
export interface LearnContentStatus {
  applied: number
  pending: number
  errors: string[]
}

export interface LearnOverview {
  providers: LearnProvider[]
  status: LearnContentStatus
}

export interface ChallengeFilter {
  provider?: LearnProviderId
  level?: LearnLevel
  tag?: string
  q?: string
}

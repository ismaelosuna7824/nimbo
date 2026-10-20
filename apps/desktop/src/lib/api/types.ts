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
  nodeId?: string
  attribute?: string
  /** Code diagnostics (Terraform syntax errors): the file and 1-based line they point at. */
  file?: string
  line?: number
}

/** Best-practice findings carry codes with this prefix. */
export const BEST_PRACTICE_PREFIX = "bp_"

export function isBestPractice(d: Diagnostic): boolean {
  return d.code.startsWith(BEST_PRACTICE_PREFIX)
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
  // derived, read-only
  edges: WorkflowEdge[]
  clouds: Cloud[]
  diagnostics: Diagnostic[]
  /** Node ID -> root .tf file the export writes it to (its `file`, or the recommended layout). */
  nodeFiles?: Record<string, string>
}

export interface WorkflowSummary {
  id: string
  name: string
  description?: string
  updatedAt: string
  nodeCount: number
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
  cloud: Cloud
  difficulty: "beginner" | "intermediate" | "advanced"
  tags: string[]
  icon?: WorkflowIcon
}

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

export type RunAction = "validate" | "plan" | "apply" | "destroy"
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
  preset: "smoke" | "load" | "stress" | "spike" | "soak" | "custom"
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
  /** wsl (Windows only) | docker-cli | docker-daemon | docker-permissions (Linux only) | images | docker-socket | docker-memory | disk | internet */
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

// ---------------------------------------------------------------- events

export type ServerEvent =
  | { type: "workflow.updated"; workflowId: string; data: Workflow }
  | { type: "workflow.deleted"; workflowId: string; data: { id: string } }
  | { type: "sandbox.status"; workflowId: string; data: SandboxStatus }
  | { type: "run.status"; workflowId: string; data: Run }
  | { type: "run.log"; workflowId: string; data: { runId: string; line: LogLine } }
  | { type: "state.updated"; workflowId: string; data: DeployedState }
  | { type: "loadtest.sample"; workflowId: string; data: { runId: string; sample: LoadSample } }
  | { type: "loadtest.status"; workflowId: string; data: LoadTestRun }
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
  /** Best-practice checks (bp_* diagnostics): on by default, off as a whole or per rule. */
  bestPractices: BestPracticeSettings
}

export interface BestPracticeSettings {
  disabled?: boolean
  /** Rule codes turned off, e.g. "bp_missing_tags". */
  disabledRules: string[]
}

/** One best-practice check (GET /best-practices/rules). */
export interface BestPracticeRule {
  code: string
  title: string
  category: "inputs" | "versions" | "hardcoding" | "tagging" | "naming" | "security" | "structure"
  /** Default severity (a few findings of a rule may be lower). */
  severity: Severity
  description: string
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
export type WorkflowFileKind = "terraform" | "tfvars" | "env" | "module" | "scaffold" | "asset"
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
  | "text"

/**
 * One file an export writes (GET /workflows/{id}/files). Generated files are rendered from the workflow;
 * editable generated *.tf change through PUT /terraform, editable assets through PUT /files/content.
 */
export interface WorkflowFile {
  /** Slash-separated, relative to the export root. */
  path: string
  size: number
  kind: WorkflowFileKind
  generated: boolean
  editable: boolean
  binary: boolean
  language: FileLanguage
}

export interface WorkflowFileContent extends WorkflowFile {
  content: string
}

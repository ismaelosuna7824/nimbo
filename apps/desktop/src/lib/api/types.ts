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
}

export interface Block {
  type: string
  labels?: string[]
  body: Body
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

export interface Diagnostic {
  severity: Severity
  code: string
  message: string
  detail?: string
  nodeId?: string
  attribute?: string
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
  /** Comments of the exported `locals` block (imported locals blocks are merged). */
  localsComments?: Comments
  providers: NamedBlock[]
  terraform?: Body
  other: Block[]
  environments: Environment[]
  activeEnvironment?: string
  // derived, read-only
  edges: WorkflowEdge[]
  clouds: Cloud[]
  diagnostics: Diagnostic[]
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
  /** Local modules: the module as a workflow (nodes unpositioned). */
  workflow?: Workflow
  /** Registry/remote modules or unreadable sources: why there is no diagram. */
  message?: string
}

import type {
  ImportRequest,
  ImportScan,
  ImportSource,
  ModuleExtraction,
  ModuleExtractRequest,
  ModuleView,
  AgentClient,
  BestPracticeRule,
  ApiErrorBody,
  Connection,
  ConnectionCheck,
  ConnectionRecipe,
  DeployedState,
  EndpointsInfo,
  Diagnostic,
  HealthInfo,
  LoadProfile,
  LoadTarget,
  LoadTestRun,
  LoadTestSpec,
  McpCatalog,
  McpCommand,
  NetworkSummary,
  NodeKind,
  Position,
  ProviderInfo,
  ReachabilityRequest,
  ReachabilityResult,
  ResourceSchema,
  ResourceSummary,
  Run,
  RunAction,
  SandboxStatus,
  ServerEvent,
  Settings,
  SetupStatus,
  Template,
  Workflow,
  WorkflowNode,
  TryRequest,
  TryResult,
  WorkflowFile,
  WorkflowFileContent,
  WorkflowSummary,
} from "./types"

export class ApiError extends Error {
  readonly status: number
  readonly diagnostics: Diagnostic[]
  /** Machine-readable code, e.g. "confirmation_required" (HTTP 428). */
  readonly code?: string
  constructor(status: number, body: ApiErrorBody) {
    super(body.error)
    this.status = status
    this.diagnostics = body.diagnostics ?? []
    this.code = body.code
  }
}

let baseUrl = (import.meta.env.VITE_NIMBO_URL as string | undefined) ?? "http://127.0.0.1:7788"

/** Set by the Tauri bootstrap once the sidecar reports its port. */
export function setApiBaseUrl(url: string) {
  baseUrl = url.replace(/\/$/, "")
}

export function apiBaseUrl() {
  return baseUrl
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${baseUrl}/api/v1${path}`, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) throw new ApiError(res.status, data ?? { error: res.statusText })
  return data as T
}

function qs(params: Record<string, string | number | boolean | undefined>) {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== "") p.set(k, String(v))
  const s = p.toString()
  return s ? `?${s}` : ""
}

export interface CatalogQuery {
  q?: string
  cloud?: string
  kind?: NodeKind
  category?: string
  supported?: boolean
  popular?: boolean
  limit?: number
}

export interface NodePatch {
  name?: string
  label?: string
  position?: Position
  positioned?: boolean
  parent?: string
  body?: WorkflowNode["body"]
  /** Root .tf file to write the node to; "" returns it to the recommended layout. */
  file?: string
}

export const api = {
  health: () => request<HealthInfo>("GET", "/health"),

  // catalog
  providers: () => request<ProviderInfo[]>("GET", "/catalog/providers"),
  categories: (cloud?: string) => request<string[]>("GET", `/catalog/categories${qs({ cloud })}`),
  resources: (q: CatalogQuery) => request<ResourceSummary[]>("GET", `/catalog/resources${qs({ ...q })}`),
  schema: (kind: NodeKind, type: string) =>
    request<ResourceSchema>("GET", `/catalog/resources/${kind}/${encodeURIComponent(type)}`),
  templates: () => request<Template[]>("GET", "/catalog/templates"),
  loadPresets: () => request<LoadProfile[]>("GET", "/catalog/load-presets"),

  // workflows
  listWorkflows: () => request<WorkflowSummary[]>("GET", "/workflows"),
  createWorkflow: (body: { name: string; description?: string; templateId?: string }) =>
    request<Workflow>("POST", "/workflows", body),
  getWorkflow: (id: string) => request<Workflow>("GET", `/workflows/${id}`),
  saveWorkflow: (wf: Workflow) => request<Workflow>("PUT", `/workflows/${wf.id}`, wf),
  deleteWorkflow: (id: string) => request<void>("DELETE", `/workflows/${id}`),
  duplicateWorkflow: (id: string, name: string) => request<Workflow>("POST", `/workflows/${id}/duplicate`, { name }),
  archiveWorkflow: (id: string) => request<Workflow>("POST", `/workflows/${id}/archive`),
  unarchiveWorkflow: (id: string) => request<Workflow>("POST", `/workflows/${id}/unarchive`),

  addNode: (id: string, body: { kind: NodeKind; type: string; name?: string; position: Position }) =>
    request<{ workflow: Workflow; nodeId: string }>("POST", `/workflows/${id}/nodes`, body),
  patchNode: (id: string, nodeId: string, patch: NodePatch) =>
    request<Workflow>("PATCH", `/workflows/${id}/nodes/${nodeId}`, patch),
  deleteNode: (id: string, nodeId: string) => request<Workflow>("DELETE", `/workflows/${id}/nodes/${nodeId}`),

  connections: (id: string, source: string, target: string) =>
    request<ConnectionRecipe[]>("GET", `/workflows/${id}/connections${qs({ source, target })}`),
  connect: (id: string, body: { source: string; target: string; recipeId: string }) =>
    request<Workflow>("POST", `/workflows/${id}/connections`, body),

  // terraform
  terraform: (id: string) => request<{ files: Record<string, string> }>("GET", `/workflows/${id}/terraform`),
  putTerraform: (id: string, files: Record<string, string>) =>
    request<Workflow>("PUT", `/workflows/${id}/terraform`, { files }),
  exportTo: (id: string, dir: string, environments: boolean) =>
    request<{ dir: string; files: string[] }>("POST", `/workflows/${id}/export`, { dir, environments }),
  exportZipUrl: (id: string, environments: boolean) =>
    `${baseUrl}/api/v1/workflows/${id}/export.zip${qs({ environments })}`,
  /** Lists the Terraform projects of a folder (modules used by others are skipped). */
  scanImport: (source: ImportSource) => request<ImportScan>("POST", "/import/scan", source),
  /** Imports projects of a folder, one workflow each, local modules included. */
  importTerraform: (body: ImportRequest) => request<{ workflows: Workflow[] }>("POST", "/import", body),
  /** Read-only view of a module call; `path` drills into nested calls ("module.a.module.b"). */
  moduleView: (id: string, nodeId: string, path?: string) =>
    request<ModuleView>("GET", `/workflows/${id}/modules/${encodeURIComponent(nodeId)}${qs({ path })}`),
  /** What extracting nodes into a local module would create; changes nothing. */
  previewExtractModule: (id: string, body: ModuleExtractRequest) =>
    request<ModuleExtraction>("POST", `/workflows/${id}/modules/extract/preview`, body),
  /** Moves nodes into a new local module modules/<name> and calls it in their place (moved blocks keep state). */
  extractModule: (id: string, body: ModuleExtractRequest) =>
    request<Workflow>("POST", `/workflows/${id}/modules/extract`, body),
  /** Brings a local module's blocks back to the root (called once, no count/for_each). */
  inlineModule: (id: string, nodeId: string) =>
    request<Workflow>("POST", `/workflows/${id}/modules/${encodeURIComponent(nodeId)}/inline`),
  /** Everything an export (with environments) writes: generated Terraform and the workflow's assets. */
  files: (id: string) => request<{ files: WorkflowFile[] }>("GET", `/workflows/${id}/files`),
  /** A text file; binary files fail with ApiError 415 (code "binary"), files over 2 MiB with 413 ("too_large"). */
  fileContent: (id: string, path: string) =>
    request<WorkflowFileContent>("GET", `/workflows/${id}/files/content${qs({ path })}`),
  /** Overwrites an editable text asset; generated *.tf go through putTerraform. */
  putFileContent: (id: string, path: string, content: string) =>
    request<{ file: WorkflowFile; workflow: Workflow }>("PUT", `/workflows/${id}/files/content${qs({ path })}`, {
      content,
    }),
  fileRawUrl: (id: string, path: string) => `${baseUrl}/api/v1/workflows/${id}/files/raw${qs({ path })}`,

  // sandbox & runs
  sandbox: (id: string) => request<SandboxStatus>("GET", `/workflows/${id}/sandbox`),
  sandboxAction: (id: string, action: "start" | "stop" | "reset") =>
    request<SandboxStatus>("POST", `/workflows/${id}/sandbox/${action}`),
  startRun: (id: string, action: RunAction, environment?: string) =>
    request<Run>("POST", `/workflows/${id}/runs`, { action, environment }),
  runs: (id: string) => request<Run[]>("GET", `/workflows/${id}/runs`),
  run: (runId: string) => request<Run>("GET", `/runs/${runId}`),
  cancelRun: (runId: string) => request<void>("POST", `/runs/${runId}/cancel`),
  state: (id: string) => request<DeployedState>("GET", `/workflows/${id}/state`),
  /** How to use what is deployed: URLs, identifiers, snippets and "Try it" actions. */
  endpoints: (id: string) => request<EndpointsInfo>("GET", `/workflows/${id}/endpoints`),
  /** Runs a "Try it" action; operation failures come back as `ok: false` with `error`. */
  tryEndpoint: (id: string, req: TryRequest) => request<TryResult>("POST", `/workflows/${id}/endpoints/try`, req),
  /** Postman Collection v2.1 download for the deployed endpoints. */
  postmanCollectionUrl: (id: string) => `${baseUrl}/api/v1/workflows/${id}/endpoints/postman.json`,

  // network checks
  /** Network model and "net_*" findings (deployed state when available, else the design). */
  network: (id: string) => request<NetworkSummary>("GET", `/workflows/${id}/network`),
  /** Can source reach destination? With `live: true` also runs real connections (can take ~30 s). */
  reachability: (id: string, req: ReachabilityRequest) =>
    request<ReachabilityResult>("POST", `/workflows/${id}/network/reachability`, req),
  /** Adds a small, clearly-labelled EC2 test instance (web server on port 80) and its security group to a subnet. */
  addTestInstance: (id: string, subnet: string) =>
    request<{ workflow: Workflow; nodeId: string }>("POST", `/workflows/${id}/network/test-instance`, { subnet }),

  // load tests
  loadTargets: (id: string) => request<LoadTarget[]>("GET", `/workflows/${id}/loadtests/targets`),
  startLoadTest: (id: string, spec: LoadTestSpec) => request<LoadTestRun>("POST", `/workflows/${id}/loadtests`, spec),
  loadTests: (id: string) => request<LoadTestRun[]>("GET", `/workflows/${id}/loadtests`),
  loadTest: (runId: string) => request<LoadTestRun>("GET", `/loadtests/${runId}`),
  stopLoadTest: (runId: string) => request<void>("POST", `/loadtests/${runId}/stop`),

  // connections library (secrets are never returned; see Connection.secretsSet)
  listConnections: () => request<Connection[]>("GET", "/connections"),
  getConnection: (id: string) => request<Connection>("GET", `/connections/${id}`),
  createConnection: (c: Partial<Connection>) => request<Connection>("POST", "/connections", c),
  updateConnection: (c: Connection) => request<Connection>("PUT", `/connections/${c.id}`, c),
  deleteConnection: (id: string) => request<void>("DELETE", `/connections/${id}`),
  /** Tests an unsaved draft; with an id, blank secrets come from the saved connection. */
  testConnectionDraft: (c: Partial<Connection>) => request<ConnectionCheck>("POST", "/connections/test", c),
  testConnection: (id: string) => request<ConnectionCheck>("POST", `/connections/${id}/test`),
  connectionTargets: (id: string) => request<LoadTarget[]>("GET", `/connections/${id}/targets`),
  startConnectionLoadTest: (id: string, spec: LoadTestSpec) =>
    request<LoadTestRun>("POST", `/connections/${id}/loadtests`, spec),
  connectionLoadTests: (id: string) => request<LoadTestRun[]>("GET", `/connections/${id}/loadtests`),

  // desktop session: the workflow open in the editor, used by MCP agents as "current"
  setSession: (openWorkflowId: string | null) => request<void>("PUT", "/session", { openWorkflowId }),

  // settings & AI agents (MCP clients)
  settings: () => request<Settings>("GET", "/settings"),
  settingsDefaults: () => request<Settings>("GET", "/settings/defaults"),
  saveSettings: (s: Settings) => request<Settings>("PUT", "/settings", s),
  bestPracticeRules: () => request<BestPracticeRule[]>("GET", "/best-practices/rules"),
  agents: () => request<AgentClient[]>("GET", "/agents"),
  installAgent: (id: string) => request<AgentClient>("POST", `/agents/${id}/install`),
  uninstallAgent: (id: string) => request<AgentClient>("DELETE", `/agents/${id}`),
  mcpCommand: () => request<McpCommand>("GET", "/mcp/command"),
  mcpCatalog: () => request<McpCatalog>("GET", "/mcp/catalog"),

  // setup / onboarding: requirement checks and one-click fixes (progress via "setup.progress" events)
  getSetup: () => request<SetupStatus>("GET", "/setup"),
  /** 202 when started; ApiError 409 if already running, 404 if unknown. */
  setupAction: (action: string) => request<void>("POST", `/setup/actions/${action}`),
}

/** Subscribes to server events; returns an unsubscribe function. */
export function subscribeEvents(workflowId: string | undefined, onEvent: (e: ServerEvent) => void): () => void {
  const es = new EventSource(`${baseUrl}/api/v1/events${qs({ workflow: workflowId })}`)
  const handler = (msg: MessageEvent<string>) => onEvent(JSON.parse(msg.data) as ServerEvent)
  const types: ServerEvent["type"][] = [
    "workflow.updated",
    "workflow.deleted",
    "sandbox.status",
    "run.status",
    "run.log",
    "state.updated",
    "loadtest.sample",
    "loadtest.status",
    "connections.updated",
    "setup.progress",
  ]
  for (const t of types) es.addEventListener(t, handler as EventListener)
  return () => es.close()
}

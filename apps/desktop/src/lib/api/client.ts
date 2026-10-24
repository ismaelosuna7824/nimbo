import type {
  AddServiceRequest,
  CapacityPlan,
  CapacityRequest,
  ChallengeResult,
  ChallengeView,
  ComposeConnectRecipe,
  ComposeConnectRequest,
  ComposeLogLine,
  ComposeNetworkRequest,
  ComposeRunRequest,
  ComposeBuild,
  ComposeStats,
  ComposeStatus,
  ComposeVolumeRequest,
  ConvertKubernetesRequest,
  ConvertKubernetesResponse,
  EnvCompare,
  EnvironmentsView,
  EnvSettings,
  MonitoringRequest,
  MonitoringResult,
  MonitoringSummary,
  DockerImage,
  FileTemplate,
  PortConflict,
  PortRemap,
  ServicePatch,
  KubeCatalogItem,
  KubeConnectRecipe,
  KubeConnectRequest,
  KubeFields,
  KubeObjectPatch,
  KubeRendered,
  KubeSchema,
  KubeLogLine,
  KubeRunRequest,
  KubeStatus,
  WorkflowKind,
  WorkflowReadiness,
  CodeProblem,
  CodeWorkspace,
  FunctionSignature,
  TypeInfo,
  ImportRequest,
  ImportScan,
  ImportSource,
  FileChangesRequest,
  ModuleExtraction,
  ModuleScaffold,
  ModuleExtractRequest,
  ModuleTree,
  ModuleView,
  AgentClient,
  BestPracticeRule,
  AccessCatalog,
  AccessEdit,
  AccessView,
  KubeAccessEdit,
  KubeAccessView,
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
  ScenarioCatalog,
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
  ChallengeDetail,
  ChallengeFilter,
  ChallengeProgress,
  ChallengeSummary,
  LearnOverview,
  LearnProviderId,
  Roadmap,
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
  /** Every resource and data type of every provider (utility ones included), compact. */
  catalogTypes: () => request<TypeInfo[]>("GET", "/catalog/types"),
  /** Several schemas at once, keyed "resource/aws_sqs_queue", "data/…", "provider/aws"; unknown keys are left out. */
  catalogSchemas: (keys: string[]) =>
    request<Record<string, ResourceSchema>>("GET", `/catalog/schemas${qs({ types: keys.join(",") })}`),
  catalogFunctions: () => request<FunctionSignature[]>("GET", "/catalog/functions"),

  // workflows
  listWorkflows: () => request<WorkflowSummary[]>("GET", "/workflows"),
  createWorkflow: (body: { name: string; description?: string; templateId?: string; kind?: WorkflowKind }) =>
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

  // access editors (Terraform; Kubernetes RBAC below)
  /** The structured permissions of a node (404 when it has no access editor). */
  access: (id: string, node: string) => request<AccessView>("GET", `/workflows/${id}/access${qs({ node })}`),
  /** Writes one access editor of a node; returns the whole workflow. */
  putAccess: (id: string, edit: AccessEdit) => request<Workflow>("PUT", `/workflows/${id}/access`, edit),
  /** Pinned official reference data (actions, roles, verbs, resource types) of a provider. */
  accessCatalog: (provider: string, params: { service?: string; resourceType?: string; q?: string } = {}) =>
    request<AccessCatalog>("GET", `/access/catalog/${provider}${qs(params)}`),
  /** The RBAC of a Kubernetes object (Role, binding, ServiceAccount or workload). */
  kubernetesAccess: (id: string, node: string) =>
    request<KubeAccessView>("GET", `/workflows/${id}/kubernetes/access${qs({ node })}`),
  putKubernetesAccess: (id: string, edit: KubeAccessEdit) =>
    request<Workflow>("PUT", `/workflows/${id}/kubernetes/access`, edit),

  // terraform
  /** Creates, edits, moves and deletes workflow files in one request; code re-imports into the diagram. */
  applyFileChanges: (id: string, body: FileChangesRequest) => request<Workflow>("PUT", `/workflows/${id}/terraform`, body),
  /** Formats HCL like terraform fmt; syntax errors fail with ApiError 400 and located diagnostics. */
  formatCode: (content: string, path?: string) =>
    request<{ content: string; changed: boolean }>("POST", "/terraform/format", { content, path }),
  /** The files of a new local module modules/<name> and the block calling it; saves nothing. */
  moduleScaffold: (id: string, name: string) =>
    request<ModuleScaffold>("POST", `/workflows/${id}/modules/scaffold`, { name }),
  /** Every .tf source of the workflow (generated and asset files) plus downloaded registry modules. */
  codeWorkspace: (id: string) => request<CodeWorkspace>("GET", `/workflows/${id}/terraform/workspace`),
  /** Problems of one file being edited; `files` holds it and any other unsaved files. */
  checkCode: (id: string, path: string, files: Record<string, string>) =>
    request<{ problems: CodeProblem[] }>("POST", `/workflows/${id}/terraform/check`, { path, files }),
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
  /** Every module call with its content (local modules, and downloaded registry/remote ones), nested calls included. */
  moduleTree: (id: string) => request<ModuleTree>("GET", `/workflows/${id}/modules`),
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
  /** Incident scenarios a load test of the workflow can inject (active environment). */
  scenarios: (id: string) => request<ScenarioCatalog>("GET", `/workflows/${id}/scenarios`),

  // capacity plans sized from load tests (latest plan kept per environment)
  planCapacity: (id: string, req: CapacityRequest) => request<CapacityPlan>("POST", `/workflows/${id}/capacity-plan`, req),
  /** The environment's latest plan ("" / absent: the active one); null when none. */
  capacityPlan: (id: string, environment?: string) =>
    request<CapacityPlan | null>("GET", `/workflows/${id}/capacity-plan${qs({ environment })}`),
  /** Writes the environment's latest plan (replicas, HPA, requests/limits) into its own files. */
  applyCapacityPlan: (id: string, body: { environment?: string; services?: string[] }) =>
    request<{ workflow: Workflow; applied: string[] }>("POST", `/workflows/${id}/capacity-plan/apply`, body),

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

  // docker workspaces: graph edits write the compose files (comments and order kept) and return the workflow
  dockerImages: () => request<DockerImage[]>("GET", "/catalog/docker/images"),
  /** The Compose Specification JSON schema (draft-07) compose-go validates against. */
  composeSchema: () => request<Record<string, unknown>>("GET", "/catalog/docker/compose-schema"),
  /** Files of the Code tab's "New from template…" menu (docker and Kubernetes). */
  fileTemplates: () => request<FileTemplate[]>("GET", "/catalog/file-templates"),
  addService: (id: string, body: AddServiceRequest) =>
    request<{ workflow: Workflow; nodeId: string }>("POST", `/workflows/${id}/compose/services`, body),
  patchService: (id: string, name: string, patch: ServicePatch) =>
    request<Workflow>("PATCH", `/workflows/${id}/compose/services/${encodeURIComponent(name)}`, patch),
  deleteService: (id: string, name: string) =>
    request<Workflow>("DELETE", `/workflows/${id}/compose/services/${encodeURIComponent(name)}`),
  addNetwork: (id: string, body: ComposeNetworkRequest) =>
    request<{ workflow: Workflow; nodeId: string }>("POST", `/workflows/${id}/compose/networks`, body),
  patchNetwork: (id: string, name: string, body: Partial<ComposeNetworkRequest>) =>
    request<Workflow>("PATCH", `/workflows/${id}/compose/networks/${encodeURIComponent(name)}`, body),
  deleteNetwork: (id: string, name: string) =>
    request<Workflow>("DELETE", `/workflows/${id}/compose/networks/${encodeURIComponent(name)}`),
  addVolume: (id: string, body: ComposeVolumeRequest) =>
    request<{ workflow: Workflow; nodeId: string }>("POST", `/workflows/${id}/compose/volumes`, body),
  patchVolume: (id: string, name: string, body: Partial<ComposeVolumeRequest>) =>
    request<Workflow>("PATCH", `/workflows/${id}/compose/volumes/${encodeURIComponent(name)}`, body),
  deleteVolume: (id: string, name: string) =>
    request<Workflow>("DELETE", `/workflows/${id}/compose/volumes/${encodeURIComponent(name)}`),
  /** Ways to make `from` use `to` (depends_on, shared network, volume, env wiring). */
  composeRecipes: (id: string, from: string, to: string) =>
    request<ComposeConnectRecipe[]>("GET", `/workflows/${id}/compose/connect${qs({ from, to })}`),
  composeConnect: (id: string, body: ComposeConnectRequest) =>
    request<Workflow>("POST", `/workflows/${id}/compose/connect`, body),
  /** Saves canvas positions (null forgets one: automatic layout again). */
  composeLayout: (id: string, positions: Record<string, Position | null>) =>
    request<Workflow>("PATCH", `/workflows/${id}/compose/layout`, { positions }),
  /** Rewrites the FROM image of a build stage (a stage node, or a single-stage service: its only stage). */
  patchComposeStage: (id: string, nodeId: string, from: string) =>
    request<Workflow>("PATCH", `/workflows/${id}/compose/stages/${encodeURIComponent(nodeId)}`, { from }),
  /** docker compose up/down/build/pull/restart; 409 code "port_conflict" when fixed host ports are busy. */
  startComposeRun: (id: string, body: ComposeRunRequest) => request<Run>("POST", `/workflows/${id}/runs`, body),
  composeStatus: (id: string) => request<ComposeStatus>("GET", `/workflows/${id}/compose/status`),
  /** Live usage per service (starts the `docker stats` stream when containers run; then "compose.stats" events). */
  composeStats: (id: string) => request<ComposeStats>("GET", `/workflows/${id}/compose/stats`),
  /** Build metrics of recent build/up runs, newest first (max 20 kept). */
  composeBuilds: (id: string, limit?: number) =>
    request<{ builds: ComposeBuild[] }>("GET", `/workflows/${id}/compose/builds${qs({ limit })}`),
  composePorts: (id: string) =>
    request<{ conflicts: PortConflict[]; remaps: PortRemap[] }>("GET", `/workflows/${id}/compose/ports`),
  composeLogs: (id: string, service?: string, tail?: number) =>
    request<{ lines: ComposeLogLine[] }>("GET", `/workflows/${id}/compose/logs${qs({ service, tail })}`),
  /** Live logs (Server-Sent Events: "compose.log" per line, "end" when the containers stop). */
  composeLogsStreamUrl: (id: string, service?: string, tail?: number) =>
    `${baseUrl}/api/v1/workflows/${id}/compose/logs${qs({ service, tail, follow: true })}`,

  // kubernetes workspaces: object edits write the manifests / kustomize resources / Helm values (comments kept)
  /** Kubernetes v1.36 swagger definitions the k8s_schema rule validates against (editor completion/hover). */
  kubernetesSchema: () => request<KubeSchema>("GET", "/catalog/kubernetes/schema"),
  kubernetesCatalog: () => request<KubeCatalogItem[]>("GET", "/catalog/kubernetes"),
  kubernetesCatalogYaml: (itemId: string, name?: string, namespace?: string) =>
    request<{ yaml: string }>("GET", `/catalog/kubernetes/${encodeURIComponent(itemId)}/yaml${qs({ name, namespace })}`),
  /** Adds a palette item (starter, image, Helm chart) to the workflow. */
  addKubernetesItem: (id: string, body: { itemId: string; name?: string; namespace?: string; source?: number; position?: Position }) =>
    request<{ workflow: Workflow; nodeIds: string[] }>("POST", `/workflows/${id}/kubernetes/catalog`, body),
  kubernetesRender: (id: string, environment?: string) =>
    request<KubeRendered>("GET", `/workflows/${id}/kubernetes/render${qs({ environment })}`),
  /** Where each field of an object is written (manifest, Kustomize patch, Helm values key) or why it is read-only. */
  kubernetesFields: (id: string, node: string) => request<KubeFields>("GET", `/workflows/${id}/kubernetes/fields${qs({ node })}`),
  patchKubernetesObject: (id: string, patch: KubeObjectPatch) =>
    request<Workflow>("PATCH", `/workflows/${id}/kubernetes/objects`, patch),
  addKubernetesObjects: (id: string, body: { yaml: string; source?: number; file?: string }) =>
    request<Workflow>("POST", `/workflows/${id}/kubernetes/objects`, body),
  deleteKubernetesObject: (id: string, node: string) =>
    request<Workflow>("DELETE", `/workflows/${id}/kubernetes/objects${qs({ node })}`),
  /** Ways to make `from` use `to` (expose, selector, route, env/volume mounts, scale…); empty `to` = new object. */
  kubernetesRecipes: (id: string, from: string, to: string) =>
    request<KubeConnectRecipe[]>("GET", `/workflows/${id}/kubernetes/connect${qs({ from, to })}`),
  kubernetesConnect: (id: string, body: KubeConnectRequest) =>
    request<Workflow>("POST", `/workflows/${id}/kubernetes/connect`, body),
  kubernetesLayout: (id: string, positions: Record<string, Position | null>) =>
    request<Workflow>("PATCH", `/workflows/${id}/kubernetes/layout`, { positions }),
  /** up/down/restart and cluster-stop/start/delete on the workflow's local cluster. */
  startKubeRun: (id: string, body: KubeRunRequest) => request<Run>("POST", `/workflows/${id}/runs`, body),
  kubeStatus: (id: string) => request<KubeStatus>("GET", `/workflows/${id}/kubernetes/status`),
  /** Live usage per workload (`service` = workload node id); then "kube.stats" events. */
  kubeStats: (id: string) => request<ComposeStats>("GET", `/workflows/${id}/kubernetes/stats`),
  kubeLogs: (id: string, q: { workload?: string; pod?: string; container?: string; tail?: number }) =>
    request<{ lines: KubeLogLine[] }>("GET", `/workflows/${id}/kubernetes/logs${qs(q)}`),
  /** Live logs (Server-Sent Events: "kube.log" per line, "end" when the pods stop). */
  kubeLogsStreamUrl: (id: string, q: { workload?: string; pod?: string; container?: string; tail?: number }) =>
    `${baseUrl}/api/v1/workflows/${id}/kubernetes/logs${qs({ ...q, follow: true })}`,
  /** Docker workflow → a new Kubernetes workflow (dryRun: preview the report and generated files). */
  convertToKubernetes: (id: string, body: ConvertKubernetesRequest) =>
    request<ConvertKubernetesResponse>("POST", `/workflows/${id}/convert/kubernetes`, body),
  /** What Add monitoring adds (or added) to a docker/Kubernetes workflow, with the local URLs once deployed. */
  monitoring: (id: string) => request<MonitoringSummary>("GET", `/workflows/${id}/monitoring`),
  /** Adds Prometheus, Grafana and alerting (again: regenerates the generated files, never duplicates; thresholds are kept). */
  addMonitoring: (id: string, body?: MonitoringRequest) => request<MonitoringResult>("POST", `/workflows/${id}/monitoring`, body),
  /** Production readiness of every environment of a docker or Kubernetes workflow (each opened on its own). */
  readiness: (id: string) => request<WorkflowReadiness>("GET", `/workflows/${id}/readiness`),

  // environments of docker and Kubernetes workflows (files: overlays, values files, compose overrides, .env.<env>)
  listEnvironments: (id: string) => request<EnvironmentsView>("GET", `/workflows/${id}/environments`),
  /** New environment from the base (`from` empty) or a copy of `from`. */
  createEnvironment: (id: string, body: { name: string; from?: string }) =>
    request<Workflow>("POST", `/workflows/${id}/environments`, body),
  renameEnvironment: (id: string, name: string, newName: string) =>
    request<Workflow>("PATCH", `/workflows/${id}/environments/${encodeURIComponent(name)}`, { name: newName }),
  deleteEnvironment: (id: string, name: string) =>
    request<Workflow>("DELETE", `/workflows/${id}/environments/${encodeURIComponent(name)}`),
  /** Each environment rendered on its own (the active one is not changed), side by side per workload/service. */
  compareEnvironments: (id: string) => request<EnvCompare>("GET", `/workflows/${id}/environments/compare`),
  /** The common knobs of one workload/service in one environment (empty target: the first). */
  environmentSettings: (id: string, env: string, target?: string) =>
    request<EnvSettings>("GET", `/workflows/${id}/environments/${encodeURIComponent(env)}/settings${qs({ target })}`),
  /** Writes knobs into that environment's own files; null drops the environment's override. */
  setEnvironmentSettings: (id: string, env: string, body: { target: string; values: Record<string, string | null> }) =>
    request<Workflow>("PATCH", `/workflows/${id}/environments/${encodeURIComponent(env)}/settings`, body),
  /** Docker: sets a variable of the environment's env file. */
  setEnvironmentVariable: (id: string, env: string, key: string, body: { value: string; secret?: boolean }) =>
    request<Workflow>("PUT", `/workflows/${id}/environments/${encodeURIComponent(env)}/variables/${encodeURIComponent(key)}`, body),
  deleteEnvironmentVariable: (id: string, env: string, key: string) =>
    request<Workflow>("DELETE", `/workflows/${id}/environments/${encodeURIComponent(env)}/variables/${encodeURIComponent(key)}`),

  // learn: roadmaps, challenge catalog and progress (content from the learn database)
  /** Providers with challenge counts and the content migrations' status (load errors included). */
  learnOverview: () => request<LearnOverview>("GET", "/learn"),
  learnRoadmap: (provider: LearnProviderId) => request<Roadmap>("GET", `/learn/providers/${provider}/roadmap`),
  learnChallenges: (filter: ChallengeFilter = {}) => request<ChallengeSummary[]>("GET", `/learn/challenges${qs({ ...filter })}`),
  learnChallenge: (id: string) => request<ChallengeDetail>("GET", `/learn/challenges/${encodeURIComponent(id)}`),
  learnProgress: () => request<ChallengeProgress[]>("GET", "/learn/progress"),
  /** "not_started" resets a challenge's progress; completion only comes from a passing check. */
  setLearnProgress: (id: string, status: "not_started" | "in_progress") =>
    request<ChallengeProgress>("PUT", `/learn/progress/${encodeURIComponent(id)}`, { status }),

  // challenge workspaces: start from a starter, check (async: poll workflowChallenge), solution copy
  startChallenge: (id: string, name?: string) =>
    request<Workflow>("POST", `/learn/challenges/${encodeURIComponent(id)}/start`, { name: name ?? "" }),
  workflowChallenge: (workflowId: string) => request<ChallengeView>("GET", `/workflows/${workflowId}/challenge`),
  checkChallenge: (workflowId: string) => request<ChallengeResult>("POST", `/workflows/${workflowId}/challenge/check`),
  cancelChallengeCheck: (workflowId: string) => request<void>("DELETE", `/workflows/${workflowId}/challenge/check`),
  /** A new workflow: the starter with the reference solution files written over it. */
  challengeSolution: (workflowId: string) => request<Workflow>("POST", `/workflows/${workflowId}/challenge/solution`),
}

/** Subscribes to server events; returns an unsubscribe function. */
export function subscribeEvents(workflowId: string | undefined, onEvent: (e: ServerEvent) => void): () => void {
  const es = new EventSource(`${baseUrl}/api/v1/events${qs({ workflow: workflowId })}`)
  const handler = (msg: MessageEvent<string>) => onEvent(JSON.parse(msg.data) as ServerEvent)
  const types: ServerEvent["type"][] = [
    "workflow.updated",
    "workflow.deleted",
    "workflow.created",
    "sandbox.status",
    "run.status",
    "run.log",
    "state.updated",
    "loadtest.sample",
    "loadtest.status",
    "compose.status",
    "compose.stats",
    "compose.build",
    "kube.status",
    "kube.stats",
    "connections.updated",
    "setup.progress",
  ]
  for (const t of types) es.addEventListener(t, handler as EventListener)
  return () => es.close()
}

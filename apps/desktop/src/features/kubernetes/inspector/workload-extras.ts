// Pure logic of the workload form's Rollout, Spread and Hardening sections and of the Namespace and
// PodDisruptionBudget forms: current values read from GET …/kubernetes/fields (the rendered object's scalar
// fields) and the patches each control writes (PATCH …/kubernetes/objects). Mirrored by the no-code harness
// (backend/internal/api/nocode_kube_test.go).

import type { KubeField } from "@/lib/api/types"

/** What one control writes (PATCH …/kubernetes/objects set/unset). */
export interface ObjectPatch {
  set?: Record<string, unknown>
  unset?: string[]
}

/** The value of the scalar field at `path` (undefined when absent). */
export function fieldValue(fields: readonly KubeField[] | undefined, path: string): unknown {
  return fields?.find((f) => f.path === path)?.value
}

/** The scalar items of the list at `path` (`path[0]`, `path[1]`…), in order. */
export function fieldList(fields: readonly KubeField[] | undefined, path: string): string[] {
  const re = new RegExp(`^${path.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\[(\\d+)\\]$`)
  return (fields ?? [])
    .flatMap((f) => {
      const m = re.exec(f.path)
      return m && f.value !== null && f.value !== undefined ? [{ i: Number(m[1]), v: String(f.value) }] : []
    })
    .sort((a, b) => a.i - b.i)
    .map((x) => x.v)
}

/** Whether any field lives at or under `path`. */
export function hasField(fields: readonly KubeField[] | undefined, path: string): boolean {
  return (fields ?? []).some((f) => f.path === path || f.path.startsWith(`${path}.`) || f.path.startsWith(`${path}[`))
}

// ---------------------------------------------------------------------------
// Rollout strategy (Deployments)

export type StrategyType = "RollingUpdate" | "Recreate"

export interface Strategy {
  type: StrategyType
  /** Text as typed: a number or a percentage; "" = the Kubernetes default (25%). */
  maxUnavailable: string
  maxSurge: string
}

const text = (v: unknown) => (v === undefined || v === null ? "" : String(v))

export function strategyOf(fields: readonly KubeField[] | undefined): Strategy {
  return {
    type: fieldValue(fields, "spec.strategy.type") === "Recreate" ? "Recreate" : "RollingUpdate",
    maxUnavailable: text(fieldValue(fields, "spec.strategy.rollingUpdate.maxUnavailable")),
    maxSurge: text(fieldValue(fields, "spec.strategy.rollingUpdate.maxSurge")),
  }
}

export const INT_OR_PERCENT_RE = /^$|^\d+%?$/

/** A number written as a number, a percentage as text. */
export function intOrPercent(v: string): number | string {
  return /^\d+$/.test(v) ? Number(v) : v
}

/** The patch of the strategy controls: Recreate drops rollingUpdate; RollingUpdate writes the typed limits. */
export function strategyPatch(s: Strategy): ObjectPatch {
  if (s.type === "Recreate") return { set: { "spec.strategy.type": "Recreate" }, unset: ["spec.strategy.rollingUpdate"] }
  const set: Record<string, unknown> = { "spec.strategy.type": "RollingUpdate" }
  const unset: string[] = []
  for (const [key, v] of [
    ["maxUnavailable", s.maxUnavailable],
    ["maxSurge", s.maxSurge],
  ] as const) {
    const path = `spec.strategy.rollingUpdate.${key}`
    if (v === "") unset.push(path)
    else set[path] = intOrPercent(v)
  }
  return { set, unset }
}

/** "0 + 1": a new pod first; both 0 is refused by the API server. */
export function strategyProblem(s: Strategy): string | undefined {
  if (s.type === "RollingUpdate" && /^0%?$/.test(s.maxUnavailable) && /^0%?$/.test(s.maxSurge)) return "Max unavailable and max surge can't both be 0"
  return undefined
}

// ---------------------------------------------------------------------------
// Topology spread (Deployments, StatefulSets)

export const SPREAD_KEYS = { nodes: "kubernetes.io/hostname", zones: "topology.kubernetes.io/zone" } as const
export type Spread = "off" | keyof typeof SPREAD_KEYS | "custom"

/** The preset the pod template's topologySpreadConstraints match ("custom" for anything else). */
export function spreadOf(fields: readonly KubeField[] | undefined, podSpec: string, podLabels: Record<string, string> = {}): Spread {
  const at = `${podSpec}.topologySpreadConstraints`
  if (!hasField(fields, at)) return "off"
  if (hasField(fields, `${at}[1]`)) return "custom"
  const c = `${at}[0]`
  const key = fieldValue(fields, `${c}.topologyKey`)
  const preset = (Object.keys(SPREAD_KEYS) as (keyof typeof SPREAD_KEYS)[]).find((k) => SPREAD_KEYS[k] === key)
  const sameLabels =
    Object.entries(podLabels).every(([k, v]) => fieldValue(fields, `${c}.labelSelector.matchLabels${keyPath(k)}`) === v) &&
    (fields ?? []).filter((f) => f.path.startsWith(`${c}.labelSelector.`)).length === Object.keys(podLabels).length
  const ok = fieldValue(fields, `${c}.maxSkew`) === 1 && fieldValue(fields, `${c}.whenUnsatisfiable`) === "ScheduleAnyway" && sameLabels
  return preset && ok ? preset : "custom"
}

/** A map key as the next segment of a field path (quoted when it holds dots, like the daemon's FormatPath). */
export function keyPath(k: string): string {
  return /[.[\]]/.test(k) || k === "" ? `["${k}"]` : `.${k}`
}

/** The patch of the Spread select: one constraint over nodes or zones counting the workload's own pods. */
export function spreadPatch(podSpec: string, spread: Exclude<Spread, "custom">, podLabels: Record<string, string>): ObjectPatch {
  const at = `${podSpec}.topologySpreadConstraints`
  if (spread === "off") return { unset: [at] }
  return {
    set: { [at]: [{ maxSkew: 1, topologyKey: SPREAD_KEYS[spread], whenUnsatisfiable: "ScheduleAnyway", labelSelector: { matchLabels: { ...podLabels } } }] },
  }
}

// ---------------------------------------------------------------------------
// Hardening (pod and container securityContext; the k8s_run_as_root, k8s_privileged and k8s_hardening findings)

export type HardeningId = "non-root" | "seccomp" | "not-privileged" | "no-escalation" | "read-only" | "drop-all"

export interface HardeningItem {
  id: HardeningId
  label: string
  /** Pod-wide or for this container. */
  scope: "pod" | "container"
  ok: boolean
  /** The patch turning it on (`true`) or off; undefined = can't be switched that way here. */
  patch: (on: boolean) => ObjectPatch | undefined
}

/** The hardening switches of container `container` (its path) in a pod spec at `podSpec`. */
export function hardeningItems(fields: readonly KubeField[] | undefined, podSpec: string, container: string): HardeningItem[] {
  const pod = `${podSpec}.securityContext`
  const sc = `${container}.securityContext`
  const v = (p: string) => fieldValue(fields, p)
  const runAsUser = v(`${pod}.runAsUser`)
  const seccomp = v(`${sc}.seccompProfile.type`) ?? v(`${pod}.seccompProfile.type`)
  const toggle = (path: string, value: unknown) => (on: boolean) => (on ? { set: { [path]: value } } : { unset: [path] })
  return [
    {
      id: "non-root",
      label: "Runs as a non-root user",
      scope: "pod",
      ok: v(`${pod}.runAsNonRoot`) === true || (typeof runAsUser === "number" && runAsUser > 0),
      patch: toggle(`${pod}.runAsNonRoot`, true),
    },
    {
      id: "seccomp",
      label: "Default seccomp profile (RuntimeDefault)",
      scope: "pod",
      ok: seccomp !== undefined && seccomp !== null && seccomp !== "",
      patch: toggle(`${pod}.seccompProfile`, { type: "RuntimeDefault" }),
    },
    {
      id: "not-privileged",
      label: "Not privileged",
      scope: "container",
      ok: v(`${sc}.privileged`) !== true,
      // Only turning privileged mode off is offered.
      patch: (on) => (on ? { unset: [`${sc}.privileged`] } : undefined),
    },
    {
      id: "no-escalation",
      label: "No privilege escalation",
      scope: "container",
      ok: v(`${sc}.allowPrivilegeEscalation`) === false,
      patch: toggle(`${sc}.allowPrivilegeEscalation`, false),
    },
    {
      id: "read-only",
      label: "Read-only root filesystem",
      scope: "container",
      ok: v(`${sc}.readOnlyRootFilesystem`) === true,
      patch: toggle(`${sc}.readOnlyRootFilesystem`, true),
    },
    {
      id: "drop-all",
      label: "Drops all Linux capabilities",
      scope: "container",
      ok: fieldList(fields, `${sc}.capabilities.drop`).some((c) => c.toUpperCase() === "ALL"),
      patch: toggle(`${sc}.capabilities.drop`, ["ALL"]),
    },
  ]
}

/** The pod's user ID field (runAsUser). */
export function runAsUserPatch(podSpec: string, v: string): ObjectPatch {
  const path = `${podSpec}.securityContext.runAsUser`
  return v === "" ? { unset: [path] } : { set: { [path]: Number(v) } }
}

/** A capability as Kubernetes writes it: upper case, without the CAP_ prefix. */
export function normalizeCapability(text: string): string {
  return text.trim().toUpperCase().replace(/^CAP_/, "")
}

/** The patch of the added-capabilities chips (an empty list removes the field). */
export function capAddPatch(container: string, caps: string[]): ObjectPatch {
  const path = `${container}.securityContext.capabilities.add`
  return caps.length ? { set: { [path]: caps } } : { unset: [path] }
}

// ---------------------------------------------------------------------------
// Namespace labels and Pod Security admission

export const POD_SECURITY_LABEL = "pod-security.kubernetes.io/enforce"
export const POD_SECURITY_LEVELS = ["privileged", "baseline", "restricted"] as const

/** The labels with the Pod Security enforce level set ("" removes it). */
export function withPodSecurity(labels: Record<string, string>, level: string): Record<string, string> {
  const next = { ...labels }
  if (level) next[POD_SECURITY_LABEL] = level
  else delete next[POD_SECURITY_LABEL]
  return next
}

/** The patch of the Namespace labels editor (no labels removes the map). */
export function labelsPatch(labels: Record<string, string>): ObjectPatch {
  return Object.keys(labels).length ? { set: { "metadata.labels": labels } } : { unset: ["metadata.labels"] }
}

// ---------------------------------------------------------------------------
// PodDisruptionBudget

export type BudgetMode = "minAvailable" | "maxUnavailable"

/** The patch of the budget controls: one of minAvailable / maxUnavailable, the other removed. */
export function budgetPatch(mode: BudgetMode, value: string): ObjectPatch {
  const other: BudgetMode = mode === "minAvailable" ? "maxUnavailable" : "minAvailable"
  return { set: { [`spec.${mode}`]: intOrPercent(value) }, unset: [`spec.${other}`] }
}

// ---------------------------------------------------------------------------
// ConfigMap and Secret keys

/** A key of a ConfigMap (with its value) or a Secret (value never shown: "" = unchanged). */
export interface DataEntry {
  key: string
  value: string
  /** Where the key is written: data (base64 for Secrets) or stringData (plain text, Secrets). */
  from: "data" | "stringData"
}

export const DATA_KEY_RE = /^[-._a-zA-Z0-9]+$/

/** The keys of a ConfigMap or Secret, from its fields (Secret values are left out). */
export function dataEntries(fields: readonly KubeField[] | undefined, kind: string): DataEntry[] {
  const out: DataEntry[] = []
  for (const f of fields ?? []) {
    const m = /^(data|stringData)(?:\.([^.[\]]+)|\["([^"]*)"\])$/.exec(f.path)
    if (!m || (m[1] === "stringData" && kind !== "Secret")) continue
    const key = m[2] ?? m[3]!
    if (out.some((e) => e.key === key)) continue
    out.push({ key, value: kind === "Secret" ? "" : text(f.value), from: m[1] as DataEntry["from"] })
  }
  return out
}

/**
 * The patch of a key list edit: removed keys are unset; ConfigMap values are written under data, Secret values
 * (only the ones typed) as plain text under stringData, replacing a base64 data entry of the same key.
 */
export function dataPatch(kind: string, before: DataEntry[], after: DataEntry[]): ObjectPatch {
  const set: Record<string, unknown> = {}
  const unset: string[] = []
  for (const b of before) {
    if (!after.some((a) => a.key === b.key)) unset.push(`${b.from}${keyPath(b.key)}`)
  }
  for (const a of after) {
    const b = before.find((x) => x.key === a.key)
    if (kind !== "Secret") {
      if (!b || b.value !== a.value) set[`data${keyPath(a.key)}`] = a.value
      continue
    }
    if (b && a.value === "") continue
    set[`stringData${keyPath(a.key)}`] = a.value
    if (b?.from === "data") unset.push(`data${keyPath(a.key)}`)
  }
  return { set, unset }
}

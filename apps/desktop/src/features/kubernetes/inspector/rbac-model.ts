// Pure helpers of the Kubernetes RBAC editors (Role rules, bindings, identities): verb groups, rule sentences and
// checks mirroring the daemon's (backend/internal/kube/rbac.go), and pickers fed by the graph and the pinned catalog.

import type { AccessCatalog, AccessLevel, KubeGraph, KubeNode, KubeRule, KubeSubject } from "@/lib/api/types"

export const RBAC_GROUP = "rbac.authorization.k8s.io"

/** Roles, bindings and ServiceAccounts: the objects whose inspector is the RBAC editor. */
export function isRbacNode(n: Pick<KubeNode, "kind" | "apiVersion">): boolean {
  if (n.kind === "ServiceAccount") return n.apiVersion === "v1"
  return ["Role", "ClusterRole", "RoleBinding", "ClusterRoleBinding"].includes(n.kind) && n.apiVersion.startsWith(`${RBAC_GROUP}/`)
}

/** Verbs by what they let an identity do, in the editor's order. */
export const VERB_GROUPS: { id: "read" | "write" | "manage" | "other"; label: string; verbs: string[] }[] = [
  { id: "read", label: "Read", verbs: ["get", "list", "watch"] },
  { id: "write", label: "Write", verbs: ["create", "update", "patch"] },
  { id: "manage", label: "Delete", verbs: ["delete", "deletecollection"] },
  { id: "other", label: "Special", verbs: ["use", "bind", "escalate", "impersonate", "approve", "sign", "*"] },
]

const VERB_ORDER = VERB_GROUPS.flatMap((g) => g.verbs)

/** The verbs of a grant level (what "access:<level>" grants on one object). */
export const LEVEL_VERBS: Record<AccessLevel, string[]> = {
  read: ["get", "list", "watch"],
  write: ["get", "list", "watch", "update", "patch"],
  manage: ["get", "list", "watch", "update", "patch", "delete"],
}

/** Verbs sorted like the editor shows them (unknown verbs last, as written). */
export function sortVerbs(verbs: string[]): string[] {
  const rank = (v: string) => {
    const i = VERB_ORDER.indexOf(v)
    return i < 0 ? VERB_ORDER.length : i
  }
  return [...new Set(verbs)].sort((a, b) => rank(a) - rank(b))
}

/** Adds or removes a verb. */
export function toggleVerb(verbs: string[], verb: string): string[] {
  return verbs.includes(verb) ? verbs.filter((v) => v !== verb) : sortVerbs([...verbs, verb])
}

/** The grant level verbs exactly match, if any. */
export function verbsLevel(verbs: string[]): AccessLevel | undefined {
  const set = sortVerbs(verbs).join(",")
  return (Object.keys(LEVEL_VERBS) as AccessLevel[]).find((l) => sortVerbs(LEVEL_VERBS[l]).join(",") === set)
}

/** "core" for the core API group (ConfigMaps, Secrets, Pods…). */
export function groupLabel(group: string): string {
  if (group === "") return "core"
  if (group === "*") return "every API group"
  return group
}

function list(items: string[], quote = false): string {
  const shown = items.map((s) => (quote ? `“${s}”` : s))
  if (shown.length <= 1) return shown.join("")
  return `${shown.slice(0, -1).join(", ")} and ${shown.at(-1)}`
}

/** A rule in words: "Can get, list and watch configmaps “settings” (core)". */
export function ruleSentence(rule: KubeRule): string {
  const verbs = rule.verbs.includes("*") ? "do anything" : list(sortVerbs(rule.verbs))
  if (rule.nonResourceURLs?.length) return `Can ${verbs} on the URLs ${list(rule.nonResourceURLs)}`
  const resources = rule.resources?.includes("*") ? "every resource" : list(rule.resources ?? []) || "nothing"
  const names = rule.resourceNames?.length ? ` ${list(rule.resourceNames, true)}` : ""
  const groups = rule.apiGroups.length ? ` (${rule.apiGroups.map(groupLabel).join(", ")})` : ""
  return `Can ${verbs} ${resources}${names}${groups}`
}

/** What keeps a rule from being saved (the daemon's checks), or undefined. */
export function ruleProblem(rule: KubeRule, kind: string): string | undefined {
  const urls = rule.nonResourceURLs ?? []
  const resources = rule.resources ?? []
  if (rule.verbs.length === 0) return "Pick at least one verb."
  if (resources.length === 0 && urls.length === 0) return "Pick at least one resource."
  if (urls.length > 0 && kind !== "ClusterRole") return "Only a ClusterRole can grant URLs."
  if (urls.length > 0 && resources.length > 0) return "A rule grants either resources or URLs, not both."
  if (resources.length > 0 && rule.apiGroups.length === 0) return "Pick an API group (core for ConfigMaps, Secrets, Pods…)."
  if (rule.resourceNames?.length && rule.verbs.some((v) => v === "create" || v === "deletecollection"))
    return "create and deletecollection can't be limited to names: put them in a rule without names."
  return undefined
}

/** A new rule: read-only access to the core group, resources to pick. */
export function blankRule(): KubeRule {
  return { apiGroups: [""], resources: [], verbs: [...LEVEL_VERBS.read] }
}

/** The API groups of the catalog, the core group first. */
export function catalogGroups(catalog?: AccessCatalog): string[] {
  const groups = new Set((catalog?.resourceTypes ?? []).map((r) => r.group ?? ""))
  return [...groups].sort((a, b) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)))
}

/** The resources (and subresources) of an API group. */
export function catalogResources(catalog: AccessCatalog | undefined, group: string): { name: string; description?: string }[] {
  return (catalog?.resourceTypes ?? []).filter((r) => (r.group ?? "") === group).map((r) => ({ name: r.name, description: r.description }))
}

/** Built-in ClusterRoles a binding can refer to (https://kubernetes.io/docs/reference/access-authn-authz/rbac/#user-facing-roles). */
export const BUILTIN_CLUSTER_ROLES: { name: string; hint: string; broad: boolean }[] = [
  { name: "view", hint: "read most objects, not Secrets", broad: false },
  { name: "edit", hint: "change most objects, Secrets included", broad: true },
  { name: "admin", hint: "everything in a namespace, RBAC included", broad: true },
  { name: "cluster-admin", hint: "everything, everywhere", broad: true },
]

export interface RoleOption {
  kind: "Role" | "ClusterRole"
  name: string
  /** Node of the workflow, when it declares the role. */
  nodeId?: string
  hint?: string
  broad?: boolean
}

/** Roles a binding can refer to: the Roles of its namespace (RoleBindings only), declared and built-in ClusterRoles. */
export function roleOptions(graph: KubeGraph | undefined, binding: Pick<KubeNode, "kind" | "namespace">): RoleOption[] {
  const nodes = graph?.nodes ?? []
  const out: RoleOption[] = []
  if (binding.kind === "RoleBinding") {
    for (const n of nodes) if (n.kind === "Role" && isRbacNode(n) && n.namespace === binding.namespace) out.push({ kind: "Role", name: n.name, nodeId: n.id })
  }
  for (const n of nodes) if (n.kind === "ClusterRole" && isRbacNode(n)) out.push({ kind: "ClusterRole", name: n.name, nodeId: n.id })
  for (const b of BUILTIN_CLUSTER_ROLES) if (!out.some((o) => o.kind === "ClusterRole" && o.name === b.name)) out.push({ kind: "ClusterRole", name: b.name, hint: b.hint, broad: b.broad })
  return out
}

/** "Role/reader": a role option's value. */
export const roleKey = (r: { kind: string; name: string }) => `${r.kind}/${r.name}`

/** ServiceAccounts declared in a namespace (by name). */
export function serviceAccountsIn(graph: KubeGraph | undefined, namespace?: string): string[] {
  return (graph?.nodes ?? []).filter((n) => n.kind === "ServiceAccount" && isRbacNode(n) && n.namespace === namespace).map((n) => n.name)
}

/** A subject in words: "ServiceAccount reports/config-reader". */
export function subjectLabel(s: KubeSubject): string {
  return s.kind === "ServiceAccount" && s.namespace ? `${s.kind} ${s.namespace}/${s.name}` : `${s.kind} ${s.name}`
}

/** Whether a subject can be saved (namespace required for ServiceAccounts of ClusterRoleBindings). */
export function subjectComplete(s: KubeSubject, bindingKind: string): boolean {
  if (!s.name.trim()) return false
  return s.kind !== "ServiceAccount" || bindingKind !== "ClusterRoleBinding" || !!s.namespace?.trim()
}

const DNS = /^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$/

/** Problem with a new ServiceAccount name, or undefined. */
export function serviceAccountNameProblem(name: string): string | undefined {
  if (!name) return "Name it."
  return DNS.test(name) ? undefined : "lowercase letters, digits, “-” and “.”"
}

/** IDs of the nodes added between two graphs of a kind (the Role a grant created, to select it). */
export function addedNodes(before: KubeGraph | undefined, after: KubeGraph | undefined, kind: string): string[] {
  const had = new Set((before?.nodes ?? []).map((n) => n.id))
  return (after?.nodes ?? []).filter((n) => n.kind === kind && !had.has(n.id)).map((n) => n.id)
}

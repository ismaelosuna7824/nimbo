// The Kubernetes OpenAPI (swagger v2) definitions of the pinned release, the
// same the daemon's k8s_schema rule validates against, turned into the
// JSON schema shape of the shared YAML layer: one root per apiVersion/kind
// (its $refs resolve against the shared definitions), int-or-string and
// quantity fields accept numbers, free-form definitions take anything.
// Kinds the release doesn't serve (custom resources) get no schema.

import type { Completion, CompletionResult } from "@codemirror/autocomplete"
import { keyCompletion, S_VALUES } from "@/features/code-intel/yaml/schema-intel"
import { keysAt, type JsonSchema } from "@/features/code-intel/yaml/schema"
import type { KeyContext, PathSegment, ValueContext } from "@/features/code-intel/yaml/context"
import { get, scalarText, type YamlNode } from "@/features/code-intel/yaml/tree"
import { api } from "@/lib/api/client"
import type { KubeSchema } from "@/lib/api/types"

export interface Gvk {
  group: string
  version: string
  kind: string
  /** "apps/v1", "v1". */
  apiVersion: string
  /** Swagger definition name. */
  definition: string
  namespaced: boolean
  description?: string
}

/** A root schema of one kind carries its GVK (docs links). */
export type KindSchema = JsonSchema & { "x-nimbo-gvk"?: Gvk }

export interface KubeSchemaIndex {
  /** "1.36.5". */
  version: string
  /** Every apiVersion/kind the release serves. */
  gvks: Gvk[]
  gvk(apiVersion: string, kind: string): Gvk | undefined
  /**
   * Root schema of an object; null for kinds the release doesn't serve. `partial`: no required fields (Kustomize
   * strategic-merge patches name only what they change).
   */
  schemaFor(apiVersion: string, kind: string, partial?: boolean): KindSchema | null
  /** Schema of a mapping document without apiVersion/kind yet: those keys for completion; judges nothing (any YAML). */
  header: JsonSchema
}

const QUANTITY = "io.k8s.apimachinery.pkg.api.resource.Quantity"
/** Free-form definitions: any value (the daemon's validator skips them too). */
const FREE_FORM: Record<string, true> = {
  "io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.JSON": true,
  "io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.JSONSchemaPropsOrArray": true,
  "io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.JSONSchemaPropsOrBool": true,
  "io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.JSONSchemaPropsOrStringArray": true,
  "io.k8s.apimachinery.pkg.runtime.RawExtension": true,
  "io.k8s.apimachinery.pkg.apis.meta.v1.FieldsV1": true,
}

/** Kinds every group carries (meta/v1 WatchEvent, DeleteOptions…): not objects one writes. */
const NOT_OBJECTS: Record<string, true> = {
  WatchEvent: true,
  DeleteOptions: true,
  Status: true,
  APIGroup: true,
  APIGroupList: true,
  APIResourceList: true,
  APIVersions: true,
}

type Raw = Record<string, unknown>

/** One swagger schema in the YAML layer's shape; `partial` drops required lists. */
function convert(raw: Raw, partial: boolean): JsonSchema {
  if (raw.format === "int-or-string") return { description: raw.description as string | undefined, "x-kubernetes-int-or-string": true }
  const out: JsonSchema = {}
  for (const [k, v] of Object.entries(raw)) {
    switch (k) {
      case "properties": {
        const props: Record<string, JsonSchema> = {}
        for (const [name, p] of Object.entries(v as Record<string, Raw>)) props[name] = convert(p, partial)
        out.properties = props
        break
      }
      case "items":
        out.items = convert(v as Raw, partial)
        break
      case "additionalProperties":
        out.additionalProperties = typeof v === "boolean" ? v : convert(v as Raw, partial)
        break
      case "required":
        if (!partial) out.required = v as string[]
        break
      case "x-kubernetes-group-version-kind":
      case "x-kubernetes-list-type":
      case "x-kubernetes-list-map-keys":
        break
      default:
        Object.assign(out, { [k]: v })
    }
  }
  return out
}

function convertDefinitions(raw: KubeSchema["definitions"], partial: boolean): Record<string, JsonSchema> {
  const defs: Record<string, JsonSchema> = {}
  for (const [name, d] of Object.entries(raw)) {
    if (FREE_FORM[name]) defs[name] = { description: d.description as string | undefined, "x-kubernetes-preserve-unknown-fields": true }
    else if (name === QUANTITY) defs[name] = { description: d.description as string | undefined, type: ["string", "number"] }
    else defs[name] = convert(d, partial)
  }
  return defs
}

/** The served definitions by apiVersion/kind, converted once (the partial variant on first use). */
export function buildKubeSchemaIndex(raw: KubeSchema): KubeSchemaIndex {
  const namespaced = new Set(raw.namespaced)
  const gvks: Gvk[] = []
  const byKey = new Map<string, Gvk>()
  for (const [name, d] of Object.entries(raw.definitions)) {
    for (const g of (d["x-kubernetes-group-version-kind"] as { group: string; version: string; kind: string }[] | undefined) ?? []) {
      const apiVersion = g.group ? `${g.group}/${g.version}` : g.version
      if (NOT_OBJECTS[g.kind]) continue
      const gvk: Gvk = {
        group: g.group,
        version: g.version,
        kind: g.kind,
        apiVersion,
        definition: name,
        namespaced: namespaced.has(`${apiVersion}/${g.kind}`),
        description: d.description as string | undefined,
      }
      if (byKey.has(`${apiVersion}/${g.kind}`)) continue
      byKey.set(`${apiVersion}/${g.kind}`, gvk)
      gvks.push(gvk)
    }
  }
  gvks.sort((a, b) => a.kind.localeCompare(b.kind) || stability(b.version) - stability(a.version))

  let full: Record<string, JsonSchema> | undefined
  let partial: Record<string, JsonSchema> | undefined
  const roots = new Map<string, KindSchema>()
  const defs = (p: boolean) => (p ? (partial ??= convertDefinitions(raw.definitions, true)) : (full ??= convertDefinitions(raw.definitions, false)))

  return {
    version: raw.version,
    gvks,
    gvk: (apiVersion, kind) => byKey.get(`${apiVersion}/${kind}`),
    schemaFor(apiVersion, kind, p = false) {
      const gvk = byKey.get(`${apiVersion}/${kind}`)
      if (!gvk) return null
      const key = `${p ? "p:" : ""}${apiVersion}/${kind}`
      let root = roots.get(key)
      if (!root) {
        root = { $ref: `#/definitions/${gvk.definition}`, definitions: defs(p), "x-nimbo-gvk": gvk }
        roots.set(key, root)
      }
      return root
    },
    header: {
      type: "object",
      "x-kubernetes-preserve-unknown-fields": true,
      properties: {
        apiVersion: { type: "string", description: "The API group and version of the object (apps/v1, v1, networking.k8s.io/v1…)." },
        kind: { type: "string", description: "The kind of object (Deployment, Service, ConfigMap…)." },
        metadata: {
          type: "object",
          "x-kubernetes-preserve-unknown-fields": true,
          description: "Standard object metadata: name, namespace, labels, annotations.",
        },
      },
    },
  }
}

/** v1 > v1beta2 > v1beta1 > v1alpha1: GA versions first. */
function stability(version: string): number {
  const m = /^v(\d+)(alpha|beta)?(\d*)$/.exec(version)
  if (!m) return 0
  const level = m[2] === "alpha" ? 0 : m[2] === "beta" ? 1 : 2
  return level * 10000 + Number(m[1]) * 100 + Number(m[3] || 0)
}

let indexPromise: Promise<KubeSchemaIndex> | undefined

/** The daemon's schema (GET /catalog/kubernetes/schema), fetched and indexed once per session. */
export function loadKubeSchema(): Promise<KubeSchemaIndex> {
  indexPromise ??= api.kubernetesSchema().then(buildKubeSchemaIndex, (err: unknown) => {
    indexPromise = undefined
    throw err
  })
  return indexPromise
}

/** apiVersion and kind of a document's root mapping. */
export function docGvk(root: YamlNode | null | undefined): { apiVersion?: string; kind?: string } {
  return { apiVersion: scalarText(get(root, "apiVersion"))?.trim() || undefined, kind: scalarText(get(root, "kind"))?.trim() || undefined }
}

/** Reference page of a kind ("…/v1.36/#deployment-v1-apps"). */
export function kindDocsUrl(version: string, gvk: Pick<Gvk, "group" | "version" | "kind">): string {
  const minor = /^(\d+\.\d+)/.exec(version)?.[1] ?? version
  const group = gvk.group ? gvk.group.replace(/\./g, "-") : "core"
  return `https://kubernetes.io/docs/reference/generated/kubernetes-api/v${minor}/#${gvk.kind.toLowerCase()}-${gvk.version}-${group}`
}

/** apiVersion: / kind: values (the other one, when written, narrows the list). */
export function gvkValues(index: KubeSchemaIndex, ctx: ValueContext, root: YamlNode | null): Completion[] {
  if (ctx.path.length !== 1) return []
  const { apiVersion, kind } = docGvk(root)
  if (ctx.key === "apiVersion") {
    const serving = kind ? index.gvks.filter((g) => g.kind === kind) : []
    const versions = serving.length ? serving.map((g) => g.apiVersion) : [...new Set(index.gvks.map((g) => g.apiVersion))].sort()
    return versions.map((v, i) => ({
      label: v,
      type: "enum",
      section: S_VALUES,
      detail: serving.length ? `serves ${kind}` : undefined,
      boost: serving.length ? serving.length - i : 0,
    }))
  }
  if (ctx.key === "kind") {
    const inVersion = apiVersion ? index.gvks.filter((g) => g.apiVersion === apiVersion) : []
    const list = inVersion.length ? inVersion : preferred(index.gvks)
    return list.map((g) => ({
      label: g.kind,
      type: "class",
      section: S_VALUES,
      detail: `${g.apiVersion}${g.namespaced ? "" : " · cluster-scoped"}`,
      info: g.description,
    }))
  }
  return []
}

/** One entry per kind: its most stable apiVersion. */
function preferred(gvks: Gvk[]): Gvk[] {
  const out = new Map<string, Gvk>()
  for (const g of gvks) {
    const cur = out.get(g.kind)
    if (!cur || stability(g.version) > stability(cur.version)) out.set(g.kind, g)
  }
  return [...out.values()]
}

/**
 * Keys of a document without apiVersion/kind: whole headers (apiVersion + kind + metadata.name) per kind, plus the
 * header keys. null elsewhere (the schema decides).
 */
export function headerKeys(index: KubeSchemaIndex, ctx: KeyContext, root: YamlNode | null): CompletionResult | null {
  if (ctx.path.length !== 0 || ctx.indent !== 0) return null
  const { apiVersion, kind } = docGvk(root)
  if (apiVersion && kind) return null
  const header = index.header
  const options: Completion[] = keysAt(header, [])
    .filter((k) => (k.name === "apiVersion" ? !apiVersion : k.name === "kind" ? !kind : true))
    .map((k) => keyCompletion(k, header, [], 0))
  if (!apiVersion && !kind) {
    for (const g of preferred(index.gvks)) {
      options.push({
        label: g.kind,
        type: "class",
        detail: g.apiVersion,
        info: g.description,
        apply: `apiVersion: ${g.apiVersion}\nkind: ${g.kind}\nmetadata:\n  name: `,
        boost: -1,
      })
    }
  }
  return options.length ? { from: ctx.from, options, validFor: /^[\w.-]*$/ } : null
}

/** Values of fields the definitions leave as free strings, by the path's tail. */
const ENUMS: { tail: PathSegment[]; values: [string, string][] }[] = [
  { tail: ["imagePullPolicy"], values: [["IfNotPresent", "Pull when the node doesn't have the image (local builds)"], ["Always", "Pull on every start"], ["Never", "Only use the node's image"]] },
  { tail: ["restartPolicy"], values: [["Always", "Restart every exited container (Deployments)"], ["OnFailure", "Restart failed containers (Jobs)"], ["Never", "Never restart"]] },
  { tail: ["protocol"], values: [["TCP", "Default"], ["UDP", ""], ["SCTP", ""]] },
  { tail: ["pathType"], values: [["Prefix", "Matches the path and everything below it"], ["Exact", "Matches the path exactly"], ["ImplementationSpecific", "Up to the ingress controller"]] },
  { tail: ["spec", "type"], values: [["ClusterIP", "Reachable inside the cluster (default)"], ["NodePort", "Also on a port of every node"], ["LoadBalancer", "Also through an external load balancer"], ["ExternalName", "A DNS alias (externalName)"]] },
  { tail: ["strategy", "type"], values: [["RollingUpdate", "Replace pods gradually (default)"], ["Recreate", "Stop every pod before starting new ones"]] },
  { tail: ["updateStrategy", "type"], values: [["RollingUpdate", "Replace pods one by one (default)"], ["OnDelete", "Replace a pod only when it is deleted"]] },
  { tail: ["podManagementPolicy"], values: [["OrderedReady", "Start pods one at a time, in order (default)"], ["Parallel", "Start and stop pods together"]] },
  { tail: ["concurrencyPolicy"], values: [["Allow", "Run jobs concurrently (default)"], ["Forbid", "Skip a run while the previous one is running"], ["Replace", "Replace the running job"]] },
  { tail: ["dnsPolicy"], values: [["ClusterFirst", "Cluster DNS first (default)"], ["ClusterFirstWithHostNet", "For pods with hostNetwork"], ["Default", "The node's DNS"], ["None", "Only dnsConfig"]] },
  { tail: ["externalTrafficPolicy"], values: [["Cluster", "Route to every node's pods (default)"], ["Local", "Only pods on the receiving node (keeps the client IP)"]] },
  { tail: ["internalTrafficPolicy"], values: [["Cluster", "Default"], ["Local", "Only pods on the same node"]] },
  { tail: ["sessionAffinity"], values: [["None", "Default"], ["ClientIP", "Stick a client to a pod"]] },
  { tail: ["accessModes", "[]"], values: [["ReadWriteOnce", "One node mounts it read-write"], ["ReadOnlyMany", "Many nodes read-only"], ["ReadWriteMany", "Many nodes read-write"], ["ReadWriteOncePod", "One pod read-write"]] },
  { tail: ["policyTypes", "[]"], values: [["Ingress", "Restrict incoming traffic"], ["Egress", "Restrict outgoing traffic"]] },
  { tail: ["matchExpressions", "[]", "operator"], values: [["In", ""], ["NotIn", ""], ["Exists", ""], ["DoesNotExist", ""]] },
  { tail: ["tolerations", "[]", "operator"], values: [["Equal", "Default"], ["Exists", "Any value"]] },
  { tail: ["effect"], values: [["NoSchedule", ""], ["PreferNoSchedule", ""], ["NoExecute", ""]] },
  { tail: ["volumeMode"], values: [["Filesystem", "Default"], ["Block", "A raw block device"]] },
  { tail: ["reclaimPolicy"], values: [["Delete", ""], ["Retain", ""]] },
  { tail: ["persistentVolumeReclaimPolicy"], values: [["Delete", ""], ["Retain", ""], ["Recycle", ""]] },
  { tail: ["volumeBindingMode"], values: [["WaitForFirstConsumer", "Bind when a pod uses the claim"], ["Immediate", "Bind at once"]] },
  { tail: ["seccompProfile", "type"], values: [["RuntimeDefault", "The container runtime's default profile"], ["Localhost", "A profile on the node"], ["Unconfined", "No seccomp"]] },
  { tail: ["capabilities", "drop", "[]"], values: [["ALL", "Drop every capability"]] },
  { tail: ["storageClassName"], values: [["local-path", "The local k3s cluster's storage class"]] },
  { tail: ["ingressClassName"], values: [["traefik", "The local k3s cluster's ingress controller"], ["nginx", "Alias of traefik on the local cluster"]] },
]

/** Values of well-known Kubernetes fields at a value path. */
export function knownFieldValues(ctx: ValueContext, kind: string | undefined): Completion[] {
  for (const e of ENUMS) {
    if (e.tail.length > ctx.path.length) continue
    if (!e.tail.every((s, i) => ctx.path[ctx.path.length - e.tail.length + i] === s)) continue
    // spec.type only means a Service type.
    if (e.tail[0] === "spec" && e.tail[1] === "type" && (ctx.path.length !== 2 || kind !== "Service")) continue
    return e.values.map(([value, description]) => ({ label: value, type: "enum", section: S_VALUES, info: description || undefined }))
  }
  return []
}

// Go to definition in Kubernetes manifests: a Service (PDB, NetworkPolicy)
// selector label → the pod template labels of the workloads it selects; a
// reference by name (configMapRef, secretKeyRef, volume claimName,
// serviceAccountName, Ingress backend…) → the object's document. Objects are
// found in the derived graph (file/line of every rendered object) and, for
// files outside every source, in the workflow's YAML texts.

import { get, nodeAt, parseYaml, scalarText, type YamlMap, type YamlNode } from "@/features/code-intel/yaml/tree"
import type { KubeNode } from "@/lib/api/types"
import { docGvk } from "./k8s-schema"

export interface ManifestDefinitionEnv {
  /** Workflow path of the edited file. */
  path: string
  /** Rendered objects (kube().graph.nodes). */
  nodes: readonly KubeNode[]
  /** Texts of the workflow's files (unsaved ones as edited; the edited file included). */
  files: Record<string, string>
}

export interface ManifestTarget {
  file: string
  line: number
}

/** Field paths whose value names another object: the tail of the path → the object's kind. */
const REFS: { tail: string[]; kind: string | ((parent: YamlMap) => string | undefined); key?: true }[] = [
  { tail: ["configMapRef", "name"], kind: "ConfigMap" },
  { tail: ["secretRef", "name"], kind: "Secret" },
  { tail: ["configMapKeyRef", "name"], kind: "ConfigMap" },
  { tail: ["secretKeyRef", "name"], kind: "Secret" },
  { tail: ["configMapKeyRef", "key"], kind: "ConfigMap", key: true },
  { tail: ["secretKeyRef", "key"], kind: "Secret", key: true },
  { tail: ["configMap", "name"], kind: "ConfigMap" },
  { tail: ["secret", "secretName"], kind: "Secret" },
  { tail: ["persistentVolumeClaim", "claimName"], kind: "PersistentVolumeClaim" },
  { tail: ["imagePullSecrets", "[]", "name"], kind: "Secret" },
  { tail: ["tls", "[]", "secretName"], kind: "Secret" },
  { tail: ["serviceAccountName"], kind: "ServiceAccount" },
  { tail: ["backend", "service", "name"], kind: "Service" },
  { tail: ["defaultBackend", "service", "name"], kind: "Service" },
  { tail: ["backendRefs", "[]", "name"], kind: (p) => scalarText(get(p, "kind")) ?? "Service" },
  { tail: ["scaleTargetRef", "name"], kind: (p) => scalarText(get(p, "kind")) },
  { tail: ["roleRef", "name"], kind: (p) => scalarText(get(p, "kind")) },
  { tail: ["subjects", "[]", "name"], kind: (p) => scalarText(get(p, "kind")) },
  { tail: ["storageClassName"], kind: "StorageClass" },
  { tail: ["ingressClassName"], kind: "IngressClass" },
  { tail: ["priorityClassName"], kind: "PriorityClass" },
  { tail: ["volumeName"], kind: "PersistentVolume" },
]

/** Kinds whose own spec.selector selects their pod template (not other objects' pods). */
const WORKLOADS: Record<string, true> = { Deployment: true, StatefulSet: true, DaemonSet: true, ReplicaSet: true, Job: true, CronJob: true, ReplicationController: true }

/** Path of the pod template's labels in a workload. */
function podLabelsPath(kind: string): string[] {
  if (kind === "CronJob") return ["spec", "jobTemplate", "spec", "template", "metadata", "labels"]
  if (kind === "Pod") return ["metadata", "labels"]
  return ["spec", "template", "metadata", "labels"]
}

function lineOfOffset(text: string, offset: number): number {
  let n = 1
  for (let i = text.indexOf("\n"); i >= 0 && i < offset; i = text.indexOf("\n", i + 1)) n++
  return n
}

/** The node at a key path under `root`, with the key's line. */
function at(root: YamlNode | null, path: readonly string[]): { node: YamlNode | null; line: number } | null {
  let node: YamlNode | null = root
  let line = root?.line ?? 0
  for (const seg of path) {
    if (node?.kind !== "map") return null
    const e: YamlMap["entries"][number] | undefined = node.entries.find((x) => x.key === seg)
    if (!e) return null
    node = e.value
    line = e.line
  }
  return { node, line }
}

/** The document of `text` holding 1-based `line` (or the first one starting after it, e.g. on a "---" line). */
function docAtLine(text: string, line: number) {
  const docs = parseYaml(text)
  return (
    docs.find((d) => lineOfOffset(text, d.from) <= line && line <= lineOfOffset(text, Math.max(d.from, d.to - 1))) ??
    docs.find((d) => lineOfOffset(text, d.from) > line) ??
    docs[0]
  )
}

/** The mapping at a key path of `root` ("[]" steps into the list item holding `pos`). */
function mappingAt(root: YamlNode | null, path: readonly string[], pos: number): YamlMap | null {
  let cur: YamlNode | null = root
  for (const seg of path) {
    if (seg === "[]") {
      if (cur?.kind !== "seq") return null
      cur = cur.items.find((it) => pos >= it.from && pos <= it.to) ?? null
    } else cur = get(cur, seg) ?? null
  }
  return cur?.kind === "map" ? cur : null
}

/** String entries of a mapping. */
function stringMap(m: YamlNode | null | undefined): Record<string, string> {
  const out: Record<string, string> = {}
  if (m?.kind === "map") for (const e of m.entries) if (e.value?.kind === "scalar") out[e.key] = e.value.value
  return out
}

/** The object of `kind` named `name`: a graph node (preferring the namespace), else a document of the YAML texts. */
function findObject(env: ManifestDefinitionEnv, kind: string, name: string, namespace: string | undefined): ManifestTarget | null {
  const ns = namespace ?? "default"
  const nodes = env.nodes.filter((n) => n.kind === kind && n.name === name && n.file)
  const node = nodes.find((n) => !n.namespace || n.namespace === ns) ?? nodes[0]
  if (node) return { file: node.file!, line: node.line ?? 1 }
  for (const [file, text] of Object.entries(env.files)) {
    if (!/\.ya?ml$/i.test(file) || !text.includes(name)) continue
    for (const d of parseYaml(text)) {
      if (docGvk(d.root).kind === kind && scalarText(get(get(d.root, "metadata"), "name")) === name) return { file, line: lineOfOffset(text, d.root?.from ?? d.from) }
    }
  }
  return null
}

/** Workloads whose pod labels carry every label of `selector`: graph nodes, else documents of the YAML texts. */
function selectedWorkloads(env: ManifestDefinitionEnv, selector: Record<string, string>, namespace: string | undefined): { file: string; line: number; kind: string }[] {
  const matches = (labels: Record<string, string> | undefined) => !!labels && Object.entries(selector).every(([k, v]) => labels[k] === v)
  const fromGraph = env.nodes
    .filter((n) => n.file && matches(n.workload?.podLabels) && (!namespace || !n.namespace || n.namespace === namespace))
    .map((n) => ({ file: n.file!, line: n.line ?? 1, kind: n.kind }))
  if (fromGraph.length) return fromGraph
  const out: { file: string; line: number; kind: string }[] = []
  for (const [file, text] of Object.entries(env.files)) {
    if (!/\.ya?ml$/i.test(file)) continue
    for (const d of parseYaml(text)) {
      const kind = docGvk(d.root).kind
      if (!kind || !(WORKLOADS[kind] || kind === "Pod")) continue
      if (matches(stringMap(at(d.root, podLabelsPath(kind))?.node))) out.push({ file, line: lineOfOffset(text, d.root?.from ?? d.from), kind })
    }
  }
  return out
}

/** Refines an object's target to a key path inside its document (falls back to the document line). */
function refine(env: ManifestDefinitionEnv, target: ManifestTarget, path: readonly string[]): ManifestTarget {
  const text = env.files[target.file]
  if (!text) return { file: target.file, line: target.line }
  const doc = docAtLine(text, target.line)
  const hit = doc && at(doc.root, path)
  return { file: target.file, line: hit ? hit.line : target.line }
}

/** Where the name under `pos` of a manifest is declared; null when it names nothing known. */
export function manifestDefinition(text: string, pos: number, env: ManifestDefinitionEnv): ManifestTarget | null {
  const doc = parseYaml(text).find((d) => d.from <= pos && pos <= Math.max(d.to, d.from))
  if (!doc?.root) return null
  const hit = nodeAt(doc.root, pos)
  if (!hit?.entry) return null
  const { kind } = docGvk(doc.root)
  const namespace = scalarText(get(get(doc.root, "metadata"), "namespace"))
  const onKey = pos >= hit.entry.keyFrom && pos <= hit.entry.keyTo
  const mapPath = onKey ? hit.path : hit.path.slice(0, -1)
  const value = scalarText(hit.entry.value)

  // Selector labels → the pod template labels of the workloads it selects (all its labels matching, else this one).
  const selector = (kind === "Service" && mapPath.join(".") === "spec.selector") || (!!kind && !WORKLOADS[kind] && mapPath[mapPath.length - 1] === "matchLabels")
  if (selector && value !== undefined) {
    const label = hit.entry.key
    const all = stringMap(mappingAt(doc.root, mapPath, pos))
    const w = selectedWorkloads(env, all, namespace)[0] ?? selectedWorkloads(env, { [label]: value }, namespace)[0]
    return w ? refine(env, w, [...podLabelsPath(w.kind), label]) : null
  }

  if (onKey || value === undefined || value === "") return null
  const fullPath = hit.path
  for (const r of REFS) {
    if (r.tail.length > fullPath.length || !r.tail.every((s, i) => fullPath[fullPath.length - r.tail.length + i] === s)) continue
    // The mapping holding the reference (its sibling keys name the kind of scaleTargetRef/roleRef/backendRefs).
    const parent = mappingAt(doc.root, mapPath, pos)
    const targetKind = typeof r.kind === "string" ? r.kind : parent ? r.kind(parent) : undefined
    if (!targetKind) return null
    if (r.key) {
      // configMapKeyRef.key → the key in the ConfigMap/Secret named by its sibling name.
      const owner = parent && scalarText(get(parent, "name"))
      if (!owner) return null
      const t = findObject(env, targetKind, owner, namespace)
      if (!t) return null
      const keyed = refine(env, t, ["data", value])
      return keyed.line !== t.line ? keyed : refine(env, t, ["stringData", value])
    }
    const t = findObject(env, targetKind, value, namespace)
    return t ? refine(env, t, []) : null
  }
  return null
}

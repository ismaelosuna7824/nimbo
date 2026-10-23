// Quick fixes of k8s_* diagnostics: mechanical YAML edits of the object the
// problem is about (the document holding its line, the container its field
// path names), written at the right indentation without touching the rest
// of the file. Pure functions over the file text.

import type { QuickFix } from "@/features/code-intel/lint"
import type { YamlMap, YamlNode } from "@/features/code-intel/yaml/tree"
import type { CodeProblem } from "@/lib/api/types"
import { docGvk } from "./k8s-schema"
import {
  documentOfLine,
  lineStartOf,
  mergeAtPath,
  mergeIntoMap,
  nodeAtPath,
  offsetOfLine,
  parseFieldPath,
  setScalar,
  yamlScalar,
  type PathStep,
  type TextChange,
  type YamlValue,
} from "./yaml-edit"

export interface KubeFixEnv {
  /** Image catalog (repository + recommended tag) to pin untagged/latest images. */
  images?: readonly { image: string; tag: string }[]
  /** Repositories of images built from the workflow (kubernetes.builds). */
  localImages?: readonly string[]
}

/** Path of the pod spec in a workload kind. */
export function podSpecPath(kind: string | undefined): string[] {
  if (kind === "CronJob") return ["spec", "jobTemplate", "spec", "template", "spec"]
  if (kind === "Pod") return ["spec"]
  return ["spec", "template", "spec"]
}

/** "nginx:1.27@sha256:…" → repository, tag. */
export function splitImageRef(ref: string): { repo: string; tag?: string; digest?: string } {
  const [noDigest, digest] = ref.split("@")
  const slash = noDigest.lastIndexOf("/")
  const colon = noDigest.lastIndexOf(":")
  if (colon > slash) return { repo: noDigest.slice(0, colon), tag: noDigest.slice(colon + 1), digest }
  return { repo: noDigest, digest }
}

/** "128Mi" × 2 → "256Mi"; null for quantities it can't read. */
export function doubleQuantity(q: string): string | null {
  const m = /^(\d+(?:\.\d+)?)([a-zA-Z]*)$/.exec(q.trim())
  if (!m) return null
  const n = Number(m[1]) * 2
  return `${Number.isInteger(n) ? n : n.toFixed(1)}${m[2]}`
}

interface Located {
  root: YamlMap
  kind?: string
  /** Steps from the root to the pod spec. */
  podSpec: string[]
  /** Steps from the root to the container, when the problem is about one. */
  container?: PathStep[]
  containerNode?: YamlMap
}

/** The object of the problem and, when its field path (or line) names one, its container. */
function locate(pr: CodeProblem, text: string): Located | null {
  const doc = documentOfLine(text, pr.line)
  if (doc?.root?.kind !== "map") return null
  const root = doc.root
  const kind = docGvk(root).kind
  const podSpec = podSpecPath(kind)
  const steps = pr.attribute ? parseFieldPath(pr.attribute) : []
  const ci = steps.findIndex((s, i) => (s === "containers" || s === "initContainers") && i + 1 < steps.length && typeof steps[i + 1] !== "string")
  let container: PathStep[] | undefined
  if (ci >= 0) container = steps.slice(0, ci + 2)
  else if (/^k8s_(latest_tag|local_image|no_requests|no_memory_limit|no_readiness_probe|no_liveness_probe|privileged)$/.test(pr.code) || (pr.code === "k8s_hardening" && !/API token|automountServiceAccountToken/i.test(`${pr.message} ${pr.fix ?? ""}`))) {
    // No field path: the container whose lines hold the problem's line, else the first.
    const list = nodeAtPath(root, [...podSpec, "containers"])
    if (list?.kind === "seq" && list.items.length) {
      const off = offsetOfLine(text, pr.line)
      const i = Math.max(0, list.items.findIndex((it) => off >= lineStartOf(text, it.from) && off <= it.to))
      container = [...podSpec, "containers", i]
    }
  }
  const node = container ? nodeAtPath(root, container) : undefined
  return { root, kind, podSpec, container, containerNode: node?.kind === "map" ? node : undefined }
}

const scalar = (n: YamlNode | null | undefined) => (n?.kind === "scalar" ? n.value : undefined)

function imageFixes(pr: CodeProblem, l: Located, text: string, env: KubeFixEnv): QuickFix[] {
  const c = l.containerNode
  const image = c?.entries.find((e) => e.key === "image")?.value
  if (!c || image?.kind !== "scalar") return []
  const { repo, tag } = splitImageRef(image.value)
  const fixText = `${pr.fix ?? ""} ${pr.detail ?? ""}`
  const local = pr.code === "k8s_local_image" || env.localImages?.some((r) => splitImageRef(r).repo === repo) || /:dev\b/.test(fixText)
  if (local) {
    const changes: TextChange[] = []
    if (tag !== "dev") changes.push(setScalar(image, yamlScalar(`${repo}:dev`)))
    changes.push(...mergeIntoMap(text, c, { imagePullPolicy: "IfNotPresent" }, true))
    return changes.length ? [{ name: `Use ${repo}:dev with imagePullPolicy: IfNotPresent`, changes }] : []
  }
  // A version named by the fix ("Use image: nginx:1.27"), else the catalog's recommended tag.
  const named = /\bimage:?\s+([\w./:@-]+?)\.?(?:\s|$)/.exec(fixText)?.[1]
  const short = repo.replace(/^(docker\.io\/)?(library\/)?/, "")
  const known = env.images?.find((i) => i.image === repo || i.image === short)
  const target = named?.includes(":") && !named.endsWith(":latest") ? named : known && `${repo}:${known.tag}`
  if (!target || target === image.value) return []
  return [{ name: `Pin the image to ${target}`, changes: [setScalar(image, yamlScalar(target))] }]
}

function requestsFix(l: Located, text: string): QuickFix[] {
  if (!l.containerNode) return []
  const changes = mergeAtPath(text, l.containerNode, ["resources"], { requests: { cpu: "100m", memory: "128Mi" } }, false)
  return changes.length ? [{ name: "Request cpu: 100m and memory: 128Mi", changes }] : []
}

function memoryLimitFix(l: Located, text: string): QuickFix[] {
  if (!l.containerNode) return []
  const request = scalar(nodeAtPath(l.containerNode, ["resources", "requests", "memory"]))
  const limit = (request && doubleQuantity(request)) || "256Mi"
  const changes = mergeAtPath(text, l.containerNode, ["resources"], { limits: { memory: limit } }, false)
  return changes.length ? [{ name: `Limit memory to ${limit}`, changes }] : []
}

function probeFix(pr: CodeProblem, l: Located, text: string): QuickFix[] {
  const c = l.containerNode
  const ports = c && nodeAtPath(c, ["ports"])
  const first = ports?.kind === "seq" ? ports.items[0] : undefined
  if (!c || first?.kind !== "map") return []
  const number = scalar(nodeAtPath(first, ["containerPort"]))
  const name = scalar(nodeAtPath(first, ["name"]))
  const port = name ?? number
  if (!port) return []
  const readiness = pr.code === "k8s_no_readiness_probe"
  const key = readiness ? "readinessProbe" : "livenessProbe"
  const check: YamlValue = name && /http/i.test(name) ? { httpGet: { path: "/", port } } : { tcpSocket: { port } }
  const probe: { [key: string]: YamlValue } = readiness ? { ...check, periodSeconds: "5" } : { ...check, initialDelaySeconds: "10", periodSeconds: "10" }
  const changes = mergeIntoMap(text, c, { [key]: probe }, false)
  const how = "httpGet" in check ? `httpGet / on port ${port}` : `tcpSocket on port ${port}`
  return changes.length ? [{ name: `Add a ${readiness ? "readiness" : "liveness"} probe (${how})`, changes }] : []
}

function runAsNonRootFix(l: Located, text: string): QuickFix[] {
  const changes = mergeAtPath(text, l.root, [...l.podSpec, "securityContext"], { runAsNonRoot: "true" }, true)
  return changes.length ? [{ name: "Set securityContext.runAsNonRoot: true", changes }] : []
}

function hardeningFix(pr: CodeProblem, l: Located, text: string): QuickFix[] {
  if (/API token|automountServiceAccountToken/i.test(`${pr.message} ${pr.fix ?? ""}`)) {
    const changes = mergeAtPath(text, l.root, l.podSpec, { automountServiceAccountToken: "false" }, true)
    return changes.length ? [{ name: "Set automountServiceAccountToken: false", changes }] : []
  }
  if (!l.containerNode) return []
  const value: { [key: string]: YamlValue } = {
    allowPrivilegeEscalation: "false",
    readOnlyRootFilesystem: "true",
    capabilities: { drop: ["ALL"] },
  }
  if (/seccomp/i.test(pr.fix ?? "")) value.seccompProfile = { type: "RuntimeDefault" }
  const changes = mergeAtPath(text, l.containerNode, ["securityContext"], value, true)
  return changes.length ? [{ name: "Harden the container's securityContext", changes }] : []
}

function privilegedFix(pr: CodeProblem, l: Located, text: string): QuickFix[] {
  if (!l.containerNode) return []
  const field = /allowPrivilegeEscalation/.test(pr.attribute ?? pr.message) ? "allowPrivilegeEscalation" : /privileged/.test(pr.attribute ?? pr.message) ? "privileged" : null
  if (!field) return []
  const changes = mergeAtPath(text, l.containerNode, ["securityContext"], { [field]: "false" }, true)
  return changes.length ? [{ name: `Set securityContext.${field}: false`, changes }] : []
}

/** "Use apiVersion: apps/v1." / "Use apps/v1 (…)" → "apps/v1". */
export function replacementApiVersion(fix: string): string | undefined {
  return /\bUse (?:apiVersion:?\s*)?((?:[a-z0-9.-]+\/)?v\d+(?:(?:alpha|beta)\d+)?)(?![\w/])/i.exec(fix)?.[1]
}

function apiVersionFix(pr: CodeProblem, l: Located): QuickFix[] {
  const version = replacementApiVersion(`${pr.fix ?? ""} ${pr.detail ?? ""}`)
  const node = l.root.entries.find((e) => e.key === "apiVersion")?.value
  if (!version || node?.kind !== "scalar" || node.value === version) return []
  return [{ name: `Use apiVersion: ${version}`, changes: [setScalar(node, version)] }]
}

function storageClassFix(l: Located, text: string): QuickFix[] {
  const changes = mergeAtPath(text, l.root, ["spec"], { storageClassName: "local-path" }, false)
  return changes.length ? [{ name: "Set storageClassName: local-path", changes }] : []
}

/** The quick fixes of a k8s_* problem in `text` (none for problems without a mechanical fix). */
export function kubeQuickFixes(pr: CodeProblem, text: string, env: KubeFixEnv = {}): QuickFix[] {
  if (!pr.code.startsWith("k8s_")) return []
  const l = locate(pr, text)
  if (!l) return []
  switch (pr.code) {
    case "k8s_latest_tag":
    case "k8s_local_image":
      return imageFixes(pr, l, text, env)
    case "k8s_no_requests":
      return requestsFix(l, text)
    case "k8s_no_memory_limit":
      return memoryLimitFix(l, text)
    case "k8s_no_readiness_probe":
    case "k8s_no_liveness_probe":
      return probeFix(pr, l, text)
    case "k8s_run_as_root":
      return runAsNonRootFix(l, text)
    case "k8s_hardening":
      return hardeningFix(pr, l, text)
    case "k8s_privileged":
      return privilegedFix(pr, l, text)
    case "k8s_deprecated_api":
    case "k8s_removed_api":
      return apiVersionFix(pr, l)
    case "k8s_pvc_storage_class":
      return storageClassFix(l, text)
  }
  return []
}

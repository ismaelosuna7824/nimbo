// Helm chart files: the Chart.yaml schema (apiVersion v2), and the schema of
// a chart's values files inferred from its values.yaml (every key with its
// default and comment) merged with values.schema.json when the chart has
// one. Inferred parts never judge (extra keys and other types are allowed,
// like Helm does); only values.schema.json's rules are validated, as Helm
// does on install.

import type { JsonSchema } from "@/features/code-intel/yaml/schema"
import { parseYaml, type YamlNode } from "@/features/code-intel/yaml/tree"

export const HELM_DOCS = "https://helm.sh/docs/topics/charts/"

const str = (description: string): JsonSchema => ({ type: "string", description })

const MAINTAINER: JsonSchema = {
  type: "object",
  properties: { name: str("Maintainer's name."), email: str("Maintainer's email."), url: str("Maintainer's URL.") },
  required: ["name"],
  additionalProperties: false,
}

const DEPENDENCY: JsonSchema = {
  type: "object",
  description: "A chart this chart depends on (vendored under charts/ by helm dependency build).",
  properties: {
    name: str("Name of the chart."),
    version: str("Version or SemVer range (^1.2.0, ~15.5)."),
    repository: str("Repository URL (https://…, oci://…) or file://../path; alias @repo of a repo added with helm repo add."),
    condition: str("Values path(s) of booleans enabling the chart (postgresql.enabled)."),
    tags: { type: "array", items: { type: "string" }, description: "Tags enabling/disabling the chart together (values tags.<tag>)." },
    "import-values": { type: "array", description: "Values imported from the child chart into this one.", items: {} },
    alias: str("Name the dependency is known by (values key) instead of its name."),
  },
  required: ["name"],
  additionalProperties: false,
}

/** Chart.yaml (chart API v2, Helm 3+). */
export const CHART_SCHEMA: JsonSchema = {
  type: "object",
  properties: {
    apiVersion: { type: "string", enum: ["v2", "v1"], description: "The chart API version: v2 for Helm 3+ charts (v1: Helm 2, legacy)." },
    name: str("The name of the chart."),
    version: str("The chart's SemVer version (bump on every change)."),
    kubeVersion: str("SemVer range of supported Kubernetes versions (>=1.25.0-0)."),
    description: str("A single-sentence description."),
    type: { type: "string", enum: ["application", "library"], description: "application (default) renders objects; library only provides templates to other charts." },
    keywords: { type: "array", items: { type: "string" }, description: "Keywords about the chart." },
    home: str("The project's home page URL."),
    sources: { type: "array", items: { type: "string" }, description: "Source code URLs." },
    dependencies: { type: "array", items: DEPENDENCY, description: "Charts this chart depends on." },
    maintainers: { type: "array", items: MAINTAINER, description: "The chart's maintainers." },
    icon: str("URL of an SVG or PNG icon."),
    appVersion: str("Version of the app the chart deploys (informational; templates often default the image tag to it)."),
    deprecated: { type: "boolean", description: "Whether the chart is deprecated." },
    annotations: { type: "object", additionalProperties: { type: "string" }, description: "Annotations (artifacthub.io/…)." },
  },
  required: ["apiVersion", "name", "version"],
  additionalProperties: false,
}

/** Reference page of a Chart.yaml field. */
export function chartDocsUrl(path: readonly string[], key: string): string {
  const top = path[0] ?? key
  if (top === "dependencies") return `${HELM_DOCS}#chart-dependencies`
  if (top === "type") return `${HELM_DOCS}#chart-types`
  return `${HELM_DOCS}#the-chartyaml-file`
}

/** The comment block right above 1-based `line` (same indentation), without "#" and helm-docs "-- " markers. */
export function commentAbove(lines: readonly string[], line: number): string | undefined {
  const indent = lines[line - 1]?.search(/\S/) ?? -1
  const out: string[] = []
  for (let i = line - 2; i >= 0; i--) {
    const l = lines[i]
    const at = l.search(/\S/)
    if (at < 0 || l[at] !== "#" || (indent >= 0 && at !== indent)) break
    out.unshift(l.slice(at + 1).replace(/^ ?(-- ?)?/, ""))
  }
  // Commented-out YAML (# foo: bar) above a key is an example, not a description; keep it anyway, it documents the key.
  const text = out.join("\n").trim()
  return text || undefined
}

/** A short YAML rendering of a node for docs ("nginx", "{…3 keys}", "[…2 items]"). */
export function previewNode(text: string, node: YamlNode | null): string {
  if (!node) return "null"
  if (node.kind === "scalar") return node.block ? node.value : node.quoted ? JSON.stringify(node.value) : node.value || '""'
  const src = text.slice(node.from, node.to)
  return src.length > 400 ? `${src.slice(0, 400)}…` : src
}

function infer(text: string, lines: readonly string[], node: YamlNode | null, description?: string): JsonSchema {
  const preview = node && node.kind === "scalar" ? previewNode(text, node) : undefined
  const desc = [description, preview !== undefined ? `Default: \`${preview.split("\n")[0]}\`` : undefined].filter(Boolean).join("\n\n") || undefined
  if (node?.kind === "map") {
    const properties: Record<string, JsonSchema> = {}
    for (const e of node.entries) {
      if (e.key === "<<") continue
      properties[e.key] = infer(text, lines, e.value, commentAbove(lines, e.line))
    }
    return { type: "object", properties, "x-kubernetes-preserve-unknown-fields": true, description: desc }
  }
  if (node?.kind === "seq") {
    const first = node.items[0]
    return { type: "array", items: first && first.kind === "map" ? infer(text, lines, first) : {}, description: desc }
  }
  // Scalars: any type (a values file may override a string with a number); booleans keep their default (true/false offered).
  if (node?.kind === "scalar" && !node.quoted && /^(true|false)$/.test(node.value)) return { description: desc, default: node.value === "true" }
  return { description: desc }
}

/** The values schema inferred from a values.yaml text (keys, defaults, comments); judges nothing. */
export function inferValuesSchema(valuesText: string): JsonSchema {
  const doc = parseYaml(valuesText)[0]
  const lines = valuesText.split("\n")
  const s = infer(valuesText, lines, doc?.root ?? null)
  return s.type === "object" ? s : { type: "object", properties: {}, "x-kubernetes-preserve-unknown-fields": true }
}

/** values.schema.json with the inferred keys (defaults, comments) merged in where it doesn't say. */
export function mergeValuesSchemas(declared: JsonSchema, inferred: JsonSchema): JsonSchema {
  if (declared.$ref || declared.oneOf || declared.anyOf || declared.allOf) return declared
  const out: JsonSchema = { ...declared, description: declared.description ?? inferred.description }
  if (inferred.properties && (declared.type === "object" || declared.properties || declared.type === undefined)) {
    const props: Record<string, JsonSchema> = { ...declared.properties }
    for (const [k, v] of Object.entries(inferred.properties)) props[k] = props[k] ? mergeValuesSchemas(props[k], v) : v
    out.properties = props
  }
  return out
}

/** The schema of a chart's values files: values.schema.json (when valid JSON) merged with the inferred one. */
export function valuesSchema(valuesText: string | undefined, schemaJson: string | undefined): JsonSchema {
  const inferred = inferValuesSchema(valuesText ?? "")
  if (!schemaJson) return inferred
  try {
    const declared = JSON.parse(schemaJson) as JsonSchema
    return declared && typeof declared === "object" ? mergeValuesSchemas(declared, inferred) : inferred
  } catch {
    return inferred
  }
}

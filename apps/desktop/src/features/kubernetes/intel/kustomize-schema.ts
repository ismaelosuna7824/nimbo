// The kustomization.yaml schema (kustomize.config.k8s.io/v1beta1
// Kustomization and v1alpha1 Component), bundled: the fields kustomize v5
// reads, with their docs, plus completion of the files and directories a
// kustomization refers to (resources, components, patches, generators).

import type { Completion } from "@codemirror/autocomplete"
import type { PathSegment, ValueContext } from "@/features/code-intel/yaml/context"
import type { JsonSchema } from "@/features/code-intel/yaml/schema"
import { S_VALUES } from "@/features/code-intel/yaml/schema-intel"
import { get, parseYaml, scalarText } from "@/features/code-intel/yaml/tree"
import { dirname, joinPath, relativePath } from "./files"

export const KUSTOMIZE_DOCS = "https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/"

const str = (description: string): JsonSchema => ({ type: "string", description })
const strList = (description: string): JsonSchema => ({ type: "array", items: { type: "string" }, description })
const strMap = (description: string): JsonSchema => ({ type: "object", additionalProperties: { type: "string" }, description })

const TARGET: JsonSchema = {
  type: "object",
  description: "Which resources the patch applies to (every field optional; all must match).",
  properties: {
    group: str("API group (apps, networking.k8s.io; empty for core)."),
    version: str("API version (v1)."),
    kind: str("Kind (Deployment)."),
    name: str("Name or regular expression."),
    namespace: str("Namespace."),
    labelSelector: str("Label selector (app=web,tier!=db)."),
    annotationSelector: str("Annotation selector."),
  },
  additionalProperties: false,
}

const GENERATOR_OPTIONS: JsonSchema = {
  type: "object",
  description: "Options of the generated ConfigMaps/Secrets.",
  properties: {
    labels: strMap("Labels added to the generated objects."),
    annotations: strMap("Annotations added to the generated objects."),
    disableNameSuffixHash: { type: "boolean", description: "Don't append the content hash to the name (rollouts no longer follow content changes)." },
    immutable: { type: "boolean", description: "Make the generated objects immutable." },
  },
  additionalProperties: false,
}

const GENERATOR: JsonSchema = {
  type: "object",
  description: "A ConfigMap or Secret generated from files, literals or env files; its name gets a content hash so workloads roll on change.",
  properties: {
    name: str("Name of the generated object (references to it are rewritten with the hash)."),
    namespace: str("Namespace of the generated object."),
    behavior: { type: "string", enum: ["create", "replace", "merge"], description: "create (default), or replace/merge an object of the same name from a base." },
    files: strList("Files whose content becomes a key each (key=path to rename)."),
    literals: strList("KEY=value pairs."),
    envs: strList("Env files (KEY=value per line) whose entries become keys."),
    type: str("Secret type (Opaque, kubernetes.io/tls…)."),
    options: GENERATOR_OPTIONS,
  },
  required: ["name"],
  additionalProperties: false,
}

const LABELS_ENTRY: JsonSchema = {
  type: "object",
  description: "Labels to add, and where.",
  properties: {
    pairs: strMap("The labels."),
    includeSelectors: { type: "boolean", description: "Also add them to selectors (changing selectors of existing Deployments fails)." },
    includeTemplates: { type: "boolean", description: "Also add them to pod templates." },
    fields: { type: "array", description: "Extra field specs to write the labels to.", items: { type: "object", properties: { path: str("Field path."), kind: str("Kind."), create: { type: "boolean" } } } },
  },
  additionalProperties: false,
}

const PATCH: JsonSchema = {
  type: "object",
  description: "A strategic-merge or JSON 6902 patch, from a file (path) or inline (patch).",
  properties: {
    path: str("Patch file, relative to this kustomization."),
    patch: str("Inline patch (a YAML document or a JSON 6902 list)."),
    target: TARGET,
    options: { type: "object", properties: { allowNameChange: { type: "boolean" }, allowKindChange: { type: "boolean" } }, additionalProperties: false },
  },
  additionalProperties: false,
}

const HELM_CHART: JsonSchema = {
  type: "object",
  description: "A Helm chart inflated into resources (kustomize build --enable-helm).",
  properties: {
    name: str("Chart name."),
    repo: str("Chart repository URL."),
    version: str("Chart version."),
    releaseName: str("Release name."),
    namespace: str("Namespace of the release."),
    valuesFile: str("Values file, relative to this kustomization."),
    valuesInline: { type: "object", description: "Values written inline.", "x-kubernetes-preserve-unknown-fields": true },
    valuesMerge: { type: "string", enum: ["merge", "override", "replace"], description: "How valuesInline combine with valuesFile." },
    includeCRDs: { type: "boolean", description: "Also render the chart's CRDs." },
    skipTests: { type: "boolean" },
    additionalValuesFiles: strList("More values files."),
    apiVersions: strList("--api-versions for the chart's Capabilities."),
    kubeVersion: str("--kube-version for the chart's Capabilities."),
  },
  required: ["name"],
  additionalProperties: false,
}

const FIELDS: Record<string, JsonSchema> = {
  apiVersion: { type: "string", enum: ["kustomize.config.k8s.io/v1beta1", "kustomize.config.k8s.io/v1alpha1"], description: "kustomize.config.k8s.io/v1beta1 (Component: v1alpha1)." },
  kind: { type: "string", enum: ["Kustomization", "Component"], description: "Kustomization, or Component for reusable pieces listed under components:." },
  metadata: { type: "object", description: "Name of the kustomization (optional).", properties: { name: str("Name."), annotations: strMap("Annotations.") } },
  resources: strList("Manifest files, directories with a kustomization, or remote URLs whose objects are included."),
  bases: { ...strList("Deprecated: list them under resources."), deprecated: true },
  components: strList("Component directories applied after resources."),
  crds: strList("CRD files whose schemas teach kustomize how to merge custom resources."),
  namespace: str("Namespace set on every namespaced object."),
  namePrefix: str("Prefix added to every object's name (references are rewritten)."),
  nameSuffix: str("Suffix added to every object's name (references are rewritten)."),
  commonLabels: { ...strMap("Labels added to every object and selector (prefer labels: with includeSelectors)."), deprecated: true },
  labels: { type: "array", description: "Labels added to objects (optionally selectors and templates).", items: LABELS_ENTRY },
  commonAnnotations: strMap("Annotations added to every object."),
  patches: { type: "array", description: "Patches (strategic merge or JSON 6902), by file or inline, with an optional target.", items: PATCH },
  patchesStrategicMerge: { ...strList("Deprecated: list them under patches (path: …)."), deprecated: true },
  patchesJson6902: { type: "array", description: "Deprecated: list them under patches (path + target).", deprecated: true, items: PATCH },
  images: {
    type: "array",
    description: "Image overrides: a new name, tag or digest for every container using the image.",
    items: {
      type: "object",
      properties: {
        name: str("The image as written in the manifests (without tag)."),
        newName: str("Replacement image name."),
        newTag: str("Replacement tag."),
        digest: str("Replacement digest (sha256:…)."),
      },
      required: ["name"],
      additionalProperties: false,
    },
  },
  replicas: {
    type: "array",
    description: "Replica counts by workload name.",
    items: {
      type: "object",
      properties: { name: str("Deployment/StatefulSet/ReplicaSet name."), count: { type: "integer", description: "Replicas." } },
      required: ["name", "count"],
      additionalProperties: false,
    },
  },
  configMapGenerator: { type: "array", description: "Generated ConfigMaps.", items: GENERATOR },
  secretGenerator: { type: "array", description: "Generated Secrets.", items: GENERATOR },
  generatorOptions: GENERATOR_OPTIONS,
  helmCharts: { type: "array", description: "Helm charts inflated into resources (needs --enable-helm).", items: HELM_CHART },
  helmGlobals: { type: "object", description: "Where charts are cached.", properties: { chartHome: str("Chart directory."), configHome: str("Helm config directory.") }, additionalProperties: false },
  replacements: { type: "array", description: "Copy a field's value from one object to fields of others.", items: { type: "object", "x-kubernetes-preserve-unknown-fields": true, properties: { path: str("Replacement file."), source: { type: "object", "x-kubernetes-preserve-unknown-fields": true }, targets: { type: "array", items: { type: "object", "x-kubernetes-preserve-unknown-fields": true } } } } },
  vars: { type: "array", description: "Deprecated: use replacements.", deprecated: true, items: { type: "object", "x-kubernetes-preserve-unknown-fields": true } },
  configurations: strList("Transformer configuration files (field specs for custom kinds)."),
  generators: strList("Generator plugin configurations."),
  transformers: strList("Transformer plugin configurations."),
  validators: strList("Validator plugin configurations."),
  openapi: { type: "object", description: "OpenAPI schema for custom kinds.", "x-kubernetes-preserve-unknown-fields": true },
  buildMetadata: { type: "array", description: "Annotations kustomize records on objects (originAnnotations, managedByLabel…).", items: { type: "string", enum: ["originAnnotations", "transformerAnnotations", "managedByLabel"] } },
  sortOptions: { type: "object", description: "Output order (legacy or fifo).", "x-kubernetes-preserve-unknown-fields": true },
}

/** The bundled Kustomization schema. */
export const KUSTOMIZATION_SCHEMA: JsonSchema = { type: "object", properties: FIELDS, additionalProperties: false }

/** Reference anchor of a kustomization field. */
export function kustomizeDocsUrl(path: readonly PathSegment[], key: string): string {
  const top = path[0] ?? key
  return FIELDS[top] ? `${KUSTOMIZE_DOCS}${top.toLowerCase()}/` : KUSTOMIZE_DOCS
}

/** What a list of a kustomization refers to: YAML files, directories (other kustomizations) or any file. */
function refKind(path: readonly PathSegment[]): "resource" | "dir" | "yaml" | "file" | null {
  switch (path.join(".")) {
    case "resources.[]":
    case "bases.[]":
      return "resource"
    case "components.[]":
      return "dir"
    case "patches.[].path":
    case "patchesJson6902.[].path":
    case "patchesStrategicMerge.[]":
    case "crds.[]":
    case "configurations.[]":
    case "generators.[]":
    case "transformers.[]":
    case "validators.[]":
    case "replacements.[].path":
    case "helmCharts.[].valuesFile":
    case "helmCharts.[].additionalValuesFiles.[]":
      return "yaml"
    case "configMapGenerator.[].files.[]":
    case "secretGenerator.[].files.[]":
    case "configMapGenerator.[].envs.[]":
    case "secretGenerator.[].envs.[]":
      return "file"
  }
  return null
}

/**
 * Files and directories a kustomization at `path` can refer to at a value position, relative to it: YAML files
 * (not itself), and directories holding a kustomization (resources, components).
 */
export function kustomizePathValues(ctx: ValueContext, path: string, files: readonly string[]): Completion[] {
  const kind = refKind(ctx.path)
  if (!kind) return []
  const dir = dirname(path)
  const out: Completion[] = []
  const seen = new Set<string>()
  const add = (target: string, type: "file" | "dir") => {
    // Directories: relative to a placeholder child, then the placeholder dropped ("" → "../..").
    const rel = type === "dir" ? relativePath(dir, target ? `${target}/_` : "_").slice(0, -2) || "." : relativePath(dir, target)
    if (seen.has(rel)) return
    seen.add(rel)
    out.push({ label: rel, type: type === "dir" ? "namespace" : "text", detail: type === "dir" ? "kustomization" : undefined, section: S_VALUES })
  }
  for (const f of files) {
    if (f === path) continue
    const isKustomization = /(^|\/)(kustomization\.ya?ml|Kustomization)$/.test(f)
    if (isKustomization) {
      // A directory with a kustomization (a base, a component), never this kustomization's own directory.
      const d = dirname(f)
      if ((kind === "resource" || kind === "dir") && d !== dir) add(d, "dir")
      continue
    }
    if (kind === "dir") continue
    if (kind === "file" || /\.ya?ml$/i.test(f)) add(f, "file")
  }
  return out
}

/**
 * Files the kustomizations use as patches (patches[].path, patchesStrategicMerge, patchesJson6902[].path): partial
 * objects, validated without required fields.
 */
export function kustomizePatchFiles(kustomizations: Record<string, string>): Set<string> {
  const out = new Set<string>()
  for (const [path, text] of Object.entries(kustomizations)) {
    const root = parseYaml(text)[0]?.root
    const refs: (string | undefined)[] = []
    for (const key of ["patches", "patchesJson6902", "patchesStrategicMerge"]) {
      const list = get(root, key)
      if (list?.kind !== "seq") continue
      for (const it of list.items) refs.push(it.kind === "scalar" ? it.value : scalarText(get(it, "path")))
    }
    for (const ref of refs) {
      // Inline patches (a YAML document as a list item) are not file references.
      if (!ref || ref.includes("\n")) continue
      const p = joinPath(dirname(path), ref)
      if (p) out.add(p)
    }
  }
  return out
}

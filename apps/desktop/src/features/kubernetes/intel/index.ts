// Language features of a Kubernetes workspace's code: manifests (keys and
// values from the Kubernetes OpenAPI of the pinned release, per document's
// apiVersion/kind), kustomizations (bundled schema, file references), Helm
// charts (Chart.yaml schema, values files from the chart's values.yaml and
// values.schema.json, template actions), go to definition across objects
// and quick fixes of k8s_* problems. Completion, hover and definitions run in
// the client; diagnostics come from the daemon's kind-aware check (render
// problems and k8s_* rules for the file), like Terraform and Docker.

import { autocompletion, type CompletionContext } from "@codemirror/autocomplete"
import type { Extension } from "@codemirror/state"
import { hoverTooltip } from "@codemirror/view"
import { definitionExtension } from "@/features/code-intel/definition"
import { checkLinter, intelTheme, type CheckOptions, type QuickFixProvider } from "@/features/code-intel/lint"
import { yamlSchemaIntel } from "@/features/code-intel/yaml"
import { S_VALUES } from "@/features/code-intel/yaml/schema-intel"
import { schemasAt, type JsonSchema } from "@/features/code-intel/yaml/schema"
import { parseYaml, type YamlNode } from "@/features/code-intel/yaml/tree"
import { loadDockerImages } from "@/features/docker/intel/images"
import { docNode } from "@/features/panels/terraform/intel/format"
import type { CodeProblem, DockerImage, KubernetesModel } from "@/lib/api/types"
import { manifestDefinition } from "./definition"
import { inChart, kubeFileKind, type KubeFileInfo } from "./files"
import { kubeQuickFixes } from "./fixes"
import { CHART_SCHEMA, chartDocsUrl, valuesSchema } from "./helm-schema"
import { templateCompletions, templateDefinition, templateHover, type ChartEnv } from "./helm-template"
import { docGvk, gvkValues, headerKeys, kindDocsUrl, knownFieldValues, loadKubeSchema, type KindSchema, type KubeSchemaIndex } from "./k8s-schema"
import { KUSTOMIZATION_SCHEMA, kustomizeDocsUrl, kustomizePatchFiles, kustomizePathValues } from "./kustomize-schema"
import { KubeProject } from "./project"

export interface KubernetesIntelOptions {
  workflowId: string
  /** Workflow path of the edited file. */
  path: string
  /** Unsaved text of the other files (checks see the project as edited). */
  buffers: () => Record<string, string>
  /** Opens a file at a line (go to definition across files). */
  openFile: (path: string, opts: { line?: number }) => void
  /** The workflow's Kubernetes model (sources, derived graph, files rendering read). */
  kube: () => KubernetesModel | undefined
}

const KUSTOMIZATION_FILE = /(^|\/)(kustomization\.ya?ml|Kustomization)$/

let index: KubeSchemaIndex | undefined
let images: DockerImage[] | undefined

/** The schema index, kept once loaded for the synchronous completion hooks. */
function kubeSchema(): Promise<KubeSchemaIndex | null> {
  return loadKubeSchema().then(
    (i) => (index = i),
    () => null,
  )
}

/** The root of the document holding `pos`. */
function rootAt(text: string, pos: number): YamlNode | null {
  return parseYaml(text).find((d) => d.from <= pos && pos <= Math.max(d.to, d.from))?.root ?? null
}

function source(pr: CodeProblem): string {
  if (pr.code === "syntax" || pr.code === "k8s_yaml") return "YAML syntax"
  if (pr.code === "k8s_render" || pr.code === "k8s_dependency") return "Render"
  return "Kubernetes"
}

/** Editor extensions for one Kubernetes file of a workflow; null for files it doesn't handle. */
export function kubernetesIntel(opts: KubernetesIntelOptions): Extension[] | null {
  const project = new KubeProject(opts.workflowId, opts.buffers)
  const info = kubeFileKind(opts.path, opts.kube(), project.paths())
  if (!info) return null
  const check = { workflowId: opts.workflowId, path: opts.path, buffers: opts.buffers }

  switch (info.kind) {
    case "manifest":
      return manifestIntel(opts, project, check)
    case "kustomization":
      return [
        ...yamlSchemaIntel({
          schemaFor: () => KUSTOMIZATION_SCHEMA,
          docsUrl: kustomizeDocsUrl,
          docsLabel: "Open the Kustomize reference ↗",
          values: (ctx) => kustomizePathValues(ctx, opts.path, project.paths()),
          source: "Kustomize",
        }),
        checkLinter(check, source),
      ]
    case "chart":
      return [
        ...yamlSchemaIntel({ schemaFor: () => CHART_SCHEMA, docsUrl: chartDocsUrl, docsLabel: "Open the Helm chart reference ↗", source: "Chart.yaml" }),
        checkLinter(check, source),
      ]
    case "values":
      return valuesIntel(info, project, check)
    case "template":
      return templateIntel(opts, info, project, check)
  }
}

/** Quick fixes of k8s_* problems (image tags from the catalog, local builds from the workflow). */
function fixesFor(opts: KubernetesIntelOptions): QuickFixProvider {
  void loadDockerImages().then(
    (list) => (images = list),
    () => {},
  )
  return (pr, ctx) => kubeQuickFixes(pr, ctx.text, { images, localImages: opts.kube()?.builds?.map((b) => b.image) })
}

function manifestIntel(opts: KubernetesIntelOptions, project: KubeProject, check: CheckOptions): Extension[] {
  void kubeSchema()
  const rendered = new Set(opts.kube()?.files ?? [])
  const yamlPaths = () => project.paths().filter((p) => /\.ya?ml$/i.test(p))
  project.prefetch(project.paths().filter((p) => KUSTOMIZATION_FILE.test(p)))
  // Files read by the render are checked by the daemon (k8s_schema, same schema): only others are validated here.
  const validate = !rendered.has(opts.path)
  const isPatch = () => kustomizePatchFiles(project.cachedTexts(project.paths().filter((p) => KUSTOMIZATION_FILE.test(p)))).has(opts.path)

  return [
    ...yamlSchemaIntel({
      schemaFor: async (doc) => {
        const idx = await kubeSchema()
        if (!idx || doc.root?.kind !== "map") return null
        const { apiVersion, kind } = docGvk(doc.root)
        if (apiVersion?.startsWith("kustomize.config.k8s.io/")) return KUSTOMIZATION_SCHEMA
        if (!apiVersion || !kind) return idx.header
        return idx.schemaFor(apiVersion, kind, isPatch())
      },
      docsUrl: (_path, _key, schema?: JsonSchema) => {
        const gvk = (schema as KindSchema | undefined)?.["x-nimbo-gvk"]
        return gvk && index ? kindDocsUrl(index.version, gvk) : undefined
      },
      docsLabel: "Open the Kubernetes API reference ↗",
      keys: (ctx, text) => (index ? headerKeys(index, ctx, rootAt(text, ctx.from)) : null),
      values: (ctx, text) => {
        const root = rootAt(text, ctx.from)
        return [...(index ? gvkValues(index, ctx, root) : []), ...knownFieldValues(ctx, docGvk(root).kind)]
      },
      validate,
      source: "Kubernetes schema",
    }),
    checkLinter(check, source, fixesFor(opts)),
    definitionExtension(
      opts.path,
      (state, pos) => {
        const text = state.doc.toString()
        const files = { ...project.cachedTexts(yamlPaths()), [opts.path]: text }
        return manifestDefinition(text, pos, { path: opts.path, nodes: opts.kube()?.graph.nodes ?? [], files })
      },
      opts.openFile,
    ),
  ]
}

function valuesIntel(info: KubeFileInfo, project: KubeProject, check: CheckOptions): Extension[] {
  const chart = info.chart ?? ""
  const valuesPath = inChart(chart, "values.yaml")
  const schemaPath = inChart(chart, "values.schema.json")
  // Helm validates values against values.schema.json on install: only then are values judged here.
  const validate = project.paths().includes(schemaPath)
  let schema: JsonSchema = valuesSchema(undefined, undefined)
  const load = async () => (schema = valuesSchema(await project.text(valuesPath), validate ? await project.text(schemaPath) : undefined))
  return [
    ...yamlSchemaIntel({
      schemaFor: load,
      // Booleans of values.yaml offer true/false.
      values: (ctx) =>
        schemasAt(schema, ctx.path).some((s) => typeof s.default === "boolean")
          ? ["true", "false"].map((v) => ({ label: v, type: "enum", section: S_VALUES }))
          : [],
      validate,
      source: "values.schema.json",
    }),
    checkLinter(check, source),
  ]
}

function templateIntel(opts: KubernetesIntelOptions, info: KubeFileInfo, project: KubeProject, check: CheckOptions): Extension[] {
  const chart = info.chart ?? ""
  const valuesPath = inChart(chart, "values.yaml")
  const schemaPath = inChart(chart, "values.schema.json")
  const templatesDir = `${inChart(chart, "templates")}/`
  const templatePaths = () => project.paths().filter((p) => p.startsWith(templatesDir) && /\.(ya?ml|tpl)$/i.test(p))
  project.prefetch([valuesPath, ...templatePaths()])

  /** The chart's values and templates; `doc` is the edited template's current text. */
  const envOf = (doc: string, texts: Record<string, string>, values: string | undefined, schemaJson: string | undefined): ChartEnv => ({
    valuesPath,
    valuesText: values,
    valuesSchema: valuesSchema(values, schemaJson),
    templates: { ...texts, [opts.path]: doc },
  })
  const env = async (doc: string): Promise<ChartEnv> => {
    const paths = templatePaths()
    const [texts, values, schemaJson] = await Promise.all([
      project.texts(paths),
      project.text(valuesPath),
      project.paths().includes(schemaPath) ? project.text(schemaPath) : undefined,
    ])
    return envOf(doc, texts, values, schemaJson)
  }
  const envSync = (doc: string): ChartEnv => envOf(doc, project.cachedTexts(templatePaths()), project.cached(valuesPath), project.cached(schemaPath))

  return [
    autocompletion({
      override: [
        async (cx: CompletionContext) => {
          if (cx.state.readOnly) return null
          const text = cx.state.doc.toString()
          return templateCompletions(text, cx.pos, cx.explicit, await env(text))
        },
      ],
      icons: true,
      maxRenderedOptions: 120,
    }),
    hoverTooltip(async (view, pos) => {
      const text = view.state.doc.toString()
      const h = templateHover(text, pos, await env(text))
      if (!h) return null
      const dom = docNode({ title: h.title, badges: h.badges, description: h.description, extra: h.extra, docsUrl: h.docsUrl, docsLabel: h.docsLabel })
      return { pos: h.from, end: h.to, above: true, create: () => ({ dom }) }
    }),
    definitionExtension(opts.path, (state, pos) => templateDefinition(state.doc.toString(), pos, envSync(state.doc.toString())), opts.openFile),
    checkLinter(check, source),
    intelTheme,
  ]
}

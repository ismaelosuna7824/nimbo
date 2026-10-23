// Language features of a Docker workspace's code: compose files (keys and
// values from the Compose Specification schema, project names, catalog
// images, go to definition), Dockerfiles (instructions, flags, stages,
// ARG/ENV, images, go to definition), .env files (variables the compose
// files read) and .dockerignore files (patterns for the stack). Completion,
// hover and navigation run in the client; diagnostics come from the daemon
// (POST /workflows/{id}/terraform/check, kind-aware: YAML/schema/Dockerfile
// syntax and dk_* checks), debounced, with quick fixes for the dk_* rules
// that have a mechanical fix.

import { autocompletion, type CompletionContext, type CompletionResult } from "@codemirror/autocomplete"
import type { Extension } from "@codemirror/state"
import { hoverTooltip } from "@codemirror/view"
import { api } from "@/lib/api/client"
import type { ComposeGraph, DockerImage } from "@/lib/api/types"
import { definitionExtension } from "@/features/code-intel/definition"
import { checkLinter, intelTheme } from "@/features/code-intel/lint"
import { dirName } from "@/features/panels/terraform/file-model"
import { composeCompletions, composeHover, type ComposeEnv, type NameSection } from "./compose-completion"
import { composeDefinitionAt } from "./compose-definition"
import { loadComposeSchema } from "./compose-schema"
import { DOCKERIGNORE_NAME, dockerignoreCompletions, stacksIn } from "./dockerignore"
import { dockerfileCompletions, dockerfileDefinitionAt, dockerfileHover } from "./dockerfile"
import { ENV_FILE_NAME, envFileCompletions, envFileHover, type EnvFileEnv } from "./env-file"
import { dockerQuickFixes } from "./quick-fixes"

let imagesPromise: Promise<DockerImage[]> | undefined

/** The image catalog, fetched once per session. */
export function loadDockerImages(): Promise<DockerImage[]> {
  imagesPromise ??= api.dockerImages().catch((err: unknown) => {
    imagesPromise = undefined
    throw err
  })
  return imagesPromise
}

export interface DockerIntelOptions {
  workflowId: string
  /** Workflow path of the edited file. */
  path: string
  /** Unsaved text of the other files (checks see the project as edited). */
  buffers: () => Record<string, string>
  /** The workflow's compose files and derived graph (names declared in the other files). */
  compose: () => { files: string[]; graph: ComposeGraph } | undefined
  /** Every workflow path (stored and pending). */
  files: () => string[]
  /** A workflow file's current text (unsaved buffer or stored content). */
  read: (path: string) => Promise<string>
  /** Opens a file of the workflow at a line (go to definition). */
  openFile: (path: string, opts: { line: number }) => void
  /** Stages new or changed files (quick fixes that create a .dockerignore). */
  writeFiles: (files: Record<string, string>) => void
}

const COMPOSE_NAME = /(^|\/)((docker-)?compose|compose\.override|docker-compose\.override)(\.[\w-]+)?\.ya?ml$/i
const DOCKERFILE_NAME = /(^|\/)(Dockerfile|Containerfile)(\.[\w.-]+)?$|\.dockerfile$/i

export type DockerFileKind = "compose" | "dockerfile" | "env" | "dockerignore"

/** What the editor offers for a file of a Docker workspace. */
export function dockerFileKind(path: string, composeFiles: readonly string[] = []): DockerFileKind | null {
  if (composeFiles.includes(path) || COMPOSE_NAME.test(path)) return "compose"
  if (DOCKERIGNORE_NAME.test(path)) return "dockerignore"
  if (DOCKERFILE_NAME.test(path)) return "dockerfile"
  if (ENV_FILE_NAME.test(path)) return "env"
  return null
}

const SECTION_OF: Record<string, NameSection> = {
  service: "services",
  network: "networks",
  volume: "volumes",
  secret: "secrets",
  config: "configs",
}

/** Names the project declares, from the derived graph (every compose file). */
function graphNames(graph: ComposeGraph | undefined): ComposeEnv["names"] {
  const out: ComposeEnv["names"] = {}
  for (const n of graph?.nodes ?? []) {
    const section = SECTION_OF[n.kind === "external" ? (n.externalKind ?? "") : n.kind]
    if (section) (out[section] ??= []).push(n.name)
  }
  return out
}

/** Hover tooltip from a synchronous or async provider of {from, to, dom}. */
function hover(get: (text: string, pos: number) => Promise<{ from: number; to: number; dom: HTMLElement } | null> | { from: number; to: number; dom: HTMLElement } | null) {
  return hoverTooltip(async (view, pos) => {
    const h = await get(view.state.doc.toString(), pos)
    return h && { pos: h.from, end: h.to, above: true, create: () => ({ dom: h.dom }) }
  })
}

function completion(source: (text: string, pos: number, explicit: boolean) => Promise<CompletionResult | null> | CompletionResult | null, max = 80) {
  return autocompletion({
    override: [(cx: CompletionContext) => (cx.state.readOnly ? null : source(cx.state.doc.toString(), cx.pos, cx.explicit))],
    icons: true,
    maxRenderedOptions: max,
  })
}

/** Editor extensions for one compose file, Dockerfile, .env or .dockerignore; null for other files. */
export function dockerIntel(opts: DockerIntelOptions): Extension[] | null {
  const composeFiles = () => opts.compose()?.files ?? []
  const kind = dockerFileKind(opts.path, composeFiles())
  if (!kind) return null
  void loadDockerImages().catch(() => {})
  const images = () => loadDockerImages().catch(() => [] as DockerImage[])
  // Content of other files navigation needs synchronously (.env lines, extends files), refreshed as they are read.
  const contents: Record<string, string> = {}
  const refresh = (paths: string[]) => {
    for (const p of paths) void opts.read(p).then((t) => (contents[p] = t), () => {})
  }
  const exists = (p: string) => opts.files().includes(p)
  const fixEnv = { images: [] as DockerImage[], files: opts.files }
  void images().then((i) => (fixEnv.images = i))
  const fixHost = { writeFiles: opts.writeFiles }

  if (kind === "dockerfile") {
    return [
      completion(async (text, pos, explicit) => dockerfileCompletions(text, pos, explicit, await images())),
      hover(dockerfileHover),
      definitionExtension(
        opts.path,
        (state, pos) => {
          const from = dockerfileDefinitionAt(state.doc.toString(), pos)
          return from === null ? null : { file: opts.path, from }
        },
        opts.openFile,
      ),
      checkLinter(opts, (pr) => (pr.code === "syntax" || pr.code === "dk_dockerfile_syntax" ? "Dockerfile syntax" : "Docker"), dockerQuickFixes("dockerfile", fixEnv), fixHost),
      intelTheme,
    ]
  }

  if (kind === "env") {
    const envOf = async (): Promise<EnvFileEnv> => {
      const files = composeFiles()
      const texts = await Promise.all(files.map((f) => opts.read(f).catch(() => "")))
      return { compose: Object.fromEntries(files.map((f, i) => [f, texts[i]])), images: await images() }
    }
    return [
      completion(async (text, pos, explicit) => envFileCompletions(text, pos, explicit, await envOf())),
      hover(async (text, pos) => envFileHover(text, pos, await envOf())),
      intelTheme,
    ]
  }

  if (kind === "dockerignore") {
    return [
      completion((text, pos, explicit) => dockerignoreCompletions(text, pos, explicit, stacksIn(dirName(opts.path), opts.files()))),
      checkLinter(opts, () => "Docker", dockerQuickFixes("compose", fixEnv), fixHost),
      intelTheme,
    ]
  }

  void loadComposeSchema().catch(() => {})
  refresh([...composeFiles().filter((f) => f !== opts.path), `${dirName(opts.path) ? `${dirName(opts.path)}/` : ""}.env`])
  const env = async (): Promise<ComposeEnv | null> => {
    const schema = await loadComposeSchema().catch(() => null)
    if (!schema) return null
    return { schema, images: await images(), names: graphNames(opts.compose()?.graph) }
  }
  return [
    completion(async (text, pos, explicit) => {
      const e = await env()
      return e && composeCompletions(text, pos, explicit, e)
    }, 120),
    hover(async (text, pos) => {
      const e = await env()
      return e && composeHover(text, pos, e)
    }),
    definitionExtension(
      opts.path,
      (state, pos) =>
        composeDefinitionAt(state.doc.toString(), pos, {
          path: opts.path,
          graph: opts.compose()?.graph,
          exists,
          read: (p) => opts.buffers()[p] ?? contents[p],
        }),
      opts.openFile,
    ),
    checkLinter(opts, (pr) => (pr.code === "syntax" || pr.code === "dk_yaml" ? "YAML syntax" : "Compose"), dockerQuickFixes("compose", fixEnv), fixHost),
    intelTheme,
  ]
}

// Compose file completion and hover: keys and values from the Compose
// Specification schema, the project's service/network/volume/secret/config
// names and image names from the Docker image catalog.

import type { Completion, CompletionResult, CompletionSection } from "@codemirror/autocomplete"
import type { DockerImage } from "@/lib/api/types"
import { docNode } from "@/features/panels/terraform/intel/format"
import { topLevelNames, type PathSegment } from "@/features/code-intel/yaml/context"
import type { JsonSchema } from "@/features/code-intel/yaml/schema"
import { schemaCompletions, schemaHover, type SchemaIntelOptions, type YamlHover } from "@/features/code-intel/yaml/schema-intel"

export interface ComposeEnv {
  schema: JsonSchema
  images: DockerImage[]
  /** Names declared in the project's other compose files (merged with the edited text's own). */
  names: Partial<Record<NameSection, string[]>>
}

export type NameSection = "services" | "networks" | "volumes" | "secrets" | "configs"

const S_NAMES: CompletionSection = { name: "In this project", rank: 0 }
const S_IMAGES: CompletionSection = { name: "Images", rank: 1 }

const DOCS = "https://docs.docker.com/reference/compose-file/"
const DOCS_LABEL = "Open the Compose file reference ↗"

/** Values the schema leaves as free strings but that only take a few. */
const KNOWN_VALUES: Record<string, { value: string; description: string }[]> = {
  restart: [
    { value: "no", description: "Never restart (default)" },
    { value: "always", description: "Always restart until removed" },
    { value: "on-failure", description: "Restart when the container exits with an error" },
    { value: "unless-stopped", description: "Restart unless it was stopped on purpose" },
  ],
  pull_policy: [
    { value: "missing", description: "Pull only when the image is not available locally (default)" },
    { value: "always", description: "Always pull" },
    { value: "never", description: "Never pull; fail when missing" },
    { value: "build", description: "Build the image" },
    { value: "daily", description: "Pull when the local image is older than a day" },
    { value: "weekly", description: "Pull when the local image is older than a week" },
  ],
  network_mode: [
    { value: "bridge", description: "The default bridge network" },
    { value: "host", description: "Share the host's network stack" },
    { value: "none", description: "No networking" },
  ],
  driver: [
    { value: "bridge", description: "Single-host network (default)" },
    { value: "overlay", description: "Multi-host network (Swarm)" },
    { value: "local", description: "Local volume driver (default)" },
  ],
}

/** The section of names a value at this path refers to, if any. */
function nameSectionOf(path: readonly PathSegment[]): NameSection | null {
  if (path[0] !== "services" || path.length < 3) return null
  const rel = path.slice(2)
  const last = rel[rel.length - 1]
  const head = rel[0]
  if ((head === "depends_on" || head === "links") && (last === "[]" || rel.length === 1)) return "services"
  if (head === "extends" && last === "service") return "services"
  if (head === "networks" && rel.length <= 2) return "networks"
  if ((head === "secrets" || head === "configs") && rel.length <= 2) return head
  if (head === "volumes_from" && last === "[]") return "services"
  return null
}

/** Keys of a mapping that are free names taken from the project (depends_on: {db: …}, networks: {back: …}). */
function nameKeysOf(path: readonly PathSegment[]): NameSection | null {
  if (path[0] !== "services" || path.length !== 3) return null
  const head = path[2]
  if (head === "depends_on") return "services"
  if (head === "networks") return "networks"
  return null
}

function docsUrl(path: readonly PathSegment[], key: string): string {
  const [top, , sub] = path
  switch (top) {
    case undefined:
      return `${DOCS}${["services", "networks", "volumes", "secrets", "configs"].includes(key) ? `${key}/` : "version-and-name/"}`
    case "services":
      if (sub === "build") return `${DOCS}build/#${key}`
      if (sub === "deploy") return `${DOCS}deploy/#${key}`
      if (sub === "develop") return `${DOCS}develop/#${key}`
      return `${DOCS}services/#${path.length > 2 ? path[2] : key}`
    case "networks":
    case "volumes":
    case "secrets":
    case "configs":
      return `${DOCS}${top}/#${key}`
    default:
      return DOCS
  }
}

/** Names of a section: the edited text's own plus the project's other files. */
function namesOf(text: string, env: ComposeEnv, section: NameSection): string[] {
  const own = (topLevelNames(text)[section] ?? []).map((n) => n.name)
  return [...new Set([...own, ...(env.names[section] ?? [])])]
}

function imageCompletions(images: DockerImage[]): Completion[] {
  return images.map((img) => ({
    label: `${img.image}:${img.tag}`,
    type: "constant",
    detail: img.label,
    section: S_IMAGES,
    info: () => docNode({ title: `${img.image}:${img.tag}`, badges: [img.category, ...img.ports.map((p) => `:${p}`)], description: img.description }),
  }))
}

/** The compose dialect on top of the schema completion: project names, catalog images, free-string values. */
function composeOptions(env: ComposeEnv): SchemaIntelOptions {
  return {
    docsUrl,
    docsLabel: DOCS_LABEL,
    knownValues: KNOWN_VALUES,
    keys: (ctx, text) => {
      const section = nameKeysOf(ctx.path)
      if (!section) return null
      const service = ctx.path[1]
      const names = namesOf(text, env, section).filter((n) => !(section === "services" && n === service))
      return {
        from: ctx.from,
        options: names.map((n) => ({ label: n, type: "variable", section: S_NAMES, apply: `${n}:\n${" ".repeat(ctx.indent + 2)}` })),
        validFor: /^[\w.-]*$/,
      }
    },
    values: (ctx, text) => {
      const options: Completion[] = []
      const service = ctx.path[0] === "services" ? ctx.path[1] : undefined
      const section = nameSectionOf(ctx.path)
      if (section) {
        for (const n of namesOf(text, env, section)) {
          if (section === "services" && n === service) continue
          options.push({ label: n, type: "variable", section: S_NAMES })
        }
      }
      // Short volume syntax: named volumes ("data:/var/lib/…").
      if (ctx.path[0] === "services" && ctx.path[2] === "volumes" && ctx.path.length === 4 && ctx.key === "") {
        for (const n of namesOf(text, env, "volumes")) options.push({ label: `${n}:`, type: "variable", section: S_NAMES, detail: "named volume" })
      }
      if (ctx.key === "image" && ctx.path[0] === "services" && ctx.path.length === 3) options.push(...imageCompletions(env.images))
      return options
    },
  }
}

/** Completions at `pos` in a compose file; null when none apply. */
export function composeCompletions(text: string, pos: number, explicit: boolean, env: ComposeEnv): CompletionResult | null {
  return schemaCompletions(text, pos, explicit, env.schema, composeOptions(env))
}

export type ComposeHover = YamlHover

/** Docs of the key (or catalog image) under the cursor. */
export function composeHover(text: string, pos: number, env: ComposeEnv): ComposeHover | null {
  const k = schemaHover(text, pos, env.schema, { docsUrl, docsLabel: DOCS_LABEL })
  if (k) return k
  // image: <ref> → the catalog entry.
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const lineEnd = text.indexOf("\n", pos) < 0 ? text.length : text.indexOf("\n", pos)
  const line = text.slice(lineStart, lineEnd)
  const m = /^(\s*image\s*:\s*["']?)([^\s"'#]+)/.exec(line)
  if (!m) return null
  const from = lineStart + m[1].length
  const to = from + m[2].length
  if (pos < from || pos > to) return null
  const [repo, tag] = splitImage(m[2])
  const img = env.images.find((i) => i.image === repo)
  if (!img) return null
  return {
    from,
    to,
    dom: docNode({
      title: m[2],
      badges: [img.category, ...img.ports.map((p) => `:${p}`), ...(tag && tag !== img.tag ? [`catalog tag ${img.tag}`] : [])],
      description: img.description,
      docsUrl: repo.includes("/") ? `https://hub.docker.com/r/${repo}` : `https://hub.docker.com/_/${repo}`,
      docsLabel: "Open on Docker Hub ↗",
    }),
  }
}

/** "nginx:1.27-alpine" → ["nginx", "1.27-alpine"]; registries with ports keep their colon. */
export function splitImage(ref: string): [string, string | undefined] {
  const noDigest = ref.split("@")[0]
  const slash = noDigest.lastIndexOf("/")
  const colon = noDigest.lastIndexOf(":")
  if (colon > slash) return [noDigest.slice(0, colon), noDigest.slice(colon + 1)]
  return [noDigest, undefined]
}

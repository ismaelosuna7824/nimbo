// Go to definition in compose files: service names (depends_on, links,
// extends, volumes_from), networks, named volumes, secrets and configs to
// their declaration (this file or another compose file of the project), and
// paths (build contexts, dockerfile, env_file, include, secret/config files)
// to the file they name. ${VAR} goes to its line in the project's .env.

import type { ComposeGraph } from "@/lib/api/types"
import type { DefinitionTarget } from "@/features/code-intel/definition"
import { get, nodeAt, parseYaml, scalarText, type YamlNode } from "@/features/code-intel/yaml/tree"
import { dirName, joinPath } from "@/features/panels/terraform/file-model"

export interface ComposeDefinitionEnv {
  /** Workflow path of the compose file. */
  path: string
  /** Declarations of the other compose files (graph nodes carry file and line). */
  graph?: ComposeGraph
  /** Whether a workflow file exists (stored or pending). */
  exists: (path: string) => boolean
  /** Text of a workflow file, when available (unsaved buffer or loaded content). */
  read?: (path: string) => string | undefined
}

type Section = "services" | "networks" | "volumes" | "secrets" | "configs"

const GRAPH_KIND: Record<Section, string> = { services: "service", networks: "network", volumes: "volume", secrets: "secret", configs: "config" }

/** Where `name` is declared under a top-level section: this text first, then the project's other compose files. */
function declaration(text: string, section: Section, name: string, env: ComposeDefinitionEnv): DefinitionTarget | null {
  for (const d of parseYaml(text)) {
    const sec = get(d.root, section)
    if (sec?.kind !== "map") continue
    const e = sec.entries.find((x) => x.key === name)
    if (e) return { file: env.path, from: e.keyFrom, line: e.line }
  }
  const n = env.graph?.nodes.find(
    (x) => x.name === name && (x.kind === GRAPH_KIND[section] || (x.kind === "external" && x.externalKind === GRAPH_KIND[section])),
  )
  return n?.file && n.file !== env.path ? { file: n.file, line: n.line ?? 1 } : null
}

/** A path written in the compose file, resolved from its folder, if that file exists. */
function fileTarget(rel: string, env: ComposeDefinitionEnv): DefinitionTarget | null {
  const p = joinPath(dirName(env.path), rel.trim())
  return p !== null && env.exists(p) ? { file: p, line: 1 } : null
}

/** The Dockerfile a build context (and optional dockerfile:) names. */
function dockerfileTarget(context: string, dockerfile: string | undefined, env: ComposeDefinitionEnv): DefinitionTarget | null {
  if (/^[a-z]+:\/\//i.test(context) || context.startsWith("git@")) return null
  const dir = joinPath(dirName(env.path), context)
  if (dir === null) return null
  const p = joinPath(dir, dockerfile ?? "Dockerfile")
  return p !== null && env.exists(p) ? { file: p, line: 1 } : null
}

/** "${VAR}" / "$VAR" around `pos` in a scalar → the line of the project's .env that sets it. */
function envVarTarget(text: string, pos: number, env: ComposeDefinitionEnv): DefinitionTarget | null {
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const lineEnd = text.indexOf("\n", pos) < 0 ? text.length : text.indexOf("\n", pos)
  const line = text.slice(lineStart, lineEnd)
  for (const m of line.matchAll(/\$\{([A-Za-z_]\w*)[^}]*\}|\$([A-Za-z_]\w*)/g)) {
    const from = lineStart + m.index
    if (pos < from || pos > from + m[0].length) continue
    if (line[m.index - 1] === "$") return null
    const name = m[1] ?? m[2]
    const dotenv = joinPath(dirName(env.path), ".env")
    if (dotenv === null || !env.exists(dotenv)) return null
    const content = env.read?.(dotenv)
    const no = content?.split("\n").findIndex((l) => new RegExp(`^\\s*(export\\s+)?${name}\\s*=`).test(l))
    return { file: dotenv, line: no !== undefined && no >= 0 ? no + 1 : 1 }
  }
  return null
}

/** The name an entry of a service field refers to ("db", "db:alias", "container:x", "data:/var/lib/…"). */
function refName(field: string, value: string): string | null {
  switch (field) {
    case "links":
      return value.split(":")[0]
    case "volumes_from":
      if (value.startsWith("container:")) return null
      return value.replace(/^service:/, "").split(":")[0]
    case "volumes": {
      const source = value.split(":")[0]
      // Paths (./data, /srv, ~/x) are bind mounts, not named volumes.
      return /^[\w][\w.-]*$/.test(source) && value.includes(":") ? source : null
    }
    default:
      return value
  }
}

const SECTION_OF_FIELD: Record<string, Section> = {
  depends_on: "services",
  links: "services",
  volumes_from: "services",
  networks: "networks",
  volumes: "volumes",
  secrets: "secrets",
  configs: "configs",
}

/** Where the name or path at `pos` of a compose file is declared, or null. */
export function composeDefinitionAt(text: string, pos: number, env: ComposeDefinitionEnv): DefinitionTarget | null {
  const doc = parseYaml(text).find((d) => d.from <= pos && pos <= d.to)
  const hit = doc && nodeAt(doc.root, pos)
  if (!hit) return null
  const { node, path, entry } = hit
  const onKey = !!entry && pos >= entry.keyFrom && pos <= entry.keyTo && node.kind === "map"
  const value = node.kind === "scalar" ? node.value : undefined
  if (value !== undefined && /\$\{?[A-Za-z_]/.test(value)) {
    const t = envVarTarget(text, pos, env)
    if (t) return t
  }

  // Top level: include: [a.yaml] / include: [{path: a.yaml}], secrets/configs file:.
  if (path[0] === "include" && value !== undefined && (path.length === 2 || path[2] === "path" || path[2] === "env_file")) return fileTarget(value, env)
  if ((path[0] === "secrets" || path[0] === "configs") && path.length === 3 && path[2] === "file" && value !== undefined) return fileTarget(value, env)
  if (path[0] !== "services" || path.length < 2) return null

  const service = path[1]
  const field = path[2]
  const rel = path.slice(2)

  // Keys naming another object: depends_on: {db: …}, networks: {back: …}.
  if (onKey && entry && path.length === 3 && (field === "depends_on" || field === "networks")) {
    return declaration(text, SECTION_OF_FIELD[field], entry.key, env)
  }
  if (value === undefined) return null

  // extends: {service: x, file: y}
  if (field === "extends") {
    const ext = get(serviceNode(doc.root, service), "extends")
    const file = scalarText(get(ext, "file"))
    if (rel[1] === "file") return fileTarget(value, env)
    if (rel[1] === "service" || rel.length === 1) {
      if (file) {
        const p = joinPath(dirName(env.path), file)
        if (p === null || !env.exists(p)) return null
        const other = env.read?.(p)
        const t = other ? declaration(other, "services", value, { ...env, path: p, graph: undefined }) : null
        return t ?? { file: p, line: 1 }
      }
      return declaration(text, "services", value, env)
    }
  }

  // Paths: build context/dockerfile, env_file.
  if (field === "build") {
    const build = get(serviceNode(doc.root, service), "build")
    const context = build?.kind === "scalar" ? build.value : (scalarText(get(build, "context")) ?? ".")
    const dockerfile = build?.kind === "map" ? scalarText(get(build, "dockerfile")) : undefined
    if (rel.length === 1 || rel[1] === "context" || rel[1] === "dockerfile") return dockerfileTarget(context, dockerfile, env)
    return null
  }
  if (field === "env_file" && (rel.length <= 2 || rel[2] === "path")) return fileTarget(value, env)

  // Names in lists or long syntax: depends_on: [db], networks: [back], secrets: [{source: x}], volumes: [{source: data}].
  const section = SECTION_OF_FIELD[field]
  if (!section) return null
  if (rel.length === 2 && rel[1] === "[]") {
    const name = refName(field, value)
    return name ? declaration(text, section, name, env) : null
  }
  if (rel.length === 3 && rel[1] === "[]" && rel[2] === "source" && (field === "secrets" || field === "configs" || field === "volumes")) {
    return declaration(text, section, value, env)
  }
  return null
}

function serviceNode(root: YamlNode | null, name: string): YamlNode | null | undefined {
  return get(get(root, "services"), name)
}

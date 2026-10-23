// .env files of a Docker workspace: completion of the variables the compose
// files read (${VAR}, ${VAR:-default}, $VAR) that the file doesn't set yet,
// and of the variables catalog images expect (POSTGRES_PASSWORD…) for the
// services that use them; hover shows where a key is used.

import type { Completion, CompletionResult, CompletionSection } from "@codemirror/autocomplete"
import type { DockerImage } from "@/lib/api/types"
import { docNode } from "@/features/panels/terraform/intel/format"
import { splitImage } from "./compose-completion"

const S_USED: CompletionSection = { name: "Used by the compose files", rank: 0 }
const S_IMAGES: CompletionSection = { name: "Expected by images", rank: 1 }

export const ENV_FILE_NAME = /(^|\/)(\.env(\.[\w.-]+)?|[\w.-]+\.env)$/

export interface VarUse {
  name: string
  /** Default written in the reference (${VAR:-x}). */
  fallback?: string
  /** Required (${VAR:?message} / ${VAR?message}). */
  required: boolean
  file: string
  line: number
}

/** Variables a compose file reads; "$$" escapes are skipped. */
export function composeVariables(text: string, file: string): VarUse[] {
  const out: VarUse[] = []
  text.split("\n").forEach((l, i) => {
    if (/^\s*#/.test(l)) return
    for (const m of l.matchAll(/\$\{([A-Za-z_]\w*)(?:(:?[-?+])([^}]*))?\}|\$([A-Za-z_]\w*)/g)) {
      if (l[m.index - 1] === "$") continue
      const op = m[2] ?? ""
      out.push({
        name: m[1] ?? m[4],
        fallback: op.endsWith("-") ? m[3] : undefined,
        required: op.endsWith("?"),
        file,
        line: i + 1,
      })
    }
  })
  return out
}

/** Keys a .env text sets. */
export function envKeys(text: string): Set<string> {
  const out = new Set<string>()
  for (const l of text.split("\n")) {
    const m = /^\s*(?:export\s+)?([A-Za-z_][\w.-]*)\s*=/.exec(l)
    if (m) out.add(m[1])
  }
  return out
}

/** Images of the compose files' services (image: lines). */
function imagesOf(texts: string[]): string[] {
  const out: string[] = []
  for (const t of texts) for (const m of t.matchAll(/^\s*image:\s*["']?([^\s"'#]+)/gm)) out.push(splitImage(m[1])[0])
  return out
}

export interface EnvFileEnv {
  /** Text of every compose file of the project, by path. */
  compose: Record<string, string>
  images: DockerImage[]
}

/** Completions at `pos` of a .env file: a key at the start of a line. */
export function envFileCompletions(text: string, pos: number, explicit: boolean, env: EnvFileEnv): CompletionResult | null {
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const before = text.slice(lineStart, pos)
  const m = /^\s*(?:export\s+)?([A-Za-z_][\w.-]*)?$/.exec(before)
  if (!m) return null
  const prefix = m[1] ?? ""
  if (!explicit && !prefix) return null
  const set = envKeys(text.slice(0, lineStart) + text.slice(text.indexOf("\n", pos) < 0 ? text.length : text.indexOf("\n", pos)))
  const options = new Map<string, Completion>()
  const uses = Object.entries(env.compose).flatMap(([file, t]) => composeVariables(t, file))
  for (const u of uses) {
    if (set.has(u.name) || options.has(u.name)) continue
    const all = uses.filter((x) => x.name === u.name)
    options.set(u.name, {
      label: u.name,
      type: "variable",
      section: S_USED,
      detail: u.required ? "required" : u.fallback !== undefined ? `default ${u.fallback || '""'}` : undefined,
      boost: u.required || u.fallback === undefined ? 2 : 0,
      apply: `${u.name}=${u.fallback ?? ""}`,
      info: () =>
        docNode({
          title: u.name,
          badges: [u.required ? "required" : u.fallback !== undefined ? "has a default" : "no default"],
          description: `Read by ${all.map((x) => `\`${x.file}:${x.line}\``).join(", ")}.`,
        }),
    })
  }
  const used = new Set(imagesOf(Object.values(env.compose)))
  for (const img of env.images) {
    if (!used.has(img.image)) continue
    for (const e of img.env ?? []) {
      if (set.has(e.key) || options.has(e.key)) continue
      options.set(e.key, {
        label: e.key,
        type: "variable",
        section: S_IMAGES,
        detail: img.label,
        apply: `${e.key}=${e.secret ? "" : e.value}`,
        info: () => docNode({ title: e.key, badges: [img.label, ...(e.secret ? ["secret"] : [])], description: e.description }),
      })
    }
  }
  return options.size ? { from: pos - prefix.length, options: [...options.values()], validFor: /^[\w.-]*$/ } : null
}

/** Where the key under the cursor is used by the compose files. */
export function envFileHover(text: string, pos: number, env: EnvFileEnv): { from: number; to: number; dom: HTMLElement } | null {
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const lineEnd = text.indexOf("\n", pos) < 0 ? text.length : text.indexOf("\n", pos)
  const m = /^(\s*(?:export\s+)?)([A-Za-z_][\w.-]*)\s*=/.exec(text.slice(lineStart, lineEnd))
  if (!m) return null
  const from = lineStart + m[1].length
  const to = from + m[2].length
  if (pos < from || pos > to) return null
  const uses = Object.entries(env.compose).flatMap(([file, t]) => composeVariables(t, file)).filter((u) => u.name === m[2])
  return {
    from,
    to,
    dom: docNode({
      title: m[2],
      description: uses.length ? `Read by ${uses.map((u) => `\`${u.file}:${u.line}\``).join(", ")}.` : "Not read by the compose files (a service's env_file may still pass it to a container).",
    }),
  }
}

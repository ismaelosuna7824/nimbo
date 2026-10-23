// Quick fixes of dk_* problems that have a mechanical fix, offered as lint
// actions in the editor: pin an image tag, add init / restart / log
// rotation to a service, bind a database port to 127.0.0.1, wait for a
// database to be healthy, exec-form CMD/ENTRYPOINT, apt/apk/pip without
// leftovers, COPY instead of ADD, and a .dockerignore with the stack's
// defaults. Each fix edits the text the problem was found in.

import type { CodeProblem, DockerImage } from "@/lib/api/types"
import { lineRange, type QuickFix, type QuickFixContext } from "@/features/code-intel/lint"
import { get, parseYaml, type YamlMap } from "@/features/code-intel/yaml/tree"
import { dirName, joinPath } from "@/features/panels/terraform/file-model"
import { splitImage } from "./compose-completion"
import { dockerignoreDefaults, stacksIn } from "./dockerignore"

export interface DockerFixEnv {
  images: DockerImage[]
  /** Workflow paths (stored and pending). */
  files: () => string[]
}

type Edit = NonNullable<QuickFix["changes"]>[number]

const column = (text: string, offset: number) => offset - (text.lastIndexOf("\n", offset - 1) + 1)

/** Tag choices for an image: the catalog's tag and, when it is more specific, its major version. */
function pinnedTags(repo: string, images: DockerImage[]): string[] {
  const img = images.find((i) => i.image === repo)
  if (!img) return []
  const out = [img.tag]
  const major = /^v?(\d+)[.-]/.exec(img.tag)
  if (major) {
    const flavor = /-(alpine|slim|bookworm|bullseye|jammy|noble)\b.*$/.exec(img.tag)?.[0] ?? ""
    const short = `${major[0].startsWith("v") ? "v" : ""}${major[1]}${flavor}`
    if (short !== img.tag) out.push(short)
  }
  return out
}

function pinFixes(text: string, line: number, re: RegExp, images: DockerImage[]): QuickFix[] {
  const { from, to } = lineRange(text, line)
  const m = re.exec(text.slice(from, to))
  if (!m) return []
  const ref = m[2]
  if (ref.includes("$") || ref.includes("@")) return []
  const [repo, tag] = splitImage(ref)
  if (tag && tag !== "latest") return []
  const start = from + m[1].length
  return pinnedTags(repo, images).map((t) => ({ name: `Pin ${repo}:${t}`, changes: [{ from: start, to: start + ref.length, insert: `${repo}:${t}` }] }))
}

/** The service (and its mapping) declared at or containing a 1-based line of a compose file. */
function serviceAt(text: string, line: number): { name: string; key: { from: number }; body: YamlMap } | null {
  for (const d of parseYaml(text)) {
    const services = get(d.root, "services")
    if (services?.kind !== "map") continue
    for (const e of services.entries) {
      const end = e.value ? text.slice(0, e.value.to).split("\n").length : e.line
      if (line >= e.line && line <= end && e.value?.kind === "map") return { name: e.key, key: { from: e.keyFrom }, body: e.value }
    }
  }
  return null
}

/** Inserts `lines` (relative indentation, two spaces per level) as the first keys of a service. */
function addToService(text: string, svc: { body: YamlMap }, lines: string[]): Edit {
  const first = svc.body.entries[0]
  const col = column(text, first.keyFrom)
  const at = text.lastIndexOf("\n", first.keyFrom - 1) + 1
  // After a merge key (<<: *defaults) the explicit keys read better; insert after it.
  const merge = svc.body.entries.find((e) => e.key === "<<")
  const pos = merge ? text.indexOf("\n", merge.value?.to ?? merge.keyTo) + 1 || text.length : at
  return { from: pos, to: pos, insert: lines.map((l) => " ".repeat(col) + l).join("\n") + "\n" }
}

function composeFixes(pr: CodeProblem, ctx: QuickFixContext, env: DockerFixEnv): QuickFix[] {
  const { text } = ctx
  switch (pr.code) {
    case "dk_latest_tag":
      return pinFixes(text, pr.line, /^(\s*(?:-\s+)?image\s*:\s*["']?)([^\s"'#]+)/, env.images)
    case "dk_no_init": {
      const svc = serviceAt(text, pr.line)
      return svc && !get(svc.body, "init") ? [{ name: "Add init: true", changes: [addToService(text, svc, ["init: true"])] }] : []
    }
    case "dk_no_restart": {
      const svc = serviceAt(text, pr.line)
      if (!svc || get(svc.body, "restart")) return []
      return [
        { name: "Add restart: unless-stopped", changes: [addToService(text, svc, ["restart: unless-stopped"])] },
        { name: "Add restart: on-failure", changes: [addToService(text, svc, ["restart: on-failure"])] },
      ]
    }
    case "dk_log_rotation": {
      const svc = serviceAt(text, pr.line)
      const logging = svc && svc.body.entries.find((e) => e.key === "logging")
      if (!logging || logging.value?.kind !== "map") return []
      const options = logging.value.entries.find((e) => e.key === "options")
      const col = column(text, logging.value.entries[0].keyFrom)
      const pad = " ".repeat(col)
      if (options?.value?.kind === "map") {
        const oc = column(text, options.value.entries[0].keyFrom)
        const add = [!get(options.value, "max-size") && 'max-size: "10m"', !get(options.value, "max-file") && 'max-file: "3"'].filter(Boolean)
        const at = text.indexOf("\n", options.value.to)
        const pos = at < 0 ? text.length : at
        return [{ name: "Rotate logs (max-size 10m, max-file 3)", changes: [{ from: pos, to: pos, insert: add.map((l) => `\n${" ".repeat(oc)}${l}`).join("") }] }]
      }
      if (options) return []
      const at = text.indexOf("\n", logging.value.to)
      const pos = at < 0 ? text.length : at
      return [
        {
          name: "Rotate logs (max-size 10m, max-file 3)",
          changes: [{ from: pos, to: pos, insert: `\n${pad}options:\n${pad}  max-size: "10m"\n${pad}  max-file: "3"` }],
        },
      ]
    }
    case "dk_db_published": {
      const svc = serviceAt(text, pr.line)
      const ports = svc && get(svc.body, "ports")
      if (ports?.kind !== "seq") return []
      const changes: Edit[] = []
      for (const it of ports.items) {
        if (it.kind === "scalar") {
          const v = it.value
          // "5432:5432", "5432" (random host port) — not "127.0.0.1:…" or "[::1]:…".
          if (!/^\d+(-\d+)?(:\d+(-\d+)?)?(\/\w+)?$/.test(v) || !v.includes(":")) continue
          const q = it.quoted ? text[it.from] : '"'
          changes.push({ from: it.from, to: it.to, insert: `${q}127.0.0.1:${v}${q}` })
        } else if (it.kind === "map" && it.entries.some((e) => e.key === "published") && !get(it, "host_ip")) {
          const first = it.entries[0]
          const col = column(text, first.keyFrom)
          const end = text.indexOf("\n", it.to)
          const pos = end < 0 ? text.length : end
          changes.push({ from: pos, to: pos, insert: `\n${" ".repeat(col)}host_ip: 127.0.0.1` })
        }
      }
      return changes.length ? [{ name: "Publish on 127.0.0.1 only", changes }] : []
    }
    case "dk_depends_healthy": {
      const svc = serviceAt(text, pr.line)
      const dep = /starts as soon as (\S+)'s container/.exec(pr.message)?.[1]
      const deps = svc && get(svc.body, "depends_on")
      if (!dep || !deps) return []
      if (deps.kind === "seq") {
        // List form → mapping form, the database waits for healthy, the others keep service_started.
        const names = deps.items.map((i) => (i.kind === "scalar" ? i.value : null))
        if (names.some((n) => n === null)) return []
        const entry = svc.body.entries.find((e) => e.key === "depends_on")!
        const col = column(text, entry.keyFrom) + 2
        const body = names
          .map((n) => `${" ".repeat(col)}${n}:\n${" ".repeat(col + 2)}condition: ${n === dep ? "service_healthy" : "service_started"}`)
          .join("\n")
        const flow = text[deps.from] === "["
        const from = flow ? entry.keyTo + 1 : text.lastIndexOf("\n", deps.from - 1)
        const end = text.indexOf("\n", deps.to)
        return [{ name: `Wait until ${dep} is healthy`, changes: [{ from, to: end < 0 ? text.length : end, insert: `\n${body}` }] }]
      }
      if (deps.kind === "map") {
        const e = deps.entries.find((x) => x.key === dep)
        const cond = e?.value?.kind === "map" ? e.value.entries.find((x) => x.key === "condition") : undefined
        if (cond?.value?.kind === "scalar") return [{ name: `Wait until ${dep} is healthy`, changes: [{ from: cond.value.from, to: cond.value.to, insert: "service_healthy" }] }]
        if (e && !e.value) {
          const end = text.indexOf("\n", e.keyTo)
          const pos = end < 0 ? text.length : end
          return [{ name: `Wait until ${dep} is healthy`, changes: [{ from: pos, to: pos, insert: `\n${" ".repeat(column(text, e.keyFrom) + 2)}condition: service_healthy` }] }]
        }
      }
      return []
    }
    case "dk_dockerignore":
      return dockerignoreFix(pr, ctx, env)
  }
  return []
}

/** Heavy folders the message lists: "sends web/node_modules (12 MB), .venv (3 MB) to the builder". */
function heavyDirs(message: string): string[] {
  return [...message.matchAll(/([^\s,]+) \(\d[^)]*\)/g)].map((m) => m[1])
}

function dockerignoreFix(pr: CodeProblem, ctx: QuickFixContext, env: DockerFixEnv): QuickFix[] {
  const dirs = heavyDirs(pr.message)
  if (/doesn't exclude them/.test(pr.message)) {
    // Reported on the .dockerignore itself: append what it misses.
    if (!/dockerignore$/.test(ctx.path) || !dirs.length) return []
    const end = ctx.text.length
    const sep = ctx.text === "" || ctx.text.endsWith("\n") ? "" : "\n"
    return [{ name: `Exclude ${dirs.join(", ")}`, changes: [{ from: end, to: end, insert: `${sep}${dirs.join("\n")}\n` }] }]
  }
  const context = /build context (\S+) sends/.exec(pr.message)?.[1]
  if (!context) return []
  const dir = joinPath(dirName(ctx.path), context)
  if (dir === null) return []
  const target = dir ? `${dir}/.dockerignore` : ".dockerignore"
  if (env.files().includes(target)) return []
  return [{ name: `Create ${target} with the stack's defaults`, files: { [target]: dockerignoreDefaults(stacksIn(dir, env.files()), dirs) } }]
}

/** The logical line (continuations joined) starting at a 1-based line: its range and text. */
function instruction(text: string, line: number): { from: number; to: number; text: string } {
  const { from } = lineRange(text, line)
  let to = lineRange(text, line).to
  for (let n = line; /\\\s*$/.test(text.slice(lineRange(text, n).from, lineRange(text, n).to)); n++) {
    const next = lineRange(text, n + 1)
    if (next.from >= text.length) break
    to = next.to
  }
  return { from, to, text: text.slice(from, to) }
}

/** Words of a simple shell command, or null when it needs a shell (pipes, &&, variables, globs, redirections, quotes). */
function simpleWords(cmd: string): string[] | null {
  const c = cmd.replace(/\\\n/g, " ").trim()
  if (!c || /[|&;<>$`*?(){}\\'"]/.test(c)) return null
  return c.split(/\s+/)
}

function dockerfileFixes(pr: CodeProblem, ctx: QuickFixContext, env: DockerFixEnv): QuickFix[] {
  const { text } = ctx
  switch (pr.code) {
    case "dk_latest_tag":
      return pinFixes(text, pr.line, /^(\s*FROM\s+(?:--\S+\s+)*)([^\s#]+)/i, env.images)
    case "dk_shell_form": {
      const ins = instruction(text, pr.line)
      const m = /^(\s*(CMD|ENTRYPOINT)\s+)([\s\S]*)$/i.exec(ins.text)
      if (!m || m[3].trimStart().startsWith("[")) return []
      const words = simpleWords(m[3])
      if (!words) return []
      const exec = `${m[1]}[${words.map((w) => JSON.stringify(w)).join(", ")}]`
      return [{ name: `Use the exec form: ${m[2].toUpperCase()} [${words.map((w) => JSON.stringify(w)).join(", ")}]`, changes: [{ from: ins.from, to: ins.to, insert: exec }] }]
    }
    case "dk_apt_install": {
      const ins = instruction(text, pr.line)
      let t = ins.text
      if (!/--no-install-recommends/.test(t)) t = t.replace(/\bapt-get(\s+-\S+)*\s+install\b/, (s) => `${s} --no-install-recommends`)
      if (!/rm -rf \/var\/lib\/apt\/lists/.test(t)) t = `${t.replace(/\s+$/, "")} \\\n    && rm -rf /var/lib/apt/lists/*`
      return t === ins.text ? [] : [{ name: "Add --no-install-recommends and clean the apt lists", changes: [{ from: ins.from, to: ins.to, insert: t }] }]
    }
    case "dk_apk_cache": {
      const ins = instruction(text, pr.line)
      const t = ins.text.replace(/\bapk(\s+-\S+)*\s+add\b(?![^\n&;|]*--no-cache)/g, (s) => `${s} --no-cache`)
      return t === ins.text ? [] : [{ name: "Use apk add --no-cache", changes: [{ from: ins.from, to: ins.to, insert: t }] }]
    }
    case "dk_pip_cache": {
      const ins = instruction(text, pr.line)
      const t = ins.text.replace(/\bpip3?(\s+-\S+)*\s+install\b(?![^\n&;|]*--no-cache-dir)/g, (s) => `${s} --no-cache-dir`)
      return t === ins.text ? [] : [{ name: "Use pip install --no-cache-dir", changes: [{ from: ins.from, to: ins.to, insert: t }] }]
    }
    case "dk_add": {
      const ins = instruction(text, pr.line)
      const m = /^(\s*)ADD(\s+(?:--(?:chown|chmod|link)\S*\s+)*)(.*)$/is.exec(ins.text)
      // Only local, non-archive sources: URLs and tarballs keep ADD's behaviour.
      if (!m || /https?:\/\/|git@|\.(tar|tgz|tar\.\w+)(\s|$)/i.test(m[3]) || /--checksum/.test(m[2])) return []
      return [{ name: "Use COPY", changes: [{ from: ins.from + m[1].length, to: ins.from + m[1].length + 3, insert: "COPY" }] }]
    }
  }
  return []
}

/** Quick fixes of a dk_* problem in a compose file or a Dockerfile. */
export function dockerQuickFixes(kind: "compose" | "dockerfile", env: DockerFixEnv) {
  return (pr: CodeProblem, ctx: QuickFixContext): QuickFix[] => (kind === "compose" ? composeFixes(pr, ctx, env) : dockerfileFixes(pr, ctx, env))
}

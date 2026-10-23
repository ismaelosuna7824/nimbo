// Dockerfile completion and hover: instructions, their flags, base images
// from the catalog, stage names (FROM <stage>, COPY --from=) and the ARG/ENV
// variables declared above the cursor.

import type { Completion, CompletionResult, CompletionSection } from "@codemirror/autocomplete"
import { snippetCompletion } from "@codemirror/autocomplete"
import type { DockerImage } from "@/lib/api/types"
import { docNode } from "@/features/panels/terraform/intel/format"

const DOCS = "https://docs.docker.com/reference/dockerfile/"
const DOCS_LABEL = "Open the Dockerfile reference ↗"

const S_INSTR: CompletionSection = { name: "Instructions", rank: 0 }
const S_STAGES: CompletionSection = { name: "Stages", rank: 0 }
const S_IMAGES: CompletionSection = { name: "Images", rank: 1 }
const S_VARS: CompletionSection = { name: "Variables", rank: 0 }
const S_FLAGS: CompletionSection = { name: "Flags", rank: 0 }

interface Instruction {
  name: string
  snippet: string
  doc: string
  flags?: { name: string; doc: string }[]
}

export const INSTRUCTIONS: Instruction[] = [
  { name: "FROM", snippet: "FROM ${image} AS ${name}", doc: "Starts a build stage from a base image. `AS name` names the stage for `COPY --from` and `--target`.", flags: [{ name: "--platform=", doc: "Platform of the base image, e.g. `linux/amd64`." }] },
  {
    name: "RUN",
    snippet: "RUN ${command}",
    doc: "Runs a command in a new layer on top of the current image (shell form or `[\"exec\", \"form\"]`).",
    flags: [
      { name: "--mount=type=cache,target=", doc: "Keeps a cache directory between builds (package managers)." },
      { name: "--mount=type=secret,id=", doc: "Exposes a build secret to this command only." },
      { name: "--mount=type=bind,", doc: "Binds files from the context or another stage, read-only." },
      { name: "--network=", doc: "Network for the command: `default`, `none` or `host`." },
    ],
  },
  { name: "CMD", snippet: 'CMD ["${executable}", "${arg}"]', doc: "Default command of the container (overridden by `docker run …` arguments and `command:` in compose)." },
  { name: "ENTRYPOINT", snippet: 'ENTRYPOINT ["${executable}"]', doc: "Executable the container runs; `CMD` becomes its default arguments." },
  { name: "LABEL", snippet: 'LABEL ${key}="${value}"', doc: "Adds metadata to the image." },
  { name: "EXPOSE", snippet: "EXPOSE ${port}", doc: "Documents the port the container listens on (`80`, `8080/tcp`, `53/udp`). Publishing is done by `ports:` in compose." },
  { name: "ENV", snippet: "ENV ${KEY}=${value}", doc: "Sets an environment variable for the following instructions and the running container." },
  {
    name: "ADD",
    snippet: "ADD ${src} ${dest}",
    doc: "Copies files, URLs or git repositories into the image; local tar archives are extracted. Prefer `COPY` for plain files.",
    flags: [
      { name: "--chown=", doc: "Owner `user:group` of the copied files." },
      { name: "--chmod=", doc: "Permissions of the copied files, e.g. `755`." },
      { name: "--checksum=", doc: "Verifies a remote file: `sha256:…`." },
      { name: "--link", doc: "Copies into an independent layer (better cache reuse)." },
    ],
  },
  {
    name: "COPY",
    snippet: "COPY ${src} ${dest}",
    doc: "Copies files from the build context (or another stage with `--from`) into the image.",
    flags: [
      { name: "--from=", doc: "Copy from a build stage, an image or a named context instead of the build context." },
      { name: "--chown=", doc: "Owner `user:group` of the copied files." },
      { name: "--chmod=", doc: "Permissions of the copied files, e.g. `755`." },
      { name: "--link", doc: "Copies into an independent layer (better cache reuse)." },
      { name: "--exclude=", doc: "Paths to leave out of the copy." },
    ],
  },
  { name: "VOLUME", snippet: 'VOLUME ["${path}"]', doc: "Declares a mount point for externally mounted volumes." },
  { name: "USER", snippet: "USER ${user}", doc: "User (and group) the following instructions and the container run as. Running as a non-root user is safer." },
  { name: "WORKDIR", snippet: "WORKDIR ${path}", doc: "Working directory for the following instructions and the container (created when missing)." },
  { name: "ARG", snippet: "ARG ${NAME}=${default}", doc: "Build-time variable, set with `build.args` in compose. An ARG before the first FROM can be used in FROM lines." },
  { name: "ONBUILD", snippet: "ONBUILD ${instruction}", doc: "Instruction run when another image builds FROM this one." },
  { name: "STOPSIGNAL", snippet: "STOPSIGNAL ${SIGTERM}", doc: "Signal sent to stop the container." },
  {
    name: "HEALTHCHECK",
    snippet: "HEALTHCHECK --interval=${10s} --timeout=${3s} --retries=${3} CMD ${command}",
    doc: "How Docker checks that the container still works (`service_healthy` in compose waits for it). `HEALTHCHECK NONE` disables the base image's.",
    flags: [
      { name: "--interval=", doc: "Time between checks (default 30s)." },
      { name: "--timeout=", doc: "Time a check may take (default 30s)." },
      { name: "--start-period=", doc: "Grace period after start where failures don't count." },
      { name: "--start-interval=", doc: "Time between checks during the start period." },
      { name: "--retries=", doc: "Consecutive failures before unhealthy (default 3)." },
    ],
  },
  { name: "SHELL", snippet: 'SHELL ["${/bin/sh}", "${-c}"]', doc: "Shell used by the shell form of RUN, CMD and ENTRYPOINT." },
  { name: "MAINTAINER", snippet: "MAINTAINER ${name}", doc: "Deprecated: use `LABEL org.opencontainers.image.authors=…`." },
]

const BY_NAME = new Map(INSTRUCTIONS.map((i) => [i.name, i]))

export interface DockerfileSymbols {
  /** Stage names (`FROM … AS name`) before `pos`, in order. */
  stages: string[]
  /** ARG and ENV names before `pos`. */
  vars: { name: string; kind: "ARG" | "ENV" }[]
}

/** Logical lines (continuations joined) with their start offsets. */
function logicalLines(text: string): { text: string; from: number }[] {
  const out: { text: string; from: number }[] = []
  const lines = text.split("\n")
  let offset = 0
  let current: { text: string; from: number } | null = null
  for (const l of lines) {
    if (current) current.text += `\n${l}`
    else current = { text: l, from: offset }
    offset += l.length + 1
    if (!/\\\s*$/.test(l) || /^\s*#/.test(l)) {
      out.push(current)
      current = null
    }
  }
  if (current) out.push(current)
  return out
}

export function dockerfileSymbols(text: string, pos = text.length): DockerfileSymbols {
  const stages: string[] = []
  const vars: DockerfileSymbols["vars"] = []
  for (const l of logicalLines(text)) {
    if (l.from >= pos) break
    const t = l.text.trim()
    const from = /^FROM\s+(?:--\S+\s+)*\S+\s+AS\s+([\w.-]+)/i.exec(t)
    if (from) stages.push(from[1])
    const arg = /^ARG\s+([A-Za-z_]\w*)/i.exec(t)
    if (arg) vars.push({ name: arg[1], kind: "ARG" })
    const env = /^ENV\s+(.+)/is.exec(t)
    if (env) {
      const body = env[1]
      // ENV A=1 B=2, or the legacy ENV A value.
      const pairs = [...body.matchAll(/([A-Za-z_]\w*)=/g)].map((m) => m[1])
      if (pairs.length) for (const p of pairs) vars.push({ name: p, kind: "ENV" })
      else {
        const legacy = /^([A-Za-z_]\w*)\s/.exec(body)
        if (legacy) vars.push({ name: legacy[1], kind: "ENV" })
      }
    }
  }
  return { stages, vars }
}

function imageOptions(images: DockerImage[]): Completion[] {
  return images.map((img) => ({
    label: `${img.image}:${img.tag}`,
    type: "constant",
    detail: img.label,
    section: S_IMAGES,
    info: () => docNode({ title: `${img.image}:${img.tag}`, badges: [img.category], description: img.description }),
  }))
}

/** Completions at `pos` in a Dockerfile; null when none apply. */
export function dockerfileCompletions(text: string, pos: number, explicit: boolean, images: DockerImage[]): CompletionResult | null {
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const before = text.slice(lineStart, pos)
  if (/^\s*#/.test(before)) return null
  const sym = dockerfileSymbols(text, lineStart)

  // $VAR / ${VAR}
  const v = /\$\{?([A-Za-z_]\w*)?$/.exec(before)
  if (v) {
    const from = pos - (v[1]?.length ?? 0)
    const options = [...new Map(sym.vars.map((x) => [x.name, x])).values()].map((x) => ({
      label: x.name,
      type: "variable",
      detail: x.kind,
      section: S_VARS,
    }))
    return options.length ? { from, options, validFor: /^\w*$/ } : null
  }

  // The instruction keyword (also after a continuation it is an argument, not an instruction).
  const prevLine = lineStart > 0 ? text.slice(text.lastIndexOf("\n", lineStart - 2) + 1, lineStart - 1) : ""
  const continued = /\\\s*$/.test(prevLine)
  const kw = /^\s*([A-Za-z]*)$/.exec(before)
  if (kw && !continued) {
    if (!explicit && kw[1] === "") return null
    return {
      from: pos - kw[1].length,
      options: INSTRUCTIONS.map((i) =>
        snippetCompletion(i.snippet, {
          label: i.name,
          type: "keyword",
          section: S_INSTR,
          boost: i.name === "MAINTAINER" ? -10 : 0,
          info: () => docNode({ title: i.name, description: i.doc, docsUrl: `${DOCS}#${i.name.toLowerCase()}`, docsLabel: DOCS_LABEL }),
        }),
      ),
      validFor: /^[A-Za-z]*$/,
    }
  }

  const instr = /^\s*([A-Za-z]+)\s/.exec(before)?.[1]?.toUpperCase()
  if (!instr) return null

  // COPY --from=<stage|image>
  const fromFlag = /--from=([\w.:/-]*)$/.exec(before)
  if (fromFlag && (instr === "COPY" || instr === "ADD" || instr === "RUN")) {
    return {
      from: pos - fromFlag[1].length,
      options: [...sym.stages.map((s) => ({ label: s, type: "class", section: S_STAGES, detail: "stage" })), ...imageOptions(images)],
      validFor: /^[\w.:/-]*$/,
    }
  }

  // Flags: "--" at a word start.
  const flag = /(?:^|\s)(--[\w-]*)$/.exec(before)
  if (flag) {
    const flags = BY_NAME.get(instr)?.flags ?? []
    if (!flags.length) return null
    return {
      from: pos - flag[1].length,
      options: flags.map((f) => ({ label: f.name, type: "property", section: S_FLAGS, info: f.doc })),
      validFor: /^[\w=,-]*$/,
    }
  }

  // FROM <image|stage> (the first argument after flags).
  if (instr === "FROM") {
    const arg = /^\s*FROM\s+(?:--\S+\s+)*([\w.:/@${}-]*)$/i.exec(before)
    if (arg) {
      if (!explicit && arg[1] === "") return null
      return {
        from: pos - arg[1].length,
        options: [...sym.stages.map((s) => ({ label: s, type: "class", section: S_STAGES, detail: "stage" })), ...imageOptions(images)],
        validFor: /^[\w.:/@-]*$/,
      }
    }
    const as = /^\s*FROM\s+(?:--\S+\s+)*\S+\s+([A-Za-z]*)$/i.exec(before)
    if (as) return { from: pos - as[1].length, options: [{ label: "AS", type: "keyword", info: "Names this stage" }], validFor: /^[A-Za-z]*$/ }
  }
  return null
}

/** Docs of the instruction (or stage name) under the cursor. */
export function dockerfileHover(text: string, pos: number): { from: number; to: number; dom: HTMLElement } | null {
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const lineEnd = text.indexOf("\n", pos) < 0 ? text.length : text.indexOf("\n", pos)
  const line = text.slice(lineStart, lineEnd)
  const m = /^(\s*)([A-Za-z]+)\b/.exec(line)
  if (m) {
    const from = lineStart + m[1].length
    const to = from + m[2].length
    const instr = BY_NAME.get(m[2].toUpperCase())
    if (instr && pos >= from && pos <= to) {
      return { from, to, dom: docNode({ title: instr.name, description: instr.doc, docsUrl: `${DOCS}#${instr.name.toLowerCase()}`, docsLabel: DOCS_LABEL }) }
    }
  }
  // A stage name: in COPY --from=x or FROM x.
  const word = /[\w.-]+/g
  for (let w = word.exec(line); w; w = word.exec(line)) {
    const from = lineStart + w.index
    const to = from + w[0].length
    if (pos < from || pos > to) continue
    const all = dockerfileSymbols(text).stages
    if (!all.includes(w[0])) return null
    const declared = logicalLines(text).find((l) => new RegExp(`^\\s*FROM\\s+.*\\s+AS\\s+${w![0].replace(/[.]/g, "\\.")}\\s*$`, "i").test(l.text))
    return {
      from,
      to,
      dom: docNode({ title: `stage ${w[0]}`, description: declared ? `\`${declared.text.trim()}\`` : undefined }),
    }
  }
  return null
}

/**
 * Go to definition in a Dockerfile: a stage name (FROM <stage>, COPY --from=<stage>, RUN --mount=from=<stage>) to its
 * `FROM … AS <stage>` line, and $VAR / ${VAR} to the last ARG or ENV declaring it above. Returns the offset or null.
 */
export function dockerfileDefinitionAt(text: string, pos: number): number | null {
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const lineEnd = text.indexOf("\n", pos) < 0 ? text.length : text.indexOf("\n", pos)
  const line = text.slice(lineStart, lineEnd)
  if (/^\s*#/.test(line)) return null
  const lines = logicalLines(text)

  // $VAR / ${VAR}
  for (const m of line.matchAll(/\$\{([A-Za-z_]\w*)[^}]*\}|\$([A-Za-z_]\w*)/g)) {
    const from = lineStart + m.index
    if (pos < from || pos > from + m[0].length) continue
    const name = m[1] ?? m[2]
    let found: number | null = null
    for (const l of lines) {
      if (l.from >= lineStart) break
      const t = l.text.trimStart()
      const lead = l.text.length - t.length
      const arg = new RegExp(`^ARG\\s+(${name})\\b`, "i").exec(t)
      const env = /^ENV\s/i.test(t) ? new RegExp(`(?:^ENV\\s+|\\s)(${name})(?:=|\\s)`, "i").exec(t) : null
      const hit = arg ?? env
      if (hit) found = l.from + lead + hit.index + hit[0].indexOf(name)
    }
    return found
  }

  // A stage name.
  const stageRef =
    /--from=([\w.-]+)/.exec(line) ??
    /^\s*FROM\s+(?:--\S+\s+)*([\w.-]+)(?:\s|$)/i.exec(line) ??
    /--mount=[^\s]*\bfrom=([\w.-]+)/.exec(line)
  if (!stageRef) return null
  const from = lineStart + stageRef.index + stageRef[0].lastIndexOf(stageRef[1])
  if (pos < from || pos > from + stageRef[1].length) return null
  const name = stageRef[1].toLowerCase()
  for (const l of lines) {
    if (l.from >= lineStart) break
    const m = /^(\s*FROM\s+(?:--\S+\s+)*\S+\s+AS\s+)([\w.-]+)/i.exec(l.text)
    if (m && m[2].toLowerCase() === name) return l.from + m[1].length
  }
  return null
}

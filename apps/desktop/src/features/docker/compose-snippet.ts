// Adding a service written as a snippet (the "Compose service" template) to
// an existing compose file, keeping everything else as written.

import { get, parseYaml } from "@/features/code-intel/yaml/tree"

/** A compose service snippet ("  app:\n    build: .") added at the end of `services:` of a compose file, renamed when taken. */
export function insertService(text: string, snippet: string): string {
  const doc = parseYaml(text).find((d) => get(d.root, "services") !== undefined)
  const services = doc && get(doc.root, "services")
  const taken = new Set(services?.kind === "map" ? services.entries.map((e) => e.key) : [])
  const lines = snippet.replace(/\n+$/, "").split("\n")
  // The service's key line (leading comment lines describe it).
  const keyLine = lines.findIndex((l) => /^\s*[^\s:#][^\s:]*:/.test(l))
  const own = keyLine < 0 ? null : /^(\s*)([^\s:#]+):/.exec(lines[keyLine])
  if (!own) return text
  // Re-indent to the existing services' indentation.
  const col = services?.kind === "map" && services.entries.length ? services.entries[0].keyFrom - (text.lastIndexOf("\n", services.entries[0].keyFrom - 1) + 1) : 2
  const shift = col - own[1].length
  let name = own[2]
  for (let n = 2; taken.has(name); n++) name = `${own[2]}${n}`
  const reindent = (l: string) => (!l.trim() ? "" : shift >= 0 ? " ".repeat(shift) + l : l.slice(Math.min(-shift, l.search(/\S/))))
  const body = lines.map((l, i) => (i === keyLine ? `${" ".repeat(col)}${name}:${l.slice(own[0].length)}` : reindent(l))).join("\n")
  if (services?.kind === "map") {
    const end = text.indexOf("\n", services.to)
    const at = end < 0 ? text.length : end
    return `${text.slice(0, at)}\n\n${body}${text.slice(at)}`
  }
  if (services !== undefined) {
    // "services:" with nothing under it yet.
    const key = doc!.root!.kind === "map" ? doc!.root!.entries.find((e) => e.key === "services")! : undefined
    const end = key ? text.indexOf("\n", key.keyTo) : -1
    const at = end < 0 ? text.length : end
    return `${text.slice(0, at)}\n${body}${text.slice(at)}`
  }
  return `${text.trim() ? `${text.replace(/\n*$/, "\n")}\n` : ""}services:\n${body}\n`
}

// Go to definition: var./local./module./data./resource references (and a
// module call's arguments) to the block that declares them, across files.

import type { CompletionEnv } from "./completion"
import { referenceAt } from "./context"
import { resolveReference } from "./resolve"
import { blockAt, blockPath, inComment, parseCached } from "./scan"
import { dirOf, stringValue } from "./symbols"

export interface Target {
  /** Workflow path of the declaring file. */
  file: string
  from: number
  line: number
}

/** Where the name at pos is declared, or null. */
export function definitionAt(text: string, pos: number, env: CompletionEnv): Target | null {
  const p = parseCached(text)
  if (inComment(p, pos)) return null
  const module = env.symbols.module(dirOf(env.path))
  const body = blockAt(p.root, pos)
  const path = blockPath(body)

  // A module call's argument -> the module's variable; its source -> the module's files.
  if (path.length === 1 && path[0].type === "module") {
    const call = module.modules.get(path[0].labels[0]?.text ?? "")
    const callee = call ? env.symbols.callee(module.dir, call) : null
    for (const a of body.attrs) {
      if (pos < a.nameFrom || pos > a.valueTo || !callee) continue
      if (a.name === "source" && pos > a.eq) {
        const first = Object.keys(env.symbols.files)
          .filter((f) => f.endsWith(".tf") && dirOf(f) === callee.dir)
          .sort((x, y) => (x.endsWith("/main.tf") ? -1 : y.endsWith("/main.tf") ? 1 : x.localeCompare(y)))[0]
        if (first && stringValue(text, a)) return { file: first, from: 0, line: 1 }
      }
      if (pos <= a.nameTo) {
        const v = callee.variables.get(a.name)
        if (v && v.file) return { file: v.file, from: v.from, line: v.line }
      }
    }
  }

  const ref = referenceAt(text, pos)
  if (!ref) return null
  const r = resolveReference(ref.segs, ref.index, module, env.symbols)
  if (!r) return null
  const d = r.kind === "output" && !r.decl.file ? r.call : r.decl
  return d.file ? { file: d.file, from: d.from, line: d.line } : null
}

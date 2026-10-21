// Resolves a reference (var.x, local.y, module.z.out, aws_s3_bucket.b.arn,
// data.x.y) to what declares it, for hover and go to definition.

import type { ModuleCallSymbol, ModuleSymbols, ResourceSymbol, SymbolIndex, ValueSymbol, VariableSymbol } from "./symbols"

export type Resolved =
  | { kind: "variable"; decl: VariableSymbol }
  | { kind: "local"; decl: ValueSymbol }
  | { kind: "module"; decl: ModuleCallSymbol }
  | { kind: "output"; decl: ValueSymbol; call: ModuleCallSymbol }
  /** attr: segments after the address up to the one under the cursor ("[]" for indexes). */
  | { kind: "resource"; decl: ResourceSymbol; attr: string[] }

/** What segs[0..=index] refers to in module m (on a root word like `var`, the name after it). */
export function resolveReference(segs: string[], index: number, m: ModuleSymbols, symbols: SymbolIndex): Resolved | null {
  const minIndex = segs[0] === "data" ? 2 : segs[0] === "var" || segs[0] === "local" || segs[0] === "module" ? 1 : 0
  const s = segs.slice(0, Math.max(index, minIndex) + 1)
  const [root, a, b] = s
  if (!a) {
    // The root word alone: only a resource type with a name after it means something.
    if (segs.length > 1 && m.resources.has(`${root}.${segs[1]}`)) {
      return { kind: "resource", decl: m.resources.get(`${root}.${segs[1]}`)!, attr: [] }
    }
    return null
  }
  switch (root) {
    case "var": {
      const v = m.variables.get(a)
      return v ? { kind: "variable", decl: v } : null
    }
    case "local": {
      const l = m.locals.get(a)
      return l ? { kind: "local", decl: l } : null
    }
    case "module": {
      const call = m.modules.get(a)
      if (!call) return null
      if (!b) return { kind: "module", decl: call }
      const out = symbols.callee(m.dir, call)?.outputs.get(b)
      return out ? { kind: "output", decl: out, call } : { kind: "module", decl: call }
    }
    case "data": {
      const name = b ?? segs[2]
      const r = name ? m.resources.get(`data.${a}.${name}`) : undefined
      return r ? { kind: "resource", decl: r, attr: s.slice(3) } : null
    }
    case "each":
    case "count":
    case "path":
    case "terraform":
    case "self":
      return null
  }
  const r = m.resources.get(`${root}.${a}`)
  return r ? { kind: "resource", decl: r, attr: s.slice(2) } : null
}

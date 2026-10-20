// A small evaluator of Terraform expressions for the Architecture diagram: enough of the language to
// resolve the counts, names and types modules compute from their inputs (`var.create ? 1 : 0`,
// `var.resource_types.web_sites`, `"${local.prefix}-db"`, `length(var.subnets) > 0`). Anything it
// cannot know (resource attributes, `each`, unsupported functions) is UNKNOWN, which spreads through
// the operations using it. Pure, no Terraform semantics beyond what the diagram needs.

/** A value the evaluator cannot know. */
export const UNKNOWN: unique symbol = Symbol("unknown")
type Val = unknown

/** Resolves `var.x` / `local.x` (UNKNOWN when it cannot). */
export type Lookup = (kind: "var" | "local", name: string) => Val

// ------------------------------------------------------------ tokens

type Tok =
  | { k: "num"; v: number }
  | { k: "str"; parts: (string | string[])[] } // literal text, or the source of a ${…} interpolation
  | { k: "id"; v: string }
  | { k: "op"; v: string }
  | { k: "eof" }

const OPS = ["==", "!=", "<=", ">=", "&&", "||", "=>", "...", "?", ":", "!", "<", ">", "+", "-", "*", "/", "%", "(", ")", "[", "]", "{", "}", ",", ".", "="]

function tokenize(src: string): Tok[] | undefined {
  const out: Tok[] = []
  let i = 0
  while (i < src.length) {
    const c = src[i]
    if (/\s/.test(c)) {
      i++
      continue
    }
    if (c === "#" || (c === "/" && src[i + 1] === "/")) {
      while (i < src.length && src[i] !== "\n") i++
      continue
    }
    if (c === "<" && src[i + 1] === "<") return undefined // heredoc
    if (/[0-9]/.test(c)) {
      const m = /^\d+(\.\d+)?([eE][+-]?\d+)?/.exec(src.slice(i))!
      out.push({ k: "num", v: Number(m[0]) })
      i += m[0].length
      continue
    }
    if (/[A-Za-z_]/.test(c)) {
      const m = /^[A-Za-z_][\w-]*/.exec(src.slice(i))!
      out.push({ k: "id", v: m[0] })
      i += m[0].length
      continue
    }
    if (c === '"') {
      const parts: (string | string[])[] = []
      let text = ""
      i++
      for (;;) {
        if (i >= src.length) return undefined
        const d = src[i]
        if (d === '"') {
          i++
          break
        }
        if (d === "\\") {
          const e = src[i + 1]
          text += e === "n" ? "\n" : e === "t" ? "\t" : e
          i += 2
          continue
        }
        if ((d === "$" || d === "%") && src[i + 1] === "{") {
          if (src[i + 2] === "~" || d === "%") return undefined // template directives
          // Find the matching brace (strings inside the interpolation may hold braces).
          let depth = 1
          let j = i + 2
          let inStr = false
          while (j < src.length && depth > 0) {
            const x = src[j]
            if (inStr) {
              if (x === "\\") j++
              else if (x === '"') inStr = false
            } else if (x === '"') inStr = true
            else if (x === "{") depth++
            else if (x === "}") depth--
            j++
          }
          if (depth) return undefined
          if (text) parts.push(text)
          text = ""
          parts.push([src.slice(i + 2, j - 1)])
          i = j
          continue
        }
        if (d === "$" && src[i + 1] === "$" && src[i + 2] === "{") {
          text += "${"
          i += 3
          continue
        }
        text += d
        i++
      }
      if (text || parts.length === 0) parts.push(text)
      out.push({ k: "str", parts })
      continue
    }
    const op = OPS.find((o) => src.startsWith(o, i))
    if (!op) return undefined
    out.push({ k: "op", v: op })
    i += op.length
  }
  out.push({ k: "eof" })
  return out
}

// ------------------------------------------------------------ evaluation

/** A Terraform object or map (not a list, null or UNKNOWN). */
export function isRecord(v: Val): v is Record<string, Val> {
  return !!v && typeof v === "object" && !Array.isArray(v)
}
/** Whether a value holds UNKNOWN anywhere. */
export function hasUnknown(v: Val): boolean {
  if (v === UNKNOWN) return true
  if (Array.isArray(v)) return v.some(hasUnknown)
  if (v && typeof v === "object") return Object.values(v).some(hasUnknown)
  return false
}
const truthy = (v: Val): boolean | typeof UNKNOWN => (v === UNKNOWN ? UNKNOWN : v === true || v === "true")
const num = (v: Val): number | typeof UNKNOWN => (typeof v === "number" ? v : typeof v === "string" && v.trim() !== "" && !Number.isNaN(Number(v)) ? Number(v) : UNKNOWN)
const equal = (a: Val, b: Val): boolean => (a === b ? true : JSON.stringify(a) === JSON.stringify(b))
const toText = (v: Val): string | typeof UNKNOWN =>
  typeof v === "string" ? v : typeof v === "number" || typeof v === "boolean" ? String(v) : UNKNOWN

const FUNCTIONS: Record<string, (args: Val[]) => Val> = {
  length: ([v]) => (typeof v === "string" || Array.isArray(v) ? v.length : isRecord(v) ? Object.keys(v).length : UNKNOWN),
  toset: ([v]) => (Array.isArray(v) ? v.filter((x, i) => v.findIndex((y) => equal(x, y)) === i) : v === null ? null : UNKNOWN),
  tolist: ([v]) => (Array.isArray(v) ? v : UNKNOWN),
  tomap: ([v]) => (isRecord(v) ? v : UNKNOWN),
  keys: ([v]) => (isRecord(v) ? Object.keys(v).sort() : UNKNOWN),
  values: ([v]) => (isRecord(v) ? Object.keys(v).sort().map((k) => v[k]) : UNKNOWN),
  tostring: ([v]) => toText(v),
  tonumber: ([v]) => num(v),
  tobool: ([v]) => (typeof v === "boolean" ? v : v === "true" ? true : v === "false" ? false : UNKNOWN),
  lower: ([v]) => (typeof v === "string" ? v.toLowerCase() : UNKNOWN),
  upper: ([v]) => (typeof v === "string" ? v.toUpperCase() : UNKNOWN),
  trimspace: ([v]) => (typeof v === "string" ? v.trim() : UNKNOWN),
  contains: ([list, v]) => (Array.isArray(list) && !hasUnknown(list) && v !== UNKNOWN ? list.some((x) => equal(x, v)) : UNKNOWN),
  concat: (args) => (args.every(Array.isArray) ? args.flat() : UNKNOWN),
  merge: (args) => (args.every((a) => a === null || isRecord(a)) ? Object.assign({}, ...args) : UNKNOWN),
  compact: ([v]) => (Array.isArray(v) ? v.filter((x) => x !== null && x !== "") : UNKNOWN),
  flatten: ([v]) => (Array.isArray(v) ? v.flat(Infinity) : UNKNOWN),
  distinct: ([v]) => (Array.isArray(v) ? v.filter((x, i) => v.findIndex((y) => equal(x, y)) === i) : UNKNOWN),
  join: ([sep, list]) => (typeof sep === "string" && Array.isArray(list) && list.every((x) => toText(x) !== UNKNOWN) ? list.map(toText).join(sep) : UNKNOWN),
  split: ([sep, s]) => (typeof sep === "string" && typeof s === "string" ? s.split(sep) : UNKNOWN),
  replace: ([s, a, b]) => (typeof s === "string" && typeof a === "string" && typeof b === "string" && !a.startsWith("/") ? s.split(a).join(b) : UNKNOWN),
  substr: ([s, o, l]) => (typeof s === "string" && typeof o === "number" && typeof l === "number" ? s.substr(o, l < 0 ? undefined : l) : UNKNOWN),
  min: (args) => (args.every((a): a is number => typeof a === "number") ? Math.min(...args) : UNKNOWN),
  max: (args) => (args.every((a): a is number => typeof a === "number") ? Math.max(...args) : UNKNOWN),
  abs: ([v]) => (typeof v === "number" ? Math.abs(v) : UNKNOWN),
  ceil: ([v]) => (typeof v === "number" ? Math.ceil(v) : UNKNOWN),
  floor: ([v]) => (typeof v === "number" ? Math.floor(v) : UNKNOWN),
  coalesce: (args) => {
    for (const a of args) {
      if (a === UNKNOWN) return UNKNOWN
      if (a !== null && a !== "") return a
    }
    return UNKNOWN
  },
  coalescelist: (args) => {
    for (const a of args) {
      if (!Array.isArray(a)) return UNKNOWN
      if (a.length) return a
    }
    return UNKNOWN
  },
  lookup: ([m, k, d]) => {
    if (!isRecord(m) || typeof k !== "string") return UNKNOWN
    const v = m[k]
    return v !== undefined ? v : d === undefined ? UNKNOWN : d
  },
  element: ([list, i]) => (Array.isArray(list) && typeof i === "number" && list.length ? list[((i % list.length) + list.length) % list.length] : UNKNOWN),
  one: ([list]) => (Array.isArray(list) ? (list.length === 0 ? null : list.length === 1 ? list[0] : UNKNOWN) : UNKNOWN),
  range: (args) => {
    if (!args.every((a): a is number => typeof a === "number")) return UNKNOWN
    const [a, b, s = 1] = args.length === 1 ? [0, args[0]] : args
    const out: number[] = []
    for (let x = a; s > 0 ? x < b : x > b; x += s) if (out.push(x) > 1000) return UNKNOWN
    return out
  },
  format: ([f, ...args]) => {
    if (typeof f !== "string") return UNKNOWN
    let i = 0
    let ok = true
    const out = f.replace(/%%|%[-+ #0]*\d*(?:\.\d+)?([sdvtqf])/g, (m) => {
      if (m === "%%") return "%"
      const t = toText(args[i++])
      if (t === UNKNOWN) ok = false
      return t === UNKNOWN ? "" : t
    })
    return ok ? out : UNKNOWN
  },
}

class Parser {
  private i = 0
  private readonly toks: Tok[]
  private readonly lookup: Lookup
  private readonly depth: number
  private readonly scope: Record<string, Val>
  private readonly fns: Record<string, (args: Val[]) => Val>
  constructor(toks: Tok[], lookup: Lookup, depth: number, scope: Record<string, Val> = {}, fns = FUNCTIONS) {
    this.toks = toks
    this.lookup = lookup
    this.depth = depth
    this.scope = scope
    this.fns = fns
  }
  private peek(o = 0) {
    return this.toks[this.i + o]
  }
  private isOp(v: string, o = 0) {
    const t = this.peek(o)
    return t?.k === "op" && t.v === v
  }
  /** The identifier at offset `o`, if the token there is one. */
  private idAt(o = 0) {
    const t = this.peek(o)
    return t?.k === "id" ? t.v : undefined
  }
  private eat(v: string) {
    if (!this.isOp(v)) throw new SyntaxError(v)
    this.i++
  }
  parse(): Val {
    const v = this.ternary()
    if (this.peek().k !== "eof") throw new SyntaxError("trailing")
    return v
  }
  private ternary(): Val {
    const cond = this.binary(0)
    if (!this.isOp("?")) return cond
    this.i++
    const a = this.ternary()
    this.eat(":")
    const b = this.ternary()
    const c = truthy(cond)
    if (c === UNKNOWN) return equal(a, b) && !hasUnknown(a) ? a : UNKNOWN
    return c ? a : b
  }
  private static readonly PREC: Record<string, number> = { "||": 1, "&&": 2, "==": 3, "!=": 3, "<": 4, ">": 4, "<=": 4, ">=": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6 }
  private binary(min: number): Val {
    let left = this.unary()
    for (;;) {
      const t = this.peek()
      if (t.k !== "op") return left
      const prec = Parser.PREC[t.v]
      if (prec === undefined || prec <= min) return left
      this.i++
      const right = this.binary(prec)
      left = Parser.apply(t.v, left, right)
    }
  }
  private static apply(op: string, a: Val, b: Val): Val {
    switch (op) {
      case "||": {
        const x = truthy(a)
        const y = truthy(b)
        return x === true || y === true ? true : x === UNKNOWN || y === UNKNOWN ? UNKNOWN : false
      }
      case "&&": {
        const x = truthy(a)
        const y = truthy(b)
        return x === false || y === false ? false : x === UNKNOWN || y === UNKNOWN ? UNKNOWN : true
      }
      case "==":
      case "!=":
        if (hasUnknown(a) || hasUnknown(b)) return UNKNOWN
        return op === "==" ? equal(a, b) : !equal(a, b)
    }
    const x = num(a)
    const y = num(b)
    if (x === UNKNOWN || y === UNKNOWN) return UNKNOWN
    switch (op) {
      case "<":
        return x < y
      case ">":
        return x > y
      case "<=":
        return x <= y
      case ">=":
        return x >= y
      case "+":
        return x + y
      case "-":
        return x - y
      case "*":
        return x * y
      case "/":
        return y === 0 ? UNKNOWN : x / y
      default:
        return y === 0 ? UNKNOWN : x % y
    }
  }
  private unary(): Val {
    if (this.isOp("!")) {
      this.i++
      const v = truthy(this.unary())
      return v === UNKNOWN ? UNKNOWN : !v
    }
    if (this.isOp("-")) {
      this.i++
      const v = num(this.unary())
      return v === UNKNOWN ? UNKNOWN : -v
    }
    return this.postfix(this.primary())
  }
  private postfix(base: Val): Val {
    let v = base
    for (;;) {
      if (this.isOp(".")) {
        this.i++
        const t = this.peek()
        if (t.k === "op" && t.v === "*") {
          this.i++
          v = UNKNOWN
          continue
        }
        if (t.k !== "id" && t.k !== "num") throw new SyntaxError("attr")
        this.i++
        v = Parser.index(v, t.v)
      } else if (this.isOp("[")) {
        this.i++
        if (this.isOp("*")) {
          this.i++
          this.eat("]")
          v = UNKNOWN
          continue
        }
        const k = this.ternary()
        this.eat("]")
        v = Parser.index(v, k)
      } else return v
    }
  }
  private static index(v: Val, k: Val): Val {
    if (v === UNKNOWN || k === UNKNOWN || v === null || v === undefined) return UNKNOWN
    if (Array.isArray(v)) return typeof k === "number" && k >= 0 && k < v.length ? v[k] : UNKNOWN
    if (isRecord(v) && (typeof k === "string" || typeof k === "number")) return v[String(k)] === undefined ? UNKNOWN : v[String(k)]
    return UNKNOWN
  }
  private primary(): Val {
    const t = this.peek()
    this.i++
    if (t.k === "num") return t.v
    if (t.k === "str") {
      let out = ""
      for (const p of t.parts) {
        if (typeof p === "string") out += p
        else {
          const v = toText(evaluate(p[0], this.lookup, this.depth + 1, this.scope))
          if (v === UNKNOWN) return UNKNOWN
          out += v
        }
      }
      return out
    }
    if (t.k === "op") {
      if (t.v === "(") {
        const v = this.ternary()
        this.eat(")")
        return v
      }
      if (t.v === "[") return this.list()
      if (t.v === "{") return this.object()
      throw new SyntaxError(t.v)
    }
    if (t.k !== "id") throw new SyntaxError("eof")
    if (t.v === "true") return true
    if (t.v === "false") return false
    if (t.v === "null") return null
    if (this.isOp("(")) return this.call(t.v)
    if (t.v in this.scope) return this.scope[t.v]
    const name = this.idAt(1)
    if ((t.v === "var" || t.v === "local") && this.isOp(".") && name) {
      this.i += 2
      return this.depth > 8 ? UNKNOWN : this.lookup(t.v, name)
    }
    // Resources, data sources, modules, each/count/path/terraform: known only to Terraform. Skip
    // their attribute chain (the postfix loop turns it into UNKNOWN).
    return UNKNOWN
  }
  private call(name: string): Val {
    this.eat("(")
    const args: Val[] = []
    // try()/can() swallow errors of their arguments.
    const lenient = name === "try" || name === "can"
    while (!this.isOp(")")) {
      if (lenient) {
        const start = this.i
        try {
          args.push(this.ternary())
        } catch {
          this.i = start
          this.skipArg()
          args.push(UNKNOWN)
        }
      } else args.push(this.ternary())
      if (this.isOp("...")) this.i++
      if (this.isOp(",")) this.i++
      else break
    }
    this.eat(")")
    if (name === "try") return args.find((a) => !hasUnknown(a)) ?? UNKNOWN
    if (name === "can") return args.every((a) => !hasUnknown(a)) ? true : UNKNOWN
    const fn = this.fns[name]
    if (!fn) return UNKNOWN
    // A known function of unknown arguments is unknown (except coalesce/try, handled above).
    if (this.fns === FUNCTIONS && name !== "coalesce" && name !== "lookup" && name !== "merge" && args.includes(UNKNOWN)) return UNKNOWN
    return fn(args)
  }
  private skipArg() {
    let depth = 0
    while (this.peek().k !== "eof") {
      const t = this.peek()
      if (t.k === "op" && ["(", "[", "{"].includes(t.v)) depth++
      if (t.k === "op" && [")", "]", "}"].includes(t.v)) {
        if (depth === 0) return
        depth--
      }
      if (depth === 0 && t.k === "op" && t.v === ",") return
      this.i++
    }
  }
  private list(): Val {
    if (this.idAt() === "for") return this.forExpr("]")
    const out: Val[] = []
    while (!this.isOp("]")) {
      out.push(this.ternary())
      if (this.isOp(",")) this.i++
      else break
    }
    this.eat("]")
    return out
  }
  private object(): Val {
    if (this.idAt() === "for") return this.forExpr("}")
    const out: Record<string, Val> = {}
    while (!this.isOp("}")) {
      const bare = this.idAt()
      let key: Val
      if (bare && (this.isOp("=", 1) || this.isOp(":", 1))) {
        this.i++
        key = bare
      } else key = this.ternary()
      if (!this.isOp("=") && !this.isOp(":")) throw new SyntaxError("object")
      this.i++
      const v = this.ternary()
      if (typeof key === "string" || typeof key === "number") out[String(key)] = v
      else return this.skipTo("}")
      if (this.isOp(",")) this.i++
    }
    this.eat("}")
    return out
  }
  /** `[for x in xs : …]` / `{for k, v in m : k => v}`, with an optional `if`. */
  private forExpr(close: "]" | "}"): Val {
    this.i++ // for
    const names: string[] = []
    for (let id = this.idAt(); id && id !== "in"; id = this.idAt()) {
      names.push(id)
      this.i++
      if (this.isOp(",")) this.i++
    }
    this.i++ // in
    const coll = this.ternary()
    this.eat(":")
    const start = this.i
    this.skipTo(close, true)
    const body = this.toks.slice(start, this.i - 1)
    if (coll === UNKNOWN || coll === null || typeof coll !== "object" || names.length === 0 || names.length > 2) return UNKNOWN
    const entries: [Val, Val][] = Array.isArray(coll) ? coll.map((v, i) => [i, v]) : isRecord(coll) ? Object.keys(coll).sort().map((k) => [k, coll[k]]) : []
    const outList: Val[] = []
    const outMap: Record<string, Val> = {}
    for (const [k, v] of entries) {
      const scope = { ...this.scope, ...(names.length === 2 ? { [names[0]]: k, [names[1]]: v } : { [names[0]]: v }) }
      const p = new Parser([...body, { k: "eof" }], this.lookup, this.depth + 1, scope)
      const r = p.forBody(close)
      if (r === UNKNOWN) return UNKNOWN
      if (!r.keep) continue
      if (close === "]") outList.push(r.value)
      else if (typeof r.key === "string" || typeof r.key === "number") outMap[String(r.key)] = r.value
      else return UNKNOWN
    }
    return close === "]" ? outList : outMap
  }
  private forBody(close: "]" | "}"): { key?: Val; value: Val; keep: boolean } | typeof UNKNOWN {
    let key: Val
    if (close === "}") {
      key = this.ternary()
      this.eat("=>")
    }
    const value = this.ternary()
    if (this.isOp("...")) this.i++
    let keep = true
    if (this.idAt() === "if") {
      this.i++
      const c = truthy(this.ternary())
      if (c === UNKNOWN) return UNKNOWN
      keep = c
    }
    return { key, value, keep }
  }
  /** Skips to the matching closer; with `consume` past it. Returns UNKNOWN. */
  private skipTo(close: string, consume = false): Val {
    let depth = 0
    while (this.peek().k !== "eof") {
      const t = this.peek()
      this.i++
      if (t.k !== "op") continue
      if (["(", "[", "{"].includes(t.v)) depth++
      else if ([")", "]", "}"].includes(t.v)) {
        if (depth === 0 && t.v === close) {
          if (!consume) this.i--
          return UNKNOWN
        }
        depth--
      }
    }
    return UNKNOWN
  }
}

/** The index past the string literal opening at `i` (interpolations may hold strings). */
function skipString(src: string, i: number): number {
  for (let j = i + 1; j < src.length; j++) {
    if (src[j] === "\\") j++
    else if (src[j] === '"') return j + 1
    else if (src[j] === "$" && src[j + 1] === "{") {
      let depth = 1
      for (j += 2; j < src.length && depth > 0; j++) {
        if (src[j] === '"') j = skipString(src, j) - 1
        else if (src[j] === "{") depth++
        else if (src[j] === "}") depth--
      }
      j--
    }
  }
  return src.length
}

/**
 * The branch of a conditional (`cond ? a : b`) Terraform takes, as source text, when its condition
 * is known (nested conditionals are followed); undefined for other expressions or unknown conditions.
 */
export function liveBranch(expr: string, lookup: Lookup): string | undefined {
  let src = expr.trim()
  // "(cond ? a : b)": the conditional inside the parentheses.
  while (src.startsWith("(")) {
    let depth = 0
    let end = -1
    for (let i = 0; i < src.length && end < 0; i++) {
      if (src[i] === '"') i = skipString(src, i) - 1
      else if (src[i] === "(") depth++
      else if (src[i] === ")" && --depth === 0) end = i
    }
    if (end !== src.length - 1) break
    src = src.slice(1, -1).trim()
  }
  let depth = 0
  let q = -1
  let nested = 0
  let colon = -1
  for (let i = 0; i < src.length && colon < 0; i++) {
    const c = src[i]
    if (c === '"') i = skipString(src, i) - 1
    else if (c === "(" || c === "[" || c === "{") depth++
    else if (c === ")" || c === "]" || c === "}") depth--
    else if (depth === 0 && c === "?") {
      if (q < 0) q = i
      else nested++
    } else if (depth === 0 && c === ":" && q >= 0) {
      if (nested) nested--
      else colon = i
    }
  }
  if (q < 0 || colon < 0) return undefined
  const cond = truthy(evaluate(src.slice(0, q), lookup))
  if (cond === UNKNOWN) return undefined
  const branch = (cond ? src.slice(q + 1, colon) : src.slice(colon + 1)).trim()
  return liveBranch(branch, lookup) ?? branch
}

/** A variable's `object({…})` type: its attributes' types. */
class ObjectType {
  readonly fields: Record<string, Val>
  constructor(fields: Record<string, Val>) {
    this.fields = fields
  }
}
/** `optional(type, default)` inside an object type. */
class Optional {
  readonly type: Val
  readonly def: Val
  constructor(type: Val, def: Val) {
    this.type = type
    this.def = def
  }
}
const TYPE_FUNCTIONS: Record<string, (args: Val[]) => Val> = {
  object: ([fields]) => (isRecord(fields) ? new ObjectType(fields) : UNKNOWN),
  optional: ([type, def]) => new Optional(type, def === undefined ? null : def),
}

/** A variable's type constraint, as far as defaults go (`object({ x = optional(string, "a") })`). */
export function parseType(src: string): Val {
  const toks = tokenize(src)
  if (!toks) return UNKNOWN
  try {
    return new Parser(toks, () => UNKNOWN, 0, {}, TYPE_FUNCTIONS).parse()
  } catch {
    return UNKNOWN
  }
}

/** A value with the defaults of its type's optional attributes filled in, as Terraform does. */
export function withDefaults(v: Val, type: Val): Val {
  if (type instanceof Optional) return withDefaults(v, type.type)
  if (!(type instanceof ObjectType) || !isRecord(v)) return v
  const out: Record<string, Val> = { ...v }
  for (const [k, t] of Object.entries(type.fields)) {
    const cur = out[k]
    if ((cur === undefined || cur === null) && t instanceof Optional && t.def !== null) out[k] = withDefaults(t.def, t.type)
    else if (cur !== undefined) out[k] = withDefaults(cur, t)
  }
  return out
}

/** Evaluates a Terraform expression; UNKNOWN when it cannot. */
export function evaluate(src: string, lookup: Lookup, depth = 0, scope: Record<string, Val> = {}): Val {
  if (depth > 8) return UNKNOWN
  const toks = tokenize(src)
  if (!toks) return UNKNOWN
  try {
    return new Parser(toks, lookup, depth, scope).parse()
  } catch {
    return UNKNOWN
  }
}

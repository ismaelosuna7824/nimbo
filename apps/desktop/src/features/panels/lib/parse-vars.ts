import type { Value } from "@/lib/api/types"

/** Parses dotenv text: `KEY=value`, optional `export`, quotes, `#` comments. */
export function parseDotEnv(text: string): { key: string; value: string }[] {
  const out: { key: string; value: string }[] = []
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (!line || line.startsWith("#")) continue
    const m = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_.-]*)\s*=\s*(.*)$/.exec(line)
    if (!m) continue
    let value = m[2]
    const quote = value[0]
    if ((quote === '"' || quote === "'") && value.lastIndexOf(quote) > 0) {
      value = value.slice(1, value.lastIndexOf(quote))
      if (quote === '"') value = value.replace(/\\n/g, "\n").replace(/\\"/g, '"')
    } else {
      value = value.replace(/\s+#.*$/, "")
    }
    out.push({ key: m[1], value })
  }
  return out
}

/**
 * Parses simple tfvars text (`name = value` lines). Quoted strings, numbers
 * and booleans become literals; anything else (lists, maps, multi-line values
 * with balanced brackets) is kept as an HCL expression.
 */
export function parseTfvars(text: string): Record<string, Value> {
  const out: Record<string, Value> = {}
  const lines = text.split(/\r?\n/)
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim()
    if (!line || line.startsWith("#") || line.startsWith("//")) continue
    const m = /^([A-Za-z_][\w-]*)\s*=\s*(.*)$/.exec(line)
    if (!m) continue
    let expr = stripComment(m[2])
    let depth = bracketDepth(expr)
    while (depth > 0 && i + 1 < lines.length) {
      const next = stripComment(lines[++i].trimEnd())
      expr += `\n${next}`
      depth += bracketDepth(next)
    }
    out[m[1]] = toValue(expr.trim())
  }
  return out
}

function toValue(expr: string): Value {
  if (/^"(?:[^"\\$]|\\.)*"$/.test(expr)) {
    try {
      return { literal: JSON.parse(expr) as string }
    } catch {
      return { expr }
    }
  }
  if (/^-?\d+(\.\d+)?([eE][+-]?\d+)?$/.test(expr)) return { literal: Number(expr) }
  if (expr === "true" || expr === "false") return { literal: expr === "true" }
  return { expr }
}

function stripComment(s: string): string {
  let inString = false
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (c === "\\" && inString) i++
    else if (c === '"') inString = !inString
    else if (!inString && (c === "#" || (c === "/" && s[i + 1] === "/"))) return s.slice(0, i).trimEnd()
  }
  return s
}

function bracketDepth(s: string): number {
  let depth = 0
  let inString = false
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (c === "\\" && inString) i++
    else if (c === '"') inString = !inString
    else if (!inString && (c === "[" || c === "{" || c === "(")) depth++
    else if (!inString && (c === "]" || c === "}" || c === ")")) depth--
  }
  return depth
}

// Display helpers shared by completion, hover and signature help.

import { openExternal } from "@/components/open-external"
import type { CtyType, FunctionSignature } from "@/lib/api/types"

/** cty type JSON as Terraform writes it: "string", ["list","string"] -> list(string). */
export function typeName(t: CtyType | unknown): string {
  if (typeof t === "string") return t === "dynamic" ? "any" : t
  if (!Array.isArray(t)) return "any"
  const [kind, inner] = t as [string, unknown]
  if (kind === "object") {
    const keys = Object.keys((inner as Record<string, unknown>) ?? {})
    return keys.length > 3 ? `object({${keys.slice(0, 3).join(", ")}, …})` : `object({${keys.join(", ")}})`
  }
  if (kind === "tuple") return "tuple([…])"
  return `${kind}(${typeName(inner)})`
}

/** Element type of a list/set/map type; the type itself otherwise. */
export function elementType(t: CtyType | unknown): unknown {
  return Array.isArray(t) && (t[0] === "list" || t[0] === "set" || t[0] === "map") ? t[1] : t
}

/** Attribute names of an object type (also inside list/set/map). */
export function objectKeys(t: CtyType | unknown): Record<string, unknown> | null {
  const e = elementType(t)
  return Array.isArray(e) && e[0] === "object" ? ((e[1] as Record<string, unknown>) ?? {}) : null
}

export function signature(fn: FunctionSignature, active = -1): { text: string; activeFrom: number; activeTo: number } {
  let text = `${fn.name}(`
  let activeFrom = -1
  let activeTo = -1
  const params = [...fn.parameters.map((p) => `${p.name} ${typeName(p.type)}`)]
  if (fn.variadicParameter) params.push(`${fn.variadicParameter.name}... ${typeName(fn.variadicParameter.type)}`)
  const activeIndex = fn.variadicParameter && active >= fn.parameters.length ? fn.parameters.length : active
  params.forEach((p, i) => {
    if (i > 0) text += ", "
    if (i === activeIndex) activeFrom = text.length
    text += p
    if (i === activeIndex) activeTo = text.length
  })
  text += `) → ${typeName(fn.returnType)}`
  return { text, activeFrom, activeTo }
}

/** Markdown-ish description text: `code` spans become <code>. */
export function richText(text: string): HTMLElement {
  const el = document.createElement("div")
  el.className = "cm-tf-doc-text"
  text.split(/(`[^`]+`)/).forEach((part) => {
    if (part.startsWith("`") && part.endsWith("`") && part.length > 1) {
      const code = document.createElement("code")
      code.textContent = part.slice(1, -1)
      el.append(code)
    } else el.append(part)
  })
  return el
}

/** Info panel shared by completion info and hover: a title line, badges, a description and a docs link. */
export function docNode(opts: { title: string; badges?: string[]; description?: string; docsUrl?: string; extra?: string }): HTMLElement {
  const root = document.createElement("div")
  root.className = "cm-tf-doc"
  const head = document.createElement("div")
  head.className = "cm-tf-doc-title"
  head.textContent = opts.title
  root.append(head)
  if (opts.badges?.length) {
    const badges = document.createElement("div")
    badges.className = "cm-tf-doc-badges"
    for (const b of opts.badges) {
      const span = document.createElement("span")
      span.className = "cm-tf-badge"
      span.textContent = b
      badges.append(span)
    }
    root.append(badges)
  }
  if (opts.description) root.append(richText(opts.description))
  if (opts.extra) {
    const pre = document.createElement("pre")
    pre.className = "cm-tf-doc-code"
    pre.textContent = opts.extra
    root.append(pre)
  }
  if (opts.docsUrl) {
    const url = opts.docsUrl
    const a = document.createElement("a")
    a.className = "cm-tf-doc-link"
    a.href = url
    a.rel = "noreferrer"
    a.textContent = "Open in the Terraform Registry ↗"
    a.addEventListener("mousedown", (e) => e.preventDefault())
    a.addEventListener("click", (e) => {
      e.preventDefault()
      void openExternal(url)
    })
    root.append(a)
  }
  return root
}

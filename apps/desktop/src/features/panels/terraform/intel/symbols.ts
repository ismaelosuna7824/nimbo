// What every module directory of a workflow declares, for references,
// hover and go to definition. Built from the same tolerant scanner as the
// cursor context, so unsaved and half-written files count too.

import type { DownloadedModule } from "@/lib/api/types"
import { parse, type Attribute, type Block } from "./scan"

export interface Decl {
  /** Workflow path of the file declaring it ("" for downloaded modules' files, which can't be opened). */
  file: string
  /** Offset of the declaring block (or attribute) and its 1-based line. */
  from: number
  line: number
}

export interface VariableSymbol extends Decl {
  name: string
  type?: string
  description?: string
  default?: string
  sensitive: boolean
}

export interface ResourceSymbol extends Decl {
  kind: "resource" | "data"
  type: string
  name: string
  /** Set when the block has count / for_each. */
  repeat: "" | "count" | "for_each"
}

export interface ModuleCallSymbol extends Decl {
  name: string
  source?: string
  version?: string
}

export interface ValueSymbol extends Decl {
  name: string
  /** Source of its expression. */
  value: string
  description?: string
}

/** Declarations of one module directory. */
export interface ModuleSymbols {
  dir: string
  variables: Map<string, VariableSymbol>
  locals: Map<string, ValueSymbol>
  outputs: Map<string, ValueSymbol>
  /** "aws_s3_bucket.b" -> resource; "data.aws_x.y" -> data source. */
  resources: Map<string, ResourceSymbol>
  modules: Map<string, ModuleCallSymbol>
  /** Provider local names configured or required here. */
  providers: Set<string>
}

/** "modules/net/main.tf" -> "modules/net"; root files -> "". */
export function dirOf(path: string): string {
  const i = path.lastIndexOf("/")
  return i < 0 ? "" : path.slice(0, i)
}

function emptySymbols(dir: string): ModuleSymbols {
  return {
    dir,
    variables: new Map(),
    locals: new Map(),
    outputs: new Map(),
    resources: new Map(),
    modules: new Map(),
    providers: new Set(),
  }
}

/** The constant string of an attribute's value, if it is one. */
export function stringValue(text: string, a: Attribute | undefined): string | undefined {
  if (!a) return undefined
  const m = /^\s*"((?:[^"\\$]|\\.|\$(?!\{))*)"\s*$/.exec(text.slice(a.valueFrom, a.valueTo))
  return m ? m[1].replace(/\\(.)/g, "$1") : undefined
}

const attrOf = (b: Block, name: string) => b.attrs.find((a) => a.name === name)

/** One file's declarations, cached by content. */
interface FileDecls {
  text: string
  add: (into: ModuleSymbols) => void
}

const fileCache = new Map<string, FileDecls>()

function fileDecls(file: string, text: string): FileDecls {
  const key = file
  const hit = fileCache.get(key)
  if (hit && hit.text === text) return hit
  const p = parse(text)
  const lineStarts: number[] = [0]
  for (let i = text.indexOf("\n"); i >= 0; i = text.indexOf("\n", i + 1)) lineStarts.push(i + 1)
  const lineAt = (pos: number) => {
    let lo = 0
    let hi = lineStarts.length - 1
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1
      if (lineStarts[mid] <= pos) lo = mid
      else hi = mid - 1
    }
    return lo + 1
  }
  const src = (a: Attribute | undefined) => (a ? text.slice(a.valueFrom, a.valueTo).trim() : undefined)
  const decls: ((into: ModuleSymbols) => void)[] = []
  for (const b of p.root.blocks) {
    const base = { file, from: b.typeFrom, line: lineAt(b.typeFrom) }
    const [l0, l1] = b.labels.map((l) => l.text)
    switch (b.type) {
      case "variable":
        if (l0)
          decls.push((m) =>
            m.variables.set(l0, {
              ...base,
              name: l0,
              type: src(attrOf(b, "type")),
              description: stringValue(text, attrOf(b, "description")),
              default: src(attrOf(b, "default")),
              sensitive: src(attrOf(b, "sensitive")) === "true",
            }),
          )
        break
      case "locals":
        for (const a of b.attrs)
          decls.push((m) =>
            m.locals.set(a.name, { file, from: a.nameFrom, line: lineAt(a.nameFrom), name: a.name, value: src(a) ?? "" }),
          )
        break
      case "output":
        if (l0)
          decls.push((m) =>
            m.outputs.set(l0, {
              ...base,
              name: l0,
              value: src(attrOf(b, "value")) ?? "",
              description: stringValue(text, attrOf(b, "description")),
            }),
          )
        break
      case "resource":
      case "data":
        if (l0 && l1) {
          const kind = b.type
          const repeat = attrOf(b, "count") ? "count" : attrOf(b, "for_each") ? "for_each" : ""
          decls.push((m) => {
            m.resources.set(kind === "data" ? `data.${l0}.${l1}` : `${l0}.${l1}`, { ...base, kind, type: l0, name: l1, repeat })
            m.providers.add(l0.slice(0, l0.indexOf("_")))
          })
        }
        break
      case "module":
        if (l0)
          decls.push((m) =>
            m.modules.set(l0, {
              ...base,
              name: l0,
              source: stringValue(text, attrOf(b, "source")),
              version: stringValue(text, attrOf(b, "version")),
            }),
          )
        break
      case "provider":
        if (l0) decls.push((m) => m.providers.add(l0))
        break
      case "terraform":
        for (const rp of b.blocks.filter((x) => x.type === "required_providers"))
          for (const a of rp.attrs) decls.push((m) => m.providers.add(a.name))
        break
    }
  }
  const out = { text, add: (into: ModuleSymbols) => decls.forEach((d) => d(into)) }
  fileCache.set(key, out)
  return out
}

/** Declarations of every directory of a workspace, and module resolution between them. */
export class SymbolIndex {
  readonly files: Record<string, string>
  readonly downloaded: DownloadedModule[]
  private dirs = new Map<string, ModuleSymbols>()

  constructor(files: Record<string, string>, downloaded: DownloadedModule[]) {
    this.files = files
    this.downloaded = downloaded
  }

  /** Declarations of a workflow directory ("" = root). */
  module(dir: string): ModuleSymbols {
    let m = this.dirs.get(dir)
    if (m) return m
    m = emptySymbols(dir)
    for (const [path, text] of Object.entries(this.files)) {
      if (path.endsWith(".tf") && dirOf(path) === dir) fileDecls(path, text).add(m)
    }
    this.dirs.set(dir, m)
    return m
  }

  /** Declarations of the module a call in dir points at: a local directory, or a module Terraform downloaded. */
  callee(dir: string, call: ModuleCallSymbol): ModuleSymbols | null {
    const source = call.source
    if (!source) return null
    if (source.startsWith("./") || source.startsWith("../")) {
      const target = joinPath(dir, source)
      if (target === null) return null
      const m = this.module(target)
      return m.variables.size || m.outputs.size || m.resources.size ? m : null
    }
    const candidates = this.downloaded.filter((d) => d.key === call.name || d.key.endsWith(`.${call.name}`))
    const dm =
      candidates.find((d) => d.source === source || d.source.endsWith(`/${source}`) || d.source.endsWith(source)) ??
      (candidates.length === 1 ? candidates[0] : undefined)
    if (!dm) return null
    const key = `registry:${dm.key}`
    let m = this.dirs.get(key)
    if (!m) {
      m = emptySymbols(key)
      for (const [name, text] of Object.entries(dm.files)) fileDecls(`${key}/${name}`, text).add(m)
      // Downloaded files are not workflow files: nothing to open.
      for (const v of [...m.variables.values(), ...m.outputs.values()]) v.file = ""
      this.dirs.set(key, m)
    }
    return m
  }
}

/** Joins a relative module source to a directory; null when it leaves the workflow. */
export function joinPath(dir: string, rel: string): string | null {
  const parts = dir ? dir.split("/") : []
  for (const seg of rel.replace(/\\/g, "/").split("/")) {
    if (seg === "" || seg === ".") continue
    if (seg === "..") {
      if (parts.length === 0) return null
      parts.pop()
    } else parts.push(seg)
  }
  return parts.join("/")
}

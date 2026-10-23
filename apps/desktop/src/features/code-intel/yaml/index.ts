// Editor extensions of a schema-described YAML dialect (Kubernetes,
// Kustomize, Helm values…): completion, hover and validation as you type,
// per document of a multi-document stream. Formatting (format.ts) is wired
// by the Code tab for every YAML file.

import { autocompletion, type CompletionContext } from "@codemirror/autocomplete"
import { linter, type Diagnostic } from "@codemirror/lint"
import type { Extension } from "@codemirror/state"
import { hoverTooltip } from "@codemirror/view"
import { intelTheme } from "@/features/code-intel/lint"
import { documentAt } from "./context"
import type { JsonSchema } from "./schema"
import { schemaCompletions, schemaHover, type SchemaIntelOptions } from "./schema-intel"
import { parseYaml, type YamlDocument } from "./tree"
import { validateYaml } from "./validate"

export interface YamlSchemaIntelOptions extends SchemaIntelOptions {
  /** The schema of one document of the file (by its parsed tree); null: no schema features for it. */
  schemaFor: (doc: YamlDocument & { text: string }) => JsonSchema | null | Promise<JsonSchema | null>
  /** Underline what the schema rejects (default true). */
  validate?: boolean
  /** Lint source label (default "Schema"). */
  source?: string
}

/** The parsed document of `text` holding `pos`. */
function docAt(text: string, pos: number): YamlDocument & { text: string } {
  const range = documentAt(text, pos)
  const doc = parseYaml(text).find((d) => d.from <= pos && pos <= Math.max(d.to, d.from)) ?? { index: range.index, from: range.from, to: range.to, root: null }
  return { ...doc, text: text.slice(doc.from, doc.to) }
}

/** Completion, hover and (optionally) validation of a YAML file against per-document schemas. */
export function yamlSchemaIntel(opts: YamlSchemaIntelOptions): Extension[] {
  const out: Extension[] = [
    autocompletion({
      override: [
        async (cx: CompletionContext) => {
          if (cx.state.readOnly) return null
          const text = cx.state.doc.toString()
          const schema = await opts.schemaFor(docAt(text, cx.pos))
          return schema && schemaCompletions(text, cx.pos, cx.explicit, schema, opts)
        },
      ],
      icons: true,
      maxRenderedOptions: 120,
    }),
    hoverTooltip(async (view, pos) => {
      const text = view.state.doc.toString()
      const schema = await opts.schemaFor(docAt(text, pos))
      const h = schema && schemaHover(text, pos, schema, opts)
      return h ? { pos: h.from, end: h.to, above: true, create: () => ({ dom: h.dom }) } : null
    }),
    intelTheme,
  ]
  if (opts.validate !== false) {
    out.push(
      linter(
        async (view) => {
          const text = view.state.doc.toString()
          const diagnostics: Diagnostic[] = []
          for (const d of parseYaml(text)) {
            const schema = await opts.schemaFor({ ...d, text: text.slice(d.from, d.to) })
            if (!schema) continue
            for (const p of validateYaml(d.root, schema)) {
              diagnostics.push({ from: p.from, to: Math.max(p.to, p.from + 1), severity: p.severity, source: opts.source ?? "Schema", message: p.message })
            }
          }
          return view.state.doc.toString() === text ? diagnostics.filter((d) => d.to <= text.length) : []
        },
        { delay: 400 },
      ),
    )
  }
  return out
}


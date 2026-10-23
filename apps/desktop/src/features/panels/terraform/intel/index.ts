// Terraform language intelligence for the code editor: completion, hover
// docs, signature help, go to definition and diagnostics as you type.
//
// Completion, hover and navigation run in the client on a tolerant scanner
// of the edited text plus catalog data cached for the session (type index,
// functions, schemas fetched in batches and prefetched for the workflow's
// types), so typing never waits on the daemon. Diagnostics come from the
// daemon's HCL parser and catalog validation (POST /terraform/check),
// debounced.

import { autocompletion, type Completion } from "@codemirror/autocomplete"
import { StateField, type EditorState, type Extension } from "@codemirror/state"
import { hoverTooltip, showTooltip, type Tooltip } from "@codemirror/view"
import type { CodeWorkspace } from "@/lib/api/types"
import { definitionExtension } from "@/features/code-intel/definition"
import { checkLinter, intelTheme } from "@/features/code-intel/lint"
import { functionIndex, loadFunctions, loadTypes, prefetchSchemas } from "./catalog"
import { terraformCompletionSource, type CompletionEnv, type TfCompletion } from "./completion"
import { contextAt } from "./context"
import { definitionAt } from "./definition"
import { signature } from "./format"
import { hoverAt } from "./hover"
import { loadWorkspace, symbolIndex, type IntelOptions } from "./workspace"

export type { IntelOptions }

const EMPTY: CodeWorkspace = { files: {}, modules: [] }

/** Schemas the files use: resource/data types and providers. */
function usedSchemas(texts: Iterable<string>): ["resource" | "data" | "provider", string][] {
  const out = new Map<string, ["resource" | "data" | "provider", string]>()
  const re = /^[ \t]*(resource|data|provider)[ \t]+"([\w-]+)"/gm
  for (const t of texts) {
    for (const m of t.matchAll(re)) {
      const kind = m[1] as "resource" | "data" | "provider"
      out.set(`${kind}/${m[2]}`, [kind, m[2]])
    }
  }
  return [...out.values()]
}

/** The signature of the function call around the cursor, as a tooltip above it. */
function signatureTooltip(state: EditorState): Tooltip | null {
  const sel = state.selection.main
  if (!sel.empty) return null
  const fns = functionIndex()
  if (!fns) return null
  const ctx = contextAt(state.doc.toString(), sel.head)
  if (ctx.kind !== "value" || !ctx.call) return null
  const fn = fns.get(ctx.call.name)
  if (!fn) return null
  const sig = signature(fn, ctx.call.arg)
  return {
    pos: ctx.call.from,
    above: true,
    strictSide: true,
    arrow: false,
    create: () => {
      const dom = document.createElement("div")
      dom.className = "cm-tf-signature"
      dom.setAttribute("role", "status")
      if (sig.activeFrom >= 0) {
        const b = document.createElement("b")
        b.textContent = sig.text.slice(sig.activeFrom, sig.activeTo)
        dom.append(sig.text.slice(0, sig.activeFrom), b, sig.text.slice(sig.activeTo))
      } else dom.textContent = sig.text
      if (fn.description) {
        const d = document.createElement("div")
        d.className = "cm-tf-signature-doc"
        d.textContent = fn.description.replace(/`/g, "")
        dom.append(d)
      }
      return { dom }
    },
  }
}

const signatureHelp = StateField.define<Tooltip | null>({
  create: signatureTooltip,
  update: (value, tr) => (tr.docChanged || tr.selection ? signatureTooltip(tr.state) : value),
  provide: (f) => showTooltip.from(f),
})

/** Editor extensions for one Terraform file of a workflow. */
export function terraformIntel(opts: IntelOptions): Extension[] {
  void loadTypes().catch(() => {})
  void loadFunctions().catch(() => {})
  void loadWorkspace(opts.workflowId)
    .then((ws) => prefetchSchemas(usedSchemas([...Object.values(ws.files), ...Object.values(opts.buffers())])))
    .catch(() => {})

  const env = async (text: string): Promise<CompletionEnv> => {
    const ws = await loadWorkspace(opts.workflowId).catch(() => EMPTY)
    return { path: opts.path, symbols: symbolIndex(opts, text, ws) }
  }

  const findDefinition = (state: EditorState, pos: number) => {
    const text = state.doc.toString()
    return definitionAt(text, pos, { path: opts.path, symbols: symbolIndex(opts, text) })
  }

  return [
    autocompletion({
      override: [terraformCompletionSource(env)],
      icons: true,
      maxRenderedOptions: 80,
      optionClass: (c: Completion) => ((c as TfCompletion).deprecated ? "cm-tf-deprecated" : ""),
      addToOptions: [
        {
          position: 90,
          render: (c: Completion) => {
            if (!(c as TfCompletion).local) return null
            const span = document.createElement("span")
            span.className = "cm-tf-badge"
            span.textContent = "runs locally"
            return span
          },
        },
      ],
    }),
    hoverTooltip(async (view, pos) => {
      const text = view.state.doc.toString()
      const h = await hoverAt(text, pos, await env(text))
      if (!h) return null
      return { pos: h.from, end: h.to, above: true, create: () => ({ dom: h.dom }) }
    }),
    signatureHelp,
    definitionExtension(opts.path, findDefinition, opts.openFile),
    checkLinter(opts, (pr) => (pr.code === "syntax" ? "Terraform syntax" : "Terraform")),
    intelTheme,
  ]
}

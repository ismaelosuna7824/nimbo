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
import { linter, type Diagnostic } from "@codemirror/lint"
import { StateField, type EditorState, type Extension } from "@codemirror/state"
import { EditorView, hoverTooltip, keymap, showTooltip, type Tooltip } from "@codemirror/view"
import { api } from "@/lib/api/client"
import type { CodeWorkspace } from "@/lib/api/types"
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

const theme = EditorView.theme({
  ".cm-tooltip": {
    backgroundColor: "var(--popover)",
    color: "var(--popover-foreground)",
    border: "1px solid var(--border)",
    borderRadius: "8px",
    boxShadow: "0 8px 24px -8px rgb(0 0 0 / 0.25)",
    fontSize: "12px",
  },
  ".cm-tooltip.cm-tooltip-autocomplete > ul": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", maxHeight: "18em" },
  ".cm-tooltip.cm-tooltip-autocomplete > ul > li": { padding: "2px 8px 2px 4px", display: "flex", alignItems: "center", gap: "4px" },
  ".cm-tooltip.cm-tooltip-autocomplete > ul > li[aria-selected]": {
    backgroundColor: "color-mix(in oklch, var(--primary) 18%, transparent)",
    color: "var(--foreground)",
  },
  ".cm-tooltip.cm-tooltip-autocomplete > ul > completion-section": {
    color: "var(--muted-foreground)",
    fontSize: "10px",
    textTransform: "uppercase",
    letterSpacing: "0.04em",
    borderBottom: "1px solid var(--border)",
    padding: "4px 8px 2px",
  },
  ".cm-completionLabel": { flexShrink: "0" },
  ".cm-completionDetail": {
    marginLeft: "auto",
    paddingLeft: "12px",
    fontStyle: "normal",
    color: "var(--muted-foreground)",
    fontFamily: "var(--font-sans, system-ui)",
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  },
  ".cm-completionMatchedText": { textDecoration: "none", fontWeight: "600", color: "var(--primary)" },
  ".cm-tf-deprecated .cm-completionLabel": { textDecoration: "line-through", opacity: "0.7" },
  ".cm-tf-badge": {
    display: "inline-block",
    flexShrink: "0",
    fontFamily: "var(--font-sans, system-ui)",
    fontSize: "10px",
    lineHeight: "1.4",
    padding: "0 5px",
    borderRadius: "9999px",
    border: "1px solid color-mix(in oklch, var(--primary) 40%, transparent)",
    color: "var(--primary)",
    backgroundColor: "color-mix(in oklch, var(--primary) 10%, transparent)",
    whiteSpace: "nowrap",
  },
  ".cm-tooltip.cm-completionInfo": { padding: "0", maxWidth: "360px" },
  ".cm-tf-doc": { padding: "8px 10px", display: "flex", flexDirection: "column", gap: "6px", maxWidth: "420px", lineHeight: "1.45" },
  ".cm-tf-doc-title": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", fontWeight: "600", wordBreak: "break-all" },
  ".cm-tf-doc-badges": { display: "flex", flexWrap: "wrap", gap: "4px" },
  ".cm-tf-doc-text code, .cm-tf-doc-code": {
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
    backgroundColor: "var(--muted)",
    borderRadius: "4px",
    padding: "0 3px",
  },
  ".cm-tf-doc-code": { margin: "0", padding: "4px 6px", whiteSpace: "pre-wrap", maxHeight: "8em", overflow: "auto" },
  ".cm-tf-doc-link": { color: "var(--primary)", textDecoration: "underline", cursor: "pointer", width: "fit-content" },
  ".cm-tf-signature": {
    padding: "4px 8px",
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
    maxWidth: "520px",
  },
  ".cm-tf-signature b": { color: "var(--primary)" },
  ".cm-tf-signature-doc": { fontFamily: "var(--font-sans, system-ui)", color: "var(--muted-foreground)", marginTop: "2px" },
  ".cm-diagnostic": { padding: "4px 8px", borderLeftWidth: "3px" },
  ".cm-diagnostic-error": { borderLeftColor: "var(--destructive)" },
  ".cm-diagnostic-warning": { borderLeftColor: "#f59e0b" },
  ".cm-lintRange-warning": {
    backgroundImage: "none",
    textDecoration: "underline wavy #f59e0b",
    textUnderlineOffset: "3px",
  },
  ".cm-lintRange-error": {
    backgroundImage: "none",
    textDecoration: "underline wavy var(--destructive)",
    textUnderlineOffset: "3px",
  },
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

  /** Jumps to the declaration of the name at pos; false when there is none. */
  const goToDefinition = (view: EditorView, pos: number): boolean => {
    const text = view.state.doc.toString()
    const target = definitionAt(text, pos, { path: opts.path, symbols: symbolIndex(opts, text) })
    if (!target) return false
    if (target.file === opts.path) {
      view.dispatch({ selection: { anchor: target.from }, effects: EditorView.scrollIntoView(target.from, { y: "center" }) })
      view.focus()
    } else opts.openFile(target.file, { line: target.line })
    return true
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
    keymap.of([{ key: "F12", run: (view) => goToDefinition(view, view.state.selection.main.head) }]),
    EditorView.domEventHandlers({
      mousedown: (e, view) => {
        if (!(e.metaKey || e.ctrlKey) || e.button !== 0) return false
        const pos = view.posAtCoords({ x: e.clientX, y: e.clientY })
        if (pos === null || !goToDefinition(view, pos)) return false
        e.preventDefault()
        return true
      },
    }),
    linter(
      async (view) => {
        const doc = view.state.doc
        const files = { ...opts.buffers(), [opts.path]: doc.toString() }
        let problems
        try {
          ;({ problems } = await api.checkCode(opts.workflowId, opts.path, files))
        } catch {
          return []
        }
        return problems.map<Diagnostic>((pr) => {
          const line = doc.line(Math.min(Math.max(1, pr.line), doc.lines))
          let from = Math.min(line.from + Math.max(0, pr.column - 1), line.to)
          const endLine = doc.line(Math.min(Math.max(pr.line, pr.endLine), doc.lines))
          let to = Math.min(endLine.from + Math.max(0, pr.endColumn - 1), endLine.to)
          // A range that only covers a line break (e.g. a missing value) underlines the line's last character.
          if (to > line.to && from === line.to) to = line.to
          if (to <= from) {
            from = Math.max(line.from, line.to - 1)
            to = line.to > from ? line.to : Math.min(from + 1, doc.length)
          }
          return {
            from,
            to,
            severity: pr.severity,
            source: pr.code === "syntax" ? "Terraform syntax" : "Terraform",
            message: pr.detail ? `${pr.message} — ${pr.detail}` : pr.message,
          }
        })
      },
      { delay: 500 },
    ),
    theme,
  ]
}

// Diagnostics as you type for every language plug-in of the code editor,
// from the daemon's kind-aware check (POST /workflows/{id}/terraform/check:
// Terraform, compose files, Dockerfiles, Kubernetes manifests), with quick
// fixes: rules that have a mechanical fix offer it as a lint action that
// edits the file (and may create others, e.g. a .dockerignore).

import { linter, type Action, type Diagnostic } from "@codemirror/lint"
import type { Extension } from "@codemirror/state"
import { EditorView } from "@codemirror/view"
import { api } from "@/lib/api/client"
import type { CodeProblem } from "@/lib/api/types"

/** One mechanical fix of a problem. */
export interface QuickFix {
  /** Button label, e.g. "Add restart: unless-stopped". */
  name: string
  /** Edits of the checked file (offsets in its current text). */
  changes?: { from: number; to: number; insert: string }[]
  /** Other files to create or overwrite (workflow paths), staged like edits in the Code tab. */
  files?: Record<string, string>
}

export interface QuickFixContext {
  /** Current text of the checked file. */
  text: string
  /** Its workflow path. */
  path: string
  /** The unsaved text of the workflow's open files (this one included), by workflow path. */
  files: () => Record<string, string>
}

/** The fixes of a problem (none for most). */
export type QuickFixProvider = (pr: CodeProblem, ctx: QuickFixContext) => QuickFix[]

/** Where fixes that touch other files go (the Code tab stages them as pending edits). */
export interface QuickFixHost {
  writeFiles(files: Record<string, string>): void
}

/** Offsets of a 1-based line of `text` (clamped to the text). */
export function lineRange(text: string, line: number): { from: number; to: number } {
  let from = 0
  for (let n = 1; n < line; n++) {
    const nl = text.indexOf("\n", from)
    if (nl < 0) return { from: text.length, to: text.length }
    from = nl + 1
  }
  const nl = text.indexOf("\n", from)
  return { from, to: nl < 0 ? text.length : nl }
}

export interface CheckOptions {
  workflowId: string
  /** Workflow path of the edited file. */
  path: string
  /** Unsaved text of the other files (checks see the project as edited). */
  buffers: () => Record<string, string>
}

/** Tooltips, completion list and lint underlines of the code editor's language features (every workspace kind). */
export const intelTheme = EditorView.theme({
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
  ".cm-diagnosticAction": {
    font: "inherit",
    fontSize: "11px",
    marginLeft: "8px",
    padding: "1px 8px",
    borderRadius: "6px",
    border: "1px solid color-mix(in oklch, var(--primary) 40%, transparent)",
    backgroundColor: "color-mix(in oklch, var(--primary) 12%, transparent)",
    color: "var(--primary)",
    cursor: "pointer",
  },
  ".cm-diagnosticAction:hover": { backgroundColor: "color-mix(in oklch, var(--primary) 22%, transparent)" },
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

/**
 * Diagnostics as you type from POST /workflows/{id}/terraform/check (kind-aware), debounced; `source` labels each
 * problem, `fixes` turns problems with a mechanical fix into lint actions applied through the editor (and `host`
 * for other files).
 */
export function checkLinter(
  opts: CheckOptions,
  source: (pr: CodeProblem) => string,
  fixes?: QuickFixProvider,
  host?: QuickFixHost,
): Extension {
  return linter(
    async (view) => {
      const doc = view.state.doc
      const text = doc.toString()
      const files = { ...opts.buffers(), [opts.path]: text }
      let problems
      try {
        ;({ problems } = await api.checkCode(opts.workflowId, opts.path, files))
      } catch {
        return []
      }
      // The text may have changed while the check ran: fixes computed on it would land in the wrong place.
      if (view.state.doc.toString() !== text) return []
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
        const actions: Action[] = (fixes?.(pr, { text, path: opts.path, files: () => files }) ?? []).map((fix) => ({
          name: fix.name,
          apply: (v) => {
            if (fix.changes?.length) v.dispatch({ changes: fix.changes, userEvent: "input.quickfix" })
            if (fix.files && host) host.writeFiles(fix.files)
          },
        }))
        return {
          from,
          to,
          severity: pr.severity,
          source: source(pr),
          message: pr.detail ? `${pr.message} — ${pr.detail}` : pr.message,
          ...(actions.length ? { actions } : {}),
        }
      })
    },
    { delay: 500 },
  )
}

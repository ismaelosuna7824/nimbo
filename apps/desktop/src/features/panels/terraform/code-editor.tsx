import { useEffect, useImperativeHandle, useLayoutEffect, useRef, type Ref } from "react"
import { useTheme } from "next-themes"
import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete"
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands"
import { bracketMatching, foldGutter, foldKeymap, indentOnInput, syntaxHighlighting } from "@codemirror/language"
import {
  findNext,
  highlightSelectionMatches,
  openSearchPanel,
  search,
  searchKeymap,
  SearchQuery,
  setSearchQuery,
} from "@codemirror/search"
import { Annotation, Compartment, EditorState, RangeSetBuilder, Transaction, type Extension } from "@codemirror/state"
import {
  Decoration,
  drawSelection,
  EditorView,
  gutter,
  GutterMarker,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from "@codemirror/view"
import type { FileLanguage, Severity } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { codeHighlightDark, codeHighlightLight, loadLanguage } from "./languages"

const baseTheme = EditorView.theme({
  "&": { height: "100%", fontSize: "12px", backgroundColor: "var(--background)", color: "var(--foreground)" },
  ".cm-scroller": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", lineHeight: "1.55" },
  ".cm-gutters": {
    backgroundColor: "var(--background)",
    color: "color-mix(in oklch, var(--muted-foreground) 70%, transparent)",
    borderRight: "1px solid var(--border)",
  },
  ".cm-lineNumbers .cm-gutterElement": { padding: "0 6px 0 10px" },
  ".cm-activeLine": { backgroundColor: "color-mix(in oklch, var(--muted) 45%, transparent)" },
  ".cm-activeLineGutter": {
    backgroundColor: "color-mix(in oklch, var(--muted) 60%, transparent)",
    color: "var(--foreground)",
  },
  "&.cm-focused": { outline: "none" },
  ".cm-cursor": { borderLeftColor: "var(--foreground)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": {
    backgroundColor: "color-mix(in oklch, var(--primary) 22%, transparent) !important",
  },
  ".cm-panels": { backgroundColor: "var(--muted)", color: "var(--foreground)" },
  ".cm-search": {
    fontSize: "12px",
    padding: "4px 28px 4px 8px",
    display: "flex",
    flexWrap: "wrap",
    alignItems: "center",
    gap: "4px 6px",
  },
  ".cm-search br": { display: "none" },
  ".cm-search label": { fontSize: "12px", color: "var(--muted-foreground)", display: "inline-flex", alignItems: "center", gap: "3px" },
  ".cm-search .cm-textfield, .cm-search .cm-button, .cm-search label": { margin: "0" },
  ".cm-panels-top": { borderBottom: "1px solid var(--border)" },
  ".cm-textfield": {
    fontSize: "12px",
    backgroundColor: "var(--background)",
    color: "var(--foreground)",
    border: "1px solid var(--border)",
    borderRadius: "6px",
    padding: "2px 6px",
  },
  ".cm-textfield:focus": { outline: "2px solid color-mix(in oklch, var(--primary) 50%, transparent)" },
  ".cm-button": {
    fontSize: "12px",
    backgroundImage: "none",
    backgroundColor: "var(--background)",
    color: "var(--foreground)",
    border: "1px solid var(--border)",
    borderRadius: "6px",
    padding: "2px 8px",
  },
  ".cm-search [name=close]": { color: "var(--muted-foreground)", fontSize: "16px", top: "2px" },
  ".cm-searchMatch": { backgroundColor: "color-mix(in oklch, orange 30%, transparent)" },
  ".cm-searchMatch-selected": { backgroundColor: "color-mix(in oklch, var(--primary) 45%, transparent)" },
  ".cm-foldGutter span": { padding: "0 4px" },
  ".cm-diag-gutter .cm-gutterElement": { display: "flex", alignItems: "center", justifyContent: "center", width: "12px" },
  ".cm-diag-marker": { display: "inline-block", width: "7px", height: "7px", borderRadius: "9999px", cursor: "help" },
  ".cm-diag-marker-error": { backgroundColor: "var(--destructive)" },
  ".cm-diag-marker-warning": { backgroundColor: "#f59e0b" },
  ".cm-diag-marker-info": { backgroundColor: "var(--primary)" },
  ".cm-diag-line-error": { backgroundColor: "color-mix(in oklch, var(--destructive) 14%, transparent)" },
  ".cm-diag-line-warning": { backgroundColor: "color-mix(in oklch, #f59e0b 12%, transparent)" },
})

export interface EditorDiagnostic {
  /** 1-based. */
  line: number
  severity: Severity
  message: string
}

export interface CodeEditorHandle {
  focus: () => void
  openSearch: () => void
  /** Moves the cursor to a 1-based line and centers it. */
  revealLine: (line: number) => void
  /** Opens the search panel with `query` and selects the next match. */
  find: (query: string) => void
  /** Replaces the text as an undoable edit (reported through onChange), e.g. after formatting. */
  replaceDocument: (text: string) => void
}

interface CodeEditorProps {
  ref?: Ref<CodeEditorHandle>
  /** Identifies the document; each keeps its own undo history, selection and scroll position. */
  docId: string
  value: string
  language: FileLanguage
  readOnly: boolean
  wrap: boolean
  diagnostics?: EditorDiagnostic[]
  onChange?: (docId: string, value: string) => void
  /** Documents whose editor state is kept (the open tabs); the others are dropped. */
  keep: readonly string[]
  className?: string
  /** Extra extensions of this document (e.g. Terraform language intelligence); keep the array stable per document. */
  extensions?: Extension[]
}

const SEVERITY_RANK: Record<Severity, number> = { error: 0, warning: 1, info: 2 }
const NO_DIAGNOSTICS: EditorDiagnostic[] = []
const NO_EXTENSIONS: Extension[] = []
/** Marks transactions that sync the document from props, so they are not reported as edits. */
const external = Annotation.define<boolean>()

class DiagnosticMarker extends GutterMarker {
  readonly severity: Severity
  readonly message: string
  constructor(severity: Severity, message: string) {
    super()
    this.severity = severity
    this.message = message
  }
  eq(other: DiagnosticMarker) {
    return other.severity === this.severity && other.message === this.message
  }
  toDOM() {
    const el = document.createElement("span")
    el.className = `cm-diag-marker cm-diag-marker-${this.severity}`
    // The gutter's width spacer has no message and stays out of the accessibility tree.
    if (this.message) {
      el.title = this.message
      el.setAttribute("aria-label", `${this.severity}: ${this.message}`)
    } else {
      el.setAttribute("aria-hidden", "true")
    }
    return el
  }
}

/** Gutter dots and line tints for diagnostics (the most severe one per line wins). */
function diagnosticsExtension(diagnostics: EditorDiagnostic[]): Extension {
  if (diagnostics.length === 0) return []
  const byLine = new Map<number, EditorDiagnostic>()
  for (const d of diagnostics) {
    const prev = byLine.get(d.line)
    if (!prev) byLine.set(d.line, d)
    else if (SEVERITY_RANK[d.severity] < SEVERITY_RANK[prev.severity]) byLine.set(d.line, { ...d, message: `${d.message}\n${prev.message}` })
    else byLine.set(d.line, { ...prev, message: `${prev.message}\n${d.message}` })
  }
  const lines = [...byLine.keys()].sort((a, b) => a - b)
  return [
    gutter({
      class: "cm-diag-gutter",
      markers: (view) => {
        const b = new RangeSetBuilder<GutterMarker>()
        for (const n of lines) {
          if (n < 1 || n > view.state.doc.lines) continue
          const d = byLine.get(n)!
          b.add(view.state.doc.line(n).from, view.state.doc.line(n).from, new DiagnosticMarker(d.severity, d.message))
        }
        return b.finish()
      },
      initialSpacer: () => new DiagnosticMarker("error", ""),
    }),
    EditorView.decorations.of((view) => {
      const b = new RangeSetBuilder<Decoration>()
      for (const n of lines) {
        if (n < 1 || n > view.state.doc.lines) continue
        const from = view.state.doc.line(n).from
        b.add(from, from, Decoration.line({ class: `cm-diag-line-${byLine.get(n)!.severity}` }))
      }
      return b.finish()
    }),
  ]
}

/** CodeMirror 6 editor for the file viewer; follows the app's light/dark theme. */
export function CodeEditor({
  ref,
  docId,
  value,
  language,
  readOnly,
  wrap,
  diagnostics = NO_DIAGNOSTICS,
  onChange,
  keep,
  className,
  extensions = NO_EXTENSIONS,
}: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const onChangeRef = useRef(onChange)
  const docRef = useRef(docId)
  /** The last text reported through onChange, so echoing it back as `value` costs nothing. */
  const lastText = useRef(value)
  const saved = useRef(new Map<string, { state: EditorState; scrollTop: number }>())
  const c = useRef({
    language: new Compartment(),
    readOnly: new Compartment(),
    wrap: new Compartment(),
    highlight: new Compartment(),
    diagnostics: new Compartment(),
    extensions: new Compartment(),
  }).current
  const { resolvedTheme } = useTheme()
  const dark = resolvedTheme !== "light"
  const config = useRef({ readOnly, wrap, dark, diagnostics, extensions })
  config.current = { readOnly, wrap, dark, diagnostics, extensions }

  useEffect(() => {
    onChangeRef.current = onChange
  }, [onChange])

  const createState = (id: string, doc: string): EditorState => {
    const cfg = config.current
    return EditorState.create({
      doc,
      extensions: [
        lineNumbers(),
        c.diagnostics.of(diagnosticsExtension(cfg.diagnostics)),
        foldGutter(),
        highlightActiveLineGutter(),
        history(),
        drawSelection(),
        indentOnInput(),
        bracketMatching(),
        closeBrackets(),
        highlightActiveLine(),
        highlightSelectionMatches(),
        search({ top: true }),
        keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...searchKeymap, ...foldKeymap, indentWithTab]),
        baseTheme,
        EditorState.tabSize.of(2),
        EditorView.contentAttributes.of({ "aria-label": `Contents of ${id}` }),
        c.language.of([]),
        c.highlight.of(syntaxHighlighting(cfg.dark ? codeHighlightDark : codeHighlightLight)),
        c.readOnly.of(EditorState.readOnly.of(cfg.readOnly)),
        c.wrap.of(cfg.wrap ? EditorView.lineWrapping : []),
        c.extensions.of(cfg.extensions),
        EditorView.updateListener.of((u) => {
          if (!u.docChanged || u.transactions.some((tr) => tr.annotation(external))) return
          lastText.current = u.state.doc.toString()
          onChangeRef.current?.(docRef.current, lastText.current)
        }),
      ],
    })
  }

  useEffect(() => {
    const v = new EditorView({ parent: host.current!, state: createState(docId, value) })
    view.current = v
    return () => {
      v.destroy()
      view.current = null
    }
    // The view is created once; later prop changes are applied by the effects below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Switch documents (keeping each one's state) and sync the text from props.
  useLayoutEffect(() => {
    const v = view.current
    if (!v) return
    if (docRef.current !== docId) {
      saved.current.set(docRef.current, { state: v.state, scrollTop: v.scrollDOM.scrollTop })
      const restored = saved.current.get(docId)
      v.setState(restored?.state ?? createState(docId, value))
      docRef.current = docId
      lastText.current = v.state.doc.toString()
      const cfg = config.current
      v.dispatch({
        effects: [
          c.readOnly.reconfigure(EditorState.readOnly.of(cfg.readOnly)),
          c.wrap.reconfigure(cfg.wrap ? EditorView.lineWrapping : []),
          c.highlight.reconfigure(syntaxHighlighting(cfg.dark ? codeHighlightDark : codeHighlightLight)),
          c.diagnostics.reconfigure(diagnosticsExtension(cfg.diagnostics)),
          c.extensions.reconfigure(cfg.extensions),
        ],
      })
      const top = restored?.scrollTop ?? 0
      requestAnimationFrame(() => v.scrollDOM.scrollTo({ top }))
    }
    if (value !== lastText.current && value !== v.state.doc.toString()) {
      v.dispatch({
        changes: { from: 0, to: v.state.doc.length, insert: value },
        annotations: [external.of(true), Transaction.addToHistory.of(false)],
      })
    }
    lastText.current = value
    // createState reads the latest props through `config`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [docId, value])

  useEffect(() => {
    let cancelled = false
    void loadLanguage(language).then((ext) => {
      if (!cancelled && docRef.current === docId) view.current?.dispatch({ effects: c.language.reconfigure(ext) })
    })
    return () => {
      cancelled = true
    }
  }, [c, docId, language])

  useEffect(() => {
    view.current?.dispatch({ effects: c.readOnly.reconfigure(EditorState.readOnly.of(readOnly)) })
  }, [c, readOnly])

  useEffect(() => {
    view.current?.dispatch({ effects: c.wrap.reconfigure(wrap ? EditorView.lineWrapping : []) })
  }, [c, wrap])

  useEffect(() => {
    view.current?.dispatch({
      effects: c.highlight.reconfigure(syntaxHighlighting(dark ? codeHighlightDark : codeHighlightLight)),
    })
  }, [c, dark])

  useEffect(() => {
    view.current?.dispatch({ effects: c.diagnostics.reconfigure(diagnosticsExtension(diagnostics)) })
  }, [c, diagnostics])

  useEffect(() => {
    view.current?.dispatch({ effects: c.extensions.reconfigure(extensions) })
  }, [c, extensions])

  useEffect(() => {
    for (const id of saved.current.keys()) if (!keep.includes(id)) saved.current.delete(id)
  }, [keep])

  useImperativeHandle(
    ref,
    () => ({
      focus: () => view.current?.focus(),
      openSearch: () => {
        if (view.current) openSearchPanel(view.current)
      },
      revealLine: (line) => {
        const v = view.current
        if (!v) return
        const { from } = v.state.doc.line(Math.min(Math.max(1, line), v.state.doc.lines))
        v.dispatch({ selection: { anchor: from }, effects: EditorView.scrollIntoView(from, { y: "center" }) })
        v.focus()
      },
      find: (query) => {
        const v = view.current
        if (!v || !query) return
        v.dispatch({ effects: setSearchQuery.of(new SearchQuery({ search: query })) })
        openSearchPanel(v)
        findNext(v)
      },
      replaceDocument: (text) => {
        const v = view.current
        if (!v || v.state.readOnly) return
        const old = v.state.doc.toString()
        // Only the differing middle changes, so the cursor and scroll position survive.
        let start = 0
        while (start < old.length && start < text.length && old[start] === text[start]) start++
        let end = 0
        while (
          end < old.length - start &&
          end < text.length - start &&
          old[old.length - 1 - end] === text[text.length - 1 - end]
        )
          end++
        if (start === old.length && start === text.length) return
        v.dispatch({
          changes: { from: start, to: old.length - end, insert: text.slice(start, text.length - end) },
          userEvent: "input.format",
        })
      },
    }),
    [],
  )

  return <div ref={host} className={cn("h-full min-h-0 overflow-hidden", className)} />
}

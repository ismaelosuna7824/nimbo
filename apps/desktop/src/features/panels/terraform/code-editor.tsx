import { useEffect, useRef } from "react"
import { useTheme } from "next-themes"
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands"
import { bracketMatching, foldGutter, indentOnInput, syntaxHighlighting } from "@codemirror/language"
import { highlightSelectionMatches, search, searchKeymap } from "@codemirror/search"
import { Compartment, EditorState } from "@codemirror/state"
import {
  drawSelection,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from "@codemirror/view"
import { cn } from "@/lib/utils"
import { hclHighlightDark, hclHighlightLight, hclLanguage } from "./hcl-language"

const baseTheme = EditorView.theme({
  "&": { height: "100%", fontSize: "12px", backgroundColor: "var(--background)", color: "var(--foreground)" },
  ".cm-scroller": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", lineHeight: "1.55" },
  ".cm-gutters": {
    backgroundColor: "var(--background)",
    color: "var(--muted-foreground)",
    borderRight: "1px solid var(--border)",
  },
  ".cm-activeLine": { backgroundColor: "color-mix(in oklch, var(--muted) 45%, transparent)" },
  ".cm-activeLineGutter": { backgroundColor: "color-mix(in oklch, var(--muted) 60%, transparent)" },
  "&.cm-focused": { outline: "none" },
  ".cm-cursor": { borderLeftColor: "var(--foreground)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": {
    backgroundColor: "color-mix(in oklch, var(--primary) 22%, transparent) !important",
  },
  ".cm-panels": { backgroundColor: "var(--muted)", color: "var(--foreground)" },
  ".cm-searchMatch": { backgroundColor: "color-mix(in oklch, orange 30%, transparent)" },
  ".cm-foldGutter span": { padding: "0 4px" },
})

interface CodeEditorProps {
  value: string
  onChange?: (value: string) => void
  readOnly?: boolean
  className?: string
}

/** CodeMirror 6 editor for HCL that follows the app's light/dark theme. */
export function CodeEditor({ value, onChange, readOnly = false, className }: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const onChangeRef = useRef(onChange)
  const compartments = useRef({ readOnly: new Compartment(), highlight: new Compartment() })
  const { resolvedTheme } = useTheme()
  const dark = resolvedTheme !== "light"

  useEffect(() => {
    onChangeRef.current = onChange
  }, [onChange])

  useEffect(() => {
    const c = compartments.current
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          highlightActiveLineGutter(),
          foldGutter(),
          history(),
          drawSelection(),
          indentOnInput(),
          bracketMatching(),
          highlightActiveLine(),
          highlightSelectionMatches(),
          search({ top: true }),
          keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, indentWithTab]),
          hclLanguage,
          baseTheme,
          EditorState.tabSize.of(2),
          c.highlight.of(syntaxHighlighting(dark ? hclHighlightDark : hclHighlightLight)),
          c.readOnly.of([EditorState.readOnly.of(readOnly), EditorView.editable.of(!readOnly)]),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) onChangeRef.current?.(u.state.doc.toString())
          }),
        ],
      }),
    })
    view.current = v
    return () => {
      v.destroy()
      view.current = null
    }
    // The view is created once; later prop changes are applied by the effects below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    const v = view.current
    if (!v || v.state.doc.toString() === value) return
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } })
  }, [value])

  useEffect(() => {
    view.current?.dispatch({
      effects: compartments.current.readOnly.reconfigure([
        EditorState.readOnly.of(readOnly),
        EditorView.editable.of(!readOnly),
      ]),
    })
  }, [readOnly])

  useEffect(() => {
    view.current?.dispatch({
      effects: compartments.current.highlight.reconfigure(
        syntaxHighlighting(dark ? hclHighlightDark : hclHighlightLight),
      ),
    })
  }, [dark])

  return <div ref={host} className={cn("h-full min-h-0 overflow-hidden", className)} />
}

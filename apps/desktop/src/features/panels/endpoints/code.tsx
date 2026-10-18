import { useEffect, useMemo, useRef } from "react"
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands"
import { bracketMatching, StreamLanguage, syntaxHighlighting } from "@codemirror/language"
import { go } from "@codemirror/legacy-modes/mode/go"
import { javascript, json } from "@codemirror/legacy-modes/mode/javascript"
import { python } from "@codemirror/legacy-modes/mode/python"
import { shell } from "@codemirror/legacy-modes/mode/shell"
import { EditorState } from "@codemirror/state"
import { drawSelection, EditorView, keymap, placeholder as placeholderExt } from "@codemirror/view"
import { highlightCode, tagHighlighter, tags as t } from "@lezer/highlight"
import { cn } from "@/lib/utils"

const LANGUAGES: Record<string, StreamLanguage<unknown>> = {
  bash: StreamLanguage.define(shell),
  shell: StreamLanguage.define(shell),
  javascript: StreamLanguage.define(javascript),
  json: StreamLanguage.define(json),
  python: StreamLanguage.define(python),
  go: StreamLanguage.define(go),
}

/**
 * Token colors as Tailwind classes, so the same highlighter serves static
 * snippets and editors and follows the light/dark theme without extra CSS.
 */
const highlighter = tagHighlighter([
  { tag: t.comment, class: "italic text-muted-foreground" },
  { tag: [t.keyword, t.controlKeyword, t.definitionKeyword, t.moduleKeyword], class: "font-semibold text-fuchsia-700 dark:text-fuchsia-400" },
  { tag: [t.string, t.special(t.string)], class: "text-emerald-700 dark:text-emerald-400" },
  { tag: [t.number, t.bool, t.null, t.atom], class: "text-amber-700 dark:text-amber-400" },
  { tag: [t.propertyName, t.attributeName], class: "text-sky-700 dark:text-sky-400" },
  { tag: [t.definition(t.variableName), t.function(t.variableName)], class: "text-violet-700 dark:text-violet-300" },
  { tag: [t.special(t.variableName), t.standard(t.variableName)], class: "text-rose-700 dark:text-rose-300" },
  { tag: [t.typeName, t.className], class: "text-teal-700 dark:text-teal-300" },
  { tag: t.operator, class: "text-muted-foreground" },
])

/** Read-only, syntax-highlighted code (no editor instance: cheap enough for many snippets). */
export function HighlightedCode({ code, language, className }: { code: string; language: string; className?: string }) {
  const parts = useMemo(() => {
    const lang = LANGUAGES[language]
    if (!lang) return [{ text: code, cls: "" }]
    const out: { text: string; cls: string }[] = []
    highlightCode(
      code,
      lang.parser.parse(code),
      highlighter,
      (text, cls) => out.push({ text, cls }),
      () => out.push({ text: "\n", cls: "" }),
    )
    return out
  }, [code, language])
  return (
    <pre className={cn("overflow-auto font-mono text-[11px] leading-relaxed whitespace-pre", className)}>
      <code>
        {parts.map((p, i) =>
          p.cls ? (
            <span key={i} className={p.cls}>
              {p.text}
            </span>
          ) : (
            p.text
          ),
        )}
      </code>
    </pre>
  )
}

const inputTheme = EditorView.theme({
  "&": { fontSize: "11px", backgroundColor: "transparent", color: "var(--foreground)" },
  ".cm-scroller": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", lineHeight: "1.55" },
  ".cm-content": { padding: "6px 0" },
  ".cm-line": { padding: "0 8px" },
  "&.cm-focused": { outline: "none" },
  ".cm-cursor": { borderLeftColor: "var(--foreground)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": {
    backgroundColor: "color-mix(in oklch, var(--primary) 22%, transparent) !important",
  },
  ".cm-placeholder": { color: "var(--muted-foreground)" },
})

/** Small editable code box (JSON bodies, message payloads) with highlighting. */
export function CodeInput({
  value,
  onChange,
  language,
  placeholder,
  className,
  ariaLabel,
}: {
  value: string
  onChange: (value: string) => void
  language: string
  placeholder?: string
  className?: string
  ariaLabel: string
}) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const onChangeRef = useRef(onChange)

  useEffect(() => {
    onChangeRef.current = onChange
  }, [onChange])

  useEffect(() => {
    const lang = LANGUAGES[language]
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: value,
        extensions: [
          history(),
          drawSelection(),
          bracketMatching(),
          keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
          lang ? [lang, syntaxHighlighting(highlighter)] : [],
          EditorView.lineWrapping,
          EditorState.tabSize.of(2),
          EditorView.contentAttributes.of({ "aria-label": ariaLabel }),
          placeholder ? placeholderExt(placeholder) : [],
          inputTheme,
          EditorView.updateListener.of((u) => {
            if (u.docChanged) onChangeRef.current(u.state.doc.toString())
          }),
        ],
      }),
    })
    view.current = v
    return () => {
      v.destroy()
      view.current = null
    }
    // Recreated only when the language changes; value updates are applied below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [language])

  useEffect(() => {
    const v = view.current
    if (!v || v.state.doc.toString() === value) return
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } })
  }, [value])

  return (
    <div
      ref={host}
      className={cn(
        "max-h-48 min-h-14 overflow-auto rounded-md border bg-background focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 dark:bg-input/30",
        className,
      )}
    />
  )
}

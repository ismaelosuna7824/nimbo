// Go to definition for every language plug-in: F12 at the cursor or
// ⌘/Ctrl-click on a name jumps to where it is declared, in the same file or
// another one of the workflow (opened in the Code tab).

import type { EditorState, Extension } from "@codemirror/state"
import { EditorView, keymap } from "@codemirror/view"

export interface DefinitionTarget {
  /** Workflow path of the declaring file; the edited file's own path jumps inside it. */
  file: string
  /** Offset in that file (same-file jumps), when known. */
  from?: number
  /** 1-based line in that file, when known. */
  line?: number
}

/**
 * F12 and ⌘/Ctrl-click: `find` locates the declaration of the name at a position (null: nothing to jump to);
 * `path` is the edited file's path; `open` shows another file at a line.
 */
export function definitionExtension(
  path: string,
  find: (state: EditorState, pos: number) => DefinitionTarget | null,
  open: (file: string, opts: { line: number }) => void,
): Extension {
  const go = (view: EditorView, pos: number): boolean => {
    const target = find(view.state, pos)
    if (!target) return false
    if (target.file === path) {
      const from = target.from ?? (target.line ? view.state.doc.line(Math.min(target.line, view.state.doc.lines)).from : 0)
      view.dispatch({ selection: { anchor: from }, effects: EditorView.scrollIntoView(from, { y: "center" }) })
      view.focus()
    } else open(target.file, { line: target.line ?? 1 })
    return true
  }
  return [
    keymap.of([{ key: "F12", run: (view) => go(view, view.state.selection.main.head) }]),
    EditorView.domEventHandlers({
      mousedown: (e, view) => {
        if (!(e.metaKey || e.ctrlKey) || e.button !== 0) return false
        const pos = view.posAtCoords({ x: e.clientX, y: e.clientY })
        if (pos === null || !go(view, pos)) return false
        e.preventDefault()
        return true
      },
    }),
  ]
}

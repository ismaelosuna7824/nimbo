import type { StreamParser } from "@codemirror/language"

const DIRECTIVE =
  /^(?:-?include|sinclude|ifeq|ifneq|ifdef|ifndef|else|endif|define|endef|export|unexport|override|private|vpath)(?=\s|$)/

/** Makefiles: comments, directives, assignments, targets, recipes and $(VAR) references. */
export const makefile: StreamParser<{ recipe: boolean }> = {
  name: "makefile",
  startState: () => ({ recipe: false }),
  token(stream, state) {
    if (stream.sol()) {
      // A tab-indented line is a recipe (shell); anything else is make syntax.
      state.recipe = stream.peek() === "\t"
      if (!state.recipe) {
        if (stream.match(DIRECTIVE)) return "keyword"
        if (stream.match(/^\.[A-Z_]+(?=\s*:)/)) return "keyword" // .PHONY, .DEFAULT_GOAL…
        if (stream.match(/^[\w.\-/$(){}%]+(?=\s*(?::=|::=|\?=|\+=|!=|=))/)) return "def"
        if (stream.match(/^[^\s:=#][^:=#]*(?=:(?!=))/)) return "propertyName"
      }
    }
    if (stream.eatSpace()) return null
    if (stream.match(/^\$(?:\([^)]*\)|\{[^}]*\}|[@<^+?*%|$]|\w)/)) return "variableName.special"
    const ch = stream.next()
    if (ch === "#") {
      stream.skipToEnd()
      return "comment"
    }
    if (ch === '"' || ch === "'") {
      if (stream.skipTo(ch)) stream.next()
      else stream.skipToEnd()
      return "string"
    }
    if (!state.recipe && ch && ":=?+!".includes(ch)) {
      stream.eatWhile(/[:=]/)
      return "operator"
    }
    stream.eatWhile(/[^\s$#"']/)
    return null
  },
}

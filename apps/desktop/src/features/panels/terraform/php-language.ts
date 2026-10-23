import type { StringStream } from "@codemirror/language"
import { clike } from "@codemirror/legacy-modes/mode/clike"

// legacy-modes has no PHP mode; clike's factory covers PHP's C-like syntax.

const words = (s: string) => Object.fromEntries(s.split(" ").map((w) => [w, true]))

export const php = clike({
  name: "php",
  keywords: words(
    "abstract and array as break callable case catch class clone const continue declare default do echo else " +
      "elseif empty enddeclare endfor endforeach endif endswitch endwhile enum extends final finally fn for foreach " +
      "function global goto if implements include include_once instanceof insteadof interface isset list match " +
      "namespace new or print private protected public readonly require require_once return static switch throw " +
      "trait try unset use var while xor yield",
  ),
  blockKeywords: words("catch class do else elseif for foreach function if interface switch trait try while"),
  atoms: words("true false null TRUE FALSE NULL __CLASS__ __DIR__ __FILE__ __FUNCTION__ __LINE__ __METHOD__ __NAMESPACE__ __TRAIT__"),
  builtin: words(
    "array_key_exists array_keys array_map array_filter array_merge array_values count date die exit explode " +
      "file_get_contents file_put_contents getenv header htmlspecialchars implode in_array is_array is_null " +
      "json_decode json_encode preg_match preg_replace printf sprintf str_replace strlen strtolower strtoupper " +
      "substr trim var_dump",
  ),
  multiLineStrings: true,
  hooks: {
    $: (stream: StringStream) => {
      stream.eatWhile(/[\w$]/)
      return "variableName.special"
    },
    "#": (stream: StringStream) => {
      if (stream.eat("[")) return false // #[Attribute]
      stream.skipToEnd()
      return "comment"
    },
    "<": (stream: StringStream) => (stream.match(/^\?(php|=)?/i) ? "meta" : false),
    "?": (stream: StringStream) => (stream.eat(">") ? "meta" : false),
  },
})

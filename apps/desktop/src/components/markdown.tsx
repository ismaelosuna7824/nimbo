import { Fragment, type ReactNode } from "react"
import { openExternal } from "@/components/open-external"
import { cn } from "@/lib/utils"

/**
 * Renders the small markdown subset Learn content uses (paragraphs, # headings,
 * - / 1. lists, ``` code blocks, `code`, **bold**, *italic*, [links](https://…))
 * as React elements — never as HTML, so content can't inject markup. Links open
 * in the system browser.
 */
export function Markdown({ text, className }: { text: string; className?: string }) {
  return <div className={cn("flex flex-col gap-2 text-sm leading-relaxed", className)}>{blocks(text)}</div>
}

export type MarkdownBlock =
  | { kind: "p"; text: string }
  | { kind: "h"; level: number; text: string }
  | { kind: "ul" | "ol"; items: string[] }
  | { kind: "code"; lang: string; text: string }

/** Splits markdown into blocks (exported for tests). */
export function parseMarkdown(text: string): MarkdownBlock[] {
  const out: MarkdownBlock[] = []
  const lines = text.replace(/\r\n/g, "\n").split("\n")
  let para: string[] = []
  const flush = () => {
    if (para.length) out.push({ kind: "p", text: para.join(" ") })
    para = []
  }
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const fence = /^\s*```(\S*)/.exec(line)
    if (fence) {
      flush()
      const body: string[] = []
      for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) body.push(lines[i])
      out.push({ kind: "code", lang: fence[1], text: body.join("\n") })
      continue
    }
    const heading = /^(#{1,4})\s+(.*)$/.exec(line)
    if (heading) {
      flush()
      out.push({ kind: "h", level: heading[1].length, text: heading[2] })
      continue
    }
    const item = /^\s*(?:([-*])|(\d+)[.)])\s+(.*)$/.exec(line)
    if (item) {
      flush()
      const kind = item[1] ? "ul" : "ol"
      const last = out[out.length - 1]
      if (last && last.kind === kind) last.items.push(item[3])
      else out.push({ kind, items: [item[3]] })
      continue
    }
    if (line.trim() === "") {
      flush()
      continue
    }
    // A continuation line of a list item (indented) joins the item.
    const last = out[out.length - 1]
    if (para.length === 0 && last && (last.kind === "ul" || last.kind === "ol") && /^\s+\S/.test(line)) {
      last.items[last.items.length - 1] += " " + line.trim()
      continue
    }
    para.push(line.trim())
  }
  flush()
  return out
}

function blocks(text: string): ReactNode[] {
  return parseMarkdown(text).map((b, i) => {
    switch (b.kind) {
      case "p":
        return <p key={i}>{inline(b.text)}</p>
      case "h":
        return (
          <p key={i} className={cn("font-semibold", b.level <= 2 ? "text-base" : "text-sm")}>
            {inline(b.text)}
          </p>
        )
      case "ul":
        return (
          <ul key={i} className="ml-5 list-disc space-y-1">
            {b.items.map((it, j) => (
              <li key={j}>{inline(it)}</li>
            ))}
          </ul>
        )
      case "ol":
        return (
          <ol key={i} className="ml-5 list-decimal space-y-1">
            {b.items.map((it, j) => (
              <li key={j}>{inline(it)}</li>
            ))}
          </ol>
        )
      case "code":
        return (
          <pre key={i} className="overflow-x-auto rounded-md border bg-muted/50 p-3 font-mono text-xs leading-relaxed">
            <code>{b.text}</code>
          </pre>
        )
    }
  })
}

// _italic_ only at word boundaries, so snake_case names (bp_missing_tags) stay literal.
const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*\s][^*]*\*|(?<![A-Za-z0-9])_[^_\s][^_]*_(?![A-Za-z0-9]))|(\[[^\]]+\]\([^)\s]+\))/g

/** Inline markdown: `code`, **bold**, *italic*, [text](url). */
export function inline(text: string): ReactNode[] {
  const out: ReactNode[] = []
  let last = 0
  for (const m of text.matchAll(INLINE)) {
    const at = m.index ?? 0
    if (at > last) out.push(text.slice(last, at))
    const token = m[0]
    const key = `${at}`
    if (m[1]) {
      out.push(
        <code key={key} className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
          {token.slice(1, -1)}
        </code>,
      )
    } else if (m[2]) {
      out.push(<strong key={key}>{inline(token.slice(2, -2))}</strong>)
    } else if (m[3]) {
      out.push(<em key={key}>{inline(token.slice(1, -1))}</em>)
    } else {
      const link = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(token)
      const href = link?.[2] ?? ""
      out.push(
        /^https?:\/\//.test(href) ? (
          <a
            key={key}
            href={href}
            className="text-primary underline-offset-4 hover:underline"
            onClick={(e) => {
              e.preventDefault()
              void openExternal(href)
            }}
          >
            {inline(link?.[1] ?? "")}
          </a>
        ) : (
          <Fragment key={key}>{link?.[1]}</Fragment>
        ),
      )
    }
    last = at + token.length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

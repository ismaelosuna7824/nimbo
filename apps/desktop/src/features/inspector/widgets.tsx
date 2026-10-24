import { XIcon } from "lucide-react"
import { useState } from "react"
import { useSyncedState } from "@/components/use-synced-state"
import { Textarea } from "@/components/ui/textarea"
import { cn } from "@/lib/utils"

export function TagInput({
  value,
  onChange,
  placeholder,
  invalid,
}: {
  value: string[]
  onChange: (v: string[]) => void
  placeholder?: string
  invalid?: boolean
}) {
  const [draft, setDraft] = useState("")
  const commit = () => {
    const parts = draft
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean)
    if (parts.length) onChange([...value, ...parts.filter((p) => !value.includes(p))])
    setDraft("")
  }
  return (
    <div
      aria-invalid={invalid || undefined}
      className="flex min-h-8 flex-wrap items-center gap-1 rounded-lg border border-input px-1.5 py-1 focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 aria-invalid:border-destructive dark:bg-input/30"
    >
      {value.map((tag) => (
        <span key={tag} className="inline-flex h-6 items-center gap-1 rounded-md bg-secondary px-1.5 font-mono text-xs">
          {tag}
          <button
            type="button"
            className="text-muted-foreground hover:text-foreground"
            onClick={() => onChange(value.filter((t) => t !== tag))}
            aria-label={`Remove ${tag}`}
          >
            <XIcon className="size-3" />
          </button>
        </span>
      ))}
      <input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault()
            commit()
          } else if (e.key === "Backspace" && !draft && value.length) {
            onChange(value.slice(0, -1))
          }
        }}
        onBlur={commit}
        placeholder={value.length ? "" : (placeholder ?? "Type and press Enter")}
        className="h-6 min-w-24 flex-1 bg-transparent px-1 text-sm outline-none placeholder:text-muted-foreground"
      />
    </div>
  )
}

/** HCL expression editor; commits on blur or ⌘/Ctrl+Enter so partial input is not saved. */
export function ExpressionInput({
  value,
  onCommit,
  placeholder,
  invalid,
  rows = 2,
  className,
}: {
  value: string
  onCommit: (expr: string) => void
  placeholder?: string
  invalid?: boolean
  rows?: number
  className?: string
}) {
  const [draft, setDraft] = useSyncedState(value)
  const commit = () => {
    if (draft !== value) onCommit(draft)
  }
  return (
    <Textarea
      value={draft}
      rows={rows}
      spellCheck={false}
      aria-invalid={invalid || undefined}
      placeholder={placeholder ?? "HCL expression, e.g. var.name or aws_s3_bucket.b.arn"}
      className={cn("min-h-0 resize-y font-mono text-xs", className)}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
          e.preventDefault()
          commit()
        }
      }}
    />
  )
}

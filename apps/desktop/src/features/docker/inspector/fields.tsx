// Small form pieces of the Docker inspector. Edits stay local until the
// field is left (blur/Enter) or a list row is added/removed, then go to the
// daemon as one PATCH that rewrites the compose file in place.

import { EyeIcon, EyeOffIcon, PlusIcon, XIcon } from "lucide-react"
import { useState } from "react"
import { useSyncedState } from "@/components/use-synced-state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { cn } from "@/lib/utils"

export function Section({ title, icon, action, children }: { title: string; icon?: React.ReactNode; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2.5">
      <div className="flex items-center gap-2">
        {icon}
        <h3 className="text-xs font-semibold">{title}</h3>
        <Separator className="flex-1" />
        {action}
      </div>
      {children}
    </section>
  )
}

/** A text field committed on blur/Enter when it changed; Escape restores it. */
export function CommitInput({
  value,
  onCommit,
  validate,
  className,
  ...rest
}: Omit<React.ComponentProps<typeof Input>, "value" | "onChange" | "onBlur"> & {
  value: string
  onCommit: (v: string) => void
  /** Problem with the text, shown under the field; commits wait until it is fixed. */
  validate?: (v: string) => string | undefined
}) {
  const [text, setText] = useSyncedState(value)
  const problem = validate?.(text)
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-1">
      <Input
        {...rest}
        className={cn("h-8 text-xs", className)}
        value={text}
        aria-invalid={!!problem || undefined}
        onChange={(e) => setText(e.target.value)}
        onBlur={() => {
          if (problem) setText(value)
          else if (text !== value) onCommit(text)
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") e.currentTarget.blur()
          if (e.key === "Escape") setText(value)
        }}
      />
      {problem && <p className="text-[11px] text-destructive">{problem}</p>}
    </div>
  )
}

/** A masked value with a reveal toggle (secret environment values). */
export function SecretInput({ value, placeholder, onCommit, label }: { value: string; placeholder?: string; onCommit: (v: string) => void; label: string }) {
  const [shown, setShown] = useState(false)
  return (
    <div className="flex min-w-0 flex-1 items-center gap-1">
      <CommitInput value={value} onCommit={onCommit} type={shown ? "text" : "password"} placeholder={placeholder} aria-label={label} className="font-mono" />
      <Button type="button" variant="ghost" size="icon-xs" onClick={() => setShown((s) => !s)} aria-label={shown ? "Hide value" : "Show value"}>
        {shown ? <EyeOffIcon /> : <EyeIcon />}
      </Button>
    </div>
  )
}

/**
 * Rows of a list with add/remove; each row renders its own fields. A new row that is not `complete` yet (an
 * environment variable without a name) stays in the form only until one of its fields makes it complete; the
 * daemon gets the list without it, since the compose file cannot hold it.
 */
export function ListEditor<T>({
  items,
  onChange,
  render,
  empty,
  addLabel,
  blank,
  complete = () => true,
}: {
  items: T[]
  onChange: (items: T[]) => void
  render: (item: T, update: (next: T) => void, index: number) => React.ReactNode
  empty: string
  addLabel: string
  blank: () => T
  complete?: (item: T) => boolean
}) {
  const [drafts, setDrafts] = useState<T[]>([])
  const rows = [...items, ...drafts]
  const update = (i: number, next: T) => {
    if (i < items.length) return onChange(items.map((x, j) => (j === i ? next : x)))
    const d = i - items.length
    if (!complete(next)) return setDrafts(drafts.map((x, j) => (j === d ? next : x)))
    setDrafts(drafts.filter((_, j) => j !== d))
    onChange([...items, next])
  }
  const remove = (i: number) => {
    if (i < items.length) onChange(items.filter((_, j) => j !== i))
    else setDrafts(drafts.filter((_, j) => j !== i - items.length))
  }
  const add = () => {
    const row = blank()
    if (complete(row)) onChange([...items, row])
    else setDrafts([...drafts, row])
  }
  return (
    <div className="flex flex-col gap-1.5">
      {rows.length === 0 && <p className="text-[11px] text-muted-foreground">{empty}</p>}
      {rows.map((item, i) => (
        <div key={i} className="flex items-start gap-1.5" data-slot="list-row">
          <div className="flex min-w-0 flex-1 items-start gap-1.5">{render(item, (next) => update(i, next), i)}</div>
          <Button type="button" variant="ghost" size="icon-xs" className="mt-1" aria-label="Remove" onClick={() => remove(i)}>
            <XIcon />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="xs" className="self-start" onClick={add}>
        <PlusIcon />
        {addLabel}
      </Button>
    </div>
  )
}

/** A label above a field. */
export function Field({ label, htmlFor, hint, children }: { label: string; htmlFor?: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor={htmlFor} className="text-[11px] font-medium text-muted-foreground">
        {label}
      </label>
      {children}
      {hint && <p className="text-[11px] text-muted-foreground">{hint}</p>}
    </div>
  )
}

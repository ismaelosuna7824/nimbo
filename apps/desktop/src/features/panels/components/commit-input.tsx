import { useRef, useState, type ComponentProps } from "react"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

type CommitInputProps = Omit<ComponentProps<typeof Input>, "value" | "onChange" | "defaultValue"> & {
  value: string
  /** Called on blur or Enter when the text changed. Return false to reject and revert. */
  onCommit: (value: string) => boolean | void
}

/**
 * Text input that keeps a local draft and commits it on blur or Enter, so
 * each keystroke does not trigger a save. Escape reverts.
 */
export function CommitInput({ value, onCommit, className, onKeyDown, onBlur, ...props }: CommitInputProps) {
  const [draft, setDraft] = useState(value)
  const [synced, setSynced] = useState(value)
  const reverting = useRef(false)

  if (value !== synced) {
    setSynced(value)
    setDraft(value)
  }

  return (
    <Input
      {...props}
      value={draft}
      className={cn("h-7 text-xs", className)}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={(e) => {
        if (reverting.current) reverting.current = false
        else if (draft !== value && onCommit(draft) === false) setDraft(value)
        onBlur?.(e)
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.currentTarget.blur()
        } else if (e.key === "Escape") {
          reverting.current = true
          setDraft(value)
          e.currentTarget.blur()
        }
        onKeyDown?.(e)
      }}
    />
  )
}

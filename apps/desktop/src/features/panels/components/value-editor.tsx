import { useState } from "react"
import { Braces, Type } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Value } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { fitsKind, hasLiteral, valueText, type ValueKind } from "../lib/values"
import { CommitInput } from "./commit-input"

interface ValueEditorProps {
  kind: ValueKind
  value: Value | undefined
  /** `undefined` clears the value. */
  onChange: (value: Value | undefined) => void
  /** Show the literal/expression switch (Pro mode). */
  allowExpr?: boolean
  /** An empty input clears the value instead of setting an empty string. */
  emptyClears?: boolean
  invalid?: boolean
  placeholder?: string
  className?: string
}

const UNSET = "__unset__"

/**
 * Edits a Value with a control matching its type. Literal edits send
 * `{literal}` and expression edits `{expr}`, never both (the backend fills
 * the other side; `expr` wins when both are present).
 */
export function ValueEditor({
  kind,
  value,
  onChange,
  allowExpr = false,
  emptyClears = false,
  invalid,
  placeholder,
  className,
}: ValueEditorProps) {
  const [forceExpr, setForceExpr] = useState(false)
  const typed = kind !== "expr" && !forceExpr && fitsKind(kind, value)

  let control
  if (!typed) {
    control = (
      <CommitInput
        value={valueText(value)}
        placeholder={placeholder ?? "HCL expression, e.g. [\"a\", \"b\"]"}
        aria-invalid={invalid || undefined}
        className="font-mono"
        spellCheck={false}
        onCommit={(text) => onChange(text.trim() === "" ? undefined : { expr: text.trim() })}
      />
    )
  } else if (kind === "bool") {
    const current = hasLiteral(value) ? String(value.literal) : UNSET
    control = (
      <Select
        value={current}
        onValueChange={(v) => onChange(v === UNSET ? undefined : { literal: v === "true" })}
      >
        <SelectTrigger size="sm" className="h-7 w-full text-xs" aria-invalid={invalid || undefined}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="true">true</SelectItem>
          <SelectItem value="false">false</SelectItem>
          {(emptyClears || current === UNSET) && (
            <SelectItem value={UNSET} className="text-muted-foreground">
              not set
            </SelectItem>
          )}
        </SelectContent>
      </Select>
    )
  } else if (kind === "number") {
    control = (
      <CommitInput
        type="number"
        value={hasLiteral(value) ? String(value.literal) : ""}
        placeholder={placeholder}
        aria-invalid={invalid || undefined}
        onCommit={(text) => {
          if (text.trim() === "") return void onChange(undefined)
          const n = Number(text)
          if (!Number.isFinite(n)) {
            toast.error(`"${text}" is not a number`)
            return false
          }
          onChange({ literal: n })
        }}
      />
    )
  } else {
    control = (
      <CommitInput
        value={hasLiteral(value) ? String(value.literal) : ""}
        placeholder={placeholder}
        aria-invalid={invalid || undefined}
        onCommit={(text) => onChange(text === "" && emptyClears ? undefined : { literal: text })}
      />
    )
  }

  return (
    <div className={cn("flex min-w-0 items-center gap-1", className)}>
      <div className="min-w-0 flex-1">{control}</div>
      {allowExpr && kind !== "expr" && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label={typed ? "Edit as expression" : "Edit as plain value"}
              disabled={!typed && !fitsKind(kind, value)}
              onClick={() => setForceExpr(typed)}
            >
              {typed ? <Braces /> : <Type />}
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            {typed ? "Use an HCL expression (e.g. a reference)" : "Back to a plain value"}
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}

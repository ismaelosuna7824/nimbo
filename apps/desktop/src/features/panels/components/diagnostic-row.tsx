import type { Diagnostic } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { SEVERITY_META } from "../lib/severity"


interface DiagnosticRowProps {
  diagnostic: Diagnostic
  /** Human name of the node the diagnostic belongs to, when any. */
  nodeLabel?: string
  onSelect?: () => void
}

export function DiagnosticRow({ diagnostic: d, nodeLabel, onSelect }: DiagnosticRowProps) {
  const { icon: Icon, className } = SEVERITY_META[d.severity]
  const content = (
    <>
      <Icon className={cn("mt-0.5 size-3.5 shrink-0", className)} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2">
          <span className="font-medium">{d.message}</span>
          {nodeLabel && <span className="font-mono text-[11px] text-muted-foreground">{nodeLabel}</span>}
          {d.attribute && (
            <span className="rounded bg-muted px-1 font-mono text-[11px] text-muted-foreground">{d.attribute}</span>
          )}
          <span className="ml-auto font-mono text-[10px] text-muted-foreground/70">{d.code}</span>
        </div>
        {d.detail && <p className="mt-0.5 whitespace-pre-wrap text-muted-foreground">{d.detail}</p>}
        {d.fix && (
          <p className="mt-0.5 whitespace-pre-wrap text-foreground/80">
            <span className="font-medium">Fix: </span>
            {d.fix}
          </p>
        )}
      </div>
    </>
  )
  if (!onSelect) return <div className="flex gap-2 px-3 py-1.5 text-xs">{content}</div>
  return (
    <button
      type="button"
      onClick={onSelect}
      className="flex w-full gap-2 px-3 py-1.5 text-left text-xs outline-none hover:bg-muted/60 focus-visible:bg-muted"
    >
      {content}
    </button>
  )
}

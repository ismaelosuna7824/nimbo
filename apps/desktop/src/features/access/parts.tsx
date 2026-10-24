import { CodeIcon, LockIcon, ShieldCheckIcon, TriangleAlertIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import type { AccessRelated, AccessWarning } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { revealFiles } from "../panels/terraform/reveal-files"

/** Least-privilege warnings; `path` keeps those of one part (a statement, a rule), `undefined` shows all. */
export function AccessWarnings({ warnings, path, className }: { warnings: AccessWarning[]; path?: string; className?: string }) {
  const shown = path === undefined ? warnings : warnings.filter((w) => w.path === path || w.path?.startsWith(`${path}/`))
  if (shown.length === 0) return null
  return (
    <ul className={cn("flex flex-col gap-1.5", className)} data-testid="access-warnings">
      {shown.map((w, i) => (
        <li
          key={`${w.code}-${w.path ?? ""}-${i}`}
          className={cn(
            "flex gap-2 rounded-md border px-2.5 py-2 text-xs",
            w.severity === "warning"
              ? "border-amber-500/40 bg-amber-500/10 text-amber-900 dark:text-amber-200"
              : "border-sky-500/30 bg-sky-500/5 text-sky-900 dark:text-sky-200",
          )}
        >
          <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
          <div className="flex min-w-0 flex-col gap-0.5">
            <span className="font-medium">{w.message}</span>
            {w.fix && <span className="text-[11px] opacity-80">{w.fix}</span>}
          </div>
        </li>
      ))}
    </ul>
  )
}

/** Shown when nothing grants too much. */
export function LeastPrivilegeOk({ label = "Least privilege: no wildcards or broad grants." }: { label?: string }) {
  return (
    <div className="flex items-center gap-1.5 text-[11px] text-emerald-600 dark:text-emerald-400">
      <ShieldCheckIcon className="size-3.5" />
      {label}
    </div>
  )
}

/**
 * Something an editor can't show visually: kept exactly as written, with its source and a way to edit it in the code.
 * Nimbo never rewrites it.
 */
export function ReadOnlySource({
  source,
  reason,
  file,
  line,
  title = "Shown as code",
}: {
  source?: string
  reason?: string
  file?: string
  line?: number
  title?: string
}) {
  return (
    <div className="flex flex-col gap-2 rounded-md border border-dashed p-2.5" data-testid="access-read-only">
      <div className="flex items-center gap-1.5 text-xs font-medium">
        <LockIcon className="size-3.5 text-muted-foreground" />
        {title}
      </div>
      {reason && <p className="text-[11px] text-muted-foreground">{reason}</p>}
      {source && (
        <pre className="max-h-48 overflow-auto rounded bg-muted/60 p-2 font-mono text-[11px] leading-snug whitespace-pre-wrap">
          {source}
        </pre>
      )}
      <Button variant="outline" size="sm" className="self-start" onClick={() => revealFiles("", file, line)}>
        <CodeIcon />
        Edit in code
      </Button>
    </div>
  )
}

/** Links to related nodes (a role's inline policies, the document a policy reads, a role's bindings). */
export function RelatedLinks({ related, onSelect }: { related?: AccessRelated[]; onSelect: (nodeId: string) => void }) {
  if (!related?.length) return null
  return (
    <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
      <span>Also:</span>
      {related.map((r) => (
        <button
          key={r.nodeId}
          type="button"
          onClick={() => onSelect(r.nodeId)}
          className="rounded border px-1.5 py-0.5 font-mono text-foreground hover:bg-accent"
          title={r.label}
        >
          {r.address}
        </button>
      ))}
    </div>
  )
}

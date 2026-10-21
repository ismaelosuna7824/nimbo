import { useMemo } from "react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { cn } from "@/lib/utils"
import { diffLines, toHunks } from "./line-diff"

export type ChangeStatus = "added" | "modified" | "renamed" | "deleted"

export interface PendingChange {
  path: string
  status: ChangeStatus
  /** Renamed: the path on the server. */
  from?: string
  /** Contents; absent when unknown or unchanged (a moved file never opened, a deleted file). */
  before?: string
  after?: string
}

interface ChangesDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  changes: PendingChange[]
  /** Primary action (e.g. "Apply changes"); omitted: only Close. */
  action?: { label: string; run: () => void; disabled?: boolean }
}

/** Read-only list of pending file changes: new, renamed and deleted files, with a unified diff of edits. */
export function ChangesDialog({ open, onOpenChange, title, description, changes, action }: ChangesDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-3 sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 space-y-3 overflow-auto">
          {changes.map((c) => (
            <FileDiff key={c.path} change={c} />
          ))}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
          {action && (
            <Button onClick={action.run} disabled={action.disabled}>
              {action.label}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

const STATUS_MARK: Record<ChangeStatus, { letter: string; label: string; className: string }> = {
  added: { letter: "A", label: "new file", className: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300" },
  modified: { letter: "M", label: "modified", className: "bg-amber-500/15 text-amber-700 dark:text-amber-300" },
  renamed: { letter: "R", label: "renamed", className: "bg-sky-500/15 text-sky-700 dark:text-sky-300" },
  deleted: { letter: "D", label: "deleted", className: "bg-red-500/15 text-red-700 dark:text-red-300" },
}

function FileDiff({ change }: { change: PendingChange }) {
  const known = change.before !== undefined && change.after !== undefined
  const { hunks, added, removed } = useMemo(() => {
    const lines = known ? diffLines(change.before!, change.after!) : []
    return {
      hunks: toHunks(lines),
      added: lines.filter((l) => l.type === "add").length,
      removed: lines.filter((l) => l.type === "del").length,
    }
  }, [change, known])
  const mark = STATUS_MARK[change.status]
  return (
    <section className="overflow-hidden rounded-lg border" aria-label={`${change.path}, ${mark.label}`}>
      <header className="flex items-center gap-2 border-b bg-muted/40 px-3 py-1.5 font-mono text-xs">
        <span
          className={cn("rounded-sm px-1 text-[10px] font-semibold", mark.className)}
          title={mark.label}
          aria-hidden
        >
          {mark.letter}
        </span>
        <span className="min-w-0 flex-1 truncate">
          {change.from && <span className="text-muted-foreground">{change.from} → </span>}
          {change.path}
        </span>
        {known && (
          <>
            <span className="text-emerald-600 tabular-nums dark:text-emerald-400">+{added}</span>
            <span className="text-red-600 tabular-nums dark:text-red-400">−{removed}</span>
          </>
        )}
      </header>
      {change.status === "deleted" ? (
        <p className="px-3 py-2 text-xs text-muted-foreground">Deleted: Terraform blocks in it leave the diagram.</p>
      ) : hunks.length === 0 ? (
        <p className="px-3 py-2 text-xs text-muted-foreground">
          {change.status === "renamed"
            ? "Renamed; content unchanged (its blocks keep their place in the diagram)."
            : change.status === "added"
              ? "Empty file."
              : "No changes."}
        </p>
      ) : (
        <pre className="overflow-x-auto py-1 font-mono text-[11.5px] leading-5">
          {hunks.map((h, i) => (
            <div key={i}>
              <div className="bg-primary/8 px-3 text-muted-foreground">
                @@ -{h.oldStart},{h.oldLines} +{h.newStart},{h.newLines} @@
              </div>
              {h.lines.map((l, j) => (
                <div
                  key={j}
                  className={cn(
                    "px-3 whitespace-pre",
                    l.type === "add" && "bg-emerald-500/12 text-emerald-800 dark:text-emerald-300",
                    l.type === "del" && "bg-red-500/12 text-red-800 dark:text-red-300",
                  )}
                >
                  <span className="mr-2 inline-block w-2 select-none text-muted-foreground">
                    {l.type === "add" ? "+" : l.type === "del" ? "−" : " "}
                  </span>
                  {l.text || " "}
                </div>
              ))}
            </div>
          ))}
        </pre>
      )}
    </section>
  )
}

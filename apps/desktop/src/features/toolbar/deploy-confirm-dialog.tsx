import { useEffect } from "react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import type { DestructiveChange } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { checkWatchedApply, runAction } from "./actions"
import { causeLabel, confirmLabel, groupDestructive } from "./deploy-confirm"
import { useUiStore } from "./ui-store"

const EMPTY: DestructiveChange[] = []

/**
 * Confirms a Terraform deploy whose plan would delete or replace resources that exist in the sandbox (a
 * needs_confirmation apply run). Opens on its own when an apply this window started ends that way, and from the
 * Deploy tab's "Review…" (e.g. for applies an agent started). Confirming applies exactly that run's saved plan.
 */
export function DeployConfirmDialog() {
  const runId = useUiStore((s) => s.confirmDeployRun)
  const setRunId = useUiStore((s) => s.setConfirmDeployRun)
  const name = useWorkflowStore((s) => s.workflow?.name)
  const nodes = useWorkflowStore((s) => s.workflow?.nodes)
  const changes = useWorkflowStore((s) => s.runs.find((r) => r.id === runId)?.destructive ?? EMPTY)
  // Watches the apply this window started (see watchApply) as its status arrives.
  useEffect(
    () =>
      useWorkflowStore.subscribe((state, prev) => {
        if (state.runs !== prev.runs) checkWatchedApply()
      }),
    [],
  )

  const groups = groupDestructive(changes)
  const n = changes.length
  const nodeName = (c: DestructiveChange) => {
    const node = c.nodeId ? nodes?.find((x) => x.id === c.nodeId) : undefined
    return node ? (node.label ?? node.name) : undefined
  }
  const item = (c: DestructiveChange) => {
    const label = nodeName(c)
    return (
      <li key={c.address} className="flex min-w-0 flex-col py-1">
        <span className="flex min-w-0 items-baseline gap-1.5">
          {label && <span className="shrink-0 font-medium">{label}</span>}
          <span
            className={label ? "min-w-0 truncate font-mono text-[11px] text-muted-foreground" : "min-w-0 truncate font-mono text-[11px]"}
            title={c.address}
          >
            {c.address}
          </span>
        </span>
        <span className="text-muted-foreground">{causeLabel(c)}</span>
      </li>
    )
  }

  return (
    <AlertDialog open={runId !== null && n > 0} onOpenChange={(o) => !o && setRunId(null)}>
      <AlertDialogContent className="data-[size=default]:sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>
            Deploy would destroy {n} resource{n === 1 ? "" : "s"} in “{name}”
          </AlertDialogTitle>
          <AlertDialogDescription>
            To apply your design, Terraform must destroy {n === 1 ? "this existing sandbox resource" : "these existing sandbox resources"}.
            {n === 1 ? "Its" : "Their"} data is lost. Nothing has been applied yet.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex max-h-72 min-w-0 flex-col gap-3 overflow-y-auto text-xs" data-testid="deploy-confirm-list">
          {groups.replace.length > 0 && (
            <section>
              <h4 className="font-semibold">Replace (destroy and re-create)</h4>
              <ul className="divide-y">{groups.replace.map(item)}</ul>
            </section>
          )}
          {groups.delete.length > 0 && (
            <section>
              <h4 className="font-semibold">Delete</h4>
              <ul className="divide-y">{groups.delete.map(item)}</ul>
            </section>
          )}
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            data-testid="deploy-confirm"
            onClick={() => runId && void runAction("apply", { confirmRunId: runId })}
          >
            {confirmLabel(groups)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

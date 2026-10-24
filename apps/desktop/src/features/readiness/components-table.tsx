import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { EnvironmentReadiness, ReadinessCell, ReadinessComponent } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { runReadinessAction } from "./actions"
import { CELL_COLUMNS, ITEM_STATUS, scoreLabel, scoreTone } from "./status"

/**
 * One row per compose service or workload: its findings, health, resources and monitoring, then the evidence of
 * real runs (load headroom, incident verdicts, capacity plan). A cell opens the problem, run or tool behind it.
 */
export function ComponentsTable({ env }: { env: EnvironmentReadiness }) {
  if (env.components.length === 0) {
    return <p className="px-3 py-2 text-xs text-muted-foreground">No services or workloads in this environment.</p>
  }
  return (
    <Table className="text-xs" data-testid="readiness-components">
      <TableHeader className="sticky top-0 z-10 bg-background">
        <TableRow>
          <TableHead className="sticky left-0 z-10 h-7 bg-background px-3 text-[11px]">Component</TableHead>
          <TableHead className="h-7 px-2 text-right text-[11px]">Score</TableHead>
          {CELL_COLUMNS.map((c) => (
            <TableHead key={c.id} className="h-7 px-2 text-[11px]" title={c.hint}>
              {c.label}
            </TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {env.components.map((c) => (
          <ComponentRow key={c.nodeId} component={c} env={env.name} />
        ))}
      </TableBody>
    </Table>
  )
}

function ComponentRow({ component, env }: { component: ReadinessComponent; env: string }) {
  const select = useWorkflowStore((s) => s.select)
  return (
    <TableRow data-testid={`readiness-component-${component.name}`} data-status={component.status}>
      <TableCell className="sticky left-0 z-[1] bg-background px-3 py-1">
        <button
          type="button"
          className="flex max-w-48 items-center gap-1.5 text-left outline-none hover:underline focus-visible:underline"
          title={`Select ${component.nodeId} on the canvas`}
          onClick={() => select(component.nodeId)}
        >
          <span className="truncate font-medium">{component.name}</span>
          <span
            className={cn(
              "rounded px-1 text-[10px]",
              component.role === "data" ? "bg-sky-500/10 text-sky-700 dark:text-sky-400" : "bg-muted text-muted-foreground",
            )}
          >
            {component.role}
          </span>
        </button>
      </TableCell>
      <TableCell className={cn("px-2 py-1 text-right font-mono tabular-nums", scoreTone(component.score))}>{scoreLabel(component.score)}</TableCell>
      {CELL_COLUMNS.map((col) => {
        const cell = component.cells.find((x) => x.id === col.id)
        return (
          <TableCell key={col.id} className="px-1 py-0.5">
            {cell && <Cell cell={cell} label={col.label} component={component.name} env={env} />}
          </TableCell>
        )
      })}
    </TableRow>
  )
}

function Cell({ cell, label, component, env }: { cell: ReadinessCell; label: string; component: string; env: string }) {
  const meta = ITEM_STATUS[cell.status]
  const hint = cell.action ? `${cell.message} — ${cell.action.label}` : cell.message
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-disabled={!cell.action}
          data-testid={`readiness-cell-${component}-${cell.id}`}
          data-status={cell.status}
          aria-label={`${label}: ${meta.label}. ${hint}`}
          onClick={() => void runReadinessAction(cell.action, cell.diagnostics, env)}
          className="flex w-full max-w-40 min-w-6 items-center gap-1 rounded px-1 py-1 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/50 aria-disabled:cursor-default aria-[disabled=false]:hover:bg-muted"
        >
          <meta.icon className={cn("size-3.5 shrink-0", meta.className)} aria-hidden />
          <span className={cn("hidden truncate text-[11px] @5xl:inline", cell.status === "skip" ? "text-muted-foreground/70" : "text-muted-foreground")}>
            {cell.message}
          </span>
        </button>
      </TooltipTrigger>
      <TooltipContent side="top">{hint}</TooltipContent>
    </Tooltip>
  )
}

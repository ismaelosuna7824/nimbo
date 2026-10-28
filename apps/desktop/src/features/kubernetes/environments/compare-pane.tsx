import { Fragment, useMemo, useState } from "react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { RefreshCw } from "lucide-react"
import { errorMessage } from "@/components/errors"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { api } from "@/lib/api/client"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { cellDiffers, visibleGroups } from "./model"

/** Every environment rendered on its own, side by side per workload: what each one changes from the base. */
export function ComparePane({ active }: { active: string }) {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const version = useWorkflowStore((s) => s.version)
  const compare = useQuery({
    queryKey: ["environments-compare", workflowId, version],
    queryFn: () => api.compareEnvironments(workflowId),
    placeholderData: keepPreviousData,
  })
  const [onlyDiffs, setOnlyDiffs] = useState(true)
  const groups = useMemo(() => visibleGroups(compare.data?.groups ?? [], onlyDiffs), [compare.data, onlyDiffs])

  if (compare.isPending) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  }
  if (compare.isError && !compare.data) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyTitle>Couldn't compare environments</EmptyTitle>
          <EmptyDescription>{errorMessage(compare.error)}</EmptyDescription>
        </EmptyHeader>
        <Button size="sm" variant="outline" onClick={() => void compare.refetch()}>
          <RefreshCw /> Try again
        </Button>
      </Empty>
    )
  }

  const { columns, errors } = compare.data
  const failed = Object.entries(errors ?? {})
  const total = compare.data.groups.length

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b px-3 py-1.5">
        <div className="flex items-center gap-1.5">
          <Switch id="env-only-diff" size="sm" checked={onlyDiffs} onCheckedChange={setOnlyDiffs} data-testid="env-only-diff" />
          <Label htmlFor="env-only-diff" className="text-xs font-normal">
            Only differences
          </Label>
        </div>
        {compare.isFetching && <Spinner className="size-3" />}
        <span className="ml-auto text-[11px] text-muted-foreground">Each environment rendered on its own; highlighted cells differ from the base.</span>
      </div>
      {failed.length > 0 && (
        <ul className="shrink-0 border-b px-3 py-1.5 text-[11px] text-destructive">
          {failed.map(([env, msg]) => (
            <li key={env} className="truncate" title={msg}>
              <span className="font-medium">{env || "Base"}</span> didn't render: {msg}
            </li>
          ))}
        </ul>
      )}
      {groups.length === 0 ? (
        <Empty className="flex-1">
          <EmptyHeader>
            <EmptyTitle className="text-sm">{total === 0 ? "Nothing to compare" : "No differences"}</EmptyTitle>
            <EmptyDescription className="text-xs">
              {total === 0
                ? "No workloads were rendered."
                : "Every environment renders the same settings as the base. Override some in Edit."}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <table className="w-max min-w-full border-separate border-spacing-0 text-xs" data-testid="env-compare-table">
            <thead className="sticky top-0 z-20">
              <tr>
                <th className="sticky left-0 z-10 min-w-44 border-b bg-background px-3 py-1.5 text-left font-medium text-muted-foreground">
                  Setting
                </th>
                {columns.map((c) => (
                  <th key={c.name || "base"} className="min-w-32 border-b bg-background px-3 py-1.5 text-left font-medium">
                    <span className="flex items-center gap-1.5">
                      <span className="truncate">{c.label}</span>
                      {c.name === active && (
                        <span className="rounded bg-background px-1 text-[10px] font-normal text-muted-foreground ring-1 ring-border">active</span>
                      )}
                    </span>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {groups.map((g) => (
                <Fragment key={g.id}>
                  <tr>
                    <th
                      colSpan={columns.length + 1}
                      className="border-b bg-muted/40 px-3 py-1 text-left font-medium"
                      scope="colgroup"
                    >
                      <span className="sticky left-3 inline-flex items-center gap-1.5">
                        <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
                          {g.kind}
                        </Badge>
                        {g.label}
                      </span>
                    </th>
                  </tr>
                  {g.rows.map((r) => (
                    <tr key={r.key} className="group/row">
                      <th
                        scope="row"
                        className="sticky left-0 z-10 max-w-64 border-b bg-background px-3 py-1 text-left font-normal group-hover/row:bg-muted"
                      >
                        <span className="flex min-w-0 items-center gap-1.5">
                          <span
                            className={cn("size-1.5 shrink-0 rounded-full", r.differs ? "bg-amber-500" : "bg-transparent")}
                            aria-label={r.differs ? "Differs between environments" : undefined}
                          />
                          <span className="truncate" title={r.label}>
                            {r.label}
                          </span>
                        </span>
                      </th>
                      {columns.map((c, i) => {
                        const value = r.values[i] ?? ""
                        const differs = cellDiffers(r, i)
                        return (
                          <td
                            key={c.name || "base"}
                            className={cn(
                              "max-w-64 border-b px-3 py-1 font-mono text-[11px] group-hover/row:bg-muted/50",
                              differs && "bg-amber-500/10 text-amber-700 group-hover/row:bg-amber-500/15 dark:text-amber-300",
                              !value && "text-muted-foreground",
                            )}
                            data-testid={`env-compare-cell-${g.id}-${r.key}-${c.name || "base"}`}
                            data-differs={differs || undefined}
                          >
                            <span className="block truncate" title={value || undefined}>
                              {value || "—"}
                            </span>
                          </td>
                        )
                      })}
                    </tr>
                  ))}
                </Fragment>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

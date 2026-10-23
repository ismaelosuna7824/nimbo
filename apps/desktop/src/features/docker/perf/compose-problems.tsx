import { CircleCheck, ScrollText } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import type { ComposeProblem } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { SEVERITY_META } from "@/features/panels/lib/severity"
import { PROBLEM_LABEL } from "./model"

/** Container problems of the compose project (OOM kills, restart loops, failed exits, unhealthy) with how to fix them. */
export function ComposeProblems({ problems, onLogs }: { problems: ComposeProblem[]; onLogs: (service: string) => void }) {
  return (
    <div data-testid="compose-problems" data-count={problems.length} className="h-full">
      {problems.length === 0 ? (
        <Empty className="h-full">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <CircleCheck />
            </EmptyMedia>
            <EmptyTitle>No container problems</EmptyTitle>
            <EmptyDescription>
              Out-of-memory kills, restart loops, failed exits and failing health checks show up here with how to fix them.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ul className="divide-y">
          {problems.map((p) => {
            const meta = SEVERITY_META[p.severity]
            const Icon = meta.icon
            return (
              <li
                key={`${p.container}:${p.kind}`}
                data-testid="compose-problem"
                data-service={p.service}
                data-kind={p.kind}
                className="flex gap-2 px-3 py-2 text-xs"
              >
                <Icon className={cn("mt-0.5 size-3.5 shrink-0", meta.className)} />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-2">
                    <span className="font-medium">{p.service}</span>
                    <span className={cn("font-medium", meta.className)}>{PROBLEM_LABEL[p.kind]}</span>
                    {p.exitCode !== undefined && <span className="font-mono text-[10px] text-muted-foreground">exit {p.exitCode}</span>}
                    {p.restarts !== undefined && <span className="text-[10px] text-muted-foreground tabular-nums">{p.restarts} restarts</span>}
                    <span className="truncate font-mono text-[10px] text-muted-foreground">{p.container}</span>
                  </div>
                  <p className="mt-0.5">{p.message}</p>
                  {p.fix && (
                    <p className="mt-0.5 text-muted-foreground">
                      <span className="font-medium text-foreground">Fix: </span>
                      {p.fix}
                    </p>
                  )}
                </div>
                <Button size="xs" variant="ghost" className="shrink-0" onClick={() => onLogs(p.service)}>
                  <ScrollText /> Logs
                </Button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

import { CircleCheck, ScrollText } from "lucide-react"
import { K8sIcon } from "@/components/k8s-icons/k8s-icon"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import type { KubeProblem } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { SEVERITY_META } from "@/features/panels/lib/severity"
import { groupProblems, nodeKind, problemLabel } from "./kube-model"

/** Pod problems of the cluster (OOM kills, crash loops, image pulls, scheduling, probes…) per workload, with fixes. */
export function KubeProblems({ problems, onLogs }: { problems: KubeProblem[]; onLogs: (nodeId: string, pod?: string) => void }) {
  const groups = groupProblems(problems)
  return (
    <div data-testid="kube-problems" data-count={problems.length} className="h-full">
      {groups.length === 0 ? (
        <Empty className="h-full">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <CircleCheck />
            </EmptyMedia>
            <EmptyTitle>No pod problems</EmptyTitle>
            <EmptyDescription>
              Out-of-memory kills, crash loops, image pull errors, unschedulable pods and failing probes show up here with how
              to fix them.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="divide-y">
          {groups.map((g) => (
            <section key={g.nodeId} data-testid={`kube-problem-group-${g.nodeId}`} data-severity={g.severity}>
              <h4 className="flex items-center gap-1.5 bg-muted/30 px-3 py-1 text-xs font-semibold">
                <K8sIcon icon={nodeKind(g.nodeId)} size={14} />
                {g.name}
                <span className="font-normal text-muted-foreground">{nodeKind(g.nodeId)}</span>
              </h4>
              <ul className="divide-y">
                {g.problems.map((p, i) => {
                  const meta = SEVERITY_META[p.severity]
                  const Icon = meta.icon
                  return (
                    <li
                      key={`${p.pod ?? ""}:${p.container ?? ""}:${p.kind}:${i}`}
                      data-testid="kube-problem"
                      data-kind={p.kind}
                      className="flex gap-2 px-3 py-2 text-xs"
                    >
                      <Icon className={cn("mt-0.5 size-3.5 shrink-0", meta.className)} />
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-x-2">
                          <span className={cn("font-medium", meta.className)}>{problemLabel(p.kind)}</span>
                          {p.exitCode !== undefined && <span className="font-mono text-[10px] text-muted-foreground">exit {p.exitCode}</span>}
                          {p.restarts !== undefined && (
                            <span className="text-[10px] text-muted-foreground tabular-nums">{p.restarts} restarts</span>
                          )}
                          {p.pod && (
                            <span className="truncate font-mono text-[10px] text-muted-foreground">
                              {p.pod}
                              {p.container ? ` / ${p.container}` : ""}
                            </span>
                          )}
                        </div>
                        <p className="mt-0.5">{p.message}</p>
                        {p.fix && (
                          <p className="mt-0.5 text-muted-foreground">
                            <span className="font-medium text-foreground">Fix: </span>
                            {p.fix}
                          </p>
                        )}
                      </div>
                      <Button size="xs" variant="ghost" className="shrink-0" onClick={() => onLogs(p.nodeId, p.pod)}>
                        <ScrollText /> Logs
                      </Button>
                    </li>
                  )
                })}
              </ul>
            </section>
          ))}
        </div>
      )}
    </div>
  )
}

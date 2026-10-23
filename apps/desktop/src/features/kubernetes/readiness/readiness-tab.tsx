import { useEffect, useMemo, useState } from "react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { ChevronRight, RefreshCw } from "lucide-react"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Progress } from "@/components/ui/progress"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { DiagnosticRow } from "@/features/panels/components/diagnostic-row"
import { api } from "@/lib/api/client"
import type { EnvironmentReadiness, ReadinessItem } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { openFinding, runReadinessAction } from "./actions"
import { defaultEnvironment, ENV_STATUS, envLabel, ITEM_STATUS } from "./status"

/**
 * Production readiness of a Kubernetes workflow: every environment rendered and checked on its own (replicas,
 * disruption budgets, probes, rollouts, spread, autoscaling and its last load test, monitoring, security, hygiene).
 * Refreshes when the workflow is saved or a load test finishes.
 */
export function ReadinessTab() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const updatedAt = useWorkflowStore((s) => s.workflow!.updatedAt)
  const active = useWorkflowStore((s) => s.workflow!.activeEnvironment ?? "")
  // Finished runs change the scaling verdicts; running ones don't count yet.
  const finishedRuns = useWorkflowStore((s) => s.loadTests.filter((r) => r.status !== "running").map((r) => r.id).join(","))
  const report = useQuery({
    queryKey: ["kubernetes-readiness", workflowId, updatedAt, finishedRuns],
    queryFn: () => api.kubernetesReadiness(workflowId),
    placeholderData: keepPreviousData,
  })
  const requested = useWorkflowStore((s) => s.readinessEnvironment)
  const [picked, setPicked] = useState<string | null>(requested)
  // "Readiness" links elsewhere (Environments tab) open this tab on one environment, once.
  useEffect(() => {
    if (requested === null) return
    setPicked(requested)
    useWorkflowStore.setState({ readinessEnvironment: null })
  }, [requested])
  const envs = report.data?.environments ?? []
  const shown = defaultEnvironment(envs, picked, active)
  const env = envs.find((e) => e.name === shown)

  if (report.isPending) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  }
  if (report.isError && !report.data) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyTitle>Couldn't check readiness</EmptyTitle>
          <EmptyDescription>{errorMessage(report.error)}</EmptyDescription>
        </EmptyHeader>
        <Button size="sm" variant="outline" onClick={() => void report.refetch()}>
          <RefreshCw /> Try again
        </Button>
      </Empty>
    )
  }

  return (
    <div className="@container flex h-full min-h-0 flex-col" data-testid="readiness-tab">
      <div className="flex min-h-0 flex-1 flex-col @3xl:flex-row">
        <nav
          aria-label="Environments"
          className="flex shrink-0 gap-1 overflow-x-auto border-b p-1.5 @3xl:w-60 @3xl:flex-col @3xl:overflow-x-visible @3xl:overflow-y-auto @3xl:border-r @3xl:border-b-0"
        >
          <div className="hidden items-center gap-1.5 px-1.5 pt-0.5 pb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase @3xl:flex">
            Environments
            <Button
              size="icon-xs"
              variant="ghost"
              className="ml-auto"
              aria-label="Check again"
              title="Check again"
              onClick={() => void report.refetch()}
            >
              {report.isFetching ? <Spinner className="size-3" /> : <RefreshCw />}
            </Button>
          </div>
          {envs.map((e) => (
            <EnvButton key={e.name} env={e} active={e.name === active} selected={e.name === shown} onSelect={() => setPicked(e.name)} />
          ))}
          <Button
            size="icon-xs"
            variant="ghost"
            className="ml-auto self-center @3xl:hidden"
            aria-label="Check again"
            title="Check again"
            onClick={() => void report.refetch()}
          >
            {report.isFetching ? <Spinner className="size-3" /> : <RefreshCw />}
          </Button>
        </nav>
        {env ? <Checklist key={env.name} env={env} /> : null}
      </div>
    </div>
  )
}

function EnvButton({ env, active, selected, onSelect }: { env: EnvironmentReadiness; active: boolean; selected: boolean; onSelect: () => void }) {
  const status = ENV_STATUS[env.status]
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={selected ? "true" : undefined}
      data-testid={`readiness-env-${env.name || "base"}`}
      className={cn(
        "flex min-w-40 shrink-0 flex-col gap-1 rounded-md px-2 py-1.5 text-left text-xs outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring/50",
        selected && "bg-muted",
      )}
    >
      <span className="flex items-center gap-1.5">
        <span className="truncate font-medium">{envLabel(env.name)}</span>
        {active && <span className="rounded bg-background px-1 text-[10px] text-muted-foreground ring-1 ring-border">active</span>}
        <span className={cn("ml-auto rounded-full px-1.5 text-[10px] font-medium whitespace-nowrap", status.badge)}>{status.label}</span>
      </span>
      <span className="flex items-center gap-2">
        <Progress value={env.score} className={cn("h-1 flex-1", status.bar)} aria-label={`Score ${env.score} of 100`} />
        <span className="w-7 text-right font-mono text-[10px] text-muted-foreground tabular-nums">{env.score}</span>
      </span>
    </button>
  )
}

function Checklist({ env }: { env: EnvironmentReadiness }) {
  const status = ENV_STATUS[env.status]
  const counts = useMemo(() => {
    const c = { open: 0, passed: 0 }
    for (const g of env.groups) for (const it of g.items) {
      if (it.status === "pass" || it.status === "info") c.passed++
      else if (it.status !== "skip") c.open++
    }
    return c
  }, [env])
  return (
    <ScrollArea className="min-h-0 flex-1" data-testid="readiness-checklist">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b px-3 py-2">
        <span className={cn("rounded-full px-2 py-0.5 text-xs font-medium", status.badge)}>{status.label}</span>
        <span className="text-xs font-medium">{env.summary}</span>
        <span className="text-[11px] text-muted-foreground">
          {env.name === "" ? "No environments: the base is checked as production." : env.productionLike ? "Production-like environment." : "Production-only checks are skipped here."}
        </span>
        <span className="ml-auto text-[11px] text-muted-foreground tabular-nums">
          {counts.passed} passed · {counts.open} open · score {env.score}
        </span>
      </div>
      {env.groups.map((g) => (
        <section key={g.id} aria-label={g.title}>
          <h3 className="sticky top-0 z-10 border-b bg-background/95 px-3 py-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase backdrop-blur">
            {g.title}
          </h3>
          <div className="divide-y">
            {g.items.map((it) => (
              <ItemRow key={it.id} item={it} env={env.name} />
            ))}
          </div>
        </section>
      ))}
    </ScrollArea>
  )
}

function ItemRow({ item, env }: { item: ReadinessItem; env: string }) {
  const meta = ITEM_STATUS[item.status]
  const findings = item.diagnostics ?? []
  const action = item.action && (
    <Button
      size="xs"
      variant={item.status === "warn" || item.status === "fail" || item.status === "todo" ? "outline" : "ghost"}
      className="shrink-0"
      data-testid={`readiness-action-${item.id}`}
      onClick={() => void runReadinessAction(item, env)}
    >
      {item.action.label}
    </Button>
  )
  const body = (
    <>
      <meta.icon className={cn("mt-0.5 size-3.5 shrink-0", meta.className)} aria-label={meta.label} />
      <div className="min-w-0 flex-1">
        <span className={cn("font-medium", item.status === "skip" && "text-muted-foreground")}>{item.title}</span>
        <p className="text-muted-foreground">{item.message}</p>
      </div>
    </>
  )
  if (findings.length === 0) {
    return (
      <div className="flex items-start gap-2 px-3 py-1.5 text-xs" data-testid={`readiness-item-${item.id}`} data-status={item.status}>
        {body}
        {action}
      </div>
    )
  }
  return (
    <Collapsible data-testid={`readiness-item-${item.id}`} data-status={item.status}>
      <div className="flex items-start gap-1 pr-3 text-xs">
        <CollapsibleTrigger className="group flex min-w-0 flex-1 items-start gap-2 py-1.5 pl-3 text-left outline-none hover:bg-muted/40 focus-visible:bg-muted">
          {body}
          <span className="mt-0.5 flex shrink-0 items-center gap-0.5 text-[11px] text-muted-foreground tabular-nums">
            {findings.length}
            <ChevronRight className="size-3.5 transition-transform group-data-[state=open]:rotate-90" />
          </span>
        </CollapsibleTrigger>
        <div className="py-1">{action}</div>
      </div>
      <CollapsibleContent className="ml-5 divide-y border-t border-dashed">
        {findings.map((d, i) => (
          <DiagnosticRow key={`${d.code}-${d.nodeId ?? ""}-${i}`} diagnostic={d} nodeLabel={d.nodeId} onSelect={d.file ? () => openFinding(d) : undefined} />
        ))}
      </CollapsibleContent>
    </Collapsible>
  )
}

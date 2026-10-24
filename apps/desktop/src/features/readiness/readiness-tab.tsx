import { useEffect, useMemo, useState } from "react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { ChevronRight, CircleDashed, RefreshCw } from "lucide-react"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Progress } from "@/components/ui/progress"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { DiagnosticRow } from "@/features/panels/components/diagnostic-row"
import { api } from "@/lib/api/client"
import type { EnvironmentReadiness, ReadinessGroup, ReadinessItem } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { openFinding, runReadinessAction } from "./actions"
import { ComponentsTable } from "./components-table"
import { defaultEnvironment, ENV_STATUS, envLabel, ITEM_STATUS, scoreLabel, scoreTone, untestedSummary } from "./status"

/**
 * Production readiness of a docker or Kubernetes workflow: every environment opened on its own and scored from its
 * rules, monitoring and the evidence of real runs (load tests, incident scenarios, capacity plans), with one row per
 * service or workload. Refreshes when the workflow is saved or a load test finishes.
 */
export function ReadinessTab() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const updatedAt = useWorkflowStore((s) => s.workflow!.updatedAt)
  const active = useWorkflowStore((s) => s.workflow!.activeEnvironment ?? "")
  // Finished runs change the load and resilience evidence; running ones don't count yet.
  const finishedRuns = useWorkflowStore((s) => s.loadTests.filter((r) => r.status !== "running").map((r) => r.id).join(","))
  const report = useQuery({
    queryKey: ["readiness", workflowId, updatedAt, finishedRuns],
    queryFn: () => api.readiness(workflowId),
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

  const refresh = (className?: string) => (
    <Button size="icon-xs" variant="ghost" className={className} aria-label="Check again" title="Check again" onClick={() => void report.refetch()}>
      {report.isFetching ? <Spinner className="size-3" /> : <RefreshCw />}
    </Button>
  )
  return (
    <div className="@container flex h-full min-h-0 flex-col" data-testid="readiness-tab">
      {/* Narrow panels scroll the whole view as one page (the components keep a usable height); wide ones scroll each
          part on its own. */}
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto @3xl:flex-row @3xl:overflow-hidden">
        <nav
          aria-label="Environments"
          className="flex shrink-0 gap-1 overflow-x-auto border-b p-1.5 @3xl:w-56 @3xl:flex-col @3xl:overflow-x-visible @3xl:overflow-y-auto @3xl:border-r @3xl:border-b-0"
        >
          <div className="hidden items-center gap-1.5 px-1.5 pt-0.5 pb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase @3xl:flex">
            Environments
            {refresh("ml-auto")}
          </div>
          {envs.map((e) => (
            <EnvButton key={e.name} env={e} active={e.name === active} selected={e.name === shown} onSelect={() => setPicked(e.name)} />
          ))}
          {refresh("ml-auto self-center @3xl:hidden")}
        </nav>
        {env ? <EnvironmentView key={env.name} env={env} /> : null}
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
      <span className="text-[10px] text-muted-foreground tabular-nums">Confidence {env.confidence}%</span>
    </button>
  )
}

function EnvironmentView({ env }: { env: EnvironmentReadiness }) {
  const status = ENV_STATUS[env.status]
  const [tab, setTab] = useState<"components" | "checklist">(env.components.length ? "components" : "checklist")
  const showGroup = (id: string) => {
    setTab("checklist")
    requestAnimationFrame(() => document.getElementById(`readiness-group-${id}`)?.scrollIntoView({ block: "start" }))
  }
  return (
    <div className="flex min-w-0 flex-1 flex-col @3xl:min-h-0" data-testid="readiness-environment">
      <header className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5">
        <div className="flex items-baseline gap-1" data-testid="readiness-score">
          <span className={cn("text-2xl leading-none font-semibold tabular-nums", scoreTone(env.score))}>{env.score}</span>
          <span className="text-[11px] text-muted-foreground">/100</span>
        </div>
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <div className="flex flex-wrap items-center gap-2">
            <span className={cn("rounded-full px-2 py-0.5 text-xs font-medium", status.badge)} data-testid="readiness-status">
              {status.label}
            </span>
            <span className="min-w-0 text-xs font-medium">{env.summary}</span>
          </div>
          <span className="text-[11px] text-muted-foreground">
            {env.name === "" && env.productionLike
              ? "No environments: the base is checked as production."
              : env.productionLike
                ? "Production-like environment: untested checks keep it at Needs work."
                : "Production-only checks are skipped here; untested checks only lower the confidence."}
          </span>
        </div>
        <Tooltip>
          <TooltipTrigger asChild>
            <div className="flex w-36 flex-col gap-1" data-testid="readiness-confidence">
              <span className="flex justify-between text-[11px] text-muted-foreground">
                Confidence <span className="font-mono tabular-nums">{env.confidence}%</span>
              </span>
              <Progress value={env.confidence} className="h-1 *:data-[slot=progress-indicator]:bg-violet-500" aria-label={`Confidence ${env.confidence}%`} />
            </div>
          </TooltipTrigger>
          <TooltipContent>{untestedSummary(env)}</TooltipContent>
        </Tooltip>
      </header>
      <div className="grid grid-cols-3 gap-px border-b bg-border @3xl:grid-cols-6" role="list" aria-label="Categories">
        {env.groups.map((g) => (
          <CategoryButton key={g.id} group={g} onSelect={() => showGroup(g.id)} />
        ))}
      </div>
      <Tabs value={tab} onValueChange={(v) => setTab(v as typeof tab)} className="flex min-h-72 flex-1 flex-col gap-0 @3xl:min-h-0">
        <TabsList variant="line" className="h-8 shrink-0 px-2">
          <TabsTrigger value="components" className="text-xs" data-testid="readiness-view-components">
            {env.components.length} {env.components.length === 1 ? "component" : "components"}
          </TabsTrigger>
          <TabsTrigger value="checklist" className="text-xs" data-testid="readiness-view-checklist">
            Checklist
          </TabsTrigger>
        </TabsList>
        <TabsContent value="components" className="min-h-0 flex-1 border-t">
          <ComponentsTable env={env} />
        </TabsContent>
        <TabsContent value="checklist" className="min-h-0 flex-1 border-t">
          <Checklist env={env} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function CategoryButton({ group, onSelect }: { group: ReadinessGroup; onSelect: () => void }) {
  return (
    <button
      type="button"
      role="listitem"
      onClick={onSelect}
      data-testid={`readiness-category-${group.id}`}
      className="flex min-w-0 flex-col bg-background px-2 py-1 text-left outline-none hover:bg-muted/50 focus-visible:bg-muted"
      title={`${group.title}: ${group.weight}% of the score${group.untested ? `, ${group.untested} not tested yet` : ""}`}
    >
      <span className="truncate text-[11px] text-muted-foreground">{group.title}</span>
      <span className="flex items-baseline gap-1">
        <span className={cn("text-sm font-semibold tabular-nums", scoreTone(group.score))}>{scoreLabel(group.score)}</span>
        <span className="text-[10px] text-muted-foreground tabular-nums">{group.weight}%</span>
        {group.untested > 0 && (
          <span className="ml-auto flex items-center gap-0.5 text-[10px] whitespace-nowrap text-violet-600 tabular-nums dark:text-violet-400">
            <CircleDashed className="size-2.5" aria-hidden />
            {group.untested}
            <span className="hidden @5xl:inline">untested</span>
          </span>
        )}
      </span>
    </button>
  )
}

function Checklist({ env }: { env: EnvironmentReadiness }) {
  const counts = useMemo(() => {
    const c = { open: 0, passed: 0, untested: 0 }
    for (const g of env.groups)
      for (const it of g.items) {
        if (it.status === "pass" || it.status === "info") c.passed++
        else if (it.status === "todo") c.untested++
        else if (it.status !== "skip") c.open++
      }
    return c
  }, [env])
  return (
    // Block, not Radix's table wrapper: rows keep the pane's width, so long findings wrap instead of being cut off.
    <ScrollArea className="h-full [&_[data-slot=scroll-area-viewport]>div]:block!" data-testid="readiness-checklist">
      <p className="border-b px-3 py-1 text-[11px] text-muted-foreground tabular-nums">
        {counts.passed} passed · {counts.open} open · {counts.untested} not tested yet
      </p>
      {env.groups.map((g) => (
        <section key={g.id} id={`readiness-group-${g.id}`} aria-label={g.title}>
          <h3 className="sticky top-0 z-10 flex items-center gap-2 border-b bg-background/95 px-3 py-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase backdrop-blur">
            {g.title}
            <span className="font-normal tracking-normal normal-case tabular-nums">
              {scoreLabel(g.score)} · weight {g.weight}%
            </span>
          </h3>
          <div className="divide-y">
            {g.items.length === 0 && <p className="px-3 py-1.5 text-xs text-muted-foreground">Nothing to check here.</p>}
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
      onClick={() => void runReadinessAction(item.action, item.diagnostics, env)}
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

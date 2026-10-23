import { useMemo, useState, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { ChevronRight, Hammer } from "lucide-react"
import { techIconFor } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import type { BestPracticeRule, ComposeBuild, ServiceBuildStats } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { formatRelative } from "@/features/panels/lib/format"
import { SEVERITY_META } from "@/features/panels/lib/severity"
import { formatDelta, formatDuration, formatSize, latestBuilds, ownSize, stepSummary } from "./model"

/** Build metrics of the newest build of every service: time, cache use, image size, biggest layers, findings, steps. */
export function ComposeBuildView() {
  const builds = useWorkflowStore((s) => s.composeBuilds)
  const latest = useMemo(() => latestBuilds(builds), [builds])
  const hasRules = latest.some((x) => x.stats.findings.some((f) => f.rules?.length))
  const rules = useQuery({
    queryKey: ["best-practices", "rules"],
    queryFn: api.bestPracticeRules,
    staleTime: Infinity,
    retry: false,
    enabled: hasRules,
  })
  const ruleByCode = useMemo(() => new Map((rules.data ?? []).map((r) => [r.code, r])), [rules.data])

  if (latest.length === 0) {
    return (
      <Empty className="h-full" data-testid="compose-build-view">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Hammer />
          </EmptyMedia>
          <EmptyTitle>No build metrics yet</EmptyTitle>
          <EmptyDescription>Build or start the project to record build metrics</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const last = builds[0]
  return (
    <div className="flex flex-col gap-2 p-2" data-testid="compose-build-view">
      <p className="px-1 text-[11px] text-muted-foreground">
        Last {last.action === "up" ? "Up" : "Build"} {formatRelative(last.at)} · {formatDuration(last.durationMs)}
        {!last.succeeded && <span className="text-destructive"> · failed</span>}
      </p>
      <div className="grid grid-cols-1 gap-2 xl:grid-cols-2">
        {latest.map(({ build, stats }) => (
          <ServiceBuildCard key={stats.service} build={build} stats={stats} newest={build === last} rules={ruleByCode} />
        ))}
      </div>
    </div>
  )
}

function Delta({ value, unit, higherIsBetter }: { value: number; unit: "bytes" | "ms" | "count"; higherIsBetter?: boolean }) {
  const d = formatDelta(value, unit, higherIsBetter)
  return (
    <span className={cn("text-[10px] tabular-nums", d.className)} title="Change since the previous build">
      {d.text}
    </span>
  )
}

function Metric({ label, value, children }: { label: string; value: string; children?: ReactNode }) {
  return (
    <div className="min-w-0 rounded-md border px-2 py-1">
      <div className="text-[10px] text-muted-foreground">{label}</div>
      <div className="flex items-baseline gap-1.5">
        <span className="truncate text-sm font-semibold tabular-nums">{value}</span>
        {children}
      </div>
    </div>
  )
}

function ServiceBuildCard({
  build,
  stats: s,
  newest,
  rules,
}: {
  build: ComposeBuild
  stats: ServiceBuildStats
  newest: boolean
  rules: Map<string, BestPracticeRule>
}) {
  const [stepsOpen, setStepsOpen] = useState(false)
  const icon = useMemo(() => techIconFor({ image: s.image, stack: s.stack ? [s.stack] : undefined, kind: "service" }), [s.image, s.stack])
  const steps = stepSummary(s.steps)
  const cacheTotal = s.cacheHits + s.cacheMisses
  const own = ownSize(s)
  const maxLayer = Math.max(1, ...s.layers.map((l) => l.sizeBytes))

  return (
    <section
      data-testid="compose-build-service"
      data-service={s.service}
      className={cn("flex min-w-0 flex-col gap-2 rounded-lg border p-2.5 text-xs", s.failed && "border-destructive/40")}
    >
      <header className="flex min-w-0 items-center gap-2">
        <TechIcon icon={icon} size={20} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate font-medium">{s.service}</span>
            {s.stack && <span className="rounded bg-muted px-1 text-[10px] text-muted-foreground">{s.stack}</span>}
            {s.failed && <span className="rounded bg-destructive/10 px-1 text-[10px] font-medium text-destructive">build failed</span>}
          </div>
          {s.image && <div className="truncate font-mono text-[10px] text-muted-foreground">{s.image}</div>}
        </div>
        {!newest && <span className="shrink-0 text-[10px] text-muted-foreground">built {formatRelative(build.at)}</span>}
      </header>

      <div className="grid grid-cols-3 gap-1.5">
        <Metric label="Build time" value={formatDuration(s.durationMs)}>
          {s.delta && <Delta value={s.delta.durationMs} unit="ms" />}
        </Metric>
        <Metric label="Cache" value={cacheTotal > 0 ? `${s.cacheHits}/${cacheTotal} cached` : "—"}>
          {s.delta && <Delta value={s.delta.cacheHits} unit="count" higherIsBetter />}
        </Metric>
        <Metric label="Image size" value={s.sizeBytes > 0 ? formatSize(s.sizeBytes) : "—"}>
          {s.delta && <Delta value={s.delta.sizeBytes} unit="bytes" />}
        </Metric>
      </div>

      {cacheTotal > 0 && (
        <div
          className="flex h-1.5 overflow-hidden rounded-full bg-amber-500/60"
          title={`${s.cacheHits} cached, ${s.cacheMisses} executed`}
          data-testid="compose-build-cache"
        >
          <div className="h-full bg-emerald-500" style={{ width: `${(s.cacheHits / cacheTotal) * 100}%` }} />
        </div>
      )}

      {s.sizeBytes > 0 && (
        <div className="flex flex-col gap-1">
          <div className="flex flex-wrap items-baseline gap-x-2 text-[11px]">
            {s.baseImage ? (
              <span className="min-w-0 truncate">
                Base <span className="font-mono">{s.baseImage}</span>{" "}
                <span className="text-muted-foreground">{s.baseSizeBytes > 0 ? formatSize(s.baseSizeBytes) : "size unknown"}</span>
              </span>
            ) : (
              <span className="text-muted-foreground">Base image unknown</span>
            )}
            {own !== undefined && (
              <span>
                · own layers <span className="font-medium tabular-nums">{formatSize(own)}</span>
              </span>
            )}
          </div>
          {own !== undefined && (
            <div className="flex h-1.5 overflow-hidden rounded-full bg-muted" title="Base image vs your own layers">
              <div className="h-full bg-muted-foreground/40" style={{ width: `${(s.baseSizeBytes / s.sizeBytes) * 100}%` }} />
              <div className="h-full bg-primary" style={{ width: `${(own / s.sizeBytes) * 100}%` }} />
            </div>
          )}
        </div>
      )}

      {s.layers.length > 0 && (
        <div>
          <div className="mb-0.5 text-[10px] font-medium text-muted-foreground">Biggest layers</div>
          <ul className="flex flex-col gap-0.5">
            {s.layers.map((l, i) => (
              <li key={i} className="grid grid-cols-[1fr_4rem_auto] items-center gap-2">
                <span className="truncate font-mono text-[10px]" title={l.createdBy}>
                  {l.createdBy || "(empty)"}
                </span>
                <div className="h-1 overflow-hidden rounded-full bg-muted">
                  <div
                    className={cn("h-full rounded-full", l.base ? "bg-muted-foreground/50" : "bg-primary")}
                    style={{ width: `${(l.sizeBytes / maxLayer) * 100}%` }}
                  />
                </div>
                <span className="flex items-center justify-end gap-1 tabular-nums">
                  {l.base && <span className="rounded bg-muted px-1 text-[9px] text-muted-foreground uppercase">base</span>}
                  {formatSize(l.sizeBytes)}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {s.findings.length > 0 && (
        <ul className="flex flex-col gap-1" data-testid="compose-build-findings">
          {s.findings.map((f, i) => {
            const meta = SEVERITY_META[f.severity]
            const Icon = meta.icon
            return (
              <li key={i} className="flex gap-1.5" data-code={f.code}>
                <Icon className={cn("mt-0.5 size-3.5 shrink-0", meta.className)} />
                <div className="min-w-0">
                  <p>{f.message}</p>
                  {f.fix && <p className="text-muted-foreground">{f.fix}</p>}
                  {f.rules && f.rules.length > 0 && (
                    <div className="mt-0.5 flex flex-wrap gap-1">
                      {f.rules.map((code) => (
                        <RuleChip key={code} code={code} rule={rules.get(code)} />
                      ))}
                    </div>
                  )}
                </div>
              </li>
            )
          })}
        </ul>
      )}

      {steps.total > 0 && (
        <Collapsible open={stepsOpen} onOpenChange={setStepsOpen}>
          <CollapsibleTrigger className="flex w-full items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground">
            <ChevronRight className={cn("size-3 shrink-0 transition-transform", stepsOpen && "rotate-90")} />
            <span className="shrink-0 whitespace-nowrap">
              Steps: {steps.executed} executed · {steps.cached} cached
            </span>
            {steps.slow.length > 0 && (
              <span className="ml-1 min-w-0 truncate">
                · slowest {steps.slow.map((x) => `${x.instruction} ${formatDuration(x.durationMs)}`).join(", ")}
              </span>
            )}
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ul className="mt-1 flex flex-col divide-y rounded-md border" data-testid="compose-build-steps">
              {s.steps
                .filter((x) => !x.internal)
                .map((x, i) => (
                  <li key={i} className={cn("flex items-center gap-2 px-2 py-0.5", x.error && "text-destructive")}>
                    <span
                      className={cn(
                        "w-14 shrink-0 text-[10px] font-medium",
                        x.error ? "text-destructive" : x.cached ? "text-emerald-600 dark:text-emerald-400" : "text-amber-600 dark:text-amber-400",
                      )}
                    >
                      {x.error ? "failed" : x.cached ? "cached" : "executed"}
                    </span>
                    {x.stage && <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{x.stage}</span>}
                    <span className="min-w-0 flex-1 truncate font-mono text-[10px]" title={x.instruction}>
                      {x.name}
                    </span>
                    <span className="shrink-0 tabular-nums text-muted-foreground">{x.cached ? "" : formatDuration(x.durationMs)}</span>
                  </li>
                ))}
            </ul>
          </CollapsibleContent>
        </Collapsible>
      )}
    </section>
  )
}

/** A best-practice rule id; hovering shows the rule (GET /best-practices/rules) when the daemon knows it. */
function RuleChip({ code, rule }: { code: string; rule: BestPracticeRule | undefined }) {
  const chip = (
    <span data-testid="compose-build-rule" className="rounded-full bg-muted px-1.5 font-mono text-[10px] text-muted-foreground">
      {code}
    </span>
  )
  if (!rule) return chip
  return (
    <Tooltip>
      <TooltipTrigger asChild>{chip}</TooltipTrigger>
      <TooltipContent className="max-w-72">
        <p className="font-medium">{rule.title}</p>
        <p className="opacity-80">{rule.description}</p>
      </TooltipContent>
    </Tooltip>
  )
}

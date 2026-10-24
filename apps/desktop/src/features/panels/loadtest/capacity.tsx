import { useContext, useMemo, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Calculator, ChevronDown, Cpu, Info, Layers, TriangleAlert, Wand2 } from "lucide-react"
import { toast } from "sonner"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { CapacityPlan, CapacityService, CloudEstimate, LoadTestRun } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { changeEnvironments } from "@/features/kubernetes/environments/actions"
import { formatBytes, formatNumber, formatRelative } from "../lib/format"
import { DENSE_TABLE } from "../variables/blocks-editors"
import { LoadRunsContext } from "./runs-context"

const LATEST = "__latest__"
const BASE = "__base__"

const CLOUD_LABEL: Record<CloudEstimate["cloud"], string> = { aws: "AWS", azure: "Azure", gcp: "Google Cloud", oci: "OCI" }

const CONFIDENCE_CLASS = {
  high: "border-emerald-500/30 text-emerald-700 dark:text-emerald-400",
  medium: "border-amber-500/40 text-amber-700 dark:text-amber-400",
  low: "border-destructive/40 text-destructive",
}

const ROLE_LABEL: Record<CapacityService["role"], string> = { app: "scales out", dependency: "data", idle: "idle" }

const usd = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 0 })
const usdHour = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 4 })

/** A run can size a plan: finished, with container usage, no fault scenario. */
export function plannable(run: LoadTestRun): boolean {
  return run.status !== "running" && run.status !== "error" && !!run.resources?.services.length && !run.spec.scenario
}

/**
 * Capacity planner: replicas, requests/limits, nodes and monthly cost per cloud for a target request rate, sized from
 * a finished load test of one environment. The latest plan of each environment is kept by the daemon; Apply writes it
 * into that environment's own files after a confirmation.
 */
export function CapacityPlanner({ initialRunId }: { initialRunId?: string | null }) {
  const workflow = useWorkflowStore((s) => s.workflow!)
  const { runs } = useContext(LoadRunsContext)
  const envNames = useMemo(() => workflow.environments.map((e) => e.name), [workflow.environments])
  const active = workflow.activeEnvironment ?? ""
  // The base is only plannable while it is what runs (no environment active): the daemon reads "" as the active one.
  const envOptions = active === "" ? ["", ...envNames] : envNames
  const initialRun = initialRunId ? runs.find((r) => r.id === initialRunId) : undefined
  const initialEnv = initialRun && envOptions.includes(initialRun.environment ?? "") ? (initialRun.environment ?? "") : active
  const [env, setEnv] = useState(initialEnv)
  const [runId, setRunId] = useState(initialRun && initialEnv === (initialRun.environment ?? "") ? initialRun.id : LATEST)
  const [target, setTarget] = useState("")
  const [p95, setP95] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const queryClient = useQueryClient()

  const envRuns = runs.filter((r) => (r.environment ?? "") === env && plannable(r))
  const chosen = runId === LATEST ? envRuns[0] : envRuns.find((r) => r.id === runId)
  const peak = chosen?.summary?.peakRps ?? 0

  const last = useQuery({
    queryKey: ["capacity-plan", workflow.id, env],
    queryFn: () => api.capacityPlan(workflow.id, env || undefined),
  })
  const plan = last.data ?? null

  const submit = async () => {
    const targetRps = Number(target)
    if (!(targetRps > 0)) {
      setError("Enter the peak requests per second to plan for.")
      return
    }
    setBusy(true)
    setError(null)
    try {
      const out = await api.planCapacity(workflow.id, {
        targetRps,
        p95Ms: Number(p95) > 0 ? Number(p95) : undefined,
        environment: env || undefined,
        runId: chosen?.id,
      })
      queryClient.setQueryData(["capacity-plan", workflow.id, env], out)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-2 overflow-auto p-2" data-testid="capacity-planner">
      <div className="flex flex-wrap items-end gap-2 rounded-lg border p-2">
        {envOptions.length > 1 && (
          <div className="flex flex-col gap-1">
            <Label className="text-[11px]">Environment</Label>
            <Select
              value={env === "" ? BASE : env}
              onValueChange={(v) => {
                setEnv(v === BASE ? "" : v)
                setRunId(LATEST)
              }}
            >
              <SelectTrigger size="sm" className="h-7 w-32 text-xs" data-testid="capacity-environment">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {envOptions.map((n) => (
                  <SelectItem key={n || BASE} value={n || BASE}>
                    {n || "Base"}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
        <div className="flex min-w-0 flex-col gap-1">
          <Label className="text-[11px]">Measured by</Label>
          <Select value={runId} onValueChange={setRunId}>
            <SelectTrigger size="sm" className="h-7 w-56 max-w-full text-xs" data-testid="capacity-run">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={LATEST}>Newest run{envRuns[0] ? ` (${runLabel(envRuns[0])})` : ""}</SelectItem>
              {envRuns.map((r) => (
                <SelectItem key={r.id} value={r.id}>
                  {runLabel(r)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex flex-col gap-1">
          <Label className="text-[11px]" htmlFor="capacity-target">
            Target req/s
          </Label>
          <Input
            id="capacity-target"
            type="number"
            min={1}
            className="h-7 w-28 text-xs"
            placeholder={peak ? formatNumber(peak * 2) : "1000"}
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            data-testid="capacity-target"
          />
        </div>
        {peak > 0 && (
          <div className="flex items-center gap-1 pb-0.5">
            {[2, 5, 10].map((k) => (
              <Button key={k} size="xs" variant="outline" onClick={() => setTarget(String(Math.round(peak * k)))}>
                {k}× peak
              </Button>
            ))}
          </div>
        )}
        <div className="flex flex-col gap-1">
          <Label className="text-[11px]" htmlFor="capacity-p95">
            p95 objective (ms)
          </Label>
          <Input
            id="capacity-p95"
            type="number"
            min={1}
            className="h-7 w-24 text-xs"
            placeholder="optional"
            value={p95}
            onChange={(e) => setP95(e.target.value)}
          />
        </div>
        <Button size="sm" className="h-7" disabled={busy || !chosen} onClick={() => void submit()} data-testid="capacity-submit">
          {busy ? <Spinner /> : <Calculator />} Plan capacity
        </Button>
        {error && <p className="basis-full text-xs text-destructive">{error}</p>}
        {!chosen && (
          <p className="basis-full text-xs text-muted-foreground">
            No finished load test of {env ? `environment ${env}` : "the base"} recorded container usage yet. Run one (a
            ramp of about a minute) from New test, then plan.
          </p>
        )}
      </div>

      {plan ? (
        <PlanView plan={plan} />
      ) : last.isPending ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner />
        </div>
      ) : (
        <Empty className="flex-1">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Calculator />
            </EmptyMedia>
            <EmptyTitle>Plan for real traffic</EmptyTitle>
            <EmptyDescription>
              Uses what a load test measured — CPU per request and memory per replica before latency degraded — to size
              replicas, requests/limits and nodes for a target rate, with a monthly cost estimate for four clouds.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}
    </div>
  )
}

function runLabel(r: LoadTestRun): string {
  const peak = r.summary ? ` · peak ${formatNumber(r.summary.peakRps)} req/s` : ""
  return `${r.spec.name} · ${formatRelative(r.startedAt)}${peak}`
}

function PlanView({ plan }: { plan: CapacityPlan }) {
  const replicas = plan.services.reduce((n, s) => n + s.replicas, 0)
  const cheapest = [...plan.clouds].sort((a, b) => a.recommended.monthlyUsd - b.recommended.monthlyUsd)[0]
  return (
    <div className="flex flex-col gap-2" data-testid="capacity-plan">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className={cn("h-5 px-1.5 text-[11px]", CONFIDENCE_CLASS[plan.confidence])} data-testid="capacity-confidence">
          {plan.confidence} confidence
        </Badge>
        <span className="text-sm font-semibold">
          {formatNumber(plan.targetRps)} req/s{plan.environment ? ` in ${plan.environment}` : ""}
        </span>
        <span className="text-xs text-muted-foreground">
          measured {formatNumber(plan.measuredRps)} req/s before degrading ({formatNumber(plan.extrapolation, 1)}×) · {plan.run.name},{" "}
          {plan.run.healthySec} healthy s · planned {formatRelative(plan.createdAt)}
        </span>
        <ApplyPlan plan={plan} />
      </div>
      <ul className="flex flex-col gap-0.5 text-xs text-muted-foreground">
        {plan.confidenceReasons.map((r) => (
          <li key={r} className="flex gap-1">
            <Info className="mt-0.5 size-3 shrink-0" /> {r}
          </li>
        ))}
      </ul>

      <div className="grid grid-cols-2 gap-2 @xl:grid-cols-4">
        <Stat label="Replicas" value={formatNumber(replicas)} sub={`${plan.services.length} services`} />
        <Stat label="vCPU requested" value={formatNumber(plan.totalCpu, 2)} />
        <Stat label="Memory requested" value={`${formatNumber(plan.totalMemGiB, 2)} GiB`} />
        <Stat
          label="Cheapest compute"
          value={cheapest ? `${usd.format(cheapest.recommended.monthlyUsd)}/mo` : "—"}
          sub={cheapest ? `${CLOUD_LABEL[cheapest.cloud]} · ${cheapest.recommended.nodes} × ${cheapest.recommended.shape}` : undefined}
        />
      </div>

      <div className="min-w-0 overflow-x-auto rounded-lg border" data-testid="capacity-services">
        <Table className={DENSE_TABLE}>
          <TableHeader>
            <TableRow>
              <TableHead>Service</TableHead>
              <TableHead>Replicas</TableHead>
              <TableHead>Per replica</TableHead>
              <TableHead>CPU</TableHead>
              <TableHead>Memory</TableHead>
              <TableHead className="min-w-72">Why</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {plan.services.map((s) => (
              <ServiceRow key={s.nodeId} s={s} kube={plan.kind === "kubernetes"} />
            ))}
          </TableBody>
        </Table>
      </div>

      <div className="grid grid-cols-1 gap-2 @xl:grid-cols-2 @6xl:grid-cols-4" data-testid="capacity-clouds">
        {plan.clouds.map((c) => (
          <CloudCard key={c.cloud} c={c} />
        ))}
      </div>
      <p className="text-[11px] text-muted-foreground" data-testid="capacity-prices">
        Estimate as of {plan.prices.asOf},{" "}
        {plan.clouds.map((c) => `${CLOUD_LABEL[c.cloud]} ${c.region}`).join(", ")}, on-demand, compute only — no storage,
        load balancers, egress or discounts.
      </p>

      {plan.caveats.length > 0 && (
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/5 p-2" data-testid="capacity-caveats">
          <div className="mb-1 flex items-center gap-1 text-[11px] font-medium text-amber-700 dark:text-amber-400">
            <TriangleAlert className="size-3" /> Caveats
          </div>
          <ul className="flex list-disc flex-col gap-0.5 pl-4 text-xs">
            {plan.caveats.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
        </div>
      )}
      <Collapsible>
        <CollapsibleTrigger className="group flex items-center gap-1 text-[11px] font-medium text-muted-foreground hover:text-foreground">
          <ChevronDown className="size-3 transition-transform group-data-[state=closed]:-rotate-90" /> How it was sized
        </CollapsibleTrigger>
        <CollapsibleContent>
          <ul className="mt-1 flex list-disc flex-col gap-0.5 pl-4 text-xs text-muted-foreground">
            {plan.assumptions.map((a) => (
              <li key={a}>{a}</li>
            ))}
          </ul>
        </CollapsibleContent>
      </Collapsible>
    </div>
  )
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="min-w-0 rounded-lg border p-2">
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className="truncate text-base font-semibold tabular-nums">{value}</div>
      {sub && <div className="truncate text-[11px] text-muted-foreground">{sub}</div>}
    </div>
  )
}

function ServiceRow({ s, kube }: { s: CapacityService; kube: boolean }) {
  const from = s.currentReplicas || s.measuredReplicas
  return (
    <TableRow data-service={s.service} data-role={s.role} className="align-top">
      <TableCell>
        <div className="flex items-center gap-1 font-medium">
          {s.service}
          {s.target && (
            <Badge variant="outline" className="h-4 px-1 text-[10px]">
              target
            </Badge>
          )}
        </div>
        <div className="text-[10px] text-muted-foreground">{ROLE_LABEL[s.role]}</div>
      </TableCell>
      <TableCell className="tabular-nums">
        <div className="font-semibold">
          {from !== s.replicas && <span className="font-normal text-muted-foreground">{from} → </span>}
          {s.replicas}
        </div>
        {s.role === "app" && <div className="text-[10px] text-muted-foreground">{formatNumber(s.replicasRaw, 2)} needed + 1</div>}
        {kube && s.hpa && (
          <div className="text-[10px] text-muted-foreground" data-testid="capacity-hpa">
            HPA {s.hpa.min}–{s.hpa.max} at {s.hpa.targetCpu}%
          </div>
        )}
      </TableCell>
      <TableCell className="tabular-nums">
        {s.perReplicaRps ? (
          <>
            <div>{formatNumber(s.perReplicaRps)} req/s</div>
            <div className="text-[10px] text-muted-foreground">
              {s.limit === "latency" ? "p95 objective" : `at ${Math.round(s.cpuCeiling * 100)}% CPU`}
            </div>
          </>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </TableCell>
      <TableCell className="tabular-nums">
        <div>{kube ? `${s.cpuRequest} / ${s.cpuLimit}` : `${s.cpuLimit} CPU`}</div>
        <div className="text-[10px] text-muted-foreground">
          <Cpu className="mr-0.5 inline size-2.5" />
          {formatNumber(s.projectedCpu, 2)} used at target
        </div>
      </TableCell>
      <TableCell className="tabular-nums">
        <div>{kube ? `${s.memRequest} / ${s.memLimit}` : s.memLimit}</div>
        <div className="text-[10px] text-muted-foreground">peak {formatBytes(s.peakMemBytes)}</div>
      </TableCell>
      <TableCell className="whitespace-normal">
        <p className="text-pretty">{s.note}</p>
        {s.caveat && (
          <p className="mt-0.5 flex gap-1 text-pretty text-amber-700 dark:text-amber-400">
            <TriangleAlert className="mt-0.5 size-3 shrink-0" />
            {s.caveat}
          </p>
        )}
      </TableCell>
    </TableRow>
  )
}

function CloudCard({ c }: { c: CloudEstimate }) {
  const r = c.recommended
  const other = c.options.find((o) => o.arch !== r.arch)
  return (
    <div className="min-w-0 rounded-lg border p-2" data-cloud={c.cloud}>
      <div className="flex items-center gap-1 text-[11px] text-muted-foreground">
        <span className="font-medium text-foreground">{CLOUD_LABEL[c.cloud]}</span> {c.region}
      </div>
      <div className="mt-0.5 text-base font-semibold tabular-nums">{usd.format(r.monthlyUsd)}/mo</div>
      <div className="flex flex-wrap items-center gap-1 text-xs">
        <span className="tabular-nums">{r.nodes} ×</span>
        <span className="truncate font-mono text-[11px]">{r.shape}</span>
        <Badge variant="outline" className="h-4 px-1 text-[10px]">
          {r.arch === "arm64" ? "ARM" : "x86"}
        </Badge>
      </div>
      <div className="text-[10px] text-muted-foreground tabular-nums">
        {r.vcpu} vCPU · {r.memGiB} GiB each ({r.allocCpu} / {r.allocMemGiB} GiB for pods) · {usdHour.format(r.hourlyUsd)}/h
      </div>
      <UsageBar label="CPU" value={r.cpuUsed} />
      <UsageBar label="Mem" value={r.memUsed} />
      {other && (
        <div className="mt-1 truncate text-[10px] text-muted-foreground">
          {other.arch === "arm64" ? "ARM" : "x86"}: {other.nodes} × {other.shape} · {usd.format(other.monthlyUsd)}/mo
        </div>
      )}
    </div>
  )
}

function UsageBar({ label, value }: { label: string; value: number }) {
  return (
    <div className="mt-1 flex items-center gap-1 text-[10px] text-muted-foreground">
      <span className="w-6">{label}</span>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
        <div className="h-full rounded-full bg-primary/70" style={{ width: `${Math.min(100, Math.round(value * 100))}%` }} />
      </div>
      <span className="w-8 text-right tabular-nums">{Math.round(value * 100)}%</span>
    </div>
  )
}

/** Writes the plan into its environment after listing every change. */
function ApplyPlan({ plan }: { plan: CapacityPlan }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const queryClient = useQueryClient()
  const changes = plan.services.filter((s) => s.apply && (Object.keys(s.apply.values).length > 0 || s.apply.skipped?.length))
  const env = plan.environment ?? ""

  if (!plan.applicable) {
    return plan.applyNote ? (
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="ml-auto">
            <Button size="xs" variant="outline" disabled data-testid="capacity-apply">
              <Wand2 /> Apply
            </Button>
          </span>
        </TooltipTrigger>
        <TooltipContent className="max-w-72">{plan.applyNote}</TooltipContent>
      </Tooltip>
    ) : null
  }

  const apply = async () => {
    setBusy(true)
    const done = await changeEnvironments((id) => api.applyCapacityPlan(id, { environment: env }).then((r) => r.workflow), {
      failure: `Couldn't apply the plan to ${env}`,
    })
    setBusy(false)
    setOpen(false)
    if (done) {
      void queryClient.invalidateQueries({ queryKey: ["capacity-plan", plan.workflowId] })
      toast.success(`Plan applied to ${env}`, {
        description: "Deploy the environment to run with it.",
        action: { label: "Compare environments", onClick: () => showBottom("environments") },
      })
    }
  }

  return (
    <>
      <Button size="xs" className="ml-auto" onClick={() => setOpen(true)} data-testid="capacity-apply">
        <Wand2 /> Apply to {env}
      </Button>
      <AlertDialog open={open} onOpenChange={(o) => !busy && setOpen(o)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Apply the plan to {env}?</AlertDialogTitle>
            <AlertDialogDescription>
              These values go into {env}’s own files; the base and other environments keep theirs. Deploy afterwards to
              run with them.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <ul className="flex max-h-72 flex-col gap-2 overflow-auto text-xs" data-testid="capacity-apply-changes">
            {changes.map((s) => (
              <li key={s.nodeId}>
                <div className="flex items-center gap-1 font-medium">
                  <Layers className="size-3" /> {s.service}
                </div>
                <ul className="ml-4 list-disc">
                  {s.apply!.changes.map((c) => (
                    <li key={c} className="tabular-nums">
                      {c}
                    </li>
                  ))}
                  {s.apply!.skipped?.map((c) => (
                    <li key={c} className="text-muted-foreground">
                      Skipped: {c}
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={busy}
              data-testid="capacity-apply-confirm"
              onClick={(e) => {
                e.preventDefault()
                void apply()
              }}
            >
              {busy && <Spinner />} Apply
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

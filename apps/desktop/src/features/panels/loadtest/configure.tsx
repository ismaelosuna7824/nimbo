import { useState, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { ArrowLeft, ArrowRight, Info, Play, Plus, ShieldAlert, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { errorMessage } from "@/components/errors"
import { api, ApiError } from "@/lib/api/client"
import type { LoadProfile, LoadTarget, LoadTestRun, LoadTestSpec, Threshold } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { ConfirmExternalDialog } from "@/features/connections/confirm-external"
import type { ExternalTarget } from "@/features/connections/safety"
import { formatDuration } from "../lib/format"
import type { LoadDraft } from "./draft"
import { DEFAULT_THRESHOLDS, METRIC_META, modeUnit, stagePoints, totalDuration } from "./metrics"
import { scenarioProblem, ScenarioStep } from "./scenario-step"

export function Sparkline({ profile, className }: { profile: LoadProfile; className?: string }) {
  const pts = stagePoints(profile.stages)
  const maxT = Math.max(1, pts[pts.length - 1][0])
  const maxY = Math.max(1, ...pts.map((p) => p[1]))
  const w = 120
  const h = 28
  const coords = pts.map(([t, y]) => `${((t / maxT) * w).toFixed(1)},${(h - 2 - (y / maxY) * (h - 4)).toFixed(1)}`)
  return (
    <svg viewBox={`0 0 ${w} ${h}`} className={cn("h-7 w-full", className)} preserveAspectRatio="none" aria-hidden>
      <polygon points={`0,${h} ${coords.join(" ")} ${w},${h}`} className="fill-primary/10" />
      <polyline points={coords.join(" ")} fill="none" className="stroke-primary" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

export function StepHeader({ n, title, hint }: { n: number; title: string; hint: string }) {
  return (
    <div className="mb-2 flex items-baseline gap-2">
      <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-primary text-[11px] font-semibold text-primary-foreground">
        {n}
      </span>
      <div>
        <h3 className="text-xs font-semibold">{title}</h3>
        <p className="text-[11px] text-muted-foreground">{hint}</p>
      </div>
    </div>
  )
}

export type SetDraft = (update: (d: LoadDraft) => LoadDraft) => void

interface ConfigureProps {
  draft: LoadDraft
  setDraft: SetDraft
  /** The target step-1 currently describes, or null while incomplete. */
  target: LoadTarget | null
  /** Step 1 UI: workflow targets or a connection's targets. */
  targetStep: ReactNode
  /** Set when the target is outside the local sandbox: Start then asks for confirmation. */
  external: ExternalTarget | null
  /** Describes the target when the API demands confirmation the UI did not expect. */
  describeForced: (target: LoadTarget) => ExternalTarget | null
  /** Starts the run on its owner (workflow or connection). */
  start: (spec: LoadTestSpec) => Promise<LoadTestRun>
  onStarted: (run: LoadTestRun) => void
  /** The target runs in a Kubernetes workflow's cluster: offer the autoscaling preset and warn about short runs. */
  autoscaling?: boolean
  /** Offer an incident scenario (docker and Kubernetes workflow targets inside the sandbox). */
  scenarios?: boolean
}

/** Runs shorter than this end before a HorizontalPodAutoscaler can add Ready pods (the daemon's minScalingRunSec). */
const MIN_SCALING_RUN_SEC = 60

/** The three-step load test wizard: target (pluggable), traffic shape, pass criteria. */
export function Configure({
  draft,
  setDraft,
  target,
  targetStep,
  external,
  describeForced,
  start,
  onStarted,
  autoscaling = false,
  scenarios = false,
}: ConfigureProps) {
  const pro = useWorkflowStore((s) => s.mode === "pro")
  const [step, setStep] = useState(1)
  const [starting, setStarting] = useState(false)
  /** Spec waiting for the user to confirm external traffic, with what to show. */
  const [pending, setPending] = useState<{ spec: LoadTestSpec; info: ExternalTarget | null } | null>(null)

  const presets = useQuery({ queryKey: ["load-presets"], queryFn: api.loadPresets, staleTime: Infinity })
  const profile = draft.profile ?? presets.data?.[0] ?? null
  const scenario = scenarios ? draft.scenario : null
  const scenarioIssue = scenarioProblem(scenario, profile) ?? (scenario && !scenario.target && scenario.id !== "traffic_spike" ? "Choose what the incident acts on." : null)

  const launch = async (spec: LoadTestSpec) => {
    setStarting(true)
    try {
      onStarted(await start(spec))
    } catch (err) {
      if (err instanceof ApiError && err.code === "confirmation_required" && !spec.confirmExternal) {
        setPending({ spec, info: describeForced(spec.target) })
        return
      }
      toast.error("Could not start the load test", { description: errorMessage(err) })
    } finally {
      setStarting(false)
    }
  }

  const onStart = () => {
    if (!target || !profile) return
    const spec: LoadTestSpec = {
      name: `${profile.label ?? profile.preset} · ${target.label}`,
      target,
      profile,
      thresholds: draft.thresholds,
      timeoutMs: draft.timeoutMs,
      maxWorkers: draft.maxWorkers,
      ...(scenario && { scenario }),
    }
    if (external) setPending({ spec, info: external })
    else void launch(spec)
  }

  const startButton = (
    <Button size="sm" onClick={onStart} disabled={!target || !profile || starting || !!scenarioIssue} title={scenarioIssue ?? undefined}>
      {starting ? <Spinner /> : <Play />} Start test
    </Button>
  )

  const trafficStep = (
    <>
      <TrafficStep
        draft={draft}
        setDraft={setDraft}
        presets={(presets.data ?? []).filter((p) => p.preset !== "autoscale" || autoscaling)}
        profile={profile}
        pro={pro}
        autoscaling={autoscaling}
      />
      {scenarios && <ScenarioStep draft={draft} setDraft={setDraft} profile={profile} />}
    </>
  )
  const thresholdStep = <ThresholdStep draft={draft} setDraft={setDraft} pro={pro} />
  const confirm = (
    <ConfirmExternalDialog
      spec={pending?.spec ?? null}
      info={pending?.info ?? null}
      onCancel={() => setPending(null)}
      onConfirm={() => {
        if (!pending) return
        setPending(null)
        void launch({ ...pending.spec, confirmExternal: true })
      }}
    />
  )

  if (pro) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        {/* Three columns from 64rem of panel up; narrower, the steps stack and scroll as one page. */}
        <div className="grid min-h-0 flex-1 grid-cols-1 divide-y overflow-auto @5xl:grid-cols-3 @5xl:divide-x @5xl:divide-y-0">
          <div className="min-h-0 overflow-auto p-2.5">{targetStep}</div>
          <div className="min-h-0 overflow-auto p-2.5">{trafficStep}</div>
          <div className="min-h-0 overflow-auto p-2.5">{thresholdStep}</div>
        </div>
        <div className="flex shrink-0 items-center justify-end gap-2 border-t px-2.5 py-1.5">
          <Summary target={target} profile={profile} external={external} />
          {startButton}
        </div>
        {confirm}
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 overflow-auto p-2.5">
        {step === 1 && targetStep}
        {step === 2 && trafficStep}
        {step === 3 && thresholdStep}
      </div>
      <div className="flex shrink-0 items-center gap-2 border-t px-2.5 py-1.5">
        <div className="flex items-center gap-1" aria-label={`Step ${step} of 3`}>
          {[1, 2, 3].map((n) => (
            <span key={n} className={cn("h-1.5 w-6 rounded-full", n <= step ? "bg-primary" : "bg-muted")} />
          ))}
        </div>
        <Summary target={target} profile={profile} external={external} />
        {step > 1 && (
          <Button size="sm" variant="ghost" onClick={() => setStep(step - 1)}>
            <ArrowLeft /> Back
          </Button>
        )}
        {step < 3 ? (
          <Button size="sm" onClick={() => setStep(step + 1)} disabled={step === 1 ? !target : !profile}>
            Next <ArrowRight />
          </Button>
        ) : (
          startButton
        )}
      </div>
      {confirm}
    </div>
  )
}

function Summary({ target, profile, external }: { target: LoadTarget | null; profile: LoadProfile | null; external: ExternalTarget | null }) {
  return (
    <span className="ml-auto flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
      {external && (
        <Badge variant="outline" className="h-4 shrink-0 gap-0.5 border-amber-500/40 px-1 text-[10px] text-amber-700 dark:text-amber-400">
          <ShieldAlert className="size-2.5" /> External
        </Badge>
      )}
      <span className="min-w-0 truncate">
        {target ? target.label : "Step 1: choose what to test"}
        {profile && ` · ${profile.label ?? profile.preset} · ${formatDuration(totalDuration(profile.stages) * 1000)}`}
      </span>
    </span>
  )
}

function TrafficStep({
  draft,
  setDraft,
  presets,
  profile,
  pro,
  autoscaling,
}: {
  draft: LoadDraft
  setDraft: SetDraft
  presets: LoadProfile[]
  profile: LoadProfile | null
  pro: boolean
  autoscaling: boolean
}) {
  const setProfile = (p: LoadProfile) => setDraft((d) => ({ ...d, profile: p }))
  const editStages = (stages: LoadProfile["stages"]) =>
    profile && setProfile({ ...profile, preset: "custom", label: "Custom", description: undefined, stages })

  return (
    <div>
      <StepHeader n={2} title="How much traffic?" hint="Choose a traffic shape. Each one answers a different question." />
      <div className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-1.5">
        {presets.map((p) => {
          const selected = profile?.preset === p.preset
          const peak = Math.max(0, ...p.stages.map((s) => s.target))
          return (
            <button
              key={p.preset}
              type="button"
              aria-pressed={selected}
              onClick={() => setProfile(structuredClone(p))}
              className={cn(
                "flex flex-col gap-0.5 rounded-lg border p-2 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring/50",
                selected && "border-primary bg-primary/5",
              )}
            >
              <span className="text-xs font-semibold">{p.label ?? p.preset}</span>
              <Sparkline profile={p} />
              <span className="line-clamp-2 text-[11px] text-muted-foreground">{p.description}</span>
              <span className="text-[10px] text-muted-foreground tabular-nums">
                {formatDuration(totalDuration(p.stages) * 1000)} · up to {peak} {modeUnit(p.mode)}
              </span>
            </button>
          )
        })}
      </div>
      {autoscaling && profile && totalDuration(profile.stages) < MIN_SCALING_RUN_SEC && (
        <p className="mt-2 flex items-start gap-1.5 text-[11px] text-muted-foreground" data-testid="loadtest-short-for-scaling">
          <Info className="mt-px size-3 shrink-0" />
          Under a minute is too short to see a HorizontalPodAutoscaler add pods (15 s metrics, 15 s sync, pod startup). Pick
          Autoscaling test to judge autoscaling.
        </p>
      )}

      {pro && profile && (
        <div className="mt-3 flex flex-col gap-2">
          <div className="flex items-center gap-2">
            <Label className="text-[11px]">Mode</Label>
            <ToggleGroup
              type="single"
              size="sm"
              variant="outline"
              value={profile.mode}
              onValueChange={(v) => v && setProfile({ ...profile, mode: v as LoadProfile["mode"] })}
            >
              <ToggleGroupItem value="rps" className="text-xs" title="Fixed arrival rate (requests per second)">
                req/s
              </ToggleGroupItem>
              <ToggleGroupItem value="concurrency" className="text-xs" title="Fixed number of concurrent virtual users">
                users
              </ToggleGroupItem>
            </ToggleGroup>
            {profile.preset === "custom" && <Badge variant="secondary">Custom</Badge>}
          </div>
          <div className="rounded-md border">
            <div className="grid grid-cols-[2rem_1fr_1fr_1.75rem] gap-1 border-b px-1.5 py-1 text-[11px] text-muted-foreground">
              <span>#</span>
              <span>Duration (s)</span>
              <span>Target ({modeUnit(profile.mode)})</span>
              <span />
            </div>
            {profile.stages.map((s, i) => (
              <div key={i} className="grid grid-cols-[2rem_1fr_1fr_1.75rem] items-center gap-1 px-1.5 py-0.5">
                <span className="text-[11px] text-muted-foreground">{i + 1}</span>
                <Input
                  type="number"
                  min={0}
                  className="h-6 text-xs"
                  title="0 = jump instantly to the target"
                  value={s.durationSec}
                  onChange={(e) =>
                    editStages(profile.stages.map((x, j) => (j === i ? { ...x, durationSec: Math.max(0, Number(e.target.value) || 0) } : x)))
                  }
                />
                <Input
                  type="number"
                  min={0}
                  className="h-6 text-xs"
                  value={s.target}
                  onChange={(e) =>
                    editStages(profile.stages.map((x, j) => (j === i ? { ...x, target: Math.max(0, Number(e.target.value) || 0) } : x)))
                  }
                />
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={`Remove stage ${i + 1}`}
                  disabled={profile.stages.length === 1}
                  onClick={() => editStages(profile.stages.filter((_, j) => j !== i))}
                >
                  <Trash2 />
                </Button>
              </div>
            ))}
            <div className="flex items-center gap-2 border-t px-1.5 py-1">
              <Button
                size="xs"
                variant="ghost"
                onClick={() => {
                  const last = profile.stages[profile.stages.length - 1]
                  editStages([...profile.stages, { durationSec: 30, target: last?.target ?? 10 }])
                }}
              >
                <Plus /> Stage
              </Button>
              <Sparkline profile={profile} className="h-5 flex-1" />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div className="flex flex-col gap-1">
              <Label className="text-[11px]">Request timeout (ms)</Label>
              <Input
                type="number"
                min={100}
                className="h-7 text-xs"
                value={draft.timeoutMs}
                onChange={(e) => setDraft((d) => ({ ...d, timeoutMs: Math.max(100, Number(e.target.value) || 5000) }))}
              />
            </div>
            <div className="flex flex-col gap-1">
              <Label className="text-[11px]" title="Cap on in-flight requests in req/s mode">
                Max workers
              </Label>
              <Input
                type="number"
                min={1}
                placeholder="auto"
                className="h-7 text-xs"
                value={draft.maxWorkers ?? ""}
                onChange={(e) =>
                  setDraft((d) => ({ ...d, maxWorkers: e.target.value ? Math.max(1, Number(e.target.value)) : undefined }))
                }
              />
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

const OPS: Threshold["op"][] = ["<", "<=", ">", ">="]

function ThresholdStep({ draft, setDraft, pro }: { draft: LoadDraft; setDraft: SetDraft; pro: boolean }) {
  const set = (thresholds: Threshold[]) => setDraft((d) => ({ ...d, thresholds }))
  return (
    <div>
      <StepHeader
        n={3}
        title="When does it pass?"
        hint="The test passes only if every criterion holds for the whole run."
      />
      <div className="flex flex-col gap-1.5">
        {draft.thresholds.map((t, i) => {
          const meta = METRIC_META[t.metric]
          const ratio = meta.unit === "ratio"
          return (
            <div key={i} className="flex items-center gap-1.5 text-xs">
              {pro ? (
                <Select
                  value={t.metric}
                  onValueChange={(m) => set(draft.thresholds.map((x, j) => (j === i ? { ...x, metric: m as Threshold["metric"] } : x)))}
                >
                  <SelectTrigger size="sm" className="h-7 min-w-0 flex-1 text-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {Object.entries(METRIC_META).map(([k, m]) => (
                      <SelectItem key={k} value={k}>
                        {m.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <span className="min-w-0 flex-1 truncate">{meta.label}</span>
              )}
              {pro ? (
                <Select
                  value={t.op}
                  onValueChange={(op) => set(draft.thresholds.map((x, j) => (j === i ? { ...x, op: op as Threshold["op"] } : x)))}
                >
                  <SelectTrigger size="sm" className="h-7 w-14 shrink-0 font-mono text-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {OPS.map((op) => (
                      <SelectItem key={op} value={op} className="font-mono">
                        {op}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <span className="w-12 shrink-0 text-muted-foreground">{t.op.startsWith("<") ? "below" : "above"}</span>
              )}
              <Input
                type="number"
                min={0}
                step={ratio ? 0.1 : 1}
                className="h-7 w-20 shrink-0 text-xs"
                value={ratio ? +(t.value * 100).toFixed(4) : t.value}
                onChange={(e) => {
                  const n = Number(e.target.value)
                  if (!Number.isFinite(n)) return
                  set(draft.thresholds.map((x, j) => (j === i ? { ...x, value: ratio ? n / 100 : n } : x)))
                }}
              />
              <span className="w-9 shrink-0 text-muted-foreground">{ratio ? "%" : meta.unit === "ms" ? "ms" : "req/s"}</span>
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label="Remove criterion"
                onClick={() => set(draft.thresholds.filter((_, j) => j !== i))}
              >
                <Trash2 />
              </Button>
            </div>
          )
        })}
        {draft.thresholds.length === 0 && (
          <p className="text-xs text-muted-foreground">No criteria — the test always passes if it completes.</p>
        )}
        <div className="flex gap-1">
          <Button
            size="xs"
            variant="ghost"
            onClick={() => set([...draft.thresholds, { metric: "p99_ms", op: "<", value: 1000 }])}
          >
            <Plus /> Add criterion
          </Button>
          <Button size="xs" variant="ghost" onClick={() => set(DEFAULT_THRESHOLDS)}>
            Reset to defaults
          </Button>
        </div>
      </div>
    </div>
  )
}

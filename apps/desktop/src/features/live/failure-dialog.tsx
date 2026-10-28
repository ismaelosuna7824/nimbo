import { useQuery } from "@tanstack/react-query"
import { FlameIcon, InfoIcon, PlayIcon, ShieldCheckIcon } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api/client"
import type { LoadProfile, LoadTestSpec, ScenarioCatalog, ScenarioSpec, WorkflowKind } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { formatDuration } from "@/features/panels/lib/format"
import { INITIAL_DRAFT, type LoadDraft } from "@/features/panels/loadtest/draft"
import { DEFAULT_THRESHOLDS, totalDuration } from "@/features/panels/loadtest/metrics"
import { instantScenario } from "@/features/panels/loadtest/scenario-result"
import { WindowBar } from "@/features/panels/loadtest/scenario-step"
import { initialSpec, scenarioProblem } from "@/features/panels/loadtest/scenario-timing"
import { targetDetail, targetKey } from "@/features/panels/loadtest/targets"
import { useWorkflowTarget } from "@/features/panels/loadtest/use-workflow-target"
import { failureChoices, faultAction, faultExpect, presetFits, rankTargets, targetPath, withPath, type FailureChoice } from "./failure"
import { useFailureTest } from "./live-store"
import type { FlowEdge } from "./traffic"

const close = () => useFailureTest.setState({ nodeId: null })

/**
 * "Test a failure…" of a Docker service or Kubernetes workload: the incidents that apply to it, explained, with the
 * traffic to run them under; starting it runs a load test with that incident, whose live impact shows on the canvas.
 * Mounted by the canvas; `edges` (caller → callee) pick the traffic that reaches the node.
 */
export function FailureTestDialog({ kind, edges, nameOf }: { kind: WorkflowKind; edges: FlowEdge[]; nameOf: (id: string) => string }) {
  const nodeId = useFailureTest((s) => s.nodeId)
  return (
    <Dialog open={!!nodeId} onOpenChange={(open) => !open && close()}>
      <DialogContent className="sm:max-w-xl" data-testid="failure-test-dialog">
        {nodeId && <FailureTestForm key={nodeId} kind={kind} nodeId={nodeId} name={nameOf(nodeId)} edges={edges} />}
      </DialogContent>
    </Dialog>
  )
}

function FailureTestForm({ kind, nodeId, name, edges }: { kind: WorkflowKind; nodeId: string; name: string; edges: FlowEdge[] }) {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const version = useWorkflowStore((s) => s.version)
  const catalog = useQuery<ScenarioCatalog>({ queryKey: ["scenarios", workflowId, version], queryFn: () => api.scenarios(workflowId) })
  const presets = useQuery({ queryKey: ["load-presets"], queryFn: api.loadPresets, staleTime: Infinity })
  // The Load test tab's target resolution (suggested targets of what runs now), on a draft of its own.
  const [draft, setDraft] = useState<LoadDraft>(INITIAL_DRAFT)
  const resolved = useWorkflowTarget(draft, setDraft)

  const choices = useMemo(() => (catalog.data ? failureChoices(catalog.data, nodeId, name, resolved.suggested) : []), [catalog.data, nodeId, name, resolved.suggested])
  const [picked, setPicked] = useState<FailureChoice["id"] | null>(null)
  const choice = choices.find((c) => c.id === picked && c.option) ?? choices.find((c) => c.option)
  const option = choice?.option

  // Traffic that goes through the node, ranked for this fault; picked once per fault.
  const ranked = useMemo(() => (option ? rankTargets(resolved.suggested, edges, nodeId, option.id) : []), [option, resolved.suggested, edges, nodeId])
  const rankedFor = useRef<string | null>(null)
  useEffect(() => {
    if (!option || ranked.length === 0) return
    const keys = ranked.map(targetKey)
    if (rankedFor.current === option.id && draft.suggestedKey && keys.includes(draft.suggestedKey)) return
    rankedFor.current = option.id
    setDraft((d) => ({ ...d, source: "suggested", suggestedKey: keys[0] }))
  }, [option, ranked, draft.suggestedKey])

  const shapes = useMemo(() => (presets.data ?? []).filter((p) => p.preset !== "autoscale" || kind === "kubernetes"), [presets.data, kind])
  const [presetId, setPresetId] = useState<LoadProfile["preset"] | null>(null)
  const fitting = option ? shapes.filter((p) => presetFits(option, p)) : []
  const profile = fitting.find((p) => p.preset === presetId) ?? fitting.find((p) => p.preset === "load") ?? fitting[0] ?? null

  // Another path of the suggested target (per target: picking another one starts from its own path).
  const [pathOf, setPathOf] = useState<{ key: string | null; path: string } | null>(null)
  const path = pathOf && pathOf.key === draft.suggestedKey ? pathOf.path : null
  const target = resolved.target && path !== null ? withPath(resolved.target, path) : resolved.target

  const [params, setParams] = useState<Record<string, string>>({})
  const base = option && profile ? initialSpec(option, profile, nodeId) : null
  const spec: ScenarioSpec | null = base && { ...base, params: { ...base.params, ...params } }
  const problem = scenarioProblem(spec, profile)
  const [starting, setStarting] = useState(false)

  const blocker = !catalog.data
    ? null
    : catalog.data.kind === ""
      ? "Failure tests run on Docker and Kubernetes workflows."
      : !option
        ? `No failure applies to ${name}.`
        : !resolved.loading && resolved.suggested.length === 0
          ? "Nothing is running to send traffic to: deploy the workflow first."
          : !target
            ? "Choose the traffic to send."
            : problem

  const start = async () => {
    if (!spec || !profile || !target || !option) return
    setStarting(true)
    try {
      const body: LoadTestSpec = {
        name: `${option.title} · ${name}`,
        target,
        profile: structuredClone(profile),
        thresholds: DEFAULT_THRESHOLDS,
        timeoutMs: INITIAL_DRAFT.timeoutMs,
        scenario: spec,
      }
      const run = await api.startLoadTest(workflowId, body)
      const s = useWorkflowStore.getState()
      if (!s.loadTests.some((r) => r.id === run.id)) useWorkflowStore.setState({ loadTests: [run, ...s.loadTests] })
      close()
    } catch (err) {
      toast.error("Could not start the failure test", { description: errorMessage(err) })
    } finally {
      setStarting(false)
    }
  }

  const total = profile ? totalDuration(profile.stages) : 0
  const instant = option ? instantScenario(option.id) : false
  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <FlameIcon className="size-4 text-muted-foreground" />
          Test a failure on <span className="font-mono">{name}</span>
        </DialogTitle>
        <DialogDescription>
          Nimbo breaks it for real while sending traffic, shows the impact on the canvas, then puts it back and grades how
          the app coped.
        </DialogDescription>
      </DialogHeader>

      {!catalog.data ? (
        <div className="flex justify-center py-6">
          <Spinner />
        </div>
      ) : (
        <div className="flex max-h-[60vh] flex-col gap-3 overflow-y-auto pr-1">
          <div role="radiogroup" aria-label="Failure" className="flex flex-col gap-1.5">
            {choices.map((c) => {
              const selected = c.id === choice?.id && !!c.option
              return (
                <button
                  key={c.id}
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  disabled={!c.option}
                  data-testid={`failure-choice-${c.id}`}
                  onClick={() => setPicked(c.id)}
                  className={cn(
                    "flex flex-col gap-0.5 rounded-lg border px-2.5 py-2 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                    c.option ? "hover:bg-muted/50" : "cursor-not-allowed opacity-60",
                    selected && "border-primary bg-primary/5",
                  )}
                >
                  <span className="text-xs font-semibold">{c.title}</span>
                  <span className="text-[11px] text-pretty text-muted-foreground">
                    {c.option ? (selected ? faultAction(c.id, kind, name) : c.option.description) : c.unavailable}
                  </span>
                </button>
              )
            })}
          </div>

          {option && (
            <div className="flex flex-col gap-2 rounded-lg border p-2.5">
              <p className="flex items-start gap-1.5 text-[11px] text-pretty">
                <InfoIcon className="mt-px size-3 shrink-0 text-muted-foreground" />
                <span>
                  <span className="font-medium">What to expect:</span> {faultExpect(option.id, kind, name)}
                </span>
              </p>
              <div className="grid grid-cols-2 gap-2">
                <div className="flex flex-col gap-1">
                  <Label className="text-[11px]">Traffic to</Label>
                  <Select value={draft.suggestedKey ?? undefined} onValueChange={(key) => setDraft((d) => ({ ...d, source: "suggested", suggestedKey: key }))}>
                    <SelectTrigger size="sm" className="h-7 w-full text-xs" aria-label="Traffic target" data-testid="failure-traffic-target">
                      <SelectValue placeholder={resolved.loading ? "Loading…" : "Nothing running"} />
                    </SelectTrigger>
                    <SelectContent>
                      {ranked.map((t) => (
                        <SelectItem key={targetKey(t)} value={targetKey(t)} className="text-xs">
                          <span className="font-mono">{t.http ? new URL(t.http.url).origin : targetDetail(t)}</span>
                          {t.label.includes(" · ") && <span className="text-muted-foreground"> · {t.label.slice(t.label.indexOf(" · ") + 3)}</span>}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex flex-col gap-1">
                  <Label className="text-[11px]">Path</Label>
                  <Input
                    className="h-7 font-mono text-xs"
                    aria-label="Path"
                    data-testid="failure-traffic-path"
                    disabled={!resolved.target?.http}
                    value={path ?? (resolved.target ? targetPath(resolved.target) : "")}
                    onChange={(e) => setPathOf({ key: draft.suggestedKey, path: e.target.value })}
                  />
                </div>
                {option.id !== "kill_replica" && option.id !== "traffic_spike" && (
                  <p className="col-span-2 -mt-1 text-[11px] text-pretty text-muted-foreground">
                    Send it to a path that uses {name} (one that reads or writes its data): requests that never reach it can&apos;t
                    show the failure.
                  </p>
                )}
                <div className="flex flex-col gap-1">
                  <Label className="text-[11px]">Traffic shape</Label>
                  <Select value={profile?.preset} onValueChange={(v) => setPresetId(v as LoadProfile["preset"])}>
                    <SelectTrigger size="sm" className="h-7 w-full text-xs" aria-label="Traffic shape" data-testid="failure-traffic-shape">
                      <SelectValue placeholder="No shape fits" />
                    </SelectTrigger>
                    <SelectContent>
                      {shapes.map((p) => (
                        <SelectItem key={p.preset} value={p.preset} disabled={!presetFits(option, p)} className="text-xs">
                          {p.label ?? p.preset} · {formatDuration(totalDuration(p.stages) * 1000)}
                          {!presetFits(option, p) && " (too short)"}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                {(option.params ?? []).map((p) => (
                  <div key={p.name} className="flex flex-col gap-1">
                    <Label className="text-[11px]">
                      {p.label}
                      {p.unit && ` (${p.unit})`}
                    </Label>
                    <Input
                      type="number"
                      min={p.min}
                      max={p.max}
                      step="any"
                      className="h-7 text-xs"
                      aria-label={p.label}
                      value={params[p.name] ?? String(p.default)}
                      onChange={(e) => setParams((cur) => ({ ...cur, [p.name]: e.target.value }))}
                    />
                  </div>
                ))}
              </div>
              {spec && profile && (
                <>
                  <p className="text-[11px] text-pretty text-muted-foreground" data-testid="failure-timing">
                    {profile.label ?? profile.preset} for {formatDuration(total * 1000)}: normal traffic first, the failure at {spec.atSec} s,{" "}
                    {instant ? `then ${spec.durationSec} s watching its effect` : `lasting ${spec.durationSec} s before Nimbo reverts it`}, then{" "}
                    {total - spec.atSec - spec.durationSec} s of recovery.
                  </p>
                  <WindowBar total={total} atSec={spec.atSec} durationSec={spec.durationSec} id={spec.id} />
                </>
              )}
            </div>
          )}

          <p className="flex items-start gap-1.5 text-[11px] text-pretty text-muted-foreground">
            <ShieldCheckIcon className="mt-px size-3 shrink-0" />
            Only this workflow's local {kind === "kubernetes" ? "cluster" : "containers"} are touched. Stop the test from the canvas at
            any time: the failure is reverted at once (and on the next start if Nimbo quits meanwhile).
          </p>
        </div>
      )}

      <DialogFooter className="items-center">
        {blocker && (
          <span className="mr-auto text-[11px] text-muted-foreground" data-testid="failure-blocker">
            {blocker}
          </span>
        )}
        <Button variant="outline" onClick={close}>
          Cancel
        </Button>
        <Button onClick={() => void start()} disabled={!!blocker || !spec || starting} data-testid="failure-start">
          {starting ? <Spinner /> : <PlayIcon />}
          Start failure test
        </Button>
      </DialogFooter>
    </>
  )
}

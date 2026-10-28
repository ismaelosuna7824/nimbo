import { useEffect, useRef } from "react"
import { useQuery } from "@tanstack/react-query"
import { Flame, Info } from "lucide-react"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api/client"
import type { LoadProfile, ScenarioCatalog, ScenarioId, ScenarioSpec } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import type { SetDraft } from "./configure"
import type { LoadDraft } from "./draft"
import { totalDuration } from "./metrics"
import { instantScenario } from "./scenario-result"
import { BASELINE_SEC, defaultTiming, initialSpec, MIN_AT_SEC, RECOVERY_SEC, scenarioProblem } from "./scenario-timing"

/**
 * Optional incident for a docker or Kubernetes workflow's load test: which fault, on which service or workload,
 * when and for how long, with a plain explanation and the fault window drawn over the test's length.
 */
export function ScenarioStep({ draft, setDraft, profile }: { draft: LoadDraft; setDraft: SetDraft; profile: LoadProfile | null }) {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const version = useWorkflowStore((s) => s.version)
  const catalog = useQuery<ScenarioCatalog>({
    queryKey: ["scenarios", workflowId, version],
    queryFn: () => api.scenarios(workflowId!),
    enabled: !!workflowId,
  })
  const spec = draft.scenario
  const options = catalog.data?.scenarios ?? []
  const opt = options.find((o) => o.id === spec?.id)
  const total = profile ? totalDuration(profile.stages) : 0

  // A scenario requested from elsewhere (Readiness) arrives untimed: apply defaults once the catalog is known.
  useEffect(() => {
    if (!spec || spec.atSec > 0 || !opt || !profile) return
    setDraft((d) => ({ ...d, scenario: initialSpec(opt, profile, spec.target) }))
  }, [spec, opt, profile, setDraft])

  // Another traffic shape (a different length) moves the incident to that length's default timing.
  const lastTotal = useRef(total)
  useEffect(() => {
    const changed = lastTotal.current !== total
    lastTotal.current = total
    if (!changed || total <= 0 || !opt) return
    setDraft((d) => (d.scenario && d.scenario.atSec > 0 ? { ...d, scenario: { ...d.scenario, ...defaultTiming(opt, total) } } : d))
  }, [total, opt, setDraft])

  if (!catalog.data || catalog.data.kind === "") return null

  const set = (patch: Partial<ScenarioSpec>) => setDraft((d) => (d.scenario ? { ...d, scenario: { ...d.scenario, ...patch } } : d))
  const pick = (id: string) => {
    const o = options.find((x) => x.id === id)
    setDraft((d) => ({ ...d, scenario: o ? initialSpec(o, profile) : null }))
  }
  const problem = scenarioProblem(spec, profile)
  const instant = spec ? instantScenario(spec.id) : false

  return (
    <div className="mt-3 rounded-lg border p-2" data-testid="loadtest-scenario-step">
      <div className="mb-1.5 flex items-center gap-1.5">
        <Flame className="size-3.5 text-muted-foreground" />
        <h3 className="text-xs font-semibold">Incident (optional)</h3>
        <span className="text-[11px] text-muted-foreground">Break something for real mid-test; Nimbo always puts it back.</span>
      </div>
      <Select value={spec?.id ?? "none"} onValueChange={pick}>
        <SelectTrigger size="sm" className="h-7 w-full text-xs" aria-label="Incident scenario" data-testid="loadtest-scenario-select">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="none" className="text-xs">
            No incident
          </SelectItem>
          {options.map((o) => (
            <SelectItem key={o.id} value={o.id} className="text-xs">
              {o.title}
            </SelectItem>
          ))}
          {catalog.data.unavailable.length > 0 && (
            <SelectGroup>
              <SelectLabel className="text-[10px]">Not available here</SelectLabel>
              {catalog.data.unavailable.map((u) => (
                <SelectItem key={u.id} value={u.id} disabled className="text-xs">
                  {u.title} — {u.reason}
                </SelectItem>
              ))}
            </SelectGroup>
          )}
        </SelectContent>
      </Select>

      {spec && opt && (
        <div className="mt-2 flex flex-col gap-2">
          <p className="text-[11px] text-pretty text-muted-foreground">{opt.description}</p>
          {opt.needsTarget && (
            <div className="flex flex-col gap-1">
              <Label className="text-[11px]">{spec.id === "kill_replica" ? "Service to crash" : spec.id === "cache_flush" ? "Cache" : "Dependency"}</Label>
              <Select value={spec.target} onValueChange={(target) => set({ target })}>
                <SelectTrigger size="sm" className="h-7 w-full text-xs" aria-label="Scenario target" data-testid="loadtest-scenario-target">
                  <SelectValue placeholder="Choose…" />
                </SelectTrigger>
                <SelectContent>
                  {opt.targets.map((t) => (
                    <SelectItem key={t.id} value={t.id} className="text-xs">
                      <span className="font-mono">{t.name}</span>
                      <span className="text-muted-foreground"> · {t.detail}</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          <div className="grid grid-cols-2 gap-2">
            <div className="flex flex-col gap-1">
              <Label className="text-[11px]" title="Seconds after the traffic starts">
                {instant ? "At (s)" : "Start at (s)"}
              </Label>
              <Input
                type="number"
                min={MIN_AT_SEC}
                className="h-7 text-xs"
                value={spec.atSec}
                aria-label="Incident start (seconds)"
                onChange={(e) => set({ atSec: Math.max(0, Math.round(Number(e.target.value) || 0)) })}
              />
            </div>
            <div className="flex flex-col gap-1">
              <Label className="text-[11px]" title={instant ? "How long to watch the effect" : "How long the fault lasts before it is reverted"}>
                {instant ? "Watch for (s)" : "Lasts (s)"}
              </Label>
              <Input
                type="number"
                min={1}
                className="h-7 text-xs"
                value={spec.durationSec}
                aria-label="Incident duration (seconds)"
                onChange={(e) => set({ durationSec: Math.max(0, Math.round(Number(e.target.value) || 0)) })}
              />
            </div>
            {(opt.params ?? []).map((p) => (
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
                  value={spec.params?.[p.name] ?? String(p.default)}
                  aria-label={p.label}
                  onChange={(e) => set({ params: { ...spec.params, [p.name]: e.target.value } })}
                />
              </div>
            ))}
          </div>
          {total > 0 && <WindowBar total={total} atSec={spec.atSec} durationSec={spec.durationSec} id={spec.id} />}
          {problem ? (
            <p className="flex items-start gap-1.5 text-[11px] text-destructive" data-testid="loadtest-scenario-problem">
              <Info className="mt-px size-3 shrink-0" /> {problem}
            </p>
          ) : (
            total > 0 &&
            (spec.atSec < BASELINE_SEC || total - spec.atSec - spec.durationSec < RECOVERY_SEC) && (
              <p className="flex items-start gap-1.5 text-[11px] text-muted-foreground">
                <Info className="mt-px size-3 shrink-0" /> Leave about {BASELINE_SEC} s before and {RECOVERY_SEC} s after the incident
                so the verdict can compare with normal traffic and measure the recovery.
              </p>
            )
          )}
        </div>
      )}
    </div>
  )
}

/** The test's length with the incident window highlighted. */
export function WindowBar({ total, atSec, durationSec, id }: { total: number; atSec: number; durationSec: number; id: ScenarioId }) {
  const left = Math.min(100, (atSec / total) * 100)
  const width = Math.max(0.8, Math.min(100 - left, (durationSec / total) * 100))
  return (
    <div className="flex flex-col gap-0.5" aria-hidden>
      <div className="relative h-2.5 rounded-sm bg-muted">
        <div
          className={instantScenario(id) ? "absolute inset-y-0 bg-destructive/25" : "absolute inset-y-0 bg-destructive/50"}
          style={{ left: `${left}%`, width: `${width}%` }}
        />
        <div className="absolute inset-y-0 w-0.5 bg-destructive" style={{ left: `${left}%` }} />
      </div>
      <div className="flex justify-between text-[10px] text-muted-foreground tabular-nums">
        <span>0 s</span>
        <span>
          {atSec}–{atSec + durationSec} s
        </span>
        <span>{total} s</span>
      </div>
    </div>
  )
}

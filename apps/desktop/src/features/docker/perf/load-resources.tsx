import { CircleAlert, CircleCheck, Cpu, TriangleAlert } from "lucide-react"
import type { LoadResources } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { UsageMeter } from "./components"
import { cpuLimitLabel, formatBytes, formatDuration, formatPct, memLimitLabel, resourcesTone, TONE_CLASS, TONE_SURFACE, verdictTone, type Tone } from "./model"

const TONE_ICON: Record<Tone, typeof CircleCheck> = { ok: CircleCheck, warning: TriangleAlert, error: CircleAlert }

/** The load test ↔ container headline: which service limited the run and what to do about it. */
export function LoadVerdictBanner({ resources }: { resources: LoadResources }) {
  const tone = resourcesTone(resources)
  const Icon = TONE_ICON[tone]
  return (
    <div data-testid="loadtest-verdict" data-tone={tone} className={cn("flex gap-2 rounded-lg border px-3 py-2", TONE_SURFACE[tone])}>
      <Icon className={cn("mt-0.5 size-4 shrink-0", TONE_CLASS[tone])} />
      <div className="min-w-0">
        <p className="text-sm font-medium">{resources.verdict}</p>
        {resources.note && <p className="text-xs text-muted-foreground">{resources.note}</p>}
      </div>
    </div>
  )
}

/** Per-service verdicts and peaks (CPU and memory against limits, startup, restarts) of a load test. */
export function LoadResourcesDetail({ resources }: { resources: LoadResources }) {
  return (
    <div className="grid grid-cols-1 gap-2 lg:grid-cols-2">
      <div className="min-w-0 rounded-lg border p-2">
        <div className="mb-1 flex items-center gap-1 text-[11px] font-medium text-muted-foreground">
          <Cpu className="size-3" /> Containers under load
        </div>
        <ul className="flex flex-col gap-1" data-testid="loadtest-verdicts">
          {resources.verdicts.map((v, i) => {
            const tone = verdictTone(v)
            const Icon = TONE_ICON[tone]
            return (
              <li key={`${v.service}:${i}`} data-service={v.service} data-kind={v.kind} className="flex gap-1.5 text-xs">
                <Icon className={cn("mt-0.5 size-3.5 shrink-0", TONE_CLASS[tone])} />
                <span>
                  <span className="font-medium">{v.service}</span> <span className="text-muted-foreground">·</span> {v.message}
                </span>
              </li>
            )
          })}
        </ul>
      </div>
      <div className="min-w-0 rounded-lg border">
        <table className="w-full text-xs" data-testid="loadtest-peaks">
          <thead className="text-left text-[11px] text-muted-foreground">
            <tr className="border-b">
              <th className="px-2 py-1.5 font-medium">Service</th>
              <th className="px-2 py-1.5 font-medium">Peak CPU</th>
              <th className="px-2 py-1.5 font-medium">Peak memory</th>
              <th className="px-2 py-1.5 font-medium">Startup</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {resources.services.map((s) => (
              <tr key={s.service} data-service={s.service}>
                <td className="px-2 py-1">
                  <span className={cn(s.target && "font-medium")}>{s.service}</span>
                  {s.target && <span className="ml-1 rounded bg-primary/10 px-1 text-[9px] text-primary uppercase">target</span>}
                  {s.oomKilled && <span className="ml-1 text-[10px] text-destructive">OOM</span>}
                  {(s.restarts ?? 0) > 0 && (
                    <span className="ml-1 text-[10px] text-amber-600 dark:text-amber-400">{s.restarts} restarts</span>
                  )}
                </td>
                <td className="w-40 px-2 py-1">
                  <div className="flex items-baseline gap-1 tabular-nums">
                    <span>{formatPct(s.peakCpuOfLimit * 100)}</span>
                    <span className="truncate text-[10px] text-muted-foreground">of {cpuLimitLabel(s.cpuLimit, s.cpuLimited)}</span>
                  </div>
                  <UsageMeter pct={s.peakCpuOfLimit * 100} />
                </td>
                <td className="w-40 px-2 py-1">
                  <div className="flex items-baseline gap-1 tabular-nums">
                    <span>{formatBytes(s.peakMemBytes)}</span>
                    <span className="truncate text-[10px] text-muted-foreground">/ {memLimitLabel(s.memLimit, s.memLimited)}</span>
                  </div>
                  <UsageMeter pct={s.peakMemOfLimit * 100} />
                </td>
                <td className="px-2 py-1 tabular-nums">{s.startupMs !== undefined ? formatDuration(s.startupMs) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

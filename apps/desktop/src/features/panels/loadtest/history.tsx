import { useContext, useMemo } from "react"
import { useQueries } from "@tanstack/react-query"
import { GitCompareArrows } from "lucide-react"
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"
import { Checkbox } from "@/components/ui/checkbox"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api/client"
import type { LoadTestRun } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { LoadRunsContext } from "./runs-context"
import { formatDuration, formatMs, formatNumber, formatPercent, formatRelative } from "../lib/format"
import { DENSE_TABLE } from "../variables/blocks-editors"
import { ChartCard } from "./charts"
import { compactTick, computeKpis, EXPLAIN, runDurationSec, SERIES_COLORS, type Kpis } from "./metrics"
import { Verdict } from "./run-results"

interface HistoryProps {
  compare: string[]
  setCompare: (ids: string[]) => void
  onOpen: (runId: string) => void
}

export function History({ compare, setCompare, onOpen }: HistoryProps) {
  const runs = useContext(LoadRunsContext).runs

  if (runs.length === 0) {
    return <p className="p-4 text-xs text-muted-foreground">No load tests yet. Configure one and press Start.</p>
  }

  const toggle = (id: string, on: boolean) =>
    setCompare(on ? [...compare.filter((x) => x !== id), id].slice(-2) : compare.filter((x) => x !== id))

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b px-2 text-xs text-muted-foreground">
        Select two runs to compare them.
        {compare.length > 0 && (
          <Button size="xs" variant="ghost" className="ml-auto" onClick={() => setCompare([])}>
            Clear selection
          </Button>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {compare.length === 2 && <Compare ids={compare as [string, string]} />}
        <Table className={DENSE_TABLE}>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Test</TableHead>
              <TableHead>Result</TableHead>
              <TableHead>Started</TableHead>
              <TableHead className="text-right">Duration</TableHead>
              <TableHead className="text-right">Requests</TableHead>
              <TableHead className="text-right">Avg req/s</TableHead>
              <TableHead className="text-right">p95</TableHead>
              <TableHead className="text-right">Errors</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {runs.map((r) => {
              const idx = compare.indexOf(r.id)
              return (
                <TableRow key={r.id} className="cursor-pointer" onClick={() => onOpen(r.id)}>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <div className="flex items-center gap-1">
                      <Checkbox
                        checked={idx !== -1}
                        onCheckedChange={(v) => toggle(r.id, v === true)}
                        aria-label={`Compare ${r.spec.name}`}
                      />
                      {idx !== -1 && <span className="font-mono text-[10px] font-semibold">{idx === 0 ? "A" : "B"}</span>}
                    </div>
                  </TableCell>
                  <TableCell className="max-w-64 truncate" title={r.spec.name}>
                    {r.external && (
                      <Badge variant="outline" className="mr-1 h-4 border-amber-500/40 px-1 text-[10px] text-amber-700 dark:text-amber-400">
                        External
                      </Badge>
                    )}
                    {r.spec.name}
                  </TableCell>
                  <TableCell>
                    <Verdict run={r} />
                  </TableCell>
                  <TableCell className="text-muted-foreground">{formatRelative(r.startedAt)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatDuration(runDurationSec(r) * 1000)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatNumber(r.summary?.requests)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatNumber(r.summary?.rps, 1)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatMs(r.summary?.latency.p95)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatPercent(r.summary?.errorRate)}</TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

const DELTA_ROWS: { key: keyof Kpis; label: string; format: (v: number | undefined) => string; lowerIsBetter: boolean; hint: string }[] = [
  { key: "p95", label: "p95 latency", format: formatMs, lowerIsBetter: true, hint: EXPLAIN.speed },
  { key: "p99", label: "p99 latency", format: formatMs, lowerIsBetter: true, hint: EXPLAIN.speed },
  { key: "avgRps", label: "Average req/s", format: (v) => formatNumber(v, 1), lowerIsBetter: false, hint: EXPLAIN.rps },
  { key: "peakRps", label: "Peak req/s", format: (v) => formatNumber(v, 1), lowerIsBetter: false, hint: EXPLAIN.rps },
  { key: "errorRate", label: "Error rate", format: (v) => formatPercent(v), lowerIsBetter: true, hint: EXPLAIN.errors },
  { key: "stability", label: "Variation (stability)", format: (v) => formatPercent(v, 0), lowerIsBetter: true, hint: EXPLAIN.stability },
  { key: "breakingPoint", label: "Breaking point", format: (v) => (v === undefined ? "not reached" : formatNumber(v)), lowerIsBetter: false, hint: EXPLAIN.capacity },
]

function Compare({ ids }: { ids: [string, string] }) {
  const results = useQueries({
    queries: ids.map((id) => ({ queryKey: ["loadtest", id, "compare"], queryFn: () => api.loadTest(id) })),
  })
  const [a, b] = results.map((r) => r.data) as [LoadTestRun | undefined, LoadTestRun | undefined]

  const rows = useMemo(() => {
    if (!a || !b) return []
    const byT = new Map<number, { t: number; aP95?: number; bP95?: number; aRps?: number; bRps?: number }>()
    for (const s of a.timeline ?? []) byT.set(s.t, { t: s.t, aP95: s.p95, aRps: s.rps })
    for (const s of b.timeline ?? []) byT.set(s.t, { ...(byT.get(s.t) ?? { t: s.t }), bP95: s.p95, bRps: s.rps })
    return [...byT.values()].sort((x, y) => x.t - y.t)
  }, [a, b])

  if (!a || !b) {
    return (
      <div className="flex h-24 items-center justify-center">
        <Spinner />
      </div>
    )
  }

  const ka = computeKpis(a.timeline ?? [], a.summary)
  const kb = computeKpis(b.timeline ?? [], b.summary)
  const p95Config: ChartConfig = {
    aP95: { label: "A p95", theme: SERIES_COLORS.a },
    bP95: { label: "B p95", theme: SERIES_COLORS.b },
  }
  const rpsConfig: ChartConfig = {
    aRps: { label: "A req/s", theme: SERIES_COLORS.a },
    bRps: { label: "B req/s", theme: SERIES_COLORS.b },
  }
  const axisX = (
    <XAxis dataKey="t" type="number" domain={["dataMin", "dataMax"]} tickLine={false} axisLine={false} minTickGap={24} tickFormatter={(t: number) => `${Math.round(t)}s`} />
  )

  return (
    <div className="grid grid-cols-1 gap-2 border-b p-2 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(16rem,0.8fr)]">
      <ChartCard title="p95 latency over time (ms)" hint={EXPLAIN.speed}>
        <ChartContainer config={p95Config} className="aspect-auto h-44 w-full">
          <LineChart data={rows} margin={{ left: 0, right: 8, top: 4 }}>
            <CartesianGrid vertical={false} />
            {axisX}
            <YAxis tickLine={false} axisLine={false} width={40} tickFormatter={compactTick} />
            <ChartTooltip content={<ChartTooltipContent />} />
            <ChartLegend content={<ChartLegendContent />} />
            <Line dataKey="aP95" stroke="var(--color-aP95)" dot={false} connectNulls isAnimationActive={false} strokeWidth={2} />
            <Line dataKey="bP95" stroke="var(--color-bP95)" dot={false} connectNulls isAnimationActive={false} strokeWidth={2} />
          </LineChart>
        </ChartContainer>
      </ChartCard>
      <ChartCard title="Throughput over time (req/s)" hint={EXPLAIN.rps}>
        <ChartContainer config={rpsConfig} className="aspect-auto h-44 w-full">
          <LineChart data={rows} margin={{ left: 0, right: 8, top: 4 }}>
            <CartesianGrid vertical={false} />
            {axisX}
            <YAxis tickLine={false} axisLine={false} width={40} tickFormatter={compactTick} />
            <ChartTooltip content={<ChartTooltipContent />} />
            <ChartLegend content={<ChartLegendContent />} />
            <Line dataKey="aRps" stroke="var(--color-aRps)" dot={false} connectNulls isAnimationActive={false} strokeWidth={2} />
            <Line dataKey="bRps" stroke="var(--color-bRps)" dot={false} connectNulls isAnimationActive={false} strokeWidth={2} />
          </LineChart>
        </ChartContainer>
      </ChartCard>
      <div className="min-w-0 rounded-lg border">
        <div className="flex items-center gap-1 px-2 pt-2 text-[11px] font-medium text-muted-foreground">
          <GitCompareArrows className="size-3" /> B compared to A
        </div>
        <Table className={DENSE_TABLE}>
          <TableHeader>
            <TableRow>
              <TableHead>Metric</TableHead>
              <TableHead className="text-right">A</TableHead>
              <TableHead className="text-right">B</TableHead>
              <TableHead className="text-right">Δ</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {DELTA_ROWS.map((row) => {
              const va = ka[row.key] as number | undefined
              const vb = kb[row.key] as number | undefined
              const delta = va !== undefined && vb !== undefined && va !== 0 ? (vb - va) / Math.abs(va) : undefined
              const better = delta !== undefined && delta !== 0 ? (delta < 0) === row.lowerIsBetter : undefined
              return (
                <TableRow key={row.key} title={row.hint}>
                  <TableCell>{row.label}</TableCell>
                  <TableCell className="text-right tabular-nums">{row.format(va)}</TableCell>
                  <TableCell className="text-right tabular-nums">{row.format(vb)}</TableCell>
                  <TableCell
                    className={cn(
                      "text-right tabular-nums",
                      better === true && "text-emerald-600 dark:text-emerald-400",
                      better === false && "text-destructive",
                    )}
                  >
                    {delta === undefined ? "—" : `${delta > 0 ? "+" : ""}${(delta * 100).toFixed(0)}%`}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

import type { ReactNode } from "react"
import { Info } from "lucide-react"
import { Bar, BarChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"
import { useArcade } from "@/components/arcade/arcade-store"
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { LoadMode, LoadSample } from "@/lib/api/types"
import { compactTick, EXPLAIN, modeUnit, SERIES_COLORS } from "./metrics"


const xAxis = (
  <XAxis
    dataKey="t"
    type="number"
    domain={["dataMin", "dataMax"]}
    tickLine={false}
    axisLine={false}
    tickMargin={4}
    minTickGap={24}
    tickFormatter={(t: number) => `${Math.round(t)}s`}
  />
)

const secondsLabel = (_: unknown, payload: readonly { payload?: { t?: number } }[]) =>
  payload[0]?.payload?.t !== undefined ? `t = ${Math.round(payload[0].payload.t)}s` : ""

/** Arcade's chart marker: a crisp 4px square on every fifth sample, so lines read as pixel plots. */
function PixelDot({ cx, cy, stroke, index }: { cx?: number; cy?: number; stroke?: string; index?: number }) {
  if (cx === undefined || cy === undefined || (index ?? 0) % 5 !== 0) return <g />
  return <rect x={Math.round(cx) - 2} y={Math.round(cy) - 2} width={4} height={4} fill={stroke} shapeRendering="crispEdges" />
}

function ChartCard({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex min-h-0 min-w-0 flex-col rounded-lg border p-2">
      <div className="mb-1 flex items-center gap-1 text-[11px] font-medium text-muted-foreground">
        {title}
        {hint && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Info className="size-3 cursor-help" />
            </TooltipTrigger>
            <TooltipContent className="max-w-64">{hint}</TooltipContent>
          </Tooltip>
        )}
      </div>
      <div className="min-h-0 flex-1">{children}</div>
    </div>
  )
}

export function LoadCharts({ samples, mode }: { samples: LoadSample[]; mode: LoadMode }) {
  const unit = modeUnit(mode)
  // Arcade plots hard steps with pixel markers; the default keeps smooth curves.
  const arcade = useArcade()
  const curve = arcade ? "stepAfter" : "monotone"
  const dot = arcade ? PixelDot : false
  const throughput: ChartConfig = {
    target: { label: `Target (${unit})`, theme: SERIES_COLORS.target },
    rps: { label: "Achieved req/s", theme: SERIES_COLORS.rps },
  }
  const latency: ChartConfig = {
    p50: { label: "p50", theme: SERIES_COLORS.p50 },
    p95: { label: "p95", theme: SERIES_COLORS.p95 },
    p99: { label: "p99", theme: SERIES_COLORS.p99 },
  }
  const errors: ChartConfig = { errors: { label: "Errors / s", theme: SERIES_COLORS.errors } }

  return (
    <div className="grid h-full min-h-44 grid-cols-1 gap-2 md:grid-cols-3">
      <ChartCard title="Traffic: target vs achieved" hint={EXPLAIN.rps}>
        <ChartContainer config={throughput} className="aspect-auto h-full min-h-32 w-full">
          <LineChart data={samples} margin={{ left: 0, right: 8, top: 4, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            {xAxis}
            <YAxis tickLine={false} axisLine={false} width={40} tickFormatter={compactTick} />
            <ChartTooltip content={<ChartTooltipContent labelFormatter={secondsLabel} />} />
            <ChartLegend content={<ChartLegendContent />} />
            <Line dataKey="rps" type={curve} stroke="var(--color-rps)" strokeWidth={2} dot={dot} isAnimationActive={false} />
            <Line dataKey="target" type={arcade ? "stepAfter" : "linear"} stroke="var(--color-target)" strokeDasharray="4 3" dot={false} isAnimationActive={false} />
          </LineChart>
        </ChartContainer>
      </ChartCard>
      <ChartCard title="Response time (ms)" hint={EXPLAIN.speed}>
        <ChartContainer config={latency} className="aspect-auto h-full min-h-32 w-full">
          <LineChart data={samples} margin={{ left: 0, right: 8, top: 4, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            {xAxis}
            <YAxis tickLine={false} axisLine={false} width={40} tickFormatter={compactTick} />
            <ChartTooltip content={<ChartTooltipContent labelFormatter={secondsLabel} />} />
            <ChartLegend content={<ChartLegendContent />} />
            <Line dataKey="p50" type={curve} stroke="var(--color-p50)" strokeWidth={arcade ? 2 : undefined} dot={dot} isAnimationActive={false} />
            <Line dataKey="p95" type={curve} stroke="var(--color-p95)" strokeWidth={2} dot={dot} isAnimationActive={false} />
            <Line dataKey="p99" type={curve} stroke="var(--color-p99)" strokeWidth={arcade ? 2 : undefined} dot={dot} isAnimationActive={false} />
          </LineChart>
        </ChartContainer>
      </ChartCard>
      <ChartCard title="Errors per second" hint={EXPLAIN.errors}>
        <ChartContainer config={errors} className="aspect-auto h-full min-h-32 w-full">
          <BarChart data={samples} margin={{ left: 0, right: 8, top: 4, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            {xAxis}
            <YAxis tickLine={false} axisLine={false} width={40} tickFormatter={compactTick} allowDecimals={false} />
            <ChartTooltip content={<ChartTooltipContent labelFormatter={secondsLabel} />} />
            <Bar dataKey="errors" fill="var(--color-errors)" isAnimationActive={false} radius={arcade ? 0 : [2, 2, 0, 0]} />
          </BarChart>
        </ChartContainer>
      </ChartCard>
    </div>
  )
}

/** One timeline point of two compared runs (A and B). */
export interface CompareRow {
  t: number
  aP95?: number
  bP95?: number
  aRps?: number
  bRps?: number
}

const p95Config: ChartConfig = {
  aP95: { label: "A p95", theme: SERIES_COLORS.a },
  bP95: { label: "B p95", theme: SERIES_COLORS.b },
}
const rpsConfig: ChartConfig = {
  aRps: { label: "A req/s", theme: SERIES_COLORS.a },
  bRps: { label: "B req/s", theme: SERIES_COLORS.b },
}
const compareAxisX = (
  <XAxis dataKey="t" type="number" domain={["dataMin", "dataMax"]} tickLine={false} axisLine={false} minTickGap={24} tickFormatter={(t: number) => `${Math.round(t)}s`} />
)

/** p95 and throughput of two runs over time (the history comparison). */
export function CompareCharts({ rows }: { rows: CompareRow[] }) {
  const arcade = useArcade()
  const curve = arcade ? "stepAfter" : undefined
  const dot = arcade ? PixelDot : false
  return (
    <>
      <ChartCard title="p95 latency over time (ms)" hint={EXPLAIN.speed}>
        <ChartContainer config={p95Config} className="aspect-auto h-44 w-full">
          <LineChart data={rows} margin={{ left: 0, right: 8, top: 4 }}>
            <CartesianGrid vertical={false} />
            {compareAxisX}
            <YAxis tickLine={false} axisLine={false} width={40} tickFormatter={compactTick} />
            <ChartTooltip content={<ChartTooltipContent />} />
            <ChartLegend content={<ChartLegendContent />} />
            <Line dataKey="aP95" type={curve} stroke="var(--color-aP95)" dot={dot} connectNulls isAnimationActive={false} strokeWidth={2} />
            <Line dataKey="bP95" type={curve} stroke="var(--color-bP95)" dot={dot} connectNulls isAnimationActive={false} strokeWidth={2} />
          </LineChart>
        </ChartContainer>
      </ChartCard>
      <ChartCard title="Throughput over time (req/s)" hint={EXPLAIN.rps}>
        <ChartContainer config={rpsConfig} className="aspect-auto h-44 w-full">
          <LineChart data={rows} margin={{ left: 0, right: 8, top: 4 }}>
            <CartesianGrid vertical={false} />
            {compareAxisX}
            <YAxis tickLine={false} axisLine={false} width={40} tickFormatter={compactTick} />
            <ChartTooltip content={<ChartTooltipContent />} />
            <ChartLegend content={<ChartLegendContent />} />
            <Line dataKey="aRps" type={curve} stroke="var(--color-aRps)" dot={dot} connectNulls isAnimationActive={false} strokeWidth={2} />
            <Line dataKey="bRps" type={curve} stroke="var(--color-bRps)" dot={dot} connectNulls isAnimationActive={false} strokeWidth={2} />
          </LineChart>
        </ChartContainer>
      </ChartCard>
    </>
  )
}

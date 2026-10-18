import type { ReactNode } from "react"
import { Info } from "lucide-react"
import { Bar, BarChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"
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

export function ChartCard({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
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
            <Line dataKey="rps" type="monotone" stroke="var(--color-rps)" strokeWidth={2} dot={false} isAnimationActive={false} />
            <Line dataKey="target" type="linear" stroke="var(--color-target)" strokeDasharray="4 3" dot={false} isAnimationActive={false} />
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
            <Line dataKey="p50" type="monotone" stroke="var(--color-p50)" dot={false} isAnimationActive={false} />
            <Line dataKey="p95" type="monotone" stroke="var(--color-p95)" strokeWidth={2} dot={false} isAnimationActive={false} />
            <Line dataKey="p99" type="monotone" stroke="var(--color-p99)" dot={false} isAnimationActive={false} />
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
            <Bar dataKey="errors" fill="var(--color-errors)" isAnimationActive={false} radius={[2, 2, 0, 0]} />
          </BarChart>
        </ChartContainer>
      </ChartCard>
    </div>
  )
}

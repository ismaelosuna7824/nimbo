import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  getSmoothStepPath,
  Handle,
  Position,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
} from "@xyflow/react"
import { CircleAlertIcon, HammerIcon, NetworkIcon, PlugIcon, TriangleAlertIcon } from "lucide-react"
import { memo } from "react"
import { useArcade } from "@/components/arcade/arcade-store"
import { CARD_SELECTED, CARD_SUBTITLE, CARD_SURFACE, CARD_TITLE, edgeStyle, FRAME_SELECTED, HANDLE, ICON_TILE, StatusDot, type StatusTone } from "@/components/canvas-card"
import { sameCardProps } from "@/components/flow-items"
import { techIcon, techIconFor } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { ComposeNode, ComposeServiceStatus } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { openEndpoints } from "@/features/panels/endpoints/use-endpoints"
import { EdgeFlow, NodeLiveBadges } from "@/features/live/live-chips"
import { ServiceCardUsage, ServiceProblemBadge } from "../perf/components"
import { FRAME_HEADER, type CanvasEdge, type CardKind } from "./model"

export interface DockerNodeData extends Record<string, unknown> {
  kind: CardKind
  node?: ComposeNode
  stages?: ComposeNode[]
  status?: ComposeServiceStatus
  external?: boolean
  errors: number
  warnings: number
  /** Endpoints card address when the service publishes ports and runs. */
  endpoint?: string
  /** A network frame without services (drawn as a compact card). */
  empty?: boolean
}

export type DockerFlowNode = Node<DockerNodeData, "service" | "small" | "frame">

export interface DockerEdgeData extends Record<string, unknown> {
  kind: CanvasEdge["kind"]
  label?: string
  live: boolean
  hovered: boolean
}

export type DockerFlowEdge = Edge<DockerEdgeData, "docker">

/** A service's (or workload's) state as a status dot (other states read as stopped; absent shows none). */
export const STATE_TONE: Partial<Record<string, StatusTone>> = {
  running: "ok",
  starting: "progress",
  error: "error",
}

/** "running · healthy", "exited (1)", "starting"… for a card. */
export function stateText(s: ComposeServiceStatus | undefined): string | undefined {
  if (!s || s.state === "absent") return undefined
  const exited = s.containers.find((c) => c.state === "exited" && c.exitCode !== 0)
  if (exited) return `exited (${exited.exitCode})`
  return [s.state, s.health].filter(Boolean).join(" · ")
}

function imageLine(node: ComposeNode): string {
  const svc = node.service
  if (svc?.build) return `build ${svc.build.context}${svc.build.target ? ` (${svc.build.target})` : ""}`
  return svc?.image?.ref ?? "no image"
}

/** "8080→80", "443→443/udp", "5432" (container only, not published); `bound` = host port actually published. */
export function portLabel(p: NonNullable<ComposeNode["service"]>["ports"][number], bound?: number): string {
  const proto = p.protocol && p.protocol !== "tcp" ? `/${p.protocol}` : ""
  const host = bound !== undefined ? String(bound) : p.host
  return host ? `${host}→${p.container}${proto}` : `${p.container}${proto}`
}

/** Host port a running container publishes for a compose port (differs from the file when Nimbo moved a busy one). */
function boundPort(p: NonNullable<ComposeNode["service"]>["ports"][number], status: ComposeServiceStatus | undefined): number | undefined {
  const proto = p.protocol || "tcp"
  const container = Number(p.container)
  return status?.containers.flatMap((c) => c.ports).find((x) => x.container === container && x.protocol === proto && x.host > 0)?.host
}

export function Badges({ errors, warnings }: { errors: number; warnings: number }) {
  return (
    <>
      {errors > 0 && (
        <span className="inline-flex h-4.5 items-center gap-0.5 rounded-full bg-destructive/12 px-1.5 text-[10px] font-medium text-destructive">
          <CircleAlertIcon className="size-2.5" />
          {errors}
        </span>
      )}
      {warnings > 0 && (
        <span className="inline-flex h-4.5 items-center gap-0.5 rounded-full bg-amber-500/12 px-1.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
          <TriangleAlertIcon className="size-2.5" />
          {warnings}
        </span>
      )}
    </>
  )
}

export const chip = "inline-flex h-4.5 shrink-0 items-center rounded-full px-1.5 font-mono text-[10px] font-medium"

export const ServiceNode = memo(function ServiceNode({ id, data, selected }: NodeProps<DockerFlowNode>) {
  const node = data.node!
  const svc = node.service
  const icon = techIconFor({ image: svc?.image?.ref, baseImages: svc?.baseImages, stack: svc?.stack, kind: "service" })
  const state = data.status?.state ?? "absent"
  const text = stateText(data.status)
  const ports = svc?.ports ?? []
  const replicas = svc?.replicas ?? 1
  const stages = data.stages ?? []
  const tone = STATE_TONE[state] ?? "off"
  return (
    <div
      data-testid="docker-service"
      data-name={node.name}
      data-state={state}
      data-health={data.status?.health ?? ""}
      className={cn(
        "group/node relative flex h-full w-full flex-col gap-1.5 px-3 pt-2.5 pb-2",
        CARD_SURFACE,
        node.inactive && "border-dashed opacity-60",
        selected && CARD_SELECTED,
      )}
    >
      <NodeLiveBadges id={id} />
      <Handle type="target" position={Position.Left} className={HANDLE} />
      <div className="flex min-w-0 items-center gap-2.5">
        <span className={cn(ICON_TILE, "bg-sky-500/8 ring-1 ring-sky-500/15 ring-inset")}>
          <TechIcon icon={icon} size={20} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className={CARD_TITLE}>{node.name}</span>
            {svc?.build && <HammerIcon className="size-3 shrink-0 text-muted-foreground" aria-label="Built from a Dockerfile" />}
            {node.inactive && <span className="rounded bg-muted px-1 text-[9px] font-semibold tracking-wider text-muted-foreground uppercase">profile</span>}
          </div>
          <div className={cn(CARD_SUBTITLE, "mt-0.5")} title={imageLine(node)}>
            {imageLine(node)}
          </div>
        </div>
        {text && (
          <StatusDot tone={tone} label={text} className="self-start pt-0.5">
            {data.status?.health ?? state}
          </StatusDot>
        )}
      </div>
      {stages.length > 1 && (
        <div className="flex min-w-0 items-center gap-1 overflow-hidden text-[10px] text-muted-foreground" aria-label="Build stages">
          {stages.map((s, i) => (
            <span key={s.id} className="flex min-w-0 items-center gap-1">
              {i > 0 && <span aria-hidden>›</span>}
              <TechIcon icon={techIconFor({ baseImages: [s.stage?.from ?? ""], kind: "build-stage" })} size={12} />
              <span className={cn("truncate font-mono", s.stage?.final && "font-semibold text-foreground")}>{s.stage?.name ?? `#${(s.stage?.index ?? i) + 1}`}</span>
            </span>
          ))}
        </div>
      )}
      <ServiceCardUsage name={node.name} status={data.status} />
      <div className="mt-auto flex min-w-0 items-center gap-1 overflow-hidden">
        {ports.slice(0, 3).map((p, i) => {
          const bound = boundPort(p, data.status)
          const moved = bound !== undefined && !!p.host && String(bound) !== p.host
          return (
            <span
              key={i}
              data-slot="port"
              title={moved ? `Port ${p.host} was busy: published on ${bound} instead (your compose file is unchanged)` : undefined}
              className={cn(
                chip,
                moved ? "bg-amber-500/12 text-amber-700 dark:text-amber-300" : p.host ? "bg-sky-500/12 text-sky-700 dark:text-sky-300" : "bg-muted text-muted-foreground",
              )}
            >
              {portLabel(p, bound)}
            </span>
          )
        })}
        {ports.length > 3 && <span className={cn(chip, "bg-muted text-muted-foreground")}>+{ports.length - 3}</span>}
        {replicas > 1 && <span className={cn(chip, "bg-violet-500/12 text-violet-700 dark:text-violet-300")}>×{replicas}</span>}
        <ServiceProblemBadge name={node.name} />
        <Badges errors={data.errors} warnings={data.warnings} />
        {data.endpoint && (
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                type="button"
                aria-label="Use it: URLs, snippets and Try it"
                className="nodrag ml-auto inline-flex size-5 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary transition-colors hover:bg-primary/20"
                onClick={(e) => {
                  e.stopPropagation()
                  openEndpoints(data.endpoint)
                }}
                onDoubleClick={(e) => e.stopPropagation()}
              >
                <PlugIcon className="size-3" />
              </button>
            </TooltipTrigger>
            <TooltipContent>Use it: URLs, code snippets and Try it</TooltipContent>
          </Tooltip>
        )}
      </div>
      <Handle type="source" position={Position.Right} className={HANDLE} />
    </div>
  )
}, sameCardProps)

const SMALL_SUBTITLE: Record<string, string> = {
  volume: "named volume",
  secret: "secret",
  config: "config",
  external: "external",
}

export const SmallNode = memo(function SmallNode({ data, selected }: NodeProps<DockerFlowNode>) {
  const node = data.node!
  const kind = node.kind === "external" ? (node.externalKind ?? "external") : node.kind
  const icon = techIconFor({ kind })
  const subtitle = node.kind === "external" ? `external ${node.externalKind ?? ""}`.trim() : SMALL_SUBTITLE[node.kind]
  return (
    <div
      data-testid={`docker-${node.kind}`}
      data-name={node.name}
      className={cn(
        "relative flex h-full w-full items-center gap-2 px-2.5",
        CARD_SURFACE,
        "rounded-lg shadow-xs",
        node.kind === "external" && "border-dashed",
        selected && CARD_SELECTED,
      )}
    >
      <Handle type="target" position={Position.Left} className={HANDLE} />
      <TechIcon icon={icon} size={18} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs font-semibold">{node.name}</div>
        <div className="truncate text-[10px] text-muted-foreground">{subtitle}</div>
      </div>
      <Badges errors={data.errors} warnings={data.warnings} />
      <Handle type="source" position={Position.Right} className={HANDLE} />
    </div>
  )
}, sameCardProps)

export const FrameNode = memo(function FrameNode({ data, selected }: NodeProps<DockerFlowNode>) {
  const node = data.node
  const name = node?.name ?? "default"
  const empty = !!node && data.empty === true
  const facts = [
    node?.network?.driver,
    node?.network?.internal ? "internal" : undefined,
    data.external ? "external" : undefined,
    node ? undefined : "implicit",
  ].filter(Boolean)
  return (
    <div
      data-testid="docker-network"
      data-name={name}
      className={cn(
        "relative h-full w-full rounded-2xl border-2 border-dashed",
        node?.network?.internal ? "border-amber-500/40 bg-amber-500/[0.04]" : "border-sky-500/30 bg-sky-500/[0.035]",
        !node && "border-muted-foreground/20 bg-muted/15",
        selected && FRAME_SELECTED,
      )}
    >
      <Handle type="target" position={Position.Left} className={HANDLE} style={{ top: FRAME_HEADER / 2 }} />
      <div className="flex items-center gap-2 px-3" style={{ height: empty ? "100%" : FRAME_HEADER }}>
        <span className="flex size-6 items-center justify-center rounded-md bg-card/80 ring-1 ring-border ring-inset">
          {node ? <TechIcon icon={techIcon("network")} size={14} /> : <NetworkIcon className="size-3.5 text-muted-foreground" />}
        </span>
        <span className="truncate text-[13px] font-semibold">{name}</span>
        <span className="truncate text-[11px] text-muted-foreground">{node ? "network" : "default network"}</span>
        {facts.length > 0 && <span className="ml-auto truncate font-mono text-[10px] text-muted-foreground">{facts.join(" · ")}</span>}
        <Badges errors={data.errors} warnings={data.warnings} />
      </div>
      <Handle type="source" position={Position.Right} className={HANDLE} style={{ top: FRAME_HEADER / 2 }} />
    </div>
  )
}, sameCardProps)

const EDGE_DASH: Partial<Record<CanvasEdge["kind"], string>> = {
  inferred: "6 5",
  volume: "2 4",
  config: "2 4",
  secret: "2 4",
  network: "8 4",
  extends: "6 3",
}

export const DockerEdge = memo(function DockerEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  selected,
  data,
}: EdgeProps<DockerFlowEdge>) {
  const arcade = useArcade()
  const [path, labelX, labelY] = arcade
    ? getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 0 })
    : getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition })
  const emphasis = selected || data?.hovered
  // The condition of a dependency and the env key of an inferred connection show without hovering.
  const showLabel = !!data?.label && (emphasis || data.kind === "depends_on" || data.kind === "inferred")
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        style={edgeStyle(emphasis, data ? EDGE_DASH[data.kind] : undefined, data?.kind === "network" ? 0.35 : 0.55)}
      />
      <EdgeFlow id={id} path={path} labelX={labelX} labelY={labelY} running={!!data?.live} />
      {showLabel && (
        <EdgeLabelRenderer>
          <div
            data-testid="docker-edge-label"
            data-kind={data!.kind}
            className={cn(
              "nodrag nopan pointer-events-none absolute rounded-md border bg-popover px-1.5 py-0.5 font-mono text-[10px] text-popover-foreground shadow-sm",
              !emphasis && "opacity-85",
            )}
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
          >
            {data!.kind === "depends_on" ? `depends_on · ${data!.label}` : data!.label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
})

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
import { CircleAlertIcon, PlugIcon, TriangleAlertIcon } from "lucide-react"
import { memo } from "react"
import { useArcade } from "@/components/arcade/arcade-store"
import { CARD_SELECTED, CARD_SUBTITLE, CARD_SURFACE, CARD_TITLE, edgeStyle, FRAME_SELECTED, HANDLE, ICON_TILE, RunningFlow, StatusDot } from "@/components/canvas-card"
import { CLOUD_META, cloudOfType } from "@/components/cloud-meta"
import { sameCardProps } from "@/components/flow-items"
import { ResourceIcon } from "@/components/resource-icon"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { EdgeKind, ResourceSchema, WorkflowNode } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { NodeLiveBadges } from "../live/live-chips"
import { openEndpoints } from "../panels/endpoints/use-endpoints"
import { PlanMarker } from "../plan/plan-marker"
import { GROUP_HEADER, NODE_HEIGHT, NODE_WIDTH } from "./layout"

export interface ResourceNodeData extends Record<string, unknown> {
  node: WorkflowNode
  schema?: ResourceSchema
  label: string
  deployed: boolean
  /** Address of the node's first usable deployed resource (Endpoints tab). */
  endpointAddress?: string
  errors: number
  warnings: number
}

export type ResourceFlowNode = Node<ResourceNodeData, "resource" | "container">

export interface RefEdgeData extends Record<string, unknown> {
  kind: EdgeKind
  attribute: string
  sourceAttr?: string
  hovered: boolean
  live: boolean
}

export type RefFlowEdge = Edge<RefEdgeData, "ref">

function accentOf(data: ResourceNodeData) {
  const cloud = data.schema?.cloud ?? cloudOfType(data.node.type)
  return cloud ? CLOUD_META[cloud].accent : "#a855f7"
}

function iconOf(data: ResourceNodeData) {
  if (data.node.kind === "module") return "package"
  return data.schema?.icon
}

/** The card's one status dot: deployed, runs locally, or export only (modules have none). */
function DeployStatus({ data }: { data: ResourceNodeData }) {
  if (data.node.kind === "module" || !data.schema) return null
  if (data.deployed) {
    return (
      <StatusDot tone="ok" label="Deployed" hint="Deployed: running in your local Floci sandbox">
        Deployed
      </StatusDot>
    )
  }
  return data.schema.flociSupported ? (
    <StatusDot tone="ready" label="Runs locally" hint="Runs in your local Floci sandbox">
      Local
    </StatusDot>
  ) : (
    <StatusDot tone="off" label="Export only" hint="Not emulated locally — it is kept in the exported Terraform but skipped by the sandbox">
      Export only
    </StatusDot>
  )
}

function ProblemCounts({ errors, warnings }: { errors: number; warnings: number }) {
  return (
    <>
      {errors > 0 && (
        <span data-slot="node-badge" className="inline-flex h-4.5 items-center gap-0.5 rounded-full bg-destructive/12 px-1.5 text-[10px] font-medium text-destructive">
          <CircleAlertIcon className="size-2.5" />
          {errors}
        </span>
      )}
      {warnings > 0 && (
        <span data-slot="node-badge" className="inline-flex h-4.5 items-center gap-0.5 rounded-full bg-amber-500/12 px-1.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
          <TriangleAlertIcon className="size-2.5" />
          {warnings}
        </span>
      )}
    </>
  )
}

/** Arcade's square packet hops along a live edge in 12 discrete steps instead of gliding. */
const PACKET_STEPS = (() => {
  const steps = Array.from({ length: 12 }, (_, i) => (i / 12).toFixed(4)).join(";")
  return { points: steps, times: steps }
})()

function KindTag({ kind }: { kind: WorkflowNode["kind"] }) {
  if (kind === "resource") return null
  return (
    <span
      className={cn(
        "rounded px-1 text-[9px] font-semibold tracking-wider uppercase",
        kind === "data" ? "bg-sky-500/15 text-sky-600 dark:text-sky-400" : "bg-violet-500/15 text-violet-600 dark:text-violet-400",
      )}
    >
      {kind === "data" ? "Data" : "Module"}
    </span>
  )
}

export const ResourceNode = memo(function ResourceNode({ data, selected }: NodeProps<ResourceFlowNode>) {
  const accent = accentOf(data)
  const { node } = data
  return (
    <div
      data-slot="node-card"
      className={cn(
        "group/node relative flex flex-col justify-between px-3 pt-2.5 pb-2",
        CARD_SURFACE,
        node.kind === "data" && "border-dashed bg-card/80",
        // Stacked sheets: a module groups resources.
        node.kind === "module" &&
          "border-violet-500/40 shadow-[4px_4px_0_-1px_var(--color-card),4px_4px_0_0_var(--color-border)] hover:shadow-[4px_4px_0_-1px_var(--color-card),4px_4px_0_0_var(--color-border)]",
        selected && CARD_SELECTED,
      )}
      style={{ width: NODE_WIDTH, height: NODE_HEIGHT }}
    >
      {/* The provider's color: a hairline along the top edge. */}
      <span
        data-slot="node-accent"
        aria-hidden
        className="pointer-events-none absolute inset-x-5 -top-px h-px opacity-90 [mask-image:linear-gradient(90deg,transparent,black_30%,black_70%,transparent)]"
        style={{ background: accent }}
      />
      <PlanMarker nodeId={node.id} />
      {/* Clear of the plan marker in the top-right corner. */}
      <NodeLiveBadges id={node.id} className="right-8" />
      <Handle type="target" position={Position.Left} className={HANDLE} />
      <div className="flex min-w-0 items-center gap-2.5">
        <span data-slot="resource-tile" className={cn(ICON_TILE, "ring-1 ring-current/15 ring-inset")} style={{ background: `${accent}1a`, color: accent }}>
          <ResourceIcon name={iconOf(data)} className="size-4.5" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className={CARD_TITLE}>{data.label}</span>
            <KindTag kind={node.kind} />
          </div>
          <div className={cn(CARD_SUBTITLE, "mt-0.5")}>{node.kind === "module" ? `module.${node.name}` : `${node.type}.${node.name}`}</div>
        </div>
      </div>
      <div className="flex min-w-0 items-center gap-1">
        <DeployStatus data={data} />
        <span className="ml-auto flex shrink-0 items-center gap-1">
          <ProblemCounts errors={data.errors} warnings={data.warnings} />
          {data.endpointAddress && <EndpointsButton address={data.endpointAddress} />}
        </span>
      </div>
      <Handle type="source" position={Position.Right} className={HANDLE} />
    </div>
  )
}, sameCardProps)

/** Opens the Endpoints tab at this node's resource: URLs, snippets and "Try it". */
function EndpointsButton({ address }: { address: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label="How to use it: URLs, snippets and Try it"
          className="nodrag inline-flex size-5 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary transition-colors hover:bg-primary/20 focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
          onClick={(e) => {
            e.stopPropagation()
            openEndpoints(address)
          }}
          onDoubleClick={(e) => e.stopPropagation()}
        >
          <PlugIcon className="size-3" />
        </button>
      </TooltipTrigger>
      <TooltipContent>Use it: URLs, code snippets and Try it</TooltipContent>
    </Tooltip>
  )
}

export const GroupNode = memo(function GroupNode({ data, selected, width, height }: NodeProps<ResourceFlowNode>) {
  const accent = accentOf(data)
  const { node } = data
  return (
    <div
      data-slot="group-card"
      className={cn("relative rounded-2xl border-2 border-dashed transition-shadow duration-150", selected && FRAME_SELECTED)}
      style={{ width, height, borderColor: `${accent}59`, background: `${accent}0a` }}
    >
      <PlanMarker nodeId={node.id} />
      <Handle type="target" position={Position.Left} className={HANDLE} style={{ top: GROUP_HEADER / 2 }} />
      <div className="flex items-center gap-2 px-3" style={{ height: GROUP_HEADER }}>
        <span className="flex size-6 items-center justify-center rounded-md ring-1 ring-current/15 ring-inset" style={{ background: `${accent}1f`, color: accent }}>
          <ResourceIcon name={iconOf(data)} className="size-3.5" />
        </span>
        <span className={CARD_TITLE}>{data.label}</span>
        <span className={CARD_SUBTITLE}>
          {node.type}.{node.name}
        </span>
        <div className="ml-auto flex shrink-0 items-center gap-1.5">
          <DeployStatus data={data} />
          <ProblemCounts errors={data.errors} warnings={data.warnings} />
        </div>
      </div>
      <Handle type="source" position={Position.Right} className={HANDLE} style={{ top: GROUP_HEADER / 2 }} />
    </div>
  )
}, sameCardProps)

export const RefEdge = memo(function RefEdge({
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
}: EdgeProps<RefFlowEdge>) {
  // Arcade draws edges as stepped orthogonal wires; the default keeps its curves.
  const arcade = useArcade()
  const [path, labelX, labelY] = arcade
    ? getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 0 })
    : getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition })
  const dependsOn = data?.kind === "depends_on"
  const live = data?.live && !dependsOn
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        style={edgeStyle(selected || data?.hovered, dependsOn ? "6 5" : undefined, 0.5, arcade ? 2 : undefined)}
      />
      {live && arcade && (
        <>
          <path d={path} className="arcade-live-edge" />
          <rect x={-3} y={-3} width={6} height={6} className="fill-sky-500" shapeRendering="crispEdges">
            <animateMotion dur="1.8s" repeatCount="indefinite" path={path} calcMode="discrete" keyPoints={PACKET_STEPS.points} keyTimes={PACKET_STEPS.times} />
          </rect>
        </>
      )}
      {live && !arcade && <RunningFlow path={path} />}
      {(data?.hovered || selected) && (
        <EdgeLabelRenderer>
          <div
            className="nodrag nopan pointer-events-none absolute rounded-md border bg-popover px-1.5 py-0.5 font-mono text-[11px] text-popover-foreground shadow-sm"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
          >
            {dependsOn ? "depends_on" : `${data?.attribute}${data?.sourceAttr ? ` ← .${data.sourceAttr}` : ""}`}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
})

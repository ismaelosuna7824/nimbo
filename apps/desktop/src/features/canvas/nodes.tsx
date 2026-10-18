import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  Handle,
  Position,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
} from "@xyflow/react"
import { CircleAlertIcon, CircleCheckIcon, CloudOffIcon, PlugIcon, TriangleAlertIcon, ZapIcon } from "lucide-react"
import { memo } from "react"
import { CLOUD_META, cloudOfType } from "@/components/cloud-meta"
import { ResourceIcon } from "@/components/resource-icon"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { EdgeKind, ResourceSchema, WorkflowNode } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { openEndpoints } from "../panels/endpoints/use-endpoints"
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

function StatusBadges({ data }: { data: ResourceNodeData }) {
  const supported = data.schema?.flociSupported
  return (
    <div className="flex min-w-0 items-center gap-1">
      {data.node.kind !== "module" && data.schema && (
        <Tooltip>
          <TooltipTrigger asChild>
            {supported ? (
              <span className="inline-flex h-4.5 items-center gap-0.5 rounded-full bg-emerald-500/12 px-1.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400">
                <ZapIcon className="size-2.5" />
                Local
              </span>
            ) : (
              <span className="inline-flex h-4.5 items-center gap-0.5 rounded-full bg-muted px-1.5 text-[10px] font-medium text-muted-foreground">
                <CloudOffIcon className="size-2.5" />
                Export only
              </span>
            )}
          </TooltipTrigger>
          <TooltipContent>
            {supported
              ? "Runs in your local Floci sandbox"
              : "Not emulated locally — it is kept in the exported Terraform but skipped by the sandbox"}
          </TooltipContent>
        </Tooltip>
      )}
      {data.deployed && (
        <span className="inline-flex h-4.5 items-center gap-0.5 rounded-full bg-sky-500/12 px-1.5 text-[10px] font-medium text-sky-600 dark:text-sky-400">
          <CircleCheckIcon className="size-2.5" />
          Deployed
        </span>
      )}
      {data.errors > 0 && (
        <span className="inline-flex h-4.5 items-center gap-0.5 rounded-full bg-destructive/12 px-1.5 text-[10px] font-medium text-destructive">
          <CircleAlertIcon className="size-2.5" />
          {data.errors}
        </span>
      )}
      {data.warnings > 0 && (
        <span className="inline-flex h-4.5 items-center gap-0.5 rounded-full bg-amber-500/12 px-1.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
          <TriangleAlertIcon className="size-2.5" />
          {data.warnings}
        </span>
      )}
    </div>
  )
}

const handleClass = "!size-2.5 !border-2 !border-background !bg-muted-foreground/70 hover:!bg-primary"

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
      className={cn(
        "group/node relative flex flex-col justify-between rounded-xl border bg-card px-3 py-2.5 text-card-foreground shadow-sm transition-shadow",
        node.kind === "data" && "border-dashed bg-card/70",
        node.kind === "module" && "border-violet-500/50 shadow-[4px_4px_0_-1px_var(--color-border)]",
        selected && "ring-2 ring-primary ring-offset-2 ring-offset-background",
      )}
      style={{ width: NODE_WIDTH, height: NODE_HEIGHT }}
    >
      <span className="absolute inset-y-2 left-0 w-1 rounded-r" style={{ background: accent }} />
      <Handle type="target" position={Position.Left} className={handleClass} />
      <div className="flex min-w-0 items-center gap-2.5">
        <span
          className="flex size-8 shrink-0 items-center justify-center rounded-lg"
          style={{ background: `${accent}1f`, color: accent }}
        >
          <ResourceIcon name={iconOf(data)} className="size-4.5" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate text-sm leading-tight font-medium">{data.label}</span>
            <KindTag kind={node.kind} />
          </div>
          <div className="truncate font-mono text-[11px] text-muted-foreground">
            {node.kind === "module" ? `module.${node.name}` : `${node.type}.${node.name}`}
          </div>
        </div>
      </div>
      <div className="flex min-w-0 items-center gap-1">
        <StatusBadges data={data} />
        {data.endpointAddress && <EndpointsButton address={data.endpointAddress} />}
      </div>
      <Handle type="source" position={Position.Right} className={handleClass} />
    </div>
  )
})

/** Opens the Endpoints tab at this node's resource: URLs, snippets and "Try it". */
function EndpointsButton({ address }: { address: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label="How to use it: URLs, snippets and Try it"
          className="nodrag ml-auto inline-flex size-5 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary hover:bg-primary/20 focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
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
      className={cn(
        "relative rounded-2xl border-2 border-dashed",
        selected && "ring-2 ring-primary ring-offset-2 ring-offset-background",
      )}
      style={{ width, height, borderColor: `${accent}66`, background: `${accent}0d` }}
    >
      <Handle type="target" position={Position.Left} className={handleClass} style={{ top: GROUP_HEADER / 2 }} />
      <div className="flex items-center gap-2 px-3" style={{ height: GROUP_HEADER }}>
        <span className="flex size-6 items-center justify-center rounded-md" style={{ background: `${accent}26`, color: accent }}>
          <ResourceIcon name={iconOf(data)} className="size-3.5" />
        </span>
        <span className="truncate text-sm font-medium">{data.label}</span>
        <span className="truncate font-mono text-[11px] text-muted-foreground">
          {node.type}.{node.name}
        </span>
        <div className="ml-auto">
          <StatusBadges data={data} />
        </div>
      </div>
      <Handle type="source" position={Position.Right} className={handleClass} style={{ top: GROUP_HEADER / 2 }} />
    </div>
  )
})

export function RefEdge({
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
  const [path, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition })
  const dependsOn = data?.kind === "depends_on"
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        style={{
          strokeWidth: selected || data?.hovered ? 2.25 : 1.5,
          strokeDasharray: dependsOn ? "6 5" : undefined,
          stroke: selected || data?.hovered ? "var(--color-primary)" : "var(--color-muted-foreground)",
          opacity: selected || data?.hovered ? 1 : 0.65,
        }}
      />
      {data?.live && !dependsOn && (
        <circle r={3} className="fill-sky-500">
          <animateMotion dur="1.8s" repeatCount="indefinite" path={path} />
        </circle>
      )}
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
}

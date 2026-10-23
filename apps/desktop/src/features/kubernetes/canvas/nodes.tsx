import { BaseEdge, EdgeLabelRenderer, getBezierPath, getSmoothStepPath, Handle, Position, type Edge, type EdgeProps, type Node, type NodeProps } from "@xyflow/react"
import { LockIcon, PlugIcon, TriangleAlertIcon } from "lucide-react"
import { memo } from "react"
import { useArcade } from "@/components/arcade/arcade-store"
import { K8sIcon } from "@/components/k8s-icons/k8s-icon"
import { techIconFor } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Badges, chip, handleClass, STATE_COLOR } from "@/features/docker/canvas/nodes"
import { ServiceCardUsage } from "@/features/docker/perf/components"
import { openEndpoints } from "@/features/panels/endpoints/use-endpoints"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import type { KubeNode } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { FRAME_HEADER, replicasText, type KubeCanvasEdge, type KubeCanvasItem, type KubeCardKind } from "./model"

export interface KubeNodeData extends Record<string, unknown> {
  kind: KubeCardKind
  node?: KubeNode
  namespace?: string
  status?: KubeCanvasItem["status"]
  pods?: KubeCanvasItem["pods"]
  hpa?: KubeCanvasItem["hpa"]
  facts?: string
  unmatched?: boolean
  errors: number
  warnings: number
  /** Endpoints card address of a Service/Ingress that is reachable now. */
  endpoint?: string
  /** A Namespace frame without objects (drawn as a compact card). */
  empty?: boolean
}

export type KubeFlowNode = Node<KubeNodeData, "workload" | "small" | "frame">

export interface KubeEdgeData extends Record<string, unknown> {
  kind: KubeCanvasEdge["kind"]
  label?: string
  live: boolean
  hovered: boolean
}

export type KubeFlowEdge = Edge<KubeEdgeData, "kube">

const POD_TONE: Record<string, string> = {
  ok: "bg-emerald-500",
  pending: "bg-amber-500 animate-pulse",
  error: "bg-destructive",
  done: "bg-muted-foreground/50",
}

/** Helm chip and the read-only lock ("Open in code") shared by every card. */
function SourceChips({ node }: { node: KubeNode }) {
  return (
    <>
      {node.sourceKind === "helm" && (
        <span className="inline-flex h-4.5 shrink-0 items-center gap-0.5 rounded-full bg-muted px-1 text-[10px]" title={`From the Helm chart ${node.sourcePath}`}>
          <TechIcon icon="helm" size={10} />
          helm
        </span>
      )}
      {!node.editable && (
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              data-testid="k8s-node-locked"
              aria-label="Open in code"
              className="nodrag inline-flex size-4.5 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground hover:text-foreground"
              onClick={(e) => {
                e.stopPropagation()
                if (node.file) revealFiles("", node.file, node.line)
              }}
              onDoubleClick={(e) => e.stopPropagation()}
            >
              <LockIcon className="size-2.5" />
            </button>
          </TooltipTrigger>
          <TooltipContent>Generated: edit it in code{node.file ? ` (${node.file})` : ""}</TooltipContent>
        </Tooltip>
      )}
    </>
  )
}

function EndpointButton({ endpoint }: { endpoint: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label="Use it: URLs, snippets and Try it"
          className="nodrag ml-auto inline-flex size-5 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary hover:bg-primary/20"
          onClick={(e) => {
            e.stopPropagation()
            openEndpoints(endpoint)
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

export const WorkloadNode = memo(function WorkloadNode({ data, selected }: NodeProps<KubeFlowNode>) {
  const node = data.node!
  const containers = node.workload?.containers ?? []
  const state = data.status?.state ?? "absent"
  const ports = containers.flatMap((c) => c.ports)
  const replicas = replicasText(node, data.status)
  const pods = data.pods ?? []
  const image = containers[0]?.image ?? ""
  return (
    <div
      data-testid={`k8s-node-${node.id}`}
      data-kind={node.kind}
      data-name={node.name}
      data-state={state}
      className={cn(
        "group/node relative flex h-full w-full flex-col gap-1.5 rounded-xl border bg-card px-3 py-2.5 text-card-foreground shadow-sm",
        selected && "ring-2 ring-primary ring-offset-2 ring-offset-background",
      )}
    >
      <span className="absolute inset-y-2 left-0 w-1 rounded-r" style={{ background: STATE_COLOR[state] ?? STATE_COLOR.absent }} />
      <Handle type="target" position={Position.Left} className={handleClass} />
      <div className="flex min-w-0 items-center gap-2.5">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border bg-background">
          <K8sIcon icon={node.kind} size={22} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate text-sm leading-tight font-medium">{node.name}</span>
            <span className="shrink-0 text-[10px] text-muted-foreground">{node.kind}</span>
          </div>
          <div className="flex min-w-0 items-center gap-1">
            {containers.slice(0, 4).map((c) => (
              <TechIcon key={c.name} icon={techIconFor({ image: c.image, stack: node.stack })} size={12} title={`${c.name}: ${c.image}`} />
            ))}
            <span className="truncate font-mono text-[11px] text-muted-foreground" title={containers.map((c) => c.image).join(", ")}>
              {image || "no containers"}
              {containers.length > 1 ? ` +${containers.length - 1}` : ""}
            </span>
          </div>
        </div>
        {data.status?.rollout && state !== "running" && (
          <Tooltip>
            <TooltipTrigger asChild>
              <span className={cn("size-2 shrink-0 rounded-full", state === "starting" && "animate-pulse")} style={{ background: STATE_COLOR[state] }} />
            </TooltipTrigger>
            <TooltipContent>{data.status.rollout}</TooltipContent>
          </Tooltip>
        )}
      </div>
      {pods.length > 0 && (
        <div className="flex min-w-0 items-center gap-1" data-slot="pods">
          {pods.slice(0, 12).map((p) => (
            <Tooltip key={p.name}>
              <TooltipTrigger asChild>
                <span data-testid="k8s-pod-dot" data-tone={p.tone} className={cn("size-2 shrink-0 rounded-full", POD_TONE[p.tone])} />
              </TooltipTrigger>
              <TooltipContent>{p.label}</TooltipContent>
            </Tooltip>
          ))}
          {pods.length > 12 && <span className="text-[10px] text-muted-foreground">+{pods.length - 12}</span>}
          {pods.find((p) => p.reason)?.reason && <span className="truncate text-[10px] font-medium text-destructive">{pods.find((p) => p.reason)!.reason}</span>}
        </div>
      )}
      <ServiceCardUsage name={node.id} status={data.status} />
      <div className="mt-auto flex min-w-0 items-center gap-1 overflow-hidden">
        {ports.slice(0, 3).map((p, i) => (
          <span key={i} data-slot="port" className={cn(chip, "bg-muted text-muted-foreground")} title={p.name}>
            {p.containerPort}
            {p.protocol && p.protocol !== "TCP" ? `/${p.protocol.toLowerCase()}` : ""}
          </span>
        ))}
        {ports.length > 3 && <span className={cn(chip, "bg-muted text-muted-foreground")}>+{ports.length - 3}</span>}
        {replicas && <span className={cn(chip, "bg-violet-500/12 text-violet-700 dark:text-violet-300")}>{replicas}</span>}
        {data.hpa && (
          <span data-slot="hpa" className={cn(chip, "bg-sky-500/12 text-sky-700 dark:text-sky-300")} title="Scaled by a HorizontalPodAutoscaler">
            HPA {data.hpa.min}–{data.hpa.max}
          </span>
        )}
        <SourceChips node={node} />
        <Badges errors={data.errors} warnings={data.warnings} />
      </div>
      <Handle type="source" position={Position.Right} className={handleClass} />
    </div>
  )
})

export const SmallNode = memo(function SmallNode({ data, selected }: NodeProps<KubeFlowNode>) {
  const node = data.node!
  return (
    <div
      data-testid={`k8s-node-${node.id}`}
      data-kind={node.kind}
      data-name={node.name}
      data-unmatched={data.unmatched || undefined}
      className={cn(
        "relative flex h-full w-full items-center gap-2 rounded-lg border bg-card px-2.5 text-card-foreground shadow-xs",
        data.unmatched && "border-amber-500/70 bg-amber-500/5",
        selected && "ring-2 ring-primary ring-offset-2 ring-offset-background",
      )}
    >
      <Handle type="target" position={Position.Left} className={handleClass} />
      <K8sIcon icon={node.kind} size={20} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1">
          <span className="truncate text-xs font-medium">{node.name}</span>
          {data.unmatched && (
            <Tooltip>
              <TooltipTrigger asChild>
                <TriangleAlertIcon data-testid="k8s-service-unmatched" className="size-3 shrink-0 text-amber-600 dark:text-amber-400" />
              </TooltipTrigger>
              <TooltipContent>Its selector matches no pods: connect it to a workload to fix the selector</TooltipContent>
            </Tooltip>
          )}
        </div>
        <div className="truncate font-mono text-[10px] text-muted-foreground" title={data.facts}>
          {data.facts}
        </div>
      </div>
      <SourceChips node={node} />
      <Badges errors={data.errors} warnings={data.warnings} />
      {data.endpoint && <EndpointButton endpoint={data.endpoint} />}
      <Handle type="source" position={Position.Right} className={handleClass} />
    </div>
  )
})

export const FrameNode = memo(function FrameNode({ data, selected }: NodeProps<KubeFlowNode>) {
  const node = data.node
  const name = data.namespace ?? node?.name ?? "default"
  const empty = !!node && data.empty === true
  return (
    <div
      data-testid={`k8s-node-${node?.id ?? `namespace:${name}`}`}
      data-kind="Namespace"
      data-name={name}
      className={cn(
        "relative h-full w-full rounded-2xl border-2 border-dashed border-blue-500/35 bg-blue-500/[0.04]",
        !node && "border-muted-foreground/25 bg-muted/20",
        selected && "ring-2 ring-primary ring-offset-2 ring-offset-background",
      )}
    >
      <Handle type="target" position={Position.Left} className={handleClass} style={{ top: FRAME_HEADER / 2 }} />
      <div className="flex items-center gap-2 px-3" style={{ height: empty ? "100%" : FRAME_HEADER }}>
        <span className="flex size-6 items-center justify-center rounded-md bg-background/80">
          <K8sIcon icon="Namespace" size={16} />
        </span>
        <span className="truncate text-sm font-medium">{name}</span>
        <span className="truncate text-[11px] text-muted-foreground">{node ? "namespace" : "namespace (implicit)"}</span>
        {node && <SourceChips node={node} />}
        <Badges errors={data.errors} warnings={data.warnings} />
      </div>
      <Handle type="source" position={Position.Right} className={handleClass} style={{ top: FRAME_HEADER / 2 }} />
    </div>
  )
})

const EDGE_DASH: Partial<Record<KubeCanvasEdge["kind"], string>> = {
  inferred: "6 5",
  env: "2 4",
  envFrom: "2 4",
  volume: "2 4",
  pvc: "2 4",
  storageclass: "2 4",
  serviceaccount: "4 4",
  binds: "4 4",
  netpol: "8 4",
  disrupts: "8 4",
}

/** Kinds whose label shows without hovering (ports of routes, env keys of inferred connections). */
const ALWAYS_LABELED: Partial<Record<KubeCanvasEdge["kind"], true>> = { routes: true, inferred: true }

export const KubeEdge = memo(function KubeEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd, selected, data }: EdgeProps<KubeFlowEdge>) {
  const arcade = useArcade()
  const [path, labelX, labelY] = arcade
    ? getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 0 })
    : getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition })
  const emphasis = selected || data?.hovered
  const text = data && (emphasis ? [data.kind, data.label].filter(Boolean).join(" · ") : ALWAYS_LABELED[data.kind] ? data.label : undefined)
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        style={{
          strokeWidth: emphasis ? 2.25 : 1.5,
          strokeDasharray: data ? EDGE_DASH[data.kind] : undefined,
          stroke: emphasis ? "var(--color-primary)" : "var(--color-muted-foreground)",
          opacity: emphasis ? 1 : 0.7,
        }}
      />
      {data?.live && (
        <circle r={3} className="fill-emerald-500">
          <animateMotion dur="1.8s" repeatCount="indefinite" path={path} />
        </circle>
      )}
      {text && (
        <EdgeLabelRenderer>
          <div
            data-testid="k8s-edge-label"
            data-kind={data!.kind}
            className={cn(
              "nodrag nopan pointer-events-none absolute rounded-md border bg-popover px-1.5 py-0.5 font-mono text-[10px] text-popover-foreground shadow-sm",
              !emphasis && "opacity-85",
            )}
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
          >
            {text}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
})

import { MousePointerClickIcon } from "lucide-react"
import { Suspense, useState } from "react"
import { CloudBadge, displayClouds } from "@/components/cloud"
import { lazyComponent } from "@/components/lazy-module"
import { useSyncedState } from "@/components/use-synced-state"
import { WorkflowIconDialog } from "@/components/workflow-icon/icon-picker"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Badge } from "@/components/ui/badge"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { WorkflowMode } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { dockerCode, kubernetesCode, terraformCode } from "../workspaces/code"

// Each kind's forms come with its workspace code, loaded when the workflow opens (see workspaces/code).
const NodeInspector = lazyComponent(terraformCode, (m) => m.NodeInspector)
const ProvidersSection = lazyComponent(terraformCode, (m) => m.ProvidersSection)
const DockerInspector = lazyComponent(dockerCode, (m) => m.DockerInspector)
const DockerOverviewHint = lazyComponent(dockerCode, (m) => m.DockerOverviewHint)
const KubeInspector = lazyComponent(kubernetesCode, (m) => m.KubeInspector)
const KubeOverviewHint = lazyComponent(kubernetesCode, (m) => m.KubeOverviewHint)
const KubeSources = lazyComponent(kubernetesCode, (m) => m.KubeSources)

export function Inspector() {
  const hasWorkflow = useWorkflowStore((s) => s.workflow !== null)
  const docker = useWorkflowStore((s) => s.workflow?.kind === "docker")
  const dockerSelected = useWorkflowStore((s) => !!s.workflow?.compose?.graph.nodes.some((n) => n.id === s.selectedNodeId))
  const kubernetes = useWorkflowStore((s) => s.workflow?.kind === "kubernetes")
  const kubeSelected = useWorkflowStore((s) => {
    const graph = s.workflow?.kubernetes?.graph
    return !!graph && (graph.nodes.some((n) => n.id === s.selectedNodeId) || !!graph.charts?.some((c) => c.id === s.selectedNodeId))
  })
  const nodeSelected = useWorkflowStore((s) => !!s.workflow?.nodes.some((n) => n.id === s.selectedNodeId))

  if (!hasWorkflow) return null
  return (
    <Suspense fallback={null}>
      {docker ? (
        dockerSelected ? (
          <DockerInspector />
        ) : (
          <WorkflowOverview />
        )
      ) : kubernetes ? (
        kubeSelected ? (
          <KubeInspector />
        ) : (
          <WorkflowOverview />
        )
      ) : nodeSelected ? (
        <NodeInspector />
      ) : (
        <WorkflowOverview />
      )}
    </Suspense>
  )
}

function WorkflowOverview() {
  const wf = useWorkflowStore((s) => s.workflow)!
  const update = useWorkflowStore((s) => s.update)
  const [description, setDescription] = useSyncedState(wf.description ?? "")
  const [pickingIcon, setPickingIcon] = useState(false)
  const errors = (wf.diagnostics ?? []).filter((d) => d.severity === "error").length
  return (
    <div className="flex h-full flex-col gap-5 overflow-y-auto p-4">
      <div className="flex items-center gap-3">
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label="Change workflow icon"
              onClick={() => setPickingIcon(true)}
              className="shrink-0 rounded-[12px] transition-transform outline-none hover:scale-105 focus-visible:ring-2 focus-visible:ring-ring"
            >
              <WorkflowIconTile icon={wf.icon} size={48} />
            </button>
          </TooltipTrigger>
          <TooltipContent>Change icon</TooltipContent>
        </Tooltip>
        <div className="flex min-w-0 flex-col gap-1.5">
          <h2 className="font-heading text-base font-medium">{wf.name}</h2>
          <div className="flex flex-wrap gap-1.5">
            {wf.kind === "docker" ? (
              <Badge variant="secondary" className="gap-1">
                <TechIcon icon="docker" size={12} />
                Docker Compose
              </Badge>
            ) : wf.kind === "kubernetes" ? (
              <Badge variant="secondary" className="gap-1">
                <TechIcon icon="kubernetes" size={12} />
                Kubernetes
              </Badge>
            ) : (
              displayClouds(wf).map((c) => <CloudBadge key={c} cloud={c} />)
            )}
          </div>
        </div>
      </div>
      <WorkflowIconDialog workflow={pickingIcon ? wf : null} onClose={() => setPickingIcon(false)} />
      <div className={cn("grid gap-2 text-center", wf.kind === "kubernetes" ? "grid-cols-4" : "grid-cols-3")}>
        {wf.kind === "docker" ? (
          <>
            <Stat label="Services" value={(wf.compose?.graph.nodes ?? []).filter((n) => n.kind === "service").length} />
            <Stat label="Networks" value={(wf.compose?.graph.nodes ?? []).filter((n) => n.kind === "network").length} />
          </>
        ) : wf.kind === "kubernetes" ? (
          <>
            <Stat label="Workloads" value={(wf.kubernetes?.graph.nodes ?? []).filter((n) => n.category === "workload").length} />
            <Stat label="Services" value={(wf.kubernetes?.graph.nodes ?? []).filter((n) => n.kind === "Service").length} />
            <Stat label="Objects" value={(wf.kubernetes?.graph.nodes ?? []).length} />
          </>
        ) : (
          <>
            <Stat label="Resources" value={wf.nodes.filter((n) => n.kind === "resource").length} />
            <Stat label="Links" value={(wf.edges ?? []).length} />
          </>
        )}
        <Stat label="Errors" value={errors} danger={errors > 0} />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="wf-description" className="text-xs font-medium">
          Description
        </label>
        <Textarea
          id="wf-description"
          value={description}
          rows={3}
          placeholder={wf.kind === "docker" || wf.kind === "kubernetes" ? "What does this stack run?" : "What does this infrastructure do?"}
          onChange={(e) => setDescription(e.target.value)}
          onBlur={() => description !== (wf.description ?? "") && update((d) => ({ ...d, description }))}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="wf-mode" className="text-xs font-medium">
          Build mode
        </label>
        <Select
          value={wf.mode === "full" ? "full" : "architecture"}
          onValueChange={(v) => v !== (wf.mode || "architecture") && update((d) => ({ ...d, mode: v as WorkflowMode }))}
        >
          <SelectTrigger id="wf-mode" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="architecture">Architecture</SelectItem>
            <SelectItem value="full">Full code</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {wf.kind === "kubernetes" && <KubeSources sources={wf.kubernetes?.sources ?? []} />}
      {wf.kind !== "docker" && wf.kind !== "kubernetes" && <ProvidersSection />}
      {wf.kind === "docker" ? (
        <DockerOverviewHint />
      ) : wf.kind === "kubernetes" ? (
        <KubeOverviewHint />
      ) : (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <MousePointerClickIcon />
            </EmptyMedia>
            <EmptyTitle>Select a resource</EmptyTitle>
            <EmptyDescription>
              Click a card on the canvas to configure it. Drag between the dots of two cards to connect them.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}
    </div>
  )
}

function Stat({ label, value, danger }: { label: string; value: number; danger?: boolean }) {
  return (
    <div className="rounded-lg border px-2 py-2">
      <div className={cn("font-heading text-lg font-medium", danger && "text-destructive")}>{value}</div>
      <div className="text-[11px] text-muted-foreground">{label}</div>
    </div>
  )
}

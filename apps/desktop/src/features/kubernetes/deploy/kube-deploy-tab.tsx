import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Layers, Play, Power, RotateCcw, Square, Trash2, TriangleAlert } from "lucide-react"
import { useShallow } from "zustand/react/shallow"
import { toast } from "sonner"
import { PixelProgress } from "@/components/arcade/pixel-progress"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { Run, RunAction } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { runAction, type RunOptions } from "@/features/toolbar/actions"
import { useUiStore } from "@/features/toolbar/ui-store"
import { RunLog } from "@/features/panels/deploy/run-log"
import { ComposeRunRow } from "@/features/docker/deploy/compose-deploy-tab"
import { ComposeResources, type ResourceRow } from "@/features/docker/perf/compose-resources"
import { cpuLimitLabel, formatBytes, TONE_CLASS } from "@/features/docker/perf/model"
import { EnvironmentSwitch } from "@/features/environments/environment-switch"
import { ClusterDetails, ClusterStateLabel, KubeAccess, type ClusterAction } from "./kube-cluster"
import { KubeEvents } from "./kube-events"
import { ALL, KubeLogs, type LogSelection } from "./kube-logs"
import { kubeKey, kubeProblemTone, workloadLabels, workloadLimits } from "./kube-model"
import { KubeProblems } from "./kube-problems"
import { KubeWorkloads } from "./kube-workloads"

const RUN_ICON: Partial<Record<Run["action"], typeof Play>> = {
  up: Play,
  down: Power,
  restart: RotateCcw,
  "cluster-start": Play,
  "cluster-stop": Square,
  "cluster-delete": Trash2,
}

type View = "workloads" | "resources" | "problems" | "events" | "run" | "logs"

/**
 * Deploy tab of a Kubernetes workspace: the local k3s cluster (state, kubeconfig, start/stop/delete), Deploy/Restart/
 * Destroy, port-forwards and Ingress URLs, every workload with its rollout and pods, live usage, pod problems, cluster
 * events, run history with logs, and live pod logs.
 */
export function KubeDeployTab() {
  const workflow = useWorkflowStore(
    useShallow(
      (s) =>
        s.workflow && {
          id: s.workflow.id,
          nodes: s.workflow.kubernetes?.graph.nodes,
          rendered: s.workflow.kubernetes?.environment ?? "",
        },
    ),
  )
  const status = useWorkflowStore((s) => s.kube)
  const runs = useWorkflowStore((s) => s.runs)
  const panelVisible = useWorkflowStore((s) => s.bottomOpen && s.bottomTab === "deploy")
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null)
  const [view, setView] = useState<View>("workloads")
  const [logs, setLogs] = useState<LogSelection>({ workload: "", pod: ALL, container: ALL })
  const [eventsFor, setEventsFor] = useState(ALL)
  const [starting, setStarting] = useState<string | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const health = useQuery({ queryKey: ["health"], queryFn: api.health, staleTime: 30_000 })
  const workloads = useMemo(() => status?.workloads ?? [], [status])
  const resourceRows = useMemo<ResourceRow[]>(() => {
    const labels = workloadLabels(workloads)
    return workloads.map((w) => {
      const limits = workloadLimits(w)
      const parts = [limits.cpu > 0 && cpuLimitLabel(limits.cpu, true), limits.mem > 0 && formatBytes(limits.mem)].filter(Boolean)
      return {
        name: w.nodeId,
        state: w.state,
        startupMs: w.startupMs,
        label: parts.length > 0 ? `${labels[w.nodeId]} · limit ${parts.join(" / ")}` : labels[w.nodeId],
      }
    })
  }, [workloads])

  if (!workflow) return null

  const active = runs.find((r) => r.status === "queued" || r.status === "running")
  const selectedRun = runs.find((r) => r.id === selectedRunId) ?? runs[0]
  const dockerDown = health.data && !health.data.docker
  const cluster = status?.cluster
  const clusterState = cluster?.state ?? "absent"
  const problems = status?.problems ?? []
  const problemsTone = kubeProblemTone(problems)
  const warnings = (status?.events ?? []).filter((e) => e.type === "Warning").length
  const busy = !!active || starting !== null || !!dockerDown
  // The selected log workload, or the first one once there are workloads.
  const logSelection = logs.workload || !workloads[0] ? logs : { ...logs, workload: workloads[0].nodeId }

  /** Starts a cluster run and shows its log; key identifies the clicked button for its spinner. */
  const start = async (action: RunAction, key: string, opts: RunOptions = {}) => {
    setStarting(key)
    const before = useWorkflowStore.getState().runs[0]?.id
    try {
      await runAction(action, opts)
    } finally {
      setStarting(null)
    }
    // runAction reports its own errors; only a new run in the history means it started.
    const latest = useWorkflowStore.getState().runs[0]
    if (latest && latest.id !== before) {
      setSelectedRunId(latest.id)
      setView("run")
    }
  }

  const showLogs = (workload: string, pod?: string) => {
    setLogs({ workload, pod: pod ?? ALL, container: ALL })
    setView("logs")
  }

  const clusterAction = (action: ClusterAction) => {
    if (action === "cluster-delete") setConfirmDelete(true)
    else void start(action, action)
  }

  return (
    // Narrow bottom panels (< 48rem, the panel is the container) stack the controls above the views and scroll as one
    // page; wide ones put them side by side, each scrolling on its own.
    <div
      className="grid h-full min-h-0 grid-rows-[auto_minmax(20rem,1fr)] overflow-y-auto @3xl:grid-cols-[minmax(17rem,22rem)_minmax(0,1fr)] @3xl:grid-rows-1 @3xl:overflow-hidden"
      data-testid="kube-deploy-tab"
    >
      <div className="flex flex-col gap-2 border-b p-2 @3xl:min-h-0 @3xl:overflow-auto @3xl:border-r @3xl:border-b-0">
        {dockerDown && (
          <div className="flex gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
            <div>
              <p className="font-medium">Docker is not available</p>
              <p className="opacity-80">
                {health.data?.dockerError ?? "The local cluster runs inside Docker. Start Docker Desktop or OrbStack and retry."}
              </p>
            </div>
          </div>
        )}

        <section className="flex flex-col gap-2 rounded-lg border p-2.5" data-testid="kube-cluster">
          <div className="flex min-w-0 items-center gap-2">
            <TechIcon icon="kubernetes" size={14} />
            <span className="truncate text-xs font-semibold" title={cluster?.name}>
              Local cluster
            </span>
            <ClusterStateLabel state={clusterState} className="ml-auto shrink-0 text-xs" />
          </div>
          <div className="flex min-w-0 items-center gap-1.5">
            <EnvironmentSwitch className="h-6 min-w-0 flex-1" />
            <Tooltip>
              <TooltipTrigger asChild>
                <span
                  className="inline-flex h-5 shrink-0 items-center gap-1 rounded-full border px-1.5 text-[11px] text-muted-foreground"
                  data-testid="kube-rendered-environment"
                >
                  <Layers className="size-3" />
                  {workflow.rendered || "base"}
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-56">
                Deploy applies the {workflow.rendered ? `“${workflow.rendered}”` : "base"} rendering (overlays / values files of
                the active environment).
              </TooltipContent>
            </Tooltip>
          </div>
          {(cluster?.message || status?.message) && (
            <p
              className={cn(
                "rounded-md bg-muted px-2 py-1.5 text-xs",
                (clusterState === "error" || status?.state === "error") && "bg-destructive/10 text-destructive",
              )}
              data-testid="kube-status-message"
            >
              {cluster?.message || status?.message}
            </p>
          )}
          <ClusterDetails cluster={cluster} busy={busy} starting={starting} onAction={clusterAction} />
          <div className="grid grid-cols-3 gap-1">
            <ActionButton
              label="Deploy"
              hint="Create or start the cluster, build and load local images, apply every source and wait for the rollouts."
              testId="kube-action-up"
              disabled={busy}
              loading={starting === "up"}
              variant="default"
              onClick={() => void start("up", "up")}
            >
              <Play />
            </ActionButton>
            <ActionButton
              label="Restart"
              hint="Rollout restart of every Deployment, StatefulSet and DaemonSet (no re-apply)."
              testId="kube-action-restart"
              disabled={busy || clusterState !== "running" || workloads.length === 0}
              loading={starting === "restart"}
              variant="secondary"
              onClick={() => void start("restart", "restart", { workload: "" })}
            >
              <RotateCcw />
            </ActionButton>
            <ActionButton
              label="Destroy"
              hint="Delete what Deploy applied (Helm releases too). PVCs (your data) and the cluster are kept unless you choose otherwise."
              testId="kube-action-down"
              disabled={busy || clusterState === "absent"}
              loading={starting === "down"}
              variant="outline"
              className="text-destructive"
              onClick={() => useUiStore.getState().setConfirmDestroy(true)}
            >
              <Power />
            </ActionButton>
          </div>
          {active && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Spinner className="size-3.5" />
              <span className="capitalize">{active.action}</span> in progress…
              <Button
                size="xs"
                variant="ghost"
                className="ml-auto"
                onClick={() =>
                  void api.cancelRun(active.id).catch((err) => toast.error("Cancel failed", { description: errorMessage(err) }))
                }
              >
                <Square /> Cancel
              </Button>
            </div>
          )}
          {active && <PixelProgress label={`${active.action} in progress`} />}
        </section>

        <section className="flex flex-col gap-1.5 rounded-lg border p-2.5" data-testid="kube-access">
          <h3 className="text-xs font-semibold">Reachable from this machine</h3>
          <KubeAccess forwards={status?.forwards ?? []} ingresses={status?.ingresses ?? []} />
        </section>

        <section className="flex min-h-0 flex-col rounded-lg border">
          <h3 className="border-b px-2.5 py-1.5 text-xs font-semibold">Runs</h3>
          {runs.length === 0 ? (
            <p className="p-2.5 text-xs text-muted-foreground">No runs yet.</p>
          ) : (
            <div className="divide-y" role="listbox" aria-label="Runs">
              {runs.map((r) => (
                <ComposeRunRow
                  key={r.id}
                  run={r}
                  icon={RUN_ICON[r.action]}
                  selected={r.id === selectedRun?.id}
                  onSelect={() => {
                    setSelectedRunId(r.id)
                    setView("run")
                  }}
                />
              ))}
            </div>
          )}
        </section>
      </div>

      <Tabs value={view} onValueChange={(v) => setView(v as View)} className="@container flex min-h-0 min-w-0 flex-col gap-0">
        <TabsList variant="line" className="h-8 w-full shrink-0 justify-start overflow-x-auto overflow-y-hidden rounded-none border-b px-2 [scrollbar-width:none]">
          <TabsTrigger value="workloads" className="flex-none text-xs">
            Workloads <span className="text-muted-foreground tabular-nums">{workloads.length}</span>
          </TabsTrigger>
          <TabsTrigger value="resources" className="flex-none text-xs" data-testid="kube-view-resources">
            Resources
          </TabsTrigger>
          <TabsTrigger value="problems" className="flex-none text-xs" data-testid="kube-view-problems">
            Problems
            {problemsTone && <span className={cn("tabular-nums", TONE_CLASS[problemsTone])}>{problems.length}</span>}
          </TabsTrigger>
          <TabsTrigger value="events" className="flex-none text-xs" data-testid="kube-view-events">
            Events
            {warnings > 0 && <span className={cn("tabular-nums", TONE_CLASS.warning)}>{warnings}</span>}
          </TabsTrigger>
          <TabsTrigger value="run" className="flex-none text-xs">
            Run log
          </TabsTrigger>
          <TabsTrigger value="logs" className="flex-none text-xs" data-testid="kube-view-logs">
            Pod logs
          </TabsTrigger>
        </TabsList>
        <TabsContent value="workloads" className="min-h-0 overflow-auto">
          {workloads.length > 0 ? (
            <KubeWorkloads
              workloads={workloads}
              nodes={workflow.nodes ?? []}
              problems={problems}
              busy={busy || clusterState !== "running"}
              starting={starting}
              onRestart={(nodeId) => void start("restart", `restart:${nodeId}`, { workload: nodeId })}
              onLogs={showLogs}
              onEvents={(nodeId) => {
                setEventsFor(nodeId)
                setView("events")
              }}
            />
          ) : (
            <Empty className="h-full">
              <EmptyHeader>
                <EmptyMedia variant="icon">{status ? <TechIcon icon="kubernetes" size={20} /> : <Spinner />}</EmptyMedia>
                <EmptyTitle>{status ? "Nothing deployed yet" : "Checking the cluster…"}</EmptyTitle>
                <EmptyDescription>
                  {status
                    ? "Press Deploy to create this workflow's local cluster and apply your manifests. Deployments, StatefulSets, DaemonSets and Jobs then show up here with their pods."
                    : "Asking the cluster which workloads of this workflow exist."}
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </TabsContent>
        <TabsContent value="resources" className="min-h-0 overflow-auto" data-testid="kube-resources">
          <ComposeResources
            services={resourceRows}
            heading="Workload"
            emptyHint="Deploy to see the CPU, memory, network and disk use of every workload, sampled from its pods once a second."
          />
        </TabsContent>
        <TabsContent value="problems" className="min-h-0 overflow-auto">
          <KubeProblems problems={problems} onLogs={showLogs} />
        </TabsContent>
        <TabsContent value="events" className="min-h-0">
          <KubeEvents events={status?.events ?? []} workloads={workloads} workload={eventsFor} onWorkloadChange={setEventsFor} />
        </TabsContent>
        <TabsContent value="run" className="min-h-0">
          {selectedRun ? (
            <RunLog key={selectedRun.id} run={selectedRun} />
          ) : (
            <Empty className="h-full">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Play />
                </EmptyMedia>
                <EmptyTitle>Run your workloads locally</EmptyTitle>
                <EmptyDescription>
                  <b>Deploy</b> creates a private k3s cluster in Docker, applies your manifests, Kustomize and Helm sources and
                  waits for the rollouts. Services then get a port on 127.0.0.1 here and in Endpoints.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </TabsContent>
        <TabsContent value="logs" forceMount className="min-h-0 data-[state=inactive]:hidden">
          <KubeLogs
            workflowId={workflow.id}
            workloads={workloads}
            selection={logSelection}
            onSelectionChange={setLogs}
            active={panelVisible && view === "logs"}
            statusKey={kubeKey(status)}
          />
        </TabsContent>
      </Tabs>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>Delete the cluster?</AlertDialogTitle>
            <AlertDialogDescription>
              Deletes this workflow's local cluster with every workload and PersistentVolume (data). Your manifests stay;
              the next Deploy creates a fresh cluster.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              data-testid="kube-cluster-delete-confirm"
              onClick={() => void start("cluster-delete", "cluster-delete")}
            >
              Delete cluster
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function ActionButton({
  label,
  hint,
  testId,
  disabled,
  loading,
  variant,
  className,
  onClick,
  children,
}: {
  label: string
  hint: string
  testId: string
  disabled: boolean
  loading: boolean
  variant: "default" | "secondary" | "outline"
  className?: string
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="sm"
          variant={variant}
          className={cn("h-auto flex-col gap-1 px-1 py-1.5 text-[11px] leading-none [&_svg]:size-3.5", className)}
          disabled={disabled || loading}
          data-testid={testId}
          onClick={onClick}
        >
          {loading ? <Spinner /> : children}
          {label}
        </Button>
      </TooltipTrigger>
      <TooltipContent className="max-w-56">{hint}</TooltipContent>
    </Tooltip>
  )
}

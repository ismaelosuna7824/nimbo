import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { ArrowRightLeft, Boxes, Download, Eye, Hammer, Layers, Play, Power, RotateCcw, Square, TriangleAlert } from "lucide-react"
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
import { Checkbox } from "@/components/ui/checkbox"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { ComposeRunAction, Run } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { runAction, type RunOptions } from "@/features/toolbar/actions"
import { StatusBadge } from "@/features/panels/components/status-badge"
import { RunLog } from "@/features/panels/deploy/run-log"
import { durationBetween, formatDuration, formatRelative } from "@/features/panels/lib/format"
import { useNow } from "@/features/panels/lib/use-now"
import { EnvironmentSwitch } from "@/features/environments/environment-switch"
import { ALL_SERVICES, ComposeLogs } from "./compose-logs"
import { composeKey, remapNote } from "./compose-model"
import { ComposeServices, ComposeStateLabel } from "./compose-services"
import { ComposeBuildView } from "../perf/compose-build-view"
import { ComposeProblems } from "../perf/compose-problems"
import { ComposeResources } from "../perf/compose-resources"
import { problemTone, TONE_CLASS } from "../perf/model"

const ACTIONS: { action: ComposeRunAction; label: string; icon: typeof Play; hint: string }[] = [
  { action: "up", label: "Up", icon: Play, hint: "Build what needs building and start every service (docker compose up)." },
  { action: "build", label: "Build", icon: Hammer, hint: "Build the images of services with a Dockerfile, without starting them." },
  { action: "pull", label: "Pull", icon: Download, hint: "Download the newest images the compose file names." },
  { action: "restart", label: "Restart", icon: RotateCcw, hint: "Restart the running containers (no rebuild)." },
  { action: "down", label: "Down", icon: Power, hint: "Stop and remove the containers. Volumes (your data) are kept unless you choose otherwise." },
]

const RUN_ICON: Partial<Record<Run["action"], typeof Play>> = Object.fromEntries(ACTIONS.map((a) => [a.action, a.icon]))

type View = "services" | "resources" | "problems" | "build" | "run" | "logs"

/**
 * Deploy tab of a Docker workspace: docker compose up/build/pull/restart/down, the containers of every service
 * (state, health, ports), live resource usage, container problems, build metrics, run history with logs, and live
 * container logs.
 */
export function ComposeDeployTab() {
  const workflow = useWorkflowStore(
    useShallow(
      (s) =>
        s.workflow && {
          id: s.workflow.id,
          project: s.workflow.compose?.name || s.workflow.compose?.project || "",
          nodes: s.workflow.compose?.graph.nodes,
        },
    ),
  )
  const status = useWorkflowStore((s) => s.compose)
  const runs = useWorkflowStore((s) => s.runs)
  const panelVisible = useWorkflowStore((s) => s.bottomOpen && s.bottomTab === "deploy")
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null)
  const [view, setView] = useState<View>("services")
  const [logService, setLogService] = useState(ALL_SERVICES)
  const [starting, setStarting] = useState<string | null>(null)
  const [confirmDown, setConfirmDown] = useState(false)
  const [removeVolumes, setRemoveVolumes] = useState(false)
  const health = useQuery({ queryKey: ["health"], queryFn: api.health, staleTime: 30_000 })

  if (!workflow) return null

  const active = runs.find((r) => r.status === "queued" || r.status === "running")
  const selectedRun = runs.find((r) => r.id === selectedRunId) ?? runs[0]
  const dockerDown = health.data && !health.data.docker
  const services = status?.services ?? []
  const state = status?.state ?? "absent"
  const problems = status?.problems ?? []
  const problemsTone = problemTone(problems)

  /** Starts a compose run and shows its log; key identifies the clicked button for its spinner. */
  const start = async (action: ComposeRunAction, key: string, opts: RunOptions = {}) => {
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

  return (
    // Narrow bottom panels (< 48rem, the panel is the container) stack the controls above the views and scroll as one
    // page; wide ones put them side by side, each scrolling on its own.
    <div
      className="grid h-full min-h-0 grid-rows-[auto_minmax(20rem,1fr)] overflow-y-auto @3xl:grid-cols-[minmax(17rem,22rem)_minmax(0,1fr)] @3xl:grid-rows-1 @3xl:overflow-hidden"
      data-testid="compose-deploy-tab"
    >
      <div className="flex flex-col gap-2 border-b p-2 @3xl:min-h-0 @3xl:overflow-auto @3xl:border-r @3xl:border-b-0">
        {dockerDown && (
          <div className="flex gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
            <div>
              <p className="font-medium">Docker is not available</p>
              <p className="opacity-80">{health.data?.dockerError ?? "Start Docker Desktop or OrbStack and retry."}</p>
            </div>
          </div>
        )}

        <section className="flex flex-col gap-2 rounded-lg border p-2.5">
          <div className="flex min-w-0 items-center gap-2">
            <Boxes className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate text-xs font-semibold" title={status?.project}>
              {workflow.project || "Containers"}
            </span>
            <ComposeStateLabel state={state} className="ml-auto shrink-0 text-xs" />
          </div>
          <DeployEnvironment runs={runs} running={state !== "absent"} />
          {status?.message && (
            <p
              className={cn(
                "rounded-md bg-muted px-2 py-1.5 text-xs",
                state === "error" && "bg-destructive/10 text-destructive",
              )}
              data-testid="compose-status-message"
            >
              {status.message}
            </p>
          )}
          {(status?.remaps.length ?? 0) > 0 && (
            <div className="flex gap-1.5 rounded-md bg-amber-500/10 p-1.5 text-xs text-amber-800 dark:text-amber-300">
              <ArrowRightLeft className="mt-0.5 size-3.5 shrink-0" />
              <div>
                <ul data-testid="compose-remaps">
                  {status!.remaps.map((r) => (
                    <li key={`${r.service}:${r.containerPort}/${r.protocol}`}>{remapNote(r)}</li>
                  ))}
                </ul>
                <p className="mt-0.5 opacity-80">Your compose file is unchanged; only this machine uses the new ports.</p>
              </div>
            </div>
          )}
          <div className="grid grid-cols-5 gap-1">
            {ACTIONS.map(({ action, label, icon: Icon, hint }) => (
              <Tooltip key={action}>
                <TooltipTrigger asChild>
                  <Button
                    size="sm"
                    variant={action === "up" ? "default" : action === "down" ? "outline" : "secondary"}
                    className={cn("h-auto flex-col gap-1 px-1 py-1.5 text-[11px] leading-none [&_svg]:size-3.5", action === "down" && "text-destructive")}
                    disabled={!!active || starting !== null || dockerDown}
                    data-testid={`compose-action-${action}`}
                    onClick={() => (action === "down" ? setConfirmDown(true) : void start(action, action))}
                  >
                    {starting === action ? <Spinner /> : <Icon />}
                    {label}
                  </Button>
                </TooltipTrigger>
                <TooltipContent className="max-w-56">{hint}</TooltipContent>
              </Tooltip>
            ))}
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
          <TabsTrigger value="services" className="flex-none text-xs">
            Services <span className="text-muted-foreground tabular-nums">{services.length}</span>
          </TabsTrigger>
          <TabsTrigger value="resources" className="flex-none text-xs" data-testid="compose-view-resources">
            Resources
          </TabsTrigger>
          <TabsTrigger value="problems" className="flex-none text-xs" data-testid="compose-view-problems">
            Problems
            {problemsTone && <span className={cn("tabular-nums", TONE_CLASS[problemsTone])}>{problems.length}</span>}
          </TabsTrigger>
          <TabsTrigger value="build" className="flex-none text-xs" data-testid="compose-view-build">
            Build
          </TabsTrigger>
          <TabsTrigger value="run" className="flex-none text-xs">
            Run log
          </TabsTrigger>
          <TabsTrigger value="logs" className="flex-none text-xs">
            Container logs
          </TabsTrigger>
        </TabsList>
        <TabsContent value="services" className="min-h-0 overflow-auto">
          {services.length > 0 ? (
            <ComposeServices
              services={services}
              nodes={workflow.nodes ?? []}
              busy={!!active || starting !== null || !!dockerDown}
              starting={starting}
              onAction={(action, service) => void start(action, `${action}:${service}`, { service })}
              onLogs={(service) => {
                setLogService(service)
                setView("logs")
              }}
            />
          ) : (
            <Empty className="h-full">
              <EmptyHeader>
                <EmptyMedia variant="icon">{status ? <Boxes /> : <Spinner />}</EmptyMedia>
                <EmptyTitle>{status ? "No services yet" : "Checking the containers…"}</EmptyTitle>
                <EmptyDescription>
                  {status
                    ? "Add a service on the canvas or in compose.yaml, then press Up to build and start it with docker compose."
                    : "Asking Docker which containers of this workspace exist."}
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </TabsContent>
        <TabsContent value="resources" className="min-h-0 overflow-auto">
          <ComposeResources services={services} />
        </TabsContent>
        <TabsContent value="problems" className="min-h-0 overflow-auto">
          <ComposeProblems
            problems={problems}
            onLogs={(service) => {
              setLogService(service)
              setView("logs")
            }}
          />
        </TabsContent>
        <TabsContent value="build" className="min-h-0 overflow-auto">
          <ComposeBuildView />
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
                <EmptyTitle>Run your containers locally</EmptyTitle>
                <EmptyDescription>
                  <b>Up</b> builds the images and starts every service with docker compose on this machine. Published
                  ports then show up here and in Endpoints.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </TabsContent>
        <TabsContent value="logs" forceMount className="min-h-0 data-[state=inactive]:hidden">
          <ComposeLogs
            workflowId={workflow.id}
            services={services.map((s) => s.name)}
            service={logService}
            onServiceChange={setLogService}
            active={panelVisible && view === "logs"}
            statusKey={composeKey(status)}
          />
        </TabsContent>
      </Tabs>

      <AlertDialog
        open={confirmDown}
        onOpenChange={(open) => {
          setConfirmDown(open)
          if (!open) setRemoveVolumes(false)
        }}
      >
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>Stop and remove the containers?</AlertDialogTitle>
            <AlertDialogDescription>
              docker compose down removes this workspace’s containers and networks. Your compose files and images stay;
              Up starts everything again.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <Label className="items-start gap-2 text-xs font-normal">
            <Checkbox
              checked={removeVolumes}
              onCheckedChange={(v) => setRemoveVolumes(v === true)}
              data-testid="compose-remove-volumes"
            />
            <span>
              Also delete volumes (data)
              <span className="block text-muted-foreground">Databases and uploads stored in named volumes are lost.</span>
            </span>
          </Label>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              data-testid="compose-down-confirm"
              onClick={() => void start("down", "down", { removeVolumes })}
            >
              {removeVolumes ? "Down and delete data" : "Down"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

/**
 * The environment the next run uses (the shared switch; hidden without environments) and, when containers exist, the
 * one the last successful `up` started them with: switching does not touch running containers until the next Up.
 */
function DeployEnvironment({ runs, running }: { runs: Run[]; running: boolean }) {
  const active = useWorkflowStore((s) => s.workflow?.activeEnvironment ?? "")
  const hasEnvironments = useWorkflowStore((s) => (s.workflow?.compose?.environments?.length ?? 0) > 0)
  if (!hasEnvironments) return null
  const lastUp = runs.find((r) => r.action === "up" && r.status === "succeeded")
  const deployed = running && lastUp ? (lastUp.environment ?? "") : undefined
  const stale = deployed !== undefined && deployed !== active
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <EnvironmentSwitch className="h-6 min-w-0 flex-1" />
      {deployed !== undefined && (
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              className={cn(
                "inline-flex h-5 shrink-0 items-center gap-1 rounded-full border px-1.5 text-[11px] text-muted-foreground",
                stale && "border-amber-500/40 text-amber-700 dark:text-amber-300",
              )}
              data-testid="compose-deployed-environment"
            >
              <Layers className="size-3" />
              {deployed || "base"}
            </span>
          </TooltipTrigger>
          <TooltipContent className="max-w-56">
            The containers run the {deployed ? `“${deployed}”` : "base"} files.
            {stale && ` Up applies ${active ? `“${active}”` : "the base"}.`}
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}

/** One run of the history list (icon by action; `icon` overrides it for actions this tab doesn't list). */
export function ComposeRunRow({
  run,
  selected,
  onSelect,
  icon,
}: {
  run: Run
  selected: boolean
  onSelect: () => void
  icon?: typeof Play
}) {
  const now = useNow(run.status === "running")
  const duration = durationBetween(run.startedAt, run.endedAt, now)
  const Icon = icon ?? RUN_ICON[run.action] ?? Eye
  return (
    <button
      type="button"
      role="option"
      aria-selected={selected}
      onClick={onSelect}
      className={cn(
        "flex w-full flex-col gap-0.5 px-2.5 py-1.5 text-left text-xs outline-none hover:bg-muted/60 focus-visible:bg-muted",
        selected && "bg-muted",
      )}
    >
      <div className="flex items-center gap-1.5">
        <Icon className="size-3.5 text-muted-foreground" />
        <span className="font-medium capitalize">{run.action}</span>
        <span className="ml-auto">
          <StatusBadge status={run.status} />
        </span>
      </div>
      <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
        <span>{formatRelative(run.createdAt)}</span>
        {duration !== undefined && <span className="tabular-nums">{formatDuration(duration)}</span>}
      </div>
    </button>
  )
}

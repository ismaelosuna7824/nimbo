import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { CircleCheck, Eye, FileSearch, Rocket, ShieldCheck, Square, Trash2, TriangleAlert } from "lucide-react"
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { PlanSummary, Run, RunAction } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { StatusBadge } from "../components/status-badge"
import { editWorkflow } from "../lib/edit"
import { durationBetween, formatDuration, formatRelative } from "../lib/format"
import { useNow } from "../lib/use-now"
import { missingRequired } from "../lib/values"
import { DeployedOutputs, DeployedResources } from "./deployed-view"
import { RunLog } from "./run-log"
import { SandboxCard } from "./sandbox-card"

const NO_ENV = "__none__"

const ACTIONS: { action: RunAction; label: string; icon: typeof Rocket; hint: string }[] = [
  { action: "validate", label: "Validate", icon: ShieldCheck, hint: "Check the Terraform code for errors without touching the sandbox." },
  { action: "plan", label: "Plan", icon: FileSearch, hint: "Preview what would be created, changed or destroyed." },
  { action: "apply", label: "Apply", icon: Rocket, hint: "Create or update the resources in the local sandbox." },
  { action: "destroy", label: "Destroy", icon: Trash2, hint: "Delete every deployed resource from the sandbox." },
]

export function PlanChips({ plan }: { plan: PlanSummary }) {
  return (
    <span className="inline-flex gap-1 font-mono text-[10px] tabular-nums">
      <span className="rounded bg-emerald-500/15 px-1 text-emerald-700 dark:text-emerald-400">+{plan.add}</span>
      <span className="rounded bg-amber-500/15 px-1 text-amber-700 dark:text-amber-400">~{plan.change}</span>
      <span className="rounded bg-destructive/15 px-1 text-destructive">-{plan.destroy}</span>
    </span>
  )
}

export function DeployTab() {
  const workflow = useWorkflowStore(
    useShallow(
      (s) =>
        s.workflow && {
          id: s.workflow.id,
          activeEnvironment: s.workflow.activeEnvironment,
          environments: s.workflow.environments,
          variables: s.workflow.variables,
          diagnostics: s.workflow.diagnostics,
        },
    ),
  )
  const runs = useWorkflowStore((s) => s.runs)
  const deployed = useWorkflowStore((s) => s.deployed)
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null)
  const [view, setView] = useState("logs")
  const [starting, setStarting] = useState<RunAction | null>(null)
  const [confirmDestroy, setConfirmDestroy] = useState(false)
  const health = useQuery({ queryKey: ["health"], queryFn: api.health, staleTime: 30_000 })

  if (!workflow) return null

  const active = runs.find((r) => r.status === "queued" || r.status === "running")
  const selectedRun = runs.find((r) => r.id === selectedRunId) ?? runs[0]
  const missing = missingRequired(workflow, workflow.activeEnvironment)
  const errors = workflow.diagnostics.filter((d) => d.severity === "error").length
  const dockerDown = health.data && !health.data.docker

  const start = async (action: RunAction) => {
    setStarting(action)
    try {
      await useWorkflowStore.getState().flush()
      const run = await api.startRun(workflow.id, action, workflow.activeEnvironment)
      const s = useWorkflowStore.getState()
      if (!s.runs.some((r) => r.id === run.id)) useWorkflowStore.setState({ runs: [run, ...s.runs] })
      setSelectedRunId(run.id)
      setView("logs")
    } catch (err) {
      toast.error(`Could not start ${action}`, { description: errorMessage(err) })
    } finally {
      setStarting(null)
    }
  }

  return (
    // Narrow bottom panels (< 48rem, the panel is the container) stack the controls above the log and scroll as one
    // page; wide ones put them side by side, each scrolling on its own.
    <div className="grid h-full min-h-0 grid-rows-[auto_minmax(20rem,1fr)] overflow-y-auto @3xl:grid-cols-[minmax(17rem,22rem)_minmax(0,1fr)] @3xl:grid-rows-1 @3xl:overflow-hidden">
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
        <SandboxCard />

        <section className="flex flex-col gap-2 rounded-lg border p-2.5">
          <div className="flex items-center gap-2">
            <span className="text-xs font-semibold">Deploy</span>
            <Select
              value={workflow.activeEnvironment ?? NO_ENV}
              onValueChange={(v) => editWorkflow((d) => void (d.activeEnvironment = v === NO_ENV ? undefined : v))}
            >
              <SelectTrigger size="sm" className="ml-auto h-6 min-w-32 text-xs" aria-label="Environment">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NO_ENV}>Defaults only</SelectItem>
                {workflow.environments.map((e) => (
                  <SelectItem key={e.name} value={e.name}>
                    {e.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {missing.length > 0 && (
            <button
              type="button"
              onClick={() => showBottom("variables")}
              className="flex items-start gap-1.5 rounded-md bg-amber-500/10 p-1.5 text-left text-xs text-amber-700 hover:bg-amber-500/15 dark:text-amber-300"
            >
              <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
              <span>
                {missing.length} required variable{missing.length === 1 ? " has" : "s have"} no value:{" "}
                <span className="font-mono">{missing.join(", ")}</span>. Set {missing.length === 1 ? "it" : "them"} in
                Variables &amp; Environments.
              </span>
            </button>
          )}
          {errors > 0 && (
            <button
              type="button"
              onClick={() => showBottom("problems")}
              className="flex items-center gap-1.5 rounded-md bg-destructive/10 p-1.5 text-left text-xs text-destructive hover:bg-destructive/15"
            >
              <TriangleAlert className="size-3.5 shrink-0" />
              {errors} problem{errors === 1 ? "" : "s"} may make the deploy fail — review them.
            </button>
          )}
          <div className="grid grid-cols-4 gap-1">
            {ACTIONS.map(({ action, label, icon: Icon, hint }) => (
              <Tooltip key={action}>
                <TooltipTrigger asChild>
                  <Button
                    size="sm"
                    variant={action === "apply" ? "default" : action === "destroy" ? "outline" : "secondary"}
                    className={cn("h-auto flex-col gap-1 px-1 py-1.5 text-[11px] leading-none [&_svg]:size-3.5", action === "destroy" && "text-destructive")}
                    disabled={!!active || starting !== null || dockerDown}
                    onClick={() => (action === "destroy" ? setConfirmDestroy(true) : void start(action))}
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
                <RunRow key={r.id} run={r} selected={r.id === selectedRun?.id} onSelect={() => setSelectedRunId(r.id)} />
              ))}
            </div>
          )}
        </section>
      </div>

      <Tabs value={view} onValueChange={setView} className="@container flex min-h-0 min-w-0 flex-col gap-0">
        <TabsList variant="line" className="h-8 w-full shrink-0 justify-start overflow-x-auto overflow-y-hidden rounded-none border-b px-2 [scrollbar-width:none]">
          <TabsTrigger value="logs" className="flex-none text-xs">
            Logs
          </TabsTrigger>
          <TabsTrigger value="resources" className="flex-none text-xs">
            Resources <span className="text-muted-foreground tabular-nums">{deployed?.resources.length ?? 0}</span>
          </TabsTrigger>
          <TabsTrigger value="outputs" className="flex-none text-xs">
            Outputs{" "}
            <span className="text-muted-foreground tabular-nums">{Object.keys(deployed?.outputs ?? {}).length}</span>
          </TabsTrigger>
        </TabsList>
        <TabsContent value="logs" className="min-h-0">
          {selectedRun ? (
            <RunLog key={selectedRun.id} run={selectedRun} />
          ) : (
            <Empty className="h-full">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Rocket />
                </EmptyMedia>
                <EmptyTitle>Deploy to your local sandbox</EmptyTitle>
                <EmptyDescription>
                  Runs Terraform inside a private local Floci sandbox — nothing touches your real cloud account. Start
                  with <b>Plan</b> to preview the changes, then <b>Apply</b> to create the resources.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </TabsContent>
        <TabsContent value="resources" className="min-h-0 overflow-auto">
          <DeployedResources />
        </TabsContent>
        <TabsContent value="outputs" className="min-h-0 overflow-auto">
          <DeployedOutputs />
        </TabsContent>
      </Tabs>

      <AlertDialog open={confirmDestroy} onOpenChange={setConfirmDestroy}>
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>Destroy all deployed resources?</AlertDialogTitle>
            <AlertDialogDescription>
              Terraform deletes every resource it created in the sandbox
              {workflow.activeEnvironment ? ` (environment “${workflow.activeEnvironment}”)` : ""}. Your diagram stays as
              it is.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => void start("destroy")}>
              Destroy
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function RunRow({ run, selected, onSelect }: { run: Run; selected: boolean; onSelect: () => void }) {
  const now = useNow(run.status === "running")
  const duration = durationBetween(run.startedAt, run.endedAt, now)
  const Icon = ACTIONS.find((a) => a.action === run.action)?.icon ?? Eye
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
        {run.environment && <span className="font-mono text-[10px] text-muted-foreground">{run.environment}</span>}
        <span className="ml-auto">
          <StatusBadge status={run.status} />
        </span>
      </div>
      <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
        <span>{formatRelative(run.createdAt)}</span>
        {duration !== undefined && <span className="tabular-nums">{formatDuration(duration)}</span>}
        {run.plan && <PlanChips plan={run.plan} />}
        {run.status === "succeeded" && run.action === "validate" && (
          <span className="flex items-center gap-0.5 text-emerald-600 dark:text-emerald-400">
            <CircleCheck className="size-3" /> valid
          </span>
        )}
      </div>
    </button>
  )
}

import { isTauri } from "@tauri-apps/api/core"
import { listen } from "@tauri-apps/api/event"
import {
  ChevronDownIcon,
  CircleAlertIcon,
  CircleCheckIcon,
  CircleMinusIcon,
  CloudCheckIcon,
  CloudUploadIcon,
  DownloadIcon,
  EllipsisIcon,
  FileCheck2Icon,
  GaugeIcon,
  ListChecksIcon,
  PanelRightIcon,
  RadarIcon,
  ScrollTextIcon,
  Trash2Icon,
  UploadIcon,
  XIcon,
} from "lucide-react"
import { lazy, Suspense, useEffect, useState } from "react"
import { useOnceTrue } from "@/components/lazy-module"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { ThemedIcon } from "@/components/arcade/themed-icon"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useSyncedState } from "@/components/use-synced-state"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Run } from "@/lib/api/types"
import type { UserMode } from "@/state/workflow-store"
import { useWorkflowStore } from "@/state/workflow-store"
import { cn } from "@/lib/utils"
import { hasMonitoring, openMonitoringDialog, useMonitoringDialog } from "@/features/monitoring/monitoring-store"
import { formatClock, formatRelative } from "../panels/lib/format"
import { useNow } from "../panels/lib/use-now"
import { closeFocusedFileTab } from "../panels/terraform/close-file-tab"
import { MOD_KEY, runAction } from "./actions"
import { activeRun, deployStatus, justDeployed, lastDeployRun, RUN_LABEL, RUNNING_LABEL } from "./deploy-status"
import { useRunNotices } from "./run-notices"
import { SandboxChip } from "./sandbox-chip"
import { useUiStore } from "./ui-store"
import { closeWorkflow } from "./workflow-actions"

/** How long the Deploy button says "Deployed" after a deploy succeeded, before the status chip takes over. */
const DEPLOYED_FLASH_MS = 2500

// The container workspaces' dialogs load the first time they open.
const MonitoringDialog = lazy(() => import("@/features/monitoring/monitoring-dialog").then((m) => ({ default: m.MonitoringDialog })))
const ConvertKubernetesDialog = lazy(() =>
  import("@/features/docker/convert-kubernetes-dialog").then((m) => ({ default: m.ConvertKubernetesDialog })),
)

function WorkflowName() {
  const name = useWorkflowStore((s) => s.workflow?.name ?? "")
  const update = useWorkflowStore((s) => s.update)
  const [draft, setDraft] = useSyncedState(name)
  const commit = () => {
    const next = draft.trim()
    if (!next) setDraft(name)
    else if (next !== name) update((d) => ({ ...d, name: next }))
  }
  return (
    // The hidden mirror sizes the input to its text so the save status sits right after the name.
    <div className="inline-grid max-w-64 min-w-16 shrink items-center">
      <span aria-hidden className="invisible col-start-1 row-start-1 truncate px-1.5 font-heading text-sm font-medium whitespace-pre">
        {draft || " "}
      </span>
      <input
        size={1}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") e.currentTarget.blur()
          if (e.key === "Escape") {
            setDraft(name)
            e.currentTarget.blur()
          }
        }}
        aria-label="Workflow name"
        title={draft}
        className="col-start-1 row-start-1 h-7 w-full min-w-0 truncate rounded-md bg-transparent px-1.5 font-heading text-sm font-medium outline-none hover:bg-muted focus:bg-muted focus:ring-2 focus:ring-ring/50"
      />
    </div>
  )
}

/** Rendered only while the tooltip is open, so the relative time is fresh. */
function SavedAgo({ iso }: { iso: string }) {
  return <>Saved · {formatRelative(iso) || "just now"}</>
}

/** Compact save status next to the name: an icon when saved, short text only while saving or failing. */
function SaveState() {
  const saving = useWorkflowStore((s) => s.saving)
  const dirty = useWorkflowStore((s) => s.dirty)
  const saveError = useWorkflowStore((s) => s.saveError)
  const updatedAt = useWorkflowStore((s) => s.workflow?.updatedAt ?? "")
  const flush = useWorkflowStore((s) => s.flush)
  if (saveError)
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            onClick={() => void flush()}
            className="flex h-6 shrink-0 items-center gap-1 rounded-md px-1.5 text-xs text-destructive hover:bg-destructive/10"
          >
            <CircleAlertIcon className="size-3.5" />
            Not saved · Retry
          </button>
        </TooltipTrigger>
        <TooltipContent className="max-w-80">{saveError}</TooltipContent>
      </Tooltip>
    )
  if (saving || dirty)
    return (
      <span role="status" className="flex h-6 shrink-0 items-center gap-1 px-1 text-xs text-muted-foreground">
        <Spinner className="size-3" />
        Saving…
      </span>
    )
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span role="status" aria-label="All changes saved" tabIndex={0} className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50">
          <CloudCheckIcon className="size-3.5" />
        </span>
      </TooltipTrigger>
      <TooltipContent>
        <SavedAgo iso={updatedAt} />
      </TooltipContent>
    </Tooltip>
  )
}

function ProblemsBadge() {
  const diagnostics = useWorkflowStore((s) => s.workflow?.diagnostics)
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const errors = (diagnostics ?? []).filter((d) => d.severity === "error").length
  const warnings = (diagnostics ?? []).filter((d) => d.severity === "warning").length
  const count = errors + warnings
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size={count > 0 ? "sm" : "icon-sm"}
          onClick={() => showBottom("problems")}
          aria-label={count > 0 ? `${count} problem${count === 1 ? "" : "s"}` : "No problems"}
          className={cn(errors > 0 ? "text-destructive" : warnings > 0 ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground")}
        >
          {count > 0 ? <ThemedIcon icon={CircleAlertIcon} pixel="warning" /> : <ThemedIcon icon={ListChecksIcon} pixel="list" />}
          {/* A zero is noise: the count only shows when there is something to look at. */}
          {count > 0 && count}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {count === 0
          ? "No problems"
          : `${errors} error${errors === 1 ? "" : "s"}, ${warnings} warning${warnings === 1 ? "" : "s"}`}
      </TooltipContent>
    </Tooltip>
  )
}

/** What the destroy action is called in each kind of workspace (menu item; the confirmation dialog explains it). */
function destroyLabel(kind: string | undefined) {
  if (kind === "docker") return "Stop and remove the containers…"
  if (kind === "kubernetes") return "Delete what Deploy applied…"
  return "Destroy everything in the sandbox…"
}

/** True for a moment after a Deploy of the open workflow succeeded (status events as they arrive). */
function useJustDeployed() {
  const [shown, setShown] = useState(false)
  useEffect(() => {
    let timer: number | undefined
    const off = useWorkflowStore.subscribe((s, prev) => {
      if (s.workflow?.id !== prev.workflow?.id || !justDeployed(prev.runs, s.runs)) return
      setShown(true)
      window.clearTimeout(timer)
      timer = window.setTimeout(() => setShown(false), DEPLOYED_FLASH_MS)
    })
    return () => {
      off()
      window.clearTimeout(timer)
    }
  }, [])
  return shown
}

/**
 * The last Deploy or Destroy at a glance ("Deployed 2m ago", "Deploy failed"); a click opens its log in the Deploy
 * tab. Green only for a deploy that worked, red only for one that failed; nothing before the first deploy.
 */
function DeployStatusChip({ run }: { run: Run }) {
  const now = useNow(true, 30_000)
  const { label, tone } = deployStatus(run, now)
  const Icon = tone === "ok" ? CircleCheckIcon : tone === "failed" ? CircleAlertIcon : CircleMinusIcon
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className={cn(
            "gap-1 px-2 text-xs font-normal text-muted-foreground",
            tone === "failed" && "text-destructive hover:text-destructive",
          )}
          onClick={() => useWorkflowStore.getState().showRun(run.id)}
          data-testid="deploy-status"
        >
          <Icon className={cn(tone === "ok" && "text-emerald-600 dark:text-emerald-400")} />
          {label}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {RUN_LABEL[run.action]} {run.status} at {formatClock(run.endedAt ?? run.createdAt)}: open its log
      </TooltipContent>
    </Tooltip>
  )
}

/**
 * Deploy as a split button: the primary action, and next to it a menu with the rest of the run lifecycle (Validate
 * and Plan for Terraform, the run log) and, last, apart and red, the destructive one. One button in the bar instead
 * of five, and destroy can't be hit by a slip of the pointer: it takes opening the menu and a confirmation.
 * The button follows the run in progress ("Deploying…", "Planning…") and says "Deployed" for a moment once a deploy
 * worked; then the status chip before it keeps the last result.
 */
function DeployButton() {
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  const terraform = kind !== "docker" && kind !== "kubernetes"
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const active = useWorkflowStore((s) => activeRun(s.runs))
  const last = useWorkflowStore((s) => lastDeployRun(s.runs))
  const deployed = useJustDeployed()
  const busy = active !== undefined
  return (
    <>
      {last && !busy && !deployed && <DeployStatusChip run={last} />}
      <div className="flex items-center">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button size="sm" className="rounded-r-none" disabled={busy} onClick={() => void runAction("apply")}>
              {busy ? <Spinner /> : deployed ? <CircleCheckIcon /> : <ThemedIcon icon={CloudUploadIcon} glyph="rocket" />}
              {active ? RUNNING_LABEL[active.action] : deployed ? "Deployed" : "Deploy"}
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            {kind === "kubernetes"
              ? "Apply every source to this workflow's local Kubernetes cluster (creates it when needed)"
              : kind === "docker"
                ? "Build and start every service on this machine (docker compose up)"
                : "Create everything in your local sandbox"}
          </TooltipContent>
        </Tooltip>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="sm" className="rounded-l-none border-l border-primary-foreground/20 px-1.5" aria-label="More deploy actions">
              <ChevronDownIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72">
            {terraform && (
              <>
                <DropdownMenuItem disabled={busy} onSelect={() => void runAction("validate")}>
                  <FileCheck2Icon />
                  Validate
                  <span className="ml-auto text-xs text-muted-foreground">check for mistakes</span>
                </DropdownMenuItem>
                <DropdownMenuItem disabled={busy} onSelect={() => void runAction("plan")}>
                  <ListChecksIcon />
                  Plan
                  <span className="ml-auto text-xs text-muted-foreground">preview changes</span>
                </DropdownMenuItem>
              </>
            )}
            <DropdownMenuItem onSelect={() => showBottom("deploy")}>
              <ScrollTextIcon />
              Runs and logs
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" disabled={busy} onSelect={() => useUiStore.getState().setConfirmDestroy(true)}>
              <Trash2Icon />
              {destroyLabel(kind)}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </>
  )
}

/**
 * The workflow's secondary actions, out of the bar: import and export, and for container workspaces monitoring and
 * the Kubernetes conversion (their dialogs are rendered here too). Learn, theme and Settings are in the navigation rail.
 */
function WorkflowMenu() {
  // The kind and the monitoring flag, not the workflow: the bar re-renders the menu on every edit otherwise.
  const open = useWorkflowStore((s) => s.workflow !== null)
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  const monitored = useWorkflowStore((s) => hasMonitoring(s.workflow))
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const setConvertOpen = useUiStore((s) => s.setConvertKubernetes)
  // Terraform workflows have no kind: guard on the workflow being open, not on the kind.
  if (!open) return null
  const docker = kind === "docker"
  const containers = docker || kind === "kubernetes"
  return (
    <>
      <DropdownMenu>
        <Tooltip>
          <TooltipTrigger asChild>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label="Workflow actions" data-testid="topbar-more">
                <EllipsisIcon />
              </Button>
            </DropdownMenuTrigger>
          </TooltipTrigger>
          <TooltipContent>Import, export{containers ? ", monitoring" : ""} and more</TooltipContent>
        </Tooltip>
        <DropdownMenuContent align="end" className="w-64">
          <DropdownMenuItem onSelect={() => openDialog("import")}>
            <UploadIcon />
            Import a project…
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => openDialog("export")}>
            <DownloadIcon />
            {containers ? "Export the project files…" : "Export Terraform…"}
          </DropdownMenuItem>
          {containers && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem data-testid="add-monitoring" onSelect={openMonitoringDialog}>
                <RadarIcon className={monitored ? "text-emerald-600 dark:text-emerald-400" : undefined} />
                {monitored ? "Monitoring: Grafana, Prometheus, alerts…" : "Add monitoring…"}
              </DropdownMenuItem>
            </>
          )}
          {docker && (
            <DropdownMenuItem data-testid="convert-kubernetes" onSelect={() => setConvertOpen(true)}>
              <TechIcon icon="kubernetes" size={16} />
              Convert to Kubernetes…
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => void closeWorkflow()}>
            <XIcon />
            Close workflow
            <DropdownMenuShortcut>{MOD_KEY} W</DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {containers && <ContainerDialogs docker={docker} />}
    </>
  )
}

/** The container workspaces' dialogs: they read the whole workflow, so only those workspaces pay for following it. */
function ContainerDialogs({ docker }: { docker: boolean }) {
  const workflow = useWorkflowStore((s) => s.workflow)
  const convertOpen = useUiStore((s) => s.convertKubernetes)
  const setConvertOpen = useUiStore((s) => s.setConvertKubernetes)
  // Mounted from the first open on, so later closes still animate.
  const monitoringUsed = useOnceTrue(useMonitoringDialog((s) => s.open))
  const convertUsed = useOnceTrue(docker && convertOpen)
  if (!workflow) return null
  return (
    <>
      {monitoringUsed && (
        <Suspense fallback={null}>
          <MonitoringDialog workflow={workflow} />
        </Suspense>
      )}
      {docker && convertUsed && (
        <Suspense fallback={null}>
          <ConvertKubernetesDialog workflow={workflow} open={convertOpen} onOpenChange={setConvertOpen} />
        </Suspense>
      )}
    </>
  )
}

/**
 * The editor's bar, kept to what a workflow needs at a glance: its name and save state on the left; on the right the
 * experience level, the sandbox, Test, the last deploy's status and Deploy, the problems count, the workflow menu and
 * the inspector toggle. It also says how the runs this window started ended (useRunNotices).
 */
export function TopBar() {
  const mode = useWorkflowStore((s) => s.mode)
  const setMode = useWorkflowStore((s) => s.setMode)
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const inspectorOpen = useUiStore((s) => s.inspectorOpen)
  const setInspectorOpen = useUiStore((s) => s.setInspectorOpen)
  useRunNotices()

  // ⌘W / Ctrl+W closes the focused file tab of the Code panel, else the workflow. On macOS the native menu owns ⌘W
  // and emits close-workflow instead.
  useEffect(() => {
    const close = () => {
      if (!closeFocusedFileTab()) void closeWorkflow()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "w" && (e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey) {
        e.preventDefault()
        close()
      }
    }
    window.addEventListener("keydown", onKey)
    if (!isTauri()) return () => window.removeEventListener("keydown", onKey)
    const unlisten = listen("close-workflow", close)
    return () => {
      window.removeEventListener("keydown", onKey)
      void unlisten.then((off) => off())
    }
  }, [])

  return (
    <header className="flex h-12 shrink-0 items-center gap-2 border-b px-2">
      <div className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden">
        <SidebarTrigger />
        <Separator orientation="vertical" className="mx-1 h-5" />
        <WorkflowName />
        <SaveState />
      </div>

      <div className="flex shrink-0 items-center gap-1.5">
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={mode}
          onValueChange={(v) => v && setMode(v as UserMode)}
          aria-label="Experience level"
        >
          <Tooltip>
            <TooltipTrigger asChild>
              <ToggleGroupItem value="guided">Guided</ToggleGroupItem>
            </TooltipTrigger>
            <TooltipContent>Essential settings, recommended connections applied for you</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <ToggleGroupItem value="pro">Pro</ToggleGroupItem>
            </TooltipTrigger>
            <TooltipContent>Every attribute, expressions and meta-arguments</TooltipContent>
          </Tooltip>
        </ToggleGroup>
        <SandboxChip />
        <Separator orientation="vertical" className="mx-1 h-5" />
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="outline" size="sm" aria-label="Load test" onClick={() => showBottom("loadtest")}>
              <ThemedIcon icon={GaugeIcon} glyph="bolt" />
              Test
            </Button>
          </TooltipTrigger>
          <TooltipContent>Send traffic to what you deployed and watch it hold up</TooltipContent>
        </Tooltip>
        <DeployButton />
        <Separator orientation="vertical" className="mx-1 h-5" />
        <ProblemsBadge />
        <WorkflowMenu />
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={inspectorOpen ? "Hide inspector" : "Show inspector"}
              onClick={() => setInspectorOpen(!inspectorOpen)}
            >
              <ThemedIcon icon={PanelRightIcon} pixel="panel" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            {inspectorOpen ? "Hide inspector" : "Show inspector"}
            <Kbd className="ml-1.5">{MOD_KEY} I</Kbd>
          </TooltipContent>
        </Tooltip>
      </div>
    </header>
  )
}

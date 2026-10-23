import { isTauri } from "@tauri-apps/api/core"
import { listen } from "@tauri-apps/api/event"
import {
  ActivityIcon,
  CircleAlertIcon,
  CloudCheckIcon,
  CloudUploadIcon,
  DownloadIcon,
  FileCheck2Icon,
  ListChecksIcon,
  PanelRightIcon,
  SearchIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react"
import { useEffect, useState } from "react"
import { Checkbox } from "@/components/ui/checkbox"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { ThemedIcon } from "@/components/arcade/themed-icon"
import { ThemeToggle } from "@/components/theme-toggle"
import { SettingsButton } from "@/features/settings/settings-button"
import { useSyncedState } from "@/components/use-synced-state"
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
import { Kbd } from "@/components/ui/kbd"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { UserMode } from "@/state/workflow-store"
import { useWorkflowStore } from "@/state/workflow-store"
import { cn } from "@/lib/utils"
import { ConvertKubernetesDialog } from "@/features/docker/convert-kubernetes-dialog"
import { MonitoringAction } from "@/features/monitoring/monitoring-action"
import { formatRelative } from "../panels/lib/format"
import { closeFocusedFileTab } from "../panels/terraform/close-file-tab"
import { MOD_KEY, runAction, useRunBusy } from "./actions"
import { SandboxChip } from "./sandbox-chip"
import { useUiStore } from "./ui-store"
import { closeWorkflow } from "./workflow-actions"

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
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => showBottom("problems")}
          className={cn(errors > 0 ? "text-destructive" : warnings > 0 ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground")}
        >
          {errors + warnings > 0 ? <ThemedIcon icon={CircleAlertIcon} pixel="warning" /> : <ThemedIcon icon={ListChecksIcon} pixel="list" />}
          {errors + warnings}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {errors} error{errors === 1 ? "" : "s"}, {warnings} warning{warnings === 1 ? "" : "s"}
      </TooltipContent>
    </Tooltip>
  )
}

function IconAction({ label, onClick, children, kbd }: { label: string; onClick: () => void; children: React.ReactNode; kbd?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-sm" onClick={onClick} aria-label={label}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {label}
        {kbd && <Kbd className="ml-1.5">{kbd}</Kbd>}
      </TooltipContent>
    </Tooltip>
  )
}

/** Docker workspaces: turns the compose project into a new Kubernetes workflow (manifests, Kustomize or Helm). */
function ConvertKubernetesAction() {
  const workflow = useWorkflowStore((s) => s.workflow)
  const open = useUiStore((s) => s.convertKubernetes)
  const setOpen = useUiStore((s) => s.setConvertKubernetes)
  if (!workflow) return null
  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="sm" aria-label="Convert to Kubernetes" data-testid="convert-kubernetes" onClick={() => setOpen(true)}>
            <TechIcon icon="kubernetes" size={16} />
            <span className="hidden 2xl:inline">To Kubernetes</span>
          </Button>
        </TooltipTrigger>
        <TooltipContent>Convert to Kubernetes: a new workflow with manifests, Kustomize or a Helm chart</TooltipContent>
      </Tooltip>
      <ConvertKubernetesDialog workflow={workflow} open={open} onOpenChange={setOpen} />
    </>
  )
}

export function TopBar() {
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  const docker = kind === "docker"
  const kube = kind === "kubernetes"
  const mode = useWorkflowStore((s) => s.mode)
  const setMode = useWorkflowStore((s) => s.setMode)
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const inspectorOpen = useUiStore((s) => s.inspectorOpen)
  const setInspectorOpen = useUiStore((s) => s.setInspectorOpen)
  const setCommandOpen = useUiStore((s) => s.setCommandOpen)
  const setConfirmDestroy = useUiStore((s) => s.setConfirmDestroy)
  const busy = useRunBusy()

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
      <div className="flex min-w-0 flex-1 items-center gap-1">
        <SidebarTrigger />
        <Separator orientation="vertical" className="mx-1 h-5" />
        <WorkflowName />
        <SaveState />
      </div>

      <div className="flex shrink-0 items-center gap-2">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="outline"
              size="sm"
              className="justify-start text-muted-foreground 2xl:w-48"
              aria-label="Search or run a command"
              onClick={() => setCommandOpen(true)}
            >
              <ThemedIcon icon={SearchIcon} pixel="search" />
              <span className="hidden 2xl:inline">Search or run…</span>
              <Kbd className="ml-auto">{MOD_KEY} K</Kbd>
            </Button>
          </TooltipTrigger>
          <TooltipContent className="2xl:hidden">Search resources, actions and workflows</TooltipContent>
        </Tooltip>
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
      </div>

      <div className="flex flex-1 items-center justify-end gap-1">
        <Separator orientation="vertical" className="mx-1 h-5" />
        {(docker || kube) && <MonitoringAction />}
        {docker && <ConvertKubernetesAction />}
        {!docker && !kube && (
          <>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="sm" disabled={busy} aria-label="Validate" onClick={() => void runAction("validate")}>
                  <ThemedIcon icon={FileCheck2Icon} pixel="check" />
                  <span className="hidden 2xl:inline">Validate</span>
                </Button>
              </TooltipTrigger>
              <TooltipContent>Check the Terraform for mistakes</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="sm" disabled={busy} aria-label="Plan" onClick={() => void runAction("plan")}>
                  <ThemedIcon icon={ListChecksIcon} pixel="list" />
                  <span className="hidden 2xl:inline">Plan</span>
                </Button>
              </TooltipTrigger>
              <TooltipContent>Preview what a deploy would change</TooltipContent>
            </Tooltip>
          </>
        )}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button size="sm" disabled={busy} onClick={() => void runAction("apply")}>
              {busy ? <Spinner /> : <ThemedIcon icon={CloudUploadIcon} glyph="rocket" />}
              Deploy
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            {kube
              ? "Apply every source to this workflow's local Kubernetes cluster (creates it when needed)"
              : docker
                ? "Build and start every service on this machine (docker compose up)"
                : "Create everything in your local sandbox"}
          </TooltipContent>
        </Tooltip>
        <IconAction
          label={
            kube
              ? "Delete what Deploy applied to the local cluster"
              : docker
                ? "Stop and remove the containers (docker compose down)"
                : "Destroy everything in the sandbox"
          }
          onClick={() => setConfirmDestroy(true)}
        >
          <ThemedIcon icon={Trash2Icon} pixel="trash" />
        </IconAction>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="outline" size="sm" aria-label="Load test" onClick={() => showBottom("loadtest")}>
              <ThemedIcon icon={ActivityIcon} glyph="bolt" />
              <span className="hidden 2xl:inline">Test</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>Send traffic to what you deployed and watch it hold up</TooltipContent>
        </Tooltip>
        <Separator orientation="vertical" className="mx-1 h-5" />
        <ProblemsBadge />
        <IconAction label="Import a project (Terraform, Docker Compose or Kubernetes)" onClick={() => openDialog("import")}>
          <ThemedIcon icon={UploadIcon} pixel="upload" />
        </IconAction>
        <IconAction label={docker || kube ? "Export the project files" : "Export Terraform"} onClick={() => openDialog("export")}>
          <ThemedIcon icon={DownloadIcon} pixel="download" />
        </IconAction>
        <ThemeToggle />
        <SettingsButton />
        <IconAction label={inspectorOpen ? "Hide inspector" : "Show inspector"} onClick={() => setInspectorOpen(!inspectorOpen)} kbd={`${MOD_KEY} I`}>
          <ThemedIcon icon={PanelRightIcon} pixel="panel" />
        </IconAction>
      </div>
    </header>
  )
}

export function DestroyDialog() {
  const open = useUiStore((s) => s.confirmDestroy)
  const setOpen = useUiStore((s) => s.setConfirmDestroy)
  const name = useWorkflowStore((s) => s.workflow?.name)
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  const docker = kind === "docker"
  const kube = kind === "kubernetes"
  const [removeVolumes, setRemoveVolumes] = useState(false)
  const [deleteCluster, setDeleteCluster] = useState(false)
  return (
    <AlertDialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        // Deleting data is opt-in every time.
        if (!o) {
          setRemoveVolumes(false)
          setDeleteCluster(false)
        }
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {docker ? `Stop “${name}”?` : kube ? `Remove “${name}” from the cluster?` : `Destroy everything in “${name}”?`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {docker
              ? "docker compose down stops and removes this workspace's containers and networks. Your compose files stay untouched and you can deploy again at any time."
              : kube
                ? "Deletes every object Deploy applied (and uninstalls Helm releases) from this workflow's local cluster. Your manifests stay untouched and you can deploy again at any time."
                : "Terraform will delete every deployed resource from your local sandbox. Your design stays untouched and you can deploy again at any time."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {docker && (
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={removeVolumes} onCheckedChange={(c) => setRemoveVolumes(c === true)} />
            Also delete the volumes (database data and other saved files)
          </label>
        )}
        {kube && (
          <div className="flex flex-col gap-2">
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={removeVolumes}
                onCheckedChange={(c) => setRemoveVolumes(c === true)}
                data-testid="kube-remove-volumes"
              />
              Also delete the PersistentVolumeClaims (database data and other saved files)
            </label>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={deleteCluster}
                onCheckedChange={(c) => setDeleteCluster(c === true)}
                data-testid="kube-delete-cluster"
              />
              Also delete the cluster (the next Deploy creates a fresh one)
            </label>
          </div>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            data-testid="destroy-confirm"
            onClick={() =>
              void runAction("destroy", docker ? { removeVolumes } : kube ? { removeVolumes, deleteCluster } : {})
            }
          >
            {docker ? "Stop and remove" : kube ? (deleteCluster ? "Remove and delete cluster" : "Remove") : "Destroy"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

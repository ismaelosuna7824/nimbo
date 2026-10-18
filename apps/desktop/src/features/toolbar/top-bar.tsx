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
import { useEffect } from "react"
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
import { formatRelative } from "../panels/lib/format"
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
          {errors + warnings > 0 ? <CircleAlertIcon /> : <ListChecksIcon />}
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

export function TopBar() {
  const mode = useWorkflowStore((s) => s.mode)
  const setMode = useWorkflowStore((s) => s.setMode)
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const inspectorOpen = useUiStore((s) => s.inspectorOpen)
  const setInspectorOpen = useUiStore((s) => s.setInspectorOpen)
  const setCommandOpen = useUiStore((s) => s.setCommandOpen)
  const setConfirmDestroy = useUiStore((s) => s.setConfirmDestroy)
  const busy = useRunBusy()

  // ⌘W / Ctrl+W closes the workflow. On macOS the native menu owns ⌘W and emits close-workflow instead.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "w" && (e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey) {
        e.preventDefault()
        void closeWorkflow()
      }
    }
    window.addEventListener("keydown", onKey)
    if (!isTauri()) return () => window.removeEventListener("keydown", onKey)
    const unlisten = listen("close-workflow", () => void closeWorkflow())
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
              <SearchIcon />
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
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="sm" disabled={busy} aria-label="Validate" onClick={() => void runAction("validate")}>
              <FileCheck2Icon />
              <span className="hidden 2xl:inline">Validate</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>Check the Terraform for mistakes</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="sm" disabled={busy} aria-label="Plan" onClick={() => void runAction("plan")}>
              <ListChecksIcon />
              <span className="hidden 2xl:inline">Plan</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>Preview what a deploy would change</TooltipContent>
        </Tooltip>
        <Button size="sm" disabled={busy} onClick={() => void runAction("apply")}>
          {busy ? <Spinner /> : <CloudUploadIcon />}
          Deploy
        </Button>
        <IconAction label="Destroy everything in the sandbox" onClick={() => setConfirmDestroy(true)}>
          <Trash2Icon />
        </IconAction>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="outline" size="sm" aria-label="Load test" onClick={() => showBottom("loadtest")}>
              <ActivityIcon />
              <span className="hidden 2xl:inline">Test</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>Send traffic to what you deployed and watch it hold up</TooltipContent>
        </Tooltip>
        <Separator orientation="vertical" className="mx-1 h-5" />
        <ProblemsBadge />
        <IconAction label="Import Terraform" onClick={() => openDialog("import")}>
          <UploadIcon />
        </IconAction>
        <IconAction label="Export Terraform" onClick={() => openDialog("export")}>
          <DownloadIcon />
        </IconAction>
        <ThemeToggle />
        <SettingsButton />
        <IconAction label={inspectorOpen ? "Hide inspector" : "Show inspector"} onClick={() => setInspectorOpen(!inspectorOpen)} kbd={`${MOD_KEY} I`}>
          <PanelRightIcon />
        </IconAction>
      </div>
    </header>
  )
}

export function DestroyDialog() {
  const open = useUiStore((s) => s.confirmDestroy)
  const setOpen = useUiStore((s) => s.setConfirmDestroy)
  const name = useWorkflowStore((s) => s.workflow?.name)
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Destroy everything in “{name}”?</AlertDialogTitle>
          <AlertDialogDescription>
            Terraform will delete every deployed resource from your local sandbox. Your design stays untouched and you can
            deploy again at any time.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={() => void runAction("destroy")}>
            Destroy
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

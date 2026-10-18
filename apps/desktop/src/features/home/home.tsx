import { useQuery } from "@tanstack/react-query"
import {
  ArchiveIcon,
  ArchiveRestoreIcon,
  ChevronRightIcon,
  CopyIcon,
  EllipsisIcon,
  LayersIcon,
  PaletteIcon,
  PencilIcon,
  PlusIcon,
  SearchIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react"
import { useRef, useState } from "react"
import { AppLogo } from "@/components/app-logo"
import { CloudBadge } from "@/components/cloud"
import { ThemeToggle } from "@/components/theme-toggle"
import { type IconTarget, WorkflowIconDialog } from "@/components/workflow-icon/icon-picker"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import { SettingsButton } from "@/features/settings/settings-button"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Kbd } from "@/components/ui/kbd"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api/client"
import type { WorkflowSummary } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { MOD_KEY, openWorkflow } from "../toolbar/actions"
import { DeleteWorkflowDialog } from "../toolbar/delete-workflow-dialog"
import { useUiStore } from "../toolbar/ui-store"
import { archiveWorkflow, duplicateWorkflow, renameWorkflow, restoreWorkflow } from "../toolbar/workflow-actions"
import { ConnectionsSection } from "../connections/home-section"
import { HealthBanner } from "./health-banner"

const relative = new Intl.RelativeTimeFormat("en", { numeric: "auto" })

function timeAgo(iso: string) {
  const seconds = (new Date(iso).getTime() - Date.now()) / 1000
  const steps: [Intl.RelativeTimeFormatUnit, number][] = [
    ["year", 31536000],
    ["month", 2592000],
    ["week", 604800],
    ["day", 86400],
    ["hour", 3600],
    ["minute", 60],
  ]
  for (const [unit, size] of steps) if (Math.abs(seconds) >= size) return relative.format(Math.round(seconds / size), unit)
  return "just now"
}

export function Home() {
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const setCommandOpen = useUiStore((s) => s.setCommandOpen)
  const workflows = useQuery({ queryKey: ["workflows"], queryFn: api.listWorkflows, retry: false })
  const [deleting, setDeleting] = useState<WorkflowSummary | null>(null)
  const [iconFor, setIconFor] = useState<IconTarget | null>(null)
  const active = (workflows.data ?? []).filter((w) => !w.archived)
  const archived = (workflows.data ?? []).filter((w) => w.archived)

  return (
    <div className="h-svh overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-8 px-8 py-10">
        <header className="flex items-center gap-3">
          <AppLogo className="size-10" />
          <div>
            <h1 className="font-heading text-xl font-semibold">Nimbo</h1>
            <p className="text-sm text-muted-foreground">Design, deploy and load-test cloud infrastructure on your laptop.</p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <Button variant="ghost" size="sm" className="text-muted-foreground" onClick={() => setCommandOpen(true)}>
              <SearchIcon />
              <Kbd>{MOD_KEY} K</Kbd>
            </Button>
            <ThemeToggle />
            <SettingsButton />
          </div>
        </header>

        <HealthBanner />

        <div className="flex flex-wrap gap-3">
          <Button size="lg" onClick={() => openDialog("new-workflow")}>
            <PlusIcon />
            New workflow
          </Button>
          <Button size="lg" variant="outline" onClick={() => openDialog("import")}>
            <UploadIcon />
            Import Terraform
          </Button>
        </div>

        <section className="flex flex-col gap-3">
          <h2 className="text-sm font-medium text-muted-foreground">Recent workflows</h2>
          {workflows.isPending ? (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {Array.from({ length: 3 }, (_, i) => (
                <Skeleton key={i} className="h-32 rounded-xl" />
              ))}
            </div>
          ) : workflows.isError ? (
            <p className="text-sm text-muted-foreground">Workflows will appear here once the engine is reachable.</p>
          ) : active.length === 0 ? (
            <Empty className="border border-dashed">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <LayersIcon />
                </EmptyMedia>
                <EmptyTitle>{archived.length > 0 ? "No active workflows" : "No workflows yet"}</EmptyTitle>
                <EmptyDescription>
                  {archived.length > 0
                    ? "Everything you built is archived below — restore one to keep working, or start something new."
                    : "A workflow is one piece of infrastructure — like an API with a queue and a database. Pick a template to see how it fits together, then deploy it to your local sandbox."}
                </EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Button onClick={() => openDialog("new-workflow")}>Browse templates</Button>
              </EmptyContent>
            </Empty>
          ) : (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {active.map((w) => (
                <WorkflowCard key={w.id} workflow={w} onDelete={() => setDeleting(w)} onChangeIcon={() => setIconFor(w)} />
              ))}
            </div>
          )}
          {archived.length > 0 && <ArchivedSection workflows={archived} onDelete={setDeleting} />}
        </section>

        <ConnectionsSection />
      </div>
      <DeleteWorkflowDialog workflow={deleting} onClose={() => setDeleting(null)} />
      <WorkflowIconDialog workflow={iconFor} onClose={() => setIconFor(null)} />
    </div>
  )
}

function WorkflowCard({
  workflow: w,
  onDelete,
  onChangeIcon,
}: {
  workflow: WorkflowSummary
  onDelete: () => void
  onChangeIcon: () => void
}) {
  /** The name being typed; null when not renaming. */
  const [draft, setDraft] = useState<string | null>(null)
  const cancelled = useRef(false)
  const commitRename = (value: string) => {
    setDraft(null)
    const next = value.trim()
    if (!cancelled.current && next && next !== w.name) void renameWorkflow(w.id, next)
  }

  return (
    <div className="group relative flex min-h-32 flex-col rounded-xl border bg-card transition-colors hover:border-primary/40">
      {draft !== null ? (
        <div className="flex flex-1 flex-col gap-1.5 p-4">
          <Input
            autoFocus
            value={draft}
            aria-label="Workflow name"
            onChange={(e) => setDraft(e.target.value)}
            onFocus={(e) => e.currentTarget.select()}
            onBlur={(e) => commitRename(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") e.currentTarget.blur()
              if (e.key === "Escape") {
                e.stopPropagation()
                cancelled.current = true
                e.currentTarget.blur()
              }
            }}
            className="h-8 font-medium"
          />
          <span className="text-xs text-muted-foreground">Enter to save · Esc to cancel</span>
        </div>
      ) : (
        <button type="button" onClick={() => void openWorkflow(w.id)} className="flex flex-1 flex-col gap-1.5 p-4 text-left">
          <span className="flex items-center gap-2.5 pr-8">
            <WorkflowIconTile icon={w.icon} size={32} />
            <span className="min-w-0 font-medium">{w.name}</span>
          </span>
          {w.description && <span className="line-clamp-2 text-xs text-muted-foreground">{w.description}</span>}
          <span className="mt-auto flex flex-wrap items-center gap-1.5 pt-2">
            {w.clouds.map((c) => (
              <CloudBadge key={c} cloud={c} />
            ))}
            <span className="text-xs text-muted-foreground">
              {w.nodeCount} resource{w.nodeCount === 1 ? "" : "s"} · {timeAgo(w.updatedAt)}
            </span>
          </span>
        </button>
      )}
      {draft === null && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100"
              aria-label={`Actions for ${w.name}`}
            >
              <EllipsisIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              onSelect={() => {
                cancelled.current = false
                setDraft(w.name)
              }}
            >
              <PencilIcon />
              Rename
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onChangeIcon}>
              <PaletteIcon />
              Change icon…
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void duplicateWorkflow(w.id, w.name)}>
              <CopyIcon />
              Duplicate
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void archiveWorkflow(w.id, w.name)}>
              <ArchiveIcon />
              Archive
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={onDelete}>
              <Trash2Icon />
              Delete…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  )
}

function ArchivedSection({ workflows, onDelete }: { workflows: WorkflowSummary[]; onDelete: (w: WorkflowSummary) => void }) {
  return (
    <Collapsible className="mt-2">
      <CollapsibleTrigger asChild>
        <Button variant="ghost" size="sm" className="group/archived -ml-2 text-muted-foreground">
          <ChevronRightIcon className="transition-transform group-data-[state=open]/archived:rotate-90" />
          Archived ({workflows.length})
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <p className="mt-1 mb-2 text-xs text-muted-foreground">
          Archived workflows keep their design and sandbox data, but their sandbox stays stopped until you restore them.
        </p>
        <ul className="flex flex-col divide-y rounded-xl border">
          {workflows.map((w) => (
            <li key={w.id} className="flex items-center gap-3 px-4 py-2.5">
              <WorkflowIconTile icon={w.icon} size={28} className="opacity-70" />
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="truncate text-sm font-medium">{w.name}</span>
                <span className="text-xs text-muted-foreground">
                  {w.nodeCount} resource{w.nodeCount === 1 ? "" : "s"} · archived {timeAgo(w.updatedAt)}
                </span>
              </div>
              <Button variant="outline" size="sm" onClick={() => void restoreWorkflow(w.id, w.name)}>
                <ArchiveRestoreIcon />
                Restore
              </Button>
              <Button variant="ghost" size="icon-sm" aria-label={`Delete ${w.name}`} className="text-muted-foreground hover:text-destructive" onClick={() => onDelete(w)}>
                <Trash2Icon />
              </Button>
            </li>
          ))}
        </ul>
      </CollapsibleContent>
    </Collapsible>
  )
}

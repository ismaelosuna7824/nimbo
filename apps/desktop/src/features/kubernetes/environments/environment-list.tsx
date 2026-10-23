import { ClipboardCheck, Copy, MoreHorizontal, Pencil, Repeat, Trash2 } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { switchEnvironment } from "@/features/environments/actions"
import type { EnvironmentsView } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import type { EnvDialog } from "./dialogs"

/** Base first, then every environment; selecting one opens it in the Edit pane. */
export function EnvironmentList({
  view,
  selected,
  onSelect,
  onDialog,
}: {
  view: EnvironmentsView
  selected: string
  onSelect: (name: string) => void
  onDialog: (d: EnvDialog) => void
}) {
  return (
    <nav
      aria-label="Environments"
      className="flex shrink-0 gap-1 overflow-x-auto border-b p-1.5 @3xl:w-64 @3xl:flex-col @3xl:overflow-x-visible @3xl:overflow-y-auto @3xl:border-r @3xl:border-b-0"
    >
      <div className="hidden px-1.5 pt-0.5 pb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase @3xl:block">
        Environments
      </div>
      <EnvRow
        name=""
        label="Base"
        files={view.baseFiles ?? []}
        hint="Shared by every environment"
        active={view.active === ""}
        selected={selected === ""}
        onSelect={() => onSelect("")}
        onDialog={onDialog}
      />
      {view.environments.map((e) => (
        <EnvRow
          key={e.name}
          name={e.name}
          label={e.name}
          files={e.files}
          prodLike={e.prodLike}
          active={e.active}
          selected={selected === e.name}
          onSelect={() => onSelect(e.name)}
          onDialog={onDialog}
        />
      ))}
    </nav>
  )
}

function EnvRow({
  name,
  label,
  files,
  hint,
  prodLike,
  active,
  selected,
  onSelect,
  onDialog,
}: {
  name: string
  label: string
  files: string[]
  hint?: string
  prodLike?: boolean
  active: boolean
  selected: boolean
  onSelect: () => void
  onDialog: (d: EnvDialog) => void
}) {
  const id = name || "base"
  const isBase = name === ""
  return (
    <div
      className={cn("group/env relative flex min-w-44 shrink-0 items-start rounded-md hover:bg-muted/60", selected && "bg-muted")}
      data-testid={`env-row-${id}`}
      data-active={active || undefined}
    >
      <button
        type="button"
        onClick={onSelect}
        aria-current={selected ? "true" : undefined}
        className="flex min-w-0 flex-1 flex-col gap-0.5 rounded-md px-2 py-1.5 text-left text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
      >
        <span className="flex min-w-0 items-center gap-1.5 pr-6">
          <span className={cn("size-1.5 shrink-0 rounded-full", active ? "bg-emerald-500" : "bg-transparent")} aria-hidden />
          <span className="truncate font-medium">{label}</span>
          {active && <span className="rounded bg-background px-1 text-[10px] text-muted-foreground ring-1 ring-border">active</span>}
          {prodLike && (
            <Badge variant="outline" className="h-4 border-amber-500/40 px-1.5 text-[10px] text-amber-700 dark:text-amber-300">
              prod-like
            </Badge>
          )}
        </span>
        <span className="truncate pl-3 font-mono text-[10px] text-muted-foreground" title={[files.join(", "), hint].filter(Boolean).join(": ")}>
          {files.join(", ") || hint || "No files"}
        </span>
      </button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            size="icon-xs"
            variant="ghost"
            className="absolute top-1 right-1 opacity-60 group-hover/env:opacity-100 aria-expanded:opacity-100"
            aria-label={`Actions for ${label}`}
            data-testid={`env-action-${id}`}
          >
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-44">
          <DropdownMenuItem disabled={active} onSelect={() => switchEnvironment(name)} data-testid={`env-switch-${id}`}>
            <Repeat /> {active ? "Active" : "Switch to"}
          </DropdownMenuItem>
          {useWorkflowStore.getState().workflow?.kind === "kubernetes" && (
            <DropdownMenuItem
              onSelect={() => useWorkflowStore.getState().showReadiness(name)}
              data-testid={`env-readiness-${id}`}
            >
              <ClipboardCheck /> Readiness
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => onDialog({ kind: "new", from: name })} data-testid={`env-duplicate-${id}`}>
            <Copy /> Duplicate…
          </DropdownMenuItem>
          {!isBase && (
            <>
              <DropdownMenuItem onSelect={() => onDialog({ kind: "rename", name })} data-testid={`env-rename-${id}`}>
                <Pencil /> Rename…
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                onSelect={() => onDialog({ kind: "delete", name })}
                data-testid={`env-delete-${id}`}
              >
                <Trash2 /> Delete…
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

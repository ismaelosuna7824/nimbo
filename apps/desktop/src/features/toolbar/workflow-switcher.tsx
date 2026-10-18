import { useQuery } from "@tanstack/react-query"
import { ArchiveIcon, CheckIcon, ChevronsUpDownIcon, CopyIcon, EllipsisIcon, PaletteIcon, PlusIcon, Trash2Icon, XIcon } from "lucide-react"
import { useState } from "react"
import { useShallow } from "zustand/react/shallow"
import { type IconTarget, WorkflowIconDialog } from "@/components/workflow-icon/icon-picker"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar"
import { api } from "@/lib/api/client"
import type { WorkflowSummary } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { MOD_KEY, openWorkflow } from "./actions"
import { DeleteWorkflowDialog } from "./delete-workflow-dialog"
import { archiveWorkflow, closeWorkflow, duplicateWorkflow } from "./workflow-actions"

const RECENT_LIMIT = 12

export function WorkflowSwitcher() {
  const current = useWorkflowStore(
    useShallow((s) => s.workflow && { id: s.workflow.id, name: s.workflow.name, icon: s.workflow.icon }),
  )
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const workflows = useQuery({ queryKey: ["workflows"], queryFn: api.listWorkflows })
  const [deleting, setDeleting] = useState<WorkflowSummary | null>(null)
  const [iconFor, setIconFor] = useState<IconTarget | null>(null)
  const recent = (workflows.data ?? []).filter((w) => !w.archived).slice(0, RECENT_LIMIT)

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu onOpenChange={(o) => o && void workflows.refetch()}>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton size="lg" className="data-[state=open]:bg-sidebar-accent">
              <WorkflowIconTile icon={current?.icon} size={32} />
              <div className="grid flex-1 text-left leading-tight">
                <span className="truncate text-sm font-medium">{current?.name ?? "Nimbo"}</span>
                <span className="truncate text-xs text-muted-foreground">Nimbo workflow</span>
              </div>
              <ChevronsUpDownIcon className="ml-auto" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent className="w-72" align="start">
            <DropdownMenuItem onSelect={() => openDialog("new-workflow")}>
              <PlusIcon />
              New workflow…
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void closeWorkflow()}>
              <XIcon />
              Close workflow
              <DropdownMenuShortcut>{MOD_KEY}W</DropdownMenuShortcut>
            </DropdownMenuItem>
            {current && (
              <DropdownMenuItem onSelect={() => setIconFor(current)}>
                <PaletteIcon />
                Change icon…
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="text-xs text-muted-foreground">Recent workflows</DropdownMenuLabel>
            {recent.length === 0 && <p className="px-1.5 py-1 text-xs text-muted-foreground">No recent workflows.</p>}
            {recent.map((w) => (
              <div key={w.id} className="group/row flex items-center">
                <DropdownMenuItem className="min-w-0 flex-1" onSelect={() => void openWorkflow(w.id)}>
                  <WorkflowIconTile icon={w.icon} size={20} />
                  <span className="truncate">{w.name}</span>
                  <span className="ml-auto text-xs text-muted-foreground">{w.nodeCount}</span>
                  {w.id === current?.id && <CheckIcon />}
                </DropdownMenuItem>
                <DropdownMenuSub>
                  <DropdownMenuSubTrigger
                    aria-label={`Actions for ${w.name}`}
                    className="size-7 shrink-0 justify-center p-0 opacity-0 group-hover/row:opacity-100 focus:opacity-100 data-open:opacity-100 [&>svg:last-child]:hidden"
                  >
                    <EllipsisIcon />
                  </DropdownMenuSubTrigger>
                  <DropdownMenuSubContent>
                    <DropdownMenuItem onSelect={() => setIconFor(w)}>
                      <PaletteIcon />
                      Change icon…
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => void archiveWorkflow(w.id, w.name)}>
                      <ArchiveIcon />
                      Archive
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => void duplicateWorkflow(w.id, w.name)}>
                      <CopyIcon />
                      Duplicate
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem variant="destructive" onSelect={() => setDeleting(w)}>
                      <Trash2Icon />
                      Delete…
                    </DropdownMenuItem>
                  </DropdownMenuSubContent>
                </DropdownMenuSub>
              </div>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <DeleteWorkflowDialog workflow={deleting} onClose={() => setDeleting(null)} />
        <WorkflowIconDialog workflow={iconFor} onClose={() => setIconFor(null)} />
      </SidebarMenuItem>
    </SidebarMenu>
  )
}

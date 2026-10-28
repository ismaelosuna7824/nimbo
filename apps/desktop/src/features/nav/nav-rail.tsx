import { useQuery } from "@tanstack/react-query"
import { GraduationCapIcon, HouseIcon, MoonIcon, PlugIcon, SearchIcon, SettingsIcon, SunIcon, XIcon, type LucideIcon } from "lucide-react"
import { useTheme } from "next-themes"
import { useEffect, type ReactNode } from "react"
import { useShallow } from "zustand/react/shallow"
import { AppLogo } from "@/components/app-logo"
import { Kbd } from "@/components/ui/kbd"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import { MOD_KEY, openWorkflow } from "@/features/toolbar/actions"
import { trackOpenWorkflows, useOpenWorkflows } from "@/features/toolbar/open-workflows"
import { closeOpenWorkflow, workflowListQuery } from "@/features/toolbar/workflow-actions"
import type { WorkflowSummary } from "@/lib/api/types"
import { useUiStore } from "@/features/toolbar/ui-store"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { closePages, goTo, keepOnePageOpen, useSection } from "./nav"
import { CUSTOM_TITLE_BAR } from "./title-bar"

/** What the rail draws of each workflow (a stable function, so react-query only reselects when the list changes). */
const selectRailEntries = (list: WorkflowSummary[]) =>
  list.filter((w) => !w.archived).map((w) => ({ id: w.id, name: w.name, icon: w.icon }))

/**
 * The open workflows, in the order they were opened. One is loaded in the editor (marked while the editor shows);
 * picking another switches to it, saving the current one first. Hover (or focus) shows a close button, like a tab.
 */
function OpenWorkflows({ editorShown }: { editorShown: boolean }) {
  const ids = useOpenWorkflows((s) => s.ids)
  const current = useWorkflowStore(useShallow((s) => s.workflow && { id: s.workflow.id, name: s.workflow.name, icon: s.workflow.icon }))
  // The shared list query (no refetch of its own), narrowed to what the rail draws: saves change updatedAt and counts,
  // not these, so structural sharing keeps the same data and the rail doesn't re-render on every save.
  const workflows = useQuery({ ...workflowListQuery, retry: false, select: selectRailEntries })
  const summaries = new Map((workflows.data ?? []).map((w) => [w.id, w]))
  const open = ids
    .map((id) => (id === current?.id ? current : summaries.get(id)))
    .filter((w): w is NonNullable<typeof w> => !!w)
  if (open.length === 0) return null
  return (
    // Full rail width with padding: the scroll box would otherwise clip the accent bar and the close badge.
    <div
      role="list"
      aria-label="Open workflows"
      className="mt-1 flex min-h-0 w-full flex-col items-center gap-1 overflow-y-auto border-t px-2 pt-2 pb-1 [scrollbar-width:none]"
    >
      {open.map((w) => (
        <div key={w.id} role="listitem" className="group/open relative">
          <RailItem
            label={w.name}
            active={editorShown && w.id === current?.id}
            onSelect={() => {
              if (w.id === current?.id) closePages()
              else {
                closePages()
                void openWorkflow(w.id)
              }
            }}
          >
            <WorkflowIconTile icon={w.icon} size={22} />
          </RailItem>
          <button
            type="button"
            aria-label={`Close ${w.name}`}
            onClick={() => void closeOpenWorkflow(w.id)}
            className="absolute -top-1 -right-1 hidden size-4 items-center justify-center rounded-full border bg-background text-muted-foreground shadow-sm group-focus-within/open:flex group-hover/open:flex hover:text-foreground"
          >
            <XIcon className="size-2.5" />
          </button>
        </div>
      ))}
    </div>
  )
}

/**
 * The app's one navigation: a slim rail on the left of every screen. Places on top (Home, Learn, Connections), then
 * the open workflows like an IDE's open files, tools at the bottom (search, theme, Settings). Every item has a name
 * (aria-label + tooltip) and the current place is marked (aria-current + an accent bar).
 */
export function NavRail() {
  const section = useSection()
  const { resolvedTheme, setTheme } = useTheme()
  const dark = resolvedTheme !== "light"

  useEffect(() => keepOnePageOpen(), [])
  useEffect(() => trackOpenWorkflows(), [])

  return (
    <nav
      aria-label="Main"
      // With the desktop frame the content panel draws its own edge (see App); otherwise the rail does.
      className={cn(
        "relative z-50 flex w-13 shrink-0 flex-col items-center gap-1 bg-rail py-2 text-sidebar-foreground",
        !CUSTOM_TITLE_BAR && "border-r",
      )}
    >
      <AppLogo className="mb-2 size-7" />
      <RailItem label="Home" icon={HouseIcon} active={section === "home"} onSelect={() => void goTo("home")} />
      <RailItem label="Learn" icon={GraduationCapIcon} active={section === "learn"} onSelect={() => void goTo("learn")} />
      <RailItem label="Connections" icon={PlugIcon} active={section === "connections"} onSelect={() => void goTo("connections")} />
      <OpenWorkflows editorShown={section === "workflow"} />

      <div className="mt-auto flex flex-col items-center gap-1 pt-1">
        {/* The desktop frame has a command bar in the title bar: the rail doesn't repeat it there. */}
        {!CUSTOM_TITLE_BAR && (
          <RailItem label="Search" kbd={`${MOD_KEY} K`} icon={SearchIcon} onSelect={() => useUiStore.getState().setCommandOpen(true)} />
        )}
        <RailItem label={dark ? "Light theme" : "Dark theme"} icon={dark ? SunIcon : MoonIcon} onSelect={() => setTheme(dark ? "light" : "dark")} />
        <RailItem
          label="Settings"
          kbd={`${MOD_KEY} ,`}
          icon={SettingsIcon}
          active={section === "settings"}
          onSelect={() => (section === "settings" ? closePages() : void goTo("settings"))}
        />
      </div>
    </nav>
  )
}

function RailItem({
  label,
  icon: Icon,
  kbd,
  active = false,
  onSelect,
  children,
}: {
  label: string
  icon?: LucideIcon
  kbd?: string
  active?: boolean
  onSelect: () => void
  children?: ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          aria-current={active ? "page" : undefined}
          onClick={onSelect}
          className={cn(
            "relative flex size-9 items-center justify-center rounded-lg outline-none transition-[color,background-color,scale] duration-150 focus-visible:ring-2 focus-visible:ring-ring/50 active:scale-95 [&>svg]:size-[18px]",
            active
              ? "bg-primary/12 text-primary dark:bg-primary/18 dark:text-[color-mix(in_oklab,var(--primary),white_30%)]"
              : "text-muted-foreground hover:bg-sidebar-accent hover:text-foreground",
          )}
        >
          {active && <span aria-hidden className="absolute -left-2 h-5 w-[3px] rounded-r-full bg-primary" />}
          {Icon ? <Icon /> : children}
        </button>
      </TooltipTrigger>
      <TooltipContent side="right">
        {label}
        {kbd && <Kbd className="ml-1.5">{kbd}</Kbd>}
      </TooltipContent>
    </Tooltip>
  )
}

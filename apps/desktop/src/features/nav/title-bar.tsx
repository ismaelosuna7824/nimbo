import { isTauri } from "@tauri-apps/api/core"
import { GraduationCapIcon, HouseIcon, PlugIcon, SearchIcon, SettingsIcon, type LucideIcon } from "lucide-react"
import { useShallow } from "zustand/react/shallow"
import { Kbd } from "@/components/ui/kbd"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import { MOD_KEY } from "@/features/toolbar/actions"
import { useUiStore } from "@/features/toolbar/ui-store"
import { useWorkflowStore } from "@/state/workflow-store"
import { useSection, type Section } from "./nav"

/**
 * macOS desktop app only: the window has no native title bar (`titleBarStyle: "Overlay"` in tauri.conf.json). The
 * traffic lights float over this strip, which shares the rail's color so rail and title bar read as one window frame
 * around the content (an inset panel, see App). In the middle sits a command bar, like a browser's address bar: it
 * names the active workspace (its icon and title; a page's name when Home, Learn, Connections or Settings is shown)
 * and opens the command palette. The strip drags the window (double click zooms) like the native bar did. Windows and
 * Linux, and the browser, keep their native frame.
 */
export const CUSTOM_TITLE_BAR = isTauri() && /Mac/i.test(navigator.platform)

// Full-window layers outside the app frame (the setup wizard) read --app-top to start below the bar.
if (CUSTOM_TITLE_BAR) document.documentElement.dataset.titlebar = ""

const PAGES: Record<Exclude<Section, "workflow">, { title: string; icon: LucideIcon }> = {
  home: { title: "Home", icon: HouseIcon },
  learn: { title: "Learn", icon: GraduationCapIcon },
  connections: { title: "Connections", icon: PlugIcon },
  settings: { title: "Settings", icon: SettingsIcon },
}

const KIND_LABEL: Record<string, string> = { docker: "Docker", kubernetes: "Kubernetes" }

export function TitleBar() {
  const section = useSection()
  const workflow = useWorkflowStore(useShallow((s) => s.workflow && { name: s.workflow.name, icon: s.workflow.icon, kind: s.workflow.kind }))
  const page = section === "workflow" || !PAGES[section] ? undefined : PAGES[section]
  const PageIcon = page?.icon
  const title = page ? page.title : (workflow?.name ?? "Nimbo")
  const kind = !page && workflow?.kind ? KIND_LABEL[workflow.kind] : undefined
  return (
    <div data-tauri-drag-region className="flex h-(--titlebar-h) shrink-0 items-center justify-center bg-rail px-24 select-none">
      <button
        type="button"
        onClick={() => useUiStore.getState().setCommandOpen(true)}
        aria-label={`${title}: search or run a command`}
        className="group flex h-7 w-full max-w-[420px] min-w-0 items-center gap-2 rounded-lg border border-border/50 bg-background/40 pr-1.5 pl-2 text-xs shadow-xs transition-colors outline-none hover:border-border hover:bg-background/80 focus-visible:ring-2 focus-visible:ring-ring/50"
      >
        {page && PageIcon ? (
          <PageIcon className="size-3.5 shrink-0 text-muted-foreground" />
        ) : (
          <WorkflowIconTile icon={workflow?.icon} size={16} />
        )}
        <span className="min-w-0 truncate font-medium text-foreground">{title}</span>
        {kind && <span className="shrink-0 text-muted-foreground">· {kind}</span>}
        <span className="ml-auto flex shrink-0 items-center gap-1.5 text-muted-foreground transition-colors group-hover:text-foreground">
          <SearchIcon className="size-3.5" />
          <Kbd>{MOD_KEY} K</Kbd>
        </span>
      </button>
    </div>
  )
}

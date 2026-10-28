import { isTauri } from "@tauri-apps/api/core"
import { getCurrentWindow } from "@tauri-apps/api/window"
import { CopyIcon, GraduationCapIcon, HouseIcon, MinusIcon, PlugIcon, SearchIcon, SettingsIcon, SquareIcon, XIcon, type LucideIcon } from "lucide-react"
import { useEffect, useState } from "react"
import { useShallow } from "zustand/react/shallow"
import { Kbd } from "@/components/ui/kbd"
import { cn } from "@/lib/utils"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import { MOD_KEY } from "@/features/toolbar/actions"
import { useUiStore } from "@/features/toolbar/ui-store"
import { useWorkflowStore } from "@/state/workflow-store"
import { useSection, type Section } from "./nav"

/**
 * Desktop app only: the window has no native title bar. On macOS it is `titleBarStyle: "Overlay"` (tauri.conf.json)
 * and the traffic lights float over this strip; on Windows and Linux the window is undecorated
 * (tauri.windows/linux.conf.json) and the strip draws minimize, maximize and close on the right. The strip shares the
 * rail's color so rail and title bar read as one window frame around the content (an inset panel, see App). In the
 * middle sits a command bar, like a browser's address bar: it names the active workspace (its icon and title; a page's
 * name when Home, Learn, Connections or Settings is shown) and opens the command palette. The strip drags the window
 * (double click maximizes) like the native bar did. The browser keeps its own frame.
 */
export const CUSTOM_TITLE_BAR = isTauri()
const IS_MAC = /Mac/i.test(navigator.platform)

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
    // The frame stays usable while a modal dialog is open (the setup wizard, confirmations): Radix sets
    // `pointer-events: none` on the body and dismisses on a pointerdown outside, so the strip opts back in and keeps
    // its pointerdowns from reaching the dialog. Window dragging listens to mousedown and is unaffected.
    <div
      data-tauri-drag-region
      onPointerDown={(e) => e.stopPropagation()}
      className={cn("pointer-events-auto relative flex h-(--titlebar-h) shrink-0 items-center justify-center bg-rail select-none", IS_MAC ? "px-24" : "px-36")}
    >
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
      {!IS_MAC && <WindowControls />}
    </div>
  )
}

/** Windows/Linux caption buttons; macOS has its traffic lights. */
function WindowControls() {
  const [maximized, setMaximized] = useState(false)
  useEffect(() => {
    const win = getCurrentWindow()
    const sync = () => void win.isMaximized().then(setMaximized)
    sync()
    const off = win.onResized(sync)
    return () => void off.then((unlisten) => unlisten())
  }, [])
  const win = getCurrentWindow()
  const button = "flex h-full w-11 items-center justify-center text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:bg-accent"
  return (
    <div className="absolute inset-y-0 right-0 flex">
      <button type="button" aria-label="Minimize" onClick={() => void win.minimize()} className={cn(button, "hover:bg-accent")}>
        <MinusIcon className="size-4" />
      </button>
      <button type="button" aria-label={maximized ? "Restore" : "Maximize"} onClick={() => void win.toggleMaximize()} className={cn(button, "hover:bg-accent")}>
        {maximized ? <CopyIcon className="size-3.5 -scale-x-100" /> : <SquareIcon className="size-3.5" />}
      </button>
      <button type="button" aria-label="Close" onClick={() => void win.close()} className={cn(button, "hover:bg-[#e81123] hover:text-white")}>
        <XIcon className="size-4" />
      </button>
    </div>
  )
}

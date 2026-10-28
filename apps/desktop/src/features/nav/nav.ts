import { useConnectionsStore } from "@/features/connections/store"
import { useLearnStore } from "@/features/learn/store"
import { useSettingsView } from "@/features/settings/store"
import { useUiStore } from "@/features/toolbar/ui-store"
import { useWorkflowStore } from "@/state/workflow-store"

/**
 * The app's top-level places. The base screen is the open workflow, or Home when none is open; Learn, Connections,
 * Settings, and Home while a workflow is open, are pages drawn over it. The editor stays mounted underneath, so the
 * workflow stays open (and in the rail), with its drafts and live runs, until it is closed on purpose (⌘W, the
 * workflow menu, the switcher).
 */
export type Section = "home" | "workflow" | "learn" | "connections" | "settings"

/** Where the user is: the open page, else the workflow, else home. */
export function useSection(): Section {
  const settings = useSettingsView((s) => s.open)
  const connections = useConnectionsStore((s) => s.viewOpen)
  const learn = useLearnStore((s) => s.viewOpen)
  const home = useUiStore((s) => s.homeOpen)
  const workflow = useWorkflowStore((s) => s.workflow !== null)
  if (settings) return "settings"
  if (connections) return "connections"
  if (learn) return "learn"
  if (home) return "home"
  return workflow ? "workflow" : "home"
}

interface Page {
  isOpen: () => boolean
  close: () => void
  subscribe: (listener: () => void) => () => void
}

const PAGES: Page[] = [
  {
    isOpen: () => useSettingsView.getState().open,
    close: () => useSettingsView.getState().closeView(),
    subscribe: (l) => useSettingsView.subscribe(l),
  },
  {
    isOpen: () => useConnectionsStore.getState().viewOpen,
    close: () => useConnectionsStore.getState().closeView(),
    subscribe: (l) => useConnectionsStore.subscribe(l),
  },
  {
    isOpen: () => useLearnStore.getState().viewOpen,
    close: () => useLearnStore.getState().closeView(),
    subscribe: (l) => useLearnStore.subscribe(l),
  },
  {
    isOpen: () => useUiStore.getState().homeOpen,
    close: () => useUiStore.getState().setHomeOpen(false),
    subscribe: (l) => useUiStore.subscribe(l),
  },
]

/** Closes whichever page (Learn, Connections, Settings, Home over a workflow) is open, back to the screen underneath. */
export function closePages() {
  for (const page of PAGES) page.close()
}

/** Goes to a top-level place. Home never closes the open workflow: it shows over it, the rail keeps the way back. */
export async function goTo(section: Section) {
  switch (section) {
    case "learn":
      return useLearnStore.getState().openView()
    case "connections":
      return useConnectionsStore.getState().openView()
    case "settings":
      return useSettingsView.getState().openView()
    case "workflow":
      return closePages()
    case "home":
      closePages()
      if (useWorkflowStore.getState().workflow) useUiStore.getState().setHomeOpen(true)
      return
  }
}

/**
 * Keeps one page open at a time: whatever opens one (rail, ⌘,, the palette, a link inside a page), the page opened
 * last wins. Returns the unsubscribe.
 */
export function keepOnePageOpen(): () => void {
  // Another workflow opened (palette, new workflow, conversion) or the open one closed: Home over it is done.
  const offWorkflow = useWorkflowStore.subscribe((s, prev) => {
    if (s.workflow?.id !== prev.workflow?.id) useUiStore.getState().setHomeOpen(false)
  })
  const offs = PAGES.map((page) => {
    let was = page.isOpen()
    return page.subscribe(() => {
      const now = page.isOpen()
      if (now && !was) for (const other of PAGES) if (other !== page && other.isOpen()) other.close()
      was = now
    })
  })
  return () => {
    offWorkflow()
    offs.forEach((off) => off())
  }
}

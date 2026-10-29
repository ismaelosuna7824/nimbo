import { lazy, Suspense, useEffect, useRef, useState } from "react"
import { usePanelRef } from "react-resizable-panels"
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable"
import { Sidebar, SidebarContent, SidebarHeader, SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { BottomPanel } from "@/features/panels"
import { CanvasArea } from "@/features/architecture/canvas-area"
import { Inspector } from "@/features/inspector/inspector"
import { useReachability } from "@/features/network/store"
import { Palette } from "@/features/palette/palette"
import { TopBar } from "@/features/toolbar/top-bar"
import { useUiStore } from "@/features/toolbar/ui-store"
import { WorkflowSwitcher } from "@/features/toolbar/workflow-switcher"
import { useWorkflowStore } from "@/state/workflow-store"

/** Height of BottomPanel's tab strip; the panel collapses to it instead of disappearing. */
const BOTTOM_COLLAPSED = "36px"
const BOTTOM_OPEN = "34%"

export function EditorShell() {
  // Read, not subscribed: toggling the dock resizes the panel imperatively below, and a subscription here would
  // re-render the whole editor (canvas, inspector, palette, every tab) on each toggle.
  const [bottomOpenAtMount] = useState(() => useWorkflowStore.getState().bottomOpen)
  const setBottomOpen = useWorkflowStore((s) => s.setBottomOpen)
  const inspectorOpen = useUiStore((s) => s.inspectorOpen)
  // Controlled so a guided tour can give the canvas the room and give it back.
  const paletteOpen = useUiStore((s) => s.paletteOpen)
  const setPaletteOpen = useUiStore((s) => s.setPaletteOpen)
  const bottomRef = usePanelRef()
  // Size to reopen at. expand() would restore the most recent size, which a panel that started collapsed (Guided)
  // doesn't have: it would open at its minimum.
  const bottomSize = useRef(BOTTOM_OPEN)

  useEffect(() => {
    const follow = (open: boolean) => {
      const panel = bottomRef.current
      if (!panel) return
      if (open && panel.isCollapsed()) panel.resize(bottomSize.current)
      if (!open && !panel.isCollapsed()) panel.collapse()
    }
    follow(useWorkflowStore.getState().bottomOpen)
    return useWorkflowStore.subscribe((s, prev) => {
      if (s.bottomOpen !== prev.bottomOpen) follow(s.bottomOpen)
    })
  }, [bottomRef])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "i" && (e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey) {
        e.preventDefault()
        const ui = useUiStore.getState()
        ui.setInspectorOpen(!ui.inspectorOpen)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  return (
    <SidebarProvider
      open={paletteOpen}
      onOpenChange={setPaletteOpen}
      className="h-full min-h-0 overflow-hidden"
      style={{ "--sidebar-width": "19rem" } as React.CSSProperties}
    >
      <Sidebar>
        <SidebarHeader>
          <WorkflowSwitcher />
        </SidebarHeader>
        <SidebarContent className="overflow-hidden">
          <Palette />
        </SidebarContent>
      </Sidebar>
      <SidebarInset className="min-w-0 overflow-hidden">
        <TopBar />
        <ResizablePanelGroup orientation="horizontal" className="min-h-0 flex-1">
          <ResizablePanel id="editor-main" minSize="40%">
            <ResizablePanelGroup orientation="vertical">
              <ResizablePanel id="editor-canvas" minSize="20%">
                <CanvasArea />
              </ResizablePanel>
              <ResizableHandle />
              <ResizablePanel
                id="editor-bottom"
                panelRef={bottomRef}
                collapsible
                collapsedSize={BOTTOM_COLLAPSED}
                minSize="160px"
                defaultSize={bottomOpenAtMount ? BOTTOM_OPEN : BOTTOM_COLLAPSED}
                onResize={(size) => {
                  const collapsed = bottomRef.current?.isCollapsed() ?? false
                  if (!collapsed) bottomSize.current = `${size.asPercentage}%`
                  if (collapsed === useWorkflowStore.getState().bottomOpen) setBottomOpen(!collapsed)
                }}
              >
                <BottomPanel />
              </ResizablePanel>
            </ResizablePanelGroup>
          </ResizablePanel>
          {inspectorOpen && (
            <>
              <ResizableHandle />
              <ResizablePanel id="editor-inspector" defaultSize="380px" minSize="300px" maxSize="50%">
                <aside className="h-full bg-background" aria-label="Inspector">
                  <Inspector />
                </aside>
              </ResizablePanel>
            </>
          )}
        </ResizablePanelGroup>
      </SidebarInset>
      <Reachability />
    </SidebarProvider>
  )
}

// The reachability check loads the first time it opens.
const ReachabilityDialog = lazy(() =>
  import("@/features/network/reachability-dialog").then((m) => ({ default: m.ReachabilityDialog })),
)

function Reachability() {
  // Every open bumps seq, so it is 0 only until the first one.
  const opened = useReachability((s) => s.seq > 0)
  return (
    opened && (
      <Suspense fallback={null}>
        <ReachabilityDialog />
      </Suspense>
    )
  )
}

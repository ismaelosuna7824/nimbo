import { useEffect } from "react"
import { usePanelRef } from "react-resizable-panels"
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable"
import { Sidebar, SidebarContent, SidebarHeader, SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { BottomPanel } from "@/features/panels"
import { Canvas } from "@/features/canvas/canvas"
import { Inspector } from "@/features/inspector/inspector"
import { Palette } from "@/features/palette/palette"
import { TopBar } from "@/features/toolbar/top-bar"
import { useUiStore } from "@/features/toolbar/ui-store"
import { WorkflowSwitcher } from "@/features/toolbar/workflow-switcher"
import { useWorkflowStore } from "@/state/workflow-store"

/** Height of BottomPanel's tab strip; the panel collapses to it instead of disappearing. */
const BOTTOM_COLLAPSED = "36px"

export function EditorShell() {
  const bottomOpen = useWorkflowStore((s) => s.bottomOpen)
  const setBottomOpen = useWorkflowStore((s) => s.setBottomOpen)
  const inspectorOpen = useUiStore((s) => s.inspectorOpen)
  const bottomRef = usePanelRef()

  useEffect(() => {
    const panel = bottomRef.current
    if (!panel) return
    if (bottomOpen && panel.isCollapsed()) panel.expand()
    if (!bottomOpen && !panel.isCollapsed()) panel.collapse()
  }, [bottomOpen, bottomRef])

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
    <SidebarProvider className="h-svh min-h-0 overflow-hidden" style={{ "--sidebar-width": "19rem" } as React.CSSProperties}>
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
                <Canvas />
              </ResizablePanel>
              <ResizableHandle />
              <ResizablePanel
                id="editor-bottom"
                panelRef={bottomRef}
                collapsible
                collapsedSize={BOTTOM_COLLAPSED}
                minSize="160px"
                defaultSize={bottomOpen ? "34%" : BOTTOM_COLLAPSED}
                onResize={() => {
                  const collapsed = bottomRef.current?.isCollapsed() ?? false
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
    </SidebarProvider>
  )
}

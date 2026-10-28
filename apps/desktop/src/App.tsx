import { lazy, Suspense, useEffect, useState, type ComponentType } from "react"
import { Spinner } from "@/components/ui/spinner"
import { AgentWorkflowNotifier } from "@/features/agents/agent-workflow-notifier"
import { CommandPalette } from "@/features/command/command-palette"
import { useConnectionsStore } from "@/features/connections/store"
import { useLearnStore } from "@/features/learn/store"
import { Home } from "@/features/home/home"
import { NavRail } from "@/features/nav/nav-rail"
import { CUSTOM_TITLE_BAR, TitleBar } from "@/features/nav/title-bar"
import { NewWorkflowDialog } from "@/features/home/new-workflow-dialog"
import { OnboardingHost } from "@/features/onboarding/wizard"
import { FeatureDialogs } from "@/features/panels/dialogs/feature-dialogs"
import { SettingsHost } from "@/features/settings/settings-view"
import { useSettingsView } from "@/features/settings/store"
import { DestroyDialog } from "@/features/toolbar/destroy-dialog"
import { DeployConfirmDialog } from "@/features/toolbar/deploy-confirm-dialog"
import { useUiStore } from "@/features/toolbar/ui-store"
import { UpdateNotifier } from "@/features/updates/update-notifier"
import { preloadLastKind } from "@/features/workspaces/code"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"

const ConnectionsView = lazy(() =>
  import("@/features/connections/connections-view").then((m) => ({ default: m.ConnectionsView })),
)
const LearnView = lazy(() => import("@/features/learn/learn-view").then((m) => ({ default: m.LearnView })))

const LAST_WORKFLOW_KEY = "nimbo.lastWorkflow"

export default function App() {
  const hasWorkflow = useWorkflowStore((s) => s.workflow !== null)
  const connectionsOpen = useConnectionsStore((s) => s.viewOpen)
  const learnOpen = useLearnStore((s) => s.viewOpen)
  const settingsOpen = useSettingsView((s) => s.open)
  const homeOver = useUiStore((s) => s.homeOpen) && hasWorkflow
  const [booting, setBooting] = useState(() => !!localStorage.getItem(LAST_WORKFLOW_KEY))
  // The editor (canvases, code editors, panels, inspector) is most of the app's code: a chunk of its own lets Home
  // paint without it; loading starts once mounted. Loaded by hand, not with React.lazy: a Suspense boundary around
  // the editor would catch every later suspension inside it (lazy tabs, the code editor) and swap the whole editor
  // for a fallback.
  const [EditorShell, setEditorShell] = useState<ComponentType | null>(null)

  useEffect(() => {
    const editorShell = import("@/components/editor-shell")
    void editorShell.then((m) => setEditorShell(() => m.EditorShell))
    const last = localStorage.getItem(LAST_WORKFLOW_KEY)
    if (!last) return
    preloadLastKind()
    useWorkflowStore
      .getState()
      .open(last)
      .then(() => {
        // Archived while the app was closed: start from home instead.
        if (!useWorkflowStore.getState().workflow?.archived) return
        useWorkflowStore.getState().close()
        localStorage.removeItem(LAST_WORKFLOW_KEY)
      })
      .catch(() => localStorage.removeItem(LAST_WORKFLOW_KEY))
      // The editor shows as soon as the boot ends: wait for its code too.
      .finally(() => editorShell.finally(() => setBooting(false)))
  }, [])

  // Forget the last workflow when the user goes back home on purpose.
  useEffect(
    () =>
      useWorkflowStore.subscribe((s, prev) => {
        if (prev.workflow && !s.workflow && !booting) localStorage.removeItem(LAST_WORKFLOW_KEY)
      }),
    [booting],
  )

  return (
    <>
      <div className={cn("flex h-svh flex-col overflow-hidden", CUSTOM_TITLE_BAR && "bg-rail")}>
        {CUSTOM_TITLE_BAR && <TitleBar />}
        <div className="flex min-h-0 flex-1">
          <NavRail />
          {/* `contain: layout` makes this the containing block of fixed descendants: the pages (fixed inset-0) and the
              editor's fixed sidebar fill the area right of the rail (and below the title bar) instead of the window.
              In the desktop frame it is an inset panel with a rounded corner under the title bar, which clips them too. */}
          <div
            className={cn(
              "relative min-w-0 flex-1 [contain:layout]",
              CUSTOM_TITLE_BAR && "overflow-hidden rounded-tl-xl border-t border-l bg-background",
            )}
          >
            {/* Pages cover the screen underneath; keep it mounted (drafts, live runs) but out of focus/AT order. */}
            <div className="h-full" inert={connectionsOpen || learnOpen || settingsOpen || homeOver}>
              {booting ? (
                <div className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground">
                  <Spinner />
                  Opening your last workflow…
                </div>
              ) : hasWorkflow ? (
                EditorShell ? (
                  <EditorShell />
                ) : (
                  <div className="flex h-full items-center justify-center text-muted-foreground">
                    <Spinner />
                  </div>
                )
              ) : (
                <Home />
              )}
            </div>
            {/* Home from the rail while a workflow is open: over the editor, which stays open (and in the rail). */}
            {homeOver && (
              <div className="fixed inset-0 z-40 bg-background">
                <Home />
              </div>
            )}
            {connectionsOpen && (
              <Suspense fallback={<div className="fixed inset-0 z-40 bg-background" />}>
                <ConnectionsView />
              </Suspense>
            )}
            {learnOpen && (
              <Suspense fallback={<div className="fixed inset-0 z-40 bg-background" />}>
                <LearnView />
              </Suspense>
            )}
            <SettingsHost />
          </div>
        </div>
      </div>
      <NewWorkflowDialog />
      <FeatureDialogs />
      <CommandPalette />
      <DestroyDialog />
      <DeployConfirmDialog />
      <OnboardingHost />
      <UpdateNotifier />
      <AgentWorkflowNotifier />
    </>
  )
}

import { useEffect, useState } from "react"
import { EditorShell } from "@/components/editor-shell"
import { Spinner } from "@/components/ui/spinner"
import { CommandPalette } from "@/features/command/command-palette"
import { ConnectionsView } from "@/features/connections/connections-view"
import { useConnectionsStore } from "@/features/connections/store"
import { Home } from "@/features/home/home"
import { NewWorkflowDialog } from "@/features/home/new-workflow-dialog"
import { OnboardingHost } from "@/features/onboarding/wizard"
import { FeatureDialogs } from "@/features/panels"
import { SettingsHost } from "@/features/settings/settings-view"
import { useSettingsView } from "@/features/settings/store"
import { DestroyDialog } from "@/features/toolbar/top-bar"
import { useWorkflowStore } from "@/state/workflow-store"

const LAST_WORKFLOW_KEY = "nimbo.lastWorkflow"

export default function App() {
  const hasWorkflow = useWorkflowStore((s) => s.workflow !== null)
  const connectionsOpen = useConnectionsStore((s) => s.viewOpen)
  const settingsOpen = useSettingsView((s) => s.open)
  const [booting, setBooting] = useState(() => !!localStorage.getItem(LAST_WORKFLOW_KEY))

  useEffect(() => {
    const last = localStorage.getItem(LAST_WORKFLOW_KEY)
    if (!last) return
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
      .finally(() => setBooting(false))
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
      {/* Connections and Settings cover the page; keep it mounted (drafts, live runs) but out of focus/AT order. */}
      <div inert={connectionsOpen || settingsOpen}>
        {booting ? (
          <div className="flex h-svh items-center justify-center gap-2 text-sm text-muted-foreground">
            <Spinner />
            Opening your last workflow…
          </div>
        ) : hasWorkflow ? (
          <EditorShell />
        ) : (
          <Home />
        )}
      </div>
      {connectionsOpen && <ConnectionsView />}
      <SettingsHost />
      <NewWorkflowDialog />
      <FeatureDialogs />
      <CommandPalette />
      <DestroyDialog />
      <OnboardingHost />
    </>
  )
}

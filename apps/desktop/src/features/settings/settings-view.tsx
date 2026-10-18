import { lazy, Suspense, useEffect } from "react"
import { useConnectionsStore } from "@/features/connections/store"
import { useWorkflowStore } from "@/state/workflow-store"
import { useSettings, useSettingsView } from "./store"

const SettingsView = lazy(() => import("./settings-page").then((m) => ({ default: m.SettingsView })))

/**
 * Mounted once by App: applies the saved default mode, handles ⌘, and shows
 * the full-page Settings view while it is open.
 */
export function SettingsHost() {
  const open = useSettingsView((s) => s.open)
  const settings = useSettings()
  const defaultMode = settings.data?.defaultMode

  useEffect(() => {
    if (defaultMode) useWorkflowStore.getState().applyDefaultMode(defaultMode)
  }, [defaultMode])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "," && (e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey) {
        e.preventDefault()
        const view = useSettingsView.getState()
        if (view.open) view.closeView()
        else view.openView()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  // Settings and Connections are both full pages: opening one closes the other.
  useEffect(() => {
    if (open) useConnectionsStore.getState().closeView()
  }, [open])
  useEffect(
    () =>
      useConnectionsStore.subscribe((s, prev) => {
        if (s.viewOpen && !prev.viewOpen) useSettingsView.getState().closeView()
      }),
    [],
  )

  return open ? (
    <Suspense fallback={<div className="fixed inset-0 z-40 bg-background" />}>
      <SettingsView />
    </Suspense>
  ) : null
}

import { useEffect } from "react"
import { toast } from "sonner"
import { useSettingsView } from "@/features/settings/store"
import { updatesEnabled, useUpdates } from "./store"

/** Let startup (engine, last workflow) settle before asking GitHub for a newer release. */
const STARTUP_DELAY_MS = 5000

/** Checks for a newer release once per launch (if enabled in Settings) and shows a toast when there is one. */
export function UpdateNotifier() {
  useEffect(() => {
    if (!updatesEnabled || !useUpdates.getState().autoCheck) return
    const timer = window.setTimeout(async () => {
      const found = await useUpdates.getState().check()
      if (!found) return
      const { selfUpdate, install } = useUpdates.getState()
      toast(`Nimbo ${found.version} is available`, {
        id: "nimbo-update",
        description: selfUpdate ? "Install it now; Nimbo restarts when it’s done." : "Download the new version from GitHub.",
        duration: Infinity,
        action: {
          label: selfUpdate ? "Update & restart" : `Download v${found.version}`,
          onClick: () => {
            if (selfUpdate) useSettingsView.getState().openView("about")
            void install()
          },
        },
        cancel: { label: "What’s new", onClick: () => useSettingsView.getState().openView("about") },
      })
    }, STARTUP_DELAY_MS)
    return () => window.clearTimeout(timer)
  }, [])
  return null
}

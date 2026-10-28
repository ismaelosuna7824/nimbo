import { useEffect, useState } from "react"
import { useWorkflowStore, type BottomTab } from "@/state/workflow-store"

/**
 * The open workflow's content version (see the store's `version`) as a bottom-panel tab should follow it: live while
 * the tab is shown, frozen while it is hidden or the panel is collapsed. Tabs stay mounted, so keying their server
 * queries on this instead of the live version keeps every save (each inspector edit) from refetching and re-rendering
 * tabs nobody looks at; showing the tab catches up, with the last answer shown while the new one loads.
 */
export function useShownVersion(tab: BottomTab): string {
  const [version, setVersion] = useState(() => useWorkflowStore.getState().version)
  useEffect(() => {
    const follow = () => {
      const s = useWorkflowStore.getState()
      if (s.workflow && s.bottomOpen && s.bottomTab === tab) setVersion(s.version)
    }
    follow()
    return useWorkflowStore.subscribe(follow)
  }, [tab])
  return version
}

import { lazy, Suspense } from "react"
import { useWorkflowStore } from "@/state/workflow-store"
import { FolderDrop } from "./folder-drop"

// Mounted closed from the start, so React fetches them right after the first render instead of in the entry chunk.
const ExportDialog = lazy(() => import("./export-dialog").then((m) => ({ default: m.ExportDialog })))
const ImportDialog = lazy(() => import("./import-dialog").then((m) => ({ default: m.ImportDialog })))

/** Import/export dialogs driven by `store.dialog`, plus window-wide folder drop. Mount once at the app root. */
export function FeatureDialogs() {
  const dialog = useWorkflowStore((s) => s.dialog)
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const setOpen = (o: boolean) => {
    if (!o) openDialog(null)
  }
  return (
    <>
      <Suspense fallback={null}>
        <ImportDialog open={dialog === "import"} onOpenChange={setOpen} />
        <ExportDialog open={dialog === "export"} onOpenChange={setOpen} />
      </Suspense>
      <FolderDrop />
    </>
  )
}

import { useWorkflowStore } from "@/state/workflow-store"
import { ExportDialog } from "./export-dialog"
import { FolderDrop } from "./folder-drop"
import { ImportDialog } from "./import-dialog"

/** Import/export dialogs driven by `store.dialog`, plus window-wide folder drop. Mount once at the app root. */
export function FeatureDialogs() {
  const dialog = useWorkflowStore((s) => s.dialog)
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const setOpen = (o: boolean) => {
    if (!o) openDialog(null)
  }
  return (
    <>
      <ImportDialog open={dialog === "import"} onOpenChange={setOpen} />
      <ExportDialog open={dialog === "export"} onOpenChange={setOpen} />
      <FolderDrop />
    </>
  )
}

import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { WorkflowIcon } from "@/lib/api/types"
import { queryClient } from "@/lib/query-client"
import { confirmLeave, useWorkflowStore } from "@/state/workflow-store"
import { openWorkflow } from "./actions"

/** Workflow lifecycle actions shared by the switcher, home and command palette. */

const refreshList = () => void queryClient.invalidateQueries({ queryKey: ["workflows"] })
const isOpen = (id: string) => useWorkflowStore.getState().workflow?.id === id

/** Saves pending edits and goes back home. */
export async function closeWorkflow() {
  const store = useWorkflowStore.getState()
  if (!store.workflow || !(await confirmLeave())) return
  await store.flush()
  if (useWorkflowStore.getState().saveError) {
    toast.error("Couldn't save your last changes", { description: "Retry from the save status next to the name, then close." })
    return
  }
  store.close()
  refreshList()
}

/** Hides a workflow from recents and stops its sandbox; closes it when open. */
export async function archiveWorkflow(id: string, name: string) {
  try {
    if (isOpen(id)) await useWorkflowStore.getState().flush()
    await api.archiveWorkflow(id)
    if (isOpen(id)) useWorkflowStore.getState().close()
    refreshList()
    toast.success(`Archived “${name}”`, {
      description: "Its sandbox was stopped. Find it under Archived on the home screen.",
      action: { label: "Undo", onClick: () => void restoreWorkflow(id, name, { quiet: true }) },
    })
  } catch (err) {
    toast.error("Couldn't archive", { description: errorMessage(err) })
  }
}

export async function restoreWorkflow(id: string, name: string, { quiet = false }: { quiet?: boolean } = {}) {
  try {
    await api.unarchiveWorkflow(id)
    refreshList()
    if (!quiet) toast.success(`Restored “${name}”`, { action: { label: "Open", onClick: () => void openWorkflow(id) } })
  } catch (err) {
    toast.error("Couldn't restore", { description: errorMessage(err) })
  }
}

export async function duplicateWorkflow(id: string, name: string) {
  try {
    if (isOpen(id)) await useWorkflowStore.getState().flush()
    const copy = await api.duplicateWorkflow(id, `${name} copy`)
    refreshList()
    toast.success(`Created “${copy.name}”`, { action: { label: "Open", onClick: () => void openWorkflow(copy.id) } })
  } catch (err) {
    toast.error("Couldn't duplicate", { description: errorMessage(err) })
  }
}

export async function deleteWorkflow(id: string, name: string) {
  try {
    await api.deleteWorkflow(id)
    if (isOpen(id)) useWorkflowStore.getState().close()
    if (localStorage.getItem("nimbo.lastWorkflow") === id) localStorage.removeItem("nimbo.lastWorkflow")
    refreshList()
    toast.success(`Deleted “${name}”`)
  } catch (err) {
    toast.error("Couldn't delete", { description: errorMessage(err) })
  }
}

/** Renames a workflow that is not open (the open one renames through the store). */
export async function renameWorkflow(id: string, name: string) {
  try {
    if (isOpen(id)) {
      useWorkflowStore.getState().update((d) => ({ ...d, name }))
      return
    }
    const wf = await api.getWorkflow(id)
    await api.saveWorkflow({ ...wf, name })
    refreshList()
  } catch (err) {
    toast.error("Couldn't rename", { description: errorMessage(err) })
  }
}

/**
 * Sets a workflow's icon (glyph MASCOT_GLYPH for the Nimbo mascot). The open
 * workflow saves through the store (debounced PUT); others are read and
 * written back.
 */
export async function setWorkflowIcon(id: string, icon: WorkflowIcon) {
  try {
    if (isOpen(id)) {
      useWorkflowStore.getState().update((d) => ({ ...d, icon }))
      return
    }
    const wf = await api.getWorkflow(id)
    await api.saveWorkflow({ ...wf, icon })
    refreshList()
  } catch (err) {
    toast.error("Couldn't change the icon", { description: errorMessage(err) })
  }
}

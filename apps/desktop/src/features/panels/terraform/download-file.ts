import { isTauri } from "@tauri-apps/api/core"
import { save } from "@tauri-apps/plugin-dialog"
import { writeFile } from "@tauri-apps/plugin-fs"
import { api } from "@/lib/api/client"
import { baseName } from "./file-model"

/**
 * Downloads one file as the export writes it (save dialog in the desktop app, a regular download in the
 * browser). Resolves false when the user cancelled the save dialog.
 */
export async function downloadWorkflowFile(workflowId: string, path: string): Promise<boolean> {
  const name = baseName(path)
  let target: string | null = null
  if (isTauri()) {
    target = await save({ title: `Save ${name}`, defaultPath: name })
    if (!target) return false
  }
  const res = await fetch(api.fileRawUrl(workflowId, path))
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  if (target) {
    await writeFile(target, new Uint8Array(await res.arrayBuffer()))
    return true
  }
  const url = URL.createObjectURL(await res.blob())
  const a = document.createElement("a")
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
  return true
}

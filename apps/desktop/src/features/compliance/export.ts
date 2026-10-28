import { isTauri } from "@tauri-apps/api/core"
import { save } from "@tauri-apps/plugin-dialog"
import { writeTextFile } from "@tauri-apps/plugin-fs"
import { api } from "@/lib/api/client"

const FILE_RE = /[^\w.-]+/g

/**
 * Saves the Markdown report of one framework (save dialog in the desktop app, a regular download in the browser).
 * Resolves false when the user cancels.
 */
export async function exportReport(workflowId: string, workflowName: string, frameworkId: string): Promise<boolean> {
  const now = new Date()
  const date = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`
  const fileName = `${workflowName.replace(FILE_RE, "-") || workflowId}-${frameworkId}-${date}.md`
  let path: string | null = null
  if (isTauri()) {
    path = await save({ title: "Save compliance report", defaultPath: fileName, filters: [{ name: "Markdown", extensions: ["md"] }] })
    if (!path) return false
  }
  const res = await fetch(api.complianceReportUrl(workflowId, frameworkId))
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  const text = await res.text()
  if (path) {
    await writeTextFile(path, text)
    return true
  }
  const url = URL.createObjectURL(new Blob([text], { type: "text/markdown" }))
  const a = document.createElement("a")
  a.href = url
  a.download = fileName
  a.click()
  URL.revokeObjectURL(url)
  return true
}

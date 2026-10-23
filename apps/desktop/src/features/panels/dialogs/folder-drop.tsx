import { useEffect, useState } from "react"
import { isTauri } from "@tauri-apps/api/core"
import { getCurrentWebview } from "@tauri-apps/api/webview"
import { FolderInputIcon } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { useWorkflowStore } from "@/state/workflow-store"
import { pendingFromPath, readDataTransfer, useImportSource, type PendingImport } from "./import-source"

function startImport(p: PendingImport) {
  useImportSource.getState().setPending(p)
  useWorkflowStore.getState().openDialog("import")
}

/**
 * Dropping a folder anywhere on the window opens the import dialog on its
 * scan results. Desktop: native drop events carry real paths; browser:
 * folders are read through DataTransferItem.webkitGetAsEntry.
 */
export function FolderDrop() {
  const [over, setOver] = useState(false)

  useEffect(() => {
    if (isTauri()) {
      let unlisten: (() => void) | undefined
      let disposed = false
      void getCurrentWebview()
        .onDragDropEvent((e) => {
          const p = e.payload
          if (p.type === "enter") setOver(p.paths.length > 0)
          else if (p.type === "leave") setOver(false)
          else if (p.type === "drop") {
            setOver(false)
            if (p.paths.length > 0) startImport(pendingFromPath(p.paths[0]))
          }
        })
        .then((fn) => {
          if (disposed) fn()
          else unlisten = fn
        })
      return () => {
        disposed = true
        unlisten?.()
      }
    }

    let depth = 0
    const hasFiles = (e: DragEvent) => !!e.dataTransfer?.types.includes("Files")
    const onEnter = (e: DragEvent) => {
      if (!hasFiles(e)) return
      depth++
      setOver(true)
    }
    const onLeave = (e: DragEvent) => {
      if (!hasFiles(e)) return
      depth = Math.max(0, depth - 1)
      if (depth === 0) setOver(false)
    }
    const onOver = (e: DragEvent) => {
      if (!hasFiles(e)) return
      e.preventDefault()
      if (e.dataTransfer) e.dataTransfer.dropEffect = "copy"
    }
    const onDrop = (e: DragEvent) => {
      depth = 0
      setOver(false)
      if (!hasFiles(e) || !e.dataTransfer) return
      e.preventDefault()
      readDataTransfer(e.dataTransfer)
        .then((p) => {
          if (p) startImport(p)
          else
            toast.error("No project files found", {
              description: "Drop a folder with .tf files, a compose file, a Dockerfile or Kubernetes manifests (kustomization, Helm chart).",
            })
        })
        .catch((err) => toast.error("Couldn't read the dropped folder", { description: errorMessage(err) }))
    }
    window.addEventListener("dragenter", onEnter)
    window.addEventListener("dragleave", onLeave)
    window.addEventListener("dragover", onOver)
    window.addEventListener("drop", onDrop)
    return () => {
      window.removeEventListener("dragenter", onEnter)
      window.removeEventListener("dragleave", onLeave)
      window.removeEventListener("dragover", onOver)
      window.removeEventListener("drop", onDrop)
    }
  }, [])

  if (!over) return null
  return (
    <div className="pointer-events-none fixed inset-0 z-[100] flex items-center justify-center bg-background/70 backdrop-blur-sm">
      <div className="flex flex-col items-center gap-3 rounded-2xl border-2 border-dashed border-primary bg-background px-12 py-10 text-center shadow-lg">
        <FolderInputIcon className="size-10 text-primary" />
        <div>
          <p className="font-heading text-lg font-medium">Drop to import Terraform</p>
          <p className="text-sm text-muted-foreground">Every project in the folder can become a workflow</p>
        </div>
      </div>
    </div>
  )
}

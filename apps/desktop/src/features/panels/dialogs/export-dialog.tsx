import { useState } from "react"
import { isTauri } from "@tauri-apps/api/core"
import { open as openDialog } from "@tauri-apps/plugin-dialog"
import { CircleCheck, Download, FolderOpen } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/ui/spinner"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import { useWorkflowStore } from "@/state/workflow-store"

export function ExportDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const workflow = useWorkflowStore((s) => s.workflow)
  const tauri = isTauri()
  const [environments, setEnvironments] = useState(true)
  const [busy, setBusy] = useState(false)
  const [written, setWritten] = useState<{ dir: string; files: string[] } | null>(null)

  const close = (o: boolean) => {
    if (!o) setWritten(null)
    onOpenChange(o)
  }

  const exportToFolder = async () => {
    if (!workflow) return
    const dir = await openDialog({ directory: true, multiple: false, title: "Export Terraform to folder" })
    if (typeof dir !== "string") return
    setBusy(true)
    try {
      await useWorkflowStore.getState().flush()
      setWritten(await api.exportTo(workflow.id, dir, environments))
    } catch (err) {
      toast.error("Export failed", { description: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  const downloadZip = async () => {
    if (!workflow) return
    setBusy(true)
    try {
      await useWorkflowStore.getState().flush()
      const res = await fetch(api.exportZipUrl(workflow.id, environments))
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
      const url = URL.createObjectURL(await res.blob())
      const a = document.createElement("a")
      a.href = url
      a.download = `${workflow.name.replace(/[^\w.-]+/g, "-") || workflow.id}.zip`
      a.click()
      URL.revokeObjectURL(url)
      toast.success("Terraform exported", { description: a.download })
      close(false)
    } catch (err) {
      toast.error("Export failed", { description: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  const hasEnvs = (workflow?.environments.length ?? 0) > 0

  return (
    <Dialog open={open && !!workflow} onOpenChange={close}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Export Terraform</DialogTitle>
          <DialogDescription>
            Clean, cloud-ready Terraform: no sandbox settings are included, so the same code deploys to your real
            account with <code>terraform apply</code>.
          </DialogDescription>
        </DialogHeader>

        {written ? (
          <div className="flex flex-col gap-2 text-xs">
            <p className="flex items-center gap-1.5 font-medium text-emerald-600 dark:text-emerald-400">
              <CircleCheck className="size-4" /> Wrote {written.files.length} file{written.files.length === 1 ? "" : "s"}
            </p>
            <p className="truncate font-mono text-muted-foreground" title={written.dir}>
              {written.dir}
            </p>
            <ul className="max-h-48 overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-[11px]">
              {written.files.map((f) => (
                <li key={f}>{f}</li>
              ))}
            </ul>
          </div>
        ) : (
          <div className="flex items-start gap-2">
            <Checkbox
              id="export-envs"
              checked={environments}
              onCheckedChange={(v) => setEnvironments(v === true)}
              className="mt-0.5"
            />
            <div className="grid gap-0.5">
              <Label htmlFor="export-envs">Include environments</Label>
              <p className="text-xs text-muted-foreground">
                {hasEnvs
                  ? `Writes each environment's values (${workflow!.environments
                      .map((e) => e.file ?? `environments/${e.name}.tfvars`)
                      .join(", ")}) and a .env.example per environment. Imported variable files keep their name. Secret values are left blank.`
                  : "This workflow has no environments yet."}
              </p>
            </div>
          </div>
        )}

        <DialogFooter>
          {written ? (
            <Button onClick={() => close(false)}>Done</Button>
          ) : (
            <>
              <Button variant="ghost" onClick={() => close(false)}>
                Cancel
              </Button>
              {tauri ? (
                <Button disabled={busy} onClick={() => void exportToFolder()}>
                  {busy ? <Spinner /> : <FolderOpen />} Choose folder & export
                </Button>
              ) : (
                <Button disabled={busy} onClick={() => void downloadZip()}>
                  {busy ? <Spinner /> : <Download />} Download .zip
                </Button>
              )}
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

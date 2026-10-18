import { useState } from "react"
import { Box, Copy, Play, RotateCcw, Square } from "lucide-react"
import { toast } from "sonner"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { CloudBadge } from "@/components/cloud"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import { useWorkflowStore } from "@/state/workflow-store"
import { StatusBadge } from "../components/status-badge"
import { copyText } from "../lib/format"

type SandboxAction = "start" | "stop" | "reset"

const CONFIRM: Record<"stop" | "reset", { title: string; description: string; action: string }> = {
  stop: {
    title: "Stop the sandbox?",
    description:
      "The emulator containers are stopped. Deployed resources stay in the sandbox and come back when you start it again.",
    action: "Stop",
  },
  reset: {
    title: "Reset the sandbox?",
    description:
      "Every emulated resource and the Terraform state are wiped. Your diagram is not changed — apply again to recreate the resources.",
    action: "Reset",
  },
}

export function SandboxCard() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const sandbox = useWorkflowStore((s) => s.sandbox)
  const [busy, setBusy] = useState<SandboxAction | null>(null)
  const [confirm, setConfirm] = useState<"stop" | "reset" | null>(null)
  const state = sandbox?.state ?? "absent"

  const run = async (action: SandboxAction) => {
    setBusy(action)
    try {
      const status = await api.sandboxAction(workflowId, action)
      useWorkflowStore.setState({ sandbox: status })
      if (action === "reset") {
        useWorkflowStore.setState({ deployed: await api.state(workflowId).catch(() => null) })
        toast.success("Sandbox reset")
      }
    } catch (err) {
      toast.error(`Could not ${action} the sandbox`, { description: errorMessage(err) })
    } finally {
      setBusy(null)
    }
  }

  return (
    <section className="rounded-lg border">
      <div className="flex items-center gap-2 px-2.5 py-2">
        <Box className="size-4 text-muted-foreground" />
        <span className="text-xs font-semibold whitespace-nowrap">Local sandbox</span>
        <StatusBadge status={state} />
        <div className="ml-auto flex gap-0.5">
          {state === "running" || state === "starting" ? (
            <Button size="xs" variant="ghost" disabled={busy !== null} onClick={() => setConfirm("stop")}>
              {busy === "stop" ? <Spinner /> : <Square />} Stop
            </Button>
          ) : (
            <Button size="xs" variant="outline" disabled={busy !== null} onClick={() => void run("start")}>
              {busy === "start" ? <Spinner /> : <Play />} Start
            </Button>
          )}
          <Button
            size="xs"
            variant="ghost"
            disabled={busy !== null || state === "absent"}
            onClick={() => setConfirm("reset")}
          >
            {busy === "reset" ? <Spinner /> : <RotateCcw />} Reset
          </Button>
        </div>
      </div>
      {sandbox?.message && (
        <p className={`px-2.5 pb-2 text-xs ${state === "error" ? "text-destructive" : "text-muted-foreground"}`}>
          {sandbox.message}
        </p>
      )}
      {sandbox && sandbox.emulators.length > 0 ? (
        <div className="divide-y border-t">
          {sandbox.emulators.map((em) => {
            const services = Object.entries(em.services ?? {})
            const running = services.filter(([, s]) => s === "running").length
            return (
              <div key={em.cloud} className="grid grid-cols-[auto_1fr_auto] items-center gap-x-2 gap-y-0.5 px-2.5 py-1.5 text-xs">
                <CloudBadge cloud={em.cloud} />
                <span className="truncate font-mono text-[11px] text-muted-foreground" title={em.image}>
                  {em.image}
                  {em.version && ` · v${em.version}`}
                </span>
                <StatusBadge status={em.state} />
                <span />
                {em.url ? (
                  <button
                    type="button"
                    className="flex min-w-0 items-center gap-1 truncate text-left font-mono text-[11px] hover:underline"
                    title="Copy URL"
                    onClick={() => void copyText(em.url).then(() => toast.success("URL copied"))}
                  >
                    {em.url}
                    <Copy className="size-3 shrink-0 opacity-60" />
                  </button>
                ) : (
                  <span />
                )}
                {services.length > 0 ? (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className="cursor-default text-[11px] text-muted-foreground tabular-nums">
                        {running}/{services.length} services
                      </span>
                    </TooltipTrigger>
                    <TooltipContent className="max-w-xs">
                      {services.map(([name, s]) => `${name}${s === "running" ? "" : ` (${s})`}`).join(", ")}
                    </TooltipContent>
                  </Tooltip>
                ) : (
                  <span />
                )}
              </div>
            )
          })}
        </div>
      ) : (
        <p className="border-t px-2.5 py-2 text-xs text-muted-foreground">
          Emulators for the clouds in this workflow start automatically on your first deploy.
        </p>
      )}

      <AlertDialog open={confirm !== null} onOpenChange={(o) => !o && setConfirm(null)}>
        {confirm && (
          <AlertDialogContent size="sm">
            <AlertDialogHeader>
              <AlertDialogTitle>{CONFIRM[confirm].title}</AlertDialogTitle>
              <AlertDialogDescription>{CONFIRM[confirm].description}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                variant={confirm === "reset" ? "destructive" : "default"}
                onClick={() => void run(confirm)}
              >
                {CONFIRM[confirm].action}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        )}
      </AlertDialog>
    </section>
  )
}

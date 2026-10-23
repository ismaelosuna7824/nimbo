import { useState } from "react"
import { PlayIcon, PowerIcon, ScrollText } from "lucide-react"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { runAction, useRunBusy } from "@/features/toolbar/actions"
import { useUiStore } from "@/features/toolbar/ui-store"
import { CLUSTER_STATE, KUBE_STATE, kubeChipSummary, workloadPods } from "./kube-model"

/** Toolbar chip of a Kubernetes workspace: the local cluster's state, its workloads and Deploy/Destroy. */
export function KubeChip() {
  const status = useWorkflowStore((s) => s.kube)
  const busy = useRunBusy()
  const [starting, setStarting] = useState(false)
  const summary = kubeChipSummary(status)
  const meta = KUBE_STATE[summary.state]
  const deploy = async () => {
    setStarting(true)
    try {
      await runAction("up")
    } finally {
      setStarting(false)
    }
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="gap-2"
          data-testid="kube-chip"
          data-state={summary.state}
          data-cluster={summary.cluster}
        >
          {summary.state === "starting" || busy ? <Spinner className="size-3" /> : <span className={cn("size-2 rounded-full", meta.dot)} />}
          {summary.label}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80" align="center">
        <div className="flex flex-col gap-3">
          <div>
            <div className="flex items-center gap-2 text-sm font-medium">
              <TechIcon icon="kubernetes" size={16} />
              Local cluster
              <span className="ml-auto flex items-center gap-1.5 text-xs font-normal text-muted-foreground">
                <span className={cn("size-1.5 rounded-full", CLUSTER_STATE[summary.cluster].dot)} />
                {CLUSTER_STATE[summary.cluster].label}
              </span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              This workspace runs on a private k3s cluster in Docker
              {status?.cluster.name ? (
                <>
                  {" "}
                  named <span className="font-mono">{status.cluster.name}</span>
                </>
              ) : null}
              . Deploy creates it and applies your manifests.
            </p>
          </div>
          {(status?.cluster.message || status?.message) && (
            <p
              className={cn(
                "rounded-md bg-muted px-2 py-1.5 text-xs",
                (summary.cluster === "error" || summary.state === "error") && "bg-destructive/10 text-destructive",
              )}
            >
              {status.cluster.message || status.message}
            </p>
          )}
          {(status?.workloads.length ?? 0) > 0 && (
            <ul className="flex max-h-56 flex-col gap-1.5 overflow-auto">
              {status!.workloads.map((w) => {
                const pods = workloadPods(w)
                return (
                  <li key={w.nodeId} className="flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs">
                    <span className={cn("size-1.5 shrink-0 rounded-full", KUBE_STATE[w.state].dot)} />
                    <span className="truncate font-medium" title={w.nodeId}>
                      {w.name}
                    </span>
                    <span className="text-muted-foreground">{KUBE_STATE[w.state].label}</span>
                    <span className="ml-auto text-muted-foreground tabular-nums" title="Ready pods / desired">
                      {pods.ready}/{w.desired}
                    </span>
                  </li>
                )
              })}
            </ul>
          )}
          <div className="flex gap-2">
            <Button size="sm" disabled={busy || starting} onClick={() => void deploy()}>
              {starting ? <Spinner /> : <PlayIcon />}
              Deploy
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={busy || starting || summary.cluster === "absent"}
              onClick={() => useUiStore.getState().setConfirmDestroy(true)}
            >
              <PowerIcon />
              Destroy…
            </Button>
            <Button size="sm" variant="ghost" className="ml-auto" onClick={() => useWorkflowStore.getState().showBottom("deploy")}>
              <ScrollText />
              Details
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}

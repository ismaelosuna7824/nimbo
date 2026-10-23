import { ExternalLink, Play, Square, Trash2 } from "lucide-react"
import { K8sIcon } from "@/components/k8s-icons/k8s-icon"
import { openExternal } from "@/components/open-external"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { KubeClusterStatus, KubeForward, KubeIngressURL } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { CopyButton } from "@/features/panels/endpoints/bits"
import { CLUSTER_STATE, kubectlSnippet } from "./kube-model"

export type ClusterAction = "cluster-start" | "cluster-stop" | "cluster-delete"

/** Status dot + label of the cluster. */
export function ClusterStateLabel({ state, className }: { state: KubeClusterStatus["state"]; className?: string }) {
  const meta = CLUSTER_STATE[state]
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)} data-testid="kube-cluster-state" data-state={state}>
      {state === "starting" ? <Spinner className="size-3" /> : <span className={cn("size-2 rounded-full", meta.dot)} />}
      {meta.label}
    </span>
  )
}

function Fact({ label, children, title }: { label: string; children: React.ReactNode; title?: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 truncate font-mono" title={title}>
        {children}
      </dd>
    </>
  )
}

/**
 * The workflow's local k3s cluster: state, versions, ports, Nimbo's kubeconfig (with a kubectl snippet) and
 * start/stop/delete.
 */
export function ClusterDetails({
  cluster,
  busy,
  starting,
  onAction,
}: {
  cluster: KubeClusterStatus | undefined
  /** A run is in progress or Docker is down. */
  busy: boolean
  starting: string | null
  onAction: (action: ClusterAction) => void
}) {
  const state = cluster?.state ?? "absent"
  const snippet = cluster?.kubeconfig ? kubectlSnippet(cluster.kubeconfig) : undefined
  const up = state === "running" || state === "starting"
  return (
    <div className="flex flex-col gap-2">
      {cluster && state !== "absent" && (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-[11px]" data-testid="kube-cluster-facts">
          <Fact label="Name">{cluster.name}</Fact>
          {cluster.version && <Fact label="Kubernetes">{cluster.version}</Fact>}
          <Fact label="Node image" title={cluster.image}>
            {cluster.image}
          </Fact>
          {cluster.apiPort !== undefined && <Fact label="API">127.0.0.1:{cluster.apiPort}</Fact>}
          {cluster.ingressPort !== undefined && (
            <Fact label="Ingress">
              127.0.0.1:{cluster.ingressPort}
              {cluster.ingressClass ? ` · ${cluster.ingressClass}` : ""}
            </Fact>
          )}
          {cluster.storageClass && <Fact label="Storage">{cluster.storageClass}</Fact>}
        </dl>
      )}
      {cluster?.kubeconfig && (
        <div className="flex flex-col gap-1" data-testid="kube-kubeconfig">
          <span className="text-[11px] text-muted-foreground">Kubeconfig (yours in ~/.kube stays untouched)</span>
          <div className="flex min-w-0 items-center gap-1 rounded-md bg-muted/60 py-0.5 pr-0.5 pl-1.5">
            <span className="min-w-0 flex-1 truncate font-mono text-[11px]" title={cluster.kubeconfig}>
              {cluster.kubeconfig}
            </span>
            <CopyButton value={cluster.kubeconfig} label="Copy kubeconfig path" what="Path copied" />
          </div>
          {snippet && (
            <div className="flex min-w-0 items-center gap-1 rounded-md bg-muted/60 py-0.5 pr-0.5 pl-1.5">
              <span className="min-w-0 flex-1 truncate font-mono text-[11px]" title={snippet} data-testid="kube-kubectl-snippet">
                {snippet}
              </span>
              <CopyButton value={snippet} label="Copy kubectl command" what="Command copied" />
            </div>
          )}
        </div>
      )}
      <div className="flex flex-wrap gap-1">
        {up ? (
          <ClusterButton
            label="Stop cluster"
            hint="Stops the cluster's container; workloads and data stay and come back on Start."
            loading={starting === "cluster-stop"}
            disabled={busy}
            testId="kube-cluster-stop"
            onClick={() => onAction("cluster-stop")}
          >
            <Square />
          </ClusterButton>
        ) : (
          <ClusterButton
            label="Start cluster"
            hint={state === "absent" ? "Deploy creates the cluster." : "Starts the stopped cluster with everything it ran."}
            loading={starting === "cluster-start"}
            disabled={busy || state === "absent"}
            testId="kube-cluster-start"
            onClick={() => onAction("cluster-start")}
          >
            <Play />
          </ClusterButton>
        )}
        <ClusterButton
          label="Delete cluster"
          hint="Deletes the cluster with every workload and volume. The next Deploy creates a fresh one."
          loading={starting === "cluster-delete"}
          disabled={busy || state === "absent"}
          destructive
          testId="kube-cluster-delete"
          onClick={() => onAction("cluster-delete")}
        >
          <Trash2 />
        </ClusterButton>
      </div>
    </div>
  )
}

function ClusterButton({
  label,
  hint,
  loading,
  disabled,
  destructive,
  testId,
  onClick,
  children,
}: {
  label: string
  hint: string
  loading: boolean
  disabled: boolean
  destructive?: boolean
  testId: string
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="xs"
          variant="outline"
          className={cn(destructive && "text-destructive")}
          disabled={disabled || loading}
          data-testid={testId}
          onClick={onClick}
        >
          {loading ? <Spinner /> : children}
          {label}
        </Button>
      </TooltipTrigger>
      <TooltipContent className="max-w-56">{hint}</TooltipContent>
    </Tooltip>
  )
}

/** Port-forwards (Service port → 127.0.0.1) and Ingress URLs of the cluster: what is reachable from this machine. */
export function KubeAccess({ forwards, ingresses }: { forwards: KubeForward[]; ingresses: KubeIngressURL[] }) {
  if (forwards.length === 0 && ingresses.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        Nothing reachable yet. Services get a port on 127.0.0.1 and Ingress rules a URL once their pods run.
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-1.5">
      {forwards.length > 0 && (
        <ul className="flex flex-col gap-1" aria-label="Port-forwards">
          {forwards.map((f) => (
            <li
              key={`${f.nodeId}:${f.port}`}
              data-testid={`kube-forward-${f.nodeId}-${f.port}`}
              data-ready={f.ready}
              className="flex min-w-0 items-center gap-1.5 text-xs"
              title={f.message}
            >
              <K8sIcon icon="Service" size={14} />
              <span className="min-w-0 truncate">
                {f.service}:{f.portName ?? f.port}
              </span>
              <span className="text-muted-foreground">→</span>
              {f.url && f.ready ? (
                <button
                  type="button"
                  className="inline-flex min-w-0 items-center gap-0.5 font-mono text-primary underline-offset-2 hover:underline"
                  title={`Open ${f.url}`}
                  onClick={() => void openExternal(f.url!)}
                >
                  127.0.0.1:{f.hostPort}
                  <ExternalLink className="size-3 shrink-0" />
                </button>
              ) : (
                <span className={cn("font-mono", !f.ready && "text-muted-foreground")}>
                  127.0.0.1:{f.hostPort}
                  {f.protocol === "UDP" ? "/udp" : ""}
                </span>
              )}
              {!f.ready && <span className="text-[10px] text-amber-600 dark:text-amber-400">waiting</span>}
            </li>
          ))}
        </ul>
      )}
      {ingresses.length > 0 && (
        <ul className="flex flex-col gap-1" aria-label="Ingress URLs">
          {ingresses.map((i) => (
            <li key={`${i.nodeId}:${i.host ?? ""}${i.path}`} data-testid={`kube-ingress-${i.nodeId}`} className="flex flex-col gap-0.5 text-xs">
              <span className="flex min-w-0 items-center gap-1.5">
                <K8sIcon icon="Ingress" size={14} />
                <button
                  type="button"
                  className="inline-flex min-w-0 items-center gap-0.5 truncate font-mono text-primary underline-offset-2 hover:underline"
                  title={`Open ${i.url}`}
                  onClick={() => void openExternal(i.url)}
                >
                  <span className="truncate">{i.url}</span>
                  <ExternalLink className="size-3 shrink-0" />
                </button>
                <CopyButton value={i.address} label="Copy address" what="Address copied" />
              </span>
              <span className="truncate pl-5 text-[10px] text-muted-foreground" title={i.address}>
                {i.host ? `via ${i.address} with header Host: ${i.host}` : `via ${i.address}`} → {i.service}
                {i.port ? `:${i.port}` : ""}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

import { useMemo } from "react"
import { K8sIcon } from "@/components/k8s-icons/k8s-icon"
import { techIconFor } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { useWorkflowStore } from "@/state/workflow-store"
import { backingWorkload, nodeKind } from "./kube-model"

/**
 * Icon of a Kubernetes endpoint (Service or Ingress node): the technology of the workload behind it, marked with the
 * object's Kubernetes icon; just the Kubernetes icon when no workload is known.
 */
export function KubeAccessIcon({ nodeId, size = 20 }: { nodeId: string; size?: number }) {
  const graph = useWorkflowStore((s) => s.workflow?.kubernetes?.graph)
  const tech = useMemo(() => {
    const workload = backingWorkload(nodeId, graph)
    const image = workload?.workload?.containers[0]?.image
    return workload ? techIconFor({ image, stack: workload.stack, kind: "service" }) : undefined
  }, [nodeId, graph])
  const kind = nodeKind(nodeId)
  if (!tech) return <K8sIcon icon={kind} size={size} />
  return (
    <span className="relative inline-flex shrink-0" data-testid={`kube-access-icon-${nodeId}`}>
      <TechIcon icon={tech} size={size} />
      <K8sIcon icon={kind} size={Math.round(size * 0.6)} className="absolute -right-1.5 -bottom-1.5" />
    </span>
  )
}

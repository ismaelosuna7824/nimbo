import { Suspense } from "react"
import { lazyComponent } from "@/components/lazy-module"
import { useWorkflowStore } from "@/state/workflow-store"
import { dockerCode, kubernetesCode, terraformCode } from "../workspaces/code"

// Each kind's catalog comes with its workspace code, loaded when the workflow opens (see workspaces/code).
const ResourcePalette = lazyComponent(terraformCode, (m) => m.ResourcePalette)
const DockerPalette = lazyComponent(dockerCode, (m) => m.DockerPalette)
const KubernetesPalette = lazyComponent(kubernetesCode, (m) => m.KubernetesPalette)

/** The left panel's catalog: cloud resources for Terraform workflows, images and building blocks for Docker and Kubernetes ones. */
export function Palette() {
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  return (
    <Suspense fallback={null}>
      {kind === "docker" ? <DockerPalette /> : kind === "kubernetes" ? <KubernetesPalette /> : <ResourcePalette />}
    </Suspense>
  )
}

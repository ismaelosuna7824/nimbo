import { useQuery } from "@tanstack/react-query"
import { useMemo } from "react"
import { useNodeSchemas, schemaKey } from "@/features/canvas/catalog"
import { useConnections } from "@/features/connections/store"
import { api } from "@/lib/api/client"
import type { Workflow } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { deriveArchitecture } from "./derive"
import { deriveDockerArchitecture } from "./docker"
import { deriveKubernetesArchitecture } from "./kubernetes"
import type { ArchModel } from "./model"

/**
 * The open Terraform workflow's architecture model (null while its module files load) and the catalog
 * label of a resource type. The Architecture view draws it; the request walkthrough follows its arrows.
 */
export function useTerraformModel(): { model: ArchModel | null; labelOf: (type: string) => string | undefined } {
  const wf = useWorkflowStore((s) => s.workflow) as Workflow
  const version = useWorkflowStore((s) => s.version)
  const loadTests = useWorkflowStore((s) => s.loadTests)
  const connections = useConnections().data
  const schemas = useNodeSchemas(wf.nodes)

  // Module calls are drawn as what they create: their content comes from the module files (read again
  // whenever the workflow's content is saved, as module files change with it).
  const hasModules = wf.nodes.some((n) => n.kind === "module")
  const moduleTree = useQuery({
    queryKey: ["module-tree", wf.id, version],
    queryFn: () => api.moduleTree(wf.id),
    enabled: hasModules,
    placeholderData: (previous) => previous,
    staleTime: Infinity,
  })
  const modulesPending = hasModules && moduleTree.isPending

  const used = useMemo(() => {
    const ids = new Set(loadTests.flatMap((r) => (r.spec.target.connectionId ? [r.spec.target.connectionId] : [])))
    return (connections ?? []).filter((c) => ids.has(c.id)).map((c) => ({ id: c.id, name: c.name, kind: c.kind }))
  }, [loadTests, connections])
  const labelOf = useMemo(() => {
    const labels = new Map<string, string>()
    for (const n of wf.nodes) {
      const label = schemas.get(schemaKey(n.kind, n.type))?.label
      if (label) labels.set(n.type, label)
    }
    return (type: string) => labels.get(type)
  }, [wf.nodes, schemas])

  const { nodes, edges, variables, locals, providers, environments, activeEnvironment } = wf
  const modules = hasModules ? moduleTree.data : undefined
  const model = useMemo(
    () =>
      modulesPending
        ? null
        : deriveArchitecture({ nodes, edges, variables, locals, providers, environments, activeEnvironment }, { connections: used, labelOf, modules }),
    [modulesPending, nodes, edges, variables, locals, providers, environments, activeEnvironment, used, labelOf, modules],
  )
  return { model, labelOf }
}

/** The open Docker workspace's architecture model: its compose graph and the containers' status. */
export function useDockerModel(): ArchModel {
  const name = useWorkflowStore((s) => s.workflow!.name)
  const compose = useWorkflowStore((s) => s.workflow!.compose)
  const status = useWorkflowStore((s) => s.compose)
  return useMemo(() => deriveDockerArchitecture({ name, compose }, status), [name, compose, status])
}

/** The open Kubernetes workspace's architecture model: its rendered objects (active environment) and the workloads' status. */
export function useKubernetesModel(): ArchModel {
  const id = useWorkflowStore((s) => s.workflow!.id)
  const name = useWorkflowStore((s) => s.workflow!.name)
  const kubernetes = useWorkflowStore((s) => s.workflow!.kubernetes)
  const status = useWorkflowStore((s) => s.kube)
  return useMemo(() => deriveKubernetesArchitecture({ id, name, kubernetes }, status), [id, name, kubernetes, status])
}

import type { Workflow } from "@/lib/api/types"

/**
 * What an environment renders on top of the base: Kustomize overlay dirs / Helm values files per source
 * (Kubernetes), compose override and env files (docker).
 */
export function environmentFiles(workflow: Workflow, name: string): string[] {
  if (workflow.kind === "kubernetes") {
    return (workflow.kubernetes?.sources ?? []).flatMap((s) => (s.envPaths?.[name] ? [s.envPaths[name]] : []))
  }
  if (workflow.kind === "docker") return workflow.compose?.environments?.find((e) => e.name === name)?.files ?? []
  return []
}

/** Environment names of a workflow (the base excluded): docker reads its compose environments, Kubernetes the workflow's. */
export function environmentNames(workflow: Workflow): string[] {
  if (workflow.kind === "docker" && workflow.compose?.environments) return workflow.compose.environments.map((e) => e.name)
  return (workflow.environments ?? []).map((e) => e.name)
}

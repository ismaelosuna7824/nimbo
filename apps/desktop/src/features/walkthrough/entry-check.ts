// Whether "Follow a request" has somewhere to start on the open workflow: loaded when the Guided tour menu opens,
// so the Build canvas does not carry the Architecture derivation until someone asks.
import { deriveArchitecture } from "@/features/architecture/derive"
import { deriveDockerArchitecture } from "@/features/architecture/docker"
import { deriveKubernetesArchitecture } from "@/features/architecture/kubernetes"
import type { ArchModel } from "@/features/architecture/model"
import { api } from "@/lib/api/client"
import { isDocker, isKubernetes, type Workflow } from "@/lib/api/types"
import { queryClient } from "@/lib/query-client"
import { NO_ENTRY_HINT, noEntryText } from "./captions"
import { operatorEntries, withClusterOperators } from "./clusters"
import { describeDocker, describeKubernetes, describeTerraform, type Describer } from "./describe"
import { leadsSomewhere, walkEntries } from "./paths"

export type EntryCheck = { ok: true } | { ok: false; reason: string }

/**
 * Whether a request has a way in that reaches something handling it, or an operator can reach a cluster; else why
 * not, said for this design. The Build canvas follows the whole design, every environment included.
 */
export async function checkRequestEntry(wf: Workflow, version: string): Promise<EntryCheck> {
  const kind = isDocker(wf) ? "docker" : isKubernetes(wf) ? "kubernetes" : "terraform"
  let model: ArchModel
  let d: Describer
  if (kind === "docker") {
    model = deriveDockerArchitecture(wf)
    d = describeDocker(model, wf.compose?.graph ?? { nodes: [], edges: [] })
  } else if (kind === "kubernetes") {
    model = deriveKubernetesArchitecture(wf)
    d = describeKubernetes(model, wf.kubernetes?.graph ?? { nodes: [], edges: [] })
  } else {
    const modules = wf.nodes.some((n) => n.kind === "module")
      ? await queryClient.fetchQuery({ queryKey: ["module-tree", wf.id, version], queryFn: () => api.moduleTree(wf.id), staleTime: Infinity })
      : undefined
    model = withClusterOperators(deriveArchitecture(wf, { modules, everyEnvironment: true }), wf)
    // Catalog details only change the wording of steps, not where a walk goes.
    d = describeTerraform({ model, nodes: wf.nodes, edges: wf.edges ?? [], schemaOf: () => undefined, deployed: new Set() })
  }
  const traits = (id: string) => d.info.get(id) ?? { async: false, supporting: false, plumbing: false }
  if (operatorEntries(model).length || walkEntries(model).some((e) => leadsSomewhere(model, e, traits))) return { ok: true }
  return { ok: false, reason: noEntryText(model, d, kind) ?? `This design has no entry point yet. ${NO_ENTRY_HINT[kind]}` }
}

// Whether "Follow a request" has somewhere to start on the open workflow: loaded when the Guided tour menu opens,
// so the Build canvas does not carry the Architecture derivation until someone asks.
import { deriveArchitecture } from "@/features/architecture/derive"
import { deriveDockerArchitecture } from "@/features/architecture/docker"
import { deriveKubernetesArchitecture } from "@/features/architecture/kubernetes"
import { api } from "@/lib/api/client"
import { isDocker, isKubernetes, type Workflow } from "@/lib/api/types"
import { queryClient } from "@/lib/query-client"
import { walkEntries } from "./paths"

/** True when Users (or the Internet) reach the design: the Build canvas follows the whole design, every environment included. */
export async function hasRequestEntry(wf: Workflow, version: string): Promise<boolean> {
  if (isDocker(wf)) return walkEntries(deriveDockerArchitecture(wf)).length > 0
  if (isKubernetes(wf)) return walkEntries(deriveKubernetesArchitecture(wf)).length > 0
  const modules = wf.nodes.some((n) => n.kind === "module")
    ? await queryClient.fetchQuery({ queryKey: ["module-tree", wf.id, version], queryFn: () => api.moduleTree(wf.id), staleTime: Infinity })
    : undefined
  return walkEntries(deriveArchitecture(wf, { modules, everyEnvironment: true })).length > 0
}

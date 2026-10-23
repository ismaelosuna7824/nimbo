import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { undoable } from "@/features/docker/actions"
import { api } from "@/lib/api/client"
import type { KubeConnectRecipe, KubeConnectRequest, KubeNode, Position, Workflow } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { setChartExpanded } from "./canvas/charts"

/** MIME type of palette entries dragged onto the Kubernetes canvas (JSON: a KubePaletteDrag). */
export const KUBE_DRAG_TYPE = "application/x-nimbo-kubernetes"

/** What a dragged palette entry carries. */
export interface KubePaletteDrag {
  itemId: string
  label: string
}

/** Inputs some connection recipes take (Ingress host/path/port, mount path, env var name). */
export type KubeRecipeInputs = Pick<KubeConnectRequest, "port" | "path" | "host" | "key">

/** Imperative Kubernetes canvas operations (command palette, inspector). */
export interface KubeCanvasHandle {
  tidy: () => Promise<void>
  /** Expands a vendored chart with its objects laid out, then focuses it (or `focus`, one of its objects). */
  expandChart: (chartId: string, focus?: string) => Promise<void>
  /** Brings an object or chart into view, expanding the collapsed chart holding it. */
  reveal: (id: string) => Promise<void>
}

let handle: KubeCanvasHandle | null = null

export function registerKubeCanvas(h: KubeCanvasHandle | null) {
  handle = h
}

export function kubeCanvasHandle() {
  return handle
}

function current(): Workflow | null {
  return useWorkflowStore.getState().workflow
}

/** Expands a vendored chart: on the canvas when it is shown, else remembered for the next time it is. */
export async function expandChart(chartId: string) {
  const wf = current()
  if (!wf) return
  if (handle) await handle.expandChart(chartId)
  else setChartExpanded(wf.id, chartId, true)
}

/** Selects an object or chart and brings it into view on the canvas (expanding the collapsed chart holding it). */
export async function showObject(id: string) {
  useWorkflowStore.getState().select(id)
  await handle?.reveal(id)
}

/** Collapses a vendored chart into its release node, which takes over the selection of its objects. */
export function collapseChart(chartId: string) {
  const store = useWorkflowStore.getState()
  const wf = store.workflow
  if (!wf) return
  setChartExpanded(wf.id, chartId, false)
  const selected = wf.kubernetes?.graph.nodes.find((n) => n.id === store.selectedNodeId)
  if (selected?.chart === chartId) store.select(chartId)
}

/** Text files an object edit can touch (manifests, kustomizations, values, Helm templates): what Undo writes back. */
function editableFiles(wf: Workflow): string[] {
  return (wf.kubernetes?.files ?? []).filter((p) => /\.(ya?ml|tpl)$/i.test(p))
}

/**
 * Adds a palette item (starter, image, Helm chart) and selects the first object it created. Dropped items land at
 * `position`; clicked ones get none, so the canvas places them next to their siblings (see placeNew).
 */
export async function addFromPalette(itemId: string, position?: Position) {
  const wf = current()
  if (!wf) return
  const store = useWorkflowStore.getState()
  try {
    const res = await api.addKubernetesItem(wf.id, { itemId, position })
    store.setWorkflow(res.workflow)
    if (res.nodeIds[0]) store.select(res.nodeIds[0])
  } catch (err) {
    toast.error("Couldn't add it", { description: errorMessage(err) })
  }
}

/** Removes an object's YAML document (and its kustomization entry when the file becomes empty). */
export async function deleteObject(node: KubeNode) {
  const ok = await undoable(`Deleted ${node.kind} ${node.name}`, (wf) => api.deleteKubernetesObject(wf.id, node.id), "Couldn't delete", editableFiles)
  if (ok) useWorkflowStore.getState().select(null)
}

/** Applies a connection recipe: `from` refers to `to` ("" = a new object, e.g. expose via a new Service). */
export function applyRecipe(from: string, to: string, recipe: KubeConnectRecipe, inputs: KubeRecipeInputs = {}) {
  return undoable(
    recipe.label,
    (wf) => api.kubernetesConnect(wf.id, { from, to, recipeId: recipe.id, kind: recipe.kind, ...inputs }),
    "Couldn't connect",
    editableFiles,
  )
}

/** Writes inspector edits of an object (field paths of GET …/kubernetes/fields). */
export async function patchObject(node: KubeNode, set?: Record<string, unknown>, unset?: string[]) {
  const wf = current()
  if (!wf) return false
  try {
    useWorkflowStore.getState().setWorkflow(await api.patchKubernetesObject(wf.id, { node: node.id, set, unset }))
    return true
  } catch (err) {
    toast.error(`Couldn't update ${node.name}`, { description: errorMessage(err) })
    return false
  }
}

/** Rolls the pods of a workload over (kubectl rollout restart) on the local cluster. */
export async function restartWorkload(node: KubeNode) {
  const wf = current()
  if (!wf) return
  try {
    await api.startKubeRun(wf.id, { action: "restart", workload: node.id })
    toast.success(`Restarting ${node.name}`)
  } catch (err) {
    toast.error(`Couldn't restart ${node.name}`, { description: errorMessage(err) })
  }
}

/** Saves canvas positions. */
export async function savePositions(positions: Map<string, Position>) {
  const wf = current()
  if (!wf || positions.size === 0) return
  try {
    useWorkflowStore.getState().setWorkflow(await api.kubernetesLayout(wf.id, Object.fromEntries(positions)))
  } catch (err) {
    toast.error("Couldn't save the layout", { description: errorMessage(err) })
  }
}

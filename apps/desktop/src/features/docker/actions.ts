import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { ComposeConnectRecipe, ComposeNode, DockerStarter, Position, ServicePatch, Workflow } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

/** MIME type of palette entries dragged onto the Docker canvas (JSON: a DockerPaletteItem). */
export const DOCKER_DRAG_TYPE = "application/x-nimbo-docker"

/** What a palette entry adds. */
export type DockerPaletteItem =
  | { kind: "image"; catalogId: string; label: string }
  | { kind: "build"; starter?: DockerStarter }
  | { kind: "network" }
  | { kind: "volume" }

/** Imperative Docker canvas operations for the command palette. */
export interface DockerCanvasHandle {
  tidy: () => Promise<void>
}

let handle: DockerCanvasHandle | null = null

export function registerDockerCanvas(h: DockerCanvasHandle | null) {
  handle = h
}

export function dockerCanvasHandle() {
  return handle
}

function current(): Workflow | null {
  return useWorkflowStore.getState().workflow
}

/** A free name: base, base-2, base-3… */
export function freeName(base: string, taken: Iterable<string>): string {
  const set = new Set(taken)
  if (!set.has(base)) return base
  for (let i = 2; ; i++) if (!set.has(`${base}-${i}`)) return `${base}-${i}`
}

function namesOf(wf: Workflow, kind: ComposeNode["kind"]) {
  return (wf.compose?.graph.nodes ?? []).filter((n) => n.kind === kind || (n.kind === "external" && n.externalKind === kind)).map((n) => n.name)
}

/**
 * Adds what a palette entry stands for and selects it. Dropped entries land at `position`; clicked ones get no
 * position, so the canvas places them next to their siblings without covering another card (see placeNew).
 */
export async function addFromPalette(item: DockerPaletteItem, position?: Position) {
  const wf = current()
  if (!wf) return
  const store = useWorkflowStore.getState()
  try {
    let res: { workflow: Workflow; nodeId: string }
    switch (item.kind) {
      case "image":
        res = await api.addService(wf.id, { catalogId: item.catalogId, position })
        break
      case "build": {
        const name = freeName("app", namesOf(wf, "service"))
        res = await api.addService(wf.id, { name, build: { context: `./${name}`, starter: item.starter }, position })
        break
      }
      case "network":
        res = await api.addNetwork(wf.id, { name: freeName("backend", namesOf(wf, "network")) })
        if (position) res.workflow = await api.composeLayout(wf.id, { [res.nodeId]: position })
        break
      case "volume":
        res = await api.addVolume(wf.id, { name: freeName("data", namesOf(wf, "volume")) })
        if (position) res.workflow = await api.composeLayout(wf.id, { [res.nodeId]: position })
        break
    }
    store.setWorkflow(res.workflow)
    store.select(res.nodeId)
  } catch (err) {
    toast.error("Couldn't add it", { description: errorMessage(err) })
  }
}

/** The files as they are now, to undo a canvas edit by writing them back. */
async function snapshot(wf: Workflow, files: string[]): Promise<Record<string, string>> {
  const entries = await Promise.all(files.map(async (p) => [p, (await api.fileContent(wf.id, p)).content] as const))
  return Object.fromEntries(entries)
}

async function restore(wf: Workflow, files: Record<string, string | null>) {
  try {
    useWorkflowStore.getState().setWorkflow(await api.applyFileChanges(wf.id, { changes: files }))
  } catch (err) {
    toast.error("Undo failed", { description: errorMessage(err) })
  }
}

/**
 * Runs a canvas edit with an Undo toast that writes `files` (default: the compose files) back as they were and
 * deletes the ones the edit created. The Kubernetes canvas passes its manifests.
 */
export async function undoable(
  label: string,
  edit: (wf: Workflow) => Promise<Workflow>,
  failure: string,
  files: (wf: Workflow) => string[] = (wf) => wf.compose?.files ?? [],
) {
  const wf = current()
  if (!wf) return false
  let before: Record<string, string> | null = null
  try {
    before = await snapshot(wf, files(wf))
  } catch {
    // No undo without the previous text; the edit itself still goes ahead.
  }
  try {
    const next = await edit(wf)
    useWorkflowStore.getState().setWorkflow(next)
    const undo = before && { ...Object.fromEntries(files(next).filter((p) => !(p in before!)).map((p) => [p, null])), ...before }
    toast.success(label, undo ? { action: { label: "Undo", onClick: () => void restore(wf, undo) } } : undefined)
    return true
  } catch (err) {
    toast.error(failure, { description: errorMessage(err) })
    return false
  }
}

/** Deletes a service, network, volume, secret or config from the compose files (references are cleaned up). */
export async function deleteComposeNode(node: ComposeNode) {
  const kind = node.kind === "external" ? node.externalKind : node.kind
  const ok = await undoable(
    `Deleted ${node.name}`,
    (wf) => {
      switch (kind) {
        case "service":
          return api.deleteService(wf.id, node.name)
        case "network":
          return api.deleteNetwork(wf.id, node.name)
        case "volume":
          return api.deleteVolume(wf.id, node.name)
        default:
          throw new Error(`Remove ${node.name} in the Code tab: ${kind ?? node.kind} entries are edited as code`)
      }
    },
    "Couldn't delete",
  )
  if (ok) useWorkflowStore.getState().select(null)
}

/** Applies a connection recipe: `from` uses `to`. */
export function applyRecipe(from: string, to: string, recipe: ComposeConnectRecipe, target?: string) {
  return undoable(
    recipe.label,
    (wf) =>
      api.composeConnect(wf.id, {
        from,
        to,
        kind: recipe.kind,
        recipeId: recipe.id,
        condition: recipe.condition,
        network: recipe.network,
        target: target ?? recipe.target,
      }),
    "Couldn't connect",
  )
}

/** Writes inspector edits of a service. */
export async function patchService(name: string, patch: ServicePatch) {
  const wf = current()
  if (!wf) return false
  try {
    useWorkflowStore.getState().setWorkflow(await api.patchService(wf.id, name, patch))
    return true
  } catch (err) {
    toast.error(`Couldn't update ${name}`, { description: errorMessage(err) })
    return false
  }
}

/** Saves canvas positions (a layout-only save: views that don't depend on positions don't refetch). */
export async function savePositions(positions: Map<string, Position>) {
  if (positions.size === 0) return
  try {
    await useWorkflowStore.getState().saveLayout((wf) => api.composeLayout(wf.id, Object.fromEntries(positions)))
  } catch (err) {
    toast.error("Couldn't save the layout", { description: errorMessage(err) })
  }
}

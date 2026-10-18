import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { NodeKind, Position } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

/** MIME type used when dragging palette entries onto the canvas. */
export const RESOURCE_DRAG_TYPE = "application/x-nimbo-resource"

/** Imperative canvas operations exposed to the palette, toolbar and command palette. */
export interface CanvasHandle {
  /** Flow position at the visible center of the canvas, offset so a new node is centered there. */
  center: () => Position
  /** Re-layouts every node and saves the positions. */
  tidy: () => Promise<void>
  /** Deletes a node, asking for confirmation when other nodes reference it. */
  remove: (nodeId: string) => void
}

let handle: CanvasHandle | null = null

export function registerCanvas(h: CanvasHandle | null) {
  handle = h
}

export function canvasHandle() {
  return handle
}

export async function addResource(kind: NodeKind, type: string, position?: Position) {
  const store = useWorkflowStore.getState()
  const wf = store.workflow
  if (!wf) return
  await store.flush()
  try {
    const { workflow, nodeId } = await api.addNode(wf.id, {
      kind,
      type,
      position: position ?? handle?.center() ?? { x: 0, y: 0 },
    })
    store.setWorkflow(workflow)
    store.select(nodeId)
  } catch (err) {
    toast.error("Couldn't add the resource", { description: errorMessage(err) })
  }
}

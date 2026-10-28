import {
  applyEdgeChanges,
  applyNodeChanges,
  ReactFlow,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
  type ReactFlowProps,
} from "@xyflow/react"
import { createContext, memo, useCallback, useContext, type ReactNode } from "react"
import { sameFlowNode } from "@/components/flow-items"
import { ContextMenu, ContextMenuTrigger } from "@/components/ui/context-menu"
import { useSyncedState } from "@/components/use-synced-state"

/**
 * Rebuilt nodes keep React Flow's DOM measurement (`measured`) from the local copy. A node object without it makes
 * React Flow drop that node's handle bounds and re-measure; when that reset lands in the same commit as the previous
 * re-measure, React Flow never observes the node again and its handles stay dead (drags from or onto them do nothing).
 * Unchanged nodes keep their measured local object, so their cards don't re-render either.
 */
function keepMeasured<N extends Node>(next: N[], current: N[]): N[] {
  const byId = new Map(current.map((n) => [n.id, n]))
  return next.map((n) => {
    const local = byId.get(n.id)
    if (!local?.measured || n.measured) return n
    return sameFlowNode(local, n) ? local : { ...n, measured: local.measured }
  })
}

type CanvasFlowProps<N extends Node, E extends Edge> = Omit<ReactFlowProps<N, E>, "nodes" | "edges" | "onNodesChange" | "onEdgesChange"> & {
  nodes: N[]
  edges: E[]
  /** A click selects an edge (otherwise edges keep the selection they are given). */
  selectableEdges?: boolean
}

/**
 * React Flow with its own copy of the nodes (and edges), reset whenever the given ones change: drags and selection
 * move the copy, so only this component (not the canvas around it, with its menus and dialogs) re-renders on every
 * drag step. Nodes are never removed through React Flow (canvases delete through the daemon).
 */
function CanvasFlowImpl<N extends Node, E extends Edge>({ nodes: given, edges: givenEdges, selectableEdges, ...props }: CanvasFlowProps<N, E>) {
  const [nodes, setNodes] = useSyncedState(given, keepMeasured)
  const [edges, setEdges] = useSyncedState(givenEdges)
  const onNodesChange = useCallback(
    (changes: NodeChange<N>[]) => setNodes((ns) => applyNodeChanges(changes.filter((c) => c.type !== "remove"), ns)),
    [setNodes],
  )
  const onEdgesChange = useCallback(
    (changes: EdgeChange<E>[]) => setEdges((es) => applyEdgeChanges(changes.filter((c) => c.type === "select"), es)),
    [setEdges],
  )
  return (
    <ReactFlow<N, E>
      {...props}
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onEdgesChange={selectableEdges ? onEdgesChange : undefined}
    />
  )
}

export const CanvasFlow = memo(CanvasFlowImpl) as typeof CanvasFlowImpl

/** The flow drawn inside CanvasContextMenu's trigger. */
const MenuTarget = createContext<ReactNode>(null)

function MenuTargetSlot() {
  return useContext(MenuTarget)
}

const MenuFrame = memo(function MenuFrame({ menu }: { menu: ReactNode }) {
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div className="h-full w-full">
          <MenuTargetSlot />
        </div>
      </ContextMenuTrigger>
      {menu}
    </ContextMenu>
  )
})

/**
 * A canvas (`children`) with its right-click menu (`menu`: the ContextMenuContent, memoized by the caller). The
 * canvas changes on every edit; it reaches the trigger through context, so the menu's own tree (root, trigger,
 * closed content) only re-renders when `menu` changes.
 */
export function CanvasContextMenu({ menu, children }: { menu: ReactNode; children: ReactNode }) {
  return (
    <MenuTarget.Provider value={children}>
      <MenuFrame menu={menu} />
    </MenuTarget.Provider>
  )
}
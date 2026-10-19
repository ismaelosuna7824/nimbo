import {
  applyEdgeChanges,
  applyNodeChanges,
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  MiniMap,
  Panel,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Connection,
  type EdgeChange,
  type NodeChange,
  type OnSelectionChangeParams,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import {
  LayoutTemplateIcon,
  MousePointerClickIcon,
  PackageIcon,
  PackageOpenIcon,
  PackageSearchIcon,
  Trash2Icon,
  WandSparklesIcon,
} from "lucide-react"
import { useTheme } from "next-themes"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { CLOUD_META, cloudOfType } from "@/components/cloud-meta"
import { errorMessage } from "@/components/errors"
import { useSyncedState } from "@/components/use-synced-state"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { api } from "@/lib/api/client"
import type { ConnectionRecipe, HopVerdict, NodeKind, Position, Workflow, WorkflowNode } from "@/lib/api/types"
import { useReachability } from "@/features/network/store"
import { useWorkflowStore } from "@/state/workflow-store"
import { addResource, registerCanvas, RESOURCE_DRAG_TYPE } from "./actions"
import { nodeLabel, schemaKey, useNodeSchemas } from "./catalog"
import { ConnectDialog, type PendingConnection } from "./connect-dialog"
import { computeLayout, GROUP_HEADER, GROUP_PAD, layoutUnpositioned, NODE_HEIGHT, NODE_WIDTH, parentMap } from "./layout"
import { isUsable, useEndpoints } from "../panels/endpoints/use-endpoints"
import { ModuleOverlay } from "./module-view"
import { openModuleView } from "./module-view-store"
import { ModuleRefactorDialogs } from "./module-refactor"
import { openExtractModule, openInlineModule } from "./module-refactor-store"
import { GroupNode, RefEdge, ResourceNode, type RefFlowEdge, type ResourceFlowNode } from "./nodes"

const nodeTypes = { resource: ResourceNode, container: GroupNode }
const edgeTypes = { ref: RefEdge }

interface Rect {
  x: number
  y: number
  w: number
  h: number
}

/** Absolute rectangles: leaves use their stored position; groups wrap their descendants. */
function nodeRects(nodes: WorkflowNode[], parents: Map<string, string>) {
  const children = new Map<string, WorkflowNode[]>()
  for (const n of nodes) {
    const p = parents.get(n.id)
    if (p) children.set(p, [...(children.get(p) ?? []), n])
  }
  const rects = new Map<string, Rect>()
  const rectOf = (n: WorkflowNode): Rect => {
    const cached = rects.get(n.id)
    if (cached) return cached
    const kids = children.get(n.id)
    let r: Rect
    if (!kids) {
      r = { x: n.position.x, y: n.position.y, w: NODE_WIDTH, h: NODE_HEIGHT }
    } else {
      const rs = kids.map(rectOf)
      const minX = Math.min(...rs.map((k) => k.x)) - GROUP_PAD
      const minY = Math.min(...rs.map((k) => k.y)) - GROUP_HEADER
      const maxX = Math.max(...rs.map((k) => k.x + k.w)) + GROUP_PAD
      const maxY = Math.max(...rs.map((k) => k.y + k.h)) + GROUP_PAD
      r = { x: minX, y: minY, w: Math.max(maxX - minX, NODE_WIDTH + 2 * GROUP_PAD), h: maxY - minY }
    }
    rects.set(n.id, r)
    return r
  }
  nodes.forEach(rectOf)
  return { rects, children }
}

function depthOf(id: string, parents: Map<string, string>) {
  let d = 0
  for (let cur = parents.get(id); cur; cur = parents.get(cur)) d++
  return d
}

function isEditableTarget(el: Element | null) {
  if (!el) return false
  const tag = el.tagName
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || (el as HTMLElement).isContentEditable
}

export function Canvas() {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  if (!workflowId) return null
  return (
    <ReactFlowProvider key={workflowId}>
      <CanvasInner />
    </ReactFlowProvider>
  )
}

/** Shallow equality of two plain objects (React Flow node/edge data). */
function sameFields(a: Record<string, unknown> | undefined, b: Record<string, unknown> | undefined) {
  if (a === b) return true
  if (!a || !b) return false
  const keys = Object.keys(a)
  return keys.length === Object.keys(b).length && keys.every((k) => Object.is(a[k], b[k]))
}

function sameFlowNode(a: ResourceFlowNode, b: ResourceFlowNode) {
  return (
    a.type === b.type &&
    a.parentId === b.parentId &&
    a.width === b.width &&
    a.height === b.height &&
    a.zIndex === b.zIndex &&
    a.selected === b.selected &&
    a.className === b.className &&
    a.position.x === b.position.x &&
    a.position.y === b.position.y &&
    sameFields(a.data, b.data)
  )
}

function sameFlowEdge(a: RefFlowEdge, b: RefFlowEdge) {
  return a.source === b.source && a.target === b.target && sameFields(a.data, b.data)
}

/**
 * Rebuilt nodes keep React Flow's DOM measurement (`measured`) from the local copy. A node object without it makes
 * React Flow drop that node's handle bounds and re-measure; when that reset lands in the same commit as the previous
 * re-measure, React Flow never observes the node again and its handles stay dead (drags from or onto them do nothing).
 * Unchanged nodes keep their measured local object, so their cards don't re-render either.
 */
function keepMeasured(next: ResourceFlowNode[], current: ResourceFlowNode[]): ResourceFlowNode[] {
  const byId = new Map(current.map((n) => [n.id, n]))
  return next.map((n) => {
    const local = byId.get(n.id)
    if (!local?.measured || n.measured) return n
    return sameFlowNode(local, n) ? local : { ...n, measured: local.measured }
  })
}

/**
 * Returns `items` with every item that `same` finds unchanged (by id) replaced
 * by the object from the previous call (or the previous array itself when
 * nothing changed), so React Flow only re-renders the cards and edges whose
 * content changed.
 */
function useStableItems<T extends { id: string }>(items: T[], same: (a: T, b: T) => boolean): T[] {
  // Holder kept across renders; only read and written while memoizing the next list.
  const [cache] = useState(() => ({ byId: new Map<string, T>(), list: [] as T[] }))
  return useMemo(() => {
    const byId = new Map<string, T>()
    let unchanged = items.length === cache.list.length
    const list = items.map((item, i) => {
      const old = cache.byId.get(item.id)
      const kept = old && same(old, item) ? old : item
      if (kept !== cache.list[i]) unchanged = false
      byId.set(item.id, kept)
      return kept
    })
    if (!unchanged) {
      cache.byId = byId
      cache.list = list
    }
    return cache.list
  }, [items, same, cache])
}

const EDGE_MARKER = { type: MarkerType.ArrowClosed, width: 16, height: 16 }
const SNAP_GRID: [number, number] = [16, 16]
const FIT_VIEW_OPTIONS = { padding: 0.2, maxZoom: 1.1 }
const PRO_OPTIONS = { hideAttribution: true }
const MINIMAP_STYLE = { width: 168, height: 112 }

/** Canvas ring of a node on a checked reachability path, by hop verdict. */
const HOP_RING: Record<HopVerdict, string> = {
  allow: "rounded-xl ring-2 ring-emerald-500 ring-offset-2 ring-offset-background",
  deny: "rounded-xl ring-2 ring-destructive ring-offset-2 ring-offset-background",
  unknown: "rounded-xl ring-2 ring-amber-500 ring-offset-2 ring-offset-background",
}

function minimapColor(n: ResourceFlowNode) {
  const cloud = n.data.schema?.cloud ?? cloudOfType(n.data.node.type)
  return cloud ? CLOUD_META[cloud].accent : "#a855f7"
}

function isValidConnection(c: Connection | RefFlowEdge) {
  return c.source !== c.target
}

function CanvasInner() {
  const wfNodes = useWorkflowStore((s) => s.workflow!.nodes)
  const wfEdges = useWorkflowStore((s) => s.workflow!.edges)
  const diagnostics = useWorkflowStore((s) => s.workflow!.diagnostics)
  const selectedNodeId = useWorkflowStore((s) => s.selectedNodeId)
  const deployed = useWorkflowStore((s) => s.deployed)
  const select = useWorkflowStore((s) => s.select)
  const update = useWorkflowStore((s) => s.update)
  const setWorkflow = useWorkflowStore((s) => s.setWorkflow)
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const { resolvedTheme } = useTheme()
  const flow = useReactFlow<ResourceFlowNode, RefFlowEdge>()
  const wrapper = useRef<HTMLDivElement>(null)
  const schemas = useNodeSchemas(wfNodes)
  const [hoveredEdge, setHoveredEdge] = useState<string | null>(null)
  const [pending, setPending] = useState<PendingConnection | null>(null)
  const [pendingDelete, setPendingDelete] = useState<{ node: WorkflowNode; users: number } | null>(null)
  const endpoints = useEndpoints().data

  const parents = useMemo(() => parentMap(wfNodes), [wfNodes])
  const { rects, children } = useMemo(() => nodeRects(wfNodes, parents), [wfNodes, parents])
  const deployedIds = useMemo(
    () => new Set((deployed?.resources ?? []).flatMap((r) => (r.nodeId ? [r.nodeId] : []))),
    [deployed],
  )

  const builtNodes = useMemo(() => {
    // First usable resource per node: what the card's "plug" button opens.
    const endpointOf = new Map<string, string>()
    for (const r of endpoints?.resources ?? []) {
      if (r.nodeId && !endpointOf.has(r.nodeId) && isUsable(r)) endpointOf.set(r.nodeId, r.address)
    }
    const diag = new Map<string, { errors: number; warnings: number }>()
    for (const d of diagnostics ?? []) {
      if (!d.nodeId) continue
      const c = diag.get(d.nodeId) ?? { errors: 0, warnings: 0 }
      if (d.severity === "error") c.errors++
      else if (d.severity === "warning") c.warnings++
      diag.set(d.nodeId, c)
    }
    const ordered = [...wfNodes].sort((a, b) => depthOf(a.id, parents) - depthOf(b.id, parents))
    return ordered.map((n): ResourceFlowNode => {
      const r = rects.get(n.id)!
      const parentId = parents.get(n.id)
      const pr = parentId ? rects.get(parentId)! : undefined
      const isGroup = children.has(n.id)
      const schema = schemas.get(schemaKey(n.kind, n.type))
      return {
        id: n.id,
        type: isGroup ? "container" : "resource",
        position: pr ? { x: r.x - pr.x, y: r.y - pr.y } : { x: r.x, y: r.y },
        parentId,
        width: r.w,
        height: r.h,
        selected: false,
        zIndex: isGroup ? -1 : undefined,
        data: {
          node: n,
          schema,
          label: nodeLabel(n, schema),
          deployed: deployedIds.has(n.id),
          endpointAddress: endpointOf.get(n.id),
          errors: diag.get(n.id)?.errors ?? 0,
          warnings: diag.get(n.id)?.warnings ?? 0,
        },
      }
    })
  }, [wfNodes, diagnostics, deployedIds, endpoints, parents, rects, children, schemas])
  const stableNodes = useStableItems(builtNodes, sameFlowNode)
  // Multi-selection (Shift/Cmd/Ctrl-click, Shift-drag box) as React Flow reports it; the inspector's node wins when it is
  // selected elsewhere (e.g. from Problems).
  const [selection, setSelection] = useState<string[]>([])
  const selectedIds = useMemo(
    () => (selectedNodeId && !selection.includes(selectedNodeId) ? [selectedNodeId] : selection),
    [selection, selectedNodeId],
  )
  // While the reachability dialog shows an answer: ring the nodes on the path by verdict, dim the rest.
  const reachHighlight = useReachability((s) => s.highlight)
  const derivedNodes = useMemo(() => {
    const selectedNodes = selectedIds.length
      ? stableNodes.map((n) => (selectedIds.includes(n.id) ? { ...n, selected: true } : n))
      : stableNodes
    if (!reachHighlight) return selectedNodes
    return selectedNodes.map((n) => {
      const verdict = reachHighlight[n.id]
      return { ...n, className: verdict ? HOP_RING[verdict] : "opacity-35" }
    })
  }, [stableNodes, selectedIds, reachHighlight])
  const onSelectionChange = useCallback(
    ({ nodes: picked }: OnSelectionChangeParams) => {
      const ids = picked.map((n) => n.id)
      setSelection((prev) => (prev.length === ids.length && prev.every((id) => ids.includes(id)) ? prev : ids))
      const current = useWorkflowStore.getState().selectedNodeId
      if (ids.length === 1 && current !== ids[0]) select(ids[0])
      else if (ids.length > 1 && current && !ids.includes(current)) select(null)
    },
    [select],
  )

  const builtEdges = useMemo(() => {
    const ids = new Set(wfNodes.map((n) => n.id))
    return (wfEdges ?? [])
      .filter((e) => ids.has(e.source) && ids.has(e.target))
      .map(
        (e): RefFlowEdge => ({
          id: e.id,
          source: e.source,
          target: e.target,
          type: "ref",
          markerEnd: EDGE_MARKER,
          data: {
            kind: e.kind,
            attribute: e.attribute,
            sourceAttr: e.sourceAttr,
            hovered: false,
            live: deployedIds.has(e.source) && deployedIds.has(e.target),
          },
        }),
      )
  }, [wfNodes, wfEdges, deployedIds])
  const stableEdges = useStableItems(builtEdges, sameFlowEdge)
  const derivedEdges = useMemo(
    () =>
      hoveredEdge
        ? stableEdges.map((e) => (e.id === hoveredEdge ? { ...e, data: { ...e.data!, hovered: true } } : e))
        : stableEdges,
    [stableEdges, hoveredEdge],
  )

  const [nodes, setNodes] = useSyncedState(derivedNodes, keepMeasured)
  const [edges, setEdges] = useSyncedState(derivedEdges)
  /** What the context menu acts on: the right-clicked node (or the selection containing it); null = the pane. */
  const [menu, setMenu] = useState<{ ids: string[]; node?: WorkflowNode } | null>(null)
  const onNodesChange = useCallback(
    (changes: NodeChange<ResourceFlowNode>[]) =>
      setNodes((ns) => applyNodeChanges(changes.filter((c) => c.type !== "remove"), ns)),
    [setNodes],
  )
  const onEdgesChange = useCallback(
    (changes: EdgeChange<RefFlowEdge>[]) =>
      setEdges((es) => applyEdgeChanges(changes.filter((c) => c.type === "select"), es)),
    [setEdges],
  )

  const savePositions = useCallback(
    (positions: Map<string, Position>) => {
      if (positions.size === 0) return
      update((draft) => ({
        ...draft,
        nodes: draft.nodes.map((n) => {
          const p = positions.get(n.id)
          return p ? { ...n, position: p, positioned: true } : n
        }),
      }))
    },
    [update],
  )

  // Auto-layout nodes that were never placed (imports, templates).
  const layoutKey = useMemo(
    () =>
      wfNodes
        .filter((n) => !n.positioned)
        .map((n) => n.id)
        .join(","),
    [wfNodes],
  )
  const layoutRunning = useRef("")
  useEffect(() => {
    if (!layoutKey || layoutRunning.current === layoutKey) return
    layoutRunning.current = layoutKey
    const { nodes: current, edges: currentEdges } = useWorkflowStore.getState().workflow!
    const allUnplaced = current.every((n) => !n.positioned)
    void layoutUnpositioned(current, currentEdges ?? [])
      .then((positions) => {
        savePositions(positions)
        if (allUnplaced) window.requestAnimationFrame(() => void flow.fitView({ padding: 0.15, duration: 300 }))
      })
      .catch((err) => toast.error("Automatic layout failed", { description: errorMessage(err) }))
  }, [layoutKey, savePositions, flow])

  const tidy = useCallback(async () => {
    const current = useWorkflowStore.getState().workflow
    if (!current || current.nodes.length === 0) return
    const positions = await computeLayout(current.nodes, current.edges ?? [])
    savePositions(positions)
    window.requestAnimationFrame(() => void flow.fitView({ padding: 0.15, duration: 400 }))
  }, [savePositions, flow])

  const onNodeDragStop = useCallback(
    (_: unknown, __: ResourceFlowNode, dragged: ResourceFlowNode[]) => {
      const draggedIds = new Set(dragged.map((d) => d.id))
      const positions = new Map<string, Position>()
      for (const d of dragged) {
        let hasDraggedAncestor = false
        for (let cur = parents.get(d.id); cur; cur = parents.get(cur)) if (draggedIds.has(cur)) hasDraggedAncestor = true
        if (hasDraggedAncestor) continue
        const abs = flow.getInternalNode(d.id)?.internals.positionAbsolute
        const before = rects.get(d.id)
        if (!abs || !before) continue
        const dx = Math.round(abs.x - before.x)
        const dy = Math.round(abs.y - before.y)
        if (dx === 0 && dy === 0) continue
        const shift = (id: string) => {
          const n = wfNodes.find((x) => x.id === id)
          if (!n) return
          positions.set(id, { x: n.position.x + dx, y: n.position.y + dy })
          for (const c of children.get(id) ?? []) shift(c.id)
        }
        shift(d.id)
      }
      savePositions(positions)
    },
    [flow, parents, rects, children, wfNodes, savePositions],
  )

  const onDragOver = useCallback((e: React.DragEvent) => {
    if (!e.dataTransfer.types.includes(RESOURCE_DRAG_TYPE)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = "copy"
  }, [])

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      const raw = e.dataTransfer.getData(RESOURCE_DRAG_TYPE)
      if (!raw) return
      e.preventDefault()
      const { kind, type } = JSON.parse(raw) as { kind: NodeKind; type: string }
      const p = flow.screenToFlowPosition({ x: e.clientX, y: e.clientY })
      void addResource(kind, type, { x: Math.round(p.x - NODE_WIDTH / 2), y: Math.round(p.y - NODE_HEIGHT / 2) })
    },
    [flow],
  )

  const restore = useCallback(
    async (prev: Workflow) => {
      try {
        setWorkflow(await api.saveWorkflow(prev))
      } catch (err) {
        toast.error("Undo failed", { description: errorMessage(err) })
      }
    },
    [setWorkflow],
  )

  const applyRecipe = useCallback(
    async (source: string, target: string, recipe: ConnectionRecipe) => {
      const before = useWorkflowStore.getState().workflow!
      try {
        setWorkflow(await api.connect(before.id, { source, target, recipeId: recipe.id }))
        toast.success("Connected", {
          description: recipe.label,
          action: { label: "Undo", onClick: () => void restore(before) },
        })
      } catch (err) {
        toast.error("Couldn't connect", { description: errorMessage(err) })
      }
    },
    [setWorkflow, restore],
  )

  const onConnect = useCallback(
    async ({ source, target }: Connection) => {
      if (!source || !target || source === target) return
      await useWorkflowStore.getState().flush()
      const current = useWorkflowStore.getState().workflow!
      let recipes: ConnectionRecipe[]
      try {
        recipes = await api.connections(current.id, source, target)
      } catch (err) {
        toast.error("Couldn't load connection options", { description: errorMessage(err) })
        return
      }
      const describe = (id: string) => {
        const n = current.nodes.find((x) => x.id === id)!
        const schema = schemas.get(schemaKey(n.kind, n.type))
        return { id, label: nodeLabel(n, schema), icon: schema?.icon }
      }
      if (recipes.length === 0) {
        toast.info("No direct connection between these resources", {
          description: `Nothing in ${describe(target).label} can point at ${describe(source).label}. Try connecting the other way round.`,
        })
        return
      }
      const curated = recipes.filter((r) => r.curated)
      if (useWorkflowStore.getState().mode === "guided" && curated.length === 1) {
        await applyRecipe(source, target, curated[0])
        return
      }
      setPending({ source: describe(source), target: describe(target), recipes })
    },
    [schemas, applyRecipe],
  )

  const deleteNode = useCallback(
    async (node: WorkflowNode) => {
      await useWorkflowStore.getState().flush()
      const before = useWorkflowStore.getState().workflow!
      try {
        setWorkflow(await api.deleteNode(before.id, node.id))
        select(null)
        const schema = schemas.get(schemaKey(node.kind, node.type))
        toast.success(`Deleted ${nodeLabel(node, schema)}`, {
          action: { label: "Undo", onClick: () => void restore(before) },
        })
      } catch (err) {
        toast.error("Couldn't delete", { description: errorMessage(err) })
      }
    },
    [setWorkflow, select, schemas, restore],
  )

  const requestDelete = useCallback(
    (id: string) => {
      const current = useWorkflowStore.getState().workflow
      const node = current?.nodes.find((n) => n.id === id)
      if (!current || !node) return
      const users = new Set(current.edges.filter((e) => e.source === id).map((e) => e.target)).size
      if (users > 0) setPendingDelete({ node, users })
      else void deleteNode(node)
    },
    [deleteNode],
  )

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Delete" && e.key !== "Backspace") return
      if (isEditableTarget(document.activeElement) || document.querySelector("[role=dialog]")) return
      const id = useWorkflowStore.getState().selectedNodeId
      if (!id) return
      e.preventDefault()
      requestDelete(id)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [requestDelete])

  useEffect(() => {
    registerCanvas({
      center: () => {
        const rect = wrapper.current?.getBoundingClientRect()
        if (!rect) return { x: 0, y: 0 }
        const p = flow.screenToFlowPosition({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 })
        const jitter = (useWorkflowStore.getState().workflow?.nodes.length ?? 0) % 5
        return { x: Math.round(p.x - NODE_WIDTH / 2 + jitter * 24), y: Math.round(p.y - NODE_HEIGHT / 2 + jitter * 24) }
      },
      tidy,
      remove: requestDelete,
    })
    return () => registerCanvas(null)
  }, [flow, tidy, requestDelete])

  // Shift/Cmd/Ctrl-click toggles the node in the selection (handled here rather than through React Flow's key state,
  // so it works whichever modifier the platform uses); a plain click selects just that node.
  const onNodeClick = useCallback(
    (e: React.MouseEvent, n: ResourceFlowNode) => {
      if (!(e.shiftKey || e.metaKey || e.ctrlKey)) {
        select(n.id)
        return
      }
      setSelection((prev) => {
        const current = useWorkflowStore.getState().selectedNodeId
        const base = prev.filter((id) => id !== n.id)
        if (current && current !== n.id && !base.includes(current)) base.push(current)
        return prev.includes(n.id) && base.length > 0 ? base : [...base, n.id]
      })
    },
    [select],
  )
  const onNodeDoubleClick = useCallback(
    (_: unknown, n: ResourceFlowNode) => n.data.node.kind === "module" && void openModuleView(n.id),
    [],
  )
  const onPaneClick = useCallback(() => select(null), [select])
  const onConnectEdge = useCallback((c: Connection) => void onConnect(c), [onConnect])
  const onEdgeMouseEnter = useCallback((_: unknown, e: RefFlowEdge) => setHoveredEdge(e.id), [])
  const onEdgeMouseLeave = useCallback(() => setHoveredEdge(null), [])
  const onNodeContextMenu = useCallback(
    (_: unknown, n: ResourceFlowNode) =>
      setMenu({
        ids: selectedIds.length > 1 && selectedIds.includes(n.id) ? selectedIds : [n.id],
        node: n.data.node,
      }),
    [selectedIds],
  )
  const onSelectionContextMenu = useCallback(
    (_: unknown, ns: ResourceFlowNode[]) => setMenu({ ids: ns.map((n) => n.id) }),
    [],
  )
  const onPaneContextMenu = useCallback(() => setMenu(null), [])
  const menuNode = menu?.ids.length === 1 ? menu.node : undefined
  return (
    <div ref={wrapper} className="relative h-full w-full" onDragOver={onDragOver} onDrop={onDrop}>
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div className="h-full w-full">
            <ReactFlow<ResourceFlowNode, RefFlowEdge>
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onNodeClick={onNodeClick}
              onNodeDoubleClick={onNodeDoubleClick}
              onNodeContextMenu={onNodeContextMenu}
              onSelectionChange={onSelectionChange}
              onSelectionContextMenu={onSelectionContextMenu}
              onPaneContextMenu={onPaneContextMenu}
              onPaneClick={onPaneClick}
              onNodeDragStop={onNodeDragStop}
              onConnect={onConnectEdge}
              isValidConnection={isValidConnection}
              onEdgeMouseEnter={onEdgeMouseEnter}
              onEdgeMouseLeave={onEdgeMouseLeave}
              deleteKeyCode={null}
              colorMode={resolvedTheme === "light" ? "light" : "dark"}
              snapToGrid
              snapGrid={SNAP_GRID}
              fitView
              fitViewOptions={FIT_VIEW_OPTIONS}
              minZoom={0.15}
              maxZoom={2}
              proOptions={PRO_OPTIONS}
              className="bg-background"
            >
              <Background variant={BackgroundVariant.Dots} gap={16} size={1.2} />
              <Controls showInteractive={false} position="bottom-left" />
              <MiniMap
                pannable
                zoomable
                nodeColor={minimapColor}
                nodeStrokeWidth={0}
                position="bottom-right"
                style={MINIMAP_STYLE}
                className="!rounded-lg !border"
              />
              {wfNodes.length > 0 && (
                <Panel position="top-right">
                  <Button variant="outline" size="sm" className="bg-background/80 backdrop-blur" onClick={() => void tidy()}>
                    <WandSparklesIcon />
                    Tidy up
                  </Button>
                </Panel>
              )}
              {selectedIds.length > 1 && (
                <Panel position="top-center">
                  <Button
                    variant="outline"
                    size="sm"
                    className="bg-background/80 backdrop-blur"
                    onClick={() => void openExtractModule(selectedIds)}
                  >
                    <PackageIcon className="text-violet-500" />
                    Extract {selectedIds.length} to module…
                  </Button>
                </Panel>
              )}
            </ReactFlow>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="min-w-52">
          {menu ? (
            <>
              {menuNode?.kind === "module" && (
                <>
                  <ContextMenuItem onSelect={() => void openModuleView(menuNode.id)}>
                    <PackageSearchIcon />
                    Open module
                  </ContextMenuItem>
                  <ContextMenuItem onSelect={() => void openInlineModule(menuNode.id)}>
                    <PackageOpenIcon />
                    Inline module…
                  </ContextMenuItem>
                </>
              )}
              <ContextMenuItem onSelect={() => void openExtractModule(menu.ids)}>
                <PackageIcon />
                {menu.ids.length > 1 ? `Extract ${menu.ids.length} to module…` : "Extract to module…"}
              </ContextMenuItem>
              {menuNode && (
                <>
                  <ContextMenuSeparator />
                  <ContextMenuItem variant="destructive" onSelect={() => requestDelete(menuNode.id)}>
                    <Trash2Icon />
                    Delete
                  </ContextMenuItem>
                </>
              )}
            </>
          ) : (
            <>
              <ContextMenuItem disabled={wfNodes.length === 0} onSelect={() => void tidy()}>
                <WandSparklesIcon />
                Tidy up
              </ContextMenuItem>
              {selectedIds.length > 1 && (
                <ContextMenuItem onSelect={() => void openExtractModule(selectedIds)}>
                  <PackageIcon />
                  Extract {selectedIds.length} selected to module…
                </ContextMenuItem>
              )}
            </>
          )}
        </ContextMenuContent>
      </ContextMenu>
      {wfNodes.length === 0 && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
          <Empty className="pointer-events-auto max-w-md border bg-background/90 backdrop-blur">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <MousePointerClickIcon />
              </EmptyMedia>
              <EmptyTitle>Your canvas is empty</EmptyTitle>
              <EmptyDescription>
                Drag a resource from the left or pick a template. Connect resources by dragging from the dot on the
                right of one card to another.
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" onClick={() => openDialog("new-workflow")}>
                <LayoutTemplateIcon />
                Start from a template
              </Button>
            </EmptyContent>
          </Empty>
        </div>
      )}
      <ModuleOverlay />
      <ModuleRefactorDialogs />
      <ConnectDialog
        pending={pending}
        onClose={() => setPending(null)}
        onPick={(recipe) => {
          const p = pending!
          setPending(null)
          void applyRecipe(p.source.id, p.target.id, recipe)
        }}
      />
      <AlertDialog open={!!pendingDelete} onOpenChange={(o) => !o && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete {pendingDelete && nodeLabel(pendingDelete.node, schemas.get(schemaKey(pendingDelete.node.kind, pendingDelete.node.type)))}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              {pendingDelete?.users === 1 ? "1 other resource uses" : `${pendingDelete?.users} other resources use`} it.
              Those links will break and show up under Problems until you fix them.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                const n = pendingDelete!.node
                setPendingDelete(null)
                void deleteNode(n)
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

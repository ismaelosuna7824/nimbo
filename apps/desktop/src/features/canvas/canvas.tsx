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
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { LayoutTemplateIcon, MousePointerClickIcon, WandSparklesIcon } from "lucide-react"
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
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { api } from "@/lib/api/client"
import type { ConnectionRecipe, NodeKind, Position, Workflow, WorkflowNode } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { addResource, registerCanvas, RESOURCE_DRAG_TYPE } from "./actions"
import { nodeLabel, schemaKey, useNodeSchemas } from "./catalog"
import { ConnectDialog, type PendingConnection } from "./connect-dialog"
import { computeLayout, GROUP_HEADER, GROUP_PAD, layoutUnpositioned, NODE_HEIGHT, NODE_WIDTH, parentMap } from "./layout"
import { isUsable, useEndpoints } from "../panels/endpoints/use-endpoints"
import { ModuleOverlay } from "./module-view"
import { openModuleView } from "./module-view-store"
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

function CanvasInner() {
  const wf = useWorkflowStore((s) => s.workflow)!
  const selectedNodeId = useWorkflowStore((s) => s.selectedNodeId)
  const deployed = useWorkflowStore((s) => s.deployed)
  const select = useWorkflowStore((s) => s.select)
  const update = useWorkflowStore((s) => s.update)
  const setWorkflow = useWorkflowStore((s) => s.setWorkflow)
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const { resolvedTheme } = useTheme()
  const flow = useReactFlow<ResourceFlowNode, RefFlowEdge>()
  const wrapper = useRef<HTMLDivElement>(null)
  const schemas = useNodeSchemas(wf.nodes)
  const [hoveredEdge, setHoveredEdge] = useState<string | null>(null)
  const [pending, setPending] = useState<PendingConnection | null>(null)
  const [pendingDelete, setPendingDelete] = useState<{ node: WorkflowNode; users: number } | null>(null)
  const endpoints = useEndpoints().data

  const parents = useMemo(() => parentMap(wf.nodes), [wf.nodes])
  const { rects, children } = useMemo(() => nodeRects(wf.nodes, parents), [wf.nodes, parents])

  const derivedNodes = useMemo(() => {
    const deployedIds = new Set((deployed?.resources ?? []).flatMap((r) => (r.nodeId ? [r.nodeId] : [])))
    // First usable resource per node: what the card's "plug" button opens.
    const endpointOf = new Map<string, string>()
    for (const r of endpoints?.resources ?? []) {
      if (r.nodeId && !endpointOf.has(r.nodeId) && isUsable(r)) endpointOf.set(r.nodeId, r.address)
    }
    const diag = new Map<string, { errors: number; warnings: number }>()
    for (const d of wf.diagnostics ?? []) {
      if (!d.nodeId) continue
      const c = diag.get(d.nodeId) ?? { errors: 0, warnings: 0 }
      if (d.severity === "error") c.errors++
      else if (d.severity === "warning") c.warnings++
      diag.set(d.nodeId, c)
    }
    const ordered = [...wf.nodes].sort((a, b) => depthOf(a.id, parents) - depthOf(b.id, parents))
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
        selected: n.id === selectedNodeId,
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
  }, [wf.nodes, wf.diagnostics, deployed, endpoints, parents, rects, children, schemas, selectedNodeId])

  const derivedEdges = useMemo(() => {
    const ids = new Set(wf.nodes.map((n) => n.id))
    const deployedIds = new Set((deployed?.resources ?? []).flatMap((r) => (r.nodeId ? [r.nodeId] : [])))
    return (wf.edges ?? [])
      .filter((e) => ids.has(e.source) && ids.has(e.target))
      .map(
        (e): RefFlowEdge => ({
          id: e.id,
          source: e.source,
          target: e.target,
          type: "ref",
          markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
          data: {
            kind: e.kind,
            attribute: e.attribute,
            sourceAttr: e.sourceAttr,
            hovered: e.id === hoveredEdge,
            live: deployedIds.has(e.source) && deployedIds.has(e.target),
          },
        }),
      )
  }, [wf.nodes, wf.edges, deployed, hoveredEdge])

  const [nodes, setNodes] = useSyncedState(derivedNodes)
  const [edges, setEdges] = useSyncedState(derivedEdges)

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
  const layoutKey = wf.nodes
    .filter((n) => !n.positioned)
    .map((n) => n.id)
    .join(",")
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
          const n = wf.nodes.find((x) => x.id === id)
          if (!n) return
          positions.set(id, { x: n.position.x + dx, y: n.position.y + dy })
          for (const c of children.get(id) ?? []) shift(c.id)
        }
        shift(d.id)
      }
      savePositions(positions)
    },
    [flow, parents, rects, children, wf.nodes, savePositions],
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

  const minimapColor = useCallback(
    (n: ResourceFlowNode) => {
      const cloud = n.data.schema?.cloud ?? cloudOfType(n.data.node.type)
      return cloud ? CLOUD_META[cloud].accent : "#a855f7"
    },
    [],
  )

  return (
    <div ref={wrapper} className="relative h-full w-full" onDragOver={onDragOver} onDrop={onDrop}>
      <ReactFlow<ResourceFlowNode, RefFlowEdge>
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={(_, n) => select(n.id)}
        onNodeDoubleClick={(_, n) => n.data.node.kind === "module" && void openModuleView(n.id)}
        onPaneClick={() => select(null)}
        onNodeDragStop={onNodeDragStop}
        onConnect={(c) => void onConnect(c)}
        isValidConnection={(c) => c.source !== c.target}
        onEdgeMouseEnter={(_, e) => setHoveredEdge(e.id)}
        onEdgeMouseLeave={() => setHoveredEdge(null)}
        deleteKeyCode={null}
        colorMode={resolvedTheme === "light" ? "light" : "dark"}
        snapToGrid
        snapGrid={[16, 16]}
        fitView
        fitViewOptions={{ padding: 0.2, maxZoom: 1.1 }}
        minZoom={0.15}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
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
          style={{ width: 168, height: 112 }}
          className="!rounded-lg !border"
        />
        {wf.nodes.length > 0 && (
          <Panel position="top-right">
            <Button variant="outline" size="sm" className="bg-background/80 backdrop-blur" onClick={() => void tidy()}>
              <WandSparklesIcon />
              Tidy up
            </Button>
          </Panel>
        )}
      </ReactFlow>
      {wf.nodes.length === 0 && (
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

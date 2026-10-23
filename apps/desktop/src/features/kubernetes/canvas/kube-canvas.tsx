import {
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
  type NodeChange,
} from "@xyflow/react"
import { Code2Icon, ExpandIcon, LayoutTemplateIcon, MousePointerClickIcon, RotateCwIcon, SplitIcon, Trash2Icon, WandSparklesIcon } from "lucide-react"
import { useTheme } from "next-themes"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
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
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { layoutCanvas } from "@/features/docker/canvas/layout"
import { looseItems, placeNew, relativePosition } from "@/features/docker/canvas/model"
import { useEndpoints } from "@/features/panels/endpoints/use-endpoints"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import { api } from "@/lib/api/client"
import type { KubeGraph, KubeNode, Position } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { addFromPalette, applyRecipe, deleteObject, KUBE_DRAG_TYPE, registerKubeCanvas, restartWorkload, savePositions, type KubePaletteDrag } from "../actions"
import { KubeEnvironmentSwitch } from "../environment-switch"
import { KubeConnectDialog, type PendingKubeConnection, type PendingKubeEnd } from "./connect-dialog"
import { buildKubeCanvas, WORKLOAD_H, WORKLOAD_W, type KubeCanvasItem } from "./model"
import { FrameNode, KubeEdge, SmallNode, WorkloadNode, type KubeFlowEdge, type KubeFlowNode } from "./nodes"

const nodeTypes = { workload: WorkloadNode, small: SmallNode, frame: FrameNode }
const edgeTypes = { kube: KubeEdge }
const EMPTY_GRAPH: KubeGraph = { nodes: [], edges: [] }
const EDGE_MARKER = { type: MarkerType.ArrowClosed, width: 16, height: 16 }
const FIT_VIEW_OPTIONS = { padding: 0.2, maxZoom: 1.1 }
const PRO_OPTIONS = { hideAttribution: true }
const SNAP_GRID: [number, number] = [16, 16]
/** Recipes that ask for details in the connect dialog before they run. */
const NEEDS_INPUT: Record<string, true> = { route: true, volume: true, expose: true, env: true }

function isEditableTarget(el: Element | null) {
  return !!el && (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || (el as HTMLElement).isContentEditable)
}

function minimapColor(n: KubeFlowNode) {
  if (n.type === "frame") return "rgba(59,130,246,0.15)"
  if (n.data.status?.state === "running") return "#10b981"
  return n.type === "workload" ? "#64748b" : "#94a3b8"
}

/** Rebuilt nodes keep React Flow's DOM measurement, so cards don't blink while it measures them again. */
function keepMeasured(next: KubeFlowNode[], current: KubeFlowNode[]): KubeFlowNode[] {
  const measured = new Map(current.map((n) => [n.id, n.measured]))
  return next.map((n) => (n.measured || !measured.get(n.id) ? n : { ...n, measured: measured.get(n.id) }))
}

const isWorkload = (n: KubeNode) => n.category === "workload"

/** The Build canvas of a Kubernetes workspace: objects in their namespace frames, with live pods and edges. */
export function KubeCanvas() {
  return (
    <ReactFlowProvider>
      <CanvasInner />
    </ReactFlowProvider>
  )
}

function CanvasInner() {
  const graph = useWorkflowStore((s) => s.workflow!.kubernetes?.graph) ?? EMPTY_GRAPH
  const status = useWorkflowStore((s) => s.kube)
  const diagnostics = useWorkflowStore((s) => s.workflow!.diagnostics)
  const selectedNodeId = useWorkflowStore((s) => s.selectedNodeId)
  const select = useWorkflowStore((s) => s.select)
  const mode = useWorkflowStore((s) => s.mode)
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const { resolvedTheme } = useTheme()
  const flow = useReactFlow<KubeFlowNode, KubeFlowEdge>()
  const endpoints = useEndpoints().data
  const [hovered, setHovered] = useState<string | null>(null)
  const [pending, setPending] = useState<PendingKubeConnection | null>(null)
  const [pendingDelete, setPendingDelete] = useState<{ node: KubeNode; users: number } | null>(null)
  const [menu, setMenu] = useState<KubeNode | null>(null)

  const canvas = useMemo(() => buildKubeCanvas(graph, status), [graph, status])
  const byId = useMemo(() => new Map(canvas.items.map((i) => [i.id, i])), [canvas])

  // Place what was never placed: everything at once with the layered layout, newcomers next to their siblings.
  const unplacedKey = looseItems(canvas.items)
    .filter((i) => !i.placed)
    .map((i) => i.id)
    .join(",")
  const placing = useRef("")
  useEffect(() => {
    if (!unplacedKey || placing.current === unplacedKey) return
    placing.current = unplacedKey
    const all = looseItems(canvas.items).every((i) => !i.placed)
    const run = all
      ? layoutCanvas(canvas)
      : Promise.resolve(placeNew(canvas.items.map((i) => ({ ...i })), canvas.edges, (i) => i.kind === "workload"))
    void run
      .then((positions) => savePositions(positions))
      .then(() => all && window.requestAnimationFrame(() => void flow.fitView({ padding: 0.15, duration: 300 })))
      .catch((err) => toast.error("Automatic layout failed", { description: errorMessage(err) }))
  }, [unplacedKey, canvas, flow])

  const tidy = useCallback(async () => {
    try {
      await savePositions(await layoutCanvas(canvas))
      window.requestAnimationFrame(() => void flow.fitView({ padding: 0.15, duration: 400 }))
    } catch (err) {
      toast.error("Tidy up failed", { description: errorMessage(err) })
    }
  }, [canvas, flow])

  const derivedNodes = useMemo(() => {
    const endpointOf = new Map<string, string>()
    for (const r of endpoints?.resources ?? []) if (r.nodeId && !endpointOf.has(r.nodeId)) endpointOf.set(r.nodeId, r.address)
    const diag = new Map<string, { errors: number; warnings: number }>()
    for (const d of diagnostics ?? []) {
      if (!d.nodeId) continue
      const c = diag.get(d.nodeId) ?? { errors: 0, warnings: 0 }
      if (d.severity === "error") c.errors++
      else if (d.severity === "warning") c.warnings++
      diag.set(d.nodeId, c)
    }
    const hasKids = new Set(canvas.items.flatMap((i) => (i.parent ? [i.parent] : [])))
    // Frames first: React Flow needs parents before their children.
    const ordered = [...canvas.items].sort((a, b) => Number(b.kind === "frame") - Number(a.kind === "frame"))
    return ordered.map(
      (i): KubeFlowNode => ({
        id: i.id,
        type: i.kind,
        position: relativePosition(i, byId),
        parentId: i.parent,
        width: i.w,
        height: i.h,
        zIndex: i.kind === "frame" ? -1 : undefined,
        selected: i.id === selectedNodeId,
        selectable: !!i.node,
        data: {
          kind: i.kind,
          node: i.node,
          namespace: i.namespace,
          status: i.status,
          pods: i.pods,
          hpa: i.hpa,
          facts: i.facts,
          unmatched: i.unmatched,
          empty: i.kind === "frame" && !hasKids.has(i.id),
          errors: diag.get(i.id)?.errors ?? 0,
          warnings: diag.get(i.id)?.warnings ?? 0,
          endpoint: endpointOf.get(i.id),
        },
      }),
    )
  }, [canvas, byId, selectedNodeId, endpoints, diagnostics])

  const derivedEdges = useMemo(
    () =>
      canvas.edges.map(
        (e): KubeFlowEdge => ({
          id: e.id,
          source: e.source,
          target: e.target,
          type: "kube",
          markerEnd: EDGE_MARKER,
          data: { kind: e.kind, label: e.label, live: e.live, hovered: e.id === hovered },
        }),
      ),
    [canvas, hovered],
  )

  const [nodes, setNodes] = useSyncedState(derivedNodes, keepMeasured)
  const onNodesChange = useCallback(
    (changes: NodeChange<KubeFlowNode>[]) => setNodes((ns) => applyNodeChanges(changes.filter((c) => c.type !== "remove"), ns)),
    [setNodes],
  )

  const onNodeDragStop = useCallback(
    (_: unknown, __: KubeFlowNode, dragged: KubeFlowNode[]) => {
      const positions = new Map<string, Position>()
      const draggedIds = new Set(dragged.map((d) => d.id))
      for (const d of dragged) {
        const item = byId.get(d.id)
        const abs = flow.getInternalNode(d.id)?.internals.positionAbsolute
        if (!item || !abs) continue
        if (item.parent && draggedIds.has(item.parent)) continue
        const dx = Math.round(abs.x - item.x)
        const dy = Math.round(abs.y - item.y)
        if (dx === 0 && dy === 0) continue
        // A namespace frame moves its objects (frames are drawn around them, not stored).
        const moved: KubeCanvasItem[] = item.kind === "frame" ? canvas.items.filter((c) => c.parent === item.id) : [item]
        if (item.kind === "frame" && moved.length === 0 && item.node) moved.push(item)
        for (const m of moved) positions.set(m.id, { x: m.x + dx, y: m.y + dy })
      }
      void savePositions(positions)
    },
    [byId, canvas, flow],
  )

  const describe = useCallback(
    (id: string): PendingKubeEnd => {
      const n = byId.get(id)?.node
      return { id, label: n?.name ?? id, kind: n?.kind ?? "" }
    },
    [byId],
  )

  /** Asks the daemon how `from` can refer to `to` ("" = a new object) and applies or offers the recipes. */
  const connect = useCallback(
    async (from: string, to: string, only?: string) => {
      const wf = useWorkflowStore.getState().workflow!
      let recipes
      try {
        recipes = await api.kubernetesRecipes(wf.id, from, to)
      } catch (err) {
        toast.error("Couldn't load connection options", { description: errorMessage(err) })
        return
      }
      if (only) recipes = recipes.filter((r) => r.kind === only)
      if (recipes.length === 0) {
        toast.info("Nothing to connect", { description: to ? `${describe(from).label} can't refer to ${describe(to).label} directly.` : `Nothing to add to ${describe(from).label}.` })
        return
      }
      const single = recipes.length === 1 ? recipes[0] : undefined
      if (single && !NEEDS_INPUT[single.kind] && (mode === "guided" || only)) {
        await applyRecipe(from, to, single)
        return
      }
      setPending({ from: describe(from), to: to ? describe(to) : undefined, recipes })
    },
    [describe, mode],
  )

  // Dragging from A's right handle to B: A refers to B (Service → Deployment selects it).
  const onConnect = useCallback(
    ({ source, target }: Connection) => {
      if (!source || !target || source === target) return
      void connect(source, target)
    },
    [connect],
  )

  const requestDelete = useCallback(
    (id: string) => {
      const node = byId.get(id)?.node
      if (!node) return
      const users = new Set(canvas.edges.filter((e) => e.target === id).map((e) => e.source)).size
      setPendingDelete({ node, users })
    },
    [byId, canvas],
  )

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Delete" && e.key !== "Backspace") return
      if (isEditableTarget(document.activeElement) || document.querySelector("[role=dialog]")) return
      const id = useWorkflowStore.getState().selectedNodeId
      if (!id || !byId.has(id)) return
      e.preventDefault()
      requestDelete(id)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [requestDelete, byId])

  useEffect(() => {
    registerKubeCanvas({ tidy })
    return () => registerKubeCanvas(null)
  }, [tidy])

  const onDragOver = useCallback((e: React.DragEvent) => {
    if (!e.dataTransfer.types.includes(KUBE_DRAG_TYPE)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = "copy"
  }, [])
  const onDrop = useCallback(
    (e: React.DragEvent) => {
      const raw = e.dataTransfer.getData(KUBE_DRAG_TYPE)
      if (!raw) return
      e.preventDefault()
      const p = flow.screenToFlowPosition({ x: e.clientX, y: e.clientY })
      const item = JSON.parse(raw) as KubePaletteDrag
      void addFromPalette(item.itemId, { x: Math.round(p.x - WORKLOAD_W / 2), y: Math.round(p.y - WORKLOAD_H / 2) })
    },
    [flow],
  )

  const openInCode = (n: KubeNode) => n.file && revealFiles("", n.file, n.line)
  const objectCount = graph.nodes.filter((n) => n.kind !== "Namespace").length

  return (
    <div className="relative h-full w-full" data-testid="k8s-canvas" onDragOver={onDragOver} onDrop={onDrop}>
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div className="h-full w-full">
            <ReactFlow<KubeFlowNode, KubeFlowEdge>
              nodes={nodes}
              edges={derivedEdges}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodesChange={onNodesChange}
              onNodeClick={(_, n) => select(n.data.node ? n.id : null)}
              onNodeDoubleClick={(_, n) => n.data.node && openInCode(n.data.node)}
              onNodeContextMenu={(_, n) => setMenu(n.data.node ?? null)}
              onPaneContextMenu={() => setMenu(null)}
              onPaneClick={() => select(null)}
              onNodeDragStop={onNodeDragStop}
              onConnect={onConnect}
              isValidConnection={(c) => c.source !== c.target}
              onEdgeMouseEnter={(_, e) => setHovered(e.id)}
              onEdgeMouseLeave={() => setHovered(null)}
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
              <MiniMap pannable zoomable nodeColor={minimapColor} nodeStrokeWidth={0} position="bottom-right" style={{ width: 168, height: 112 }} className="!rounded-lg !border" />
              {/* Right of the Build/Architecture switch the canvas area draws at the top left. */}
              <Panel position="top-left" style={{ marginLeft: 232 }}>
                <KubeEnvironmentSwitch className="bg-background/80 backdrop-blur" />
              </Panel>
              {canvas.items.length > 0 && (
                <Panel position="top-right">
                  <Button variant="outline" size="sm" className="bg-background/80 backdrop-blur" onClick={() => void tidy()}>
                    <WandSparklesIcon />
                    Tidy up
                  </Button>
                </Panel>
              )}
            </ReactFlow>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="min-w-52" data-testid="k8s-node-menu">
          {menu ? (
            <>
              {menu.file && (
                <ContextMenuItem onSelect={() => openInCode(menu)}>
                  <Code2Icon />
                  Open in code
                </ContextMenuItem>
              )}
              {isWorkload(menu) && (
                <>
                  <ContextMenuItem onSelect={() => void connect(menu.id, "", "expose")}>
                    <SplitIcon />
                    Expose via Service
                  </ContextMenuItem>
                  <ContextMenuItem onSelect={() => void connect(menu.id, "", "scale")}>
                    <ExpandIcon />
                    Add HPA
                  </ContextMenuItem>
                  {menu.kind !== "Job" && menu.kind !== "CronJob" && menu.kind !== "Pod" && (
                    <ContextMenuItem disabled={status?.cluster.state !== "running"} onSelect={() => void restartWorkload(menu)}>
                      <RotateCwIcon />
                      Restart
                    </ContextMenuItem>
                  )}
                </>
              )}
              <ContextMenuSeparator />
              <ContextMenuItem variant="destructive" onSelect={() => requestDelete(menu.id)}>
                <Trash2Icon />
                Delete
              </ContextMenuItem>
            </>
          ) : (
            <ContextMenuItem disabled={canvas.items.length === 0} onSelect={() => void tidy()}>
              <WandSparklesIcon />
              Tidy up
            </ContextMenuItem>
          )}
        </ContextMenuContent>
      </ContextMenu>
      {objectCount === 0 && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
          <Empty className="pointer-events-auto max-w-md border bg-background/90 backdrop-blur">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <MousePointerClickIcon />
              </EmptyMedia>
              <EmptyTitle>No objects yet</EmptyTitle>
              <EmptyDescription>
                Drag a starter, an image or a Helm chart from the Kubernetes palette on the left. Connect objects by dragging
                from the dot on the right of one card to another (a Service to the Deployment it should select).
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
      <KubeConnectDialog
        pending={pending}
        onClose={() => setPending(null)}
        onPick={(recipe, inputs) => {
          const p = pending!
          setPending(null)
          void applyRecipe(p.from.id, p.to?.id ?? "", recipe, inputs)
        }}
      />
      <AlertDialog open={!!pendingDelete} onOpenChange={(o) => !o && setPendingDelete(null)}>
        <AlertDialogContent data-testid="k8s-delete-dialog">
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete {pendingDelete?.node.kind} {pendingDelete?.node.name}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Its YAML document is removed from {pendingDelete?.node.file ?? "its file"}.
              {pendingDelete && pendingDelete.users > 0 && ` ${pendingDelete.users === 1 ? "1 other object refers" : `${pendingDelete.users} other objects refer`} to it; they keep their references.`}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              data-testid="k8s-delete-confirm"
              onClick={() => {
                const n = pendingDelete!.node
                setPendingDelete(null)
                void deleteObject(n)
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

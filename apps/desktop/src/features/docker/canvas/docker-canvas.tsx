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
import { Code2Icon, LayoutTemplateIcon, MousePointerClickIcon, Trash2Icon, WandSparklesIcon } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { techIconFor } from "@/components/tech-icons"
import { useTheme } from "next-themes"
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
import { useEndpoints } from "@/features/panels/endpoints/use-endpoints"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import { api } from "@/lib/api/client"
import type { ComposeGraph, ComposeNode, Position } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import {
  addFromPalette,
  applyRecipe,
  deleteComposeNode,
  DOCKER_DRAG_TYPE,
  registerDockerCanvas,
  savePositions,
  type DockerPaletteItem,
} from "../actions"
import { ComposeConnectDialog, type PendingComposeConnection } from "./connect-dialog"
import { layoutCanvas } from "./layout"
import { buildCanvas, looseItems, placeNew, relativePosition, SERVICE_H, SERVICE_W, type CanvasItem } from "./model"
import { DockerEdge, FrameNode, ServiceNode, SmallNode, type DockerFlowEdge, type DockerFlowNode } from "./nodes"

const nodeTypes = { service: ServiceNode, small: SmallNode, frame: FrameNode }
const edgeTypes = { docker: DockerEdge }
const EMPTY_GRAPH: ComposeGraph = { nodes: [], edges: [] }
const EDGE_MARKER = { type: MarkerType.ArrowClosed, width: 16, height: 16 }
const FIT_VIEW_OPTIONS = { padding: 0.2, maxZoom: 1.1 }
const PRO_OPTIONS = { hideAttribution: true }
const SNAP_GRID: [number, number] = [16, 16]

function isEditableTarget(el: Element | null) {
  return !!el && (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || (el as HTMLElement).isContentEditable)
}

function minimapColor(n: DockerFlowNode) {
  if (n.type === "frame") return "rgba(14,165,233,0.15)"
  if (n.data.status?.state === "running") return "#10b981"
  return n.type === "service" ? "#64748b" : "#94a3b8"
}

/** Rebuilt nodes keep React Flow's DOM measurement, so cards don't blink while it measures them again. */
function keepMeasured(next: DockerFlowNode[], current: DockerFlowNode[]): DockerFlowNode[] {
  const measured = new Map(current.map((n) => [n.id, n.measured]))
  return next.map((n) => (n.measured || !measured.get(n.id) ? n : { ...n, measured: measured.get(n.id) }))
}

/** The Build canvas of a Docker workspace: services in their network frames, volumes, secrets, edges. */
export function DockerCanvas() {
  return (
    <ReactFlowProvider>
      <CanvasInner />
    </ReactFlowProvider>
  )
}

function CanvasInner() {
  const graph = useWorkflowStore((s) => s.workflow!.compose?.graph) ?? EMPTY_GRAPH
  const status = useWorkflowStore((s) => s.compose)
  const diagnostics = useWorkflowStore((s) => s.workflow!.diagnostics)
  const selectedNodeId = useWorkflowStore((s) => s.selectedNodeId)
  const select = useWorkflowStore((s) => s.select)
  const mode = useWorkflowStore((s) => s.mode)
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const { resolvedTheme } = useTheme()
  const flow = useReactFlow<DockerFlowNode, DockerFlowEdge>()
  const wrapper = useRef<HTMLDivElement>(null)
  const endpoints = useEndpoints().data
  const [hovered, setHovered] = useState<string | null>(null)
  const [pending, setPending] = useState<PendingComposeConnection | null>(null)
  const [pendingDelete, setPendingDelete] = useState<{ node: ComposeNode; users: number } | null>(null)
  const [menu, setMenu] = useState<ComposeNode | null>(null)

  const canvas = useMemo(() => buildCanvas(graph, status), [graph, status])
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
    const run = all ? layoutCanvas(canvas) : Promise.resolve(placeNew(canvas.items.map((i) => ({ ...i })), canvas.edges))
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
    return ordered.map((i): DockerFlowNode => {
      const running = i.status?.state === "running"
      return {
        id: i.id,
        type: i.kind === "frame" ? "frame" : i.kind === "service" ? "service" : "small",
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
          stages: i.stages,
          status: i.status,
          external: i.external,
          empty: i.kind === "frame" && !hasKids.has(i.id),
          errors: diag.get(i.id)?.errors ?? 0,
          warnings: diag.get(i.id)?.warnings ?? 0,
          endpoint: running ? endpointOf.get(i.id) : undefined,
        },
      }
    })
  }, [canvas, byId, selectedNodeId, endpoints, diagnostics])

  const derivedEdges = useMemo(
    () =>
      canvas.edges.map((e): DockerFlowEdge => {
        // Membership in a further network has no direction: draw it from whichever end sits further left, so it
        // runs from a right-side handle to a left-side one instead of looping back across the canvas.
        const a = byId.get(e.source)
        const b = byId.get(e.target)
        const flip = e.kind === "network" && !!a && !!b && a.x > b.x
        return {
          id: e.id,
          source: flip ? e.target : e.source,
          target: flip ? e.source : e.target,
          type: "docker",
          markerEnd: e.kind === "network" ? undefined : EDGE_MARKER,
          data: { kind: e.kind, label: e.label, live: e.live, hovered: e.id === hovered },
        }
      }),
    [canvas, byId, hovered],
  )

  const [nodes, setNodes] = useSyncedState(derivedNodes, keepMeasured)
  const onNodesChange = useCallback(
    (changes: NodeChange<DockerFlowNode>[]) => setNodes((ns) => applyNodeChanges(changes.filter((c) => c.type !== "remove"), ns)),
    [setNodes],
  )

  const onNodeDragStop = useCallback(
    (_: unknown, __: DockerFlowNode, dragged: DockerFlowNode[]) => {
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
        // A frame moves its services (frames are drawn around them, not stored).
        const moved: CanvasItem[] = item.kind === "frame" ? canvas.items.filter((c) => c.parent === item.id) : [item]
        if (item.kind === "frame" && moved.length === 0 && item.node) moved.push(item)
        for (const m of moved) positions.set(m.id, { x: m.x + dx, y: m.y + dy })
      }
      void savePositions(positions)
    },
    [byId, canvas, flow],
  )

  const describe = useCallback(
    (id: string) => {
      const n = byId.get(id)?.node
      const svc = n?.service
      const kind = n?.kind === "external" ? n.externalKind : n?.kind
      return {
        id,
        label: n?.name ?? id,
        icon: techIconFor({ image: svc?.image?.ref, baseImages: svc?.baseImages, stack: svc?.stack, kind: kind ?? "service" }),
      }
    },
    [byId],
  )

  // Dragging from A's right handle to B: A uses B (api → db).
  const onConnect = useCallback(
    async ({ source, target }: Connection) => {
      if (!source || !target || source === target) return
      const wf = useWorkflowStore.getState().workflow!
      let recipes
      try {
        recipes = await api.composeRecipes(wf.id, source, target)
      } catch (err) {
        toast.error("Couldn't load connection options", { description: errorMessage(err) })
        return
      }
      if (recipes.length === 0) {
        toast.info("Nothing to connect", { description: `${describe(source).label} can't use ${describe(target).label} directly.` })
        return
      }
      const only = recipes.length === 1 ? recipes[0] : undefined
      if (only && mode === "guided" && (only.kind !== "volume" || only.target)) {
        await applyRecipe(source, target, only)
        return
      }
      setPending({ from: describe(source), to: describe(target), recipes })
    },
    [describe, mode],
  )

  const requestDelete = useCallback(
    (id: string) => {
      const node = byId.get(id)?.node
      if (!node || node.kind === "build-stage") return
      const users = new Set(canvas.edges.filter((e) => e.target === id).map((e) => e.source)).size
      const members = canvas.items.filter((i) => i.parent === id).length
      if (users + members > 0) setPendingDelete({ node, users: users + members })
      else void deleteComposeNode(node)
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
    registerDockerCanvas({ tidy })
    return () => registerDockerCanvas(null)
  }, [tidy])

  const onDragOver = useCallback((e: React.DragEvent) => {
    if (!e.dataTransfer.types.includes(DOCKER_DRAG_TYPE)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = "copy"
  }, [])
  const onDrop = useCallback(
    (e: React.DragEvent) => {
      const raw = e.dataTransfer.getData(DOCKER_DRAG_TYPE)
      if (!raw) return
      e.preventDefault()
      const p = flow.screenToFlowPosition({ x: e.clientX, y: e.clientY })
      void addFromPalette(JSON.parse(raw) as DockerPaletteItem, { x: Math.round(p.x - SERVICE_W / 2), y: Math.round(p.y - SERVICE_H / 2) })
    },
    [flow],
  )

  const openInCode = (n: ComposeNode) => n.file && revealFiles("", n.file, n.line)
  const serviceCount = graph.nodes.filter((n) => n.kind === "service").length

  return (
    <div ref={wrapper} className="relative h-full w-full" data-testid="docker-canvas" onDragOver={onDragOver} onDrop={onDrop}>
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div className="h-full w-full">
            <ReactFlow<DockerFlowNode, DockerFlowEdge>
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
              onConnect={(c) => void onConnect(c)}
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
        <ContextMenuContent className="min-w-52">
          {menu ? (
            <>
              {menu.file && (
                <ContextMenuItem onSelect={() => openInCode(menu)}>
                  <Code2Icon />
                  Open in code
                </ContextMenuItem>
              )}
              {(menu.kind === "service" || menu.kind === "network" || menu.kind === "volume") && (
                <>
                  <ContextMenuSeparator />
                  <ContextMenuItem variant="destructive" onSelect={() => requestDelete(menu.id)}>
                    <Trash2Icon />
                    Delete
                  </ContextMenuItem>
                </>
              )}
            </>
          ) : (
            <ContextMenuItem disabled={canvas.items.length === 0} onSelect={() => void tidy()}>
              <WandSparklesIcon />
              Tidy up
            </ContextMenuItem>
          )}
        </ContextMenuContent>
      </ContextMenu>
      {serviceCount === 0 && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
          <Empty className="pointer-events-auto max-w-md border bg-background/90 backdrop-blur">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <MousePointerClickIcon />
              </EmptyMedia>
              <EmptyTitle>No services yet</EmptyTitle>
              <EmptyDescription>
                Drag an image from the Docker palette on the left, or build your own from a Dockerfile. Connect services by
                dragging from the dot on the right of one card to another.
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
      <ComposeConnectDialog
        pending={pending}
        onClose={() => setPending(null)}
        onPick={(recipe, target) => {
          const p = pending!
          setPending(null)
          void applyRecipe(p.from.id, p.to.id, recipe, target)
        }}
      />
      <AlertDialog open={!!pendingDelete} onOpenChange={(o) => !o && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {pendingDelete?.node.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              {pendingDelete?.users === 1 ? "1 other item uses" : `${pendingDelete?.users} other items use`} it. Their
              references are removed from the compose file too.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                const n = pendingDelete!.node
                setPendingDelete(null)
                void deleteComposeNode(n)
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

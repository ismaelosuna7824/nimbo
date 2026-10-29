import {
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  MiniMap,
  Panel,
  ReactFlowProvider,
  useReactFlow,
  type Connection,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { Code2Icon, FlameIcon, LayoutTemplateIcon, MousePointerClickIcon, Trash2Icon, WandSparklesIcon } from "lucide-react"
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { MINIMAP_CARD, MINIMAP_FRAME, minimapTint } from "@/components/canvas-card"
import { CanvasContextMenu, CanvasFlow } from "@/components/canvas-flow"
import { errorMessage } from "@/components/errors"
import { sameFlowEdge, sameFlowNode, useStableItems } from "@/components/flow-items"
import { useComplianceMarked } from "@/features/compliance/highlight"
import { techIconFor } from "@/components/tech-icons"
import { useTheme } from "next-themes"
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
import { ContextMenuContent, ContextMenuItem, ContextMenuSeparator } from "@/components/ui/context-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { EnvironmentSwitch } from "@/features/environments/environment-switch"
import { FailureTestDialog } from "@/features/live/failure-dialog"
import { openFailureTest } from "@/features/live/live-store"
import { LiveTrafficOverlay } from "@/features/live/live-overlay"
import { useEndpoints } from "@/features/panels/endpoints/use-endpoints"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import { api } from "@/lib/api/client"
import type { ComposeConnectRecipe, ComposeGraph, ComposeNode, Position } from "@/lib/api/types"
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
import { useWalkthroughEdges, useWalkthroughNodes } from "@/features/walkthrough/highlight"
import { GuidedTourButton, WalkthroughOverlay } from "@/features/walkthrough/overlay"
import { useWalkthroughOpen } from "@/features/walkthrough/store"

const nodeTypes = { service: ServiceNode, small: SmallNode, frame: FrameNode }
/** Frames that are only layout; a frame drawn for a compose object (a network) can be marked. */
const isFrame = (n: DockerFlowNode) => n.type === "frame" && !n.data.node
/** The walkthrough fades cards off the path but leaves the network frames around them as they are. */
const isAnyFrame = (n: DockerFlowNode) => n.type === "frame"
/** A hop's edges during the walkthrough: the hover look, label included. */
const emphasizeEdge = (e: DockerFlowEdge): DockerFlowEdge => ({ ...e, data: { ...e.data!, hovered: true } })
const edgeTypes = { docker: DockerEdge }
const EMPTY_GRAPH: ComposeGraph = { nodes: [], edges: [] }
const EDGE_MARKER = { type: MarkerType.ArrowClosed, width: 16, height: 16 }
const FIT_VIEW_OPTIONS = { padding: 0.2, maxZoom: 1.1 }
const PRO_OPTIONS = { hideAttribution: true }
const SNAP_GRID: [number, number] = [16, 16]
const MINIMAP_STYLE = { width: 168, height: 112 }
const ENV_PANEL_STYLE = { marginLeft: 232 }

function isEditableTarget(el: Element | null) {
  return !!el && (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || (el as HTMLElement).isContentEditable)
}

function minimapColor(n: DockerFlowNode) {
  if (n.type === "frame") return MINIMAP_FRAME
  return n.data.status?.state === "running" ? minimapTint("var(--color-emerald-500)") : MINIMAP_CARD
}

function isValidConnection(c: { source: string; target: string }) {
  return c.source !== c.target
}

const openInCode = (n: ComposeNode) => n.file && revealFiles("", n.file, n.line)

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
  // The ids being placed, so a re-render while the save is in flight doesn't place them twice. Cleared once nothing
  // is unplaced (or the save failed): the same ids can come back later (monitoring removed and added again, a service
  // deleted and re-added) and must be placed again, not skipped as "already done".
  const placing = useRef("")
  useEffect(() => {
    if (!unplacedKey) {
      placing.current = ""
      return
    }
    if (placing.current === unplacedKey) return
    placing.current = unplacedKey
    const all = looseItems(canvas.items).every((i) => !i.placed)
    const run = all ? layoutCanvas(canvas) : Promise.resolve(placeNew(canvas.items.map((i) => ({ ...i })), canvas.edges))
    void run
      .then((positions) => savePositions(positions))
      .then(() => all && window.requestAnimationFrame(() => void flow.fitView({ padding: 0.15, duration: 300 })))
      .catch((err) => {
        placing.current = ""
        toast.error("Automatic layout failed", { description: errorMessage(err) })
      })
  }, [unplacedKey, canvas, flow])

  // Lays out the workflow as it is when asked (read then, so the callback stays the same across edits).
  const tidy = useCallback(async () => {
    const s = useWorkflowStore.getState()
    try {
      await savePositions(await layoutCanvas(buildCanvas(s.workflow?.compose?.graph ?? EMPTY_GRAPH, s.compose)))
      window.requestAnimationFrame(() => void flow.fitView({ padding: 0.15, duration: 400 }))
    } catch (err) {
      toast.error("Tidy up failed", { description: errorMessage(err) })
    }
  }, [flow])

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
  // The Compliance tab's "Show on canvas" rings the services of a control and dims the rest; "Follow a request" rings the current hop.
  const nodes = useWalkthroughNodes(useComplianceMarked(useStableItems(derivedNodes, sameFlowNode), isFrame), isAnyFrame)

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
  const edges = useWalkthroughEdges(useStableItems(derivedEdges, sameFlowEdge), emphasizeEdge)

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

  const nameOf = useCallback((id: string) => byId.get(id)?.node?.name ?? id, [byId])

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

  const onNodeClick = useCallback((_: unknown, n: DockerFlowNode) => select(n.data.node ? n.id : null), [select])
  const onNodeDoubleClick = useCallback((_: unknown, n: DockerFlowNode) => n.data.node && openInCode(n.data.node), [])
  const onNodeContextMenu = useCallback((_: unknown, n: DockerFlowNode) => setMenu(n.data.node ?? null), [])
  const onPaneContextMenu = useCallback(() => setMenu(null), [])
  const onPaneClick = useCallback(() => select(null), [select])
  const onConnectEdge = useCallback((c: Connection) => void onConnect(c), [onConnect])
  const onEdgeMouseEnter = useCallback((_: unknown, e: DockerFlowEdge) => setHovered(e.id), [])
  const onEdgeMouseLeave = useCallback(() => setHovered(null), [])
  const closeConnect = useCallback(() => setPending(null), [])
  const pickRecipe = useCallback(
    (recipe: ComposeConnectRecipe, target?: string) => {
      const p = pending!
      setPending(null)
      void applyRecipe(p.from.id, p.to.id, recipe, target)
    },
    [pending],
  )
  const cancelDelete = useCallback(() => setPendingDelete(null), [])
  const confirmDelete = useCallback(() => {
    const n = pendingDelete!.node
    setPendingDelete(null)
    void deleteComposeNode(n)
  }, [pendingDelete])

  const serviceCount = graph.nodes.filter((n) => n.kind === "service").length
  // Memoized parts of the canvas: it re-renders on every edit (the cards' data), these only when what they show does.
  const hasItems = canvas.items.length > 0
  // The walkthrough's caption takes the bottom of the canvas: the minimap steps aside meanwhile.
  const walking = useWalkthroughOpen()
  const overlays = useMemo(
    () => (
      <>
        <Background variant={BackgroundVariant.Dots} gap={16} size={1.2} />
        <Controls showInteractive={false} position="bottom-left" />
        {!walking && <MiniMap pannable zoomable nodeColor={minimapColor} nodeStrokeWidth={0} position="bottom-right" style={MINIMAP_STYLE} />}
        {/* Right of the Build/Architecture switch the canvas area draws at the top left. */}
        <Panel position="top-left" style={ENV_PANEL_STYLE}>
          <EnvironmentSwitch className="bg-card/80 shadow-md backdrop-blur-md dark:bg-card/80" />
        </Panel>
        {hasItems && (
          <Panel position="top-right" className="flex items-center gap-2">
            <GuidedTourButton />
            <Button variant="outline" size="sm" className="bg-card/80 shadow-md backdrop-blur-md dark:bg-card/80" onClick={() => void tidy()}>
              <WandSparklesIcon />
              Tidy up
            </Button>
          </Panel>
        )}
        <LiveTrafficOverlay kind="docker" edges={canvas.edges} />
        <WalkthroughOverlay />
      </>
    ),
    [hasItems, tidy, canvas.edges, walking],
  )
  const menuContent = useMemo(
    () => (
      <ContextMenuContent className="min-w-52">
        {menu ? (
          <>
            {menu.file && (
              <ContextMenuItem onSelect={() => openInCode(menu)}>
                <Code2Icon />
                Open in code
              </ContextMenuItem>
            )}
            {menu.kind === "service" && (
              <ContextMenuItem data-testid="docker-menu-failure" onSelect={() => openFailureTest(menu.id)}>
                <FlameIcon />
                Test a failure…
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
          <ContextMenuItem disabled={!hasItems} onSelect={() => void tidy()}>
            <WandSparklesIcon />
            Tidy up
          </ContextMenuItem>
        )}
      </ContextMenuContent>
    ),
    [menu, hasItems, tidy, requestDelete],
  )

  return (
    <div ref={wrapper} className="relative h-full w-full" data-testid="docker-canvas" onDragOver={onDragOver} onDrop={onDrop}>
      <CanvasContextMenu menu={menuContent}>
        <CanvasFlow<DockerFlowNode, DockerFlowEdge>
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodeClick={onNodeClick}
          onNodeDoubleClick={onNodeDoubleClick}
          onNodeContextMenu={onNodeContextMenu}
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
          {overlays}
        </CanvasFlow>
      </CanvasContextMenu>
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
      <ComposeConnectDialog pending={pending} onClose={closeConnect} onPick={pickRecipe} />
      <DeleteDialog pending={pendingDelete} onCancel={cancelDelete} onConfirm={confirmDelete} />
      <FailureTestDialog kind="docker" edges={canvas.edges} nameOf={nameOf} />
    </div>
  )
}

/** Asks before deleting what other items use. Memoized: the canvas re-renders on every edit, the closed dialog doesn't. */
const DeleteDialog = memo(function DeleteDialog({
  pending,
  onCancel,
  onConfirm,
}: {
  pending: { node: ComposeNode; users: number } | null
  onCancel: () => void
  onConfirm: () => void
}) {
  return (
    <AlertDialog open={!!pending} onOpenChange={(o) => !o && onCancel()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {pending?.node.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            {pending?.users === 1 ? "1 other item uses" : `${pending?.users} other items use`} it. Their
            references are removed from the compose file too.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={onConfirm}>
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
})

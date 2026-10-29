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
import { ChevronsDownUpIcon, ChevronsUpDownIcon, Code2Icon, ExpandIcon, FileSlidersIcon, FlameIcon, GlobeIcon, LayoutTemplateIcon, MousePointerClickIcon, RotateCwIcon, ShieldPlusIcon, SplitIcon, Trash2Icon, WandSparklesIcon } from "lucide-react"
import { useTheme } from "next-themes"
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { MINIMAP_CARD, MINIMAP_FRAME, minimapTint } from "@/components/canvas-card"
import { CanvasContextMenu, CanvasFlow } from "@/components/canvas-flow"
import { errorMessage } from "@/components/errors"
import { sameFlowEdge, sameFlowNode, useStableItems } from "@/components/flow-items"
import { useComplianceMarked } from "@/features/compliance/highlight"
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
import { looseItems, placeNew, relativePosition } from "@/features/docker/canvas/model"
import { useEndpoints } from "@/features/panels/endpoints/use-endpoints"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import { api } from "@/lib/api/client"
import type { KubeConnectRecipe, KubeGraph, KubeNode, Position } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import {
  addFromPalette,
  applyRecipe,
  collapseChart,
  deleteObject,
  giveOwnRole,
  KUBE_DRAG_TYPE,
  registerKubeCanvas,
  restartWorkload,
  savePositions,
  type KubePaletteDrag,
  type KubeRecipeInputs,
} from "../actions"
import { EnvironmentSwitch } from "@/features/environments/environment-switch"
import { FailureTestDialog } from "@/features/live/failure-dialog"
import { openFailureTest } from "@/features/live/live-store"
import { LiveTrafficOverlay } from "@/features/live/live-overlay"
import { expandedCharts, setChartExpanded, useExpandedCharts } from "./charts"
import { KubeConnectDialog, type PendingKubeConnection, type PendingKubeEnd } from "./connect-dialog"
import { buildKubeCanvas, layoutChart, layoutKubeCanvas, withChartSpots, WORKLOAD_H, WORKLOAD_W, type ChartSummary, type KubeCanvasItem } from "./model"
import { ChartNode, FrameNode, KubeEdge, SmallNode, WorkloadNode, type KubeFlowEdge, type KubeFlowNode } from "./nodes"
import { useWalkthroughEdges, useWalkthroughNodes } from "@/features/walkthrough/highlight"
import { GuidedTourButton, WalkthroughOverlay } from "@/features/walkthrough/overlay"
import { useWalkthroughOpen } from "@/features/walkthrough/store"

const nodeTypes = { workload: WorkloadNode, small: SmallNode, frame: FrameNode, chart: ChartNode }
const edgeTypes = { kube: KubeEdge }
/** Frames that are only layout (implicit namespaces, expanded charts); a Namespace object's frame can be marked. */
const isFrame = (n: KubeFlowNode) => n.type === "frame" && !n.data.node
/** The walkthrough fades cards off the path but leaves the namespace and chart frames around them as they are. */
const isAnyFrame = (n: KubeFlowNode) => n.type === "frame"
/** A hop's edges during the walkthrough: the hover look, label included. */
const emphasizeEdge = (e: KubeFlowEdge): KubeFlowEdge => ({ ...e, data: { ...e.data!, hovered: true } })
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
  if (n.type === "frame") return MINIMAP_FRAME
  if (n.type === "chart") return minimapTint("var(--color-violet-500)")
  return n.data.status?.state === "running" ? minimapTint("var(--color-emerald-500)") : MINIMAP_CARD
}

const MINIMAP_STYLE = { width: 168, height: 112 }
const ENV_PANEL_STYLE = { marginLeft: 232 }

const openInCode = (n: KubeNode) => n.file && revealFiles("", n.file, n.line)

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
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
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
  const expanded = useExpandedCharts(workflowId)
  const [hovered, setHovered] = useState<string | null>(null)
  const [pending, setPending] = useState<PendingKubeConnection | null>(null)
  const [pendingDelete, setPendingDelete] = useState<{ node: KubeNode; users: number } | null>(null)
  const [menu, setMenu] = useState<KubeNode | null>(null)
  const [chartMenu, setChartMenu] = useState<ChartSummary | null>(null)

  const canvas = useMemo(() => buildKubeCanvas(graph, status, expanded), [graph, status, expanded])
  const byId = useMemo(() => new Map(canvas.items.map((i) => [i.id, i])), [canvas])

  // Place what was never placed: everything at once with the layered layout; the objects of an expanded chart none
  // of which has a position as a chart; other newcomers next to their siblings.
  const unplacedKey = looseItems(canvas.items)
    .filter((i) => !i.placed)
    .map((i) => i.id)
    .join(",")
  // The ids being placed, so a re-render while the save is in flight doesn't place them twice. Cleared once nothing
  // is unplaced (or the save failed): the same ids can come back later (an object deleted and re-added, a chart
  // collapsed and expanded again) and must be placed again, not skipped as "already done".
  const placing = useRef("")
  useEffect(() => {
    if (!unplacedKey) {
      placing.current = ""
      return
    }
    if (placing.current === unplacedKey) return
    placing.current = unplacedKey
    const all = looseItems(canvas.items).every((i) => !i.placed)
    const fresh = canvas.items.find((f) => f.chart?.expanded && f.kind === "frame" && canvas.items.every((i) => i.parent !== f.id || !i.placed))
    const run = all
      ? layoutKubeCanvas(graph, status, expanded)
      : fresh
        ? layoutChart(canvas, fresh.id, fresh.chart!.group.position)
        : Promise.resolve(placeNew(canvas.items.map((i) => ({ ...i })), canvas.edges, (i) => i.kind === "workload"))
    void run
      .then((positions) => savePositions(positions))
      .then(() => all && window.requestAnimationFrame(() => void flow.fitView({ padding: 0.15, duration: 300 })))
      .catch((err) => {
        placing.current = ""
        toast.error("Automatic layout failed", { description: errorMessage(err) })
      })
  }, [unplacedKey, canvas, flow, graph, status, expanded])

  // Lays out the workflow as it is when asked (read then, so the callback stays the same across edits).
  const tidy = useCallback(async () => {
    const s = useWorkflowStore.getState()
    try {
      await savePositions(await layoutKubeCanvas(s.workflow?.kubernetes?.graph ?? EMPTY_GRAPH, s.kube, expandedCharts(workflowId)))
      window.requestAnimationFrame(() => void flow.fitView({ padding: 0.15, duration: 400 }))
    } catch (err) {
      toast.error("Tidy up failed", { description: errorMessage(err) })
    }
  }, [workflowId, flow])

  // Focus after an expansion: the chart's frame, or the object asked for once its card is drawn.
  const focusing = useRef<string | null>(null)
  const focus = useCallback(
    (id: string) => {
      window.requestAnimationFrame(() => void flow.fitView({ nodes: [{ id }], padding: 0.3, maxZoom: 1, duration: 400 }))
    },
    [flow],
  )
  /** Expands a chart: its objects laid out (tidy, at the card's spot when free) before they are drawn. */
  const expanding = useRef(new Set<string>())
  const expand = useCallback(
    async (chartId: string, target?: string) => {
      const wf = useWorkflowStore.getState().workflow
      const g = wf?.kubernetes?.graph
      const group = g?.charts?.find((c) => c.id === chartId)
      if (!wf || !g || !group || expanding.current.has(chartId)) return
      const open = expandedCharts(wf.id)
      if (open.includes(chartId)) {
        focus(target ?? chartId)
        return
      }
      expanding.current.add(chartId)
      try {
        await savePositions(await layoutChart(buildKubeCanvas(g, useWorkflowStore.getState().kube, [...open, chartId]), chartId, group.position))
      } catch (err) {
        toast.error("Couldn't lay out the chart", { description: errorMessage(err) })
      } finally {
        expanding.current.delete(chartId)
      }
      focusing.current = target ?? chartId
      setChartExpanded(wf.id, chartId, true)
    },
    [focus],
  )
  const reveal = useCallback(
    async (id: string) => {
      const chart = useWorkflowStore.getState().workflow?.kubernetes?.graph.nodes.find((n) => n.id === id)?.chart
      if (chart) await expand(chart, id)
      else focus(id)
    },
    [expand, focus],
  )

  // Selecting an object of a collapsed chart (Problems, search, the chart's inspector) expands the chart.
  const revealed = useRef<string | null>(null)
  useEffect(() => {
    if (!selectedNodeId || revealed.current === selectedNodeId) return
    revealed.current = selectedNodeId
    const chart = graph.nodes.find((n) => n.id === selectedNodeId)?.chart
    if (chart && !expanded.includes(chart) && graph.charts?.some((c) => c.id === chart)) void expand(chart, selectedNodeId)
  }, [selectedNodeId, graph, expanded, expand])

  const derivedNodes = useMemo(() => {
    const endpointOf = new Map<string, string>()
    for (const r of endpoints?.resources ?? []) if (r.nodeId && !endpointOf.has(r.nodeId)) endpointOf.set(r.nodeId, r.address)
    const diag = new Map<string, { errors: number; warnings: number }>()
    const count = (id: string, severity: string) => {
      const c = diag.get(id) ?? { errors: 0, warnings: 0 }
      if (severity === "error") c.errors++
      else if (severity === "warning") c.warnings++
      diag.set(id, c)
    }
    const chartOf = new Map(graph.nodes.flatMap((n) => (n.chart ? [[n.id, n.chart] as const] : [])))
    for (const d of diagnostics ?? []) {
      if (!d.nodeId) continue
      count(d.nodeId, d.severity)
      // A chart's card and frame count its objects' problems.
      const chart = chartOf.get(d.nodeId)
      if (chart) count(chart, d.severity)
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
        selectable: !!i.node || !!i.chart,
        data: {
          kind: i.kind,
          node: i.node,
          namespace: i.namespace,
          chart: i.chart,
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
  }, [canvas, byId, selectedNodeId, endpoints, diagnostics, graph])
  // The Compliance tab's "Show on canvas" rings the objects of a control (a collapsed chart's card stands for its
  // objects) and dims the rest; "Follow a request" rings the current hop.
  const chartOf = useMemo(() => new Map(graph.nodes.flatMap((n) => (n.chart ? [[n.id, n.chart] as const] : []))), [graph])
  const nodes = useWalkthroughNodes(useComplianceMarked(useStableItems(derivedNodes, sameFlowNode), isFrame, chartOf), isAnyFrame)

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
  const edges = useWalkthroughEdges(useStableItems(derivedEdges, sameFlowEdge), emphasizeEdge)
  useEffect(() => {
    const id = focusing.current
    if (!id || !nodes.some((n) => n.id === id)) return
    focusing.current = null
    focus(id)
  }, [nodes, focus])

  const nameOf = useCallback((id: string) => byId.get(id)?.node?.name ?? id, [byId])
  const describe = useCallback(
    (id: string): PendingKubeEnd => {
      const n = byId.get(id)?.node
      return { id, label: n?.name ?? id, kind: n?.kind ?? "" }
    },
    [byId],
  )

  /** The object card under the pointer when an identity (workload, ServiceAccount) card is dropped on it. */
  const grantTarget = useCallback(
    (event: MouseEvent | TouchEvent, dragged: KubeFlowNode[]) => {
      const node = dragged.length === 1 ? byId.get(dragged[0].id)?.node : undefined
      if (!node || !(isWorkload(node) || node.kind === "ServiceAccount")) return undefined
      const point = "changedTouches" in event ? event.changedTouches[0] : event
      if (!point) return undefined
      const p = flow.screenToFlowPosition({ x: point.clientX, y: point.clientY })
      const hit = flow
        .getIntersectingNodes({ x: p.x, y: p.y, width: 1, height: 1 })
        .find((n) => n.id !== node.id && n.type !== "frame" && !!n.data.node && !n.data.chart)
      return hit?.id
    },
    [byId, flow],
  )

  const onNodeDragStop = useCallback(
    (event: MouseEvent | TouchEvent, _: KubeFlowNode, dragged: KubeFlowNode[]) => {
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
        // A frame (namespace, expanded chart) moves its objects (frames are drawn around them, not stored).
        const moved: KubeCanvasItem[] = item.kind === "frame" ? canvas.items.filter((c) => c.parent === item.id) : [item]
        if (item.kind === "frame" && moved.length === 0 && item.node) moved.push(item)
        for (const m of moved) positions.set(m.id, { x: m.x + dx, y: m.y + dy })
      }
      const target = grantTarget(event, dragged)
      if (!target) {
        void savePositions(withChartSpots(canvas, positions))
        return
      }
      // Dropped onto a Role or an object: offer the grants ("What can it do?") and put the card back.
      const from = dragged[0].id
      void (async () => {
        const wf = useWorkflowStore.getState().workflow!
        const grants = (await api.kubernetesRecipes(wf.id, from, target).catch(() => [])).filter((r) => r.kind === "access")
        if (grants.length === 0) {
          await savePositions(withChartSpots(canvas, positions))
          return
        }
        flow.setNodes((ns) => ns.map((n) => (n.id === from ? (nodes.find((d) => d.id === from) ?? n) : n)))
        setPending({ from: describe(from), to: describe(target), recipes: grants })
      })()
    },
    [byId, canvas, flow, grantTarget, describe, nodes],
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
    registerKubeCanvas({ tidy, expandChart: expand, reveal })
    return () => registerKubeCanvas(null)
  }, [tidy, expand, reveal])

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

  const onNodeClick = useCallback((_: unknown, n: KubeFlowNode) => select(n.data.node || n.data.chart ? n.id : null), [select])
  const onNodeDoubleClick = useCallback(
    (_: unknown, n: KubeFlowNode) => {
      if (n.data.node) openInCode(n.data.node)
      else if (n.type === "chart") void expand(n.id)
    },
    [expand],
  )
  const onNodeContextMenu = useCallback((_: unknown, n: KubeFlowNode) => {
    setMenu(n.data.node ?? null)
    setChartMenu(n.data.chart ?? null)
  }, [])
  const onPaneContextMenu = useCallback(() => {
    setMenu(null)
    setChartMenu(null)
  }, [])
  const onPaneClick = useCallback(() => select(null), [select])
  // A chart's card or frame stands for many objects: connect its objects instead.
  const isValidConnection = useCallback(
    (c: { source: string; target: string }) => c.source !== c.target && !byId.get(c.source)?.chart && !byId.get(c.target)?.chart,
    [byId],
  )
  const onEdgeMouseEnter = useCallback((_: unknown, e: KubeFlowEdge) => setHovered(e.id), [])
  const onEdgeMouseLeave = useCallback(() => setHovered(null), [])
  const closeConnect = useCallback(() => setPending(null), [])
  const pickRecipe = useCallback(
    (recipe: KubeConnectRecipe, inputs: KubeRecipeInputs) => {
      const p = pending!
      setPending(null)
      void applyRecipe(p.from.id, p.to?.id ?? "", recipe, inputs)
    },
    [pending],
  )
  const cancelDelete = useCallback(() => setPendingDelete(null), [])
  const confirmDelete = useCallback(() => {
    const n = pendingDelete!.node
    setPendingDelete(null)
    void deleteObject(n)
  }, [pendingDelete])

  const objectCount = graph.nodes.filter((n) => n.kind !== "Namespace").length
  // Memoized parts of the canvas: it re-renders on every edit (the cards' data), these only when what they show does.
  const hasItems = canvas.items.length > 0
  const clusterRunning = status?.cluster.state === "running"
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
        <LiveTrafficOverlay kind="kubernetes" edges={canvas.edges} />
        <WalkthroughOverlay surface="build" />
      </>
    ),
    [hasItems, tidy, canvas.edges, walking],
  )
  const menuContent = useMemo(
    () => (
      <ContextMenuContent className="min-w-52" data-testid="k8s-node-menu">
        {chartMenu ? (
          <>
            {chartMenu.expanded ? (
              <ContextMenuItem onSelect={() => collapseChart(chartMenu.group.id)}>
                <ChevronsDownUpIcon />
                Collapse chart
              </ContextMenuItem>
            ) : (
              <ContextMenuItem onSelect={() => void expand(chartMenu.group.id)}>
                <ChevronsUpDownIcon />
                Expand {chartMenu.members.length} objects
              </ContextMenuItem>
            )}
            {chartMenu.group.valuesFiles?.[0] && (
              <ContextMenuItem onSelect={() => revealFiles("", chartMenu.group.valuesFiles![0])}>
                <FileSlidersIcon />
                Open values
              </ContextMenuItem>
            )}
          </>
        ) : menu ? (
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
                <ContextMenuItem data-testid="k8s-menu-allow-all" onSelect={() => void connect(menu.id, "", "allowAll")}>
                  <GlobeIcon />
                  Accept traffic from anywhere
                </ContextMenuItem>
                {menu.kind !== "Job" && menu.kind !== "CronJob" && menu.kind !== "Pod" && (
                  <ContextMenuItem disabled={!clusterRunning} onSelect={() => void restartWorkload(menu)}>
                    <RotateCwIcon />
                    Restart
                  </ContextMenuItem>
                )}
              </>
            )}
            {(menu.kind === "Deployment" || menu.kind === "StatefulSet") && (
              <ContextMenuItem data-testid="k8s-menu-failure" onSelect={() => openFailureTest(menu.id)}>
                <FlameIcon />
                Test a failure…
              </ContextMenuItem>
            )}
            {(isWorkload(menu) || (menu.kind === "ServiceAccount" && menu.apiVersion === "v1")) && (
              <ContextMenuItem data-testid="k8s-menu-own-role" onSelect={() => void giveOwnRole(menu)}>
                <ShieldPlusIcon />
                Give it its own Role
              </ContextMenuItem>
            )}
            <ContextMenuSeparator />
            <ContextMenuItem variant="destructive" onSelect={() => requestDelete(menu.id)}>
              <Trash2Icon />
              Delete
            </ContextMenuItem>
          </>
        ) : (
          <ContextMenuItem disabled={!hasItems} onSelect={() => void tidy()}>
            <WandSparklesIcon />
            Tidy up
          </ContextMenuItem>
        )}
      </ContextMenuContent>
    ),
    [chartMenu, menu, hasItems, clusterRunning, expand, connect, requestDelete, tidy],
  )

  return (
    <div className="relative h-full w-full" data-testid="k8s-canvas" onDragOver={onDragOver} onDrop={onDrop}>
      <CanvasContextMenu menu={menuContent}>
        <CanvasFlow<KubeFlowNode, KubeFlowEdge>
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
          onConnect={onConnect}
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
      <KubeConnectDialog pending={pending} onClose={closeConnect} onPick={pickRecipe} />
      <DeleteDialog pending={pendingDelete} onCancel={cancelDelete} onConfirm={confirmDelete} />
      <FailureTestDialog kind="kubernetes" edges={canvas.edges} nameOf={nameOf} />
    </div>
  )
}

/** Asks before deleting an object. Memoized: the canvas re-renders on every edit, the closed dialog doesn't. */
const DeleteDialog = memo(function DeleteDialog({
  pending,
  onCancel,
  onConfirm,
}: {
  pending: { node: KubeNode; users: number } | null
  onCancel: () => void
  onConfirm: () => void
}) {
  return (
    <AlertDialog open={!!pending} onOpenChange={(o) => !o && onCancel()}>
      <AlertDialogContent data-testid="k8s-delete-dialog">
        <AlertDialogHeader>
          <AlertDialogTitle>
            Delete {pending?.node.kind} {pending?.node.name}?
          </AlertDialogTitle>
          <AlertDialogDescription>
            Its YAML document is removed from {pending?.node.file ?? "its file"}.
            {pending && pending.users > 0 && ` ${pending.users === 1 ? "1 other object refers" : `${pending.users} other objects refer`} to it; they keep their references.`}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" data-testid="k8s-delete-confirm" onClick={onConfirm}>
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
})

import {
  applyNodeChanges,
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  Panel,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Edge,
  type EdgeProps,
  type FitViewOptions,
  type Node,
  type NodeChange,
  type NodeProps,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { DownloadIcon } from "lucide-react"
import { memo, useCallback, useEffect, useMemo, useRef } from "react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { useSyncedState } from "@/components/use-synced-state"
import { isDocker, isKubernetes } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { ACTOR_H, BORDER_CARD_H, BORDER_CARD_W, CARD_H, CARD_W, polylinePath, type Point } from "./geometry"
import type { ItemKind } from "./layout"
import type { ArchActor, ArchGroup, FlowKind } from "./model"
import { groupIconWidth } from "./scene"
import { registerArchScene, useArchUi } from "./store"
import { legendHeight, type CardText, type LegendEntry } from "./styles"
import { ActorShape, CardShape, DiagramDefs, EdgeShape, GroupShape, HeaderShape, LegendShape } from "./svg"
import { useDockerScene, useKubernetesScene, useScene, type Scene } from "./use-scene"

type ArchNodeData =
  | { kind: "group"; group: ArchGroup; icon?: string }
  | { kind: "card"; nodeId: string; icon: string; text: CardText; existing?: boolean; onBorder?: boolean }
  | { kind: "actor"; actor: ArchActor; icon: string }
  | { kind: "header"; title: string; subtitle: string; width: number }
  | { kind: "legend"; entries: LegendEntry[] }

type ArchNode = Node<ArchNodeData & Record<string, unknown>>

interface FlowData extends Record<string, unknown> {
  kind: FlowKind
  route: Point[]
}
type FlowEdge = Edge<FlowData, "archFlow">

const hiddenHandle = "!pointer-events-none !opacity-0"
const PRO_OPTIONS = { hideAttribution: true }
/** The diagram is a document: a white surface whatever the app theme. */
const SURFACE_STYLE = { backgroundColor: "#ffffff" }
/** Fit with room at the top for the Build / Architecture switch and Export button floating over the diagram. */
const FIT_VIEW: FitViewOptions<ArchNode> = { padding: { top: "64px", x: 0.08, bottom: 0.08 }, maxZoom: 1.1 }

function Handles() {
  return (
    <>
      <Handle type="target" position={Position.Left} isConnectable={false} className={hiddenHandle} />
      <Handle type="source" position={Position.Right} isConnectable={false} className={hiddenHandle} />
    </>
  )
}

const GroupNode = memo(function GroupNode({ data, selected, width, height }: NodeProps<ArchNode>) {
  if (data.kind !== "group") return null
  const g = data.group
  return (
    <div style={{ width, height }} data-testid="arch-group" data-group-kind={g.kind} aria-label={g.label}>
      <svg width={width} height={height} className="block overflow-visible">
        <GroupShape width={width ?? 0} height={height ?? 0} kind={g.kind} tier={g.tier} scope={g.scope} label={g.label} sublabel={g.sublabel} icon={data.icon} iconWidth={groupIconWidth(g)} selected={selected} />
      </svg>
      <Handles />
    </div>
  )
})

const CardNode = memo(function CardNode({ data, selected }: NodeProps<ArchNode>) {
  if (data.kind !== "card") return null
  const width = data.onBorder ? BORDER_CARD_W : CARD_W
  const height = data.onBorder ? BORDER_CARD_H : CARD_H
  return (
    <div style={{ width, height }} data-testid="arch-card" aria-label={data.text.title}>
      <svg width={width} height={height} className="block overflow-visible">
        <CardShape icon={data.icon} text={data.text} selected={selected} existing={data.existing} onBorder={data.onBorder} />
      </svg>
      <Handles />
    </div>
  )
})

const ActorNode = memo(function ActorNode({ data, selected, width }: NodeProps<ArchNode>) {
  if (data.kind !== "actor") return null
  return (
    <div style={{ width, height: ACTOR_H }} data-testid="arch-actor" aria-label={data.actor.label}>
      <svg width={width} height={ACTOR_H} className="block overflow-visible">
        <ActorShape width={width ?? 0} icon={data.icon} label={data.actor.label} sublabel={data.actor.sublabel} selected={selected} />
      </svg>
      <Handles />
    </div>
  )
})

const HeaderNode = memo(function HeaderNode({ data }: NodeProps<ArchNode>) {
  if (data.kind !== "header") return null
  return (
    <div style={{ width: data.width, height: 60 }} data-testid="arch-header">
      <svg width={data.width} height={60} className="block overflow-visible">
        <HeaderShape title={data.title} subtitle={data.subtitle} width={data.width} />
      </svg>
    </div>
  )
})

const LegendNode = memo(function LegendNode({ data }: NodeProps<ArchNode>) {
  if (data.kind !== "legend") return null
  const h = legendHeight(data.entries)
  return (
    <div style={{ width: 236, height: h }} data-testid="arch-legend">
      <svg width={236} height={h} className="block overflow-visible">
        <LegendShape entries={data.entries} />
      </svg>
    </div>
  )
})

const FlowEdgeView = memo(function FlowEdgeView({ data }: EdgeProps<FlowEdge>) {
  if (!data) return null
  return (
    <g data-testid="arch-edge" data-kind={data.kind}>
      <EdgeShape path={polylinePath(data.route)} kind={data.kind} />
    </g>
  )
})

const nodeTypes = { archGroup: GroupNode, archCard: CardNode, archActor: ActorNode, archHeader: HeaderNode, archLegend: LegendNode }
const edgeTypes = { archFlow: FlowEdgeView }
const NODE_TYPE: Record<ItemKind, string> = { group: "archGroup", card: "archCard", actor: "archActor" }

/** Depth of a group in the container tree (for stacking). */
function depthOf(scene: Scene, id: string) {
  let d = 0
  for (let cur = scene.diagram.byId.get(id)?.parent; cur; cur = scene.diagram.byId.get(cur)?.parent) d++
  return d
}

/** React Flow nodes of a scene: groups (parents first), cards and actors, then the title block and legend. */
function sceneNodes(scene: Scene): ArchNode[] {
  const groups = new Map(scene.model.groups.map((g) => [g.id, g]))
  const actors = new Map(scene.model.actors.map((a) => [a.id, a]))
  const cardsById = new Map(scene.model.cards.map((c) => [c.id, c]))
  const out: ArchNode[] = scene.diagram.items.map((it) => {
    const base = {
      id: it.id,
      type: NODE_TYPE[it.kind],
      position: { x: it.x, y: it.y },
      parentId: it.parent,
      width: it.width,
      height: it.height,
      zIndex: it.kind === "group" ? depthOf(scene, it.id) : 1000,
    }
    if (it.kind === "group") return { ...base, data: { kind: "group", group: groups.get(it.id)!, icon: scene.groupIcons.get(it.id) } }
    if (it.kind === "card") {
      const c = scene.cards.get(it.id)!
      const card = cardsById.get(it.id)!
      return { ...base, data: { kind: "card", nodeId: card.nodeId, icon: c.icon, text: c.text, existing: card.existing, onBorder: card.onBorder } }
    }
    return { ...base, data: { kind: "actor", actor: actors.get(it.id)!, icon: scene.actorIcons.get(it.id)! } }
  })
  const { header, legendRect, title, subtitle, legend } = scene.deco
  out.push(
    {
      id: "__arch-header",
      type: "archHeader",
      position: { x: header.x, y: header.y },
      width: header.width,
      height: header.height,
      selectable: false,
      zIndex: 1000,
      data: { kind: "header", title, subtitle, width: header.width },
    },
    {
      id: "__arch-legend",
      type: "archLegend",
      position: { x: legendRect.x, y: legendRect.y },
      width: legendRect.width,
      height: legendRect.height,
      selectable: false,
      zIndex: 1000,
      data: { kind: "legend", entries: legend },
    },
  )
  return out
}

function sceneEdges(scene: Scene): FlowEdge[] {
  return scene.diagram.edges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    type: "archFlow",
    selectable: false,
    focusable: false,
    zIndex: 500,
    data: { kind: e.kind, route: e.route },
  }))
}

/** The workflow node a diagram item stands for: a card's resource (or module call, or compose service), a group's VPC/subnet/network. */
function nodeIdOf(n: ArchNode) {
  return n.data.kind === "card" ? n.data.nodeId : n.data.kind === "group" ? n.data.group.nodeId : undefined
}

/** Keeps React Flow's DOM measurements and the selection when the scene rebuilds the nodes. */
function carryNodeState(next: ArchNode[], current: ArchNode[]): ArchNode[] {
  const byId = new Map(current.map((n) => [n.id, n]))
  return next.map((n) => {
    const cur = byId.get(n.id)
    return cur ? { ...n, measured: cur.measured, selected: cur.selected } : n
  })
}

export function ArchitectureView() {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const kind = useWorkflowStore((s) => (isDocker(s.workflow) ? "docker" : isKubernetes(s.workflow) ? "kubernetes" : "terraform"))
  if (!workflowId) return null
  // Docker workspaces are drawn from their compose graph, Kubernetes ones from their rendered objects,
  // Terraform ones from their resources: one scene hook per kind, each in its own component (hooks
  // cannot be chosen conditionally).
  return (
    <ReactFlowProvider key={workflowId}>
      {kind === "docker" ? <DockerDiagram /> : kind === "kubernetes" ? <KubernetesDiagram /> : <TerraformDiagram />}
    </ReactFlowProvider>
  )
}

function TerraformDiagram() {
  return <ArchitectureInner scene={useScene()} empty="Add resources in Build to see them here." />
}

function DockerDiagram() {
  return <ArchitectureInner scene={useDockerScene()} empty="Add services in Build to see them here." />
}

function KubernetesDiagram() {
  return <ArchitectureInner scene={useKubernetesScene()} empty="Add workloads in Build to see them here." />
}

/** The read-only diagram: pan, zoom, select a card to inspect it, export it as an image. Build edits the design. */
function ArchitectureInner({ scene, empty }: { scene: Scene | null; empty: string }) {
  const select = useWorkflowStore((s) => s.select)
  const selectedNodeId = useWorkflowStore((s) => s.selectedNodeId)
  const openExport = useArchUi((s) => s.openExport)
  const flow = useReactFlow<ArchNode, FlowEdge>()

  useEffect(() => {
    registerArchScene(scene)
    return () => registerArchScene(null)
  }, [scene])

  const builtNodes = useMemo(() => (scene ? sceneNodes(scene) : []), [scene])
  const edges = useMemo(() => (scene ? sceneEdges(scene) : []), [scene])
  const [nodes, setNodes] = useSyncedState(builtNodes, carryNodeState)
  // Only measurements and selection change here: the diagram is read-only.
  const onNodesChange = useCallback(
    (changes: NodeChange<ArchNode>[]) => setNodes((ns) => applyNodeChanges(changes.filter((c) => c.type === "dimensions" || c.type === "select"), ns)),
    [setNodes],
  )

  // A node picked elsewhere (Problems, the inspector) is selected here too.
  useEffect(() => {
    if (!selectedNodeId) return
    setNodes((ns) => {
      const target = ns.find((n) => nodeIdOf(n) === selectedNodeId)
      if (!target || target.selected) return ns
      return ns.map((n) => (n.selected === (n === target) ? n : { ...n, selected: n === target }))
    })
  }, [selectedNodeId, setNodes, builtNodes])

  // Fit the whole diagram once it first appears.
  const fitted = useRef(false)
  useEffect(() => {
    if (!scene || fitted.current) return
    fitted.current = true
    window.requestAnimationFrame(() => void flow.fitView(FIT_VIEW))
  }, [scene, flow])

  const onNodeClick = useCallback((_: unknown, n: ArchNode) => select(nodeIdOf(n) ?? null), [select])
  const onPaneClick = useCallback(() => select(null), [select])

  return (
    <div className="relative h-full w-full" data-testid="architecture-view">
      <svg width={0} height={0} className="absolute" aria-hidden>
        <DiagramDefs />
      </svg>
      <ReactFlow<ArchNode, FlowEdge>
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onNodeClick={onNodeClick}
        onPaneClick={onPaneClick}
        nodesDraggable={false}
        nodesConnectable={false}
        edgesFocusable={false}
        elevateNodesOnSelect={false}
        elevateEdgesOnSelect={false}
        deleteKeyCode={null}
        selectionKeyCode={null}
        multiSelectionKeyCode={null}
        colorMode="light"
        minZoom={0.1}
        maxZoom={2.5}
        fitViewOptions={FIT_VIEW}
        proOptions={PRO_OPTIONS}
        style={SURFACE_STYLE}
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1.1} color="#d5dbe3" bgColor="#ffffff" />
        <Controls showInteractive={false} position="bottom-left" />
        <Panel position="top-right">
          <Button size="sm" onClick={() => openExport("architecture")}>
            <DownloadIcon />
            Export image…
          </Button>
        </Panel>
      </ReactFlow>
      {!scene && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center gap-2 text-sm text-slate-500">
          <Spinner />
          Drawing the architecture…
        </div>
      )}
      {scene && scene.model.cards.length === 0 && scene.model.groups.length === 0 && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center text-sm text-slate-500">
          {empty}
        </div>
      )}
    </div>
  )
}

// "Tour the infrastructure": the Build canvas toured from its roots to its leaves (tour.ts), the cards in focus
// glowing, the camera easing from step to step and a caption saying what each resource is, why it is in this
// design and what is built on it next.
import { useStore, useStoreApi, type ReactFlowState } from "@xyflow/react"
import { MapIcon, RotateCcwIcon } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef } from "react"
import { Button } from "@/components/ui/button"
import type { Rect } from "@/features/architecture/geometry"
import { schemaKey, useNodeSchemas } from "@/features/canvas/catalog"
import { useWorkflowStore } from "@/state/workflow-store"
import { useFocusCamera, useHighlight, usePlayerKeys, useReducedMotion } from "./player-hooks"
import { AlsoChip, PlayerPanel, WhereDot } from "./player-ui"
import { replayWalkthrough, useWalkthrough, type EdgeMark, type Highlight, type NodeMark } from "./store"
import { buildTour, tourCaption, type TourGraph, type TourStep } from "./tour"
import { dockerTourGraph, kubeTourGraph, notCreated, terraformTourGraph, type CanvasCard, type CanvasLink } from "./tour-graph"
import { unionRect } from "./travel"

type Kind = "terraform" | "docker" | "kubernetes"

/** Reading time of a step at 1×, plus a little per extra line. */
const DWELL_MS = 3200
const PER_LINE_MS = 700

interface CanvasRead {
  /** Cards and edges as a string: equal while nothing came, went or moved. */
  key: string
  cards: CanvasCard[]
  links: CanvasLink[]
}

/** The canvas as the tour reads it: cards (with frames and absolute positions) and edges. */
function readCanvas(s: ReactFlowState): CanvasRead {
  const cards: CanvasCard[] = [...s.nodeLookup.values()].map((n) => ({
    id: n.id,
    x: n.internals.positionAbsolute.x,
    y: n.internals.positionAbsolute.y,
    ...(n.parentId ? { parent: n.parentId } : {}),
    ...(n.type === "container" || n.type === "frame" ? { frame: true } : {}),
  }))
  const links: CanvasLink[] = s.edges.map((e) => ({ id: e.id, source: e.source, target: e.target }))
  const key = `${cards.map((c) => `${c.id}|${c.parent ?? ""}|${Math.round(c.x)}|${Math.round(c.y)}`).join(";")}#${links.map((l) => `${l.id}|${l.source}|${l.target}`).join(";")}`
  return { key, cards, links }
}

export default function TourLayer({ kind }: { kind: Kind }) {
  const wf = useWorkflowStore((s) => s.workflow!)
  const deployed = useWorkflowStore((s) => s.deployed)
  const schemas = useNodeSchemas(wf.nodes)
  // Read again when cards or edges come, go or move, not when the tour's own marks restyle them.
  const canvas = useStore(readCanvas, (a, b) => a.key === b.key)

  const graph = useMemo((): TourGraph => {
    const { cards, links } = canvas
    if (kind === "docker") return dockerTourGraph({ graph: wf.compose?.graph ?? { nodes: [], edges: [] }, links, cards })
    if (kind === "kubernetes") return kubeTourGraph({ graph: wf.kubernetes?.graph ?? { nodes: [], edges: [] }, links, cards })
    const absent = notCreated(wf)
    return terraformTourGraph({
      nodes: wf.nodes,
      links,
      cards,
      schemaOf: (n) => schemas.get(schemaKey(n.kind, n.type)),
      deployed: new Set((deployed?.resources ?? []).flatMap((r) => (r.nodeId ? [r.nodeId] : []))),
      ...(absent.size && wf.activeEnvironment ? { absent: { ids: absent, env: wf.activeEnvironment } } : {}),
    })
  }, [canvas, kind, wf, schemas, deployed])
  const steps = useMemo(() => buildTour(graph), [graph])
  return <TourPlayer graph={graph} steps={steps} />
}

function TourPlayer({ graph, steps }: { graph: TourGraph; steps: TourStep[] }) {
  const stepIndex = useWalkthrough((s) => s.step)
  const playing = useWalkthrough((s) => s.playing)
  const speed = useWalkthrough((s) => s.speed)
  const select = useWorkflowStore((s) => s.select)
  const reduced = useReducedMotion()
  const store = useStoreApi()
  const panel = useRef<HTMLDivElement>(null)
  const index = Math.min(stepIndex, steps.length)
  const atEnd = index >= steps.length
  const step = atEnd ? undefined : steps[index]
  const caption = useMemo(() => (step ? tourCaption(graph, steps, index) : null), [graph, steps, index, step])

  // Autoplay: read, next; the end stops.
  useEffect(() => {
    if (!playing || !step || !caption) return
    const last = index + 1 >= steps.length
    const ms = (DWELL_MS + PER_LINE_MS * Math.max(0, caption.lines.length - 1)) / speed
    const t = window.setTimeout(() => useWalkthrough.setState({ step: index + 1, ...(last ? { playing: false } : {}) }), ms)
    return () => window.clearTimeout(t)
  }, [playing, step, caption, index, steps.length, speed])

  // Marks: the step glows, the one before keeps a faint ring, earlier ones stay readable, the rest fades.
  const highlight = useMemo((): Highlight | null => {
    if (!steps.length) return null
    const nodes: Record<string, NodeMark> = {}
    const edges: Record<string, EdgeMark> = {}
    const shownUpTo = atEnd ? steps.length : index
    steps.slice(0, shownUpTo).forEach((s, i) => {
      const mark: NodeMark = atEnd || i === index - 1 ? "near" : "path"
      for (const id of [...s.nodes, ...s.also]) nodes[id] = mark
      for (const id of s.edges) edges[id] = "path"
    })
    if (step) {
      for (const id of step.nodes) nodes[id] = "glow"
      for (const id of step.also) nodes[id] = "near"
      for (const id of step.edges) edges[id] = "current"
    }
    return { nodes, edges }
  }, [steps, step, index, atEnd])
  useHighlight(highlight)

  const focus = (): Rect | undefined => {
    const lookup = store.getState().nodeLookup
    const rectOf = (id: string): Rect | undefined => {
      const n = lookup.get(id)
      if (!n) return undefined
      const p = n.internals.positionAbsolute
      return { x: p.x, y: p.y, width: n.measured.width ?? n.width ?? 0, height: n.measured.height ?? n.height ?? 0 }
    }
    // The step with what it is built on (its plumbing stays out of the frame: it can sit far away).
    const ids = step ? [...step.nodes, ...step.parents] : steps.flatMap((s) => s.nodes)
    return unionRect(ids.flatMap((id) => rectOf(id) ?? []))
  }
  useFocusCamera(focus, `${steps.length}|${index}`, panel, reduced, 1)

  const go = useCallback((to: number) => useWalkthrough.setState({ step: Math.max(0, Math.min(to, steps.length)) }), [steps.length])
  usePlayerKeys(go, index)

  const real = graph.nodes.filter((n) => !n.plumbing).length
  return (
    <PlayerPanel
      title="Tour the infrastructure"
      icon={<MapIcon className="size-4 shrink-0 text-primary" />}
      counter={step ? `${index + 1}/${steps.length}` : undefined}
      controls={step ? { index, go } : undefined}
      panelRef={panel}
    >
      {!steps.length ? (
        <p className="text-muted-foreground" data-testid="tour-empty">
          Nothing on the canvas yet: add resources to tour them.
        </p>
      ) : caption && step ? (
        <div className="flex flex-col gap-1.5" data-testid="tour-step">
          <div className="flex min-w-0 items-center gap-2">
            <span data-testid="walkthrough-name" className="truncate font-semibold">
              {caption.title}
            </span>
            {step.nodes.length > 1 && <span className="shrink-0 text-xs text-muted-foreground">{step.nodes.length} resources</span>}
            {caption.where && <WhereDot where={caption.where} className="ml-auto" />}
          </div>
          {caption.about && <p className="text-xs text-muted-foreground">{caption.about}</p>}
          <ul data-testid="walkthrough-caption" className="flex flex-col gap-1">
            {caption.lines.map((l) =>
              step.nodes.length === 1 ? (
                <li key={l.id} className="leading-snug">
                  {l.why}
                </li>
              ) : (
                <li key={l.id} className="leading-snug">
                  <span className="font-medium">{l.label}</span> <span className="text-muted-foreground">— {l.why}</span>
                </li>
              ),
            )}
            {caption.more > 0 && <li className="text-xs text-muted-foreground">+{caption.more} more on the canvas</li>}
          </ul>
          <p data-testid="tour-next" className="text-xs text-muted-foreground">
            {caption.next}
          </p>
          {caption.also.length > 0 && (
            <div className="flex flex-wrap items-center gap-1" data-testid="walkthrough-also">
              <span className="mr-0.5 text-xs text-muted-foreground">Also created here:</span>
              {caption.also.slice(0, 6).map((chip) => (
                <AlsoChip key={chip.nodeId ?? chip.label} chip={chip} onSelect={select} />
              ))}
              {caption.also.length > 6 && <span className="text-xs text-muted-foreground">+{caption.also.length - 6} more</span>}
            </div>
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-2" data-testid="walkthrough-summary">
          <p className="font-medium">
            You toured {real} resource{real === 1 ? "" : "s"} in {steps.length} step{steps.length === 1 ? "" : "s"}, from the foundations to what runs on them.
          </p>
          <div>
            <Button size="sm" variant="outline" onClick={replayWalkthrough}>
              <RotateCcwIcon />
              Replay
            </Button>
          </div>
        </div>
      )}
    </PlayerPanel>
  )
}

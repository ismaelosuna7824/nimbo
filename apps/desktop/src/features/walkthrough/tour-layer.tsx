// "Tour the infrastructure": the Build canvas toured from its roots to its leaves (tour.ts), the cards in focus
// glowing, the camera easing from stop to stop at a readable zoom and a compact caption saying what each resource
// is, why it is in this design and what is built on it next.
import { useStore, useStoreApi, type ReactFlowState } from "@xyflow/react"
import { MapIcon, RotateCcwIcon } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import type { Rect } from "@/features/architecture/geometry"
import { schemaKey, useNodeSchemas } from "@/features/canvas/catalog"
import { useWorkflowStore } from "@/state/workflow-store"
import { useFocusCamera, useHighlight, usePlayerKeys, useReducedMotion, type CameraFocus } from "./player-hooks"
import { OneLine, PlayerPanel, WhereDot } from "./player-ui"
import { replayWalkthrough, useWalkthrough, type EdgeMark, type Highlight, type NodeMark } from "./store"
import { buildTour, tourCaption, tourStops, type TourGraph, type TourStop } from "./tour"
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
  const stops = useMemo(() => tourStops(buildTour(graph)), [graph])
  return <TourPlayer graph={graph} stops={stops} />
}

/** Lines a group's overview lists before "+N more". */
const LIST_LINES = 4

function TourPlayer({ graph, stops }: { graph: TourGraph; stops: TourStop[] }) {
  const stepIndex = useWalkthrough((s) => s.step)
  const playing = useWalkthrough((s) => s.playing)
  const speed = useWalkthrough((s) => s.speed)
  const reduced = useReducedMotion()
  const store = useStoreApi()
  const panel = useRef<HTMLDivElement>(null)
  // The overview list of the stop it was unfolded on ("+N more"); a new stop starts folded.
  const [expandedAt, setExpandedAt] = useState(-1)
  const index = Math.min(stepIndex, stops.length)
  const atEnd = index >= stops.length
  const stop = atEnd ? undefined : stops[index]
  const caption = useMemo(() => (stop ? tourCaption(graph, stops, index) : null), [graph, stops, index, stop])
  const steps = stops.length ? stops[stops.length - 1].step : 0

  const expanded = expandedAt === index

  // Autoplay: read, next; the end stops.
  useEffect(() => {
    if (!playing || !stop || !caption) return
    const last = index + 1 >= stops.length
    const ms = (DWELL_MS + PER_LINE_MS * Math.max(0, Math.min(LIST_LINES, caption.lines.length) - 1)) / speed
    const t = window.setTimeout(() => useWalkthrough.setState({ step: index + 1, ...(last ? { playing: false } : {}) }), ms)
    return () => window.clearTimeout(t)
  }, [playing, stop, caption, index, stops.length, speed])

  // Marks: the focus glows; its group and the stop before keep a faint ring; earlier stops stay readable; the rest fades.
  const highlight = useMemo((): Highlight | null => {
    if (!stops.length) return null
    const nodes: Record<string, NodeMark> = {}
    const edges: Record<string, EdgeMark> = {}
    stops.slice(0, atEnd ? stops.length : index).forEach((s, i) => {
      const mark: NodeMark = atEnd || i === index - 1 ? "near" : "path"
      for (const id of [...s.nodes, ...s.also]) nodes[id] = mark
      for (const id of s.edges) edges[id] = "path"
    })
    if (stop) {
      for (const id of stop.group) nodes[id] = "near"
      for (const id of stop.nodes) nodes[id] = "glow"
      for (const id of stop.also) nodes[id] = "near"
      for (const id of stop.edges) edges[id] = "current"
    }
    return { nodes, edges }
  }, [stops, stop, index, atEnd])
  useHighlight(highlight)

  // The focus with what it is built on (its plumbing stays out of the frame: it can sit far away); when that is too
  // large to read, the camera centers on the first card in focus.
  const focus = (): CameraFocus | undefined => {
    const lookup = store.getState().nodeLookup
    const rectOf = (id: string): Rect | undefined => {
      const n = lookup.get(id)
      if (!n) return undefined
      const p = n.internals.positionAbsolute
      return { x: p.x, y: p.y, width: n.measured.width ?? n.width ?? 0, height: n.measured.height ?? n.height ?? 0 }
    }
    const ids = stop ? [...stop.nodes, ...stop.parents] : stops.flatMap((s) => s.nodes)
    const rect = unionRect(ids.flatMap((id) => rectOf(id) ?? []))
    const anchor = rectOf(stop ? stop.nodes[0] : (stops[0]?.nodes[0] ?? ""))
    return rect && { rect, ...(anchor ? { anchor } : {}) }
  }
  useFocusCamera(focus, `${stops.length}|${index}`, panel, reduced, 1)

  const go = useCallback((to: number) => useWalkthrough.setState({ step: Math.max(0, Math.min(to, stops.length)) }), [stops.length])
  usePlayerKeys(go, index)

  const real = graph.nodes.filter((n) => !n.plumbing).length
  const shown = caption && (expanded ? caption.lines : caption.lines.slice(0, LIST_LINES))
  return (
    <PlayerPanel
      title="Tour the infrastructure"
      icon={<MapIcon className="size-4 shrink-0 text-primary" />}
      counter={stop ? `${stop.label}/${steps}` : undefined}
      controls={stop ? { index, go } : undefined}
      panelRef={panel}
    >
      {!stops.length ? (
        <p className="text-muted-foreground" data-testid="tour-empty">
          Nothing on the canvas yet: add resources to tour them.
        </p>
      ) : caption && stop && shown ? (
        <div className="flex min-w-0 flex-col gap-1" data-testid="tour-step">
          <div className="flex min-w-0 items-center gap-2">
            <span data-testid="walkthrough-name" className="truncate font-semibold" title={caption.about}>
              {caption.title}
            </span>
            {caption.context ? (
              <span className="truncate text-xs text-muted-foreground">{caption.context}</span>
            ) : (
              stop.nodes.length > 1 && <span className="shrink-0 text-xs text-muted-foreground">{stop.nodes.length} resources</span>
            )}
            {caption.where && <WhereDot where={caption.where} className="ml-auto" />}
          </div>
          {stop.nodes.length === 1 ? (
            <p data-testid="walkthrough-caption" className="line-clamp-2 leading-snug" title={caption.lines[0].why}>
              {caption.lines[0].why}
            </p>
          ) : (
            <ul data-testid="walkthrough-caption" className="flex flex-col">
              {shown.map((l) => (
                <li key={l.id} className="truncate text-xs leading-5" title={`${l.label} — ${l.why}`}>
                  <span className="font-medium">{l.label}</span> <span className="text-muted-foreground">— {l.why}</span>
                </li>
              ))}
              {caption.lines.length > LIST_LINES && (
                <li>
                  <button type="button" className="text-xs text-primary hover:underline" onClick={() => setExpandedAt(expanded ? -1 : index)}>
                    {expanded ? "Show less" : `+${caption.lines.length - LIST_LINES} more`}
                  </button>
                </li>
              )}
            </ul>
          )}
          <OneLine testId="tour-next" text={caption.next} />
          {caption.also.length > 0 && <OneLine testId="walkthrough-also" text={`Also created here: ${caption.also.map((c) => c.label).join(", ")}`} />}
        </div>
      ) : (
        <div className="flex items-center gap-3" data-testid="walkthrough-summary">
          <p className="min-w-0 flex-1 font-medium">
            You toured {real} resource{real === 1 ? "" : "s"} in {steps} step{steps === 1 ? "" : "s"}, from the foundations to what runs on them.
          </p>
          <Button size="sm" variant="outline" onClick={replayWalkthrough}>
            <RotateCcwIcon />
            Replay
          </Button>
        </div>
      )}
    </PlayerPanel>
  )
}

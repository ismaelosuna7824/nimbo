// The request walkthrough drawn inside a canvas' React Flow (Build or Architecture): it derives the
// paths (paths.ts) from the Architecture model, plays them step by step with a dot travelling each hop,
// keeps the camera on the current hop, marks the canvas (store highlight) and shows the caption panel.
// Loaded the first time someone follows a request (see overlay.tsx).
import { Panel, ViewportPortal, useReactFlow, useStore, useStoreApi } from "@xyflow/react"
import {
  ChevronLeftIcon,
  ChevronRightIcon,
  ClockIcon,
  PauseIcon,
  PlayIcon,
  RotateCcwIcon,
  RouteIcon,
  SendIcon,
  XIcon,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { StatusDot, type StatusTone } from "@/components/canvas-card"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { measurePath, pointAt, roundedRoute } from "@/features/architecture/animate"
import type { Point, Rect } from "@/features/architecture/geometry"
import type { ArchModel } from "@/features/architecture/model"
import { useDockerModel, useKubernetesModel, useTerraformModel } from "@/features/architecture/use-model"
import type { Scene } from "@/features/architecture/use-scene"
import { schemaKey, useNodeSchemas } from "@/features/canvas/catalog"
import { isUsable, openEndpoints, useEndpoints } from "@/features/panels/endpoints/use-endpoints"
import { isDocker, isKubernetes, type ComposeGraph, type KubeGraph } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { captionOf, NO_ENTRY_HINT, summaryText, type Caption } from "./captions"
import { describeDocker, describeKubernetes, describeTerraform, type Chip, type Describer } from "./describe"
import { buildWalk, flowStarts, SHOW_ALL, walkEntries, walkSummary, type Entry, type HopTraits, type Step, type Walk } from "./paths"
import { closeWalkthrough, replayWalkthrough, SPEEDS, useWalkthrough, type EdgeMark, type Highlight, type NodeMark } from "./store"
import { arrivalRoute, bridgeRoute, cameraFor, hopLinks, unionRect } from "./travel"

export type LayerProps = { surface: "build" } | { surface: "architecture"; scene: Scene }

type Kind = "terraform" | "docker" | "kubernetes"

/** Dot speed in flow units per second at 1×, and the bounds of one hop's travel. */
const DOT_SPEED = 320
const TRAVEL_MS = { min: 600, max: 1600 }
/** Reading time on each step at 1×. */
const DWELL_MS = 2800
const CAMERA_MS = 650
const NO_TRAITS: HopTraits = { async: false, supporting: false }
const EMPTY_COMPOSE: ComposeGraph = { nodes: [], edges: [] }
const EMPTY_KUBE: KubeGraph = { nodes: [], edges: [] }
const WHERE_TONE: Record<"deployed" | "local" | "export", StatusTone> = { deployed: "ok", local: "ready", export: "off" }

export default function WalkthroughLayer(props: LayerProps) {
  const kind: Kind = useWorkflowStore((s) => (isDocker(s.workflow) ? "docker" : isKubernetes(s.workflow) ? "kubernetes" : "terraform"))
  if (props.surface === "architecture") return <ArchitectureLayer scene={props.scene} kind={kind} />
  return kind === "docker" ? <DockerBuildLayer /> : kind === "kubernetes" ? <KubeBuildLayer /> : <TerraformBuildLayer />
}

function TerraformBuildLayer() {
  const { model } = useTerraformModel()
  return model ? <BuildLayer model={model} kind="terraform" /> : null
}

function DockerBuildLayer() {
  return <BuildLayer model={useDockerModel()} kind="docker" />
}

function KubeBuildLayer() {
  return <BuildLayer model={useKubernetesModel()} kind="kubernetes" />
}

/** What the open workflow says about the model's items (names, types, roles, supporting pieces). */
function useDescriber(model: ArchModel, kind: Kind): Describer {
  const wf = useWorkflowStore((s) => s.workflow!)
  const deployed = useWorkflowStore((s) => s.deployed)
  const schemas = useNodeSchemas(wf.nodes)
  return useMemo(() => {
    if (kind === "docker") return describeDocker(model, wf.compose?.graph ?? EMPTY_COMPOSE)
    if (kind === "kubernetes") return describeKubernetes(model, wf.kubernetes?.graph ?? EMPTY_KUBE)
    return describeTerraform({
      model,
      nodes: wf.nodes,
      edges: wf.edges ?? [],
      schemaOf: (n) => schemas.get(schemaKey(n.kind, n.type)),
      deployed: new Set((deployed?.resources ?? []).flatMap((r) => (r.nodeId ? [r.nodeId] : []))),
    })
  }, [model, kind, wf.compose, wf.kubernetes, wf.nodes, wf.edges, schemas, deployed])
}

/** How a canvas draws the model: its node IDs for an item, their rectangles and the route of a hop. */
interface Surface {
  idsOf: (item: string) => string[]
  rectOf: (id: string) => Rect | undefined
  hop: (step: Step) => { route: Point[] | null; edges: string[]; via: string[] }
}

function ArchitectureLayer({ scene, kind }: { scene: Scene; kind: Kind }) {
  const describer = useDescriber(scene.model, kind)
  const surface = useMemo((): Surface => {
    const { byId, edges } = scene.diagram
    const routes = new Map(edges.map((e) => [e.id, e.route]))
    return {
      idsOf: (item) => (byId.has(item) ? [item] : []),
      rectOf: (id) => {
        const it = byId.get(id)
        return it && { x: it.ax, y: it.ay, width: it.width, height: it.height }
      },
      hop: (step) => {
        const route = step.edge && routes.get(step.edge.id)
        return {
          route: route ? roundedRoute(step.reversed ? [...route].reverse() : route) : null,
          edges: step.edge ? [step.edge.id] : [],
          via: [],
        }
      },
    }
  }, [scene])
  return <Player model={scene.model} describer={describer} surface={surface} kind={kind} />
}

function BuildLayer({ model, kind }: { model: ArchModel; kind: Kind }) {
  const describer = useDescriber(model, kind)
  const store = useStoreApi()
  const surface = useMemo((): Surface => {
    const itemNodes = new Set([...describer.info.values()].flatMap((i) => i.nodeIds))
    // Read live: cards move and get measured while the walkthrough runs.
    const rectOf = (id: string): Rect | undefined => {
      const n = store.getState().nodeLookup.get(id)
      if (!n) return undefined
      const p = n.internals.positionAbsolute
      return { x: p.x, y: p.y, width: n.measured.width ?? n.width ?? 0, height: n.measured.height ?? n.height ?? 0 }
    }
    const idsOf = (item: string) => (describer.info.get(item)?.nodeIds ?? []).filter((id) => store.getState().nodeLookup.has(id))
    const rectOfItem = (item: string) => unionRect(idsOf(item).flatMap((id) => rectOf(id) ?? []))
    return {
      idsOf,
      rectOf,
      hop: (step) => {
        if (!step.from) return { route: null, edges: [], via: [] }
        const to = rectOfItem(step.at)
        const from = idsOf(step.from)
        // Users and the Internet are not on the Build canvas: the request comes in from the left.
        if (!from.length) return { route: to ? arrivalRoute(to) : null, edges: [], via: [] }
        const links = hopLinks(store.getState().edges, from, idsOf(step.at), (id) => itemNodes.has(id))
        const a = rectOfItem(step.from)
        const mid = links.via[0] ? rectOf(links.via[0]) : undefined
        const route = a && to ? (mid ? [...bridgeRoute(a, mid), ...bridgeRoute(mid, to).slice(1)] : bridgeRoute(a, to)) : null
        return { route, ...links }
      },
    }
  }, [describer, store])
  return <Player model={model} describer={describer} surface={surface} kind={kind} />
}

function useReducedMotion() {
  const [reduced, setReduced] = useState(() => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false)
  useEffect(() => {
    const mq = window.matchMedia?.("(prefers-reduced-motion: reduce)")
    if (!mq) return
    const onChange = () => setReduced(mq.matches)
    mq.addEventListener("change", onChange)
    return () => mq.removeEventListener("change", onChange)
  }, [])
  return reduced
}

function travelMs(route: Point[] | null, speed: number) {
  if (!route || route.length < 2) return 0
  const ms = (measurePath(route).length / DOT_SPEED) * 1000
  return Math.min(TRAVEL_MS.max, Math.max(TRAVEL_MS.min, ms)) / speed
}

function Player({ model, describer, surface, kind }: { model: ArchModel; describer: Describer; surface: Surface; kind: Kind }) {
  const chosenEntry = useWalkthrough((s) => s.entry)
  const choices = useWalkthrough((s) => s.choices)
  const stepIndex = useWalkthrough((s) => s.step)
  const playing = useWalkthrough((s) => s.playing)
  const speed = useWalkthrough((s) => s.speed)
  const reduced = useReducedMotion()
  const flow = useReactFlow()
  const viewWidth = useStore((s) => s.width)
  const viewHeight = useStore((s) => s.height)
  const panel = useRef<HTMLDivElement>(null)

  const traits = useCallback((id: string) => describer.info.get(id) ?? NO_TRAITS, [describer])
  const entries = useMemo(() => walkEntries(model), [model])
  const starts = useMemo(() => (entries.length ? [] : flowStarts(model, traits)), [entries, model, traits])
  const entry = useMemo(() => {
    const key = chosenEntry ?? (entries.length === 1 ? entries[0].target : null)
    return [...entries, ...starts].find((e) => e.target === key) ?? null
  }, [chosenEntry, entries, starts])
  const walk = useMemo(() => (entry ? buildWalk(model, entry, choices, traits) : null), [entry, model, choices, traits])
  const index = walk ? Math.min(stepIndex, walk.pending ? walk.pending.step : walk.steps.length) : 0
  const atSummary = !!walk && !walk.pending && index >= walk.steps.length
  const step = walk && !atSummary ? walk.steps[index] : undefined
  const waiting = !!walk?.pending && index === walk.pending.step

  // Autoplay: travel, read, next. A fork without a chosen way waits for the choice.
  useEffect(() => {
    if (!playing || !walk || atSummary || waiting) return
    const travel = reduced ? 0 : travelMs(surface.hop(walk.steps[index]).route, speed)
    const last = !walk.pending && index + 1 >= walk.steps.length
    const t = window.setTimeout(() => useWalkthrough.setState({ step: index + 1, ...(last ? { playing: false } : {}) }), travel + DWELL_MS / speed)
    return () => window.clearTimeout(t)
  }, [playing, walk, index, atSummary, waiting, reduced, speed, surface])

  // The canvas marks: the current item ringed, where it came from (and helpers in between) clear, the rest of the path softer.
  useEffect(() => {
    if (!walk) {
      useWalkthrough.setState({ highlight: null })
      return
    }
    const nodes: Record<string, NodeMark> = {}
    const edges: Record<string, EdgeMark> = {}
    const mark = (ids: string[], m: NodeMark) => {
      for (const id of ids) if (m === "current" || nodes[id] !== "current") nodes[id] = m
    }
    for (const s of walk.steps) {
      mark(surface.idsOf(s.at), atSummary ? "near" : "path")
      const h = surface.hop(s)
      for (const id of h.edges) edges[id] = "path"
      mark(h.via, atSummary ? "near" : "path")
    }
    if (walk.pending) for (const o of walk.pending.options) mark(surface.idsOf(o), "path")
    if (step) {
      const h = surface.hop(step)
      for (const id of h.edges) edges[id] = "current"
      if (step.from) mark(surface.idsOf(step.from), "near")
      mark(h.via, "near")
      mark(surface.idsOf(step.at), "current")
    }
    const next: Highlight = { nodes, edges }
    const prev = useWalkthrough.getState().highlight
    if (!prev || JSON.stringify(prev) !== JSON.stringify(next)) useWalkthrough.setState({ highlight: next })
  }, [walk, step, atSummary, surface])
  useEffect(() => () => useWalkthrough.setState({ highlight: null }), [])

  // The camera follows the hop (both ends in view, above the caption panel); the summary shows the whole path.
  useEffect(() => {
    if (!walk || !viewWidth || !viewHeight) return
    const rectsOf = (item: string) => surface.idsOf(item).flatMap((id) => surface.rectOf(id) ?? [])
    let focus: Rect | undefined
    if (atSummary) focus = unionRect(walk.steps.flatMap((s) => rectsOf(s.at)))
    else if (step) {
      const own = rectsOf(step.at)
      const from = step.from ? rectsOf(step.from) : []
      const via = surface.hop(step).via.flatMap((id) => surface.rectOf(id) ?? [])
      // An item off the canvas (Users on Build): look at where the request goes next.
      const ahead = !own.length && walk.steps[index + 1] ? rectsOf(walk.steps[index + 1].at) : []
      focus = unionRect([...own, ...from, ...via, ...ahead])
      const route = surface.hop(step).route
      if (focus && route?.length) focus = unionRect([focus, { x: route[0].x, y: route[0].y, width: 1, height: 1 }])
    }
    if (!focus) return
    const covered = (panel.current?.offsetHeight ?? 220) + 32
    const cam = cameraFor(focus, { width: viewWidth, height: viewHeight }, covered, { maxZoom: atSummary ? 1 : 1.1 })
    void flow.setCenter(cam.x, cam.y, { zoom: cam.zoom, duration: reduced ? 0 : CAMERA_MS })
  }, [walk, step, index, atSummary, surface, flow, viewWidth, viewHeight, reduced])

  const go = useCallback(
    (to: number) => useWalkthrough.setState({ step: Math.max(0, Math.min(to, walk?.pending ? walk.pending.step : (walk?.steps.length ?? 0))) }),
    [walk],
  )

  // ← → step, space plays or pauses, Esc closes (not while typing, nor inside a dialog). Handled in the capture phase
  // and stopped there: React Flow must not also nudge a selected card with the arrows (↑ ↓ on the canvas are swallowed
  // for the same reason) or pan with space.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const el = e.target instanceof HTMLElement ? e.target : null
      // Keys typed elsewhere (a page over the editor, the inspector, the dock) are theirs.
      if (el && el !== document.body && !el.closest("[data-canvas-view]")) return
      if (el?.closest("[role=dialog], [role=alertdialog]") || el?.isContentEditable || (el && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName))) return
      if (e.key === "Escape") closeWalkthrough()
      else if (e.key === "ArrowRight") go(index + 1)
      else if (e.key === "ArrowLeft") go(index - 1)
      else if (e.key === " " && !el?.closest("button, a")) useWalkthrough.setState((s) => ({ playing: !s.playing }))
      else if (!((e.key === "ArrowUp" || e.key === "ArrowDown") && el?.closest(".react-flow"))) return
      e.preventDefault()
      e.stopPropagation()
    }
    window.addEventListener("keydown", onKey, true)
    return () => window.removeEventListener("keydown", onKey, true)
  }, [go, index])

  const caption = useMemo(() => (walk && step ? captionOf(walk, index, describer) : null), [walk, step, index, describer])
  const route = useMemo(() => (step && !reduced ? surface.hop(step).route : null), [step, reduced, surface])
  // Choices name the service too: two "orders" (a table and a queue) must not look alike.
  const nameOf = (id: string) => {
    const i = describer.info.get(id)
    return i?.ref ?? i?.name ?? id
  }

  let body: ReactNode
  if (!walk) {
    body = entries.length ? (
      <EntryPicker title="Where does the request come in?" options={entries} nameOf={nameOf} />
    ) : (
      <div data-testid="walkthrough-no-entry" className="flex flex-col gap-1.5">
        <p className="font-medium">This design has no entry point yet</p>
        <p className="text-xs text-muted-foreground">{NO_ENTRY_HINT[kind]}</p>
        {starts.length > 0 && <EntryPicker title="Or follow what flows from:" options={starts} nameOf={nameOf} />}
      </div>
    )
  } else if (atSummary) {
    body = <Summary model={model} walk={walk} describer={describer} />
  } else if (caption && step) {
    body = <StepCaption caption={caption} step={step} index={index} choice={choices[step.at]} nameOf={nameOf} />
  }

  return (
    <>
      {route && step && (
        <ViewportPortal>
          <Dot key={`${index}:${step.at}`} route={route} duration={travelMs(route, speed)} />
        </ViewportPortal>
      )}
      <Panel position="bottom-center" className="!mb-4 w-[34rem] max-w-[calc(100%-2rem)]">
        <div
          ref={panel}
          role="region"
          aria-label="Follow a request"
          data-testid="walkthrough-panel"
          className="flex flex-col gap-2 rounded-xl border bg-background/95 p-3 text-sm shadow-lg backdrop-blur"
        >
          <div className="flex items-center gap-2">
            <RouteIcon className="size-4 shrink-0 text-primary" />
            <span className="font-medium">Follow a request</span>
            {walk && !atSummary && (
              <span data-testid="walkthrough-step-counter" className="text-xs text-muted-foreground tabular-nums">
                {index + 1}/{walk.steps.length}
                {walk.pending ? "+" : ""}
              </span>
            )}
            <div className="ml-auto flex items-center gap-0.5">
              <SpeedButton />
              <IconButton label="Close (Esc)" onClick={closeWalkthrough}>
                <XIcon />
              </IconButton>
            </div>
          </div>
          {body}
          {walk && !atSummary && (
            <div className="flex items-center gap-1 border-t pt-2">
              <IconButton label="Restart" onClick={replayWalkthrough}>
                <RotateCcwIcon />
              </IconButton>
              <IconButton label="Previous step (←)" onClick={() => go(index - 1)} disabled={index === 0}>
                <ChevronLeftIcon />
              </IconButton>
              <Button
                size="sm"
                variant="outline"
                className="w-20"
                onClick={() => useWalkthrough.setState((s) => ({ playing: !s.playing }))}
                aria-label={playing ? "Pause (space)" : "Play (space)"}
              >
                {playing ? <PauseIcon /> : <PlayIcon />}
                {playing ? "Pause" : "Play"}
              </Button>
              <IconButton label="Next step (→)" onClick={() => go(index + 1)} disabled={waiting}>
                <ChevronRightIcon />
              </IconButton>
              {waiting && <span className="ml-auto text-xs text-muted-foreground">Pick a way to go on</span>}
            </div>
          )}
        </div>
      </Panel>
    </>
  )
}

function IconButton({ label, onClick, disabled, children }: { label: string; onClick: () => void; disabled?: boolean; children: ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button size="icon-sm" variant="ghost" aria-label={label} onClick={onClick} disabled={disabled}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

function SpeedButton() {
  const speed = useWalkthrough((s) => s.speed)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="xs"
          variant="ghost"
          className="w-10 font-mono tabular-nums"
          aria-label={`Speed ${speed}×`}
          onClick={() => useWalkthrough.setState({ speed: SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length] })}
        >
          {speed}×
        </Button>
      </TooltipTrigger>
      <TooltipContent>Speed</TooltipContent>
    </Tooltip>
  )
}

function choiceChip(selected: boolean) {
  return cn("h-6 rounded-full px-2.5 text-xs", selected && "border-primary bg-primary/10 text-primary hover:bg-primary/15")
}

function EntryPicker({ title, options, nameOf }: { title: string; options: Entry[]; nameOf: (id: string) => string }) {
  return (
    <div className="flex flex-col gap-1.5" data-testid="walkthrough-entries">
      <p className="text-xs text-muted-foreground">{title}</p>
      <div className="flex flex-wrap gap-1.5">
        {options.map((e) => (
          <Button
            key={e.target}
            size="xs"
            variant="outline"
            className={choiceChip(false)}
            onClick={() => useWalkthrough.setState({ entry: e.target, choices: {}, step: 0, playing: true })}
          >
            {nameOf(e.target)}
          </Button>
        ))}
      </div>
    </div>
  )
}

function StepCaption({ caption: c, step, index, choice, nameOf }: { caption: Caption; step: Step; index: number; choice: string | undefined; nameOf: (id: string) => string }) {
  const select = useWorkflowStore((s) => s.select)
  // Taking a way goes on along it at once.
  const choose = (way: string) => useWalkthrough.setState((s) => ({ choices: { ...s.choices, [step.at]: way }, step: index + 1 }))
  return (
    <div className="flex flex-col gap-1.5" data-testid="walkthrough-step">
      <div className="flex min-w-0 items-center gap-2">
        <span data-testid="walkthrough-name" className="truncate font-semibold">
          {c.name}
        </span>
        <span className="truncate text-xs text-muted-foreground">{c.type}</span>
        {c.where && (
          <StatusDot tone={WHERE_TONE[c.where.tone]} label={c.where.text} className="ml-auto">
            {c.where.tone === "export" ? "Export only" : c.where.tone === "deployed" ? "Deployed" : "Local"}
          </StatusDot>
        )}
      </div>
      <p data-testid="walkthrough-caption" className="leading-snug">
        {c.text}
      </p>
      {c.about && <p className="text-xs text-muted-foreground">{c.about}</p>}
      {(c.via || c.facts.length > 0) && (
        <p className="text-xs text-muted-foreground" data-testid="walkthrough-via">
          {c.via && (
            <>
              Connected by <span className="font-mono text-foreground">{c.via}</span>
            </>
          )}
          {c.via && c.facts.length > 0 && " · "}
          {c.facts.length > 0 && <span className="font-mono">{c.facts.join(" · ")}</span>}
        </p>
      )}
      {c.async && (
        <p data-testid="walkthrough-async" className="flex items-start gap-1.5 rounded-lg border border-primary/25 bg-primary/5 px-2 py-1.5 text-xs">
          <ClockIcon className="mt-px size-3.5 shrink-0 text-primary" />
          <span>
            <span className="font-medium">Asynchronous:</span> the request returns here; the message continues later.
          </span>
        </p>
      )}
      {c.also.length > 0 && (
        <div className="flex flex-wrap items-center gap-1" data-testid="walkthrough-also">
          <span className="mr-0.5 text-xs text-muted-foreground">Also involved:</span>
          {c.also.slice(0, 6).map((chip) => (
            <AlsoChip key={chip.label} chip={chip} onSelect={select} />
          ))}
          {c.also.length > 6 && <span className="text-xs text-muted-foreground">+{c.also.length - 6} more</span>}
        </div>
      )}
      {step.branches && !step.joins && (
        <div className="flex flex-wrap items-center gap-1.5" data-testid="walkthrough-choices">
          <span className="text-xs text-muted-foreground">Which way?</span>
          {step.branches.map((b) => (
            <Button key={b} size="xs" variant="outline" className={choiceChip(choice === b)} onClick={() => choose(b)}>
              {nameOf(b)}
            </Button>
          ))}
          <Button size="xs" variant="outline" className={choiceChip(choice === SHOW_ALL)} onClick={() => choose(SHOW_ALL)}>
            Show all
          </Button>
        </div>
      )}
    </div>
  )
}

function AlsoChip({ chip, onSelect }: { chip: Chip; onSelect: (id: string) => void }) {
  const body = (
    <span className="inline-flex h-5 max-w-48 items-center truncate rounded-full border bg-muted/60 px-2 text-[11px] text-foreground">{chip.label}</span>
  )
  const tip = chip.detail ?? (chip.nodeId ? `Show ${chip.label}` : undefined)
  const trigger = chip.nodeId ? (
    <button type="button" className="rounded-full focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none" onClick={() => onSelect(chip.nodeId!)}>
      {body}
    </button>
  ) : (
    body
  )
  if (!tip) return trigger
  return (
    <Tooltip>
      <TooltipTrigger asChild>{trigger}</TooltipTrigger>
      <TooltipContent>{tip}</TooltipContent>
    </Tooltip>
  )
}

function Summary({ model, walk, describer }: { model: ArchModel; walk: Walk; describer: Describer }) {
  const endpoints = useEndpoints().data?.resources
  // "Send a real request": the entry's deployed endpoint (else the first one along the path) in the Endpoints tab's Try it.
  const target = useMemo(() => {
    const live = (endpoints ?? []).filter((r) => r.nodeId && isUsable(r) && ((r.endpoints?.length ?? 0) > 0 || r.actions.length > 0))
    for (const s of walk.steps) {
      const ids = describer.info.get(s.at)?.nodeIds ?? []
      const r = live.find((x) => ids.includes(x.nodeId!))
      if (r) return r
    }
    return undefined
  }, [endpoints, walk, describer])
  const names = [...new Set(walk.steps.map((s) => describer.info.get(s.at)?.name ?? s.at))]
  return (
    <div className="flex flex-col gap-2" data-testid="walkthrough-summary">
      <p className="font-medium">{summaryText(walkSummary(model, walk))}</p>
      <p className="text-xs text-muted-foreground">{names.join(" → ")}</p>
      {walk.truncated && <p className="text-xs text-muted-foreground">The walk stopped after {walk.steps.length} steps; pick fewer ways to see the rest.</p>}
      <div className="flex items-center gap-2">
        <Button size="sm" variant="outline" onClick={replayWalkthrough}>
          <RotateCcwIcon />
          Replay
        </Button>
        {target && (
          <Button size="sm" onClick={() => openEndpoints(target.address, { tryIt: true })}>
            <SendIcon />
            Send a real request
          </Button>
        )}
      </div>
    </div>
  )
}

/** The request: a dot travelling the hop's route once, then resting at its end. */
function Dot({ route, duration }: { route: Point[]; duration: number }) {
  const el = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const path = measurePath(route)
    const place = (d: number) => {
      const p = pointAt(path, d)
      if (el.current) el.current.style.transform = `translate(${p.x}px, ${p.y}px)`
    }
    place(0)
    const start = performance.now()
    let frame = 0
    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / Math.max(1, duration))
      // Ease in and out: the request leaves gently and arrives softly.
      place(path.length * (t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2))
      if (t < 1) frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [route, duration])
  return (
    <div ref={el} className="pointer-events-none absolute top-0 left-0 z-[2000]" data-testid="walkthrough-dot">
      <span className="-mt-1.5 -ml-1.5 block size-3 rounded-full bg-primary shadow-[0_0_0_4px_color-mix(in_oklab,var(--color-primary)_25%,transparent)]" />
    </div>
  )
}

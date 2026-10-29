// The walkthrough drawn inside the Build canvas' React Flow. "Follow a request" derives the request paths (paths.ts)
// from the Architecture model, plays them step by step with a dot travelling each hop, keeps the camera on the
// current hop, marks the canvas (store highlight) and shows the caption panel; "Tour the infrastructure"
// (tour-layer.tsx) walks the canvas graph from its roots. Loaded the first time someone starts one (see overlay.tsx).
import { ViewportPortal, useStoreApi } from "@xyflow/react"
import { ClockIcon, RotateCcwIcon, RouteIcon, SendIcon } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, type ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { measurePath, pointAt } from "@/features/architecture/animate"
import type { Point, Rect } from "@/features/architecture/geometry"
import type { ArchModel } from "@/features/architecture/model"
import { useDockerModel, useKubernetesModel, useTerraformModel } from "@/features/architecture/use-model"
import { schemaKey, useNodeSchemas } from "@/features/canvas/catalog"
import { isUsable, openEndpoints, useEndpoints } from "@/features/panels/endpoints/use-endpoints"
import { isDocker, isKubernetes, type ComposeGraph, type KubeGraph } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { captionOf, NO_ENTRY_HINT, summaryText, type Caption } from "./captions"
import { describeDocker, describeKubernetes, describeTerraform, type Describer } from "./describe"
import { buildWalk, flowStarts, SHOW_ALL, walkEntries, walkSummary, type Entry, type HopTraits, type Step, type Walk } from "./paths"
import { useFocusCamera, useHighlight, usePlayerKeys, useReducedMotion, type CameraFocus } from "./player-hooks"
import { OneLine, PlayerPanel, WhereDot } from "./player-ui"
import { replayWalkthrough, useWalkthrough, type EdgeMark, type Highlight, type NodeMark } from "./store"
import TourLayer from "./tour-layer"
import { arrivalRoute, bridgeRoute, hopLinks, unionRect } from "./travel"

type Kind = "terraform" | "docker" | "kubernetes"

/** Dot speed in flow units per second at 1×, and the bounds of one hop's travel. */
const DOT_SPEED = 320
const TRAVEL_MS = { min: 600, max: 1600 }
/** Reading time on each step at 1×. */
const DWELL_MS = 2800
const NO_TRAITS: HopTraits = { async: false, supporting: false }
const EMPTY_COMPOSE: ComposeGraph = { nodes: [], edges: [] }
const EMPTY_KUBE: KubeGraph = { nodes: [], edges: [] }

export default function WalkthroughLayer() {
  const kind: Kind = useWorkflowStore((s) => (isDocker(s.workflow) ? "docker" : isKubernetes(s.workflow) ? "kubernetes" : "terraform"))
  const mode = useWalkthrough((s) => s.mode)
  if (mode === "tour") return <TourLayer kind={kind} />
  return kind === "docker" ? <DockerBuildLayer /> : kind === "kubernetes" ? <KubeBuildLayer /> : <TerraformBuildLayer />
}

/** Cards the active environment does not create (count = 0) though the design has them. */
function useAbsent(design: ArchModel | null, active: ArchModel | null): { ids: Set<string>; env: string } | undefined {
  const env = useWorkflowStore((s) => s.workflow?.activeEnvironment)
  return useMemo(() => {
    if (!design || !active || !env) return undefined
    const here = new Set(active.cards.map((c) => c.id))
    const ids = new Set(design.cards.filter((c) => !here.has(c.id)).map((c) => c.nodeId))
    return ids.size ? { ids, env } : undefined
  }, [design, active, env])
}

// The Build canvas shows every resource of the design, so its walkthrough follows the whole design: a front door the
// active environment skips (count = 0 in a sandbox environment) is still where requests come in.
function TerraformBuildLayer() {
  const { model } = useTerraformModel({ everyEnvironment: true })
  const { model: active } = useTerraformModel()
  const absent = useAbsent(model, active)
  return model ? <BuildLayer model={model} kind="terraform" absent={absent} /> : null
}

function DockerBuildLayer() {
  return <BuildLayer model={useDockerModel()} kind="docker" />
}

function KubeBuildLayer() {
  return <BuildLayer model={useKubernetesModel()} kind="kubernetes" />
}

/** What the open workflow says about the model's items (names, types, roles, supporting pieces). */
function useDescriber(model: ArchModel, kind: Kind, absent?: { ids: Set<string>; env: string }): Describer {
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
      absent,
    })
  }, [model, kind, wf.compose, wf.kubernetes, wf.nodes, wf.edges, schemas, deployed, absent])
}

/** How a canvas draws the model: its node IDs for an item, their rectangles and the route of a hop. */
interface Surface {
  idsOf: (item: string) => string[]
  rectOf: (id: string) => Rect | undefined
  hop: (step: Step) => { route: Point[] | null; edges: string[]; via: string[] }
}

function BuildLayer({ model, kind, absent }: { model: ArchModel; kind: Kind; absent?: { ids: Set<string>; env: string } }) {
  const describer = useDescriber(model, kind, absent)
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
  const highlight = useMemo((): Highlight | null => {
    if (!walk) return null
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
    return { nodes, edges }
  }, [walk, step, atSummary, surface])
  useHighlight(highlight)

  // The camera follows the hop (both ends in view, above the caption panel), the current item when they are too far
  // apart to read; the summary shows the whole path (from the entry when it is too large).
  const focus = (): CameraFocus | undefined => {
    if (!walk) return undefined
    const rectsOf = (item: string) => surface.idsOf(item).flatMap((id) => surface.rectOf(id) ?? [])
    if (atSummary) {
      const rect = unionRect(walk.steps.flatMap((s) => rectsOf(s.at)))
      const first = unionRect(walk.steps.slice(0, 2).flatMap((s) => rectsOf(s.at)))
      return rect && { rect, ...(first ? { anchor: first } : {}) }
    }
    if (!step) return undefined
    const own = rectsOf(step.at)
    const from = step.from ? rectsOf(step.from) : []
    const via = surface.hop(step).via.flatMap((id) => surface.rectOf(id) ?? [])
    // An item off the canvas (Users on Build): look at where the request goes next.
    const ahead = !own.length && walk.steps[index + 1] ? rectsOf(walk.steps[index + 1].at) : []
    const anchor = unionRect([...own, ...ahead])
    const rect = unionRect([...own, ...from, ...via, ...ahead])
    const route = surface.hop(step).route
    const all = rect && route?.length ? unionRect([rect, { x: route[0].x, y: route[0].y, width: 1, height: 1 }]) : rect
    return all && { rect: all, ...(anchor ? { anchor } : {}) }
  }
  const walkKey = walk ? walk.steps.map((s) => s.at).join(">") : ""
  useFocusCamera(focus, `${walkKey}|${index}|${atSummary}`, panel, reduced, atSummary ? 1 : 1.1)

  const go = useCallback(
    (to: number) => useWalkthrough.setState({ step: Math.max(0, Math.min(to, walk?.pending ? walk.pending.step : (walk?.steps.length ?? 0))) }),
    [walk],
  )
  usePlayerKeys(go, index)

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
      <PlayerPanel
        title="Follow a request"
        icon={<RouteIcon className="size-4 shrink-0 text-primary" />}
        counter={walk && !atSummary ? `${index + 1}/${walk.steps.length}${walk.pending ? "+" : ""}` : undefined}
        controls={walk && !atSummary ? { index, go, nextDisabled: waiting, hint: waiting ? "Pick a way to go on" : undefined } : undefined}
        panelRef={panel}
      >
        {body}
      </PlayerPanel>
    </>
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

/** One step, compact: a header, what happens (two lines at most), and one line each for the connection, the asynchronous hand-off, what else is involved and the ways out. */
function StepCaption({ caption: c, step, index, choice, nameOf }: { caption: Caption; step: Step; index: number; choice: string | undefined; nameOf: (id: string) => string }) {
  // Taking a way goes on along it at once.
  const choose = (way: string) => useWalkthrough.setState((s) => ({ choices: { ...s.choices, [step.at]: way }, step: index + 1 }))
  const via = [c.via ? `Connected by ${c.via}` : "", c.facts.join(" · ")].filter(Boolean).join(" · ")
  return (
    <div className="flex min-w-0 flex-col gap-1" data-testid="walkthrough-step">
      <div className="flex min-w-0 items-center gap-2">
        <span data-testid="walkthrough-name" className="truncate font-semibold" title={c.about}>
          {c.name}
        </span>
        <span className="truncate text-xs text-muted-foreground" title={c.about}>
          {c.type}
        </span>
        {c.where && <WhereDot where={c.where} className="ml-auto" />}
      </div>
      <p data-testid="walkthrough-caption" className="line-clamp-2 leading-snug" title={c.text}>
        {c.text}
      </p>
      {via && <OneLine testId="walkthrough-via" text={via} />}
      {c.async && (
        <OneLine
          testId="walkthrough-async"
          icon={<ClockIcon className="size-3.5 shrink-0 text-primary" />}
          text="Asynchronous: the request returns here; the message continues later."
          className="text-foreground"
        />
      )}
      {c.also.length > 0 && <OneLine testId="walkthrough-also" text={`Also involved: ${c.also.map((x) => x.label).join(", ")}`} />}
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

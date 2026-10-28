import { useMemo, useState } from "react"
import { useMutation } from "@tanstack/react-query"
import {
  ArrowDownIcon,
  ArrowUpDownIcon,
  CheckIcon,
  CircleCheckIcon,
  CircleHelpIcon,
  CircleXIcon,
  FlaskConicalIcon,
  GlobeIcon,
  LightbulbIcon,
  PlusIcon,
  RadioIcon,
  RouteIcon,
  ShieldXIcon,
  XIcon,
} from "lucide-react"
import { errorMessage } from "@/components/errors"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import type {
  HopVerdict,
  LiveCheck,
  NetEndpoint,
  NetPlacement,
  NetSubnet,
  NetProtocol,
  PlacementKind,
  ReachabilityHop,
  ReachabilityResult,
} from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { CopyButton } from "../panels/endpoints/bits"
import { nodeLabelById } from "../panels/lib/nodes"
import { BasisBadge, TermHint } from "./glossary"
import { NET_GLOSSARY, SUBNET_META } from "./terms"
import { closeReachability, INTERNET, useReachability } from "./store"
import { addTestInstance, useNetwork } from "./use-network"

const PORT_PRESETS = [
  { port: 22, label: "SSH" },
  { port: 80, label: "HTTP" },
  { port: 443, label: "HTTPS" },
  { port: 3306, label: "MySQL" },
  { port: 5432, label: "PostgreSQL" },
  { port: 6379, label: "Redis" },
]

const PLACEMENT_LABEL: Record<PlacementKind, string> = {
  instance: "Instances",
  function: "Functions",
  database: "Databases",
  cache: "Caches",
  "load-balancer": "Load balancers",
  "container-service": "Container services",
  kubernetes: "Kubernetes clusters",
  interface: "Network interfaces",
}

const VERDICT_META = {
  reachable: {
    icon: CircleCheckIcon,
    title: "✓ Reachable",
    className: "border-emerald-500/40 bg-emerald-500/10 text-emerald-800 dark:text-emerald-200",
  },
  unreachable: {
    icon: CircleXIcon,
    title: "✗ Not reachable",
    className: "border-destructive/40 bg-destructive/10 text-destructive",
  },
  unknown: {
    icon: CircleHelpIcon,
    title: "? Can't tell",
    className: "border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-200",
  },
} as const

const HOP_META: Record<HopVerdict, { mark: string; label: string; className: string }> = {
  allow: { mark: "✓", label: "Allowed", className: "text-emerald-600 dark:text-emerald-400" },
  deny: { mark: "✗", label: "Blocked", className: "text-destructive" },
  unknown: { mark: "?", label: "Unknown", className: "text-amber-600 dark:text-amber-400" },
}

/** Worst verdict wins when several hops share a canvas node. */
const HOP_RANK: Record<HopVerdict, number> = { allow: 0, unknown: 1, deny: 2 }

const endpointValue = (e: NetEndpoint | null) => (!e ? "" : e.kind === "internet" ? "internet" : `node:${e.nodeId ?? ""}`)

function parseEndpoint(value: string): NetEndpoint {
  return value === "internet" ? INTERNET : { kind: "node", nodeId: value.slice("node:".length) }
}

/**
 * Asks "can traffic get from A to B?" and shows the answer hop by hop, highlighting the
 * path on the canvas. A side sheet without a backdrop so the highlighted canvas stays visible.
 */
export function ReachabilityDialog() {
  const open = useReachability((s) => s.open)
  const seq = useReachability((s) => s.seq)
  const workflowId = useWorkflowStore((s) => s.workflow?.id)

  return (
    <Sheet open={open && !!workflowId} onOpenChange={(o) => !o && closeReachability()} modal={false}>
      <SheetContent
        side="right"
        className="w-full gap-0 data-[side=right]:sm:max-w-lg"
        onInteractOutside={(e) => e.preventDefault()}
        data-testid="reachability-dialog"
      >
        <SheetHeader className="border-b pr-12">
          <SheetTitle className="flex items-center gap-2">
            <RouteIcon className="size-4 text-primary" />
            Check reachability
          </SheetTitle>
          <SheetDescription className="text-xs">{NET_GLOSSARY.reachability}</SheetDescription>
        </SheetHeader>
        {/* Keyed by question: re-opening with another preset starts without the previous answer. */}
        {open && workflowId && <ReachabilityBody key={`${workflowId}:${seq}`} workflowId={workflowId} />}
      </SheetContent>
    </Sheet>
  )
}

function ReachabilityBody({ workflowId }: { workflowId: string }) {
  const { source, destination, protocol, port } = useReachability()
  const nodes = useWorkflowStore((s) => s.workflow?.nodes)
  const deployed = useWorkflowStore((s) => (s.deployed?.resources ?? []).some((r) => r.mode === "managed"))
  const network = useNetwork()
  const summary = network.data
  const labels = useMemo(() => nodeLabelById(nodes), [nodes])
  const [result, setResult] = useState<ReachabilityResult | null>(null)
  const hasInstance = (nodes ?? []).some((n) => n.kind === "resource" && n.type === "aws_instance")

  const check = useMutation({
    mutationFn: (live: boolean) =>
      api.reachability(workflowId, { source, destination: destination!, protocol, port, live: live || undefined }),
  })
  // Per-call callback: it doesn't fire once the question changed (check.reset() detaches the answer in flight).
  const run = (live: boolean) =>
    check.mutate(live, {
      onSuccess: (r) => {
        setResult(r)
        const highlight: Record<string, HopVerdict> = {}
        for (const hop of r.path ?? []) {
          if (!hop.nodeId) continue
          const prev = highlight[hop.nodeId]
          if (!prev || HOP_RANK[hop.verdict] > HOP_RANK[prev]) highlight[hop.nodeId] = hop.verdict
        }
        useReachability.setState({ highlight })
      },
    })

  const placementLabel = (p: NetPlacement) => {
    const label = p.nodeId ? labels[p.nodeId] : undefined
    const siblings = (summary?.placements ?? []).filter((x) => x.nodeId && x.nodeId === p.nodeId).length
    return !label ? p.address : siblings > 1 ? `${label} · ${p.address}` : label
  }
  const groups = useMemo(() => {
    const out = new Map<PlacementKind, NetPlacement[]>()
    for (const p of summary?.placements ?? []) out.set(p.kind, [...(out.get(p.kind) ?? []), p])
    return [...out.entries()]
  }, [summary])

  /** Select value of an endpoint: the placement it names (by address or node ID), else its raw value. */
  const valueOf = (e: NetEndpoint | null) => {
    if (!e || e.kind === "internet") return endpointValue(e)
    const p = summary?.placements.find((x) => x.address === e.nodeId || x.nodeId === e.nodeId)
    return p ? `node:${p.address}` : endpointValue(e)
  }
  const sourceValue = valueOf(source)
  const destinationValue = valueOf(destination)
  const knownValues = new Set(["internet", ...(summary?.placements ?? []).map((p) => `node:${p.address}`)])

  const set = (patch: Partial<{ source: NetEndpoint; destination: NetEndpoint | null; protocol: NetProtocol; port: number }>) => {
    useReachability.setState(patch)
    setResult(null)
    check.reset()
    useReachability.setState({ highlight: null })
  }

  const busy = check.isPending
  const liveRunning = busy && check.variables === true
  const sameEnds = !!destination && sourceValue === destinationValue
  const canCheck = !!destination && !sameEnds && (protocol === "icmp" || (port >= 0 && port <= 65535)) && !busy

  const endpointPicker = (id: string, label: string, value: string, onChange: (v: string) => void, internetFirst: boolean) => {
    const internet = (
      <SelectItem value="internet">
        <GlobeIcon /> Internet
      </SelectItem>
    )
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <Label htmlFor={id} className="text-xs">
          {label}
        </Label>
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger id={id} size="sm" aria-label={label} className="w-full min-w-0">
            <SelectValue placeholder="Pick a resource…" />
          </SelectTrigger>
          <SelectContent position="popper">
            {internetFirst && internet}
            {groups.map(([kind, items]) => (
              <SelectGroup key={kind}>
                <SelectLabel>{PLACEMENT_LABEL[kind] ?? kind}</SelectLabel>
                {items.map((p) => (
                  <SelectItem key={p.address} value={`node:${p.address}`}>
                    {placementLabel(p)}
                  </SelectItem>
                ))}
              </SelectGroup>
            ))}
            {value && !knownValues.has(value) && (
              <SelectItem value={value}>{labels[value.slice("node:".length)] ?? value.slice("node:".length)}</SelectItem>
            )}
            {!internetFirst && internet}
          </SelectContent>
        </Select>
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      <div className="flex flex-col gap-3 border-b p-4">
        {summary && (
          <div className="flex items-center gap-2">
            <BasisBadge basis={summary.basis} reason={summary.basisReason} />
          </div>
        )}
        <div className="flex items-end gap-2">
          {endpointPicker("reach-from", "From", sourceValue, (v) => set({ source: parseEndpoint(v) }), true)}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Swap from and to"
                disabled={!destination}
                onClick={() => destination && set({ source: destination, destination: source })}
              >
                <ArrowUpDownIcon className="rotate-90" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Swap</TooltipContent>
          </Tooltip>
          {endpointPicker("reach-to", "To", destinationValue, (v) => set({ destination: parseEndpoint(v) }), false)}
        </div>
        {(summary?.placements.length ?? 0) === 0 && !network.isPending && (
          <p className="text-xs text-muted-foreground">
            Nothing in this workflow sits in a network yet: add an instance, a database or a function inside a VPC.
          </p>
        )}
        <div className="flex items-end gap-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="reach-protocol" className="text-xs">
              Protocol
            </Label>
            <Select value={protocol} onValueChange={(v) => set({ protocol: v as NetProtocol })}>
              <SelectTrigger id="reach-protocol" size="sm" aria-label="Protocol" className="w-24">
                <SelectValue />
              </SelectTrigger>
              <SelectContent position="popper">
                <SelectItem value="tcp">TCP</SelectItem>
                <SelectItem value="udp">UDP</SelectItem>
                <SelectItem value="icmp">ICMP (ping)</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="reach-port" className="text-xs">
              Port
            </Label>
            <Input
              id="reach-port"
              type="number"
              min={0}
              max={65535}
              className="h-7 w-24 text-xs"
              value={protocol === "icmp" ? "" : port}
              placeholder={protocol === "icmp" ? "—" : undefined}
              disabled={protocol === "icmp"}
              onChange={(e) => set({ port: Number(e.target.value) })}
            />
          </div>
        </div>
        {protocol !== "icmp" && (
          <div className="flex flex-wrap gap-1" role="group" aria-label="Common ports">
            {PORT_PRESETS.map((p) => (
              <Button
                key={p.port}
                variant={port === p.port ? "secondary" : "outline"}
                size="xs"
                aria-pressed={port === p.port}
                onClick={() => set({ port: p.port })}
              >
                <span className="tabular-nums">{p.port}</span>
                <span className="text-muted-foreground">{p.label}</span>
              </Button>
            ))}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" disabled={!canCheck} onClick={() => run(false)} data-testid="reachability-check">
            {busy && !liveRunning ? <Spinner /> : <RouteIcon />}
            Check
          </Button>
          <Tooltip>
            <TooltipTrigger asChild>
              {/* Wrapper keeps the tooltip working while the button is disabled. */}
              <span tabIndex={deployed ? -1 : 0}>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!deployed || !canCheck}
                  onClick={() => run(true)}
                  data-testid="reachability-live"
                >
                  {liveRunning ? <Spinner /> : <RadioIcon />}
                  Run live test
                </Button>
              </span>
            </TooltipTrigger>
            <TooltipContent className="max-w-72">
              {deployed
                ? "Opens real connections between the deployed resources in your local sandbox and compares them with the analysis."
                : "Deploy first: the live test connects to the resources running in your sandbox."}
            </TooltipContent>
          </Tooltip>
          {sameEnds && <span className="text-xs text-muted-foreground">Pick two different ends.</span>}
        </div>
        {!hasInstance && summary && <TestInstanceHint subnets={summary.subnets} />}
      </div>

      <div className="flex flex-col gap-4 p-4">
        {liveRunning && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground" data-testid="live-running">
            <Spinner />
            Running real connections in your sandbox… this can take up to 30 seconds.
          </div>
        )}
        {check.isError && (
          <Alert variant="destructive">
            <CircleXIcon />
            <AlertTitle>Couldn't check reachability</AlertTitle>
            <AlertDescription>{errorMessage(check.error)}</AlertDescription>
          </Alert>
        )}
        {result && <ResultView result={result} />}
      </div>
    </div>
  )
}

function TestInstanceHint({ subnets }: { subnets: NetSubnet[] }) {
  const firstPublic = subnets.find((s) => s.classification === "public") ?? subnets[0]
  const [subnet, setSubnet] = useState(firstPublic?.address ?? "")
  const [adding, setAdding] = useState(false)
  const nodes = useWorkflowStore((s) => s.workflow?.nodes)
  const labels = useMemo(() => nodeLabelById(nodes), [nodes])
  if (subnets.length === 0) return null
  const selected = subnets.some((s) => s.address === subnet) ? subnet : (firstPublic?.address ?? "")
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-dashed p-3 text-xs" data-testid="test-instance-hint">
      <p>
        <FlaskConicalIcon className="mr-1 inline size-3.5 text-primary" />
        No instance to test with — add a test instance: a tiny web server on port 80, so you can see traffic really flow
        after deploying.
      </p>
      <div className="flex items-center gap-2">
        <Select value={selected} onValueChange={setSubnet}>
          <SelectTrigger size="sm" aria-label="Subnet for the test instance" className="min-w-0 flex-1">
            <SelectValue placeholder="Pick a subnet…" />
          </SelectTrigger>
          <SelectContent position="popper">
            {subnets.map((s) => (
              <SelectItem key={s.address} value={s.address}>
                {(s.nodeId && labels[s.nodeId]) || s.address}
                <span className="text-muted-foreground">({SUBNET_META[s.classification]?.label ?? "Unknown"})</span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          size="sm"
          variant="outline"
          disabled={!selected || adding}
          onClick={async () => {
            setAdding(true)
            const nodeId = await addTestInstance(selected)
            setAdding(false)
            if (nodeId) useReachability.setState({ source: INTERNET, destination: { kind: "node", nodeId } })
          }}
        >
          {adding ? <Spinner /> : <PlusIcon />}
          Add test instance
        </Button>
      </div>
    </div>
  )
}

function ResultView({ result }: { result: ReachabilityResult }) {
  const meta = VERDICT_META[result.verdict] ?? VERDICT_META.unknown
  const blockers = result.blockers ?? []
  const suggestions = result.suggestions ?? []
  return (
    <div className="flex flex-col gap-4" data-testid="reachability-result">
      <div
        role="status"
        data-testid="reachability-verdict"
        data-verdict={result.verdict}
        className={cn("flex flex-col gap-1.5 rounded-lg border p-3", meta.className)}
      >
        <div className="flex items-center gap-2">
          <meta.icon className="size-4 shrink-0" />
          <span className="font-heading text-sm font-semibold">{meta.title}</span>
          <span className="ml-auto">
            <BasisBadge basis={result.basis} />
          </span>
        </div>
        {result.summary && <p className="text-xs text-foreground/90">{result.summary}</p>}
      </div>

      {blockers.length > 0 && (
        <div className="flex flex-col gap-1.5" data-testid="reachability-blockers">
          <h4 className="flex items-center gap-1.5 text-xs font-semibold text-destructive">
            <ShieldXIcon className="size-3.5" /> What blocks it
          </h4>
          <ul className="flex flex-col gap-1 text-xs">
            {blockers.map((b, i) => (
              <li key={i} className="rounded-md bg-destructive/8 px-2 py-1.5 font-medium">
                {b}
              </li>
            ))}
          </ul>
        </div>
      )}
      {suggestions.length > 0 && (
        <div className="flex flex-col gap-1.5" data-testid="reachability-suggestions">
          <h4 className="flex items-center gap-1.5 text-xs font-semibold">
            <LightbulbIcon className="size-3.5 text-amber-500" /> How to fix it
          </h4>
          <ul className="flex flex-col gap-1 text-xs">
            {suggestions.map((s, i) => (
              <li key={i} className="rounded-md bg-amber-500/10 px-2 py-1.5">
                <span className="font-medium">Fix: </span>
                {s}
              </li>
            ))}
          </ul>
        </div>
      )}

      {(result.path?.length ?? 0) > 0 && (
        <div className="flex flex-col gap-1.5">
          <h4 className="flex items-center gap-1.5 text-xs font-semibold">
            <RouteIcon className="size-3.5" /> Path, hop by hop
            <TermHint text="Each step a packet goes through, in order. ✓ lets it pass, ✗ stops it, ? means Nimbo can't tell (a value only known after deploying)." />
          </h4>
          <ol className="flex flex-col" data-testid="reachability-path">
            {result.path.map((hop, i) => (
              <HopRow key={i} hop={hop} last={i === result.path.length - 1} />
            ))}
          </ol>
        </div>
      )}

      {result.live && <LiveView live={result.live} />}
    </div>
  )
}

function HopRow({ hop, last }: { hop: ReachabilityHop; last: boolean }) {
  const meta = HOP_META[hop.verdict] ?? HOP_META.unknown
  const select = useWorkflowStore((s) => s.select)
  return (
    <li className="flex gap-2" data-testid="reachability-hop" data-verdict={hop.verdict} data-layer={hop.layer}>
      <div className="flex flex-col items-center">
        <span
          aria-label={meta.label}
          className={cn("flex size-5 shrink-0 items-center justify-center rounded-full border text-xs font-bold", meta.className)}
        >
          {meta.mark}
        </span>
        {!last && <ArrowDownIcon className="my-0.5 size-3 text-muted-foreground/60" />}
      </div>
      <div className="min-w-0 flex-1 pb-2 text-xs">
        <div className="flex flex-wrap items-baseline gap-x-2">
          {hop.nodeId ? (
            <button type="button" className="font-medium hover:underline" onClick={() => select(hop.nodeId!)}>
              {hop.component}
            </button>
          ) : (
            <span className="font-medium">{hop.component}</span>
          )}
          <span className="text-[10px] tracking-wide text-muted-foreground uppercase">{hop.layer}</span>
        </div>
        <p className={cn("mt-0.5", hop.verdict === "deny" ? "font-medium text-destructive" : "text-muted-foreground")}>
          {hop.reason}
        </p>
        {hop.enforcement === "live" && (
          <span className="mt-1 inline-flex items-center gap-1 rounded-full bg-emerald-500/12 px-1.5 text-[10px] font-medium text-emerald-700 dark:text-emerald-300">
            <CheckIcon className="size-3" /> Verified live
          </span>
        )}
        {hop.enforcement === "analysis" && (
          <span className="mt-1 inline-flex items-center gap-1 rounded-full bg-muted px-1.5 text-[10px] font-medium text-muted-foreground">
            Checked by analysis — the local emulator doesn't enforce this; AWS would
          </span>
        )}
      </div>
    </li>
  )
}

function LiveView({ live }: { live: LiveCheck }) {
  return (
    <div className="flex flex-col gap-2" data-testid="live-results">
      <h4 className="flex items-center gap-1.5 text-xs font-semibold">
        <RadioIcon className="size-3.5 text-sky-500" /> Live test
      </h4>
      {live.unavailable && (
        <Alert>
          <CircleHelpIcon />
          <AlertTitle>The live test couldn't run</AlertTitle>
          <AlertDescription>{live.unavailable}</AlertDescription>
        </Alert>
      )}
      {(live.probes ?? []).map((p, i) => (
        <div key={i} className="flex flex-col gap-1.5 rounded-lg border p-2.5 text-xs" data-testid="live-probe">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{p.name}</span>
            <span
              title="Compared with what the analysis expects from the local emulator, which only enforces some layers (see below)."
              className={cn(
                "ml-auto inline-flex h-5 items-center rounded-full px-2 text-[11px] font-medium",
                p.matches
                  ? "bg-emerald-500/12 text-emerald-700 dark:text-emerald-300"
                  : "bg-amber-500/15 text-amber-800 dark:text-amber-200",
              )}
            >
              {p.matches ? "As predicted" : "Not as predicted"}
            </span>
          </div>
          <div className="flex items-start gap-1 rounded-md bg-muted px-2 py-1">
            <code className="min-w-0 flex-1 font-mono text-[11px] break-all whitespace-pre-wrap">{p.command}</code>
            <CopyButton value={p.command} label="Copy command" what="Command copied" />
          </div>
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground">
            <span>
              Connected:{" "}
              <span className={cn("font-medium", p.connected ? "text-emerald-600 dark:text-emerald-400" : "text-destructive")}>
                {p.connected ? "Yes" : "No"}
              </span>
            </span>
            <span>
              Result: <span className="font-medium text-foreground">{p.result}</span>
            </span>
            <span className="tabular-nums">{Math.round(p.latencyMs)} ms</span>
            {!p.matches && <span>The analysis expected {p.predicted ? "a connection" : "no connection"}.</span>}
          </div>
          {p.output && (
            <pre className="max-h-32 overflow-auto rounded-md bg-muted/60 px-2 py-1 font-mono text-[11px] whitespace-pre-wrap">
              {p.output}
            </pre>
          )}
        </div>
      ))}
      {(live.verified?.length ?? 0) > 0 && (
        <p className="text-xs">
          <span className="font-medium">Verified for real: </span>
          {live.verified.join(", ")}
        </p>
      )}
      {(live.analysisOnly?.length ?? 0) > 0 && (
        <p className="text-xs text-muted-foreground">
          <span className="font-medium">Checked by analysis only </span>(the local emulator doesn't enforce these; AWS would):{" "}
          {live.analysisOnly.join(", ")}
        </p>
      )}
      {live.note && <p className="text-xs text-muted-foreground">{live.note}</p>}
      {!live.unavailable && (live.probes ?? []).length === 0 && (
        <p className="flex items-center gap-1 text-xs text-muted-foreground">
          <XIcon className="size-3.5" /> No connection could be tried for this pair.
        </p>
      )}
    </div>
  )
}

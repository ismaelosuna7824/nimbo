import { useEffect, useMemo, useRef, useState } from "react"
import { Cable, CircleAlert, Play, Plug, RefreshCw, Rocket } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { errorMessage } from "@/components/errors"
import type { ResourceAccess } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { runAction, useRunBusy } from "@/features/toolbar/actions"
import { runningServices } from "@/features/docker/deploy/compose-model"
import { KubeAccessIcon } from "@/features/kubernetes/deploy/kube-access-icon"
import { kubeChipSummary } from "@/features/kubernetes/deploy/kube-model"
import { ConnectCard, ContainersCard, KubeClusterCard, PostmanCard } from "./connect-card"
import { DOCKER_KIND_GROUPS, KIND_GROUPS, KIND_ICON, KUBE_KIND_GROUPS, type KindGroup } from "./model"
import { ResourceCard } from "./resource-card"
import { SUPPORTING_SECTION_ID, SupportingSection } from "./supporting-section"
import { cardDomId, tryTarget, useEndpoints, useEndpointsFocus } from "./use-endpoints"

/**
 * Scrolls the list box so the element with domId sits at its top. Stacked (narrow panel) the list doesn't scroll on
 * its own: the whole tab, its parent, does.
 */
function scrollWithin(list: HTMLElement | null, domId: string) {
  const el = document.getElementById(domId)
  const box = list && list.scrollHeight <= list.clientHeight ? list.parentElement : list
  if (!box || !el) return
  const top = box.scrollTop + el.getBoundingClientRect().top - box.getBoundingClientRect().top - 8
  box.scrollTo({ top, behavior: "smooth" })
}

/**
 * How to use what is deployed: the sandbox connection details (the containers
 * in a Docker workspace), then one card per resource (addresses, snippets,
 * "Try it"), grouped by kind.
 */
export function EndpointsTab() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const kind = useWorkflowStore((s) => s.workflow!.kind)
  const docker = kind === "docker"
  const kube = kind === "kubernetes"
  const query = useEndpoints()
  const info = query.data
  const resources = useMemo(() => info?.resources ?? [], [info])
  const supporting = useMemo(() => resources.filter((r) => r.supporting), [resources])
  const groups = useMemo(
    () =>
      ((kube ? KUBE_KIND_GROUPS : docker ? DOCKER_KIND_GROUPS : KIND_GROUPS) as (KindGroup & {
        match?: (r: ResourceAccess) => boolean
      })[])
        .map((g) => ({
          ...g,
          items: resources.filter((r) => !r.supporting && (g.match ? g.match(r) : g.kinds.includes(r.kind))),
        }))
        .filter((g) => g.items.length > 0),
    [resources, docker, kube],
  )
  const [tryOpen, setTryOpen] = useState<Record<string, boolean>>({})
  const [supportingOpen, setSupportingOpen] = useState(false)
  const [highlight, setHighlight] = useState<string | null>(null)
  const focusSeq = useEndpointsFocus((s) => s.seq)
  const scroller = useRef<HTMLDivElement>(null)

  // Scroll to the resource another view asked for (canvas, inspector, toast)
  // once its card is rendered (next frame: the tab may just have been shown;
  // a supporting row first needs its section expanded). Only our own list
  // scrolls, never the app layout.
  useEffect(() => {
    const { address, tryIt } = useEndpointsFocus.getState()
    const target = address ? resources.find((r) => r.address === address) : undefined
    if (!address || !target) return
    useEndpointsFocus.setState({ address: null })
    requestAnimationFrame(() => {
      if (target.supporting) setSupportingOpen(true)
      if (tryIt) setTryOpen((s) => ({ ...s, [address]: true }))
      setHighlight(address)
      // The expanded section renders before the frame after next.
      requestAnimationFrame(() => requestAnimationFrame(() => scrollWithin(scroller.current, cardDomId(address))))
    })
  }, [focusSeq, resources])

  useEffect(() => {
    if (!highlight) return
    const timer = window.setTimeout(() => setHighlight(null), 1800)
    return () => window.clearTimeout(timer)
  }, [highlight])

  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  }

  if (query.isError) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 text-xs">
        <p className="flex items-center gap-1.5 text-destructive">
          <CircleAlert className="size-3.5" /> Couldn't load the endpoints: {errorMessage(query.error)}
        </p>
        <Button size="xs" variant="outline" onClick={() => void query.refetch()}>
          <RefreshCw /> Retry
        </Button>
      </div>
    )
  }

  return (
    // Narrow bottom panels (< 48rem, the panel is the container) stack the connection cards above the resources and
    // scroll as one page (the "On this page" index is left out); wide ones put them side by side.
    <div className="grid h-full min-h-0 grid-rows-[auto_1fr] overflow-y-auto @3xl:grid-cols-[minmax(16rem,20rem)_minmax(0,1fr)] @3xl:grid-rows-1 @3xl:overflow-hidden">
      <aside className="flex flex-col gap-2 border-b p-2 @3xl:min-h-0 @3xl:overflow-auto @3xl:border-r @3xl:border-b-0">
        {kube ? <KubeClusterCard /> : docker ? <ContainersCard /> : <ConnectCard sandbox={info?.sandbox ?? []} />}
        {resources.length > 0 && <PostmanCard />}
        {(groups.length > 0 || supporting.length > 0) && (
          <nav aria-label="Deployed resources" className="hidden flex-col gap-2 rounded-lg border p-2.5 @3xl:flex">
            <h3 className="text-xs font-semibold">On this page</h3>
            {groups.map((g) => (
              <div key={g.id} className="flex flex-col gap-0.5">
                <span className="text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">{g.title}</span>
                {g.items.map((r) => (
                  <IndexLink key={r.address} resource={r} kube={kube} />
                ))}
              </div>
            ))}
            {supporting.length > 0 && (
              <button
                type="button"
                className="flex min-w-0 items-center gap-1.5 rounded px-1 py-0.5 text-left text-xs text-muted-foreground hover:bg-muted"
                onClick={() => {
                  setSupportingOpen(true)
                  requestAnimationFrame(() =>
                    requestAnimationFrame(() => scrollWithin(scroller.current, SUPPORTING_SECTION_ID)),
                  )
                }}
              >
                <Cable className="size-3 shrink-0" />
                <span className="truncate">Supporting resources ({supporting.length})</span>
              </button>
            )}
          </nav>
        )}
      </aside>

      <div ref={scroller} className="min-w-0 p-3 @3xl:min-h-0 @3xl:overflow-auto">
        {resources.length === 0 ? (
          kube ? <NothingForwarded /> : docker ? <NothingPublished /> : <NothingDeployed />
        ) : (
          <div className="mx-auto flex max-w-5xl flex-col gap-5">
            {groups.map((g) => (
              <section key={g.id} className="flex flex-col gap-2" aria-labelledby={`endpoints-group-${g.id}`}>
                <div className="flex items-baseline gap-2">
                  <g.icon className="size-3.5 self-center text-muted-foreground" />
                  <h3 id={`endpoints-group-${g.id}`} className="text-sm font-semibold">
                    {g.title}
                  </h3>
                  <span className="text-xs text-muted-foreground">{g.blurb}</span>
                </div>
                {g.items.map((r) => (
                  <ResourceCard
                    // A redeploy that changes the address starts a fresh "Try it" request.
                    key={`${r.address}|${r.endpoints?.[0]?.url ?? ""}`}
                    resource={r}
                    workflowId={workflowId}
                    target={tryTarget(r, resources)}
                    highlighted={highlight === r.address}
                    tryOpen={tryOpen[r.address] ?? false}
                    setTryOpen={(open) => setTryOpen((s) => ({ ...s, [r.address]: open }))}
                    media={kube && r.nodeId ? <KubeAccessIcon nodeId={r.nodeId} /> : undefined}
                  />
                ))}
              </section>
            ))}
            {supporting.length > 0 && (
              <SupportingSection
                resources={supporting}
                open={supportingOpen}
                onOpenChange={setSupportingOpen}
                highlight={highlight}
              />
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function IndexLink({ resource: r, kube }: { resource: ResourceAccess; kube: boolean }) {
  const Icon = KIND_ICON[r.kind] ?? KIND_ICON.generic
  return (
    <button
      type="button"
      className={cn(
        "flex min-w-0 items-center gap-1.5 rounded px-1 py-0.5 text-left text-xs hover:bg-muted",
        r.warning && "text-muted-foreground",
      )}
      onClick={() => useEndpointsFocus.getState().focus(r.address)}
    >
      {kube && r.nodeId ? (
        <KubeAccessIcon nodeId={r.nodeId} size={12} />
      ) : (
        <Icon className="size-3 shrink-0 text-muted-foreground" />
      )}
      <span className="truncate">{r.label}</span>
    </button>
  )
}

function NothingDeployed() {
  const busy = useRunBusy()
  const lastApply = useWorkflowStore((s) => s.runs.find((r) => r.action === "apply"))
  const failed = !busy && lastApply?.status === "failed"
  return (
    <Empty className="h-full">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <Plug />
        </EmptyMedia>
        <EmptyTitle>{busy ? "Deploying…" : failed ? "The last deploy failed" : "Nothing deployed yet"}</EmptyTitle>
        <EmptyDescription>
          After you deploy, every API, queue, bucket and table gets an address you can use from your browser, curl,
          Postman or code. Deploy to see them here.
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent className="flex-row justify-center">
        <Button size="sm" disabled={busy} onClick={() => void runAction("apply", { showLog: false })}>
          {busy ? <Spinner /> : <Rocket />} {failed ? "Retry deploy" : "Deploy"}
        </Button>
        {(busy || failed) && (
          <Button size="sm" variant="outline" onClick={() => useWorkflowStore.getState().showBottom("deploy")}>
            View logs
          </Button>
        )}
      </EmptyContent>
    </Empty>
  )
}

/** Kubernetes workspace: no Service is forwarded yet (cluster down, nothing deployed, or no Service declares a port). */
function NothingForwarded() {
  const busy = useRunBusy()
  const summary = kubeChipSummary(useWorkflowStore((s) => s.kube))
  const lastUp = useWorkflowStore((s) => s.runs.find((r) => r.action === "up"))
  const failed = !busy && lastUp?.status === "failed"
  const running = summary.cluster === "running" && summary.running > 0
  return (
    <Empty className="h-full" data-testid="kube-endpoints-empty">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <Plug />
        </EmptyMedia>
        <EmptyTitle>
          {busy
            ? "Deploying to your cluster…"
            : failed
              ? "The last Deploy failed"
              : running
                ? "No Service exposes a port"
                : summary.cluster === "running"
                  ? "Your workloads are not running"
                  : "Your cluster is not running"}
        </EmptyTitle>
        <EmptyDescription>
          {running && !busy
            ? "Pods are reachable from this machine through Services (forwarded to 127.0.0.1) and Ingress rules. Add a Service for a workload on the canvas and Deploy again."
            : "After Deploy, every Service port is forwarded to this machine and every Ingress rule gets a URL you can use from your browser, curl, Postman or a database client."}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent className="flex-row justify-center">
        <Button size="sm" disabled={busy} onClick={() => void runAction("up", { showLog: false })}>
          {busy ? <Spinner /> : <Rocket />} {failed ? "Retry Deploy" : running ? "Deploy again" : "Deploy"}
        </Button>
        {(busy || failed) && (
          <Button size="sm" variant="outline" onClick={() => useWorkflowStore.getState().showBottom("deploy")}>
            View logs
          </Button>
        )}
      </EmptyContent>
    </Empty>
  )
}

/** Docker workspace: no service publishes a port yet (containers down, or none declares ports:). */
function NothingPublished() {
  const busy = useRunBusy()
  const running = useWorkflowStore((s) => runningServices(s.compose))
  const lastUp = useWorkflowStore((s) => s.runs.find((r) => r.action === "up"))
  const failed = !busy && lastUp?.status === "failed"
  return (
    <Empty className="h-full">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <Plug />
        </EmptyMedia>
        <EmptyTitle>
          {busy
            ? "Starting your containers…"
            : failed
              ? "The last Up failed"
              : running > 0
                ? "No service publishes a port"
                : "Your containers are not running"}
        </EmptyTitle>
        <EmptyDescription>
          {running > 0 && !busy
            ? "Services are reachable from this machine only through published ports. Add one to a service (ports: \"8080:80\") on the canvas or in compose.yaml and press Up again."
            : "After docker compose up, every service that publishes a port gets an address you can use from your browser, curl, Postman or a database client."}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent className="flex-row justify-center">
        <Button size="sm" disabled={busy} onClick={() => void runAction("up", { showLog: false })}>
          {busy ? <Spinner /> : <Play />} {failed ? "Retry Up" : running > 0 ? "Up again" : "Start containers"}
        </Button>
        {(busy || failed) && (
          <Button size="sm" variant="outline" onClick={() => useWorkflowStore.getState().showBottom("deploy")}>
            View logs
          </Button>
        )}
      </EmptyContent>
    </Empty>
  )
}

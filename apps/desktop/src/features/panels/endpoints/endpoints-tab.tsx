import { useEffect, useMemo, useRef, useState } from "react"
import { Cable, CircleAlert, Plug, RefreshCw, Rocket } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { errorMessage } from "@/components/errors"
import type { ResourceAccess } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { runAction, useRunBusy } from "@/features/toolbar/actions"
import { ConnectCard, PostmanCard } from "./connect-card"
import { KIND_GROUPS, KIND_ICON } from "./model"
import { ResourceCard } from "./resource-card"
import { SUPPORTING_SECTION_ID, SupportingSection } from "./supporting-section"
import { cardDomId, tryTarget, useEndpoints, useEndpointsFocus } from "./use-endpoints"

/** Scrolls the list box so the element with domId sits at its top. */
function scrollWithin(box: HTMLElement | null, domId: string) {
  const el = document.getElementById(domId)
  if (!box || !el) return
  const top = box.scrollTop + el.getBoundingClientRect().top - box.getBoundingClientRect().top - 8
  box.scrollTo({ top, behavior: "smooth" })
}

/**
 * How to use what is deployed: the sandbox connection details, then one card
 * per resource (addresses, snippets, "Try it"), grouped by kind.
 */
export function EndpointsTab() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const query = useEndpoints()
  const info = query.data
  const resources = useMemo(() => info?.resources ?? [], [info])
  const supporting = useMemo(() => resources.filter((r) => r.supporting), [resources])
  const groups = useMemo(
    () =>
      KIND_GROUPS.map((g) => ({
        ...g,
        items: resources.filter((r) => !r.supporting && g.kinds.includes(r.kind)),
      })).filter((g) => g.items.length > 0),
    [resources],
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
    <div className="grid h-full min-h-0 grid-cols-[minmax(16rem,20rem)_minmax(0,1fr)]">
      <aside className="flex min-h-0 flex-col gap-2 overflow-auto border-r p-2">
        <ConnectCard sandbox={info?.sandbox ?? []} />
        {resources.length > 0 && <PostmanCard />}
        {(groups.length > 0 || supporting.length > 0) && (
          <nav aria-label="Deployed resources" className="flex flex-col gap-2 rounded-lg border p-2.5">
            <h3 className="text-xs font-semibold">On this page</h3>
            {groups.map((g) => (
              <div key={g.id} className="flex flex-col gap-0.5">
                <span className="text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">{g.title}</span>
                {g.items.map((r) => (
                  <IndexLink key={r.address} resource={r} />
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

      <div ref={scroller} className="min-h-0 overflow-auto p-3">
        {resources.length === 0 ? (
          <NothingDeployed />
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

function IndexLink({ resource: r }: { resource: ResourceAccess }) {
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
      <Icon className="size-3 shrink-0 text-muted-foreground" />
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

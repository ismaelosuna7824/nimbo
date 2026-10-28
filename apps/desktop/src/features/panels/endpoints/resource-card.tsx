import { useState, type ReactNode } from "react"
import { ChevronRight, ExternalLink, FlaskConical, Pencil, TriangleAlert } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { openExternal } from "@/components/open-external"
import type { HTTPEndpoint, ResourceAccess, Snippet } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { CopyButton, FieldRow, MethodBadge } from "./bits"
import { HighlightedCode } from "./code"
import { httpDraftFor, KIND_ICON, type HttpDraft } from "./model"
import { TryIt } from "./try-it"
import { cardDomId, openEndpoints } from "./use-endpoints"

/**
 * Splits a greedy route's sample URL into the fixed part and the sample path
 * the user may edit: after the last occurrence of the route's literal prefix
 * ("/orders/" for "/orders/{proxy+}", "/" for "/{proxy+}").
 */
function splitProxyUrl(e: HTTPEndpoint): { base: string; sample: string } {
  const brace = e.path.indexOf("{")
  const prefix = brace >= 0 ? e.path.slice(0, brace) || "/" : "/"
  const at = e.url.lastIndexOf(prefix)
  const cut = at >= 0 ? at + prefix.length : e.url.lastIndexOf("/") + 1
  return { base: e.url.slice(0, cut), sample: e.url.slice(cut) }
}

export function ResourceCard({
  resource: r,
  workflowId,
  target,
  highlighted,
  tryOpen,
  setTryOpen,
  media,
}: {
  resource: ResourceAccess
  workflowId: string
  /** TryRequest.nodeId for this resource. */
  target: string
  highlighted?: boolean
  tryOpen: boolean
  setTryOpen: (open: boolean) => void
  /** Replaces the kind icon (Kubernetes: the backing workload's technology). */
  media?: ReactNode
}) {
  const Icon = KIND_ICON[r.kind] ?? KIND_ICON.generic
  const endpoints = r.endpoints ?? []
  const httpAction = r.actions.find((a) => a.input === "http")
  const [http, setHttp] = useState<HttpDraft>(() =>
    httpDraftFor(endpoints[0]?.method ?? "GET", endpoints[0]?.url ?? "", httpAction?.default),
  )

  const tryEndpoint = (method: string, url: string) => {
    setHttp((d) => ({ ...httpDraftFor(method, url, d.body || httpAction?.default), headers: d.headers }))
    setTryOpen(true)
  }

  return (
    <article
      id={cardDomId(r.address)}
      className={cn(
        "@container flex scroll-mt-2 flex-col gap-3 rounded-lg border bg-card p-3 transition-shadow",
        highlighted && "ring-2 ring-primary",
      )}
    >
      <header className="flex items-start gap-2.5">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          {media ?? <Icon className="size-4" />}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2">
            <h4 className="truncate text-sm font-semibold">{r.label}</h4>
            <span className="truncate font-mono text-[11px] text-muted-foreground" title={r.address}>
              {r.type}
            </span>
          </div>
          <p className="text-xs text-muted-foreground">{r.summary}</p>
        </div>
      </header>

      {r.warning && (
        <div className="flex gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs text-amber-800 dark:text-amber-300">
          <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
          <p>{r.warning}</p>
        </div>
      )}

      {r.fields.length > 0 && (
        <div className="grid gap-1 @3xl:grid-cols-2">
          {r.fields.map((f) => (
            <FieldRow key={f.label} field={f} />
          ))}
        </div>
      )}

      {endpoints.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="text-[11px] font-medium text-muted-foreground">
            {endpoints.length === 1 ? "Address" : `Addresses (${endpoints.length} routes)`}
          </span>
          <div className="divide-y rounded-md border">
            {endpoints.map((e) => (
              <EndpointRow key={`${e.method} ${e.path}`} endpoint={e} onTry={httpAction ? tryEndpoint : undefined} />
            ))}
          </div>
        </div>
      )}

      {r.snippets.length > 0 && <Snippets snippets={r.snippets} />}

      {r.actions.length > 0 && (
        <Collapsible open={tryOpen} onOpenChange={setTryOpen} className="rounded-md border border-primary/30 bg-primary/[0.03]">
          <CollapsibleTrigger className="group/try flex w-full items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium">
            <ChevronRight className="size-3.5 transition-transform group-data-[state=open]/try:rotate-90" />
            <FlaskConical className="size-3.5 text-primary" />
            Try it
            <span className="font-normal text-muted-foreground">— right here, no extra tools needed</span>
          </CollapsibleTrigger>
          <CollapsibleContent className="border-t p-2.5">
            <TryIt
              workflowId={workflowId}
              target={target}
              actions={r.actions}
              http={http}
              setHttp={setHttp}
              startsFunction={r.kind === "function" || r.kind === "function-url" || r.kind === "http-api"}
            />
          </CollapsibleContent>
        </Collapsible>
      )}
    </article>
  )
}

function EndpointRow({ endpoint: e, onTry }: { endpoint: HTTPEndpoint; onTry?: (method: string, url: string) => void }) {
  const proxy = e.proxy ? splitProxyUrl(e) : null
  const [sample, setSample] = useState(proxy?.sample ?? "")
  const url = proxy ? proxy.base + sample : e.url
  const browsable = e.method === "GET" || e.method === "ANY"
  return (
    <div className="flex min-w-0 items-center gap-2 px-2 py-1">
      <MethodBadge method={e.method} />
      <div className="min-w-0 flex-1">
        {proxy ? (
          <div className="flex min-w-0 items-center font-mono text-xs">
            <span className="truncate" title={proxy.base}>
              {proxy.base}
            </span>
            <Tooltip>
              <TooltipTrigger asChild>
                <label className="ml-0.5 inline-flex shrink-0 items-center gap-0.5 rounded bg-primary/10 px-1 text-primary focus-within:ring-2 focus-within:ring-ring/50">
                  <input
                    value={sample}
                    onChange={(ev) => setSample(ev.target.value)}
                    size={Math.max(sample.length, 4)}
                    spellCheck={false}
                    aria-label={`Path for ${e.method} ${e.path}`}
                    className="bg-transparent outline-none"
                  />
                  <Pencil className="size-2.5" />
                </label>
              </TooltipTrigger>
              <TooltipContent className="max-w-64">
                This route ({e.path}) accepts any path. Type the one you want to call.
              </TooltipContent>
            </Tooltip>
          </div>
        ) : (
          <span className="block truncate font-mono text-xs select-all" title={url}>
            {url}
          </span>
        )}
        {e.description && <span className="block truncate text-[11px] text-muted-foreground">{e.description}</span>}
      </div>
      <CopyButton value={url} label="Copy URL" what="URL copied" />
      {browsable && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-xs"
              className="text-muted-foreground hover:text-foreground"
              aria-label="Open in browser"
              onClick={() => void openExternal(url)}
            >
              <ExternalLink />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Open in your browser</TooltipContent>
        </Tooltip>
      )}
      {onTry && (
        <Button variant="outline" size="xs" onClick={() => onTry(e.method, url)}>
          <FlaskConical /> Try
        </Button>
      )}
    </div>
  )
}

function Snippets({ snippets }: { snippets: Snippet[] }) {
  const [tab, setTab] = useState(snippets[0].label)
  const current = snippets.find((s) => s.label === tab) ?? snippets[0]
  return (
    <Tabs value={current.label} onValueChange={setTab} className="gap-0 rounded-md border">
      <div className="flex items-center border-b pr-1">
        <TabsList variant="line" className="h-7 justify-start px-1">
          {snippets.map((s) => (
            <TabsTrigger key={s.label} value={s.label} className="flex-none px-2 text-[11px]">
              {s.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <CopyButton value={current.code} label={`Copy ${current.label} snippet`} what="Snippet copied" className="ml-auto" />
      </div>
      {snippets.map((s) => (
        <TabsContent key={s.label} value={s.label}>
          <HighlightedCode code={s.code} language={s.language} className="max-h-56 p-2" />
        </TabsContent>
      ))}
    </Tabs>
  )
}

/**
 * Inspector version: identifiers, the first address and snippet, and a link
 * to the full card (with "Try it") in the Endpoints tab. `extra` sits right
 * after the identifiers (the inspector's other live values).
 */
export function CompactResourceCard({ resource: r, extra }: { resource: ResourceAccess; extra?: ReactNode }) {
  const endpoint = r.endpoints?.[0]
  const snippet = r.snippets[0]
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-muted-foreground">{r.summary}</p>
      {r.warning && (
        <div className="flex gap-1.5 rounded-md border border-amber-500/40 bg-amber-500/10 p-1.5 text-[11px] text-amber-800 dark:text-amber-300">
          <TriangleAlert className="mt-0.5 size-3 shrink-0" />
          <p>{r.warning}</p>
        </div>
      )}
      {endpoint && (
        <div className="flex min-w-0 items-center gap-1.5 rounded-md bg-muted/50 py-0.5 pr-0.5 pl-1.5">
          <MethodBadge method={endpoint.method} />
          <span className="min-w-0 flex-1 truncate font-mono text-xs" title={endpoint.url}>
            {endpoint.url}
          </span>
          <CopyButton value={endpoint.url} label="Copy URL" what="URL copied" />
        </div>
      )}
      {r.fields.map((f) => (
        <FieldRow key={f.label} field={f} compact />
      ))}
      {extra}
      {snippet && (
        <div className="relative rounded-md border">
          <div className="flex items-center border-b px-2 py-0.5 text-[11px] text-muted-foreground">
            {snippet.label}
            <CopyButton value={snippet.code} label={`Copy ${snippet.label} snippet`} what="Snippet copied" className="ml-auto" />
          </div>
          <HighlightedCode code={snippet.code} language={snippet.language} className="max-h-40 p-2" />
        </div>
      )}
      <div className="flex gap-1.5">
        {r.actions.length > 0 && (
          <Button size="xs" onClick={() => openEndpoints(r.address, { tryIt: true })}>
            <FlaskConical /> Try it
          </Button>
        )}
        <Button size="xs" variant="outline" onClick={() => openEndpoints(r.address)}>
          {r.endpoints?.length || r.snippets.length > 1 ? "All addresses & snippets" : "Open in Endpoints"}
        </Button>
      </div>
    </div>
  )
}

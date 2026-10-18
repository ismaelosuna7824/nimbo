import { useQuery } from "@tanstack/react-query"
import { CloudOffIcon, SearchIcon, StarIcon, XIcon, ZapIcon } from "lucide-react"
import { useEffect, useState } from "react"
import { CLOUD_META } from "@/components/cloud-meta"
import { useSyncedState } from "@/components/use-synced-state"
import { ResourceIcon } from "@/components/resource-icon"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api, type CatalogQuery } from "@/lib/api/client"
import { ALL_CLOUDS, type Cloud, type ResourceSummary } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { addResource, RESOURCE_DRAG_TYPE } from "../canvas/actions"

const CLOUD_TAB_LABEL: Record<Cloud, string> = { aws: "AWS", azure: "Azure", gcp: "GCP", oci: "OCI" }

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = window.setTimeout(() => setV(value), ms)
    return () => window.clearTimeout(t)
  }, [value, ms])
  return v
}

function useResources(q: CatalogQuery, enabled = true) {
  return useQuery({
    queryKey: ["resources", q],
    queryFn: () => api.resources(q),
    staleTime: 5 * 60_000,
    enabled,
  })
}

export function Palette() {
  const mode = useWorkflowStore((s) => s.mode)
  const workflowClouds = useWorkflowStore((s) => s.workflow?.clouds)
  const [cloud, setCloud] = useState<Cloud>(workflowClouds?.[0] ?? "aws")
  const [supportedOnly, setSupportedOnly] = useSyncedState(mode === "guided")
  const [text, setText] = useState("")
  const q = useDebounced(text.trim(), 200)

  const providers = useQuery({ queryKey: ["providers"], queryFn: api.providers, staleTime: Infinity })
  const provider = providers.data?.find((p) => p.cloud === cloud)
  const supported = supportedOnly || undefined

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-col gap-2.5 px-3 pt-1 pb-2">
        <Tabs value={cloud} onValueChange={(v) => setCloud(v as Cloud)}>
          <TabsList className="w-full">
            {ALL_CLOUDS.map((c) => (
              <TabsTrigger key={c} value={c} className="gap-1 text-xs">
                <span className={cn("size-1.5 rounded-full", CLOUD_META[c].dot)} />
                {CLOUD_TAB_LABEL[c]}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <InputGroup>
          <InputGroupAddon>
            <SearchIcon />
          </InputGroupAddon>
          <InputGroupInput
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={`Search ${CLOUD_TAB_LABEL[cloud]} resources…`}
            aria-label="Search resources"
          />
          {text && (
            <InputGroupAddon align="inline-end">
              <InputGroupButton size="icon-xs" onClick={() => setText("")} aria-label="Clear search">
                <XIcon />
              </InputGroupButton>
            </InputGroupAddon>
          )}
        </InputGroup>
        <div className="flex items-center justify-between gap-2">
          <Label htmlFor="palette-supported" className="flex items-center gap-1.5 text-xs font-normal text-muted-foreground">
            <ZapIcon className="size-3.5 text-emerald-500" />
            Runs locally only
          </Label>
          <Switch id="palette-supported" size="sm" checked={supportedOnly} onCheckedChange={setSupportedOnly} />
        </div>
        {provider && (
          <p className="text-[11px] leading-snug text-muted-foreground">
            All {provider.resourceCount.toLocaleString()} {CLOUD_TAB_LABEL[cloud]} resources and{" "}
            {provider.dataCount.toLocaleString()} data sources are searchable
            {supportedOnly ? " (turn off the switch to include ones that are export only)" : ""}.
          </p>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-4">
        {q ? (
          <SearchResults query={{ q, cloud, supported, limit: 80 }} />
        ) : (
          <Browse cloud={cloud} supported={supported} />
        )}
      </div>
    </div>
  )
}

function SearchResults({ query }: { query: CatalogQuery }) {
  const res = useResources(query)
  if (res.isPending) return <ListSkeleton />
  if (res.isError) return <p className="px-2 py-4 text-sm text-destructive">Search failed: {res.error.message}</p>
  if (res.data.length === 0)
    return (
      <p className="px-2 py-6 text-center text-sm text-muted-foreground">
        No matches.{query.supported ? " Try turning off “Runs locally only”." : ""}
      </p>
    )
  return <ResourceList items={res.data} />
}

function Browse({ cloud, supported }: { cloud: Cloud; supported?: boolean }) {
  const popular = useResources({ cloud, popular: true, supported })
  const categories = useQuery({
    queryKey: ["categories", cloud],
    queryFn: () => api.categories(cloud),
    staleTime: Infinity,
  })
  return (
    <div className="flex flex-col gap-3">
      <section>
        <h3 className="flex items-center gap-1.5 px-2 pb-1 text-xs font-medium text-muted-foreground">
          <StarIcon className="size-3.5" />
          Popular
        </h3>
        {popular.isPending ? (
          <ListSkeleton />
        ) : popular.data && popular.data.length > 0 ? (
          <ResourceList items={popular.data} />
        ) : (
          <p className="px-2 text-xs text-muted-foreground">No popular resources for this filter.</p>
        )}
      </section>
      <section>
        <h3 className="px-2 pb-1 text-xs font-medium text-muted-foreground">Browse by category</h3>
        <Accordion type="multiple" className="px-1">
          {(categories.data ?? []).map((c) => (
            <AccordionItem key={c} value={c} className="border-none">
              <AccordionTrigger className="px-1 py-1.5 text-sm hover:no-underline">{c}</AccordionTrigger>
              <AccordionContent className="pb-1">
                <CategoryList cloud={cloud} category={c} supported={supported} />
              </AccordionContent>
            </AccordionItem>
          ))}
        </Accordion>
      </section>
    </div>
  )
}

function CategoryList({ cloud, category, supported }: { cloud: Cloud; category: string; supported?: boolean }) {
  const res = useResources({ cloud, category, supported, limit: 300 })
  if (res.isPending) return <ListSkeleton rows={3} />
  if (!res.data?.length) return <p className="px-2 text-xs text-muted-foreground">Nothing here for this filter.</p>
  return <ResourceList items={res.data} />
}

function ListSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="flex flex-col gap-1.5 px-2">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  )
}

function ResourceList({ items }: { items: ResourceSummary[] }) {
  return (
    <ul className="flex flex-col gap-0.5">
      {items.map((r) => (
        <li key={`${r.kind}:${r.type}`}>
          <PaletteItem item={r} />
        </li>
      ))}
    </ul>
  )
}

function PaletteItem({ item }: { item: ResourceSummary }) {
  const accent = CLOUD_META[item.cloud].accent
  return (
    <Tooltip delayDuration={600}>
      <TooltipTrigger asChild>
        <button
          type="button"
          draggable
          onDragStart={(e) => {
            e.dataTransfer.setData(RESOURCE_DRAG_TYPE, JSON.stringify({ kind: item.kind, type: item.type }))
            e.dataTransfer.effectAllowed = "copy"
          }}
          onClick={() => void addResource(item.kind, item.type)}
          className="group flex w-full cursor-grab items-center gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-sidebar-accent active:cursor-grabbing"
        >
          <span
            className="flex size-7 shrink-0 items-center justify-center rounded-md"
            style={{ background: `${accent}1f`, color: accent }}
          >
            <ResourceIcon name={item.icon} className="size-4" />
          </span>
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-1.5">
              <span className="truncate text-sm">{item.label}</span>
              {item.kind === "data" && (
                <span className="rounded bg-sky-500/15 px-1 text-[9px] font-semibold tracking-wider text-sky-600 uppercase dark:text-sky-400">
                  Data
                </span>
              )}
            </span>
            <span className="block truncate font-mono text-[10.5px] text-muted-foreground">{item.type}</span>
          </span>
          {item.flociSupported ? (
            <ZapIcon className="size-3.5 shrink-0 text-emerald-500" aria-label="Runs locally" />
          ) : (
            <CloudOffIcon className="size-3.5 shrink-0 text-muted-foreground/60" aria-label="Export only" />
          )}
        </button>
      </TooltipTrigger>
      <TooltipContent side="right">
        {item.flociSupported ? "Runs in your local sandbox" : "Export only — not emulated locally"} · click to add or
        drag onto the canvas
      </TooltipContent>
    </Tooltip>
  )
}

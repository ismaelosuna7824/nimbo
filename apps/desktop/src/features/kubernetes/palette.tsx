import { useQuery } from "@tanstack/react-query"
import { ChevronRightIcon, SearchIcon, XIcon } from "lucide-react"
import { memo, useMemo, useState } from "react"
import { ThemedIcon } from "@/components/arcade/themed-icon"
import { K8sIcon } from "@/components/k8s-icons/k8s-icon"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { CATEGORY_LABEL } from "@/features/docker/palette"
import { api } from "@/lib/api/client"
import type { DockerImageCategory, KubeCatalogItem } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { addFromPalette, KUBE_DRAG_TYPE, type KubePaletteDrag } from "./actions"

const CHART_CATEGORY: Record<string, string> = {
  database: "Databases",
  cache: "Caches",
  messaging: "Messaging",
  ingress: "Ingress",
  security: "Security",
  observability: "Observability",
  gitops: "GitOps",
}

/** Text a search matches: label, description, image, chart, kinds and category. */
export function matchesItem(item: KubeCatalogItem, q: string): boolean {
  const hay = [
    item.label,
    item.description,
    item.image,
    item.chart && `${item.chart.name} ${item.chart.version}`,
    item.kinds.join(" "),
    item.category,
    CATEGORY_LABEL[item.category as DockerImageCategory],
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase()
  return q
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((w) => hay.includes(w))
}

/** The palette of a Kubernetes workspace: starters, workloads from container images and pinned Helm charts. */
export function KubernetesPalette() {
  const [q, setQ] = useState("")
  const catalog = useQuery({ queryKey: ["kubernetes-catalog"], queryFn: api.kubernetesCatalog, staleTime: Infinity })
  const query = q.trim()
  const groups = useMemo(() => {
    const items = catalog.data ?? []
    const images = new Map<string, KubeCatalogItem[]>()
    for (const i of items) if (i.group === "image") images.set(i.category, [...(images.get(i.category) ?? []), i])
    return { starters: items.filter((i) => i.group === "starter"), images, charts: items.filter((i) => i.group === "chart") }
  }, [catalog.data])
  const results = useMemo(() => (query ? (catalog.data ?? []).filter((i) => matchesItem(i, query)) : []), [catalog.data, query])
  const imageCategories = [...groups.images.keys()].sort(
    (a, b) => order(a) - order(b) || a.localeCompare(b),
  )

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="k8s-palette">
      <div className="flex flex-col gap-2.5 px-3 pt-1 pb-2">
        <Tabs value="kubernetes">
          <TabsList className="w-full">
            <TabsTrigger value="kubernetes" className="gap-1.5 text-xs">
              <TechIcon icon="kubernetes" size={14} />
              Kubernetes
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <InputGroup>
          <InputGroupAddon>
            <ThemedIcon icon={SearchIcon} pixel="search" />
          </InputGroupAddon>
          <InputGroupInput value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search objects, images, charts…" aria-label="Search the Kubernetes catalog" />
          {q && (
            <InputGroupAddon align="inline-end">
              <InputGroupButton size="icon-xs" onClick={() => setQ("")} aria-label="Clear search">
                <XIcon />
              </InputGroupButton>
            </InputGroupAddon>
          )}
        </InputGroup>
        <p className="text-[11px] leading-snug text-muted-foreground">
          Click or drag onto the canvas. Images come as a workload with its Service; charts are pinned and vendored.
        </p>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-4">
        {catalog.isPending ? (
          <ListSkeleton />
        ) : catalog.isError ? (
          <p className="px-2 text-xs text-destructive">Couldn't load the Kubernetes catalog: {catalog.error.message}</p>
        ) : query ? (
          results.length === 0 ? (
            <p className="px-2 py-6 text-center text-sm text-muted-foreground">Nothing matches.</p>
          ) : (
            <ul className="flex flex-col gap-0.5">
              {results.map((i) => (
                <li key={i.id}>
                  <PaletteItem item={i} />
                </li>
              ))}
            </ul>
          )
        ) : (
          <div className="flex flex-col gap-3">
            <section>
              <h3 className="flex items-center gap-1.5 px-2 pb-1 text-xs font-medium text-muted-foreground">
                <TechIcon icon="kubernetes" size={14} />
                Starters
              </h3>
              <ul className="flex flex-col gap-0.5">
                {groups.starters.map((i) => (
                  <li key={i.id}>
                    <PaletteItem item={i} />
                  </li>
                ))}
              </ul>
            </section>
            {groups.images.size > 0 && (
              <section>
                <h3 className="px-2 pb-1 text-xs font-medium text-muted-foreground">From an image</h3>
                <Accordion type="multiple" className="px-1" defaultValue={["web", "language", "database"]}>
                  {imageCategories.map((c) => (
                    <AccordionItem key={c} value={c} className="border-none">
                      <AccordionTrigger className="px-1 py-1.5 text-sm hover:no-underline">
                        <span className="flex items-center gap-2">
                          {CATEGORY_LABEL[c as DockerImageCategory] ?? c}
                          <span className="text-xs text-muted-foreground">{groups.images.get(c)!.length}</span>
                        </span>
                      </AccordionTrigger>
                      <AccordionContent className="pb-1">
                        <ul className="flex flex-col gap-0.5">
                          {groups.images.get(c)!.map((i) => (
                            <li key={i.id}>
                              <PaletteItem item={i} />
                            </li>
                          ))}
                        </ul>
                      </AccordionContent>
                    </AccordionItem>
                  ))}
                </Accordion>
              </section>
            )}
            {groups.charts.length > 0 && (
              <section>
                <h3 className="flex items-center gap-1.5 px-2 pb-1 text-xs font-medium text-muted-foreground">
                  <TechIcon icon="helm" size={14} />
                  Helm charts
                </h3>
                <ul className="flex flex-col gap-0.5">
                  {groups.charts.map((i) => (
                    <li key={i.id}>
                      <PaletteItem item={i} />
                    </li>
                  ))}
                </ul>
              </section>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

/** Docker catalog category order for images; unknown categories last. */
function order(category: string) {
  const i = Object.keys(CATEGORY_LABEL).indexOf(category)
  return i < 0 ? Number.MAX_SAFE_INTEGER : i
}

function ListSkeleton() {
  return (
    <div className="flex flex-col gap-1.5 px-2">
      {Array.from({ length: 5 }, (_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  )
}

function detailOf(item: KubeCatalogItem): string {
  if (item.chart) return `${item.chart.name} ${item.chart.version}${item.chart.appVersion ? ` · app ${item.chart.appVersion}` : ""} · ${CHART_CATEGORY[item.category] ?? item.category}`
  if (item.image) return `${item.image} · ${item.workload ?? "Deployment"}`
  return item.kinds.join(" + ")
}

const PaletteItem = memo(function PaletteItem({ item }: { item: KubeCatalogItem }) {
  const [open, setOpen] = useState(false)
  const drag: KubePaletteDrag = { itemId: item.id, label: item.label }
  // Charts add a whole source; starters and images preview their YAML.
  const previewable = item.group !== "chart"
  return (
    <div className="flex flex-col">
      <div className="group flex items-center rounded-md hover:bg-sidebar-accent">
        <Tooltip delayDuration={600}>
          <TooltipTrigger asChild>
            <button
              type="button"
              draggable
              data-testid={`k8s-palette-item-${item.id}`}
              data-label={item.label}
              onDragStart={(e) => {
                e.dataTransfer.setData(KUBE_DRAG_TYPE, JSON.stringify(drag))
                e.dataTransfer.effectAllowed = "copy"
              }}
              onClick={() => void addFromPalette(item.id)}
              className="flex min-w-0 flex-1 cursor-grab items-center gap-2.5 px-2 py-1.5 text-left active:cursor-grabbing"
            >
              <span className="relative flex size-7 shrink-0 items-center justify-center rounded-md border bg-background">
                {item.group === "starter" ? <K8sIcon icon={item.kinds[0] ?? ""} size={18} /> : <TechIcon icon={item.icon} size={18} title={item.label} />}
                {item.group === "chart" && (
                  <span className="absolute -right-1 -bottom-1 rounded-full border bg-background p-px">
                    <TechIcon icon="helm" size={10} title="Helm chart" />
                  </span>
                )}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm">{item.label}</span>
                <span className={cn("block truncate text-muted-foreground", item.group === "starter" ? "text-[11px]" : "font-mono text-[10.5px]")}>{detailOf(item)}</span>
              </span>
            </button>
          </TooltipTrigger>
          <TooltipContent side="right" className="max-w-72">
            {item.description} · click to add or drag onto the canvas
          </TooltipContent>
        </Tooltip>
        {previewable && (
          <button
            type="button"
            aria-label={open ? "Hide YAML" : "Preview YAML"}
            aria-expanded={open}
            data-testid={`k8s-palette-preview-${item.id}`}
            onClick={() => setOpen((o) => !o)}
            className="mr-1 inline-flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground opacity-0 group-hover:opacity-100 hover:text-foreground focus-visible:opacity-100 aria-expanded:opacity-100"
          >
            <ChevronRightIcon className={cn("size-3.5 transition-transform", open && "rotate-90")} />
          </button>
        )}
      </div>
      {open && <YamlPreview itemId={item.id} />}
    </div>
  )
})

function YamlPreview({ itemId }: { itemId: string }) {
  const yaml = useQuery({ queryKey: ["kubernetes-catalog-yaml", itemId], queryFn: () => api.kubernetesCatalogYaml(itemId), staleTime: Infinity })
  if (yaml.isPending) return <Skeleton className="mx-2 my-1 h-24" />
  if (yaml.isError) return <p className="px-2 py-1 text-[11px] text-destructive">{yaml.error.message}</p>
  return <pre className="mx-2 my-1 max-h-64 overflow-auto rounded-md border bg-muted/50 p-2 font-mono text-[10.5px] leading-snug">{yaml.data.yaml}</pre>
}

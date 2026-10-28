import { useQuery } from "@tanstack/react-query"
import { HammerIcon, SearchIcon, XIcon } from "lucide-react"
import { memo, useMemo, useState } from "react"
import { ThemedIcon } from "@/components/arcade/themed-icon"
import { CategoryIcon } from "@/components/category-icon"
import { techIcon } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { DockerImage, DockerImageCategory, DockerStarter } from "@/lib/api/types"
import { addFromPalette, DOCKER_DRAG_TYPE, type DockerPaletteItem } from "./actions"
import { loadDockerImages } from "./intel/images"

export const CATEGORY_LABEL: Record<DockerImageCategory, string> = {
  web: "Web servers & proxies",
  gateway: "API gateways",
  language: "Languages & runtimes",
  database: "Databases",
  cache: "Caches",
  search: "Search & vector",
  messaging: "Messaging & streaming",
  storage: "Storage",
  observability: "Observability",
  auth: "Auth & secrets",
  workflow: "Workflow & data",
  ai: "AI",
  devtools: "Dev tools",
  apps: "Apps",
  cms: "CMS",
  network: "DNS & networking",
  tools: "Tools",
}

const CATEGORY_ORDER = Object.keys(CATEGORY_LABEL) as DockerImageCategory[]

/** "Build from Dockerfile" starters: the daemon scaffolds a buildable Dockerfile (and app) when the folder is new. */
const STARTERS: { starter: DockerStarter; label: string; icon: string; hint: string }[] = [
  { starter: "node", label: "Node.js app", icon: "node", hint: "Tiny HTTP server on port 8080" },
  { starter: "python", label: "Python app", icon: "python", hint: "Tiny HTTP server on port 8080" },
  { starter: "go", label: "Go app", icon: "go", hint: "Tiny HTTP server on port 8080, multi-stage build" },
  { starter: "static", label: "Static site", icon: "nginx", hint: "nginx serving index.html on port 80" },
  { starter: "alpine", label: "Plain Dockerfile", icon: "alpine", hint: "FROM alpine: start from scratch" },
]

/** Text a search matches: label, image, description and category. */
export function matchesImage(img: DockerImage, q: string): boolean {
  const hay = `${img.label} ${img.image}:${img.tag} ${img.description} ${CATEGORY_LABEL[img.category] ?? img.category}`.toLowerCase()
  return q
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((w) => hay.includes(w))
}

/** The palette of a Docker workspace: the image catalog, services built from a Dockerfile, networks and volumes. */
export function DockerPalette() {
  const [q, setQ] = useState("")
  const images = useQuery({ queryKey: ["docker-images"], queryFn: loadDockerImages, staleTime: Infinity })
  const query = q.trim()
  const byCategory = useMemo(() => {
    const out = new Map<DockerImageCategory, DockerImage[]>()
    for (const img of images.data ?? []) out.set(img.category, [...(out.get(img.category) ?? []), img])
    return out
  }, [images.data])
  const results = useMemo(() => (query ? (images.data ?? []).filter((i) => matchesImage(i, query)) : []), [images.data, query])

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="docker-palette">
      <div className="flex flex-col gap-2.5 px-3 pt-1 pb-2">
        <Tabs value="docker">
          <TabsList className="w-full">
            <TabsTrigger value="docker" className="gap-1.5 text-xs">
              <TechIcon icon="docker" size={14} />
              Docker
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <InputGroup>
          <InputGroupAddon>
            <ThemedIcon icon={SearchIcon} pixel="search" />
          </InputGroupAddon>
          <InputGroupInput value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search images…" aria-label="Search images" />
          {q && (
            <InputGroupAddon align="inline-end">
              <InputGroupButton size="icon-xs" onClick={() => setQ("")} aria-label="Clear search">
                <XIcon />
              </InputGroupButton>
            </InputGroupAddon>
          )}
        </InputGroup>
        <p className="text-[11px] leading-snug text-muted-foreground">
          Click or drag onto the canvas. Images come with their ports, settings, health check and data volume.
        </p>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-4">
        {query ? (
          images.isPending ? (
            <ListSkeleton />
          ) : results.length === 0 ? (
            <p className="px-2 py-6 text-center text-sm text-muted-foreground">No images match. Build your own from a Dockerfile below the list.</p>
          ) : (
            <ul className="flex flex-col gap-0.5">
              {results.map((img) => (
                <li key={img.id}>
                  <ImageItem img={img} />
                </li>
              ))}
            </ul>
          )
        ) : (
          <div className="flex flex-col gap-3">
            <section>
              <h3 className="flex items-center gap-1.5 px-2 pb-1 text-xs font-medium text-muted-foreground">
                <HammerIcon className="size-3.5" />
                Build from Dockerfile
              </h3>
              <ul className="flex flex-col gap-0.5">
                {STARTERS.map((s) => (
                  <li key={s.starter}>
                    <PaletteButton item={{ kind: "build", starter: s.starter }} icon={s.icon} label={s.label} detail={s.hint} tip="Adds a service built from its own folder; a starter Dockerfile is created" />
                  </li>
                ))}
              </ul>
            </section>
            <section>
              <h3 className="px-2 pb-1 text-xs font-medium text-muted-foreground">Networking & storage</h3>
              <ul className="flex flex-col gap-0.5">
                <li>
                  <PaletteButton item={{ kind: "network" }} icon="network" label="Network" detail="services on it reach each other by name" tip="A user-defined network: a frame you put services in" />
                </li>
                <li>
                  <PaletteButton item={{ kind: "volume" }} icon="volume" label="Volume" detail="data that survives restarts" tip="A named volume; connect a service to it to mount it" />
                </li>
              </ul>
            </section>
            <section>
              <h3 className="px-2 pb-1 text-xs font-medium text-muted-foreground">Images</h3>
              {images.isPending ? (
                <ListSkeleton />
              ) : images.isError ? (
                <p className="px-2 text-xs text-destructive">Couldn't load the image catalog: {images.error.message}</p>
              ) : (
                <Accordion type="multiple" className="px-1" defaultValue={["web", "language", "database"]}>
                  {CATEGORY_ORDER.filter((c) => byCategory.has(c)).map((c) => (
                    <AccordionItem key={c} value={c} className="border-none">
                      <AccordionTrigger className="px-1 py-1.5 text-sm hover:no-underline">
                        <span className="flex items-center gap-2">
                          <CategoryIcon category={c} />
                          {CATEGORY_LABEL[c]}
                          <span className="text-xs text-muted-foreground">{byCategory.get(c)!.length}</span>
                        </span>
                      </AccordionTrigger>
                      <AccordionContent className="pb-1">
                        <ul className="flex flex-col gap-0.5">
                          {byCategory.get(c)!.map((img) => (
                            <li key={img.id}>
                              <ImageItem img={img} />
                            </li>
                          ))}
                        </ul>
                      </AccordionContent>
                    </AccordionItem>
                  ))}
                </Accordion>
              )}
            </section>
          </div>
        )}
      </div>
    </div>
  )
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

const ImageItem = memo(function ImageItem({ img }: { img: DockerImage }) {
  return (
    <PaletteButton
      item={{ kind: "image", catalogId: img.id, label: img.label }}
      icon={img.icon}
      label={img.label}
      detail={`${img.image}:${img.tag}${img.ports.length ? ` · ${img.ports.join(", ")}` : ""}`}
      tip={img.description}
      mono
    />
  )
})

function PaletteButton({
  item,
  icon,
  label,
  detail,
  tip,
  mono,
}: {
  item: DockerPaletteItem
  icon: string
  label: string
  detail: string
  tip: string
  mono?: boolean
}) {
  return (
    <Tooltip delayDuration={600}>
      <TooltipTrigger asChild>
        <button
          type="button"
          draggable
          data-testid="docker-palette-item"
          data-label={label}
          onDragStart={(e) => {
            e.dataTransfer.setData(DOCKER_DRAG_TYPE, JSON.stringify(item))
            e.dataTransfer.effectAllowed = "copy"
          }}
          onClick={() => void addFromPalette(item)}
          className="group flex w-full cursor-grab items-center gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-sidebar-accent active:cursor-grabbing"
        >
          <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-background">
            <TechIcon icon={techIcon(icon)} size={18} title={label} />
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm">{label}</span>
            <span className={mono ? "block truncate font-mono text-[10.5px] text-muted-foreground" : "block truncate text-[11px] text-muted-foreground"}>
              {detail}
            </span>
          </span>
        </button>
      </TooltipTrigger>
      <TooltipContent side="right" className="max-w-72">
        {tip} · click to add or drag onto the canvas
      </TooltipContent>
    </Tooltip>
  )
}

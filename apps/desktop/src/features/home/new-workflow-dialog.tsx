import { useMutation, useQuery } from "@tanstack/react-query"
import { CloudIcon, FilePlus2Icon, WaypointsIcon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"
import { CloudBadge } from "@/components/cloud"
import { CLOUD_META } from "@/components/cloud-meta"
import { errorMessage } from "@/components/errors"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import { techIcon } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { api } from "@/lib/api/client"
import { ALL_CLOUDS, type Cloud, type Template } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { dialogKind, type Kind, patternLabel, templateSections, type TemplateFilter } from "./template-groups"

const DIFFICULTY: Record<Template["difficulty"], { label: string; className: string }> = {
  beginner: { label: "Beginner", className: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300" },
  intermediate: { label: "Intermediate", className: "bg-amber-500/15 text-amber-700 dark:text-amber-300" },
  advanced: { label: "Advanced", className: "bg-rose-500/15 text-rose-700 dark:text-rose-300" },
}

const BLANK = "__blank"

export function NewWorkflowDialog() {
  const open = useWorkflowStore((s) => s.dialog === "new-workflow")
  const openDialog = useWorkflowStore((s) => s.openDialog)
  return (
    <Dialog open={open} onOpenChange={(o) => openDialog(o ? "new-workflow" : null)}>
      <DialogContent className="flex max-h-[88vh] flex-col sm:max-w-3xl">
        <NewWorkflowForm />
      </DialogContent>
    </Dialog>
  )
}

const BLANK_NAME: Record<Kind, string> = { terraform: "Untitled workflow", docker: "Untitled stack", kubernetes: "Untitled cluster app" }

/** Lives inside DialogContent, so its state resets every time the dialog opens. */
function NewWorkflowForm() {
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const templates = useQuery({ queryKey: ["templates"], queryFn: api.templates, staleTime: Infinity })
  // From a Docker or Kubernetes workspace (its empty canvas, the switcher) the dialog starts on that kind's templates.
  const [kind, setKind] = useState<Kind>(() => dialogKind(useWorkflowStore.getState().workflow?.kind))
  const [choice, setChoice] = useState<string>(BLANK)
  const [name, setName] = useState("")
  const [nameTouched, setNameTouched] = useState(false)
  const [level, setLevel] = useState<string>("all")
  const [filter, setFilter] = useState<TemplateFilter>("all")
  const [cloud, setCloud] = useState<Cloud | "all">("all")
  const docker = kind === "docker"
  const kubernetes = kind === "kubernetes"

  const create = useMutation({
    mutationFn: async () => {
      const template = templates.data?.find((t) => t.id === choice)
      const wf = await api.createWorkflow({
        name: name.trim() || template?.name || BLANK_NAME[kind],
        templateId: template?.id,
        ...(kind !== "terraform" ? { kind } : {}),
      })
      await useWorkflowStore.getState().open(wf.id)
    },
    onSuccess: () => openDialog(null),
    onError: (err) => toast.error("Couldn't create the workflow", { description: errorMessage(err) }),
  })

  const pick = (id: string) => {
    setChoice(id)
    if (!nameTouched) setName(templates.data?.find((t) => t.id === id)?.name ?? "")
  }

  const switchKind = (k: Kind) => {
    setKind(k)
    setChoice(BLANK)
    if (!nameTouched) setName("")
  }

  const { general, edge } = templateSections(templates.data ?? [], { kind, level, filter, cloud })
  const card = (t: Template) => (
    <TemplateCard key={t.id} selected={choice === t.id} onSelect={() => pick(t.id)} onConfirm={() => create.mutate()} id={t.id}>
      <div className="flex items-start justify-between gap-2">
        <span className="flex items-center gap-2 font-medium">
          <WorkflowIconTile icon={t.icon} size={24} />
          {t.name}
        </span>
        <span className={cn("shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium", DIFFICULTY[t.difficulty]?.className)}>
          {DIFFICULTY[t.difficulty]?.label ?? t.difficulty}
        </span>
      </div>
      <p className="line-clamp-3 text-xs text-muted-foreground">{t.description}</p>
      <div className="mt-auto flex flex-wrap items-center gap-1 pt-1">
        {t.kind === "docker" || t.kind === "kubernetes" ? (
          <StackIcons tags={t.tags} platform={t.kind} />
        ) : (
          t.cloud && <CloudBadge cloud={t.cloud} />
        )}
        {t.pattern && (
          <Badge variant="outline" className="text-[10px]">
            {patternLabel(t.pattern)}
          </Badge>
        )}
        {t.tags
          .filter((tag) => tag !== t.pattern)
          .slice(0, t.pattern ? 2 : 3)
          .map((tag) => (
            <Badge key={tag} variant="secondary" className="text-[10px]">
              {tag}
            </Badge>
          ))}
      </div>
    </TemplateCard>
  )

  return (
    <>
        <DialogHeader>
          <DialogTitle>New workflow</DialogTitle>
          <DialogDescription>
            {docker
              ? "Containers from a docker compose file: see them as a diagram, edit them visually or as code, run them with Docker."
              : kubernetes
                ? "Kubernetes manifests, Kustomize or a Helm chart: see the objects as a diagram, edit them visually or as code, run them on a local cluster."
                : "Start from a ready-made architecture or an empty canvas. Everything runs on your machine."}
          </DialogDescription>
        </DialogHeader>
        <ToggleGroup
          type="single"
          variant="outline"
          value={kind}
          onValueChange={(v) => v && switchKind(v as Kind)}
          aria-label="Workflow kind"
          className="w-full"
        >
          <ToggleGroupItem value="terraform" className="flex-1 gap-2">
            <CloudIcon />
            Cloud (Terraform)
          </ToggleGroupItem>
          <ToggleGroupItem value="docker" className="flex-1 gap-2">
            <TechIcon icon="docker" size={16} />
            Containers (Docker)
          </ToggleGroupItem>
          <ToggleGroupItem value="kubernetes" className="flex-1 gap-2" data-testid="new-workflow-kind-kubernetes">
            <TechIcon icon="kubernetes" size={16} />
            Kubernetes
          </ToggleGroupItem>
        </ToggleGroup>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="wf-name">Name</Label>
          <Input
            id="wf-name"
            value={name}
            autoFocus
            placeholder={docker ? "e.g. Shop backend" : kubernetes ? "e.g. Shop on Kubernetes" : "e.g. Orders API"}
            onChange={(e) => {
              setName(e.target.value)
              setNameTouched(true)
            }}
            onKeyDown={(e) => e.key === "Enter" && !create.isPending && create.mutate()}
          />
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">Templates</span>
            <ToggleGroup
              type="single"
              size="sm"
              variant="outline"
              value={filter}
              onValueChange={(v) => v && setFilter(v as TemplateFilter)}
              aria-label="Template group"
            >
              <ToggleGroupItem value="all">All</ToggleGroupItem>
              <ToggleGroupItem value="edge-patterns" className="gap-1">
                <WaypointsIcon />
                Edge patterns
              </ToggleGroupItem>
            </ToggleGroup>
          </div>
          <ToggleGroup type="single" size="sm" variant="outline" value={level} onValueChange={(v) => v && setLevel(v)}>
            <ToggleGroupItem value="all">All</ToggleGroupItem>
            <ToggleGroupItem value="beginner">Beginner</ToggleGroupItem>
            <ToggleGroupItem value="intermediate">Intermediate</ToggleGroupItem>
            <ToggleGroupItem value="advanced">Advanced</ToggleGroupItem>
          </ToggleGroup>
        </div>
        <div className="-mx-1 min-h-0 flex-1 overflow-y-auto px-1 py-1">
          {filter === "all" && (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              <TemplateCard selected={choice === BLANK} onSelect={() => pick(BLANK)}>
                <div className="flex items-center gap-2 font-medium">
                  <FilePlus2Icon className="size-4" />
                  {docker ? "Empty compose file" : kubernetes ? "Empty manifests" : "Blank canvas"}
                </div>
                <p className="text-xs text-muted-foreground">
                  {docker
                    ? "Start with no services and add images or your own Dockerfiles from the palette."
                    : kubernetes
                      ? "Start with an empty k8s/ folder and add workloads, images or Helm charts from the palette."
                      : "Start empty and drag resources in from the palette."}
                </p>
              </TemplateCard>
              {templates.isPending &&
                Array.from({ length: 5 }, (_, i) => <Skeleton key={i} className="h-32 rounded-xl" />)}
              {general.map(card)}
            </div>
          )}
          {(edge.length > 0 || filter === "edge-patterns" || cloud !== "all") && !templates.isPending && (
            <section aria-label="Edge patterns" data-testid="edge-patterns" className={cn("flex flex-col gap-2", filter === "all" && "mt-5")}>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h3 className="flex items-center gap-1.5 text-sm font-medium">
                    <WaypointsIcon className="size-4" />
                    Edge patterns
                  </h3>
                  <p className="text-xs text-muted-foreground">
                    Reverse proxy, API gateway and load balancer: how client traffic reaches your services.
                  </p>
                </div>
                {kind === "terraform" && (
                  <ToggleGroup
                    type="single"
                    size="sm"
                    variant="outline"
                    value={cloud}
                    onValueChange={(v) => v && setCloud(v as Cloud | "all")}
                    aria-label="Edge patterns cloud"
                  >
                    <ToggleGroupItem value="all">All clouds</ToggleGroupItem>
                    {ALL_CLOUDS.map((c) => (
                      <ToggleGroupItem key={c} value={c}>
                        {CLOUD_META[c].label}
                      </ToggleGroupItem>
                    ))}
                  </ToggleGroup>
                )}
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">{edge.map(card)}</div>
              {edge.length === 0 && <p className="text-xs text-muted-foreground">No edge pattern templates match these filters.</p>}
            </section>
          )}
          {templates.isError && (
            <p className="mt-3 text-sm text-destructive">Couldn't load templates: {errorMessage(templates.error)}</p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => openDialog(null)}>
            Cancel
          </Button>
          <Button onClick={() => create.mutate()} disabled={create.isPending}>
            {create.isPending
              ? "Creating…"
              : choice === BLANK
                ? docker
                  ? "Create empty stack"
                  : kubernetes
                    ? "Create empty app"
                    : "Create blank workflow"
                : "Create from template"}
          </Button>
        </DialogFooter>
    </>
  )
}

/** Logos of the technologies a Docker/Kubernetes template's tags name (unknown tags are left to the text badges). */
function StackIcons({ tags, platform }: { tags: string[]; platform: "docker" | "kubernetes" }) {
  const icons = tags
    .map((t) => techIcon(t.toLowerCase()))
    .filter((i, n, all) => !i.generic && i.id !== platform && all.findIndex((x) => x.id === i.id) === n)
  return (
    <span className="mr-1 flex items-center gap-1">
      <TechIcon icon={platform} size={16} />
      {icons.slice(0, 4).map((i) => (
        <TechIcon key={i.id} icon={i} size={16} />
      ))}
    </span>
  )
}

function TemplateCard({
  selected,
  onSelect,
  onConfirm,
  id,
  children,
}: {
  selected: boolean
  onSelect: () => void
  onConfirm?: () => void
  id?: string
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      onDoubleClick={onConfirm}
      aria-pressed={selected}
      data-template={id}
      className={cn(
        "flex min-h-32 flex-col gap-1.5 rounded-xl border bg-card p-3 text-left text-sm transition-colors hover:border-primary/40",
        selected && "border-primary ring-2 ring-primary/30",
      )}
    >
      {children}
    </button>
  )
}

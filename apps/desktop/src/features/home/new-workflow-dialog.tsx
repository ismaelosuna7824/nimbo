import { useMutation, useQuery } from "@tanstack/react-query"
import { FilePlus2Icon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"
import { CloudBadge } from "@/components/cloud"
import { errorMessage } from "@/components/errors"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import { api } from "@/lib/api/client"
import type { Template } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"

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

/** Lives inside DialogContent, so its state resets every time the dialog opens. */
function NewWorkflowForm() {
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const templates = useQuery({ queryKey: ["templates"], queryFn: api.templates, staleTime: Infinity })
  const [choice, setChoice] = useState<string>(BLANK)
  const [name, setName] = useState("")
  const [nameTouched, setNameTouched] = useState(false)
  const [level, setLevel] = useState<string>("all")

  const create = useMutation({
    mutationFn: async () => {
      const template = templates.data?.find((t) => t.id === choice)
      const wf = await api.createWorkflow({
        name: name.trim() || template?.name || "Untitled workflow",
        templateId: template?.id,
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

  const visible = (templates.data ?? []).filter((t) => level === "all" || t.difficulty === level)

  return (
    <>
        <DialogHeader>
          <DialogTitle>New workflow</DialogTitle>
          <DialogDescription>
            Start from a ready-made architecture or an empty canvas. Everything runs on your machine.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="wf-name">Name</Label>
          <Input
            id="wf-name"
            value={name}
            autoFocus
            placeholder="e.g. Orders API"
            onChange={(e) => {
              setName(e.target.value)
              setNameTouched(true)
            }}
            onKeyDown={(e) => e.key === "Enter" && !create.isPending && create.mutate()}
          />
        </div>
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm font-medium">Templates</span>
          <ToggleGroup type="single" size="sm" variant="outline" value={level} onValueChange={(v) => v && setLevel(v)}>
            <ToggleGroupItem value="all">All</ToggleGroupItem>
            <ToggleGroupItem value="beginner">Beginner</ToggleGroupItem>
            <ToggleGroupItem value="intermediate">Intermediate</ToggleGroupItem>
            <ToggleGroupItem value="advanced">Advanced</ToggleGroupItem>
          </ToggleGroup>
        </div>
        <div className="-mx-1 min-h-0 flex-1 overflow-y-auto px-1 py-1">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            <TemplateCard selected={choice === BLANK} onSelect={() => pick(BLANK)}>
              <div className="flex items-center gap-2 font-medium">
                <FilePlus2Icon className="size-4" />
                Blank canvas
              </div>
              <p className="text-xs text-muted-foreground">Start empty and drag resources in from the palette.</p>
            </TemplateCard>
            {templates.isPending &&
              Array.from({ length: 5 }, (_, i) => <Skeleton key={i} className="h-32 rounded-xl" />)}
            {visible.map((t) => (
              <TemplateCard key={t.id} selected={choice === t.id} onSelect={() => pick(t.id)} onConfirm={() => create.mutate()}>
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
                  <CloudBadge cloud={t.cloud} />
                  {t.tags.slice(0, 3).map((tag) => (
                    <Badge key={tag} variant="secondary" className="text-[10px]">
                      {tag}
                    </Badge>
                  ))}
                </div>
              </TemplateCard>
            ))}
          </div>
          {templates.isError && (
            <p className="mt-3 text-sm text-destructive">Couldn't load templates: {errorMessage(templates.error)}</p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => openDialog(null)}>
            Cancel
          </Button>
          <Button onClick={() => create.mutate()} disabled={create.isPending}>
            {create.isPending ? "Creating…" : choice === BLANK ? "Create blank workflow" : "Create from template"}
          </Button>
        </DialogFooter>
    </>
  )
}

function TemplateCard({
  selected,
  onSelect,
  onConfirm,
  children,
}: {
  selected: boolean
  onSelect: () => void
  onConfirm?: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      onDoubleClick={onConfirm}
      aria-pressed={selected}
      className={cn(
        "flex min-h-32 flex-col gap-1.5 rounded-xl border bg-card p-3 text-left text-sm transition-colors hover:border-primary/40",
        selected && "border-primary ring-2 ring-primary/30",
      )}
    >
      {children}
    </button>
  )
}

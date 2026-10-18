import { ArrowRightIcon, SparklesIcon } from "lucide-react"
import { ResourceIcon } from "@/components/resource-icon"
import { Badge } from "@/components/ui/badge"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import type { ConnectionRecipe } from "@/lib/api/types"

export interface PendingConnection {
  source: { id: string; label: string; icon?: string }
  target: { id: string; label: string; icon?: string }
  recipes: ConnectionRecipe[]
}

export function ConnectDialog({
  pending,
  onPick,
  onClose,
}: {
  pending: PendingConnection | null
  onPick: (recipe: ConnectionRecipe) => void
  onClose: () => void
}) {
  const curated = pending?.recipes.filter((r) => r.curated) ?? []
  const generic = pending?.recipes.filter((r) => !r.curated) ?? []
  return (
    <Dialog open={!!pending} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>How should these connect?</DialogTitle>
          <DialogDescription asChild>
            <div className="flex items-center gap-2 pt-1">
              <span className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-foreground">
                <ResourceIcon name={pending?.source.icon} className="size-3.5" />
                {pending?.source.label}
              </span>
              <ArrowRightIcon className="size-4" />
              <span className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-foreground">
                <ResourceIcon name={pending?.target.icon} className="size-3.5" />
                {pending?.target.label}
              </span>
            </div>
          </DialogDescription>
        </DialogHeader>
        <div className="flex max-h-[60vh] flex-col gap-4 overflow-y-auto">
          {curated.length > 0 && (
            <RecipeGroup title="Recommended" recipes={curated} onPick={onPick} />
          )}
          {generic.length > 0 && (
            <RecipeGroup
              title={curated.length > 0 ? "Other ways" : "Pick the attribute to link"}
              recipes={generic}
              onPick={onPick}
            />
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

function RecipeGroup({
  title,
  recipes,
  onPick,
}: {
  title: string
  recipes: ConnectionRecipe[]
  onPick: (recipe: ConnectionRecipe) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="text-xs font-medium text-muted-foreground">{title}</div>
      {recipes.map((r) => (
        <button
          key={r.id}
          type="button"
          onClick={() => onPick(r)}
          className="group flex w-full items-start gap-3 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-accent"
        >
          {r.curated ? (
            <SparklesIcon className="mt-0.5 size-4 shrink-0 text-amber-500" />
          ) : (
            <ArrowRightIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          )}
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 text-sm font-medium">
              {r.label}
              {r.curated && <Badge variant="secondary">Best practice</Badge>}
            </div>
            {r.description && <p className="mt-0.5 text-xs text-muted-foreground">{r.description}</p>}
          </div>
        </button>
      ))}
    </div>
  )
}

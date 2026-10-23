import { ArrowRightIcon, HardDriveIcon, LinkIcon, NetworkIcon, VariableIcon } from "lucide-react"
import { useState } from "react"
import type { TechIconInfo } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import type { ComposeConnectKind, ComposeConnectRecipe } from "@/lib/api/types"

export interface PendingComposeConnection {
  /** The node that uses `to` (where the drag started). */
  from: { id: string; label: string; icon: TechIconInfo }
  to: { id: string; label: string; icon: TechIconInfo }
  recipes: ComposeConnectRecipe[]
}

const KIND: Record<ComposeConnectKind, { title: string; icon: typeof LinkIcon }> = {
  depends_on: { title: "Start order", icon: LinkIcon },
  env: { title: "Connection settings", icon: VariableIcon },
  network: { title: "Network", icon: NetworkIcon },
  volume: { title: "Storage", icon: HardDriveIcon },
}

const ORDER: ComposeConnectKind[] = ["depends_on", "env", "network", "volume"]

/** Ways to connect two compose nodes; a volume mount asks for its path in the container. */
export function ComposeConnectDialog({
  pending,
  onPick,
  onClose,
}: {
  pending: PendingComposeConnection | null
  onPick: (recipe: ComposeConnectRecipe, target?: string) => void
  onClose: () => void
}) {
  const [mount, setMount] = useState<ComposeConnectRecipe | null>(null)
  const [path, setPath] = useState("/data")
  const close = () => {
    setMount(null)
    onClose()
  }
  return (
    <Dialog open={!!pending} onOpenChange={(o) => !o && close()}>
      <DialogContent className="sm:max-w-lg" data-testid="compose-connect-dialog">
        <DialogHeader>
          <DialogTitle>How should these connect?</DialogTitle>
          <DialogDescription asChild>
            <div className="flex items-center gap-2 pt-1">
              <span className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-foreground">
                {pending && <TechIcon icon={pending.from.icon} size={14} />}
                {pending?.from.label}
              </span>
              <ArrowRightIcon className="size-4" />
              <span className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-foreground">
                {pending && <TechIcon icon={pending.to.icon} size={14} />}
                {pending?.to.label}
              </span>
            </div>
          </DialogDescription>
        </DialogHeader>
        {mount ? (
          <form
            className="flex flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault()
              const target = path.trim()
              if (!target.startsWith("/")) return
              setMount(null)
              onPick(mount, target)
            }}
          >
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mount-path">Path inside {pending?.from.label}</Label>
              <Input id="mount-path" autoFocus value={path} onChange={(e) => setPath(e.target.value)} placeholder="/var/lib/data" />
              <p className="text-xs text-muted-foreground">Where the volume's files appear in the container (an absolute path).</p>
            </div>
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => setMount(null)}>
                Back
              </Button>
              <Button type="submit" disabled={!path.trim().startsWith("/")}>
                Mount
              </Button>
            </div>
          </form>
        ) : (
          <div className="flex max-h-[60vh] flex-col gap-4 overflow-y-auto">
            {ORDER.map((kind) => {
              const recipes = pending?.recipes.filter((r) => r.kind === kind) ?? []
              if (recipes.length === 0) return null
              const { title, icon: Icon } = KIND[kind]
              return (
                <div key={kind} className="flex flex-col gap-2">
                  <div className="text-xs font-medium text-muted-foreground">{title}</div>
                  {recipes.map((r) => (
                    <button
                      key={r.id}
                      type="button"
                      data-recipe={r.id}
                      onClick={() => (r.kind === "volume" && !r.target ? setMount(r) : onPick(r))}
                      className="group flex w-full items-start gap-3 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-accent"
                    >
                      <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground group-hover:text-primary" />
                      <div className="min-w-0 flex-1">
                        <div className="text-sm font-medium">{r.label}</div>
                        {r.description && <p className="mt-0.5 text-xs text-muted-foreground">{r.description}</p>}
                        {r.env && r.env.length > 0 && (
                          <ul className="mt-1.5 flex flex-col gap-0.5 rounded-md bg-muted/60 px-2 py-1 font-mono text-[11px]">
                            {r.env.map((e) => (
                              <li key={e.key} className="truncate">
                                {e.key}=<span className="text-muted-foreground">{e.value}</span>
                              </li>
                            ))}
                          </ul>
                        )}
                      </div>
                    </button>
                  ))}
                </div>
              )
            })}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

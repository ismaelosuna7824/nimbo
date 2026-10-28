import { ArrowRightIcon, BrickWallIcon, ExpandIcon, GlobeIcon, HardDriveIcon, LinkIcon, RouteIcon, ShieldIcon, ShieldPlusIcon, SplitIcon, VariableIcon } from "lucide-react"
import { memo, useState } from "react"
import { K8sIcon } from "@/components/k8s-icons/k8s-icon"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { AccessLevelPicker } from "@/features/access/access-level-picker"
import { accessOptions } from "@/features/access/support"
import type { KubeConnectRecipe } from "@/lib/api/types"
import type { KubeRecipeInputs } from "../actions"

export interface PendingKubeEnd {
  id: string
  label: string
  kind: string
}

export interface PendingKubeConnection {
  /** The object that refers to `to` (where the drag started). */
  from: PendingKubeEnd
  /** Absent: the recipe creates the object (expose via a new Service, add an HPA). */
  to?: PendingKubeEnd
  recipes: KubeConnectRecipe[]
}

const KIND_ICON: Record<string, typeof LinkIcon> = {
  expose: SplitIcon,
  selects: LinkIcon,
  route: RouteIcon,
  envFrom: VariableIcon,
  env: VariableIcon,
  volume: HardDriveIcon,
  serviceaccount: ShieldIcon,
  scale: ExpandIcon,
  access: ShieldPlusIcon,
  allowTraffic: BrickWallIcon,
  allowAll: GlobeIcon,
}

/** Kinds that run with an identity of their own (grants go to them). */
const IDENTITY_KINDS: Record<string, true> = { Deployment: true, StatefulSet: true, DaemonSet: true, ReplicaSet: true, Job: true, CronJob: true, Pod: true, ServiceAccount: true }

/** The identity and resource ends of a grant (the drag can go either way). */
function grantEnds(p: PendingKubeConnection): { identity?: string; resource?: string } {
  if (!p.to) return {}
  return IDENTITY_KINDS[p.from.kind] || !IDENTITY_KINDS[p.to.kind] ? { identity: p.from.label, resource: p.to.label } : { identity: p.to.label, resource: p.from.label }
}

/** Recipes that ask for something before they run. */
const NEEDS_INPUT: Record<string, true> = { route: true, volume: true, expose: true, env: true }

const PORT_RE = /^\d{1,5}$/

/**
 * Ways to connect two Kubernetes objects; routes, mounts, new Services and env wiring ask for their details.
 * Memoized: the canvas re-renders on every edit, the closed dialog doesn't (pass stable callbacks).
 */
export const KubeConnectDialog = memo(function KubeConnectDialog({
  pending,
  onPick,
  onClose,
}: {
  pending: PendingKubeConnection | null
  onPick: (recipe: KubeConnectRecipe, inputs: KubeRecipeInputs) => void
  onClose: () => void
}) {
  const [asking, setAsking] = useState<KubeConnectRecipe | null>(null)
  const all = pending?.recipes ?? []
  // Read / Write / Manage grants: the shared "What can it do?" cards; bind, own Role and wiring recipes below.
  const grants = accessOptions(all, (r) => r.kind === "access" && !!r.level)
  const others = all.filter((r) => !grants.includes(r))
  const close = () => {
    setAsking(null)
    onClose()
  }
  return (
    <Dialog open={!!pending} onOpenChange={(o) => !o && close()}>
      <DialogContent className="sm:max-w-lg" data-testid="k8s-connect-dialog">
        <DialogHeader>
          <DialogTitle>{pending?.to ? "How should these connect?" : `Add to ${pending?.from.label ?? ""}`}</DialogTitle>
          <DialogDescription asChild>
            <div className="flex items-center gap-2 pt-1">
              {pending && <End end={pending.from} />}
              {pending?.to && (
                <>
                  <ArrowRightIcon className="size-4" />
                  <End end={pending.to} />
                </>
              )}
            </div>
          </DialogDescription>
        </DialogHeader>
        {asking && pending ? (
          <RecipeInputs
            key={asking.id}
            recipe={asking}
            pending={pending}
            onBack={() => setAsking(null)}
            onSubmit={(inputs) => {
              setAsking(null)
              onPick(asking, inputs)
            }}
          />
        ) : (
          <div className="flex max-h-[60vh] flex-col gap-3 overflow-y-auto">
            {pending && grants.length > 0 && <AccessLevelPicker options={grants} onPick={(r) => onPick(r, {})} {...grantEnds(pending)} />}
            {grants.length > 0 && others.length > 0 && <div className="text-xs font-medium text-muted-foreground">Or connect them</div>}
            {others.map((r) => {
              const Icon = KIND_ICON[r.kind] ?? LinkIcon
              return (
                <button
                  key={r.id}
                  type="button"
                  data-testid={`k8s-connect-recipe-${r.id}`}
                  data-recipe={r.id}
                  onClick={() => (NEEDS_INPUT[r.kind] ? setAsking(r) : onPick(r, {}))}
                  className="group flex w-full items-start gap-3 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-accent"
                >
                  <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground group-hover:text-primary" />
                  <div className="min-w-0 flex-1">
                    <div className="text-sm font-medium">{r.label}</div>
                    {r.description && <p className="mt-0.5 text-xs text-muted-foreground">{r.description}</p>}
                  </div>
                </button>
              )
            })}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
})

function End({ end }: { end: PendingKubeEnd }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5 rounded-md border px-2 py-1 text-foreground">
      <K8sIcon icon={end.kind} size={14} />
      <span className="truncate">{end.label}</span>
    </span>
  )
}

function RecipeInputs({
  recipe,
  pending,
  onBack,
  onSubmit,
}: {
  recipe: KubeConnectRecipe
  pending: PendingKubeConnection
  onBack: () => void
  onSubmit: (inputs: KubeRecipeInputs) => void
}) {
  const [host, setHost] = useState("")
  const [path, setPath] = useState(recipe.kind === "route" ? "/" : "/etc/config")
  const [port, setPort] = useState("")
  const [key, setKey] = useState("")
  const portOk = port === "" || PORT_RE.test(port)
  const pathOk = path.startsWith("/")
  const valid = recipe.kind === "volume" ? pathOk : recipe.kind === "route" ? pathOk && portOk : recipe.kind === "env" ? key === "" || /^[A-Za-z_][A-Za-z0-9_]*$/.test(key) : portOk
  const submit = () => {
    if (!valid) return
    const p = port ? Number(port) : undefined
    switch (recipe.kind) {
      case "route":
        return onSubmit({ host: host.trim() || undefined, path, port: p })
      case "volume":
        return onSubmit({ path })
      case "env":
        return onSubmit({ key: key || undefined })
      default:
        return onSubmit({ port: p })
    }
  }
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <p className="text-sm font-medium">{recipe.label}</p>
      {recipe.kind === "route" && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="k8s-connect-host">Host</Label>
          <Input id="k8s-connect-host" data-testid="k8s-connect-host" autoFocus value={host} onChange={(e) => setHost(e.target.value)} placeholder="app.localhost (any host when empty)" />
        </div>
      )}
      {(recipe.kind === "route" || recipe.kind === "volume") && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="k8s-connect-path">{recipe.kind === "route" ? "Path" : `Path inside ${pending.from.label}`}</Label>
          <Input id="k8s-connect-path" data-testid="k8s-connect-path" autoFocus={recipe.kind === "volume"} value={path} onChange={(e) => setPath(e.target.value)} className="font-mono" />
          {recipe.kind === "volume" && <p className="text-xs text-muted-foreground">Where the files appear in the containers (an absolute path).</p>}
        </div>
      )}
      {(recipe.kind === "route" || recipe.kind === "expose") && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="k8s-connect-port">{recipe.kind === "route" ? "Service port" : "Port"}</Label>
          <Input
            id="k8s-connect-port"
            data-testid="k8s-connect-port"
            autoFocus={recipe.kind === "expose"}
            inputMode="numeric"
            value={port}
            onChange={(e) => setPort(e.target.value)}
            placeholder={recipe.kind === "route" ? "first port of the Service" : "the container's first port"}
            aria-invalid={!portOk || undefined}
            className="font-mono"
          />
        </div>
      )}
      {recipe.kind === "env" && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="k8s-connect-key">Variable name</Label>
          <Input id="k8s-connect-key" data-testid="k8s-connect-key" autoFocus value={key} onChange={(e) => setKey(e.target.value)} placeholder={`${(pending.to?.label ?? "SERVICE").toUpperCase().replace(/[^A-Z0-9]/g, "_")}_URL`} className="font-mono" />
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onBack}>
          Back
        </Button>
        <Button type="submit" data-testid="k8s-connect-apply" disabled={!valid}>
          Apply
        </Button>
      </div>
    </form>
  )
}

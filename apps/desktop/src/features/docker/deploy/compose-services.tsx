import { useMemo, type ReactNode } from "react"
import { ExternalLink, Play, RotateCcw, ScrollText } from "lucide-react"
import { openExternal } from "@/components/open-external"
import { techIconFor } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { ComposeNode, ComposeServiceStatus, ContainerHealth } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { COMPOSE_STATE, servicePorts, serviceReplicas, serviceRestarts } from "./compose-model"

const HEALTH_CLASS: Record<ContainerHealth, string> = {
  healthy: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
  unhealthy: "border-destructive/30 bg-destructive/10 text-destructive",
  starting: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
}

/** Status dot + label of a project or service state. */
export function ComposeStateLabel({ state, className }: { state: ComposeServiceStatus["state"]; className?: string }) {
  const meta = COMPOSE_STATE[state]
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      {state === "starting" ? <Spinner className="size-3" /> : <span className={cn("size-2 rounded-full", meta.dot)} />}
      {meta.label}
    </span>
  )
}

export function HealthBadge({ health }: { health: ContainerHealth }) {
  return (
    <Badge variant="outline" className={cn("h-4.5 px-1.5 text-[10px] capitalize", HEALTH_CLASS[health])}>
      {health}
    </Badge>
  )
}

/** Icon of a service from its graph facts (image, Dockerfile base images, stack hints). */
function serviceIcon(node: ComposeNode | undefined) {
  const f = node?.service
  return techIconFor({ image: f?.image?.ref, baseImages: f?.baseImages, stack: f?.stack, kind: "service" })
}

/** One row per compose service: state, health, replicas, published ports, restarts and per-service actions. */
export function ComposeServices({
  services,
  nodes,
  busy,
  starting,
  onAction,
  onLogs,
}: {
  services: ComposeServiceStatus[]
  nodes: ComposeNode[]
  /** A run is in progress: per-service actions wait for it. */
  busy: boolean
  /** "<action>:<service>" being started. */
  starting: string | null
  onAction: (action: "up" | "restart", service: string) => void
  onLogs: (service: string) => void
}) {
  const byId = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes])
  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-background text-left text-[11px] text-muted-foreground">
        <tr className="border-b">
          <th className="px-3 py-1.5 font-medium">Service</th>
          <th className="px-2 py-1.5 font-medium">State</th>
          <th className="px-2 py-1.5 font-medium">Replicas</th>
          <th className="px-2 py-1.5 font-medium">Ports</th>
          <th className="px-2 py-1.5 font-medium">Restarts</th>
          <th className="px-3 py-1.5" aria-label="Actions" />
        </tr>
      </thead>
      <tbody className="divide-y">
        {services.map((s) => (
          <ServiceRow
            key={s.name}
            service={s}
            node={byId.get(s.nodeId) ?? nodes.find((n) => n.kind === "service" && n.name === s.name)}
            busy={busy}
            starting={starting}
            onAction={onAction}
            onLogs={onLogs}
          />
        ))}
      </tbody>
    </table>
  )
}

function ServiceRow({
  service: s,
  node,
  busy,
  starting,
  onAction,
  onLogs,
}: {
  service: ComposeServiceStatus
  node: ComposeNode | undefined
  busy: boolean
  starting: string | null
  onAction: (action: "up" | "restart", service: string) => void
  onLogs: (service: string) => void
}) {
  const icon = useMemo(() => serviceIcon(node), [node])
  const ports = servicePorts(s)
  const replicas = serviceReplicas(s)
  const restarts = serviceRestarts(s)
  const image = node?.service?.image?.ref ?? s.containers[0]?.image ?? (node?.service?.build ? "built from Dockerfile" : "")
  const up = s.state === "running" || s.state === "starting"
  const exited = s.containers.find((c) => c.state === "exited" && c.exitCode !== 0)

  return (
    <tr data-testid="compose-service-row" data-service={s.name} data-state={s.state} className="hover:bg-muted/40">
      <td className="px-3 py-1.5">
        <div className="flex min-w-0 items-center gap-2">
          <TechIcon icon={icon} size={20} />
          <div className="min-w-0">
            <div className="truncate font-medium">{s.name}</div>
            {image && <div className="truncate font-mono text-[10px] text-muted-foreground">{image}</div>}
          </div>
        </div>
      </td>
      <td className="px-2 py-1.5">
        <div className="flex flex-wrap items-center gap-1.5">
          <ComposeStateLabel state={s.state} />
          {s.health && <HealthBadge health={s.health} />}
          {exited && <span className="font-mono text-[10px] text-destructive">exit {exited.exitCode}</span>}
        </div>
      </td>
      <td className="px-2 py-1.5 tabular-nums">
        {replicas.total === 0 ? <span className="text-muted-foreground">—</span> : `${replicas.running}/${replicas.total}`}
      </td>
      <td className="px-2 py-1.5">
        {ports.length === 0 ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <div className="flex flex-wrap gap-x-2 gap-y-0.5">
            {ports.map((p) =>
              p.url ? (
                <button
                  key={`${p.host}/${p.protocol}`}
                  type="button"
                  className="inline-flex items-center gap-0.5 font-mono text-primary underline-offset-2 hover:underline"
                  title={`Open ${p.url} (container port ${p.container})`}
                  onClick={() => void openExternal(p.url!)}
                >
                  {p.host}
                  <ExternalLink className="size-3" />
                </button>
              ) : (
                <span key={`${p.host}/${p.protocol}`} className="font-mono" title={`Container port ${p.container}`}>
                  {p.host}
                  {p.protocol === "udp" ? "/udp" : ""}
                </span>
              ),
            )}
          </div>
        )}
      </td>
      <td className={cn("px-2 py-1.5 tabular-nums", restarts > 0 && "text-amber-700 dark:text-amber-400")}>{restarts}</td>
      <td className="px-3 py-1.5">
        <div className="flex justify-end gap-0.5">
          <RowAction
            label={up ? `Recreate ${s.name} (docker compose up ${s.name})` : `Start ${s.name} and what it depends on`}
            disabled={busy}
            loading={starting === `up:${s.name}`}
            onClick={() => onAction("up", s.name)}
          >
            <Play />
          </RowAction>
          <RowAction
            label={`Restart ${s.name}`}
            disabled={busy || !up}
            loading={starting === `restart:${s.name}`}
            onClick={() => onAction("restart", s.name)}
          >
            <RotateCcw />
          </RowAction>
          <RowAction label={`Logs of ${s.name}`} onClick={() => onLogs(s.name)}>
            <ScrollText />
          </RowAction>
        </div>
      </td>
    </tr>
  )
}

/** Icon button of a table row with a tooltip and a spinner while its action starts. */
export function RowAction({
  label,
  disabled,
  loading,
  onClick,
  children,
}: {
  label: string
  disabled?: boolean
  loading?: boolean
  onClick: () => void
  children: ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button size="icon-xs" variant="ghost" aria-label={label} disabled={disabled || loading} onClick={onClick}>
          {loading ? <Spinner /> : children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

import { EyeIcon, PencilLineIcon, ShieldAlertIcon } from "lucide-react"
import type { AccessLevel } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import type { AccessOption } from "./support"

const LEVEL_META: Record<AccessLevel, { icon: typeof EyeIcon; hint: string; tone: string }> = {
  read: { icon: EyeIcon, hint: "Look, never change", tone: "text-sky-500" },
  write: { icon: PencilLineIcon, hint: "Read and change data", tone: "text-emerald-500" },
  manage: { icon: ShieldAlertIcon, hint: "Full control of this one resource", tone: "text-amber-500" },
}

/**
 * "What can it do?" — the shared grant question of every provider: the user drags an identity onto a resource and
 * picks Read, Write or Manage; each card previews exactly what Nimbo writes in the provider's own permission model.
 */
export function AccessLevelPicker<T extends AccessOption>({
  options,
  onPick,
  identity,
  resource,
}: {
  options: T[]
  onPick: (option: T) => void
  identity?: string
  resource?: string
}) {
  if (options.length === 0) return null
  return (
    <div className="flex flex-col gap-2" data-testid="access-level-picker">
      <div className="flex flex-col gap-0.5">
        <div className="text-sm font-medium">What can it do?</div>
        {identity && resource && (
          <p className="text-xs text-muted-foreground">
            Nimbo grants <span className="font-medium text-foreground">{identity}</span> only what it needs on{" "}
            <span className="font-medium text-foreground">{resource}</span>.
          </p>
        )}
      </div>
      <div className="grid gap-2 sm:grid-cols-3">
        {options.map((o) => {
          const meta = LEVEL_META[o.level ?? "manage"]
          const Icon = meta.icon
          return (
            <button
              key={o.id}
              type="button"
              data-testid={`access-level-${o.level}`}
              onClick={() => onPick(o)}
              className={cn(
                "group flex min-w-0 flex-col items-start gap-1.5 rounded-lg border p-3 text-left transition-colors",
                "hover:border-primary/50 hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
              )}
            >
              <span className="flex items-center gap-1.5 text-sm font-medium">
                <Icon className={cn("size-4", meta.tone)} />
                {o.label}
              </span>
              <span className="text-[11px] text-muted-foreground">{meta.hint}</span>
              {o.description && (
                <span
                  className="line-clamp-5 w-full text-[11px] leading-snug [overflow-wrap:anywhere] text-muted-foreground"
                  title={o.description}
                >
                  {o.description}
                </span>
              )}
            </button>
          )
        })}
      </div>
    </div>
  )
}

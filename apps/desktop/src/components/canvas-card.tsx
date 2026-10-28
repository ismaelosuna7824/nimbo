import type { CSSProperties, ReactNode } from "react"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

/**
 * The look shared by the canvas cards (Terraform, Docker, Kubernetes): a card-level surface that lifts on hover and
 * gets an accent halo (ring + soft glow) when selected. Only shadows and border color animate.
 */
export const CARD_SURFACE =
  "rounded-xl border bg-card text-card-foreground shadow-sm transition-[box-shadow,border-color] duration-150 hover:border-foreground/15 hover:shadow-md"
export const CARD_SELECTED = "border-primary/70 ring-4 ring-primary/15 shadow-glow hover:border-primary/70 hover:shadow-glow"
/** Selection of a frame (network, namespace, chart, module group): its dashed border keeps its color. */
export const FRAME_SELECTED = "ring-2 ring-primary/70 ring-offset-2 ring-offset-canvas"
/** The icon tile at the top left of a card. */
export const ICON_TILE = "flex size-8 shrink-0 items-center justify-center rounded-lg"
/** A card's name and its quieter, smaller monospace subtitle (address, image, facts). */
export const CARD_TITLE = "truncate text-[13px] leading-tight font-semibold tracking-[-0.005em]"
export const CARD_SUBTITLE = "truncate font-mono text-[10.5px] leading-snug text-muted-foreground/90"
/** Connection handles: small muted dots, the accent on hover. */
export const HANDLE = "!size-2.5 !border-2 !border-canvas !bg-muted-foreground/60 transition-colors hover:!bg-primary"

/**
 * What a status dot says. Filled = something is live (ok), in progress or broken; hollow = could run but isn't
 * (ready) or won't run here (off). Red and amber keep their meaning: broken and needs work.
 */
export type StatusTone = "ok" | "progress" | "error" | "ready" | "off"

const TONE: Record<StatusTone, string> = {
  ok: "bg-emerald-500 ring-[3px] ring-emerald-500/20",
  progress: "bg-amber-500 ring-[3px] ring-amber-500/20 motion-safe:animate-pulse",
  error: "bg-destructive ring-[3px] ring-destructive/20",
  ready: "border-[1.5px] border-emerald-500/80",
  off: "border-[1.5px] border-muted-foreground/50",
}

/**
 * A card's one status: a dot with its meaning in a tooltip and in its accessible name. `children` is an optional
 * short visible word next to it (e.g. "Deployed").
 */
export function StatusDot({ tone, label, hint, children, className }: { tone: StatusTone; label: string; hint?: ReactNode; children?: ReactNode; className?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span data-slot="node-status" data-tone={tone} className={cn("inline-flex min-w-0 shrink-0 items-center gap-1.5", className)}>
          <span role="img" aria-label={label} className={cn("size-2 shrink-0 rounded-full", TONE[tone])} />
          {children && <span className="truncate text-[11px] font-medium text-muted-foreground">{children}</span>}
        </span>
      </TooltipTrigger>
      <TooltipContent>{hint ?? label}</TooltipContent>
    </Tooltip>
  )
}

/** Edge stroke: muted and thin at rest, the accent when hovered or selected (`width` pins it, e.g. arcade's wires). */
export function edgeStyle(emphasis: boolean | undefined, dash?: string, rest = 0.5, width?: number): CSSProperties {
  return {
    strokeWidth: width ?? (emphasis ? 2 : 1.25),
    strokeDasharray: dash,
    stroke: emphasis ? "var(--color-primary)" : "var(--color-muted-foreground)",
    opacity: emphasis ? 1 : rest,
    transition: "stroke 150ms, opacity 150ms, stroke-width 150ms",
  }
}

/** Minimap fills from the theme: cards muted, tinted a little by their provider (or state) color; frames faint. */
export const MINIMAP_CARD = "color-mix(in oklab, var(--muted-foreground) 45%, transparent)"
export const MINIMAP_FRAME = "color-mix(in oklab, var(--muted-foreground) 10%, transparent)"
const tints = new Map<string, string>()
export function minimapTint(color: string): string {
  let tint = tints.get(color)
  if (!tint) tints.set(color, (tint = `color-mix(in oklab, ${color} 35%, ${MINIMAP_CARD})`))
  return tint
}

/** A slow dash drifting along an edge between running (deployed) ends; still with reduced motion. */
export function RunningFlow({ path }: { path: string }) {
  return <path d={path} fill="none" strokeWidth={1.5} className="edge-flow pointer-events-none stroke-emerald-500/70" />
}

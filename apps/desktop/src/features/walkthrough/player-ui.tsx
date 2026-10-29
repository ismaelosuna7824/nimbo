// What the request walkthrough and the infrastructure tour share on screen: the caption panel and its controls,
// where a component runs and the "also involved" chips (their hooks are in player-hooks.ts).
import { Panel } from "@xyflow/react"
import { ChevronLeftIcon, ChevronRightIcon, PauseIcon, PlayIcon, RotateCcwIcon, XIcon } from "lucide-react"
import { useCallback, type ReactNode, type RefObject } from "react"
import { StatusDot, type StatusTone } from "@/components/canvas-card"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Chip, Where } from "./describe"
import { closeWalkthrough, replayWalkthrough, SPEEDS, useWalkthrough } from "./store"

export function IconButton({ label, onClick, disabled, children }: { label: string; onClick: () => void; disabled?: boolean; children: ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button size="icon-sm" variant="ghost" aria-label={label} onClick={onClick} disabled={disabled}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

function SpeedButton() {
  const speed = useWalkthrough((s) => s.speed)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="xs"
          variant="ghost"
          className="w-10 font-mono tabular-nums"
          aria-label={`Speed ${speed}×`}
          onClick={() => useWalkthrough.setState({ speed: SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length] })}
        >
          {speed}×
        </Button>
      </TooltipTrigger>
      <TooltipContent>Speed</TooltipContent>
    </Tooltip>
  )
}

const WHERE_TONE: Record<Where["tone"], StatusTone> = { deployed: "ok", local: "ready", export: "off", absent: "off" }
const WHERE_WORD: Record<Where["tone"], string> = { deployed: "Deployed", local: "Local", export: "Export only", absent: "Not created" }

/** Where a component runs: the card's status dot, its meaning in the tooltip. */
export function WhereDot({ where, className }: { where: Where; className?: string }) {
  return (
    <StatusDot tone={WHERE_TONE[where.tone]} label={where.text} className={className}>
      {WHERE_WORD[where.tone]}
    </StatusDot>
  )
}

/** A supporting piece; selects its card when it has one. */
export function AlsoChip({ chip, onSelect }: { chip: Chip; onSelect: (id: string) => void }) {
  const body = (
    <span className="inline-flex h-5 max-w-48 items-center truncate rounded-full border bg-muted/60 px-2 text-[11px] text-foreground">{chip.label}</span>
  )
  const tip = chip.detail ?? (chip.nodeId ? `Show ${chip.label}` : undefined)
  const trigger = chip.nodeId ? (
    <button type="button" className="rounded-full focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none" onClick={() => onSelect(chip.nodeId!)}>
      {body}
    </button>
  ) : (
    body
  )
  if (!tip) return trigger
  return (
    <Tooltip>
      <TooltipTrigger asChild>{trigger}</TooltipTrigger>
      <TooltipContent>{tip}</TooltipContent>
    </Tooltip>
  )
}

export interface PlayerControls {
  index: number
  /** Next is not possible (a fork waits for a choice). */
  nextDisabled?: boolean
  /** A short note right of the controls. */
  hint?: string
  go: (to: number) => void
}

/**
 * The caption panel at the bottom of the canvas: title, step counter, speed and close; the step's `children`; and
 * Restart / Previous / Play-Pause / Next when `controls` are given.
 */
export function PlayerPanel({
  title,
  icon,
  counter,
  controls,
  panelRef,
  children,
}: {
  title: string
  icon: ReactNode
  counter?: string
  controls?: PlayerControls
  panelRef: RefObject<HTMLDivElement | null>
  children: ReactNode
}) {
  const playing = useWalkthrough((s) => s.playing)
  const toggle = useCallback(() => useWalkthrough.setState((s) => ({ playing: !s.playing })), [])
  return (
    <Panel position="bottom-center" className="!mb-4 w-[34rem] max-w-[calc(100%-2rem)]">
      <div ref={panelRef} role="region" aria-label={title} data-testid="walkthrough-panel" className="flex flex-col gap-2 rounded-xl border bg-background/95 p-3 text-sm shadow-lg backdrop-blur">
        <div className="flex items-center gap-2">
          {icon}
          <span className="font-medium">{title}</span>
          {counter && (
            <span data-testid="walkthrough-step-counter" className="text-xs text-muted-foreground tabular-nums">
              {counter}
            </span>
          )}
          <div className="ml-auto flex items-center gap-0.5">
            <SpeedButton />
            <IconButton label="Close (Esc)" onClick={closeWalkthrough}>
              <XIcon />
            </IconButton>
          </div>
        </div>
        {children}
        {controls && (
          <div className="flex items-center gap-1 border-t pt-2">
            <IconButton label="Restart" onClick={replayWalkthrough}>
              <RotateCcwIcon />
            </IconButton>
            <IconButton label="Previous step (←)" onClick={() => controls.go(controls.index - 1)} disabled={controls.index === 0}>
              <ChevronLeftIcon />
            </IconButton>
            <Button size="sm" variant="outline" className="w-20" onClick={toggle} aria-label={playing ? "Pause (space)" : "Play (space)"}>
              {playing ? <PauseIcon /> : <PlayIcon />}
              {playing ? "Pause" : "Play"}
            </Button>
            <IconButton label="Next step (→)" onClick={() => controls.go(controls.index + 1)} disabled={controls.nextDisabled}>
              <ChevronRightIcon />
            </IconButton>
            {controls.hint && <span className="ml-auto text-xs text-muted-foreground">{controls.hint}</span>}
          </div>
        )}
      </div>
    </Panel>
  )
}

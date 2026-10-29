// What the request walkthrough and the infrastructure tour share on screen: the caption panel and its controls,
// where a component runs and one-line caption rows (their hooks are in player-hooks.ts).
import { Panel } from "@xyflow/react"
import { ChevronLeftIcon, ChevronRightIcon, PauseIcon, PlayIcon, RotateCcwIcon, XIcon } from "lucide-react"
import { useCallback, type ReactNode, type RefObject } from "react"
import { StatusDot, type StatusTone } from "@/components/canvas-card"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import type { Where } from "./describe"
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

/** One line of a caption (a connection, what else is involved, what is built on it next): cut short, whole in a tooltip. */
export function OneLine({ text, testId, icon, className }: { text: string; testId?: string; icon?: ReactNode; className?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <p data-testid={testId} className={cn("flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground", className)}>
          {icon}
          <span className="truncate">{text}</span>
        </p>
      </TooltipTrigger>
      <TooltipContent className="max-w-md">{text}</TooltipContent>
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
 * The caption panel at the bottom of the canvas, compact: one row with the title, step counter, Restart / Previous /
 * Play-Pause / Next (when `controls` are given), speed and close; then the step's `children`.
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
    <Panel position="bottom-center" className="!mb-3 w-[30rem] max-w-[calc(100%-2rem)]">
      <div ref={panelRef} role="region" aria-label={title} data-testid="walkthrough-panel" className="flex flex-col gap-1.5 rounded-xl border bg-background/95 px-3 py-2 text-sm shadow-lg backdrop-blur">
        <div className="flex min-w-0 items-center gap-1.5">
          {icon}
          <span className="truncate text-xs font-medium text-muted-foreground">{title}</span>
          {counter && (
            <span data-testid="walkthrough-step-counter" className="shrink-0 text-xs text-muted-foreground tabular-nums">
              {counter}
            </span>
          )}
          <div className="ml-auto flex shrink-0 items-center gap-0.5">
            {controls && (
              <>
                <IconButton label="Restart" onClick={replayWalkthrough}>
                  <RotateCcwIcon />
                </IconButton>
                <IconButton label="Previous step (←)" onClick={() => controls.go(controls.index - 1)} disabled={controls.index === 0}>
                  <ChevronLeftIcon />
                </IconButton>
                <IconButton label={playing ? "Pause (space)" : "Play (space)"} onClick={toggle}>
                  {playing ? <PauseIcon /> : <PlayIcon />}
                </IconButton>
                <IconButton label="Next step (→)" onClick={() => controls.go(controls.index + 1)} disabled={controls.nextDisabled}>
                  <ChevronRightIcon />
                </IconButton>
              </>
            )}
            <SpeedButton />
            <IconButton label="Close (Esc)" onClick={closeWalkthrough}>
              <XIcon />
            </IconButton>
          </div>
        </div>
        {children}
        {controls?.hint && <p className="text-xs text-muted-foreground">{controls.hint}</p>}
      </div>
    </Panel>
  )
}

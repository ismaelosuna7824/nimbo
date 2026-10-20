import { lazy, Suspense, useEffect } from "react"
import { Kbd, KbdGroup } from "@/components/ui/kbd"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Canvas } from "@/features/canvas/canvas"
import { ExportImageDialog } from "./export-dialog"
import { switchView, useCanvasView, VIEW_SHORTCUT, type CanvasView } from "./store"

// The diagram view (and its icon set) loads the first time someone opens it.
const ArchitectureView = lazy(() => import("./view").then((m) => ({ default: m.ArchitectureView })))

/** The editor's canvas area: the Build canvas or the Architecture diagram, with the switch between them. */
export function CanvasArea() {
  const view = useCanvasView()

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "a" && (e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey) {
        e.preventDefault()
        switchView()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  return (
    <div className="relative h-full w-full" data-canvas-view={view}>
      {view === "build" ? (
        <Canvas />
      ) : (
        <Suspense
          fallback={
            <div className="flex h-full items-center justify-center gap-2 bg-white text-sm text-slate-500">
              <Spinner />
              Drawing the architecture…
            </div>
          }
        >
          <ArchitectureView />
        </Suspense>
      )}
      <ViewSwitch view={view} />
      <ExportImageDialog />
    </div>
  )
}

function ViewSwitch({ view }: { view: CanvasView }) {
  return (
    <div className="absolute top-[15px] left-[15px] z-10">
      <Tooltip>
        <TooltipTrigger asChild>
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            value={view}
            onValueChange={(v) => v && switchView(v as CanvasView)}
            aria-label="Canvas view"
            className="bg-background shadow-xs"
          >
            <ToggleGroupItem value="build">Build</ToggleGroupItem>
            <ToggleGroupItem value="architecture">Architecture</ToggleGroupItem>
          </ToggleGroup>
        </TooltipTrigger>
        <TooltipContent side="bottom" className="max-w-none gap-3">
          <span className="whitespace-nowrap">Build edits the design; Architecture draws a shareable diagram</span>
          <KbdGroup className="shrink-0">
            {VIEW_SHORTCUT.map((k) => (
              <Kbd key={k}>{k}</Kbd>
            ))}
          </KbdGroup>
        </TooltipContent>
      </Tooltip>
    </div>
  )
}

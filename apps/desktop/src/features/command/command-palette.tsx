import { lazy, Suspense, useEffect } from "react"
import { CommandDialog } from "@/components/ui/command"
import { useUiStore } from "../toolbar/ui-store"

// The body (every command, its icons and the actions it reaches) is a chunk of its own, off the startup path.
const PaletteBody = lazy(() => import("./palette-body").then((m) => ({ default: m.PaletteBody })))

export function CommandPalette() {
  const open = useUiStore((s) => s.commandOpen)
  const setOpen = useUiStore((s) => s.setCommandOpen)

  useEffect(() => {
    // Fetched after the first paint, so the first ⌘K opens a filled palette.
    void import("./palette-body")
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        useUiStore.getState().setCommandOpen(!useUiStore.getState().commandOpen)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  return (
    <CommandDialog
      open={open}
      onOpenChange={setOpen}
      title="Command palette"
      description="Search resources, actions and workflows"
      className="sm:max-w-xl"
    >
      <Suspense fallback={null}>
        <PaletteBody />
      </Suspense>
    </CommandDialog>
  )
}

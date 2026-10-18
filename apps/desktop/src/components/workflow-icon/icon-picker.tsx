import { SearchIcon } from "lucide-react"
import { useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { setWorkflowIcon } from "@/features/toolbar/workflow-actions"
import type { WorkflowIcon } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { DEFAULT_TILE_COLOR, TILE_COLORS, tileColorById } from "./colors"
import { GLYPHS, glyphById, MASCOT_GLYPH } from "./glyphs"
import { WorkflowIconTile } from "./workflow-icon-tile"

export interface IconTarget {
  id: string
  name: string
  icon?: WorkflowIcon | null
}

const COLUMNS = 6

/** Picks a workflow's pixel icon and tile color; open while `workflow` is set. */
export function WorkflowIconDialog({ workflow, onClose }: { workflow: IconTarget | null; onClose: () => void }) {
  return (
    <Dialog open={workflow !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-md">
        {workflow && <IconPickerForm key={workflow.id} workflow={workflow} onDone={onClose} />}
      </DialogContent>
    </Dialog>
  )
}

function IconPickerForm({ workflow, onDone }: { workflow: IconTarget; onDone: () => void }) {
  const current = workflow.icon
  /** null = the Nimbo mascot (explicit MASCOT_GLYPH, or a glyph this build doesn't know). */
  const [glyph, setGlyph] = useState<string | null>(current && glyphById(current.glyph) ? current.glyph : null)
  const [color, setColor] = useState((current && tileColorById(current.color)?.id) ?? DEFAULT_TILE_COLOR.id)
  const [query, setQuery] = useState("")
  const buttons = useRef<(HTMLButtonElement | null)[]>([])

  const q = query.trim().toLowerCase()
  const visible = q ? GLYPHS.filter((g) => `${g.id} ${g.label} ${g.keywords}`.toLowerCase().includes(q)) : GLYPHS
  const selectedIndex = visible.findIndex((g) => g.id === glyph)
  const tabStop = Math.max(selectedIndex, 0)
  const value: WorkflowIcon = { glyph: glyph ?? MASCOT_GLYPH, color }

  const save = (icon: WorkflowIcon) => {
    void setWorkflowIcon(workflow.id, icon)
    onDone()
  }

  const onGridKeyDown = (e: React.KeyboardEvent) => {
    const from = buttons.current.findIndex((b) => b === document.activeElement)
    if (from < 0) return
    const last = visible.length - 1
    const moves: Partial<Record<string, number>> = {
      ArrowRight: Math.min(from + 1, last),
      ArrowLeft: Math.max(from - 1, 0),
      ArrowDown: Math.min(from + COLUMNS, last),
      ArrowUp: Math.max(from - COLUMNS, 0),
      Home: 0,
      End: last,
    }
    const to = Object.hasOwn(moves, e.key) ? moves[e.key] : undefined
    if (to === undefined) return
    e.preventDefault()
    buttons.current[to]?.focus()
  }

  return (
    <>
      <DialogHeader>
        <DialogTitle>Workflow icon</DialogTitle>
        <DialogDescription>Pick a pixel icon and a color for “{workflow.name}”.</DialogDescription>
      </DialogHeader>

      <div className="flex items-center gap-3">
        <WorkflowIconTile icon={value} size={64} />
        <div className="flex min-w-0 flex-1 flex-col gap-1.5">
          <span className="truncate text-sm font-medium" aria-live="polite">
            {glyph ? glyphById(glyph)?.label : "Nimbo mascot"}
          </span>
          <Button variant="outline" size="sm" className="self-start" disabled={!glyph} onClick={() => setGlyph(null)}>
            Use Nimbo mascot
          </Button>
        </div>
      </div>

      <ToggleGroup
        type="single"
        size="sm"
        spacing={1}
        value={color}
        onValueChange={(v) => v && setColor(v)}
        aria-label="Tile color"
        className="flex-wrap"
      >
        {TILE_COLORS.map((c) => (
          <ToggleGroupItem
            key={c.id}
            value={c.id}
            aria-label={c.label}
            title={c.label}
            className="size-8 rounded-md p-0 data-[state=on]:ring-2 data-[state=on]:ring-ring"
          >
            <span
              className="size-5 rounded-[5px] border"
              style={{ borderColor: c.base, backgroundImage: `linear-gradient(160deg, ${c.base}, ${c.dark})` }}
            />
          </ToggleGroupItem>
        ))}
      </ToggleGroup>

      <InputGroup>
        <InputGroupAddon>
          <SearchIcon />
        </InputGroupAddon>
        <InputGroupInput value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search icons…" aria-label="Search icons" />
      </InputGroup>

      <div
        role="listbox"
        aria-label="Icons"
        className="grid max-h-80 grid-cols-6 gap-1.5 overflow-y-auto p-0.5"
        onKeyDown={onGridKeyDown}
      >
        {visible.map((g, i) => (
          <button
            key={g.id}
            ref={(el) => {
              buttons.current[i] = el
            }}
            type="button"
            role="option"
            aria-selected={g.id === glyph}
            aria-label={g.label}
            title={g.label}
            tabIndex={i === tabStop ? 0 : -1}
            onClick={() => setGlyph(g.id)}
            onDoubleClick={() => save({ glyph: g.id, color })}
            className={cn(
              "flex aspect-square items-center justify-center rounded-lg border border-transparent outline-none transition-colors hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring",
              g.id === glyph && "border-primary bg-accent",
            )}
          >
            <WorkflowIconTile icon={{ glyph: g.id, color }} size={40} />
          </button>
        ))}
        {visible.length === 0 && (
          <p className="col-span-6 py-6 text-center text-sm text-muted-foreground">No icons match “{query.trim()}”.</p>
        )}
      </div>

      <DialogFooter>
        <DialogClose asChild>
          <Button variant="outline">Cancel</Button>
        </DialogClose>
        <Button onClick={() => save(value)}>Save</Button>
      </DialogFooter>
    </>
  )
}

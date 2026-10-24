import { PlusIcon, XIcon } from "lucide-react"
import { useState } from "react"
import { ResourceIcon } from "@/components/resource-icon"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { Attr, Value } from "@/lib/api/types"
import { nodeLabel, schemaKey } from "../canvas/catalog"
import { mapValue, resolveReference } from "./body"
import { useInspector } from "./context"
import { ReferencePicker } from "./reference-picker"

type Row = { key: string; value: Value }

const complete = (rows: Row[]): Attr[] => rows.filter((r) => r.key.trim()).map((r) => ({ name: r.key.trim(), value: r.value }))

/**
 * A map of strings (tags, dimensions, app settings, metadata) as rows: a key, and a typed text or a
 * Link to… pick (var./local., another node's attribute) per value. Values holding an expression are
 * kept untouched and shown read-only, with an X that turns them back into text. A `user_data` entry's
 * Link to… offers Base64-encode.
 */
export function MapField({ entries, onChange }: { entries: Attr[]; onChange: (v: Value | undefined) => void }) {
  const [rows, setRows] = useState<Row[]>(() => entries.map((e) => ({ key: e.name, value: e.value })))
  const external = JSON.stringify(entries)
  const [seen, setSeen] = useState(external)
  if (seen !== external) {
    setSeen(external)
    if (JSON.stringify(complete(rows)) !== external) setRows(entries.map((e) => ({ key: e.name, value: e.value })))
  }
  const emit = (next: Row[]) => {
    setRows(next)
    onChange(mapValue(complete(next)))
  }
  const setRow = (i: number, row: Partial<Row>) => emit(rows.map((x, j) => (j === i ? { ...x, ...row } : x)))

  return (
    <div className="flex flex-col gap-1.5" data-testid="map-field">
      {rows.map((r, i) => (
        <div key={i} className="flex items-center gap-1.5">
          <Input
            value={r.key}
            placeholder="key"
            className="h-7 flex-1 font-mono text-xs"
            onChange={(e) => setRow(i, { key: e.target.value })}
          />
          {r.value.expr !== undefined ? (
            <ExprValue expr={r.value.expr} onUnlink={() => setRow(i, { value: { literal: "" } })} />
          ) : (
            <Input
              value={r.value.literal === undefined || r.value.literal === null ? "" : String(r.value.literal)}
              placeholder="value"
              className="h-7 flex-[1.4] text-xs"
              onChange={(e) => setRow(i, { value: { literal: e.target.value } })}
            />
          )}
          <ReferencePicker target="string" base64={r.key.trim() === "user_data"} onPick={(expr) => setRow(i, { value: { expr } })} />
          <Button variant="ghost" size="icon-xs" onClick={() => emit(rows.filter((_, j) => j !== i))} aria-label="Remove entry">
            <XIcon />
          </Button>
        </div>
      ))}
      <Button variant="outline" size="xs" className="self-start" onClick={() => setRows([...rows, { key: "", value: { literal: "" } }])}>
        <PlusIcon />
        Add entry
      </Button>
    </div>
  )
}

/** A map value holding an expression: a chip for a reference to another node, read-only text otherwise. */
function ExprValue({ expr, onUnlink }: { expr: string; onUnlink: () => void }) {
  const { nodes, schemas, select } = useInspector()
  const ref = resolveReference(expr, nodes)
  const schema = ref ? schemas.get(schemaKey(ref.node.kind, ref.node.type)) : undefined
  return (
    <span className="inline-flex h-7 min-w-0 flex-[1.4] items-center gap-1 rounded-md border border-primary/30 bg-primary/8 px-2 text-xs">
      {ref ? (
        <button type="button" onClick={() => select(ref.node.id)} className="inline-flex min-w-0 items-center gap-1 hover:underline">
          <ResourceIcon name={schema?.icon} className="size-3 shrink-0" />
          <span className="truncate font-medium">{nodeLabel(ref.node, schema)}</span>
          {ref.attr && <span className="shrink-0 font-mono text-muted-foreground">.{ref.attr}</span>}
        </button>
      ) : (
        <span className="truncate font-mono" title={expr}>
          {expr}
        </span>
      )}
      <button
        type="button"
        onClick={onUnlink}
        className="ml-auto flex size-4 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"
        aria-label="Unlink"
      >
        <XIcon className="size-3" />
      </button>
    </span>
  )
}

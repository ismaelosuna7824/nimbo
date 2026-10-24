import { PlusIcon, Trash2Icon } from "lucide-react"
import { Button } from "@/components/ui/button"
import type { Block, Body, SchemaAttribute } from "@/lib/api/types"
import { AttrField } from "./attr-field"
import { blocksOf, emptyBody, getAttr, setAttr, setBlocks } from "./body"
import { objectListFields, objectListFromValue, objectListToValue } from "./object-list"

/**
 * A list(object)/set(object) attribute (security group rules, NSG rules, …) as cards, one field per
 * object attribute. Items written as blocks (`ingress { … }`) stay blocks and a new card adds one; a
 * value of object constructors is edited in place; any other expression keeps the expression box.
 */
export function ObjectListField({
  attr,
  body,
  onChange,
  path,
}: {
  attr: SchemaAttribute
  body: Body
  onChange: (b: Body) => void
  path: string
}) {
  const fields = objectListFields(attr.type) ?? []
  const value = getAttr(body, attr.name)
  const blocks = blocksOf(body, attr.name)
  const fromValue = value !== undefined ? objectListFromValue(value) : null
  if (value !== undefined && fromValue === null) {
    return <AttrField attr={attr} value={value} path={path} onChange={(v) => onChange(setAttr(body, attr.name, v))} />
  }
  const items = fromValue ?? blocks.map((b) => b.body)
  const write = (next: Body[]) => {
    if (fromValue) {
      onChange(setAttr(body, attr.name, objectListToValue(next)))
      return
    }
    const out: Block[] = next.map((b, i) => (i < blocks.length ? { ...blocks[i], body: b } : { type: attr.name, body: b }))
    onChange(setBlocks(body, attr.name, out))
  }
  const removeAt = (i: number) => {
    if (fromValue) {
      write(items.filter((_, j) => j !== i))
      return
    }
    onChange(setBlocks(body, attr.name, blocks.filter((_, j) => j !== i)))
  }
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-dashed p-2.5" data-testid={`object-list-${attr.name}`}>
      <div className="flex items-center gap-2">
        <span className="min-w-0 truncate font-mono text-xs font-medium">{attr.name}</span>
        {attr.required && <span className="text-[10px] text-destructive">required</span>}
        <span className="text-[10px] text-muted-foreground">{items.length}</span>
        <Button variant="ghost" size="xs" className="ml-auto" onClick={() => write([...items, emptyBody()])} data-testid={`object-list-add-${attr.name}`}>
          <PlusIcon />
          Add
        </Button>
      </div>
      {items.map((item, i) => (
        <div key={i} className="flex flex-col gap-3 rounded-md border bg-muted/30 p-2.5">
          <div className="flex items-center gap-2">
            <span className="text-[11px] text-muted-foreground">
              {attr.name} #{i + 1}
            </span>
            <Button variant="ghost" size="icon-xs" className="ml-auto" onClick={() => removeAt(i)} aria-label={`Remove ${attr.name} #${i + 1}`}>
              <Trash2Icon />
            </Button>
          </div>
          {fields.map((f) => (
            <AttrField
              key={f.name}
              attr={f}
              path={`${path}[${i}].${f.name}`}
              value={getAttr(item, f.name)}
              onChange={(v) => write(items.map((b, j) => (j === i ? setAttr(b, f.name, v) : b)))}
            />
          ))}
        </div>
      ))}
    </div>
  )
}

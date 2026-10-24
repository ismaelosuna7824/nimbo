import { ChevronRightIcon, LinkIcon, VariableIcon } from "lucide-react"
import { useState } from "react"
import { ResourceIcon } from "@/components/resource-icon"
import { Button } from "@/components/ui/button"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Switch } from "@/components/ui/switch"
import type { CtyType } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { nodeAddress, nodeLabel, schemaKey } from "../canvas/catalog"
import { base64Of, ctyLabel } from "./body"
import { useInspector } from "./context"
import { instanceKeys, linkableAttributes, nestedReferences, refFit, scopeReferences, type RefOption } from "./references"

/**
 * Link to…: another node's attribute (nested block attributes and list elements behind "More…"; one
 * entry per instance of count/for_each nodes), an input variable or a local. With `target` (the
 * field's type) only what fits is listed; with `base64` (user data) a "Base64-encode" switch wraps
 * the pick in base64encode(). `onPick` receives the expression and its type.
 */
export function ReferencePicker({
  onPick,
  className,
  target,
  base64,
}: {
  onPick: (expr: string, type: CtyType) => void
  className?: string
  target?: CtyType
  base64?: boolean
}) {
  const { nodes, nodeId, schemas, variables, locals } = useInspector()
  const [open, setOpen] = useState(false)
  const [encode, setEncode] = useState(false)
  const [expanded, setExpanded] = useState<string[]>([])
  const encoding = base64 === true && encode
  const fits = (o: RefOption) => (encoding ? refFit("string", o.type) !== null : target === undefined || refFit(target, o.type) !== null)
  const scope = scopeReferences(variables, locals).filter(fits)
  const others = nodes.filter((n) => n.id !== nodeId && n.kind !== "module")
  if (others.length === 0 && scope.length === 0) return null
  const pick = (o: RefOption, expr: string) => {
    if (encoding) onPick(base64Of(expr), "string")
    else onPick(expr, o.type)
    setOpen(false)
  }
  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (!o) setExpanded([])
      }}
    >
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="xs"
          className={cn("h-5 gap-1 px-1.5 text-[11px] text-muted-foreground", className)}
          data-testid="link-to"
        >
          <LinkIcon className="size-3" />
          Link to…
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0" align="end">
        {base64 && (
          <label className="flex items-center gap-2 border-b px-3 py-2 text-xs">
            <Switch checked={encode} onCheckedChange={setEncode} aria-label="Base64-encode" data-testid="link-base64" />
            <span>
              Base64-encode <span className="text-muted-foreground">(user data that must be base64)</span>
            </span>
          </label>
        )}
        <Command>
          <CommandInput placeholder="Search resources, attributes and variables…" />
          <CommandList className="max-h-80">
            <CommandEmpty>Nothing to link to.</CommandEmpty>
            {scope.length > 0 && (
              <CommandGroup
                heading={
                  <span className="flex items-center gap-1.5">
                    <VariableIcon className="size-3" />
                    Variables and locals
                  </span>
                }
              >
                {scope.map((o) => (
                  <CommandItem key={o.path} value={o.path} onSelect={() => pick(o, o.path)} data-testid={`link-option-${o.path}`}>
                    <span className="font-mono text-xs">{o.path}</span>
                    <span className="ml-auto font-mono text-[10px] text-muted-foreground">{ctyLabel(o.type)}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            {others.map((n) => {
              const schema = schemas.get(schemaKey(n.kind, n.type))
              const addr = nodeAddress(n)
              const instances = instanceKeys(n.body, variables, locals) ?? [""]
              const attrs = linkableAttributes(schema?.block).filter(fits)
              const nested = nestedReferences(schema?.block).filter(fits)
              const more = expanded.includes(n.id)
              if (instances.length === 0 || (attrs.length === 0 && nested.length === 0)) return null
              const shown = instances.flatMap((inst) => [...attrs, ...(more ? nested : [])].map((o) => ({ o, path: `${inst}.${o.path}`.replace(/^\./, "") })))
              return (
                <CommandGroup
                  key={n.id}
                  heading={
                    <span className="flex items-center gap-1.5">
                      <ResourceIcon name={schema?.icon} className="size-3" />
                      {nodeLabel(n, schema)}
                      <span className="truncate font-mono text-[10px] opacity-70">{addr}</span>
                    </span>
                  }
                >
                  {shown.map(({ o, path }) => {
                    const expr = path.startsWith("[") ? `${addr}${path}` : `${addr}.${path}`
                    return (
                      <CommandItem
                        key={path}
                        value={`${nodeLabel(n, schema)} ${expr}`}
                        onSelect={() => pick(o, expr)}
                        data-testid={`link-option-${expr}`}
                      >
                        <span className="truncate font-mono text-xs">{path}</span>
                      </CommandItem>
                    )
                  })}
                  {nested.length > 0 && !more && (
                    <CommandItem
                      value={`${nodeLabel(n, schema)} ${addr} more nested`}
                      onSelect={() => setExpanded([...expanded, n.id])}
                      className="text-muted-foreground"
                      data-testid={`link-more-${addr}`}
                    >
                      <ChevronRightIcon className="size-3" />
                      <span className="text-xs">More… ({nested.length * instances.length} nested)</span>
                    </CommandItem>
                  )}
                </CommandGroup>
              )
            })}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

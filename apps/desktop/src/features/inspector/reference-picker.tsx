import { LinkIcon } from "lucide-react"
import { useState } from "react"
import { ResourceIcon } from "@/components/resource-icon"
import { Button } from "@/components/ui/button"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { ResourceSchema } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { nodeAddress, nodeLabel, schemaKey } from "../canvas/catalog"
import { useInspector } from "./context"

const PREFERRED = [
  "arn",
  "id",
  "name",
  "url",
  "invoke_arn",
  "qualified_arn",
  "endpoint",
  "function_name",
  "bucket",
  "queue_url",
  "execution_arn",
  "api_endpoint",
  "self_link",
  "primary_connection_string",
]

/** Attributes worth linking to: well-known outputs first, then everything the provider computes. */
function linkableAttributes(schema: ResourceSchema | undefined) {
  if (!schema) return []
  const attrs = schema.block.attributes
  const preferred = PREFERRED.filter((p) => attrs.some((a) => a.name === p))
  const computed = attrs
    .filter((a) => a.computed && !PREFERRED.includes(a.name))
    .map((a) => a.name)
    .sort()
  return [...preferred, ...computed]
}

export function ReferencePicker({ onPick, className }: { onPick: (expr: string) => void; className?: string }) {
  const { nodes, nodeId, schemas } = useInspector()
  const [open, setOpen] = useState(false)
  const others = nodes.filter((n) => n.id !== nodeId && n.kind !== "module")
  if (others.length === 0) return null
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="xs"
          className={cn("h-5 gap-1 px-1.5 text-[11px] text-muted-foreground", className)}
        >
          <LinkIcon className="size-3" />
          Link to…
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0" align="end">
        <Command>
          <CommandInput placeholder="Search resources and attributes…" />
          <CommandList className="max-h-80">
            <CommandEmpty>Nothing to link to.</CommandEmpty>
            {others.map((n) => {
              const schema = schemas.get(schemaKey(n.kind, n.type))
              const addr = nodeAddress(n)
              const attrs = linkableAttributes(schema)
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
                  {attrs.map((a) => (
                    <CommandItem
                      key={a}
                      value={`${nodeLabel(n, schema)} ${addr}.${a}`}
                      onSelect={() => {
                        onPick(`${addr}.${a}`)
                        setOpen(false)
                      }}
                    >
                      <span className="font-mono text-xs">{a}</span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              )
            })}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

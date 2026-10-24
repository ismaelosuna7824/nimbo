import { PlusIcon, XIcon } from "lucide-react"
import { useState } from "react"
import { ResourceIcon } from "@/components/resource-icon"
import { Button } from "@/components/ui/button"
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import type { Body, NamedBlock, SchemaAttribute, WorkflowNode } from "@/lib/api/types"
import { nodeAddress, nodeLabel, schemaKey } from "../canvas/catalog"
import { AttrField } from "./attr-field"
import { blocksOf, emptyBody, getAttr, setAttr, setBlocks } from "./body"
import { useInspector } from "./context"
import { ExpressionInput } from "./widgets"

export const META_ATTRS = new Set(["count", "for_each", "depends_on", "provider", "providers", "source", "version"])

const COUNT: SchemaAttribute = {
  name: "count",
  type: "number",
  optional: true,
  description: "Create this many identical copies. Conflicts with for_each.",
}
const FOR_EACH: SchemaAttribute = {
  name: "for_each",
  type: "dynamic",
  optional: true,
  description: "Create one copy per element of a map or set of strings (each.key / each.value).",
}
const LIFECYCLE: SchemaAttribute[] = [
  { name: "create_before_destroy", type: "bool", optional: true, description: "Create the replacement before destroying the old object." },
  { name: "prevent_destroy", type: "bool", optional: true, description: "Reject any plan that would destroy this resource." },
  { name: "ignore_changes", type: "dynamic", optional: true, description: "Attributes whose drift Terraform should ignore, e.g. [tags]." },
  { name: "replace_triggered_by", type: "dynamic", optional: true, description: "Replace this resource when any of these references change." },
]

function parseDependsOn(expr: string | undefined) {
  if (!expr) return []
  return expr
    .trim()
    .replace(/^\[|\]$/g, "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
}

function providerAliases(providers: NamedBlock[], typePrefix: string) {
  return providers.flatMap((p) => {
    const alias = getAttr(p.body, "alias")?.literal
    return typeof alias === "string" && typePrefix.startsWith(`${p.name}_`) ? [`${p.name}.${alias}`] : []
  })
}

export function MetaArguments({
  node,
  providers,
  onChange,
}: {
  node: WorkflowNode
  providers: NamedBlock[]
  onChange: (b: Body) => void
}) {
  const body = node.body
  const aliases = providerAliases(providers, node.type)
  const provider = getAttr(body, "provider")?.expr
  const lifecycle = blocksOf(body, "lifecycle")[0]
  const lifecycleBody = lifecycle?.body ?? emptyBody()
  const setLifecycle = (b: Body) =>
    onChange(setBlocks(body, "lifecycle", b.attrs.length || b.blocks.length ? [{ type: "lifecycle", body: b }] : []))

  return (
    <div className="flex flex-col gap-4">
      <AttrField attr={COUNT} value={getAttr(body, "count")} onChange={(v) => onChange(setAttr(body, "count", v))} />
      <AttrField attr={FOR_EACH} value={getAttr(body, "for_each")} onChange={(v) => onChange(setAttr(body, "for_each", v))} />
      <DependsOn node={node} onChange={onChange} />
      {node.kind !== "module" && (
        <div className="flex flex-col gap-1.5">
          <span className="font-mono text-xs font-medium">provider</span>
          <Select
            value={provider ?? "__default"}
            onValueChange={(v) => onChange(setAttr(body, "provider", v === "__default" ? undefined : { expr: v }))}
          >
            <SelectTrigger className="h-8 w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__default">Default provider</SelectItem>
              {[...new Set([...aliases, ...(provider ? [provider] : [])])].map((a) => (
                <SelectItem key={a} value={a}>
                  {a}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {aliases.length === 0 && (
            <p className="text-[11px] text-muted-foreground">
              Add a provider with an alias (Providers, in the workflow overview: click the canvas background) to target another region or account.
            </p>
          )}
        </div>
      )}
      {node.kind === "resource" && (
        <div className="flex flex-col gap-3 rounded-lg border border-dashed p-2.5">
          <span className="font-mono text-xs font-medium">lifecycle</span>
          {LIFECYCLE.map((a) => (
            <AttrField
              key={a.name}
              attr={a}
              path={`lifecycle.${a.name}`}
              value={getAttr(lifecycleBody, a.name)}
              onChange={(v) => setLifecycle(setAttr(lifecycleBody, a.name, v))}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function DependsOn({ node, onChange }: { node: WorkflowNode; onChange: (b: Body) => void }) {
  const { nodes, schemas, select } = useInspector()
  const [open, setOpen] = useState(false)
  const current = parseDependsOn(getAttr(node.body, "depends_on")?.expr)
  const write = (list: string[]) =>
    onChange(setAttr(node.body, "depends_on", list.length ? { expr: `[${list.join(", ")}]` } : undefined))
  const candidates = nodes.filter((n) => n.id !== node.id && !current.includes(nodeAddress(n)))
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center">
        <span className="font-mono text-xs font-medium">depends_on</span>
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <Button variant="ghost" size="xs" className="ml-auto h-5 text-[11px] text-muted-foreground" disabled={!candidates.length}>
              <PlusIcon />
              Add
            </Button>
          </PopoverTrigger>
          <PopoverContent className="w-72 p-0" align="end">
            <Command>
              <CommandInput placeholder="Wait for…" />
              <CommandList>
                <CommandEmpty>No resources.</CommandEmpty>
                {candidates.map((n) => {
                  const schema = schemas.get(schemaKey(n.kind, n.type))
                  return (
                    <CommandItem
                      key={n.id}
                      value={`${nodeLabel(n, schema)} ${nodeAddress(n)}`}
                      onSelect={() => {
                        write([...current, nodeAddress(n)])
                        setOpen(false)
                      }}
                    >
                      <ResourceIcon name={schema?.icon} />
                      <span className="truncate">{nodeLabel(n, schema)}</span>
                      <span className="ml-auto truncate font-mono text-[10px] text-muted-foreground">{nodeAddress(n)}</span>
                    </CommandItem>
                  )
                })}
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
      </div>
      {current.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">Only needed for hidden dependencies Terraform can't infer from references.</p>
      ) : (
        <div className="flex flex-wrap gap-1">
          {current.map((addr) => {
            const target = nodes.find((n) => nodeAddress(n) === addr)
            const schema = target && schemas.get(schemaKey(target.kind, target.type))
            return (
              <span key={addr} className="inline-flex h-6 items-center gap-1 rounded-md border px-1.5 text-xs">
                <button type="button" className="inline-flex items-center gap-1" onClick={() => target && select(target.id)}>
                  <ResourceIcon name={schema?.icon} className="size-3" />
                  {target ? nodeLabel(target, schema) : <span className="font-mono">{addr}</span>}
                </button>
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground"
                  onClick={() => write(current.filter((a) => a !== addr))}
                  aria-label={`Remove ${addr}`}
                >
                  <XIcon className="size-3" />
                </button>
              </span>
            )
          })}
        </div>
      )}
    </div>
  )
}

/** Attributes without a schema (module inputs, or attributes the provider schema doesn't know) edited as expressions. */
export function FreeformAttrs({
  body,
  onChange,
  exclude,
  addLabel,
}: {
  body: Body
  onChange: (b: Body) => void
  exclude: Set<string>
  addLabel: string
}) {
  const [name, setName] = useState("")
  const [expr, setExpr] = useState("")
  const attrs = (body.attrs ?? []).filter((a) => !exclude.has(a.name))
  const validName = /^[a-zA-Z_][a-zA-Z0-9_-]*$/.test(name) && !getAttr(body, name)
  return (
    <div className="flex flex-col gap-3">
      {attrs.map((a) => (
        <div key={a.name} className="flex flex-col gap-1">
          <div className="flex items-center">
            <span className="font-mono text-xs font-medium">{a.name}</span>
            <Button
              variant="ghost"
              size="icon-xs"
              className="ml-auto"
              onClick={() => onChange(setAttr(body, a.name, undefined))}
              aria-label={`Remove ${a.name}`}
            >
              <XIcon />
            </Button>
          </div>
          <ExpressionInput
            value={a.value.expr ?? JSON.stringify(a.value.literal ?? null)}
            onCommit={(e) => onChange(setAttr(body, a.name, e.trim() ? { expr: e.trim() } : undefined))}
            rows={1}
          />
        </div>
      ))}
      <div className="flex flex-col gap-1.5 rounded-lg border border-dashed p-2">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="name" className="h-7 font-mono text-xs" />
        <Textarea
          value={expr}
          onChange={(e) => setExpr(e.target.value)}
          rows={1}
          spellCheck={false}
          className="min-h-0 font-mono text-xs"
          placeholder='value, e.g. "10.0.0.0/16" or var.region'
        />
        <Button
          size="xs"
          variant="outline"
          className="self-start"
          disabled={!validName || !expr.trim()}
          onClick={() => {
            onChange(setAttr(body, name, { expr: expr.trim() }))
            setName("")
            setExpr("")
          }}
        >
          <PlusIcon />
          {addLabel}
        </Button>
      </div>
    </div>
  )
}

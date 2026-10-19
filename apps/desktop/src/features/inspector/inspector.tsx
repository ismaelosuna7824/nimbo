import {
  BookOpenIcon,
  CircleAlertIcon,
  CloudOffIcon,
  MousePointerClickIcon,
  PackageOpenIcon,
  PlugIcon,
  RadioIcon,
  SearchIcon,
  Trash2Icon,
  TriangleAlertIcon,
  ZapIcon,
} from "lucide-react"
import { lazy, Suspense, useMemo, useState } from "react"
import { toast } from "sonner"
import { CloudBadge } from "@/components/cloud"
import { cloudOfType, CLOUD_META } from "@/components/cloud-meta"
import { errorMessage } from "@/components/errors"
import { useSyncedState } from "@/components/use-synced-state"
import { openExternal } from "@/components/open-external"
import { ResourceIcon } from "@/components/resource-icon"
import { WorkflowIconDialog } from "@/components/workflow-icon/icon-picker"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import { isNetworkFinding, type Body, type ResourceSchema, type SchemaAttribute, type WorkflowNode } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { canvasHandle } from "../canvas/actions"
import { openModuleView } from "../canvas/module-view-store"
import { nodeAddress, nodeLabel, useNodeSchemas, useSchema } from "../canvas/catalog"
import { NetworkSection } from "../network/network-section"
import { useEndpoints } from "../panels/endpoints/use-endpoints"
import { AttrField } from "./attr-field"
import { BlockForm } from "./block-form"
import { getAttr, setAttr } from "./body"
import { InspectorContext, type InspectorContextValue } from "./context"
import { FileField } from "./file-field"
import { LiveValues } from "./live-values"
import { FreeformAttrs, META_ATTRS, MetaArguments } from "./meta-arguments"

// Snippet highlighting (CodeMirror parsers) loads only for deployed resources.
const CompactResourceCard = lazy(() =>
  import("../panels/endpoints/resource-card").then((m) => ({ default: m.CompactResourceCard })),
)

const NAME_RE = /^[a-zA-Z_][a-zA-Z0-9_-]*$/

const MODULE_SOURCE: SchemaAttribute = {
  name: "source",
  type: "string",
  required: true,
  description: "Where the module lives: a registry address (terraform-aws-modules/vpc/aws), a git URL or a local path.",
}
const MODULE_VERSION: SchemaAttribute = {
  name: "version",
  type: "string",
  optional: true,
  description: "Version constraint for registry modules, e.g. ~> 5.0.",
}

export function Inspector() {
  const hasWorkflow = useWorkflowStore((s) => s.workflow !== null)
  const nodes = useWorkflowStore((s) => s.workflow?.nodes)
  const allDiagnostics = useWorkflowStore((s) => s.workflow?.diagnostics)
  const selectedNodeId = useWorkflowStore((s) => s.selectedNodeId)
  const mode = useWorkflowStore((s) => s.mode)
  const select = useWorkflowStore((s) => s.select)
  const schemas = useNodeSchemas(nodes)
  const node = nodes?.find((n) => n.id === selectedNodeId)

  const ctx = useMemo<InspectorContextValue | null>(
    () =>
      nodes && node
        ? {
            nodeId: node.id,
            mode,
            nodes,
            schemas,
            diagnostics: (allDiagnostics ?? []).filter((d) => d.nodeId === node.id),
            select,
          }
        : null,
    [nodes, allDiagnostics, node, mode, schemas, select],
  )

  if (!hasWorkflow) return null
  if (!node || !ctx) return <WorkflowOverview />
  return (
    <InspectorContext.Provider value={ctx}>
      <NodeInspector key={node.id} node={node} />
    </InspectorContext.Provider>
  )
}

function NodeInspector({ node }: { node: WorkflowNode }) {
  const allDiagnostics = useWorkflowStore((s) => s.workflow!.diagnostics)
  const providers = useWorkflowStore((s) => s.workflow!.providers)
  const mode = useWorkflowStore((s) => s.mode)
  const update = useWorkflowStore((s) => s.update)
  const deployed = useWorkflowStore((s) => s.deployed)
  const schemaQuery = useSchema(node.kind, node.type)
  const schema = schemaQuery.data
  const [filter, setFilter] = useState("")
  const diagnostics = (allDiagnostics ?? []).filter((d) => d.nodeId === node.id)
  const live = (deployed?.resources ?? []).filter((r) => r.nodeId === node.id)
  const endpoints = useEndpoints()
  const usable = (endpoints.data?.resources ?? []).filter((r) => r.nodeId === node.id)
  const pro = mode === "pro"

  const setBody = (b: Body) =>
    update((d) => ({ ...d, nodes: d.nodes.map((n) => (n.id === node.id ? { ...n, body: b } : n)) }))

  const schemaNames = new Set(schema?.block.attributes.map((a) => a.name) ?? [])
  const unknownAttrs = new Set(
    (node.body.attrs ?? []).filter((a) => !schemaNames.has(a.name) && !META_ATTRS.has(a.name)).map((a) => a.name),
  )

  return (
    <div className="flex h-full flex-col">
      <NodeHeader node={node} schema={schema} />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="flex flex-col gap-5 p-4">
          <NameFields node={node} />
          {node.kind !== "module" && schema && <SupportNote schema={schema} />}
          {diagnostics.filter((d) => !d.attribute && !isNetworkFinding(d)).length > 0 && (
            <div className="flex flex-col gap-1.5">
              {diagnostics
                .filter((d) => !d.attribute && !isNetworkFinding(d))
                .map((d, i) => (
                  <Alert key={i} variant={d.severity === "error" ? "destructive" : "default"}>
                    {d.severity === "error" ? <CircleAlertIcon /> : <TriangleAlertIcon />}
                    <AlertTitle>{d.message}</AlertTitle>
                    {d.detail && <AlertDescription>{d.detail}</AlertDescription>}
                  </Alert>
                ))}
            </div>
          )}

          {usable.length > 0 && (
            <Section title="Use it" icon={<PlugIcon className="size-3.5 text-primary" />}>
              {usable.map((r) => (
                <div key={r.address} className="flex flex-col gap-1.5">
                  {usable.length > 1 && <span className="font-mono text-[11px] text-muted-foreground">{r.address}</span>}
                  <Suspense fallback={<Skeleton className="h-20 w-full" />}>
                    <CompactResourceCard resource={r} />
                  </Suspense>
                </div>
              ))}
            </Section>
          )}

          {live.length > 0 && (
            <Section title="Live" icon={<RadioIcon className="size-3.5 text-sky-500" />}>
              <LiveValues resources={live} />
            </Section>
          )}

          {node.kind === "resource" && <NetworkSection node={node} section={Section} />}

          <Section title="Settings">
            {node.kind === "module" ? (
              <div className="flex flex-col gap-4">
                <AttrField attr={MODULE_SOURCE} value={getAttr(node.body, "source")} onChange={(v) => setBody(setAttr(node.body, "source", v))} />
                <AttrField attr={MODULE_VERSION} value={getAttr(node.body, "version")} onChange={(v) => setBody(setAttr(node.body, "version", v))} />
                <Button variant="outline" size="sm" className="self-start" onClick={() => void openModuleView(node.id)}>
                  <PackageOpenIcon />
                  Open module
                </Button>
                <span className="text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">Inputs</span>
                <FreeformAttrs body={node.body} onChange={setBody} exclude={META_ATTRS} addLabel="Add input" />
              </div>
            ) : schemaQuery.isPending ? (
              <div className="flex flex-col gap-3">
                <Skeleton className="h-12" />
                <Skeleton className="h-12" />
                <Skeleton className="h-12" />
              </div>
            ) : schema ? (
              <div className="flex flex-col gap-4">
                {pro && (
                  <div className="relative">
                    <SearchIcon className="absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                    <Input
                      value={filter}
                      onChange={(e) => setFilter(e.target.value)}
                      placeholder="Filter attributes…"
                      className="h-7 pl-7 text-xs"
                    />
                  </div>
                )}
                <BlockForm schema={schema.block} body={node.body} onChange={setBody} filter={filter} />
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">
                No provider schema for <span className="font-mono">{node.type}</span>; edit its attributes as expressions below.
              </p>
            )}
          </Section>

          {node.kind !== "module" && (unknownAttrs.size > 0 || (!schema && !schemaQuery.isPending)) && (
            <Section title={schema ? "Other attributes" : "Attributes"}>
              <FreeformAttrs
                body={node.body}
                onChange={setBody}
                exclude={new Set([...META_ATTRS, ...schemaNames])}
                addLabel="Add attribute"
              />
            </Section>
          )}

          {pro && (
            <Section title="Meta-arguments">
              <MetaArguments node={node} providers={providers ?? []} onChange={setBody} />
            </Section>
          )}

          {pro && (
            <Section title="Code">
              <FileField node={node} />
            </Section>
          )}
        </div>
      </div>
    </div>
  )
}

function Section({ title, icon, children }: { title: string; icon?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        {icon}
        <h3 className="text-xs font-semibold">{title}</h3>
        <Separator className="flex-1" />
      </div>
      {children}
    </section>
  )
}

function NodeHeader({ node, schema }: { node: WorkflowNode; schema?: ResourceSchema }) {
  const cloud = schema?.cloud ?? cloudOfType(node.type)
  const accent = cloud ? CLOUD_META[cloud].accent : "#a855f7"
  return (
    <div className="flex items-start gap-3 border-b p-4">
      <span
        data-slot="resource-tile"
        className="flex size-10 shrink-0 items-center justify-center rounded-xl"
        style={{ background: `${accent}1f`, color: accent }}
      >
        <ResourceIcon name={node.kind === "module" ? "package" : schema?.icon} className="size-5" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-heading text-base leading-tight font-medium">{nodeLabel(node, schema)}</div>
        <div className="truncate font-mono text-[11px] text-muted-foreground">{nodeAddress(node)}</div>
        <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
          {cloud && <CloudBadge cloud={cloud} />}
          {node.kind !== "resource" && (
            <span className="rounded-full border px-2 text-xs capitalize">{node.kind === "data" ? "Data source" : "Module"}</span>
          )}
        </div>
      </div>
      <div className="flex items-center gap-0.5">
        {schema?.docsUrl && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-sm" onClick={() => void openExternal(schema.docsUrl!)} aria-label="Documentation">
                <BookOpenIcon />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Terraform docs</TooltipContent>
          </Tooltip>
        )}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon-sm" onClick={() => canvasHandle()?.remove(node.id)} aria-label="Delete">
              <Trash2Icon />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Delete (⌫)</TooltipContent>
        </Tooltip>
      </div>
    </div>
  )
}

function NameFields({ node }: { node: WorkflowNode }) {
  const setWorkflow = useWorkflowStore((s) => s.setWorkflow)
  const [name, setName] = useSyncedState(node.name)
  const [label, setLabel] = useSyncedState(node.label ?? "")
  const nameValid = NAME_RE.test(name)

  const patch = async (p: { name?: string; label?: string }) => {
    const store = useWorkflowStore.getState()
    await store.flush()
    try {
      setWorkflow(await api.patchNode(store.workflow!.id, node.id, p))
    } catch (err) {
      toast.error("Couldn't rename", { description: errorMessage(err) })
      setName(node.name)
      setLabel(node.label ?? "")
    }
  }

  return (
    <div className="grid grid-cols-2 gap-3">
      <div className="flex flex-col gap-1.5">
        <label htmlFor="node-label" className="text-xs font-medium">
          Display name
        </label>
        <Input
          id="node-label"
          className="h-8"
          value={label}
          placeholder="Friendly name"
          onChange={(e) => setLabel(e.target.value)}
          onBlur={() => label !== (node.label ?? "") && void patch({ label })}
          onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="node-name" className="text-xs font-medium">
          Terraform name
        </label>
        <Input
          id="node-name"
          className={cn("h-8 font-mono text-xs")}
          value={name}
          aria-invalid={!nameValid || undefined}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => {
            if (!nameValid) setName(node.name)
            else if (name !== node.name) void patch({ name })
          }}
          onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
        />
      </div>
      {!nameValid && (
        <p className="col-span-2 -mt-1 text-[11px] text-destructive">
          Use letters, digits, “_” or “-”, starting with a letter or “_”.
        </p>
      )}
    </div>
  )
}

function SupportNote({ schema }: { schema: ResourceSchema }) {
  return schema.flociSupported ? (
    <div className="flex items-start gap-2 rounded-lg bg-emerald-500/10 px-3 py-2 text-xs text-emerald-700 dark:text-emerald-300">
      <ZapIcon className="mt-0.5 size-3.5 shrink-0" />
      <span>
        Runs locally in your Floci sandbox{schema.service ? ` (${schema.service} service)` : ""}.
        {schema.description ? ` ${schema.description}` : ""}
      </span>
    </div>
  ) : (
    <div className="flex items-start gap-2 rounded-lg bg-muted px-3 py-2 text-xs text-muted-foreground">
      <CloudOffIcon className="mt-0.5 size-3.5 shrink-0" />
      <span>
        Export only: Floci doesn't emulate this service, so local deploys may fail for it. It is still part of the
        Terraform you export for a real cloud.
      </span>
    </div>
  )
}

function WorkflowOverview() {
  const wf = useWorkflowStore((s) => s.workflow)!
  const update = useWorkflowStore((s) => s.update)
  const [description, setDescription] = useSyncedState(wf.description ?? "")
  const [pickingIcon, setPickingIcon] = useState(false)
  const errors = (wf.diagnostics ?? []).filter((d) => d.severity === "error").length
  return (
    <div className="flex h-full flex-col gap-5 overflow-y-auto p-4">
      <div className="flex items-center gap-3">
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label="Change workflow icon"
              onClick={() => setPickingIcon(true)}
              className="shrink-0 rounded-[12px] transition-transform outline-none hover:scale-105 focus-visible:ring-2 focus-visible:ring-ring"
            >
              <WorkflowIconTile icon={wf.icon} size={48} />
            </button>
          </TooltipTrigger>
          <TooltipContent>Change icon</TooltipContent>
        </Tooltip>
        <div className="flex min-w-0 flex-col gap-1.5">
          <h2 className="font-heading text-base font-medium">{wf.name}</h2>
          <div className="flex flex-wrap gap-1.5">
            {(wf.clouds ?? []).map((c) => (
              <CloudBadge key={c} cloud={c} />
            ))}
          </div>
        </div>
      </div>
      <WorkflowIconDialog workflow={pickingIcon ? wf : null} onClose={() => setPickingIcon(false)} />
      <div className="grid grid-cols-3 gap-2 text-center">
        <Stat label="Resources" value={wf.nodes.filter((n) => n.kind === "resource").length} />
        <Stat label="Links" value={(wf.edges ?? []).length} />
        <Stat label="Errors" value={errors} danger={errors > 0} />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="wf-description" className="text-xs font-medium">
          Description
        </label>
        <Textarea
          id="wf-description"
          value={description}
          rows={3}
          placeholder="What does this infrastructure do?"
          onChange={(e) => setDescription(e.target.value)}
          onBlur={() => description !== (wf.description ?? "") && update((d) => ({ ...d, description }))}
        />
      </div>
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <MousePointerClickIcon />
          </EmptyMedia>
          <EmptyTitle>Select a resource</EmptyTitle>
          <EmptyDescription>
            Click a card on the canvas to configure it. Drag between the dots of two cards to connect them.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    </div>
  )
}

function Stat({ label, value, danger }: { label: string; value: number; danger?: boolean }) {
  return (
    <div className="rounded-lg border px-2 py-2">
      <div className={cn("font-heading text-lg font-medium", danger && "text-destructive")}>{value}</div>
      <div className="text-[11px] text-muted-foreground">{label}</div>
    </div>
  )
}

import {
  BookOpenIcon,
  CircleAlertIcon,
  CloudOffIcon,
  PackageOpenIcon,
  SlidersHorizontalIcon,
  Trash2Icon,
  TriangleAlertIcon,
  ZapIcon,
} from "lucide-react"
import { memo, useMemo, useRef } from "react"
import { toast } from "sonner"
import { CloudBadge } from "@/components/cloud"
import { cloudOfType, CLOUD_META } from "@/components/cloud-meta"
import { errorMessage } from "@/components/errors"
import { useSyncedState } from "@/components/use-synced-state"
import { openExternal } from "@/components/open-external"
import { ResourceIcon } from "@/components/resource-icon"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import { isNetworkFinding, type Body, type ResourceSchema, type SchemaAttribute, type WorkflowNode } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { canvasHandle } from "../canvas/actions"
import { openModuleView } from "../canvas/module-view-store"
import { nodeAddress, nodeLabel, useNodeSchemas, useSchema } from "../canvas/catalog"
import { AccessSection } from "../access/access-section"
import { NetworkSection } from "../network/network-section"
import { PlannedChangesSection } from "../plan/planned-changes"
import { AttrField } from "./attr-field"
import { getAttr, setAttr } from "./body"
import { ConnectSection } from "./connect-section"
import { InspectorContext, type InspectorContextValue } from "./context"
import { FileField } from "./file-field"
import { InspectorBody, InspectorSection } from "./inspector-section"
import { FreeformAttrs, META_ATTRS, MetaArguments } from "./meta-arguments"
import { SettingsForm } from "./settings-form"

const NAME_RE = /^[a-zA-Z_][a-zA-Z0-9_-]*$/

// Constant: section headings are memoized on their props.
const SETTINGS_ICON = <SlidersHorizontalIcon className="size-3.5 text-muted-foreground" />

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

/** The selected Terraform node's form (resource, data source or module). */
export function NodeInspector() {
  const selectedNodeId = useWorkflowStore((s) => s.selectedNodeId)
  const nodes = useWorkflowStore((s) => s.workflow?.nodes)
  const refNodes = useReferenceNodes(selectedNodeId)
  const variables = useWorkflowStore((s) => s.workflow?.variables)
  const locals = useWorkflowStore((s) => s.workflow?.locals)
  const allDiagnostics = useWorkflowStore((s) => s.workflow?.diagnostics)
  const mode = useWorkflowStore((s) => s.mode)
  const select = useWorkflowStore((s) => s.select)
  const schemas = useNodeSchemas(nodes)
  const node = nodes?.find((n) => n.id === selectedNodeId)

  const ctx = useMemo<InspectorContextValue | null>(
    () =>
      refNodes && selectedNodeId
        ? {
            nodeId: selectedNodeId,
            mode,
            nodes: refNodes,
            variables: variables ?? [],
            locals: locals ?? [],
            schemas,
            diagnostics: (allDiagnostics ?? []).filter((d) => d.nodeId === selectedNodeId),
            select,
          }
        : null,
    [refNodes, variables, locals, allDiagnostics, selectedNodeId, mode, schemas, select],
  )

  if (!node || !ctx) return null
  return (
    <InspectorContext.Provider value={ctx}>
      <NodeForm key={node.id} node={node} />
    </InspectorContext.Provider>
  )
}

/** Node fields references never read: where a card sits on the canvas. */
const LAYOUT_FIELDS: Partial<Record<keyof WorkflowNode, true>> = { position: true, positioned: true }

function sameForReferences(a: WorkflowNode, b: WorkflowNode, selected: boolean) {
  if (a === b) return true
  const keys = Object.keys(a) as (keyof WorkflowNode)[]
  if (keys.length !== Object.keys(b).length) return false
  // The selected node's own body is what its form edits: fields, pickers and chips only reference other nodes' bodies.
  return keys.every((k) => LAYOUT_FIELDS[k] || (selected && k === "body") || a[k] === b[k])
}

/**
 * The workflow's nodes as the form's references see them (pickers, chips, depends_on): the same list until a change
 * they can see. Typing in a field or moving a card replaces `nodes`; handing that to the context would re-render every
 * field of the form instead of the edited one.
 */
function useReferenceNodes(selectedNodeId: string | null): WorkflowNode[] | undefined {
  const prev = useRef<WorkflowNode[] | undefined>(undefined)
  return useWorkflowStore((s) => {
    const next = s.workflow?.nodes
    const last = prev.current
    if (last && next && last.length === next.length && last.every((n, i) => sameForReferences(n, next[i], n.id === selectedNodeId)))
      return last
    prev.current = next
    return next
  })
}

function NodeForm({ node }: { node: WorkflowNode }) {
  const allDiagnostics = useWorkflowStore((s) => s.workflow!.diagnostics)
  const providers = useWorkflowStore((s) => s.workflow!.providers)
  const mode = useWorkflowStore((s) => s.mode)
  const update = useWorkflowStore((s) => s.update)
  const schemaQuery = useSchema(node.kind, node.type)
  const schema = schemaQuery.data ?? undefined
  const notes = (allDiagnostics ?? []).filter((d) => d.nodeId === node.id && !d.attribute && !isNetworkFinding(d))
  const pro = mode === "pro"

  const setBody = (b: Body) =>
    update((d) => ({ ...d, nodes: d.nodes.map((n) => (n.id === node.id ? { ...n, body: b } : n)) }))

  const schemaNames = new Set(schema?.block.attributes.map((a) => a.name) ?? [])
  const unknownAttrs = new Set(
    (node.body.attrs ?? []).filter((a) => !schemaNames.has(a.name) && !META_ATTRS.has(a.name)).map((a) => a.name),
  )

  return (
    <div className="flex h-full flex-col">
      <NodeHeader id={node.id} kind={node.kind} type={node.type} name={node.name} label={node.label} schema={schema} />
      <InspectorBody>
        <NameFields node={node} />
        {node.kind !== "module" && schema && <SupportNote schema={schema} />}
        {notes.length > 0 && (
          <div className="flex flex-col gap-1.5">
            {notes.map((d, i) => (
              <Alert key={i} variant={d.severity === "error" ? "destructive" : "default"}>
                {d.severity === "error" ? <CircleAlertIcon /> : <TriangleAlertIcon />}
                <AlertTitle>{d.message}</AlertTitle>
                {d.detail && <AlertDescription>{d.detail}</AlertDescription>}
              </Alert>
            ))}
          </div>
        )}

        <ConnectSection nodeId={node.id} />

        <InspectorSection id="settings" title="Settings" icon={SETTINGS_ICON}>
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
            <SettingsForm schema={schema.block} body={node.body} onChange={setBody} />
          ) : (
            <p className="text-sm text-muted-foreground">
              No provider schema for <span className="font-mono">{node.type}</span>; edit its attributes as expressions below.
            </p>
          )}
        </InspectorSection>

        {node.kind !== "module" && (unknownAttrs.size > 0 || (!schema && !schemaQuery.isPending)) && (
          <InspectorSection id="other-attributes" title={schema ? "Other attributes" : "Attributes"} navLabel="Other">
            <FreeformAttrs
              body={node.body}
              onChange={setBody}
              exclude={new Set([...META_ATTRS, ...schemaNames])}
              addLabel="Add attribute"
            />
          </InspectorSection>
        )}

        <AccessSection node={node} />
        {node.kind === "resource" && <NetworkSection node={node} />}
        <PlannedChangesSection nodeId={node.id} />

        {pro && (
          <InspectorSection id="meta-arguments" title="Meta-arguments" navLabel="Meta">
            <MetaArguments node={node} providers={providers ?? []} onChange={setBody} />
          </InspectorSection>
        )}

        {pro && (
          <InspectorSection id="code" title="Code">
            <FileField node={node} />
          </InspectorSection>
        )}
      </InspectorBody>
    </div>
  )
}

/** Memoized on what it shows: typing in the form replaces the node, not its name or type. */
const NodeHeader = memo(function NodeHeader({
  id,
  kind,
  type,
  name,
  label,
  schema,
}: Pick<WorkflowNode, "id" | "kind" | "type" | "name" | "label"> & { schema?: ResourceSchema }) {
  const cloud = schema?.cloud ?? cloudOfType(type)
  const accent = cloud ? CLOUD_META[cloud].accent : "#a855f7"
  return (
    <div className="flex items-start gap-3 border-b p-4">
      <span
        data-slot="resource-tile"
        className="flex size-10 shrink-0 items-center justify-center rounded-xl"
        style={{ background: `${accent}1f`, color: accent }}
      >
        <ResourceIcon name={kind === "module" ? "package" : schema?.icon} className="size-5" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-heading text-base leading-tight font-medium">{nodeLabel({ label, type }, schema)}</div>
        <div className="truncate font-mono text-[11px] text-muted-foreground">{nodeAddress({ kind, type, name })}</div>
        <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
          {cloud && <CloudBadge cloud={cloud} />}
          {kind !== "resource" && (
            <span className="rounded-full border px-2 text-xs capitalize">{kind === "data" ? "Data source" : "Module"}</span>
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
            <Button variant="ghost" size="icon-sm" onClick={() => canvasHandle()?.remove(id)} aria-label="Delete">
              <Trash2Icon />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Delete (⌫)</TooltipContent>
        </Tooltip>
      </div>
    </div>
  )
})

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

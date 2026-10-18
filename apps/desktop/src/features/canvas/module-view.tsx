import {
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
} from "@xyflow/react"
import { useQuery } from "@tanstack/react-query"
import { ChevronRightIcon, CloudDownloadIcon, PackageIcon, XIcon } from "lucide-react"
import { useTheme } from "next-themes"
import { Fragment, useEffect, useMemo, useState } from "react"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Kbd } from "@/components/ui/kbd"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api/client"
import type { NamedBlock, Position, Workflow } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { useModuleView } from "./module-view-store"
import { nodeLabel, schemaKey, useNodeSchemas } from "./catalog"
import { computeLayout } from "./layout"
import { RefEdge, ResourceNode, type RefFlowEdge, type ResourceFlowNode } from "./nodes"

const nodeTypes = { resource: ResourceNode }
const edgeTypes = { ref: RefEdge }

/** Full-canvas overlay showing what is inside a module call. Mount inside the canvas. */
export function ModuleOverlay() {
  const nodeId = useModuleView((s) => s.nodeId)
  const path = useModuleView((s) => s.path)
  const setPath = useModuleView((s) => s.setPath)
  const close = useModuleView((s) => s.close)
  const wf = useWorkflowStore((s) => s.workflow)
  const node = wf?.nodes.find((n) => n.id === nodeId)

  // The module disappeared (deleted, workflow switched): nothing to show.
  useEffect(() => {
    if (nodeId && !node) close()
  }, [nodeId, node, close])

  useEffect(() => {
    if (!nodeId) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || document.querySelector("[role=dialog]:not([data-module-view])")) return
      e.preventDefault()
      close()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [nodeId, close])

  const nested = path.map((p) => `module.${p}`).join(".")
  const view = useQuery({
    queryKey: ["module-view", wf?.id, nodeId, nested, wf?.updatedAt],
    queryFn: () => api.moduleView(wf!.id, nodeId!, nested || undefined),
    enabled: !!wf && !!node,
    retry: false,
  })

  if (!wf || !node) return null
  const crumbs = [`module.${node.name}`, ...path.map((p) => `module.${p}`)]
  const mod = view.data?.workflow

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={`Inside ${crumbs.join(".")}`}
      data-module-view=""
      className="absolute inset-0 z-20 flex flex-col bg-background"
    >
      <div className="flex h-11 shrink-0 items-center gap-2 border-b px-3">
        <PackageIcon className="size-4 text-violet-500" />
        <nav aria-label="Module path" className="flex min-w-0 items-center gap-1 text-sm">
          <button type="button" className="truncate text-muted-foreground hover:text-foreground" onClick={close}>
            {wf.name}
          </button>
          {crumbs.map((c, i) => (
            <Fragment key={c + i}>
              <ChevronRightIcon className="size-3.5 shrink-0 text-muted-foreground" />
              {i === crumbs.length - 1 ? (
                <span className="truncate font-mono text-xs font-medium">{c}</span>
              ) : (
                <button
                  type="button"
                  className="truncate font-mono text-xs text-muted-foreground hover:text-foreground"
                  onClick={() => setPath(path.slice(0, i))}
                >
                  {c}
                </button>
              )}
            </Fragment>
          ))}
        </nav>
        {view.data?.source && (
          <span className="ml-2 hidden truncate font-mono text-[11px] text-muted-foreground md:inline">
            source = &quot;{view.data.source}&quot;
          </span>
        )}
        <span className="ml-auto rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">Read-only</span>
        <Button variant="ghost" size="sm" onClick={close} aria-label="Close module view">
          <Kbd>Esc</Kbd>
          <XIcon />
        </Button>
      </div>
      <div className="relative min-h-0 flex-1">
        {view.isPending ? (
          <div className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground">
            <Spinner /> Reading the module…
          </div>
        ) : view.isError ? (
          <ModuleMessage title="Couldn't open the module" message={errorMessage(view.error)} />
        ) : !mod ? (
          <ModuleMessage
            remote={!view.data.local}
            title={crumbs[crumbs.length - 1]}
            message={view.data.message ?? "This module has no content to show."}
          />
        ) : (
          <div className="flex h-full">
            <div className="min-w-0 flex-1">
              <ReactFlowProvider>
                <ModuleCanvas key={`${nodeId}/${nested}`} module={mod} onOpen={(name) => setPath([...path, name])} />
              </ReactFlowProvider>
            </div>
            <ModuleInterface module={mod} />
          </div>
        )}
      </div>
    </div>
  )
}

function ModuleMessage({ title, message, remote }: { title: string; message: string; remote?: boolean }) {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <Empty className="max-w-md border">
        <EmptyHeader>
          <EmptyMedia variant="icon">{remote ? <CloudDownloadIcon /> : <PackageIcon />}</EmptyMedia>
          <EmptyTitle className="font-mono text-sm">{title}</EmptyTitle>
          <EmptyDescription>{message}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    </div>
  )
}

function ModuleCanvas({ module: mod, onOpen }: { module: Workflow; onOpen: (name: string) => void }) {
  const { resolvedTheme } = useTheme()
  const flow = useReactFlow()
  const schemas = useNodeSchemas(mod.nodes)
  const [positions, setPositions] = useState<Map<string, Position> | null>(null)

  useEffect(() => {
    let live = true
    void computeLayout(mod.nodes, mod.edges ?? []).then((p) => {
      if (!live) return
      setPositions(p)
      window.requestAnimationFrame(() => void flow.fitView({ padding: 0.2, maxZoom: 1.1 }))
    })
    return () => {
      live = false
    }
  }, [mod, flow])

  const nodes = useMemo((): ResourceFlowNode[] => {
    if (!positions) return []
    const diag = new Map<string, { errors: number; warnings: number }>()
    for (const d of mod.diagnostics ?? []) {
      if (!d.nodeId) continue
      const c = diag.get(d.nodeId) ?? { errors: 0, warnings: 0 }
      if (d.severity === "error") c.errors++
      else if (d.severity === "warning") c.warnings++
      diag.set(d.nodeId, c)
    }
    return mod.nodes.map((n) => {
      const schema = schemas.get(schemaKey(n.kind, n.type))
      return {
        id: n.id,
        type: "resource",
        position: positions.get(n.id) ?? { x: 0, y: 0 },
        data: {
          node: n,
          schema,
          label: nodeLabel(n, schema),
          deployed: false,
          errors: diag.get(n.id)?.errors ?? 0,
          warnings: diag.get(n.id)?.warnings ?? 0,
        },
      }
    })
  }, [mod, positions, schemas])

  const edges = useMemo(
    (): RefFlowEdge[] =>
      (mod.edges ?? []).map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        type: "ref",
        markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
        data: { kind: e.kind, attribute: e.attribute, sourceAttr: e.sourceAttr, hovered: false, live: false },
      })),
    [mod],
  )

  if (mod.nodes.length === 0) {
    return <ModuleMessage title="No resources" message="This module only declares inputs, outputs or locals." />
  }
  return (
    <ReactFlow<ResourceFlowNode, RefFlowEdge>
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      nodesDraggable={false}
      nodesConnectable={false}
      onNodeDoubleClick={(_, n) => n.data.node.kind === "module" && onOpen(n.data.node.name)}
      zoomOnDoubleClick={false}
      colorMode={resolvedTheme === "light" ? "light" : "dark"}
      minZoom={0.15}
      maxZoom={2}
      proOptions={{ hideAttribution: true }}
      className="bg-muted/30"
    >
      <Background variant={BackgroundVariant.Dots} gap={16} size={1.2} />
      <Controls showInteractive={false} position="bottom-left" />
    </ReactFlow>
  )
}

/** Expression text of a block attribute, if set. */
function attrExpr(b: NamedBlock, name: string) {
  return b.body.attrs?.find((a) => a.name === name)?.value.expr
}

/** Inputs (variables) and outputs: the module's contract with its caller. */
function ModuleInterface({ module: mod }: { module: Workflow }) {
  const inputs = mod.variables ?? []
  const outputs = mod.outputs ?? []
  if (inputs.length === 0 && outputs.length === 0) return null
  return (
    <aside className="flex w-64 shrink-0 flex-col gap-4 overflow-y-auto border-l p-3 text-xs">
      {inputs.length > 0 && (
        <section className="flex flex-col gap-1.5">
          <h3 className="text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">Inputs</h3>
          {inputs.map((v) => {
            const type = attrExpr(v, "type")
            const def = attrExpr(v, "default")
            const desc = attrExpr(v, "description")
            return (
              <div key={v.name} className="flex flex-col rounded-md border px-2 py-1.5">
                <span className="font-mono font-medium">{v.name}</span>
                <span className="truncate font-mono text-[11px] text-muted-foreground">
                  {type ?? "any"}
                  {def !== undefined ? ` = ${def}` : " (required)"}
                </span>
                {desc && <span className="mt-0.5 text-muted-foreground">{desc.replace(/^"|"$/g, "")}</span>}
              </div>
            )
          })}
        </section>
      )}
      {outputs.length > 0 && (
        <section className="flex flex-col gap-1.5">
          <h3 className="text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">Outputs</h3>
          {outputs.map((o) => (
            <div key={o.name} className="flex flex-col rounded-md border px-2 py-1.5">
              <span className="font-mono font-medium">{o.name}</span>
              <span className="truncate font-mono text-[11px] text-muted-foreground">{attrExpr(o, "value")}</span>
            </div>
          ))}
        </section>
      )}
    </aside>
  )
}

import { useQuery } from "@tanstack/react-query"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { useNodeSchemas, schemaKey } from "@/features/canvas/catalog"
import { useConnections } from "@/features/connections/store"
import { api } from "@/lib/api/client"
import type { Workflow } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { deriveArchitecture } from "./derive"
import { FONT_FAMILY, resetTextMeasure } from "./geometry"
import { autoLayout, buildDiagram, layoutKey, type AutoLayout, type Diagram } from "./layout"
import type { ArchModel } from "./model"
import { actorIconOf, cardScene, decorations, groupIconOf, sharedServiceNames, type Decorations } from "./scene"
import type { CardText } from "./styles"

export interface Scene {
  model: ArchModel
  diagram: Diagram
  deco: Decorations
  cards: Map<string, { icon: string; text: CardText }>
  groupIcons: Map<string, string | undefined>
  actorIcons: Map<string, string>
}

let fontsReady: Promise<void> | undefined
/** Text is measured with the diagram font; wait for it once so widths are right. */
function loadFonts() {
  fontsReady ??= Promise.all(
    ["400 11px", "500 11px", "600 12px", "600 13px", "700 24px"].map((f) => document.fonts.load(`${f} ${FONT_FAMILY}`)),
  ).then(
    () => resetTextMeasure(),
    () => undefined,
  )
  return fontsReady
}

/** Derives, lays out and decorates the open workflow's Architecture diagram. */
export function useScene(): Scene | null {
  const wf = useWorkflowStore((s) => s.workflow) as Workflow
  const loadTests = useWorkflowStore((s) => s.loadTests)
  const connections = useConnections().data
  const schemas = useNodeSchemas(wf.nodes)

  // Module calls are drawn as what they create: their content comes from the module files (read again
  // whenever the workflow is saved, as module files change with it).
  const hasModules = wf.nodes.some((n) => n.kind === "module")
  const moduleTree = useQuery({
    queryKey: ["module-tree", wf.id, wf.updatedAt],
    queryFn: () => api.moduleTree(wf.id),
    enabled: hasModules,
    placeholderData: (previous) => previous,
    staleTime: Infinity,
  })
  const modulesPending = hasModules && moduleTree.isPending

  const used = useMemo(() => {
    const ids = new Set(loadTests.flatMap((r) => (r.spec.target.connectionId ? [r.spec.target.connectionId] : [])))
    return (connections ?? []).filter((c) => ids.has(c.id)).map((c) => ({ id: c.id, name: c.name, kind: c.kind }))
  }, [loadTests, connections])
  const labelOf = useMemo(() => {
    const labels = new Map<string, string>()
    for (const n of wf.nodes) {
      const label = schemas.get(schemaKey(n.kind, n.type))?.label
      if (label) labels.set(n.type, label)
    }
    return (type: string) => labels.get(type)
  }, [wf.nodes, schemas])

  const { nodes, edges, variables, locals, providers, environments, activeEnvironment } = wf
  const modules = hasModules ? moduleTree.data : undefined
  const model = useMemo(
    () =>
      modulesPending
        ? null
        : deriveArchitecture({ nodes, edges, variables, locals, providers, environments, activeEnvironment }, { connections: used, labelOf, modules }),
    [modulesPending, nodes, edges, variables, locals, providers, environments, activeEnvironment, used, labelOf, modules],
  )

  const key = model ? layoutKey(model) : ""
  const [auto, setAuto] = useState<{ key: string; layout: AutoLayout } | null>(null)
  // Lay out again only when the structure changes (key), not when descriptions do.
  const requested = useRef("")
  useEffect(() => {
    if (!model || requested.current === key) return
    requested.current = key
    void loadFonts()
      .then(() => autoLayout(model))
      .then((layout) => requested.current === key && setAuto({ key, layout }))
      .catch((err) => toast.error("Diagram layout failed", { description: errorMessage(err) }))
  }, [key, model])

  return useMemo(() => {
    if (!auto || !model) return null
    const diagram = buildDiagram(model, auto.layout)
    const shared = sharedServiceNames(model.cards)
    const cards = new Map(model.cards.map((c) => [c.id, cardScene(c, labelOf, shared)]))
    return {
      model,
      diagram,
      deco: decorations(model, diagram, wf),
      cards,
      groupIcons: new Map(model.groups.map((g) => [g.id, groupIconOf(g)])),
      actorIcons: new Map(model.actors.map((a) => [a.id, actorIconOf(a)])),
    }
  }, [auto, model, labelOf, wf])
}

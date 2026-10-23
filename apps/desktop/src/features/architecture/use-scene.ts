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
import { deriveDockerArchitecture, dockerCardScene, dockerGroupIconOf, dockerLegend, dockerSubtitle } from "./docker"
import { deriveKubernetesArchitecture, kubeCardScene, kubeGroupIconOf, kubeLegend, kubeSubtitle } from "./kubernetes"
import { FONT_FAMILY, resetTextMeasure } from "./geometry"
import { autoLayout, buildDiagram, layoutKey, type AutoLayout, type Diagram } from "./layout"
import type { ArchModel } from "./model"
import { actorIconOf, cardScene, decorations, frameDecorations, groupIconOf, sharedServiceNames, type Decorations } from "./scene"
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

  const auto = useAutoLayout(model)

  return useMemo(() => {
    if (!auto || !model) return null
    const diagram = buildDiagram(model, auto)
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

/** The open Docker workspace's diagram: derived from its compose graph and the containers' status. */
export function useDockerScene(): Scene | null {
  const wf = useWorkflowStore((s) => s.workflow) as Workflow
  const status = useWorkflowStore((s) => s.compose)
  const { name, compose } = wf
  const model = useMemo(() => deriveDockerArchitecture({ name, compose }, status), [name, compose, status])
  const auto = useAutoLayout(model)

  return useMemo(() => {
    if (!auto) return null
    const diagram = buildDiagram(model, auto)
    const graph = new Map((compose?.graph.nodes ?? []).map((n) => [n.id, n]))
    return {
      model,
      diagram,
      deco: frameDecorations(diagram, name, dockerSubtitle({ name, compose }), dockerLegend(model)),
      cards: new Map(model.cards.map((c) => [c.id, dockerCardScene(c, graph.get(c.nodeId))])),
      groupIcons: new Map(model.groups.map((g) => [g.id, dockerGroupIconOf(g)])),
      actorIcons: new Map(model.actors.map((a) => [a.id, actorIconOf(a)])),
    }
  }, [auto, model, name, compose])
}

/** The open Kubernetes workspace's diagram: derived from its rendered objects (active environment) and the workloads' status. */
export function useKubernetesScene(): Scene | null {
  const wf = useWorkflowStore((s) => s.workflow) as Workflow
  const status = useWorkflowStore((s) => s.kube)
  const { id, name, kubernetes } = wf
  const model = useMemo(() => deriveKubernetesArchitecture({ id, name, kubernetes }, status), [id, name, kubernetes, status])
  const auto = useAutoLayout(model)

  return useMemo(() => {
    if (!auto) return null
    const diagram = buildDiagram(model, auto)
    const graph = new Map((kubernetes?.graph.nodes ?? []).map((n) => [n.id, n]))
    return {
      model,
      diagram,
      deco: frameDecorations(diagram, name, kubeSubtitle({ kubernetes }), kubeLegend(model)),
      cards: new Map(model.cards.map((c) => [c.id, kubeCardScene(c, graph.get(c.nodeId))])),
      groupIcons: new Map(model.groups.map((g) => [g.id, kubeGroupIconOf(g)])),
      actorIcons: new Map(model.actors.map((a) => [a.id, actorIconOf(a)])),
    }
  }, [auto, model, name, kubernetes])
}

/**
 * ELK's placement of a model (null until the first one is done). Lays out again only when the
 * structure changes (layoutKey), not when descriptions do; meanwhile the last layout stays.
 */
function useAutoLayout(model: ArchModel | null): AutoLayout | null {
  const key = model ? layoutKey(model) : ""
  const [auto, setAuto] = useState<{ key: string; layout: AutoLayout } | null>(null)
  const requested = useRef("")
  useEffect(() => {
    if (!model || requested.current === key) return
    requested.current = key
    void loadFonts()
      .then(() => autoLayout(model))
      .then((layout) => requested.current === key && setAuto({ key, layout }))
      .catch((err) => toast.error("Diagram layout failed", { description: errorMessage(err) }))
  }, [key, model])
  return auto?.layout ?? null
}

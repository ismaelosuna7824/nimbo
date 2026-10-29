import type { Edge, Node } from "@xyflow/react"
import { useMemo } from "react"
import { cn } from "@/lib/utils"
import { useWalkthrough, type NodeMark } from "./store"

/** The current hop: the accent ring of focus. Its neighbours on the hop stay as they are; the rest of the path and design fade. */
const NODE_CLASS: Record<NodeMark | "off", string> = {
  current: "rounded-xl ring-2 ring-primary ring-offset-2 ring-offset-background transition-opacity duration-200",
  near: "transition-opacity duration-200",
  path: "opacity-60 transition-opacity duration-200",
  off: "opacity-25 transition-opacity duration-200",
}

/**
 * `nodes` as the walkthrough shows them: the current component ringed, the others faded (frames, see
 * `isFrame`, keep their look so the layout stays readable). Without a walkthrough the same array comes back.
 */
export function useWalkthroughNodes<N extends Node>(nodes: N[], isFrame: (n: N) => boolean): N[] {
  const marks = useWalkthrough((s) => s.highlight?.nodes)
  return useMemo(() => {
    if (!marks) return nodes
    return nodes.map((n) => {
      const mark = marks[n.id]
      if (isFrame(n) && mark !== "current") return n
      return { ...n, className: cn(n.className, NODE_CLASS[mark ?? "off"]) }
    })
  }, [nodes, marks, isFrame])
}

/** True while the walkthrough marks this canvas: hover labels of edges stay hidden (the caption says how a hop connects). */
export function useWalkthroughShowing(): boolean {
  return useWalkthrough((s) => s.highlight !== null)
}

/**
 * `edges` as the walkthrough shows them: the hop's edges emphasized by `emphasize` (the canvas' hover
 * look), edges off the path faded. Without a walkthrough the same array comes back.
 */
export function useWalkthroughEdges<E extends Edge>(edges: E[], emphasize: (e: E) => E): E[] {
  const marks = useWalkthrough((s) => s.highlight?.edges)
  return useMemo(() => {
    if (!marks) return edges
    return edges.map((e) => {
      const mark = marks[e.id]
      if (mark === "current") return emphasize(e)
      return mark ? e : { ...e, className: cn(e.className, "opacity-20 transition-opacity duration-200") }
    })
  }, [edges, marks, emphasize])
}

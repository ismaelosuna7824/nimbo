import type { Edge, Node } from "@xyflow/react"
import { useMemo } from "react"
import { cn } from "@/lib/utils"
import { useWalkthrough, type NodeMark } from "./store"

/**
 * The current hop: the accent ring of focus; the tour's focus: the accent glow (`tour-glow`, index.css). What was
 * just shown keeps a faint ring; the rest of the path and design fade.
 */
const NODE_CLASS: Record<NodeMark | "off", string> = {
  current: "rounded-xl ring-2 ring-primary ring-offset-2 ring-offset-background transition-opacity duration-200",
  glow: "rounded-xl tour-glow transition-opacity duration-200",
  near: "rounded-xl ring-1 ring-primary/40 transition-opacity duration-200",
  path: "opacity-60 transition-opacity duration-200",
  off: "opacity-25 transition-opacity duration-200",
}

/**
 * `nodes` as the walkthrough shows them: the focus ringed or glowing, the others faded. Frames (see `isFrame`) keep
 * their look so the layout stays readable, unless they are the focus. Without a walkthrough the same array comes back.
 */
export function useWalkthroughNodes<N extends Node>(nodes: N[], isFrame: (n: N) => boolean): N[] {
  const marks = useWalkthrough((s) => s.highlight?.nodes)
  return useMemo(() => {
    if (!marks) return nodes
    return nodes.map((n) => {
      const mark = marks[n.id]
      if (isFrame(n)) return mark === "current" || mark === "glow" ? { ...n, className: cn(n.className, NODE_CLASS[mark], "rounded-2xl") } : n
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

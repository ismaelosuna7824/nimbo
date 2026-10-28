import type { Edge, Node, NodeProps } from "@xyflow/react"
import { useMemo, useState } from "react"
import { jsonEqual } from "@/lib/json-equal"

/** Same card: everything a canvas sets on a React Flow node (data compared by value, e.g. rebuilt pod or stage lists). */
export function sameFlowNode<N extends Node>(a: N, b: N) {
  return (
    a.type === b.type &&
    a.parentId === b.parentId &&
    a.width === b.width &&
    a.height === b.height &&
    a.zIndex === b.zIndex &&
    a.selected === b.selected &&
    a.selectable === b.selectable &&
    a.className === b.className &&
    a.position.x === b.position.x &&
    a.position.y === b.position.y &&
    jsonEqual(a.data, b.data)
  )
}

export function sameFlowEdge<E extends Edge>(a: E, b: E) {
  return a.source === b.source && a.target === b.target && a.type === b.type && a.markerEnd === b.markerEnd && jsonEqual(a.data, b.data)
}

/**
 * Returns `items` with every item that `same` finds unchanged (by id) replaced
 * by the object from the previous call (or the previous array itself when
 * nothing changed), so React Flow only re-renders the cards and edges whose
 * content changed.
 */
export function useStableItems<T extends { id: string }>(items: T[], same: (a: T, b: T) => boolean): T[] {
  // Holder kept across renders; only read and written while memoizing the next list.
  const [cache] = useState(() => ({ byId: new Map<string, T>(), list: [] as T[] }))
  return useMemo(() => {
    const byId = new Map<string, T>()
    let unchanged = items.length === cache.list.length
    const list = items.map((item, i) => {
      const old = cache.byId.get(item.id)
      const kept = old && same(old, item) ? old : item
      if (kept !== cache.list[i]) unchanged = false
      byId.set(item.id, kept)
      return kept
    })
    if (!unchanged) {
      cache.byId = byId
      cache.list = list
    }
    return cache.list
  }, [items, same, cache])
}

/**
 * Memo comparison of card components: a card draws its data, selection and size, never where it sits, so a drag
 * step (a new absolute position each frame) doesn't re-render the dragged card.
 */
export function sameCardProps<N extends Node>(a: NodeProps<N>, b: NodeProps<N>) {
  return (
    a.id === b.id &&
    a.type === b.type &&
    a.data === b.data &&
    a.selected === b.selected &&
    a.width === b.width &&
    a.height === b.height &&
    a.isConnectable === b.isConnectable
  )
}

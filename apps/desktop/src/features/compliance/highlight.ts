import { useMemo } from "react"
import type { Node } from "@xyflow/react"
import { create } from "zustand"
import type { ComplianceControl } from "@/lib/api/types"

/** How a resource shows while the Compliance tab highlights it: a finding of the control(s) on it, or none. */
export type ComplianceMark = "failing" | "passing"

interface HighlightState {
  /** Node id → mark while "Show on canvas" is on (null: off). Unmarked cards are dimmed. */
  marks: Record<string, ComplianceMark> | null
}

export const useComplianceHighlight = create<HighlightState>(() => ({ marks: null }))

export function clearComplianceHighlight() {
  useComplianceHighlight.setState({ marks: null })
}

/**
 * Marks for one control (its resources: failing ones red, the rest green) or, without one, every resource a violated
 * control has findings on.
 */
export function complianceMarks(controls: ComplianceControl[], control: ComplianceControl | null): Record<string, ComplianceMark> {
  const marks: Record<string, ComplianceMark> = {}
  if (control) {
    for (const r of control.resources) marks[r.nodeId] = r.failing ? "failing" : "passing"
    return marks
  }
  for (const c of controls) {
    if (c.status !== "violated") continue
    for (const r of c.resources) if (r.failing) marks[r.nodeId] = "failing"
  }
  return marks
}

/** Same rings as the reachability highlight: red where a control fails, green where it holds. */
const RING: Record<ComplianceMark, string> = {
  failing: "rounded-xl ring-2 ring-destructive ring-offset-2 ring-offset-background",
  passing: "rounded-xl ring-2 ring-emerald-500 ring-offset-2 ring-offset-background",
}

/**
 * `nodes` with the compliance highlight applied: marked cards ringed, the others dimmed; frames (`isFrame`) are left
 * as they are so the layout stays readable. `alias` maps a hidden node to the card that stands for it (an object of a
 * collapsed Helm chart → the chart's card), which takes its mark (failing wins). Without a highlight the same array
 * comes back, so the canvas keeps its stable node objects.
 */
export function useComplianceMarked<N extends Node>(nodes: N[], isFrame: (n: N) => boolean, alias?: Map<string, string>): N[] {
  const marks = useComplianceHighlight((s) => s.marks)
  return useMemo(() => {
    if (!marks) return nodes
    const shown: Record<string, ComplianceMark> = { ...marks }
    if (alias) {
      for (const [id, mark] of Object.entries(marks)) {
        const to = alias.get(id)
        if (to && shown[to] !== "failing") shown[to] = mark
      }
    }
    return nodes.map((n) => {
      if (isFrame(n)) return n
      const mark = shown[n.id]
      return { ...n, className: mark ? RING[mark] : "opacity-35" }
    })
  }, [nodes, marks, isFrame, alias])
}

// What the request walkthrough and the infrastructure tour share behind the screen: keys, reduced motion, the
// camera and the canvas marks.
import { useReactFlow, useStore } from "@xyflow/react"
import { useEffect, useRef, useState, type RefObject } from "react"
import type { Rect } from "@/features/architecture/geometry"
import { closeWalkthrough, useWalkthrough, type Highlight } from "./store"
import { cameraFor } from "./travel"

const CAMERA_MS = 650

export function useReducedMotion() {
  const [reduced, setReduced] = useState(() => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false)
  useEffect(() => {
    const mq = window.matchMedia?.("(prefers-reduced-motion: reduce)")
    if (!mq) return
    const onChange = () => setReduced(mq.matches)
    mq.addEventListener("change", onChange)
    return () => mq.removeEventListener("change", onChange)
  }, [])
  return reduced
}

/**
 * ← → step (through `go`), space plays or pauses, Esc closes (not while typing, nor inside a dialog or menu).
 * Handled in the capture phase and stopped there: React Flow must not also nudge a selected card with the arrows
 * (↑ ↓ on the canvas are swallowed for the same reason) or pan with space.
 */
export function usePlayerKeys(go: (to: number) => void, index: number) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const el = e.target instanceof HTMLElement ? e.target : null
      // Keys typed elsewhere (a page over the editor, the inspector, the dock) are theirs.
      if (el && el !== document.body && !el.closest("[data-canvas-view]")) return
      if (el?.closest("[role=dialog], [role=alertdialog], [role=menu]") || el?.isContentEditable || (el && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName))) return
      if (e.key === "Escape") closeWalkthrough()
      else if (e.key === "ArrowRight") go(index + 1)
      else if (e.key === "ArrowLeft") go(index - 1)
      else if (e.key === " " && !el?.closest("button, a")) useWalkthrough.setState((s) => ({ playing: !s.playing }))
      else if (!((e.key === "ArrowUp" || e.key === "ArrowDown") && el?.closest(".react-flow"))) return
      e.preventDefault()
      e.stopPropagation()
    }
    window.addEventListener("keydown", onKey, true)
    return () => window.removeEventListener("keydown", onKey, true)
  }, [go, index])
}

/** Publishes the canvas marks (only when they changed: every canvas re-renders on a new highlight); cleared on unmount. */
export function useHighlight(next: Highlight | null) {
  useEffect(() => {
    const prev = useWalkthrough.getState().highlight
    if (JSON.stringify(prev) !== JSON.stringify(next)) useWalkthrough.setState({ highlight: next })
  }, [next])
  useEffect(() => () => useWalkthrough.setState({ highlight: null }), [])
}

/**
 * Eases the camera to `focus` (flow coordinates) above the caption panel, whenever `key` changes (a new step) or
 * the viewport is resized; with reduced motion it jumps.
 */
export function useFocusCamera(focus: () => Rect | undefined, key: string, panel: RefObject<HTMLDivElement | null>, reduced: boolean, maxZoom = 1.1) {
  const flow = useReactFlow()
  const width = useStore((s) => s.width)
  const height = useStore((s) => s.height)
  // The latest `focus` (it reads the canvas live); only `key` and the viewport move the camera.
  const read = useRef(focus)
  useEffect(() => {
    read.current = focus
  })
  useEffect(() => {
    const rect = read.current()
    if (!rect || !width || !height) return
    const covered = (panel.current?.offsetHeight ?? 220) + 32
    const cam = cameraFor(rect, { width, height }, covered, { maxZoom })
    void flow.setCenter(cam.x, cam.y, { zoom: cam.zoom, duration: reduced ? 0 : CAMERA_MS })
  }, [key, width, height, reduced, maxZoom, flow, panel])
}

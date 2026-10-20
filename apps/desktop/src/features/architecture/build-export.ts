import { fitScale, type ExportedImage } from "./export"
import { EXPORT_MARGIN, type Rect } from "./geometry"

/** The Build canvas root (marked by the canvas area). */
function buildCanvas() {
  return document.querySelector<HTMLElement>("[data-canvas-view='build'] .react-flow")
}

/** Bounds of every Build canvas card in flow coordinates, plus the export margin. */
export function buildCanvasBounds(): Rect | null {
  const root = buildCanvas()
  if (!root) return null
  const nodes = [...root.querySelectorAll<HTMLElement>(".react-flow__node")]
  if (nodes.length === 0) return null
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  for (const el of nodes) {
    const m = new DOMMatrixReadOnly(getComputedStyle(el).transform)
    minX = Math.min(minX, m.m41)
    minY = Math.min(minY, m.m42)
    maxX = Math.max(maxX, m.m41 + el.offsetWidth)
    maxY = Math.max(maxY, m.m42 + el.offsetHeight)
  }
  return {
    x: Math.floor(minX - EXPORT_MARGIN),
    y: Math.floor(minY - EXPORT_MARGIN),
    width: Math.ceil(maxX - minX + 2 * EXPORT_MARGIN),
    height: Math.ceil(maxY - minY + 2 * EXPORT_MARGIN),
  }
}

/**
 * The whole Build canvas (not just the visible part) as a PNG, as the app draws it: the viewport is
 * cloned at zoom 1 and rendered with its fonts by html-to-image.
 */
export async function exportBuildCanvas(scale: number, background: "solid" | "transparent"): Promise<ExportedImage> {
  const root = buildCanvas()
  const viewport = root?.querySelector<HTMLElement>(".react-flow__viewport")
  const bounds = buildCanvasBounds()
  if (!root || !viewport || !bounds) throw new Error("The canvas is empty")
  const s = fitScale(bounds.width, bounds.height, scale)
  // Lazy: only exports need html-to-image; keep it out of the startup bundle.
  const { toBlob } = await import("html-to-image")
  const blob = await toBlob(viewport, {
    width: bounds.width,
    height: bounds.height,
    pixelRatio: s,
    backgroundColor: background === "solid" ? getComputedStyle(root).backgroundColor : undefined,
    style: { width: `${bounds.width}px`, height: `${bounds.height}px`, transform: `translate(${-bounds.x}px, ${-bounds.y}px) scale(1)` },
  })
  if (!blob) throw new Error("The canvas could not be drawn")
  return { blob, width: Math.round(bounds.width * s), height: Math.round(bounds.height * s), scale: s }
}

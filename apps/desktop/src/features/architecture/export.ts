import { createElement, type ReactElement } from "react"
import { isTauri } from "@tauri-apps/api/core"
import { save } from "@tauri-apps/plugin-dialog"
import { writeFile } from "@tauri-apps/plugin-fs"
import geistLatin from "@fontsource-variable/geist/files/geist-latin-wght-normal.woff2?url"
import geistLatinExt from "@fontsource-variable/geist/files/geist-latin-ext-wght-normal.woff2?url"
import { EXPORT_MARGIN, type Rect } from "./geometry"
import { buildSceneSvg } from "./scene-svg"
import type { Scene } from "./use-scene"

export type ImageFormat = "png" | "svg"
export type ImageScale = 1 | 2 | 3 | 4

export interface ImageOptions {
  format: ImageFormat
  scale: ImageScale
  /** Solid background (white for the diagram, the canvas color for Build) or transparent. */
  background: "solid" | "transparent"
  title: boolean
  legend: boolean
}

export interface ExportedImage {
  blob: Blob
  width: number
  height: number
  /** Scale actually used (lower than asked when the image would exceed the browser's canvas limits). */
  scale: number
}

/** Largest canvas the export draws: WebKit and Chromium both handle 16384 px sides and ~140 MP. */
const MAX_SIDE = 16384
const MAX_PIXELS = 140_000_000

/** The highest scale ≤ `scale` whose canvas fits the limits for a `width`×`height` image. */
export function fitScale(width: number, height: number, scale: number) {
  let s = scale
  while (s > 1 && (width * s > MAX_SIDE || height * s > MAX_SIDE || width * height * s * s > MAX_PIXELS)) s--
  return s
}

/** Absolute extent of what the export draws. */
export function exportBounds(scene: Scene, opts: Pick<ImageOptions, "title" | "legend">): Rect {
  const rects: Rect[] = [scene.diagram.bounds]
  if (opts.title) rects.push(scene.deco.header)
  if (opts.legend) rects.push(scene.deco.legendRect)
  // Edge routes may leave the item bounds (detours around groups and cards).
  for (const e of scene.diagram.edges) for (const p of e.route) rects.push({ x: p.x, y: p.y, width: 0, height: 0 })
  const x = Math.min(...rects.map((r) => r.x)) - EXPORT_MARGIN
  const y = Math.min(...rects.map((r) => r.y)) - EXPORT_MARGIN
  const right = Math.max(...rects.map((r) => r.x + r.width)) + EXPORT_MARGIN
  const bottom = Math.max(...rects.map((r) => r.y + r.height)) + EXPORT_MARGIN
  return { x: Math.floor(x), y: Math.floor(y), width: Math.ceil(right - x), height: Math.ceil(bottom - y) }
}

const dataUrls = new Map<string, Promise<string>>()

/** A same-origin asset as a data: URL (MIME type from its extension), so the standalone SVG carries it. */
function inline(url: string): Promise<string> {
  if (url.startsWith("data:")) return Promise.resolve(url)
  let hit = dataUrls.get(url)
  if (!hit) {
    const mime = /\.svg(\?|$)/.test(url) ? "image/svg+xml" : /\.woff2(\?|$)/.test(url) ? "font/woff2" : "image/png"
    hit = fetch(url).then(async (res) => {
      if (!res.ok) throw new Error(`${res.status} ${url}`)
      const bytes = new Uint8Array(await res.arrayBuffer())
      let binary = ""
      for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
      return `data:${mime};base64,${btoa(binary)}`
    })
    hit.catch(() => dataUrls.delete(url))
    dataUrls.set(url, hit)
  }
  return hit
}

/** @font-face rules embedding the diagram font (Latin and Latin Extended). */
async function fontFaces() {
  const [latin, latinExt] = await Promise.all([inline(geistLatin), inline(geistLatinExt)])
  const face = (src: string, range: string) =>
    `@font-face{font-family:'Geist Variable';font-style:normal;font-weight:100 900;src:url(${src}) format('woff2');unicode-range:${range};}`
  return (
    face(latinExt, "U+0100-02BA,U+02BD-02C5,U+02C7-02CC,U+02CE-02D7,U+02DD-02FF,U+0304,U+0308,U+0329,U+1D00-1DBF,U+1E00-1E9F,U+1EF2-1EFF,U+2020,U+20A0-20AB,U+20AD-20C0,U+2113,U+2C60-2C7F,U+A720-A7FF") +
    face(latin, "U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD")
  )
}

/** The whole diagram as a standalone SVG document: fonts and icons embedded, no external references. */
export async function diagramSvg(scene: Scene, opts: ImageOptions): Promise<{ svg: string; width: number; height: number }> {
  const bounds = exportBounds(scene, opts)
  const urls = new Set<string>()
  for (const c of scene.cards.values()) urls.add(c.icon)
  for (const i of scene.groupIcons.values()) if (i) urls.add(i)
  for (const i of scene.actorIcons.values()) urls.add(i)
  const entries = await Promise.all([...urls].map(async (u) => [u, await inline(u)] as const))
  const icons = new Map(entries)
  // Lazy: the server renderer is only needed to serialize exports; keep it out of the startup bundle.
  const [fonts, { renderToStaticMarkup }] = await Promise.all([fontFaces(), import("react-dom/server")])
  const element: ReactElement = createElement(buildSceneSvg, {
    scene,
    bounds,
    icon: (u: string) => icons.get(u) ?? u,
    fonts,
    background: opts.background === "solid" ? "#ffffff" : undefined,
    title: opts.title,
    legend: opts.legend,
  })
  const svg = `<?xml version="1.0" encoding="UTF-8"?>\n${renderToStaticMarkup(element)}`
  return { svg, width: bounds.width, height: bounds.height }
}

/** Rasterizes an SVG document at `scale` into a PNG. */
export async function svgToPng(svg: string, width: number, height: number, scale: number, background?: string): Promise<Blob> {
  const url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml;charset=utf-8" }))
  try {
    const img = new Image()
    img.decoding = "sync"
    img.src = url
    await img.decode()
    const canvas = document.createElement("canvas")
    canvas.width = Math.round(width * scale)
    canvas.height = Math.round(height * scale)
    const ctx = canvas.getContext("2d")
    if (!ctx) throw new Error("This system cannot draw images (no 2D canvas)")
    const draw = () => {
      ctx.setTransform(1, 0, 0, 1, 0, 0)
      ctx.clearRect(0, 0, canvas.width, canvas.height)
      if (background) {
        ctx.fillStyle = background
        ctx.fillRect(0, 0, canvas.width, canvas.height)
      }
      ctx.setTransform(scale, 0, 0, scale, 0, 0)
      ctx.drawImage(img, 0, 0, width, height)
    }
    // WebKit may lay out an SVG image's embedded fonts after the first paint: draw again a moment later.
    draw()
    await new Promise((r) => setTimeout(r, 120))
    draw()
    return await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("The image is too large to encode"))), "image/png"),
    )
  } finally {
    URL.revokeObjectURL(url)
  }
}

export async function exportDiagram(scene: Scene, opts: ImageOptions): Promise<ExportedImage> {
  const { svg, width, height } = await diagramSvg(scene, opts)
  if (opts.format === "svg") return { blob: new Blob([svg], { type: "image/svg+xml" }), width, height, scale: 1 }
  const scale = fitScale(width, height, opts.scale)
  const blob = await svgToPng(svg, width, height, scale, opts.background === "solid" ? "#ffffff" : undefined)
  return { blob, width: Math.round(width * scale), height: Math.round(height * scale), scale }
}

/** Saves `blob` as `fileName`: the native save dialog in the desktop app, a download in the browser. False when cancelled. */
export async function saveImage(blob: Blob, fileName: string, format: ImageFormat): Promise<boolean> {
  if (isTauri()) {
    const path = await save({
      title: "Save image",
      defaultPath: fileName,
      filters: [format === "png" ? { name: "PNG image", extensions: ["png"] } : { name: "SVG image", extensions: ["svg"] }],
    })
    if (!path) return false
    await writeFile(path, new Uint8Array(await blob.arrayBuffer()))
    return true
  }
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = fileName
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  return true
}

/** "Items API" → "items-api-architecture.png". */
export function imageFileName(name: string, suffix: string, format: ImageFormat) {
  const base = name.trim().toLowerCase().replace(/[^\w.-]+/g, "-").replace(/^-+|-+$/g, "") || "workflow"
  return `${base}-${suffix}.${format}`
}

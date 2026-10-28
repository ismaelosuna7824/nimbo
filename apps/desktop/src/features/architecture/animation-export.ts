// Animated export of the Architecture diagram: the still diagram is rasterized once at the frame's scale,
// then every frame copies it and draws the traffic pulses (animate.ts) on top with the 2D canvas. GIF frames
// are indexed only where pulses are drawn; video frames go to the encoder whole.
import { ACCENT } from "./styles"
import { dirtyTiles, flowAt, flowPlan, frameCount, frameLayout, frameTime, PULSE_RADIUS, type FrameLayout, type SizePreset } from "./animate"
import { GIF_MAX_PIXELS, GIF_MAX_SIDE, gifDelays, gifFrameRate, gifPalette, gifWriter, paletteIndexer } from "./encode-gif"
import { encodeMp4, recordVideo, VIDEO_FPS, VIDEO_MAX_PIXELS, VIDEO_MAX_SIDE, videoSupport, yieldToUi, type VideoJob } from "./encode-video"
import { diagramSvg, exportBounds, rasterizeSvg, type FileFormat, type ImageScale } from "./export"
import type { Rect } from "./geometry"
import type { Scene } from "./use-scene"

export type AnimatedFormat = "gif" | "video"
export const DURATIONS = [4, 6, 10] as const
export type AnimationDuration = (typeof DURATIONS)[number]

export interface AnimationOptions {
  format: AnimatedFormat
  preset: SizePreset
  duration: AnimationDuration
  /** Pixels per diagram unit for the "fit" preset (lowered to the format's size limit). */
  scale: ImageScale
  title: boolean
  legend: boolean
}

export interface ExportedAnimation {
  blob: Blob
  format: FileFormat
  width: number
  height: number
  fps: number
  frames: number
  /** Encoding time in milliseconds. */
  elapsed: number
}

/** Animated exports always sit on white: GIF transparency is 1-bit and video has none. */
const BACKGROUND = "#ffffff"

/** Frame size, diagram placement and frame rate of an animated export. */
export function animationFrame(scene: Scene, opts: AnimationOptions): FrameLayout & { fps: number } {
  const b = exportBounds(scene, opts)
  const gif = opts.format === "gif"
  const layout = frameLayout(b, opts.preset, {
    scale: opts.scale,
    maxSide: gif ? GIF_MAX_SIDE : VIDEO_MAX_SIDE,
    maxPixels: gif ? GIF_MAX_PIXELS : VIDEO_MAX_PIXELS,
  })
  return { ...layout, fps: gif ? gifFrameRate(layout.width, layout.height) : VIDEO_FPS }
}

/** A glowing pulse in the accent color, drawn once and stamped for every pulse and trail ghost. */
function pulseSprite(radius: number) {
  const glow = radius * 2.8
  const size = Math.ceil(glow * 2) + 2
  const canvas = document.createElement("canvas")
  canvas.width = canvas.height = size
  const ctx = canvas.getContext("2d")!
  const c = size / 2
  const halo = ctx.createRadialGradient(c, c, radius * 0.6, c, c, glow)
  halo.addColorStop(0, "rgba(249, 115, 22, 0.45)")
  halo.addColorStop(1, "rgba(249, 115, 22, 0)")
  ctx.fillStyle = halo
  ctx.fillRect(0, 0, size, size)
  ctx.beginPath()
  ctx.arc(c, c, radius, 0, Math.PI * 2)
  ctx.fillStyle = ACCENT
  ctx.fill()
  ctx.beginPath()
  ctx.arc(c, c, radius * 0.4, 0, Math.PI * 2)
  ctx.fillStyle = "#fff4e6"
  ctx.fill()
  return { canvas, size, radius }
}

/** Draws frame `i` (still diagram, node highlights, pulses) and returns the areas that differ from the still. */
function frameRenderer(scene: Scene, opts: AnimationOptions, layout: FrameLayout, base: HTMLCanvasElement, frames: number) {
  const bounds = exportBounds(scene, opts)
  const plan = flowPlan(scene.diagram, opts.duration)
  const s = layout.scale
  // Pulses scale with the diagram but stay visible when a wide diagram is shrunk into a social frame.
  const pulseScale = Math.max(s, Math.min(layout.width, layout.height) / 240 / PULSE_RADIUS.data)
  const sprite = pulseSprite(PULSE_RADIUS.data * pulseScale)
  const px = (x: number) => layout.dx + (x - bounds.x) * s
  const py = (y: number) => layout.dy + (y - bounds.y) * s
  return (ctx: CanvasRenderingContext2D, i: number): Rect[] => {
    ctx.globalAlpha = 1
    ctx.drawImage(base, 0, 0)
    const { pulses, flashes } = flowAt(plan, frameTime(i, frames, opts.duration))
    const dirty: Rect[] = []
    const pad = 4
    for (const f of flashes) {
      const x = px(f.rect.x - pad)
      const y = py(f.rect.y - pad)
      const w = (f.rect.width + 2 * pad) * s
      const h = (f.rect.height + 2 * pad) * s
      ctx.strokeStyle = ACCENT
      for (const [width, alpha] of [
        [7, 0.16],
        [2, 0.9],
      ]) {
        ctx.globalAlpha = alpha * f.strength
        ctx.lineWidth = width * s
        ctx.beginPath()
        ctx.roundRect(x, y, w, h, 11 * s)
        ctx.stroke()
      }
      const m = 4 * s + 2
      dirty.push({ x: x - m, y: y - m, width: w + 2 * m, height: h + 2 * m })
    }
    for (const p of pulses) {
      const size = (sprite.size * p.radius * pulseScale) / sprite.radius
      const x = px(p.x) - size / 2
      const y = py(p.y) - size / 2
      ctx.globalAlpha = p.alpha
      ctx.drawImage(sprite.canvas, x, y, size, size)
      dirty.push({ x: x - 1, y: y - 1, width: size + 2, height: size + 2 })
    }
    ctx.globalAlpha = 1
    return dirty
  }
}

export interface AnimationProgress {
  signal: AbortSignal
  /** Share of frames done, 0–1. */
  onProgress: (done: number) => void
}

/** Renders and encodes the animated diagram (GIF, or video as MP4 — WebM where only that can be recorded). */
export async function exportAnimation(scene: Scene, opts: AnimationOptions, { signal, onProgress }: AnimationProgress): Promise<ExportedAnimation> {
  const started = performance.now()
  const layout = animationFrame(scene, opts)
  const { width, height, fps } = layout
  const support = opts.format === "video" ? await videoSupport(width, height, fps) : null
  if (opts.format === "video" && !support) throw new Error(`This system cannot encode a ${width} × ${height} video`)
  const { svg, width: w, height: h } = await diagramSvg(scene, { format: "png", scale: 1, background: "solid", title: opts.title, legend: opts.legend })
  signal.throwIfAborted()
  const base = await rasterizeSvg(svg, w, h, layout, BACKGROUND)
  const frames = frameCount(opts.duration, fps)
  const render = frameRenderer(scene, opts, layout, base, frames)
  const canvas = document.createElement("canvas")
  canvas.width = width
  canvas.height = height
  const progress = (done: number) => onProgress(done / frames)
  const result = { width, height, fps, frames }

  if (!support) {
    const ctx = canvas.getContext("2d", { willReadFrequently: true })
    if (!ctx) throw new Error("This system cannot draw images (no 2D canvas)")
    ctx.drawImage(base, 0, 0)
    const still = ctx.getImageData(0, 0, width, height).data
    // Palette: the still diagram plus what pulses and highlights look like over it at a few moments.
    const samples: Uint8ClampedArray[] = [still]
    for (let k = 0; k < 8; k++) {
      const dirty = render(ctx, Math.floor((k * frames) / 8))
      for (const r of dirtyTiles(dirty, width, height)) samples.push(ctx.getImageData(r.x, r.y, r.width, r.height).data)
    }
    const palette = gifPalette(samples)
    const index = paletteIndexer(palette)
    const stillIndex = new Uint8Array(width * height)
    index(still, stillIndex, width, 0, 0, width, height)
    const cur = new Uint8Array(width * height)
    const gif = gifWriter(width, height, palette)
    const delays = gifDelays(frames, opts.duration)
    for (let i = 0; i < frames; i++) {
      signal.throwIfAborted()
      cur.set(stillIndex)
      for (const r of dirtyTiles(render(ctx, i), width, height)) index(ctx.getImageData(r.x, r.y, r.width, r.height).data, cur, width, r.x, r.y, r.width, r.height)
      gif.add(cur, delays[i])
      progress(i + 1)
      await yieldToUi()
    }
    const bytes = gif.finish()
    return { ...result, blob: new Blob([bytes], { type: "image/gif" }), format: "gif", elapsed: performance.now() - started }
  }

  const ctx = canvas.getContext("2d")
  if (!ctx) throw new Error("This system cannot draw images (no 2D canvas)")
  const job: VideoJob = { width, height, fps, frames, canvas, signal, onProgress: progress, draw: (i) => void render(ctx, i) }
  const blob = support.method === "webcodecs" ? await encodeMp4(job, support.codec) : await recordVideo(job, support.mimeType)
  return { ...result, blob, format: support.container, elapsed: performance.now() - started }
}

// Animated GIF encoding for the diagram export. Only the pulses change between frames, so the palette is
// quantized once (the still diagram plus sample pulses: colors stay stable across frames) and every frame
// after the first stores just the pixels that changed; the rest is the transparent index over the previous
// frame, which LZW packs into almost nothing.
import { GIFEncoder, nearestColorIndex, quantize, type Palette } from "gifenc"

/** Largest GIF the export writes: beyond this, files and decoding get heavy for social media. */
export const GIF_MAX_SIDE = 1920
export const GIF_MAX_PIXELS = 2_600_000

/** Frames per second: 20, or 15 above Full HD's pixel count to keep encoding and playback light. */
export function gifFrameRate(width: number, height: number) {
  return width * height > 1920 * 1080 ? 15 : 20
}

/**
 * Frame delays in milliseconds, whole centiseconds (GIF's unit) that add up to exactly `duration`
 * seconds, so a 15 fps loop does not drift (6.67 cs frames become 7, 7, 6, …).
 */
export function gifDelays(count: number, duration: number) {
  const total = Math.round(duration * 100)
  return Array.from({ length: count }, (_, i) => (Math.round(((i + 1) * total) / count) - Math.round((i * total) / count)) * 10)
}

/** Colors: at most 255, leaving one index for "unchanged" (transparent). */
export function gifPalette(samples: (Uint8Array | Uint8ClampedArray)[]): Palette {
  const size = samples.reduce((n, s) => n + s.length, 0)
  const all = new Uint8Array(size)
  let at = 0
  for (const s of samples) {
    all.set(s, at)
    at += s.length
  }
  return quantize(all, 255)
}

/**
 * Maps RGBA pixels to palette indices through an RGB565 cache (as gifenc's applyPalette does), kept across
 * frames: `index(rgba, out, outWidth, x, y, w, h)` writes the `w`×`h` block at (x, y) of `out`.
 */
export function paletteIndexer(palette: Palette) {
  const cache = new Int16Array(65536).fill(-1)
  return (rgba: Uint8Array | Uint8ClampedArray, out: Uint8Array, outWidth: number, x: number, y: number, w: number, h: number) => {
    for (let row = 0; row < h; row++) {
      let o = (y + row) * outWidth + x
      let i = row * w * 4
      for (let col = 0; col < w; col++, o++, i += 4) {
        const r = rgba[i]
        const g = rgba[i + 1]
        const b = rgba[i + 2]
        const key = ((r >> 3) << 11) | ((g >> 2) << 5) | (b >> 3)
        let idx = cache[key]
        if (idx < 0) idx = cache[key] = nearestColorIndex(palette, [r, g, b])
        out[o] = idx
      }
    }
  }
}

/** `out` = `cur` where it differs from `prev`, `transparent` where it does not. */
export function diffFrame(cur: Uint8Array, prev: Uint8Array, out: Uint8Array, transparent: number) {
  for (let i = 0; i < cur.length; i++) out[i] = cur[i] === prev[i] ? transparent : cur[i]
}

/** A looping GIF written frame by frame from palette indices. */
export function gifWriter(width: number, height: number, palette: Palette) {
  const transparent = palette.length
  const encoder = GIFEncoder({ initialCapacity: 1 << 20 })
  const out = new Uint8Array(width * height)
  let prev: Uint8Array | null = null
  return {
    /** Adds a frame; `index` is not kept, but the next frame is compared with a copy of it. */
    add(index: Uint8Array, delay: number) {
      if (!prev) {
        encoder.writeFrame(index, width, height, { palette: [...palette, [255, 0, 255]], delay, repeat: 0, dispose: 1 })
        prev = index.slice()
        return
      }
      diffFrame(index, prev, out, transparent)
      encoder.writeFrame(out, width, height, { delay, transparent: true, transparentIndex: transparent, dispose: 1 })
      prev.set(index)
    },
    finish() {
      encoder.finish()
      return encoder.bytes()
    },
  }
}

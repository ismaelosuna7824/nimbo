import { describe, expect, test } from "bun:test"
import { diffFrame, gifDelays, gifFrameRate, gifPalette, gifWriter, paletteIndexer } from "./encode-gif"

describe("GIF timing and size", () => {
  test("delays are whole centiseconds adding up to the loop", () => {
    expect(new Set(gifDelays(120, 6))).toEqual(new Set([50]))
    const d15 = gifDelays(90, 6)
    expect(d15.reduce((a, b) => a + b, 0)).toBe(6000)
    expect(new Set(d15)).toEqual(new Set([60, 70]))
  })

  test("drops to 15 fps above Full HD", () => {
    expect(gifFrameRate(1080, 1350)).toBe(20)
    expect(gifFrameRate(1920, 1080)).toBe(20)
    expect(gifFrameRate(1920, 1200)).toBe(15)
  })
})

/** RGBA pixels of `colors`, each repeated `n` times. */
const pixels = (colors: number[][], n = 1) => Uint8Array.from(colors.flatMap((c) => Array.from({ length: n }, () => [...c, 255]).flat()))

describe("palette", () => {
  test("keeps the diagram's and the pulse's colors and leaves room for the transparent index", () => {
    const white = [255, 255, 255]
    const ink = [15, 23, 42]
    const accent = [249, 115, 22]
    const palette = gifPalette([pixels([white], 1000), pixels([ink], 50), pixels([accent], 5)])
    expect(palette.length).toBeLessThanOrEqual(255)
    const index = paletteIndexer(palette)
    const out = new Uint8Array(3)
    index(pixels([white, ink, accent]), out, 3, 0, 0, 3, 1)
    const near = (i: number, c: number[]) => palette[out[i]].every((v, k) => Math.abs(v - c[k]) <= 8)
    expect([near(0, white), near(1, ink), near(2, accent)]).toEqual([true, true, true])
  })

  test("indexes a block into its place in the frame", () => {
    const index = paletteIndexer([
      [0, 0, 0],
      [255, 255, 255],
    ])
    const out = new Uint8Array(16)
    index(pixels([[250, 250, 250]], 4), out, 4, 1, 1, 2, 2)
    expect([...out]).toEqual([0, 0, 0, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 0])
  })
})

describe("frames", () => {
  test("only changed pixels are stored; the rest is transparent", () => {
    const out = new Uint8Array(4)
    diffFrame(Uint8Array.from([1, 2, 3, 4]), Uint8Array.from([1, 9, 3, 9]), out, 255)
    expect([...out]).toEqual([255, 2, 255, 4])
  })

  test("writes a looping GIF89a with one image per frame", () => {
    const palette = [
      [255, 255, 255],
      [249, 115, 22],
    ]
    const gif = gifWriter(4, 3, palette)
    const frame = new Uint8Array(12)
    gif.add(frame, 50)
    frame[5] = 1
    gif.add(frame, 50)
    gif.add(frame, 50)
    const bytes = gif.finish()
    expect(new TextDecoder().decode(bytes.subarray(0, 6))).toBe("GIF89a")
    expect([bytes[6] | (bytes[7] << 8), bytes[8] | (bytes[9] << 8)]).toEqual([4, 3])
    expect(new TextDecoder().decode(bytes)).toContain("NETSCAPE2.0")
    // Graphic control extensions: one per frame, 5 cs each, "do not dispose" so unchanged pixels show through.
    const gces = [...bytes.keys()].filter((i) => bytes[i] === 0x21 && bytes[i + 1] === 0xf9)
    expect(gces.length).toBe(3)
    expect(gces.map((i) => bytes[i + 4] | (bytes[i + 5] << 8))).toEqual([5, 5, 5])
    expect(gces.map((i) => (bytes[i + 3] >> 2) & 7)).toEqual([1, 1, 1])
    expect(gces.map((i) => bytes[i + 3] & 1)).toEqual([0, 1, 1])
    expect(bytes[bytes.length - 1]).toBe(0x3b)
  })
})

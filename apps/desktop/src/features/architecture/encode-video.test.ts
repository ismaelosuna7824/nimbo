import { describe, expect, test } from "bun:test"
import { avcCodecs, videoBitrate } from "./encode-video"

describe("avcCodecs", () => {
  test("picks the lowest H.264 level that holds the frame, High profile first", () => {
    expect(avcCodecs(1080, 1350, 30)).toEqual(["avc1.640028", "avc1.4d0028", "avc1.42e028"])
    expect(avcCodecs(1920, 1080, 30)[0]).toBe("avc1.640028")
    expect(avcCodecs(720, 720, 30)[0]).toBe("avc1.64001f")
    expect(avcCodecs(3840, 2160, 30)[0]).toBe("avc1.640033")
  })

  test("nothing beyond H.264's largest frame", () => {
    expect(avcCodecs(8192, 4320, 30)).toEqual([])
  })
})

describe("videoBitrate", () => {
  test("scales with pixels per second within 2–24 Mbps", () => {
    expect(videoBitrate(1080, 1350, 30)).toBe(Math.round(1080 * 1350 * 30 * 0.12))
    expect(videoBitrate(320, 240, 30)).toBe(2e6)
    expect(videoBitrate(3840, 2160, 60)).toBe(24e6)
  })
})

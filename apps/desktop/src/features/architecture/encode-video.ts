// Video encoding for the diagram export. H.264 MP4 (what LinkedIn, X and Instagram take) through WebCodecs'
// VideoEncoder and Mediabunny's MP4 muxer, frame by frame and faster than real time. Where WebCodecs cannot
// encode H.264 (some WebKitGTK builds), MediaRecorder records a canvas stream in real time: MP4 when it
// can, else WebM — the file is then named and labelled WebM, never passed off as MP4.

export type VideoContainer = "mp4" | "webm"

export type VideoSupport =
  | { method: "webcodecs"; container: "mp4"; codec: string }
  | { method: "recorder"; container: VideoContainer; mimeType: string }

export const VIDEO_FPS = 30
/** H.264 level 5.1's largest frame (36 864 macroblocks) holds 4K; stay within it. */
export const VIDEO_MAX_SIDE = 3840
export const VIDEO_MAX_PIXELS = 3840 * 2160

/** H.264 levels: largest frame (macroblocks) and macroblocks per second, with the level's codec-string byte. */
const AVC_LEVELS = [
  { level: 0x1f, frame: 3600, rate: 108_000 }, // 3.1
  { level: 0x20, frame: 5120, rate: 216_000 }, // 3.2
  { level: 0x28, frame: 8192, rate: 245_760 }, // 4.0
  { level: 0x2a, frame: 8704, rate: 522_240 }, // 4.2
  { level: 0x32, frame: 22_080, rate: 589_824 }, // 5.0
  { level: 0x33, frame: 36_864, rate: 983_040 }, // 5.1
  { level: 0x34, frame: 36_864, rate: 2_073_600 }, // 5.2
]
/** High, Main and Constrained Baseline: profile_idc and constraint flags of the codec string. */
const AVC_PROFILES = ["6400", "4d00", "42e0"]

/** "avc1.PPCCLL" strings for a `width`×`height` video at `fps`, best profile first, at the lowest level that holds it. */
export function avcCodecs(width: number, height: number, fps: number): string[] {
  const frame = Math.ceil(width / 16) * Math.ceil(height / 16)
  const level = AVC_LEVELS.find((l) => frame <= l.frame && frame * fps <= l.rate)
  if (!level) return []
  const hex = level.level.toString(16).padStart(2, "0")
  return AVC_PROFILES.map((p) => `avc1.${p}${hex}`)
}

/** Bitrate for flat diagram graphics: ~0.12 bits per pixel per frame, 2–24 Mbps. */
export function videoBitrate(width: number, height: number, fps: number) {
  return Math.round(Math.min(24e6, Math.max(2e6, width * height * fps * 0.12)))
}

const RECORDER_TYPES: { mimeType: string; container: VideoContainer }[] = [
  { mimeType: "video/mp4;codecs=avc1.640028", container: "mp4" },
  { mimeType: "video/mp4;codecs=avc1", container: "mp4" },
  { mimeType: "video/mp4", container: "mp4" },
  { mimeType: "video/webm;codecs=vp9", container: "webm" },
  { mimeType: "video/webm;codecs=vp8", container: "webm" },
  { mimeType: "video/webm", container: "webm" },
]

function encoderConfig(codec: string, width: number, height: number, fps: number): VideoEncoderConfig {
  return { codec, width, height, bitrate: videoBitrate(width, height, fps), framerate: fps, avc: { format: "avc" } }
}

/** How this browser can encode a `width`×`height` video (even sides), or null when it cannot. */
export async function videoSupport(width: number, height: number, fps = VIDEO_FPS): Promise<VideoSupport | null> {
  if (typeof VideoEncoder !== "undefined" && typeof VideoFrame !== "undefined") {
    for (const codec of avcCodecs(width, height, fps)) {
      try {
        const { supported } = await VideoEncoder.isConfigSupported(encoderConfig(codec, width, height, fps))
        if (supported) return { method: "webcodecs", container: "mp4", codec }
      } catch {
        // An invalid config throws instead of answering "unsupported"; try the next profile.
      }
    }
  }
  if (typeof MediaRecorder !== "undefined" && typeof HTMLCanvasElement.prototype.captureStream === "function") {
    const hit = RECORDER_TYPES.find((t) => MediaRecorder.isTypeSupported(t.mimeType))
    if (hit) return { method: "recorder", ...hit }
  }
  return null
}

export interface VideoJob {
  width: number
  height: number
  fps: number
  frames: number
  /** Draws frame `i` on `canvas` (the size of the video). */
  draw: (i: number) => void
  canvas: HTMLCanvasElement
  signal: AbortSignal
  onProgress: (done: number) => void
}

/** Lets the page paint and handle input between frames (no 4 ms timer clamping). */
export function yieldToUi() {
  // `scheduler.yield` is missing in WebKit; a message round trip is the next best macrotask.
  if (typeof scheduler !== "undefined" && typeof scheduler.yield === "function") return scheduler.yield()
  return new Promise<void>((resolve) => {
    const ch = new MessageChannel()
    ch.port1.onmessage = () => resolve()
    ch.port2.postMessage(null)
  })
}

/** Encodes the frames into an H.264 MP4 with WebCodecs, as fast as the encoder goes. */
export async function encodeMp4(job: VideoJob, codec: string): Promise<Blob> {
  // Loaded on the first video export only: the muxer stays out of the export dialog's chunk.
  const { Output, Mp4OutputFormat, BufferTarget, EncodedVideoPacketSource, EncodedPacket } = await import("mediabunny")
  const target = new BufferTarget()
  const output = new Output({ format: new Mp4OutputFormat({ fastStart: "in-memory" }), target })
  const source = new EncodedVideoPacketSource("avc")
  output.addVideoTrack(source, { frameRate: job.fps })
  await output.start()

  let failure: unknown
  let writing = Promise.resolve()
  const encoder = new VideoEncoder({
    output: (chunk, meta) => {
      const packet = EncodedPacket.fromEncodedChunk(chunk)
      writing = writing.then(() => source.add(packet, meta)).catch((err: unknown) => void (failure ??= err))
    },
    error: (err) => void (failure ??= err),
  })
  try {
    encoder.configure(encoderConfig(codec, job.width, job.height, job.fps))
    const frameUs = 1e6 / job.fps
    for (let i = 0; i < job.frames; i++) {
      if (failure) throw failure
      job.signal.throwIfAborted()
      job.draw(i)
      const frame = new VideoFrame(job.canvas, { timestamp: Math.round(i * frameUs), duration: Math.round(frameUs) })
      // A key frame every 2 s keeps seeking and platform re-encoding cheap.
      encoder.encode(frame, { keyFrame: i % (job.fps * 2) === 0 })
      frame.close()
      job.onProgress(i + 1)
      while (encoder.encodeQueueSize > 4) await new Promise((r) => setTimeout(r, 1))
      await yieldToUi()
    }
    await encoder.flush()
    await writing
    if (failure) throw failure
    await output.finalize()
  } catch (err) {
    if (output.state !== "finalized" && output.state !== "canceled") await output.cancel()
    throw err
  } finally {
    if (encoder.state !== "closed") encoder.close()
  }
  return new Blob([target.buffer!], { type: "video/mp4" })
}

/** Records the frames with MediaRecorder from a canvas stream, in real time (the recorder timestamps them). */
export async function recordVideo(job: VideoJob, mimeType: string): Promise<Blob> {
  const stream = job.canvas.captureStream(job.fps)
  const recorder = new MediaRecorder(stream, { mimeType, videoBitsPerSecond: videoBitrate(job.width, job.height, job.fps) })
  const chunks: Blob[] = []
  recorder.ondataavailable = (e) => e.data.size && chunks.push(e.data)
  const stopped = new Promise<void>((resolve, reject) => {
    recorder.onstop = () => resolve()
    recorder.onerror = (e) => reject(e instanceof ErrorEvent && e.error ? e.error : new Error("The video recording failed"))
  })
  try {
    job.draw(0)
    recorder.start()
    const start = performance.now()
    for (let i = 0; i < job.frames; i++) {
      job.signal.throwIfAborted()
      job.draw(i)
      job.onProgress(i + 1)
      const wait = start + ((i + 1) * 1000) / job.fps - performance.now()
      await new Promise((r) => setTimeout(r, Math.max(0, wait)))
    }
    recorder.stop()
    await stopped
  } finally {
    if (recorder.state !== "inactive") recorder.stop()
    for (const t of stream.getTracks()) t.stop()
  }
  return new Blob(chunks, { type: mimeType.split(";")[0] })
}

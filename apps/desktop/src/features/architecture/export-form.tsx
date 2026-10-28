import { DownloadIcon } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Progress } from "@/components/ui/progress"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useWorkflowStore } from "@/state/workflow-store"
import { bestPreset, frameCount, type SizePreset } from "./animate"
import { animationFrame, DURATIONS, exportAnimation, type AnimatedFormat, type AnimationDuration } from "./animation-export"
import { buildCanvasBounds, exportBuildCanvas } from "./build-export"
import { videoSupport, type VideoSupport } from "./encode-video"
import { exportBounds, exportDiagram, exportFileName, fitScale, saveFile, type ImageFormat, type ImageOptions, type ImageScale } from "./export"
import { useArchUi } from "./store"

const SCALES: ImageScale[] = [1, 2, 3, 4]
type Format = ImageFormat | AnimatedFormat

const PRESETS: { value: SizePreset; label: string }[] = [
  { value: "fit", label: "Fit diagram" },
  { value: "square", label: "Square 1080 × 1080" },
  { value: "portrait", label: "Portrait 1080 × 1350 (LinkedIn/Instagram)" },
  { value: "landscape", label: "Landscape 1920 × 1080" },
]

let videoProbe: Promise<VideoSupport | null> | undefined
/** How this system encodes video, probed once at Full HD (the export checks its actual size again). */
function probeVideo() {
  videoProbe ??= videoSupport(1920, 1080).catch(() => null)
  return videoProbe
}

const kb = (bytes: number) => (bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`)

/** Export form: the Architecture diagram (PNG/SVG, animated GIF/video) or the Build canvas (PNG). */
export function ExportForm({ target, onDone }: { target: "architecture" | "build"; onDone: () => void }) {
  const name = useWorkflowStore((s) => s.workflow?.name ?? "workflow")
  const archScene = useArchUi((s) => s.scene)
  const diagram = target === "architecture"
  const [opts, setOpts] = useState<ImageOptions>({ format: "png", scale: 2, background: "solid", title: true, legend: true })
  const [chosen, setChosen] = useState<Format>("png")
  // Social size that shows this diagram largest: landscape for wide diagrams, portrait for tall ones.
  const [preset, setPreset] = useState<SizePreset>(() => (archScene ? bestPreset(exportBounds(archScene, opts)) : "portrait"))
  const [duration, setDuration] = useState<AnimationDuration>(6)
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState<number | null>(null)
  const [video, setVideo] = useState<VideoSupport | null | undefined>(undefined)
  const abort = useRef<AbortController | null>(null)
  const format: Format = diagram ? chosen : "png"
  const animated = format === "gif" || format === "video"

  useEffect(() => {
    let live = true
    if (diagram) void probeVideo().then((v) => live && setVideo(v))
    return () => {
      live = false
    }
  }, [diagram])
  // Closing the dialog stops an export in progress.
  useEffect(() => () => abort.current?.abort(), [])

  const anim = useMemo(
    () => (animated && archScene ? { format, preset, duration, scale: opts.scale, title: opts.title, legend: opts.legend } : null),
    [animated, archScene, format, preset, duration, opts.scale, opts.title, opts.legend],
  )
  const frame = useMemo(() => (anim && archScene ? animationFrame(archScene, anim) : null), [anim, archScene])
  const size = useMemo(() => {
    const b = diagram ? (archScene ? exportBounds(archScene, opts) : null) : buildCanvasBounds()
    return b && { width: b.width, height: b.height }
  }, [diagram, archScene, opts])

  const scale = size && format === "png" ? fitScale(size.width, size.height, opts.scale) : 1
  const outW = frame ? frame.width : size ? Math.round(size.width * scale) : 0
  const outH = frame ? frame.height : size ? Math.round(size.height * scale) : 0
  const videoExt = video?.container ?? "mp4"
  const fileFormat = format === "video" ? videoExt : format

  const run = async () => {
    setBusy(true)
    const ctrl = new AbortController()
    abort.current = ctrl
    try {
      if (anim && archScene) {
        setProgress(0)
        let shown = 0
        const out = await exportAnimation(archScene, anim, {
          signal: ctrl.signal,
          onProgress: (p) => {
            if (p - shown < 0.01 && p < 1) return
            shown = p
            setProgress(p)
          },
        })
        const file = exportFileName(name, "architecture", out.format)
        if (await saveFile(out.blob, file, out.format)) {
          toast.success(out.format === "gif" ? "Animation exported" : "Video exported", {
            description: `${file} · ${out.width} × ${out.height} px · ${duration} s at ${out.fps} fps · ${kb(out.blob.size)} · encoded in ${(out.elapsed / 1000).toFixed(1)} s`,
          })
          onDone()
        }
        return
      }
      const image = diagram
        ? archScene
          ? await exportDiagram(archScene, { ...opts, format: format as ImageFormat })
          : null
        : await exportBuildCanvas(opts.scale, opts.background)
      if (!image) throw new Error("The diagram is still being laid out")
      const file = exportFileName(name, diagram ? "architecture" : "canvas", format as ImageFormat)
      if (await saveFile(image.blob, file, format as ImageFormat)) {
        toast.success("Image exported", {
          description: `${file} · ${image.width} × ${image.height} px · ${kb(image.blob.size)}`,
        })
        onDone()
      }
    } catch (err) {
      if (!ctrl.signal.aborted) toast.error("Export failed", { description: errorMessage(err) })
    } finally {
      abort.current = null
      setBusy(false)
      setProgress(null)
    }
  }

  const videoLabel = video?.container === "webm" ? "Video (WebM)" : "Video (MP4)"
  const videoItem = (
    <ToggleGroupItem value="video" disabled={!video}>
      {videoLabel}
    </ToggleGroupItem>
  )

  return (
    <>
      <DialogHeader>
        <DialogTitle>{diagram ? "Export architecture diagram" : "Export canvas image"}</DialogTitle>
        <DialogDescription>
          {!diagram
            ? "The whole Build canvas as it looks in the app."
            : animated
              ? "The whole diagram with requests flowing along its arrows, in a seamless loop for social media posts."
              : "The whole diagram, not just what is on screen, with fonts and icons embedded."}
        </DialogDescription>
      </DialogHeader>
      <fieldset className="grid gap-4" disabled={busy}>
        {diagram && (
          <Row label="Format">
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              value={chosen}
              onValueChange={(v) => {
                if (!v) return
                setChosen(v as Format)
                if (v === "png" || v === "svg") setOpts((o) => ({ ...o, format: v }))
              }}
              aria-label="Format"
            >
              <ToggleGroupItem value="png">PNG</ToggleGroupItem>
              <ToggleGroupItem value="svg">SVG</ToggleGroupItem>
              <ToggleGroupItem value="gif">GIF</ToggleGroupItem>
              {video === null ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span tabIndex={0}>{videoItem}</span>
                  </TooltipTrigger>
                  <TooltipContent>This system cannot encode video: neither WebCodecs H.264 nor MediaRecorder is available.</TooltipContent>
                </Tooltip>
              ) : (
                videoItem
              )}
            </ToggleGroup>
          </Row>
        )}
        {animated && (
          <Row label="Size">
            <Select value={preset} onValueChange={(v) => setPreset(v as SizePreset)}>
              <SelectTrigger size="sm" className="w-72" aria-label="Size">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PRESETS.map((p) => (
                  <SelectItem key={p.value} value={p.value}>
                    {p.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Row>
        )}
        {(format === "png" || (animated && preset === "fit")) && (
          <Row label="Scale">
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              value={String(opts.scale)}
              onValueChange={(v) => v && setOpts((o) => ({ ...o, scale: Number(v) as ImageScale }))}
              aria-label="Scale"
            >
              {SCALES.map((s) => (
                <ToggleGroupItem key={s} value={String(s)}>
                  {s}×
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </Row>
        )}
        {animated && (
          <Row label="Duration">
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              value={String(duration)}
              onValueChange={(v) => v && setDuration(Number(v) as AnimationDuration)}
              aria-label="Duration"
            >
              {DURATIONS.map((d) => (
                <ToggleGroupItem key={d} value={String(d)}>
                  {d} s
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </Row>
        )}
        {!animated && (
          <Row label="Background">
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              value={opts.background}
              onValueChange={(v) => v && setOpts((o) => ({ ...o, background: v as ImageOptions["background"] }))}
              aria-label="Background"
            >
              <ToggleGroupItem value="solid">{diagram ? "White" : "Canvas"}</ToggleGroupItem>
              <ToggleGroupItem value="transparent">Transparent</ToggleGroupItem>
            </ToggleGroup>
          </Row>
        )}
        {diagram && (
          <div className="flex gap-6">
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={opts.title} onCheckedChange={(v) => setOpts((o) => ({ ...o, title: v === true }))} />
              Include title
            </label>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={opts.legend} onCheckedChange={(v) => setOpts((o) => ({ ...o, legend: v === true }))} />
              Include legend
            </label>
          </div>
        )}
        <div className="grid gap-1 text-xs text-muted-foreground">
          <p data-testid="export-size" data-width={outW} data-height={outH}>
            {!size
              ? "Nothing to export yet"
              : frame
                ? `${outW} × ${outH} px · ${duration} s loop · ${frame.fps} fps · white background`
                : `${outW} × ${outH} px${format === "svg" ? " (vector)" : ""}`}
            {format === "png" && scale < opts.scale && ` — reduced to ${scale}× to stay within image size limits`}
          </p>
          {frame && preset === "fit" && frame.scale < opts.scale && (
            <p>
              Drawn at {frame.scale.toFixed(2)}× to stay within {format === "gif" ? "1920" : "3840"} px.
            </p>
          )}
          {frame && preset !== "fit" && (
            <p>
              Diagram drawn at {frame.scale.toFixed(2)}×{frame.scale < 0.6 ? ": text may be hard to read — try another size or turn off the legend." : "."}
            </p>
          )}
          {format === "gif" && <p>GIF: 256 colors, {frame?.fps ?? 20} fps (15 above Full HD to keep files small), loops forever.</p>}
          {format === "video" && video?.method === "webcodecs" && <p>H.264 MP4 for LinkedIn, X and Instagram; they loop short videos.</p>}
          {format === "video" && video?.method === "recorder" && (
            <p>
              {video.container === "mp4"
                ? `This system records the MP4 in real time: it takes ${duration} s.`
                : `This system cannot encode MP4: the video is recorded in real time as WebM. Convert it to MP4 before posting to Instagram or LinkedIn.`}
            </p>
          )}
        </div>
        {progress !== null && (
          <div className="grid gap-1.5" data-testid="export-progress">
            <Progress value={progress * 100} aria-label="Export progress" />
            <p className="text-xs text-muted-foreground">
              {frame ? `Encoding frame ${Math.round(progress * frameCount(duration, frame.fps))} of ${frameCount(duration, frame.fps)}…` : "Encoding…"}
            </p>
          </div>
        )}
      </fieldset>
      <DialogFooter>
        <Button variant="ghost" onClick={() => (busy ? abort.current?.abort() : onDone())}>
          Cancel
        </Button>
        <Button disabled={busy || !size || (format === "video" && !video)} onClick={() => void run()}>
          {busy ? <Spinner /> : <DownloadIcon />} Export {fileFormat.toUpperCase()}
        </Button>
      </DialogFooter>
    </>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <Label className="text-sm">{label}</Label>
      {children}
    </div>
  )
}

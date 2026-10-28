import { DownloadIcon } from "lucide-react"
import { useMemo, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useWorkflowStore } from "@/state/workflow-store"
import { buildCanvasBounds, exportBuildCanvas } from "./build-export"
import { exportBounds, exportDiagram, fitScale, imageFileName, saveImage, type ImageFormat, type ImageOptions, type ImageScale } from "./export"
import { useArchUi } from "./store"

const SCALES: ImageScale[] = [1, 2, 3, 4]

/** Export image form: the Architecture diagram (PNG/SVG) or the Build canvas (PNG). */
export function ExportForm({ target, onDone }: { target: "architecture" | "build"; onDone: () => void }) {
  const name = useWorkflowStore((s) => s.workflow?.name ?? "workflow")
  const archScene = useArchUi((s) => s.scene)
  const diagram = target === "architecture"
  const [opts, setOpts] = useState<ImageOptions>({ format: "png", scale: 2, background: "solid", title: true, legend: true })
  const [busy, setBusy] = useState(false)
  const format: ImageFormat = diagram ? opts.format : "png"
  const size = useMemo(() => {
    const b = diagram ? (archScene ? exportBounds(archScene, opts) : null) : buildCanvasBounds()
    return b && { width: b.width, height: b.height }
  }, [diagram, archScene, opts])

  const scale = size && format === "png" ? fitScale(size.width, size.height, opts.scale) : 1
  const outW = size ? Math.round(size.width * scale) : 0
  const outH = size ? Math.round(size.height * scale) : 0

  const run = async () => {
    setBusy(true)
    try {
      const image = diagram
        ? archScene
          ? await exportDiagram(archScene, { ...opts, format })
          : null
        : await exportBuildCanvas(opts.scale, opts.background)
      if (!image) throw new Error("The diagram is still being laid out")
      const file = imageFileName(name, diagram ? "architecture" : "canvas", format)
      if (await saveImage(image.blob, file, format)) {
        toast.success("Image exported", {
          description: `${file} · ${image.width} × ${image.height} px · ${Math.max(1, Math.round(image.blob.size / 1024))} KB`,
        })
        onDone()
      }
    } catch (err) {
      toast.error("Export failed", { description: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <DialogHeader>
        <DialogTitle>{diagram ? "Export architecture diagram" : "Export canvas image"}</DialogTitle>
        <DialogDescription>
          {diagram
            ? "The whole diagram, not just what is on screen, with fonts and icons embedded."
            : "The whole Build canvas as it looks in the app."}
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-4">
        {diagram && (
          <Row label="Format">
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              value={opts.format}
              onValueChange={(v) => v && setOpts((o) => ({ ...o, format: v as ImageFormat }))}
              aria-label="Format"
            >
              <ToggleGroupItem value="png">PNG</ToggleGroupItem>
              <ToggleGroupItem value="svg">SVG</ToggleGroupItem>
            </ToggleGroup>
          </Row>
        )}
        {format === "png" && (
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
        <p className="text-xs text-muted-foreground" data-testid="export-size" data-width={outW} data-height={outH}>
          {size ? `${outW} × ${outH} px${format === "svg" ? " (vector)" : ""}` : "Nothing to export yet"}
          {format === "png" && scale < opts.scale && ` — reduced to ${scale}× to stay within image size limits`}
        </p>
      </div>
      <DialogFooter>
        <Button variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button disabled={busy || !size} onClick={() => void run()}>
          {busy ? <Spinner /> : <DownloadIcon />} Export {format.toUpperCase()}
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

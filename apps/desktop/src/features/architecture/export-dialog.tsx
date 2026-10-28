import { lazy, Suspense } from "react"
import { Dialog, DialogContent } from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/spinner"
import { useArchUi } from "./store"

// The form reaches the diagram model and its icon registry (export.ts → scene → derive): load it on first export,
// like the Architecture view, instead of with the editor.
const ExportForm = lazy(() => import("./export-form").then((m) => ({ default: m.ExportForm })))

/** Export image dialog: the Architecture diagram (PNG/SVG) or the Build canvas (PNG). */
export function ExportImageDialog() {
  const target = useArchUi((s) => s.exportTarget)
  const close = useArchUi((s) => s.closeExport)
  return (
    <Dialog open={!!target} onOpenChange={(o) => !o && close()}>
      <DialogContent className="sm:max-w-md">
        {target && (
          <Suspense
            fallback={
              <div className="flex h-40 items-center justify-center">
                <Spinner />
              </div>
            }
          >
            <ExportForm target={target} onDone={close} />
          </Suspense>
        )}
      </DialogContent>
    </Dialog>
  )
}

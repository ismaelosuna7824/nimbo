import { lazyModule } from "@/components/lazy-module"
import type { WorkflowKind } from "@/lib/api/types"

// Each workspace kind's editor UI (canvas, inspector, palette) is a chunk of its own, so a workflow loads only its
// kind's code. The store's open() loads it before showing the workflow: the editor then mounts it without suspending
// (see lazyComponent), and a reopened workflow shows no fallback.
export const terraformCode = lazyModule(() => import("./terraform"))
export const dockerCode = lazyModule(() => import("./docker"))
export const kubernetesCode = lazyModule(() => import("./kubernetes"))

const LAST_KIND_KEY = "nimbo.lastWorkflowKind"

export function kindCode(kind: WorkflowKind | undefined) {
  return kind === "docker" ? dockerCode : kind === "kubernetes" ? kubernetesCode : terraformCode
}

/** Loads a workflow kind's editor code, remembering the kind for the next launch (preloadLastKind). */
export function loadKindCode(kind: WorkflowKind | undefined) {
  localStorage.setItem(LAST_KIND_KEY, kind ?? "")
  return kindCode(kind).preload()
}

/**
 * At launch, while the last workflow is fetched: starts loading its kind's code next to the editor's. Waiting for the
 * workflow to learn its kind would fetch the chunk only once the editor's code has run, delaying the first paint.
 */
export function preloadLastKind() {
  const kind = localStorage.getItem(LAST_KIND_KEY)
  if (kind !== null)
    void kindCode(kind as WorkflowKind)
      .preload()
      .catch(() => undefined)
}

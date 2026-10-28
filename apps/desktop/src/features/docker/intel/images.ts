// The Docker image catalog. Kept apart from the editor intel (./index) so the palettes and the command palette can
// read it without pulling CodeMirror into their bundle.

import { api } from "@/lib/api/client"
import type { DockerImage } from "@/lib/api/types"

let imagesPromise: Promise<DockerImage[]> | undefined

/** The image catalog, fetched once per session. */
export function loadDockerImages(): Promise<DockerImage[]> {
  imagesPromise ??= api.dockerImages().catch((err: unknown) => {
    imagesPromise = undefined
    throw err
  })
  return imagesPromise
}

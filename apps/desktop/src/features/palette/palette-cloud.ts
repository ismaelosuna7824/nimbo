import { ALL_CLOUDS, type Cloud } from "@/lib/api/types"

/** localStorage key of the palette's cloud tab the user picked for a workflow. */
export const paletteCloudKey = (workflowId: string) => `nimbo.paletteCloud.${workflowId}`

/** The palette's cloud tab: the one the user picked for this workflow, else the workflow's first cloud, else AWS. */
export function paletteCloud(picked: string | null | undefined, workflowClouds: readonly Cloud[] | undefined): Cloud {
  const known = ALL_CLOUDS.find((c) => c === picked)
  return known ?? workflowClouds?.[0] ?? "aws"
}

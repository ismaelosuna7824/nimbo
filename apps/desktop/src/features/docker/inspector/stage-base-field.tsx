import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import { useWorkflowStore } from "@/state/workflow-store"
import { CommitInput, Field } from "./fields"

/**
 * The FROM image of a build stage: a stage node, or a single-stage build's service (its only stage). Not for a stage
 * built on another stage of the same Dockerfile. Rewrites only the image of that FROM line.
 */
export function StageBaseField({ nodeId, from }: { nodeId: string; from: string }) {
  const id = useWorkflowStore((s) => s.workflow!.id)
  const commit = async (next: string) => {
    try {
      useWorkflowStore.getState().setWorkflow(await api.patchComposeStage(id, nodeId, next))
    } catch (err) {
      toast.error("Couldn't change the base image", { description: errorMessage(err) })
    }
  }
  return (
    <Field label="Base image (FROM)" htmlFor={`stage-from-${nodeId}`} hint="Rewrites the Dockerfile's FROM line; pin a version tag (nginx:1.27-alpine).">
      <CommitInput
        id={`stage-from-${nodeId}`}
        className="font-mono"
        list="docker-image-options"
        value={from}
        validate={(v) => (v !== "" && !/\s/.test(v) ? undefined : "An image reference, no spaces")}
        onCommit={(next) => void commit(next)}
      />
    </Field>
  )
}

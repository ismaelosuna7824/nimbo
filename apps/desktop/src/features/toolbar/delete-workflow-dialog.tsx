import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { deleteWorkflow } from "./workflow-actions"

/** Confirms deleting a workflow; open while `workflow` is set. */
export function DeleteWorkflowDialog({ workflow, onClose }: { workflow: { id: string; name: string } | null; onClose: () => void }) {
  return (
    <AlertDialog open={workflow !== null} onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete “{workflow?.name}”?</AlertDialogTitle>
          <AlertDialogDescription>
            The design and its local sandbox (containers, emulator data, Terraform state) are removed for good. Exported
            Terraform files on disk are not touched. To just hide it, archive it instead.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={() => workflow && void deleteWorkflow(workflow.id, workflow.name)}>
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

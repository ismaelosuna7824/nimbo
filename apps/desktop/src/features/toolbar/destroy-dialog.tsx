import { useState } from "react"
import { Checkbox } from "@/components/ui/checkbox"
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
import { useWorkflowStore } from "@/state/workflow-store"
import { runAction } from "./actions"
import { useUiStore } from "./ui-store"

export function DestroyDialog() {
  const open = useUiStore((s) => s.confirmDestroy)
  const setOpen = useUiStore((s) => s.setConfirmDestroy)
  const name = useWorkflowStore((s) => s.workflow?.name)
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  const docker = kind === "docker"
  const kube = kind === "kubernetes"
  const [removeVolumes, setRemoveVolumes] = useState(false)
  const [deleteCluster, setDeleteCluster] = useState(false)
  return (
    <AlertDialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        // Deleting data is opt-in every time.
        if (!o) {
          setRemoveVolumes(false)
          setDeleteCluster(false)
        }
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {docker ? `Stop “${name}”?` : kube ? `Remove “${name}” from the cluster?` : `Destroy everything in “${name}”?`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {docker
              ? "docker compose down stops and removes this workspace's containers and networks. Your compose files stay untouched and you can deploy again at any time."
              : kube
                ? "Deletes every object Deploy applied (and uninstalls Helm releases) from this workflow's local cluster. Your manifests stay untouched and you can deploy again at any time."
                : "Terraform will delete every deployed resource from your local sandbox. Your design stays untouched and you can deploy again at any time."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {docker && (
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={removeVolumes} onCheckedChange={(c) => setRemoveVolumes(c === true)} />
            Also delete the volumes (database data and other saved files)
          </label>
        )}
        {kube && (
          <div className="flex flex-col gap-2">
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={removeVolumes}
                onCheckedChange={(c) => setRemoveVolumes(c === true)}
                data-testid="kube-remove-volumes"
              />
              Also delete the PersistentVolumeClaims (database data and other saved files)
            </label>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={deleteCluster}
                onCheckedChange={(c) => setDeleteCluster(c === true)}
                data-testid="kube-delete-cluster"
              />
              Also delete the cluster (the next Deploy creates a fresh one)
            </label>
          </div>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            data-testid="destroy-confirm"
            onClick={() =>
              void runAction("destroy", docker ? { removeVolumes } : kube ? { removeVolumes, deleteCluster } : {})
            }
          >
            {docker ? "Stop and remove" : kube ? (deleteCluster ? "Remove and delete cluster" : "Remove") : "Destroy"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

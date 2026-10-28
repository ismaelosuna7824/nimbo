import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { ApiError, api } from "@/lib/api/client"
import type { ComposeRunAction, ComposeRunRequest, KubeRunRequest, RunAction } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { useUiStore } from "./ui-store"

const RUN_LABEL: Record<RunAction, string> = {
  validate: "Validation",
  plan: "Plan",
  apply: "Deploy",
  destroy: "Destroy",
  up: "Deploy",
  down: "Destroy",
  build: "Build",
  pull: "Pull",
  restart: "Restart",
  "cluster-stop": "Stop cluster",
  "cluster-start": "Start cluster",
  "cluster-delete": "Delete cluster",
}

/** What the toolbar's Deploy/Destroy mean in a Docker workspace: docker compose up/down. */
const COMPOSE_ACTION: Partial<Record<RunAction, ComposeRunAction>> = {
  apply: "up",
  destroy: "down",
  up: "up",
  down: "down",
  build: "build",
  pull: "pull",
  restart: "restart",
}

/** What the toolbar's Deploy/Destroy mean in a Kubernetes workspace: apply to / delete from its local cluster. */
const KUBE_ACTION: Partial<Record<RunAction, KubeRunRequest["action"]>> = {
  apply: "up",
  destroy: "down",
  up: "up",
  down: "down",
  restart: "restart",
  "cluster-stop": "cluster-stop",
  "cluster-start": "cluster-start",
  "cluster-delete": "cluster-delete",
}

export type RunOptions = { showLog?: boolean; confirmRunId?: string } & Omit<ComposeRunRequest, "action"> &
  Pick<KubeRunRequest, "workload" | "deleteCluster">

/**
 * Saves pending edits, starts a run and shows its log (unless showLog is false, e.g. when started from the load test
 * tab): Terraform in the sandbox, docker compose for a Docker workspace (Deploy = up, Destroy = down), or the local
 * cluster of a Kubernetes workspace (Deploy = up, Destroy = down).
 */
export async function runAction(action: RunAction, opts: RunOptions = {}) {
  const { showLog = true, workload, deleteCluster, confirmRunId, ...compose } = opts
  const store = useWorkflowStore.getState()
  const wf = store.workflow
  if (!wf) return
  const docker = wf.kind === "docker"
  const kube = wf.kind === "kubernetes"
  const composeAction = docker ? COMPOSE_ACTION[action] : undefined
  const kubeAction = kube ? KUBE_ACTION[action] : undefined
  if ((docker && !composeAction) || (kube && !kubeAction)) {
    toast.info(`${RUN_LABEL[action]} is for Terraform workflows`, {
      description: docker
        ? "Docker workspaces are checked as you edit; Deploy builds and starts them with docker compose."
        : "Kubernetes workspaces are checked as you edit; Deploy applies them to a local cluster on this machine.",
    })
    return
  }
  await store.flush()
  try {
    const run = kubeAction
      ? await api.startKubeRun(wf.id, {
          action: kubeAction,
          workload,
          removeVolumes: compose.removeVolumes,
          deleteCluster,
          timeoutSec: compose.timeoutSec,
        })
      : composeAction
        ? await api.startComposeRun(wf.id, { action: composeAction, ...compose })
        : await api.startRun(
            wf.id,
            action,
            useWorkflowStore.getState().workflow?.activeEnvironment,
            // Terraform apply: confirmRunId applies exactly the saved plan of that needs_confirmation run.
            action === "apply" && confirmRunId ? { confirmDestroy: true, confirmRunId } : undefined,
          )
    if (action === "apply" && !kube && !docker) watchApply(run.id)
    const s = useWorkflowStore.getState()
    if (!s.runs.some((r) => r.id === run.id)) useWorkflowStore.setState({ runs: [run, ...s.runs] })
    if (showLog) store.showBottom("deploy")
  } catch (err) {
    if (err instanceof ApiError && err.code === "port_conflict") {
      await offerFreePorts(action, opts)
      return
    }
    if (kube && err instanceof ApiError && err.code === "docker_missing") {
      toast.error("Docker is not running", {
        description: "The local Kubernetes cluster runs inside Docker. Start Docker Desktop or OrbStack, then deploy again.",
      })
      return
    }
    if (kube && err instanceof ApiError && err.status === 400 && err.diagnostics.length > 0) {
      store.showBottom("problems")
      toast.error(`${RUN_LABEL[action]} couldn't start`, {
        description: `The manifests don't render: ${err.diagnostics[0].message}${err.diagnostics.length > 1 ? ` (+${err.diagnostics.length - 1} more in Problems)` : ""}`,
      })
      return
    }
    toast.error(`${RUN_LABEL[action]} couldn't start`, { description: errorMessage(err) })
  }
}

/**
 * Remembers a Terraform apply this window started: if it ends needs_confirmation (its plan would delete or replace
 * existing resources), the Deploy confirmation dialog opens on its own.
 */
export function watchApply(runId: string) {
  useUiStore.getState().setWatchedApply(runId)
  checkWatchedApply()
}

/** Opens the Deploy confirmation once the watched apply needs it; forgets the apply once it ended otherwise. */
export function checkWatchedApply() {
  const ui = useUiStore.getState()
  if (!ui.watchedApply) return
  const run = useWorkflowStore.getState().runs.find((r) => r.id === ui.watchedApply)
  if (!run || run.status === "queued" || run.status === "running") return
  ui.setWatchedApply(null)
  if (run.status === "needs_confirmation") ui.setConfirmDeployRun(run.id)
}

/** compose up refused because fixed host ports are taken: say which and offer to publish on free ones instead. */
async function offerFreePorts(action: RunAction, opts: RunOptions) {
  const wf = useWorkflowStore.getState().workflow
  if (!wf) return
  const conflicts = await api
    .composePorts(wf.id)
    .then((r) => r.conflicts)
    .catch(() => [])
  const lines = conflicts.map(
    (c) => `${c.service}: port ${c.hostPort}${c.holder ? ` is used by ${c.holder}` : " is in use"} → ${c.suggestedPort}`,
  )
  toast.warning("Some host ports are already in use", {
    description: lines.length ? lines.join("\n") : "Another program already listens on a port the compose file publishes.",
    duration: 20_000,
    action: { label: "Use free ports", onClick: () => void runAction(action, { ...opts, remapPorts: true }) },
  })
}

export async function sandboxAction(action: "start" | "stop" | "reset") {
  const wf = useWorkflowStore.getState().workflow
  if (!wf) return
  try {
    const status = await api.sandboxAction(wf.id, action)
    if (useWorkflowStore.getState().workflow?.id === wf.id) useWorkflowStore.setState({ sandbox: status })
    if (action === "reset") {
      toast.success("Sandbox reset", { description: "Emulator data and Terraform state were wiped." })
      void useWorkflowStore.getState().refreshRuntime()
    }
  } catch (err) {
    toast.error(`Couldn't ${action} the sandbox`, { description: errorMessage(err) })
  }
}

/** True while a Terraform run of the open workflow is queued or running. */
export function useRunBusy() {
  return useWorkflowStore((s) => s.runs.some((r) => r.status === "queued" || r.status === "running"))
}

export async function openWorkflow(id: string) {
  try {
    await useWorkflowStore.getState().open(id)
    // Picking a workflow on Home shown over the open one (even that same one) goes back to the editor.
    useUiStore.getState().setHomeOpen(false)
  } catch (err) {
    toast.error("Couldn't open the workflow", { description: errorMessage(err) })
  }
}

const isMac = typeof navigator !== "undefined" && /Mac/i.test(navigator.platform)
/** Modifier key label for shortcut hints. */
export const MOD_KEY = isMac ? "⌘" : "Ctrl"

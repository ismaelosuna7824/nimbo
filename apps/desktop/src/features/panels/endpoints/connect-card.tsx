import { useState } from "react"
import { isTauri } from "@tauri-apps/api/core"
import { save } from "@tauri-apps/plugin-dialog"
import { writeTextFile } from "@tauri-apps/plugin-fs"
import { Boxes, Download, Play, Plug, ShieldCheck, Terminal } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { CloudBadge } from "@/components/cloud"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { SandboxAccess } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { runAction, sandboxAction, useRunBusy } from "@/features/toolbar/actions"
import { composeChipSummary } from "@/features/docker/deploy/compose-model"
import { kubeChipSummary, kubectlSnippet } from "@/features/kubernetes/deploy/kube-model"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { copyText } from "../lib/format"
import { CopyButton, FieldRow } from "./bits"

/** "Connect to your local cloud": what SDKs and CLIs need to reach the sandbox. */
export function ConnectCard({ sandbox }: { sandbox: SandboxAccess[] }) {
  const state = useWorkflowStore((s) => s.sandbox?.state ?? "absent")
  const [starting, setStarting] = useState(false)

  return (
    <section className="flex flex-col gap-2 rounded-lg border p-2.5">
      <h3 className="flex items-center gap-1.5 text-xs font-semibold">
        <Plug className="size-3.5 text-primary" /> Connect to your local cloud
      </h3>
      {sandbox.length === 0 ? (
        <>
          <p className="text-xs text-muted-foreground">
            {state === "starting"
              ? "Your sandbox is starting…"
              : "Your sandbox is not running. Start it to see the address and the test credentials your code needs."}
          </p>
          <Button
            size="xs"
            className="self-start"
            disabled={starting || state === "starting"}
            onClick={() => {
              setStarting(true)
              void sandboxAction("start").finally(() => setStarting(false))
            }}
          >
            {starting || state === "starting" ? <Spinner /> : <Play />} Start sandbox
          </Button>
        </>
      ) : (
        <>
          <p className="flex gap-1.5 text-xs text-muted-foreground">
            <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
            These work with any AWS SDK or the AWS CLI — nothing reaches your real AWS account.
          </p>
          {sandbox.map((s) => (
            <SandboxBlock key={s.cloud} access={s} multiple={sandbox.length > 1} />
          ))}
        </>
      )}
    </section>
  )
}

/** Docker workspace: the containers instead of a cloud sandbox (no SDK endpoint or test credentials to show). */
export function ContainersCard() {
  const status = useWorkflowStore((s) => s.compose)
  const busy = useRunBusy()
  const summary = composeChipSummary(status)
  const starting = busy || summary.state === "starting"

  return (
    <section className="flex flex-col gap-2 rounded-lg border p-2.5">
      <h3 className="flex items-center gap-1.5 text-xs font-semibold">
        <Boxes className="size-3.5 text-primary" /> Your containers
      </h3>
      {summary.running === 0 ? (
        <>
          <p className="text-xs text-muted-foreground">
            {starting
              ? "Your containers are starting…"
              : "Your containers are not running. Start them to see the address of every port they publish."}
          </p>
          {status?.message && <p className="text-xs text-destructive">{status.message}</p>}
          <Button size="xs" className="self-start" disabled={starting} onClick={() => void runAction("up", { showLog: false })}>
            {starting ? <Spinner /> : <Play />} Start containers
          </Button>
        </>
      ) : (
        <>
          <p className="text-xs font-medium">
            {summary.running} of {summary.total} service{summary.total === 1 ? "" : "s"} running
          </p>
          <p className="flex gap-1.5 text-xs text-muted-foreground">
            <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
            Published ports listen on this machine only. Inside the project, services reach each other by service name
            (for example db:5432), not by these addresses.
          </p>
        </>
      )}
    </section>
  )
}

/** Kubernetes workspace: the local cluster (state, kubectl access) instead of a cloud sandbox. */
export function KubeClusterCard() {
  const status = useWorkflowStore((s) => s.kube)
  const busy = useRunBusy()
  const summary = kubeChipSummary(status)
  const starting = busy || summary.cluster === "starting" || summary.state === "starting"
  const snippet = status?.cluster.kubeconfig ? kubectlSnippet(status.cluster.kubeconfig) : undefined

  return (
    <section className="flex flex-col gap-2 rounded-lg border p-2.5" data-testid="kube-endpoints-cluster">
      <h3 className="flex items-center gap-1.5 text-xs font-semibold">
        <TechIcon icon="kubernetes" size={14} /> Your local cluster
      </h3>
      {summary.cluster !== "running" || summary.running === 0 ? (
        <>
          <p
            className="rounded-md border border-amber-500/40 bg-amber-500/10 p-1.5 text-xs text-amber-800 dark:text-amber-300"
            data-testid="kube-endpoints-warning"
          >
            {starting
              ? "Your cluster is starting… addresses appear once the pods run."
              : summary.cluster !== "running"
                ? "The cluster is not running, so nothing below answers. Deploy to start it and forward every Service port to this machine."
                : "No workload is running yet. Deploy (again) to start the pods behind these Services."}
          </p>
          {(status?.cluster.message || status?.message) && (
            <p className="text-xs text-destructive">{status?.cluster.message || status?.message}</p>
          )}
          <Button size="xs" className="self-start" disabled={starting} onClick={() => void runAction("up", { showLog: false })}>
            {starting ? <Spinner /> : <Play />} Deploy
          </Button>
        </>
      ) : (
        <>
          <p className="text-xs font-medium">
            {summary.running} of {summary.total} workload{summary.total === 1 ? "" : "s"} running
          </p>
          <p className="flex gap-1.5 text-xs text-muted-foreground">
            <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
            Service ports are forwarded to 127.0.0.1 on this machine only. Inside the cluster, pods reach each other by
            Service name (for example db:5432 or db.default.svc.cluster.local).
          </p>
        </>
      )}
      {snippet && (
        <div className="flex min-w-0 items-center gap-1 rounded-md bg-muted/60 py-0.5 pr-0.5 pl-1.5">
          <Terminal className="size-3 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate font-mono text-[11px]" title={snippet}>
            {snippet}
          </span>
          <CopyButton value={snippet} label="Copy kubectl command" what="Command copied" />
        </div>
      )}
    </section>
  )
}

function SandboxBlock({ access: s, multiple }: { access: SandboxAccess; multiple: boolean }) {
  return (
    <div className="flex flex-col gap-1">
      {multiple && <CloudBadge cloud={s.cloud} />}
      <FieldRow
        field={{
          label: "Endpoint",
          value: s.url,
          hint: "The address of your local cloud. Point your SDK or CLI here instead of the real cloud. It stays the same when the sandbox restarts, so URLs you save keep working.",
        }}
        compact
      />
      {s.region && <FieldRow field={{ label: "Region", value: s.region }} compact />}
      {s.credentials.map((c) => (
        <FieldRow key={c.label} field={c} compact />
      ))}
      {s.envSnippet && (
        <Button
          size="xs"
          variant="secondary"
          className="mt-1 self-start"
          onClick={() =>
            void copyText(s.envSnippet).then(
              () =>
                toast.success("Environment variables copied", {
                  description: "Paste them in a terminal; AWS tools started from it then use your sandbox.",
                }),
              () => toast.error("Couldn't copy to the clipboard"),
            )
          }
        >
          <Terminal /> Copy environment variables
        </Button>
      )}
      {s.notes.length > 0 && (
        <ul className="mt-1 list-disc pl-4 text-[11px] text-muted-foreground">
          {s.notes.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      )}
    </div>
  )
}

const POSTMAN_FILE_RE = /[^\w.-]+/g

/** Downloads the Postman collection (save dialog in the desktop app, a regular download in the browser). */
async function exportPostman(workflowId: string, workflowName: string): Promise<boolean> {
  const fileName = `${workflowName.replace(POSTMAN_FILE_RE, "-") || workflowId}.postman_collection.json`
  if (isTauri()) {
    const path = await save({
      title: "Save Postman collection",
      defaultPath: fileName,
      filters: [{ name: "Postman collection", extensions: ["json"] }],
    })
    if (!path) return false
    const res = await fetch(api.postmanCollectionUrl(workflowId))
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    await writeTextFile(path, await res.text())
    return true
  }
  const res = await fetch(api.postmanCollectionUrl(workflowId))
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  const url = URL.createObjectURL(await res.blob())
  const a = document.createElement("a")
  a.href = url
  a.download = fileName
  a.click()
  URL.revokeObjectURL(url)
  return true
}

export function PostmanCard() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const workflowName = useWorkflowStore((s) => s.workflow!.name)
  const kind = useWorkflowStore((s) => s.workflow!.kind)
  const [busy, setBusy] = useState(false)
  return (
    <section className="flex flex-col gap-1.5 rounded-lg border p-2.5">
      <h3 className="text-xs font-semibold">Use Postman?</h3>
      <p className="text-[11px] text-muted-foreground">
        Get every request below as a ready-made collection. In Postman choose <b>Import</b>, then pick the downloaded
        file —{" "}
        {kind === "kubernetes"
          ? "requests already point at the ports your cluster forwards to this machine."
          : kind === "docker"
            ? "requests already point at the ports your containers publish."
            : "requests already point at your sandbox and include the test credentials."}
      </p>
      <Button
        size="xs"
        variant="outline"
        className="self-start"
        disabled={busy}
        onClick={() => {
          setBusy(true)
          exportPostman(workflowId, workflowName)
            .then((saved) => {
              if (saved) toast.success("Postman collection saved", { description: "In Postman: Import → pick the file." })
            })
            .catch((err) => toast.error("Couldn't export the Postman collection", { description: errorMessage(err) }))
            .finally(() => setBusy(false))
        }}
      >
        {busy ? <Spinner /> : <Download />} Export to Postman
      </Button>
    </section>
  )
}

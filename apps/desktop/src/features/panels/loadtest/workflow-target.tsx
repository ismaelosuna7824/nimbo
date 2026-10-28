import { useMemo } from "react"
import { useShallow } from "zustand/react/shallow"
import { CircleAlert, Globe, Play, Plug, Rocket, Route, ScrollText, Zap } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { Connection, LoadTarget } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { runAction, useRunBusy } from "@/features/toolbar/actions"
import { KIND_META } from "@/features/connections/meta"
import { useConnections, useConnectionsStore } from "@/features/connections/store"
import { ConnectionTargetPicker } from "@/features/connections/target-picker"
import { INITIAL_CONNECTION_TARGET } from "@/features/connections/target-draft"
import { openReachability } from "@/features/network/store"
import { hasNetworkResources } from "@/features/network/use-network"
import { firstPublishedUrl, runningServices } from "@/features/docker/deploy/compose-model"
import { firstKubeUrl, runningWorkloads } from "@/features/kubernetes/deploy/kube-model"
import { nodeLabelById } from "../lib/nodes"
import { CopyButton, MethodBadge } from "../endpoints/bits"
import { apiPathOf } from "../endpoints/model"
import { openEndpoints, useEndpoints } from "../endpoints/use-endpoints"
import { StepHeader, type SetDraft } from "./configure"
import type { LoadDraft } from "./draft"
import { AWS_OPERATIONS, HTTP_METHODS, targetDetail, targetKey } from "./targets"
import type { WorkflowTarget } from "./use-workflow-target"

export function WorkflowTargetStep({
  draft,
  setDraft,
  resolved,
  pro,
}: {
  draft: LoadDraft
  setDraft: SetDraft
  resolved: WorkflowTarget
  pro: boolean
}) {
  const kind = useWorkflowStore((s) => s.workflow!.kind)
  const docker = kind === "docker"
  const kube = kind === "kubernetes"
  // Labels and the network flag, not the nodes: this tab stays mounted, and every inspector keystroke replaces them.
  const labels = useWorkflowStore(
    useShallow((s): Record<string, string> =>
      kube
        ? Object.fromEntries((s.workflow!.kubernetes?.graph.nodes ?? []).map((n) => [n.id, `${n.kind} ${n.name}`]))
        : docker
          ? Object.fromEntries((s.workflow!.compose?.graph.nodes ?? []).filter((n) => n.kind === "service").map((n) => [n.id, n.name]))
          : nodeLabelById(s.workflow!.nodes),
    ),
  )
  const networkResources = useWorkflowStore((s) => hasNetworkResources(s.workflow!.nodes))
  // Docker workspaces: running services stand for deployed resources, published ports for the emulator address;
  // Kubernetes workspaces: running workloads, and the first port-forward / ingress address.
  const deployedCount = useWorkflowStore((s) =>
    kube
      ? runningWorkloads(s.kube)
      : docker
        ? runningServices(s.compose)
        : (s.deployed?.resources.filter((r) => r.mode === "managed").length ?? 0),
  )
  const emulatorUrl = useWorkflowStore((s) =>
    kube
      ? firstKubeUrl(s.kube)
      : docker
        ? firstPublishedUrl(s.compose)
        : s.sandbox?.emulators.find((e) => e.state === "running")?.url,
  )
  const accessResources = useEndpoints().data?.resources
  const { suggested: targets, loading } = resolved
  const groups = useMemo(() => {
    const out: { nodeId: string; label: string; items: { t: LoadTarget; key: string }[] }[] = []
    for (const t of targets) {
      const nodeId = t.nodeId ?? ""
      let g = out.find((x) => x.nodeId === nodeId)
      if (!g) {
        g = { nodeId, label: nodeId ? (labels[nodeId] ?? nodeId) : "Other", items: [] }
        out.push(g)
      }
      g.items.push({ t, key: targetKey(t) })
    }
    return out
  }, [targets, labels])

  return (
    <div>
      <StepHeader
        n={1}
        title="What do you want to test?"
        hint="Pick a deployed endpoint or resource to send traffic to, or a saved connection to a service deployed elsewhere."
      />
      {draft.source !== "connection" && <DeployGate />}
      <ToggleGroup
        type="single"
        size="sm"
        variant="outline"
        value={draft.source}
        onValueChange={(v) => v && setDraft((d) => ({ ...d, source: v as LoadDraft["source"] }))}
        className="mb-2"
      >
        <ToggleGroupItem value="suggested" className="text-xs">
          <Zap /> Suggested
        </ToggleGroupItem>
        <ToggleGroupItem value="http" className="text-xs">
          <Globe /> Custom HTTP
        </ToggleGroupItem>
        {pro && (
          <ToggleGroupItem value="aws" className="text-xs">
            AWS operation
          </ToggleGroupItem>
        )}
        <ToggleGroupItem value="connection" className="text-xs">
          <Plug /> Connection
        </ToggleGroupItem>
      </ToggleGroup>

      {draft.source === "suggested" &&
        (loading ? (
          <Spinner />
        ) : targets.length === 0 ? (
          <div className="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">
            {kube ? (
              <p>
                {deployedCount > 0
                  ? "Your workloads are running, but no Service forwards an HTTP port and no Ingress routes to one. Add a Service (or an Ingress) on the canvas and Deploy again, or test another address with Custom HTTP or a Connection."
                  : "Nothing to test yet. Deploy the workflow to its local cluster — every Service port and Ingress rule that answers HTTP then shows up here."}
              </p>
            ) : docker ? (
              <p>
                {deployedCount > 0
                  ? "Your containers are running, but none publishes an HTTP port. Add one to a service (ports: \"8080:80\") and press Up again, or test another address with Custom HTTP or a Connection."
                  : "Nothing to test yet. Start the containers (Up in the Deploy tab) — every service that publishes an HTTP port then shows up here."}
              </p>
            ) : deployedCount > 0 ? (
              <>
                <p>
                  Your resources are deployed, but none of them receives traffic on its own. Networks, subnets, route tables,
                  security groups and IAM are plumbing: load tests need something that answers requests, such as an HTTP API,
                  a function, a queue, a bucket or a table. Add one and deploy again, or test a service you already have with
                  Custom HTTP or a Connection.
                </p>
                {networkResources && (
                  <p className="mt-2">
                    Want to know who can reach what in your network instead?{" "}
                    <Button variant="link" size="xs" className="h-auto px-0" onClick={() => void openReachability()}>
                      <Route /> Run network checks
                    </Button>
                  </p>
                )}
              </>
            ) : (
              <p>Nothing to test yet. Deploy the workflow (button above) — its HTTP endpoints, queues, buckets, tables and functions then show up here.</p>
            )}
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            <Button variant="link" size="xs" className="-mt-1 self-start px-0" onClick={() => openEndpoints()}>
              <Plug /> See all endpoints
            </Button>
            <div className="flex flex-col gap-2" role="radiogroup" aria-label="Targets">
              {groups.map((g) => (
                <div key={g.nodeId}>
                  <div className="mb-1 truncate font-mono text-[11px] text-muted-foreground">{g.label}</div>
                  <div className="flex flex-col gap-1">
                    {g.items.map(({ t, key }) => (
                      <div key={key} className="relative">
                        <button
                          type="button"
                          role="radio"
                          aria-checked={draft.suggestedKey === key}
                          onClick={() => setDraft((d) => ({ ...d, suggestedKey: key }))}
                          className={cn(
                            "flex w-full items-center gap-2 rounded-md border px-2 py-1.5 text-left text-xs outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring/50",
                            t.http && "pr-8",
                            draft.suggestedKey === key && "border-primary bg-primary/5",
                          )}
                        >
                          {t.http ? (
                            <HttpTargetLabel
                              method={t.http.method}
                              url={t.http.url}
                              path={accessResources && apiPathOf(t.http.url, accessResources)}
                            />
                          ) : (
                            <>
                              <Badge variant="outline" className="h-4 px-1 font-mono text-[10px] uppercase">
                                {t.kind}
                              </Badge>
                              <span className="min-w-0 flex-1">
                                <span className="block truncate font-medium">{t.label}</span>
                                <span className="block truncate font-mono text-[10px] text-muted-foreground">
                                  {targetDetail(t)}
                                </span>
                              </span>
                            </>
                          )}
                        </button>
                        {t.http && (
                          <CopyButton
                            value={t.http.url}
                            label="Copy URL"
                            what="URL copied"
                            className="absolute top-1/2 right-1 -translate-y-1/2"
                          />
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </div>
        ))}

      {draft.source === "http" && (
        <div className="flex flex-col gap-2">
          <div className="flex gap-1">
            <Select value={draft.http.method} onValueChange={(m) => setDraft((d) => ({ ...d, http: { ...d.http, method: m } }))}>
              <SelectTrigger size="sm" className="w-24 text-xs" aria-label="Method">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {HTTP_METHODS.map((m) => (
                  <SelectItem key={m} value={m}>
                    {m}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input
              className="h-7 font-mono text-xs"
              placeholder={`${emulatorUrl ?? (kube ? "http://127.0.0.1:<forwarded port>" : docker ? "http://127.0.0.1:<published port>" : "http://127.0.0.1:<sandbox port>")}/…`}
              value={draft.http.url}
              onChange={(e) => setDraft((d) => ({ ...d, http: { ...d.http, url: e.target.value } }))}
              aria-label="URL"
            />
          </div>
          {resolved.external && (
            <p className="text-[11px] text-amber-700 dark:text-amber-400">
              {resolved.external.where} is outside {docker || kube ? "this machine" : "your local sandbox"} — you will be asked to
              confirm before traffic is sent.
            </p>
          )}
          <Label className="text-[11px] text-muted-foreground">Headers (one “Name: value” per line)</Label>
          <Textarea
            rows={2}
            className="min-h-0 font-mono text-xs"
            value={draft.http.headersText}
            onChange={(e) => setDraft((d) => ({ ...d, http: { ...d.http, headersText: e.target.value } }))}
          />
          {draft.http.method !== "GET" && draft.http.method !== "HEAD" && (
            <>
              <Label className="text-[11px] text-muted-foreground">Body</Label>
              <Textarea
                rows={3}
                className="min-h-0 font-mono text-xs"
                value={draft.http.body}
                onChange={(e) => setDraft((d) => ({ ...d, http: { ...d.http, body: e.target.value } }))}
              />
            </>
          )}
        </div>
      )}

      {draft.source === "aws" && (
        <div className="grid grid-cols-[6rem_1fr] items-center gap-x-2 gap-y-1.5 text-xs">
          <Label className="text-[11px]">Operation</Label>
          <Input
            list="nimbo-aws-ops"
            className="h-7 font-mono text-xs"
            value={draft.aws.operation}
            onChange={(e) => setDraft((d) => ({ ...d, aws: { ...d.aws, operation: e.target.value } }))}
          />
          <datalist id="nimbo-aws-ops">
            {AWS_OPERATIONS.map((o) => (
              <option key={o} value={o} />
            ))}
          </datalist>
          <Label className="text-[11px]">Resource</Label>
          <Input
            className="h-7 font-mono text-xs"
            placeholder="queue URL, bucket, table, function name or ARN"
            value={draft.aws.resource}
            onChange={(e) => setDraft((d) => ({ ...d, aws: { ...d.aws, resource: e.target.value } }))}
          />
          <Label className="text-[11px]">Key</Label>
          <Input
            className="h-7 font-mono text-xs"
            placeholder="object key / item key ({{seq}}, {{uuid}})"
            value={draft.aws.key ?? ""}
            onChange={(e) => setDraft((d) => ({ ...d, aws: { ...d.aws, key: e.target.value } }))}
          />
          <Label className="self-start pt-1 text-[11px]">Payload</Label>
          <Textarea
            rows={3}
            className="min-h-0 font-mono text-xs"
            placeholder='{"id": "{{uuid}}", "at": "{{now}}"}'
            value={draft.aws.payload ?? ""}
            onChange={(e) => setDraft((d) => ({ ...d, aws: { ...d.aws, payload: e.target.value } }))}
          />
        </div>
      )}

      {draft.source === "connection" && <ConnectionSource draft={draft} setDraft={setDraft} connection={resolved.connection} />}
    </div>
  )
}

/** Pick a saved connection, then one of its suggested targets or a custom one. */
function ConnectionSource({ draft, setDraft, connection }: { draft: LoadDraft; setDraft: SetDraft; connection: Connection | undefined }) {
  const connections = useConnections()
  const openView = useConnectionsStore((s) => s.openView)
  const list = connections.data ?? []

  if (connections.isPending) return <Spinner />
  if (list.length === 0) {
    return (
      <div className="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">
        <p>
          No saved connections yet. A connection points at a service that is already deployed somewhere else — an HTTP API,
          a database or an AWS account — so you can load-test it from here.
        </p>
        <Button size="xs" className="mt-2" onClick={() => openView("new")}>
          <Plug /> New connection
        </Button>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-1.5">
        <Select
          value={draft.connection.connectionId ?? ""}
          onValueChange={(id) => setDraft((d) => ({ ...d, connection: { ...INITIAL_CONNECTION_TARGET, connectionId: id } }))}
        >
          <SelectTrigger size="sm" className="min-w-0 flex-1 text-xs" aria-label="Connection">
            <SelectValue placeholder="Choose a saved connection" />
          </SelectTrigger>
          <SelectContent>
            {list.map((c) => {
              const Icon = KIND_META[c.kind].icon
              return (
                <SelectItem key={c.id} value={c.id}>
                  <Icon /> {c.name}
                  <span className="text-muted-foreground">· {KIND_META[c.kind].label}</span>
                </SelectItem>
              )
            })}
          </SelectContent>
        </Select>
        <Button size="xs" variant="ghost" onClick={() => openView(connection?.id)}>
          Manage
        </Button>
      </div>
      {connection ? (
        <ConnectionTargetPicker
          connection={connection}
          value={draft.connection}
          onChange={(update) => setDraft((d) => ({ ...d, connection: update(d.connection) }))}
        />
      ) : (
        <p className="text-[11px] text-muted-foreground">Runs started here still belong to this workflow’s history.</p>
      )}
    </div>
  )
}

/**
 * Load tests hit what is deployed in the sandbox. Until something is deployed,
 * explain that and offer the deploy right here instead of a dead end.
 */
function DeployGate() {
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  const docker = kind === "docker"
  const kube = kind === "kubernetes"
  const deployedCount = useWorkflowStore((s) =>
    kube
      ? runningWorkloads(s.kube)
      : docker
        ? runningServices(s.compose)
        : (s.deployed?.resources.filter((r) => r.mode === "managed").length ?? 0),
  )
  const lastDeploy = useWorkflowStore((s) => s.runs.find((r) => r.action === (docker || kube ? "up" : "apply")))
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const busy = useRunBusy()
  if (deployedCount > 0) return null
  const failed = lastDeploy?.status === "failed"
  const DeployIcon = docker ? Play : Rocket
  return (
    <div className={cn("mb-2 rounded-lg border p-2.5 text-xs", failed ? "border-destructive/50 bg-destructive/5" : "border-primary/40 bg-primary/5")}>
      <p className="flex items-center gap-1.5 font-medium">
        {failed ? <CircleAlert className="size-3.5 text-destructive" /> : <DeployIcon className="size-3.5" />}
        {kube
          ? busy
            ? "Deploying to your local cluster…"
            : failed
              ? "The last Deploy failed"
              : "First, deploy to your local cluster"
          : docker
            ? busy
              ? "Starting your containers…"
              : failed
                ? "The last Up failed"
                : "First, start your containers"
            : busy
              ? "Deploying to your local sandbox…"
              : failed
                ? "The last deploy failed"
                : "First, deploy your infrastructure"}
      </p>
      <p className="mt-1 text-muted-foreground">
        {kube
          ? busy
            ? "Nimbo is applying your manifests and waiting for the rollouts. Targets appear here once Services are forwarded."
            : "Load tests send real traffic to the Services and Ingress rules of this workflow's local k3s cluster. Deploy once (it runs in Docker on this machine), then pick a target below, choose a traffic shape and press Start test. To test a service deployed elsewhere, choose Connection."
          : docker
            ? busy
              ? "docker compose is building and starting your services. Targets appear here once they publish their ports."
              : "Load tests send real traffic to the ports your containers publish on this machine. Start them once with docker compose, then pick a target below, choose a traffic shape and press Start test. To test a service deployed elsewhere, choose Connection."
            : busy
              ? "Terraform is creating your resources in Floci (usually under a minute). Targets appear here when it finishes."
              : "Load tests send real traffic to what is running in the local Floci sandbox. Deploy it once (nothing touches your real cloud), then pick a target below, choose a traffic shape and press Start test. To test a service deployed elsewhere, choose Connection."}
      </p>
      <div className="mt-2 flex gap-1.5">
        <Button size="xs" onClick={() => void runAction("apply", { showLog: false })} disabled={busy}>
          {busy ? <Spinner /> : <DeployIcon />}{" "}
          {kube
            ? failed
              ? "Retry deploy"
              : "Deploy now"
            : docker
              ? failed
                ? "Retry Up"
                : "Start containers"
              : failed
                ? "Retry deploy"
                : "Deploy now"}
        </Button>
        <Button size="xs" variant="outline" onClick={() => showBottom("deploy")}>
          <ScrollText /> View logs
        </Button>
      </div>
    </div>
  )
}

/** "GET /items" up front, the full address underneath. */
function HttpTargetLabel({ method, url, path }: { method: string; url: string; path?: string }) {
  if (!path) {
    try {
      path = new URL(url).pathname
    } catch {
      path = url
    }
  }
  return (
    <>
      <MethodBadge method={method} />
      <span className="min-w-0 flex-1">
        <span className="block truncate font-mono font-medium">{path}</span>
        <span className="block truncate font-mono text-[10px] text-muted-foreground" title={url}>
          {url}
        </span>
      </span>
    </>
  )
}

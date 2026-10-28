import { Code2Icon, ExternalLinkIcon, FlameIcon, MousePointerClickIcon, RadioIcon, Trash2Icon } from "lucide-react"
import { techIconFor } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { openExternal } from "@/components/open-external"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { InspectorBody, InspectorSection } from "@/features/inspector/inspector-section"
import { openFailureTest } from "@/features/live/live-store"
import { DiagnosticRow } from "@/features/panels/components/diagnostic-row"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import { api } from "@/lib/api/client"
import type { ComposeNode, ComposeServiceStatus, Workflow } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { deleteComposeNode } from "../actions"
import { stateText } from "../canvas/nodes"
import { CommitInput, Field } from "./fields"
import { ServiceForm } from "./service-form"
import { StageBaseField } from "./stage-base-field"

const NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/

/** The inspector of a Docker workspace's selected node (null when nothing of the graph is selected). */
export function DockerInspector() {
  const graph = useWorkflowStore((s) => s.workflow!.compose?.graph)
  const selected = useWorkflowStore((s) => s.selectedNodeId)
  const node = graph?.nodes.find((n) => n.id === selected)
  if (!node) return null
  return <NodeInspector key={node.id} node={node} />
}

function NodeInspector({ node }: { node: ComposeNode }) {
  const diagnostics = useWorkflowStore((s) => s.workflow!.diagnostics).filter((d) => d.nodeId === node.id)
  const status = useWorkflowStore((s) => s.compose?.services.find((x) => x.nodeId === node.id || `service:${x.name}` === node.id))
  return (
    <div className="flex h-full flex-col" data-testid="docker-inspector">
      <Header node={node} status={status} />
      <InspectorBody>
        {diagnostics.length > 0 && (
          <div className="-mx-3 flex flex-col rounded-lg border">
            {diagnostics.map((d, i) => (
              <DiagnosticRow key={i} diagnostic={d} onSelect={d.file ? () => revealFiles("", d.file, d.line) : undefined} />
            ))}
          </div>
        )}
        {status && status.state !== "absent" && <Runtime status={status} />}
        {node.kind === "service" && node.service ? <ServiceForm key={node.id} node={node} /> : null}
        {node.kind === "network" && <NetworkForm node={node} />}
        {node.kind === "volume" && <VolumeForm node={node} />}
        {(node.kind === "secret" || node.kind === "config" || node.kind === "external" || node.kind === "build-stage") && <ReadOnlyFacts node={node} />}
      </InspectorBody>
    </div>
  )
}

function Header({ node, status }: { node: ComposeNode; status?: ComposeServiceStatus }) {
  const svc = node.service
  const kind = node.kind === "external" ? (node.externalKind ?? "external") : node.kind
  const icon = techIconFor({ image: svc?.image?.ref, baseImages: svc?.baseImages, stack: svc?.stack, kind })
  const subtitle = svc ? (svc.build ? `build ${svc.build.context}` : (svc.image?.ref ?? "")) : node.kind === "external" ? `external ${node.externalKind ?? ""}` : node.kind
  const deletable = node.kind === "service" || node.kind === "network" || node.kind === "volume"
  return (
    <div className="flex items-start gap-3 border-b p-4">
      <span className="flex size-10 shrink-0 items-center justify-center rounded-xl border bg-background">
        <TechIcon icon={icon} size={24} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-heading text-base leading-tight font-medium">{node.name}</div>
        <div className="truncate font-mono text-[11px] text-muted-foreground">{subtitle}</div>
        <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
          <Badge variant="secondary" className="capitalize">
            {kind === "build-stage" ? "Build stage" : kind}
          </Badge>
          {icon.generic ? null : <Badge variant="outline">{icon.label}</Badge>}
          {node.inactive && <Badge variant="outline">profile off</Badge>}
          {stateText(status) && <Badge variant="outline">{stateText(status)}</Badge>}
        </div>
      </div>
      <div className="flex items-center gap-0.5">
        {node.file && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label="Open in code" onClick={() => revealFiles("", node.file, node.line)}>
                <Code2Icon />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Open {node.file}
              {node.line ? `:${node.line}` : ""}
            </TooltipContent>
          </Tooltip>
        )}
        {node.kind === "service" && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label="Test a failure…" data-testid="docker-inspector-failure" onClick={() => openFailureTest(node.id)}>
                <FlameIcon />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Test a failure: break it under load and watch the impact</TooltipContent>
          </Tooltip>
        )}
        {deletable && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label="Delete" onClick={() => void deleteComposeNode(node)}>
                <Trash2Icon />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Delete (⌫)</TooltipContent>
          </Tooltip>
        )}
      </div>
    </div>
  )
}

/** Containers of a running service: state, health, restarts and the URLs of published ports. */
function Runtime({ status }: { status: ComposeServiceStatus }) {
  return (
    <InspectorSection id="docker:running" title="Running" icon={<RadioIcon className="size-3.5 text-sky-600 dark:text-sky-400" />}>
      <ul className="flex flex-col gap-1.5">
        {status.containers.map((c) => (
          <li key={c.id} className="flex flex-col gap-1 rounded-md border px-2 py-1.5 text-xs">
            <div className="flex items-center gap-2">
              <span className={cn("size-2 rounded-full", c.state === "running" ? "bg-emerald-500" : c.state === "restarting" ? "bg-amber-500" : "bg-muted-foreground/50")} />
              <span className="truncate font-mono">{c.name}</span>
              <span className="ml-auto text-muted-foreground">{[c.state, c.health].filter(Boolean).join(" · ")}</span>
            </div>
            {c.restarts > 0 && <span className="text-[11px] text-amber-600 dark:text-amber-400">Restarted {c.restarts}×</span>}
            {c.ports.map((p) =>
              p.url ? (
                <button key={`${p.host}/${p.protocol}`} type="button" className="flex items-center gap-1 self-start font-mono text-[11px] text-primary hover:underline" onClick={() => void openExternal(p.url!)}>
                  {p.url}
                  <ExternalLinkIcon className="size-3" />
                </button>
              ) : (
                <span key={`${p.host}/${p.protocol}`} className="font-mono text-[11px] text-muted-foreground">
                  {p.hostIp || "0.0.0.0"}:{p.host} → {p.container}/{p.protocol}
                </span>
              ),
            )}
          </li>
        ))}
      </ul>
    </InspectorSection>
  )
}

function usersOf(nodeId: string) {
  const graph = useWorkflowStore.getState().workflow?.compose?.graph
  const names = new Map((graph?.nodes ?? []).map((n) => [n.id, n.name]))
  return [...new Set((graph?.edges ?? []).filter((e) => e.source === nodeId).map((e) => names.get(e.target) ?? e.target))]
}

async function run(edit: () => Promise<Workflow>, failure: string) {
  try {
    useWorkflowStore.getState().setWorkflow(await edit())
  } catch (err) {
    toast.error(failure, { description: errorMessage(err) })
  }
}

function NetworkForm({ node }: { node: ComposeNode }) {
  const id = useWorkflowStore((s) => s.workflow!.id)
  const members = usersOf(node.id)
  const patch = (body: { name?: string; driver?: string; internal?: boolean; external?: boolean }) =>
    void run(() => api.patchNetwork(id, node.name, body), `Couldn't update ${node.name}`)
  return (
    <>
      <InspectorSection id="docker:network" title="Network">
        <Field label="Name" htmlFor="net-name" hint="Renaming updates every service attached to it.">
          <CommitInput id="net-name" className="font-mono" value={node.name} validate={(v) => (NAME_RE.test(v) ? undefined : "Letters, digits, “_”, “.” or “-”")} onCommit={(name) => patch({ name })} />
        </Field>
        <Field label="Driver" htmlFor="net-driver">
          <CommitInput id="net-driver" className="font-mono" value={node.network?.driver ?? ""} placeholder="bridge" onCommit={(driver) => patch({ driver })} />
        </Field>
        <label className="flex items-center gap-2 text-xs">
          <Checkbox checked={!!node.network?.internal} onCheckedChange={(c) => patch({ internal: c === true })} />
          Internal: no access to the outside world
        </label>
        <label className="flex items-center gap-2 text-xs">
          <Checkbox checked={!!node.external} onCheckedChange={(c) => patch({ external: c === true })} />
          External: created outside this project
        </label>
      </InspectorSection>
      <InspectorSection id="docker:network-members" title="Services on it">
        {members.length ? <p className="font-mono text-xs">{members.join(", ")}</p> : <p className="text-[11px] text-muted-foreground">None yet: drag a service into it, or connect a service to it.</p>}
      </InspectorSection>
    </>
  )
}

function VolumeForm({ node }: { node: ComposeNode }) {
  const id = useWorkflowStore((s) => s.workflow!.id)
  const users = usersOf(node.id)
  const patch = (body: { name?: string; driver?: string; external?: boolean }) => void run(() => api.patchVolume(id, node.name, body), `Couldn't update ${node.name}`)
  return (
    <>
      <InspectorSection id="docker:volume" title="Volume">
        <Field label="Name" htmlFor="vol-name" hint="Renaming updates the services that mount it.">
          <CommitInput id="vol-name" className="font-mono" value={node.name} validate={(v) => (NAME_RE.test(v) ? undefined : "Letters, digits, “_”, “.” or “-”")} onCommit={(name) => patch({ name })} />
        </Field>
        <Field label="Driver" htmlFor="vol-driver">
          <CommitInput id="vol-driver" className="font-mono" value={node.volume?.driver ?? ""} placeholder="local" onCommit={(driver) => patch({ driver })} />
        </Field>
        <label className="flex items-center gap-2 text-xs">
          <Checkbox checked={!!node.external} onCheckedChange={(c) => patch({ external: c === true })} />
          External: created outside this project
        </label>
      </InspectorSection>
      <InspectorSection id="docker:volume-users" title="Mounted by">
        {users.length ? <p className="font-mono text-xs">{users.join(", ")}</p> : <p className="text-[11px] text-muted-foreground">Nobody yet: connect a service to it to mount it.</p>}
      </InspectorSection>
    </>
  )
}

function ReadOnlyFacts({ node }: { node: ComposeNode }) {
  const facts: [string, string | undefined][] =
    node.kind === "build-stage"
      ? [
          ["From", node.stage?.fromStage],
          ["Dockerfile", node.stage && `${node.stage.dockerfile}:${node.stage.line}`],
          ["Exposes", node.stage?.expose?.join(", ")],
          ["User", node.stage?.user],
          ["Workdir", node.stage?.workdir],
          ["Command", node.stage?.cmd],
        ]
      : [
          ["File", node.resource?.file],
          ["Environment variable", node.resource?.environment],
          ["Inline content", node.resource?.content ? "yes" : undefined],
          ["Used by", usersOf(node.id).join(", ") || undefined],
        ]
  return (
    <InspectorSection id="docker:details" title="Details">
      {node.kind === "build-stage" && node.stage && !node.stage.fromStage && <StageBaseField nodeId={node.id} from={node.stage.from} />}
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        {facts
          .filter(([, v]) => v)
          .map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="truncate font-mono">{v}</dd>
            </div>
          ))}
      </dl>
      <p className="text-[11px] text-muted-foreground">Edit it in the Code tab.</p>
    </InspectorSection>
  )
}

/** Shown when nothing is selected in a Docker workspace. */
export function DockerOverviewHint() {
  return (
    <Empty className="border border-dashed">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <MousePointerClickIcon />
        </EmptyMedia>
        <EmptyTitle>Select a service</EmptyTitle>
        <EmptyDescription>Click a card to configure its image, ports, environment, volumes and health check. Drag between the dots of two cards to connect them.</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

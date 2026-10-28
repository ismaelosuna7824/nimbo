import {
  ActivityIcon,
  BoxIcon,
  CpuIcon,
  HardDriveIcon,
  LayersIcon,
  NetworkIcon,
  PlugIcon,
  ShieldCheckIcon,
  SlidersHorizontalIcon,
  VariableIcon,
  XIcon,
} from "lucide-react"
import { Fragment } from "react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { CommitInput, ListEditor, SecretInput } from "@/features/docker/inspector/fields"
import { InspectorSection, InspectorSubsection } from "@/features/inspector/inspector-section"
import { SECRET_KEY } from "@/features/docker/inspector/service-form"
import type { KubeContainer, KubeContainerPort, KubeEnv, KubeEnvFrom, KubeNode, KubeProbe, KubeVolume, KubeVolumeMount } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { patchObject } from "../actions"
import { KField, KList } from "./kube-fields"
import { WorkloadIdentity } from "./rbac-forms"
import {
  containerPath,
  containerPorts,
  EDITABLE_VOLUME,
  envFromList,
  envPatch,
  mountList,
  podSpecPath,
  probeObject,
  PROBES,
  volumesPatch,
} from "./paths"
import { HardeningSection, RolloutSection, SpreadField } from "./workload-sections"

const PORT_RE = /^\d{1,5}$/
const CPU_RE = /^$|^\d+(\.\d+)?m?$/
const MEMORY_RE = /^$|^\d+(\.\d+)?(Ki|Mi|Gi|Ti|K|M|G|T|k|m)?$/
const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/
const SCALED: Record<string, true> = { Deployment: true, StatefulSet: true, ReplicaSet: true }

type Patch = (set?: Record<string, unknown>, unset?: string[]) => void

/**
 * A workload grouped by concern: containers (image, resources), scaling & rollout, networking, config & secrets,
 * storage, probes, security, scheduling, then the rarely touched in "Advanced". Each group holds every container's
 * part of it, one subsection per container when there are several.
 */
export function WorkloadForm({ node }: { node: KubeNode }) {
  const w = node.workload!
  const patch: Patch = (set, unset) => void patchObject(node, set, unset)
  const graph = useWorkflowStore((s) => s.workflow!.kubernetes?.graph)
  const inNamespace = (kind: string) => (graph?.nodes ?? []).filter((n) => n.kind === kind && n.namespace === node.namespace).map((n) => n.name)
  const configMaps = inNamespace("ConfigMap")
  const secrets = inNamespace("Secret")
  const claims = inNamespace("PersistentVolumeClaim")
  const volumesAt = `${podSpecPath(node.kind)}.volumes`
  const multi = w.containers.length > 1
  const volumes = (w.volumes ?? []).map((v) => v.name)
  const part = { node, patch }

  return (
    <>
      {w.containers.length > 0 && (
        <InspectorSection id="k8s:containers" title="Containers" icon={<BoxIcon className="size-3.5" />}>
          <PerContainer containers={w.containers}>
            {(c) => (
              <>
                <ContainerImage {...part} container={c} />
                {multi ? (
                  <ContainerResources {...part} container={c} />
                ) : (
                  <InspectorSubsection title="Resources" icon={<CpuIcon className="size-3.5" />}>
                    <ContainerResources {...part} container={c} />
                  </InspectorSubsection>
                )}
              </>
            )}
          </PerContainer>
        </InspectorSection>
      )}

      {(SCALED[node.kind] || node.kind === "CronJob") && (
        <InspectorSection id="k8s:scaling" title="Scaling & rollout" navLabel="Scaling" icon={<LayersIcon className="size-3.5" />}>
          <div className="grid grid-cols-2 gap-2">
            {SCALED[node.kind] && (
              <KField path="spec.replicas" label="Replicas" htmlFor="k8s-replicas" display={String(w.replicas ?? 1)} hint="Empty: 1, or what an autoscaler decides.">
                <CommitInput
                  id="k8s-replicas"
                  inputMode="numeric"
                  value={w.replicas === undefined ? "" : String(w.replicas)}
                  placeholder="1"
                  validate={(v) => (/^$|^\d+$/.test(v) && Number(v) <= 100 ? undefined : "0–100")}
                  onCommit={(v) => (v === "" ? patch(undefined, ["spec.replicas"]) : patch({ "spec.replicas": Number(v) }))}
                />
              </KField>
            )}
            {node.kind === "CronJob" && (
              <KField path="spec.schedule" label="Schedule" htmlFor="k8s-schedule" display={w.schedule}>
                <CommitInput id="k8s-schedule" className="font-mono" value={w.schedule ?? ""} placeholder="*/5 * * * *" onCommit={(v) => patch({ "spec.schedule": v })} />
              </KField>
            )}
          </div>
          {node.kind === "Deployment" && <RolloutSection node={node} />}
        </InspectorSection>
      )}

      {w.containers.length > 0 && (
        <>
          <InspectorSection id="k8s:networking" title="Networking" icon={<PlugIcon className="size-3.5" />}>
            <PerContainer containers={w.containers}>{(c) => <ContainerPorts {...part} container={c} />}</PerContainer>
          </InspectorSection>
          <InspectorSection id="k8s:config" title="Config & secrets" navLabel="Config" icon={<VariableIcon className="size-3.5" />}>
            <PerContainer containers={w.containers}>{(c) => <ContainerEnv {...part} container={c} configMaps={configMaps} secrets={secrets} />}</PerContainer>
          </InspectorSection>
        </>
      )}

      <InspectorSection id="k8s:storage" title="Storage" icon={<HardDriveIcon className="size-3.5" />}>
        <InspectorSubsection title="Volumes">
          <KList path={volumesAt} display={(w.volumes ?? []).map((v) => `${v.name} (${v.kind}${v.source ? ` ${v.source}` : ""})`).join(", ")}>
            <ListEditor<KubeVolume>
              items={w.volumes ?? []}
              empty="No volumes: containers only have their own filesystem."
              addLabel="Add volume"
              blank={() => ({ name: "", kind: "emptyDir" })}
              complete={(v) => NAME_RE.test(v.name) && (v.kind === "emptyDir" || !!v.source)}
              onChange={(next) => {
                const p = volumesPatch(node.kind, w.volumes ?? [], next)
                patch(p.set, p.unset)
              }}
              render={(v, update) =>
                EDITABLE_VOLUME[v.kind] ? (
                  <>
                    <CommitInput aria-label="Volume name" width="basis-28" className="font-mono" placeholder="name" value={v.name} validate={(x) => (x === "" || NAME_RE.test(x) ? undefined : "lowercase, digits, “-”")} onCommit={(name) => update({ ...v, name })} />
                    <Select value={v.kind} onValueChange={(kind) => update({ ...v, kind: kind as KubeVolume["kind"], source: undefined })}>
                      <SelectTrigger size="sm" className="h-8 w-28 text-xs" aria-label="Volume kind">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="emptyDir">emptyDir</SelectItem>
                        <SelectItem value="configMap">ConfigMap</SelectItem>
                        <SelectItem value="secret">Secret</SelectItem>
                        <SelectItem value="persistentVolumeClaim">PVC</SelectItem>
                      </SelectContent>
                    </Select>
                    {v.kind !== "emptyDir" && (
                      <CommitInput
                        aria-label="Source"
                        className="font-mono"
                        list={v.kind === "configMap" ? "k8s-configmaps" : v.kind === "secret" ? "k8s-secrets" : "k8s-claims"}
                        placeholder={v.kind === "persistentVolumeClaim" ? "claim" : "name"}
                        value={v.source ?? ""}
                        onCommit={(source) => update({ ...v, source })}
                      />
                    )}
                  </>
                ) : (
                  <span className="mt-1.5 truncate font-mono text-xs text-muted-foreground">
                    {v.name} ({v.kind}
                    {v.source ? ` ${v.source}` : ""}): edit in code
                  </span>
                )
              }
            />
          </KList>
          {(w.volumeClaimTemplates ?? []).length > 0 && (
            <p className="text-[11px] text-muted-foreground">
              Claim templates: {(w.volumeClaimTemplates ?? []).map((t) => `${t.name}${t.size ? ` ${t.size}` : ""}${t.storageClass ? ` (${t.storageClass})` : ""}`).join(", ")}
            </p>
          )}
        </InspectorSubsection>
        {w.containers.length > 0 && (
          <InspectorSubsection title="Mounts">
            <PerContainer containers={w.containers} nested>
              {(c) => <ContainerMounts {...part} container={c} volumes={volumes} />}
            </PerContainer>
          </InspectorSubsection>
        )}
      </InspectorSection>

      {w.containers.length > 0 && (
        <InspectorSection id="k8s:probes" title="Probes" icon={<ActivityIcon className="size-3.5" />}>
          <PerContainer containers={w.containers}>{(c) => <ContainerProbes {...part} container={c} />}</PerContainer>
        </InspectorSection>
      )}

      <InspectorSection id="k8s:security" title="Security" icon={<ShieldCheckIcon className="size-3.5" />}>
        <HardeningSection node={node} />
        <WorkloadIdentity node={node} />
      </InspectorSection>

      {(node.kind === "Deployment" || node.kind === "StatefulSet") && (
        <InspectorSection id="k8s:scheduling" title="Scheduling" icon={<NetworkIcon className="size-3.5" />}>
          <SpreadField node={node} />
        </InspectorSection>
      )}

      {w.initContainers && w.initContainers.length > 0 && (
        <InspectorSection id="k8s:advanced" title="Advanced" icon={<SlidersHorizontalIcon className="size-3.5" />} defaultOpen={false}>
          <InspectorSubsection title="Init containers">
            <ul className="flex flex-col gap-0.5 font-mono text-xs">
              {w.initContainers.map((c) => (
                <li key={c.name} className="truncate">
                  {c.name} <span className="text-muted-foreground">{c.image}</span>
                </li>
              ))}
            </ul>
            <p className="text-[11px] text-muted-foreground">Edit them in code.</p>
          </InspectorSubsection>
        </InspectorSection>
      )}

      {/* Outside the sections: inputs of several groups suggest from them, open or not. */}
      <datalist id="k8s-configmaps">
        {configMaps.map((n) => (
          <option key={n} value={n} />
        ))}
      </datalist>
      <datalist id="k8s-secrets">
        {secrets.map((n) => (
          <option key={n} value={n} />
        ))}
      </datalist>
      <datalist id="k8s-claims">
        {claims.map((n) => (
          <option key={n} value={n} />
        ))}
      </datalist>
    </>
  )
}

/**
 * One container's part of a group: as is with a single container, under "Container <name>" with several. Already
 * inside a subsection (`nested`), the container is a small label instead, since subsections don't nest.
 */
function PerContainer({ containers, nested, children }: { containers: KubeContainer[]; nested?: boolean; children: (c: KubeContainer) => React.ReactNode }) {
  if (containers.length <= 1) return containers.map((c) => <Fragment key={c.name}>{children(c)}</Fragment>)
  return containers.map((c) =>
    nested ? (
      <div key={c.name} className="flex flex-col gap-2">
        <span className="text-[11px] font-medium text-muted-foreground">Container {c.name}</span>
        {children(c)}
      </div>
    ) : (
      <InspectorSubsection key={c.name} title={`Container ${c.name}`} icon={<BoxIcon className="size-3.5" />}>
        {children(c)}
      </InspectorSubsection>
    ),
  )
}

interface ContainerPart {
  node: KubeNode
  container: KubeContainer
  patch: Patch
}

const setList = (patch: Patch, path: string, list: unknown[]) => (list.length ? patch({ [path]: list }) : patch(undefined, [path]))

function ContainerImage({ node, container: c, patch }: ContainerPart) {
  const at = containerPath(node.kind, c.name)
  return (
    <KField path={`${at}.image`} label="Image" htmlFor={`k8s-image-${c.name}`} display={c.image}>
      <CommitInput id={`k8s-image-${c.name}`} className="font-mono" value={c.image} placeholder="nginx:1.27-alpine" validate={(v) => (v.trim() ? undefined : "An image")} onCommit={(image) => patch({ [`${at}.image`]: image })} />
    </KField>
  )
}

function ContainerPorts({ node, container: c, patch }: ContainerPart) {
  const at = containerPath(node.kind, c.name)
  return (
    <KList path={`${at}.ports`} display={c.ports.map((p) => p.containerPort).join(", ")}>
      <ListEditor<KubeContainerPort>
        items={c.ports}
        empty="No ports declared."
        addLabel="Add port"
        blank={() => ({ containerPort: 8080 })}
        onChange={(ports) => setList(patch, `${at}.ports`, containerPorts(ports))}
        render={(p, update) => (
          <>
            <CommitInput aria-label="Port name" width="basis-24" className="font-mono" placeholder="name" value={p.name ?? ""} onCommit={(name) => update({ ...p, name: name || undefined })} />
            <CommitInput aria-label="Container port" className="font-mono" value={String(p.containerPort)} validate={(v) => (PORT_RE.test(v) ? undefined : "A port")} onCommit={(v) => update({ ...p, containerPort: Number(v) })} />
            <Select value={p.protocol ?? "TCP"} onValueChange={(protocol) => update({ ...p, protocol: protocol === "TCP" ? undefined : protocol })}>
              <SelectTrigger size="sm" className="h-8 w-20 text-xs" aria-label="Protocol">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="TCP">TCP</SelectItem>
                <SelectItem value="UDP">UDP</SelectItem>
              </SelectContent>
            </Select>
          </>
        )}
      />
    </KList>
  )
}

function ContainerProbes({ node, container: c, patch }: ContainerPart) {
  const at = containerPath(node.kind, c.name)
  return PROBES.map((p) => (
    <KList key={p.key} path={`${at}.${p.field}`} display={probeText(c.probes[p.key])}>
      <ProbeEditor label={p.label} probe={c.probes[p.key]} defaultPort={c.ports[0] ? String(c.ports[0].containerPort) : "8080"} onChange={(probe) => (probe ? patch({ [`${at}.${p.field}`]: probeObject(probe) }) : patch(undefined, [`${at}.${p.field}`]))} />
    </KList>
  ))
}

function ContainerResources({ node, container: c, patch }: ContainerPart) {
  const at = containerPath(node.kind, c.name)
  return (
    <div className="grid grid-cols-2 gap-2">
      {(["requests", "limits"] as const).flatMap((kind) =>
        (["cpu", "memory"] as const).map((res) => {
          const path = `${at}.resources.${kind}.${res}`
          const value = c.resources[kind][res] ?? ""
          return (
            <KField key={path} path={path} label={`${res === "cpu" ? "CPU" : "Memory"} ${kind === "requests" ? "request" : "limit"}`} htmlFor={path} display={value}>
              <CommitInput
                id={path}
                className="font-mono"
                value={value}
                placeholder={res === "cpu" ? "e.g. 250m" : "e.g. 256Mi"}
                validate={(v) => ((res === "cpu" ? CPU_RE : MEMORY_RE).test(v) ? undefined : res === "cpu" ? "e.g. 0.5 or 500m" : "e.g. 512Mi")}
                onCommit={(v) => (v ? patch({ [path]: v }) : patch(undefined, [path]))}
              />
            </KField>
          )
        }),
      )}
    </div>
  )
}

function ContainerEnv({ node, container: c, patch, configMaps, secrets }: ContainerPart & { configMaps: string[]; secrets: string[] }) {
  const at = containerPath(node.kind, c.name)
  return (
    <>
      <KList path={`${at}.env`} display={c.env.map((e) => e.name).join(", ")}>
        <ListEditor<KubeEnv>
          items={c.env}
          empty="No variables."
          addLabel="Add variable"
          blank={() => ({ name: "", value: "" })}
          complete={(e) => e.name !== "" && (!e.from || (!!e.from.name && !!e.from.key))}
          onChange={(env) => {
            const p = envPatch(at, c.env, env)
            patch(p.set, p.unset)
          }}
          render={(e, update) => <EnvRow env={e} update={update} configMaps={configMaps} secrets={secrets} />}
        />
      </KList>
      <KList path={`${at}.envFrom`} display={c.envFrom.map((e) => `${e.kind} ${e.name}`).join(", ")}>
        <span className="text-[11px] font-medium text-muted-foreground">All keys of</span>
        <ListEditor<KubeEnvFrom>
          items={c.envFrom}
          empty="No ConfigMaps or Secrets loaded whole."
          addLabel="Load a ConfigMap or Secret"
          blank={() => ({ kind: "ConfigMap", name: configMaps[0] ?? "" })}
          complete={(e) => e.name !== ""}
          onChange={(list) => setList(patch, `${at}.envFrom`, envFromList(list.filter((e) => e.name)))}
          render={(e, update) => (
            <>
              <Select value={e.kind} onValueChange={(kind) => update({ ...e, kind: kind as KubeEnvFrom["kind"] })}>
                <SelectTrigger size="sm" className="h-8 w-28 text-xs" aria-label="Kind">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="ConfigMap">ConfigMap</SelectItem>
                  <SelectItem value="Secret">Secret</SelectItem>
                </SelectContent>
              </Select>
              <CommitInput aria-label="Name" className="font-mono" list={e.kind === "Secret" ? "k8s-secrets" : "k8s-configmaps"} placeholder="name" value={e.name} onCommit={(name) => update({ ...e, name })} />
              <CommitInput aria-label="Prefix" width="basis-20" className="font-mono" placeholder="prefix" value={e.prefix ?? ""} onCommit={(prefix) => update({ ...e, prefix: prefix || undefined })} />
            </>
          )}
        />
      </KList>
    </>
  )
}

function ContainerMounts({ node, container: c, patch, volumes }: ContainerPart & { volumes: string[] }) {
  const at = containerPath(node.kind, c.name)
  return (
    <KList path={`${at}.volumeMounts`} display={c.volumeMounts.map((m) => `${m.name}:${m.mountPath}`).join(", ")}>
      <ListEditor<KubeVolumeMount>
        items={c.volumeMounts}
        empty="Nothing mounted."
        addLabel="Mount a volume"
        blank={() => ({ name: volumes[0] ?? "", mountPath: "/data" })}
        complete={(m) => m.name !== "" && m.mountPath.startsWith("/")}
        onChange={(list) => setList(patch, `${at}.volumeMounts`, mountList(list))}
        render={(m, update) => (
          <>
            <Select value={m.name || undefined} onValueChange={(name) => update({ ...m, name })}>
              <SelectTrigger size="sm" className="h-8 w-28 text-xs" aria-label="Volume">
                <SelectValue placeholder="volume" />
              </SelectTrigger>
              <SelectContent>
                {[...new Set([...volumes, ...(m.name ? [m.name] : [])])].map((v) => (
                  <SelectItem key={v} value={v}>
                    {v}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <CommitInput aria-label="Mount path" className="font-mono" value={m.mountPath} validate={(v) => (v.startsWith("/") ? undefined : "An absolute path")} onCommit={(mountPath) => update({ ...m, mountPath })} />
            <label className="mt-2 flex items-center gap-1 text-[11px] text-muted-foreground">
              <Checkbox checked={!!m.readOnly} onCheckedChange={(v) => update({ ...m, readOnly: v === true || undefined })} />
              ro
            </label>
          </>
        )}
      />
    </KList>
  )
}

function probeText(p: KubeProbe | undefined): string {
  if (!p) return "none"
  if (p.type === "exec") return `exec ${p.command ?? ""}`
  return `${p.type} ${p.type === "http" ? (p.path ?? "/") : ""}:${p.port ?? ""}`
}

const NUM_RE = /^$|^\d+$/
const num = (v: string) => (v === "" ? undefined : Number(v))

function ProbeEditor({ label, probe, defaultPort, onChange }: { label: string; probe?: KubeProbe; defaultPort: string; onChange: (p: KubeProbe | undefined) => void }) {
  if (!probe) {
    return (
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="text-muted-foreground">{label}: none</span>
        <Button type="button" variant="outline" size="xs" onClick={() => onChange({ type: "http", path: "/", port: defaultPort })}>
          Add {label.toLowerCase()} probe
        </Button>
      </div>
    )
  }
  const set = (next: Partial<KubeProbe>) => onChange({ ...probe, ...next })
  return (
    <div className="flex flex-col gap-1.5 rounded-md border p-2" data-slot="probe">
      <div className="flex items-center gap-1.5">
        <span className="w-16 shrink-0 text-xs font-medium">{label}</span>
        <Select value={probe.type} onValueChange={(type) => set({ type: type as KubeProbe["type"] })}>
          <SelectTrigger size="sm" className="h-8 w-20 text-xs" aria-label={`${label} probe type`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="http">HTTP</SelectItem>
            <SelectItem value="tcp">TCP</SelectItem>
            <SelectItem value="exec">exec</SelectItem>
            <SelectItem value="grpc">gRPC</SelectItem>
          </SelectContent>
        </Select>
        {probe.type === "http" && <CommitInput aria-label={`${label} path`} className="font-mono" value={probe.path ?? "/"} onCommit={(path) => set({ path })} />}
        {probe.type === "exec" ? (
          <CommitInput aria-label={`${label} command`} className="font-mono" placeholder="command" value={probe.command ?? ""} onCommit={(command) => set({ command })} />
        ) : (
          <CommitInput aria-label={`${label} port`} width="basis-20" className="font-mono" placeholder="port" value={probe.port ?? ""} onCommit={(port) => set({ port })} />
        )}
        <Button type="button" variant="ghost" size="icon-xs" aria-label={`Remove ${label.toLowerCase()} probe`} onClick={() => onChange(undefined)}>
          <XIcon />
        </Button>
      </div>
      <div className="grid grid-cols-4 gap-1.5">
        {(
          [
            ["initialDelaySeconds", "Delay s"],
            ["periodSeconds", "Every s"],
            ["timeoutSeconds", "Timeout s"],
            ["failureThreshold", "Failures"],
          ] as const
        ).map(([key, text]) => (
          <CommitInput
            key={key}
            aria-label={`${label} ${text}`}
            inputMode="numeric"
            placeholder={text}
            value={probe[key] === undefined ? "" : String(probe[key])}
            validate={(v) => (NUM_RE.test(v) ? undefined : "Seconds")}
            onCommit={(v) => {
              const next: KubeProbe = { ...probe }
              next[key] = num(v)
              onChange(next)
            }}
          />
        ))}
      </div>
    </div>
  )
}

const ENV_SOURCE = { value: "Value", ConfigMap: "ConfigMap key", Secret: "Secret key" } as const

function EnvRow({ env: e, update, configMaps, secrets }: { env: KubeEnv; update: (e: KubeEnv) => void; configMaps: string[]; secrets: string[] }) {
  const from = e.from?.kind
  if (from === "Field" || from === "Resource") {
    return (
      <span className="mt-1.5 min-w-0 truncate font-mono text-xs">
        {e.name} <span className="text-muted-foreground">← {from === "Field" ? "field" : "resource"} {e.from?.key}</span>
      </span>
    )
  }
  const source = from ?? "value"
  const names = from === "Secret" ? secrets : configMaps
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-1">
      <div className="flex min-w-0 items-start gap-1.5">
        <CommitInput aria-label="Name" width="basis-32" className="font-mono" placeholder="NAME" value={e.name} validate={(v) => (/^[A-Za-z_][\w.-]*$/.test(v) || v === "" ? undefined : "Letters, digits and “_”")} onCommit={(name) => update({ ...e, name })} />
        <Select
          value={source}
          onValueChange={(v) => update(v === "value" ? { name: e.name, value: "" } : { name: e.name, from: { kind: v as "ConfigMap" | "Secret", name: (v === "Secret" ? secrets : configMaps)[0] ?? "", key: "" } })}
        >
          <SelectTrigger size="sm" className="h-8 min-w-0 flex-1 text-xs" aria-label={`Source of ${e.name}`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {Object.entries(ENV_SOURCE).map(([k, text]) => (
              <SelectItem key={k} value={k}>
                {text}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {source === "value" ? (
        e.secret || SECRET_KEY.test(e.name) ? (
          <SecretInput label={`Value of ${e.name}`} value={e.value ?? ""} placeholder={e.secret && e.value === undefined ? "•••••• (unchanged)" : "value"} onCommit={(value) => update({ name: e.name, value })} />
        ) : (
          <CommitInput aria-label={`Value of ${e.name}`} className="font-mono" placeholder="value" value={e.value ?? ""} onCommit={(value) => update({ ...e, value })} />
        )
      ) : (
        <div className="flex min-w-0 items-start gap-1.5">
          <CommitInput aria-label={`${from} of ${e.name}`} className="font-mono" list={from === "Secret" ? "k8s-secrets" : "k8s-configmaps"} placeholder={names[0] ?? "name"} value={e.from?.name ?? ""} onCommit={(name) => update({ ...e, from: { ...e.from!, name } })} />
          <CommitInput aria-label={`Key of ${e.name}`} className="font-mono" placeholder="key" value={e.from?.key ?? ""} onCommit={(key) => update({ ...e, from: { ...e.from!, key } })} />
        </div>
      )}
    </div>
  )
}

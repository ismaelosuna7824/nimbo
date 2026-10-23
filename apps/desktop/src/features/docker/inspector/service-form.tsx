import { useQuery } from "@tanstack/react-query"
import { ActivityIcon, BoxIcon, HardDriveIcon, LinkIcon, NetworkIcon, PlugIcon, RotateCwIcon, VariableIcon } from "lucide-react"
import { Checkbox } from "@/components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type {
  ComposeDependency,
  ComposeEnvVar,
  ComposeHealthcheck,
  ComposeMount,
  ComposeNode,
  ComposePortMapping,
  DependsOnCondition,
  ServicePatch,
} from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { patchService } from "../actions"
import { loadDockerImages } from "../intel"
import { overridden } from "../environments/model"
import { CommitInput, Field, ListEditor, SecretInput, Section } from "./fields"
import { OverrideMark, useActiveComposeEnvironment } from "./override-mark"

const NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/
const PORT_RE = /^\d{1,5}(-\d{1,5})?$/
const DURATION_RE = /^$|^(\d+(\.\d+)?(ns|us|ms|s|m|h))+$/
/** Keys whose values are secrets (masked; the daemon redacts them too). */
export const SECRET_KEY = /(pass(word)?|secret|token|api[_-]?key|private[_-]?key|credential)/i

const RESTART = [
  { value: "no", label: "Never" },
  { value: "always", label: "Always" },
  { value: "on-failure", label: "On failure" },
  { value: "unless-stopped", label: "Unless stopped" },
]

const CONDITIONS: { value: DependsOnCondition; label: string }[] = [
  { value: "service_started", label: "started" },
  { value: "service_healthy", label: "healthy" },
  { value: "service_completed_successfully", label: "completed" },
]

const NONE = "__none__"

/** Every section of a service: source, ports, environment, volumes, dependencies, health, restart, resources, networks. */
export function ServiceForm({ node }: { node: ComposeNode }) {
  const svc = node.service!
  const graph = useWorkflowStore((s) => s.workflow!.compose?.graph)
  const pro = useWorkflowStore((s) => s.mode === "pro")
  const services = (graph?.nodes ?? []).filter((n) => n.kind === "service" && n.id !== node.id).map((n) => n.name)
  const networks = (graph?.nodes ?? []).filter((n) => n.kind === "network" || (n.kind === "external" && n.externalKind === "network")).map((n) => n.name)
  const volumes = (graph?.nodes ?? []).filter((n) => n.kind === "volume" || (n.kind === "external" && n.externalKind === "volume")).map((n) => n.name)
  const images = useQuery({ queryKey: ["docker-images"], queryFn: loadDockerImages, staleTime: Infinity })
  const patch = (p: ServicePatch) => void patchService(node.name, p)
  const env = useActiveComposeEnvironment()
  const mark = (...attrs: string[]) => <OverrideMark facts={svc} attrs={attrs} onReset={(resetOverrides) => patch({ resetOverrides })} />

  return (
    <div className="flex flex-col gap-5">
      {env && (
        <p className="rounded-md bg-muted/60 px-2 py-1.5 text-[11px] text-muted-foreground" data-testid="docker-env-edit-hint">
          <span className="font-medium text-foreground">{env.name}</span> is active:{" "}
          {env.file ? (
            <>
              fields marked “{env.name} override” are written to <span className="font-mono">{env.file}</span>; the others to
              the base files, shared by every environment.
            </>
          ) : (
            <>
              it only changes variables (<span className="font-mono">{env.envFile}</span>), so edits here go to the base files,
              shared by every environment.
            </>
          )}
        </p>
      )}
      <Section title="Container" icon={<BoxIcon className="size-3.5" />}>
        <Field label="Service name" htmlFor="svc-name" hint="Other services reach it by this name on shared networks.">
          <CommitInput
            id="svc-name"
            className="font-mono"
            value={node.name}
            validate={(v) => (NAME_RE.test(v) ? undefined : "Letters, digits, “_”, “.” or “-”, starting with a letter or digit")}
            onCommit={(name) => patch({ name })}
          />
        </Field>
        <ToggleGroup
          type="single"
          size="sm"
          variant="outline"
          value={svc.build ? "build" : "image"}
          aria-label="Image source"
          onValueChange={(v) => {
            if (v === "build" && !svc.build) patch({ build: { context: `./${node.name}` } })
            if (v === "image" && svc.build) patch({ build: null, image: svc.image?.ref ?? `${node.name}:latest` })
          }}
        >
          <ToggleGroupItem value="image">Image</ToggleGroupItem>
          <ToggleGroupItem value="build">Build from Dockerfile</ToggleGroupItem>
        </ToggleGroup>
        {svc.build ? (
          <div className="grid grid-cols-2 gap-2">
            <Field label="Context" htmlFor="svc-context" badge={mark("build")}>
              <CommitInput id="svc-context" className="font-mono" value={svc.build.context} onCommit={(context) => patch({ build: { ...svc.build!, context } })} />
            </Field>
            <Field label="Dockerfile" htmlFor="svc-dockerfile">
              <CommitInput id="svc-dockerfile" className="font-mono" value={svc.build.dockerfile} onCommit={(dockerfile) => patch({ build: { ...svc.build!, dockerfile } })} />
            </Field>
            <Field label="Target stage" htmlFor="svc-target">
              <CommitInput id="svc-target" className="font-mono" value={svc.build.target ?? ""} placeholder="last stage" onCommit={(target) => patch({ build: { ...svc.build!, target: target || undefined } })} />
            </Field>
            <Field label="Image name (optional)" htmlFor="svc-image-tag">
              <CommitInput id="svc-image-tag" className="font-mono" value={svc.image?.ref ?? ""} placeholder="tag the build" onCommit={(image) => patch({ image: image || null })} />
            </Field>
          </div>
        ) : (
          <Field label="Image" htmlFor="svc-image" badge={mark("image")}>
            <CommitInput id="svc-image" className="font-mono" list="docker-image-options" value={svc.image?.ref ?? ""} placeholder="nginx:1.27-alpine" onCommit={(image) => patch({ image: image || null })} />
            <datalist id="docker-image-options">
              {(images.data ?? []).map((i) => (
                <option key={i.id} value={`${i.image}:${i.tag}`}>
                  {i.label}
                </option>
              ))}
            </datalist>
          </Field>
        )}
        {pro && (
          <Field label="Command" htmlFor="svc-command" hint="Overrides the image's CMD (shell form)." badge={mark("command")}>
            <CommitInput id="svc-command" className="font-mono" value={svc.command ?? ""} placeholder="from the image" onCommit={(command) => patch({ command: command || null })} />
          </Field>
        )}
      </Section>

      <Section title="Ports" icon={<PlugIcon className="size-3.5" />} action={mark("ports")}>
        <ListEditor<ComposePortMapping>
          items={svc.ports}
          empty="Not published: only other services on its networks can reach it."
          addLabel="Publish a port"
          blank={() => ({ container: String(svc.expose[0] ?? "80"), host: "" })}
          onChange={(ports) => patch({ ports: ports.map(({ published: _, ...p }) => ({ ...p, host: p.host || undefined })) })}
          render={(p, update) => (
            <>
              <CommitInput aria-label="Host port" className="font-mono" placeholder="host (auto)" value={p.host ?? ""} validate={(v) => (v === "" || PORT_RE.test(v) ? undefined : "A port or range")} onCommit={(host) => update({ ...p, host })} />
              <span className="mt-1.5 text-xs text-muted-foreground">→</span>
              <CommitInput aria-label="Container port" className="font-mono" placeholder="container" value={p.container} validate={(v) => (PORT_RE.test(v) ? undefined : "A port or range")} onCommit={(container) => update({ ...p, container })} />
              <Select value={p.protocol ?? "tcp"} onValueChange={(protocol) => update({ ...p, protocol: protocol === "tcp" ? undefined : protocol })}>
                <SelectTrigger size="sm" className="h-8 w-20 text-xs" aria-label="Protocol">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="tcp">tcp</SelectItem>
                  <SelectItem value="udp">udp</SelectItem>
                </SelectContent>
              </Select>
            </>
          )}
        />
      </Section>

      <Section title="Environment" icon={<VariableIcon className="size-3.5" />} action={mark("environment")}>
        <ListEditor<ComposeEnvVar>
          items={svc.environment.filter((e) => !e.fromFile)}
          empty="No variables."
          addLabel="Add variable"
          blank={() => ({ key: "", value: "" })}
          complete={(e) => e.key !== ""}
          onChange={(env) =>
            patch({
              environment: env
                .filter((e) => e.key)
                // A redacted secret goes back without its value: the daemon keeps the stored one. A value with
                // ${VAR}s goes back as written, never interpolated (that would copy .env passwords into the file).
                .map((e) => (e.secret && e.value === undefined ? { key: e.key, secret: true } : { key: e.key, value: e.raw ?? e.value })),
            })
          }
          render={(e, update) => {
            const secret = e.secret || SECRET_KEY.test(e.key)
            return (
              <>
                <CommitInput aria-label="Name" width="basis-36" className="font-mono" placeholder="NAME" value={e.key} validate={(v) => (/^[A-Za-z_][\w.-]*$/.test(v) || v === "" ? undefined : "Letters, digits and “_”")} onCommit={(key) => update({ ...e, key })} />
                {secret ? (
                  <SecretInput label={`Value of ${e.key}`} value={e.raw ?? e.value ?? ""} placeholder={e.secret && e.value === undefined ? "•••••• (unchanged)" : "value"} onCommit={(value) => update({ key: e.key, value })} />
                ) : (
                  <CommitInput aria-label={`Value of ${e.key}`} className="font-mono" placeholder={e.unresolved ? "not set in .env" : "value"} value={e.raw ?? e.value ?? ""} onCommit={(value) => update({ ...e, value, raw: undefined })} />
                )}
              </>
            )
          }}
        />
        {svc.envFiles && svc.envFiles.length > 0 && <p className="text-[11px] text-muted-foreground">Also from {svc.envFiles.join(", ")} (edit in the Code tab).</p>}
      </Section>

      <Section title="Volumes" icon={<HardDriveIcon className="size-3.5" />} action={mark("volumes")}>
        <ListEditor<ComposeMount>
          items={svc.volumes}
          empty="Nothing mounted: data is lost when the container is recreated."
          addLabel="Mount"
          blank={() => ({ type: "volume", source: volumes[0] ?? "", target: "/data" })}
          onChange={(mounts) => patch({ volumes: mounts.filter((m) => m.target) })}
          render={(m, update) => (
            <>
              <Select value={m.type ?? "volume"} onValueChange={(type) => update({ ...m, type: type as ComposeMount["type"] })}>
                <SelectTrigger size="sm" className="h-8 w-24 text-xs" aria-label="Mount type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="volume">volume</SelectItem>
                  <SelectItem value="bind">bind</SelectItem>
                  <SelectItem value="tmpfs">tmpfs</SelectItem>
                </SelectContent>
              </Select>
              {m.type !== "tmpfs" && (
                <CommitInput aria-label="Source" className="font-mono" list={m.type === "bind" ? undefined : "docker-volume-options"} placeholder={m.type === "bind" ? "./host/path" : "volume"} value={m.source ?? ""} onCommit={(source) => update({ ...m, source })} />
              )}
              <CommitInput aria-label="Path in the container" className="font-mono" placeholder="/data" value={m.target} validate={(v) => (v.startsWith("/") ? undefined : "An absolute path")} onCommit={(target) => update({ ...m, target })} />
              <label className="mt-2 flex items-center gap-1 text-[11px] text-muted-foreground">
                <Checkbox checked={!!m.readOnly} onCheckedChange={(c) => update({ ...m, readOnly: c === true || undefined })} />
                ro
              </label>
            </>
          )}
        />
        <datalist id="docker-volume-options">
          {volumes.map((v) => (
            <option key={v} value={v} />
          ))}
        </datalist>
      </Section>

      <Section title="Depends on" icon={<LinkIcon className="size-3.5" />} action={mark("depends_on")}>
        <ListEditor<ComposeDependency>
          items={svc.dependsOn}
          empty="Starts without waiting for other services."
          addLabel="Add dependency"
          blank={() => ({ service: services.find((s) => !svc.dependsOn.some((d) => d.service === s)) ?? "", condition: "service_started" })}
          complete={(d) => d.service !== ""}
          onChange={(deps) => patch({ dependsOn: deps.filter((d) => d.service).map((d) => ({ service: d.service, condition: d.condition })) })}
          render={(d, update) => (
            <>
              <Select value={d.service || NONE} onValueChange={(service) => update({ ...d, service: service === NONE ? "" : service })}>
                <SelectTrigger size="sm" className="h-8 min-w-0 flex-1 text-xs" aria-label="Service">
                  <SelectValue placeholder="service" />
                </SelectTrigger>
                <SelectContent>
                  {services.length === 0 && <SelectItem value={NONE} disabled>No other services</SelectItem>}
                  {services.map((s) => (
                    <SelectItem key={s} value={s}>
                      {s}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Select value={d.condition ?? "service_started"} onValueChange={(condition) => update({ ...d, condition })}>
                <SelectTrigger size="sm" className="h-8 w-32 text-xs" aria-label="Wait until">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {CONDITIONS.map((c) => (
                    <SelectItem key={c.value} value={c.value}>
                      until {c.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </>
          )}
        />
      </Section>

      <Section title="Health check" icon={<ActivityIcon className="size-3.5" />} action={mark("healthcheck")}>
        <HealthcheckFields hc={svc.healthcheck} fromDockerfile={svc.healthcheckFrom === "dockerfile"} onChange={(healthcheck) => patch({ healthcheck })} />
      </Section>

      <Section title="Restart & resources" icon={<RotateCwIcon className="size-3.5" />}>
        <div className="grid grid-cols-2 gap-2">
          <Field label="Restart" badge={mark("restart")}>
            <Select value={svc.restart ?? NONE} onValueChange={(v) => patch({ restart: v === NONE ? null : v })}>
              <SelectTrigger size="sm" className="h-8 w-full text-xs" aria-label="Restart policy">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>Default (never)</SelectItem>
                {RESTART.map((r) => (
                  <SelectItem key={r.value} value={r.value}>
                    {r.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Replicas" htmlFor="svc-replicas" badge={mark("deploy.replicas")}>
            <CommitInput
              id="svc-replicas"
              inputMode="numeric"
              value={String(svc.replicas ?? 1)}
              validate={(v) => (/^\d+$/.test(v) && Number(v) <= 50 ? undefined : "0–50")}
              // 1 is compose's default: dropped from the file, unless the environment's override sets it (dropping it
              // there would bring back the base's count).
              onCommit={(v) => patch({ replicas: v === "1" && !overridden(svc, "deploy.replicas") ? null : Number(v) })}
            />
          </Field>
          <Field label="CPUs" htmlFor="svc-cpus" badge={mark("deploy.resources")}>
            <CommitInput id="svc-cpus" className="font-mono" placeholder="no limit" value={svc.resources?.cpus ?? ""} validate={(v) => (v === "" || /^\d+(\.\d+)?$/.test(v) ? undefined : "e.g. 0.5")} onCommit={(cpus) => patch({ resources: cpus || svc.resources?.memory ? { ...svc.resources, cpus: cpus || undefined } : null })} />
          </Field>
          <Field label="Memory" htmlFor="svc-memory" badge={mark("deploy.resources")}>
            <CommitInput id="svc-memory" className="font-mono" placeholder="no limit" value={svc.resources?.memory ?? ""} validate={(v) => (v === "" || /^\d+(\.\d+)?[bkmg]?b?$/i.test(v) ? undefined : "e.g. 512M")} onCommit={(memory) => patch({ resources: memory || svc.resources?.cpus ? { ...svc.resources, memory: memory || undefined } : null })} />
          </Field>
        </div>
      </Section>

      <Section title="Networks" icon={<NetworkIcon className="size-3.5" />} action={mark("networks")}>
        {networks.length === 0 ? (
          <p className="text-[11px] text-muted-foreground">On the project's default network with every other service. Add a network from the palette to separate them.</p>
        ) : (
          <div className="flex flex-col gap-1.5">
            {networks.map((n) => {
              const on = svc.networks.includes(n)
              return (
                <label key={n} className="flex items-center gap-2 text-xs">
                  <Checkbox checked={on} onCheckedChange={(c) => patch({ networks: c === true ? [...svc.networks, n] : svc.networks.filter((x) => x !== n) })} />
                  <span className="font-mono">{n}</span>
                </label>
              )
            })}
            {svc.networks.length === 0 && <p className="text-[11px] text-muted-foreground">None checked: the project's default network.</p>}
          </div>
        )}
      </Section>
    </div>
  )
}

function HealthcheckFields({ hc, fromDockerfile, onChange }: { hc?: ComposeHealthcheck; fromDockerfile: boolean; onChange: (hc: ComposeHealthcheck | null) => void }) {
  if (!hc || hc.disabled) {
    return (
      <div className="flex flex-col gap-1.5">
        <p className="text-[11px] text-muted-foreground">
          {hc?.disabled ? "Disabled." : "No health check: services that wait for it to be healthy never start."}
        </p>
        <button type="button" className="self-start text-xs text-primary underline-offset-2 hover:underline" onClick={() => onChange({ test: "CMD-SHELL curl -fsS http://localhost/ || exit 1", interval: "10s", timeout: "3s", retries: 5 })}>
          Add a health check
        </button>
      </div>
    )
  }
  const set = (p: Partial<ComposeHealthcheck>) => onChange({ ...hc, ...p })
  return (
    <div className="flex flex-col gap-2">
      {fromDockerfile && <p className="text-[11px] text-muted-foreground">From the Dockerfile's HEALTHCHECK; changing it here overrides it in the compose file.</p>}
      <Field label="Test" htmlFor="hc-test" hint="CMD-SHELL runs it in a shell; exit code 0 = healthy.">
        <CommitInput id="hc-test" className="font-mono" value={hc.test} onCommit={(test) => set({ test })} />
      </Field>
      <div className="grid grid-cols-4 gap-2">
        <Field label="Interval">
          <CommitInput aria-label="Interval" className="font-mono" value={hc.interval ?? ""} placeholder="30s" validate={(v) => (DURATION_RE.test(v) ? undefined : "e.g. 10s")} onCommit={(interval) => set({ interval: interval || undefined })} />
        </Field>
        <Field label="Timeout">
          <CommitInput aria-label="Timeout" className="font-mono" value={hc.timeout ?? ""} placeholder="30s" validate={(v) => (DURATION_RE.test(v) ? undefined : "e.g. 3s")} onCommit={(timeout) => set({ timeout: timeout || undefined })} />
        </Field>
        <Field label="Retries">
          <CommitInput aria-label="Retries" value={hc.retries === undefined ? "" : String(hc.retries)} placeholder="3" validate={(v) => (v === "" || /^\d+$/.test(v) ? undefined : "A number")} onCommit={(v) => set({ retries: v ? Number(v) : undefined })} />
        </Field>
        <Field label="Start period">
          <CommitInput aria-label="Start period" className="font-mono" value={hc.startPeriod ?? ""} placeholder="0s" validate={(v) => (DURATION_RE.test(v) ? undefined : "e.g. 20s")} onCommit={(startPeriod) => set({ startPeriod: startPeriod || undefined })} />
        </Field>
      </div>
      <button type="button" className="self-start text-xs text-muted-foreground underline-offset-2 hover:text-destructive hover:underline" onClick={() => onChange(null)}>
        Remove health check
      </button>
    </div>
  )
}

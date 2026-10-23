import { ExpandIcon, HardDriveIcon, LinkIcon, PlugIcon, RouteIcon, TriangleAlertIcon } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Checkbox } from "@/components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { CommitInput, ListEditor, Section } from "@/features/docker/inspector/fields"
import type { KubeNode, KubeRoute, KubeServicePort } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { patchObject } from "../actions"
import { KField, KList } from "./kube-fields"
import { hpaMetrics, ingressRules, servicePorts } from "./paths"

const PORT_RE = /^\d{1,5}$/
const LABEL_KEY_RE = /^([a-z0-9.-]+\/)?[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$/
const SERVICE_TYPES = ["ClusterIP", "NodePort", "LoadBalancer"]

type Pair = { key: string; value: string }

export function ServiceForm({ node }: { node: KubeNode }) {
  const svc = node.service!
  const patch = (set?: Record<string, unknown>, unset?: string[]) => void patchObject(node, set, unset)
  const selector = Object.entries(svc.selector ?? {}).map(([key, value]) => ({ key, value }))
  return (
    <div className="flex flex-col gap-5">
      {svc.unmatched && (
        <Alert data-testid="k8s-service-unmatched-alert">
          <TriangleAlertIcon />
          <AlertTitle>The selector matches no pods</AlertTitle>
          <AlertDescription>Traffic to this Service goes nowhere. Drag from it to a workload on the canvas to point the selector at that workload's pods.</AlertDescription>
        </Alert>
      )}
      <Section title="Service" icon={<PlugIcon className="size-3.5" />}>
        <KField path="spec.type" label="Type" display={svc.type}>
          <Select value={svc.type} onValueChange={(type) => patch({ "spec.type": type })} disabled={svc.type === "ExternalName"}>
            <SelectTrigger size="sm" className="h-8 w-full text-xs" aria-label="Service type">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {[...new Set([...SERVICE_TYPES, svc.type])].map((t) => (
                <SelectItem key={t} value={t}>
                  {t}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </KField>
        <span className="text-[11px] font-medium text-muted-foreground">Ports</span>
        <KList path="spec.ports" display={svc.ports.map((p) => `${p.port}→${p.targetPort ?? p.port}`).join(", ")}>
          <ListEditor<KubeServicePort>
            items={svc.ports}
            empty="No ports."
            addLabel="Add port"
            blank={() => ({ port: 80, targetPort: "8080" })}
            onChange={(ports) => (ports.length ? patch({ "spec.ports": servicePorts(ports) }) : patch(undefined, ["spec.ports"]))}
            render={(p, update) => (
              <>
                <CommitInput aria-label="Port name" className="w-20 flex-none font-mono" placeholder="name" value={p.name ?? ""} onCommit={(name) => update({ ...p, name: name || undefined })} />
                <CommitInput aria-label="Port" className="font-mono" value={String(p.port)} validate={(v) => (PORT_RE.test(v) ? undefined : "A port")} onCommit={(v) => update({ ...p, port: Number(v) })} />
                <span className="mt-1.5 text-xs text-muted-foreground">→</span>
                <CommitInput aria-label="Target port" className="font-mono" placeholder="same" value={p.targetPort ?? ""} onCommit={(targetPort) => update({ ...p, targetPort: targetPort || undefined })} />
                {svc.type === "NodePort" && (
                  <CommitInput aria-label="Node port" className="w-20 flex-none font-mono" placeholder="node" value={p.nodePort ? String(p.nodePort) : ""} validate={(v) => (v === "" || PORT_RE.test(v) ? undefined : "A port")} onCommit={(v) => update({ ...p, nodePort: v ? Number(v) : undefined })} />
                )}
              </>
            )}
          />
        </KList>
      </Section>
      <Section title="Selector" icon={<LinkIcon className="size-3.5" />}>
        <KList path="spec.selector" display={selector.map((s) => `${s.key}=${s.value}`).join(", ")}>
          <ListEditor<Pair>
            items={selector}
            empty="No selector: the Service selects no pods by itself."
            addLabel="Add label"
            blank={() => ({ key: "", value: "" })}
            complete={(p) => p.key !== ""}
            onChange={(pairs) => (pairs.length ? patch({ "spec.selector": Object.fromEntries(pairs.filter((p) => p.key).map((p) => [p.key, p.value])) }) : patch(undefined, ["spec.selector"]))}
            render={(p, update) => (
              <>
                <CommitInput aria-label="Label" className="font-mono" placeholder="app" value={p.key} validate={(v) => (v === "" || LABEL_KEY_RE.test(v) ? undefined : "A label key")} onCommit={(key) => update({ ...p, key })} />
                <span className="mt-1.5 text-xs text-muted-foreground">=</span>
                <CommitInput aria-label={`Value of ${p.key}`} className="font-mono" placeholder="value" value={p.value} onCommit={(value) => update({ ...p, value })} />
              </>
            )}
          />
        </KList>
        <p className="text-[11px] text-muted-foreground">Connecting the Service to a workload on the canvas sets this for you.</p>
      </Section>
    </div>
  )
}

export function IngressForm({ node }: { node: KubeNode }) {
  const routes = node.routes ?? []
  const graph = useWorkflowStore((s) => s.workflow!.kubernetes?.graph)
  const services = (graph?.nodes ?? []).filter((n) => n.kind === "Service" && n.namespace === node.namespace)
  const patch = (set?: Record<string, unknown>, unset?: string[]) => void patchObject(node, set, unset)
  const firstPort = (name: string) => services.find((s) => s.name === name)?.service?.ports[0]?.port ?? 80
  const hosts = [...new Set(routes.flatMap((r) => (r.host ? [r.host] : [])))]
  const tls = routes.some((r) => r.tls)
  const cls = routes.find((r) => r.class)?.class ?? ""
  if (node.kind !== "Ingress") {
    return (
      <Section title="Routes" icon={<RouteIcon className="size-3.5" />}>
        <RouteList routes={routes} />
        <p className="text-[11px] text-muted-foreground">Edit {node.kind} rules in code.</p>
      </Section>
    )
  }
  return (
    <div className="flex flex-col gap-5">
      <Section title="Ingress" icon={<RouteIcon className="size-3.5" />}>
        <KField path="spec.ingressClassName" label="Class" htmlFor="k8s-ingress-class" display={cls} hint="traefik is the local cluster's default; nginx works as an alias.">
          <CommitInput id="k8s-ingress-class" className="font-mono" value={cls} placeholder="default class" onCommit={(v) => (v ? patch({ "spec.ingressClassName": v }) : patch(undefined, ["spec.ingressClassName"]))} />
        </KField>
        <span className="text-[11px] font-medium text-muted-foreground">Rules</span>
        <KList path="spec.rules" display={routes.map((r) => `${r.host ?? "*"}${r.path ?? "/"} → ${r.service}:${r.port ?? ""}`).join(", ")}>
          <ListEditor<KubeRoute>
            items={routes}
            empty="No rules: nothing is routed."
            addLabel="Add rule"
            blank={() => ({ path: "/", service: services[0]?.name ?? "" })}
            complete={(r) => r.service !== ""}
            onChange={(next) => {
              const rules = ingressRules(next.filter((r) => r.service), firstPort)
              if (rules.length) patch({ "spec.rules": rules })
              else patch(undefined, ["spec.rules"])
            }}
            render={(r, update) => (
              <div className="flex min-w-0 flex-1 flex-col gap-1">
                <div className="flex min-w-0 items-start gap-1.5">
                  <CommitInput aria-label="Host" className="font-mono" placeholder="any host" value={r.host ?? ""} onCommit={(host) => update({ ...r, host: host || undefined })} />
                  <CommitInput aria-label="Path" className="w-24 flex-none font-mono" value={r.path ?? "/"} validate={(v) => (v.startsWith("/") ? undefined : "Starts with /")} onCommit={(path) => update({ ...r, path })} />
                </div>
                <div className="flex min-w-0 items-start gap-1.5">
                  <span className="mt-1.5 text-xs text-muted-foreground">→</span>
                  <CommitInput aria-label="Service" className="font-mono" list="k8s-services" placeholder="service" value={r.service} onCommit={(service) => update({ ...r, service })} />
                  <CommitInput aria-label="Service port" className="w-20 flex-none font-mono" placeholder={String(firstPort(r.service))} value={r.port ?? ""} onCommit={(port) => update({ ...r, port: port || undefined })} />
                </div>
              </div>
            )}
          />
        </KList>
        <datalist id="k8s-services">
          {services.map((s) => (
            <option key={s.id} value={s.name} />
          ))}
        </datalist>
        <KList path="spec.tls" display={tls ? `TLS for ${hosts.join(", ")}` : "no TLS"}>
          <label className="flex items-center gap-2 text-xs">
            <Checkbox
              checked={tls}
              disabled={!tls && hosts.length === 0}
              onCheckedChange={(c) => (c === true ? patch({ "spec.tls": [{ hosts, secretName: `${node.name}-tls` }] }) : patch(undefined, ["spec.tls"]))}
            />
            HTTPS (TLS) for {hosts.length ? hosts.join(", ") : "its hosts (add a host first)"}
          </label>
        </KList>
      </Section>
    </div>
  )
}

function RouteList({ routes }: { routes: KubeRoute[] }) {
  if (routes.length === 0) return <p className="text-[11px] text-muted-foreground">No rules.</p>
  return (
    <ul className="flex flex-col gap-0.5 font-mono text-xs">
      {routes.map((r, i) => (
        <li key={i} className="truncate">
          {r.host ?? "*"}
          {r.path ?? "/"} <span className="text-muted-foreground">→</span> {r.service}
          {r.port ? `:${r.port}` : ""}
        </li>
      ))}
    </ul>
  )
}

export function HpaForm({ node }: { node: KubeNode }) {
  const s = node.scaler!
  const patch = (set?: Record<string, unknown>, unset?: string[]) => void patchObject(node, set, unset)
  const target = useWorkflowStore((st) => st.workflow!.kubernetes?.graph.nodes.find((n) => n.id === s.target))
  const select = useWorkflowStore((st) => st.select)
  const intRe = /^\d+$/
  return (
    <Section title="Autoscaling" icon={<ExpandIcon className="size-3.5" />}>
      <p className="text-xs">
        Scales{" "}
        {target ? (
          <button type="button" className="font-mono text-primary hover:underline" onClick={() => select(target.id)}>
            {target.kind} {target.name}
          </button>
        ) : (
          <span className="font-mono">{s.target}</span>
        )}
      </p>
      <div className="grid grid-cols-3 gap-2">
        <KField path="spec.minReplicas" label="Min" htmlFor="k8s-hpa-min" display={String(s.minReplicas)}>
          <CommitInput id="k8s-hpa-min" inputMode="numeric" value={String(s.minReplicas)} validate={(v) => (intRe.test(v) && Number(v) >= 1 && Number(v) <= s.maxReplicas ? undefined : `1–${s.maxReplicas}`)} onCommit={(v) => patch({ "spec.minReplicas": Number(v) })} />
        </KField>
        <KField path="spec.maxReplicas" label="Max" htmlFor="k8s-hpa-max" display={String(s.maxReplicas)}>
          <CommitInput id="k8s-hpa-max" inputMode="numeric" value={String(s.maxReplicas)} validate={(v) => (intRe.test(v) && Number(v) >= s.minReplicas && Number(v) <= 100 ? undefined : `${s.minReplicas}–100`)} onCommit={(v) => patch({ "spec.maxReplicas": Number(v) })} />
        </KField>
        <KField path="spec.metrics" label="CPU target %" htmlFor="k8s-hpa-cpu" display={s.cpu ? `${s.cpu}%` : ""}>
          <CommitInput
            id="k8s-hpa-cpu"
            inputMode="numeric"
            value={s.cpu ? String(s.cpu) : ""}
            placeholder="none"
            validate={(v) => (v === "" || (intRe.test(v) && Number(v) >= 1 && Number(v) <= 100) ? undefined : "1–100")}
            onCommit={(v) => {
              const metrics = hpaMetrics({ cpu: v ? Number(v) : undefined, memory: s.memory })
              if (metrics.length) patch({ "spec.metrics": metrics })
              else patch(undefined, ["spec.metrics"])
            }}
          />
        </KField>
      </div>
      <p className="text-[11px] text-muted-foreground">Needs CPU requests on the containers; the local cluster runs metrics-server.</p>
    </Section>
  )
}

export function ClaimForm({ node }: { node: KubeNode }) {
  const st = node.storage ?? {}
  const patch = (set?: Record<string, unknown>, unset?: string[]) => void patchObject(node, set, unset)
  return (
    <Section title="Storage" icon={<HardDriveIcon className="size-3.5" />}>
      <div className="grid grid-cols-2 gap-2">
        <KField path="spec.resources.requests.storage" label="Size" htmlFor="k8s-pvc-size" display={st.size}>
          <CommitInput id="k8s-pvc-size" className="font-mono" value={st.size ?? ""} placeholder="1Gi" validate={(v) => (/^\d+(\.\d+)?(Ki|Mi|Gi|Ti)?$/.test(v) ? undefined : "e.g. 1Gi")} onCommit={(v) => patch({ "spec.resources.requests.storage": v })} />
        </KField>
        <KField path="spec.storageClassName" label="Storage class" htmlFor="k8s-pvc-class" display={st.storageClass}>
          <CommitInput id="k8s-pvc-class" className="font-mono" value={st.storageClass ?? ""} placeholder="local-path (default)" onCommit={(v) => (v ? patch({ "spec.storageClassName": v }) : patch(undefined, ["spec.storageClassName"]))} />
        </KField>
      </div>
      {st.accessModes && st.accessModes.length > 0 && <p className="font-mono text-[11px] text-muted-foreground">{st.accessModes.join(", ")}</p>}
    </Section>
  )
}

import { ExpandIcon, HardDriveIcon, KeyRoundIcon, LinkIcon, PlugIcon, RouteIcon, ShieldCheckIcon, TriangleAlertIcon } from "lucide-react"
import { useContext } from "react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Checkbox } from "@/components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { CommitInput, ListEditor, SecretInput } from "@/features/docker/inspector/fields"
import { InspectorSection } from "@/features/inspector/inspector-section"
import type { KubeNode, KubeRoute, KubeServicePort } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { patchObject } from "../actions"
import { KField, KList, KubeFieldsContext } from "./kube-fields"
import { hpaMetrics, ingressRules, servicePorts } from "./paths"
import {
  budgetPatch,
  DATA_KEY_RE,
  dataEntries,
  dataPatch,
  labelsPatch,
  POD_SECURITY_LABEL,
  POD_SECURITY_LEVELS,
  withPodSecurity,
  type BudgetMode,
  type DataEntry,
} from "./workload-extras"

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
      <InspectorSection id="k8s:service" title="Service" icon={<PlugIcon className="size-3.5" />}>
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
                <CommitInput aria-label="Port name" width="basis-20" className="font-mono" placeholder="name" value={p.name ?? ""} onCommit={(name) => update({ ...p, name: name || undefined })} />
                <CommitInput aria-label="Port" className="font-mono" value={String(p.port)} validate={(v) => (PORT_RE.test(v) ? undefined : "A port")} onCommit={(v) => update({ ...p, port: Number(v) })} />
                <span className="mt-1.5 text-xs text-muted-foreground">→</span>
                <CommitInput aria-label="Target port" className="font-mono" placeholder="same" value={p.targetPort ?? ""} onCommit={(targetPort) => update({ ...p, targetPort: targetPort || undefined })} />
                {svc.type === "NodePort" && (
                  <CommitInput aria-label="Node port" width="basis-20" className="font-mono" placeholder="node" value={p.nodePort ? String(p.nodePort) : ""} validate={(v) => (v === "" || PORT_RE.test(v) ? undefined : "A port")} onCommit={(v) => update({ ...p, nodePort: v ? Number(v) : undefined })} />
                )}
              </>
            )}
          />
        </KList>
      </InspectorSection>
      <InspectorSection id="k8s:selector" title="Selector" icon={<LinkIcon className="size-3.5" />}>
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
      </InspectorSection>
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
      <InspectorSection id="k8s:routes" title="Routes" icon={<RouteIcon className="size-3.5" />}>
        <RouteList routes={routes} />
        <p className="text-[11px] text-muted-foreground">Edit {node.kind} rules in code.</p>
      </InspectorSection>
    )
  }
  return (
    <div className="flex flex-col gap-5">
      <InspectorSection id="k8s:ingress" title="Ingress" icon={<RouteIcon className="size-3.5" />}>
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
                  <CommitInput aria-label="Path" width="basis-24" className="font-mono" value={r.path ?? "/"} validate={(v) => (v.startsWith("/") ? undefined : "Starts with /")} onCommit={(path) => update({ ...r, path })} />
                </div>
                <div className="flex min-w-0 items-start gap-1.5">
                  <span className="mt-1.5 text-xs text-muted-foreground">→</span>
                  <CommitInput aria-label="Service" className="font-mono" list="k8s-services" placeholder="service" value={r.service} onCommit={(service) => update({ ...r, service })} />
                  <CommitInput aria-label="Service port" width="basis-20" className="font-mono" placeholder={String(firstPort(r.service))} value={r.port ?? ""} onCommit={(port) => update({ ...r, port: port || undefined })} />
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
      </InspectorSection>
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
    <InspectorSection id="k8s:autoscaling" title="Autoscaling" icon={<ExpandIcon className="size-3.5" />}>
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
      <div className="grid grid-cols-2 gap-2">
        <KField path="spec.minReplicas" label="Min" htmlFor="k8s-hpa-min" display={String(s.minReplicas)}>
          <CommitInput id="k8s-hpa-min" inputMode="numeric" value={String(s.minReplicas)} validate={(v) => (intRe.test(v) && Number(v) >= 1 && Number(v) <= s.maxReplicas ? undefined : `1–${s.maxReplicas}`)} onCommit={(v) => patch({ "spec.minReplicas": Number(v) })} />
        </KField>
        <KField path="spec.maxReplicas" label="Max" htmlFor="k8s-hpa-max" display={String(s.maxReplicas)}>
          <CommitInput id="k8s-hpa-max" inputMode="numeric" value={String(s.maxReplicas)} validate={(v) => (intRe.test(v) && Number(v) >= s.minReplicas && Number(v) <= 100 ? undefined : `${s.minReplicas}–100`)} onCommit={(v) => patch({ "spec.maxReplicas": Number(v) })} />
        </KField>
        {(["cpu", "memory"] as const).map((res) => (
          <KField key={res} path="spec.metrics" label={`${res === "cpu" ? "CPU" : "Memory"} target %`} htmlFor={`k8s-hpa-${res}`} display={s[res] ? `${s[res]}%` : ""}>
            <CommitInput
              id={`k8s-hpa-${res}`}
              inputMode="numeric"
              value={s[res] ? String(s[res]) : ""}
              placeholder="none"
              validate={(v) => (v === "" || (intRe.test(v) && Number(v) >= 1 && Number(v) <= 100) ? undefined : "1–100")}
              onCommit={(v) => {
                const metrics = hpaMetrics({ cpu: s.cpu, memory: s.memory, [res]: v ? Number(v) : undefined })
                if (metrics.length) patch({ "spec.metrics": metrics })
                else patch(undefined, ["spec.metrics"])
              }}
            />
          </KField>
        ))}
      </div>
      <p className="text-[11px] text-muted-foreground">
        Needs requests on the containers for the resources it watches (CPU is the usual signal: memory rarely drops when load does); the local cluster runs
        metrics-server.
      </p>
    </InspectorSection>
  )
}

export function ClaimForm({ node }: { node: KubeNode }) {
  const st = node.storage ?? {}
  const patch = (set?: Record<string, unknown>, unset?: string[]) => void patchObject(node, set, unset)
  return (
    <InspectorSection id="k8s:claim" title="Storage" icon={<HardDriveIcon className="size-3.5" />}>
      <div className="grid grid-cols-2 gap-2">
        <KField path="spec.resources.requests.storage" label="Size" htmlFor="k8s-pvc-size" display={st.size}>
          <CommitInput id="k8s-pvc-size" className="font-mono" value={st.size ?? ""} placeholder="1Gi" validate={(v) => (/^\d+(\.\d+)?(Ki|Mi|Gi|Ti)?$/.test(v) ? undefined : "e.g. 1Gi")} onCommit={(v) => patch({ "spec.resources.requests.storage": v })} />
        </KField>
        <KField path="spec.storageClassName" label="Storage class" htmlFor="k8s-pvc-class" display={st.storageClass}>
          <CommitInput id="k8s-pvc-class" className="font-mono" value={st.storageClass ?? ""} placeholder="local-path (default)" onCommit={(v) => (v ? patch({ "spec.storageClassName": v }) : patch(undefined, ["spec.storageClassName"]))} />
        </KField>
      </div>
      {st.accessModes && st.accessModes.length > 0 && <p className="font-mono text-[11px] text-muted-foreground">{st.accessModes.join(", ")}</p>}
    </InspectorSection>
  )
}

/** Pairs editor of a string map (labels, selectors). */
function PairsEditor({ pairs, empty, placeholder, onChange }: { pairs: Pair[]; empty: string; placeholder: string; onChange: (map: Record<string, string>) => void }) {
  return (
    <ListEditor<Pair>
      items={pairs}
      empty={empty}
      addLabel="Add label"
      blank={() => ({ key: "", value: "" })}
      complete={(p) => p.key !== ""}
      onChange={(next) => onChange(Object.fromEntries(next.filter((p) => p.key).map((p) => [p.key, p.value])))}
      render={(p, update) => (
        <>
          <CommitInput aria-label="Label" className="font-mono" placeholder={placeholder} value={p.key} validate={(v) => (v === "" || LABEL_KEY_RE.test(v) ? undefined : "A label key")} onCommit={(key) => update({ ...p, key })} />
          <span className="mt-1.5 text-xs text-muted-foreground">=</span>
          <CommitInput aria-label={`Value of ${p.key}`} className="font-mono" placeholder="value" value={p.value} onCommit={(value) => update({ ...p, value })} />
        </>
      )}
    />
  )
}

const toPairs = (m: Record<string, string> | undefined): Pair[] => Object.entries(m ?? {}).map(([key, value]) => ({ key, value }))

const POD_SECURITY_HINT: Record<string, string> = {
  "": "Not enforced: any pod is admitted.",
  privileged: "Unrestricted: any pod is admitted.",
  baseline: "Blocks known privilege escalations (privileged, host namespaces, hostPath).",
  restricted: "Also requires non-root, no privilege escalation, dropped capabilities and a seccomp profile.",
}

/** A Namespace's labels, with the Pod Security admission level as a select. */
export function NamespaceForm({ node }: { node: KubeNode }) {
  const labels = node.labels ?? {}
  const write = (next: Record<string, string>) => {
    const p = labelsPatch(next)
    void patchObject(node, p.set, p.unset)
  }
  const level = labels[POD_SECURITY_LABEL] ?? ""
  return (
    <InspectorSection id="k8s:namespace" title="Namespace" icon={<ShieldCheckIcon className="size-3.5" />}>
      <KField path="metadata.labels" label="Pod Security (enforce)" htmlFor="k8s-ns-pod-security" display={level} hint={POD_SECURITY_HINT[level]}>
        <Select value={level || "none"} onValueChange={(v) => write(withPodSecurity(labels, v === "none" ? "" : v))}>
          <SelectTrigger id="k8s-ns-pod-security" size="sm" className="h-8 w-full text-xs" aria-label="Pod Security level" data-testid="k8s-ns-pod-security">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="none">Not enforced</SelectItem>
            {POD_SECURITY_LEVELS.map((l) => (
              <SelectItem key={l} value={l}>
                {l}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </KField>
      <span className="text-[11px] font-medium text-muted-foreground">Labels</span>
      <KList path="metadata.labels" display={Object.entries(labels).map(([k, v]) => `${k}=${v}`).join(", ")}>
        <PairsEditor pairs={toPairs(labels)} empty="No labels." placeholder="team" onChange={write} />
      </KList>
    </InspectorSection>
  )
}

const BUDGET_RE = /^\d+%?$/

/** A PodDisruptionBudget: how many pods may be evicted at once, and which pods. */
export function BudgetForm({ node }: { node: KubeNode }) {
  const d = node.disruption ?? {}
  const mode: BudgetMode = d.maxUnavailable !== undefined || d.minAvailable === undefined ? "maxUnavailable" : "minAvailable"
  const value = (mode === "maxUnavailable" ? d.maxUnavailable : d.minAvailable) ?? ""
  const write = (m: BudgetMode, v: string) => {
    const p = budgetPatch(m, v)
    void patchObject(node, p.set, p.unset)
  }
  const graph = useWorkflowStore((s) => s.workflow!.kubernetes?.graph)
  const selector = d.selector ?? {}
  const selected = (graph?.nodes ?? []).filter(
    (n) => n.workload && n.namespace === node.namespace && Object.keys(selector).length > 0 && Object.entries(selector).every(([k, v]) => n.workload?.podLabels?.[k] === v),
  )
  return (
    <div className="flex flex-col gap-5">
      <InspectorSection id="k8s:budget" title="Disruption budget" navLabel="Budget" icon={<ShieldCheckIcon className="size-3.5" />}>
        <div className="grid grid-cols-2 gap-2">
          <KField path={`spec.${mode}`} label="Rule" htmlFor="k8s-pdb-mode" display={mode}>
            <Select value={mode} onValueChange={(m) => value && write(m as BudgetMode, value)}>
              <SelectTrigger id="k8s-pdb-mode" size="sm" className="h-8 w-full text-xs" aria-label="Budget rule" data-testid="k8s-pdb-mode">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="maxUnavailable">Max unavailable</SelectItem>
                <SelectItem value="minAvailable">Min available</SelectItem>
              </SelectContent>
            </Select>
          </KField>
          <KField path={`spec.${mode}`} label="Pods" htmlFor="k8s-pdb-value" display={value}>
            <CommitInput id="k8s-pdb-value" className="font-mono" value={value} placeholder="1" validate={(v) => (BUDGET_RE.test(v) ? undefined : "A number or a %")} onCommit={(v) => write(mode, v)} />
          </KField>
        </div>
        <p className="text-[11px] text-muted-foreground">Max unavailable 1 lets node drains move pods one at a time; a minimum equal to the replicas blocks every eviction.</p>
      </InspectorSection>
      <InspectorSection id="k8s:selector" title="Selector" icon={<LinkIcon className="size-3.5" />}>
        <KList path="spec.selector.matchLabels" display={Object.entries(selector).map(([k, v]) => `${k}=${v}`).join(", ")}>
          <PairsEditor
            pairs={toPairs(selector)}
            empty="No selector: the budget protects no pods."
            placeholder="app"
            onChange={(m) => (Object.keys(m).length ? void patchObject(node, { "spec.selector.matchLabels": m }) : void patchObject(node, undefined, ["spec.selector.matchLabels"]))}
          />
        </KList>
        <p className="text-[11px] text-muted-foreground" data-testid="k8s-pdb-selected">
          {selected.length ? `Protects ${selected.map((n) => `${n.kind} ${n.name}`).join(", ")}.` : "Selects no workload: use the labels of a workload's pods."}
        </p>
      </InspectorSection>
    </div>
  )
}

/**
 * The keys of a ConfigMap (with their values) or a Secret (write-only values, written as plain text under
 * stringData: the API server stores them base64-encoded).
 */
export function DataForm({ node }: { node: KubeNode }) {
  const { fields } = useContext(KubeFieldsContext)!
  const secret = node.kind === "Secret"
  const entries = dataEntries(fields, node.kind)
  const write = (next: DataEntry[]) => {
    const p = dataPatch(node.kind, entries, next)
    if (Object.keys(p.set ?? {}).length || p.unset?.length) void patchObject(node, p.set, p.unset?.length ? p.unset : undefined)
  }
  return (
    <InspectorSection id="k8s:data" title={secret ? "Secret keys" : "Keys"} icon={<KeyRoundIcon className="size-3.5" />}>
      <KList path={secret ? "stringData" : "data"} display={(node.data ?? []).join(", ")}>
        <ListEditor<DataEntry>
          items={entries}
          empty="No keys."
          addLabel="Add key"
          blank={() => ({ key: "", value: "", from: secret ? "stringData" : "data" })}
          complete={(e) => DATA_KEY_RE.test(e.key)}
          onChange={write}
          render={(e, update) => (
            <>
              <CommitInput
                aria-label="Key"
                width="basis-36"
                className="font-mono"
                placeholder="KEY"
                value={e.key}
                validate={(v) => (v === "" || DATA_KEY_RE.test(v) ? (v !== e.key && entries.some((x) => x.key === v) ? "Already a key" : undefined) : "Letters, digits, - _ .")}
                onCommit={(key) => update({ ...e, key })}
              />
              {secret ? (
                <SecretInput label={`Value of ${e.key}`} value="" placeholder={entries.some((x) => x.key === e.key) ? "unchanged" : "value"} onCommit={(value) => update({ ...e, value })} />
              ) : (
                <CommitInput aria-label={`Value of ${e.key}`} className="font-mono" placeholder="value" value={e.value} onCommit={(value) => update({ ...e, value })} />
              )}
            </>
          )}
        />
      </KList>
      <p className="text-[11px] text-muted-foreground">
        {secret
          ? "Values are written to your files as plain text under stringData and never shown here: keep real credentials out of git."
          : "Not for credentials: keep passwords and tokens in a Secret."}
      </p>
    </InspectorSection>
  )
}

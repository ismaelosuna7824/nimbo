import { PlusIcon, RefreshCwIcon, ShieldCheckIcon, XIcon } from "lucide-react"
import { useContext, useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { CommitInput } from "@/features/docker/inspector/fields"
import { InspectorSubsection } from "@/features/inspector/inspector-section"
import type { KubeNode } from "@/lib/api/types"
import { patchObject } from "../actions"
import { KField, KubeFieldsContext } from "./kube-fields"
import { containerPath, fieldAccess, podSpecPath } from "./paths"
import {
  capAddPatch,
  fieldList,
  fieldValue,
  hardeningItems,
  INT_OR_PERCENT_RE,
  normalizeCapability,
  runAsUserPatch,
  spreadOf,
  spreadPatch,
  strategyOf,
  strategyPatch,
  strategyProblem,
  type ObjectPatch,
  type Spread,
  type Strategy,
} from "./workload-extras"

const apply = (node: KubeNode, p: ObjectPatch | undefined) => {
  if (p) void patchObject(node, p.set, p.unset?.length ? p.unset : undefined)
}

/** Deployment rollout: RollingUpdate (with its limits) or Recreate. A part of the workload's "Scaling & rollout". */
export function RolloutSection({ node }: { node: KubeNode }) {
  const { fields } = useContext(KubeFieldsContext)!
  const s = strategyOf(fields)
  const write = (next: Strategy) => {
    if (!strategyProblem(next)) apply(node, strategyPatch(next))
  }
  const limit = (key: "maxUnavailable" | "maxSurge", label: string, placeholder: string) => (
    <KField path={`spec.strategy.rollingUpdate.${key}`} label={label} htmlFor={`k8s-rollout-${key}`} display={s[key]}>
      <CommitInput
        id={`k8s-rollout-${key}`}
        className="font-mono"
        value={s[key]}
        placeholder={placeholder}
        validate={(v) => (!INT_OR_PERCENT_RE.test(v) ? "A number or a %" : strategyProblem({ ...s, [key]: v }))}
        onCommit={(v) => write({ ...s, [key]: v })}
      />
    </KField>
  )
  return (
    <InspectorSubsection title="Rollout" icon={<RefreshCwIcon className="size-3.5" />}>
      <KField path="spec.strategy.type" label="Strategy" display={s.type}>
        <Select value={s.type} onValueChange={(type) => write({ ...s, type: type as Strategy["type"] })}>
          <SelectTrigger size="sm" className="h-8 w-full text-xs" aria-label="Rollout strategy" data-testid="k8s-rollout-strategy">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="RollingUpdate">Rolling update (replace pods gradually)</SelectItem>
            <SelectItem value="Recreate">Recreate (stop every pod first)</SelectItem>
          </SelectContent>
        </Select>
      </KField>
      {s.type === "RollingUpdate" ? (
        <>
          <div className="grid grid-cols-2 gap-2">
            {limit("maxUnavailable", "Max unavailable", "25%")}
            {limit("maxSurge", "Max surge", "25%")}
          </div>
          <p className="text-[11px] text-muted-foreground">Max unavailable 0 with max surge 1 starts each new pod before stopping an old one: no capacity is lost.</p>
        </>
      ) : (
        <p className="text-[11px] text-muted-foreground">Every rollout stops all pods before starting new ones: only needed for a volume one pod at a time can use.</p>
      )}
    </InspectorSubsection>
  )
}

const SPREAD_LABEL: Record<Spread, string> = { off: "Off", nodes: "Across nodes", zones: "Across zones", custom: "Custom (edit in code)" }

/** The topologySpreadConstraints preset of a Deployment or StatefulSet. */
export function SpreadField({ node }: { node: KubeNode }) {
  const { fields } = useContext(KubeFieldsContext)!
  const at = podSpecPath(node.kind)
  const labels = node.workload?.podLabels ?? {}
  const spread = spreadOf(fields, at, labels)
  return (
    <KField path={`${at}.topologySpreadConstraints`} label="Spread replicas" htmlFor="k8s-spread" display={SPREAD_LABEL[spread]} hint="Keeps replicas on different nodes or zones, so one failure doesn't take them all.">
      <Select value={spread} disabled={spread === "custom" || Object.keys(labels).length === 0} onValueChange={(v) => apply(node, spreadPatch(at, v as Exclude<Spread, "custom">, labels))}>
        <SelectTrigger id="k8s-spread" size="sm" className="h-8 w-full text-xs" aria-label="Spread replicas" data-testid="k8s-spread">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {(spread === "custom" ? (["custom"] as Spread[]) : []).concat(["off", "nodes", "zones"]).map((s) => (
            <SelectItem key={s} value={s}>
              {SPREAD_LABEL[s]}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </KField>
  )
}

/**
 * Least privilege for the pods: non-root user and RuntimeDefault seccomp (pod), then per container no privileged mode,
 * no privilege escalation, a read-only root filesystem and no Linux capabilities (the Pod Security "restricted"
 * profile; findings k8s_run_as_root, k8s_privileged, k8s_hardening). A part of the workload's "Security".
 */
export function HardeningSection({ node }: { node: KubeNode }) {
  const { fields } = useContext(KubeFieldsContext)!
  const at = podSpecPath(node.kind)
  const containers = node.workload?.containers ?? []
  const multi = containers.length > 1
  const perContainer = containers.map((c) => ({ name: c.name, path: containerPath(node.kind, c.name), items: hardeningItems(fields, at, containerPath(node.kind, c.name)) }))
  const podItems = perContainer[0]?.items.filter((i) => i.scope === "pod") ?? []
  const all = [...podItems, ...perContainer.flatMap((c) => c.items.filter((i) => i.scope === "container"))]
  const passed = all.filter((i) => i.ok).length
  const user = fieldValue(fields, `${at}.securityContext.runAsUser`)
  const editable = (path: string) => fieldAccess(fields, path, node)
  if (all.length === 0) return null
  return (
    <InspectorSubsection
      title="Hardening"
      icon={<ShieldCheckIcon className="size-3.5 text-emerald-600 dark:text-emerald-400" />}
      className="gap-2.5"
      data-testid="k8s-hardening"
      action={
        <Badge variant={passed === all.length ? "secondary" : "outline"} data-testid="k8s-hardening-score">
          {passed}/{all.length}
        </Badge>
      }
    >
      <div className="flex items-center gap-2">
        <Progress value={(passed / all.length) * 100} className="h-1.5 flex-1" aria-label="Hardening score" />
        <span className="text-[11px] text-muted-foreground">{passed === all.length ? "Restricted: every protection is on." : `${all.length - passed} protection${all.length - passed === 1 ? "" : "s"} off`}</span>
      </div>
      {podItems.map((item) => (
        <Toggle key={item.id} id={item.id} label={item.label} checked={item.ok} access={editable(`${at}.securityContext`)} onChange={(on) => apply(node, item.patch(on))} hint={HINT[item.id]}>
          {item.id === "non-root" && (
            <KField path={`${at}.securityContext.runAsUser`} label="User ID" htmlFor="k8s-run-as-user" display={user === undefined ? "" : String(user)}>
              <CommitInput
                id="k8s-run-as-user"
                className="h-7 w-32 font-mono text-xs"
                inputMode="numeric"
                placeholder="image default"
                value={user === undefined || user === null ? "" : String(user)}
                validate={(v) => (/^$|^\d+$/.test(v) ? (v === "0" ? "0 is root" : undefined) : "A user ID")}
                onCommit={(v) => apply(node, runAsUserPatch(at, v))}
              />
            </KField>
          )}
        </Toggle>
      ))}
      {perContainer.map((c) => (
        <div key={c.name} className="flex flex-col gap-2.5" data-testid={`k8s-hardening-container-${c.name}`}>
          {multi && <span className="text-[11px] font-medium text-muted-foreground">Container {c.name}</span>}
          {c.items
            .filter((i) => i.scope === "container")
            .filter((i) => i.id !== "not-privileged" || !i.ok)
            .map((item) => (
              <Toggle
                key={item.id}
                id={`${item.id}-${c.name}`}
                label={item.label}
                checked={item.ok}
                access={editable(`${c.path}.securityContext`)}
                onChange={(on) => apply(node, item.patch(on))}
                hint={HINT[item.id]}
              >
                {item.id === "drop-all" && <CapabilityChips path={c.path} added={fieldList(fields, `${c.path}.securityContext.capabilities.add`)} node={node} />}
              </Toggle>
            ))}
        </div>
      ))}
    </InspectorSubsection>
  )
}

const HINT: Record<string, React.ReactNode> = {
  "non-root": (
    <>
      <code className="font-mono">runAsNonRoot: true</code>: the kubelet refuses to start a container as root. The image must run as another user (e.g. an
      unprivileged variant such as nginx-unprivileged), or set a user ID.
    </>
  ),
  seccomp: (
    <>
      <code className="font-mono">seccompProfile: RuntimeDefault</code> blocks the system calls containers never need.
    </>
  ),
  "not-privileged": <>Privileged mode gives the container every capability and the node's devices. Turn it off and add back only the capability it needs.</>,
  "no-escalation": (
    <>
      <code className="font-mono">allowPrivilegeEscalation: false</code>: a process can't gain more privileges than its parent (setuid binaries).
    </>
  ),
  "read-only": <>The app can't modify its own image; mount an emptyDir volume where it writes (e.g. /tmp).</>,
  "drop-all": (
    <>
      <code className="font-mono">capabilities.drop: [ALL]</code>; add back only what it needs (NET_BIND_SERVICE to listen below port 1024).
    </>
  ),
}

function Toggle({
  id,
  label,
  checked,
  access,
  hint,
  onChange,
  children,
}: {
  id: string
  label: string
  checked: boolean
  access: { editable: boolean; reason?: string }
  hint: React.ReactNode
  onChange: (on: boolean) => void
  children?: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-start gap-2">
        <Switch id={`k8s-hardening-${id}`} size="sm" className="mt-0.5" checked={checked} disabled={!access.editable} onCheckedChange={onChange} data-testid={`k8s-hardening-${id}`} />
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <label htmlFor={`k8s-hardening-${id}`} className="text-xs font-medium">
            {label}
          </label>
          <span className="text-[11px] leading-snug text-muted-foreground">{access.editable ? hint : (access.reason ?? "Read-only here")}</span>
        </div>
      </div>
      {children && <div className="pl-8">{children}</div>}
    </div>
  )
}

/** Capabilities added back to a container (securityContext.capabilities.add). */
function CapabilityChips({ node, path, added }: { node: KubeNode; path: string; added: string[] }) {
  const [text, setText] = useState("")
  const value = normalizeCapability(text)
  const error = value && !/^[A-Z_]+$/.test(value) ? "A capability name, e.g. NET_BIND_SERVICE" : undefined
  const write = (caps: string[]) => apply(node, capAddPatch(path, caps))
  const add = () => {
    if (!value || error) return
    if (!added.includes(value)) write([...added, value])
    setText("")
  }
  return (
    <div className="flex flex-wrap items-center gap-1" data-testid={`k8s-capabilities-add-${node.name}`}>
      <span className="font-mono text-[10px] text-muted-foreground">add:</span>
      {added.map((cap) => (
        <Badge key={cap} variant="secondary" className="gap-0.5 pr-0.5 font-mono text-[10px]">
          {cap}
          <button
            type="button"
            className="flex size-3.5 items-center justify-center rounded-full hover:bg-foreground/10"
            aria-label={`Remove ${cap}`}
            onClick={() => write(added.filter((c) => c !== cap))}
          >
            <XIcon className="size-2.5" />
          </button>
        </Badge>
      ))}
      <Input
        aria-label="Add a capability"
        aria-invalid={!!error}
        title={error}
        className="h-6 w-36 font-mono text-[11px]"
        placeholder="NET_BIND_SERVICE"
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") add()
        }}
      />
      <Button variant="ghost" size="icon-xs" aria-label="Add a capability" disabled={!value || !!error} onClick={add}>
        <PlusIcon />
      </Button>
    </div>
  )
}

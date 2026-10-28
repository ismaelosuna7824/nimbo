import { FileLockIcon, PlusIcon, ShieldCheckIcon, XIcon } from "lucide-react"
import { useState, type ReactNode } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Switch } from "@/components/ui/switch"
import { DiagnosticRow } from "@/features/panels/components/diagnostic-row"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import type { ComposeNode, ServicePatch } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { patchService } from "../actions"
import { InspectorSection } from "@/features/inspector/inspector-section"
import { CommitInput } from "./fields"
import { withScope } from "./service-patch"
import {
  capDropPatch,
  dropsAllCapabilities,
  hardeningItems,
  hasNoNewPrivileges,
  isRootUser,
  mountsDockerSocket,
  noNewPrivilegesPatch,
  nonRootUserFor,
  normalizeCapability,
  readOnlyPatch,
  SECRET_NAME_RE,
  secretCandidates,
  withItem,
  withoutItem,
  type SecretCandidate,
} from "./hardening"

/** Findings the toggles of this section fix (backend/internal/compose rules). */
const FINDINGS = ["dk_root_user", "dk_hardening", "dk_privileged", "dk_docker_sock", "dk_no_init", "dk_secret_env"]

/**
 * The "Security" section of a service: least privilege for its container — non-root user, no capabilities, read-only
 * filesystem, no privilege escalation, an init process, no privileged mode or Docker socket, and secrets mounted as
 * files instead of environment variables. The score stays on the heading so it reads even while collapsed.
 */
export function HardeningSection({
  node,
  isSecretKey,
  mark,
  scoped = false,
}: {
  node: ComposeNode
  /** Whether an environment key looks like a credential (the form's SECRET_KEY). */
  isSecretKey: (key: string) => boolean
  /** Override badge of compose attributes (see OverrideMark). */
  mark: (...attrs: string[]) => ReactNode
  /** Toggles write to the active environment's override file only (moving a secret always edits the base files). */
  scoped?: boolean
}) {
  const svc = node.service!
  const allDiagnostics = useWorkflowStore((s) => s.workflow!.diagnostics)
  const graph = useWorkflowStore((s) => s.workflow!.compose?.graph)
  const findings = allDiagnostics.filter((d) => d.nodeId === node.id && FINDINGS.includes(d.code))
  const items = hardeningItems(svc)
  const passed = items.filter((i) => i.ok).length
  const patch = (p: ServicePatch) => void patchService(node.name, withScope(p, scoped))
  const target = nonRootUserFor(svc)
  const image = svc.build ? svc.baseImages.at(-1) : svc.image?.ref
  const declaredSecrets = (graph?.nodes ?? []).filter((n) => n.kind === "secret" || (n.kind === "external" && n.externalKind === "secret")).map((n) => n.name)
  const candidates = secretCandidates(svc, isSecretKey)

  return (
    <InspectorSection
      id="docker:security"
      title="Security"
      icon={<ShieldCheckIcon className="size-3.5 text-emerald-600 dark:text-emerald-400" />}
      action={
        <Badge variant={passed === items.length ? "secondary" : "outline"} data-testid="docker-hardening-score">
          {passed}/{items.length}
        </Badge>
      }
      data-testid="docker-hardening"
    >
      <div className="flex items-center gap-2">
        <Progress value={(passed / items.length) * 100} className="h-1.5 flex-1" aria-label="Hardening score" />
        <span className="text-[11px] text-muted-foreground">
          {passed === items.length ? "Least privilege: every protection is on." : `${items.length - passed} protection${items.length - passed === 1 ? "" : "s"} off`}
        </span>
      </div>

      <Toggle
        id="nonroot"
        label="Run as non-root"
        checked={!isRootUser(svc.user)}
        disabled={svc.userFrom === "dockerfile" && !isRootUser(svc.user)}
        badge={mark("user")}
        onChange={(on) => patch({ user: on ? target.user : null })}
        hint={
          svc.userFrom === "dockerfile" && !isRootUser(svc.user) ? (
            <>
              The Dockerfile already switches to <code className="font-mono">USER {svc.user}</code>.
            </>
          ) : !isRootUser(svc.user) ? (
            <>
              Runs as <code className="font-mono">user: {svc.user}</code>.
            </>
          ) : target.documented ? (
            <>
              Sets <code className="font-mono">user: {target.user}</code>, the unprivileged user {image ? <span className="font-mono">{image}</span> : "the image"} documents.
            </>
          ) : (
            <>
              Sets <code className="font-mono">user: "{target.user}"</code>: this image documents no unprivileged user, so make sure the app can read its
              files and listens on a port above 1024 (or add a USER to its Dockerfile).
            </>
          )
        }
      >
        {svc.userFrom === "compose" && !isRootUser(svc.user) && (
          <CommitInput
            aria-label="User"
            className="h-7 w-40 font-mono text-xs"
            value={svc.user ?? ""}
            validate={(v) => (v === "" || !isRootUser(v) ? undefined : "Not root: a user name or uid[:gid]")}
            onCommit={(user) => patch({ user: user || null })}
          />
        )}
      </Toggle>

      <Toggle
        id="capdrop"
        label="Drop all capabilities"
        checked={dropsAllCapabilities(svc)}
        badge={mark("cap_drop", "cap_add")}
        onChange={(on) => patch(capDropPatch(svc, on))}
        hint={
          <>
            <code className="font-mono">cap_drop: [ALL]</code>; add back only what the app needs (NET_BIND_SERVICE to listen below port 1024).
          </>
        }
      >
        {dropsAllCapabilities(svc) && (
          <Chips
            testId="hardening-cap-add"
            label="cap_add"
            items={svc.capAdd ?? []}
            placeholder="NET_BIND_SERVICE"
            normalize={normalizeCapability}
            onChange={(capAdd) => patch({ capAdd: capAdd.length ? capAdd : null })}
          />
        )}
      </Toggle>

      <Toggle
        id="readonly"
        label="Read-only filesystem"
        checked={!!svc.readOnly}
        badge={mark("read_only", "tmpfs")}
        onChange={(on) => patch(readOnlyPatch(svc, on))}
        hint={
          <>
            <code className="font-mono">read_only: true</code>; the paths the app writes get a <code className="font-mono">tmpfs</code> (in memory, empty on
            restart).
          </>
        }
      >
        {(svc.readOnly || (svc.tmpfs ?? []).length > 0) && (
          <Chips
            testId="hardening-tmpfs"
            label="tmpfs"
            items={svc.tmpfs ?? []}
            placeholder="/tmp"
            validate={(v) => (v.startsWith("/") ? undefined : "An absolute path in the container")}
            onChange={(tmpfs) => patch({ tmpfs: tmpfs.length ? tmpfs : null })}
          />
        )}
      </Toggle>

      <Toggle
        id="nonewprivs"
        label="No new privileges"
        checked={hasNoNewPrivileges(svc)}
        badge={mark("security_opt")}
        onChange={(on) => patch(noNewPrivilegesPatch(svc, on))}
        hint={
          <>
            <code className="font-mono">security_opt: [no-new-privileges:true]</code>: setuid programs can't regain root.
          </>
        }
      />

      <Toggle
        id="init"
        label="Init process"
        checked={!!svc.init}
        badge={mark("init")}
        onChange={(on) => patch({ init: on })}
        hint={
          <>
            <code className="font-mono">init: true</code>: a tiny init forwards stop signals and reaps zombie processes.
          </>
        }
      />

      <Toggle
        id="notprivileged"
        label="Not privileged"
        checked={!svc.privileged}
        badge={mark("privileged")}
        onChange={(on) => patch({ privileged: !on })}
        hint={
          svc.privileged ? (
            <>
              <code className="font-mono">privileged: true</code> gives the container every device and capability of the host. Turning this on removes it.
            </>
          ) : (
            "No privileged mode."
          )
        }
      />

      {mountsDockerSocket(svc) && (
        <div className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-[11px]" data-testid="hardening-docker-sock">
          <span className="flex-1">The Docker socket is mounted: whoever controls this container controls Docker, as root on the host.</span>
          <Button
            variant="outline"
            size="xs"
            onClick={() => patch({ volumes: svc.volumes.filter((v) => !(v.type === "bind" && (v.source ?? "").endsWith("docker.sock"))) })}
          >
            Remove the mount
          </Button>
        </div>
      )}

      <div className="flex flex-col gap-1.5 pt-1" data-testid="hardening-secrets">
        <div className="flex items-center gap-1.5 text-xs font-medium">
          <FileLockIcon className="size-3.5" />
          Secrets as files
          {mark("secrets")}
        </div>
        {(svc.secrets ?? []).length > 0 && (
          <div className="flex flex-wrap gap-1">
            {(svc.secrets ?? []).map((s) => (
              <Badge key={s} variant="outline" className="font-mono text-[10px]">
                {s} → /run/secrets/{s}
              </Badge>
            ))}
          </div>
        )}
        {candidates.length === 0 ? (
          <p className="text-[11px] text-muted-foreground">No password, token or key written in the service's environment.</p>
        ) : (
          candidates.map((c) => <SecretMove key={c.key} service={node.name} candidate={c} declared={declaredSecrets} image={image} />)
        )}
      </div>

      {findings.length > 0 && (
        <div className="flex flex-col gap-1" data-testid="hardening-findings">
          <span className="text-[11px] font-medium text-muted-foreground">Related findings</span>
          <div className="-mx-1 flex flex-col rounded-md border">
            {findings.map((d, i) => (
              <DiagnosticRow key={i} diagnostic={d} onSelect={d.file ? () => revealFiles("", d.file, d.line) : undefined} />
            ))}
          </div>
        </div>
      )}
    </InspectorSection>
  )
}

function Toggle({
  id,
  label,
  checked,
  disabled,
  badge,
  hint,
  onChange,
  children,
}: {
  id: string
  label: string
  checked: boolean
  disabled?: boolean
  badge?: ReactNode
  hint: ReactNode
  onChange: (on: boolean) => void
  children?: ReactNode
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-start gap-2">
        <Switch id={`hardening-${id}`} size="sm" className="mt-0.5" checked={checked} disabled={disabled} onCheckedChange={onChange} data-testid={`hardening-${id}`} />
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <label htmlFor={`hardening-${id}`} className="flex items-center gap-1.5 text-xs font-medium">
            {label}
            {badge}
          </label>
          <span className="text-[11px] leading-snug text-muted-foreground">{hint}</span>
        </div>
      </div>
      {children && <div className="pl-8">{children}</div>}
    </div>
  )
}

/** Removable chips with an add field (capabilities, tmpfs paths). */
function Chips({
  testId,
  label,
  items,
  placeholder,
  normalize = (v) => v.trim(),
  validate,
  onChange,
}: {
  testId: string
  label: string
  items: string[]
  placeholder: string
  normalize?: (v: string) => string
  validate?: (v: string) => string | undefined
  onChange: (items: string[]) => void
}) {
  const [text, setText] = useState("")
  const value = normalize(text)
  const error = value && validate ? validate(value) : undefined
  const add = () => {
    if (!value || error) return
    onChange(withItem(items, value))
    setText("")
  }
  return (
    <div className="flex flex-wrap items-center gap-1" data-testid={testId}>
      <span className="font-mono text-[10px] text-muted-foreground">{label}:</span>
      {items.map((it) => (
        <Badge key={it} variant="secondary" className="gap-0.5 pr-0.5 font-mono text-[10px]">
          {it}
          <button
            type="button"
            className="flex size-3.5 items-center justify-center rounded-full hover:bg-foreground/10"
            aria-label={`Remove ${it}`}
            onClick={() => onChange(withoutItem(items, it) ?? [])}
          >
            <XIcon className="size-2.5" />
          </button>
        </Badge>
      ))}
      <Input
        aria-label={`Add to ${label}`}
        aria-invalid={!!error}
        title={error}
        className="h-6 w-32 font-mono text-[11px]"
        placeholder={placeholder}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") add()
        }}
      />
      <Button variant="ghost" size="icon-xs" aria-label={`Add to ${label}`} disabled={!value || !!error} onClick={add}>
        <PlusIcon />
      </Button>
    </div>
  )
}

/** "Move to a secret file" for one environment variable. */
function SecretMove({ service, candidate: c, declared, image }: { service: string; candidate: SecretCandidate; declared: string[]; image?: string }) {
  const [secret, setSecret] = useState(c.secret)
  const [fileEnv, setFileEnv] = useState(c.imageReadsFile)
  const [busy, setBusy] = useState(false)
  const valid = SECRET_NAME_RE.test(secret)
  const reuse = declared.includes(secret)
  const move = async () => {
    setBusy(true)
    await patchService(service, { secretFromEnv: { key: c.key, secret, fileEnv } })
    setBusy(false)
  }
  return (
    <div className="flex flex-col gap-1.5 rounded-md border p-2" data-testid={`hardening-secret-${c.key}`}>
      <div className="flex flex-wrap items-center gap-1.5">
        <code className="font-mono text-xs font-medium">{c.key}</code>
        <span className="text-[11px] text-muted-foreground">→ secret</span>
        <Input
          aria-label={`Secret name for ${c.key}`}
          aria-invalid={!valid}
          className="h-7 w-40 font-mono text-xs"
          list={`secret-names-${service}`}
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
        />
        <datalist id={`secret-names-${service}`}>
          {declared.map((d) => (
            <option key={d} value={d} />
          ))}
        </datalist>
      </div>
      <p className="text-[11px] leading-snug text-muted-foreground">
        {reuse ? (
          <>
            Uses the declared secret <code className="font-mono">{secret}</code> (its file is kept), mounts it at{" "}
            <code className="font-mono">/run/secrets/{secret}</code> and removes {c.key} from the environment.
          </>
        ) : (
          <>
            Writes the value to <code className="font-mono">./secrets/{secret || "…"}.txt</code> (keep it out of git), declares the secret, mounts it at{" "}
            <code className="font-mono">/run/secrets/{secret || "…"}</code> and removes {c.key} from the environment.
          </>
        )}
      </p>
      <label className="flex items-start gap-1.5 text-[11px]">
        <Checkbox className="mt-0.5" checked={fileEnv} onCheckedChange={(v) => setFileEnv(v === true)} data-testid={`hardening-secret-fileenv-${c.key}`} />
        <span className={cn(!c.imageReadsFile && "text-muted-foreground")}>
          Set <code className="font-mono">{c.key}_FILE=/run/secrets/{secret || "…"}</code>
          {c.imageReadsFile ? (
            <> — {image ? <span className="font-mono">{image}</span> : "the image"} reads it (documented).</>
          ) : (
            <> — only if your app reads {c.key}_FILE: this image isn't known to (Docker Official Images such as postgres, mysql and mongo do).</>
          )}
        </span>
      </label>
      <div>
        <Button size="xs" variant="outline" disabled={!valid || busy} onClick={() => void move()} data-testid={`hardening-secret-move-${c.key}`}>
          <FileLockIcon />
          Move to a secret file
        </Button>
      </div>
    </div>
  )
}

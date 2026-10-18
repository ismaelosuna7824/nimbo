import { useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { CircleDollarSign, KeyRound, Plus, Save, Trash2, Undo2, Unplug, Zap } from "lucide-react"
import { toast } from "sonner"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { Connection, ConnectionCheck, ConnectionKind, ConnectionSecret, HTTPAuth } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { CheckResult } from "./check-status"
import { AWS_REGIONS, CONNECTION_KINDS, isDatabase, KIND_META } from "./meta"
import { useConnectionsStore } from "./store"

interface Form {
  kind: ConnectionKind | null
  name: string
  description: string
  baseUrl: string
  headers: { name: string; value: string }[]
  authType: HTTPAuth["type"]
  token: string
  authUsername: string
  authPassword: string
  authHeader: string
  insecureSkipVerify: boolean
  dbMode: "uri" | "fields"
  uri: string
  host: string
  port: string
  username: string
  password: string
  database: string
  tls: boolean
  profile: string
  region: string
  /** Stored secrets the user asked to remove. */
  clear: ConnectionSecret[]
}

function toForm(c: Connection | undefined): Form {
  const stored = c?.secretsSet ?? []
  return {
    kind: c?.kind ?? null,
    name: c?.name ?? "",
    description: c?.description ?? "",
    baseUrl: c?.baseUrl ?? "",
    headers: Object.entries(c?.headers ?? {}).map(([name, value]) => ({ name, value })),
    authType: c?.auth?.type ?? "none",
    token: "",
    authUsername: c?.auth?.username ?? "",
    authPassword: "",
    authHeader: c?.auth?.header ?? "",
    insecureSkipVerify: c?.insecureSkipVerify ?? false,
    dbMode: stored.includes("uri") && !c?.host ? "uri" : "fields",
    uri: "",
    host: c?.host ?? "",
    port: c?.port ? String(c.port) : c ? String(KIND_META[c.kind].defaultPort ?? "") : "",
    username: c?.username ?? "",
    password: "",
    database: c?.database ?? "",
    tls: c?.tls ?? false,
    profile: c?.profile ?? "",
    region: c?.region ?? "",
    clear: [],
  }
}

/** Request body for create/update/test. Blank secrets keep stored ones; secrets the form no longer uses are cleared. */
function toPayload(f: Form, base: Connection | undefined): Partial<Connection> {
  const kind = f.kind!
  const stored = base?.secretsSet ?? []
  const clear = new Set<ConnectionSecret>(f.clear)
  const out: Partial<Connection> = {
    ...(base ? { id: base.id, createdAt: base.createdAt, updatedAt: base.updatedAt } : {}),
    name: f.name.trim(),
    kind,
    description: f.description.trim() || undefined,
  }
  if (kind === "http") {
    const headers = Object.fromEntries(f.headers.filter((h) => h.name.trim()).map((h) => [h.name.trim(), h.value]))
    out.baseUrl = f.baseUrl.trim()
    out.headers = Object.keys(headers).length ? headers : undefined
    out.insecureSkipVerify = f.insecureSkipVerify || undefined
    if (f.authType === "bearer") out.auth = { type: "bearer", token: f.token }
    else if (f.authType === "api_key") out.auth = { type: "api_key", token: f.token, header: f.authHeader.trim() || undefined }
    else if (f.authType === "basic") out.auth = { type: "basic", username: f.authUsername, password: f.authPassword }
    else out.auth = { type: "none" }
    if (f.authType !== "bearer" && f.authType !== "api_key") clear.add("auth.token")
    if (f.authType !== "basic") clear.add("auth.password")
  } else if (isDatabase(kind)) {
    if (f.dbMode === "uri") {
      out.uri = f.uri.trim()
      clear.add("password")
    } else {
      out.host = f.host.trim()
      out.port = Number(f.port) || undefined
      out.username = f.username.trim() || undefined
      out.password = f.password
      out.database = f.database.trim() || undefined
      out.tls = f.tls || undefined
      clear.add("uri")
    }
  } else {
    out.profile = f.profile.trim() || undefined
    out.region = f.region || undefined
  }
  // Only clear what is stored and not being replaced in this request.
  const replaced: Record<ConnectionSecret, string | undefined> = {
    password: out.password,
    uri: out.uri,
    "auth.token": out.auth?.token,
    "auth.password": out.auth?.password,
  }
  const clearSecrets = [...clear].filter((s) => stored.includes(s) && !replaced[s])
  if (clearSecrets.length) out.clearSecrets = clearSecrets
  return out
}

function validate(f: Form): string | null {
  if (!f.kind) return "Choose what kind of service this is."
  if (!f.name.trim()) return "Give the connection a name."
  if (f.kind === "http" && !/^https?:\/\/\S+$/i.test(f.baseUrl.trim())) return "Enter a base URL starting with http:// or https://."
  if (f.kind === "aws" && !f.region) return "Choose an AWS region."
  return null
}

const DB_HOST_PLACEHOLDER: Partial<Record<ConnectionKind, string>> = {
  postgres: "postgres://user:password@db.example.com:5432/app?sslmode=require",
  mysql: "user:password@tcp(db.example.com:3306)/app?tls=true",
  redis: "rediss://default:password@cache.example.com:6379/0",
  mongodb: "mongodb+srv://user:password@cluster0.example.mongodb.net/app",
}

/**
 * Create/edit form for a connection: kind cards, per-kind fields, protected
 * secrets, "Test connection" on the unsaved draft, save and delete.
 */
export function ConnectionEditor({ connection, onSaved }: { connection?: Connection; onSaved?: (c: Connection) => void }) {
  const queryClient = useQueryClient()
  const recordCheck = useConnectionsStore((s) => s.recordCheck)
  const forget = useConnectionsStore((s) => s.forget)
  const [initial] = useState(() => toForm(connection))
  const [form, setForm] = useState(initial)
  const [check, setCheck] = useState<ConnectionCheck | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const set = (patch: Partial<Form>) => {
    setForm((f) => ({ ...f, ...patch }))
    setCheck(null)
    setError(null)
  }
  const dirty = JSON.stringify(form) !== JSON.stringify(initial)
  const stored = connection?.secretsSet ?? []

  const test = useMutation({
    mutationFn: () => api.testConnectionDraft(toPayload(form, connection)),
    onSuccess: (result) => {
      setCheck(result)
      if (connection && !dirty) recordCheck(connection.id, result)
    },
    onError: (err) => setCheck({ ok: false, latencyMs: 0, message: "The test could not run", detail: errorMessage(err) }),
  })
  const save = useMutation({
    mutationFn: () => {
      const payload = toPayload(form, connection)
      return connection ? api.updateConnection(payload as Connection) : api.createConnection(payload)
    },
    onSuccess: (saved) => {
      void queryClient.invalidateQueries({ queryKey: ["connections"] })
      if (check) recordCheck(saved.id, check)
      toast.success(connection ? `Saved “${saved.name}”` : `Created “${saved.name}”`)
      onSaved?.(saved)
    },
    onError: (err) => setError(errorMessage(err)),
  })
  const remove = useMutation({
    mutationFn: () => api.deleteConnection(connection!.id),
    onSuccess: () => {
      forget(connection!.id)
      void queryClient.invalidateQueries({ queryKey: ["connections"] })
      toast.success(`Deleted “${connection!.name}”`)
    },
    onError: (err) => toast.error("Couldn't delete", { description: errorMessage(err) }),
  })

  const submit = () => {
    const problem = validate(form)
    if (problem) setError(problem)
    else save.mutate()
  }

  if (!form.kind) {
    return (
      <div className="flex flex-col gap-3">
        <div>
          <h2 className="text-sm font-semibold">What do you want to connect to?</h2>
          <p className="text-xs text-muted-foreground">
            A connection points at a service that is already running somewhere — staging, production or another laptop. Save it
            once, then load-test it from here or from any workflow.
          </p>
        </div>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(13rem,1fr))] gap-2" role="radiogroup" aria-label="Connection kind">
          {CONNECTION_KINDS.map((k) => (
            <button
              key={k.kind}
              type="button"
              role="radio"
              aria-checked={false}
              onClick={() =>
                set({ kind: k.kind, port: k.defaultPort ? String(k.defaultPort) : "", name: form.name || `My ${k.label}` })
              }
              className="flex items-start gap-2.5 rounded-xl border bg-card p-3 text-left outline-none hover:border-primary/50 hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-ring/50"
            >
              <k.icon className="mt-0.5 size-5 shrink-0 text-primary" />
              <span>
                <span className="block text-sm font-medium">{k.label}</span>
                <span className="block text-xs text-muted-foreground">{k.description}</span>
              </span>
            </button>
          ))}
        </div>
      </div>
    )
  }

  const meta = KIND_META[form.kind]
  const secret = (name: ConnectionSecret) => ({
    stored: stored.includes(name),
    cleared: form.clear.includes(name),
    onClear: () => set({ clear: [...form.clear, name] }),
    onUndo: () => set({ clear: form.clear.filter((s) => s !== name) }),
  })

  return (
    <form
      className="flex max-w-2xl flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <div className="flex items-center gap-2">
        <meta.icon className="size-4 text-primary" />
        <span className="text-sm font-semibold">{meta.label}</span>
        <span className="truncate text-xs text-muted-foreground">{meta.description}</span>
        {!connection && (
          <Button type="button" size="xs" variant="ghost" className="ml-auto" onClick={() => set({ kind: null })}>
            Change kind
          </Button>
        )}
      </div>

      <Grid>
        <Field label="Name" htmlFor="conn-name">
          <Input id="conn-name" value={form.name} onChange={(e) => set({ name: e.target.value })} placeholder="Staging API" />
        </Field>
        <Field label="Description" htmlFor="conn-desc" optional>
          <Input id="conn-desc" value={form.description} onChange={(e) => set({ description: e.target.value })} placeholder="What is this?" />
        </Field>
      </Grid>

      {form.kind === "http" && (
        <>
          <Grid>
            <Field label="Base URL" htmlFor="conn-url" hint="Test paths are appended to it, e.g. /health.">
              <Input
                id="conn-url"
                className="font-mono"
                value={form.baseUrl}
                onChange={(e) => set({ baseUrl: e.target.value })}
                placeholder="https://api.example.com/v1"
              />
            </Field>
            <Field label="Authentication">
              <Select value={form.authType} onValueChange={(v) => set({ authType: v as HTTPAuth["type"] })}>
                <SelectTrigger className="w-full" aria-label="Authentication">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">None</SelectItem>
                  <SelectItem value="bearer">Bearer token</SelectItem>
                  <SelectItem value="basic">Basic (username + password)</SelectItem>
                  <SelectItem value="api_key">API key header</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            {form.authType === "bearer" && (
              <Field label="Token" htmlFor="conn-token">
                <SecretInput id="conn-token" value={form.token} onChange={(token) => set({ token })} {...secret("auth.token")} />
              </Field>
            )}
            {form.authType === "api_key" && (
              <>
                <Field label="Header name" htmlFor="conn-key-header">
                  <Input
                    id="conn-key-header"
                    className="font-mono"
                    value={form.authHeader}
                    onChange={(e) => set({ authHeader: e.target.value })}
                    placeholder="X-API-Key"
                  />
                </Field>
                <Field label="API key" htmlFor="conn-token">
                  <SecretInput id="conn-token" value={form.token} onChange={(token) => set({ token })} {...secret("auth.token")} />
                </Field>
              </>
            )}
            {form.authType === "basic" && (
              <>
                <Field label="Username" htmlFor="conn-basic-user">
                  <Input id="conn-basic-user" value={form.authUsername} onChange={(e) => set({ authUsername: e.target.value })} autoComplete="off" />
                </Field>
                <Field label="Password" htmlFor="conn-basic-pass">
                  <SecretInput id="conn-basic-pass" value={form.authPassword} onChange={(authPassword) => set({ authPassword })} {...secret("auth.password")} />
                </Field>
              </>
            )}
          </Grid>
          <div className="flex flex-col gap-1.5">
            <Label className="text-xs">Headers sent with every request</Label>
            {form.headers.map((h, i) => (
              <div key={i} className="flex gap-1.5">
                <Input
                  className="h-7 w-48 font-mono text-xs"
                  placeholder="Header-Name"
                  value={h.name}
                  onChange={(e) => set({ headers: form.headers.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)) })}
                  aria-label={`Header ${i + 1} name`}
                />
                <Input
                  className="h-7 font-mono text-xs"
                  placeholder="value"
                  value={h.value}
                  onChange={(e) => set({ headers: form.headers.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)) })}
                  aria-label={`Header ${i + 1} value`}
                />
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  aria-label={`Remove header ${i + 1}`}
                  onClick={() => set({ headers: form.headers.filter((_, j) => j !== i) })}
                >
                  <Trash2 />
                </Button>
              </div>
            ))}
            <Button type="button" size="xs" variant="ghost" className="self-start" onClick={() => set({ headers: [...form.headers, { name: "", value: "" }] })}>
              <Plus /> Header
            </Button>
          </div>
          <Toggle
            id="conn-insecure"
            label="Ignore TLS certificate errors"
            hint="Only for self-signed certificates on test servers."
            checked={form.insecureSkipVerify}
            onChange={(insecureSkipVerify) => set({ insecureSkipVerify })}
          />
        </>
      )}

      {isDatabase(form.kind) && (
        <>
          <ToggleGroup
            type="single"
            size="sm"
            variant="outline"
            value={form.dbMode}
            onValueChange={(v) => v && set({ dbMode: v as Form["dbMode"] })}
            className="self-start"
          >
            <ToggleGroupItem value="fields" className="text-xs">
              Fields
            </ToggleGroupItem>
            <ToggleGroupItem value="uri" className="text-xs">
              Connection string
            </ToggleGroupItem>
          </ToggleGroup>
          {form.dbMode === "uri" ? (
            <Field label="Connection string" htmlFor="conn-uri" hint="Stored only on this machine and never shown again.">
              <SecretInput id="conn-uri" value={form.uri} onChange={(uri) => set({ uri })} placeholder={DB_HOST_PLACEHOLDER[form.kind]} {...secret("uri")} />
            </Field>
          ) : (
            <>
              <Grid>
                <Field label="Host" htmlFor="conn-host">
                  <Input id="conn-host" className="font-mono" value={form.host} onChange={(e) => set({ host: e.target.value })} placeholder="db.example.com" />
                </Field>
                <Field label="Port" htmlFor="conn-port">
                  <Input
                    id="conn-port"
                    type="number"
                    min={1}
                    max={65535}
                    value={form.port}
                    onChange={(e) => set({ port: e.target.value })}
                    placeholder={String(meta.defaultPort ?? "")}
                  />
                </Field>
                <Field label="Username" htmlFor="conn-user" optional={form.kind === "redis"}>
                  <Input id="conn-user" value={form.username} onChange={(e) => set({ username: e.target.value })} autoComplete="off" />
                </Field>
                <Field label="Password" htmlFor="conn-pass">
                  <SecretInput id="conn-pass" value={form.password} onChange={(password) => set({ password })} {...secret("password")} />
                </Field>
                <Field
                  label={form.kind === "redis" ? "Database number" : "Database"}
                  htmlFor="conn-db"
                  optional={form.kind === "redis" || form.kind === "mongodb"}
                >
                  <Input
                    id="conn-db"
                    className="font-mono"
                    value={form.database}
                    onChange={(e) => set({ database: e.target.value })}
                    placeholder={form.kind === "redis" ? "0" : "app"}
                  />
                </Field>
              </Grid>
              <Toggle id="conn-tls" label="Use TLS" hint="Encrypt the connection (required by most managed databases)." checked={form.tls} onChange={(tls) => set({ tls })} />
            </>
          )}
        </>
      )}

      {form.kind === "aws" && (
        <>
          <Grid>
            <Field label="AWS CLI profile" htmlFor="conn-profile" hint="From ~/.aws/config; empty uses the default credential chain.">
              <Input id="conn-profile" className="font-mono" value={form.profile} onChange={(e) => set({ profile: e.target.value })} placeholder="default" />
            </Field>
            <Field label="Region">
              <Select value={form.region} onValueChange={(region) => set({ region })}>
                <SelectTrigger className="w-full font-mono" aria-label="Region">
                  <SelectValue placeholder="Choose a region" />
                </SelectTrigger>
                <SelectContent>
                  {AWS_REGIONS.map((r) => (
                    <SelectItem key={r} value={r} className="font-mono">
                      {r}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </Grid>
          <p className="flex gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-2.5 text-xs text-amber-800 dark:text-amber-300">
            <CircleDollarSign className="mt-0.5 size-3.5 shrink-0" />
            Uses your local AWS CLI profile — the same credentials as the aws command. Load tests on this connection generate real
            traffic in that account and may cost money. Nothing is sent until you start a test and confirm it.
          </p>
        </>
      )}

      {check && <CheckResult check={check} />}
      {error && <p className="text-xs text-destructive">{error}</p>}

      <div className="flex flex-wrap items-center gap-2 border-t pt-3">
        <Button type="button" variant="outline" disabled={test.isPending || !!validate(form)} onClick={() => test.mutate()}>
          {test.isPending ? <Spinner /> : <Zap />} Test connection
        </Button>
        <Button type="submit" disabled={save.isPending || (!!connection && !dirty)}>
          {save.isPending ? <Spinner /> : <Save />} {connection ? "Save changes" : "Save connection"}
        </Button>
        {connection && dirty && (
          <Button type="button" variant="ghost" onClick={() => set(initial)}>
            <Undo2 /> Discard changes
          </Button>
        )}
        {connection && (
          <Button type="button" variant="destructive" className="ml-auto" onClick={() => setConfirmDelete(true)}>
            <Unplug /> Delete
          </Button>
        )}
      </div>

      {connection && (
        <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete “{connection.name}”?</AlertDialogTitle>
              <AlertDialogDescription>
                The saved settings and secrets are removed from this machine, together with this connection’s load test history.
                The service itself is not touched. Workflow runs that used it keep their results.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction variant="destructive" onClick={() => remove.mutate()}>
                Delete
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </form>
  )
}

function Grid({ children }: { children: React.ReactNode }) {
  return <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">{children}</div>
}

function Field({
  label,
  htmlFor,
  hint,
  optional,
  children,
}: {
  label: string
  htmlFor?: string
  hint?: string
  optional?: boolean
  children: React.ReactNode
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <Label htmlFor={htmlFor} className="text-xs">
        {label}
        {optional && <span className="font-normal text-muted-foreground">(optional)</span>}
      </Label>
      {children}
      {hint && <p className="text-[11px] text-muted-foreground">{hint}</p>}
    </div>
  )
}

function Toggle({ id, label, hint, checked, onChange }: { id: string; label: string; hint: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="flex items-start gap-2.5">
      <Switch id={id} checked={checked} onCheckedChange={onChange} className="mt-0.5" />
      <Label htmlFor={id} className="flex flex-col items-start gap-0.5 text-xs">
        {label}
        <span className="font-normal text-muted-foreground">{hint}</span>
      </Label>
    </div>
  )
}

/** Password-style input for a secret the API never returns: blank keeps the stored value. */
function SecretInput({
  id,
  value,
  onChange,
  placeholder,
  stored,
  cleared,
  onClear,
  onUndo,
}: {
  id: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  stored: boolean
  cleared: boolean
  onClear: () => void
  onUndo: () => void
}) {
  if (stored && cleared) {
    return (
      <div className="flex h-8 items-center gap-2 rounded-lg border border-dashed px-2.5 text-xs text-muted-foreground">
        <KeyRound className="size-3.5" /> Removed when you save
        <Button type="button" size="xs" variant="ghost" className="ml-auto" onClick={onUndo}>
          Undo
        </Button>
      </div>
    )
  }
  return (
    <div className="flex gap-1.5">
      <Input
        id={id}
        type="password"
        autoComplete="new-password"
        className={cn("font-mono", stored && "placeholder:font-sans")}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={stored ? "Stored — leave blank to keep" : placeholder}
      />
      {stored && (
        <Button type="button" size="sm" variant="ghost" className="h-8" onClick={onClear} title="Remove the stored value">
          Clear
        </Button>
      )}
    </div>
  )
}

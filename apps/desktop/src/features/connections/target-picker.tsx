import { useEffect } from "react"
import { useQuery } from "@tanstack/react-query"
import { Plus, Sparkles, Trash2, TriangleAlert, Wrench } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { Connection, LoadTarget, MongoTarget } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { AWS_OPERATIONS, HTTP_METHODS, targetDetail, targetKey } from "../panels/loadtest/targets"
import { targetSideEffect } from "./safety"
import { TARGET_KIND, type ConnectionTargetDraft } from "./target-draft"

export type TargetDraftUpdate = (update: (d: ConnectionTargetDraft) => ConnectionTargetDraft) => void

const MONGO_OPS: MongoTarget["operation"][] = ["findOne", "find", "insertOne", "updateOne", "deleteOne", "runCommand"]

const PLACEHOLDER_HINT = "Templates: {{seq}} (1, 2, 3…), {{uuid}}, {{rand}} (random number), {{now}} (timestamp)."

/**
 * Step-1 content for a saved connection: its suggested targets (from the engine,
 * flagged when they write data) or a custom request per connection kind.
 */
export function ConnectionTargetPicker({ connection, value, onChange }: { connection: Connection; value: ConnectionTargetDraft; onChange: TargetDraftUpdate }) {
  const targets = useQuery({
    queryKey: ["connection-targets", connection.id, connection.updatedAt],
    queryFn: () => api.connectionTargets(connection.id),
    retry: false,
  })
  const loaded = targets.data

  // Bind the draft to this connection and preselect a read-only suggestion.
  useEffect(() => {
    if (value.connectionId !== connection.id) {
      onChange((d) => ({ ...d, connectionId: connection.id, suggested: null, mongo: { ...d.mongo, database: connection.database ?? "" } }))
      return
    }
    if (value.mode !== "suggested" || !loaded?.length) return
    if (value.suggested && loaded.some((t) => targetKey(t) === targetKey(value.suggested!))) return
    onChange((d) => ({ ...d, suggested: loaded.find((t) => !targetSideEffect(t)) ?? loaded[0] }))
  }, [connection.id, connection.database, value.connectionId, value.mode, value.suggested, loaded, onChange])

  const set = <K extends keyof ConnectionTargetDraft>(key: K, patch: Partial<ConnectionTargetDraft[K]>) =>
    onChange((d) => ({ ...d, [key]: { ...(d[key] as object), ...patch } }))
  const kind = TARGET_KIND[connection.kind]

  return (
    <div className="flex flex-col gap-2">
      <ToggleGroup
        type="single"
        size="sm"
        variant="outline"
        value={value.mode}
        onValueChange={(v) => v && onChange((d) => ({ ...d, mode: v as ConnectionTargetDraft["mode"] }))}
      >
        <ToggleGroupItem value="suggested" className="text-xs">
          <Sparkles /> Suggested
        </ToggleGroupItem>
        <ToggleGroupItem value="custom" className="text-xs">
          <Wrench /> Custom
        </ToggleGroupItem>
      </ToggleGroup>

      {value.mode === "suggested" &&
        (targets.isPending ? (
          <Spinner />
        ) : targets.isError ? (
          <p className="rounded-lg border border-destructive/40 bg-destructive/5 p-2 text-xs text-destructive">
            Couldn’t list targets: {errorMessage(targets.error)}. Use Custom, or check the connection in Settings.
          </p>
        ) : !loaded?.length ? (
          <p className="rounded-lg border border-dashed p-2.5 text-xs text-muted-foreground">
            No suggestions for this connection. Describe the request under Custom.
          </p>
        ) : (
          <div className="flex flex-col gap-1" role="radiogroup" aria-label="Suggested targets">
            {loaded.map((t) => (
              <SuggestedRow
                key={targetKey(t)}
                target={t}
                selected={!!value.suggested && targetKey(value.suggested) === targetKey(t)}
                onSelect={() => onChange((d) => ({ ...d, suggested: t }))}
              />
            ))}
          </div>
        ))}

      {value.mode === "custom" && kind === "http" && (
        <div className="flex flex-col gap-2">
          <div className="flex gap-1">
            <Select value={value.http.method} onValueChange={(method) => set("http", { method })}>
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
              placeholder="/path?query — or a full https:// URL"
              value={value.http.url}
              onChange={(e) => set("http", { url: e.target.value })}
              aria-label="Path or URL"
            />
          </div>
          <p className="text-[11px] text-muted-foreground">
            Paths are appended to <span className="font-mono">{connection.baseUrl || "the base URL"}</span>; empty means the base URL
            itself. The connection’s headers and auth are sent too.
          </p>
          <Label className="text-[11px] text-muted-foreground">Extra headers (one “Name: value” per line)</Label>
          <Textarea rows={2} className="min-h-0 font-mono text-xs" value={value.http.headersText} onChange={(e) => set("http", { headersText: e.target.value })} />
          {value.http.method !== "GET" && value.http.method !== "HEAD" && (
            <>
              <Label className="text-[11px] text-muted-foreground">Body</Label>
              <Textarea
                rows={3}
                className="min-h-0 font-mono text-xs"
                placeholder='{"id": "{{uuid}}"}'
                value={value.http.body}
                onChange={(e) => set("http", { body: e.target.value })}
              />
            </>
          )}
        </div>
      )}

      {value.mode === "custom" && kind === "sql" && (
        <div className="flex flex-col gap-2">
          <Label className="text-[11px]">Query</Label>
          <Textarea
            rows={3}
            className="min-h-0 font-mono text-xs"
            placeholder={connection.kind === "postgres" ? "SELECT * FROM users WHERE id = $1" : "SELECT * FROM users WHERE id = ?"}
            value={value.sql.query}
            onChange={(e) => set("sql", { query: e.target.value })}
          />
          <div className="flex items-center gap-2">
            <Label className="text-[11px]">Mode</Label>
            <ToggleGroup
              type="single"
              size="sm"
              variant="outline"
              value={value.sql.mode}
              onValueChange={(v) => v && set("sql", { mode: v as "query" | "exec" })}
            >
              <ToggleGroupItem value="query" className="text-xs" title="Reads all returned rows">
                Query (read rows)
              </ToggleGroupItem>
              <ToggleGroupItem value="exec" className="text-xs" title="Discards results; for INSERT/UPDATE/DELETE">
                Exec
              </ToggleGroupItem>
            </ToggleGroup>
          </div>
          <Label className="text-[11px]">Arguments</Label>
          <div className="flex flex-col gap-1">
            {value.sql.args.map((a, i) => (
              <div key={i} className="flex items-center gap-1">
                <span className="w-7 shrink-0 text-right font-mono text-[11px] text-muted-foreground">
                  {connection.kind === "postgres" ? `$${i + 1}` : `?${i + 1}`}
                </span>
                <Input
                  className="h-7 font-mono text-xs"
                  value={a}
                  placeholder="{{rand}}"
                  onChange={(e) => set("sql", { args: value.sql.args.map((x, j) => (j === i ? e.target.value : x)) })}
                  aria-label={`Argument ${i + 1}`}
                />
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={`Remove argument ${i + 1}`}
                  onClick={() => set("sql", { args: value.sql.args.filter((_, j) => j !== i) })}
                >
                  <Trash2 />
                </Button>
              </div>
            ))}
            <Button size="xs" variant="ghost" className="self-start" onClick={() => set("sql", { args: [...value.sql.args, ""] })}>
              <Plus /> Argument
            </Button>
          </div>
          <p className="text-[11px] text-muted-foreground">
            Arguments are sent as query parameters ({connection.kind === "postgres" ? "$1, $2…" : "?, ?…"}, in order) — never pasted into the
            SQL. {PLACEHOLDER_HINT}
          </p>
        </div>
      )}

      {value.mode === "custom" && kind === "redis" && (
        <div className="flex flex-col gap-1.5">
          <Label className="text-[11px]">Command</Label>
          <Input
            className="h-7 font-mono text-xs"
            placeholder="SET bench:{{seq}} {{uuid}}"
            value={value.redis.command}
            onChange={(e) => set("redis", { command: e.target.value })}
          />
          <p className="text-[11px] text-muted-foreground">
            One command per request, e.g. <span className="font-mono">GET bench:{"{{rand}}"}</span>. Use double quotes for an argument with spaces.{" "}
            {PLACEHOLDER_HINT}
          </p>
        </div>
      )}

      {value.mode === "custom" && kind === "mongo" && (
        <div className="grid grid-cols-[6rem_1fr] items-center gap-x-2 gap-y-1.5 text-xs">
          <Label className="text-[11px]">Operation</Label>
          <Select value={value.mongo.operation} onValueChange={(op) => set("mongo", { operation: op as MongoTarget["operation"] })}>
            <SelectTrigger size="sm" className="text-xs" aria-label="Operation">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {MONGO_OPS.map((op) => (
                <SelectItem key={op} value={op}>
                  {op}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Label className="text-[11px]">Database</Label>
          <Input
            className="h-7 font-mono text-xs"
            placeholder={connection.database || "app"}
            value={value.mongo.database}
            onChange={(e) => set("mongo", { database: e.target.value })}
          />
          {value.mongo.operation !== "runCommand" && (
            <>
              <Label className="text-[11px]">Collection</Label>
              <Input
                className="h-7 font-mono text-xs"
                placeholder="users"
                value={value.mongo.collection ?? ""}
                onChange={(e) => set("mongo", { collection: e.target.value })}
              />
            </>
          )}
          <Label className="self-start pt-1 text-[11px]">
            {value.mongo.operation === "insertOne" ? "Document" : value.mongo.operation === "runCommand" ? "Command" : "Filter"}
          </Label>
          <Textarea
            rows={3}
            className="min-h-0 font-mono text-xs"
            placeholder={value.mongo.operation === "insertOne" ? '{"_id": "{{uuid}}", "at": "{{now}}"}' : value.mongo.operation === "runCommand" ? '{"ping": 1}' : '{"n": {{rand}}}'}
            value={value.mongo.document ?? ""}
            onChange={(e) => set("mongo", { document: e.target.value })}
          />
          {value.mongo.operation === "updateOne" && (
            <>
              <Label className="self-start pt-1 text-[11px]">Update</Label>
              <Textarea
                rows={2}
                className="min-h-0 font-mono text-xs"
                placeholder='{"$set": {"seen": "{{now}}"}}'
                value={value.mongo.update ?? ""}
                onChange={(e) => set("mongo", { update: e.target.value })}
              />
            </>
          )}
          <p className="col-span-2 text-[11px] text-muted-foreground">JSON. {PLACEHOLDER_HINT}</p>
        </div>
      )}

      {value.mode === "custom" && kind === "aws" && (
        <div className="grid grid-cols-[6rem_1fr] items-center gap-x-2 gap-y-1.5 text-xs">
          <Label className="text-[11px]">Operation</Label>
          <Input
            list="nimbo-conn-aws-ops"
            className="h-7 font-mono text-xs"
            value={value.aws.operation}
            onChange={(e) => set("aws", { operation: e.target.value })}
          />
          <datalist id="nimbo-conn-aws-ops">
            {AWS_OPERATIONS.map((o) => (
              <option key={o} value={o} />
            ))}
          </datalist>
          <Label className="text-[11px]">Resource</Label>
          <Input
            className="h-7 font-mono text-xs"
            placeholder="queue URL, bucket, table, function, stream, bus or state machine ARN"
            value={value.aws.resource}
            onChange={(e) => set("aws", { resource: e.target.value })}
          />
          <Label className="text-[11px]">Key</Label>
          <Input
            className="h-7 font-mono text-xs"
            placeholder="object key / item key ({{seq}}, {{uuid}})"
            value={value.aws.key ?? ""}
            onChange={(e) => set("aws", { key: e.target.value })}
          />
          <Label className="self-start pt-1 text-[11px]">Payload</Label>
          <Textarea
            rows={3}
            className="min-h-0 font-mono text-xs"
            placeholder='{"id": "{{uuid}}", "at": "{{now}}"}'
            value={value.aws.payload ?? ""}
            onChange={(e) => set("aws", { payload: e.target.value })}
          />
          <p className="col-span-2 text-[11px] text-destructive">
            Runs against your real AWS account ({connection.region || "profile default region"}) and may cost money.
          </p>
        </div>
      )}
    </div>
  )
}

function SuggestedRow({ target: t, selected, onSelect }: { target: LoadTarget; selected: boolean; onSelect: () => void }) {
  const effect = targetSideEffect(t)
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={cn(
        "flex items-center gap-2 rounded-md border px-2 py-1.5 text-left text-xs outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring/50",
        selected && "border-primary bg-primary/5",
      )}
    >
      <Badge variant="outline" className="h-4 px-1 font-mono text-[10px] uppercase">
        {t.kind}
      </Badge>
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium">{t.label.replace(/\s*\((writes data|invokes function)\)\s*$/i, "")}</span>
        <span className="block truncate font-mono text-[10px] text-muted-foreground">{targetDetail(t)}</span>
      </span>
      {effect && (
        <Badge variant="outline" className="h-4 shrink-0 gap-0.5 border-amber-500/40 px-1 text-[10px] text-amber-700 dark:text-amber-400">
          <TriangleAlert className="size-2.5" /> {effect}
        </Badge>
      )}
    </button>
  )
}

import { useState, type ReactNode } from "react"
import { CircleDot, ClipboardPaste, Copy, Eye, EyeOff, Layers, Lock, LockOpen, Plus, Trash2 } from "lucide-react"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Environment, Workflow } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { CommitInput } from "../components/commit-input"
import { ValueEditor } from "../components/value-editor"
import { editWorkflow } from "../lib/edit"
import { parseDotEnv, parseTfvars } from "../lib/parse-vars"
import { getAttr, missingRequired, uniqueName, valueText, variableKind } from "../lib/values"
import { DENSE_TABLE } from "./blocks-editors"

const ENV_NAME = /^[A-Za-z0-9_-]+$/
const SUGGESTED_ENV_NAMES = ["dev", "staging", "prod"]

function editEnv(name: string, mutate: (env: Environment, wf: Workflow) => void) {
  editWorkflow((d) => {
    const env = d.environments.find((e) => e.name === name)
    if (env) mutate(env, d)
  })
}

export function EnvironmentsEditor() {
  const workflow = useWorkflowStore((s) => s.workflow)!
  const [selected, setSelected] = useState<string | null>(null)
  const envs = workflow.environments
  const names = envs.map((e) => e.name)
  const current = envs.find((e) => e.name === selected) ?? envs.find((e) => e.name === workflow.activeEnvironment) ?? envs[0]

  const create = () => {
    const name = SUGGESTED_ENV_NAMES.find((n) => !names.includes(n)) ?? uniqueName("env", names)
    editWorkflow((d) => {
      d.environments.push({ name, values: {}, envVars: [] })
      if (!d.activeEnvironment) d.activeEnvironment = name
    })
    setSelected(name)
  }

  if (envs.length === 0) {
    return (
      <Empty className="h-full p-4">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Layers />
          </EmptyMedia>
          <EmptyTitle>No environments yet</EmptyTitle>
          <EmptyDescription>
            An environment (dev, staging, prod…) holds the values your variables take and the environment variables
            passed to Terraform when you deploy.
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button size="sm" onClick={create}>
            <Plus /> Create environment
          </Button>
        </EmptyContent>
      </Empty>
    )
  }

  return (
    <div className="flex h-full min-h-0">
      <div className="flex w-40 shrink-0 flex-col border-r">
        <div className="flex h-8 items-center justify-between border-b pr-1 pl-2.5 text-[11px] font-medium text-muted-foreground">
          Environments
          <Button size="icon-xs" variant="ghost" aria-label="New environment" onClick={create}>
            <Plus />
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-auto p-1" role="listbox" aria-label="Environments">
          {envs.map((env) => {
            const missing = missingRequired(workflow, env.name).length
            const active = env.name === workflow.activeEnvironment
            return (
              <button
                key={env.name}
                type="button"
                role="option"
                aria-selected={env === current}
                onClick={() => setSelected(env.name)}
                className={cn(
                  "flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/50",
                  env === current && "bg-muted font-medium",
                )}
              >
                <CircleDot className={cn("size-3", active ? "text-emerald-500" : "text-transparent")} />
                <span className="min-w-0 flex-1 truncate">{env.name}</span>
                {missing > 0 && (
                  <Badge variant="destructive" className="h-4 px-1 text-[10px]" title={`${missing} required missing`}>
                    {missing}
                  </Badge>
                )}
              </button>
            )
          })}
        </div>
      </div>
      {current && <EnvironmentDetail key={current.name} env={current} onRenamed={setSelected} />}
    </div>
  )
}

function EnvironmentDetail({ env, onRenamed }: { env: Environment; onRenamed: (name: string) => void }) {
  const workflow = useWorkflowStore((s) => s.workflow)!
  const pro = useWorkflowStore((s) => s.mode === "pro")
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [paste, setPaste] = useState<"env" | "tfvars" | null>(null)
  const active = env.name === workflow.activeEnvironment
  const missing = missingRequired(workflow, env.name)
  const names = workflow.environments.map((e) => e.name)
  const varNames = workflow.variables.map((v) => v.name)
  const orphanValues = Object.keys(env.values).filter((k) => !varNames.includes(k))

  const rename = (next: string) => {
    if (!ENV_NAME.test(next)) {
      toast.error(`Invalid environment name "${next}"`, { description: "Use letters, digits, dashes or underscores." })
      return false
    }
    if (names.includes(next)) {
      toast.error(`An environment named "${next}" already exists`)
      return false
    }
    editEnv(env.name, (e, d) => {
      e.name = next
      if (d.activeEnvironment === env.name) d.activeEnvironment = next
    })
    onRenamed(next)
  }

  const duplicate = () => {
    const name = uniqueName(`${env.name}-copy`, names)
    editWorkflow((d) => {
      const src = d.environments.find((e) => e.name === env.name)
      if (src) d.environments.push({ ...structuredClone(src), name })
    })
    onRenamed(name)
  }

  const remove = () =>
    editWorkflow((d) => {
      d.environments = d.environments.filter((e) => e.name !== env.name)
      if (d.activeEnvironment === env.name) d.activeEnvironment = d.environments[0]?.name
    })

  const importText = (text: string) => {
    if (paste === "env") {
      const pairs = parseDotEnv(text)
      if (pairs.length === 0) return toast.error("No KEY=value lines found")
      editEnv(env.name, (e) => {
        for (const { key, value } of pairs) {
          const existing = e.envVars.find((v) => v.key === key)
          if (existing) existing.value = value
          else e.envVars.push({ key, value, secret: /secret|password|token|key/i.test(key) || undefined })
        }
      })
      toast.success(`Imported ${pairs.length} environment variable${pairs.length === 1 ? "" : "s"}`)
    } else {
      const values = parseTfvars(text)
      const keys = Object.keys(values)
      if (keys.length === 0) return toast.error("No name = value lines found")
      editEnv(env.name, (e) => Object.assign(e.values, values))
      const unknown = keys.filter((k) => !varNames.includes(k))
      toast.success(`Imported ${keys.length} value${keys.length === 1 ? "" : "s"}`, {
        description: unknown.length ? `No variable named: ${unknown.join(", ")}` : undefined,
      })
    }
    setPaste(null)
  }

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-1 border-b px-2">
        <CommitInput value={env.name} aria-label="Environment name" className="h-6 w-36 font-mono" onCommit={rename} />
        {active ? (
          <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
            <CircleDot /> Active
          </Badge>
        ) : (
          <Button size="xs" variant="ghost" onClick={() => editWorkflow((d) => void (d.activeEnvironment = env.name))}>
            <CircleDot /> Set active
          </Button>
        )}
        <div className="ml-auto flex items-center gap-0.5">
          <Button size="xs" variant="ghost" onClick={duplicate}>
            <Copy /> Duplicate
          </Button>
          <Button size="xs" variant="ghost" className="text-destructive" onClick={() => setConfirmDelete(true)}>
            <Trash2 /> Delete
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        <SectionTitle
          title="Variable values"
          hint={
            missing.length > 0 ? (
              <span className="text-destructive">
                {missing.length} required variable{missing.length === 1 ? "" : "s"} without a value
              </span>
            ) : (
              "Empty values fall back to the variable's default"
            )
          }
          action={
            <Button size="xs" variant="ghost" onClick={() => setPaste("tfvars")}>
              <ClipboardPaste /> Paste .tfvars
            </Button>
          }
        />
        <Table className={DENSE_TABLE}>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[30%]">Variable</TableHead>
              <TableHead>Value</TableHead>
              <TableHead className="w-8" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {workflow.variables.length === 0 && orphanValues.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={3} className="py-3! text-center text-muted-foreground">
                  Add variables on the left to give them per-environment values.
                </TableCell>
              </TableRow>
            )}
            {workflow.variables.map((v) => {
              const def = getAttr(v.body, "default")
              const isMissing = missing.includes(v.name)
              return (
                <TableRow key={v.name}>
                  <TableCell>
                    <div className="flex items-center gap-1.5">
                      <span className="truncate font-mono">{v.name}</span>
                      <span className="font-mono text-[10px] text-muted-foreground">
                        {valueText(getAttr(v.body, "type"))}
                      </span>
                      {isMissing && (
                        <Badge variant="destructive" className="h-4 px-1 text-[10px]">
                          Required
                        </Badge>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <ValueEditor
                      kind={variableKind(v)}
                      value={env.values[v.name]}
                      allowExpr={pro}
                      emptyClears
                      invalid={isMissing}
                      placeholder={def ? `Default: ${valueText(def)}` : "Value required"}
                      onChange={(val) =>
                        editEnv(env.name, (e) => {
                          if (val === undefined) delete e.values[v.name]
                          else e.values[v.name] = val
                        })
                      }
                    />
                  </TableCell>
                  <TableCell />
                </TableRow>
              )
            })}
            {orphanValues.map((k) => (
              <TableRow key={`orphan-${k}`}>
                <TableCell>
                  <span className="font-mono text-muted-foreground line-through">{k}</span>
                  <span className="ml-1.5 text-[10px] text-amber-600 dark:text-amber-400">no such variable</span>
                </TableCell>
                <TableCell className="truncate font-mono text-muted-foreground">{valueText(env.values[k])}</TableCell>
                <TableCell>
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label={`Remove value ${k}`}
                    onClick={() => editEnv(env.name, (e) => void delete e.values[k])}
                  >
                    <Trash2 />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <SectionTitle
          title="Environment variables"
          hint="Passed to Terraform during deploys"
          action={
            <Button size="xs" variant="ghost" onClick={() => setPaste("env")}>
              <ClipboardPaste /> Paste .env
            </Button>
          }
        />
        <EnvVarRows env={env} />
      </div>

      <PasteDialog format={paste} onClose={() => setPaste(null)} onImport={importText} />

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>Delete environment “{env.name}”?</AlertDialogTitle>
            <AlertDialogDescription>
              Its variable values and environment variables are removed. Deployed resources are not affected.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={remove}>
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function SectionTitle({ title, hint, action }: { title: string; hint?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex h-8 items-center gap-2 border-y bg-muted/30 px-2.5 text-[11px] first:border-t-0">
      <span className="font-semibold">{title}</span>
      {hint && <span className="truncate text-muted-foreground">{hint}</span>}
      <div className="ml-auto">{action}</div>
    </div>
  )
}

function EnvVarRows({ env }: { env: Environment }) {
  const [revealed, setRevealed] = useState<Record<number, boolean>>({})
  const keys = env.envVars.map((v) => v.key)

  return (
    <div className="flex flex-col">
      <Table className={DENSE_TABLE}>
        <TableHeader>
          <TableRow>
            <TableHead className="w-[30%]">Key</TableHead>
            <TableHead>Value</TableHead>
            <TableHead className="w-16 text-center">Secret</TableHead>
            <TableHead className="w-8" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {env.envVars.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={4} className="py-3! text-center whitespace-normal text-muted-foreground">
                e.g. <code>TF_VAR_db_password</code> or provider settings. Secrets are masked and left blank in exports.
              </TableCell>
            </TableRow>
          )}
          {env.envVars.map((ev, i) => (
            <TableRow key={i}>
              <TableCell>
                <CommitInput
                  value={ev.key}
                  className="font-mono"
                  placeholder="KEY"
                  onCommit={(k) => {
                    const key = k.trim()
                    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
                      toast.error(`Invalid key "${k}"`)
                      return false
                    }
                    if (keys.includes(key)) {
                      toast.error(`"${key}" is already defined`)
                      return false
                    }
                    editEnv(env.name, (e) => void (e.envVars[i].key = key))
                  }}
                />
              </TableCell>
              <TableCell>
                <div className="flex items-center gap-0.5">
                  <CommitInput
                    value={ev.value}
                    type={ev.secret && !revealed[i] ? "password" : "text"}
                    className="font-mono"
                    autoComplete="off"
                    spellCheck={false}
                    onCommit={(val) => editEnv(env.name, (e) => void (e.envVars[i].value = val))}
                  />
                  {ev.secret && (
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={revealed[i] ? "Hide value" : "Reveal value"}
                      onClick={() => setRevealed((r) => ({ ...r, [i]: !r[i] }))}
                    >
                      {revealed[i] ? <EyeOff /> : <Eye />}
                    </Button>
                  )}
                </div>
              </TableCell>
              <TableCell className="text-center">
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      size="icon-xs"
                      variant={ev.secret ? "secondary" : "ghost"}
                      aria-pressed={!!ev.secret}
                      aria-label="Secret"
                      onClick={() => editEnv(env.name, (e) => void (e.envVars[i].secret = !ev.secret || undefined))}
                    >
                      {ev.secret ? <Lock /> : <LockOpen />}
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>{ev.secret ? "Secret: masked, blank in exports" : "Mark as secret"}</TooltipContent>
                </Tooltip>
              </TableCell>
              <TableCell>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={`Delete ${ev.key}`}
                  onClick={() => editEnv(env.name, (e) => void e.envVars.splice(i, 1))}
                >
                  <Trash2 />
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <div className="p-1.5">
        <Button
          size="xs"
          variant="ghost"
          onClick={() => editEnv(env.name, (e) => void e.envVars.push({ key: uniqueName("NEW_VAR", keys), value: "" }))}
        >
          <Plus /> Add environment variable
        </Button>
      </div>
    </div>
  )
}

function PasteDialog({
  format,
  onClose,
  onImport,
}: {
  format: "env" | "tfvars" | null
  onClose: () => void
  onImport: (text: string) => void
}) {
  const [text, setText] = useState("")
  const isEnv = format === "env"
  return (
    <Dialog
      open={format !== null}
      onOpenChange={(open) => {
        if (!open) {
          setText("")
          onClose()
        }
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{isEnv ? "Paste .env" : "Paste .tfvars"}</DialogTitle>
          <DialogDescription>
            {isEnv
              ? "One KEY=value per line. Existing keys are overwritten; keys that look like secrets are masked."
              : "One name = value per line. Strings, numbers and booleans become plain values; lists and maps are kept as expressions."}
          </DialogDescription>
        </DialogHeader>
        <Textarea
          autoFocus
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={10}
          spellCheck={false}
          className="font-mono text-xs"
          placeholder={isEnv ? "API_TOKEN=abc123\nLOG_LEVEL=debug" : 'region        = "us-east-1"\ninstance_count = 2'}
        />
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={!text.trim()}
            onClick={() => {
              onImport(text)
              setText("")
            }}
          >
            Import
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

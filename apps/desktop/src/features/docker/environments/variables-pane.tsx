import { useState } from "react"
import { EyeIcon, EyeOffIcon, KeyRound, Plus, RotateCcw, X } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { CommitInput } from "@/features/docker/inspector/fields"
import { SECRET_KEY } from "@/features/docker/inspector/service-form"
import { changeEnvironments } from "@/features/kubernetes/environments/actions"
import { api } from "@/lib/api/client"
import type { EnvironmentInfo, EnvironmentVariable, EnvironmentsView } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { baseEnvFile, dotenvValue, infoEnvFile, sortVariables, variableKeyError } from "./model"

/**
 * The variables of one docker environment: its .env.<env>, read after the project's .env (its values win). Inherited
 * .env values show as placeholders; typing one overrides it in .env.<env>. Secret values stay hidden until revealed.
 */
export function VariablesPane({ view, environment }: { view: EnvironmentsView; environment: string }) {
  const info = view.environments.find((e) => e.name === environment)
  if (!info) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyTitle className="text-sm">Base variables</EmptyTitle>
          <EmptyDescription className="text-xs">
            The base reads <span className="font-mono">.env</span> (edit it in the Code tab). Pick an environment to override
            values in its own <span className="font-mono">.env.&lt;env&gt;</span>.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return <EnvironmentVariables key={info.name} info={info} />
}

function EnvironmentVariables({ info }: { info: EnvironmentInfo }) {
  const file = infoEnvFile(info)
  const vars = sortVariables(info.variables ?? [])
  const own = vars.filter((v) => v.overridden).map((v) => v.key)
  const set = (key: string, value: string) =>
    changeEnvironments((id) => api.setEnvironmentVariable(id, info.name, key, { value }), { failure: `Couldn't set ${key}` })
  const remove = (key: string) =>
    changeEnvironments((id) => api.deleteEnvironmentVariable(id, info.name, key), { failure: `Couldn't remove ${key}` })

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="env-variables">
      <p className="shrink-0 border-b px-3 py-1.5 text-[11px] text-muted-foreground">
        <span className="font-mono">{file}</span> is read after <span className="font-mono">{baseEnvFile(file)}</span>: values set
        here win in {info.name}. {own.length === 0 && "It is created with the first override."}
      </p>
      <div className="min-h-0 flex-1 overflow-auto">
        <Table className="text-xs">
          <TableHeader className="sticky top-0 z-10 bg-background">
            <TableRow>
              <TableHead className="h-7 w-[30%] px-3 text-[11px]">Variable</TableHead>
              <TableHead className="h-7 px-2 text-[11px]">Value in {info.name}</TableHead>
              <TableHead className="hidden h-7 w-[28%] px-2 text-[11px] @2xl:table-cell">In .env</TableHead>
              <TableHead className="h-7 w-9 px-1" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {vars.map((v) => (
              <VariableRow key={v.key} v={v} env={info.name} file={file} onSet={set} onRemove={remove} />
            ))}
            {vars.length === 0 && (
              <TableRow>
                <TableCell colSpan={4} className="px-3 py-3 text-center text-muted-foreground">
                  No variables in .env or {file} yet.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
        <AddVariable existing={own} onAdd={set} />
      </div>
    </div>
  )
}

function VariableRow({
  v,
  env,
  file,
  onSet,
  onRemove,
}: {
  v: EnvironmentVariable
  env: string
  file: string
  onSet: (key: string, value: string) => Promise<boolean>
  onRemove: (key: string) => Promise<boolean>
}) {
  const id = `env-var-${v.key}`
  const inherited = v.inBase ? (v.secret ? "••••••" : (v.base ?? "")) : undefined
  return (
    <TableRow data-testid={id} data-overridden={v.overridden || undefined}>
      <TableCell className="px-3 py-1 align-middle">
        <span className="flex min-w-0 items-center gap-1.5">
          {v.secret && <KeyRound className="size-3 shrink-0 text-muted-foreground" aria-label="Secret" />}
          <span className="truncate font-mono" title={v.key}>
            {v.key}
          </span>
          {v.overridden ? (
            <Badge variant="outline" className="h-4 shrink-0 border-sky-500/40 px-1 text-[10px] text-sky-700 dark:text-sky-300">
              {env}
            </Badge>
          ) : (
            <span className="shrink-0 text-[10px] text-muted-foreground">.env</span>
          )}
        </span>
      </TableCell>
      <TableCell className="px-2 py-1">
        {v.secret ? (
          <SecretValue v={v} file={v.overridden ? file : baseEnvFile(file)} onCommit={(value) => void onSet(v.key, value)} />
        ) : (
          <CommitInput
            aria-label={`Value of ${v.key} in ${env}`}
            className="h-7 font-mono"
            value={v.overridden ? (v.value ?? "") : ""}
            placeholder={v.overridden ? "empty" : (inherited ?? "")}
            onCommit={(value) => void (v.overridden || value !== "" ? onSet(v.key, value) : undefined)}
            data-testid={`${id}-value`}
          />
        )}
      </TableCell>
      <TableCell className="hidden max-w-0 px-2 py-1 @2xl:table-cell">
        <span className="block truncate font-mono text-muted-foreground" title={v.secret ? undefined : inherited}>
          {inherited === undefined ? "—" : inherited || '""'}
        </span>
      </TableCell>
      <TableCell className="px-1 py-1">
        {v.overridden && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button size="icon-xs" variant="ghost" aria-label={v.inBase ? `Use the .env value of ${v.key}` : `Remove ${v.key}`} onClick={() => void onRemove(v.key)} data-testid={`${id}-remove`}>
                {v.inBase ? <RotateCcw /> : <X />}
              </Button>
            </TooltipTrigger>
            <TooltipContent>{v.inBase ? "Back to the .env value" : `Remove from ${file}`}</TooltipContent>
          </Tooltip>
        )}
      </TableCell>
    </TableRow>
  )
}

/** A secret's value: hidden (never sent by the environments view); revealing reads it from its env file. */
function SecretValue({ v, file, onCommit }: { v: EnvironmentVariable; file: string; onCommit: (value: string) => void }) {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const [value, setValue] = useState<string | null>(null)
  const [shown, setShown] = useState(false)
  const [loading, setLoading] = useState(false)
  const reveal = async () => {
    if (shown) return setShown(false)
    if (value === null) {
      setLoading(true)
      try {
        const f = await api.fileContent(workflowId, file)
        setValue(dotenvValue(f.content, v.key) ?? "")
      } catch (err) {
        toast.error(`Couldn't read ${file}`, { description: errorMessage(err) })
        return
      } finally {
        setLoading(false)
      }
    }
    setShown(true)
  }
  const placeholder = v.overridden ? "•••••• (unchanged)" : v.inBase ? "•••••• from .env" : "value"
  return (
    <div className="flex min-w-0 items-center gap-1">
      <CommitInput
        aria-label={`Value of ${v.key}`}
        className="h-7 font-mono"
        type={shown ? "text" : "password"}
        // Inherited values are shown as placeholders: typing one overrides it here.
        value={shown && v.overridden ? (value ?? "") : ""}
        placeholder={shown && !v.overridden ? value || placeholder : placeholder}
        onCommit={(next) => {
          if (next === "" && !(shown && v.overridden)) return
          setValue(next)
          onCommit(next)
        }}
        data-testid={`env-var-${v.key}-value`}
      />
      {(v.overridden || v.inBase) && (
        <Button type="button" variant="ghost" size="icon-xs" onClick={() => void reveal()} aria-label={shown ? "Hide value" : "Show value"} data-testid={`env-var-${v.key}-reveal`}>
          {loading ? <Spinner /> : shown ? <EyeOffIcon /> : <EyeIcon />}
        </Button>
      )}
    </div>
  )
}

/** A new variable of this environment (a key .env doesn't have, or one it overrides). */
function AddVariable({ existing, onAdd }: { existing: string[]; onAdd: (key: string, value: string) => Promise<boolean> }) {
  const [key, setKey] = useState("")
  const [value, setValue] = useState("")
  const [touched, setTouched] = useState(false)
  const [pending, setPending] = useState(false)
  const error = variableKeyError(key, existing)
  const secret = SECRET_KEY.test(key)
  const submit = async () => {
    setTouched(true)
    if (error || pending) return
    setPending(true)
    const ok = await onAdd(key, value)
    setPending(false)
    if (ok) {
      setKey("")
      setValue("")
      setTouched(false)
    }
  }
  return (
    <form
      className="flex flex-wrap items-start gap-2 border-t px-3 py-2"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <div className="flex min-w-32 flex-1 basis-40 flex-col gap-1">
        <Input
          value={key}
          onChange={(e) => setKey(e.target.value.trim())}
          placeholder="NEW_VARIABLE"
          aria-label="New variable name"
          aria-invalid={(touched && !!error) || undefined}
          className="h-7 font-mono text-xs"
          spellCheck={false}
          autoComplete="off"
          data-testid="env-var-new-key"
        />
        {touched && error && <p className="text-[11px] text-destructive">{error}</p>}
      </div>
      <Input
        value={value}
        onChange={(e) => setValue(e.target.value)}
        type={secret ? "password" : "text"}
        placeholder="value"
        aria-label="New variable value"
        className="h-7 min-w-32 flex-1 basis-40 font-mono text-xs"
        spellCheck={false}
        autoComplete="off"
        data-testid="env-var-new-value"
      />
      <Button type="submit" size="xs" variant="outline" className="h-7" disabled={pending} data-testid="env-var-add">
        {pending ? <Spinner /> : <Plus />} Add
      </Button>
    </form>
  )
}

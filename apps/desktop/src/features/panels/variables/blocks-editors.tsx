import type { ReactNode } from "react"
import { Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { NamedBlock } from "@/lib/api/types"
import { useShallow } from "zustand/react/shallow"
import { useWorkflowStore } from "@/state/workflow-store"
import { cn } from "@/lib/utils"
import { editWorkflow as edit } from "../lib/edit"
import { CommitInput } from "../components/commit-input"
import { ValueEditor } from "../components/value-editor"
import { emptyBody, getAttr, hasLiteral, setAttr, uniqueName, valueText, variableKind } from "../lib/values"

export const DENSE_TABLE =
  "text-xs [&_th]:h-7 [&_th]:px-1.5 [&_th]:text-[11px] [&_th]:font-medium [&_th]:text-muted-foreground [&_td]:px-1.5 [&_td]:py-1"

const IDENT = /^[A-Za-z_][A-Za-z0-9_-]*$/
const TYPE_SUGGESTIONS = ["string", "number", "bool", "list(string)", "set(string)", "map(string)", "list(number)", "any"]

function validIdent(name: string, taken: string[], what: string): boolean {
  if (!IDENT.test(name)) {
    toast.error(`Invalid ${what} name "${name}"`, {
      description: "Use letters, digits, underscores or dashes; start with a letter or underscore.",
    })
    return false
  }
  if (taken.includes(name)) {
    toast.error(`A ${what} named "${name}" already exists`)
    return false
  }
  return true
}

function literalText(block: NamedBlock, attr: string): string {
  const v = getAttr(block.body, attr)
  return hasLiteral(v) && typeof v.literal === "string" ? v.literal : valueText(v)
}

function EmptyRow({ colSpan, children }: { colSpan: number; children: ReactNode }) {
  return (
    <TableRow className="hover:bg-transparent">
      <TableCell colSpan={colSpan} className="py-4! text-center whitespace-normal text-muted-foreground">
        {children}
      </TableCell>
    </TableRow>
  )
}

export function VariablesEditor() {
  const workflow = useWorkflowStore(useShallow((s) => ({ variables: s.workflow!.variables })))
  const pro = useWorkflowStore((s) => s.mode === "pro")
  const names = workflow.variables.map((v) => v.name)

  const rename = (index: number, next: string) => {
    const prev = workflow.variables[index].name
    if (!validIdent(next, names, "variable")) return false
    edit((d) => {
      d.variables[index].name = next
      for (const env of d.environments) {
        if (prev in env.values) {
          env.values[next] = env.values[prev]
          delete env.values[prev]
        }
      }
    })
  }

  const add = () =>
    edit((d) =>
      d.variables.push({ name: uniqueName("new_variable", names), body: emptyBody([{ name: "type", value: { expr: "string" } }]) }),
    )

  return (
    <div className="flex flex-col">
      <datalist id="nimbo-variable-types">
        {TYPE_SUGGESTIONS.map((t) => (
          <option key={t} value={t} />
        ))}
      </datalist>
      {/* Narrow panels scroll the table sideways instead of squeezing the inputs to a few characters. */}
      <Table className={cn(DENSE_TABLE, "min-w-[46rem]")}>
        <TableHeader>
          <TableRow>
            <TableHead className="w-[18%]">Name</TableHead>
            <TableHead className="w-[14%]">Type</TableHead>
            <TableHead className="w-[26%]">Default</TableHead>
            <TableHead>Description</TableHead>
            <TableHead className="w-16 text-center">Sensitive</TableHead>
            <TableHead className="w-8" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {workflow.variables.length === 0 && (
            <EmptyRow colSpan={6}>
              Variables are inputs you can change per environment (region, instance size, names…).
            </EmptyRow>
          )}
          {workflow.variables.map((v, i) => {
            const def = getAttr(v.body, "default")
            return (
              <TableRow key={`${i}-${v.name}`}>
                <TableCell>
                  <CommitInput value={v.name} className="font-mono" onCommit={(n) => rename(i, n.trim())} />
                </TableCell>
                <TableCell>
                  <CommitInput
                    value={valueText(getAttr(v.body, "type"))}
                    placeholder="any"
                    list="nimbo-variable-types"
                    className="font-mono"
                    onCommit={(t) =>
                      edit((d) => setAttr(d.variables[i].body, "type", t.trim() ? { expr: t.trim() } : undefined))
                    }
                  />
                </TableCell>
                <TableCell>
                  {def === undefined ? (
                    <div className="flex items-center gap-1.5">
                      <span className="text-muted-foreground">Required</span>
                      <Button
                        size="xs"
                        variant="ghost"
                        onClick={() =>
                          edit((d) => {
                            const kind = variableKind(d.variables[i])
                            const initial =
                              kind === "number" ? { literal: 0 } : kind === "bool" ? { literal: false } : kind === "string" ? { literal: "" } : { expr: "null" }
                            setAttr(d.variables[i].body, "default", initial)
                          })
                        }
                      >
                        <Plus /> default
                      </Button>
                    </div>
                  ) : (
                    <div className="flex items-center gap-0.5">
                      <ValueEditor
                        className="flex-1"
                        kind={variableKind(v)}
                        value={def}
                        allowExpr={pro}
                        onChange={(val) =>
                          edit((d) => setAttr(d.variables[i].body, "default", val ?? { literal: "" }))
                        }
                      />
                      <Button
                        size="icon-xs"
                        variant="ghost"
                        aria-label="Remove default (make required)"
                        title="Remove default (make required)"
                        onClick={() => edit((d) => setAttr(d.variables[i].body, "default", undefined))}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  )}
                </TableCell>
                <TableCell>
                  <CommitInput
                    value={literalText(v, "description")}
                    placeholder="What is this for?"
                    onCommit={(t) =>
                      edit((d) => setAttr(d.variables[i].body, "description", t ? { literal: t } : undefined))
                    }
                  />
                </TableCell>
                <TableCell className="text-center">
                  <Switch
                    size="sm"
                    checked={hasLiteral(getAttr(v.body, "sensitive")) && getAttr(v.body, "sensitive")!.literal === true}
                    aria-label="Sensitive"
                    onCheckedChange={(on) =>
                      edit((d) => setAttr(d.variables[i].body, "sensitive", on ? { literal: true } : undefined))
                    }
                  />
                </TableCell>
                <TableCell>
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label={`Delete variable ${v.name}`}
                    onClick={() => edit((d) => void d.variables.splice(i, 1))}
                  >
                    <Trash2 />
                  </Button>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      <div className="p-1.5">
        <Button size="xs" variant="ghost" onClick={add}>
          <Plus /> Add variable
        </Button>
      </div>
    </div>
  )
}

export function OutputsEditor() {
  const workflow = useWorkflowStore(useShallow((s) => ({ outputs: s.workflow!.outputs })))
  const names = workflow.outputs.map((o) => o.name)

  return (
    <div className="flex flex-col">
      <Table className={cn(DENSE_TABLE, "min-w-[40rem]")}>
        <TableHeader>
          <TableRow>
            <TableHead className="w-[20%]">Name</TableHead>
            <TableHead className="w-[38%]">Value expression</TableHead>
            <TableHead>Description</TableHead>
            <TableHead className="w-16 text-center">Sensitive</TableHead>
            <TableHead className="w-8" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {workflow.outputs.length === 0 && (
            <EmptyRow colSpan={5}>
              Outputs expose values after deploying, e.g. a queue URL: <code>aws_sqs_queue.jobs.url</code>
            </EmptyRow>
          )}
          {workflow.outputs.map((o, i) => (
            <TableRow key={`${i}-${o.name}`}>
              <TableCell>
                <CommitInput
                  value={o.name}
                  className="font-mono"
                  onCommit={(n) => {
                    if (!validIdent(n.trim(), names, "output")) return false
                    edit((d) => void (d.outputs[i].name = n.trim()))
                  }}
                />
              </TableCell>
              <TableCell>
                <CommitInput
                  value={valueText(getAttr(o.body, "value"))}
                  placeholder="aws_s3_bucket.site.arn"
                  className="font-mono"
                  spellCheck={false}
                  onCommit={(t) => {
                    if (!t.trim()) {
                      toast.error("An output needs a value expression")
                      return false
                    }
                    edit((d) => setAttr(d.outputs[i].body, "value", { expr: t.trim() }))
                  }}
                />
              </TableCell>
              <TableCell>
                <CommitInput
                  value={literalText(o, "description")}
                  onCommit={(t) => edit((d) => setAttr(d.outputs[i].body, "description", t ? { literal: t } : undefined))}
                />
              </TableCell>
              <TableCell className="text-center">
                <Switch
                  size="sm"
                  checked={hasLiteral(getAttr(o.body, "sensitive")) && getAttr(o.body, "sensitive")!.literal === true}
                  aria-label="Sensitive"
                  onCheckedChange={(on) =>
                    edit((d) => setAttr(d.outputs[i].body, "sensitive", on ? { literal: true } : undefined))
                  }
                />
              </TableCell>
              <TableCell>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={`Delete output ${o.name}`}
                  onClick={() => edit((d) => void d.outputs.splice(i, 1))}
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
          onClick={() =>
            edit((d) =>
              d.outputs.push({ name: uniqueName("new_output", names), body: emptyBody([{ name: "value", value: { expr: "null" } }]) }),
            )
          }
        >
          <Plus /> Add output
        </Button>
      </div>
    </div>
  )
}

export function LocalsEditor() {
  const workflow = useWorkflowStore(useShallow((s) => ({ locals: s.workflow!.locals })))
  const names = workflow.locals.map((l) => l.name)

  return (
    <div className="flex flex-col">
      <Table className={cn(DENSE_TABLE, "min-w-[28rem]")}>
        <TableHeader>
          <TableRow>
            <TableHead className="w-[24%]">Name</TableHead>
            <TableHead>Expression</TableHead>
            <TableHead className="w-8" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {workflow.locals.length === 0 && (
            <EmptyRow colSpan={3}>
              Locals are named expressions reused across resources, referenced as <code>local.name</code>.
            </EmptyRow>
          )}
          {workflow.locals.map((l, i) => (
            <TableRow key={`${i}-${l.name}`}>
              <TableCell>
                <CommitInput
                  value={l.name}
                  className="font-mono"
                  onCommit={(n) => {
                    if (!validIdent(n.trim(), names, "local")) return false
                    edit((d) => void (d.locals[i].name = n.trim()))
                  }}
                />
              </TableCell>
              <TableCell>
                <CommitInput
                  value={valueText(l.value)}
                  className="font-mono"
                  spellCheck={false}
                  onCommit={(t) => {
                    if (!t.trim()) {
                      toast.error("A local needs an expression")
                      return false
                    }
                    edit((d) => void (d.locals[i].value = { expr: t.trim() }))
                  }}
                />
              </TableCell>
              <TableCell>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={`Delete local ${l.name}`}
                  onClick={() => edit((d) => void d.locals.splice(i, 1))}
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
          onClick={() => edit((d) => void d.locals.push({ name: uniqueName("new_local", names), value: { expr: '""' } }))}
        >
          <Plus /> Add local
        </Button>
      </div>
    </div>
  )
}

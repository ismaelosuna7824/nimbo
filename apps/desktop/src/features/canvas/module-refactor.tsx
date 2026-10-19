import { useQuery } from "@tanstack/react-query"
import { ArrowRightIcon, PackageIcon, PackageOpenIcon, TriangleAlertIcon } from "lucide-react"
import { useEffect, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api/client"
import type { ModuleIO, Workflow, WorkflowNode } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { revealFiles } from "../panels/terraform/reveal-files"
import { useModuleView } from "./module-view-store"
import { useModuleRefactor } from "./module-refactor-store"

const closeExtract = () => useModuleRefactor.setState({ extract: null })

const MODULE_NAME = /^[a-z][a-z0-9_]*$/

/** Suggests a snake_case module name: a local name shared by the nodes, else the first type without its provider. */
function suggestName(wf: Workflow, ids: string[]) {
  const nodes = wf.nodes.filter((n) => ids.includes(n.id))
  const counts = new Map<string, number>()
  for (const n of nodes) counts.set(n.name, (counts.get(n.name) ?? 0) + 1)
  const shared = [...counts.entries()].filter(([, c]) => c > 1).sort((a, b) => b[1] - a[1])[0]?.[0]
  const base = (shared ?? nodes[0]?.type.replace(/^[a-z0-9]+_/, "") ?? "module")
    .toLowerCase()
    .replace(/[^a-z0-9_]+/g, "_")
    .replace(/^[^a-z]+/, "")
  const taken = new Set(wf.nodes.filter((n) => n.kind === "module").map((n) => n.name))
  let name = base || "module"
  for (let i = 2; taken.has(name); i++) name = `${base}_${i}`
  return name
}

/** The extract and inline dialogs; mount once inside the canvas. */
export function ModuleRefactorDialogs() {
  return (
    <>
      <ExtractModuleDialog />
      <InlineModuleDialog />
    </>
  )
}

function ExtractModuleDialog() {
  const ids = useModuleRefactor((s) => s.extract)
  const wf = useWorkflowStore((s) => (ids ? s.workflow : null))
  return (
    <Dialog open={!!ids} onOpenChange={(o) => !o && closeExtract()}>
      <DialogContent className="sm:max-w-2xl">
        {ids && wf && <ExtractModuleForm key={ids.join(",")} ids={ids} wf={wf} />}
      </DialogContent>
    </Dialog>
  )
}

/** Dialog body; mounted per opening so the name starts from a fresh suggestion. */
function ExtractModuleForm({ ids, wf }: { ids: string[]; wf: Workflow }) {
  const [name, setName] = useState(() => suggestName(wf, ids))
  const [debounced, setDebounced] = useState(name)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const t = setTimeout(() => setDebounced(name.trim()), 300)
    return () => clearTimeout(t)
  }, [name])

  const valid = MODULE_NAME.test(name.trim())
  const preview = useQuery({
    queryKey: ["module-extract-preview", wf.id, wf.updatedAt, ids, debounced],
    queryFn: () => api.previewExtractModule(wf.id, { nodeIds: ids, name: debounced }),
    enabled: MODULE_NAME.test(debounced),
    retry: false,
  })
  const nodes = wf.nodes.filter((n) => ids.includes(n.id))
  const ready = valid && debounced === name.trim() && preview.isSuccess && !busy

  const extract = async () => {
    if (!ready) return
    const moduleName = name.trim()
    setBusy(true)
    try {
      const store = useWorkflowStore.getState()
      await store.flush()
      const updated = await api.extractModule(wf.id, { nodeIds: ids, name: moduleName })
      store.setWorkflow(updated)
      const call = updated.nodes.find((n) => n.kind === "module" && n.name === moduleName)
      store.select(call?.id ?? null)
      closeExtract()
      const dir = `modules/${moduleName}/`
      toast.success(`Extracted ${ids.length} block${ids.length === 1 ? "" : "s"} into module.${moduleName}`, {
        description: "Moved blocks keep anything already deployed: the next plan only moves resources.",
        action: { label: "Open files", onClick: () => revealFiles(dir, `${dir}main.tf`) },
      })
    } catch (err) {
      toast.error("Couldn't extract the module", { description: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <PackageIcon className="size-4 text-violet-500" />
            Extract to module
          </DialogTitle>
          <DialogDescription>
            Moves {nodes.length} block{nodes.length === 1 ? "" : "s"} into a local module and calls it from the root in
            their place, with typed inputs and described outputs.
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex min-h-0 flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            void extract()
          }}
        >
          <Field>
            <FieldLabel htmlFor="module-name">Module name</FieldLabel>
            <Input
              id="module-name"
              value={name}
              autoFocus
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
              aria-invalid={!valid}
              onChange={(e) => setName(e.target.value)}
            />
            <FieldDescription>
              {valid ? (
                <>
                  Becomes <code className="font-mono">module.{name.trim()}</code> with its files in{" "}
                  <code className="font-mono">modules/{name.trim()}/</code>.
                </>
              ) : (
                "Use snake_case: lowercase letters, digits and underscores, starting with a letter."
              )}
            </FieldDescription>
          </Field>
          <div className="flex flex-wrap gap-1">
            {nodes.map((n) => (
              <Badge key={n.id} variant="outline" className="font-mono">
                {address(n)}
              </Badge>
            ))}
          </div>
          <div className="max-h-[45vh] min-h-0 overflow-y-auto">
            {!valid ? null : preview.isPending || debounced !== name.trim() ? (
              <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
                <Spinner /> Working out inputs and outputs…
              </div>
            ) : preview.isError ? (
              <Alert variant="destructive">
                <TriangleAlertIcon />
                <AlertTitle>These blocks can't be extracted as they are</AlertTitle>
                <AlertDescription>{errorMessage(preview.error)}</AlertDescription>
              </Alert>
            ) : (
              <div className="flex flex-col gap-4">
                <IOList
                  title="Inputs"
                  empty="Nothing from the root: the module is self-contained."
                  items={preview.data.inputs}
                  hint="variables the module reads; the call passes"
                />
                <IOList
                  title="Outputs"
                  empty="Nothing in the root reads these blocks."
                  items={preview.data.outputs}
                  hint="what the root reads as module.<name>.<output>"
                />
                {(preview.data.providers.length > 0 || preview.data.dependsOn.length > 0) && (
                  <p className="text-xs text-muted-foreground">
                    {preview.data.providers.length > 0 && (
                      <>
                        Providers passed: <code className="font-mono">{preview.data.providers.join(", ")}</code>.{" "}
                      </>
                    )}
                    {preview.data.dependsOn.length > 0 && (
                      <>
                        The call depends on <code className="font-mono">{preview.data.dependsOn.join(", ")}</code>.
                      </>
                    )}
                  </p>
                )}
                {preview.data.moved.length > 0 && (
                  <Alert>
                    <TriangleAlertIcon />
                    <AlertTitle>
                      Adds {preview.data.moved.length} <code className="font-mono">moved</code> block
                      {preview.data.moved.length === 1 ? "" : "s"}
                    </AlertTitle>
                    <AlertDescription>
                      <p>
                        Deployed resources change address (for example{" "}
                        <code className="font-mono">{preview.data.moved[0].from}</code> →{" "}
                        <code className="font-mono">{preview.data.moved[0].to}</code>). The moved blocks tell
                        Terraform to move their state instead of destroying and recreating them: the next plan shows
                        only moves. Keep them until every environment has applied.
                      </p>
                    </AlertDescription>
                  </Alert>
                )}
              </div>
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={closeExtract}>
              Cancel
            </Button>
            <Button type="submit" disabled={!ready}>
              {busy ? <Spinner /> : <PackageIcon />}
              Extract
            </Button>
          </DialogFooter>
        </form>
    </>
  )
}

function address(n: WorkflowNode) {
  if (n.kind === "data") return `data.${n.type}.${n.name}`
  if (n.kind === "module") return `module.${n.name}`
  return `${n.type}.${n.name}`
}

function IOList({ title, items, empty, hint }: { title: string; items: ModuleIO[]; empty: string; hint: string }) {
  return (
    <section className="flex flex-col gap-1.5">
      <h3 className="text-xs font-medium text-muted-foreground">
        {title} <span className="font-normal">({items.length} — {hint})</span>
      </h3>
      {items.length === 0 ? (
        <p className="text-xs text-muted-foreground">{empty}</p>
      ) : (
        <ul className="flex flex-col divide-y rounded-md border text-xs">
          {items.map((io) => (
            <li key={io.name} className="flex flex-col gap-0.5 px-2.5 py-1.5">
              <div className="flex min-w-0 items-center gap-2 font-mono">
                <span className="font-medium">{io.name}</span>
                {io.type && <span className="text-muted-foreground">{io.type}</span>}
                {io.sensitive && <Badge variant="secondary">sensitive</Badge>}
                <ArrowRightIcon className="size-3 shrink-0 text-muted-foreground" />
                <span className="min-w-0 truncate text-muted-foreground" title={io.value}>
                  {io.value}
                </span>
              </div>
              <p className="text-muted-foreground">{io.description}</p>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function InlineModuleDialog() {
  const nodeId = useModuleRefactor((s) => s.inline)
  const node = useWorkflowStore((s) => (nodeId ? s.workflow?.nodes.find((n) => n.id === nodeId) : undefined))
  const [busy, setBusy] = useState(false)
  const close = () => useModuleRefactor.setState({ inline: null })

  const inline = async () => {
    const store = useWorkflowStore.getState()
    if (!store.workflow || !node) return
    setBusy(true)
    try {
      await store.flush()
      const updated = await api.inlineModule(store.workflow.id, node.id)
      store.setWorkflow(updated)
      store.select(null)
      if (useModuleView.getState().nodeId === node.id) useModuleView.getState().close()
      close()
      toast.success(`Inlined module.${node.name}`, {
        description: "Its blocks are back in the root; moved blocks keep anything already deployed.",
      })
    } catch (err) {
      toast.error("Couldn't inline the module", { description: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog open={!!nodeId && !!node} onOpenChange={(o) => !o && close()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="flex items-center gap-2">
            <PackageOpenIcon className="size-4 text-violet-500" />
            Inline module.{node?.name}?
          </AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="flex flex-col gap-2">
              <p>
                Its blocks move back to the root in place of the call: inputs become the values the call passes and
                outputs the expressions they expose.
              </p>
              <p>
                <code className="font-mono">moved</code> blocks keep anything already deployed, so the next plan only
                moves resources. The module's Terraform files and README are deleted.
              </p>
              <p>Only local modules called once, without count or for_each, can be inlined.</p>
            </div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={busy}
            onClick={(e) => {
              e.preventDefault()
              void inline()
            }}
          >
            {busy ? <Spinner /> : <PackageOpenIcon />}
            Inline module
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

import { useState } from "react"
import { PackageIcon } from "lucide-react"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
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
import type { ModuleScaffold } from "@/lib/api/types"

/** Same rule as the daemon (and Extract to module): snake_case, both the module call name and its folder. */
const MODULE_NAME = /^[a-z][a-z0-9_]*$/

interface NewModuleDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  workflowId: string
  /** Why modules/<name> can't be used (it exists, possibly as a pending file), or undefined. */
  taken: (dir: string) => string | undefined
  /** Adds the module's files (and the call when asked) as pending changes. */
  onCreate: (scaffold: ModuleScaffold, addCall: boolean) => void
}

/** "New module…": a local module folder with its standard files, added as pending changes. */
export function NewModuleDialog({ open, onOpenChange, ...props }: NewModuleDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">{open && <NewModuleForm {...props} onDone={() => onOpenChange(false)} />}</DialogContent>
    </Dialog>
  )
}

function NewModuleForm({
  workflowId,
  taken,
  onCreate,
  onDone,
}: Omit<NewModuleDialogProps, "open" | "onOpenChange"> & { onDone: () => void }) {
  const [name, setName] = useState("")
  const [addCall, setAddCall] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const trimmed = name.trim()
  const problem = !trimmed
    ? undefined
    : !MODULE_NAME.test(trimmed)
      ? "Use snake_case: lowercase letters, digits and underscores, starting with a letter."
      : taken(`modules/${trimmed}`)

  const create = async () => {
    if (!trimmed || problem || busy) return
    setBusy(true)
    setError(null)
    try {
      onCreate(await api.moduleScaffold(workflowId, trimmed), addCall)
      onDone()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <PackageIcon className="size-4 text-violet-500" />
          New module
        </DialogTitle>
        <DialogDescription>
          Creates a local module folder with versions.tf, variables.tf, outputs.tf, main.tf and a README. Nothing is
          saved until you apply your changes.
        </DialogDescription>
      </DialogHeader>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          void create()
        }}
      >
        <Field>
          <FieldLabel htmlFor="new-module-name">Module name</FieldLabel>
          <Input
            id="new-module-name"
            value={name}
            autoFocus
            spellCheck={false}
            autoComplete="off"
            className="font-mono"
            aria-invalid={!!problem}
            onChange={(e) => setName(e.target.value)}
          />
          <FieldDescription className={problem ? "text-destructive" : undefined}>
            {problem ??
              (trimmed ? (
                <>
                  Files in <code className="font-mono">modules/{trimmed}/</code>, called as{" "}
                  <code className="font-mono">module.{trimmed}</code>.
                </>
              ) : (
                "snake_case, e.g. queues or network."
              ))}
          </FieldDescription>
        </Field>
        <Field orientation="horizontal">
          <Checkbox id="new-module-call" checked={addCall} onCheckedChange={(v) => setAddCall(v === true)} />
          <FieldLabel htmlFor="new-module-call" className="font-normal">
            Call it from the root code (adds a <code className="font-mono">module</code> block)
          </FieldLabel>
        </Field>
        {error && <p className="text-xs text-destructive">{error}</p>}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onDone}>
            Cancel
          </Button>
          <Button type="submit" disabled={!trimmed || !!problem || busy}>
            {busy && <Spinner />} Create module
          </Button>
        </DialogFooter>
      </form>
    </>
  )
}

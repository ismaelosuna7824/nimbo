import { useState } from "react"
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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api/client"
import type { EnvironmentsView } from "@/lib/api/types"
import { changeEnvironments } from "./actions"
import { environmentNameError, environmentSources, newEnvironmentFiles, renamedFiles } from "./model"

const BASE = "__base"

/** Which environment dialog is open. */
export type EnvDialog = { kind: "new"; from: string } | { kind: "rename"; name: string } | { kind: "delete"; name: string } | null

/** The open dialog of the Environments tab; `onDone` gets the environment to select afterwards. */
export function EnvironmentDialogs({
  dialog,
  view,
  onClose,
  onDone,
}: {
  dialog: EnvDialog
  view: EnvironmentsView
  onClose: () => void
  onDone: (selected: string) => void
}) {
  return (
    <>
      <NewEnvironmentDialog
        key={dialog?.kind === "new" ? `new:${dialog.from}` : "new"}
        open={dialog?.kind === "new"}
        from={dialog?.kind === "new" ? dialog.from : ""}
        view={view}
        onClose={onClose}
        onDone={onDone}
      />
      <RenameEnvironmentDialog
        key={dialog?.kind === "rename" ? `rename:${dialog.name}` : "rename"}
        name={dialog?.kind === "rename" ? dialog.name : null}
        view={view}
        onClose={onClose}
        onDone={onDone}
      />
      <DeleteEnvironmentDialog name={dialog?.kind === "delete" ? dialog.name : null} view={view} onClose={onClose} onDone={onDone} />
    </>
  )
}

function NewEnvironmentDialog({
  open,
  from: initialFrom,
  view,
  onClose,
  onDone,
}: {
  open: boolean
  from: string
  view: EnvironmentsView
  onClose: () => void
  onDone: (selected: string) => void
}) {
  const [name, setName] = useState("")
  const [from, setFrom] = useState(initialFrom)
  const [touched, setTouched] = useState(false)
  const [pending, setPending] = useState(false)
  const existing = view.environments.map((e) => e.name)
  const error = environmentNameError(name, existing)
  const source = view.environments.find((e) => e.name === from)
  const files = source ? renamedFiles(source.files, source.name, name) : newEnvironmentFiles(view.sources, name)
  const kinds = [...new Set(environmentSources(view.sources).map((s) => (s.kind === "helm" ? "Helm chart" : s.kind === "compose" ? "compose project" : "Kustomize base")))]
  const plain = view.sources.filter((s) => !s.supported)

  const submit = async () => {
    setTouched(true)
    if (error || pending) return
    setPending(true)
    const ok = await changeEnvironments((id) => api.createEnvironment(id, { name, from: from || undefined }), {
      success: `Created ${name}`,
      failure: `Couldn't create ${name}`,
    })
    setPending(false)
    if (ok) onDone(name)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !pending && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{initialFrom ? `Duplicate ${initialFrom}` : "New environment"}</DialogTitle>
          <DialogDescription>
            {view.kind === "docker" ? (
              <>
                A compose override file plus an optional <span className="font-mono">.env.&lt;name&gt;</span> for variables, read
                after the base files.
              </>
            ) : (
              <>
                Created for every {kinds.join(" and ") || "Kustomize base and Helm chart"} in the workspace.
                {plain.length > 0 && " Plain manifests stay the same in every environment."}
              </>
            )}
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <div className="grid gap-1.5">
            <Label htmlFor="env-new-name">Name</Label>
            <Input
              id="env-new-name"
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value.trim())}
              onBlur={() => name && setTouched(true)}
              placeholder="staging"
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
              aria-invalid={touched && !!error}
              data-testid="env-new-name"
            />
            {touched && error && <p className="text-xs text-destructive">{error}</p>}
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="env-new-from">Start from</Label>
            <Select value={from || BASE} onValueChange={(v) => setFrom(v === BASE ? "" : v)}>
              <SelectTrigger id="env-new-from" className="w-full" data-testid="env-new-from">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={BASE} data-testid="env-new-from-base">
                  Base
                </SelectItem>
                {view.environments.map((e) => (
                  <SelectItem key={e.name} value={e.name} data-testid={`env-new-from-${e.name}`}>
                    {e.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              {source
                ? view.kind === "docker"
                  ? `A copy of ${source.name}'s override file and variables.`
                  : `A copy of ${source.name}: names that mention it (namespaces, hosts) are renamed.`
                : "Empty: it renders exactly the base until you override something."}
            </p>
          </div>
          {files.length > 0 && (
            <div className="grid gap-1">
              <span className="text-xs text-muted-foreground">Creates</span>
              <ul className="grid gap-0.5 rounded-md bg-muted/50 px-2 py-1.5">
                {files.map((f) => (
                  <li key={f} className="truncate font-mono text-[11px]" title={f}>
                    {f}
                  </li>
                ))}
              </ul>
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose} disabled={pending}>
              Cancel
            </Button>
            <Button type="submit" disabled={pending || (touched && !!error)} data-testid="env-new-submit">
              {pending && <Spinner />}
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RenameEnvironmentDialog({
  name: current,
  view,
  onClose,
  onDone,
}: {
  name: string | null
  view: EnvironmentsView
  onClose: () => void
  onDone: (selected: string) => void
}) {
  const [name, setName] = useState(current ?? "")
  const [pending, setPending] = useState(false)
  const env = view.environments.find((e) => e.name === current)
  const error = environmentNameError(
    name,
    view.environments.map((e) => e.name),
    current ?? undefined,
  )
  const unchanged = name === current

  const submit = async () => {
    if (!current || error || unchanged || pending) return
    setPending(true)
    const ok = await changeEnvironments((id) => api.renameEnvironment(id, current, name), {
      success: `Renamed ${current} to ${name}`,
      failure: `Couldn't rename ${current}`,
    })
    setPending(false)
    if (ok) onDone(name)
  }

  return (
    <Dialog open={current !== null} onOpenChange={(o) => !o && !pending && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Rename {current}</DialogTitle>
          <DialogDescription>
            Its files move to the new name{env?.active ? "; it stays the active environment" : ""}.
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <div className="grid gap-1.5">
            <Label htmlFor="env-rename-input">New name</Label>
            <Input
              id="env-rename-input"
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value.trim())}
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
              aria-invalid={!!error}
              data-testid="env-rename-input"
            />
            {error && <p className="text-xs text-destructive">{error}</p>}
          </div>
          {env && env.files.length > 0 && !unchanged && !error && (
            <ul className="grid gap-0.5 rounded-md bg-muted/50 px-2 py-1.5">
              {env.files.map((f) => {
                const to = renamedFiles([f], env.name, name)[0]
                return (
                  <li key={f} className="truncate font-mono text-[11px]" title={`${f} → ${to}`}>
                    {f} <span className="text-muted-foreground">→</span> {to}
                  </li>
                )
              })}
            </ul>
          )}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose} disabled={pending}>
              Cancel
            </Button>
            <Button type="submit" disabled={pending || !!error || unchanged} data-testid="env-rename-submit">
              {pending && <Spinner />}
              Rename
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function DeleteEnvironmentDialog({
  name,
  view,
  onClose,
  onDone,
}: {
  name: string | null
  view: EnvironmentsView
  onClose: () => void
  onDone: (selected: string) => void
}) {
  const [pending, setPending] = useState(false)
  const env = view.environments.find((e) => e.name === name)

  const remove = async () => {
    if (!name || pending) return
    setPending(true)
    const ok = await changeEnvironments((id) => api.deleteEnvironment(id, name), {
      success: `Deleted ${name}`,
      failure: `Couldn't delete ${name}`,
    })
    setPending(false)
    if (ok) onDone("")
  }

  return (
    <AlertDialog open={name !== null} onOpenChange={(o) => !o && !pending && onClose()}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>Delete environment “{name}”?</AlertDialogTitle>
          <AlertDialogDescription>
            {env && env.files.length > 0 ? "Deletes its files; the base and other environments stay." : "The base and other environments stay."}
            {env?.active && " Base becomes the active environment."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {env && env.files.length > 0 && (
          <ul className="grid gap-0.5 rounded-md bg-muted/50 px-2 py-1.5">
            {env.files.map((f) => (
              <li key={f} className="truncate font-mono text-[11px]" title={f}>
                {f}
              </li>
            ))}
          </ul>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={pending}
            onClick={(e) => {
              e.preventDefault()
              void remove()
            }}
            data-testid="env-delete-confirm"
          >
            {pending && <Spinner />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

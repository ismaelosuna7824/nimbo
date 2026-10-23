import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { FilePlus2Icon } from "lucide-react"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api/client"
import type { FileTemplate } from "@/lib/api/types"
import { cn } from "@/lib/utils"

interface NewFromTemplateDialogProps {
  /** The folder the file goes in ("" = the workflow root); null: closed. */
  dir: string | null
  onClose: () => void
  /** Template kinds offered (a Kubernetes workspace also builds images: Dockerfiles and .dockerignore too). */
  kinds: FileTemplate["kind"][]
  /** Compose files a snippet (a service) can go into. */
  composeFiles: string[]
  /** Why a new file can't have this path (it exists, …), or undefined. */
  validate: (path: string) => string | undefined
  onCreate: (path: string, content: string) => void
  /** Adds a snippet (a compose service) under `services:` of a compose file. */
  onInsert: (composeFile: string, snippet: string) => void
}

/** "New from template…": a Dockerfile per stack, .dockerignore, .env, a compose service, nginx.conf, Kustomize and Helm files. */
export function NewFromTemplateDialog({ dir, onClose, ...props }: NewFromTemplateDialogProps) {
  return (
    <Dialog open={dir !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-xl">{dir !== null && <NewFromTemplateForm dir={dir} onDone={onClose} {...props} />}</DialogContent>
    </Dialog>
  )
}

function NewFromTemplateForm({
  dir,
  kinds,
  composeFiles,
  validate,
  onCreate,
  onInsert,
  onDone,
}: Omit<NewFromTemplateDialogProps, "dir" | "onClose"> & { dir: string; onDone: () => void }) {
  const templates = useQuery({ queryKey: ["file-templates"], queryFn: api.fileTemplates, staleTime: Infinity })
  const offered = useMemo(
    () => (templates.data ?? []).filter((t) => kinds.includes(t.kind) && (!t.snippet || composeFiles.length > 0)),
    [templates.data, kinds, composeFiles.length],
  )
  const groups = useMemo(() => {
    const out = new Map<string, FileTemplate[]>()
    for (const t of offered) out.set(t.group, [...(out.get(t.group) ?? []), t])
    return [...out]
  }, [offered])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const selected = offered.find((t) => t.id === selectedId) ?? offered[0]
  const [names, setNames] = useState<Record<string, string>>({})
  const defaultPath = selected ? (selected.snippet ? composeFiles[0] : dir ? `${dir}/${selected.fileName}` : selected.fileName) : ""
  const path = (selected && names[selected.id]) ?? defaultPath
  const problem = !selected ? undefined : selected.snippet ? (composeFiles.includes(path) ? undefined : "Pick one of the compose files") : !path.trim() ? "Enter a path" : validate(path.trim())

  const create = () => {
    if (!selected || problem) return
    if (selected.snippet) onInsert(path, selected.content)
    else onCreate(path.trim(), selected.content)
    onDone()
  }

  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <FilePlus2Icon className="size-4 text-sky-500" />
          New from template
        </DialogTitle>
        <DialogDescription>
          Starting points that follow the best practices Nimbo checks. Nothing is saved until you apply your changes.
        </DialogDescription>
      </DialogHeader>
      <form
        className="flex min-w-0 flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          create()
        }}
      >
        {templates.isPending ? (
          <div className="flex justify-center py-6">
            <Spinner />
          </div>
        ) : templates.isError ? (
          <p className="text-xs text-destructive">{errorMessage(templates.error)}</p>
        ) : (
          <div role="listbox" aria-label="Templates" className="max-h-72 overflow-y-auto rounded-md border p-1">
            {groups.map(([group, list]) => (
              <div key={group} role="group" aria-label={group} className="pb-1">
                <div className="px-2 pt-1.5 pb-1 text-[10px] font-medium tracking-wide text-muted-foreground uppercase">{group}</div>
                {list.map((t) => (
                  <button
                    key={t.id}
                    type="button"
                    role="option"
                    aria-selected={t.id === selected?.id}
                    onClick={() => setSelectedId(t.id)}
                    className={cn(
                      "flex w-full min-w-0 flex-col items-start rounded-sm px-2 py-1 text-left text-xs hover:bg-accent",
                      t.id === selected?.id && "bg-primary/15 hover:bg-primary/20",
                    )}
                  >
                    <span className="font-medium">{t.label}</span>
                    <span className="truncate text-[11px] text-muted-foreground">{t.description}</span>
                  </button>
                ))}
              </div>
            ))}
          </div>
        )}
        {selected && (
          <Field>
            <FieldLabel htmlFor="new-from-template-path">{selected.snippet ? "Add to" : "File"}</FieldLabel>
            {selected.snippet ? (
              <select
                id="new-from-template-path"
                className="h-8 rounded-md border bg-transparent px-2 font-mono text-xs"
                value={path}
                onChange={(e) => setNames((n) => ({ ...n, [selected.id]: e.target.value }))}
              >
                {composeFiles.map((f) => (
                  <option key={f} value={f}>
                    {f}
                  </option>
                ))}
              </select>
            ) : (
              <Input
                id="new-from-template-path"
                value={path}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                aria-invalid={!!problem}
                onChange={(e) => setNames((n) => ({ ...n, [selected.id]: e.target.value }))}
              />
            )}
            <FieldDescription className={problem ? "text-destructive" : undefined}>
              {problem ?? (selected.snippet ? "The service is added at the end of services:." : "Relative to the workflow's files.")}
            </FieldDescription>
          </Field>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onDone}>
            Cancel
          </Button>
          <Button type="submit" disabled={!selected || !!problem}>
            {selected?.snippet ? "Add service" : "Create file"}
          </Button>
        </DialogFooter>
      </form>
    </>
  )
}

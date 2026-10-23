// Inspector fields gated by GET …/kubernetes/fields: an editable path renders its input, a read-only one (a Helm
// template expression, a value several keys feed) its value, the reason and a link to where it is written.

import { Code2Icon, LockIcon } from "lucide-react"
import { createContext, useContext } from "react"
import { Field } from "@/features/docker/inspector/fields"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import type { KubeField, KubeNode } from "@/lib/api/types"
import { fieldAccess, valuesHint, type FieldAccess } from "./paths"

export interface KubeFieldsValue {
  node: KubeNode
  /** Undefined while loading (the object's own editability applies meanwhile). */
  fields?: KubeField[]
}

export const KubeFieldsContext = createContext<KubeFieldsValue | null>(null)

export function useFieldAccess(path: string): FieldAccess {
  const ctx = useContext(KubeFieldsContext)!
  return fieldAccess(ctx.fields, path, ctx.node)
}

/** "Open in code" at the field's line (or the object's document). */
export function OpenInCode({ field, node, label = "Open in code" }: { field?: KubeField; node: KubeNode; label?: string }) {
  const file = field?.file ?? node.file
  if (!file) return null
  const line = field?.file ? field.line : node.line
  return (
    <button type="button" className="inline-flex items-center gap-1 self-start text-[11px] text-primary hover:underline" onClick={() => revealFiles("", file, line)}>
      <Code2Icon className="size-3" />
      {label}
      <span className="font-mono text-muted-foreground">
        {file}
        {line ? `:${line}` : ""}
      </span>
    </button>
  )
}

/** Why a path is read-only, with its value and where to change it. */
export function ReadOnlyNote({ access, display }: { access: FieldAccess; display?: string }) {
  const { node } = useContext(KubeFieldsContext)!
  return (
    <div className="flex flex-col gap-1 rounded-md border border-dashed bg-muted/40 px-2 py-1.5" data-slot="read-only">
      {display !== undefined && <span className="truncate font-mono text-xs">{display || "—"}</span>}
      <span className="flex items-start gap-1 text-[11px] text-muted-foreground">
        <LockIcon className="mt-0.5 size-3 shrink-0" />
        {access.reason ?? "Read-only here"}
      </span>
      <OpenInCode field={access.field} node={node} />
    </div>
  )
}

/** A labelled field at `path`: its input when the path can be written, else the read-only note. */
export function KField({
  path,
  label,
  display,
  hint,
  htmlFor,
  children,
}: {
  path: string
  label: string
  /** The current value as text, shown when read-only. */
  display?: string
  hint?: string
  htmlFor?: string
  children: React.ReactNode
}) {
  const access = useFieldAccess(path)
  const values = valuesHint(access.field)
  return (
    <div data-testid={`k8s-inspector-field-${path}`} data-editable={access.editable} className="flex min-w-0 flex-col gap-0.5">
      <Field label={label} htmlFor={access.editable ? htmlFor : undefined} hint={hint}>
        {access.editable ? children : <ReadOnlyNote access={access} display={display} />}
      </Field>
      {values && <p className="font-mono text-[10.5px] text-muted-foreground">{values}</p>}
    </div>
  )
}

/** A list section at `path`: its editor when writable, else the read-only note above the items as text. */
export function KList({ path, display, children }: { path: string; display: string; children: React.ReactNode }) {
  const access = useFieldAccess(path)
  const values = valuesHint(access.field)
  return (
    <div data-testid={`k8s-inspector-field-${path}`} data-editable={access.editable} className="flex min-w-0 flex-col gap-1">
      {access.editable ? children : <ReadOnlyNote access={access} display={display} />}
      {values && <p className="font-mono text-[10.5px] text-muted-foreground">{values}</p>}
    </div>
  )
}

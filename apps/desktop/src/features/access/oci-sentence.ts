// Pure helpers of the OCI policy sentence builder: the statement grammar
// (https://docs.oracle.com/en-us/iaas/Content/Identity/Concepts/policysyntax.htm), mirrored from
// backend/internal/access/oci.go so the editor can preview and validate sentences before saving.

import type { OCIStatement, WorkflowNode } from "@/lib/api/types"

export const OCI_SUBJECT_TYPES = ["group", "dynamic-group", "any-user", "any-group", "service"] as const
export const OCI_VERBS = ["inspect", "read", "use", "manage"] as const

/** Subject types that name no one ("any-user", "any-group"). */
export function takesNoNames(subjectType?: string) {
  return subjectType === "any-user" || subjectType === "any-group"
}

/** A new statement: a group reading something in a compartment. */
export function blankStatement(): OCIStatement {
  return { editable: true, subjectType: "group", subjects: [], verb: "read", resourceType: "", location: "compartment", compartment: "" }
}

/** Index just past the "}" closing the `${` at text[i], or -1 (strings inside are skipped). */
function interpEnd(text: string, i: number): number {
  let depth = 0
  for (let j = i + 1; j < text.length; j++) {
    const c = text[j]
    if (c === "{") depth++
    else if (c === "}") {
      depth--
      if (depth === 0) return j + 1
    } else if (c === '"') {
      for (j++; j < text.length && text[j] !== '"'; j++) if (text[j] === "\\") j++
      if (j >= text.length) return -1
    }
  }
  return -1
}

interface Token {
  s: string
  start: number
}

/** Words of a statement: "," is a word of its own; ${...}, '...', "..." and {...} never split. */
export function tokenize(text: string): Token[] | null {
  const out: Token[] = []
  let start = -1
  const flush = (end: number) => {
    if (start >= 0) out.push({ s: text.slice(start, end), start })
    start = -1
  }
  let i = 0
  while (i < text.length) {
    const c = text[i]
    if (/\s/.test(c)) {
      flush(i)
      i++
      continue
    }
    if (c === ",") {
      flush(i)
      out.push({ s: ",", start: i })
      i++
      continue
    }
    if (start < 0) start = i
    if (text.startsWith("${", i)) {
      const end = interpEnd(text, i)
      if (end < 0) return null
      i = end
    } else if (c === "'" || c === '"') {
      const j = text.indexOf(c, i + 1)
      if (j < 0) return null
      i = j + 1
    } else if (c === "{") {
      let depth = 0
      let j = i
      for (; j < text.length; j++) {
        if (text[j] === "{") depth++
        else if (text[j] === "}" && --depth === 0) break
      }
      if (j >= text.length) return null
      i = j + 1
    } else i++
  }
  flush(text.length)
  return out
}

/** Splits a comma-separated subject list ("a, id ocid1…, ${x}") without breaking ${...} or quotes. */
export function splitSubjects(text: string): string[] {
  const toks = tokenize(text)
  if (!toks) return [text.trim()].filter(Boolean)
  const out: string[] = []
  let cur: string[] = []
  for (const t of toks) {
    if (t.s === ",") {
      if (cur.length) out.push(cur.join(" "))
      cur = []
    } else cur.push(t.s)
  }
  if (cur.length) out.push(cur.join(" "))
  return out
}

const is = (t: Token | undefined, word: string) => t !== undefined && t.s.toLowerCase() === word

/** Parses a policy sentence; `error` explains what the visual editor can't show. */
export function parseStatement(text: string): { statement: OCIStatement } | { error: string } {
  const toks = tokenize(text.trim())
  if (!toks) return { error: "Unbalanced quotes, braces or ${...}." }
  if (toks.length === 0) return { error: "Empty statement." }
  const first = toks[0].s.toLowerCase()
  if (first === "endorse" || first === "admit" || first === "define") return { error: `Cross-tenancy statements (${toks[0].s}) are kept as written.` }
  if (first !== "allow") return { error: "Not an Allow statement." }
  let i = 1
  const next = () => toks[i++]
  const subjectType = next()?.s.toLowerCase()
  if (!subjectType || !(OCI_SUBJECT_TYPES as readonly string[]).includes(subjectType))
    return { error: "Statements start with Allow group, dynamic-group, any-user, any-group or service." }
  const subjects: string[] = []
  let cur: string[] = []
  for (;;) {
    const t = next()
    if (!t) return { error: 'Missing "to <verb>".' }
    if (is(t, "to")) break
    if (t.s === ",") {
      if (!cur.length) return { error: "Empty subject in the list." }
      subjects.push(cur.join(" "))
      cur = []
    } else cur.push(t.s)
  }
  if (cur.length) subjects.push(cur.join(" "))
  else if (subjects.length) return { error: "Empty subject in the list." }
  if (takesNoNames(subjectType) !== (subjects.length === 0))
    return { error: takesNoNames(subjectType) ? `${subjectType} takes no names.` : `Missing the ${subjectType} name.` }
  const verb = next()
  if (verb?.s.startsWith("{")) return { error: "Permission lists ({PERMISSION, ...}) are kept as written." }
  if (!verb || !(OCI_VERBS as readonly string[]).includes(verb.s.toLowerCase())) return { error: "Unknown verb: use inspect, read, use or manage." }
  const resource = next()
  if (!resource || is(resource, "in")) return { error: "Missing the resource-type." }
  if (resource.s.startsWith("{")) return { error: "Permission lists ({PERMISSION, ...}) are kept as written." }
  if (!is(next(), "in")) return { error: 'Missing "in <location>" after the resource-type.' }
  const loc = next()
  const statement: OCIStatement = {
    editable: true,
    subjectType,
    subjects,
    verb: verb.s.toLowerCase(),
    resourceType: resource.s,
    location: "tenancy",
  }
  if (is(loc, "compartment")) {
    const c = next()
    if (!c || is(c, "where")) return { error: "Missing the compartment name." }
    statement.location = "compartment"
    statement.compartment = c.s
    if (is(c, "id")) {
      const id = next()
      if (!id) return { error: "Missing the compartment OCID." }
      statement.compartment = `id ${id.s}`
    }
  } else if (!is(loc, "tenancy")) return { error: "The location must be tenancy or compartment <name>." }
  const where = next()
  if (where) {
    if (!is(where, "where") || i >= toks.length) return { error: "Unexpected words after the location." }
    statement.condition = text.trim().slice(toks[i].start).trim()
  }
  return { statement }
}

/** The sentence of a statement ("Allow group a to read buckets in compartment b where …"). */
export function formatStatement(st: OCIStatement): string {
  const subjects = (st.subjects ?? []).map((s) => s.trim()).filter(Boolean)
  const who = [st.subjectType || "group", subjects.join(", ")].filter(Boolean).join(" ")
  const where = st.location === "tenancy" ? "tenancy" : `compartment ${st.compartment?.trim() || "…"}`
  const cond = st.condition?.trim() ? ` where ${st.condition.trim()}` : ""
  return `Allow ${who} to ${st.verb || "…"} ${st.resourceType?.trim() || "…"} in ${where}${cond}`
}

/** What keeps a statement from being saved (empty when it can be). */
export function statementProblems(st: OCIStatement): string[] {
  if (!st.editable) return []
  const out: string[] = []
  const subjects = (st.subjects ?? []).filter((s) => s.trim())
  if (!takesNoNames(st.subjectType) && subjects.length === 0) out.push(`Choose the ${st.subjectType ?? "group"}.`)
  if (!st.resourceType?.trim()) out.push("Choose what it applies to (a resource-type).")
  else if (/[\s{},]/.test(st.resourceType.trim())) out.push("A resource-type is one word, like object-family.")
  if (st.location === "compartment" && !st.compartment?.trim()) out.push("Choose the compartment.")
  if (out.length === 0) {
    // The sentence must read back the same: no stray keywords or commas in the names.
    const back = parseStatement(formatStatement(st))
    if ("error" in back) out.push(back.error)
    else if (!sameStatement(back.statement, st)) out.push("A name contains a keyword (to, in, where) or a comma: quote it or link it.")
  }
  return out
}

function norm(st: OCIStatement) {
  return {
    subjectType: st.subjectType ?? "",
    subjects: (st.subjects ?? []).map((s) => s.trim()).filter(Boolean),
    verb: st.verb ?? "",
    resourceType: st.resourceType?.trim() ?? "",
    location: st.location ?? "",
    compartment: st.location === "tenancy" ? "" : (st.compartment?.trim() ?? ""),
    condition: st.condition?.trim() ?? "",
  }
}

/** Whether two statements say the same (whitespace and the source aside). */
export function sameStatement(a: OCIStatement, b: OCIStatement): boolean {
  if (a.editable !== b.editable) return false
  if (!a.editable) return a.source === b.source
  return JSON.stringify(norm(a)) === JSON.stringify(norm(b))
}

/** Whether the edited list differs from the saved one (order included). */
export function statementsChanged(saved: OCIStatement[], draft: OCIStatement[]): boolean {
  return saved.length !== draft.length || saved.some((s, i) => s.source !== draft[i].source || !sameStatement(s, draft[i]))
}

/** Moves item `from` to `to` (both clamped). */
export function moveItem<T>(list: T[], from: number, to: number): T[] {
  const out = list.slice()
  if (from < 0 || from >= out.length) return out
  const [item] = out.splice(from, 1)
  out.splice(Math.max(0, Math.min(to, out.length)), 0, item)
  return out
}

/** A named identity or compartment of the workflow to pick in a sentence. */
export interface NamedNode {
  nodeId: string
  address: string
  /** The `name` attribute when it is a constant ("app-developers"). */
  name?: string
  /** Template interpolation of its name ("${oci_identity_group.dev.name}"). */
  reference: string
}

/** Terraform types whose `name` is used for a subject type / the compartment. */
const NAMED_TYPES: Record<string, string> = {
  group: "oci_identity_group",
  "dynamic-group": "oci_identity_dynamic_group",
  compartment: "oci_identity_compartment",
}

/** The workflow's groups, dynamic groups or compartments (`of`: "group" | "dynamic-group" | "compartment"). */
export function namedNodes(nodes: WorkflowNode[], of: string): NamedNode[] {
  const type = NAMED_TYPES[of]
  if (!type) return []
  return nodes
    .filter((n) => n.kind === "resource" && n.type === type)
    .map((n) => {
      const address = `${n.type}.${n.name}`
      const attr = n.body.attrs.find((a) => a.name === "name")
      const name = typeof attr?.value.literal === "string" ? attr.value.literal : undefined
      return { nodeId: n.id, address, name, reference: `\${${address}.name}` }
    })
}

/** What a picked node inserts: its constant name when it has one (policies compare names), else the reference. */
export function namedValue(n: NamedNode, prefer: "name" | "reference" = "name"): string {
  return prefer === "name" && n.name ? n.name : n.reference
}

/**
 * The condition with a value picked with Link to… appended as a quoted OCI string
 * (`target.key.id =` + oci_kms_key.k.id → `target.key.id = '${oci_kms_key.k.id}'`).
 */
export function appendConditionValue(condition: string | undefined, expr: string): string {
  const cur = (condition ?? "").trimEnd()
  return `${cur}${cur ? " " : ""}'\${${expr}}'`
}

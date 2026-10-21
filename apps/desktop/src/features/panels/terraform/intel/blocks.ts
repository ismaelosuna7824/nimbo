// What a block body accepts: provider schemas for resource, data and
// provider blocks (nested blocks included), Terraform's own blocks, meta-
// arguments, and a module call's input variables.

import type { CtyType, ResourceSchema, SchemaBlock } from "@/lib/api/types"
import { loadSchema } from "./catalog"
import type { Block } from "./scan"
import type { ModuleSymbols, SymbolIndex } from "./symbols"

export interface ItemSpec {
  name: string
  kind: "attr" | "block"
  /** Schema type of an attribute. */
  type?: CtyType
  /** Type shown for Terraform's own arguments ("bool", "list(reference)"). */
  typeText?: string
  required?: boolean
  computed?: boolean
  deprecated?: boolean
  sensitive?: boolean
  description?: string
  /** Allowed values (curated in the catalog, or fixed by Terraform). */
  enum?: string[]
  nesting?: string
  minItems?: number
  maxItems?: number
  /** Schema of a nested block (its required arguments go into the snippet). */
  block?: SchemaBlock
  /** Placeholder labels of a labeled block (`provisioner "local-exec"`). */
  labels?: string[]
  /** Meta-arguments and Terraform's own blocks. */
  meta?: boolean
}

export interface BodySpec {
  /** "aws_sqs_queue", "lifecycle", "module vpc": shown in hover titles. */
  owner: string
  items: ItemSpec[]
  docsUrl?: string
  schema?: ResourceSchema
}

const attr = (name: string, typeText: string, description: string, extra: Partial<ItemSpec> = {}): ItemSpec => ({
  name,
  kind: "attr",
  typeText,
  description,
  meta: true,
  ...extra,
})
const block = (name: string, description: string, extra: Partial<ItemSpec> = {}): ItemSpec => ({
  name,
  kind: "block",
  description,
  meta: true,
  ...extra,
})

const CONDITION = [
  attr("condition", "bool", "Expression that must be true.", { required: true }),
  attr("error_message", "string", "Message shown when the condition is false.", { required: true }),
]

const LIFECYCLE: ItemSpec[] = [
  attr("create_before_destroy", "bool", "Create the replacement before destroying this object.", { enum: ["true", "false"] }),
  attr("prevent_destroy", "bool", "Reject any plan that would destroy this object.", { enum: ["true", "false"] }),
  attr("ignore_changes", "list(attribute)", "Attributes whose changes are ignored when planning updates (or `all`)."),
  attr("replace_triggered_by", "list(reference)", "Replace this object when any of these references change."),
  block("precondition", "A condition checked before the object is planned.", { nesting: "list" }),
  block("postcondition", "A condition checked after the object is planned or applied.", { nesting: "list" }),
]

const CONNECTION: ItemSpec[] = [
  attr("type", "string", "Connection type.", { enum: ["ssh", "winrm"] }),
  attr("host", "string", "Address of the resource to connect to.", { required: true }),
  attr("user", "string", "User to connect as."),
  attr("password", "string", "Password for the connection."),
  attr("private_key", "string", "Contents of an SSH private key."),
  attr("port", "number", "Port to connect to."),
  attr("timeout", "string", 'Time to wait for the connection, e.g. "5m".'),
  attr("agent", "bool", "Use ssh-agent for authentication."),
  attr("bastion_host", "string", "Bastion host to connect through."),
]

const PROVISIONERS: Record<string, ItemSpec[]> = {
  "local-exec": [
    attr("command", "string", "Command to run on the machine running Terraform.", { required: true }),
    attr("working_dir", "string", "Working directory of the command."),
    attr("interpreter", "list(string)", "Interpreter and its arguments."),
    attr("environment", "map(string)", "Environment variables of the command."),
    attr("quiet", "bool", "Do not print the command."),
  ],
  "remote-exec": [
    attr("inline", "list(string)", "Commands to run on the remote resource."),
    attr("script", "string", "Local script copied and run on the remote resource."),
    attr("scripts", "list(string)", "Local scripts copied and run in order."),
  ],
  file: [
    attr("source", "string", "Local file or directory to copy."),
    attr("content", "string", "Content to copy to the destination."),
    attr("destination", "string", "Path on the remote resource.", { required: true }),
  ],
}
const PROVISIONER_COMMON: ItemSpec[] = [
  attr("when", "keyword", "Run at creation (default) or at destroy time.", { enum: ["create", "destroy"] }),
  attr("on_failure", "keyword", "Fail the apply (default) or continue on failure.", { enum: ["fail", "continue"] }),
  block("connection", "How to reach the resource.", { maxItems: 1, nesting: "single" }),
]

const RESOURCE_META: ItemSpec[] = [
  attr("count", "number", "Number of instances to create; reference them with `count.index`."),
  attr("for_each", "map or set(string)", "One instance per element; reference them with `each.key` and `each.value`."),
  attr("depends_on", "list(reference)", "Explicit dependencies Terraform can't infer from references."),
  attr("provider", "reference", "Provider configuration to use, e.g. `aws.west`."),
  block("lifecycle", "Lifecycle customizations of the resource.", { maxItems: 1, nesting: "single" }),
]
const RESOURCE_ONLY_META: ItemSpec[] = [
  block("provisioner", "Actions run on the local or remote machine after creation or before destruction.", {
    labels: ["local-exec"],
    nesting: "list",
  }),
  block("connection", "Default connection for provisioners.", { maxItems: 1, nesting: "single" }),
]

const VARIABLE: ItemSpec[] = [
  attr("type", "type", "Type constraint of the value."),
  attr("description", "string", "What the variable is for (shown in docs and prompts)."),
  attr("default", "any", "Value used when none is given; without it the variable is required."),
  attr("sensitive", "bool", "Hide the value in plan and apply output.", { enum: ["true", "false"] }),
  attr("nullable", "bool", "Whether null is an acceptable value (default true).", { enum: ["true", "false"] }),
  attr("ephemeral", "bool", "Available during the run only, never stored in state or plan.", { enum: ["true", "false"] }),
  block("validation", "A rule the value must follow.", { nesting: "list" }),
]

const OUTPUT: ItemSpec[] = [
  attr("value", "any", "Value of the output.", { required: true }),
  attr("description", "string", "What the output is."),
  attr("sensitive", "bool", "Hide the value in CLI output.", { enum: ["true", "false"] }),
  attr("ephemeral", "bool", "Only pass the value between modules during the run.", { enum: ["true", "false"] }),
  attr("depends_on", "list(reference)", "Explicit dependencies."),
  block("precondition", "A condition checked before the output is evaluated.", { nesting: "list" }),
]

const MODULE_META: ItemSpec[] = [
  attr("source", "string", "Where the module comes from: a local path (./modules/x), a registry address or a URL.", {
    required: true,
  }),
  attr("version", "string", 'Version constraint of a registry module, e.g. "~> 5.0".'),
  attr("providers", "map(provider)", "Provider configurations passed to the module."),
  attr("count", "number", "Number of module instances."),
  attr("for_each", "map or set(string)", "One module instance per element."),
  attr("depends_on", "list(reference)", "Explicit dependencies."),
]

const TERRAFORM: ItemSpec[] = [
  attr("required_version", "string", 'Terraform versions this configuration supports, e.g. ">= 1.5".'),
  block("required_providers", "Providers this module needs, with their source and version.", { maxItems: 1, nesting: "single" }),
  block("backend", "Where state is stored.", { labels: ["s3"], maxItems: 1 }),
  block("cloud", "HCP Terraform settings.", { maxItems: 1, nesting: "single" }),
  attr("experiments", "list(keyword)", "Experimental language features to enable."),
  block("provider_meta", "Module metadata passed to a provider.", { labels: ["aws"] }),
]

const OWN_BODIES: Record<string, ItemSpec[]> = {
  variable: VARIABLE,
  output: OUTPUT,
  terraform: TERRAFORM,
  moved: [
    attr("from", "address", "Previous address of the object.", { required: true }),
    attr("to", "address", "New address of the object.", { required: true }),
  ],
  import: [
    attr("to", "address", "Resource address to import into.", { required: true }),
    attr("id", "string", "Cloud ID of the existing object."),
    attr("identity", "object", "Identity of the existing object (instead of id)."),
    attr("provider", "reference", "Provider configuration to import with."),
    attr("for_each", "map or set", "Import several objects."),
  ],
  removed: [
    attr("from", "address", "Address of the resource or module removed from the configuration.", { required: true }),
    block("lifecycle", "Whether to destroy the object or only forget it.", { maxItems: 1, nesting: "single" }),
    block("provisioner", "Destroy-time provisioner.", { labels: ["local-exec"] }),
  ],
  check: [
    block("assert", "A condition checked at the end of every plan and apply.", { nesting: "list" }),
    block("data", "A data source scoped to this check.", { labels: ["aws_x", "name"] }),
  ],
  validation: CONDITION,
  precondition: CONDITION,
  postcondition: CONDITION,
  assert: CONDITION,
  connection: CONNECTION,
  dynamic: [
    attr("for_each", "collection", "One nested block per element.", { required: true }),
    attr("iterator", "name", "Name of the iterator (defaults to the block type)."),
    attr("labels", "list(string)", "Labels of each generated block."),
    block("content", "Body of each generated block.", { maxItems: 1, nesting: "single", minItems: 1 }),
  ],
}

const REMOVED_LIFECYCLE = [attr("destroy", "bool", "Destroy the object (true) or only remove it from state (false).", { required: true, enum: ["true", "false"] })]

/** Items of a schema block. */
export function schemaItems(b: SchemaBlock): ItemSpec[] {
  return [
    ...b.attributes.map<ItemSpec>((a) => ({
      name: a.name,
      kind: "attr",
      type: a.type,
      required: a.required,
      computed: a.computed && !a.optional && !a.required,
      deprecated: a.deprecated,
      sensitive: a.sensitive,
      description: a.description,
      enum: a.enum,
    })),
    ...b.blocks.map<ItemSpec>((nb) => ({
      name: nb.name,
      kind: "block",
      nesting: nb.nesting,
      minItems: nb.minItems,
      maxItems: nb.maxItems,
      required: (nb.minItems ?? 0) > 0,
      block: nb.block,
    })),
  ]
}

export interface BodyEnv {
  symbols: SymbolIndex
  /** The module directory of the file. */
  module: ModuleSymbols
  text: string
}

/** The schema-driven body of a top-level resource/data/provider block. */
async function topSchema(top: Block): Promise<ResourceSchema | null> {
  const label = top.labels[0]?.text
  if (!label) return null
  if (top.type === "resource" || top.type === "data" || top.type === "ephemeral") {
    return loadSchema(top.type === "data" ? "data" : "resource", label).catch(() => null)
  }
  if (top.type === "provider") return loadSchema("provider", label).catch(() => null)
  return null
}

/** What the innermost block of path accepts; null for free-form bodies (locals, backend config). */
export async function bodySpec(path: Block[], env: BodyEnv): Promise<BodySpec | null> {
  if (path.length === 0) return null
  const top = path[0]
  const label = top.labels[0]?.text ?? ""

  if (top.type === "module") {
    if (path.length > 1) return null
    const call = env.module.modules.get(label)
    const callee = call ? env.symbols.callee(env.module.dir, call) : null
    const inputs: ItemSpec[] = callee
      ? [...callee.variables.values()].map((v) => ({
          name: v.name,
          kind: "attr",
          typeText: v.type ?? "any",
          required: v.default === undefined,
          description: v.description,
          sensitive: v.sensitive,
        }))
      : []
    return { owner: `module ${label}`, items: [...inputs, ...MODULE_META] }
  }

  if (top.type === "resource" || top.type === "data" || top.type === "provider" || top.type === "ephemeral") {
    const schema = await topSchema(top)
    let items: ItemSpec[] | null = schema ? schemaItems(schema.block) : []
    if (top.type === "provider") items.push(attr("alias", "string", "Name of an alternate configuration of this provider."))
    else {
      items.push(...RESOURCE_META)
      if (top.type === "resource") items.push(...RESOURCE_ONLY_META)
    }
    let owner = label
    let cur: SchemaBlock | null = schema?.block ?? null
    for (let i = 1; i < path.length; i++) {
      const b = path[i]
      const parent = path[i - 1]
      if (b.type === "lifecycle" && i === 1) {
        items = LIFECYCLE
        cur = null
      } else if (b.type === "provisioner") {
        items = [...(PROVISIONERS[b.labels[0]?.text ?? ""] ?? []), ...PROVISIONER_COMMON]
        cur = null
      } else if (OWN_BODIES[b.type] && b.type !== "dynamic" && (cur === null || !cur.blocks.some((x) => x.name === b.type))) {
        items = OWN_BODIES[b.type]
        cur = null
      } else if (b.type === "dynamic") {
        items = OWN_BODIES.dynamic
      } else {
        const name = b.type === "content" && parent.type === "dynamic" ? (parent.labels[0]?.text ?? "") : b.type
        const nb = cur?.blocks.find((x) => x.name === name)
        if (!nb) return null
        cur = nb.block
        items = schemaItems(nb.block)
        if (nb.block.blocks.length > 0) {
          // Bodies with nested blocks also accept dynamic generation of them.
          items.push(block("dynamic", "Generates nested blocks from a collection.", { labels: ["name"] }))
        }
      }
      owner = `${owner} › ${b.type === "content" ? (parent.labels[0]?.text ?? b.type) : b.type}`
    }
    if (path.length === 1 && schema?.block.blocks.length) {
      items.push(block("dynamic", "Generates nested blocks from a collection.", { labels: ["name"] }))
    }
    return { owner, items, docsUrl: schema?.docsUrl, schema: schema ?? undefined }
  }

  // Terraform's own blocks.
  let items: ItemSpec[] | null = OWN_BODIES[top.type] ?? null
  for (let i = 1; i < path.length && items; i++) {
    const b = path[i]
    if (top.type === "removed" && b.type === "lifecycle") items = REMOVED_LIFECYCLE
    else if (b.type === "provisioner") items = [...(PROVISIONERS[b.labels[0]?.text ?? ""] ?? []), ...PROVISIONER_COMMON]
    else if (top.type === "terraform" && b.type === "required_providers") {
      items = requiredProviderItems(env)
    } else items = OWN_BODIES[b.type] ?? null
  }
  if (!items) return null
  return { owner: path.map((b) => b.type).join(" › "), items }
}

/** `required_providers` entries: the providers the module uses, with their registry source. */
function requiredProviderItems(env: BodyEnv): ItemSpec[] {
  const sources: Record<string, string> = { oci: "oracle/oci" }
  return [...env.module.providers].map((p) => ({
    name: p,
    kind: "attr",
    typeText: "object({source, version})",
    description: `Source ${sources[p] ?? `hashicorp/${p}`}.`,
  }))
}

/** A dynamic block's iterator name. */
export function iteratorName(b: Block, text: string): string {
  const it = b.attrs.find((a) => a.name === "iterator")
  return (it && text.slice(it.valueFrom, it.valueTo).trim()) || (b.labels[0]?.text ?? "")
}

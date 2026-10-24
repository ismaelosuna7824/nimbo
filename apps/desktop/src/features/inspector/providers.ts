// Provider configurations of a Terraform workflow (the Providers section of the workflow overview):
// add one with an alias and a region, remove one nothing uses. Mirrored by the no-code audit harness
// gesture `provider` (backend/internal/api/nocode_tf_test.go).
import type { NamedBlock, Value, WorkflowNode } from "@/lib/api/types"
import { emptyBody, getAttr, setAttr } from "./body"
import { variableType } from "./references"

/** Providers the section offers, with the attribute that sets their region (azurerm has none: it needs `features {}`). */
export const PROVIDER_REGION: Record<string, string | null> = { aws: "region", google: "region", oci: "region", azurerm: null }

/** The alias of a provider block ("" for the default configuration). */
export function providerAlias(p: NamedBlock): string {
  const a = getAttr(p.body, "alias")?.literal
  return typeof a === "string" ? a : ""
}

/** The provider reference of a configuration as resources write it (`aws` or `aws.dr`). */
export function providerRef(p: NamedBlock): string {
  const alias = providerAlias(p)
  return alias ? `${p.name}.${alias}` : p.name
}

/** Nodes using a configuration: `provider = aws.dr` for an alias; for the default one, the provider's nodes without a `provider`. */
export function providerUsers(nodes: WorkflowNode[], p: NamedBlock): WorkflowNode[] {
  const alias = providerAlias(p)
  return nodes.filter((n) => {
    if (n.kind === "module") return false
    const set = getAttr(n.body, "provider")?.expr?.trim()
    if (alias) return set === `${p.name}.${alias}`
    return !set && n.type.startsWith(`${p.name}_`)
  })
}

/** Provider names the section offers: the ones the workflow configures or whose resources it has. */
export function providerNames(providers: NamedBlock[], nodes: WorkflowNode[]): string[] {
  const names = new Set(providers.map((p) => p.name))
  for (const n of nodes) {
    const prefix = n.type.split("_")[0]
    if (Object.hasOwn(PROVIDER_REGION, prefix)) names.add(prefix)
  }
  return [...names].filter((n) => Object.hasOwn(PROVIDER_REGION, n)).sort()
}

/** Variables a region can link to (string or untyped). */
export function regionVariables(variables: NamedBlock[]): string[] {
  return variables.filter((v) => {
    const t = variableType(v)
    return t === "string" || t === "dynamic"
  }).map((v) => `var.${v.name}`)
}

/** Why a configuration can't be added (an empty name/alias rule or a duplicate), or null. */
export function addProblem(providers: NamedBlock[], name: string, alias: string): string | null {
  if (!Object.hasOwn(PROVIDER_REGION, name)) return "Pick a provider."
  if (alias && !/^[a-zA-Z_][a-zA-Z0-9_-]*$/.test(alias)) return "Aliases start with a letter or _ and hold letters, digits, _ or -."
  if (providers.some((p) => p.name === name && providerAlias(p) === alias)) {
    return alias ? `${name}.${alias} already exists.` : `${name} already has a default configuration: give this one an alias.`
  }
  return null
}

/** The providers with a new configuration (alias, region; azurerm gets its required `features {}`). */
export function addProvider(providers: NamedBlock[], name: string, alias: string, region: Value | undefined): NamedBlock[] {
  let body = emptyBody()
  if (alias) body = setAttr(body, "alias", { literal: alias })
  const regionAttr = PROVIDER_REGION[name]
  if (regionAttr && region) body = setAttr(body, regionAttr, region)
  if (name === "azurerm") body = { ...body, blocks: [{ type: "features", body: emptyBody() }] }
  return [...providers, { name, body }]
}

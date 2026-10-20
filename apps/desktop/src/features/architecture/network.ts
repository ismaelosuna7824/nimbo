// Network plumbing of every provider as the Architecture diagram folds it: security groups, NSGs,
// firewall rules and security lists become "in 443 from anywhere" lines on what they protect;
// route tables and routes become "routes: 0.0.0.0/0 → NAT" lines on their subnets (or VPC).
import type { Body, WorkflowNode } from "@/lib/api/types"
import { baseType } from "./icons/classify"
import type { Resolver } from "./resolve"

/** The node a block references through an exact attribute path ("route_rules[1].network_entity_id"). */
export type RefAt = (n: WorkflowNode, path: string) => WorkflowNode | undefined

/**
 * Security groups, NSGs, firewall rules and security lists: a line on every card (or subnet) they
 * apply to, on their VPC when nothing uses them.
 */
export const SECURITY_GROUP =
  /^(aws_security_group|aws_default_security_group|azurerm_network_security_group|google_compute_firewall|oci_core_security_list|oci_core_default_security_list|oci_core_network_security_group)$/
/** Rule resources of a security group, folded into it. */
export const SECURITY_RULE =
  /^(aws_security_group_rule|aws_vpc_security_group_ingress_rule|aws_vpc_security_group_egress_rule|azurerm_network_security_rule|oci_core_network_security_group_security_rule)$/
/** Route tables: a "routes: …" line on the subnets associated with them (their VPC when none is). */
export const ROUTE_TABLE = /^(aws_route_table|aws_default_route_table|azurerm_route_table|oci_core_route_table|oci_core_default_route_table)$/
/** Route resources: into their table; GCP routes (no tables) into their VPC. */
export const ROUTE = /^(aws_route|azurerm_route|google_compute_route)$/

/** Label prefix of a security group's line. */
const PREFIX: Record<string, string> = {
  aws_security_group: "SG",
  aws_default_security_group: "default SG",
  azurerm_network_security_group: "NSG",
  google_compute_firewall: "FW",
  oci_core_security_list: "SL",
  oci_core_default_security_list: "default SL",
  oci_core_network_security_group: "NSG",
}

const isStr = (v: unknown): v is string => typeof v === "string" && v !== ""
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.filter(isStr) : isStr(v) ? [v] : [])
const unique = <T,>(xs: T[]) => [...new Set(xs)]

/** Well-known source ranges by name. */
const NAMED_RANGES: Record<string, string> = {
  "0.0.0.0/0": "anywhere",
  "::/0": "anywhere",
  "*": "anywhere",
  Internet: "anywhere",
  any: "anywhere",
  "35.235.240.0/20": "IAP",
  "35.191.0.0/16": "health checks",
  "130.211.0.0/22": "health checks",
  VirtualNetwork: "VNet",
  AzureLoadBalancer: "Azure LB",
}

/** "SG app", "NSG web", "FW allow-ssh": a security group's name with its kind. */
export function securityName(n: WorkflowNode, r: Resolver) {
  const t = baseType(n.type)
  const name = r.attr(n, t.startsWith("oci_") ? "display_name" : "name")
  return `${PREFIX[t] ?? "SG"} ${isStr(name) ? name : n.name}`
}

/** Ports and sources of inbound rules: "in 443 from anywhere; 8080 from SG alb". */
class Inbound {
  private readonly bySource = new Map<string, string[]>()
  add(port: string | undefined, sources: string[]) {
    if (!port) return
    const named = unique(sources.map((s) => NAMED_RANGES[s] ?? s))
    for (const s of named.length ? named : [""]) this.bySource.set(s, [...(this.bySource.get(s) ?? []), port])
  }
  text() {
    if (this.bySource.size === 0) return undefined
    return `in ${[...this.bySource].map(([source, ports]) => `${unique(ports).join(", ")}${source ? ` from ${source}` : ""}`).join("; ")}`
  }
}

/** "443", "8000–8080", "all" from a port range. */
function portRange(from: unknown, to: unknown, all: boolean) {
  if (all) return "all"
  const a = typeof from === "string" && /^\d+$/.test(from) ? Number(from) : from
  const b = typeof to === "string" && /^\d+$/.test(to) ? Number(to) : to
  if (typeof a !== "number") return undefined
  return a === b || b === undefined ? String(a) : a === 0 && b === 65535 ? "all" : `${a}–${b}`
}

/** Security group names referenced by an expression ("aws_security_group.alb.id" → "SG alb"). */
function groupRefs(expr: string | undefined, prefix: string, type: string) {
  return [...(expr ?? "").matchAll(new RegExp(`${type}\\.([\\w-]+)`, "g"))].map((m) => `${prefix} ${m[1]}`)
}

/** Inbound rules of a security group (its own blocks and its folded rule resources). */
export function securitySummary(n: WorkflowNode, rules: WorkflowNode[], r: Resolver): string | undefined {
  const t = baseType(n.type)
  const out = new Inbound()
  const val = (b: Body, name: string) => r.value(b.attrs.find((x) => x.name === name)?.value)
  const expr = (b: Body, name: string) => b.attrs.find((x) => x.name === name)?.value.expr
  const sub = (b: Body, name: string) => b.blocks.find((x) => x.type === name)?.body
  if (t === "aws_security_group" || t === "aws_default_security_group") {
    const ingress = (b: Body) => {
      const from = val(b, "from_port")
      const to = val(b, "to_port")
      const all = ["-1", "all"].includes(String(val(b, "protocol") ?? val(b, "ip_protocol") ?? "")) || (from === 0 && to === 65535)
      const cidrs = ["cidr_blocks", "cidr_ipv4", "ipv6_cidr_blocks", "cidr_ipv6"].flatMap((k) => strs(val(b, k)))
      const groups = ["security_groups", "source_security_group_id", "referenced_security_group_id"].flatMap((k) => groupRefs(expr(b, k), "SG", "aws_security_group"))
      const sources = [...(cidrs.some((c) => c === "0.0.0.0/0" || c === "::/0") ? ["anywhere"] : cidrs), ...groups, ...(val(b, "self") === true ? ["itself"] : [])]
      out.add(portRange(from, to, all), sources)
    }
    for (const b of n.body.blocks) if (b.type === "ingress") ingress(b.body)
    for (const rule of rules) {
      const rt = baseType(rule.type)
      if (rt === "aws_vpc_security_group_ingress_rule" || (rt === "aws_security_group_rule" && r.attr(rule, "type") === "ingress")) ingress(rule.body)
    }
  } else if (t === "azurerm_network_security_group") {
    const rule = (b: Body) => {
      if (!/^inbound$/i.test(String(val(b, "direction") ?? "")) || !/^allow$/i.test(String(val(b, "access") ?? ""))) return
      const ports = [...strs(val(b, "destination_port_range")), ...strs(val(b, "destination_port_ranges"))].map((p) => (p === "*" ? "all" : p.replace("-", "–")))
      const sources = [
        ...strs(val(b, "source_address_prefix")),
        ...strs(val(b, "source_address_prefixes")),
        ...groupRefs(expr(b, "source_application_security_group_ids"), "ASG", "azurerm_application_security_group"),
      ]
      for (const p of ports) out.add(p, sources)
    }
    for (const b of n.body.blocks) if (b.type === "security_rule") rule(b.body)
    for (const x of rules) rule(x.body)
  } else if (t === "google_compute_firewall") {
    if (/^EGRESS$/i.test(String(r.attr(n, "direction") ?? ""))) return undefined
    const sources = [
      ...strs(r.attr(n, "source_ranges")),
      ...strs(r.attr(n, "source_tags")).map((s) => `tag ${s}`),
      ...(findExpr(n.body, "source_service_accounts") ? ["service account"] : []),
    ]
    for (const b of n.body.blocks) {
      if (b.type !== "allow") continue
      const protocol = String(val(b.body, "protocol") ?? "")
      const ports = strs(val(b.body, "ports")).map((p) => p.replace("-", "–"))
      if (protocol === "all" || (!ports.length && /^(tcp|udp)$/.test(protocol))) out.add("all", sources)
      else if (protocol === "icmp") out.add("ICMP", sources)
      else for (const p of ports) out.add(p, sources)
    }
  } else {
    // OCI security lists (ingress_security_rules blocks) and NSGs (rule resources).
    const rule = (b: Body, nsgRule: boolean) => {
      if (nsgRule && !/^INGRESS$/i.test(String(val(b, "direction") ?? ""))) return
      const protocol = String(val(b, "protocol") ?? "")
      const options = sub(b, "tcp_options") ?? sub(b, "udp_options")
      const range = options && (nsgRule ? sub(options, "destination_port_range") : (sub(options, "destination_port_range") ?? options))
      const port = protocol === "all" ? "all" : protocol === "1" ? "ICMP" : range ? portRange(val(range, "min"), val(range, "max"), false) : "all"
      const source = nsgRule && /NETWORK_SECURITY_GROUP/.test(String(val(b, "source_type") ?? "")) ? groupRefs(expr(b, "source"), "NSG", "oci_core_network_security_group") : strs(val(b, "source"))
      out.add(port, source)
    }
    for (const b of n.body.blocks) if (b.type === "ingress_security_rules") rule(b.body, false)
    for (const x of rules) rule(x.body, true)
  }
  return out.text()
}

function findExpr(b: Body, name: string) {
  return b.attrs.find((a) => a.name === name)?.value.expr
}

/** Network tags and service accounts a GCP firewall rule applies to (none: the whole VPC). */
export function firewallTargets(n: WorkflowNode, r: Resolver) {
  return { tags: strs(r.attr(n, "target_tags")), serviceAccounts: findExpr(n.body, "target_service_accounts") }
}

/** Route targets of AWS routes by attribute ("gateway_id" is told apart by the referenced type). */
const AWS_ROUTE_TARGET: Record<string, string> = {
  nat_gateway_id: "NAT",
  egress_only_gateway_id: "egress-only IGW",
  transit_gateway_id: "TGW",
  vpc_peering_connection_id: "peering",
  vpc_endpoint_id: "VPC endpoint",
  network_interface_id: "ENI",
  local_gateway_id: "local gateway",
  carrier_gateway_id: "carrier gateway",
  core_network_arn: "Cloud WAN",
}
/** Azure next hop types. */
const AZURE_NEXT_HOP: Record<string, string> = {
  Internet: "Internet",
  VirtualAppliance: "NVA",
  VnetLocal: "VNet",
  VirtualNetworkGateway: "VPN gateway",
  None: "drop",
}
/** OCI route targets by the referenced type. */
const OCI_TARGET: readonly (readonly [RegExp, string])[] = [
  [/internet_gateway/, "IGW"],
  [/nat_gateway/, "NAT"],
  [/service_gateway/, "Service gateway"],
  [/drg/, "DRG"],
  [/local_peering_gateway/, "LPG"],
  [/private_ip/, "private IP"],
]
/** GCP next hops other than a gateway or an IP. */
const GCP_NEXT_HOP: readonly (readonly [string, string])[] = [
  ["next_hop_instance", "instance"],
  ["next_hop_ilb", "internal LB"],
  ["next_hop_vpn_tunnel", "VPN tunnel"],
]

/**
 * "0.0.0.0/0 → NAT" lines of a route table (its inline routes and its route resources) or of a
 * GCP route. `refAt` names what a route rule's target references.
 */
export function routeLines(n: WorkflowNode, routes: WorkflowNode[], r: Resolver, refAt: RefAt): string[] {
  const t = baseType(n.type)
  const val = (b: Body, name: string) => r.value(b.attrs.find((x) => x.name === name)?.value)
  const out: (string | undefined)[] = []
  if (t === "google_compute_route") {
    const b = n.body
    const dest = val(b, "dest_range")
    const gateway = findExpr(b, "next_hop_gateway")
    const ip = val(b, "next_hop_ip")
    const hop = gateway
      ? /default-internet-gateway/.test(gateway)
        ? "internet gateway"
        : "gateway"
      : isStr(ip)
        ? ip
        : GCP_NEXT_HOP.find(([attr]) => findExpr(b, attr))?.[1]
    out.push(isStr(dest) && hop ? `${dest} → ${hop}` : undefined)
  } else if (t.startsWith("aws_")) {
    const route = (b: Body) => {
      const dest =
        ["destination_cidr_block", "cidr_block", "destination_ipv6_cidr_block", "ipv6_cidr_block"].map((k) => val(b, k)).find(isStr) ??
        (findExpr(b, "destination_prefix_list_id") ? "prefix list" : undefined)
      const target = b.attrs
        .map((a) => {
          if (a.name !== "gateway_id") return AWS_ROUTE_TARGET[a.name]
          const v = `${a.value.expr ?? a.value.literal ?? ""}`
          return /aws_internet_gateway\.|igw-/.test(v) ? "IGW" : /aws_vpn_gateway\.|vgw-/.test(v) ? "VGW" : /local/.test(v) ? undefined : "gateway"
        })
        .find(isStr)
      return dest && target ? `${dest} → ${target}` : undefined
    }
    out.push(...n.body.blocks.filter((b) => b.type === "route").map((b) => route(b.body)), ...routes.map((x) => route(x.body)))
  } else if (t.startsWith("azurerm_")) {
    const route = (b: Body) => {
      const dest = val(b, "address_prefix")
      const type = String(val(b, "next_hop_type") ?? "")
      const hop = AZURE_NEXT_HOP[type] ?? (type || undefined)
      const ip = val(b, "next_hop_in_ip_address")
      return isStr(dest) && hop ? `${dest} → ${hop}${hop === "NVA" && isStr(ip) ? ` ${ip}` : ""}` : undefined
    }
    out.push(...n.body.blocks.filter((b) => b.type === "route").map((b) => route(b.body)), ...routes.map((x) => route(x.body)))
  } else {
    n.body.blocks
      .filter((b) => b.type === "route_rules")
      .forEach((b, i) => {
        const service = /SERVICE_CIDR_BLOCK/.test(String(val(b.body, "destination_type") ?? ""))
        const dest = service ? "Oracle services" : val(b.body, "destination") ?? val(b.body, "cidr_block")
        const ref = refAt(n, `route_rules[${i}].network_entity_id`)
        const text = `${ref ? baseType(ref.type) : ""} ${findExpr(b.body, "network_entity_id") ?? ""}`
        const hop = OCI_TARGET.find(([re]) => re.test(text))?.[1] ?? (findExpr(b.body, "network_entity_id") ? "gateway" : undefined)
        out.push(isStr(dest) && hop ? `${dest} → ${hop}` : undefined)
      })
  }
  return unique(out.filter(isStr))
}

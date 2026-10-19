import type { SubnetClassification } from "@/lib/api/types"

/** Beginner explanations of network jargon, shown as tooltips. */
export const NET_GLOSSARY = {
  public:
    "Public subnet: its route table sends internet traffic (0.0.0.0/0) to an internet gateway, so instances with a public IP can be reached from the internet and reach it.",
  private:
    "Private subnet: no route from the internet. Its resources can still call out (updates, APIs) through a NAT gateway, but nobody outside can start a connection to them.",
  isolated:
    "Isolated subnet: no route to the internet in either direction. Good for databases that only your own resources should talk to.",
  unknown: "Nimbo couldn't tell how this subnet reaches the internet (some values are only known after deploying).",
  "internet-gateway":
    "Internet gateway: the VPC's door to the internet. Subnets whose route table points 0.0.0.0/0 at it become public.",
  "nat-gateway":
    "NAT gateway: lets resources in private subnets start connections to the internet (outbound only). It lives in a public subnet; replies come back, new inbound connections don't.",
  "route-table":
    "Route table: the signposts of a subnet. Each route says where traffic for an address range goes (inside the VPC, to an internet gateway, to a NAT gateway…).",
  "security-group":
    "Security group: a firewall around each resource. It only lists what is allowed in and out, and it is stateful: replies to an allowed connection are let through automatically.",
  nacl: "Network ACL: a firewall around a whole subnet. It is stateless: replies are checked too, so return traffic on ephemeral ports (1024-65535) must be allowed explicitly. Rules have numbers and can deny.",
  "ephemeral-ports":
    "Ephemeral ports (1024-65535): the temporary ports a client opens to receive replies. Stateless firewalls like network ACLs must allow them for responses to get back.",
  basis:
    "Network checks read the deployed sandbox when there is one (exact addresses and IDs), otherwise your design (expressions evaluated with defaults).",
  reachability:
    "Reachability analysis follows a connection hop by hop (routes, gateways, network ACLs, security groups) and tells you where it would be blocked, without sending any traffic.",
} as const

export type NetTerm = keyof typeof NET_GLOSSARY

export const SUBNET_META: Record<SubnetClassification, { label: string; className: string }> = {
  public: { label: "Public", className: "bg-emerald-500/12 text-emerald-700 dark:text-emerald-300" },
  private: { label: "Private", className: "bg-sky-500/12 text-sky-700 dark:text-sky-300" },
  isolated: { label: "Isolated", className: "bg-violet-500/12 text-violet-700 dark:text-violet-300" },
  unknown: { label: "Unknown", className: "bg-muted text-muted-foreground" },
}


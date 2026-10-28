import { useMemo, useState } from "react"
import { FlaskConicalIcon, RouteIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { isNetworkFinding, type NetRule, type NetworkSummary, type WorkflowNode } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { InspectorSection } from "../inspector/inspector-section"
import { DiagnosticRow } from "../panels/components/diagnostic-row"
import { BasisBadge, SubnetBadge, Term } from "./glossary"
import type { NetTerm } from "./terms"
import { INTERNET, openReachability, type ReachabilityPreset } from "./store"
import { addTestInstance, useNetwork } from "./use-network"

// Constant: section headings are memoized on their props.
const ICON = <RouteIcon className="size-3.5 text-teal-500" />

/** Gateway / route target kinds with a beginner explanation. */
const TERM_OF_KIND: Record<string, NetTerm> = {
  "internet-gateway": "internet-gateway",
  "nat-gateway": "nat-gateway",
}

const KIND_LABEL: Record<string, string> = {
  local: "inside the network",
  "internet-gateway": "internet gateway",
  "egress-only-gateway": "egress-only internet gateway",
  "nat-gateway": "NAT gateway",
  "vpc-endpoint": "VPC endpoint",
  peering: "peering connection",
  "transit-gateway": "transit gateway",
  instance: "instance",
  "network-interface": "network interface",
  "vpn-gateway": "VPN gateway",
  unknown: "unknown target",
}

function KindLabel({ kind }: { kind: string }) {
  const term = TERM_OF_KIND[kind]
  const label = KIND_LABEL[kind] ?? kind
  return term ? <Term term={term}>{label}</Term> : <>{label}</>
}

/** Everything the network model says about one canvas node. */
function entriesOf(summary: NetworkSummary, nodeId: string) {
  return {
    networks: summary.networks.filter((x) => x.nodeId === nodeId),
    subnets: summary.subnets.filter((x) => x.nodeId === nodeId),
    routeTables: summary.routeTables.filter((x) => x.nodeId === nodeId),
    gateways: summary.gateways.filter((x) => x.nodeId === nodeId),
    securityGroups: summary.securityGroups.filter((x) => x.nodeId === nodeId),
    placements: summary.placements.filter((x) => x.nodeId === nodeId),
  }
}

/**
 * Inspector "Network" section: subnet classification, security group rules, where a
 * workload sits, this node's network findings and shortcuts to the reachability check.
 * Renders nothing for nodes the network model doesn't know.
 */
export function NetworkSection({ node }: { node: WorkflowNode }) {
  const network = useNetwork()
  const summary = network.data
  const allDiagnostics = useWorkflowStore((s) => s.workflow?.diagnostics)
  const findings = useMemo(
    () => (allDiagnostics ?? []).filter((d) => d.nodeId === node.id && isNetworkFinding(d)),
    [allDiagnostics, node.id],
  )
  const [adding, setAdding] = useState(false)
  const entries = useMemo(() => (summary ? entriesOf(summary, node.id) : null), [summary, node.id])
  if (!summary || !entries) return null
  const known = Object.values(entries).some((list) => list.length > 0)
  if (!known && findings.length === 0) return null

  const subnetByAddress = new Map(summary.subnets.map((s) => [s.address, s]))
  const sgByAddress = new Map(summary.securityGroups.map((g) => [g.address, g]))
  const many = (n: number) => n > 1

  // Who to ask about: the workload itself, else one that sits in this subnet / uses this security group.
  const related =
    entries.placements[0] ??
    summary.placements.find((p) => entries.subnets.some((s) => p.subnets.includes(s.address))) ??
    summary.placements.find((p) => entries.securityGroups.some((g) => p.securityGroups.includes(g.address)))
  const natPrivate =
    entries.gateways.some((g) => g.kind === "nat-gateway") &&
    summary.placements.find((p) => p.subnets.some((s) => subnetByAddress.get(s)?.classification === "private"))
  const preset: ReachabilityPreset = natPrivate
    ? { source: { kind: "node", nodeId: natPrivate.address }, destination: INTERNET, protocol: "tcp", port: 443 }
    : related
      ? { source: INTERNET, destination: { kind: "node", nodeId: related.address } }
      : {}

  return (
    <InspectorSection id="network" title="Network" icon={ICON}>
      <div className="flex flex-col gap-3 text-xs" data-testid="network-section">
        <div>
          <BasisBadge basis={summary.basis} reason={summary.basisReason} />
        </div>

        {entries.networks.map((n) => (
          <div key={n.address} className="flex flex-col gap-0.5">
            {many(entries.networks.length) && <span className="font-mono text-[11px] text-muted-foreground">{n.address}</span>}
            <span>
              Address range:{" "}
              <span className="font-mono">{n.cidrs.length ? n.cidrs.join(", ") : "known after deploy"}</span>
            </span>
            <span className="text-muted-foreground">
              {summary.subnets.filter((s) => s.network === n.address).length} subnet(s) in this network.
            </span>
          </div>
        ))}

        {entries.subnets.map((s) => (
          <div key={s.address} className="flex flex-col gap-1.5" data-testid="network-subnet">
            {many(entries.subnets.length) && <span className="font-mono text-[11px] text-muted-foreground">{s.address}</span>}
            <div className="flex flex-wrap items-center gap-2">
              <SubnetBadge classification={s.classification} />
              {s.cidr && <span className="font-mono">{s.cidr}</span>}
              {s.zone && <span className="text-muted-foreground">Zone {s.zone}</span>}
            </div>
            <p className="text-muted-foreground">{s.why}</p>
            {s.routeTable && (
              <span>
                <Term term="route-table">Route table</Term>: <span className="font-mono">{s.routeTable}</span>
              </span>
            )}
            {s.nacl && (
              <span>
                <Term term="nacl">Network ACL</Term>: <span className="font-mono">{s.nacl}</span>
              </span>
            )}
          </div>
        ))}

        {entries.routeTables.map((rt) => (
          <div key={rt.address} className="flex flex-col gap-1">
            {many(entries.routeTables.length) && <span className="font-mono text-[11px] text-muted-foreground">{rt.address}</span>}
            <span className="font-medium">
              <Term term="route-table">Routes</Term>
              {rt.main && <span className="font-normal text-muted-foreground"> (main route table of the network)</span>}
            </span>
            <ul className="flex flex-col gap-0.5">
              {rt.routes.map((r, i) => (
                <li key={i}>
                  <span className="font-mono">{r.destination}</span> → <KindLabel kind={r.targetKind} />
                  {r.target && <span className="font-mono text-muted-foreground"> {r.target}</span>}
                </li>
              ))}
            </ul>
            <span className="text-muted-foreground">
              Used by: {rt.subnets.length ? rt.subnets.join(", ") : "no subnet yet"}
            </span>
          </div>
        ))}

        {entries.gateways.map((g) => (
          <div key={g.address} className="flex flex-col gap-0.5">
            {many(entries.gateways.length) && <span className="font-mono text-[11px] text-muted-foreground">{g.address}</span>}
            <span className="font-medium capitalize">
              <KindLabel kind={g.kind} />
              {g.service && <span className="font-normal normal-case"> for {g.service}</span>}
            </span>
            {g.subnet && (
              <span className="text-muted-foreground">
                Sits in <span className="font-mono">{g.subnet}</span>
              </span>
            )}
            {g.detail && <p className="text-muted-foreground">{g.detail}</p>}
          </div>
        ))}

        {entries.securityGroups.map((g) => (
          <div key={g.address} className="flex flex-col gap-1.5" data-testid="network-security-group">
            {many(entries.securityGroups.length) && <span className="font-mono text-[11px] text-muted-foreground">{g.address}</span>}
            <p>
              <Term term="security-group" className="font-medium">
                Security group
              </Term>
              : {g.summary}
            </p>
            <RuleList title="Inbound (ingress)" rules={g.ingress} />
            <RuleList title="Outbound (egress)" rules={g.egress} />
          </div>
        ))}

        {entries.placements.map((p) => (
          <div key={p.address} className="flex flex-col gap-1.5" data-testid="network-placement">
            {many(entries.placements.length) && <span className="font-mono text-[11px] text-muted-foreground">{p.address}</span>}
            {!p.inVpc ? (
              <p className="text-muted-foreground">Runs outside any VPC: it reaches the internet directly and can't reach private resources.</p>
            ) : (
              <>
                <div className="flex flex-col gap-1">
                  <span className="font-medium">Subnets</span>
                  {p.subnets.length === 0 && <span className="text-muted-foreground">Not known yet</span>}
                  {p.subnets.map((addr) => {
                    const sub = subnetByAddress.get(addr)
                    return (
                      <span key={addr} className="flex flex-wrap items-center gap-1.5">
                        {sub && <SubnetBadge classification={sub.classification} />}
                        <span className="font-mono">{addr}</span>
                        {sub?.zone && <span className="text-muted-foreground">{sub.zone}</span>}
                      </span>
                    )
                  })}
                  {p.zones.length > 0 && <span className="text-muted-foreground">Zones: {p.zones.join(", ")}</span>}
                </div>
                <p>
                  <span
                    data-testid="placement-public"
                    className={p.public ? "font-medium text-amber-700 dark:text-amber-300" : "font-medium"}
                  >
                    {p.public ? "Has a public address" : "Private address only"}
                  </span>
                  {p.publicWhy && <span className="text-muted-foreground"> — {p.publicWhy}</span>}
                </p>
              </>
            )}
            {p.ports && p.ports.length > 0 && <span>Listens on port {p.ports.join(", ")}</span>}
            {p.securityGroups.length > 0 && (
              <div className="flex flex-col gap-0.5">
                <Term term="security-group" className="self-start font-medium">
                  Security groups
                </Term>
                {p.securityGroups.map((addr) => (
                  <span key={addr}>
                    <span className="font-mono">{addr}</span>
                    {sgByAddress.get(addr)?.summary && (
                      <span className="text-muted-foreground"> — {sgByAddress.get(addr)!.summary}</span>
                    )}
                  </span>
                ))}
              </div>
            )}
          </div>
        ))}

        {findings.length > 0 && (
          <div className="flex flex-col divide-y rounded-lg border" data-testid="network-findings">
            {findings.map((d, i) => (
              <DiagnosticRow key={`${d.code}-${i}`} diagnostic={d} />
            ))}
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => void openReachability(preset)}>
            <RouteIcon />
            Check reachability…
          </Button>
          {node.type === "aws_subnet" && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={adding}
                  data-testid="add-test-instance"
                  onClick={async () => {
                    setAdding(true)
                    await addTestInstance(entries.subnets[0]?.address ?? node.id)
                    setAdding(false)
                  }}
                >
                  {adding ? <Spinner /> : <FlaskConicalIcon />}
                  Add a test instance here
                </Button>
              </TooltipTrigger>
              <TooltipContent className="max-w-72">
                Adds a tiny EC2 instance running a web server on port 80 (and its security group) to this subnet, so you
                can see traffic really flow after deploying.
              </TooltipContent>
            </Tooltip>
          )}
        </div>
      </div>
    </InspectorSection>
  )
}

function RuleList({ title, rules }: { title: string; rules: NetRule[] }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">{title}</span>
      {rules.length === 0 ? (
        <span className="text-muted-foreground">Nothing allowed</span>
      ) : (
        <ul className="flex flex-col gap-0.5">
          {rules.map((r, i) => (
            <li key={i}>
              <span className="font-mono">{r.protocol === "all" ? "All traffic" : r.protocol.toUpperCase()}</span>{" "}
              {r.protocol !== "all" && (
                <span className="font-mono">{r.ports === "all" ? "all ports" : `port ${r.ports}`}</span>
              )}{" "}
              <span className="text-muted-foreground">{title.startsWith("Inbound") ? "from" : "to"}</span>{" "}
              <span className="font-mono">{r.peer}</span>
              {r.description && <span className="text-muted-foreground"> — {r.description}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

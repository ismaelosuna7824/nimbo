import type { NodeKind } from "@/lib/api/types"
import type { ArchCategory, ArchProvider } from "./icons/classify"

/** What a diagram container stands for; decides its look and label icon. */
export type GroupKind =
  | "cloud"
  | "account"
  | "region"
  | "vpc"
  | "az"
  | "subnet"
  | "logical"
  | "external"
  | "other"
  // Docker workspaces: the machine running the containers, the compose project, one network.
  | "docker-host"
  | "compose-project"
  | "docker-network"
  // Kubernetes workspaces: the local cluster, one namespace, a namespace's data tier.
  | "k8s-cluster"
  | "k8s-namespace"
  | "k8s-data"

/** Subnet tier, from routes to an internet/NAT gateway, public IPs or the subnet's name. */
export type SubnetTier = "public" | "private" | "unknown"

/** A Docker network the containers cannot leave (internal) or that the project uses but does not create (external). */
export type NetworkScope = "internal" | "external"

export interface ArchGroup {
  id: string
  parent?: string
  kind: GroupKind
  label: string
  /** Lines under the label (CIDRs, AZs, location; a subnet's routes, a VPC's unused security groups). */
  sublabel?: string[]
  provider?: ArchProvider
  tier?: SubnetTier
  /** Docker networks only. */
  scope?: NetworkScope
  /** Workflow node behind the group (VPC, subnet, resource group…); selecting the group opens it. */
  nodeId?: string
  /** Cards of one kind ("ECS services (6)"): laid out as a grid of at most three rows. */
  packed?: boolean
}

export interface ArchCard {
  /** The workflow node ID. */
  id: string
  parent?: string
  nodeId: string
  provider?: ArchProvider
  type: string
  kind: NodeKind
  category: ArchCategory
  /** The node's own label, when the user gave it one (else the view titles the card with the service name). */
  label?: string
  /** Terraform name of the block ("items"), shown after the service name. */
  name: string
  /** Smart facts: instance type, runtime, engine, CIDR, counts… */
  details: string[]
  /** Folded helper resources, e.g. "+ 2 IAM Role Policies". */
  folded?: string
  /** Security groups of the card with their inbound rules, e.g. "SG alb: in 443 from anywhere". */
  security?: string[]
  /** Drawn small on its parent group's top border (an internet gateway on its VPC), outside the automatic layout. */
  onBorder?: boolean
  /** Data source: an existing resource the design only reads (dashed). Docker: a service whose profiles are not active. */
  existing?: boolean
}

export type ActorKind = "users" | "internet" | "service"

export interface ArchActor {
  id: string
  parent?: string
  kind: ActorKind
  label: string
  sublabel?: string
  /** Placed above this item, outside every container (the Internet above its internet gateway). */
  above?: string
}

/** data: requests, events and reads/writes (solid); control: identity, keys and telemetry (dashed). */
export type FlowKind = "data" | "control"

export interface ArchEdge {
  id: string
  source: string
  target: string
  kind: FlowKind
}

/** The derived architecture of a workflow, before layout. Arrays are in a deterministic order. */
export interface ArchModel {
  groups: ArchGroup[]
  cards: ArchCard[]
  actors: ArchActor[]
  edges: ArchEdge[]
  /** Clouds and regions the design spans, for the default subtitle. */
  providers: ArchProvider[]
  regions: string[]
}

export const PROVIDER_NAME: Record<ArchProvider, string> = {
  aws: "AWS Cloud",
  azure: "Microsoft Azure",
  gcp: "Google Cloud",
  oci: "Oracle Cloud",
}

export const PROVIDER_SHORT: Record<ArchProvider, string> = {
  aws: "AWS",
  azure: "Azure",
  gcp: "Google Cloud",
  oci: "OCI",
}

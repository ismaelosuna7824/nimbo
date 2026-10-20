import { describe, expect, test } from "bun:test"
import type { Body, WorkflowEdge, WorkflowNode } from "@/lib/api/types"
import { deriveArchitecture, type DeriveWorkflow } from "./derive"
import type { ArchModel } from "./model"

// Synthetic designs per provider, written as HCL expressions; references (edges) are derived from
// the expressions the way the daemon does, with indexed block paths ("route_rules[0].network_entity_id").
type Blocks = [type: string, attrs: Record<string, string>, blocks?: Blocks][]
const toBody = (attrs: Record<string, string>, blocks: Blocks = []): Body => ({
  attrs: Object.entries(attrs).map(([name, expr]) => ({ name, value: { expr } })),
  blocks: blocks.map(([type, a, b]) => ({ type, body: toBody(a, b) })),
})
const res = (address: string, attrs: Record<string, string> = {}, blocks: Blocks = []): WorkflowNode => {
  const data = address.startsWith("data.")
  const [type, name] = (data ? address.slice(5) : address).split(".")
  return { id: address, kind: data ? "data" : "resource", type, name, position: { x: 0, y: 0 }, positioned: true, body: toBody(attrs, blocks) }
}
const REF = /(?<![\w.])((?:data\.)?[a-z][a-z0-9]*_[\w-]*\.[A-Za-z_][\w-]*)/g
function edgesOf(nodes: WorkflowNode[]): WorkflowEdge[] {
  const ids = new Set(nodes.map((n) => n.id))
  const out: WorkflowEdge[] = []
  const walk = (n: WorkflowNode, b: Body, prefix: string) => {
    for (const a of b.attrs) {
      for (const m of a.value.expr?.matchAll(REF) ?? []) {
        if (!ids.has(m[1]) || m[1] === n.id) continue
        const attribute = prefix + a.name
        out.push({ id: `${m[1]}>${n.id}:${attribute}`, source: m[1], target: n.id, kind: attribute === "depends_on" ? "depends_on" : "reference", attribute })
      }
    }
    const seen: Record<string, number> = {}
    for (const blk of b.blocks) {
      const i = seen[blk.type] ?? 0
      seen[blk.type] = i + 1
      walk(n, blk.body, `${prefix}${blk.type}[${i}].`)
    }
  }
  for (const n of nodes) walk(n, n.body, "")
  return out
}
const design = (provider: string, providerAttrs: Record<string, string>, nodes: WorkflowNode[], variables: Record<string, string> = {}): DeriveWorkflow => ({
  nodes,
  edges: edgesOf(nodes),
  variables: Object.entries(variables).map(([name, expr]) => ({ name, body: toBody({ default: expr }) })),
  locals: [],
  providers: [{ name: provider, body: toBody(providerAttrs) }],
  environments: [],
})
const helpers = (m: ArchModel) => ({
  card: (id: string) => m.cards.find((c) => c.id === id)!,
  group: (id: string) => m.groups.find((g) => g.id === id)!,
  cardIds: m.cards.map((c) => c.id),
  edge: (s: string, t: string) => m.edges.find((e) => e.source === s && e.target === t),
})

describe("Azure", () => {
  const m = deriveArchitecture(
    design("azurerm", {}, [
      res("azurerm_resource_group.main", { name: '"rg-shop"', location: '"East US"' }),
      res("azurerm_virtual_network.main", { resource_group_name: "azurerm_resource_group.main.name", location: "azurerm_resource_group.main.location", address_space: '["10.0.0.0/16"]' }),
      res("azurerm_subnet.app", { virtual_network_name: "azurerm_virtual_network.main.name", address_prefixes: '["10.0.1.0/24"]' }),
      res("azurerm_subnet.data", { virtual_network_name: "azurerm_virtual_network.main.name", address_prefixes: '["10.0.2.0/24"]' }),
      res("azurerm_network_security_group.web", { name: '"web-nsg"', resource_group_name: "azurerm_resource_group.main.name" }, [
        ["security_rule", { direction: '"Inbound"', access: '"Allow"', destination_port_range: '"443"', source_address_prefix: '"Internet"' }],
        ["security_rule", { direction: '"Inbound"', access: '"Deny"', destination_port_range: '"22"', source_address_prefix: '"*"' }],
      ]),
      res("azurerm_subnet_network_security_group_association.app", { subnet_id: "azurerm_subnet.app.id", network_security_group_id: "azurerm_network_security_group.web.id" }),
      res("azurerm_network_security_group.vm", { name: '"vm-nsg"' }),
      res("azurerm_network_security_rule.ssh", {
        network_security_group_name: "azurerm_network_security_group.vm.name",
        direction: '"Inbound"',
        access: '"Allow"',
        destination_port_range: '"22"',
        source_address_prefix: '"VirtualNetwork"',
      }),
      res("azurerm_route_table.app", { name: '"app-rt"' }, [["route", { address_prefix: '"0.0.0.0/0"', next_hop_type: '"VirtualAppliance"', next_hop_in_ip_address: '"10.0.0.4"' }]]),
      res("azurerm_subnet_route_table_association.app", { subnet_id: "azurerm_subnet.app.id", route_table_id: "azurerm_route_table.app.id" }),
      res("azurerm_nat_gateway.out", { resource_group_name: "azurerm_resource_group.main.name", location: "azurerm_resource_group.main.location" }),
      res("azurerm_subnet_nat_gateway_association.app", { subnet_id: "azurerm_subnet.app.id", nat_gateway_id: "azurerm_nat_gateway.out.id" }),
      res("azurerm_public_ip.vm", { resource_group_name: "azurerm_resource_group.main.name" }),
      res("azurerm_network_interface.vm", { resource_group_name: "azurerm_resource_group.main.name" }, [
        ["ip_configuration", { subnet_id: "azurerm_subnet.app.id", public_ip_address_id: "azurerm_public_ip.vm.id" }],
      ]),
      res("azurerm_network_interface_security_group_association.vm", { network_interface_id: "azurerm_network_interface.vm.id", network_security_group_id: "azurerm_network_security_group.vm.id" }),
      res("azurerm_linux_virtual_machine.app", { resource_group_name: "azurerm_resource_group.main.name", location: "azurerm_resource_group.main.location", size: '"Standard_B2s"', network_interface_ids: "[azurerm_network_interface.vm.id]" }),
      res("azurerm_mssql_server.db", { resource_group_name: "azurerm_resource_group.main.name", location: "azurerm_resource_group.main.location", version: '"12.0"' }),
      res("azurerm_mssql_database.orders", { server_id: "azurerm_mssql_server.db.id" }),
      res("azurerm_private_dns_zone.sql", { name: '"privatelink.database.windows.net"', resource_group_name: "azurerm_resource_group.main.name" }),
      res("azurerm_private_dns_zone_virtual_network_link.sql", { private_dns_zone_name: "azurerm_private_dns_zone.sql.name", virtual_network_id: "azurerm_virtual_network.main.id" }),
      res("azurerm_private_endpoint.sql", { subnet_id: "azurerm_subnet.data.id", location: "azurerm_resource_group.main.location" }, [
        ["private_service_connection", { private_connection_resource_id: "azurerm_mssql_server.db.id" }],
        ["private_dns_zone_group", { private_dns_zone_ids: "[azurerm_private_dns_zone.sql.id]" }],
      ]),
      res("azurerm_service_plan.web", { resource_group_name: "azurerm_resource_group.main.name", location: "azurerm_resource_group.main.location", os_type: '"Linux"', sku_name: '"P1v3"' }),
      res("azurerm_linux_web_app.web", {
        resource_group_name: "azurerm_resource_group.main.name",
        location: "azurerm_resource_group.main.location",
        service_plan_id: "azurerm_service_plan.web.id",
        virtual_network_subnet_id: "azurerm_subnet.app.id",
      }),
      res("azurerm_key_vault.main", { resource_group_name: "azurerm_resource_group.main.name", location: "azurerm_resource_group.main.location" }),
      res("azurerm_role_assignment.web_kv", { scope: "azurerm_key_vault.main.id", principal_id: "azurerm_linux_web_app.web.identity[0].principal_id" }),
      res("azurerm_cdn_frontdoor_profile.edge", { resource_group_name: "azurerm_resource_group.main.name", sku_name: '"Standard_AzureFrontDoor"' }),
      res("azurerm_cdn_frontdoor_endpoint.edge", { cdn_frontdoor_profile_id: "azurerm_cdn_frontdoor_profile.edge.id" }),
      res("azurerm_cdn_frontdoor_origin_group.web", { cdn_frontdoor_profile_id: "azurerm_cdn_frontdoor_profile.edge.id" }),
      res("azurerm_cdn_frontdoor_origin.web", { cdn_frontdoor_origin_group_id: "azurerm_cdn_frontdoor_origin_group.web.id", host_name: "azurerm_linux_web_app.web.default_hostname" }),
      res("azurerm_servicebus_namespace.bus", { resource_group_name: "azurerm_resource_group.main.name", location: "azurerm_resource_group.main.location", sku: '"Standard"' }),
      res("azurerm_servicebus_queue.orders", { namespace_id: "azurerm_servicebus_namespace.bus.id" }),
      res("azurerm_virtual_machine_extension.boot", { virtual_machine_id: "azurerm_linux_virtual_machine.app.id" }),
    ]),
  )
  const { card, group, cardIds, edge } = helpers(m)

  test("places services in resource group → region → VNet → subnet; global ones in the resource group", () => {
    expect(group("group:vpc:azurerm_virtual_network.main").parent).toBe("group:region:azure:eastus:group:account:azurerm_resource_group.main")
    expect(group("group:region:azure:eastus:group:account:azurerm_resource_group.main")).toMatchObject({ label: "Region · eastus", parent: "group:account:azurerm_resource_group.main" })
    expect(card("azurerm_linux_virtual_machine.app").parent).toBe("group:subnet:azurerm_subnet.app")
    // Front Door has no location: in the resource group itself.
    expect(card("azurerm_cdn_frontdoor_profile.edge").parent).toBe("group:logical:group:account:azurerm_resource_group.main:edge")
    // The NAT gateway sits in the VNet of the subnets it serves.
    expect(card("azurerm_nat_gateway.out").parent).toBe("group:vpc:azurerm_virtual_network.main")
  })

  test("folds NSGs, route tables, NICs, public IPs, private endpoints and DNS links", () => {
    for (const id of [
      "azurerm_network_security_group.web",
      "azurerm_network_security_group.vm",
      "azurerm_route_table.app",
      "azurerm_network_interface.vm",
      "azurerm_public_ip.vm",
      "azurerm_private_endpoint.sql",
      "azurerm_private_dns_zone.sql",
      "azurerm_service_plan.web",
      "azurerm_cdn_frontdoor_origin.web",
      "azurerm_servicebus_queue.orders",
      "azurerm_mssql_database.orders",
      "azurerm_virtual_machine_extension.boot",
    ])
      expect(cardIds).not.toContain(id)
    // Subnet association: a line on the subnet (allow rules only); routes with the NAT gateway's.
    expect(group("group:subnet:azurerm_subnet.app").sublabel).toEqual(["10.0.1.0/24", "routes: 0.0.0.0/0 → NVA 10.0.0.4, 0.0.0.0/0 → NAT", "NSG web-nsg: in 443 from anywhere"])
    // NIC association: a line on the machine using the NIC, which also brings its public IP.
    expect(card("azurerm_linux_virtual_machine.app").security).toEqual(["NSG vm-nsg: in 22 from VNet"])
    expect(card("azurerm_linux_virtual_machine.app").details).toEqual(["Standard_B2s", "public IP"])
    expect(card("azurerm_linux_virtual_machine.app").folded).toBe("+ Virtual machine extension")
    expect(card("azurerm_mssql_server.db").details).toEqual(["v12.0", "private DNS", "private endpoint"])
    expect(card("azurerm_mssql_server.db").folded).toBe("+ SQL Server database, Private DNS zone virtual network link")
    // A plan hosting one app sizes it.
    expect(card("azurerm_linux_web_app.web").details).toEqual(["Linux", "P1v3"])
  })

  test("draws the data flows of the services", () => {
    expect(edge("actor:users", "azurerm_cdn_frontdoor_profile.edge")?.kind).toBe("data")
    expect(edge("azurerm_cdn_frontdoor_profile.edge", "azurerm_linux_web_app.web")?.kind).toBe("data")
    expect(edge("azurerm_linux_web_app.web", "azurerm_key_vault.main")?.kind).toBe("control")
    // VNet integration: an arrow into the subnet, not a placement there.
    expect(card("azurerm_linux_web_app.web").parent).toBe("group:logical:group:region:azure:eastus:group:account:azurerm_resource_group.main:compute")
    expect(edge("azurerm_linux_web_app.web", "group:subnet:azurerm_subnet.app")).toBeDefined()
    expect(edge("azurerm_nat_gateway.out", "actor:internet")).toBeDefined()
    // The web app is behind Front Door: Users reach Front Door only.
    expect(edge("actor:users", "azurerm_linux_web_app.web")).toBeUndefined()
  })

  test("AzAPI resources are drawn as the azurerm service of their ARM type", () => {
    const azapi = deriveArchitecture(
      design("azapi", {}, [
        res("azapi_resource.rg", { type: '"Microsoft.Resources/resourceGroups@2024-03-01"', name: '"rg-api"', location: '"westeurope"' }),
        res("azapi_resource.plan", { type: '"Microsoft.Web/serverfarms@2024-04-01"', parent_id: "azapi_resource.rg.id", location: "azapi_resource.rg.location" }),
        res("azapi_resource.func", { type: '"Microsoft.Web/sites@2024-04-01"', parent_id: "azapi_resource.rg.id", location: "azapi_resource.rg.location", body: '{ kind = "functionapp", properties = { serverFarmId = azapi_resource.plan.id } }' }),
        res("azapi_resource.settings", { type: '"Microsoft.Web/sites/config@2024-04-01"', parent_id: "azapi_resource.func.id" }),
        res("azapi_resource.telemetry", { type: '"Microsoft.Resources/deployments@2024-03-01"', parent_id: "azapi_resource.rg.id" }),
      ]),
    )
    const a = helpers(azapi)
    expect(azapi.cards.map((c) => [c.id, c.type])).toEqual([["azapi_resource.func", "azurerm_linux_function_app"]])
    expect(a.group("group:account:azapi_resource.rg")).toMatchObject({ label: "Resource group · rg-api" })
    expect(a.card("azapi_resource.func").parent).toBe("group:logical:group:region:azure:westeurope:group:account:azapi_resource.rg:compute")
    expect(a.card("azapi_resource.func").folded).toBe("+ Service plan, Azapi child")
  })
})

describe("GCP", () => {
  const m = deriveArchitecture(
    design("google", { project: '"shop"', region: '"us-central1"' }, [
      res("google_compute_network.vpc", { auto_create_subnetworks: "false" }),
      res("google_compute_subnetwork.app", { network: "google_compute_network.vpc.id", region: '"us-central1"', ip_cidr_range: '"10.10.0.0/24"' }),
      res("google_compute_firewall.ssh", { name: '"allow-ssh"', network: "google_compute_network.vpc.name", source_ranges: '["35.235.240.0/20"]', target_tags: '["ssh"]' }, [
        ["allow", { protocol: '"tcp"', ports: '["22"]' }],
      ]),
      res("google_compute_firewall.hc", { name: '"allow-hc"', network: "google_compute_network.vpc.name", source_ranges: '["35.191.0.0/16", "130.211.0.0/22"]' }, [["allow", { protocol: '"tcp"', ports: '["80"]' }]]),
      res("google_compute_route.egress", { network: "google_compute_network.vpc.name", dest_range: '"0.0.0.0/0"', next_hop_gateway: '"default-internet-gateway"' }),
      res("google_compute_instance.bastion", { machine_type: '"e2-micro"', zone: '"us-central1-a"', tags: '["ssh"]' }, [["network_interface", { subnetwork: "google_compute_subnetwork.app.id" }]]),
      res("google_compute_router.nat", { network: "google_compute_network.vpc.id", region: '"us-central1"' }),
      res("google_compute_router_nat.nat", { router: "google_compute_router.nat.name", nat_ip_allocate_option: '"AUTO_ONLY"' }),
      res("google_compute_global_address.psa", { purpose: '"VPC_PEERING"', prefix_length: "16", network: "google_compute_network.vpc.id" }),
      res("google_service_networking_connection.psa", { network: "google_compute_network.vpc.id", reserved_peering_ranges: "[google_compute_global_address.psa.name]" }),
      res("google_sql_database_instance.db", { database_version: '"POSTGRES_16"', depends_on: "[google_service_networking_connection.psa]" }, [
        ["settings", { tier: '"db-custom-2-7680"' }, [["ip_configuration", { private_network: "google_compute_network.vpc.id" }]]],
      ]),
      res("google_vpc_access_connector.run", { network: "google_compute_network.vpc.name", region: '"us-central1"', ip_cidr_range: '"10.8.0.0/28"' }),
      res("google_secret_manager_secret.db", {}),
      res("google_cloud_run_v2_service.api", { location: '"us-central1"', ingress: '"INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"' }, [
        [
          "template",
          {},
          [
            ["vpc_access", { connector: "google_vpc_access_connector.run.id" }],
            ["containers", { image: '"api"' }, [["env", {}, [["value_source", {}, [["secret_key_ref", { secret: "google_secret_manager_secret.db.secret_id" }]]]]]]],
          ],
        ],
      ]),
      res("google_compute_region_network_endpoint_group.api", { region: '"us-central1"', network_endpoint_type: '"SERVERLESS"' }, [
        ["cloud_run", { service: "google_cloud_run_v2_service.api.name" }],
      ]),
      res("google_compute_backend_service.api", {}, [["backend", { group: "google_compute_region_network_endpoint_group.api.id" }]]),
      res("google_compute_url_map.api", { default_service: "google_compute_backend_service.api.id" }),
      res("google_compute_managed_ssl_certificate.api", {}),
      res("google_compute_target_https_proxy.api", { url_map: "google_compute_url_map.api.id", ssl_certificates: "[google_compute_managed_ssl_certificate.api.id]" }),
      res("google_compute_global_address.lb", {}),
      res("google_compute_global_forwarding_rule.https", { target: "google_compute_target_https_proxy.api.id", ip_address: "google_compute_global_address.lb.address", port_range: '"443"' }),
      // Port 80 redirects to HTTPS on the same IP: one load balancer, named after the HTTPS rule.
      res("google_compute_url_map.redirect", {}, [["default_url_redirect", { https_redirect: "true" }]]),
      res("google_compute_target_http_proxy.redirect", { url_map: "google_compute_url_map.redirect.id" }),
      res("google_compute_global_forwarding_rule.http", { target: "google_compute_target_http_proxy.redirect.id", ip_address: "google_compute_global_address.lb.address", port_range: '"80"' }),
      res("google_pubsub_topic.orders", {}),
      res("google_pubsub_subscription.orders_push", { topic: "google_pubsub_topic.orders.id" }, [["push_config", { push_endpoint: "google_cloud_run_v2_service.api.uri" }]]),
      res("google_cloud_scheduler_job.sync", { region: '"us-central1"', schedule: '"0 * * * *"' }, [["pubsub_target", { topic_name: "google_pubsub_topic.orders.id" }]]),
      res("google_cloud_run_v2_service_iam_member.invoker", { name: "google_cloud_run_v2_service.api.name", member: '"allUsers"' }),
    ]),
  )
  const { card, group, cardIds, edge } = helpers(m)

  test("places services in project → region → VPC → subnet, global ones in the Global group", () => {
    expect(group("group:vpc:google_compute_network.vpc").parent).toBe("group:region:gcp:us-central1:group:account:gcp:shop")
    expect(card("google_compute_instance.bastion").parent).toBe("group:subnet:google_compute_subnetwork.app")
    expect(card("google_compute_global_forwarding_rule.https").parent).toBe("group:logical:group:region:gcp:global:group:account:gcp:shop:edge")
    expect(group("group:region:gcp:global:group:account:gcp:shop")).toMatchObject({ label: "Global", kind: "region" })
    // The Global group comes before the regions.
    const order = m.groups.map((g) => g.id)
    expect(order.indexOf("group:region:gcp:global:group:account:gcp:shop")).toBeLessThan(order.indexOf("group:region:gcp:us-central1:group:account:gcp:shop"))
  })

  test("folds firewall rules into the cards they target (else the VPC) and routes into the VPC", () => {
    expect(cardIds).not.toContain("google_compute_firewall.ssh")
    expect(cardIds).not.toContain("google_compute_route.egress")
    expect(card("google_compute_instance.bastion").security).toEqual(["FW allow-ssh: in 22 from IAP"])
    expect(group("group:vpc:google_compute_network.vpc").sublabel).toEqual(["routes: 0.0.0.0/0 → internet gateway", "firewall allow-hc: in 80 from health checks"])
  })

  test("Cloud Router with NAT, private service access and the VPC connector", () => {
    expect(card("google_compute_router.nat").details).toEqual(["Cloud NAT"])
    expect(card("google_compute_router.nat").parent).toBe("group:vpc:google_compute_network.vpc")
    expect(edge("google_compute_router.nat", "actor:internet")).toBeDefined()
    expect(cardIds).not.toContain("google_compute_global_address.psa")
    expect(card("google_service_networking_connection.psa").details).toEqual(["/16 reserved range"])
    expect(edge("google_service_networking_connection.psa", "google_sql_database_instance.db")?.kind).toBe("data")
    expect(card("google_vpc_access_connector.run").parent).toBe("group:vpc:google_compute_network.vpc")
    expect(edge("google_cloud_run_v2_service.api", "google_vpc_access_connector.run")).toBeDefined()
  })

  test("a load balancer is one card from its forwarding rule to the serverless NEG", () => {
    for (const id of [
      "google_compute_global_forwarding_rule.http",
      "google_compute_target_https_proxy.api",
      "google_compute_target_http_proxy.redirect",
      "google_compute_url_map.api",
      "google_compute_url_map.redirect",
      "google_compute_backend_service.api",
      "google_compute_region_network_endpoint_group.api",
      "google_compute_managed_ssl_certificate.api",
      "google_compute_global_address.lb",
    ])
      expect(cardIds).not.toContain(id)
    expect(card("google_compute_global_forwarding_rule.https").details).toEqual(["external", "global", "static IP", "managed TLS", "HTTPS"])
    expect(card("google_compute_global_forwarding_rule.https").folded).toBeUndefined()
    expect(edge("google_compute_global_forwarding_rule.https", "google_cloud_run_v2_service.api")?.kind).toBe("data")
    // Cloud Run only takes traffic from the load balancer.
    expect(edge("actor:users", "google_compute_global_forwarding_rule.https")).toBeDefined()
    expect(edge("actor:users", "google_cloud_run_v2_service.api")).toBeUndefined()
  })

  test("Pub/Sub push, Scheduler and Secret Manager flows", () => {
    expect(cardIds).not.toContain("google_pubsub_subscription.orders_push")
    expect(card("google_pubsub_topic.orders").details).toEqual(["push subscription"])
    expect(edge("google_pubsub_topic.orders", "google_cloud_run_v2_service.api")?.kind).toBe("data")
    expect(edge("google_cloud_scheduler_job.sync", "google_pubsub_topic.orders")?.kind).toBe("data")
    expect(edge("google_cloud_run_v2_service.api", "google_secret_manager_secret.db")?.kind).toBe("control")
    expect(card("google_cloud_run_v2_service.api").details).toContain("public")
  })
})

describe("OCI", () => {
  const m = deriveArchitecture(
    design(
      "oci",
      { region: '"us-ashburn-1"' },
      [
        res("oci_identity_tag_namespace.arch", { compartment_id: "var.compartment_ocid" }),
        res("oci_core_vcn.main", { compartment_id: "var.compartment_ocid", cidr_blocks: '["10.0.0.0/16"]' }),
        res("oci_core_internet_gateway.igw", { compartment_id: "var.compartment_ocid", vcn_id: "oci_core_vcn.main.id" }),
        res("oci_core_nat_gateway.nat", { compartment_id: "var.compartment_ocid", vcn_id: "oci_core_vcn.main.id" }),
        res("oci_core_service_gateway.sgw", { compartment_id: "var.compartment_ocid", vcn_id: "oci_core_vcn.main.id" }),
        res("oci_core_route_table.public", { compartment_id: "var.compartment_ocid", vcn_id: "oci_core_vcn.main.id" }, [
          ["route_rules", { destination: '"0.0.0.0/0"', network_entity_id: "oci_core_internet_gateway.igw.id" }],
        ]),
        res("oci_core_route_table.private", { compartment_id: "var.compartment_ocid", vcn_id: "oci_core_vcn.main.id" }, [
          ["route_rules", { destination: '"0.0.0.0/0"', network_entity_id: "oci_core_nat_gateway.nat.id" }],
          ["route_rules", { destination: '"all-iad-services-in-oracle-services-network"', destination_type: '"SERVICE_CIDR_BLOCK"', network_entity_id: "oci_core_service_gateway.sgw.id" }],
        ]),
        res("oci_core_security_list.web", { compartment_id: "var.compartment_ocid", vcn_id: "oci_core_vcn.main.id", display_name: '"web"' }, [
          ["ingress_security_rules", { protocol: '"6"', source: '"0.0.0.0/0"' }, [["tcp_options", { min: "443", max: "443" }]]],
        ]),
        res("oci_core_subnet.public", {
          compartment_id: "var.compartment_ocid",
          vcn_id: "oci_core_vcn.main.id",
          cidr_block: '"10.0.1.0/24"',
          route_table_id: "oci_core_route_table.public.id",
          security_list_ids: "[oci_core_security_list.web.id]",
        }),
        res("oci_core_subnet.private", {
          compartment_id: "var.compartment_ocid",
          vcn_id: "oci_core_vcn.main.id",
          cidr_block: '"10.0.2.0/24"',
          availability_domain: '"Uocm:US-ASHBURN-AD-1"',
          prohibit_public_ip_on_vnic: "true",
          route_table_id: "var.private_routes ? oci_core_route_table.private.id : oci_core_route_table.public.id",
        }),
        res("oci_core_network_security_group.lb", { compartment_id: "var.compartment_ocid", vcn_id: "oci_core_vcn.main.id", display_name: '"lb"' }),
        res("oci_core_network_security_group.app", { compartment_id: "var.compartment_ocid", vcn_id: "oci_core_vcn.main.id", display_name: '"app"' }),
        res("oci_core_network_security_group_security_rule.app_in", {
          network_security_group_id: "oci_core_network_security_group.app.id",
          direction: '"INGRESS"',
          protocol: '"6"',
          source_type: '"NETWORK_SECURITY_GROUP"',
          source: "oci_core_network_security_group.lb.id",
        }, [["tcp_options", {}, [["destination_port_range", { min: "8080", max: "8080" }]]]]),
        res("oci_load_balancer_load_balancer.web", {
          compartment_id: "var.compartment_ocid",
          shape: '"flexible"',
          subnet_ids: "[oci_core_subnet.public.id]",
          network_security_group_ids: "[oci_core_network_security_group.lb.id]",
        }),
        res("oci_load_balancer_backend_set.app", { load_balancer_id: "oci_load_balancer_load_balancer.web.id" }),
        res("oci_load_balancer_backend.app", { load_balancer_id: "oci_load_balancer_load_balancer.web.id", backendset_name: "oci_load_balancer_backend_set.app.name", ip_address: "oci_core_instance.app.private_ip" }),
        res("oci_core_instance.app", { compartment_id: "var.compartment_ocid", shape: '"VM.Standard.E4.Flex"', availability_domain: "var.ad" }, [
          ["create_vnic_details", { subnet_id: "oci_core_subnet.private.id", nsg_ids: "[oci_core_network_security_group.app.id]" }],
          ["shape_config", { ocpus: "2", memory_in_gbs: "16" }],
        ]),
        res("oci_database_autonomous_database.db", { compartment_id: "var.compartment_ocid", db_workload: '"OLTP"', subnet_id: "oci_core_subnet.private.id", compute_count: "2" }),
        res("oci_core_drg.drg", { compartment_id: "var.compartment_ocid" }),
        res("oci_core_drg_attachment.main", { drg_id: "oci_core_drg.drg.id", vcn_id: "oci_core_vcn.main.id" }),
      ],
      { private_routes: "true" },
    ),
  )
  const { card, group, cardIds, edge } = helpers(m)

  test("places services in compartment → region → VCN → (AD) → subnet", () => {
    expect(group("group:vpc:oci_core_vcn.main").parent).toBe("group:region:oci:us-ashburn-1:group:account:oci:compartment")
    expect(group("group:account:oci:compartment")).toMatchObject({ label: "Compartment", parent: "group:cloud:oci" })
    expect(group("group:subnet:oci_core_subnet.private").parent).toBe("group:az:oci_core_vcn.main:Uocm:US-ASHBURN-AD-1")
    expect(group("group:az:oci_core_vcn.main:Uocm:US-ASHBURN-AD-1").label).toBe("Availability domain · Uocm:US-ASHBURN-AD-1")
    expect(card("oci_core_instance.app").parent).toBe("group:subnet:oci_core_subnet.private")
    expect(card("oci_load_balancer_load_balancer.web").parent).toBe("group:subnet:oci_core_subnet.public")
  })

  test("folds security lists, NSGs and route tables; gateways sit like AWS's", () => {
    for (const id of ["oci_core_security_list.web", "oci_core_network_security_group.app", "oci_core_route_table.private", "oci_identity_tag_namespace.arch", "oci_load_balancer_backend.app"])
      expect(cardIds).not.toContain(id)
    expect(group("group:subnet:oci_core_subnet.public")).toMatchObject({ tier: "public", sublabel: ["10.0.1.0/24", "routes: 0.0.0.0/0 → IGW", "SL web: in 443 from anywhere"] })
    // Only the branch of the conditional Terraform takes counts.
    expect(group("group:subnet:oci_core_subnet.private")).toMatchObject({ tier: "private", sublabel: ["10.0.2.0/24", "routes: 0.0.0.0/0 → NAT, Oracle services → Service gateway"] })
    expect(card("oci_core_instance.app").security).toEqual(["NSG app: in 8080 from NSG lb"])
    expect(card("oci_core_internet_gateway.igw").onBorder).toBe(true)
    expect(card("oci_core_nat_gateway.nat").parent).toBe("group:vpc:oci_core_vcn.main")
    expect(edge("actor:internet", "oci_core_internet_gateway.igw")).toBeDefined()
    expect(edge("oci_core_internet_gateway.igw", "group:subnet:oci_core_subnet.public")).toBeDefined()
  })

  test("load balancer flows, ADB facts and an opaque DRG attached to its VCN", () => {
    expect(edge("actor:users", "oci_load_balancer_load_balancer.web")).toBeDefined()
    expect(edge("oci_load_balancer_load_balancer.web", "oci_core_instance.app")?.kind).toBe("data")
    expect(card("oci_core_instance.app").details).toEqual(["VM.Standard.E4.Flex", "2 OCPU", "16 GB"])
    expect(card("oci_database_autonomous_database.db").details).toEqual(["Transaction Processing", "2 ECPU"])
    expect(card("oci_core_drg.drg").folded).toBe("+ Core DRG attachment")
    expect(edge("oci_core_drg.drg", "group:vpc:oci_core_vcn.main")).toBeDefined()
  })
})

describe("instances", () => {
  const counted = (environments: DeriveWorkflow["environments"], activeEnvironment?: string) =>
    deriveArchitecture({
      ...design("aws", { region: '"us-east-1"' }, [res("aws_sqs_queue.jobs"), res("aws_sns_topic.alerts", { count: "var.alerts ? 1 : 0" })], { alerts: "false" }),
      environments,
      activeEnvironment,
    }).cards.map((c) => c.id)

  test("blocks whose count is 0 are not drawn; with environments, what any of them creates is", () => {
    expect(counted([])).toEqual(["aws_sqs_queue.jobs"])
    const envs = [
      { name: "dev", values: { alerts: { expr: "true" } }, envVars: [] },
      { name: "prod", values: { alerts: { expr: "false" } }, envVars: [] },
    ]
    expect(counted(envs)).toEqual(["aws_sns_topic.alerts", "aws_sqs_queue.jobs"])
    expect(counted(envs, "prod")).toEqual(["aws_sqs_queue.jobs"])
  })
})

// Regressions found drawing the "Online shop" designs of each cloud.
describe("online shop · AWS", () => {
  const m = deriveArchitecture(
    design("aws", { region: '"us-east-1"' }, [
      res("aws_vpc.main", { cidr_block: '"10.0.0.0/16"' }),
      res("aws_default_security_group.main", { vpc_id: "aws_vpc.main.id" }),
      res("aws_internet_gateway.main", { vpc_id: "aws_vpc.main.id" }),
      res("aws_subnet.public", { vpc_id: "aws_vpc.main.id", cidr_block: '"10.0.0.0/24"' }),
      res("aws_subnet.data", { vpc_id: "aws_vpc.main.id", cidr_block: '"10.0.20.0/24"' }),
      res("aws_route_table.public", { vpc_id: "aws_vpc.main.id" }, [["route", { cidr_block: '"0.0.0.0/0"', gateway_id: "aws_internet_gateway.main.id" }]]),
      res("aws_route_table_association.public", { subnet_id: "aws_subnet.public.id", route_table_id: "aws_route_table.public.id" }),
      // The data tier's route table has no route at all.
      res("aws_route_table.data", { vpc_id: "aws_vpc.main.id" }),
      res("aws_route_table_association.data", { subnet_id: "aws_subnet.data.id", route_table_id: "aws_route_table.data.id" }),
      res("aws_db_subnet_group.main", { subnet_ids: "[aws_subnet.data.id]" }),
      res("aws_db_instance.main", { engine: '"postgres"', db_subnet_group_name: "aws_db_subnet_group.main.name" }),
      res("data.aws_ec2_managed_prefix_list.cloudfront", { name: '"com.amazonaws.global.cloudfront.origin-facing"' }),
      res("aws_security_group.alb", { vpc_id: "aws_vpc.main.id" }, [["ingress", { from_port: "443", to_port: "443", protocol: '"tcp"', prefix_list_ids: "[data.aws_ec2_managed_prefix_list.cloudfront.id]" }]]),
      res("aws_lb.main", { subnets: "[aws_subnet.public.id]", security_groups: "[aws_security_group.alb.id]" }),
      res("aws_route53_zone.main", { name: '"shop.example"' }),
      res("aws_acm_certificate.origin", { domain_name: '"origin.shop.example"' }),
      res("aws_route53_record.origin_cert_validation", { for_each: "{ for o in aws_acm_certificate.origin.domain_validation_options : o.domain_name => o }", zone_id: "aws_route53_zone.main.zone_id" }),
      res("aws_acm_certificate_validation.origin", {
        certificate_arn: "aws_acm_certificate.origin.arn",
        validation_record_fqdns: "[for r in aws_route53_record.origin_cert_validation : r.fqdn]",
      }),
      res("aws_lb_listener.https", { load_balancer_arn: "aws_lb.main.arn", certificate_arn: "aws_acm_certificate_validation.origin.certificate_arn" }),
      // CloudFront reaches the load balancer through a DNS name of the zone.
      res("aws_route53_record.origin", { zone_id: "aws_route53_zone.main.zone_id", name: '"origin"' }, [["alias", { name: "aws_lb.main.dns_name", zone_id: "aws_lb.main.zone_id" }]]),
      res("aws_cloudfront_distribution.cdn", {}, [
        ["origin", { domain_name: "aws_route53_record.origin.fqdn" }],
        ["viewer_certificate", { acm_certificate_arn: "aws_acm_certificate_validation.origin.certificate_arn" }],
      ]),
      res("aws_route53_record.apex", { zone_id: "aws_route53_zone.main.zone_id", name: '"shop.example"' }, [["alias", { name: "aws_cloudfront_distribution.cdn.domain_name" }]]),
      // Lookups only a bucket policy and a security group read.
      res("data.aws_elb_service_account.main"),
      res("data.aws_iam_policy_document.logs", {}, [["statement", {}, [["principals", { identifiers: "[data.aws_elb_service_account.main.arn]" }]]]]),
      res("aws_s3_bucket.logs"),
      res("aws_s3_bucket_policy.logs", { bucket: "aws_s3_bucket.logs.id", policy: "data.aws_iam_policy_document.logs.json" }),
      res("aws_ecr_repository.api"),
      res("aws_ecr_repository.checkout"),
      res("aws_ecr_lifecycle_policy.services", { for_each: "{ api = aws_ecr_repository.api.name, checkout = aws_ecr_repository.checkout.name }", repository: "each.value" }),
      res("aws_kms_key.main"),
      res("aws_iam_role.task"),
    ]),
  )
  const { card, group, cardIds, edge } = helpers(m)

  test("a CDN origin at a DNS name of the zone is an arrow to what the name points at", () => {
    expect(edge("aws_cloudfront_distribution.cdn", "aws_lb.main")?.kind).toBe("data")
    expect(edge("aws_route53_zone.main", "aws_lb.main")).toBeUndefined()
    expect(edge("aws_route53_zone.main", "aws_cloudfront_distribution.cdn")?.kind).toBe("data")
    // The load balancer is behind CloudFront: Users reach the zone only.
    expect(m.edges.filter((e) => e.source === "actor:users").map((e) => e.target)).toEqual(["aws_route53_zone.main"])
  })

  test("certificate validations are their certificate's, and so are the references to them", () => {
    for (const id of ["aws_acm_certificate_validation.origin", "aws_route53_record.origin_cert_validation"]) expect(cardIds).not.toContain(id)
    expect(card("aws_acm_certificate.origin").details).toEqual(["DNS validated"])
    expect(card("aws_acm_certificate.origin").folded).toBeUndefined()
    expect(card("aws_route53_zone.main").folded).toBe("+ 2 Route 53 records")
    expect(edge("aws_cloudfront_distribution.cdn", "aws_acm_certificate.origin")?.kind).toBe("control")
    // Through the HTTPS listener folded into the load balancer.
    expect(edge("aws_lb.main", "aws_acm_certificate.origin")?.kind).toBe("control")
  })

  test("lookups of account IDs and address lists are not infrastructure", () => {
    expect(cardIds).not.toContain("data.aws_elb_service_account.main")
    expect(cardIds).not.toContain("data.aws_ec2_managed_prefix_list.cloudfront")
  })

  test("a helper made for each of several cards is in each of them", () => {
    expect(card("aws_ecr_repository.api").folded).toBe("+ ECR lifecycle policy")
    expect(card("aws_ecr_repository.checkout").folded).toBe("+ ECR lifecycle policy")
  })

  test("global IAM, isolated subnets and the default security group", () => {
    expect(group("group:logical:group:cloud:aws:security").label).toBe("IAM")
    expect(group("group:logical:group:region:aws:us-east-1:security").label).toBe("Identity & security")
    expect(group("group:subnet:aws_subnet.data")).toMatchObject({ label: "Isolated subnet", tier: "private" })
    expect(group("group:subnet:aws_subnet.public").label).toBe("Public subnet")
    expect(cardIds).not.toContain("aws_default_security_group.main")
    expect(group("group:vpc:aws_vpc.main").sublabel).toEqual(["10.0.0.0/16", "default SG: deny all"])
  })
})

describe("online shop · GCP", () => {
  const m = deriveArchitecture({
    ...design("google", { project: '"shop"', region: '"us-central1"' }, [
      res("google_redis_instance.cache", { region: '"us-central1"', tier: '"STANDARD_HA"' }),
      res("google_secret_manager_secret.redis_auth", {}),
      // The version stores Redis' auth string: it is still its secret's.
      res("google_secret_manager_secret_version.redis_auth", { secret: "google_secret_manager_secret.redis_auth.id", secret_data: "google_redis_instance.cache.auth_string" }),
      res("google_artifact_registry_repository.containers", { location: '"us-central1"' }),
      res("google_cloud_run_v2_service.api", { location: '"us-central1"' }, [
        [
          "template",
          {},
          [
            [
              "containers",
              { image: "local.api_image" },
              [["env", {}, [["value_source", {}, [["secret_key_ref", { secret: "google_secret_manager_secret.redis_auth.secret_id", version: "google_secret_manager_secret_version.redis_auth.version" }]]]]]],
            ],
          ],
        ],
      ]),
    ]),
    // The image names the registry through two locals.
    locals: [
      { name: "registry", value: { expr: '"${google_artifact_registry_repository.containers.location}-docker.pkg.dev/shop/${google_artifact_registry_repository.containers.repository_id}"' } },
      { name: "api_image", value: { expr: '"${local.registry}/api:1.0.0"' } },
    ],
  })
  const { card, cardIds, edge } = helpers(m)

  test("a secret version is in its secret, not in what it stores", () => {
    expect(cardIds).not.toContain("google_secret_manager_secret_version.redis_auth")
    expect(card("google_secret_manager_secret.redis_auth").folded).toBe("+ Secret manager secret version")
    expect(card("google_redis_instance.cache").folded).toBeUndefined()
    expect(edge("google_cloud_run_v2_service.api", "google_secret_manager_secret.redis_auth")?.kind).toBe("control")
    expect(edge("google_secret_manager_secret.redis_auth", "google_redis_instance.cache")).toBeUndefined()
  })

  test("references through locals are followed", () => {
    expect(edge("google_cloud_run_v2_service.api", "google_artifact_registry_repository.containers")?.kind).toBe("control")
  })
})

describe("online shop · Azure", () => {
  const rg = { resource_group_name: "azurerm_resource_group.main.name", location: "azurerm_resource_group.main.location" }
  const m = deriveArchitecture(
    design("azurerm", {}, [
      res("azurerm_resource_group.main", { name: '"rg-shop"', location: '"eastus"' }),
      res("azurerm_virtual_network.main", { ...rg, address_space: '["10.40.0.0/16"]' }),
      res("azurerm_subnet.app", { virtual_network_name: "azurerm_virtual_network.main.name", address_prefixes: '["10.40.1.0/24"]' }),
      res("azurerm_subnet.data", { virtual_network_name: "azurerm_virtual_network.main.name", address_prefixes: '["10.40.3.0/24"]' }),
      res("azurerm_subnet.endpoints", { virtual_network_name: "azurerm_virtual_network.main.name", address_prefixes: '["10.40.4.0/24"]' }),
      res("azurerm_user_assigned_identity.api", rg),
      res("azurerm_key_vault.main", rg),
      res("azurerm_key_vault_secret.db", { key_vault_id: "azurerm_key_vault.main.id" }),
      res("azurerm_redis_cache.main", rg),
      res("azurerm_redis_cache_access_policy_assignment.api", { redis_cache_id: "azurerm_redis_cache.main.id", object_id: "azurerm_user_assigned_identity.api.principal_id" }),
      res("azurerm_servicebus_namespace.main", rg),
      res("azurerm_servicebus_queue.orders", { namespace_id: "azurerm_servicebus_namespace.main.id" }),
      res("azurerm_role_assignment.api_orders", { scope: "azurerm_servicebus_queue.orders.id", principal_id: "azurerm_user_assigned_identity.api.principal_id" }),
      res("azurerm_linux_web_app.api", {
        ...rg,
        virtual_network_subnet_id: "azurerm_subnet.app.id",
        app_settings:
          "{ DB_PASSWORD = \"@Microsoft.KeyVault(SecretUri=${azurerm_key_vault_secret.db.versionless_id})\", REDIS_USER = azurerm_redis_cache_access_policy_assignment.api.object_id_alias, QUEUE = azurerm_servicebus_queue.orders.name }",
      }, [["identity", { identity_ids: "[azurerm_user_assigned_identity.api.id]" }]]),
      res("azurerm_function_app_flex_consumption.orders", { ...rg, public_network_access_enabled: "false" }),
      res("azurerm_storage_account.web", rg),
      res("azurerm_storage_account_static_website.web", { storage_account_id: "azurerm_storage_account.web.id", index_document: '"index.html"' }),
      res("azurerm_private_endpoint.redis", { ...rg, subnet_id: "azurerm_subnet.endpoints.id" }, [["private_service_connection", { private_connection_resource_id: "azurerm_redis_cache.main.id" }]]),
      res("azurerm_private_endpoint.vault", { ...rg, subnet_id: "azurerm_subnet.endpoints.id" }, [["private_service_connection", { private_connection_resource_id: "azurerm_key_vault.main.id" }]]),
    ]),
  )
  const { card, group, cardIds, edge } = helpers(m)

  test("an app with its public endpoint turned off is no entry for Users", () => {
    expect(edge("actor:users", "azurerm_function_app_flex_consumption.orders")).toBeUndefined()
    expect(card("azurerm_function_app_flex_consumption.orders").details).toContain("private")
    expect(edge("actor:users", "azurerm_linux_web_app.api")).toBeDefined()
  })

  test("reading a vault's secret reads the vault", () => {
    expect(edge("azurerm_linux_web_app.api", "azurerm_key_vault.main")?.kind).toBe("control")
  })

  test("location-less sub-resources fold into their parent", () => {
    expect(cardIds).not.toContain("azurerm_redis_cache_access_policy_assignment.api")
    expect(card("azurerm_redis_cache.main").folded).toBe("+ Redis cache access policy assignment")
    expect(cardIds).not.toContain("azurerm_storage_account_static_website.web")
    expect(card("azurerm_storage_account.web").details).toContain("static website")
    expect(edge("azurerm_linux_web_app.api", "azurerm_redis_cache.main")?.kind).toBe("data")
  })

  test("services behind private endpoints are drawn in the endpoints' subnet", () => {
    expect(card("azurerm_redis_cache.main").parent).toBe("group:subnet:azurerm_subnet.endpoints")
    expect(card("azurerm_key_vault.main").parent).toBe("group:subnet:azurerm_subnet.endpoints")
    expect(group("group:subnet:azurerm_subnet.app").label).toBe("Private subnet · App")
    expect(group("group:subnet:azurerm_subnet.data").label).toBe("Private subnet · Data")
  })

  test("a managed identity's grants are its workload's arrows, the identity only a dashed line", () => {
    expect(edge("azurerm_user_assigned_identity.api", "azurerm_servicebus_namespace.main")).toBeUndefined()
    expect(edge("azurerm_user_assigned_identity.api", "azurerm_redis_cache.main")).toBeUndefined()
    expect(edge("azurerm_linux_web_app.api", "azurerm_servicebus_namespace.main")?.kind).toBe("data")
    expect(edge("azurerm_linux_web_app.api", "azurerm_user_assigned_identity.api")?.kind).toBe("control")
  })
})

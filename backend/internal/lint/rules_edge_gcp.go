package lint

import (
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Google Cloud edge and GKE rules (see rules_edge_aws.go).
func init() {
	rules = append(rules,
		edgeRule(warning("bp_gcp_ssl_policy_weak", "GCP: SSL policies require TLS 1.2", CategorySecurity,
			"google_compute_ssl_policy resources use the RESTRICTED profile, or MODERN/CUSTOM with min_tls_version = \"TLS_1_2\"; the defaults (COMPATIBLE, TLS_1_0) allow weak ciphers (CIS GCP 3.11)."),
			"https://cloud.google.com/load-balancing/docs/ssl-policies-concepts", false, checkGCPSSLPolicyWeak,
			model.PillarSecurity),
		edgeRule(warning("bp_gcp_https_proxy_ssl_policy", "GCP: HTTPS and SSL proxies set an SSL policy", CategorySecurity,
			"Target HTTPS and SSL proxies set ssl_policy; without one they use Google's default policy, which accepts TLS 1.0 (CIS GCP 3.11)."),
			"https://cloud.google.com/load-balancing/docs/use-ssl-policies", true, checkGCPHTTPSProxySSLPolicy,
			model.PillarSecurity),
		edgeRule(info("bp_gcp_cloud_armor", "GCP: external backend services have Cloud Armor", CategorySecurity,
			"Backend services of external load balancers set security_policy to a Cloud Armor policy (WAF rules and rate limiting at Google's edge)."),
			"https://cloud.google.com/armor/docs/security-policy-overview", true, checkGCPCloudArmor,
			model.PillarSecurity),
		edgeRule(info("bp_gcp_armor_rules", "GCP: Cloud Armor policies filter and rate-limit", CategorySecurity,
			"Cloud Armor security policies have rules besides the default one, including a throttle or rate_based_ban rule."),
			"https://cloud.google.com/armor/docs/rate-limiting-overview", false, checkGCPArmorRules,
			model.PillarSecurity, model.PillarReliability),
		edgeRule(info("bp_gcp_cloud_run_public", "GCP: Cloud Run services are public only on purpose", CategorySecurity,
			"Cloud Run services granting roles/run.invoker to allUsers or allAuthenticatedUsers are only reachable through a load balancer (ingress internal/load balancer) or are labeled public = \"true\"."),
			"https://cloud.google.com/run/docs/authenticating/public", false, checkGCPCloudRunPublic,
			model.PillarSecurity),
		edgeRule(warning("bp_gcp_gke_legacy_abac", "GCP: GKE clusters don't use legacy ABAC", CategorySecurity,
			"GKE clusters don't set enable_legacy_abac = true, which grants broad static permissions outside Kubernetes RBAC."),
			"https://cloud.google.com/kubernetes-engine/docs/how-to/hardening-your-cluster#leave_abac_disabled", false, checkGCPGKELegacyABAC,
			model.PillarSecurity),
		edgeRule(info("bp_gcp_gke_release_channel", "GCP: GKE clusters follow a release channel", CategorySecurity,
			"GKE clusters don't opt out of release channels (release_channel { channel = \"UNSPECIFIED\" }), so security patches arrive automatically."),
			"https://cloud.google.com/kubernetes-engine/docs/concepts/release-channels", false, checkGCPGKEReleaseChannel,
			model.PillarSecurity, model.PillarOperations),
		edgeRule(warning("bp_gcp_gke_private_nodes", "GCP: GKE nodes have no public IPs", CategorySecurity,
			"GKE clusters set private_cluster_config { enable_private_nodes = true }, so nodes are only reachable inside the VPC (egress through Cloud NAT)."),
			"https://cloud.google.com/kubernetes-engine/docs/concepts/network-isolation", true, checkGCPGKEPrivateNodes,
			model.PillarSecurity),
		edgeRule(warning("bp_gcp_gke_control_plane_networks", "GCP: GKE control planes restrict their sources", CategorySecurity,
			"GKE clusters have master_authorized_networks_config (or a private endpoint only), so the Kubernetes API isn't reachable from the whole internet."),
			"https://cloud.google.com/kubernetes-engine/docs/how-to/authorized-networks", true, checkGCPGKEControlPlaneNetworks,
			model.PillarSecurity),
		edgeRule(warning("bp_gcp_gke_workload_identity", "GCP: GKE workloads use Workload Identity", CategorySecurity,
			"Standard GKE clusters set workload_identity_config { workload_pool = \"<project>.svc.id.goog\" }, so pods get their own IAM identity instead of the node's service account (Autopilot has it on)."),
			"https://cloud.google.com/kubernetes-engine/docs/how-to/workload-identity", true, checkGCPGKEWorkloadIdentity,
			model.PillarSecurity),
	)
}

// gcpWeakSSL says why a google_compute_ssl_policy body allows weak TLS (""
// when it doesn't or can't be read).
func gcpWeakSSL(b *model.Body) string {
	profile := "COMPATIBLE"
	if getAttr(b, "profile") != nil {
		v, ok := attrString(b, "profile")
		if !ok {
			return ""
		}
		profile = v
	}
	if profile == "RESTRICTED" {
		return ""
	}
	minTLS := "TLS_1_0"
	if getAttr(b, "min_tls_version") != nil {
		v, ok := attrString(b, "min_tls_version")
		if !ok {
			return ""
		}
		minTLS = v
	}
	switch {
	case profile == "COMPATIBLE":
		return "uses the COMPATIBLE profile (weak ciphers)"
	case minTLS == "TLS_1_0" || minTLS == "TLS_1_1":
		return fmt.Sprintf("accepts TLS %s", strings.ReplaceAll(strings.TrimPrefix(minTLS, "TLS_"), "_", "."))
	}
	return ""
}

func checkGCPSSLPolicyWeak(p *pass) {
	for _, n := range p.ofType("google_compute_ssl_policy", "google_compute_region_ssl_policy") {
		why := gcpWeakSSL(&n.Body)
		if why == "" {
			continue
		}
		p.report(finding{
			node: n, attribute: "min_tls_version",
			message: fmt.Sprintf("%s %s", addr(n), why),
			why:     "TLS 1.0/1.1 and the COMPATIBLE cipher list allow downgrade attacks and ciphers with known weaknesses between clients and the load balancer.",
			fix:     "Set profile = \"MODERN\" and min_tls_version = \"TLS_1_2\" (or profile = \"RESTRICTED\").",
		})
	}
}

func checkGCPHTTPSProxySSLPolicy(p *pass) {
	for _, n := range p.ofType("google_compute_target_https_proxy", "google_compute_region_target_https_proxy", "google_compute_target_ssl_proxy") {
		if getAttr(&n.Body, "ssl_policy") != nil {
			continue
		}
		p.report(finding{
			node: n, attribute: "ssl_policy",
			message: fmt.Sprintf("%s uses Google's default SSL policy (TLS 1.0, COMPATIBLE ciphers)", addr(n)),
			why:     "Without an SSL policy the load balancer still negotiates TLS 1.0 and weak ciphers with any client that asks for them.",
			fix:     "Add a google_compute_ssl_policy with profile = \"MODERN\" and min_tls_version = \"TLS_1_2\" and set ssl_policy = google_compute_ssl_policy.<name>.id.",
		})
	}
}

// gcpExternalBackend: a backend service of an external load balancer
// (load_balancing_scheme unset, EXTERNAL or EXTERNAL_MANAGED).
func gcpExternalBackend(n *model.Node) bool {
	if getAttr(&n.Body, "load_balancing_scheme") == nil {
		return true
	}
	s, ok := attrString(&n.Body, "load_balancing_scheme")
	return ok && (s == "EXTERNAL" || s == "EXTERNAL_MANAGED")
}

func checkGCPCloudArmor(p *pass) {
	for _, n := range p.ofType("google_compute_backend_service") {
		if !gcpExternalBackend(n) {
			continue
		}
		if a := getAttr(&n.Body, "security_policy"); a != nil {
			if s, ok := attrString(&n.Body, "security_policy"); !ok || s != "" {
				continue
			}
		}
		p.report(finding{
			node: n, attribute: "security_policy",
			message: fmt.Sprintf("%s has no Cloud Armor security policy", addr(n)),
			why:     "Every request from the internet reaches the backends: injection probes, bad bots and request floods are only stopped by the application, if at all.",
			fix:     "Add a google_compute_security_policy with preconfigured WAF rules (evaluatePreconfiguredWaf('sqli-v33-stable'), 'xss-v33-stable') and a throttle rule, and set security_policy = google_compute_security_policy.<name>.id.",
		})
	}
}

// gcpDefaultRulePriority is the priority of a Cloud Armor policy's default
// rule.
const gcpDefaultRulePriority = 2147483647

// gcpArmorRule reads a Cloud Armor rule: whether it is the default rule and
// whether it rate-limits; known is false when its priority or action is an
// expression.
func gcpArmorRule(b *model.Body) (isDefault, limits, known bool) {
	prio, okPrio := attrNumber(b, "priority")
	action, okAction := attrString(b, "action")
	if !okPrio || !okAction {
		return false, false, false
	}
	return prio == gcpDefaultRulePriority, action == "throttle" || action == "rate_based_ban", true
}

func checkGCPArmorRules(p *pass) {
	for _, n := range p.ofType("google_compute_security_policy") {
		if t, ok := attrString(&n.Body, "type"); getAttr(&n.Body, "type") != nil && (!ok || t != "CLOUD_ARMOR") || azHasDynamic(&n.Body, "rule") {
			continue
		}
		bodies := []*model.Body{}
		for _, r := range blocks(&n.Body, "rule") {
			bodies = append(bodies, &r.Body)
		}
		extra, unknown := p.referrers(n, nameOf(n, "name"), []string{"security_policy"}, "google_compute_security_policy_rule")
		if unknown {
			continue
		}
		for _, r := range extra {
			bodies = append(bodies, &r.Body)
		}
		filters, limits := false, false
		for _, b := range bodies {
			def, lim, known := gcpArmorRule(b)
			if !known {
				filters, limits = true, true
				break
			}
			filters = filters || !def
			limits = limits || lim
		}
		var msg string
		switch {
		case !filters:
			msg = fmt.Sprintf("%s has only its default rule", addr(n))
		case !limits:
			msg = fmt.Sprintf("%s has no throttle or rate_based_ban rule", addr(n))
		default:
			continue
		}
		p.report(finding{
			node: n, attribute: "rule",
			message: msg,
			why:     "A policy with only the default rule lets every request through; without a rate-limiting rule one client can still flood, scrape or brute-force the backends (OWASP API4:2023).",
			fix:     "Add rules with preconfigured WAF expressions (evaluatePreconfiguredWaf('sqli-v33-stable')) and a rule with action = \"throttle\" and rate_limit_options { rate_limit_threshold { count = 500, interval_sec = 60 }, conform_action = \"allow\", exceed_action = \"deny(429)\", enforce_on_key = \"IP\" }.",
		})
	}
}

// gcpRunInternal: the Cloud Run service only accepts traffic from the VPC
// or through a load balancer, or is labeled public on purpose. Unknown
// (an expression) counts as intent.
func gcpRunInternal(svc *model.Node) bool {
	if a := getAttr(&svc.Body, "labels"); a != nil {
		if m, ok := literal(a.Value).(map[string]any); ok {
			for k, v := range m {
				if strings.EqualFold(k, "public") && v == "true" {
					return true
				}
			}
		}
	}
	if getAttr(&svc.Body, "ingress") == nil {
		return false
	}
	ing, ok := attrString(&svc.Body, "ingress")
	return !ok || ing != "INGRESS_TRAFFIC_ALL"
}

func checkGCPCloudRunPublic(p *pass) {
	for _, n := range p.ofType("google_cloud_run_v2_service_iam_member", "google_cloud_run_v2_service_iam_binding",
		"google_cloud_run_service_iam_member", "google_cloud_run_service_iam_binding") {
		if role, _ := attrString(&n.Body, "role"); role != "roles/run.invoker" {
			continue
		}
		member := gcpPublicMember(&n.Body, "member", "members")
		if member == "" {
			continue
		}
		svcAttr := "name"
		if strings.HasPrefix(n.Type, "google_cloud_run_service_") {
			svcAttr = "service"
		}
		svc := p.referencedNode(&n.Body, svcAttr)
		if svc != nil && svc.Type == "google_cloud_run_v2_service" && gcpRunInternal(svc) {
			continue
		}
		target := "a Cloud Run service"
		if svc != nil {
			target = addr(svc)
		}
		p.report(finding{
			node: n, attribute: "member",
			message: fmt.Sprintf("%s lets %s invoke %s", addr(n), member, target),
			why:     "A service anyone can invoke is open to scraping, abuse and request floods that scale it (and the bill) up; often it was only meant to be called by a frontend, a load balancer or another service.",
			fix:     "Grant roles/run.invoker to the calling service account instead; for a public endpoint put it behind an external load balancer with Cloud Armor (ingress = \"INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER\"), or label the service public = \"true\" when it is meant to be open.",
		})
	}
}

// ---------------------------------------------------------------------------
// GKE

func checkGCPGKELegacyABAC(p *pass) {
	for _, n := range p.ofType("google_container_cluster") {
		if !attrIsTrue(&n.Body, "enable_legacy_abac") {
			continue
		}
		p.report(finding{
			node: n, attribute: "enable_legacy_abac",
			message: fmt.Sprintf("%s enables legacy ABAC", addr(n)),
			why:     "Legacy ABAC grants broad, statically configured permissions that bypass Kubernetes RBAC: service accounts and users get far more access than they need.",
			fix:     "Remove enable_legacy_abac (false is the default) and grant access with RBAC Roles and RoleBindings or IAM.",
		})
	}
}

func checkGCPGKEReleaseChannel(p *pass) {
	for _, n := range p.ofType("google_container_cluster") {
		rc := azNested(&n.Body, "release_channel")
		if rc == nil {
			continue
		}
		if ch, _ := attrString(rc, "channel"); ch != "UNSPECIFIED" {
			continue
		}
		p.report(finding{
			node: n, attribute: "release_channel[0].channel",
			message: fmt.Sprintf("%s opts out of GKE release channels", addr(n)),
			why:     "Outside a release channel, control plane and node upgrades (with their security patches) are manual and easy to fall behind on.",
			fix:     "Set release_channel { channel = \"REGULAR\" } (\"STABLE\" for production if you prefer slower, more tested versions).",
		})
	}
}

func checkGCPGKEPrivateNodes(p *pass) {
	for _, n := range p.ofType("google_container_cluster") {
		if azHasDynamic(&n.Body, "private_cluster_config") {
			continue
		}
		if cfg := azNested(&n.Body, "private_cluster_config"); cfg != nil && attrNotFalse(cfg, "enable_private_nodes") {
			continue
		}
		p.report(finding{
			node: n, attribute: "private_cluster_config",
			message: fmt.Sprintf("%s gives its nodes public IP addresses", addr(n)),
			why:     "Nodes with public IPs can be reached from the internet: a NodePort, a misconfigured firewall rule or a kubelet vulnerability is then exposed.",
			fix:     "Add private_cluster_config { enable_private_nodes = true, master_ipv4_cidr_block = \"172.16.0.0/28\" } and a Cloud NAT (google_compute_router_nat) for egress.",
		})
	}
}

func checkGCPGKEControlPlaneNetworks(p *pass) {
	for _, n := range p.ofType("google_container_cluster") {
		if hasBlock(&n.Body, "master_authorized_networks_config") {
			continue
		}
		if cfg := azNested(&n.Body, "private_cluster_config"); cfg != nil && attrNotFalse(cfg, "enable_private_endpoint") {
			continue
		}
		p.report(finding{
			node: n, attribute: "master_authorized_networks_config",
			message: fmt.Sprintf("%s exposes its control plane to the whole internet", addr(n)),
			why:     "A Kubernetes API reachable from anywhere is scanned constantly; a leaked credential or a control-plane vulnerability is then exploitable from any address.",
			fix:     "Add master_authorized_networks_config { cidr_blocks { cidr_block = \"<office/VPN/CI CIDR>\", display_name = \"admin\" } }, or use a private endpoint.",
		})
	}
}

func checkGCPGKEWorkloadIdentity(p *pass) {
	for _, n := range p.ofType("google_container_cluster") {
		if attrNotFalse(&n.Body, "enable_autopilot") || hasBlock(&n.Body, "workload_identity_config") {
			continue
		}
		p.report(finding{
			node: n, attribute: "workload_identity_config",
			message: fmt.Sprintf("%s doesn't enable Workload Identity", addr(n)),
			why:     "Without Workload Identity every pod acts with the node's service account (and can read its metadata server credentials): one compromised pod gets the permissions of all of them.",
			fix:     "Add workload_identity_config { workload_pool = \"${var.project_id}.svc.id.goog\" } and node_config { workload_metadata_config { mode = \"GKE_METADATA\" } } on the node pools; bind Kubernetes service accounts to Google service accounts.",
		})
	}
}

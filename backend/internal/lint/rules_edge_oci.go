package lint

import (
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// OCI edge rules (see rules_edge_aws.go). OCI's current WAF is the
// oci_waf_web_app_firewall family (the edge WAAS policies are legacy).
func init() {
	rules = append(rules,
		edgeRule(info("bp_oci_apigw_rate_limit", "OCI: API deployments rate-limit clients", CategorySecurity,
			"API Gateway deployments set specification.request_policies.rate_limiting (requests per second per client IP or in total)."),
			"https://docs.oracle.com/en-us/iaas/Content/APIGateway/Tasks/apigatewaylimitingbackendaccess.htm", false, checkOCIAPIGWRateLimit,
			model.PillarSecurity, model.PillarReliability, model.PillarCost),
		edgeRule(info("bp_oci_apigw_auth", "OCI: API deployments authenticate callers", CategorySecurity,
			"API Gateway deployments set specification.request_policies.authentication (JWT or an authorizer function) unless they are public on purpose."),
			"https://docs.oracle.com/en-us/iaas/Content/APIGateway/Tasks/apigatewayaddingauthzauthn.htm", true, checkOCIAPIGWAuth,
			model.PillarSecurity),
		edgeRule(warning("bp_oci_lb_http_listener", "OCI: public load balancers don't serve plain HTTP", CategorySecurity,
			"HTTP listeners of public load balancers have an ssl_configuration or a rule set (an HTTP-to-HTTPS redirect) instead of serving plain HTTP."),
			"https://docs.oracle.com/en-us/iaas/Content/Balance/Tasks/managinglisteners.htm", true, checkOCILBHTTPListener,
			model.PillarSecurity),
		edgeRule(info("bp_oci_lb_waf", "OCI: public load balancers have a WAF", CategorySecurity,
			"Public load balancers are protected by an oci_waf_web_app_firewall (backend_type LOAD_BALANCER) with a firewall policy."),
			"https://docs.oracle.com/en-us/iaas/Content/WAF/Concepts/overview.htm", true, checkOCILBWAF,
			model.PillarSecurity),
		edgeRule(info("bp_oci_waf_rate_limit", "OCI: WAF policies rate-limit clients", CategorySecurity,
			"oci_waf_web_app_firewall_policy resources have request_rate_limiting rules, so one client can't flood the application."),
			"https://docs.oracle.com/en-us/iaas/Content/WAF/RateLimiting/rate_limiting_rule_management.htm", false, checkOCIWAFRateLimit,
			model.PillarSecurity, model.PillarReliability),
		edgeRule(info("bp_oci_secret_rotation", "OCI: vault secrets rotate automatically", CategorySecurity,
			"Vault secrets have rotation_config { is_scheduled_rotation_enabled = true } with a target system that rotates them."),
			"https://docs.oracle.com/en-us/iaas/Content/secret-management/Concepts/manage-secrets.htm", true, checkOCISecretRotation,
			model.PillarSecurity),
	)
}

// ociRequestPolicies returns the deployment-level request policies of an
// API deployment; dynamic reports a dynamic specification or
// request_policies block (not readable).
func ociRequestPolicies(n *model.Node) (b *model.Body, dynamic bool) {
	spec := azNested(&n.Body, "specification")
	if spec == nil {
		return nil, azHasDynamic(&n.Body, "specification")
	}
	if azHasDynamic(spec, "request_policies") {
		return nil, true
	}
	return azNested(spec, "request_policies"), false
}

func checkOCIAPIGWRateLimit(p *pass) {
	for _, n := range p.ofType("oci_apigateway_deployment") {
		pol, dynamic := ociRequestPolicies(n)
		if dynamic || pol != nil && hasBlock(pol, "rate_limiting") {
			continue
		}
		p.report(finding{
			node: n, attribute: "specification[0].request_policies",
			message: fmt.Sprintf("%s has no rate limiting", addr(n)),
			why:     "Without a rate limit one client can flood the backends (functions, instances) behind the gateway, scrape the API or brute-force it, and every call costs (OWASP API4:2023).",
			fix:     "Add specification { request_policies { rate_limiting { rate_in_requests_per_second = 10, rate_key = \"CLIENT_IP\" } } } (size it per environment).",
		})
	}
}

func checkOCIAPIGWAuth(p *pass) {
	for _, n := range p.ofType("oci_apigateway_deployment") {
		pol, dynamic := ociRequestPolicies(n)
		if dynamic || pol != nil && (hasBlock(pol, "authentication") || hasBlock(pol, "dynamic_authentication")) {
			continue
		}
		p.report(finding{
			node: n, attribute: "specification[0].request_policies",
			message: fmt.Sprintf("%s doesn't authenticate callers", addr(n)),
			why:     "A deployment without authentication answers anyone on the internet; unless it is public on purpose, its data and backends are one URL away from scrapers and attackers.",
			fix:     "Add specification { request_policies { authentication { type = \"JWT_AUTHENTICATION\" (issuers, audiences, public_keys) or \"CUSTOM_AUTHENTICATION\" with an authorizer function_id } } }.",
		})
	}
}

// ociPublicLB returns the load balancer a listener belongs to when it is a
// known public one (is_private unset or false), else nil.
func (p *pass) ociPublicLB(listener *model.Node) *model.Node {
	lb := p.referencedNode(&listener.Body, "load_balancer_id")
	if lb == nil || lb.Type != "oci_load_balancer_load_balancer" || !missingOrFalse(&lb.Body, "is_private") {
		return nil
	}
	return lb
}

func checkOCILBHTTPListener(p *pass) {
	for _, n := range p.ofType("oci_load_balancer_listener") {
		proto, _ := attrString(&n.Body, "protocol")
		lb := p.ociPublicLB(n)
		if !strings.EqualFold(proto, "HTTP") || lb == nil || hasBlock(&n.Body, "ssl_configuration") || getAttr(&n.Body, "rule_set_names") != nil {
			continue
		}
		p.report(finding{
			node: n, attribute: "protocol",
			message: fmt.Sprintf("%s serves plain HTTP on public %s", addr(n), addr(lb)),
			why:     "Requests and responses (session cookies, tokens, form data) travel unencrypted between users and the load balancer, where anyone on the path can read or change them.",
			fix:     "Add an HTTPS listener (port 443, ssl_configuration with a certificate and protocols = [\"TLSv1.2\", \"TLSv1.3\"]) and give this listener a rule set with a REDIRECT rule to HTTPS.",
		})
	}
}

func checkOCILBWAF(p *pass) {
	for _, n := range p.ofType("oci_load_balancer_load_balancer") {
		if !missingOrFalse(&n.Body, "is_private") || p.targeted(n, "", []string{"load_balancer_id"}, "oci_waf_web_app_firewall") {
			continue
		}
		p.report(finding{
			node:    n,
			message: fmt.Sprintf("%s is public without a web application firewall", addr(n)),
			why:     "Every request from the internet reaches the backends: injection probes, bad bots and request floods are only stopped by the application, if at all.",
			fix:     fmt.Sprintf("Add an oci_waf_web_app_firewall_policy (request_protection rules and request_rate_limiting) and an oci_waf_web_app_firewall with backend_type = \"LOAD_BALANCER\" and load_balancer_id = %s.id.", addr(n)),
		})
	}
}

func checkOCIWAFRateLimit(p *pass) {
	for _, n := range p.ofType("oci_waf_web_app_firewall_policy") {
		if azHasDynamic(&n.Body, "request_rate_limiting") {
			continue
		}
		if rl := azNested(&n.Body, "request_rate_limiting"); rl != nil && hasBlock(rl, "rules") {
			continue
		}
		p.report(finding{
			node: n, attribute: "request_rate_limiting",
			message: fmt.Sprintf("%s has no rate limiting rules", addr(n)),
			why:     "Protection rules block known exploits, not volume: without rate limiting one client can flood the application, scrape it or brute-force logins (OWASP API4:2023).",
			fix:     "Add request_rate_limiting { rules { type = \"REQUEST_RATE_LIMITING\", name = \"per-ip\", action_name = <a RETURN_HTTP_RESPONSE action with code 429>, configurations { period_in_seconds = 60, requests_limit = 300 } } }.",
		})
	}
}

func checkOCISecretRotation(p *pass) {
	for _, n := range p.ofType("oci_vault_secret") {
		if azHasDynamic(&n.Body, "rotation_config") {
			continue
		}
		if cfg := azNested(&n.Body, "rotation_config"); cfg != nil && attrNotFalse(cfg, "is_scheduled_rotation_enabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "rotation_config",
			message: fmt.Sprintf("%s never rotates", addr(n)),
			why:     "A secret that never changes stays valid forever once it leaks (logs, a laptop, a former employee); rotation limits how long a leak is useful.",
			fix:     "Add rotation_config { is_scheduled_rotation_enabled = true, rotation_interval = \"P30D\", target_system_details { target_system_type = \"ADB\" (adb_id) or \"FUNCTION\" (function_id) } }.",
		})
	}
}

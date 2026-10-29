package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Edge and data rules: the public entry points of a design (API gateways,
// load balancers, CDNs, WAFs) and the secrets and queues behind them. The
// default ones flag a setting written the insecure way (plain HTTP to the
// internet, TLS 1.0 policies, an unthrottled stage, a WAF without a
// rate-based rule); the ones every starter would trip and that mostly matter
// in production (a WAF in front of every load balancer, access logs,
// rotation) are opt-in, and reviews run them anyway. The rules_edge_*.go
// files hold the same practices for each cloud.
func init() {
	rules = append(rules,
		edgeRule(info("bp_apigw_throttling", "API stages are throttled", CategorySecurity,
			"API Gateway stages set a throttling rate and burst: default_route_settings on HTTP/WebSocket stages, an aws_api_gateway_method_settings (or a usage plan) on REST stages."),
			"https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-request-throttling.html", false, checkAPIGWThrottling,
			model.PillarSecurity, model.PillarReliability, model.PillarCost),
		edgeRule(info("bp_apigw_access_logs", "API stages write access logs", CategorySecurity,
			"API Gateway stages (REST and HTTP/WebSocket) set access_log_settings with a CloudWatch log group, so every request leaves a record of who called what."),
			"https://docs.aws.amazon.com/apigateway/latest/developerguide/set-up-logging.html", true, checkAPIGWAccessLogs,
			model.PillarOperations, model.PillarSecurity),
		edgeRule(info("bp_apigw_route_auth", "API routes require authorization", CategorySecurity,
			"HTTP API routes set authorization_type and REST methods authorization to something other than NONE (or require an API key); CORS preflight and health/public paths are exempt."),
			"https://docs.aws.amazon.com/apigateway/latest/developerguide/apigateway-control-access-to-api.html", true, checkAPIGWRouteAuth,
			model.PillarSecurity),
		edgeRule(warning("bp_cloudfront_https", "CloudFront uses HTTPS end to end", CategorySecurity,
			"CloudFront cache behaviors don't set viewer_protocol_policy = \"allow-all\" and custom origins don't use origin_protocol_policy = \"http-only\" (S3 website origins, which only speak HTTP, are exempt) or SSLv3."),
			"https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/using-https.html", false, checkCloudFrontHTTPS,
			model.PillarSecurity),
		edgeRule(info("bp_cloudfront_waf", "CloudFront distributions have a WAF", CategorySecurity,
			"CloudFront distributions set web_acl_id to an AWS WAF web ACL (scope CLOUDFRONT), which filters common exploits and rate-limits clients at the edge."),
			"https://docs.aws.amazon.com/waf/latest/developerguide/cloudfront-features.html", true, checkCloudFrontWAF,
			model.PillarSecurity),
		edgeRule(warning("bp_alb_http_forward", "Internet-facing load balancers don't serve plain HTTP", CategorySecurity,
			"HTTP listeners of internet-facing load balancers redirect to HTTPS instead of forwarding requests in plain text."),
			"https://docs.aws.amazon.com/elasticloadbalancing/latest/application/create-https-listener.html", false, checkALBHTTPForward,
			model.PillarSecurity),
		edgeRule(warning("bp_lb_tls_policy", "Load balancer listeners accept TLS 1.2 or later only", CategorySecurity,
			"HTTPS and TLS listeners set ssl_policy to a TLS 1.2+ policy (ELBSecurityPolicy-TLS13-1-2-*); unset, the API default ELBSecurityPolicy-2016-08 still accepts TLS 1.0."),
			"https://docs.aws.amazon.com/elasticloadbalancing/latest/application/describe-ssl-policies.html", false, checkLBTLSPolicy,
			model.PillarSecurity),
		edgeRule(warning("bp_alb_drop_invalid_headers", "Application load balancers drop invalid headers", CategorySecurity,
			"Application load balancers set drop_invalid_header_fields = true, so requests with malformed HTTP headers (a request-smuggling vector) never reach the targets."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/elb-controls.html#elb-4", false, checkALBDropInvalidHeaders,
			model.PillarSecurity),
		edgeRule(info("bp_alb_waf", "Internet-facing application load balancers have a WAF", CategorySecurity,
			"Internet-facing application load balancers are protected by an aws_wafv2_web_acl_association with a regional web ACL."),
			"https://docs.aws.amazon.com/waf/latest/developerguide/waf-chapter.html", true, checkALBWAF,
			model.PillarSecurity),
		edgeRule(info("bp_waf_rate_limit", "WAF web ACLs rate-limit clients", CategorySecurity,
			"aws_wafv2_web_acl resources have a rule with a rate_based_statement, so one client can't flood the application (OWASP API4:2023)."),
			"https://docs.aws.amazon.com/waf/latest/developerguide/waf-rule-statement-type-rate-based.html", false, checkWAFRateLimit,
			model.PillarSecurity, model.PillarReliability),
		edgeRule(info("bp_secret_rotation", "Secrets rotate automatically", CategorySecurity,
			"Secrets Manager secrets have an aws_secretsmanager_secret_rotation, so a leaked credential stops working on the next rotation."),
			"https://docs.aws.amazon.com/secretsmanager/latest/userguide/rotating-secrets.html", true, checkSecretRotation,
			model.PillarSecurity),
		edgeRule(info("bp_sqs_encryption", "Queues are encrypted at rest", CategorySecurity,
			"SQS queues don't turn server-side encryption off (sqs_managed_sse_enabled = false) without a KMS key (kms_master_key_id)."),
			"https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-server-side-encryption.html", false, checkSQSEncryption,
			model.PillarSecurity),
	)
}

// edgeRule is a documented rule serving pillars (most relevant first).
func edgeRule(r model.BestPracticeRule, docs string, optIn bool, check func(*pass), pillars ...model.Pillar) rule {
	return waRule(r, docs, optIn, check, pillars...)
}

// hasBlock: b has a typ block, written out or generated by a dynamic block
// (counted as intent).
func hasBlock(b *model.Body, typ string) bool {
	return len(blocks(b, typ)) > 0 || azHasDynamic(b, typ)
}

// awsInternetLB returns the load balancer a listener belongs to when it is a
// known internet-facing one (internal unset or false), else nil.
func (p *pass) awsInternetLB(listener *model.Node) *model.Node {
	lb := p.referencedNode(&listener.Body, "load_balancer_arn")
	if lb == nil || lb.Type != "aws_lb" && lb.Type != "aws_alb" || !missingOrFalse(&lb.Body, "internal") {
		return nil
	}
	return lb
}

// awsALB: an application load balancer (load_balancer_type unset or
// "application").
func awsALB(n *model.Node) bool {
	if getAttr(&n.Body, "load_balancer_type") == nil {
		return true
	}
	t, ok := attrString(&n.Body, "load_balancer_type")
	return ok && t == "application"
}

// ---------------------------------------------------------------------------
// API Gateway

// throttles: settings set a throttling rate or burst (any value counts).
func throttles(b *model.Body) bool {
	return getAttr(b, "throttling_rate_limit") != nil || getAttr(b, "throttling_burst_limit") != nil
}

func checkAPIGWThrottling(p *pass) {
	why := "Without a stage limit every client shares the account-wide quota (10,000 requests per second): one script or bot can exhaust it for every API in the Region and run up the bill of the functions behind it (OWASP API4:2023)."
	for _, n := range p.ofType("aws_apigatewayv2_stage") {
		if azHasDynamic(&n.Body, "default_route_settings") || azHasDynamic(&n.Body, "route_settings") {
			continue
		}
		ok := false
		for _, typ := range []string{"default_route_settings", "route_settings"} {
			for _, b := range blocks(&n.Body, typ) {
				ok = ok || throttles(&b.Body)
			}
		}
		if ok {
			continue
		}
		p.report(finding{
			node: n, attribute: "default_route_settings",
			message: fmt.Sprintf("%s has no throttling limits", addr(n)),
			why:     why,
			fix:     "Add default_route_settings { throttling_rate_limit = 100, throttling_burst_limit = 200 } (size them to the expected traffic; variables per environment), and a WAF rate-based rule for per-client limits.",
		})
	}
	for _, n := range p.ofType("aws_api_gateway_stage") {
		settings, unknown := p.referrers(n, nameOf(n, "stage_name"), []string{"stage_name"}, "aws_api_gateway_method_settings")
		plans, unknownPlan := p.referrers(n, nameOf(n, "stage_name"), []string{"api_stages.stage"}, "aws_api_gateway_usage_plan")
		if unknown || unknownPlan {
			continue
		}
		ok := false
		for _, s := range settings {
			for _, b := range blocks(&s.Body, "settings") {
				ok = ok || throttles(&b.Body)
			}
			ok = ok || azHasDynamic(&s.Body, "settings")
		}
		for _, u := range plans {
			ok = ok || hasBlock(&u.Body, "throttle_settings")
		}
		if ok {
			continue
		}
		p.report(finding{
			node:    n,
			message: fmt.Sprintf("%s has no throttling limits", addr(n)),
			why:     why,
			fix:     fmt.Sprintf("Add an aws_api_gateway_method_settings with rest_api_id and stage_name = %s.stage_name, method_path = \"*/*\" and settings { throttling_rate_limit = 100, throttling_burst_limit = 200 }.", addr(n)),
		})
	}
}

func checkAPIGWAccessLogs(p *pass) {
	for _, n := range p.ofType("aws_api_gateway_stage", "aws_apigatewayv2_stage") {
		if hasBlock(&n.Body, "access_log_settings") {
			continue
		}
		p.report(finding{
			node: n, attribute: "access_log_settings",
			message: fmt.Sprintf("%s writes no access logs", addr(n)),
			why:     "Without access logs there is no record of who called which route, from where and with what result: abuse, credential stuffing or a broken client go unnoticed and can't be investigated.",
			fix:     "Add an aws_cloudwatch_log_group (with retention_in_days) and access_log_settings { destination_arn = aws_cloudwatch_log_group.<name>.arn, format = jsonencode({ requestId = \"$context.requestId\", ip = \"$context.identity.sourceIp\", routeKey = \"$context.routeKey\", status = \"$context.status\" }) }.",
		})
	}
}

// publicRoute matches the routes meant to be open: health checks and
// explicitly public paths.
var publicRoute = regexp.MustCompile(`(?i)/(health|healthz|ping|status|public)\b`)

func checkAPIGWRouteAuth(p *pass) {
	why := "A route without authorization answers anyone on the internet; unless it is meant to be public, its data and the backend behind it are one URL away from scrapers and attackers."
	for _, n := range p.ofType("aws_apigatewayv2_route") {
		key, known := attrString(&n.Body, "route_key")
		if !known || strings.HasPrefix(key, "OPTIONS ") || strings.HasPrefix(key, "$disconnect") || publicRoute.MatchString(key) || attrNotFalse(&n.Body, "api_key_required") {
			continue
		}
		if getAttr(&n.Body, "authorization_type") != nil {
			if t, ok := attrString(&n.Body, "authorization_type"); !ok || t != "NONE" {
				continue
			}
		}
		p.report(finding{
			node: n, attribute: "authorization_type",
			message: fmt.Sprintf("%s (%s) requires no authorization", addr(n), key),
			why:     why,
			fix:     "Set authorization_type = \"JWT\" (Cognito or any OIDC issuer) or \"CUSTOM\" with authorizer_id, or \"AWS_IAM\" for service-to-service calls; keep NONE only for routes that are public on purpose.",
		})
	}
	for _, n := range p.ofType("aws_api_gateway_method") {
		method, _ := attrString(&n.Body, "http_method")
		auth, known := attrString(&n.Body, "authorization")
		if !known || auth != "NONE" || method == "OPTIONS" || attrNotFalse(&n.Body, "api_key_required") {
			continue
		}
		if res := p.referencedNode(&n.Body, "resource_id"); res != nil && publicRoute.MatchString("/"+nameOf(res, "path_part")) {
			continue
		}
		p.report(finding{
			node: n, attribute: "authorization",
			message: fmt.Sprintf("%s requires no authorization", addr(n)),
			why:     why,
			fix:     "Set authorization = \"COGNITO_USER_POOLS\" or \"CUSTOM\" with authorizer_id, or \"AWS_IAM\"; keep NONE only for methods that are public on purpose.",
		})
	}
}

// ---------------------------------------------------------------------------
// CloudFront

// s3WebsiteOrigin: the origin is an S3 website endpoint, which only serves
// HTTP.
func s3WebsiteOrigin(origin *model.Body) bool {
	a := getAttr(origin, "domain_name")
	if a == nil {
		return false
	}
	src := source(a.Value)
	return strings.Contains(src, "website_endpoint") || strings.Contains(src, "s3-website")
}

func checkCloudFrontHTTPS(p *pass) {
	for _, n := range p.ofType("aws_cloudfront_distribution") {
		for _, typ := range []string{"default_cache_behavior", "ordered_cache_behavior"} {
			for i, b := range blocks(&n.Body, typ) {
				if v, _ := attrString(&b.Body, "viewer_protocol_policy"); v == "allow-all" {
					p.report(finding{
						node: n, attribute: fmt.Sprintf("%s[%d].viewer_protocol_policy", typ, i),
						message: fmt.Sprintf("%s serves viewers over plain HTTP (viewer_protocol_policy = \"allow-all\")", addr(n)),
						why:     "Over plain HTTP, cookies, tokens and page content can be read and changed by anyone on the network path (public Wi-Fi, a compromised router).",
						fix:     "Set viewer_protocol_policy = \"redirect-to-https\" (or \"https-only\" for APIs).",
					})
				}
			}
		}
		for i, o := range blocks(&n.Body, "origin") {
			cfg := azNested(&o.Body, "custom_origin_config")
			if cfg == nil || s3WebsiteOrigin(&o.Body) {
				continue
			}
			if v, _ := attrString(cfg, "origin_protocol_policy"); v == "http-only" {
				p.report(finding{
					node: n, attribute: fmt.Sprintf("origin[%d].custom_origin_config[0].origin_protocol_policy", i),
					message: fmt.Sprintf("%s reaches its origin over plain HTTP (origin_protocol_policy = \"http-only\")", addr(n)),
					why:     "Between CloudFront and the origin the requests (with cookies and authorization headers) cross the internet unencrypted.",
					fix:     "Set origin_protocol_policy = \"https-only\" with origin_ssl_protocols = [\"TLSv1.2\"] and give the origin a certificate (ACM on the load balancer).",
				})
			}
			if slices.Contains(attrStrings(cfg, "origin_ssl_protocols"), "SSLv3") {
				p.report(finding{
					node: n, attribute: fmt.Sprintf("origin[%d].custom_origin_config[0].origin_ssl_protocols", i),
					message: fmt.Sprintf("%s allows SSLv3 to its origin", addr(n)),
					why:     "SSLv3 is broken (POODLE): a network attacker can decrypt the traffic between CloudFront and the origin.",
					fix:     "Set origin_ssl_protocols = [\"TLSv1.2\"].",
				})
			}
		}
	}
}

func checkCloudFrontWAF(p *pass) {
	for _, n := range p.ofType("aws_cloudfront_distribution") {
		if a := getAttr(&n.Body, "web_acl_id"); a != nil {
			if s, ok := attrString(&n.Body, "web_acl_id"); !ok || s != "" {
				continue
			}
		}
		p.report(finding{
			node: n, attribute: "web_acl_id",
			message: fmt.Sprintf("%s has no WAF web ACL", addr(n)),
			why:     "Without a web ACL every request reaches the origin: SQL injection and XSS probes, bad bots and request floods are only stopped by the application, if at all.",
			fix:     "Add an aws_wafv2_web_acl with scope = \"CLOUDFRONT\" (created in us-east-1) with the AWSManagedRulesCommonRuleSet managed group and a rate_based_statement rule, and set web_acl_id = aws_wafv2_web_acl.<name>.arn.",
		})
	}
}

// ---------------------------------------------------------------------------
// Load balancers

func checkALBHTTPForward(p *pass) {
	for _, n := range p.ofType("aws_lb_listener", "aws_alb_listener") {
		proto, _ := attrString(&n.Body, "protocol")
		lb := p.awsInternetLB(n)
		if proto != "HTTP" || lb == nil {
			continue
		}
		for i, a := range blocks(&n.Body, "default_action") {
			if t, _ := attrString(&a.Body, "type"); t != "forward" {
				continue
			}
			p.report(finding{
				node: n, attribute: fmt.Sprintf("default_action[%d].type", i),
				message: fmt.Sprintf("%s forwards plain HTTP from the internet (%s)", addr(n), addr(lb)),
				why:     "Requests and responses (session cookies, tokens, form data) travel unencrypted between users and the load balancer, where anyone on the path can read or change them.",
				fix:     "Add an HTTPS listener (port 443, an ACM certificate_arn, ssl_policy = \"ELBSecurityPolicy-TLS13-1-2-2021-06\") and make this listener's default_action a redirect { port = \"443\", protocol = \"HTTPS\", status_code = \"HTTP_301\" }.",
			})
			break
		}
	}
}

// modernELBPolicy matches the predefined ELB security policies whose
// minimum is TLS 1.2 (or 1.3).
var modernELBPolicy = regexp.MustCompile(`^ELBSecurityPolicy-(TLS13-1-[23]|TLS-1-2|FS-1-2)-`)

func checkLBTLSPolicy(p *pass) {
	for _, n := range p.ofType("aws_lb_listener", "aws_alb_listener") {
		proto, _ := attrString(&n.Body, "protocol")
		if proto != "HTTPS" && proto != "TLS" {
			continue
		}
		var msg string
		switch policy, known := attrString(&n.Body, "ssl_policy"); {
		case getAttr(&n.Body, "ssl_policy") == nil:
			msg = fmt.Sprintf("%s has no ssl_policy (the API default ELBSecurityPolicy-2016-08 accepts TLS 1.0)", addr(n))
		case known && !modernELBPolicy.MatchString(policy):
			msg = fmt.Sprintf("%s uses %s, which accepts TLS versions older than 1.2", addr(n), policy)
		default:
			continue
		}
		p.report(finding{
			node: n, attribute: "ssl_policy",
			message: msg,
			why:     "TLS 1.0 and 1.1 have known weaknesses and are no longer accepted by PCI DSS and most compliance baselines; clients that still need them are rare.",
			fix:     "Set ssl_policy = \"ELBSecurityPolicy-TLS13-1-2-2021-06\" (TLS 1.3 and 1.2).",
		})
	}
}

func checkALBDropInvalidHeaders(p *pass) {
	for _, n := range p.ofType("aws_lb", "aws_alb") {
		if !awsALB(n) || !missingOrFalse(&n.Body, "drop_invalid_header_fields") {
			continue
		}
		p.report(finding{
			node: n, attribute: "drop_invalid_header_fields",
			message: fmt.Sprintf("%s forwards requests with invalid HTTP headers", addr(n)),
			why:     "Malformed header names are a building block of HTTP request smuggling and header injection against the targets behind the load balancer.",
			fix:     "Set drop_invalid_header_fields = true.",
		})
	}
}

func checkALBWAF(p *pass) {
	for _, n := range p.ofType("aws_lb", "aws_alb") {
		if !awsALB(n) || !missingOrFalse(&n.Body, "internal") || p.targeted(n, "", []string{"resource_arn"}, "aws_wafv2_web_acl_association") {
			continue
		}
		p.report(finding{
			node:    n,
			message: fmt.Sprintf("%s is internet-facing without a WAF web ACL", addr(n)),
			why:     "Every request from the internet reaches the targets: injection probes, bad bots and request floods are only stopped by the application, if at all.",
			fix:     fmt.Sprintf("Add an aws_wafv2_web_acl with scope = \"REGIONAL\" (AWSManagedRulesCommonRuleSet plus a rate_based_statement rule) and an aws_wafv2_web_acl_association with resource_arn = %s.arn.", addr(n)),
		})
	}
}

// ---------------------------------------------------------------------------
// WAF, secrets, queues

// wafRuleRateLimits reports whether a WAF rule body rate-limits; unknown
// when it references a rule group Nimbo can't read.
func wafRuleRateLimits(rule *model.Body) (yes, unknown bool) {
	st := azNested(rule, "statement")
	if st == nil {
		return false, false
	}
	if hasBlock(st, "rate_based_statement") {
		return true, false
	}
	return false, hasBlock(st, "rule_group_reference_statement")
}

func checkWAFRateLimit(p *pass) {
	for _, n := range p.ofType("aws_wafv2_web_acl") {
		if azHasDynamic(&n.Body, "rule") {
			continue
		}
		if a := getAttr(&n.Body, "rule_json"); a != nil {
			src := source(a.Value)
			if strings.Contains(src, "RateBasedStatement") || len(references(a.Value)) > 0 || strings.Contains(src, "(") {
				continue
			}
		}
		ok, unknown := false, false
		for _, r := range blocks(&n.Body, "rule") {
			yes, maybe := wafRuleRateLimits(&r.Body)
			ok, unknown = ok || yes, unknown || maybe
		}
		if ok || unknown {
			continue
		}
		p.report(finding{
			node: n, attribute: "rule",
			message: fmt.Sprintf("%s has no rate-based rule", addr(n)),
			why:     "Managed rule groups block known exploits, not volume: without a rate-based rule one client can flood the application, scrape it or brute-force logins (OWASP API4:2023).",
			fix:     "Add a rule with action { block {} } and statement { rate_based_statement { limit = 1000, aggregate_key_type = \"IP\" } } (limit per 5 minutes per IP; tune it to real traffic) and a visibility_config.",
		})
	}
}

func checkSecretRotation(p *pass) {
	for _, n := range p.ofType("aws_secretsmanager_secret") {
		if p.targeted(n, nameOf(n, "name"), []string{"secret_id"}, "aws_secretsmanager_secret_rotation") {
			continue
		}
		p.report(finding{
			node:    n,
			message: fmt.Sprintf("%s never rotates", addr(n)),
			why:     "A secret that never changes stays valid forever once it leaks (logs, a laptop, a former employee); rotation limits how long a leak is useful.",
			fix:     fmt.Sprintf("Add an aws_secretsmanager_secret_rotation with secret_id = %s.id, a rotation_lambda_arn (or a managed rotation for RDS master passwords) and rotation_rules { automatically_after_days = 30 }.", addr(n)),
		})
	}
}

func checkSQSEncryption(p *pass) {
	for _, n := range p.ofType("aws_sqs_queue") {
		if !hasLiteralFalse(&n.Body, "sqs_managed_sse_enabled") || getAttr(&n.Body, "kms_master_key_id") != nil {
			continue
		}
		p.report(finding{
			node: n, attribute: "sqs_managed_sse_enabled",
			message: fmt.Sprintf("%s turns server-side encryption off", addr(n)),
			why:     "Messages often carry personal data or tokens; unencrypted queues fail most compliance baselines and a storage-level leak exposes them in clear text.",
			fix:     "Remove sqs_managed_sse_enabled = false (SSE-SQS is the default) or set kms_master_key_id to a KMS key for key-level access control and audit.",
		})
	}
}

package advisor

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// AWS recommendations: WAF and throttling on public entry points, data
// protection, secrets, dead-letter queues, customer-managed keys,
// autoscaling, alarms, log retention and tracing.

func awsRecs(d *design) []rec {
	var out []rec
	awsEdge(d, &out)
	awsThrottling(d, &out)
	awsSecrets(d, &out)
	awsPrivateDatabase(d, &out)
	awsDataProtection(d, &out)
	awsDLQ(d, &out)
	awsEncryption(d, &out)
	awsAutoscaling(d, &out)
	awsAlarms(d, &out)
	awsLogRetention(d, &out)
	awsTracing(d, &out)
	return out
}

var (
	pillarsEdge    = []model.Pillar{model.PillarSecurity, model.PillarReliability}
	pillarsData    = []model.Pillar{model.PillarReliability, model.PillarSecurity}
	pillarsSecret  = []model.Pillar{model.PillarSecurity}
	pillarsAsync   = []model.Pillar{model.PillarReliability, model.PillarOperations}
	pillarsScaling = []model.Pillar{model.PillarPerformance, model.PillarReliability, model.PillarCost}
	pillarsAlarms  = []model.Pillar{model.PillarOperations, model.PillarReliability}
	pillarsLogs    = []model.Pillar{model.PillarOperations, model.PillarCost, model.PillarSecurity}
	pillarsTracing = []model.Pillar{model.PillarOperations, model.PillarPerformance}
)

// ---------------------------------------------------------------------------
// Public entry points

// awsRESTStages are the REST API stages that are reachable from the
// internet (endpoint type not PRIVATE).
func awsRESTStages(d *design) []*model.Node {
	var out []*model.Node
	for _, st := range d.ofType("aws_api_gateway_stage") {
		private := false
		for _, api := range d.referenced(st, "aws_api_gateway_rest_api") {
			if types, ok := literal(path(&api.Body, "endpoint_configuration"), "types").([]any); ok && slices.Contains(types, any("PRIVATE")) {
				private = true
			}
		}
		if !private {
			out = append(out, st)
		}
	}
	return out
}

// awsPublicALBs are the internet-facing application load balancers.
func awsPublicALBs(d *design) []*model.Node {
	var out []*model.Node
	for _, lb := range d.ofType("aws_lb", "aws_alb") {
		typ, _ := str(&lb.Body, "load_balancer_type")
		if isTrue(&lb.Body, "internal") || typ != "" && typ != "application" {
			continue
		}
		out = append(out, lb)
	}
	return out
}

// awsFronted reports an entry point behind a CloudFront distribution
// (the distribution refers to it or to its API).
func awsFronted(d *design, n *model.Node) bool {
	if d.referredBy(n, "aws_cloudfront_distribution") {
		return true
	}
	for _, api := range d.referenced(n, "aws_api_gateway_rest_api", "aws_apigatewayv2_api") {
		if d.referredBy(api, "aws_cloudfront_distribution") {
			return true
		}
	}
	return false
}

// awsWebACL returns an existing web ACL of scope, if any.
func awsWebACL(d *design, scope string) *model.Node {
	for _, acl := range d.ofType("aws_wafv2_web_acl") {
		if s, _ := str(&acl.Body, "scope"); s == scope {
			return acl
		}
	}
	return nil
}

// wafv2ACL is a web ACL with the AWS common and known-bad-inputs managed
// rule groups and a per-IP rate-based rule.
func wafv2ACL(name, scope string) string {
	rule := func(name string, priority int, action, statement string) string {
		return fmt.Sprintf(`
  rule {
    name = %q
    priority = %d
    %s
    statement {
      %s
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name = %q
      sampled_requests_enabled = true
    }
  }
`, name, priority, action, statement, name)
	}
	managed := func(group string) string {
		return fmt.Sprintf("managed_rule_group_statement {\n        name = %q\n        vendor_name = \"AWS\"\n      }", group)
	}
	none := "override_action {\n      none {}\n    }"
	return fmt.Sprintf(`resource "aws_wafv2_web_acl" %q {
  name = %q
  scope = %q

  default_action {
    allow {}
  }
%s%s%s
  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name = %q
    sampled_requests_enabled = true
  }
}`, name, strings.ReplaceAll(name, "_", "-"), scope,
		rule("aws-common", 1, none, managed("AWSManagedRulesCommonRuleSet")),
		rule("aws-known-bad-inputs", 2, none, managed("AWSManagedRulesKnownBadInputsRuleSet")),
		rule("rate-limit-per-ip", 3, "action {\n      block {}\n    }", "# Requests per client IP in a 5-minute window.\n      rate_based_statement {\n        limit = 2000\n        aggregate_key_type = \"IP\"\n      }"),
		strings.ReplaceAll(name, "_", "-"))
}

const wafSkip = "Internal-only entry points (private API, internal load balancer) and throwaway demos with no real users."

// awsEdge recommends WAF web ACLs (managed rules + rate-based rule) for
// public REST APIs and ALBs (regional) and CloudFront distributions; HTTP
// APIs cannot take a web ACL, so they get CloudFront with WAF in front
// (production).
func awsEdge(d *design, out *[]rec) {
	var regional []*model.Node
	for _, n := range slices.Concat(awsRESTStages(d), awsPublicALBs(d)) {
		d.public = true
		if !d.referredBy(n, "aws_wafv2_web_acl_association") && !awsFronted(d, n) {
			regional = append(regional, n)
		}
	}
	if len(regional) > 0 {
		var parts []string
		add := []Addition{}
		acl := awsWebACL(d, "REGIONAL")
		aclAddr := ""
		if acl != nil {
			aclAddr = addr(acl)
		} else {
			name := d.unique("aws_wafv2_web_acl", "regional")
			aclAddr = "aws_wafv2_web_acl." + name
			parts = append(parts, wafv2ACL(name, "REGIONAL"))
			add = append(add, Addition{Type: "aws_wafv2_web_acl", Purpose: "REGIONAL web ACL: AWS managed common and known-bad-inputs rule groups plus a per-IP rate-based rule"})
		}
		for _, n := range regional {
			parts = append(parts, fmt.Sprintf("resource \"aws_wafv2_web_acl_association\" %q {\n  resource_arn = %s.arn\n  web_acl_arn = %s.arn\n}",
				d.unique("aws_wafv2_web_acl_association", label(n, "waf")), addr(n), aclAddr))
		}
		add = append(add, Addition{Type: "aws_wafv2_web_acl_association", Purpose: "attaches the web ACL to each public REST API stage and load balancer"})
		d.add(out, prioEdge, Recommendation{
			ID:    "aws_public_api_waf",
			Title: "Put an AWS WAF web ACL with rate limiting in front of the public entry points",
			Why: "Public REST APIs and application load balancers take every request from the internet. A web ACL blocks common exploits (AWS managed rule groups) " +
				"and a rate-based rule stops a single client from flooding the API (OWASP API4:2023 unrestricted resource consumption), before your code and bill see the traffic.",
			Pillars: pillarsEdge,
			Nodes:   addrs(regional),
			Add:     add,
			Snippet: joinLines(parts...),
			Scope:   ScopeRecommended,
			Skip:    wafSkip,
			Rules:   []string{"bp_alb_waf", "bp_waf_rate_limit"},
			Docs:    "https://docs.aws.amazon.com/waf/latest/developerguide/waf-rule-statement-type-rate-based.html",
		})
	}

	var dists []*model.Node
	for _, n := range d.ofType("aws_cloudfront_distribution") {
		d.public = true
		if !set(&n.Body, "web_acl_id") {
			dists = append(dists, n)
		}
	}
	if len(dists) > 0 {
		var parts []string
		add := []Addition{}
		acl := awsWebACL(d, "CLOUDFRONT")
		aclAddr := ""
		if acl != nil {
			aclAddr = addr(acl)
		} else {
			name := d.unique("aws_wafv2_web_acl", "cloudfront")
			aclAddr = "aws_wafv2_web_acl." + name
			parts = append(parts, "# CLOUDFRONT-scope web ACLs are created in us-east-1: use a provider for that region if yours differs.\n"+wafv2ACL(name, "CLOUDFRONT"))
			add = append(add, Addition{Type: "aws_wafv2_web_acl", Purpose: "CLOUDFRONT web ACL: AWS managed rule groups plus a per-IP rate-based rule"})
		}
		for _, n := range dists {
			parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  web_acl_id = %s.arn\n}", addr(n), n.Type, n.Name, aclAddr))
		}
		add = append(add, Addition{Type: "aws_cloudfront_distribution", Purpose: "web_acl_id: the web ACL filters requests at the edge"})
		d.add(out, prioEdge, Recommendation{
			ID:      "aws_cloudfront_waf",
			Title:   "Attach an AWS WAF web ACL with rate limiting to the CloudFront distributions",
			Why:     "CloudFront is the public front door: a web ACL on it filters exploits and floods at the edge for every origin behind it (load balancers, APIs, buckets).",
			Pillars: pillarsEdge,
			Nodes:   addrs(dists),
			Add:     add,
			Snippet: joinLines(parts...),
			Scope:   ScopeRecommended,
			Skip:    "Static sites without forms or APIs, where a WAF only adds cost.",
			Rules:   []string{"bp_cloudfront_waf", "bp_waf_rate_limit"},
			Docs:    "https://docs.aws.amazon.com/waf/latest/developerguide/cloudfront-features.html",
		})
	}

	// HTTP APIs (API Gateway v2) cannot be associated with a web ACL.
	var httpAPIs []*model.Node
	for _, api := range d.ofType("aws_apigatewayv2_api") {
		d.public = true
		if p, _ := str(&api.Body, "protocol_type"); p == "HTTP" && !d.referredBy(api, "aws_cloudfront_distribution") {
			httpAPIs = append(httpAPIs, api)
		}
	}
	if len(httpAPIs) > 0 {
		aclName := d.unique("aws_wafv2_web_acl", "http_api_edge")
		parts := []string{"# CLOUDFRONT-scope web ACLs are created in us-east-1: use a provider for that region if yours differs.\n" + wafv2ACL(aclName, "CLOUDFRONT")}
		for _, api := range httpAPIs {
			parts = append(parts, fmt.Sprintf(`resource "aws_cloudfront_distribution" %q {
  enabled = true
  web_acl_id = aws_wafv2_web_acl.%s.arn

  origin {
    origin_id = "api"
    domain_name = replace(%s.api_endpoint, "https://", "")
    custom_origin_config {
      http_port = 80
      https_port = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols = ["TLSv1.2"]
    }
  }

  default_cache_behavior {
    target_origin_id = "api"
    viewer_protocol_policy = "https-only"
    allowed_methods = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
    cached_methods = ["GET", "HEAD"]
    cache_policy_id = "4135ea2d-6df8-44a3-9df3-4b5a84be39ad" # Managed-CachingDisabled
    origin_request_policy_id = "b689b0a8-53d0-40ab-baf2-68738e2966ac" # Managed-AllViewerExceptHostHeader
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }
}`, d.unique("aws_cloudfront_distribution", label(api, "edge")), aclName, addr(api)))
		}
		d.add(out, prioEdge, Recommendation{
			ID:    "aws_http_api_edge_waf",
			Title: "Serve the public HTTP API through CloudFront with an AWS WAF web ACL",
			Why: "AWS WAF cannot be associated with API Gateway HTTP APIs (only REST APIs, load balancers, CloudFront and a few others). To filter exploits and rate-limit per client IP, " +
				"put a CloudFront distribution with a web ACL in front, or use a REST API stage instead. Clients can still call the execute-api URL directly until you disable it (disable_execute_api_endpoint, with a custom domain).",
			Pillars: pillarsEdge,
			Nodes:   addrs(httpAPIs),
			Add: []Addition{
				{Type: "aws_wafv2_web_acl", Purpose: "CLOUDFRONT web ACL: AWS managed rule groups plus a per-IP rate-based rule"},
				{Type: "aws_cloudfront_distribution", Purpose: "edge entry point in front of the HTTP API, with the web ACL"},
			},
			Snippet: joinLines(parts...),
			Scope:   ScopeProduction,
			Skip:    "Development stages and internal APIs: stage throttling is enough there.",
			Rules:   []string{"bp_cloudfront_waf", "bp_waf_rate_limit", "bp_cloudfront_https"},
			Docs:    "https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-vs-rest.html",
		})
	}
}

// awsThrottling recommends stage throttling and access logs for every API
// Gateway stage (REST and HTTP).
func awsThrottling(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	add := map[string]Addition{}
	logGroup := func(st *model.Node) string {
		name := d.unique("aws_cloudwatch_log_group", label(st, "access"))
		parts = append(parts, fmt.Sprintf("resource \"aws_cloudwatch_log_group\" %q {\n  name = \"/aws/apigateway/%s-access\"\n  retention_in_days = 30\n}", name, dashed(st.Name)))
		add["aws_cloudwatch_log_group"] = Addition{Type: "aws_cloudwatch_log_group", Purpose: "access logs of each stage, with a retention period"}
		return "aws_cloudwatch_log_group." + name
	}
	const accessFormat = `jsonencode({ requestId = "$context.requestId", ip = "$context.identity.sourceIp", status = "$context.status", latency = "$context.responseLatency" })`
	for _, st := range d.ofType("aws_apigatewayv2_stage") {
		throttled := set(path(&st.Body, "default_route_settings"), "throttling_rate_limit")
		logged := set(&st.Body, "access_log_settings")
		if throttled && logged {
			continue
		}
		nodes = append(nodes, st)
		var body []string
		if !throttled {
			body = append(body, "  default_route_settings {\n    throttling_burst_limit = 100\n    throttling_rate_limit = 50\n  }")
			add["aws_apigatewayv2_stage"] = Addition{Type: "aws_apigatewayv2_stage", Purpose: "default_route_settings throttling (requests per second and burst) and access_log_settings"}
		}
		if !logged {
			body = append(body, fmt.Sprintf("  access_log_settings {\n    destination_arn = %s.arn\n    format = %s\n  }", logGroup(st), accessFormat))
			add["aws_apigatewayv2_stage"] = Addition{Type: "aws_apigatewayv2_stage", Purpose: "default_route_settings throttling (requests per second and burst) and access_log_settings"}
		}
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n%s\n}", addr(st), st.Type, st.Name, strings.Join(body, "\n")))
	}
	for _, st := range d.ofType("aws_api_gateway_stage") {
		throttled := false
		for _, ms := range d.referrers(st, "aws_api_gateway_method_settings") {
			if set(path(&ms.Body, "settings"), "throttling_rate_limit") {
				throttled = true
			}
		}
		logged := set(&st.Body, "access_log_settings")
		if throttled && logged {
			continue
		}
		nodes = append(nodes, st)
		if !throttled {
			apiID := expr(&st.Body, "rest_api_id")
			if apiID == "" {
				apiID = `"<rest-api-id>"`
			}
			parts = append(parts, fmt.Sprintf(`resource "aws_api_gateway_method_settings" %q {
  rest_api_id = %s
  stage_name = %s.stage_name
  method_path = "*/*"

  settings {
    throttling_burst_limit = 100
    throttling_rate_limit = 50
    metrics_enabled = true
  }
}`, d.unique("aws_api_gateway_method_settings", label(st, "all")), apiID, addr(st)))
			add["aws_api_gateway_method_settings"] = Addition{Type: "aws_api_gateway_method_settings", Purpose: "stage-wide throttling (method_path \"*/*\") and CloudWatch metrics"}
		}
		if !logged {
			parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  access_log_settings {\n    destination_arn = %s.arn\n    format = %s\n  }\n}", addr(st), st.Type, st.Name, logGroup(st), accessFormat))
			add["aws_api_gateway_stage"] = Addition{Type: "aws_api_gateway_stage", Purpose: "access_log_settings"}
		}
	}
	if len(nodes) == 0 {
		return
	}
	adds := []Addition{}
	for _, k := range sortedKeys(add) {
		adds = append(adds, add[k])
	}
	d.add(out, prioThrottle, Recommendation{
		ID:    "aws_api_throttling",
		Title: "Throttle the API Gateway stages and turn on access logs",
		Why: "Without stage throttling only the account-wide limit (10,000 requests per second by default) caps traffic: one client can exhaust it and the Lambda or container concurrency behind it. " +
			"Access logs are how you find who caused a spike or an error.",
		Pillars: []model.Pillar{model.PillarSecurity, model.PillarReliability, model.PillarOperations},
		Nodes:   addrs(nodes),
		Add:     adds,
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Set the limits from expected peak traffic; keep them generous in development rather than removing them.",
		Rules:   []string{"bp_apigw_throttling", "bp_apigw_access_logs"},
		Docs:    "https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-request-throttling.html",
	})
}

// ---------------------------------------------------------------------------
// Data stores

// awsSecrets recommends RDS-managed master passwords (Secrets Manager) for
// databases with a password in the configuration, and Secrets Manager for
// Lambda environment variables holding literal secrets.
func awsSecrets(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	adds := []Addition{}
	for _, db := range d.ofType("aws_db_instance", "aws_rds_cluster") {
		pw := "password"
		if db.Type == "aws_rds_cluster" {
			pw = "master_password"
		}
		if !set(&db.Body, pw) || isTrue(&db.Body, "manage_master_user_password") {
			continue
		}
		nodes = append(nodes, db)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s and remove %s:\nresource %q %q {\n  manage_master_user_password = true\n}", addr(db), pw, db.Type, db.Name))
	}
	if len(nodes) > 0 {
		adds = append(adds, Addition{Type: "aws_db_instance", Purpose: "manage_master_user_password = true: RDS generates the master password, stores it in Secrets Manager and rotates it"})
	}
	var fns []*model.Node
	for _, fn := range d.ofType("aws_lambda_function") {
		vars, _ := literal(path(&fn.Body, "environment"), "variables").(map[string]any)
		for _, k := range sortedKeys(vars) {
			if v, ok := vars[k].(string); ok && v != "" && secretKey.MatchString(k) {
				fns = append(fns, fn)
				name := d.unique("aws_secretsmanager_secret", label(fn, strings.ToLower(k)))
				parts = append(parts, fmt.Sprintf("# Replace %s's %s literal: store the value here (set it outside Terraform) and pass the ARN instead.\nresource \"aws_secretsmanager_secret\" %q {\n  name = %q\n}",
					addr(fn), k, name, strings.ReplaceAll(name, "_", "-")))
				break
			}
		}
	}
	if len(fns) > 0 {
		nodes = append(nodes, fns...)
		adds = append(adds, Addition{Type: "aws_secretsmanager_secret", Purpose: "holds each secret; the function reads it at runtime with secretsmanager:GetSecretValue on that ARN only"})
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioSecrets, Recommendation{
		ID:    "aws_secrets_manager",
		Title: "Keep credentials in AWS Secrets Manager, not in the configuration",
		Why: "Passwords written in Terraform end up in the state file, plan output and environment variables readable by anyone with read access to the function or state. " +
			"Secrets Manager stores them encrypted, audits access and can rotate them.",
		Pillars: pillarsSecret,
		Nodes:   addrs(nodes),
		Add:     adds,
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Non-secret configuration (feature flags, URLs): plain variables or SSM Parameter Store are fine.",
		Rules:   []string{"bp_sensitive_not_marked", "bp_secret_rotation"},
		Docs:    "https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/rds-secrets-manager.html",
	})
}

// awsPrivateDatabase recommends private placement for publicly accessible
// databases.
func awsPrivateDatabase(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, db := range d.ofType("aws_db_instance") {
		if isTrue(&db.Body, "publicly_accessible") {
			nodes = append(nodes, db)
			parts = append(parts, fmt.Sprintf("# Merge into the existing %s (and place it in private subnets through db_subnet_group_name):\nresource %q %q {\n  publicly_accessible = false\n}", addr(db), db.Type, db.Name))
		}
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioPrivate, Recommendation{
		ID:      "aws_private_database",
		Title:   "Keep the databases private",
		Why:     "A publicly accessible database gets a public DNS name: its security group becomes the only barrier against password guessing and engine exploits. Applications in the VPC reach it privately.",
		Pillars: pillarsSecret,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "aws_db_instance", Purpose: "publicly_accessible = false, in private subnets; only the application security group may connect"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Rules:   []string{"bp_rds_public"},
		Docs:    "https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_VPC.WorkingWithRDSInstanceinaVPC.html",
	})
}

// awsDataProtection recommends backups and point-in-time recovery for data
// stores: RDS backup retention, DynamoDB PITR, S3 versioning.
func awsDataProtection(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	add := map[string]Addition{}
	for _, db := range d.ofType("aws_db_instance", "aws_rds_cluster") {
		if days, ok := number(&db.Body, "backup_retention_period"); ok && days >= 7 || !ok && set(&db.Body, "backup_retention_period") {
			continue
		}
		if db.Type == "aws_db_instance" && set(&db.Body, "replicate_source_db") {
			continue
		}
		nodes = append(nodes, db)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  backup_retention_period = 7\n  copy_tags_to_snapshot = true\n}", addr(db), db.Type, db.Name))
		add[db.Type] = Addition{Type: db.Type, Purpose: "backup_retention_period of at least 7 days: automated backups with point-in-time restore"}
	}
	for _, t := range d.ofType("aws_dynamodb_table") {
		if isTrue(path(&t.Body, "point_in_time_recovery"), "enabled") {
			continue
		}
		nodes = append(nodes, t)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  point_in_time_recovery {\n    enabled = true\n  }\n}", addr(t), t.Type, t.Name))
		add[t.Type] = Addition{Type: t.Type, Purpose: "point_in_time_recovery: restore to any second of the last 35 days"}
	}
	for _, b := range d.ofType("aws_s3_bucket") {
		versioned := false
		for _, v := range d.referrers(b, "aws_s3_bucket_versioning") {
			if s, ok := str(path(&v.Body, "versioning_configuration"), "status"); !ok || s == "Enabled" {
				versioned = true
			}
		}
		if versioned || set(&b.Body, "versioning") {
			continue
		}
		nodes = append(nodes, b)
		parts = append(parts, fmt.Sprintf("resource \"aws_s3_bucket_versioning\" %q {\n  bucket = %s.id\n\n  versioning_configuration {\n    status = \"Enabled\"\n  }\n}", d.unique("aws_s3_bucket_versioning", b.Name), addr(b)))
		add["aws_s3_bucket_versioning"] = Addition{Type: "aws_s3_bucket_versioning", Purpose: "keeps overwritten and deleted objects recoverable (add a lifecycle rule to expire old versions)"}
	}
	if len(nodes) == 0 {
		return
	}
	adds := []Addition{}
	for _, k := range sortedKeys(add) {
		adds = append(adds, add[k])
	}
	d.add(out, prioData, Recommendation{
		ID:      "aws_data_protection",
		Title:   "Turn on backups and point-in-time recovery for the data stores",
		Why:     "A bad deploy, a wrong DELETE or ransomware can destroy data in seconds. Automated backups, point-in-time recovery and object versioning make it recoverable; they cost little next to the data they protect.",
		Pillars: pillarsData,
		Nodes:   addrs(nodes),
		Add:     adds,
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Scratch data you can recreate from source (build caches, test fixtures).",
		Rules:   []string{"bp_rds_backup_retention", "bp_dynamodb_pitr", "bp_s3_versioning"},
		Docs:    "https://docs.aws.amazon.com/wellarchitected/latest/reliability-pillar/back-up-data.html",
	})
}

// awsDLQ recommends dead-letter queues for SQS queues that are not
// dead-letter queues themselves.
func awsDLQ(d *design, out *[]rec) {
	queues := d.ofType("aws_sqs_queue")
	isDLQ := map[string]bool{}
	for _, q := range queues {
		for _, src := range d.referenced(q, "aws_sqs_queue") {
			if strings.Contains(expr(&q.Body, "redrive_policy"), addr(src)) {
				isDLQ[src.ID] = true
			}
		}
	}
	for _, rp := range d.ofType("aws_sqs_queue_redrive_policy") {
		for _, q := range d.referenced(rp, "aws_sqs_queue") {
			if strings.Contains(expr(&rp.Body, "redrive_policy"), addr(q)) {
				isDLQ[q.ID] = true
			}
		}
	}
	var nodes []*model.Node
	var parts []string
	for _, q := range queues {
		if isDLQ[q.ID] || set(&q.Body, "redrive_policy") || d.referredBy(q, "aws_sqs_queue_redrive_policy") {
			continue
		}
		nodes = append(nodes, q)
		dlq := d.unique("aws_sqs_queue", label(q, "dlq"))
		body := "  message_retention_seconds = 1209600\n"
		if name, ok := str(&q.Body, "name"); ok {
			dlqName := name + "-dlq"
			if base, fifo := strings.CutSuffix(name, ".fifo"); fifo {
				dlqName = base + "-dlq.fifo"
			}
			body = fmt.Sprintf("  name = %q\n", dlqName) + body
		}
		if isTrue(&q.Body, "fifo_queue") {
			body = "  fifo_queue = true\n" + body
		}
		parts = append(parts, fmt.Sprintf("resource \"aws_sqs_queue\" %q {\n%s}\n\n# Merge into the existing %s:\nresource \"aws_sqs_queue\" %q {\n  redrive_policy = jsonencode({\n    deadLetterTargetArn = aws_sqs_queue.%s.arn\n    maxReceiveCount = 5\n  })\n}",
			dlq, body, addr(q), q.Name, dlq))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioAsync, Recommendation{
		ID:      "aws_sqs_dlq",
		Title:   "Give each queue a dead-letter queue",
		Why:     "A message that always fails (a poison message) is retried until it expires, blocking FIFO groups and burning consumer capacity; then it is lost. A dead-letter queue keeps it for inspection and replay.",
		Pillars: pillarsAsync,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "aws_sqs_queue", Purpose: "dead-letter queue (14-day retention) referenced by the source queue's redrive_policy after 5 receives"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Queues whose messages are safe to drop (best-effort notifications).",
		Rules:   []string{"bp_sqs_dlq"},
		Docs:    "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html",
	})
}

// awsEncryption recommends a customer-managed KMS key for data stores and
// messaging that use AWS-owned keys or none (production).
func awsEncryption(d *design, out *[]rec) {
	type target struct {
		types []string
		ok    func(n *model.Node) bool
		patch func(key string) string
	}
	targets := []target{
		{[]string{"aws_db_instance", "aws_rds_cluster"}, func(n *model.Node) bool { return set(&n.Body, "kms_key_id") || set(&n.Body, "replicate_source_db") },
			func(key string) string { return fmt.Sprintf("  storage_encrypted = true\n  kms_key_id = %s.arn", key) }},
		{[]string{"aws_dynamodb_table"}, func(n *model.Node) bool { return set(path(&n.Body, "server_side_encryption"), "kms_key_arn") },
			func(key string) string {
				return fmt.Sprintf("  server_side_encryption {\n    enabled = true\n    kms_key_arn = %s.arn\n  }", key)
			}},
		{[]string{"aws_sns_topic", "aws_sqs_queue"}, func(n *model.Node) bool { return set(&n.Body, "kms_master_key_id") },
			func(key string) string { return fmt.Sprintf("  kms_master_key_id = %s.arn", key) }},
	}
	var nodes []*model.Node
	var patches []string
	key := ""
	for _, t := range targets {
		for _, n := range d.ofType(t.types...) {
			if t.ok(n) {
				continue
			}
			if key == "" {
				key = "aws_kms_key." + d.unique("aws_kms_key", "data")
			}
			nodes = append(nodes, n)
			patches = append(patches, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n%s\n}", addr(n), n.Type, n.Name, t.patch(key)))
		}
	}
	if len(nodes) == 0 {
		return
	}
	keyBlock := fmt.Sprintf("resource \"aws_kms_key\" %q {\n  description = \"Data at rest\"\n  enable_key_rotation = true\n  deletion_window_in_days = 30\n}", strings.TrimPrefix(key, "aws_kms_key."))
	d.add(out, prioEncryption, Recommendation{
		ID:    "aws_encryption_cmk",
		Title: "Encrypt data stores and messages with a customer-managed KMS key",
		Why: "A customer-managed key lets you control and audit who can decrypt the data (key policy, CloudTrail), rotate the key and revoke access in an incident. " +
			"Changing the key of an existing database or table replaces or re-encrypts it, so decide before production data lands. Services that publish to an encrypted topic or queue (alarms, events) need kms:GenerateDataKey and kms:Decrypt in the key policy.",
		Pillars: []model.Pillar{model.PillarSecurity},
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "aws_kms_key", Purpose: "customer-managed key with yearly rotation"},
		},
		Snippet: joinLines(append([]string{keyBlock}, patches...)...),
		Scope:   ScopeProduction,
		Skip:    "Development data: AWS-managed encryption at rest is enough there.",
		Rules:   []string{"bp_unencrypted_storage", "bp_kms_rotation", "bp_sqs_encryption"},
		Docs:    "https://docs.aws.amazon.com/kms/latest/developerguide/concepts.html#customer-cmk",
	})
}

// ---------------------------------------------------------------------------
// Compute

// awsAutoscaling recommends bounded target-tracking autoscaling for ECS
// services and Auto Scaling groups.
func awsAutoscaling(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	add := map[string]Addition{}
	for _, svc := range d.ofType("aws_ecs_service") {
		if d.referredBy(svc, "aws_appautoscaling_target") || strings.EqualFold(strFallback(&svc.Body, "scheduling_strategy"), "DAEMON") {
			continue
		}
		nodes = append(nodes, svc)
		cluster := `"default"`
		if cs := d.referenced(svc, "aws_ecs_cluster"); len(cs) > 0 {
			cluster = addr(cs[0]) + ".name"
		} else if e := expr(&svc.Body, "cluster"); e != "" {
			cluster = fmt.Sprintf("element(split(\"/\", %s), 1)", e)
		}
		tgt := d.unique("aws_appautoscaling_target", svc.Name)
		parts = append(parts, fmt.Sprintf(`resource "aws_appautoscaling_target" %q {
  service_namespace = "ecs"
  scalable_dimension = "ecs:service:DesiredCount"
  resource_id = "service/${%s}/${%s.name}"
  min_capacity = 2
  max_capacity = 10
}

resource "aws_appautoscaling_policy" %q {
  name = %q
  policy_type = "TargetTrackingScaling"
  service_namespace = aws_appautoscaling_target.%s.service_namespace
  scalable_dimension = aws_appautoscaling_target.%s.scalable_dimension
  resource_id = aws_appautoscaling_target.%s.resource_id

  target_tracking_scaling_policy_configuration {
    target_value = 60
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
  }
}

# Merge into the existing %s so applies don't reset the scaled count:
resource "aws_ecs_service" %q {
  lifecycle {
    ignore_changes = [desired_count]
  }
}`, tgt, cluster, addr(svc), d.unique("aws_appautoscaling_policy", label(svc, "cpu")), strings.ReplaceAll(svc.Name, "_", "-")+"-cpu", tgt, tgt, tgt, addr(svc), svc.Name))
		add["aws_appautoscaling_target"] = Addition{Type: "aws_appautoscaling_target", Purpose: "desired count between min_capacity 2 and max_capacity 10"}
		add["aws_appautoscaling_policy"] = Addition{Type: "aws_appautoscaling_policy", Purpose: "target tracking on average CPU (60%)"}
	}
	for _, asg := range d.ofType("aws_autoscaling_group") {
		if d.referredBy(asg, "aws_autoscaling_policy") {
			continue
		}
		nodes = append(nodes, asg)
		parts = append(parts, fmt.Sprintf(`resource "aws_autoscaling_policy" %q {
  name = %q
  autoscaling_group_name = %s.name
  policy_type = "TargetTrackingScaling"

  target_tracking_configuration {
    target_value = 60
    predefined_metric_specification {
      predefined_metric_type = "ASGAverageCPUUtilization"
    }
  }
}`, d.unique("aws_autoscaling_policy", label(asg, "cpu")), strings.ReplaceAll(asg.Name, "_", "-")+"-cpu", addr(asg)))
		add["aws_autoscaling_policy"] = Addition{Type: "aws_autoscaling_policy", Purpose: "target tracking on average CPU (60%) between the group's min_size and max_size"}
	}
	if len(nodes) == 0 {
		return
	}
	adds := []Addition{}
	for _, k := range sortedKeys(add) {
		adds = append(adds, add[k])
	}
	d.add(out, prioScaling, Recommendation{
		ID:    "aws_autoscaling",
		Title: "Scale the compute tiers automatically, within bounds",
		Why: "A fixed size is too small for a traffic peak (latency, errors) and paid for in full while idle. Target tracking adds capacity before saturation; " +
			"min 2 keeps a spare across failures and the max bound caps cost and runaway scaling.",
		Pillars: pillarsScaling,
		Nodes:   addrs(nodes),
		Add:     adds,
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Fixed-size batch workers and development environments (keep one task there).",
		Rules:   []string{"bp_ecs_autoscaling", "bp_asg_single_instance"},
		Docs:    "https://docs.aws.amazon.com/autoscaling/application/userguide/application-auto-scaling-target-tracking.html",
	})
}

// strFallback returns a constant string attribute or "".
func strFallback(b *model.Body, name string) string {
	s, _ := str(b, name)
	return s
}

// awsAlarm is the metric of an entry point or function to alarm on.
type awsAlarm struct {
	namespace, errors, latency string
	latencyThreshold           string
	dims                       string
}

// awsAlarms recommends alarms on 5xx errors and p95 latency for API stages,
// load balancers and functions (production).
func awsAlarms(d *design, out *[]rec) {
	var nodes []*model.Node
	var alarms []string
	topic := ""
	for _, n := range d.ofType("aws_apigatewayv2_stage", "aws_api_gateway_stage", "aws_lb", "aws_alb", "aws_lambda_function") {
		if d.referredBy(n, "aws_cloudwatch_metric_alarm") {
			continue
		}
		var m awsAlarm
		switch n.Type {
		case "aws_apigatewayv2_stage":
			m = awsAlarm{"AWS/ApiGateway", "5xx", "Latency", "1000", fmt.Sprintf("{\n    ApiId = %s\n    Stage = %s.name\n  }", cmp.Or(expr(&n.Body, "api_id"), `"<api-id>"`), addr(n))}
		case "aws_api_gateway_stage":
			apis := d.referenced(n, "aws_api_gateway_rest_api")
			if len(apis) == 0 {
				continue
			}
			m = awsAlarm{"AWS/ApiGateway", "5XXError", "Latency", "1000", fmt.Sprintf("{\n    ApiName = %s.name\n    Stage = %s.stage_name\n  }", addr(apis[0]), addr(n))}
		case "aws_lb", "aws_alb":
			if typ, _ := str(&n.Body, "load_balancer_type"); typ != "" && typ != "application" {
				continue
			}
			m = awsAlarm{"AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", "TargetResponseTime", "1", fmt.Sprintf("{\n    LoadBalancer = %s.arn_suffix\n  }", addr(n))}
		case "aws_lambda_function":
			m = awsAlarm{"AWS/Lambda", "Errors", "Duration", "3000", fmt.Sprintf("{\n    FunctionName = %s.function_name\n  }", addr(n))}
		}
		nodes = append(nodes, n)
		if len(nodes) > 3 {
			continue // the snippet shows the first three; the rest follow the same pattern
		}
		if topic == "" {
			topic = d.unique("aws_sns_topic", "alerts")
		}
		base := strings.ReplaceAll(label(n, ""), "_", "-")
		alarms = append(alarms, fmt.Sprintf(`resource "aws_cloudwatch_metric_alarm" %q {
  alarm_name = %q
  namespace = %q
  metric_name = %q
  dimensions = %s
  statistic = "Sum"
  period = 60
  evaluation_periods = 5
  threshold = 5
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data = "notBreaching"
  alarm_actions = [aws_sns_topic.%s.arn]
}

resource "aws_cloudwatch_metric_alarm" %q {
  alarm_name = %q
  namespace = %q
  metric_name = %q
  dimensions = %s
  extended_statistic = "p95"
  period = 60
  evaluation_periods = 5
  threshold = %s
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data = "notBreaching"
  alarm_actions = [aws_sns_topic.%s.arn]
}`, d.unique("aws_cloudwatch_metric_alarm", label(n, "errors")), base+"-errors", m.namespace, m.errors, m.dims, topic,
			d.unique("aws_cloudwatch_metric_alarm", label(n, "p95")), base+"-p95-latency", m.namespace, m.latency, m.dims, m.latencyThreshold, topic))
	}
	if len(nodes) == 0 {
		return
	}
	snippet := []string{fmt.Sprintf("resource \"aws_sns_topic\" %q {\n  name = %q\n}", topic, strings.ReplaceAll(topic, "_", "-"))}
	if len(nodes) > 3 {
		alarms = append(alarms, fmt.Sprintf("# Same pattern for: %s", strings.Join(addrs(nodes[3:]), ", ")))
	}
	d.add(out, prioAlarms, Recommendation{
		ID:      "aws_alarms",
		Title:   "Alarm on 5xx errors and p95 latency of the entry points and functions",
		Why:     "Users notice errors and slow responses first; alarms on server errors and the 95th percentile latency tell you before they do (averages hide the slow tail).",
		Pillars: pillarsAlarms,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "aws_cloudwatch_metric_alarm", Purpose: "server errors over 5 per minute for 5 minutes, and p95 latency above the target"},
			{Type: "aws_sns_topic", Purpose: "where alarms notify (subscribe e-mail, chat or paging)"},
		},
		Snippet: joinLines(append(snippet, alarms...)...),
		Scope:   ScopeProduction,
		Skip:    "Development environments nobody is on call for.",
		Rules:   []string{"bp_cloudwatch_alarms"},
		Docs:    "https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/AlarmThatSendsEmail.html",
	})
}

// awsLogRetention recommends log groups with a retention for Lambda
// functions and a retention for log groups that keep logs forever.
func awsLogRetention(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	groups := d.ofType("aws_cloudwatch_log_group")
	for _, fn := range d.ofType("aws_lambda_function") {
		has := len(d.referenced(fn, "aws_cloudwatch_log_group")) > 0 || d.referredBy(fn, "aws_cloudwatch_log_group")
		if name, ok := str(&fn.Body, "function_name"); ok && !has {
			for _, g := range groups {
				if n, _ := str(&g.Body, "name"); n == "/aws/lambda/"+name {
					has = true
				}
			}
		}
		if has {
			continue
		}
		nodes = append(nodes, fn)
		parts = append(parts, fmt.Sprintf("resource \"aws_cloudwatch_log_group\" %q {\n  name = \"/aws/lambda/${%s.function_name}\"\n  retention_in_days = 30\n}", d.unique("aws_cloudwatch_log_group", label(fn, "logs")), addr(fn)))
	}
	for _, g := range groups {
		if days, ok := number(&g.Body, "retention_in_days"); ok && days > 0 || !ok && set(&g.Body, "retention_in_days") {
			continue
		}
		nodes = append(nodes, g)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  retention_in_days = 30\n}", addr(g), g.Type, g.Name))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioLogs, Recommendation{
		ID:      "aws_log_retention",
		Title:   "Give every log group a retention period",
		Why:     "CloudWatch keeps logs forever by default: storage cost grows without bound and old logs, possibly with personal data, are never purged. A Lambda function without a declared log group gets one with no retention.",
		Pillars: pillarsLogs,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "aws_cloudwatch_log_group", Purpose: "log group per function with retention_in_days (30 here; follow your retention policy)"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Rules:   []string{"bp_log_retention"},
		Docs:    "https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/Working-with-log-groups-and-streams.html#SettingLogRetention",
	})
}

// awsTracing recommends X-Ray active tracing for Lambda functions and REST
// API stages (production).
func awsTracing(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	var adds []Addition
	for _, fn := range d.ofType("aws_lambda_function") {
		if m, _ := str(path(&fn.Body, "tracing_config"), "mode"); m == "Active" {
			continue
		}
		if len(nodes) == 0 {
			adds = append(adds,
				Addition{Type: "aws_lambda_function", Purpose: "tracing_config { mode = \"Active\" }"},
				Addition{Type: "aws_iam_role_policy_attachment", Purpose: "AWSXRayDaemonWriteAccess on the function role"})
		}
		nodes = append(nodes, fn)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  tracing_config {\n    mode = \"Active\"\n  }\n}", addr(fn), fn.Type, fn.Name))
		if roles := d.referenced(fn, "aws_iam_role"); len(roles) > 0 {
			parts = append(parts, fmt.Sprintf("resource \"aws_iam_role_policy_attachment\" %q {\n  role = %s.name\n  policy_arn = \"arn:aws:iam::aws:policy/AWSXRayDaemonWriteAccess\"\n}", d.unique("aws_iam_role_policy_attachment", label(fn, "xray")), addr(roles[0])))
		}
	}
	for _, st := range d.ofType("aws_api_gateway_stage") {
		if isTrue(&st.Body, "xray_tracing_enabled") {
			continue
		}
		if !slices.ContainsFunc(adds, func(a Addition) bool { return a.Type == st.Type }) {
			adds = append(adds, Addition{Type: "aws_api_gateway_stage", Purpose: "xray_tracing_enabled = true"})
		}
		nodes = append(nodes, st)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  xray_tracing_enabled = true\n}", addr(st), st.Type, st.Name))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioTracing, Recommendation{
		ID:    "aws_tracing",
		Title: "Trace requests end to end with AWS X-Ray",
		Why: "When a request is slow, traces show which hop (API, function, database call) took the time; metrics alone can't. " +
			"Functions need xray:PutTraceSegments (the AWSXRayDaemonWriteAccess managed policy). HTTP APIs don't support X-Ray; trace from the functions behind them.",
		Pillars: pillarsTracing,
		Nodes:   addrs(nodes),
		Add:     adds,
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Local:   LocalExportOnly + " (AWS X-Ray: the settings deploy, traces are not collected)",
		Skip:    "Low-traffic internal tools: logs and metrics are enough.",
		Docs:    "https://docs.aws.amazon.com/lambda/latest/dg/services-xray.html",
	})
}

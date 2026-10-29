package lint

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func httpStage(extra string) string {
	return `resource "aws_apigatewayv2_api" "http" {
  name          = "api"
  protocol_type = "HTTP"
}
resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.http.id
  name        = "$default"
  auto_deploy = true
` + extra + `
}
`
}

const restStage = `resource "aws_api_gateway_rest_api" "api" { name = "api" }
resource "aws_api_gateway_stage" "prod" {
  rest_api_id   = aws_api_gateway_rest_api.api.id
  deployment_id = "d"
  stage_name    = "prod"
}
`

func httpRoute(extra string) string {
	return `resource "aws_apigatewayv2_route" "r" {
  api_id = "a"
` + extra + `
}
`
}

func cloudFront(extra string) string {
	return `resource "aws_cloudfront_distribution" "cdn" {
  enabled = true
  default_cache_behavior {
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = "app"
    viewer_protocol_policy = "redirect-to-https"
  }
  restrictions {
    geo_restriction { restriction_type = "none" }
  }
  viewer_certificate { cloudfront_default_certificate = true }
` + extra + `
}
`
}

func cloudFrontOrigin(domain, policy string) string {
	return cloudFront(`  origin {
    origin_id   = "app"
    domain_name = ` + domain + `
    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "` + policy + `"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }`)
}

func alb(extra string) string {
	return `resource "aws_lb" "app" {
  name    = "app"
  subnets = ["s1", "s2"]
` + extra + `
}
`
}

func albListener(lbExtra, listener string) string {
	return alb(lbExtra) + `resource "aws_lb_listener" "l" {
  load_balancer_arn = aws_lb.app.arn
` + listener + `
}
`
}

const forwardAction = `  default_action {
    type             = "forward"
    target_group_arn = "tg"
  }`

func webACL(rules string) string {
	return `resource "aws_wafv2_web_acl" "acl" {
  name  = "acl"
  scope = "REGIONAL"
  default_action {
    allow {}
  }
  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "acl"
    sampled_requests_enabled   = true
  }
` + rules + `
}
`
}

func wafRule(statement string) string {
	return `  rule {
    name     = "r"
    priority = 1
    action {
      block {}
    }
    statement {
` + statement + `
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "r"
      sampled_requests_enabled   = true
    }
  }`
}

const secret = `resource "aws_secretsmanager_secret" "db" { name = "db" }
`

func apim(policy string) string {
	return `resource "azurerm_api_management" "main" {
  name                = "apim"
  location            = "westeurope"
  resource_group_name = "rg"
  publisher_name      = "x"
  publisher_email     = "x@example.com"
  sku_name            = "Consumption_0"
}
` + policy
}

func apimAPIPolicy(xml string) string {
	return `resource "azurerm_api_management_api_policy" "p" {
  api_name            = "shop"
  api_management_name = azurerm_api_management.main.name
  resource_group_name = "rg"
  xml_content         = ` + xml + `
}
`
}

func aks(extra string) string {
	return `resource "azurerm_kubernetes_cluster" "k" {
  name                = "k"
  location            = "westeurope"
  resource_group_name = "rg"
  dns_prefix          = "k"
  default_node_pool {
    name       = "default"
    node_count = 1
    vm_size    = "Standard_D2s_v5"
  }
  identity { type = "SystemAssigned" }
` + extra + `
}
`
}

func keyVault(extra string) string {
	return `resource "azurerm_key_vault" "kv" {
  name                = "kv"
  location            = "westeurope"
  resource_group_name = "rg"
  tenant_id           = "t"
  sku_name            = "standard"
` + extra + `
}
`
}

func law(extra string) string {
	return `resource "azurerm_log_analytics_workspace" "law" {
  name                = "law"
  location            = "westeurope"
  resource_group_name = "rg"
` + extra + `
}
`
}

func sslPolicy(extra string) string {
	return `resource "google_compute_ssl_policy" "p" {
  name = "p"
` + extra + `
}
`
}

func backendService(extra string) string {
	return `resource "google_compute_backend_service" "b" {
  name = "b"
` + extra + `
}
`
}

func armor(rules string) string {
	return `resource "google_compute_security_policy" "p" {
  name = "p"
  rule {
    action   = "allow"
    priority = 2147483647
    match {
      versioned_expr = "SRC_IPS_V1"
      config { src_ip_ranges = ["*"] }
    }
  }
` + rules + `
}
`
}

const armorThrottle = `  rule {
    action   = "throttle"
    priority = 1000
    match {
      versioned_expr = "SRC_IPS_V1"
      config { src_ip_ranges = ["*"] }
    }
    rate_limit_options {
      conform_action = "allow"
      exceed_action  = "deny(429)"
      enforce_on_key = "IP"
      rate_limit_threshold {
        count        = 500
        interval_sec = 60
      }
    }
  }`

const armorDenyRule = `  rule {
    action   = "deny(403)"
    priority = 1000
    match {
      expr { expression = "evaluatePreconfiguredWaf('sqli-v33-stable')" }
    }
  }`

func runService(extra string) string {
	return `resource "google_cloud_run_v2_service" "s" {
  name     = "s"
  location = "us-central1"
  template {
    containers { image = "us-docker.pkg.dev/cloudrun/container/hello" }
  }
` + extra + `
}
resource "google_cloud_run_v2_service_iam_member" "inv" {
  name     = google_cloud_run_v2_service.s.name
  location = "us-central1"
  role     = "roles/run.invoker"
  member   = "allUsers"
}
`
}

func gke(extra string) string {
	return `resource "google_container_cluster" "k" {
  name               = "k"
  location           = "us-central1"
  initial_node_count = 1
` + extra + `
}
`
}

func ociAPIDeployment(policies string) string {
	return `resource "oci_apigateway_deployment" "d" {
  compartment_id = "ocid"
  gateway_id     = "ocid"
  path_prefix    = "/v1"
  specification {
` + policies + `
    routes {
      path    = "/items"
      methods = ["GET"]
      backend {
        type = "HTTP_BACKEND"
        url  = "https://example.com"
      }
    }
  }
}
`
}

func ociLB(extra string) string {
	return `resource "oci_load_balancer_load_balancer" "lb" {
  compartment_id = "ocid"
  display_name   = "lb"
  shape          = "flexible"
  subnet_ids     = ["ocid"]
` + extra + `
}
`
}

func ociListener(lbExtra, extra string) string {
	return ociLB(lbExtra) + `resource "oci_load_balancer_listener" "http" {
  name                     = "http"
  load_balancer_id         = oci_load_balancer_load_balancer.lb.id
  default_backend_set_name = "app"
  port                     = 80
` + extra + `
}
`
}

func ociWAFPolicy(extra string) string {
	return `resource "oci_waf_web_app_firewall_policy" "p" {
  compartment_id = "ocid"
` + extra + `
}
`
}

func ociSecret(extra string) string {
	return `resource "oci_vault_secret" "s" {
  compartment_id = "ocid"
  vault_id       = "ocid"
  key_id         = "ocid"
  secret_name    = "db"
` + extra + `
}
`
}

// edgeCases are the cases of the edge, cluster and secrets rules
// (rules_edge_*.go); init adds them to structureCases so TestRules runs them.
var edgeCases = map[string][]structureCase{
	"bp_apigw_throttling": {
		{name: "HTTP stage without limits", src: httpStage(""), want: 1},
		{name: "HTTP stage with default route limits", src: httpStage(`  default_route_settings {
    throttling_rate_limit  = 100
    throttling_burst_limit = 200
  }`), want: 0},
		{name: "limits from variables", src: httpStage(`  default_route_settings {
    throttling_rate_limit = var.rate
  }`), want: 0},
		{name: "settings without throttling", src: httpStage(`  default_route_settings { detailed_metrics_enabled = true }`), want: 1},
		{name: "dynamic route settings", src: httpStage(`  dynamic "default_route_settings" {
    for_each = var.limits
    content { throttling_rate_limit = default_route_settings.value }
  }`), want: 0},
		{name: "REST stage without method settings", src: restStage, want: 1},
		{name: "REST stage with method settings", src: restStage + `resource "aws_api_gateway_method_settings" "all" {
  rest_api_id = aws_api_gateway_rest_api.api.id
  stage_name  = aws_api_gateway_stage.prod.stage_name
  method_path = "*/*"
  settings {
    throttling_rate_limit  = 100
    throttling_burst_limit = 200
  }
}`, want: 0},
		{name: "REST method settings for an unknown stage", src: restStage + `resource "aws_api_gateway_method_settings" "all" {
  rest_api_id = "a"
  stage_name  = var.stage
  method_path = "*/*"
  settings { metrics_enabled = true }
}`, want: 0},
		{name: "REST stage throttled by a usage plan", src: restStage + `resource "aws_api_gateway_usage_plan" "u" {
  name = "u"
  api_stages {
    api_id = aws_api_gateway_rest_api.api.id
    stage  = aws_api_gateway_stage.prod.stage_name
  }
  throttle_settings {
    rate_limit  = 100
    burst_limit = 200
  }
}`, want: 0},
	},
	"bp_apigw_access_logs": {
		{name: "no access logs", src: httpStage(""), want: 1},
		{name: "access logs", src: httpStage(`  access_log_settings {
    destination_arn = "arn:aws:logs:us-east-1:111122223333:log-group:api"
    format          = "$context.requestId"
  }`), want: 0},
		{name: "REST stage without access logs", src: restStage, want: 1},
	},
	"bp_apigw_route_auth": {
		{name: "route without authorization", src: httpRoute(`  route_key = "GET /items"`), want: 1},
		{name: "explicit NONE", src: httpRoute(`  route_key          = "GET /items"
  authorization_type = "NONE"`), want: 1},
		{name: "JWT", src: httpRoute(`  route_key          = "GET /items"
  authorization_type = "JWT"
  authorizer_id      = "a"`), want: 0},
		{name: "authorization from a variable", src: httpRoute(`  route_key          = "GET /items"
  authorization_type = var.auth`), want: 0},
		{name: "health check", src: httpRoute(`  route_key = "GET /health"`), want: 0},
		{name: "CORS preflight", src: httpRoute(`  route_key = "OPTIONS /items"`), want: 0},
		{name: "API key", src: httpRoute(`  route_key        = "GET /items"
  api_key_required = true`), want: 0},
		{name: "REST method NONE", src: `resource "aws_api_gateway_method" "m" {
  rest_api_id   = "a"
  resource_id   = "r"
  http_method   = "GET"
  authorization = "NONE"
}`, want: 1},
		{name: "REST method with Cognito", src: `resource "aws_api_gateway_method" "m" {
  rest_api_id   = "a"
  resource_id   = "r"
  http_method   = "GET"
  authorization = "COGNITO_USER_POOLS"
  authorizer_id = "a"
}`, want: 0},
		{name: "REST public path", src: `resource "aws_api_gateway_resource" "health" {
  rest_api_id = "a"
  parent_id   = "p"
  path_part   = "health"
}
resource "aws_api_gateway_method" "m" {
  rest_api_id   = "a"
  resource_id   = aws_api_gateway_resource.health.id
  http_method   = "GET"
  authorization = "NONE"
}`, want: 0},
	},
	"bp_cloudfront_https": {
		{name: "redirect to HTTPS", src: cloudFront(""), want: 0},
		{name: "allow-all viewers", src: strings.Replace(cloudFront(""), `"redirect-to-https"`, `"allow-all"`, 1), want: 1},
		{name: "viewer policy from a variable", src: strings.Replace(cloudFront(""), `"redirect-to-https"`, `var.viewer_policy`, 1), want: 0},
		{name: "http-only origin", src: cloudFrontOrigin(`aws_lb.app.dns_name`, "http-only"), want: 1},
		{name: "https-only origin", src: cloudFrontOrigin(`aws_lb.app.dns_name`, "https-only"), want: 0},
		{name: "S3 website origin", src: cloudFrontOrigin(`aws_s3_bucket_website_configuration.site.website_endpoint`, "http-only"), want: 0},
		{name: "SSLv3 to the origin", src: strings.Replace(cloudFrontOrigin(`aws_lb.app.dns_name`, "https-only"), `["TLSv1.2"]`, `["SSLv3", "TLSv1.2"]`, 1), want: 1},
	},
	"bp_cloudfront_waf": {
		{name: "no web ACL", src: cloudFront(""), want: 1},
		{name: "web ACL", src: cloudFront(`  web_acl_id = aws_wafv2_web_acl.edge.arn`), want: 0},
		{name: "empty web ACL", src: cloudFront(`  web_acl_id = ""`), want: 1},
	},
	"bp_alb_http_forward": {
		{name: "HTTP forward on a public ALB", src: albListener("", `  port     = 80
  protocol = "HTTP"
`+forwardAction), want: 1},
		{name: "HTTP redirect", src: albListener("", `  port     = 80
  protocol = "HTTP"
  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }`), want: 0},
		{name: "internal ALB", src: albListener(`  internal = true`, `  port     = 80
  protocol = "HTTP"
`+forwardAction), want: 0},
		{name: "internal from a variable", src: albListener(`  internal = var.internal`, `  port     = 80
  protocol = "HTTP"
`+forwardAction), want: 0},
		{name: "action type from an expression", src: albListener("", `  port     = 80
  protocol = "HTTP"
  default_action {
    type             = var.https ? "redirect" : "forward"
    target_group_arn = "tg"
  }`), want: 0},
		{name: "HTTPS forward", src: albListener("", `  port            = 443
  protocol        = "HTTPS"
  ssl_policy      = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn = "arn"
`+forwardAction), want: 0},
	},
	"bp_lb_tls_policy": {
		{name: "no ssl policy", src: albListener("", `  port            = 443
  protocol        = "HTTPS"
  certificate_arn = "arn"
`+forwardAction), want: 1},
		{name: "legacy policy", src: albListener("", `  port            = 443
  protocol        = "HTTPS"
  ssl_policy      = "ELBSecurityPolicy-2016-08"
  certificate_arn = "arn"
`+forwardAction), want: 1},
		{name: "TLS 1.1 policy", src: albListener("", `  port            = 443
  protocol        = "HTTPS"
  ssl_policy      = "ELBSecurityPolicy-TLS13-1-1-2021-06"
  certificate_arn = "arn"
`+forwardAction), want: 1},
		{name: "TLS 1.2 policy", src: albListener("", `  port            = 443
  protocol        = "HTTPS"
  ssl_policy      = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn = "arn"
`+forwardAction), want: 0},
		{name: "FS TLS 1.2 policy", src: albListener("", `  port            = 443
  protocol        = "HTTPS"
  ssl_policy      = "ELBSecurityPolicy-FS-1-2-Res-2020-10"
  certificate_arn = "arn"
`+forwardAction), want: 0},
		{name: "policy from a variable", src: albListener("", `  port            = 443
  protocol        = "HTTPS"
  ssl_policy      = var.ssl_policy
  certificate_arn = "arn"
`+forwardAction), want: 0},
		{name: "NLB TLS listener without policy", src: albListener(`  load_balancer_type = "network"`, `  port            = 443
  protocol        = "TLS"
  certificate_arn = "arn"
`+forwardAction), want: 1},
		{name: "plain HTTP listener", src: albListener("", `  port     = 80
  protocol = "HTTP"
`+forwardAction), want: 0},
	},
	"bp_alb_drop_invalid_headers": {
		{name: "unset", src: alb(""), want: 1},
		{name: "off", src: alb(`  drop_invalid_header_fields = false`), want: 1},
		{name: "on", src: alb(`  drop_invalid_header_fields = true`), want: 0},
		{name: "expression", src: alb(`  drop_invalid_header_fields = var.drop`), want: 0},
		{name: "network load balancer", src: alb(`  load_balancer_type = "network"`), want: 0},
	},
	"bp_alb_waf": {
		{name: "public ALB without WAF", src: alb(""), want: 1},
		{name: "associated web ACL", src: alb("") + `resource "aws_wafv2_web_acl_association" "a" {
  resource_arn = aws_lb.app.arn
  web_acl_arn  = "arn"
}`, want: 0},
		{name: "association to an unknown resource", src: alb("") + `resource "aws_wafv2_web_acl_association" "a" {
  resource_arn = var.lb_arn
  web_acl_arn  = "arn"
}`, want: 0},
		{name: "internal ALB", src: alb(`  internal = true`), want: 0},
		{name: "network load balancer", src: alb(`  load_balancer_type = "network"`), want: 0},
	},
	"bp_waf_rate_limit": {
		{name: "managed rules only", src: webACL(wafRule(`      managed_rule_group_statement {
        name        = "AWSManagedRulesCommonRuleSet"
        vendor_name = "AWS"
      }`)), want: 1},
		{name: "rate-based rule", src: webACL(wafRule(`      rate_based_statement {
        limit              = 1000
        aggregate_key_type = "IP"
      }`)), want: 0},
		{name: "custom rule group", src: webACL(wafRule(`      rule_group_reference_statement { arn = var.group }`)), want: 0},
		{name: "dynamic rules", src: webACL(`  dynamic "rule" {
    for_each = var.rules
    content {
      name     = rule.key
      priority = rule.value
    }
  }`), want: 0},
		{name: "rule_json with a rate-based statement", src: webACL(`  rule_json = jsonencode([{ Name = "rate", Statement = { RateBasedStatement = { Limit = 1000 } } }])`), want: 0},
		{name: "rule_json from a file", src: webACL(`  rule_json = file("rules.json")`), want: 0},
		{name: "no rules", src: webACL(""), want: 1},
	},
	"bp_secret_rotation": {
		{name: "never rotates", src: secret, want: 1},
		{name: "rotation", src: secret + `resource "aws_secretsmanager_secret_rotation" "db" {
  secret_id           = aws_secretsmanager_secret.db.id
  rotation_lambda_arn = "arn"
  rotation_rules { automatically_after_days = 30 }
}`, want: 0},
		{name: "rotation of an unknown secret", src: secret + `resource "aws_secretsmanager_secret_rotation" "x" {
  secret_id = var.secret
  rotation_rules { automatically_after_days = 30 }
}`, want: 0},
	},
	"bp_sqs_encryption": {
		{name: "default SSE", src: `resource "aws_sqs_queue" "q" { name = "q" }`, want: 0},
		{name: "SSE off", src: `resource "aws_sqs_queue" "q" {
  name                    = "q"
  sqs_managed_sse_enabled = false
}`, want: 1},
		{name: "SSE off with KMS", src: `resource "aws_sqs_queue" "q" {
  name                    = "q"
  sqs_managed_sse_enabled = false
  kms_master_key_id       = "alias/aws/sqs"
}`, want: 0},
		{name: "SSE from a variable", src: `resource "aws_sqs_queue" "q" {
  name                    = "q"
  sqs_managed_sse_enabled = var.sse
}`, want: 0},
	},

	// Azure
	"bp_azure_apim_rate_limit": {
		{name: "no policy", src: apim(""), want: 1},
		{name: "rate-limit policy", src: apim(apimAPIPolicy(`"<policies><inbound><base /><rate-limit calls=\"100\" renewal-period=\"60\" /></inbound></policies>"`)), want: 0},
		{name: "rate-limit-by-key heredoc", src: apim(apimAPIPolicy(`<<-XML
    <policies>
      <inbound>
        <rate-limit-by-key calls="100" renewal-period="60" counter-key="@(context.Request.IpAddress)" />
      </inbound>
    </policies>
  XML`)), want: 0},
		{name: "quota", src: apim(apimAPIPolicy(`"<policies><inbound><quota calls=\"10000\" renewal-period=\"86400\" /></inbound></policies>"`)), want: 0},
		{name: "policy without limits", src: apim(apimAPIPolicy(`"<policies><inbound><base /></inbound></policies>"`)), want: 1},
		{name: "policy from a file", src: apim(apimAPIPolicy(`file("policy.xml")`)), want: 0},
		{name: "policy for an unknown service", src: apim(`resource "azurerm_api_management_api_policy" "p" {
  api_name            = "shop"
  api_management_name = var.apim
  resource_group_name = "rg"
  xml_content         = "<policies />"
}`), want: 0},
	},
	"bp_azure_messaging_tls": {
		{name: "service bus TLS 1.0", src: `resource "azurerm_servicebus_namespace" "sb" {
  name                = "sb"
  location            = "westeurope"
  resource_group_name = "rg"
  sku                 = "Standard"
  minimum_tls_version = "1.0"
}`, want: 1},
		{name: "event hubs TLS 1.1", src: `resource "azurerm_eventhub_namespace" "eh" {
  name                = "eh"
  location            = "westeurope"
  resource_group_name = "rg"
  sku                 = "Standard"
  minimum_tls_version = "1.1"
}`, want: 1},
		{name: "default", src: `resource "azurerm_servicebus_namespace" "sb" {
  name                = "sb"
  location            = "westeurope"
  resource_group_name = "rg"
  sku                 = "Standard"
}`, want: 0},
		{name: "TLS 1.2", src: `resource "azurerm_eventhub_namespace" "eh" {
  name                = "eh"
  location            = "westeurope"
  resource_group_name = "rg"
  sku                 = "Standard"
  minimum_tls_version = "1.2"
}`, want: 0},
		{name: "variable", src: `resource "azurerm_servicebus_namespace" "sb" {
  name                = "sb"
  location            = "westeurope"
  resource_group_name = "rg"
  sku                 = "Standard"
  minimum_tls_version = var.tls
}`, want: 0},
	},
	"bp_azure_aks_rbac": {
		{name: "default", src: aks(""), want: 0},
		{name: "RBAC off", src: aks(`  role_based_access_control_enabled = false`), want: 1},
		{name: "variable", src: aks(`  role_based_access_control_enabled = var.rbac`), want: 0},
	},
	"bp_azure_aks_api_ranges": {
		{name: "public API server", src: aks(""), want: 1},
		{name: "private cluster", src: aks(`  private_cluster_enabled = true`), want: 0},
		{name: "authorized ranges", src: aks(`  api_server_access_profile {
    authorized_ip_ranges = ["203.0.113.0/24"]
  }`), want: 0},
		{name: "empty ranges", src: aks(`  api_server_access_profile {
    authorized_ip_ranges = []
  }`), want: 1},
		{name: "ranges from a variable", src: aks(`  api_server_access_profile {
    authorized_ip_ranges = var.admin_cidrs
  }`), want: 0},
	},
	"bp_azure_aks_azure_policy": {
		{name: "unset", src: aks(""), want: 1},
		{name: "on", src: aks(`  azure_policy_enabled = true`), want: 0},
		{name: "variable", src: aks(`  azure_policy_enabled = var.policy`), want: 0},
	},
	"bp_azure_kv_default_deny": {
		{name: "no network ACLs", src: keyVault(""), want: 1},
		{name: "allow by default", src: keyVault(`  network_acls {
    default_action = "Allow"
    bypass         = "AzureServices"
  }`), want: 1},
		{name: "deny by default", src: keyVault(`  network_acls {
    default_action = "Deny"
    bypass         = "AzureServices"
  }`), want: 0},
		{name: "public access off", src: keyVault(`  public_network_access_enabled = false`), want: 0},
		{name: "action from a variable", src: keyVault(`  network_acls {
    default_action = var.kv_action
    bypass         = "AzureServices"
  }`), want: 0},
	},
	"bp_azure_law_retention": {
		{name: "default 30 days", src: law(""), want: 1},
		{name: "30 days", src: law(`  retention_in_days = 30`), want: 1},
		{name: "90 days", src: law(`  retention_in_days = 90`), want: 0},
		{name: "variable", src: law(`  retention_in_days = var.retention`), want: 0},
	},

	// Google Cloud
	"bp_gcp_ssl_policy_weak": {
		{name: "defaults", src: sslPolicy(""), want: 1},
		{name: "modern TLS 1.0", src: sslPolicy(`  profile = "MODERN"`), want: 1},
		{name: "modern TLS 1.2", src: sslPolicy(`  profile         = "MODERN"
  min_tls_version = "TLS_1_2"`), want: 0},
		{name: "restricted", src: sslPolicy(`  profile = "RESTRICTED"`), want: 0},
		{name: "compatible TLS 1.2", src: sslPolicy(`  profile         = "COMPATIBLE"
  min_tls_version = "TLS_1_2"`), want: 1},
		{name: "profile from a variable", src: sslPolicy(`  profile = var.profile`), want: 0},
	},
	"bp_gcp_https_proxy_ssl_policy": {
		{name: "no SSL policy", src: `resource "google_compute_target_https_proxy" "p" {
  name             = "p"
  url_map          = "u"
  ssl_certificates = ["c"]
}`, want: 1},
		{name: "SSL policy", src: `resource "google_compute_target_https_proxy" "p" {
  name             = "p"
  url_map          = "u"
  ssl_certificates = ["c"]
  ssl_policy       = google_compute_ssl_policy.modern.id
}`, want: 0},
	},
	"bp_gcp_cloud_armor": {
		{name: "external without policy", src: backendService(""), want: 1},
		{name: "external managed with policy", src: backendService(`  load_balancing_scheme = "EXTERNAL_MANAGED"
  security_policy       = google_compute_security_policy.p.id`), want: 0},
		{name: "internal", src: backendService(`  load_balancing_scheme = "INTERNAL_MANAGED"`), want: 0},
		{name: "scheme from a variable", src: backendService(`  load_balancing_scheme = var.scheme`), want: 0},
	},
	"bp_gcp_armor_rules": {
		{name: "default rule only", src: armor(""), want: 1},
		{name: "filters without rate limit", src: armor(armorDenyRule), want: 1},
		{name: "filters and throttle", src: armor(armorDenyRule + "\n" + armorThrottle), want: 0},
		{name: "standalone throttle rule", src: armor(armorDenyRule) + `resource "google_compute_security_policy_rule" "rate" {
  security_policy = google_compute_security_policy.p.name
  action          = "rate_based_ban"
  priority        = 900
}`, want: 0},
		{name: "rule for an unknown policy", src: armor("") + `resource "google_compute_security_policy_rule" "rate" {
  security_policy = var.policy
  action          = "throttle"
  priority        = 900
}`, want: 0},
		{name: "edge policy", src: `resource "google_compute_security_policy" "edge" {
  name = "edge"
  type = "CLOUD_ARMOR_EDGE"
}`, want: 0},
	},
	"bp_gcp_cloud_run_public": {
		{name: "public invoker", src: runService(""), want: 1},
		{name: "behind a load balancer", src: runService(`  ingress = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"`), want: 0},
		{name: "labeled public", src: runService(`  labels = { public = "true" }`), want: 0},
		{name: "ingress all", src: runService(`  ingress = "INGRESS_TRAFFIC_ALL"`), want: 1},
		{name: "ingress from a variable", src: runService(`  ingress = var.ingress`), want: 0},
		{name: "service account invoker", src: `resource "google_cloud_run_v2_service_iam_member" "inv" {
  name     = "s"
  location = "us-central1"
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.web.email}"
}`, want: 0},
		{name: "binding with allAuthenticatedUsers", src: `resource "google_cloud_run_v2_service_iam_binding" "inv" {
  name     = "s"
  location = "us-central1"
  role     = "roles/run.invoker"
  members  = ["allAuthenticatedUsers"]
}`, want: 1},
	},
	"bp_gcp_gke_legacy_abac": {
		{name: "default", src: gke(""), want: 0},
		{name: "legacy ABAC", src: gke(`  enable_legacy_abac = true`), want: 1},
		{name: "variable", src: gke(`  enable_legacy_abac = var.abac`), want: 0},
	},
	"bp_gcp_gke_release_channel": {
		{name: "default", src: gke(""), want: 0},
		{name: "unspecified", src: gke(`  release_channel { channel = "UNSPECIFIED" }`), want: 1},
		{name: "regular", src: gke(`  release_channel { channel = "REGULAR" }`), want: 0},
	},
	"bp_gcp_gke_private_nodes": {
		{name: "public nodes", src: gke(""), want: 1},
		{name: "private nodes", src: gke(`  private_cluster_config {
    enable_private_nodes   = true
    master_ipv4_cidr_block = "172.16.0.0/28"
  }`), want: 0},
		{name: "explicitly public", src: gke(`  private_cluster_config { enable_private_nodes = false }`), want: 1},
		{name: "variable", src: gke(`  private_cluster_config { enable_private_nodes = var.private }`), want: 0},
	},
	"bp_gcp_gke_control_plane_networks": {
		{name: "open control plane", src: gke(""), want: 1},
		{name: "authorized networks", src: gke(`  master_authorized_networks_config {
    cidr_blocks {
      cidr_block   = "203.0.113.0/24"
      display_name = "office"
    }
  }`), want: 0},
		{name: "private endpoint", src: gke(`  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = true
  }`), want: 0},
	},
	"bp_gcp_gke_workload_identity": {
		{name: "no workload identity", src: gke(""), want: 1},
		{name: "workload identity", src: gke(`  workload_identity_config { workload_pool = "${var.project}.svc.id.goog" }`), want: 0},
		{name: "autopilot", src: gke(`  enable_autopilot = true`), want: 0},
	},

	// OCI
	"bp_oci_apigw_rate_limit": {
		{name: "no request policies", src: ociAPIDeployment(""), want: 1},
		{name: "rate limiting", src: ociAPIDeployment(`    request_policies {
      rate_limiting {
        rate_in_requests_per_second = 10
        rate_key                    = "CLIENT_IP"
      }
    }`), want: 0},
		{name: "authentication only", src: ociAPIDeployment(`    request_policies {
      authentication {
        type        = "CUSTOM_AUTHENTICATION"
        function_id = "ocid"
      }
    }`), want: 1},
		{name: "dynamic request policies", src: ociAPIDeployment(`    dynamic "request_policies" {
      for_each = var.policies
      content {}
    }`), want: 0},
	},
	"bp_oci_apigw_auth": {
		{name: "no authentication", src: ociAPIDeployment(""), want: 1},
		{name: "authentication", src: ociAPIDeployment(`    request_policies {
      authentication {
        type        = "CUSTOM_AUTHENTICATION"
        function_id = "ocid"
      }
    }`), want: 0},
		{name: "rate limiting only", src: ociAPIDeployment(`    request_policies {
      rate_limiting {
        rate_in_requests_per_second = 10
        rate_key                    = "CLIENT_IP"
      }
    }`), want: 1},
	},
	"bp_oci_lb_http_listener": {
		{name: "HTTP on a public LB", src: ociListener("", `  protocol = "HTTP"`), want: 1},
		{name: "HTTP on a private LB", src: ociListener(`  is_private = true`, `  protocol = "HTTP"`), want: 0},
		{name: "HTTP with a redirect rule set", src: ociListener("", `  protocol       = "HTTP"
  rule_set_names = ["redirect_to_https"]`), want: 0},
		{name: "TLS", src: ociListener("", `  protocol = "HTTP"
  ssl_configuration {
    certificate_name        = "cert"
    verify_peer_certificate = false
  }`), want: 0},
		{name: "unknown LB", src: `resource "oci_load_balancer_listener" "http" {
  name                     = "http"
  load_balancer_id         = var.lb
  default_backend_set_name = "app"
  port                     = 80
  protocol                 = "HTTP"
}`, want: 0},
	},
	"bp_oci_lb_waf": {
		{name: "public LB without WAF", src: ociLB(""), want: 1},
		{name: "WAF", src: ociLB("") + `resource "oci_waf_web_app_firewall" "waf" {
  compartment_id             = "ocid"
  backend_type               = "LOAD_BALANCER"
  load_balancer_id           = oci_load_balancer_load_balancer.lb.id
  web_app_firewall_policy_id = "ocid"
}`, want: 0},
		{name: "private LB", src: ociLB(`  is_private = true`), want: 0},
	},
	"bp_oci_waf_rate_limit": {
		{name: "no rate limiting", src: ociWAFPolicy(""), want: 1},
		{name: "rate limiting rules", src: ociWAFPolicy(`  request_rate_limiting {
    rules {
      type        = "REQUEST_RATE_LIMITING"
      name        = "per-ip"
      action_name = "tooMany"
      configurations {
        period_in_seconds = 60
        requests_limit    = 300
      }
    }
  }`), want: 0},
		{name: "empty rate limiting", src: ociWAFPolicy(`  request_rate_limiting {}`), want: 1},
	},
	"bp_oci_secret_rotation": {
		{name: "never rotates", src: ociSecret(""), want: 1},
		{name: "scheduled rotation", src: ociSecret(`  rotation_config {
    is_scheduled_rotation_enabled = true
    rotation_interval             = "P30D"
    target_system_details {
      target_system_type = "FUNCTION"
      function_id        = "ocid"
    }
  }`), want: 0},
		{name: "rotation off", src: ociSecret(`  rotation_config {
    is_scheduled_rotation_enabled = false
    target_system_details {
      target_system_type = "FUNCTION"
      function_id        = "ocid"
    }
  }`), want: 1},
	},
}

func init() {
	for code, list := range edgeCases {
		structureCases[code] = append(structureCases[code], list...)
	}
}

// edgeDefaults pins the severity and default of every edge rule: warnings
// only for settings that expose traffic or data, production-only practices
// (a WAF everywhere, access logs, rotation, private clusters) opt-in.
var edgeDefaults = map[string]struct {
	severity model.Severity
	optIn    bool
}{
	"bp_apigw_throttling":               {model.SeverityInfo, false},
	"bp_apigw_access_logs":              {model.SeverityInfo, true},
	"bp_apigw_route_auth":               {model.SeverityInfo, true},
	"bp_cloudfront_https":               {model.SeverityWarning, false},
	"bp_cloudfront_waf":                 {model.SeverityInfo, true},
	"bp_alb_http_forward":               {model.SeverityWarning, false},
	"bp_lb_tls_policy":                  {model.SeverityWarning, false},
	"bp_alb_drop_invalid_headers":       {model.SeverityWarning, false},
	"bp_alb_waf":                        {model.SeverityInfo, true},
	"bp_waf_rate_limit":                 {model.SeverityInfo, false},
	"bp_secret_rotation":                {model.SeverityInfo, true},
	"bp_sqs_encryption":                 {model.SeverityInfo, false},
	"bp_azure_apim_rate_limit":          {model.SeverityInfo, false},
	"bp_azure_messaging_tls":            {model.SeverityWarning, false},
	"bp_azure_aks_rbac":                 {model.SeverityWarning, false},
	"bp_azure_aks_api_ranges":           {model.SeverityWarning, true},
	"bp_azure_aks_azure_policy":         {model.SeverityInfo, true},
	"bp_azure_kv_default_deny":          {model.SeverityInfo, true},
	"bp_azure_law_retention":            {model.SeverityInfo, true},
	"bp_gcp_ssl_policy_weak":            {model.SeverityWarning, false},
	"bp_gcp_https_proxy_ssl_policy":     {model.SeverityWarning, true},
	"bp_gcp_cloud_armor":                {model.SeverityInfo, true},
	"bp_gcp_armor_rules":                {model.SeverityInfo, false},
	"bp_gcp_cloud_run_public":           {model.SeverityInfo, false},
	"bp_gcp_gke_legacy_abac":            {model.SeverityWarning, false},
	"bp_gcp_gke_release_channel":        {model.SeverityInfo, false},
	"bp_gcp_gke_private_nodes":          {model.SeverityWarning, true},
	"bp_gcp_gke_control_plane_networks": {model.SeverityWarning, true},
	"bp_gcp_gke_workload_identity":      {model.SeverityWarning, true},
	"bp_oci_apigw_rate_limit":           {model.SeverityInfo, false},
	"bp_oci_apigw_auth":                 {model.SeverityInfo, true},
	"bp_oci_lb_http_listener":           {model.SeverityWarning, true},
	"bp_oci_lb_waf":                     {model.SeverityInfo, true},
	"bp_oci_waf_rate_limit":             {model.SeverityInfo, false},
	"bp_oci_secret_rotation":            {model.SeverityInfo, true},
}

func TestEdgeRulesMetadata(t *testing.T) {
	byCode := map[string]model.BestPracticeRule{}
	for _, r := range Rules() {
		byCode[r.Code] = r
	}
	if len(edgeDefaults) != len(edgeCases) {
		t.Errorf("edgeDefaults lists %d rules, edgeCases %d", len(edgeDefaults), len(edgeCases))
	}
	for code, want := range edgeDefaults {
		r, ok := byCode[code]
		if !ok {
			t.Errorf("%s: not a rule", code)
			continue
		}
		if r.Severity != want.severity || r.DefaultOff != want.optIn {
			t.Errorf("%s: severity %s, opt-in %v; want %s, %v", code, r.Severity, r.DefaultOff, want.severity, want.optIn)
		}
		if r.Title == "" || r.Description == "" || r.Category != CategorySecurity && r.Category != CategoryOperations {
			t.Errorf("%s: title, description and category required (%+v)", code, r)
		}
		if r.Severity == model.SeverityWarning && r.Pillars[0] != model.PillarSecurity {
			t.Errorf("%s: only security rules warn (pillars %v)", code, r.Pillars)
		}
		if len(edgeCases[code]) < 2 {
			t.Errorf("%s: needs positive and negative cases", code)
		}
	}
}

// Every finding of an edge rule is about a node and names the fix.
func TestEdgeFindingsHaveFix(t *testing.T) {
	for code, cases := range edgeCases {
		for _, tc := range cases {
			if tc.want == 0 {
				continue
			}
			for _, d := range caseFindings(t, tc, code) {
				if d.NodeID == "" || d.Fix == "" || d.Detail == "" {
					t.Errorf("%s/%s: finding without node, why or fix: %+v", code, tc.name, d)
				}
			}
		}
	}
}

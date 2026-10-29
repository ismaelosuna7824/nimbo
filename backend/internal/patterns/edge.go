package patterns

func edge() []Pattern {
	return []Pattern{
		{
			ID:      "api-gateway",
			Name:    "API gateway (gateway routing)",
			Kind:    KindPattern,
			Problem: "Clients would otherwise call many backend endpoints directly, coupling them to internal topology and duplicating auth, throttling and TLS in every service.",
			UseWhen: []string{
				"public or partner APIs",
				"several backend services exposed under one domain",
				"centralized authentication, throttling, versioning and request validation",
			},
			AvoidWhen: []string{
				"a single internal service with a load balancer already in front (an extra hop adds latency and cost)",
			},
			Tradeoffs: []string{
				"one entry point, decoupled clients, consistent policies",
				"another component to scale and secure; risk of putting business logic in it",
			},
			Practices: []string{
				"keep business logic out of the gateway; route, authenticate, throttle, validate",
				"JWT/OIDC authorizers at the gateway; services still verify identity",
				"throttling and usage plans per client; WAF in front",
				"access logs, tracing and per-route metrics; versioned routes (/v1)",
				"private integrations (VPC link) to internal services",
			},
			Blocks: []Block{
				aws("API Gateway (HTTP or REST API) with routes integrated with Lambda, ALB/ECS via VPC link; usage plans for throttling.", "aws_apigatewayv2_api", "aws_apigatewayv2_route", "aws_apigatewayv2_integration", "aws_api_gateway_rest_api", "aws_api_gateway_usage_plan"),
				azure("API Management with APIs and products in front of Container Apps/AKS.", "azurerm_api_management", "azurerm_api_management_api", "azurerm_api_management_product"),
				gcp("External HTTPS load balancer URL map routing to Cloud Run backends (serverless NEGs).", "google_compute_url_map", "google_compute_backend_service", "google_compute_global_forwarding_rule"),
				oci("API Gateway with deployments routing to Functions or HTTP backends.", "oci_apigateway_gateway", "oci_apigateway_deployment"),
				docker("Kong, Traefik, KrakenD or APISIX in front of backend services.", "kong", "traefik", "krakend", "apisix"),
				kube("Gateway + HTTPRoute (Gateway API) or Ingress routing to Services.", "Gateway", "HTTPRoute", "Ingress", "Service"),
			},
			Related:   []string{"backends-for-frontends", "gateway-offloading", "rate-limiting", "microservices", "federated-identity"},
			Templates: []string{"aws-api-gateway", "azure-api-gateway", "gcp-api-gateway", "oci-api-gateway", "docker-api-gateway", "k8s-api-gateway"},
			Keywords:  []string{"api gateway", "gateway", "pasarela", "api", "apis", "rest", "rest api", "api rest", "public api", "api publica", "endpoints", "routing", "enrutamiento", "crud", "graphql"},
			Sources: []Source{
				azP("Gateway Routing pattern", "gateway-routing"),
				awsP("API routing patterns", "api-routing.html"),
				msioP("API gateway", "apigateway.html"),
			},
		},
		{
			ID:      "backends-for-frontends",
			Name:    "Backends for frontends (BFF)",
			Kind:    KindPattern,
			Problem: "Web, mobile and partner clients need different data shapes and flows; one general-purpose backend becomes bloated and a bottleneck between teams.",
			UseWhen: []string{
				"distinct client experiences (web, iOS/Android, smart TV, partners) with different needs",
				"separate frontend teams that want to own their backend adaptation",
			},
			AvoidWhen: []string{
				"a single client type",
				"clients needs are nearly identical (one API suffices)",
			},
			Tradeoffs: []string{
				"tailored, efficient APIs per client and team autonomy",
				"duplicated logic across BFFs; more services to run",
			},
			Practices: []string{
				"BFFs aggregate and adapt; domain logic stays in downstream services",
				"one BFF per experience owned by that frontend team",
				"handle tokens server-side in the BFF for browser clients (no tokens in local storage)",
				"share common concerns via the gateway, not copy-paste",
			},
			Blocks: []Block{
				aws("Separate API Gateway APIs and Lambda/ECS services per client type.", "aws_apigatewayv2_api", "aws_lambda_function", "aws_ecs_service"),
				azure("Separate Container Apps per client behind API Management.", "azurerm_container_app", "azurerm_api_management_api"),
				gcp("Separate Cloud Run services per client.", "google_cloud_run_v2_service"),
				oci("Separate API Gateway deployments and functions per client.", "oci_apigateway_deployment", "oci_functions_function"),
				docker("One BFF service (node) per client behind a gateway.", "node", "traefik"),
				kube("One Deployment per BFF with HTTPRoutes per host/path.", "Deployment", "Service", "HTTPRoute"),
			},
			Related:  []string{"api-gateway", "microservices"},
			Keywords: []string{"bff", "backend for frontend", "backends for frontends", "mobile", "movil", "app movil", "mobile app", "web and mobile", "web y movil", "multiple clients", "varios clientes"},
			Sources: []Source{
				azP("Backends for Frontends pattern", "backends-for-frontends"),
			},
		},
		{
			ID:      "gateway-offloading",
			Name:    "Gateway offloading",
			Kind:    KindPattern,
			Problem: "Every service re-implements cross-cutting concerns (TLS termination, WAF, compression, auth, caching), inconsistently.",
			UseWhen: []string{
				"shared TLS certificates, WAF and authentication across services",
				"public internet-facing workloads",
			},
			AvoidWhen: []string{
				"the concern needs service-specific business context",
			},
			Tradeoffs: []string{
				"consistent edge security and simpler services",
				"the gateway becomes critical shared infrastructure; must be highly available",
			},
			Practices: []string{
				"terminate TLS at the edge with managed certificates; redirect HTTP→HTTPS",
				"WAF with managed rule sets and rate-based rules on every public entry point",
				"security headers, compression and edge caching at the proxy",
				"re-encrypt to backends or keep them private",
			},
			Blocks: []Block{
				aws("CloudFront and/or ALB with WAFv2 web ACL in front of services.", "aws_cloudfront_distribution", "aws_lb", "aws_wafv2_web_acl"),
				azure("Front Door with a WAF policy, or Application Gateway with a WAF policy.", "azurerm_cdn_frontdoor_profile", "azurerm_cdn_frontdoor_firewall_policy", "azurerm_application_gateway", "azurerm_web_application_firewall_policy"),
				gcp("External HTTPS load balancer with Cloud Armor security policy.", "google_compute_global_forwarding_rule", "google_compute_backend_service", "google_compute_security_policy"),
				oci("Load balancer with OCI WAF policy.", "oci_load_balancer_load_balancer", "oci_waf_web_app_firewall", "oci_waf_web_app_firewall_policy"),
				docker("NGINX/Caddy/Traefik reverse proxy terminating TLS in front of services; oauth2-proxy for auth.", "nginx", "caddy", "traefik", "oauth2-proxy"),
				kube("Ingress controller with TLS Secret, or Gateway with listeners.", "Ingress", "IngressClass", "Gateway", "Secret"),
			},
			Related:   []string{"api-gateway", "rate-limiting", "sidecar", "federated-identity"},
			Templates: []string{"aws-reverse-proxy", "azure-reverse-proxy", "gcp-reverse-proxy", "oci-reverse-proxy", "docker-reverse-proxy", "k8s-reverse-proxy"},
			Keywords:  []string{"reverse proxy", "proxy inverso", "tls", "ssl", "https", "waf", "firewall", "edge", "borde", "certificates", "certificados", "cdn"},
			Sources: []Source{
				azP("Gateway Offloading pattern", "gateway-offloading"),
			},
		},
		{
			ID:      "static-content-hosting",
			Name:    "Static content hosting",
			Kind:    KindPattern,
			Problem: "Serving static assets (SPA, images, downloads) from compute servers wastes capacity and adds latency.",
			UseWhen: []string{
				"single-page apps, static sites, documentation, media and downloads",
				"global audiences benefiting from a CDN",
			},
			AvoidWhen: []string{
				"content must be rendered per request with private data",
			},
			Tradeoffs: []string{
				"cheap, fast and scales without servers",
				"cache invalidation and deploy versioning to manage",
			},
			Practices: []string{
				"private bucket served only through the CDN (origin access control)",
				"content-hashed file names with long cache TTLs; short TTL for index.html",
				"HTTPS only, security headers, WAF on the distribution",
			},
			Blocks: []Block{
				aws("Private S3 bucket behind CloudFront with origin access control.", "aws_s3_bucket", "aws_cloudfront_distribution", "aws_cloudfront_origin_access_control"),
				azure("Storage account static website, or Static Web Apps, behind Front Door.", "azurerm_storage_account", "azurerm_storage_account_static_website", "azurerm_static_web_app"),
				gcp("Cloud Storage bucket behind a backend bucket with Cloud CDN.", "google_storage_bucket", "google_compute_backend_bucket"),
				oci("Object Storage bucket (public read or via pre-authenticated requests).", "oci_objectstorage_bucket"),
				docker("NGINX or Caddy serving the build output.", "nginx", "caddy"),
				kube("NGINX Deployment + Service + Ingress serving static files.", "Deployment", "Service", "Ingress"),
			},
			Related:   []string{"valet-key", "cache-aside", "serverless"},
			Templates: []string{"aws-static-site", "docker-nginx-static"},
			Keywords:  []string{"static site", "sitio estatico", "pagina estatica", "landing", "landing page", "spa", "react", "angular", "vue", "frontend", "cdn", "assets", "blog", "documentation", "documentacion"},
			Sources: []Source{
				azP("Static Content Hosting pattern", "static-content-hosting"),
			},
		},
		{
			ID:      "valet-key",
			Name:    "Valet key",
			Kind:    KindPattern,
			Problem: "Proxying uploads and downloads of large files through the application wastes compute and bandwidth.",
			UseWhen: []string{
				"users upload or download large files (images, video, documents)",
				"direct, time-limited access to a specific object is acceptable",
			},
			AvoidWhen: []string{
				"content must be inspected or transformed before storage (validate asynchronously after upload instead, or proxy)",
			},
			Tradeoffs: []string{
				"offloads data transfer from the app; scales with storage",
				"tokens leak if long-lived; validation happens after upload",
			},
			Practices: []string{
				"short-lived presigned URLs/SAS scoped to one object and one operation",
				"limit content type and size in the signed policy",
				"scan/validate uploads asynchronously via storage events before use",
				"private bucket; never public write",
			},
			Blocks: []Block{
				aws("App issues S3 presigned URLs; S3 notifications trigger post-upload processing.", "aws_s3_bucket", "aws_s3_bucket_notification", "aws_lambda_function"),
				azure("App issues user-delegation SAS tokens for a blob container; Event Grid on blob created.", "azurerm_storage_account", "azurerm_storage_container", "azurerm_eventgrid_system_topic"),
				gcp("Signed URLs for Cloud Storage objects; bucket notifications to Pub/Sub.", "google_storage_bucket", "google_storage_notification"),
				oci("Pre-authenticated requests on Object Storage.", "oci_objectstorage_bucket", "oci_objectstorage_preauthrequest"),
				docker("MinIO presigned URLs.", "minio"),
				kube("MinIO StatefulSet with an Ingress for presigned access.", "StatefulSet", "Service", "Ingress"),
			},
			Related:   []string{"claim-check", "static-content-hosting", "federated-identity"},
			Templates: []string{"oci-object-storage"},
			Keywords:  []string{"upload", "uploads", "subir archivos", "subida de archivos", "archivos grandes", "large files", "presigned", "signed url", "url firmada", "download", "descargas", "file storage", "almacenamiento de archivos", "imagenes", "videos"},
			Sources: []Source{
				azP("Valet Key pattern", "valet-key"),
				{"Amazon S3: Sharing objects with presigned URLs", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/ShareObjectPreSignedURL.html"},
			},
		},
		{
			ID:      "federated-identity",
			Name:    "Federated identity",
			Kind:    KindPattern,
			Problem: "Each application managing its own users and passwords is insecure and gives users many credentials.",
			UseWhen: []string{
				"user login, SSO, social login, B2B partners",
				"several applications sharing one identity",
			},
			AvoidWhen: []string{
				"machine-to-machine only (use workload identity/IAM roles)",
			},
			Tradeoffs: []string{
				"delegated authentication, MFA and SSO out of the box",
				"dependency on the identity provider; token handling to get right",
			},
			Practices: []string{
				"OIDC/OAuth 2.0 with authorization code + PKCE; never roll your own auth",
				"validate tokens (issuer, audience, expiry, signature) at the gateway and services",
				"short-lived access tokens, rotating refresh tokens, MFA",
				"claims-based authorization; least privilege scopes",
			},
			Blocks: []Block{
				aws("Cognito user pool with identity providers; API Gateway JWT authorizers.", "aws_cognito_user_pool", "aws_cognito_identity_provider", "aws_apigatewayv2_api"),
				azure("Microsoft Entra ID (outside Terraform resources here) validated by API Management policies.", "azurerm_api_management"),
				gcp("Identity Platform with OIDC providers.", "google_identity_platform_config"),
				oci("IAM identity domains as the identity provider.", "oci_identity_domain"),
				docker("Keycloak, Zitadel or Authentik as the IdP; oauth2-proxy in front of apps.", "keycloak", "zitadel", "authentik", "oauth2-proxy"),
				kube("Keycloak Deployment with a database StatefulSet; Ingress auth via oauth2-proxy.", "Deployment", "StatefulSet", "Ingress", "Secret"),
			},
			Related:  []string{"api-gateway", "gateway-offloading", "valet-key"},
			Keywords: []string{"login", "auth", "authentication", "autenticacion", "sso", "single sign on", "oauth", "oidc", "identity", "identidad", "sign in", "iniciar sesion", "social login", "mfa"},
			Sources: []Source{
				azP("Federated Identity pattern", "federated-identity"),
			},
		},
	}
}

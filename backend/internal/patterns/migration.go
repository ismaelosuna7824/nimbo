package patterns

func migration() []Pattern {
	return []Pattern{
		{
			ID:      "strangler-fig",
			Name:    "Strangler fig",
			Kind:    KindPattern,
			Problem: "Rewriting a legacy system in one big bang is risky; it must be replaced gradually while it keeps serving users.",
			UseWhen: []string{
				"migrating a monolith or legacy system to new services or the cloud",
				"features can be moved one route/capability at a time",
			},
			AvoidWhen: []string{
				"the system is small enough to replace at once",
				"requests cannot be intercepted and routed",
			},
			Tradeoffs: []string{
				"incremental, reversible migration with continuous delivery of value",
				"old and new run side by side for a long time; data synchronization is hard",
			},
			Practices: []string{
				"put a routing façade (gateway/proxy) in front of the legacy system first",
				"move one capability at a time; route it to the new service; keep a rollback switch",
				"anti-corruption layer between new and legacy models",
				"plan data migration/synchronization per capability (CDC, dual reads)",
				"retire legacy code paths as soon as traffic moves",
			},
			Blocks: []Block{
				aws("API Gateway or ALB listener rules route migrated paths to new Lambda/ECS services and the rest to the legacy backend.", "aws_api_gateway_rest_api", "aws_lb", "aws_lb_listener_rule", "aws_ecs_service", "aws_lambda_function"),
				azure("API Management or Front Door routes migrated APIs to Container Apps, the rest to the legacy backend.", "azurerm_api_management", "azurerm_api_management_api", "azurerm_container_app"),
				gcp("Load balancer URL map path rules split between new Cloud Run services and the legacy backend.", "google_compute_url_map", "google_compute_backend_service", "google_cloud_run_v2_service"),
				oci("API Gateway deployment routes to new functions or the legacy backend.", "oci_apigateway_deployment", "oci_functions_function"),
				docker("Traefik/NGINX routing paths to the new service or the legacy container.", "traefik", "nginx"),
				kube("Ingress/HTTPRoute path rules split traffic between new and legacy Services.", "Ingress", "HTTPRoute", "Service", "Deployment"),
			},
			Related:  []string{"anti-corruption-layer", "api-gateway", "microservices", "modular-monolith"},
			Keywords: []string{"strangler", "strangler fig", "migrate", "migrar", "migracion", "migration", "legacy", "legado", "monolith", "monolito", "modernize", "modernizar", "rewrite", "reescribir"},
			Sources: []Source{
				azP("Strangler Fig pattern", "strangler-fig"),
				awsP("Strangler fig pattern", "strangler-fig.html"),
				msioP("Strangler application", "refactoring/strangler-application.html"),
			},
		},
		{
			ID:      "anti-corruption-layer",
			Name:    "Anti-corruption layer",
			Kind:    KindPattern,
			Problem: "Integrating with a legacy or external system leaks its model and quirks into new services.",
			UseWhen: []string{
				"new services must talk to a legacy system during migration",
				"integrating third-party systems with a different domain model",
			},
			AvoidWhen: []string{
				"the models are already compatible",
			},
			Tradeoffs: []string{
				"clean domain models in new code",
				"an extra hop and component to maintain; latency",
			},
			Practices: []string{
				"translate at one boundary; never let legacy types escape",
				"scale and monitor the ACL like any service",
				"retire it with the legacy system",
			},
			Blocks: []Block{
				aws("Lambda or ECS service translating between new APIs and the legacy system.", "aws_lambda_function", "aws_ecs_service"),
				azure("Container App adapter service.", "azurerm_container_app"),
				gcp("Cloud Run adapter service.", "google_cloud_run_v2_service"),
				oci("Function adapter.", "oci_functions_function"),
				docker("Adapter service between new services and legacy containers.", "node", "golang"),
				kube("Adapter Deployment + Service.", "Deployment", "Service"),
			},
			Related:  []string{"strangler-fig", "microservices"},
			Keywords: []string{"anti corruption", "anticorrupcion", "acl", "legacy integration", "integracion legado", "adapter", "adaptador", "third party", "terceros", "erp", "mainframe"},
			Sources: []Source{
				azP("Anti-corruption Layer pattern", "anti-corruption-layer"),
				awsP("Anti-corruption layer pattern", "acl.html"),
			},
		},
		{
			ID:      "sidecar",
			Name:    "Sidecar",
			Kind:    KindPattern,
			Problem: "Cross-cutting capabilities (proxying, mTLS, log shipping, config reload) must be added to services written in different languages without changing them.",
			UseWhen: []string{
				"polyglot services needing the same infrastructure features",
				"service mesh, log/metrics agents, auth proxies",
			},
			AvoidWhen: []string{
				"a library in a single-language codebase is simpler",
				"latency-critical paths where an extra hop matters",
			},
			Tradeoffs: []string{
				"language-agnostic, independently upgraded capabilities",
				"more containers per instance: resources and lifecycle coupling",
			},
			Practices: []string{
				"keep sidecars small with resource limits",
				"share the lifecycle with the app (start before, stop after)",
				"pin versions and roll them out like application code",
			},
			Blocks: []Block{
				aws("Extra container definitions in the ECS task definition (e.g. Envoy, ADOT collector, FireLens).", "aws_ecs_task_definition", "aws_ecs_service"),
				azure("Additional containers in the Container App template; Dapr sidecar.", "azurerm_container_app"),
				gcp("Cloud Run multi-container services (sidecars).", "google_cloud_run_v2_service"),
				oci("Sidecar containers in OKE pods.", "oci_containerengine_cluster"),
				docker("Companion service sharing the network namespace (network_mode: service:app).", "envoy", "otel-collector"),
				kube("Extra container (or native sidecar initContainer) in the Deployment pod spec.", "Deployment"),
			},
			Related:  []string{"gateway-offloading", "circuit-breaker", "microservices"},
			Keywords: []string{"sidecar", "service mesh", "malla de servicios", "envoy", "istio", "linkerd", "mtls", "dapr"},
			Sources: []Source{
				azP("Sidecar pattern", "sidecar"),
				msioP("Sidecar", "deployment/sidecar.html"),
			},
		},
	}
}

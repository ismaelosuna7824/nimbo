package wellarch

// Layout guides: the folder and environment layout per platform, matching
// Nimbo's exports (hclcodec environments layout, compose environments,
// Kustomize overlays / Helm values). The Terraform clouds share one.

var layouts = map[string]func() *LayoutGuide{
	"aws":        terraformLayout,
	"azure":      terraformLayout,
	"gcp":        terraformLayout,
	"oci":        terraformLayout,
	"docker":     composeLayout,
	"kubernetes": kubernetesLayout,
}

func terraformLayout() *LayoutGuide {
	return &LayoutGuide{
		Summary: "One workflow is one root module with one state per environment. A small design with one environment stays a single root (main.tf, variables.tf, outputs.tf, versions.tf, providers.tf); from two environments up use the environments layout: the resources in modules/<service>, one thin root per environment calling it. " +
			"Modules group a logical concern (network, data, app), never wrap a single resource, nest at most 2 levels, hold no provider blocks (required_providers only) and expose their resources through outputs. " +
			"One state per environment with a remote backend, never CLI workspaces; pin Terraform and providers (~> in roots, >= minimums in modules); validate variable inputs (validation blocks), mark secrets sensitive and feed them from TF_VAR_* secrets, never tfvars; tag every resource with name, environment and owner from one locals map (default_tags where the provider has them). " +
			"Past ~100 resources per state split by layer (network, data, app) into separate workflows linked by data sources, not shared state. Pick the layout with plan_structure.",
		Tree: `<workflow>/                    # layout "environments" (single: one root with the same files)
├── README.md  .gitignore      # .terraform.lock.hcl committed
├── modules/
│   ├── <service>/             # the workflow's resources
│   │   ├── main.tf | <category>.tf  variables.tf  outputs.tf
│   │   └── versions.tf        # required_providers (>= minimums), no provider blocks
│   └── network/  data/  …     # logical groups, ≤2 levels deep
└── environments/
    ├── dev/                   # one root = one state
    │   ├── backend.tf         # remote backend, partial config (+ backend.hcl.example)
    │   ├── main.tf            # module "<service>" { source = "../../modules/<service>" }
    │   ├── providers.tf  versions.tf (~> pins)  variables.tf  outputs.tf
    │   ├── terraform.tfvars   # non-secret values of this environment
    │   └── .env.example       # env vars, secrets blank
    └── prod/`,
		Environments: []string{
			"dev: smallest sizes, single AZ, short retention, no deletion protection.",
			"staging: production-like shape at reduced size to rehearse changes.",
			"prod: multi-AZ, deletion protection, backups and retention, WAF and alarms on public entry points, autoscaling with min/max bounds.",
		},
		Sources: []Source{
			{"AWS Prescriptive Guidance: Terraform code base structure", "https://docs.aws.amazon.com/prescriptive-guidance/latest/terraform-aws-provider-best-practices/structure.html"},
			{"HashiCorp Terraform style guide", "https://developer.hashicorp.com/terraform/language/style"},
			{"Google Cloud: Terraform root modules best practices", "https://docs.cloud.google.com/docs/terraform/best-practices/root-modules"},
			{"Google Cloud: Terraform general style and structure", "https://cloud.google.com/docs/terraform/best-practices/general-style-structure"},
		},
	}
}

func composeLayout() *LayoutGuide {
	return &LayoutGuide{
		Summary: "One base compose.yaml holds the services, networks and named volumes every environment shares; each environment adds only what it changes in compose.<env>.yaml and/or an env file .env.<env> (either alone is an environment), run as docker compose -f compose.yaml -f compose.<env>.yaml --env-file .env --env-file .env.<env>. " +
			"Credentials go in secrets: (files under secrets/, gitignored) or secret env vars, never inline environment: values or images; .env files are gitignored and documented by .env.example. " +
			"Production overlays set restart policies, resource limits and healthchecks and never bind-mount source code; bind mounts and debug ports belong to the dev overlay.",
		Tree: `<project>/
├── compose.yaml          # base: services, networks, named volumes
├── compose.dev.yaml      # dev overlay: bind mounts, debug ports
├── compose.prod.yaml     # prod overlay: restart, limits, no code bind mounts
├── .env                  # shared defaults (gitignored) + .env.example
├── .env.dev  .env.prod   # per-environment variables (gitignored)
├── secrets/              # files referenced by secrets: (gitignored)
├── Dockerfile  .dockerignore
└── …`,
		Environments: []string{
			"dev: bind mounts and debug ports allowed, one replica.",
			"prod: restart: unless-stopped/always, CPU/memory/PID limits, healthchecks, secrets:, no code bind mounts, only the proxy publishes ports.",
		},
		Sources: []Source{
			{"Use Compose in production", "https://docs.docker.com/compose/how-tos/production/"},
			{"Compose deploy specification", "https://docs.docker.com/reference/compose-file/deploy/"},
		},
	}
}

func kubernetesLayout() *LayoutGuide {
	return &LayoutGuide{
		Summary: "Kustomize: k8s/base holds the manifests every environment shares and overlays/<env> patches only what differs (replicas, images, resources, ConfigMap values, Secret generators); Helm: chart/ with values.yaml plus values-<env>.yaml. Plain manifests are the same in every environment. " +
			"One namespace per app and environment, labelled with Pod Security Standards (pod-security.kubernetes.io/enforce: restricted where the workload allows). " +
			"The prod overlay adds a HorizontalPodAutoscaler with minReplicas ≥ 2 and a maxReplicas bound, a PodDisruptionBudget (only with ≥2 replicas), default-deny NetworkPolicies plus explicit allows, and TLS on the Ingress; dev stays at one replica.",
		Tree: `k8s/
├── base/
│   ├── kustomization.yaml
│   ├── deployment.yaml   # probes, requests/limits, restricted securityContext
│   ├── service.yaml  ingress.yaml
│   └── networkpolicy.yaml
└── overlays/
    ├── dev/
    │   └── kustomization.yaml   # namespace <app>-dev, replicas 1
    └── prod/
        ├── kustomization.yaml   # namespace <app>-prod, PSS labels
        ├── hpa.yaml  pdb.yaml   # min ≥2, max bound; PDB with ≥2 replicas
        └── patch-resources.yaml
chart/                           # Helm alternative
├── Chart.yaml  values.yaml
└── values-dev.yaml  values-prod.yaml`,
		Environments: []string{
			"dev: one replica, no HPA/PDB, relaxed resources.",
			"prod: HPA (min ≥2, max bound), PDB, NetworkPolicy default-deny, PSS restricted labels, Ingress TLS, pinned image tags or digests.",
		},
		Sources: []Source{
			{"Kustomize: declarative management of Kubernetes objects", "https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/"},
			{"Kubernetes Pod Security Standards", "https://kubernetes.io/docs/concepts/security/pod-security-standards/"},
			{"Kubernetes Pod Security Admission", "https://kubernetes.io/docs/concepts/security/pod-security-admission/"},
		},
	}
}

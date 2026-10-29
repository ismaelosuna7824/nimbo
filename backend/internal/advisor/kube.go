package advisor

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Kubernetes recommendations: Traefik rate limiting (and TLS in
// production) on Ingresses, Secrets instead of literal credentials,
// NetworkPolicies, persistent storage for databases, requests, limits and
// probes, HorizontalPodAutoscalers, PodDisruptionBudgets and per-environment
// overlays. They read the saved graph of rendered objects
// (Workflow.Kubernetes.Graph); objects of vendored charts are left to their
// chart's values.

// kubeGraph indexes the workflow's own objects.
type kubeGraph struct {
	nodes []*model.KubeNode
	byID  map[string]*model.KubeNode
	edges []model.KubeEdge
}

func (g *kubeGraph) ofKind(kinds ...string) []*model.KubeNode {
	var out []*model.KubeNode
	for _, n := range g.nodes {
		if slices.Contains(kinds, n.Kind) {
			out = append(out, n)
		}
	}
	return out
}

// incoming are the nodes with an edge of kind into target; outgoing the
// nodes source has an edge of kind to.
func (g *kubeGraph) incoming(target, kind string) []*model.KubeNode {
	var out []*model.KubeNode
	for _, e := range g.edges {
		if e.Target == target && e.Kind == kind {
			if n := g.byID[e.Source]; n != nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func (g *kubeGraph) outgoing(source, kind string) []*model.KubeNode {
	var out []*model.KubeNode
	for _, e := range g.edges {
		if e.Source == source && e.Kind == kind {
			if n := g.byID[e.Target]; n != nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func ns(n *model.KubeNode) string { return cmp.Or(n.Namespace, "default") }

func kubeRecs(wf *model.Workflow) ([]rec, bool) {
	k := wf.Kubernetes
	if k == nil {
		return nil, false
	}
	g := &kubeGraph{byID: map[string]*model.KubeNode{}, edges: k.Graph.Edges}
	var names []string
	for i := range k.Graph.Nodes {
		n := &k.Graph.Nodes[i]
		if n.Chart != "" {
			continue
		}
		g.nodes = append(g.nodes, n)
		g.byID[n.ID] = n
		if n.Kind == "Namespace" {
			names = append(names, n.Name)
		}
	}
	envs := map[string]bool{}
	for _, s := range k.Sources {
		for e := range s.Environments {
			envs[e] = true
		}
	}
	names = append(names, sortedKeys(envs)...)
	names = append(names, k.Environment)
	production := prodLike(wf, names...)
	var out []rec
	add := func(priority int, r Recommendation) {
		if r.Local == "" {
			r.Local = LocalRuns
		}
		out = append(out, rec{Recommendation: r, priority: priority})
	}
	kubeIngress(g, add)
	kubeSecrets(g, add)
	kubeNetworkPolicy(g, add)
	kubePersistence(g, add)
	kubeRuntime(g, add)
	kubeHPA(g, add)
	kubePDB(g, add)
	if production && len(envs) < 2 && len(g.ofKind("Deployment", "StatefulSet", "DaemonSet")) > 0 {
		kubeEnvironments(g, len(envs), add)
	}
	return out, production
}

// isTraefikMiddleware reports a Traefik Middleware custom resource.
func isTraefikMiddleware(n *model.KubeNode) bool {
	return n.Kind == "Middleware" && (strings.HasPrefix(n.APIVersion, "traefik.io/") || strings.HasPrefix(n.APIVersion, "traefik.containo.us/"))
}

func kubeIngress(g *kubeGraph, add func(int, Recommendation)) {
	middlewares := map[string]bool{}
	for _, n := range g.nodes {
		if isTraefikMiddleware(n) {
			middlewares[ns(n)] = true
		}
	}
	var limited []string
	var docs []string
	var tlsNodes []string
	var tlsDocs []string
	done := map[string]bool{}
	for _, ing := range g.ofKind("Ingress") {
		if len(ing.Routes) == 0 {
			continue
		}
		namespace := ns(ing)
		if !middlewares[namespace] {
			limited = append(limited, ing.ID)
			if !done[namespace] {
				done[namespace] = true
				docs = append(docs, fmt.Sprintf(`apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: rate-limit
  namespace: %s
spec:
  rateLimit:
    average: 50
    burst: 100
`, namespace))
			}
			docs = append(docs, fmt.Sprintf(`# Merge into the existing Ingress %s/%s:
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
  annotations:
    traefik.ingress.kubernetes.io/router.middlewares: %s-rate-limit@kubernetescrd
`, namespace, ing.Name, ing.Name, namespace, namespace))
		}
		var hosts []string
		for _, r := range ing.Routes {
			if !r.TLS && r.Host != "" && !strings.HasSuffix(r.Host, ".localhost") && r.Host != "localhost" && !slices.Contains(hosts, r.Host) {
				hosts = append(hosts, r.Host)
			}
		}
		if len(hosts) > 0 {
			tlsNodes = append(tlsNodes, ing.ID)
			var b strings.Builder
			fmt.Fprintf(&b, "# Merge into the existing Ingress %s/%s (the Secret comes from cert-manager or your certificate pipeline):\napiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: %s\n  namespace: %s\nspec:\n  tls:\n    - secretName: %s-tls\n      hosts:\n", namespace, ing.Name, ing.Name, namespace, ing.Name)
			for _, h := range hosts {
				fmt.Fprintf(&b, "        - %s\n", h)
			}
			tlsDocs = append(tlsDocs, b.String())
		}
	}
	if len(limited) > 0 {
		add(prioEdge, Recommendation{
			ID:    "k8s_ingress_rate_limit",
			Title: "Rate-limit the Ingresses with a Traefik middleware",
			Why: "An Ingress forwards every request to the Services behind it; nothing stops one client from flooding them. The local cluster (k3s) routes Ingresses through Traefik, so a RateLimit middleware " +
				"referenced by the router.middlewares annotation limits each client IP (50 requests per second on average, bursts of 100). With ingress-nginx in production use its nginx.ingress.kubernetes.io/limit-rps annotation instead.",
			Pillars: pillarsEdge,
			Nodes:   limited,
			Add: []Addition{
				{Type: "Middleware", Purpose: "traefik.io/v1alpha1 rateLimit per client IP, one per namespace"},
				{Type: "Ingress", Purpose: "traefik.ingress.kubernetes.io/router.middlewares annotation referencing it"},
			},
			Snippet: strings.Join(docs, "---\n"),
			Scope:   ScopeRecommended,
			Skip:    "Ingresses that only serve internal tools on localhost.",
			Docs:    "https://doc.traefik.io/traefik/middlewares/http/ratelimit/",
		})
	}
	if len(tlsNodes) > 0 {
		add(prioEdge+2, Recommendation{
			ID:      "k8s_ingress_tls",
			Title:   "Serve the Ingress hosts over TLS",
			Why:     "Without spec.tls the Ingress serves plain HTTP: credentials and cookies cross the network readable. A TLS section with a certificate Secret (cert-manager issues and renews it) turns HTTPS on.",
			Pillars: []model.Pillar{model.PillarSecurity},
			Nodes:   tlsNodes,
			Add:     []Addition{{Type: "Ingress", Purpose: "spec.tls with a certificate Secret per host"}, {Type: "Secret", Purpose: "kubernetes.io/tls certificate (cert-manager Certificate in production)"}},
			Snippet: strings.Join(tlsDocs, "---\n"),
			Scope:   ScopeProduction,
			Skip:    "*.localhost development hosts.",
			Rules:   []string{"k8s_ingress_tls"},
			Docs:    "https://kubernetes.io/docs/concepts/services-networking/ingress/#tls",
		})
	}
}

func kubeSecrets(g *kubeGraph, add func(int, Recommendation)) {
	var nodes []string
	var docs []string
	for _, w := range g.ofKind("Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "Pod") {
		if w.Workload == nil {
			continue
		}
		var keys []string
		container := ""
		for _, c := range w.Workload.Containers {
			for _, e := range c.Env {
				if e.Secret && e.From == nil {
					keys = append(keys, e.Name)
					container = cmp.Or(container, c.Name)
				}
			}
		}
		if len(keys) == 0 {
			continue
		}
		nodes = append(nodes, w.ID)
		secret := w.Name + "-credentials"
		var b strings.Builder
		fmt.Fprintf(&b, "# Create the Secret outside the manifests (kubectl create secret generic %s --from-literal=…), then merge the env entries into %s/%s:\napiVersion: apps/v1\nkind: %s\nmetadata:\n  name: %s\n  namespace: %s\nspec:\n  template:\n    spec:\n      containers:\n        - name: %s\n          env:\n", secret, w.Kind, w.Name, w.Kind, w.Name, ns(w), container)
		for _, k := range keys {
			fmt.Fprintf(&b, "            - name: %s\n              valueFrom:\n                secretKeyRef:\n                  name: %s\n                  key: %s\n", k, secret, strings.ToLower(k))
		}
		docs = append(docs, b.String())
	}
	for _, cm := range g.ofKind("ConfigMap") {
		var keys []string
		for _, k := range cm.Data {
			if secretKey.MatchString(k) {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			continue
		}
		nodes = append(nodes, cm.ID)
		docs = append(docs, fmt.Sprintf("# Move %s out of ConfigMap %s/%s into a Secret created outside the manifests:\napiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\ntype: Opaque\nstringData: {}\n", strings.Join(keys, ", "), ns(cm), cm.Name, cm.Name+"-secrets", ns(cm)))
	}
	if len(nodes) == 0 {
		return
	}
	add(prioSecrets, Recommendation{
		ID:    "k8s_secrets",
		Title: "Keep credentials in Secrets, not in manifests or ConfigMaps",
		Why: "Literal credentials in a manifest or ConfigMap are committed to git and readable by anyone who can read the namespace's configuration. Secrets are a separate RBAC resource, can be encrypted at rest " +
			"and are filled from outside the repository (kubectl, Sealed Secrets or External Secrets in production).",
		Pillars: pillarsSecret,
		Nodes:   nodes,
		Add:     []Addition{{Type: "Secret", Purpose: "holds the credentials; containers read them with secretKeyRef"}},
		Snippet: strings.Join(docs, "---\n"),
		Scope:   ScopeRecommended,
		Rules:   []string{"k8s_secret_env", "k8s_secret_in_configmap"},
		Docs:    "https://kubernetes.io/docs/concepts/security/secrets-good-practices/",
	})
}

func kubeNetworkPolicy(g *kubeGraph, add func(int, Recommendation)) {
	covered := map[string]bool{}
	for _, n := range g.ofKind("NetworkPolicy") {
		covered[ns(n)] = true
	}
	var namespaces, nodes []string
	for _, w := range g.ofKind("Deployment", "StatefulSet", "DaemonSet") {
		if n := ns(w); !covered[n] && n != "kube-system" {
			nodes = append(nodes, w.ID)
			if !slices.Contains(namespaces, n) {
				namespaces = append(namespaces, n)
			}
		}
	}
	if len(namespaces) == 0 {
		return
	}
	slices.Sort(namespaces)
	var docs []string
	for _, n := range namespaces {
		docs = append(docs, fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-ingress
  namespace: %s
spec:
  podSelector: {}
  policyTypes:
    - Ingress
`, n), fmt.Sprintf(`# Pods of the namespace and the ingress controller (Traefik in kube-system) may still connect.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-namespace-and-ingress
  namespace: %s
spec:
  podSelector: {}
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector: {}
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
`, n))
	}
	add(prioNetwork, Recommendation{
		ID:    "k8s_network_policy",
		Title: "Deny ingress by default with NetworkPolicies",
		Why: "Without a NetworkPolicy every pod accepts connections from every other pod in the cluster: one compromised workload reaches the databases of all namespaces. " +
			"A default-deny policy plus an explicit allow list limits that blast radius; k3s enforces NetworkPolicies with its embedded controller.",
		Pillars: pillarsSecret,
		Nodes:   nodes,
		Add: []Addition{
			{Type: "NetworkPolicy", Purpose: "default-deny ingress per namespace"},
			{Type: "NetworkPolicy", Purpose: "allow the namespace's own pods and the ingress controller"},
		},
		Snippet: strings.Join(docs, "---\n"),
		Scope:   ScopeRecommended,
		Rules:   []string{"k8s_network_policy"},
		Docs:    "https://kubernetes.io/docs/concepts/services-networking/network-policies/#default-deny-all-ingress-traffic",
	})
}

// kubeData returns the data image of a workload's containers.
func kubeData(w *model.KubeNode) (dataImage, string, bool) {
	for _, c := range w.Workload.Containers {
		if c.ImageRef == nil {
			continue
		}
		repo := c.ImageRef.Repo
		if d, ok := dataImages[repo[strings.LastIndex(repo, "/")+1:]]; ok && d.dir != "" && !strings.Contains(strings.TrimPrefix(repo, "library/"), "/") {
			return d, c.Name, true
		}
	}
	return dataImage{}, "", false
}

func kubePersistence(g *kubeGraph, add func(int, Recommendation)) {
	var nodes []string
	var docs []string
	for _, w := range g.ofKind("Deployment", "StatefulSet") {
		if w.Workload == nil || len(w.Workload.VolumeClaimTemplates) > 0 || len(g.outgoing(w.ID, model.KubeEdgePVC)) > 0 {
			continue
		}
		d, container, ok := kubeData(w)
		if !ok {
			continue
		}
		nodes = append(nodes, w.ID)
		header := fmt.Sprintf("# Replace Deployment/%s with a StatefulSet (same selector and pod template, plus serviceName) that keeps %s's data on a claim per replica:", w.Name, d.label)
		if w.Kind == "StatefulSet" {
			header = fmt.Sprintf("# Merge into StatefulSet/%s: a claim per replica for %s's data.", w.Name, d.label)
		}
		docs = append(docs, header+fmt.Sprintf(`
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: %s
  namespace: %s
spec:
  template:
    spec:
      containers:
        - name: %s
          volumeMounts:
            - name: data
              mountPath: %s
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes:
          - ReadWriteOnce
        resources:
          requests:
            storage: 10Gi
`, w.Name, ns(w), container, d.dir))
	}
	if len(nodes) == 0 {
		return
	}
	add(prioData, Recommendation{
		ID:    "k8s_persistent_data",
		Title: "Keep database data on PersistentVolumeClaims",
		Why: "Without a volume claim the database writes into the container's filesystem: a restart, reschedule or rollout deletes the data. A StatefulSet with volumeClaimTemplates keeps it on a PersistentVolume " +
			"(k3s provisions them with local-path); in production also schedule dumps or VolumeSnapshots.",
		Pillars: pillarsData,
		Nodes:   nodes,
		Add:     []Addition{{Type: "StatefulSet", Purpose: "volumeClaimTemplates mounted on the data directory"}, {Type: "PersistentVolumeClaim", Purpose: "created per replica from the template"}},
		Snippet: strings.Join(docs, "---\n"),
		Scope:   ScopeRecommended,
		Skip:    "Caches and test databases you reseed on every start.",
		Rules:   []string{"k8s_ephemeral_data"},
		Docs:    "https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/#stable-storage",
	})
}

func kubeRuntime(g *kubeGraph, add func(int, Recommendation)) {
	var nodes []string
	var docs []string
	for _, w := range g.ofKind("Deployment", "StatefulSet", "DaemonSet") {
		if w.Workload == nil {
			continue
		}
		var b strings.Builder
		for _, c := range w.Workload.Containers {
			sized := c.Resources.Requests.CPU != "" && c.Resources.Requests.Memory != "" && c.Resources.Limits.Memory != ""
			probed := c.Probes.Readiness != nil
			if sized && probed {
				continue
			}
			fmt.Fprintf(&b, "        - name: %s\n", c.Name)
			if !sized {
				b.WriteString("          resources:\n            requests:\n              cpu: 100m\n              memory: 128Mi\n            limits:\n              memory: 256Mi\n")
			}
			if !probed {
				port := ""
				for _, p := range c.Ports {
					port = cmp.Or(port, fmt.Sprint(p.ContainerPort))
				}
				if port != "" {
					fmt.Fprintf(&b, "          readinessProbe:\n            tcpSocket:\n              port: %s\n            periodSeconds: 10\n", port)
				}
			}
		}
		if b.Len() == 0 {
			continue
		}
		nodes = append(nodes, w.ID)
		if len(nodes) > 3 {
			continue
		}
		docs = append(docs, fmt.Sprintf("# Merge into %s/%s (size from kubectl top under load; use an HTTP readinessProbe on a health path if the app has one):\napiVersion: apps/v1\nkind: %s\nmetadata:\n  name: %s\n  namespace: %s\nspec:\n  template:\n    spec:\n      containers:\n%s", w.Kind, w.Name, w.Kind, w.Name, ns(w), b.String()))
	}
	if len(nodes) == 0 {
		return
	}
	if len(nodes) > 3 {
		docs[len(docs)-1] += "# Same for: " + strings.Join(nodes[3:], ", ") + "\n"
	}
	add(prioRuntime, Recommendation{
		ID:    "k8s_resources_probes",
		Title: "Give each container requests, a memory limit and a readiness probe",
		Why: "Without requests the scheduler packs pods blindly and the HPA has nothing to compute utilization from; without a memory limit one leak takes the node down. " +
			"Without a readiness probe a Service sends traffic to pods that are still starting or stuck.",
		Pillars: []model.Pillar{model.PillarReliability, model.PillarPerformance},
		Nodes:   nodes,
		Add: []Addition{
			{Type: "resources", Purpose: "CPU and memory requests, memory limit"},
			{Type: "readinessProbe", Purpose: "the pod receives traffic only when it answers"},
		},
		Snippet: strings.Join(docs, "---\n"),
		Scope:   ScopeRecommended,
		Rules:   []string{"k8s_no_requests", "k8s_no_memory_limit", "k8s_no_readiness_probe"},
		Docs:    "https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/",
	})
}

func kubeHPA(g *kubeGraph, add func(int, Recommendation)) {
	var nodes []string
	var docs []string
	for _, w := range g.ofKind("Deployment") {
		// Only Deployments behind a Service: queue workers need event-driven scaling.
		if w.Workload == nil || len(g.incoming(w.ID, model.KubeEdgeScales)) > 0 || len(g.incoming(w.ID, model.KubeEdgeSelects)) == 0 {
			continue
		}
		if _, _, data := kubeData(w); data {
			continue
		}
		nodes = append(nodes, w.ID)
		docs = append(docs, fmt.Sprintf(`apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: %s
  namespace: %s
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: %s
  minReplicas: 2
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
`, w.Name, ns(w), w.Name))
	}
	if len(nodes) == 0 {
		return
	}
	add(prioScaling, Recommendation{
		ID:    "k8s_hpa",
		Title: "Scale the serving Deployments with a HorizontalPodAutoscaler",
		Why: "A fixed replica count is too small for a peak and wasted while idle. An HPA on CPU (the containers need CPU requests) keeps 2 replicas for availability and caps at a maximum that bounds cost; " +
			"k3s ships metrics-server, so it works locally.",
		Pillars: pillarsScaling,
		Nodes:   nodes,
		Add:     []Addition{{Type: "HorizontalPodAutoscaler", Purpose: "2 to 10 replicas at 70% average CPU"}},
		Snippet: strings.Join(docs, "---\n"),
		Scope:   ScopeProduction,
		Skip:    "Development namespaces and fixed-size workers (use KEDA only once a queue-length signal exists).",
		Rules:   []string{"k8s_no_hpa", "k8s_hpa_no_requests"},
		Docs:    "https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/",
	})
}

func kubePDB(g *kubeGraph, add func(int, Recommendation)) {
	var nodes []string
	var docs []string
	for _, w := range g.ofKind("Deployment", "StatefulSet") {
		if w.Workload == nil || len(w.Workload.Selector) == 0 || len(g.incoming(w.ID, model.KubeEdgeDisrupts)) > 0 {
			continue
		}
		replicas := 1
		if w.Workload.Replicas != nil {
			replicas = *w.Workload.Replicas
		}
		for _, h := range g.incoming(w.ID, model.KubeEdgeScales) {
			if h.Scaler != nil && h.Scaler.MinReplicas > replicas {
				replicas = h.Scaler.MinReplicas
			}
		}
		if replicas < 2 {
			continue
		}
		nodes = append(nodes, w.ID)
		var b strings.Builder
		fmt.Fprintf(&b, "apiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata:\n  name: %s\n  namespace: %s\nspec:\n  maxUnavailable: 1\n  selector:\n    matchLabels:\n", w.Name, ns(w))
		for _, k := range sortedKeys(w.Workload.Selector) {
			fmt.Fprintf(&b, "      %s: %q\n", k, w.Workload.Selector[k])
		}
		docs = append(docs, b.String())
	}
	if len(nodes) == 0 {
		return
	}
	add(prioScaling+1, Recommendation{
		ID:      "k8s_pdb",
		Title:   "Keep replicas available during node maintenance with PodDisruptionBudgets",
		Why:     "Node drains and cluster upgrades evict pods without regard for your replicas; a PodDisruptionBudget makes them evict one at a time, so the service keeps answering.",
		Pillars: []model.Pillar{model.PillarReliability},
		Nodes:   nodes,
		Add:     []Addition{{Type: "PodDisruptionBudget", Purpose: "maxUnavailable: 1 for each replicated workload"}},
		Snippet: strings.Join(docs, "---\n"),
		Scope:   ScopeProduction,
		Skip:    "Single-replica workloads: a budget there blocks drains.",
		Rules:   []string{"k8s_pdb"},
		Docs:    "https://kubernetes.io/docs/tasks/run-application/configure-pdb/",
	})
}

func kubeEnvironments(g *kubeGraph, have int, add func(int, Recommendation)) {
	var b strings.Builder
	b.WriteString("# overlays/prod/kustomization.yaml (overlays/dev/ the same with dev sizes)\napiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../../base\n")
	var replicas []string
	for _, w := range g.ofKind("Deployment") {
		if w.Workload == nil || len(replicas) == 3 {
			continue
		}
		if _, _, data := kubeData(w); !data {
			replicas = append(replicas, fmt.Sprintf("  - name: %s\n    count: 2\n", w.Name))
		}
	}
	if len(replicas) > 0 {
		b.WriteString("replicas:\n" + strings.Join(replicas, ""))
	}
	state := "no environments"
	if have == 1 {
		state = "a single environment"
	}
	add(prioEnvironment, Recommendation{
		ID:    "k8s_environments",
		Title: "Split dev and prod into Kustomize overlays",
		Why: fmt.Sprintf("The deployment looks production-bound but has %s. Keep the shared manifests in base/ and the per-environment differences (replicas, resources, hosts, HPA) in overlays/<env>/ "+
			"(values-<env>.yaml for Helm charts), so dev and prod render from the same base and Nimbo switches between them.", state),
		Pillars: []model.Pillar{model.PillarOperations, model.PillarReliability},
		Add: []Addition{
			{Type: "Kustomization", Purpose: "overlays/prod: production replicas, resources and hosts"},
			{Type: "Kustomization", Purpose: "overlays/dev: small sizes for local runs"},
		},
		Snippet: b.String(),
		Scope:   ScopeProduction,
		Skip:    "Manifests that only ever run on the local cluster.",
		Docs:    "https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/#bases-and-overlays",
	})
}

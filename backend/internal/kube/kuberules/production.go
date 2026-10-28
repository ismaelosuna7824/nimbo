package kuberules

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Production rules: how a workload survives rollouts, node drains and
// node failures (topology spread, disruption budgets, rollout strategy) and
// the hygiene a production cluster expects (labels, quotas, digests).

// hpaFor is the HorizontalPodAutoscaler scaling w (nil when none).
func (c *checker) hpaFor(w *workload) *object {
	for _, o := range c.in.objects {
		if o.kind == "HorizontalPodAutoscaler" && o.namespace == w.o.namespace &&
			str(o.node, "spec", "scaleTargetRef", "kind") == w.o.kind && str(o.node, "spec", "scaleTargetRef", "name") == w.o.name {
			return o
		}
	}
	return nil
}

// minReplicas is the fewest pods w runs: the HPA's minReplicas when one
// scales it (the HPA owns spec.replicas then), else spec.replicas (default 1).
func (c *checker) minReplicas(w *workload) int {
	if h := c.hpaFor(w); h != nil {
		if m, ok := intAt(h.node, "spec", "minReplicas"); ok {
			return m
		}
		return 1
	}
	if n, ok := intAt(w.o.node, "spec", "replicas"); ok {
		return n
	}
	return 1
}

// selectorLabels are the labels identifying w's pods in fix hints: its
// spec.selector.matchLabels, else the pod labels.
func selectorLabels(w *workload) map[string]string {
	if s := labelSelector(get(w.o.node, "spec", "selector")); s != nil && len(s.labels) > 0 {
		return s.labels
	}
	return w.podLabels
}

// spreadKeys are the topology keys that put replicas on different nodes
// or zones.
var spreadKeys = map[string]bool{"kubernetes.io/hostname": true, "topology.kubernetes.io/zone": true}

// selectsPods reports whether the label selector at n selects labels (a
// missing or empty selector counts no pods for spreading and anti-affinity).
func selectsPods(n *yaml.Node, labels map[string]string) bool {
	s := labelSelector(n)
	return !s.empty() && s.matches(labels)
}

// spreads reports whether w's pods are spread over nodes or zones by a
// topology spread constraint or pod anti-affinity that counts its own pods.
func spreads(w *workload) bool {
	for _, t := range items(w.pod, "topologySpreadConstraints") {
		if spreadKeys[str(t, "topologyKey")] && selectsPods(get(t, "labelSelector"), w.podLabels) {
			return true
		}
	}
	for _, t := range items(w.pod, "affinity", "podAntiAffinity", "requiredDuringSchedulingIgnoredDuringExecution") {
		if spreadKeys[str(t, "topologyKey")] && selectsPods(get(t, "labelSelector"), w.podLabels) {
			return true
		}
	}
	for _, p := range items(w.pod, "affinity", "podAntiAffinity", "preferredDuringSchedulingIgnoredDuringExecution") {
		t := get(p, "podAffinityTerm")
		if spreadKeys[str(t, "topologyKey")] && selectsPods(get(t, "labelSelector"), w.podLabels) {
			return true
		}
	}
	return false
}

// checkTopologySpread: in production-like environments the replicas of a
// Deployment or StatefulSet should be spread over nodes, or one node
// failure takes them all down.
func (c *checker) checkTopologySpread() {
	if !prodLike(c.in.env) {
		return
	}
	for _, w := range c.workloads {
		if w.o.kind != "Deployment" && w.o.kind != "StatefulSet" || w.pod == nil {
			continue
		}
		n := c.minReplicas(w)
		if n < 2 || spreads(w) {
			continue
		}
		c.diag(model.SeverityWarning, "k8s_topology_spread", w.o, w.podPath,
			fmt.Sprintf("%s runs %d replicas in %s that may all land on the same node", w.label(), n, c.in.env),
			fmt.Sprintf("Add topologySpreadConstraints: [{maxSkew: 1, topologyKey: kubernetes.io/hostname, whenUnsatisfiable: ScheduleAnyway, labelSelector: {matchLabels: %s}}] to the pod template.", labelsYAML(selectorLabels(w))))
	}
}

// scaled resolves an int-or-percent field against total pods (percentages
// round up or down like the controller that reads them); def applies when
// the field is absent. ok is false for unparsable values.
func scaled(n *yaml.Node, total int, roundUp bool, def string) (v int, ok bool) {
	s := def
	if n = resolve(n); !isNull(n) {
		s = n.Value
	}
	if p, isPct := strings.CutSuffix(s, "%"); isPct {
		pct, err := strconv.Atoi(p)
		if err != nil {
			return 0, false
		}
		f := float64(pct) * float64(total) / 100
		if roundUp {
			return int(math.Ceil(f)), true
		}
		return int(math.Floor(f)), true
	}
	i, err := strconv.Atoi(s)
	return i, err == nil
}

// isZero reports whether an int-or-percent field is literally 0 or 0%.
func isZero(n *yaml.Node) bool {
	n = resolve(n)
	return !isNull(n) && (n.Value == "0" || n.Value == "0%")
}

// checkDisruptionBudgets: a PodDisruptionBudget should allow at least one
// eviction, or node drains and cluster upgrades hang on it; a pod covered
// by two budgets can't be evicted at all. (A budget selecting no pods is
// k8s_selector_mismatch; a missing one is k8s_single_replica.)
func (c *checker) checkDisruptionBudgets() {
	budgets := map[*workload][]*object{}
	for _, o := range c.in.objects {
		if o.kind != "PodDisruptionBudget" || o.group() != "policy" {
			continue
		}
		sel := labelSelector(get(o.node, "spec", "selector"))
		if sel.empty() {
			continue
		}
		ws := c.selected(o.namespace, sel)
		if len(ws) == 0 {
			continue
		}
		total, counted := 0, true
		var names []string
		for _, w := range ws {
			budgets[w] = append(budgets[w], o)
			names = append(names, w.label())
			if w.o.kind != "Deployment" && w.o.kind != "StatefulSet" && w.o.kind != "ReplicaSet" {
				counted = false // DaemonSet pods follow the node count
			}
			total += c.minReplicas(w)
		}
		target := strings.Join(names, ", ")
		maxU, minA := get(o.node, "spec", "maxUnavailable"), get(o.node, "spec", "minAvailable")
		switch {
		case isZero(maxU):
			c.diag(model.SeverityWarning, "k8s_pdb", o, "spec.maxUnavailable",
				fmt.Sprintf("PodDisruptionBudget %s allows no eviction of %s (maxUnavailable: 0): node drains and cluster upgrades hang on it", o.name, target),
				"Use maxUnavailable: 1 (or minAvailable: 1 with 2+ replicas) so pods move one at a time.")
		case !isNull(minA) && strings.TrimSpace(resolve(minA).Value) == "100%":
			c.diag(model.SeverityWarning, "k8s_pdb", o, "spec.minAvailable",
				fmt.Sprintf("PodDisruptionBudget %s requires every pod of %s to stay up (minAvailable: 100%%): node drains and cluster upgrades hang on it", o.name, target),
				"Use maxUnavailable: 1 (or minAvailable: 1 with 2+ replicas) so pods move one at a time.")
		case !isNull(minA) && counted:
			need, ok := scaled(minA, total, true, "")
			if !ok || need < total || total == 0 {
				break
			}
			if total == 1 {
				c.diag(model.SeverityWarning, "k8s_pdb", o, "spec.minAvailable",
					fmt.Sprintf("PodDisruptionBudget %s protects %s, which runs a single replica: minAvailable: %s blocks every eviction", o.name, target, resolve(minA).Value),
					"Run 2+ replicas (or an HPA with minReplicas: 2), or drop the PodDisruptionBudget in this environment.")
				break
			}
			c.diag(model.SeverityWarning, "k8s_pdb", o, "spec.minAvailable",
				fmt.Sprintf("PodDisruptionBudget %s requires %d available pods but %s runs %d: no pod can be evicted", o.name, need, target, total),
				fmt.Sprintf("Lower minAvailable to %d or use maxUnavailable: 1.", total-1))
		}
	}
	for _, w := range c.workloads {
		if l := budgets[w]; len(l) > 1 {
			c.diag(model.SeverityWarning, "k8s_pdb", l[1], "spec.selector",
				fmt.Sprintf("%s's pods are selected by PodDisruptionBudgets %s and %s: the eviction API refuses to evict pods covered by more than one budget", w.label(), l[0].name, l[1].name),
				"Keep one PodDisruptionBudget per workload (narrow the selectors so they don't overlap).")
		}
	}
}

// rwoClaim reports whether w mounts a ReadWriteOnce claim (two pods of a
// rolling update can't share it: Recreate is the right strategy).
func (c *checker) rwoClaim(w *workload) bool {
	for _, v := range items(w.pod, "volumes") {
		claim := str(v, "persistentVolumeClaim", "claimName")
		if claim == "" {
			continue
		}
		pvc := c.get("PersistentVolumeClaim", w.o.namespace, claim)
		if pvc == nil {
			return true
		}
		for _, m := range items(pvc.node, "spec", "accessModes") {
			if m.Value == "ReadWriteOnce" || m.Value == "ReadWriteOncePod" {
				return true
			}
		}
	}
	return false
}

// checkRolloutStrategy: a rollout should keep serving. Deployments with
// 2+ replicas shouldn't stop them all (Recreate, maxUnavailable 100%);
// maxSurge and maxUnavailable both 0 is refused by the API server.
func (c *checker) checkRolloutStrategy() {
	prod := prodLike(c.in.env)
	for _, w := range c.workloads {
		switch w.o.kind {
		case "Deployment":
			typ := str(w.o.node, "spec", "strategy", "type")
			ru := get(w.o.node, "spec", "strategy", "rollingUpdate")
			if typ == "" || typ == "RollingUpdate" {
				surge, unav := get(ru, "maxSurge"), get(ru, "maxUnavailable")
				if isZero(surge) && isZero(unav) {
					c.diag(model.SeverityError, "k8s_rollout_strategy", w.o, "spec.strategy.rollingUpdate",
						fmt.Sprintf("%s: maxSurge and maxUnavailable can't both be 0 (the API server refuses it)", w.label()),
						"Use maxUnavailable: 0 with maxSurge: 1 (new pod first) or maxSurge: 0 with maxUnavailable: 1.")
					continue
				}
			}
			if !prod {
				continue
			}
			n := c.minReplicas(w)
			if n < 2 {
				continue // a single replica is k8s_single_replica
			}
			switch typ {
			case "Recreate":
				if !c.rwoClaim(w) {
					c.diag(model.SeverityWarning, "k8s_rollout_strategy", w.o, "spec.strategy.type",
						fmt.Sprintf("%s uses the Recreate strategy: every rollout stops all %d replicas before starting new ones", w.label(), n),
						"Use strategy {type: RollingUpdate, rollingUpdate: {maxUnavailable: 0, maxSurge: 1}} (Recreate is only needed for a ReadWriteOnce volume).")
				}
			case "", "RollingUpdate":
				if v, ok := scaled(get(ru, "maxUnavailable"), n, false, "25%"); ok && v >= n {
					c.diag(model.SeverityWarning, "k8s_rollout_strategy", w.o, "spec.strategy.rollingUpdate.maxUnavailable",
						fmt.Sprintf("%s's rolling update may take all %d replicas down at once (maxUnavailable: %s)", w.label(), n, str(ru, "maxUnavailable")),
						"Use maxUnavailable: 0 with maxSurge: 1 (or maxUnavailable: 1) so some pods keep serving during a rollout.")
				}
			}
		case "StatefulSet":
			if prod && str(w.o.node, "spec", "updateStrategy", "type") == "OnDelete" {
				c.diag(model.SeverityInfo, "k8s_rollout_strategy", w.o, "spec.updateStrategy.type",
					fmt.Sprintf("%s uses updateStrategy OnDelete in %s: a new version only reaches a pod when someone deletes it", w.label(), c.in.env),
					"Use updateStrategy RollingUpdate (the default) unless an operator rolls the pods.")
			}
		}
	}
}

// Recommended labels (https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/).
var (
	requiredLabels = []string{"app.kubernetes.io/name", "app.kubernetes.io/instance"}
	softLabels     = []string{"app.kubernetes.io/part-of", "app.kubernetes.io/version"}
)

// checkRecommendedLabels: workloads should carry the recommended
// app.kubernetes.io labels dashboards, kubectl and Helm/Argo CD group by.
func (c *checker) checkRecommendedLabels() {
	for _, w := range c.workloads {
		labels := strMap(w.o.node, "metadata", "labels")
		var missing []string
		for _, l := range requiredLabels {
			if labels[l] == "" {
				missing = append(missing, l)
			}
		}
		if len(missing) == 0 {
			continue
		}
		for _, l := range softLabels {
			if labels[l] == "" {
				missing = append(missing, l)
			}
		}
		path := "metadata.labels"
		if get(w.o.node, "metadata", "labels") == nil {
			path = "metadata"
		}
		c.diag(model.SeverityInfo, "k8s_recommended_labels", w.o, path,
			fmt.Sprintf("%s is missing the recommended labels %s", w.label(), strings.Join(missing, ", ")),
			fmt.Sprintf("Add metadata.labels {app.kubernetes.io/name: %s, app.kubernetes.io/instance: %s} (plus part-of and version); kustomize labels or a Helm helper set them on every object.", w.o.name, w.o.name))
	}
}

// checkQuotas (opt-in): production-like namespaces should cap what they
// can take from the cluster (ResourceQuota) and give containers default
// requests and limits (LimitRange).
func (c *checker) checkQuotas() {
	if !prodLike(c.in.env) {
		return
	}
	seen := map[string]bool{}
	for _, w := range c.workloads {
		ns := w.o.namespace
		if seen[ns] || systemNamespaces[ns] {
			continue
		}
		seen[ns] = true
		var missing []string
		for _, kind := range []string{"ResourceQuota", "LimitRange"} {
			found := false
			for _, o := range c.in.objects {
				if o.kind == kind && o.group() == "" && o.namespace == ns {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, kind)
			}
		}
		if len(missing) == 0 {
			continue
		}
		at, path := w.o, "metadata.namespace"
		for _, o := range c.in.objects {
			if o.kind == "Namespace" && o.group() == "" && o.name == ns {
				at, path = o, "metadata"
				break
			}
		}
		c.diag(model.SeverityInfo, "k8s_resource_quota", at, path,
			fmt.Sprintf("Namespace %s has no %s in %s", ns, strings.Join(missing, " or "), c.in.env),
			"Add a ResourceQuota (requests.cpu, requests.memory, limits.memory, pods) and a LimitRange with default container requests and limits.")
	}
}

// checkImageDigests (opt-in): production-like images should be pinned by
// digest (a tag can be moved to other content); locally built images are
// skipped (they are loaded into the nodes, not pulled).
func (c *checker) checkImageDigests() {
	if !prodLike(c.in.env) {
		return
	}
	for _, w := range c.workloads {
		for _, ct := range w.containers() {
			img := str(ct.n, "image")
			if img == "" || strings.Contains(img, "@sha256:") {
				continue
			}
			if ref := compose.ParseImage(img); ref != nil && c.in.localImages[ref.Repo] {
				continue
			}
			c.diag(model.SeverityInfo, "k8s_image_digest", w.o, field(ct.path, "image"),
				fmt.Sprintf("%s %s: image %s is not pinned by digest", w.label(), ct.label(), img),
				fmt.Sprintf("Pin it as %s@sha256:<digest> (docker buildx imagetools inspect %s prints it).", img, img))
		}
	}
}

// checkEphemeralData: a Deployment or StatefulSet running a database or
// message broker image should keep its data on a PersistentVolumeClaim;
// in the container filesystem or an emptyDir every restart, rollout or
// reschedule starts empty. (hostPath keeps data on the node and is
// k8s_host_path.)
func (c *checker) checkEphemeralData() {
	for _, w := range c.workloads {
		if w.o.kind != "Deployment" && w.o.kind != "StatefulSet" || c.persistent(w) {
			continue
		}
		for _, ct := range w.containers() {
			img := str(ct.n, "image")
			paths := compose.DataPaths(img)
			if cat := compose.DataCategory(img); !ct.serves(w) || cat != "database" && cat != "messaging" || len(paths) == 0 {
				continue
			}
			fix := fmt.Sprintf("Mount a PersistentVolumeClaim on %s (a persistentVolumeClaim volume, or volumeClaimTemplates in a StatefulSet).", paths[0])
			if w.o.kind == "StatefulSet" {
				fix = fmt.Sprintf("Add spec.volumeClaimTemplates (e.g. {metadata: {name: data}, spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}}) and mount it on %s.", paths[0])
			}
			c.diag(model.SeverityWarning, "k8s_ephemeral_data", w.o, field(ct.path, "image"),
				fmt.Sprintf("%s %s runs %s without a PersistentVolumeClaim: its data is lost when the pod restarts or moves", w.label(), ct.label(), img), fix)
			break
		}
	}
}

// persistent reports whether w's pods keep data beyond their lifetime:
// claim templates, a persistentVolumeClaim volume or a hostPath.
func (c *checker) persistent(w *workload) bool {
	if len(items(w.o.node, "spec", "volumeClaimTemplates")) > 0 {
		return true
	}
	for _, v := range items(w.pod, "volumes") {
		if get(v, "persistentVolumeClaim") != nil || get(v, "hostPath") != nil {
			return true
		}
	}
	return false
}

// checkAutoscaling (opt-in): in production-like environments a stateless
// Deployment running 2+ replicas with cpu requests is ready for a
// HorizontalPodAutoscaler; a fixed replica count is sized for the peak
// (idle capacity) or for the average (overload at the peak).
func (c *checker) checkAutoscaling() {
	if !prodLike(c.in.env) {
		return
	}
	for _, w := range c.workloads {
		if w.o.kind != "Deployment" || c.hpaFor(w) != nil || c.stateful(w) {
			continue
		}
		n := c.minReplicas(w)
		if n < 2 {
			continue // a single replica is k8s_single_replica
		}
		defaults, _ := c.limitRangeDefaults(w.o.namespace)
		requests := false
		for _, ct := range w.containers() {
			if !ct.serves(w) {
				continue
			}
			if len(missingRequests(ct.n, defaults, "cpu")) > 0 {
				requests = false
				break
			}
			requests = true
		}
		if !requests {
			continue // missing requests are k8s_no_requests
		}
		c.diag(model.SeverityInfo, "k8s_no_hpa", w.o, "spec.replicas",
			fmt.Sprintf("%s runs a fixed %d replicas in %s: no HorizontalPodAutoscaler scales it", w.label(), n, c.in.env),
			fmt.Sprintf("Add a HorizontalPodAutoscaler: {spec: {scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: %s}, minReplicas: %d, maxReplicas: %d, metrics: [{type: Resource, resource: {name: cpu, target: {type: Utilization, averageUtilization: 70}}}]}}.", w.o.name, n, 2*n))
	}
}

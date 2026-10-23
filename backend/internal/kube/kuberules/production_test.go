package kuberules

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// deployment is a Deployment "name" in namespace shop; spec and pod are
// extra fields of spec (indented 2) and of the pod spec (indented 6).
func deployment(name, spec, pod string) string {
	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: shop
spec:
%[2]s
  selector:
    matchLabels: {app: %[1]s}
  template:
    metadata:
      labels: {app: %[1]s}
    spec:
%[3]s
      containers:
        - name: app
          image: nginx:1.27
`, name, spec, pod)
}

func hpa(name string, min int) string {
	return fmt.Sprintf(`apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: %[1]s, namespace: shop}
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: %[1]s}
  minReplicas: %[2]d
  maxReplicas: 5
`, name, min)
}

func pdb(name, budget, app string) string {
	return fmt.Sprintf(`apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: {name: %s, namespace: shop}
spec:
  %s
  selector:
    matchLabels: {app: %s}
`, name, budget, app)
}

func docs(parts ...string) map[string]string {
	return map[string]string{"a.yaml": strings.Join(parts, "---\n")}
}

func only(diags []model.Diagnostic, code string) []model.Diagnostic {
	var out []model.Diagnostic
	for _, d := range diags {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

const spreadHost = `      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector: {matchLabels: {app: web}}`

func TestTopologySpread(t *testing.T) {
	cases := []struct {
		name, env, spec, pod string
		extra                []string
		want                 int
	}{
		{"two replicas unspread in prod", "prod", "  replicas: 2", "", nil, 1},
		{"same in dev", "dev", "  replicas: 2", "", nil, 0},
		{"one replica", "prod", "  replicas: 1", "", nil, 0},
		{"HPA minReplicas 2 without spec.replicas", "prod", "", "", []string{hpa("web", 2)}, 1},
		{"HPA minReplicas 1 overrides replicas 3", "prod", "  replicas: 3", "", []string{hpa("web", 1)}, 0},
		{"hostname spread", "prod", "  replicas: 2", spreadHost, nil, 0},
		{"zone spread", "prod", "  replicas: 2", strings.Replace(spreadHost, "kubernetes.io/hostname", "topology.kubernetes.io/zone", 1), nil, 0},
		{"spread on an unrelated key", "prod", "  replicas: 2", strings.Replace(spreadHost, "kubernetes.io/hostname", "rack", 1), nil, 1},
		{"spread counting other pods", "prod", "  replicas: 2", strings.Replace(spreadHost, "app: web", "app: other", 1), nil, 1},
		{"preferred anti-affinity", "staging", "  replicas: 2", `      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                topologyKey: kubernetes.io/hostname
                labelSelector: {matchLabels: {app: web}}`, nil, 0},
		{"required anti-affinity", "prod", "  replicas: 2", `      affinity:
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            - topologyKey: kubernetes.io/hostname
              labelSelector: {matchLabels: {app: web}}`, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := only(checkFiles(t, tc.env, docs(append([]string{deployment("web", tc.spec, tc.pod)}, tc.extra...)...)), "k8s_topology_spread")
			if len(got) != tc.want {
				t.Fatalf("got %d findings, want %d: %v", len(got), tc.want, codes(got))
			}
			for _, d := range got {
				if d.Severity != model.SeverityWarning || !strings.Contains(d.Fix, "matchLabels: {app: web}") {
					t.Errorf("finding %+v", d)
				}
			}
		})
	}
}

func TestDisruptionBudgets(t *testing.T) {
	cases := []struct {
		name, env string
		parts     []string
		want      []string // message fragments, one per finding
	}{
		{"minAvailable 1 of 2", "prod", []string{deployment("web", "  replicas: 2", ""), pdb("web", "minAvailable: 1", "web")}, nil},
		{"minAvailable 2 of 2", "prod", []string{deployment("web", "  replicas: 2", ""), pdb("web", "minAvailable: 2", "web")}, []string{"requires 2 available pods but Deployment web runs 2"}},
		{"minAvailable 2 of 3", "prod", []string{deployment("web", "  replicas: 3", ""), pdb("web", "minAvailable: 2", "web")}, nil},
		{"HPA minReplicas counts", "prod", []string{deployment("web", "", ""), hpa("web", 2), pdb("web", "minAvailable: 2", "web")}, []string{"runs 2"}},
		{"single replica in any environment", "dev", []string{deployment("web", "", ""), pdb("web", "minAvailable: 1", "web")}, []string{"runs a single replica"}},
		{"maxUnavailable 0", "", []string{deployment("web", "  replicas: 3", ""), pdb("web", "maxUnavailable: 0", "web")}, []string{"maxUnavailable: 0"}},
		{"maxUnavailable 0%", "", []string{deployment("web", "  replicas: 3", ""), pdb("web", "maxUnavailable: 0%", "web")}, []string{"maxUnavailable: 0"}},
		{"maxUnavailable 1", "", []string{deployment("web", "  replicas: 1", ""), pdb("web", "maxUnavailable: 1", "web")}, nil},
		{"minAvailable 100%", "", []string{deployment("web", "  replicas: 3", ""), pdb("web", "minAvailable: 100%", "web")}, []string{"minAvailable: 100%"}},
		{"minAvailable 50% of 2", "", []string{deployment("web", "  replicas: 2", ""), pdb("web", "minAvailable: 50%", "web")}, nil},
		{"minAvailable 50% of 1 rounds up", "", []string{deployment("web", "  replicas: 1", ""), pdb("web", "minAvailable: 50%", "web")}, []string{"single replica"}},
		{"selecting nothing is k8s_selector_mismatch", "", []string{deployment("web", "  replicas: 2", ""), pdb("web", "minAvailable: 5", "nope")}, nil},
		{"two budgets", "", []string{deployment("web", "  replicas: 3", ""), pdb("a", "minAvailable: 1", "web"), pdb("b", "maxUnavailable: 1", "web")}, []string{"PodDisruptionBudgets a and b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := only(checkFiles(t, tc.env, docs(tc.parts...)), "k8s_pdb")
			if len(got) != len(tc.want) {
				t.Fatalf("got %d findings, want %d: %v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i].Message, w) || got[i].Severity != model.SeverityWarning || got[i].Fix == "" {
					t.Errorf("finding %q, want %q", got[i].Message, w)
				}
			}
		})
	}
}

func TestRolloutStrategy(t *testing.T) {
	rwo := `apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: data, namespace: shop}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: local-path
  resources: {requests: {storage: 1Gi}}
`
	volume := `      volumes:
        - name: data
          persistentVolumeClaim: {claimName: data}`
	sts := `apiVersion: apps/v1
kind: StatefulSet
metadata: {name: db, namespace: shop}
spec:
  serviceName: db
  replicas: 2
  updateStrategy: {type: OnDelete}
  selector: {matchLabels: {app: db}}
  template:
    metadata: {labels: {app: db}}
    spec:
      containers:
        - name: db
          image: postgres:17-alpine
`
	cases := []struct {
		name, env string
		parts     []string
		want      model.Severity // "" = no finding
	}{
		{"default strategy", "prod", []string{deployment("web", "  replicas: 2", "")}, ""},
		{"Recreate with 2 replicas", "prod", []string{deployment("web", "  replicas: 2\n  strategy: {type: Recreate}", "")}, model.SeverityWarning},
		{"Recreate in dev", "dev", []string{deployment("web", "  replicas: 2\n  strategy: {type: Recreate}", "")}, ""},
		{"Recreate with one replica", "prod", []string{deployment("web", "  replicas: 1\n  strategy: {type: Recreate}", "")}, ""},
		{"Recreate for a ReadWriteOnce volume", "prod", []string{deployment("web", "  replicas: 2\n  strategy: {type: Recreate}", volume), rwo}, ""},
		{"maxUnavailable 100%", "prod", []string{deployment("web", "  replicas: 4\n  strategy: {rollingUpdate: {maxUnavailable: 100%}}", "")}, model.SeverityWarning},
		{"maxUnavailable equals replicas", "prod", []string{deployment("web", "  replicas: 2\n  strategy: {rollingUpdate: {maxUnavailable: 2}}", "")}, model.SeverityWarning},
		{"maxUnavailable replicas-1", "prod", []string{deployment("web", "  replicas: 2\n  strategy: {rollingUpdate: {maxUnavailable: 1}}", "")}, ""},
		{"maxUnavailable 50% rounds down", "prod", []string{deployment("web", "  replicas: 3\n  strategy: {rollingUpdate: {maxUnavailable: 50%}}", "")}, ""},
		{"maxSurge 1 maxUnavailable 0", "prod", []string{deployment("web", "  replicas: 2\n  strategy: {type: RollingUpdate, rollingUpdate: {maxSurge: 1, maxUnavailable: 0}}", "")}, ""},
		{"both 0 is refused in any environment", "", []string{deployment("web", "  replicas: 1\n  strategy: {rollingUpdate: {maxSurge: 0, maxUnavailable: 0}}", "")}, model.SeverityError},
		{"StatefulSet OnDelete", "prod", []string{sts}, model.SeverityInfo},
		{"StatefulSet OnDelete in dev", "dev", []string{sts}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := only(checkFiles(t, tc.env, docs(tc.parts...)), "k8s_rollout_strategy")
			switch {
			case tc.want == "" && len(got) > 0:
				t.Fatalf("unexpected %v", got)
			case tc.want != "" && (len(got) != 1 || got[0].Severity != tc.want):
				t.Fatalf("got %v, want one %s", got, tc.want)
			}
		})
	}
}

func TestRecommendedLabels(t *testing.T) {
	bare := deployment("web", "  replicas: 1", "")
	got := only(checkFiles(t, "", docs(bare)), "k8s_recommended_labels")
	if len(got) != 1 || got[0].Severity != model.SeverityInfo || !strings.Contains(got[0].Message, "app.kubernetes.io/instance") {
		t.Fatalf("bare deployment: %v", got)
	}
	labeled := strings.Replace(bare, "  namespace: shop\n", "  namespace: shop\n  labels: {app.kubernetes.io/name: web, app.kubernetes.io/instance: web}\n", 1)
	if got := only(checkFiles(t, "prod", docs(labeled)), "k8s_recommended_labels"); len(got) != 0 {
		t.Fatalf("name+instance are enough: %v", got)
	}
	half := strings.Replace(bare, "  namespace: shop\n", "  namespace: shop\n  labels: {app.kubernetes.io/name: web}\n", 1)
	if got := only(checkFiles(t, "", docs(half)), "k8s_recommended_labels"); len(got) != 1 || strings.Contains(got[0].Message, "app.kubernetes.io/name,") {
		t.Fatalf("missing instance only: %v", got)
	}
}

func TestOptInProductionRules(t *testing.T) {
	ns := "apiVersion: v1\nkind: Namespace\nmetadata: {name: shop}\n"
	quota := "apiVersion: v1\nkind: ResourceQuota\nmetadata: {name: q, namespace: shop}\nspec: {hard: {pods: \"20\"}}\n"
	limits := "apiVersion: v1\nkind: LimitRange\nmetadata: {name: l, namespace: shop}\nspec: {limits: [{type: Container, default: {memory: 256Mi}}]}\n"
	files := docs(ns, deployment("web", "  replicas: 1", ""))
	on := model.BestPracticeSettings{EnabledRules: []string{"k8s_resource_quota", "k8s_image_digest"}}

	// Off by default.
	for _, code := range []string{"k8s_resource_quota", "k8s_image_digest"} {
		if hasCode(FilterSettings(checkFiles(t, "prod", files), model.BestPracticeSettings{}), code) {
			t.Errorf("%s reported without opting in", code)
		}
		if hasCode(FilterSettings(checkFiles(t, "dev", files), on), code) {
			t.Errorf("%s reported in dev", code)
		}
	}
	diags := FilterSettings(checkFiles(t, "prod", files), on)
	if q := only(diags, "k8s_resource_quota"); len(q) != 1 || !strings.Contains(q[0].Message, "ResourceQuota or LimitRange") || q[0].Attribute != "metadata" {
		t.Errorf("quota: %v", q)
	}
	if d := only(diags, "k8s_image_digest"); len(d) != 1 || !strings.Contains(d[0].Message, "nginx:1.27") {
		t.Errorf("digest: %v", d)
	}

	pinned := strings.Replace(deployment("web", "  replicas: 1", ""), "nginx:1.27", "nginx:1.27@sha256:"+strings.Repeat("a", 64), 1)
	diags = FilterSettings(checkFiles(t, "prod", docs(ns, quota, limits, pinned)), on)
	if hasCode(diags, "k8s_resource_quota") || hasCode(diags, "k8s_image_digest") {
		t.Errorf("quota+limits+digest still flagged: %v", codes(diags))
	}
	// Half the guard rails: still flagged, naming what's missing.
	if q := only(FilterSettings(checkFiles(t, "prod", docs(ns, quota, pinned)), on), "k8s_resource_quota"); len(q) != 1 || !strings.Contains(q[0].Message, "no LimitRange") {
		t.Errorf("quota without LimitRange: %v", q)
	}
	// Locally built images can't be pinned by digest.
	local := strings.Replace(deployment("web", "  replicas: 1", ""), "nginx:1.27", "api:dev", 1)
	if hasCode(FilterSettings(checkFiles(t, "prod", docs(local)), on), "k8s_image_digest") {
		t.Error("local image flagged for digest")
	}
}

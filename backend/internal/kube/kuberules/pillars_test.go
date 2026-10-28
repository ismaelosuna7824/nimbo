package kuberules

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Every k8s_* rule names at least one Well-Architected pillar, without
// duplicates, and the pillar table names no unknown rule.
func TestRulePillars(t *testing.T) {
	for _, r := range Rules() {
		if len(r.Pillars) == 0 {
			t.Errorf("%s has no pillar", r.Code)
		}
		for i, p := range r.Pillars {
			if !slices.Contains(model.Pillars, p) || slices.Contains(r.Pillars[:i], p) {
				t.Errorf("%s: bad or repeated pillar %q", r.Code, p)
			}
		}
	}
	for code := range rulePillars {
		if _, ok := ruleByCode[code]; !ok {
			t.Errorf("rulePillars names unknown rule %s", code)
		}
	}
}

func TestEphemeralData(t *testing.T) {
	db := func(kind, extra string) string {
		return strings.NewReplacer("Deployment", kind, "nginx:1.27", "postgres:17-alpine").Replace(deployment("db", "  replicas: 1", extra))
	}
	claim := "      volumes: [{name: data, persistentVolumeClaim: {claimName: data}}]"
	pvc := "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: data, namespace: shop}\nspec: {storageClassName: local-path, accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}\n"
	templates := strings.Replace(db("StatefulSet", ""), "  replicas: 1", "  replicas: 1\n  serviceName: db\n  volumeClaimTemplates:\n    - metadata: {name: data}\n      spec: {storageClassName: local-path, accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}", 1)

	for name, tc := range map[string]struct {
		files map[string]string
		want  int
	}{
		"deployment without claim":  {docs(db("Deployment", "")), 1},
		"statefulset without claim": {docs(db("StatefulSet", "      volumes: [{name: data, emptyDir: {}}]")), 1},
		"deployment with claim":     {docs(db("Deployment", claim), pvc), 0},
		"statefulset with template": {docs(templates), 0},
		"not a database":            {docs(deployment("web", "  replicas: 1", "")), 0},
		"cache":                     {docs(strings.Replace(deployment("cache", "  replicas: 1", ""), "nginx:1.27", "redis:7.4-alpine", 1)), 0},
	} {
		got := only(checkFiles(t, "", tc.files), "k8s_ephemeral_data")
		if len(got) != tc.want {
			t.Errorf("%s: %d findings, want %d: %v", name, len(got), tc.want, got)
			continue
		}
		if tc.want == 1 && (got[0].Severity != model.SeverityWarning || !strings.HasSuffix(got[0].Attribute, "containers[0].image") || !strings.Contains(got[0].Message, "postgres:17-alpine")) {
			t.Errorf("%s: %+v", name, got[0])
		}
	}
}

func TestAutoscalingOptIn(t *testing.T) {
	requests := "      containers:\n        - name: app\n          image: nginx:1.27\n          resources: {requests: {cpu: 100m, memory: 128Mi}}\n"
	sized := func(name, replicas string) string {
		return strings.Replace(deployment(name, "  replicas: "+replicas, ""), "      containers:\n        - name: app\n          image: nginx:1.27\n", requests, 1)
	}
	on := model.BestPracticeSettings{EnabledRules: []string{"k8s_no_hpa"}}
	files := docs(sized("web", "3"))

	if hasCode(FilterSettings(checkFiles(t, "prod", files), model.BestPracticeSettings{}), "k8s_no_hpa") {
		t.Error("k8s_no_hpa reported without opting in")
	}
	if hasCode(FilterSettings(checkFiles(t, "dev", files), on), "k8s_no_hpa") {
		t.Error("k8s_no_hpa reported in dev")
	}
	got := only(FilterSettings(checkFiles(t, "prod", files), on), "k8s_no_hpa")
	if len(got) != 1 || got[0].Severity != model.SeverityInfo || got[0].Attribute != "spec.replicas" || !strings.Contains(got[0].Fix, "maxReplicas: 6") {
		t.Fatalf("no_hpa: %v", got)
	}
	for name, f := range map[string]map[string]string{
		"has an HPA":     docs(sized("web", "3"), hpa("web", 3)),
		"single replica": docs(sized("web", "1")),
		"no requests":    docs(deployment("web", "  replicas: 3", "")),
		"recreate":       docs(strings.Replace(sized("web", "3"), "  replicas: 3", "  replicas: 3\n  strategy: {type: Recreate}", 1)),
	} {
		if hasCode(FilterSettings(checkFiles(t, "prod", f), on), "k8s_no_hpa") {
			t.Errorf("%s: k8s_no_hpa reported", name)
		}
	}
}

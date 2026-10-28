package compliance_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func writeDesign(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func lookup(t *testing.T, id string) compliance.Framework {
	t.Helper()
	f, ok := compliance.Lookup(id)
	if !ok {
		t.Fatalf("%s is not registered", id)
	}
	return f
}

// wantStatuses checks the status of controls: "met", "manual", … or
// "violated a,b" with the addresses of the failing resources.
func wantStatuses(t *testing.T, r compliance.Result, want map[string]string) {
	t.Helper()
	for id, status := range want {
		i := slices.IndexFunc(r.Controls, func(c compliance.ControlResult) bool { return c.ID == id })
		if i < 0 {
			t.Errorf("no control %s", id)
			continue
		}
		c := r.Controls[i]
		got := string(c.Status)
		if c.Status == compliance.StatusViolated {
			var failing []string
			for _, res := range c.Resources {
				if res.Failing {
					failing = append(failing, res.Address)
				}
			}
			got += " " + strings.Join(failing, ",")
		}
		if got != status {
			t.Errorf("%s %s: %q, want %q (%s; findings %+v)", r.Framework.ID, id, got, status, c.Reason, c.Findings)
		}
	}
}

// The CIS Docker pack grades a compose design through the real dk_* rules,
// opt-in ones included, on its services only.
func TestCISDockerGradesADesign(t *testing.T) {
	dir := writeDesign(t, map[string]string{"compose.yaml": `services:
  web:
    image: nginx:1.27
    ports: ["80:80"]
    cap_add: [SYS_ADMIN]
    read_only: true
    security_opt: ["no-new-privileges:true"]
    pids_limit: 100
  db:
    image: postgres:16
    cap_drop: [ALL]
    volumes: ["db-data:/var/lib/postgresql/data"]
volumes:
  db-data:
`})
	p := compose.Open(context.Background(), dir, []string{"compose.yaml"}, nil, compose.Options{})
	if p.Model == nil {
		t.Fatal(p.Diagnostics)
	}
	wf := &model.Workflow{Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Files: []string{"compose.yaml"}, Graph: p.Graph(nil)}}
	f := lookup(t, "cis-docker-1.8")
	cfg := model.BestPracticeSettings{}
	diags := compose.FilterSettings(slices.Clone(p.Diagnostics), compliance.GradingSettings(cfg, f))
	r := compliance.Evaluate(f, wf, diags, cfg, nil)
	wantStatuses(t, r, map[string]string{
		"1.1":  "manual",
		"4.1":  "met", // nothing built here
		"5.4":  "violated web",
		"5.5":  "met",
		"5.8":  "violated web",
		"5.13": "violated db",
		"5.14": "violated web",
		"5.22": "met",
		"5.26": "violated db",
		"5.29": "violated db",
		"5.32": "met",
		"5.7":  "manual",
	})
	// Service controls count services, not the volume.
	if c := r.Controls[slices.IndexFunc(r.Controls, func(c compliance.ControlResult) bool { return c.ID == "5.13" })]; len(c.Resources) != 2 || c.Reason != "1 of 2 resources have findings." {
		t.Errorf("5.13 resources %+v (%s)", c.Resources, c.Reason)
	}

	// A rule the user turned off leaves its control ungraded.
	off := model.BestPracticeSettings{DisabledRules: []string{"dk_read_only"}}
	r = compliance.Evaluate(f, wf, compose.FilterSettings(slices.Clone(p.Diagnostics), compliance.GradingSettings(off, f)), off, nil)
	wantStatuses(t, r, map[string]string{"5.13": "not_evaluated", "5.26": "violated db"})
}

// The CIS Kubernetes pack grades the rendered objects through the real
// k8s_* rules: workloads, Roles and bindings each on their own controls.
func TestCISKubernetesGradesADesign(t *testing.T) {
	dir := writeDesign(t, map[string]string{"k8s/app.yaml": `apiVersion: v1
kind: Namespace
metadata: {name: shop}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      hostNetwork: true
      automountServiceAccountToken: false
      securityContext: {runAsNonRoot: true, runAsUser: 10001, seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: api
          image: nginx:1.27
          securityContext: {capabilities: {drop: [ALL]}, allowPrivilegeEscalation: false}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: shop}
spec:
  selector: {matchLabels: {app: web}}
  template:
    metadata: {labels: {app: web}}
    spec:
      containers:
        - name: web
          image: nginx:1.27
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: reader, namespace: shop}
rules:
  - apiGroups: [""]
    resources: [secrets]
    verbs: [list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: api-admin}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}
subjects:
  - {kind: ServiceAccount, name: api, namespace: shop}
`})
	wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}}}}
	p := kube.Open(context.Background(), dir, wf, kube.Options{})
	wf.Kubernetes.Graph = p.Graph(nil)
	f := lookup(t, "cis-kubernetes-2.0")
	cfg := model.BestPracticeSettings{}
	r := compliance.Evaluate(f, wf, kuberules.Check(p, wf, compliance.GradingSettings(cfg, f)), cfg, nil)
	api, web := model.KubeNodeID("Deployment", "shop", "api"), model.KubeNodeID("Deployment", "shop", "web")
	wantStatuses(t, r, map[string]string{
		"1.2":    "manual",
		"5.1.1":  "violated " + model.KubeNodeID("ClusterRoleBinding", "", "api-admin"),
		"5.1.2":  "violated " + model.KubeNodeID("Role", "shop", "reader"),
		"5.1.3":  "met",
		"5.1.6":  "violated " + web, // mounts a token it doesn't use
		"5.1.7":  "met",
		"5.2.5":  "violated " + api, // hostNetwork
		"5.2.7":  "violated " + web,
		"5.2.9":  "violated " + web,
		"5.2.10": "met",
		"5.2.12": "met",
		"5.3.2":  "violated " + api + "," + web,
		"5.6.2":  "violated " + web,
		"5.6.4":  "met",
	})
}

// dk_privileged flags privileged, host networking and host PID: each counts
// only for its own control (network_mode: host is not a privileged container).
func TestCISDockerHostPrivilegesCountPerSetting(t *testing.T) {
	dir := writeDesign(t, map[string]string{"compose.yaml": `services:
  net:
    image: nginx:1.27
    network_mode: host
  pids:
    image: alpine:3.20
    pid: host
  priv:
    image: alpine:3.20
    privileged: true
`})
	p := compose.Open(context.Background(), dir, []string{"compose.yaml"}, nil, compose.Options{})
	if p.Model == nil {
		t.Fatal(p.Diagnostics)
	}
	wf := &model.Workflow{Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Files: []string{"compose.yaml"}, Graph: p.Graph(nil)}}
	f := lookup(t, "cis-docker-1.8")
	r := compliance.Evaluate(f, wf, compose.FilterSettings(slices.Clone(p.Diagnostics), compliance.GradingSettings(model.BestPracticeSettings{}, f)), model.BestPracticeSettings{}, nil)
	wantStatuses(t, r, map[string]string{"5.5": "violated priv", "5.10": "violated net", "5.16": "violated pids"})
}

// k8s_privileged and k8s_host_namespaces flag several settings: each counts
// only for its own control, and 5.6.3 grades security contexts, not tokens.
func TestCISKubernetesSharedRulesCountPerSetting(t *testing.T) {
	deploy := func(name, pod, container string) string {
		return `---
apiVersion: apps/v1
kind: Deployment
metadata: {name: ` + name + `, namespace: shop}
spec:
  selector: {matchLabels: {app: ` + name + `}}
  template:
    metadata: {labels: {app: ` + name + `}}
    spec:
      ` + pod + `
      containers:
        - name: app
          image: nginx:1.27
          securityContext: ` + container + `
`
	}
	hardened := `{allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities: {drop: [ALL]}}`
	dir := writeDesign(t, map[string]string{"k8s/app.yaml": "apiVersion: v1\nkind: Namespace\nmetadata: {name: shop}\n" +
		deploy("a", "hostPID: true", "{}") +
		deploy("b", "hostNetwork: true", "{}") +
		deploy("c", "automountServiceAccountToken: false", "{allowPrivilegeEscalation: true}") +
		deploy("d", "automountServiceAccountToken: false", "{privileged: true}") +
		// Hardened, but the API token it doesn't use is mounted.
		deploy("e", "securityContext: {runAsNonRoot: true, runAsUser: 10001, seccompProfile: {type: RuntimeDefault}}", hardened)})
	wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}}}}
	p := kube.Open(context.Background(), dir, wf, kube.Options{})
	wf.Kubernetes.Graph = p.Graph(nil)
	f := lookup(t, "cis-kubernetes-2.0")
	r := compliance.Evaluate(f, wf, kuberules.Check(p, wf, compliance.GradingSettings(model.BestPracticeSettings{}, f)), model.BestPracticeSettings{}, nil)
	id := func(name string) string { return model.KubeNodeID("Deployment", "shop", name) }
	wantStatuses(t, r, map[string]string{
		"5.2.2": "violated " + id("d"),
		"5.2.3": "violated " + id("a"),
		"5.2.4": "met",
		"5.2.5": "violated " + id("b"),
		"5.2.6": "violated " + id("c"),
		"5.6.3": "violated " + strings.Join([]string{id("a"), id("b"), id("c"), id("d")}, ","),
		"5.1.6": "violated " + strings.Join([]string{id("a"), id("b"), id("e")}, ","),
	})
}

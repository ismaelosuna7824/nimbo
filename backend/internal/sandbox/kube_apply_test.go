package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func obj(apiVersion, kind, name string, extra ...map[string]any) map[string]any {
	o := map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": map[string]any{"name": name}}
	for _, e := range extra {
		for k, v := range e {
			o[k] = v
		}
	}
	return o
}

func kinds(objs []map[string]any) []string {
	var out []string
	for _, o := range objs {
		out = append(out, str(o, "kind"))
	}
	return out
}

func TestSortForApply(t *testing.T) {
	objs := []map[string]any{
		obj("networking.k8s.io/v1", "Ingress", "web"),
		obj("cert-manager.io/v1", "Certificate", "tls"),
		obj("apps/v1", "Deployment", "web"),
		obj("v1", "Service", "web"),
		obj("v1", "ConfigMap", "cfg"),
		obj("apiextensions.k8s.io/v1", "CustomResourceDefinition", "certificates.cert-manager.io"),
		obj("v1", "Secret", "s"),
		obj("v1", "Namespace", "shop"),
		obj("apps/v1", "Deployment", "api"), // stable after web
		obj("autoscaling/v2", "HorizontalPodAutoscaler", "web"),
		obj("v1", "PersistentVolumeClaim", "data"),
		obj("v1", "ServiceAccount", "sa"),
	}
	sortForApply(objs)
	want := []string{"Namespace", "CustomResourceDefinition", "ServiceAccount", "Secret", "ConfigMap", "PersistentVolumeClaim",
		"Service", "Deployment", "Deployment", "HorizontalPodAutoscaler", "Ingress", "Certificate"}
	if got := kinds(objs); !slices.Equal(got, want) {
		t.Fatalf("order = %v\nwant    %v", got, want)
	}
	if n, _ := objMeta(objs[7]); n != "web" {
		t.Errorf("deployments not stable: %v", objs[7])
	}

	refs := []kubeRef{{Kind: "Namespace", Name: "shop"}, {Kind: "Deployment", Name: "web"}, {Kind: "Service", Name: "web"}, {Kind: "Certificate", Name: "x"}}
	sortRefsForDelete(refs)
	var got []string
	for _, r := range refs {
		got = append(got, r.Kind)
	}
	if !slices.Equal(got, []string{"Certificate", "Deployment", "Service", "Namespace"}) {
		t.Errorf("delete order = %v", got)
	}
}

func TestPrepareObjects(t *testing.T) {
	deploy := obj("apps/v1", "Deployment", "api", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"initContainers": []any{map[string]any{"name": "migrate", "image": "api:dev"}},
		"containers": []any{
			map[string]any{"name": "api", "image": "api:dev", "imagePullPolicy": "Always"},
			map[string]any{"name": "sidecar", "image": "nginx:1.27"},
			map[string]any{"name": "worker", "image": "docker.io/library/worker", "imagePullPolicy": "Never"},
		},
	}}}})
	cron := obj("batch/v1", "CronJob", "nightly", map[string]any{"spec": map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": "job", "image": "worker"}},
	}}}}}})
	ns := obj("v1", "Namespace", "shop")
	role := obj("rbac.authorization.k8s.io/v1", "ClusterRole", "reader", map[string]any{"metadata": map[string]any{"name": "reader", "namespace": "stray"}})
	pinned := obj("v1", "ConfigMap", "cfg", map[string]any{"metadata": map[string]any{"name": "cfg", "namespace": "other"}})
	src := KubeApplySource{Path: "k8s", Namespace: "shop", Objects: []map[string]any{deploy, cron, ns, role, pinned}}
	built := map[string]bool{normalizeImage("api:dev"): true, normalizeImage("worker"): true}
	scoped := func(_, kind string) (bool, error) { return kind != "Namespace" && kind != "ClusterRole", nil }

	out, notes, err := prepareObjects(src, built, scoped)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(out); !slices.Equal(got, []string{"Namespace", "ConfigMap", "ClusterRole", "Deployment", "CronJob"}) {
		t.Fatalf("kinds = %v", got)
	}
	byKind := map[string]map[string]any{}
	for _, o := range out {
		byKind[str(o, "kind")] = o
	}
	nsOf := func(kind string) string { _, n := objMeta(byKind[kind]); return n }
	if nsOf("Deployment") != "shop" || nsOf("ConfigMap") != "other" || nsOf("ClusterRole") != "" || nsOf("Namespace") != "" {
		t.Errorf("namespaces: deploy %q cm %q role %q ns %q", nsOf("Deployment"), nsOf("ConfigMap"), nsOf("ClusterRole"), nsOf("Namespace"))
	}
	spec := podSpecs(byKind["Deployment"])[0]
	policy := func(key string, i int) string { return str(spec[key].([]any)[i].(map[string]any), "imagePullPolicy") }
	if policy("initContainers", 0) != "IfNotPresent" || policy("containers", 0) != "IfNotPresent" || policy("containers", 1) != "" || policy("containers", 2) != "Never" {
		t.Errorf("pull policies: %v", spec)
	}
	if p := str(podSpecs(byKind["CronJob"])[0]["containers"].([]any)[0].(map[string]any), "imagePullPolicy"); p != "IfNotPresent" {
		t.Errorf("cronjob policy %q", p)
	}
	if len(notes) != 3 {
		t.Errorf("notes = %v", notes)
	}
	// The source objects are not modified.
	if _, n := objMeta(deploy); n != "" || str(deploy["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any), "imagePullPolicy") != "Always" {
		t.Errorf("source mutated: %v", deploy)
	}
	if imgs := KubeObjectImages(deploy); !slices.Equal(imgs, []string{"api:dev", "api:dev", "nginx:1.27", "docker.io/library/worker"}) {
		t.Errorf("images = %v", imgs)
	}

	if _, _, err := prepareObjects(KubeApplySource{Path: "k8s", Objects: []map[string]any{{"kind": "Service"}}}, nil, scoped); err == nil {
		t.Error("object without apiVersion accepted")
	}
	if _, _, err := prepareObjects(KubeApplySource{Path: "k8s", Objects: []map[string]any{obj("v1", "Service", "")}}, nil, scoped); err == nil {
		t.Error("object without name accepted")
	}
	def, _, _ := prepareObjects(KubeApplySource{Objects: []map[string]any{obj("v1", "Service", "web")}}, nil, scoped)
	if _, n := objMeta(def[0]); n != "default" {
		t.Errorf("default namespace = %q", n)
	}
}

func TestNormalizeImageAndNodeIDs(t *testing.T) {
	for in, want := range map[string]string{
		"api": "api:latest", "api:dev": "api:dev", "docker.io/library/api": "api:latest", "library/api:1": "api:1",
		"ghcr.io/acme/api:1.2": "ghcr.io/acme/api:1.2", "localhost:5000/api": "localhost:5000/api:latest", "api@sha256:abc": "api:latest",
	} {
		if got := normalizeImage(in); got != want {
			t.Errorf("normalizeImage(%q) = %q, want %q", in, got, want)
		}
	}
	if id := kubeNodeID("apps/v1", "Deployment", "default", "web"); id != "Deployment/default/web" {
		t.Errorf("deployment id %q", id)
	}
	if id := kubeNodeID("cert-manager.io/v1", "Certificate", "default", "tls"); id != "Certificate.cert-manager.io/default/tls" {
		t.Errorf("custom id %q", id)
	}
	if id := kubeNodeID("rbac.authorization.k8s.io/v1", "ClusterRole", "", "admin"); id != "ClusterRole/admin" {
		t.Errorf("cluster-scoped id %q", id)
	}
	if k := (kubeRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "a", Name: "b"}).key(); k != (kubeRef{APIVersion: "apps/v1beta2", Kind: "Deployment", Namespace: "a", Name: "b"}).key() {
		t.Errorf("inventory key depends on the version: %s", k)
	}
}

func TestKubeBuildPlan(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"api/Dockerfile", "services/worker/Dockerfile", "web/Dockerfile", ".hidden/api/Dockerfile", "node_modules/x/Dockerfile", "a/b/c/d/deep/Dockerfile"} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	explicit := []model.KubeBuild{{Image: "web:dev", Context: "web", Target: "prod"}}
	images := []string{"api:dev", "worker", "web:dev", "nginx:1.27", "ghcr.io/acme/api:1", "x", "deep:1"}
	plan := KubeBuildPlan(dir, explicit, images)
	got := map[string]string{}
	for _, b := range plan {
		got[b.Image] = b.Context + "|" + b.Target
	}
	want := map[string]string{"api:dev": "api|", "web:dev": "web|prod", "worker": "services/worker|"}
	if len(got) != len(want) {
		t.Fatalf("plan = %+v", plan)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if _, err := safeRel("../outside"); err == nil {
		t.Error("escaping build context accepted")
	}
	if p, err := safeRel(""); err != nil || p != "." {
		t.Errorf("empty context = %q, %v", p, err)
	}
}

func TestHelmPullPolicyRenderer(t *testing.T) {
	in := bytes.NewBufferString(`---
# Source: hello/templates/cm.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cfg
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hello
spec:
  template:
    spec:
      containers:
        - name: hello
          image: hello:dev
`)
	var notes []string
	out, err := pullPolicyRenderer{built: map[string]bool{"hello:dev": true}, notes: &notes}.Run(in)
	if err != nil {
		t.Fatal(err)
	}
	docs := splitYAMLDocs(out.String())
	if len(docs) != 2 || !strings.Contains(docs[0], "# Source: hello/templates/cm.yaml") || !strings.Contains(docs[1], "imagePullPolicy: IfNotPresent") || len(notes) != 1 {
		t.Fatalf("rendered:\n%s\nnotes %v", out, notes)
	}
}

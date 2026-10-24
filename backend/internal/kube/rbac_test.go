package kube

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// rbacProject writes files into a temp dir with one source.
func rbacProject(t *testing.T, src model.KubeSource, files map[string]string) (string, *model.Workflow) {
	t.Helper()
	dir := t.TempDir()
	data := map[string][]byte{}
	for k, v := range files {
		data[k] = []byte(v)
	}
	writeFiles(t, dir, data)
	return dir, &model.Workflow{ID: "rbac", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{src}}}
}

// rbacEdit runs fn on a fresh render and writes the edited files.
func rbacEdit(t *testing.T, dir string, wf *model.Workflow, fn func(p *Project, e *Editor) error) error {
	t.Helper()
	p := render(t, dir, wf, "")
	e := p.Edit()
	if err := fn(p, e); err != nil {
		return err
	}
	writeFiles(t, dir, e.Files())
	return nil
}

func rbacView(t *testing.T, dir string, wf *model.Workflow, id string) *model.KubeAccessView {
	t.Helper()
	p := render(t, dir, wf, "")
	v, err := p.RBACView(object(t, p, id))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func warningCodes(v *model.KubeAccessView) []string {
	var out []string
	for _, w := range v.Warnings {
		out = append(out, w.Code+"@"+w.Path)
	}
	return out
}

const rbacRole = `# Reader of the app's settings.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: reader
  namespace: shop
rules:
  # Settings and credentials.
  - apiGroups: [""]
    resources: [configmaps, secrets] # both
    verbs: [get, list, watch]
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
  - apiGroups: [apps]
    resources: [deployments]
    resourceNames: [web]
    verbs: [get]
`

func TestRBACRoleRoundTrip(t *testing.T) {
	dir, wf := rbacProject(t, model.KubeSource{Kind: model.KubeManifests, Path: "k8s"}, map[string]string{"k8s/role.yaml": rbacRole})
	v := rbacView(t, dir, wf, "Role/shop/reader")
	if !v.Editable || len(v.Rules) != 3 || !slices.Equal(v.Rules[0].APIGroups, []string{""}) || !slices.Equal(v.Rules[2].ResourceNames, []string{"web"}) {
		t.Fatalf("view %+v", v)
	}
	if got := warningCodes(v); !slices.Equal(got, []string{"k8s_secrets_access@rules/0", "k8s_wildcard@rules/1", "k8s_secrets_access@rules/1"}) {
		t.Fatalf("warnings %v", got)
	}

	// Unchanged rules: nothing written.
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		if err := e.ApplyRBAC(object(t, p, "Role/shop/reader"), model.KubeAccessEdit{Rules: v.Rules}); err != nil {
			return err
		}
		if len(e.Files()) != 0 {
			t.Errorf("an unchanged edit wrote %v", e.Files())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Drop the wildcard rule, narrow the first, add one.
	rules := []model.KubeRule{
		{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get", "list", "watch"}},
		v.Rules[2],
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
	}
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		return e.ApplyRBAC(object(t, p, "Role/shop/reader"), model.KubeAccessEdit{Rules: rules})
	}); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, "k8s/role.yaml")
	want := `# Reader of the app's settings.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: reader
  namespace: shop
rules:
  # Settings and credentials.
  - apiGroups: [""]
    resources: [configmaps] # both
    verbs: [get, list, watch]
  - apiGroups: [apps]
    resources: [deployments]
    resourceNames: [web]
    verbs: [get]
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list]
`
	if got != want {
		t.Fatalf("role written as:\n%s\nwant:\n%s\n(first diff: %s)", got, want, firstDiff(got, want))
	}
	v = rbacView(t, dir, wf, "Role/shop/reader")
	if len(v.Rules) != 3 || !slices.Equal(v.Rules[2].Resources, []string{"pods"}) || len(v.Warnings) != 0 {
		t.Fatalf("view after edit %+v", v)
	}

	// Invalid rules are refused.
	for _, bad := range []model.KubeRule{
		{APIGroups: []string{""}, Resources: []string{"pods"}},
		{APIGroups: []string{}, Resources: []string{"pods"}, Verbs: []string{"get"}},
		{APIGroups: []string{""}, Resources: []string{"pods"}, ResourceNames: []string{"a"}, Verbs: []string{"create"}},
		{NonResourceURLs: []string{"/healthz"}, Verbs: []string{"get"}},
	} {
		if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
			return e.ApplyRBAC(object(t, p, "Role/shop/reader"), model.KubeAccessEdit{Rules: []model.KubeRule{bad}})
		}); err == nil {
			t.Errorf("rule %+v accepted", bad)
		}
	}
}

const rbacBindings = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: reporter
  namespace: shop
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: reporter-admin
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
  - kind: ServiceAccount
    name: reporter
    namespace: shop
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: reader
  namespace: shop
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: reader # the Role
subjects:
  - kind: ServiceAccount
    name: reporter
    namespace: shop
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: report
  namespace: shop
spec:
  schedule: "0 2 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: reporter
          restartPolicy: OnFailure
          containers:
            - name: report
              image: kubectl:1
`

func TestRBACBindingsAndIdentity(t *testing.T) {
	dir, wf := rbacProject(t, model.KubeSource{Kind: model.KubeManifests, Path: "k8s"}, map[string]string{"k8s/role.yaml": rbacRole, "k8s/rbac.yaml": rbacBindings})
	crb := rbacView(t, dir, wf, "ClusterRoleBinding/reporter-admin")
	if crb.RoleRef == nil || crb.RoleRef.Name != "cluster-admin" || len(crb.Subjects) != 1 {
		t.Fatalf("binding view %+v", crb)
	}
	if got := warningCodes(crb); !slices.Equal(got, []string{"k8s_cluster_admin@roleRef", "k8s_cluster_scope@subjects/0"}) {
		t.Fatalf("binding warnings %v", got)
	}
	if !strings.Contains(crb.Warnings[1].Message, "used by report") {
		t.Errorf("cluster scope message %q", crb.Warnings[1].Message)
	}
	job := rbacView(t, dir, wf, "CronJob/shop/report")
	if job.ServiceAccount != "reporter" || !slices.ContainsFunc(job.Related, func(r model.AccessRelated) bool { return r.NodeID == "Role/shop/reader" }) {
		t.Fatalf("workload view %+v", job)
	}
	if got := warningCodes(job); !slices.Contains(got, "k8s_cluster_admin@") || !slices.Contains(got, "k8s_cluster_scope@") {
		t.Fatalf("workload warnings %v", got)
	}

	// Bindings: roleRef and subjects edited in place.
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		return e.ApplyRBAC(object(t, p, "RoleBinding/shop/reader"), model.KubeAccessEdit{
			RoleRef:  &model.KubeRoleRef{Kind: "ClusterRole", Name: "view"},
			Subjects: []model.KubeSubject{{Kind: "ServiceAccount", Name: "reporter", Namespace: "shop"}, {Kind: "Group", Name: "auditors"}},
		})
	}); err != nil {
		t.Fatal(err)
	}
	file := read(t, dir, "k8s/rbac.yaml")
	if !strings.Contains(file, "  kind: ClusterRole\n  name: view # the Role\n") ||
		!strings.Contains(file, "    namespace: shop\n  - kind: Group\n    name: auditors\n    apiGroup: rbac.authorization.k8s.io\n---\napiVersion: batch/v1") {
		t.Fatalf("binding written as:\n%s", file)
	}
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		return e.ApplyRBAC(object(t, p, "ClusterRoleBinding/reporter-admin"), model.KubeAccessEdit{RoleRef: &model.KubeRoleRef{Kind: "Role", Name: "x"}})
	}); err == nil {
		t.Error("a ClusterRoleBinding took a Role")
	}

	// Workload identity: its own ServiceAccount (created) and no token.
	off := false
	api := "api"
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		return e.ApplyRBAC(object(t, p, "CronJob/shop/report"), model.KubeAccessEdit{ServiceAccount: &api, AutomountToken: &off})
	}); err != nil {
		t.Fatal(err)
	}
	file = read(t, dir, "k8s/rbac.yaml")
	if !strings.Contains(file, "          serviceAccountName: api\n") || !strings.Contains(file, "automountServiceAccountToken: false") {
		t.Fatalf("identity written as:\n%s", file)
	}
	if sa := read(t, dir, "k8s/serviceaccount-api.yaml"); sa != "apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: api\n  namespace: shop\n" {
		t.Fatalf("new ServiceAccount:\n%s", sa)
	}
	job = rbacView(t, dir, wf, "CronJob/shop/report")
	if job.ServiceAccount != "api" || job.AutomountToken == nil || *job.AutomountToken || len(job.Warnings) != 0 {
		t.Fatalf("workload after edit %+v", job)
	}
	// The ServiceAccount's token automount.
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		return e.ApplyRBAC(object(t, p, "ServiceAccount/shop/api"), model.KubeAccessEdit{AutomountToken: &off})
	}); err != nil {
		t.Fatal(err)
	}
	if sa := read(t, dir, "k8s/serviceaccount-api.yaml"); !strings.HasSuffix(sa, "automountServiceAccountToken: false\n") {
		t.Fatalf("ServiceAccount:\n%s", sa)
	}
}

func TestRBACWorkloadWarnings(t *testing.T) {
	deploy := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web, namespace: shop}\nspec:\n  selector: {matchLabels: {app: web}}\n  template:\n    metadata: {labels: {app: web}}\n    spec:\n      containers:\n        - {name: web, image: nginx}\n"
	dir, wf := rbacProject(t, model.KubeSource{Kind: model.KubeManifests, Path: "k8s"}, map[string]string{"k8s/web.yaml": deploy})
	if got := warningCodes(rbacView(t, dir, wf, "Deployment/shop/web")); !slices.Equal(got, []string{"k8s_token_automount@"}) {
		t.Fatalf("token warning %v", got)
	}
	grant := "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata: {name: default-view, namespace: shop}\nroleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: edit}\nsubjects:\n  - {kind: ServiceAccount, name: default}\n"
	writeFiles(t, dir, map[string][]byte{"k8s/grant.yaml": []byte(grant)})
	if got := warningCodes(rbacView(t, dir, wf, "Deployment/shop/web")); !slices.Equal(got, []string{"k8s_cluster_admin@", "k8s_default_sa@"}) {
		t.Fatalf("default SA warnings %v", got)
	}
}

func TestRBACReadOnly(t *testing.T) {
	// Kustomize: a patch changes the binding's roleRef, not the Role's rules.
	dir, wf := rbacProject(t, model.KubeSource{Kind: model.KubeKustomize, Path: "base"}, map[string]string{
		"base/kustomization.yaml": "resources:\n  - role.yaml\n  - binding.yaml\npatches:\n  - path: patch.yaml\n",
		"base/role.yaml":          "apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata: {name: reader, namespace: shop}\nrules:\n  - apiGroups: [\"\"]\n    resources: [pods]\n    verbs: [get]\n",
		"base/binding.yaml":       "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata: {name: reader, namespace: shop}\nroleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: reader}\nsubjects:\n  - {kind: ServiceAccount, name: app, namespace: shop}\n",
		"base/patch.yaml":         "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata: {name: reader, namespace: shop}\nroleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: other}\n",
	})
	if v := rbacView(t, dir, wf, "Role/shop/reader"); !v.Editable {
		t.Fatalf("kustomize role not editable: %s", v.Reason)
	}
	b := rbacView(t, dir, wf, "RoleBinding/shop/reader")
	if b.Editable || !strings.Contains(b.Reason, "kustomization changes roleRef") || b.RoleRef.Name != "other" {
		t.Fatalf("kustomize binding %+v", b)
	}
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		return e.ApplyRBAC(object(t, p, "RoleBinding/shop/reader"), model.KubeAccessEdit{Subjects: []model.KubeSubject{}})
	}); err == nil {
		t.Fatal("a read-only binding was edited")
	}
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		return e.ApplyRBAC(object(t, p, "Role/shop/reader"), model.KubeAccessEdit{Rules: []model.KubeRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}}}})
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "base/role.yaml"); !strings.HasSuffix(got, "    verbs: [get, list]\n") {
		t.Fatalf("kustomize role written as:\n%s", got)
	}

	// Helm: rendered from a template.
	dir, wf = rbacProject(t, model.KubeSource{Kind: model.KubeHelm, Path: "chart", Release: "app", Namespace: "shop"}, map[string]string{
		"chart/Chart.yaml":          "apiVersion: v2\nname: app\nversion: 0.1.0\n",
		"chart/values.yaml":         "verbs: [get]\n",
		"chart/templates/role.yaml": "apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata:\n  name: {{ .Release.Name }}-reader\nrules:\n  - apiGroups: [\"\"]\n    resources: [pods]\n    verbs: {{ toJson .Values.verbs }}\n",
	})
	h := rbacView(t, dir, wf, "Role/shop/app-reader")
	if h.Editable || !strings.Contains(h.Reason, "Helm chart") || len(h.Rules) != 1 {
		t.Fatalf("helm role %+v", h)
	}
}

func recipeIDs(rs []ConnectRecipe) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

const grantApp = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  selector:
    matchLabels: {app: web}
  template:
    metadata:
      labels: {app: web}
    spec:
      containers:
        - name: web
          image: nginx
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: shop
data: {a: "1"}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: flags
  namespace: shop
data: {b: "2"}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pod-reader
rules:
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list]
`

func TestRBACGrants(t *testing.T) {
	dir, wf := rbacProject(t, model.KubeSource{Kind: model.KubeManifests, Path: "k8s"}, map[string]string{"k8s/app.yaml": grantApp})
	p := render(t, dir, wf, "")
	rs, err := p.Recipes("Deployment/shop/web", "ConfigMap/shop/settings")
	if err != nil {
		t.Fatal(err)
	}
	if got := recipeIDs(rs); !slices.Equal(got, []string{"envFrom", "volume", "access:read", "access:write", "access:manage"}) {
		t.Fatalf("recipes %v", got)
	}
	read0 := rs[2]
	wantDesc := `Create ServiceAccount web; run web's pods as ServiceAccount web; create Role web-read-settings in shop allowing get, list, watch on configmaps "settings"; bind Role web-read-settings to ServiceAccount web with RoleBinding web-read-settings in shop.`
	if read0.Kind != RecipeAccess || read0.Level != model.AccessRead || read0.Label != "Read" || read0.Description != wantDesc {
		t.Fatalf("read recipe %+v", read0)
	}
	if rs[4].Description == "" || !strings.Contains(rs[4].Description, "get, list, watch, update, patch, delete on configmaps") {
		t.Fatalf("manage recipe %+v", rs[4])
	}
	// Object grants need the identity as the dragged end: the other way
	// round only wiring recipes are offered.
	if back, _ := p.Recipes("ConfigMap/shop/settings", "Deployment/shop/web"); slices.ContainsFunc(back, func(r ConnectRecipe) bool { return r.Kind == RecipeAccess }) {
		t.Fatalf("reverse recipes offer grants: %v", recipeIDs(back))
	}

	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		_, err := e.Connect(ConnectRequest{From: "Deployment/shop/web", To: "ConfigMap/shop/settings", RecipeID: "access:read"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "k8s/app.yaml"); !strings.Contains(got, "          image: nginx\n      serviceAccountName: web\n---") {
		t.Fatalf("serviceAccountName not set:\n%s", got)
	}
	wantFile := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: web
  namespace: shop
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: web-read-settings
  namespace: shop
rules:
  - apiGroups: [""]
    resources: [configmaps]
    resourceNames: [settings]
    verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: web-read-settings
  namespace: shop
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: web-read-settings
subjects:
  - kind: ServiceAccount
    name: web
    namespace: shop
`
	if got := read(t, dir, "k8s/serviceaccount-web.yaml"); got != wantFile {
		t.Fatalf("grant written as:\n%s\n(first diff: %s)", got, firstDiff(got, wantFile))
	}

	// A second grant extends the same Role.
	p = render(t, dir, wf, "")
	rs, err = p.Recipes("Deployment/shop/web", "ConfigMap/shop/flags")
	if err != nil {
		t.Fatal(err)
	}
	if d := rs[2].Description; d != `Add get, list, watch on configmaps "flags" to Role web-read-settings (already bound to ServiceAccount web).` {
		t.Fatalf("second grant %q", d)
	}
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		_, err := e.Connect(ConnectRequest{From: "Deployment/shop/web", To: "ConfigMap/shop/flags", RecipeID: "access:read"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "k8s/serviceaccount-web.yaml"); !strings.Contains(got, "    resourceNames: [settings, flags]\n") || strings.Count(got, "\nkind: Role\n") != 1 {
		t.Fatalf("second grant written as:\n%s", got)
	}
	// Write on settings: the verbs differ, a second rule.
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		_, err := e.Connect(ConnectRequest{From: "Deployment/shop/web", To: "ConfigMap/shop/settings", RecipeID: "access:write"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	v := rbacView(t, dir, wf, "Role/shop/web-read-settings")
	if len(v.Rules) != 2 || !slices.Equal(v.Rules[1].Verbs, []string{"get", "list", "watch", "update", "patch"}) {
		t.Fatalf("rules after write grant %+v", v.Rules)
	}

	// access:bind: a RoleBinding to the ClusterRole, in the workload's namespace.
	p = render(t, dir, wf, "")
	rs, err = p.Recipes("Deployment/shop/web", "ClusterRole/pod-reader")
	if err != nil || !slices.Equal(recipeIDs(rs), []string{"access:bind"}) || rs[0].Label != "Run with this role" {
		t.Fatalf("bind recipes %+v %v", rs, err)
	}
	if rs[0].Description != "Bind ClusterRole pod-reader to ServiceAccount web with RoleBinding web-pod-reader in shop (its rules apply in shop only)." {
		t.Fatalf("bind description %q", rs[0].Description)
	}
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		_, err := e.Connect(ConnectRequest{From: "Deployment/shop/web", To: "ClusterRole/pod-reader", RecipeID: "access:bind"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	b := rbacView(t, dir, wf, "RoleBinding/shop/web-pod-reader")
	if b.RoleRef == nil || *b.RoleRef != (model.KubeRoleRef{Kind: "ClusterRole", Name: "pod-reader"}) || len(b.Subjects) != 1 || b.Subjects[0].Name != "web" {
		t.Fatalf("bind result %+v", b)
	}
	// Bound already: no recipe left.
	if _, err := render(t, dir, wf, "").Recipes("Deployment/shop/web", "ClusterRole/pod-reader"); err == nil {
		t.Fatal("bind offered twice")
	}

	// access:role: an empty Role for a ServiceAccount, its rules picked later.
	p = render(t, dir, wf, "")
	rs, err = p.Recipes("ServiceAccount/shop/web", "")
	if err != nil || !slices.Equal(recipeIDs(rs), []string{"access:role"}) {
		t.Fatalf("role recipes %+v %v", rs, err)
	}
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		_, err := e.Connect(ConnectRequest{From: "ServiceAccount/shop/web", RecipeID: "access:role"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if v := rbacView(t, dir, wf, "Role/shop/web"); !v.Editable || len(v.Rules) != 0 {
		t.Fatalf("empty role %+v", v)
	}
}

func TestRBACCatalog(t *testing.T) {
	c := RBACCatalog("", "")
	if c.Source.URL == "" || c.Source.Retrieved == "" || len(c.Verbs) < 8 {
		t.Fatalf("catalog %+v", c.Source)
	}
	has := func(group, name string) bool {
		return slices.ContainsFunc(c.ResourceTypes, func(r model.AccessResourceType) bool { return r.Group == group && r.Name == name })
	}
	for _, r := range [][2]string{{"", "configmaps"}, {"", "secrets"}, {"apps", "deployments"}, {"", "pods/log"}, {"batch", "cronjobs"}, {rbacGroup, "roles"}} {
		if !has(r[0], r[1]) {
			t.Errorf("catalog misses %s/%s", r[0], r[1])
		}
	}
	if apps := RBACCatalog("apps", ""); slices.ContainsFunc(apps.ResourceTypes, func(r model.AccessResourceType) bool { return r.Group != "apps" }) {
		t.Error("group filter")
	}
}

package kube

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const netpolApps = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: bank
spec:
  selector:
    matchLabels: {app: web}
  template:
    metadata:
      labels: {app: web}
    spec:
      containers:
        - name: web
          image: nginx:1.27
          ports:
            - containerPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: bank
spec:
  selector:
    matchLabels: {app: api}
  template:
    metadata:
      labels: {app: api}
    spec:
      containers:
        - name: api
          image: api:1
          ports:
            - containerPort: 9898
            - containerPort: 9999
              protocol: UDP
---
# api's policy without policyTypes: implicitly Ingress only.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: api-in
  namespace: bank
spec:
  podSelector:
    matchLabels: {app: api}
  ingress:
    - from:
        - podSelector: {}
`

// TestNetpolRecipes: web → api writes api's ingress into its existing
// policy and creates web's policy for the egress; "anywhere" opens a
// workload's ports to every source.
func TestNetpolRecipes(t *testing.T) {
	dir, wf := rbacProject(t, model.KubeSource{Kind: model.KubeManifests, Path: "k8s"}, map[string]string{"k8s/apps.yaml": netpolApps})
	p := render(t, dir, wf, "")
	recipes, err := p.Recipes("Deployment/bank/web", "Deployment/bank/api")
	if err != nil {
		t.Fatal(err)
	}
	want := "NetworkPolicy api-in: api accepts connections from web on ports 9898, 9999/UDP. NetworkPolicy web: web may connect to api on the same ports."
	if len(recipes) == 0 || recipes[0].ID != RecipeAllowTraffic || recipes[0].Description != want {
		t.Fatalf("recipes %+v", recipes)
	}
	for _, req := range []ConnectRequest{
		{From: "Deployment/bank/web", To: "Deployment/bank/api", RecipeID: RecipeAllowTraffic},
		{From: "Deployment/bank/web", RecipeID: RecipeAllowAll},
		{From: "Deployment/bank/api", To: "Deployment/bank/web", RecipeID: RecipeAllowTraffic},
	} {
		if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
			_, err := e.Connect(req)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	p = render(t, dir, wf, "")
	api := object(t, p, "NetworkPolicy/bank/api-in")
	// The implicit Ingress type is kept when Egress is added.
	if got := list(dig(api.Obj, "spec", "policyTypes")); !slices.Equal(got, []any{"Ingress", "Egress"}) {
		t.Errorf("api policyTypes %v", got)
	}
	ingress := list(dig(api.Obj, "spec", "ingress"))
	if len(ingress) != 2 || str(list(dig(ingress[1], "from"))[0], "podSelector", "matchLabels", "app") != "web" || len(list(dig(ingress[1], "ports"))) != 2 {
		t.Errorf("api ingress %v", ingress)
	}
	web := object(t, p, "NetworkPolicy/bank/web")
	if got := list(dig(web.Obj, "spec", "policyTypes")); !slices.Equal(got, []any{"Egress", "Ingress"}) {
		t.Errorf("web policyTypes %v", got)
	}
	egress := list(dig(web.Obj, "spec", "egress"))
	if len(egress) != 1 || str(list(dig(egress[0], "to"))[0], "podSelector", "matchLabels", "app") != "api" {
		t.Errorf("web egress %v", egress)
	}
	in := list(dig(web.Obj, "spec", "ingress"))
	if len(in) != 2 || dig(in[0], "from") != nil || len(list(dig(in[0], "ports"))) != 1 || str(list(dig(in[1], "from"))[0], "podSelector", "matchLabels", "app") != "api" {
		t.Errorf("web ingress %v", in)
	}
	data, err := os.ReadFile(filepath.Join(dir, "k8s/apps.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# api's policy without policyTypes") {
		t.Errorf("comments lost:\n%s", data)
	}
	// A workload without declared ports can't be the target.
	if r := p.netpolRecipes(object(t, p, "Deployment/bank/web"), &Object{Kind: "Deployment", Obj: map[string]any{}}); r != nil {
		t.Errorf("recipes for a portless workload %+v", r)
	}
}

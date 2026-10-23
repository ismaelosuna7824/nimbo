package kubecatalog

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// check renders yaml as a manifests source and returns the render and
// rule findings.
func check(t *testing.T, yaml string) ([]model.Diagnostic, *kube.Project) {
	t.Helper()
	dir := writeFiles(t, map[string][]byte{"k8s/objects.yaml": []byte(yaml)})
	wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s", Namespace: "app"}}}}
	p, diags := kube.Derive(context.Background(), dir, wf)
	return slices.Concat(diags, kuberules.Check(p, wf, model.BestPracticeSettings{})), p
}

// referencing are starters that point at another object (a Service, a
// Deployment) the user connects afterwards.
var referencing = map[string][]string{
	"ingress":   {"k8s_ingress_backend"},
	"httproute": {"k8s_ingress_backend"},
	"hpa":       {"k8s_unknown_ref"},
	"pdb":       {"k8s_selector_mismatch"},
}

func TestStartersAreClean(t *testing.T) {
	ids := map[string]bool{}
	for _, it := range Items() {
		if ids[it.ID] {
			t.Errorf("duplicate id %s", it.ID)
		}
		ids[it.ID] = true
		if it.Label == "" || it.Description == "" || it.Category == "" || it.Icon == "" || len(it.Kinds) == 0 {
			t.Errorf("incomplete item %+v", it)
		}
	}
	for _, s := range starters {
		t.Run(s.ID, func(t *testing.T) {
			y, err := YAML(s.ID, "My App", "")
			if err != nil {
				t.Fatal(err)
			}
			diags, p := check(t, y)
			var kinds []string
			for _, o := range p.Objects {
				kinds = append(kinds, o.Kind)
				if !strings.HasPrefix(o.Name, "my-app") {
					t.Errorf("%s %s not named after the request", o.Kind, o.Name)
				}
			}
			if !slices.Equal(kinds, s.Kinds) && !(s.ID == "statefulset" && slices.Equal(kinds, []string{"Service", "StatefulSet"})) {
				t.Errorf("kinds %v, item says %v", kinds, s.Kinds)
			}
			for _, d := range diags {
				if d.Severity == model.SeverityInfo || slices.Contains(referencing[s.ID], d.Code) {
					continue
				}
				t.Errorf("%s %s: %s", d.Severity, d.Code, d.Message)
			}
		})
	}
}

func TestImagesAreClean(t *testing.T) {
	n, stateful := 0, 0
	for _, im := range compose.Images() {
		it, ok := imageItem(im)
		if !ok {
			continue
		}
		n++
		if it.Workload == "StatefulSet" {
			stateful++
		}
		y, err := YAML(it.ID, "", "")
		if err != nil {
			t.Fatal(err)
		}
		diags, p := check(t, y)
		if len(p.Objects) == 0 {
			t.Errorf("%s rendered nothing", it.ID)
		}
		for _, d := range diags {
			if d.Severity != model.SeverityInfo {
				t.Errorf("%s: %s %s %s", it.ID, d.Severity, d.Code, d.Message)
			}
		}
	}
	// Every runnable image of the Docker catalog is in the palette.
	if n < 150 || stateful < 20 {
		t.Errorf("%d images (%d StatefulSets)", n, stateful)
	}
	t.Logf("%d images: %d StatefulSets, %d Deployments; %d starters; %d charts", n, stateful, n-stateful, len(starters), len(charts))
}

func TestImageObjects(t *testing.T) {
	y, err := YAML("image:postgres", "orders-db", "shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kind: StatefulSet", "name: orders-db", "namespace: shop", "clusterIP: None", "volumeClaimTemplates:",
		"secretKeyRef:", "name: orders-db-secrets", "POSTGRES_PASSWORD", "readinessProbe:", "pg_isready -U ${POSTGRES_USER}",
		"requests:", "memory: 512Mi",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("missing %q in\n%s", want, y)
		}
	}
	if y, _ := YAML("image:nginx", "", ""); !strings.Contains(y, "kind: Deployment") || strings.Contains(y, "StatefulSet") {
		t.Errorf("nginx is a Deployment:\n%s", y)
	}
	if _, err := YAML("chart:postgresql", "", ""); err == nil {
		t.Error("charts have no YAML")
	}
	if _, err := YAML("nope", "", ""); err == nil {
		t.Error("unknown id accepted")
	}
}

func TestObjectName(t *testing.T) {
	for in, want := range map[string]string{"My API": "my-api", "  web_1 ": "web-1", "Ünïcode!!": "n-code", "": ""} {
		if got := ObjectName(in); got != want {
			t.Errorf("ObjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

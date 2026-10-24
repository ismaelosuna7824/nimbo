package api

import (
	"context"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/challenge"
	"github.com/ismaelosuna7824/nimbo/backend/internal/learn"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// shippedChallenge is the final definition of a content challenge.
func shippedChallenge(t *testing.T, id string) model.Challenge {
	t.Helper()
	fsys := learn.Migrations()
	names, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var out *model.Challenge
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatal(err)
		}
		m, err := learn.ParseMigration(name, data, challenge.Validate)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, op := range m.Ops {
			if op.Kind == "challenge" && op.ID == id && op.Challenge != nil {
				c := *op.Challenge
				c.ID = id
				out = &c
			}
		}
	}
	if out == nil {
		t.Fatalf("challenge %s not shipped", id)
	}
	return *out
}

// gradeStatic grades a challenge's static checks on a saved workflow.
func gradeStatic(t *testing.T, e *testEnv, c model.Challenge, wfID string) map[string]model.ChallengeGoalResult {
	t.Helper()
	wf, err := e.st.Get(wfID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := challenge.Analyze(context.Background(), nil, wf, e.st.AssetsDir(wfID), "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.ChallengeGoalResult{}
	for _, k := range c.Checks {
		if k.Type == model.CheckValidate || k.Type == model.CheckRulesClean || k.Type == model.CheckHas {
			out[k.ID] = challenge.GradeStatic(k, a)
		}
	}
	return out
}

// TestKubernetesRBACChallengeThroughEditors solves
// kubernetes-rbac-least-privilege with the canvas and inspector endpoints
// only (delete, grant recipes, RBAC editors): no file is written by hand.
func TestKubernetesRBACChallengeThroughEditors(t *testing.T) {
	e := newEnv(t)
	c := shippedChallenge(t, "kubernetes-rbac-least-privilege")
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/learn/challenges/"+c.ID+"/start", map[string]string{}, &wf)
	if failed := slices.ContainsFunc(slices.Collect(maps.Values(gradeStatic(t, e, c, wf.ID))), func(r model.ChallengeGoalResult) bool { return !r.Passed }); !failed {
		t.Fatal("the starter passes every check")
	}
	base := "/workflows/" + wf.ID + "/kubernetes/"

	// The ClusterRoleBinding is flagged, then deleted from the canvas.
	var crb model.KubeAccessView
	e.mustCall(http.StatusOK, "GET", base+"access?node="+url.QueryEscape("ClusterRoleBinding/config-reader-admin"), nil, &crb)
	if !slices.ContainsFunc(crb.Warnings, func(w model.AccessWarning) bool { return w.Code == "k8s_cluster_admin" }) ||
		!slices.ContainsFunc(crb.Warnings, func(w model.AccessWarning) bool { return w.Code == "k8s_cluster_scope" }) {
		t.Fatalf("cluster-admin binding warnings %+v", crb.Warnings)
	}
	e.mustCall(http.StatusOK, "DELETE", base+"objects?node="+url.QueryEscape("ClusterRoleBinding/config-reader-admin"), nil, &wf)

	// "Give it its own Role" on the ServiceAccount, then its rules.
	var recipes []map[string]any
	e.mustCall(http.StatusOK, "GET", base+"connect?from="+url.QueryEscape("ServiceAccount/reports/config-reader")+"&to=", nil, &recipes)
	if len(recipes) != 1 || recipes[0]["id"] != "access:role" || recipes[0]["kind"] != "access" {
		t.Fatalf("recipes %+v", recipes)
	}
	e.mustCall(http.StatusOK, "POST", base+"connect", map[string]any{"from": "ServiceAccount/reports/config-reader", "recipeId": "access:role"}, &wf)
	e.mustCall(http.StatusOK, "PUT", base+"access", model.KubeAccessEdit{NodeID: "Role/reports/config-reader", Rules: []model.KubeRule{
		{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get", "list", "watch"}},
	}}, &wf)

	// The web Deployment never calls the API: no token.
	var web model.KubeAccessView
	e.mustCall(http.StatusOK, "GET", base+"access?node="+url.QueryEscape("Deployment/reports/web"), nil, &web)
	if !slices.ContainsFunc(web.Warnings, func(w model.AccessWarning) bool { return w.Code == "k8s_token_automount" }) {
		t.Fatalf("web warnings %+v", web.Warnings)
	}
	off := false
	e.mustCall(http.StatusOK, "PUT", base+"access", model.KubeAccessEdit{NodeID: "Deployment/reports/web", AutomountToken: &off}, &wf)

	// The CronJob's view shows its permissions.
	var job model.KubeAccessView
	e.mustCall(http.StatusOK, "GET", base+"access?node="+url.QueryEscape("CronJob/reports/config-report"), nil, &job)
	if job.ServiceAccount != "config-reader" || len(job.Warnings) != 0 ||
		!slices.ContainsFunc(job.Related, func(r model.AccessRelated) bool {
			return r.NodeID == "Role/reports/config-reader" && strings.Contains(r.Label, "get, list, watch configmaps")
		}) {
		t.Fatalf("cronjob view %+v", job)
	}

	for id, r := range gradeStatic(t, e, c, wf.ID) {
		if !r.Passed {
			t.Errorf("check %s fails: %s", id, r.Detail)
		}
	}

	// Errors: unknown kinds and read-only fields.
	e.mustCall(http.StatusNotFound, "GET", base+"access?node="+url.QueryEscape("Namespace/reports"), nil, nil)
	e.mustCall(http.StatusBadRequest, "PUT", base+"access", model.KubeAccessEdit{NodeID: "Role/reports/config-reader", AutomountToken: &off}, nil)
	var cat model.AccessCatalog
	e.mustCall(http.StatusOK, "GET", "/access/catalog/kubernetes?service=core&q=configmaps", nil, &cat)
	if len(cat.ResourceTypes) != 1 || cat.ResourceTypes[0].Name != "configmaps" || cat.Source.Retrieved == "" {
		t.Fatalf("catalog %+v", cat)
	}
}

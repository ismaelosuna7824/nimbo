package scenario

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func composeWF(apiReplicas int, apiRestart string) *model.Workflow {
	svc := func(name, image, restart string, replicas int) model.ComposeNode {
		f := &model.ServiceFacts{Restart: restart}
		if image != "" {
			f.Image = &model.ImageRef{Ref: image}
		}
		if replicas > 0 {
			f.Replicas = new(replicas)
		}
		return model.ComposeNode{ID: model.ComposeServiceID(name), Kind: model.ComposeService, Name: name, Service: f}
	}
	return &model.Workflow{ID: "w", Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Graph: model.ComposeGraph{
		Nodes: []model.ComposeNode{
			svc("api", "", apiRestart, apiReplicas),
			svc("db", "postgres:16-alpine", "", 0),
			svc("cache", "redis:7-alpine", "", 0),
			svc("tools", "busybox:1.36", "", 0),
		},
		Edges: []model.ComposeEdge{
			{Source: "service:db", Target: "service:api", Kind: model.ComposeEdgeDependsOn},
			{Source: "service:cache", Target: "service:api", Kind: model.ComposeEdgeInferred},
		},
	}}}
}

func option(c model.ScenarioCatalog, id string) *model.ScenarioOption {
	for i := range c.Scenarios {
		if c.Scenarios[i].ID == id {
			return &c.Scenarios[i]
		}
	}
	return nil
}

func targetIDs(o *model.ScenarioOption) string {
	var out []string
	for _, t := range o.Targets {
		out = append(out, t.ID)
	}
	return strings.Join(out, ",")
}

func TestComposeCatalog(t *testing.T) {
	c := Catalog(composeWF(2, ""))
	if c.Kind != model.WorkflowDocker || len(c.Scenarios) != 5 {
		t.Fatalf("catalog %+v", c)
	}
	if got := targetIDs(option(c, model.ScenarioKillReplica)); got != "service:api" {
		t.Errorf("kill targets %q", got)
	}
	if got := targetIDs(option(c, model.ScenarioDependencyDown)); got != "service:cache,service:db" {
		t.Errorf("dependency targets %q", got)
	}
	if o := option(c, model.ScenarioDependencyLatency); targetIDs(o) != "service:cache,service:db" || len(o.Params) != 3 {
		t.Errorf("latency %+v", o)
	}
	if got := targetIDs(option(c, model.ScenarioCacheFlush)); got != "service:cache" {
		t.Errorf("cache targets %q", got)
	}
	if o := option(c, model.ScenarioTrafficSpike); o.NeedsTarget || len(o.Targets) != 0 {
		t.Errorf("spike %+v", o)
	}

	// One replica without a restart policy: nothing brings a crash back.
	c = Catalog(composeWF(1, ""))
	if option(c, model.ScenarioKillReplica) != nil || len(c.Unavailable) != 1 || c.Unavailable[0].ID != model.ScenarioKillReplica || c.Unavailable[0].Reason == "" {
		t.Errorf("single replica: %+v", c)
	}
	// A restart policy is enough.
	if o := option(Catalog(composeWF(1, "unless-stopped")), model.ScenarioKillReplica); o == nil || o.Targets[0].Detail != "1 replica, restart: unless-stopped" {
		t.Errorf("restart policy: %+v", o)
	}
}

func TestKubeCatalog(t *testing.T) {
	wf := &model.Workflow{ID: "k", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Environment: "prod", Graph: model.KubeGraph{
		Nodes: []model.KubeNode{
			{ID: "Deployment/shop/web", Kind: "Deployment", Name: "web", Namespace: "shop", Workload: &model.KubeWorkload{Replicas: new(2), Containers: []model.KubeContainer{{Image: "nginx:1.27"}}}},
			{ID: "StatefulSet/shop/redis", Kind: "StatefulSet", Name: "redis", Namespace: "shop", Workload: &model.KubeWorkload{Replicas: new(1), Containers: []model.KubeContainer{{Image: "valkey/valkey:8"}}}},
			{ID: "Service/shop/redis", Kind: "Service", Name: "redis", Namespace: "shop"},
			{ID: "CronJob/shop/report", Kind: "CronJob", Name: "report", Namespace: "shop", Workload: &model.KubeWorkload{}},
			{ID: "HorizontalPodAutoscaler/shop/web", Kind: "HorizontalPodAutoscaler", Scaler: &model.KubeScaler{Target: "Deployment/shop/web", MinReplicas: 2, MaxReplicas: 5}},
		},
		Edges: []model.KubeEdge{
			{Source: "Service/shop/redis", Target: "StatefulSet/shop/redis", Kind: model.KubeEdgeSelects},
			{Source: "Deployment/shop/web", Target: "Service/shop/redis", Kind: model.KubeEdgeInferred},
		},
	}}}
	c := Catalog(wf)
	if c.Kind != model.WorkflowKubernetes || c.Environment != "prod" {
		t.Fatalf("catalog %+v", c)
	}
	o := option(c, model.ScenarioKillReplica)
	if targetIDs(o) != "Deployment/shop/web,StatefulSet/shop/redis" || o.Targets[0].Detail != "Deployment, 2 replicas, HPA 2-5" {
		t.Errorf("kill %+v", o)
	}
	if got := targetIDs(option(c, model.ScenarioDependencyDown)); got != "StatefulSet/shop/redis" {
		t.Errorf("dependency %q", got)
	}
	if got := targetIDs(option(c, model.ScenarioCacheFlush)); got != "StatefulSet/shop/redis" {
		t.Errorf("cache %q", got)
	}
}

func TestTerraformHasNoScenarios(t *testing.T) {
	c := Catalog(&model.Workflow{ID: "tf"})
	if c.Kind != "" || len(c.Scenarios) != 0 {
		t.Errorf("%+v", c)
	}
	if err := Validate(&model.Workflow{ID: "tf"}, &model.ScenarioSpec{ID: model.ScenarioTrafficSpike, AtSec: 10, DurationSec: 10}, 60); err == nil {
		t.Error("terraform workflow accepted a scenario")
	}
}

func TestValidate(t *testing.T) {
	wf := composeWF(2, "")
	ok := model.ScenarioSpec{ID: model.ScenarioDependencyLatency, Target: "service:db", AtSec: 10, DurationSec: 20, Params: map[string]string{"delayMs": "300"}}
	for _, tc := range []struct {
		name string
		edit func(s *model.ScenarioSpec)
		want string
	}{
		{"valid", func(*model.ScenarioSpec) {}, ""},
		{"window ends exactly at the profile end", func(s *model.ScenarioSpec) { s.AtSec, s.DurationSec = 40, 20 }, ""},
		{"window past the profile", func(s *model.ScenarioSpec) { s.AtSec, s.DurationSec = 41, 20 }, "ends after the load profile"},
		{"no baseline", func(s *model.ScenarioSpec) { s.AtSec = MinAtSec - 1 }, "at least 5"},
		{"target not a dependency", func(s *model.ScenarioSpec) { s.Target = "service:api" }, "cannot target"},
		{"param out of range", func(s *model.ScenarioSpec) { s.Params["delayMs"] = "0" }, "from 1 to 10000"},
		{"unknown param", func(s *model.ScenarioSpec) { s.Params["foo"] = "1" }, "no param"},
		{"unknown scenario", func(s *model.ScenarioSpec) { s.ID = "meteor" }, "unknown scenario"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ok
			s.Params = map[string]string{"delayMs": "300"}
			tc.edit(&s)
			err := Validate(wf, &s, 60)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
		})
	}

	spike := model.ScenarioSpec{ID: model.ScenarioTrafficSpike, Target: "service:db", AtSec: 10}
	if err := Validate(wf, &spike, 60); err != nil || spike.Target != "" || spike.DurationSec != 30 {
		t.Errorf("spike defaults: %v %+v", err, spike)
	}
	kill := model.ScenarioSpec{ID: model.ScenarioKillReplica, Target: "service:api", AtSec: 10}
	if err := Validate(composeWF(1, ""), &kill, 60); err == nil || !strings.Contains(err.Error(), "does not apply") {
		t.Errorf("kill on single replica: %v", err)
	}
}

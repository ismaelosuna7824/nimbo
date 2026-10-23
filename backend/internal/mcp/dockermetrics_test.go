package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestGetDockerMetricsBeforeAnyRun(t *testing.T) {
	cs, _, base := sessionURL(t)
	body, _ := json.Marshal(map[string]any{"name": "perf", "kind": "docker", "templateId": "docker-node-postgres-redis"})
	resp, err := http.Post(base+"/api/v1/workflows", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var wf model.Workflow
	if err := json.NewDecoder(resp.Body).Decode(&wf); err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, err)
	}
	resp.Body.Close()

	var v dockerMetricsView
	mustTool(t, cs, "get_docker_metrics", map[string]any{"workflow": wf.ID}, &v)
	var names []string
	for _, s := range v.Services {
		names = append(names, s.Service+":"+s.State)
	}
	if strings.Join(names, ",") != "api:absent,cache:absent,db:absent" || v.Project != "nimbo-dk-"+wf.ID || len(v.Builds) != 0 || v.LastLoadTest != nil {
		t.Errorf("view %+v", v)
	}
	if !strings.Contains(v.Hint, "run the project (up)") {
		t.Errorf("hint %q", v.Hint)
	}
	mustTool(t, cs, "get_docker_metrics", map[string]any{"workflow": wf.ID, "service": "db"}, &v)
	if len(v.Services) != 1 || v.Services[0].Service != "db" {
		t.Errorf("service filter: %+v", v.Services)
	}
	if text, isErr := callTool(t, cs, "get_docker_metrics", map[string]any{"workflow": wf.ID, "service": "nope"}); !isErr || !strings.Contains(text, `service "nope" is not in perf`) {
		t.Errorf("unknown service: %s", text)
	}
	var tf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "tf"}, &tf)
	if text, isErr := callTool(t, cs, "get_docker_metrics", map[string]any{"workflow": tf.ID}); !isErr || !strings.Contains(text, "Terraform workflow") {
		t.Errorf("terraform workflow: %s", text)
	}
}

func TestViewLoadRunBottleneck(t *testing.T) {
	startup := int64(10365)
	run := model.LoadTestRun{
		ID: "lt-1", Status: model.LoadPassed, StartedAt: time.Now(),
		Spec: model.LoadTestSpec{Target: model.LoadTarget{Label: "GET /notes · api:3000"}, Profile: model.LoadProfile{Preset: "custom"}},
		Resources: &model.LoadResources{
			Service: "api",
			Verdict: "api reached 99% of its 0.25 CPU limit at 877 req/s — CPU-bound; raise limits or add replicas",
			Services: []model.LoadServiceResources{
				{Service: "api", CPULimit: 0.25, CPULimited: true, MemLimit: 256 << 20, MemLimited: true, PeakCPUOf: 1.173, PeakMem: 45 << 20, PeakMemOf: 0.176, StartupMs: &startup},
				{Service: "db", CPULimit: 14, PeakCPUOf: 0.021, PeakMem: 28 << 20},
			},
			Verdicts: []model.ResourceVerdict{
				{Service: "api", Kind: "cpu-bound", Bottleneck: true, Message: "api reached 99% of its 0.25 CPU limit at 877 req/s — CPU-bound; raise limits or add replicas"},
				{Service: "db", Kind: "ok", Message: "db at 3% CPU (no limit) — not the bottleneck"},
			},
		},
	}
	v := viewLoadRun(&run)
	if v.Bottleneck != run.Resources.Verdict || len(v.Containers) != 2 {
		t.Fatalf("view %+v", v)
	}
	if want := "api: peak CPU 117% of 0.25 CPU, peak memory 45 MiB (18% of 256 MiB), startup 10.4 s — api reached 99%"; !strings.HasPrefix(v.Containers[0], want) {
		t.Errorf("api line %q", v.Containers[0])
	}
	if want := "db: peak CPU 2% of the host's 14 CPUs, peak memory 28 MiB — db at 3% CPU"; !strings.HasPrefix(v.Containers[1], want) {
		t.Errorf("db line %q", v.Containers[1])
	}
	if v := viewLoadRun(&model.LoadTestRun{}); v.Bottleneck != "" || v.Containers != nil {
		t.Errorf("runs without resources: %+v", v)
	}
}

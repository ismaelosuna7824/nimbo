package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// fakeDocker emulates the docker CLI calls of fault injection for one
// compose project: container states, pause/unpause/start, helper runs.
type fakeDocker struct {
	mu       sync.Mutex
	wf       string
	state    map[string]string // id -> running | paused | exited
	service  map[string]string // id -> service
	started  map[string]string // id -> StartedAt
	netem    map[string]bool   // id -> netem qdisc present
	calls    [][]string
	restarts bool // a crashed container comes back (restart policy)
}

func newFakeDocker(wf string) *fakeDocker {
	return &fakeDocker{
		wf:      wf,
		state:   map[string]string{"api1": "running", "api2": "running", "db1": "running", "other": "running"},
		service: map[string]string{"api1": "api", "api2": "api", "db1": "db", "other": "db"},
		started: map[string]string{"api1": "t0", "api2": "t0", "db1": "t0", "other": "t0"},
		netem:   map[string]bool{},
	}
}

func (f *fakeDocker) run(_ context.Context, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	label := func(id string) string {
		if id == "other" {
			return "someone-else"
		}
		return f.wf
	}
	switch args[0] {
	case "ps":
		var svc string
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "label="+composeServiceLabel+"="); ok {
				svc = v
			}
		}
		var out []string
		for _, id := range []string{"api1", "api2", "db1"} { // "other" carries foreign labels: ps filters it out
			if f.service[id] == svc {
				out = append(out, id)
			}
		}
		return strings.Join(out, "\n"), nil
	case "inspect":
		last := args[len(args)-1]
		if slices.Contains(args, "--format") {
			format := args[slices.Index(args, "--format")+1]
			switch {
			case strings.HasPrefix(format, "{{.State.Pid}}"):
				return "4242 " + f.started[last], nil
			case strings.HasPrefix(format, "{{.State.Status}} {{.State.StartedAt}}"):
				return f.state[last] + " " + f.started[last], nil
			case strings.HasPrefix(format, "{{.State.Status}} {{index"):
				if _, ok := f.state[last]; !ok {
					return "", errors.New("docker inspect: No such container: " + last)
				}
				return f.state[last] + " " + label(last), nil
			case format == "{{.State.Status}}":
				return f.state[last], nil
			}
			return "", fmt.Errorf("unexpected format %q", format)
		}
		var cs []map[string]any
		for _, id := range args[3:] {
			cs = append(cs, map[string]any{
				"Id": id, "Name": "/nimbo-dk-" + f.wf + "-" + id,
				"State":  map[string]any{"Status": f.state[id]},
				"Config": map[string]any{"Image": "postgres:16", "Labels": map[string]string{composeProjectLabel: model.ComposeProjectName(f.wf), ComposeLabel: label(id), composeServiceLabel: f.service[id]}},
			})
		}
		b, _ := json.Marshal(cs)
		return string(b), nil
	case "pause":
		for _, id := range args[1:] {
			f.state[id] = "paused"
		}
	case "unpause":
		f.state[args[1]] = "running"
	case "start":
		f.state[args[1]] = "running"
		f.started[args[1]] = "t2"
	case "run":
		cmd := strings.Join(args, " ")
		target := ""
		for i, a := range args {
			if a == "--network" && strings.HasPrefix(args[i+1], "container:") {
				target = strings.TrimPrefix(args[i+1], "container:")
			}
		}
		switch {
		case strings.Contains(cmd, "kill -9 4242"): // api1's main process (first replica by name)
			f.state["api1"] = "exited"
			if f.restarts {
				f.state["api1"], f.started["api1"] = "running", "t1"
			}
		case strings.Contains(cmd, "tc qdisc replace"):
			f.netem[target] = true
		case strings.Contains(cmd, "tc qdisc del"):
			delete(f.netem, target)
		}
	}
	return "", nil
}

func testOps(t *testing.T, d *fakeDocker) *faultOps {
	return &faultOps{docker: d.run, tools: func(context.Context) error { return nil }, dir: filepath.Join(t.TempDir(), "faults"), poll: 10 * time.Millisecond, wait: time.Second}
}

func pending(t *testing.T, o *faultOps) []pendingFault {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(o.dir, "*.json"))
	var out []pendingFault
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var p pendingFault
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestDependencyDownPausesThenUnpauses(t *testing.T) {
	d := newFakeDocker("wf1")
	o := testOps(t, d)
	f, err := o.injectCompose(context.Background(), "wf1", "db", model.ScenarioSpec{ID: model.ScenarioDependencyDown, Target: "service:db"}, model.LoadTarget{})
	if err != nil {
		t.Fatal(err)
	}
	if d.state["db1"] != "paused" || d.state["other"] != "running" {
		t.Fatalf("states %v: only the workflow's db container may be paused", d.state)
	}
	// The recipe is on disk while the fault is active.
	if p := pending(t, o); len(p) != 1 || p[0].Action != revertUnpause || !slices.Equal(p[0].Containers, []string{"db1"}) {
		t.Fatalf("pending %+v", p)
	}
	obs, err := f.Revert(context.Background())
	if err != nil || !obs.Verified || d.state["db1"] != "running" {
		t.Fatalf("revert: %v %+v %v", err, obs, d.state)
	}
	if p := pending(t, o); len(p) != 0 {
		t.Errorf("recipe left after revert: %+v", p)
	}
	// Idempotent.
	if _, err := f.Revert(context.Background()); err != nil {
		t.Error(err)
	}
}

func TestLatencyAddsAndRemovesNetem(t *testing.T) {
	d := newFakeDocker("wf1")
	o := testOps(t, d)
	f, err := o.injectCompose(context.Background(), "wf1", "db", model.ScenarioSpec{ID: model.ScenarioDependencyLatency, Target: "service:db", Params: map[string]string{"delayMs": "300", "lossPct": "1.5"}}, model.LoadTarget{})
	if err != nil {
		t.Fatal(err)
	}
	if !d.netem["db1"] || d.netem["other"] {
		t.Fatalf("netem %v", d.netem)
	}
	if !strings.Contains(f.Detail(), "delay 300ms loss 1.5%") {
		t.Errorf("detail %q", f.Detail())
	}
	add := strings.Join(d.calls[len(d.calls)-1], " ")
	if !strings.Contains(add, "--network container:db1 --cap-add NET_ADMIN") || !strings.Contains(add, ChaosToolsImage) {
		t.Errorf("helper run %q", add)
	}
	obs, err := f.Revert(context.Background())
	if err != nil || !obs.Verified || d.netem["db1"] {
		t.Fatalf("revert: %v %+v netem %v", err, obs, d.netem)
	}
}

func TestKillReplicaRestartPolicyVersusNimboStart(t *testing.T) {
	for _, restarts := range []bool{true, false} {
		t.Run(fmt.Sprintf("restart policy %v", restarts), func(t *testing.T) {
			d := newFakeDocker("wf1")
			d.restarts = restarts
			o := testOps(t, d)
			f, err := o.injectCompose(context.Background(), "wf1", "api", model.ScenarioSpec{ID: model.ScenarioKillReplica, Target: "service:api"}, model.LoadTarget{})
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond) // the watcher polls every 10 ms
			obs, err := f.Revert(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if obs.Back != restarts || obs.SelfHealed != restarts || !obs.Verified {
				t.Errorf("observation %+v", obs)
			}
			if d.state["api1"] != "running" {
				t.Errorf("api1 %s after revert", d.state["api1"])
			}
			startedByNimbo := slices.ContainsFunc(d.calls, func(c []string) bool { return c[0] == "start" })
			if startedByNimbo == restarts {
				t.Errorf("docker start called %v with restart policy %v", startedByNimbo, restarts)
			}
			if p := pending(t, o); len(p) != 0 {
				t.Errorf("recipe left: %+v", p)
			}
		})
	}
}

func TestRecoverReplaysLeftoverRecipes(t *testing.T) {
	d := newFakeDocker("wf1")
	o := testOps(t, d)
	// A daemon died with db paused, netem on api2 and api1 crashed.
	d.state["db1"], d.state["api1"], d.netem["api2"] = "paused", "exited", true
	for _, p := range []pendingFault{
		{ID: "f-1", Workflow: "wf1", Action: revertUnpause, Containers: []string{"db1"}},
		{ID: "f-2", Workflow: "wf1", Action: revertNetem, Containers: []string{"api2"}},
		{ID: "f-3", Workflow: "wf1", Action: revertStart, Containers: []string{"api1"}},
		{ID: "f-4", Workflow: "wf1", Action: revertUnpause, Containers: []string{"gone"}},
	} {
		if err := o.save(p); err != nil {
			t.Fatal(err)
		}
		activeFaults.Delete(p.ID) // saved by a previous process
	}
	n, err := o.recover(context.Background(), "")
	if err != nil || n != 4 {
		t.Fatalf("recovered %d: %v", n, err)
	}
	if d.state["db1"] != "running" || d.state["api1"] != "running" || d.netem["api2"] {
		t.Errorf("after recovery: %v netem %v", d.state, d.netem)
	}
	if p := pending(t, o); len(p) != 0 {
		t.Errorf("recipes left: %+v", p)
	}
}

func TestRecoverSkipsActiveAndForeign(t *testing.T) {
	d := newFakeDocker("wf1")
	o := testOps(t, d)
	f, err := o.injectCompose(context.Background(), "wf1", "db", model.ScenarioSpec{ID: model.ScenarioDependencyDown, Target: "service:db"}, model.LoadTarget{})
	if err != nil {
		t.Fatal(err)
	}
	// A recipe naming a container of another workflow is refused, not applied.
	d.state["other"] = "paused"
	if err := o.save(pendingFault{ID: "f-x", Workflow: "wf1", Action: revertUnpause, Containers: []string{"other"}}); err != nil {
		t.Fatal(err)
	}
	activeFaults.Delete("f-x")
	if _, err := o.recover(context.Background(), "wf1"); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Errorf("foreign container: %v", err)
	}
	if d.state["db1"] != "paused" || d.state["other"] != "paused" {
		t.Errorf("recovery touched the active fault or a foreign container: %v", d.state)
	}
	if _, err := f.Revert(context.Background()); err != nil {
		t.Fatal(err)
	}
	activeFaults.Delete("f-x")
	os.Remove(filepath.Join(o.dir, "f-x.json"))
}

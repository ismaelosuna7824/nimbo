package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// editEnv edits envProject opened in environment env, writes the changed
// files back and reopens it there; it returns what changed and the
// reopened project.
func editEnv(t *testing.T, env string, fn func(e *Editor) error) (map[string]string, *Project, string) {
	t.Helper()
	dir := writeTree(t, envProject)
	p := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{Environment: env})
	if p.Model == nil {
		t.Fatal(p.Diagnostics)
	}
	out := edit(t, p, fn)
	for rel, content := range out {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	after := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{Environment: env})
	if after.Model == nil {
		t.Fatalf("edited project does not load: %v\n%v", after.Diagnostics, out)
	}
	return out, after, dir
}

func patchOf(t *testing.T, kv ...any) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	for i := 0; i+1 < len(kv); i += 2 {
		raw, err := json.Marshal(kv[i+1])
		if err != nil {
			t.Fatal(err)
		}
		out[kv[i].(string)] = raw
	}
	return out
}

// Deleting a field while an environment is active removes it from that
// environment's override only: the base files, shared by every
// environment, keep their value, which applies again.
func TestEnvironmentDeleteKeepsBase(t *testing.T) {
	out, prod, _ := editEnv(t, "prod", func(e *Editor) error {
		return e.PatchService("api", patchOf(t,
			"command", nil,
			"resources", nil,
			// A dropped: prod's A goes, the base's A stays.
			"environment", []map[string]string{{"key": "B", "value": "fromprod"}, {"key": "C", "value": "c"}},
		))
	})
	if _, ok := out["compose.yaml"]; ok {
		t.Fatalf("the base file changed:\n%s", out["compose.yaml"])
	}
	prodFile := out["compose.prod.yaml"]
	if strings.Contains(prodFile, "command") || strings.Contains(prodFile, "A: prod") || strings.Contains(prodFile, "resources") || !strings.Contains(prodFile, "C: c") {
		t.Errorf("override:\n%s", prodFile)
	}
	api := prod.Model.Services["api"]
	if strings.Join(api.Command, " ") != "node a.js" || *api.Environment["A"] != "base" || *api.Environment["C"] != "c" {
		t.Errorf("command %v environment %v", api.Command, api.Environment)
	}
	if l := api.Deploy.Resources.Limits; l.NanoCPUs != 1 || l.MemoryBytes != 256<<20 {
		t.Errorf("limits %+v", l)
	}
	if *api.Deploy.Replicas != 2 {
		t.Errorf("replicas %v", *api.Deploy.Replicas)
	}

	// Without an environment the base is edited, as before.
	base, _, _ := editEnv(t, "", func(e *Editor) error { return e.PatchService("api", patchOf(t, "command", nil)) })
	if strings.Contains(base["compose.yaml"], "command") {
		t.Errorf("base:\n%s", base["compose.yaml"])
	}
}

// List edits in an environment whose override appends to the base's list
// say exactly which items the service gets (!override), so items are
// neither duplicated nor kept from the base.
func TestEnvironmentListEditsReplace(t *testing.T) {
	// expose: base [9000] + prod [9100]; the inspector sends the merged list.
	out, prod, _ := editEnv(t, "prod", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "expose", []string{"9000", "9200"}))
	})
	if _, ok := out["compose.yaml"]; ok {
		t.Fatalf("the base file changed:\n%s", out["compose.yaml"])
	}
	if got := prod.Model.Services["api"].Expose; !slices.Equal(got, []string{"9000", "9200"}) {
		t.Errorf("expose %v\n%s", got, out["compose.prod.yaml"])
	}
	if !strings.Contains(out["compose.prod.yaml"], "expose: !override") {
		t.Errorf("override:\n%s", out["compose.prod.yaml"])
	}

	// Removing the base's item in prod.
	_, prod, _ = editEnv(t, "prod", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "expose", []string{"9100"}))
	})
	if got := prod.Model.Services["api"].Expose; !slices.Equal(got, []string{"9100"}) {
		t.Errorf("expose %v", got)
	}

	// Emptying it in prod.
	out, prod, _ = editEnv(t, "prod", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "expose", []string{}))
	})
	if got := prod.Model.Services["api"].Expose; len(got) != 0 {
		t.Errorf("expose %v\n%s", got, out["compose.prod.yaml"])
	}

	// A list already tagged !override is edited in place and stays tagged.
	out, prod, _ = editEnv(t, "prod", func(e *Editor) error {
		return e.PatchService("db", patchOf(t, "ports", []map[string]string{{"host": "5433", "container": "5432"}, {"host": "5434", "container": "5432"}}))
	})
	ports := prod.Model.Services["db"].Ports
	if len(ports) != 2 || ports[0].Published != "5433" || ports[1].Published != "5434" || !strings.Contains(out["compose.prod.yaml"], "!override") {
		t.Errorf("db ports %+v\n%s", ports, out["compose.prod.yaml"])
	}

	// Ports only the base sets are edited in the base (shared), untagged.
	out, prod, _ = editEnv(t, "prod", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "ports", []map[string]string{{"host": "3200", "container": "3000"}}))
	})
	if _, ok := out["compose.prod.yaml"]; ok || strings.Contains(out["compose.yaml"], "!override") {
		t.Errorf("files %v", out)
	}
	if p := prod.Model.Services["api"].Ports; len(p) != 1 || p[0].Published != "3200" {
		t.Errorf("api ports %+v", p)
	}
}

// resetOverrides drops attributes from the active environment's override:
// the base values apply again and the base file is untouched.
func TestEnvironmentResetOverrides(t *testing.T) {
	out, prod, _ := editEnv(t, "prod", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "resetOverrides", []string{"deploy.replicas", "environment", "expose"}))
	})
	if _, ok := out["compose.yaml"]; ok {
		t.Fatalf("the base file changed:\n%s", out["compose.yaml"])
	}
	api := prod.Model.Services["api"]
	if api.Deploy.Replicas != nil || *api.Environment["A"] != "base" || !slices.Equal(api.Expose, []string{"9000"}) {
		t.Errorf("api %+v %v %v", api.Deploy, api.Environment, api.Expose)
	}
	for _, n := range prod.Graph(nil).Nodes {
		if n.Name == "api" && n.Service != nil && !slices.Equal(n.Service.Overrides, []string{"command", "deploy.resources"}) {
			t.Errorf("overrides %v", n.Service.Overrides)
		}
	}
	// No environment: nothing to reset.
	dir := writeTree(t, envProject)
	e, err := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{}).Edit()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.PatchService("api", patchOf(t, "resetOverrides", []string{"image"})); err == nil {
		t.Error("reset without an environment")
	}
}

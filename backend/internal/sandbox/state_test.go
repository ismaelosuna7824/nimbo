package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// A restarted engine reads the deployed state without running `terraform show` again, until the state changes.
func TestStateShowSurvivesEngineRestart(t *testing.T) {
	home := t.TempDir()
	calls := filepath.Join(home, "docker-calls")
	docker := filepath.Join(home, "docker")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\n" +
		"case \"$*\" in *' show '*) echo '{\"format_version\":\"1.0\",\"values\":{\"root_module\":{}}}';; esac\n"
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := func() *Manager {
		m, err := New(Config{Home: home, Docker: docker})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	shows := func() int {
		b, _ := os.ReadFile(calls)
		return strings.Count(string(b), " show ")
	}
	wf := &model.Workflow{ID: "wf-state", Name: "state"}
	m := engine()
	work := m.WorkDir(wf.ID)
	if err := os.MkdirAll(filepath.Join(work, ".terraform"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(work, "terraform.tfstate")
	if err := os.WriteFile(state, []byte(`{"version":4,"serial":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	for i, mgr := range []*Manager{m, engine()} {
		if _, err := mgr.State(context.Background(), wf); err != nil {
			t.Fatalf("engine %d: %v", i, err)
		}
	}
	if n := shows(); n != 1 {
		t.Fatalf("terraform show ran %d times across a restart, want 1", n)
	}

	if err := os.WriteFile(state, []byte(`{"version":4,"serial":22}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine().State(context.Background(), wf); err != nil {
		t.Fatal(err)
	}
	if n := shows(); n != 2 {
		t.Fatalf("terraform show ran %d times after the state changed, want 2", n)
	}
}

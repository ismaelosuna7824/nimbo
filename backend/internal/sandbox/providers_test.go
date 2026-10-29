package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

var testCatalog = sync.OnceValues(catalog.Load)

func compatCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := testCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// googleWorkflow requires hashicorp/google at constraint and declares
// resources of types.
func googleWorkflow(constraint string, types ...string) *model.Workflow {
	rp := model.Body{Attrs: []model.Attr{{Name: "google", Value: model.Value{Expr: `{ source = "hashicorp/google", version = "` + constraint + `" }`}}}}
	wf := &model.Workflow{ID: "wf-providers", Clouds: []model.Cloud{model.CloudGCP},
		Terraform: &model.Body{Blocks: []model.Block{{Type: "required_providers", Body: rp}}}}
	for _, typ := range types {
		wf.Nodes = append(wf.Nodes, model.Node{Kind: model.KindResource, Type: typ, Name: "main"})
	}
	return wf
}

func lockFile(versions map[string]string) string {
	var b strings.Builder
	for typ, v := range versions {
		b.WriteString("provider \"registry.terraform.io/hashicorp/" + typ + "\" {\n  version = \"" + v + "\"\n}\n")
	}
	return b.String()
}

func TestStaleLocks(t *testing.T) {
	wf := googleWorkflow("~> 7.46")
	if got := staleLocks(wf, map[string]string{"google": "6.50.0", "aws": "5.0.0"}); len(got) != 1 || got[0] != "hashicorp/google 6.50.0 (required: ~> 7.46)" {
		t.Errorf("staleLocks = %v", got)
	}
	for _, lock := range []map[string]string{{"google": "7.46.1"}, {}, {"google": "not-a-version"}} {
		if got := staleLocks(wf, lock); len(got) != 0 {
			t.Errorf("%v: staleLocks = %v", lock, got)
		}
	}
	if got := staleLocks(googleWorkflow(""), map[string]string{"google": "6.50.0"}); len(got) != 0 {
		t.Errorf("no constraint: %v", got)
	}
}

func TestProviderWarnings(t *testing.T) {
	cat := compatCatalog(t)
	google, _ := cat.ProviderCompat("google")
	sql := googleWorkflow("~> 6.0", "google_sql_database_instance", "google_sql_database")
	ws := providerWarnings(cat, sql, map[string]string{"google": "6.50.0", "random": "3.9.1"})
	if len(ws) != 1 {
		t.Fatalf("warnings = %q", ws)
	}
	for _, want := range []string{"hashicorp/google 6.50.0 (selected by the lock file)", "google_sql_database.main", "Cloud SQL databases",
		"Require hashicorp/google 7.26 or newer", `version = "` + google.Recommended + `"`} {
		if !strings.Contains(ws[0], want) {
			t.Errorf("warning lacks %q: %s", want, ws[0])
		}
	}
	if strings.Contains(ws[0], "google_sql_database_instance.main") {
		t.Errorf("instances work on old releases: %s", ws[0])
	}
	// Older than the verified minimum, without an affected resource.
	ws = providerWarnings(cat, googleWorkflow("~> 7.0", "google_storage_bucket"), map[string]string{"google": "7.10.0"})
	if len(ws) != 1 || !strings.Contains(ws[0], "older than "+google.LocalMinimum) || !strings.Contains(ws[0], google.Recommended) {
		t.Errorf("warnings = %q", ws)
	}
	for name, tc := range map[string]struct {
		cat  *catalog.Catalog
		wf   *model.Workflow
		lock map[string]string
	}{
		"current release": {cat, sql, map[string]string{"google": google.Catalog}},
		"newer major":     {cat, sql, map[string]string{"google": "8.9.0"}},
		"not initialized": {cat, sql, nil},
		"no catalog":      {nil, sql, map[string]string{"google": "6.50.0"}},
		"other namespace": {cat, &model.Workflow{Terraform: &model.Body{Blocks: []model.Block{{Type: "required_providers", Body: model.Body{Attrs: []model.Attr{
			{Name: "google", Value: model.Value{Expr: `{ source = "acme/google", version = "~> 1.0" }`}}}}}}}, Nodes: sql.Nodes}, map[string]string{"google": "1.0.0"}},
	} {
		if ws := providerWarnings(tc.cat, tc.wf, tc.lock); len(ws) != 0 {
			t.Errorf("%s: warnings = %q", name, ws)
		}
	}
}

// initDocker is a docker CLI stand-in for terraform init: it records its
// arguments, fails like Terraform on a stale lock when mismatch exists and
// -upgrade is off, and writes upgraded as the lock file on -upgrade.
func initDocker(t *testing.T, home, work, upgraded string) (docker, calls, mismatch string) {
	t.Helper()
	calls, mismatch = filepath.Join(home, "calls"), filepath.Join(home, "mismatch")
	newLock := filepath.Join(home, "upgraded.lock.hcl")
	if err := os.WriteFile(newLock, []byte(upgraded), 0o644); err != nil {
		t.Fatal(err)
	}
	docker = filepath.Join(home, "docker")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\n" +
		"case \"$*\" in\n" +
		"  *-upgrade=true*) cp " + newLock + " " + filepath.Join(work, ".terraform.lock.hcl") + ";;\n" +
		"  *) if [ -e " + mismatch + " ]; then echo 'Error: locked provider registry.terraform.io/hashicorp/google 7.40.0 " + lockMismatch + " ~> 7.46; must use terraform init -upgrade' >&2; exit 1; fi;;\n" +
		"esac\n"
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return docker, calls, mismatch
}

func TestInitWorkdirUpgradesStaleLock(t *testing.T) {
	cat := compatCatalog(t)
	google, _ := cat.ProviderCompat("google")
	for _, tc := range []struct {
		name        string
		constraint  string
		lock        map[string]string // before the run
		mismatch    bool              // init reports a lock entry outside a (module's) constraint
		wantInits   []string          // -upgrade flag of each init
		wantLog     string
		wantWarning string
	}{
		{name: "lock outside the constraint", constraint: "~> 7.46", lock: map[string]string{"google": "6.50.0"},
			wantInits: []string{"-upgrade=true"}, wantLog: "The lock file pins hashicorp/google 6.50.0 (required: ~> 7.46)"},
		{name: "init reports a mismatch", constraint: "~> 7.0", lock: map[string]string{"google": "7.40.0"}, mismatch: true,
			wantInits: []string{"-upgrade=false", "-upgrade=true"}, wantLog: "doesn't match the configured version constraints"},
		{name: "lock within the constraint", constraint: "~> 7.46", lock: map[string]string{"google": google.Catalog},
			wantInits: []string{"-upgrade=false"}},
		{name: "old lock the constraint still allows", constraint: "~> 6.0", lock: map[string]string{"google": "6.50.0"},
			wantInits: []string{"-upgrade=false"}, wantWarning: "hashicorp/google 6.50.0 (selected by the lock file) is known to break google_sql_database.main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			m, err := New(Config{Home: home, Catalog: cat})
			if err != nil {
				t.Fatal(err)
			}
			wf := googleWorkflow(tc.constraint, "google_sql_database")
			work := m.WorkDir(wf.ID)
			if err := os.MkdirAll(work, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, ".terraform.lock.hcl"), []byte(lockFile(tc.lock)), 0o644); err != nil {
				t.Fatal(err)
			}
			docker, calls, mismatch := initDocker(t, home, work, lockFile(map[string]string{"google": google.Catalog}))
			if tc.mismatch {
				if err := os.WriteFile(mismatch, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			m.cfg.Docker = docker
			rs := &runState{run: model.Run{ID: "r1", WorkflowID: wf.ID}}
			if err := m.initWorkdir(context.Background(), rs, wf, work, readLockVersions(work), nil, nil); err != nil {
				t.Fatalf("initWorkdir: %v\n%v", err, rs.run.Log)
			}
			raw, _ := os.ReadFile(calls)
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) != len(tc.wantInits) {
				t.Fatalf("docker calls %q, want inits %v", lines, tc.wantInits)
			}
			for i, flag := range tc.wantInits {
				if !strings.HasSuffix(lines[i], " init -input=false -no-color "+flag) {
					t.Errorf("call %d = %q, want %s", i, lines[i], flag)
				}
			}
			var log strings.Builder
			for _, l := range rs.run.Log {
				log.WriteString(l.Text + "\n")
			}
			if tc.wantLog != "" && !strings.Contains(log.String(), tc.wantLog) {
				t.Errorf("log lacks %q:\n%s", tc.wantLog, log.String())
			}
			switch {
			case tc.wantWarning == "" && len(rs.run.Warnings) > 0:
				t.Errorf("warnings = %q", rs.run.Warnings)
			case tc.wantWarning != "":
				if len(rs.run.Warnings) != 1 || !strings.Contains(rs.run.Warnings[0], tc.wantWarning) || !strings.Contains(log.String(), "Warning: "+rs.run.Warnings[0]) {
					t.Errorf("warnings = %q, want %q (logged)", rs.run.Warnings, tc.wantWarning)
				}
			}
		})
	}
}

func TestInitWorkdirFailureWithoutMismatch(t *testing.T) {
	home := t.TempDir()
	docker := filepath.Join(home, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\necho \"$*\" >> "+filepath.Join(home, "calls")+"\necho 'Error: registry unreachable' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := New(Config{Home: home, Docker: docker, Catalog: compatCatalog(t)})
	if err != nil {
		t.Fatal(err)
	}
	wf := googleWorkflow("~> 7.46")
	rs := &runState{run: model.Run{ID: "r1", WorkflowID: wf.ID}}
	if err := m.initWorkdir(context.Background(), rs, wf, m.WorkDir(wf.ID), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("err = %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(home, "calls")); strings.Count(string(raw), " init ") != 1 || rs.run.ExitCode != 1 {
		t.Errorf("calls %q exit %d: other failures are not retried", raw, rs.run.ExitCode)
	}
}

func TestLockedVersions(t *testing.T) {
	m, err := New(Config{Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	assets := t.TempDir()
	if got := m.LockedVersions("wf1", assets); len(got) != 0 {
		t.Errorf("no lock files: %v", got)
	}
	if err := os.WriteFile(filepath.Join(assets, ".terraform.lock.hcl"), []byte(lockFile(map[string]string{"google": "7.0.0"})), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := m.LockedVersions("wf1", assets); got["google"] != "7.0.0" {
		t.Errorf("imported lock: %v", got)
	}
	work := m.WorkDir("wf1")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".terraform.lock.hcl"), []byte(lockFile(map[string]string{"google": "7.46.1"})), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := m.LockedVersions("wf1", assets); got["google"] != "7.46.1" {
		t.Errorf("the sandbox's lock wins: %v", got)
	}
}

// A gap of the catalog release itself names its own fix, not the
// recommended constraint (which is affected too).
func TestProviderWarningsEmulatorGap(t *testing.T) {
	cat := compatCatalog(t)
	oci, _ := cat.ProviderCompat("oci")
	wf := &model.Workflow{Nodes: []model.Node{{Kind: model.KindResource, Type: "oci_functions_application", Name: "app"}}}
	ws := providerWarnings(cat, wf, map[string]string{"oci": oci.Catalog})
	if len(ws) != 1 || !strings.Contains(ws[0], "oci_functions_application.app") || !strings.Contains(ws[0], `version = "9.2.0"`) || strings.Contains(ws[0], oci.Recommended) {
		t.Errorf("warnings = %q", ws)
	}
}

package challenge_test

import (
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/challenge"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/learn"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// staticTypes are the checks graded without a runtime.
var staticTypes = []string{model.CheckValidate, model.CheckRulesClean, model.CheckHas}

// TestContentStaticChecks grades every shipped challenge's static checks
// (validate, rules_clean, has) on its starter and on its reference
// solution: the solution must pass all of them (and, for Terraform, be
// valid against the provider schemas), and a challenge graded only
// statically must fail at least one on its starter. Runtime checks
// (deploy, http, load, scenario, readiness) are verified for real.
// Each migration file is parsed on its own, so one broken file is
// reported without hiding the others (references are the learn tests').
func TestContentStaticChecks(t *testing.T) {
	fsys := learn.Migrations()
	names, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	final := map[string]model.Challenge{}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatal(err)
		}
		m, err := learn.ParseMigration(name, data, challenge.Validate)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, op := range m.Ops {
			switch {
			case op.Kind == "challenge" && op.Op == "delete":
				delete(final, op.ID)
			case op.Kind == "challenge" && op.Challenge != nil:
				c := *op.Challenge
				c.ID = op.ID
				final[op.ID] = c
			}
		}
	}
	if len(final) == 0 {
		t.Fatal("no challenges")
	}
	for _, id := range slices.Sorted(maps.Keys(final)) {
		c := final[id]
		t.Run(c.ID, func(t *testing.T) {
			t.Parallel()
			starter, err := challenge.ResolveStarter(nil, c.Starter)
			if err != nil {
				t.Fatal(err)
			}
			var static []model.ChallengeCheck
			for _, k := range c.Checks {
				if slices.Contains(staticTypes, k.Type) {
					static = append(static, k)
				}
			}
			if len(static) > 0 {
				starterFailed := grade(t, c, starter, starter.Files, static, func(k model.ChallengeCheck, r model.ChallengeGoalResult) {})
				if len(static) == len(c.Checks) && !starterFailed {
					t.Errorf("the starter passes every check: it must fail at least one")
				}
			}
			if len(c.SolutionFiles) == 0 {
				return
			}
			// A Terraform solution must also be valid against the provider
			// schemas, whether or not the challenge grades validate.
			solutionChecks := static
			if starter.Kind == "" && !slices.ContainsFunc(static, func(k model.ChallengeCheck) bool { return k.Type == model.CheckValidate }) {
				solutionChecks = append(slices.Clone(static), model.ChallengeCheck{ID: "schema-valid", Type: model.CheckValidate})
			}
			if len(solutionChecks) == 0 {
				return
			}
			grade(t, c, starter, starter.WithSolution(c.SolutionFiles), solutionChecks, func(k model.ChallengeCheck, r model.ChallengeGoalResult) {
				if !r.Passed {
					t.Errorf("solution fails %s (%s): %s", k.ID, k.Type, r.Detail)
				}
			})
		})
	}
}

// grade opens files as the challenge's workflow and grades the static
// checks, reporting each result; it returns whether any failed.
func grade(t *testing.T, c model.Challenge, st challenge.Starter, files map[string][]byte, checks []model.ChallengeCheck, each func(model.ChallengeCheck, model.ChallengeGoalResult)) bool {
	t.Helper()
	dir := t.TempDir()
	var wf *model.Workflow
	switch st.Kind {
	case model.WorkflowDocker, model.WorkflowKubernetes:
		for rel, data := range files {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		wf = &model.Workflow{ID: "challenge-test", Name: c.Title, Kind: st.Kind}
		if st.Kind == model.WorkflowDocker {
			wf.Compose = &model.ComposeModel{Files: compose.DefaultFiles(func(n string) bool { _, ok := files[n]; return ok })}
		} else {
			wf.Kubernetes = &model.KubernetesModel{Sources: slices.Clone(st.Kubernetes.Sources), Builds: slices.Clone(st.Kubernetes.Builds)}
		}
	default:
		var err error
		if wf, err = challenge.ImportTerraform(nil, c.Title, files); err != nil {
			t.Fatalf("import: %v", err)
		}
	}
	analyses := map[string]*challenge.Analysis{}
	failed := false
	for _, k := range checks {
		a, ok := analyses[k.Environment]
		if !ok {
			var err error
			if a, err = challenge.Analyze(context.Background(), nil, wf, dir, k.Environment); err != nil {
				r := model.ChallengeGoalResult{ID: k.ID, Status: model.ChallengeError, Detail: err.Error()}
				failed = true
				each(k, r)
				continue
			}
			analyses[k.Environment] = a
		}
		r := challenge.GradeStatic(k, a)
		failed = failed || !r.Passed
		each(k, r)
	}
	return failed
}

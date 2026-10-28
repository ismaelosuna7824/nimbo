package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestAwesomeCompose runs over a checkout of github.com/docker/awesome-compose
// (NIMBO_AWESOME_COMPOSE=<dir>): every project must load, round-trip byte
// for byte, give a consistent graph, and survive edit/undo pairs (rename,
// env set/delete, unchanged list patches) byte for byte.
func TestAwesomeCompose(t *testing.T) {
	root := os.Getenv("NIMBO_AWESOME_COMPOSE")
	if root == "" {
		t.Skip("set NIMBO_AWESOME_COMPOSE to a checkout of docker/awesome-compose")
	}
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" || d.Name() == "node_modules" {
			return filepath.SkipDir
		}
		has := func(n string) bool { _, err := os.Stat(filepath.Join(p, n)); return err == nil }
		if len(DefaultFiles(has)) > 0 {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Strings(dirs)
	ctx := context.Background()
	var loaded, identical, graphOK, edits, editOK int
	advice := map[string]int{}   // findings per rule (default settings)
	projects := map[string]int{} // projects with a finding per rule
	services := 0
	for _, dir := range dirs {
		name, _ := filepath.Rel(root, dir)
		has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
		files := DefaultFiles(has)
		p := Open(ctx, dir, files, nil, Options{})
		var errs []string
		for _, d := range p.Diagnostics {
			if d.Severity == model.SeverityError {
				errs = append(errs, d.Code+" "+d.File+":"+itoa(d.Line)+" "+d.Message)
			}
		}
		if p.Model == nil {
			t.Errorf("%s: not loaded: %v", name, errs)
			continue
		}
		loaded++
		if len(errs) > 0 {
			t.Logf("%s: diagnostics: %v", name, errs)
		}
		services += len(p.Model.Services)
		hit := map[string]bool{}
		// Every rule, opt-in ones included (marked in the summary).
		for _, d := range FilterSettings(slices.Clone(p.Diagnostics), allRules()) {
			if d.Severity == model.SeverityError {
				continue
			}
			advice[d.Code]++
			if !hit[d.Code] {
				hit[d.Code] = true
				projects[d.Code]++
			}
			if !ruleByCode[d.Code].DefaultOff {
				t.Logf("%s: %s %s %s:%d %s", name, d.Severity, d.Code, d.File, d.Line, d.Message)
			}
		}
		same := true
		for rel, d := range p.Docs {
			src, _ := os.ReadFile(filepath.Join(dir, rel))
			if string(d.Bytes()) != string(src) {
				same = false
				t.Errorf("%s/%s: round trip differs", name, rel)
			}
		}
		if same {
			identical++
		}
		g := p.Graph(nil)
		ids := map[string]bool{}
		for _, n := range g.Nodes {
			ids[n.ID] = true
		}
		ok := true
		for _, s := range p.SortedServiceNames() {
			if !ids[ServiceID(s)] {
				ok = false
				t.Errorf("%s: no node for service %s", name, s)
			}
		}
		for _, e := range g.Edges {
			if !ids[e.Source] || !ids[e.Target] {
				ok = false
				t.Errorf("%s: dangling edge %s", name, e.ID)
			}
		}
		if ok {
			graphOK++
		}
		var kinds []string
		for _, n := range g.Nodes {
			if n.Kind == model.ComposeService {
				kinds = append(kinds, n.Name+"("+strings.Join(append(append([]string{}, n.Service.Stack...), n.Service.BaseImages...), ",")+")")
			}
		}
		t.Logf("%s: %d nodes %d edges; %s", name, len(g.Nodes), len(g.Edges), strings.Join(kinds, " "))

		// Edit/undo pairs.
		orig := map[string]string{}
		for rel, d := range p.Docs {
			orig[rel] = string(d.Bytes())
		}
		for _, svc := range p.SortedServiceNames() {
			pairs := []struct {
				label  string
				do, un func(e *Editor) error
			}{
				{"rename", func(e *Editor) error { return e.RenameService(svc, svc+"-renamed") }, func(e *Editor) error { return e.RenameService(svc+"-renamed", svc) }},
				{"env", func(e *Editor) error { v := "x"; return e.envSet(svc, "NIMBO_TEST", &v) }, func(e *Editor) error { return e.envDelete(svc, "NIMBO_TEST") }},
				{"same-lists", func(e *Editor) error {
					f := p.serviceFacts(p.Model.Services[svc])
					ports, _ := json.Marshal(f.Ports)
					vols, _ := json.Marshal(f.Volumes)
					deps, _ := json.Marshal(f.DependsOn)
					env, _ := json.Marshal(envSpecs(f.Environment))
					patch := map[string]json.RawMessage{"ports": ports, "volumes": vols, "dependsOn": deps, "environment": env}
					if len(f.Ports) == 0 {
						delete(patch, "ports")
					}
					if len(f.Volumes) == 0 {
						delete(patch, "volumes")
					}
					if len(f.DependsOn) == 0 {
						delete(patch, "dependsOn")
					}
					return e.PatchService(svc, patch)
				}, nil},
			}
			for _, pair := range pairs {
				edits++
				q := Open(ctx, dir, files, nil, Options{})
				ed, err := q.Edit()
				if err != nil {
					t.Errorf("%s %s: %v", name, svc, err)
					continue
				}
				if err := pair.do(ed); err != nil {
					t.Errorf("%s %s %s: %v", name, svc, pair.label, err)
					continue
				}
				// The edited files load.
				r := Open(ctx, dir, files, nil, Options{Content: ed.Files()})
				if r.Model == nil {
					t.Errorf("%s %s %s: edited project does not load: %v\n%s", name, svc, pair.label, r.Diagnostics, ed.Files())
					continue
				}
				if pair.un != nil {
					if err := pair.un(ed); err != nil {
						t.Errorf("%s %s undo %s: %v", name, svc, pair.label, err)
						continue
					}
				}
				back := true
				for rel, d := range q.Docs {
					if string(d.Bytes()) != orig[rel] {
						back = false
						t.Errorf("%s %s %s: not restored in %s:\n%s", name, svc, pair.label, rel, d.Bytes())
					}
				}
				if back {
					editOK++
				}
			}
		}
	}
	t.Logf("awesome-compose: %d projects, %d loaded, %d round-trip identical, %d graphs consistent, %d/%d edit/undo pairs identical",
		len(dirs), loaded, identical, graphOK, editOK, edits)
	for _, code := range sortedKeys(advice) {
		optIn := ""
		if ruleByCode[code].DefaultOff {
			optIn = " (opt-in)"
		}
		t.Logf("advice %-22s %3d findings in %2d/%d projects (%d services)%s", code, advice[code], projects[code], loaded, services, optIn)
	}
}

// allRules turns every opt-in rule on (a function: files of the package
// append rules from init).
func allRules() model.BestPracticeSettings {
	var s model.BestPracticeSettings
	for _, r := range rules {
		if r.DefaultOff {
			s.EnabledRules = append(s.EnabledRules, r.Code)
		}
	}
	return s
}

func envSpecs(env []model.ServiceEnv) []EnvSpec {
	out := make([]EnvSpec, 0, len(env))
	for _, e := range env {
		out = append(out, EnvSpec{Key: e.Key, Value: e.Value, Secret: e.Secret})
	}
	return slices.Clip(out)
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

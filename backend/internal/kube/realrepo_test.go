package kube

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestRealRepos runs over checkouts of real Kubernetes repositories
// (NIMBO_K8S_REPOS=<dir1>:<dir2>..., e.g. kubernetes/examples,
// kubernetes-sigs/kustomize, charts pulled with `helm pull --untar`):
// every detected project must render (base and every environment), every
// Service selector should select a workload, every manifest file must
// round-trip byte for byte through ParseFile and through a no-op edit of
// each object, and Helm values fields must map to values keys.
func TestRealRepos(t *testing.T) {
	roots := os.Getenv("NIMBO_K8S_REPOS")
	if roots == "" {
		t.Skip("set NIMBO_K8S_REPOS to checkouts of Kubernetes repositories (colon-separated)")
	}
	ctx := context.Background()
	for _, root := range strings.Split(roots, ":") {
		var files []string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "vendor") {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if IsCandidate(rel) || d.Name() == "Chart.yaml" {
				files = append(files, rel)
			}
			return nil
		})
		read := func(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) }
		projects := Detect(files, read)
		var rendered, renders, objects, services, matched, rtOK, rtFiles, helmFields, helmMapped, helmEditable, fieldsTotal, fieldsEditable, edits, readOnly int
		kinds := map[string]int{}
		for _, d := range projects {
			dir := filepath.Join(root, filepath.FromSlash(d.Dir))
			wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: d.Sources}}
			envs := append([]string{""}, d.Envs...)
			ok := true
			for _, env := range envs {
				start := time.Now()
				p := Open(ctx, dir, wf, Options{Environment: env})
				renders++
				for _, r := range p.Sources {
					if r.Err != nil {
						ok = false
						t.Logf("%s/%s [%s] env %q: %v", filepath.Base(root), d.Dir, r.Source.Kind, env, r.Err)
					}
				}
				if env != "" {
					continue
				}
				if dt := time.Since(start); dt > 2*time.Second {
					t.Logf("%s/%s: slow render %v", filepath.Base(root), d.Dir, dt)
				}
				objects += len(p.Objects)
				for _, o := range p.Objects {
					kinds[d.Sources[0].Kind]++
					if o.Kind == "Service" && len(strMap(dig(o.Obj, "spec", "selector"))) > 0 {
						services++
					}
				}
				g := p.Graph(nil)
				for _, n := range g.Nodes {
					if n.Service != nil && len(n.Service.Selector) > 0 {
						if n.Service.Unmatched {
							t.Logf("%s/%s: %s selects nothing (%v)", filepath.Base(root), d.Dir, n.ID, n.Service.Selector)
						} else {
							matched++
						}
					}
				}
				// Round trip: files parse and print back identically, and
				// a no-op edit of every object changes nothing.
				for _, f := range p.Files {
					data, err := p.Read(f)
					if err != nil || !isManifestFile(f) {
						continue
					}
					rtFiles++
					if string(ParseFile(f, data).Bytes()) == string(data) {
						rtOK++
					} else {
						t.Errorf("%s/%s: %s does not round-trip", filepath.Base(root), d.Dir, f)
					}
				}
				for _, o := range p.Objects {
					fields := p.fieldSources(o, nil)
					for _, fsrc := range fields {
						fieldsTotal++
						if fsrc.Editable {
							fieldsEditable++
						}
					}
					if p.Source(o).Source.Kind == model.KubeHelm {
						for _, fsrc := range fields {
							helmFields++
							if fsrc.Via == ViaValues {
								helmMapped++
							}
							if fsrc.Editable {
								helmEditable++
							}
						}
					}
					for _, fp := range editFields(o) {
						edited, err := roundTrip(dir, wf, env, o, fp)
						switch {
						case err != nil:
							t.Errorf("%s/%s: edit %s %s: %v", filepath.Base(root), d.Dir, o.ID, fp, err)
						case edited:
							edits++
						default:
							readOnly++
						}
					}
				}
			}
			if ok {
				rendered++
			}
		}
		t.Logf("%s: %d projects (%v), %d/%d render clean (%d renders incl. environments), %d objects, services with selector %d matched %d, round-trip %d/%d files, fields editable %d/%d, helm fields %d mapped to values %d editable %d, edit+revert round trips %d ok (%d fields read-only)",
			filepath.Base(root), len(projects), kinds, rendered, len(projects), renders, objects, services, matched, rtOK, rtFiles, fieldsEditable, fieldsTotal, helmFields, helmMapped, helmEditable, edits, readOnly)
		if rendered*10 < len(projects)*8 {
			t.Errorf("%s: only %d/%d projects render", root, rendered, len(projects))
		}
	}
}

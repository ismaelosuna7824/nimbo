package challenge

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"

	"go.yaml.in/yaml/v4"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubecatalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Challenge starters too large to inline in a migration (apps with
// Dockerfiles and lockfiles) live in _starters/<id>/ with a starter.yaml
// manifest (kind, Kubernetes sources and builds). A file named
// "<x>.nimbo" is written as <x>.
//
//go:embed all:_starters
var starterFS embed.FS

const starterManifest = "starter.yaml"

// manifest is a starter.yaml.
type manifest struct {
	Kind       string                 `json:"kind"`
	Kubernetes *model.KubernetesModel `json:"kubernetes,omitempty"`
}

// Starter is a resolved starter: what a new challenge workflow holds.
type Starter struct {
	Kind  string // model.WorkflowDocker, model.WorkflowKubernetes or "" (Terraform)
	Cloud model.Cloud
	Files map[string][]byte
	// Kubernetes: the sources and builds.
	Kubernetes *model.KubernetesModel
	// TemplateID is the starter template ("" for inline files).
	TemplateID string
}

var defaultCatalog = sync.OnceValues(catalog.Load)

// starterIDs lists the embedded starters.
func starterIDs() []string {
	entries, _ := fs.ReadDir(starterFS, "_starters")
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func readManifest(id string) (manifest, error) {
	data, err := starterFS.ReadFile(path.Join("_starters", id, starterManifest))
	if err != nil {
		return manifest{}, err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return manifest{}, err
	}
	js, err := json.Marshal(raw)
	if err != nil {
		return manifest{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(js))
	dec.DisallowUnknownFields()
	var m manifest
	if err := dec.Decode(&m); err != nil {
		return manifest{}, fmt.Errorf("starter %s: %w", id, err)
	}
	return m, nil
}

func starterFiles(id string) map[string][]byte {
	root := path.Join("_starters", id)
	files := map[string][]byte{}
	_ = fs.WalkDir(starterFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		if rel == starterManifest {
			return nil
		}
		data, err := starterFS.ReadFile(p)
		if err != nil {
			return err
		}
		files[strings.TrimSuffix(rel, ".nimbo")] = data
		return nil
	})
	return files
}

// ResolveStarter returns the files and kind of a challenge's starter: a
// challenge starter, a docker or Kubernetes template, a Terraform catalog
// template (cat; nil loads the built-in catalog), or inline files.
func ResolveStarter(cat *catalog.Catalog, s model.ChallengeStarter) (Starter, error) {
	if s.TemplateID != "" {
		if len(s.Files) > 0 || s.Kind != "" {
			return Starter{}, fmt.Errorf("starter: use templateId or kind+files, not both")
		}
		return resolveTemplate(cat, s.TemplateID)
	}
	if len(s.Files) == 0 {
		return Starter{}, fmt.Errorf("starter needs a templateId or files")
	}
	out := Starter{Files: make(map[string][]byte, len(s.Files)), Cloud: s.Cloud}
	for rel, content := range s.Files {
		if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || strings.HasPrefix(rel, "../") {
			return Starter{}, fmt.Errorf("starter file %q: use a clean relative slash path", rel)
		}
		out.Files[rel] = []byte(content)
	}
	switch s.Kind {
	case "terraform":
		if s.Cloud == "" {
			return Starter{}, fmt.Errorf("a terraform starter needs a cloud")
		}
		if s.Kubernetes != nil {
			return Starter{}, fmt.Errorf("kubernetes sources are for kubernetes starters")
		}
	case model.WorkflowDocker:
		if s.Kubernetes != nil {
			return Starter{}, fmt.Errorf("kubernetes sources are for kubernetes starters")
		}
		out.Kind = model.WorkflowDocker
	case model.WorkflowKubernetes:
		out.Kind = model.WorkflowKubernetes
		if s.Kubernetes != nil {
			out.Kubernetes = cloneKube(s.Kubernetes)
			break
		}
		names := slices.Sorted(maps.Keys(out.Files))
		found := kube.Detect(names, func(rel string) ([]byte, error) {
			if b, ok := out.Files[rel]; ok {
				return b, nil
			}
			return nil, fs.ErrNotExist
		})
		if len(found) != 1 || found[0].Dir != "." {
			return Starter{}, fmt.Errorf("kubernetes starter: found %d projects in the files; list starter.kubernetes.sources", len(found))
		}
		out.Kubernetes = &model.KubernetesModel{Sources: found[0].Sources}
	default:
		return Starter{}, fmt.Errorf("starter kind %q: use terraform, docker or kubernetes", s.Kind)
	}
	return out, nil
}

func resolveTemplate(cat *catalog.Catalog, id string) (Starter, error) {
	if slices.Contains(starterIDs(), id) {
		m, err := readManifest(id)
		if err != nil {
			return Starter{}, err
		}
		st := Starter{Kind: m.Kind, Files: starterFiles(id), TemplateID: id}
		if m.Kind == model.WorkflowKubernetes {
			if m.Kubernetes == nil {
				return Starter{}, fmt.Errorf("starter %s: kubernetes sources missing", id)
			}
			st.Kubernetes = cloneKube(m.Kubernetes)
		}
		return st, nil
	}
	if files, ok := compose.TemplateFiles(id); ok {
		return Starter{Kind: model.WorkflowDocker, Files: files, TemplateID: id}, nil
	}
	if files, k, ok := kubecatalog.TemplateFiles(id); ok {
		return Starter{Kind: model.WorkflowKubernetes, Files: files, Kubernetes: k, TemplateID: id}, nil
	}
	if cat == nil {
		var err error
		if cat, err = defaultCatalog(); err != nil {
			return Starter{}, err
		}
	}
	if files, ok := cat.TemplateFiles(id); ok {
		st := Starter{Files: files, TemplateID: id}
		for _, t := range cat.Templates() {
			if t.ID == id {
				st.Cloud = t.Cloud
			}
		}
		return st, nil
	}
	return Starter{}, fmt.Errorf("unknown starter template %q", id)
}

func cloneKube(k *model.KubernetesModel) *model.KubernetesModel {
	out := &model.KubernetesModel{Sources: slices.Clone(k.Sources), Builds: slices.Clone(k.Builds)}
	for i := range out.Sources {
		out.Sources[i].Environments = maps.Clone(out.Sources[i].Environments)
	}
	return out
}

// WithSolution returns the starter's files with the solution files
// written over them.
func (s Starter) WithSolution(solution map[string]string) map[string][]byte {
	out := maps.Clone(s.Files)
	for rel, content := range solution {
		out[rel] = []byte(content)
	}
	return out
}

package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Kubernetes environments: Kustomize overlays and Helm values files (see
// kube.CreateEnvironment). Every change is written to the workflow's files
// and saved like any other edit, so the graph, the k8s_* rules, Readiness
// and Deploy follow it.

// kubeEnvironmentsNote explains Kubernetes environments.
const kubeEnvironmentsNote = "Environments are Kustomize overlays (overlays/<env>) and Helm values files (values-<env>.yaml) on top of the base: " +
	"new ones are created for every Kustomize and Helm source. The canvas, rules, Readiness and Deploy follow the active environment."

func (s *Server) kubeEnvironments(wf *model.Workflow) model.EnvironmentsView {
	view := model.EnvironmentsView{Kind: wf.Kind, Environments: []model.EnvironmentInfo{}, Sources: []model.EnvironmentSource{}, Note: kubeEnvironmentsNote}
	var sources []model.KubeSource
	if k := wf.Kubernetes; k != nil {
		view.Active = k.Environment
		sources = k.Sources
	}
	for _, src := range sources {
		es := model.EnvironmentSource{Label: src.Kind + " " + src.Path, Kind: src.Kind, Path: src.Path, Supported: kube.TakesEnvironments(src.Kind)}
		switch src.Kind {
		case model.KubeHelm:
			es.Label = "Helm chart " + src.Path
			es.Note = "values-<env>.yaml next to the chart's values.yaml"
		case model.KubeKustomize:
			es.Label = "Kustomize " + src.Path
			es.Note = "an overlay directory with a kustomization.yaml on top of " + src.Path
		default:
			es.Label = "Manifests " + src.Path
			es.Note = "plain manifests deploy the same in every environment (move them into a Kustomize base to vary them)"
		}
		view.Sources = append(view.Sources, es)
	}
	for _, e := range wf.Environments {
		info := model.EnvironmentInfo{Name: e.Name, Active: e.Name == view.Active, ProdLike: model.ProdLike(e.Name), Files: []string{}}
		for _, src := range sources {
			if p := src.EnvPaths[e.Name]; p != "" {
				info.Files = append(info.Files, p)
			}
		}
		view.Environments = append(view.Environments, info)
	}
	return view
}

// applyKubeEnvPlan writes an environment plan's files and sources into wf
// (inside mutate) and removes the directories it emptied.
func (s *Server) applyKubeEnvPlan(wf *model.Workflow, plan *kube.EnvPlan) error {
	wf.Kubernetes.Sources = plan.Sources
	if err := s.writeKubeFiles(wf.ID, plan.Files); err != nil {
		return err
	}
	return s.pruneEmptyDirs(wf.ID, plan.Files, plan.Dirs)
}

// pruneEmptyDirs removes the directories the deleted files left empty
// (overlays/qa, then overlays when it was the last one), deepest first;
// nothing happens without emptied roots (a values file deletion).
func (s *Server) pruneEmptyDirs(id string, files map[string][]byte, roots []string) error {
	if len(roots) == 0 {
		return nil
	}
	dirs := map[string]bool{}
	for rel, data := range files {
		if data != nil {
			continue
		}
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	slices.SortFunc(list, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
	root, err := os.OpenRoot(s.cfg.Store.AssetsDir(id))
	if err != nil {
		return err
	}
	defer root.Close()
	for _, d := range list {
		entries, err := fs.ReadDir(root.FS(), d)
		if err != nil || len(entries) > 0 {
			continue
		}
		if err := root.Remove(filepath.FromSlash(d)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Server) kubeCreateEnvironment(id, name, from string) (*model.Workflow, error) {
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireKube(wf); err != nil {
			return nil, err
		}
		plan, err := kube.CreateEnvironment(s.cfg.Store.AssetsDir(id), wf, name, from)
		if err != nil {
			return nil, kubeEditErr(err)
		}
		return nil, s.applyKubeEnvPlan(wf, plan)
	})
}

func (s *Server) kubeRenameEnvironment(id, name, to string) (*model.Workflow, error) {
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireKube(wf); err != nil {
			return nil, err
		}
		plan, err := kube.RenameEnvironment(s.cfg.Store.AssetsDir(id), wf, name, to)
		if err != nil {
			return nil, kubeEditErr(err)
		}
		if wf.ActiveEnvironment == name {
			wf.ActiveEnvironment = to
		}
		// Derive keeps environments' entries by name.
		for i := range wf.Environments {
			if wf.Environments[i].Name == name {
				wf.Environments[i].Name = to
			}
		}
		return nil, s.applyKubeEnvPlan(wf, plan)
	})
}

func (s *Server) kubeDeleteEnvironment(id, name string) (*model.Workflow, error) {
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireKube(wf); err != nil {
			return nil, err
		}
		plan, err := kube.DeleteEnvironment(s.cfg.Store.AssetsDir(id), wf, name)
		if err != nil {
			return nil, kubeEditErr(err)
		}
		if wf.ActiveEnvironment == name {
			wf.ActiveEnvironment = ""
		}
		return nil, s.applyKubeEnvPlan(wf, plan)
	})
}

func (s *Server) kubeCompareEnvironments(r *http.Request, wf *model.Workflow) model.EnvCompare {
	return kube.CompareEnvironments(r.Context(), s.cfg.Store.AssetsDir(wf.ID), wf)
}

// openKubeEnv renders wf for env and for the base.
func (s *Server) openKubeEnv(ctx context.Context, wf *model.Workflow, env string) (p, base *kube.Project) {
	dir := s.cfg.Store.AssetsDir(wf.ID)
	return kube.Open(ctx, dir, wf, kube.Options{Environment: env}), kube.Open(ctx, dir, wf, kube.Options{NoSourceMap: true})
}

func (s *Server) kubeEnvironmentSettings(r *http.Request, wf *model.Workflow, env, target string) (model.EnvSettings, error) {
	p, base := s.openKubeEnv(r.Context(), wf, env)
	out, err := kube.EnvSettings(p, base, wf, target)
	if err != nil {
		return out, kubeEditErr(err)
	}
	return out, nil
}

func (s *Server) kubeSetEnvironmentSettings(id, env, target string, values map[string]*string) (*model.Workflow, error) {
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireKube(wf); err != nil {
			return nil, err
		}
		p, base := s.openKubeEnv(context.Background(), wf, env)
		e := p.Edit()
		if err := e.SetEnvSettings(base, wf, target, values); err != nil {
			return nil, kubeEditErr(err)
		}
		files := e.Files()
		if len(files) == 0 {
			return nil, nil
		}
		if err := s.writeKubeFiles(wf.ID, files); err != nil {
			return nil, fmt.Errorf("write %s: %w", env, err)
		}
		return nil, nil
	})
}

package api

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// kubePrefix keys the Kubernetes projects of a scan ("kubernetes:<dir>"):
// a folder may also be a Terraform or docker project.
const kubePrefix = "kubernetes:"

// kubeProjects finds the Kubernetes projects of the scanned folder: Helm
// charts, Kustomize trees (a base with its overlays as environments) and
// folders of plain manifests (see kube.Detect).
func (rs *repoScan) kubeProjects() []kube.Detected {
	var files []string
	_ = filepath.WalkDir(rs.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != rs.root && skipScanDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		rel := relSlash(rs.root, p)
		if d.Type().IsRegular() && (kube.IsCandidate(rel) || d.Name() == "Chart.yaml") && len(files) < maxScanFiles {
			files = append(files, rel)
		}
		return nil
	})
	read := func(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(rs.root, filepath.FromSlash(rel))) }
	return kube.Detect(files, read)
}

// kubeName suggests a workflow name: the folder's (the scanned folder's for
// the root), with "-k8s" when another project has that name.
func (rs *repoScan) kubeName(d kube.Detected, taken map[string]string) string {
	name := rs.rootName
	if d.Dir != "." {
		name = filepath.Base(filepath.FromSlash(d.Dir))
	}
	for _, n := range taken {
		if n == name {
			return name + "-k8s"
		}
	}
	return name
}

// kubeSummary describes a Kubernetes project for the scan result: its
// sources, how many objects they render and the render errors.
func (rs *repoScan) kubeSummary(d kube.Detected, taken map[string]string) model.ImportProject {
	ip := model.ImportProject{
		Path: kubePrefix + d.Dir, Name: rs.kubeName(d, taken), Kind: model.WorkflowKubernetes, Layout: model.LayoutSingle,
		KubeSources: d.Sources, Environments: d.Envs, Providers: []string{}, Clouds: []model.Cloud{}, TfvarsFiles: []string{}, Diagnostics: []model.Diagnostic{},
	}
	wf := &model.Workflow{Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: d.Sources}}
	p := kube.Open(context.Background(), filepath.Join(rs.root, filepath.FromSlash(d.Dir)), wf, kube.Options{})
	ip.Objects = len(p.Objects)
	for _, diag := range p.Diagnostics {
		if diag.Severity == model.SeverityError {
			ip.Diagnostics = append(ip.Diagnostics, diag)
		}
	}
	t, err := rs.kubeTree(d)
	if err == nil {
		ip.Bytes, ip.FileCount = t.bytes, len(t.files)
		ip.Diagnostics = append(ip.Diagnostics, t.diags...)
		err = t.tooLarge()
	}
	if err != nil {
		ip.Diagnostics = append(ip.Diagnostics, model.Diagnostic{Severity: model.SeverityError, Code: "k8s_import_size", Message: err.Error()})
	}
	return ip
}

// kubeTree lists the files a Kubernetes project imports: its folder
// (dependency, VCS and hidden folders left out).
func (rs *repoScan) kubeTree(d kube.Detected) (*dockerTree, error) {
	dir := filepath.Join(rs.root, filepath.FromSlash(d.Dir))
	t := &dockerTree{base: dir, sub: ".", files: map[string]treeFile{}, sizes: map[string]int64{}}
	if err := t.walk(dir, nil); err != nil {
		return nil, err
	}
	return t, nil
}

// planKube reads a Kubernetes project's files into a new workflow.
func (rs *repoScan) planKube(d kube.Detected, name string) (*importPlan, error) {
	t, err := rs.kubeTree(d)
	if err != nil {
		return nil, err
	}
	if err := t.tooLarge(); err != nil {
		return nil, err
	}
	plan := &importPlan{project: d.Dir, files: make(map[string][]byte, len(t.files)), diags: t.diags}
	for _, rel := range slices.Sorted(maps.Keys(t.files)) {
		tf := t.files[rel]
		data, err := os.ReadFile(tf.abs)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", tf.abs, err)
		}
		plan.files[rel] = data
		if tf.exec {
			plan.exec = append(plan.exec, rel)
		}
	}
	plan.wf = &model.Workflow{Name: name, Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: slices.Clone(d.Sources)}}
	return plan, nil
}

// kubeByKey indexes the Kubernetes projects by scan key.
func (rs *repoScan) kubeByKey() map[string]kube.Detected {
	out := map[string]kube.Detected{}
	for _, d := range rs.kubeProjects() {
		out[kubePrefix+d.Dir] = d
	}
	return out
}

func isKubeKey(p string) bool { return strings.HasPrefix(p, kubePrefix) }

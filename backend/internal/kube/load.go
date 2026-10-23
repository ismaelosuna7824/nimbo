// Package kube reads, renders and edits the Kubernetes workflows' files:
// plain manifests (multi-document YAML), Kustomize trees (rendered
// in-process with sigs.k8s.io/kustomize/api, like `kubectl kustomize`) and
// Helm charts (rendered in-process with the helm.sh/helm/v4 engine, like
// `helm template`, against the pinned KubeVersion). The graph is derived
// from the RENDERED objects of the active environment; every object keeps
// the file and line that produced it, and edits are written back into the
// files in place (comments, order and quoting kept) where the field comes
// from: the manifest, a Kustomize patch or transformer, a Helm values key,
// or a literal in a Helm template.
package kube

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// KubeVersion is the Kubernetes version Helm charts are rendered for
// (Capabilities.KubeVersion); the local cluster and the schema checks use
// the same version.
const KubeVersion = "v1.36.5"

// Options tune Open.
type Options struct {
	// Environment selects the overlays/values files of the sources (see
	// model.KubeSource.Environments); "" renders the base.
	Environment string
	// Content overrides files (slash paths relative to the workflow files)
	// with unsaved content; a nil value marks a deleted file.
	Content map[string][]byte
	// NoSourceMap skips the Helm field source maps (Fields computes them
	// lazily anyway).
	NoSourceMap bool
}

// Object is one rendered Kubernetes object.
type Object struct {
	ID         string
	APIVersion string
	Kind       string
	Name       string
	// Namespace is the effective namespace ("" for cluster-scoped kinds).
	Namespace string
	// Obj is the rendered object.
	Obj map[string]any
	// Node is the yaml mapping node of the rendered document; its lines
	// are relative to the document (Locate maps fields to file lines).
	Node *Node
	// Source indexes the workflow's Kubernetes.Sources.
	Source int
	// File is the file that declares the object (a manifest, a Kustomize
	// resource or generator's kustomization, a Helm template or CRD), a
	// slash path relative to the workflow files; Line is the 1-based line
	// its document starts at (Helm templates: 1). Doc is the document
	// index in File (-1 when not a plain document: generated objects,
	// Helm templates).
	File string
	Line int
	Doc  int
	// Template is the chart-relative template of a Helm object
	// ("templates/deployment.yaml", "charts/redis/templates/x.yaml").
	Template string
	// orgKind/orgName/orgNamespace identify a Kustomize resource in its
	// file before name prefixes, suffixes and namespaces were applied.
	orgKind, orgName, orgNamespace string
	// generated: a Kustomize generator made the object (ConfigMap/Secret).
	generated bool
	// matchKey identifies a Helm object across renders (see helmMap).
	matchKey string
	tplIndex int // position among its template's rendered documents
}

// Rendered is one source of a workflow, rendered.
type Rendered struct {
	Index   int
	Source  model.KubeSource
	Objects []*Object
	// YAML is the rendered multi-document YAML (what `up` applies).
	YAML []byte
	// Err is the render error (nil when it rendered).
	Err error
	// Env is the overlay directory / values file the environment adds ("").
	Env string
	// Files are the workflow files rendering read.
	Files []string
	// helm keeps what Fields needs to build the Helm source map.
	helm *helmRender
	// kust keeps the kustomization chain of the rendered root.
	kust *kustTree
}

// Project is a Kubernetes workflow's files, rendered for an environment.
type Project struct {
	Dir         string
	Environment string
	Sources     []*Rendered
	// Objects are every source's objects, in apply order.
	Objects     []*Object
	Files       []string
	Diagnostics []model.Diagnostic

	fs *files
}

// Open reads and renders the sources of the Kubernetes workflow wf whose
// files are in dir. It never fails: problems are diagnostics (file:line);
// a source that can't be rendered has Err set and no objects.
func Open(ctx context.Context, dir string, wf *model.Workflow, opts Options) *Project {
	p := &Project{Dir: dir, Environment: opts.Environment, fs: newFiles(dir, opts.Content)}
	var sources []model.KubeSource
	if wf.Kubernetes != nil {
		sources = wf.Kubernetes.Sources
	}
	if len(sources) == 0 {
		p.diag(model.SeverityError, "k8s_load", "", 0, "The workflow has no Kubernetes source",
			"Add manifests (a k8s/ folder of YAML files), a kustomization or a Helm chart in the Code tab, then list it in the workflow's sources.")
	}
	read := map[string]bool{}
	for i, src := range sources {
		if ctx.Err() != nil {
			break
		}
		r := &Rendered{Index: i, Source: src}
		switch src.Kind {
		case model.KubeManifests:
			p.renderManifests(r, opts.Environment)
		case model.KubeKustomize:
			p.renderKustomize(r, opts.Environment)
		case model.KubeHelm:
			p.renderHelm(r, opts.Environment)
		default:
			r.Err = fmt.Errorf("unknown source kind %q: use manifests, kustomize or helm", src.Kind)
		}
		if r.Err != nil {
			p.diag(model.SeverityError, "k8s_render", errFile(r), errLine(r.Err), fmt.Sprintf("%s %s: %v", src.Kind, src.Path, r.Err), "")
		}
		for _, f := range r.Files {
			read[f] = true
		}
		p.Sources = append(p.Sources, r)
	}
	p.Files = slices.Sorted(maps.Keys(read))
	p.index()
	return p
}

// index fills the objects' IDs (unique across sources) and Objects.
func (p *Project) index() {
	clusterScoped := map[string]bool{}
	for _, r := range p.Sources {
		for _, o := range r.Objects {
			if o.Kind == "CustomResourceDefinition" {
				if scope, _ := dig(o.Obj, "spec", "scope").(string); scope == "Cluster" {
					group, _ := dig(o.Obj, "spec", "group").(string)
					kind, _ := dig(o.Obj, "spec", "names", "kind").(string)
					clusterScoped[groupKind(group, kind)] = true
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, r := range p.Sources {
		def := r.Source.Namespace
		if def == "" {
			def = "default"
		}
		for _, o := range r.Objects {
			gk := groupKind(apiGroup(o.APIVersion), o.Kind)
			if isClusterScoped(o.Kind, apiGroup(o.APIVersion)) || clusterScoped[gk] {
				o.Namespace = ""
			} else if o.Namespace == "" {
				o.Namespace = def
			}
			id := model.KubeNodeID(nodeKind(o.APIVersion, o.Kind), o.Namespace, o.Name)
			if seen[id] {
				p.diag(model.SeverityWarning, "k8s_duplicate", o.File, o.Line,
					fmt.Sprintf("%s %s is declared twice (the second one, in %s, replaces the first when applied)", o.Kind, o.Name, sourceLabel(r.Source)), "")
				for n := 2; seen[id]; n++ {
					id = fmt.Sprintf("%s~%d", model.KubeNodeID(nodeKind(o.APIVersion, o.Kind), o.Namespace, o.Name), n)
				}
			}
			seen[id] = true
			o.ID = id
			p.Objects = append(p.Objects, o)
		}
	}
}

// Object returns the object with node ID id.
func (p *Project) Object(id string) *Object {
	for _, o := range p.Objects {
		if o.ID == id {
			return o
		}
	}
	return nil
}

// Source returns the rendered source of o.
func (p *Project) Source(o *Object) *Rendered {
	for _, r := range p.Sources {
		if r.Index == o.Source {
			return r
		}
	}
	return nil
}

// Read reads a workflow file (unsaved content first).
func (p *Project) Read(rel string) ([]byte, error) { return p.fs.read(rel) }

func (p *Project) diag(sev model.Severity, code, file string, line int, msg, fix string) {
	p.Diagnostics = append(p.Diagnostics, model.Diagnostic{Severity: sev, Code: code, Message: msg, Fix: fix, File: file, Line: line})
}

func sourceLabel(s model.KubeSource) string { return s.Kind + " " + s.Path }

// ---------------------------------------------------------------------------
// Files

// files reads the workflow's files with unsaved overrides.
type files struct {
	dir     string
	content map[string][]byte
	all     []string // lazily listed
	listed  bool
}

func newFiles(dir string, content map[string][]byte) *files {
	return &files{dir: dir, content: content}
}

// skipDirs are never read (dependencies, VCS, caches).
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true, ".terraform": true, ".idea": true, ".vscode": true}

func (f *files) read(rel string) ([]byte, error) {
	if data, ok := f.content[rel]; ok {
		if data == nil {
			return nil, fs.ErrNotExist
		}
		return data, nil
	}
	if !inside(rel) {
		return nil, fmt.Errorf("%s is outside the workflow", rel)
	}
	root, err := os.OpenRoot(f.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(filepath.FromSlash(rel))
}

func (f *files) exists(rel string) bool {
	if data, ok := f.content[rel]; ok {
		return data != nil
	}
	_, err := f.read(rel)
	return err == nil
}

// list returns every file (slash paths, sorted).
func (f *files) list() []string {
	if f.listed {
		return f.all
	}
	f.listed = true
	set := map[string]bool{}
	if root, err := os.OpenRoot(f.dir); err == nil {
		_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != "." && skipDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type().IsRegular() {
				set[p] = true
			}
			return nil
		})
		root.Close()
	}
	for rel, data := range f.content {
		if data == nil {
			delete(set, rel)
		} else {
			set[rel] = true
		}
	}
	f.all = slices.Sorted(maps.Keys(set))
	return f.all
}

// under lists the files below dir ("." = all).
func (f *files) under(dir string) []string {
	if dir == "." || dir == "" {
		return f.list()
	}
	var out []string
	for _, p := range f.list() {
		if strings.HasPrefix(p, dir+"/") {
			out = append(out, p)
		}
	}
	return out
}

// isDir reports whether rel is a directory holding files.
func (f *files) isDir(rel string) bool {
	return rel == "." || rel == "" || len(f.under(rel)) > 0
}

func inside(rel string) bool {
	return rel != "" && !path.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, "../") && path.Clean(rel) == rel
}

// cleanRel normalizes a source path ("./k8s/" -> "k8s").
func cleanRel(p string) string {
	p = path.Clean(strings.TrimPrefix(filepath.ToSlash(p), "./"))
	if p == "" {
		return "."
	}
	return p
}

// ---------------------------------------------------------------------------
// Object helpers

// dig walks nested maps.
func dig(m any, keys ...string) any {
	cur := m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

func str(m any, keys ...string) string {
	switch v := dig(m, keys...).(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func apiGroup(apiVersion string) string {
	if g, _, ok := strings.Cut(apiVersion, "/"); ok {
		return g
	}
	return ""
}

func groupKind(group, kind string) string {
	if group == "" {
		return kind
	}
	return kind + "." + group
}

// builtinGroups are the API groups whose kinds keep a bare Kind in node IDs
// (Gateway API included: its kinds are first-class in the graph).
var builtinGroups = map[string]bool{
	"": true, "apps": true, "batch": true, "autoscaling": true, "policy": true,
	"networking.k8s.io": true, "rbac.authorization.k8s.io": true, "storage.k8s.io": true,
	"apiextensions.k8s.io": true, "admissionregistration.k8s.io": true, "scheduling.k8s.io": true,
	"coordination.k8s.io": true, "discovery.k8s.io": true, "node.k8s.io": true,
	"certificates.k8s.io": true, "apiregistration.k8s.io": true, "events.k8s.io": true,
	"flowcontrol.apiserver.k8s.io": true, "resource.k8s.io": true, "gateway.networking.k8s.io": true,
}

// nodeKind is the Kind part of a node ID: "<Kind>.<group>" outside the
// built-in groups.
func nodeKind(apiVersion, kind string) string {
	g := apiGroup(apiVersion)
	if builtinGroups[g] {
		return kind
	}
	return kind + "." + g
}

// IsBuiltin reports whether apiVersion belongs to a built-in API group (or
// the Gateway API): other objects are custom resources.
func IsBuiltin(apiVersion string) bool { return builtinGroups[apiGroup(apiVersion)] }

var clusterKinds = map[string]bool{
	"Namespace": true, "Node": true, "PersistentVolume": true, "StorageClass": true, "ClusterRole": true,
	"ClusterRoleBinding": true, "CustomResourceDefinition": true, "PriorityClass": true, "IngressClass": true,
	"ValidatingWebhookConfiguration": true, "MutatingWebhookConfiguration": true, "ValidatingAdmissionPolicy": true,
	"ValidatingAdmissionPolicyBinding": true, "MutatingAdmissionPolicy": true, "MutatingAdmissionPolicyBinding": true,
	"APIService": true, "CSIDriver": true, "CSINode": true, "VolumeAttachment": true, "RuntimeClass": true,
	"GatewayClass": true, "CertificateSigningRequest": true, "FlowSchema": true, "PriorityLevelConfiguration": true,
	"ComponentStatus": true, "VolumeSnapshotClass": true, "DeviceClass": true,
	// Well-known cluster-scoped custom resources.
	"ClusterIssuer": true, "ClusterSecretStore": true, "ClusterPolicy": true, "ClusterTriggerBinding": true,
}

func isClusterScoped(kind, group string) bool {
	if !clusterKinds[kind] {
		return false
	}
	// A custom "Node" or "Namespace" kind in another group is namespaced
	// unless its CRD says otherwise.
	switch kind {
	case "ClusterIssuer", "ClusterSecretStore", "ClusterPolicy", "ClusterTriggerBinding", "VolumeSnapshotClass":
		return true
	}
	return builtinGroups[group]
}

// Category is the graph category of a kind.
func Category(apiVersion, kind string) string {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Pod", "Job", "CronJob", "ReplicationController":
		if IsBuiltin(apiVersion) {
			return model.KubeCatWorkload
		}
	case "Service", "Ingress", "IngressClass", "Gateway", "HTTPRoute", "GRPCRoute", "TCPRoute", "TLSRoute", "UDPRoute", "GatewayClass", "NetworkPolicy", "Endpoints", "EndpointSlice":
		if IsBuiltin(apiVersion) {
			return model.KubeCatNetwork
		}
	case "ConfigMap", "Secret":
		return model.KubeCatConfig
	case "PersistentVolumeClaim", "PersistentVolume", "StorageClass", "VolumeAttachment", "CSIDriver":
		return model.KubeCatStorage
	case "HorizontalPodAutoscaler", "PodDisruptionBudget", "VerticalPodAutoscaler", "ResourceQuota", "LimitRange", "PriorityClass":
		return model.KubeCatAutoscaling
	case "ServiceAccount", "Role", "ClusterRole", "RoleBinding", "ClusterRoleBinding":
		return model.KubeCatRBAC
	}
	if !IsBuiltin(apiVersion) {
		return model.KubeCatCustom
	}
	return model.KubeCatCluster
}

// podSpec returns the pod spec of a workload object (nil otherwise) and
// the keys leading to it.
func podSpec(o *Object) (map[string]any, []string) {
	var keys []string
	switch o.Kind {
	case "Pod":
		keys = []string{"spec"}
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "ReplicationController":
		keys = []string{"spec", "template", "spec"}
	case "CronJob":
		keys = []string{"spec", "jobTemplate", "spec", "template", "spec"}
	default:
		return nil, nil
	}
	m, _ := dig(o.Obj, keys...).(map[string]any)
	return m, keys
}

// PodTemplateLabels are the labels of a workload's pods.
func PodTemplateLabels(o *Object) map[string]string {
	switch o.Kind {
	case "Pod":
		return strMap(dig(o.Obj, "metadata", "labels"))
	case "CronJob":
		return strMap(dig(o.Obj, "spec", "jobTemplate", "spec", "template", "metadata", "labels"))
	}
	return strMap(dig(o.Obj, "spec", "template", "metadata", "labels"))
}

// PodSpec exposes podSpec for other packages (rules, runtime).
func PodSpec(o *Object) map[string]any {
	m, _ := podSpec(o)
	return m
}

// PodSpecPath is the dotted path of a workload's pod spec ("" for other
// kinds): "spec.template.spec".
func PodSpecPath(o *Object) string {
	_, keys := podSpec(o)
	return strings.Join(keys, ".")
}

func strMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = fmt.Sprint(val)
	}
	return out
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case uint64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		var i int
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil && fmt.Sprint(i) == n {
			return i, true
		}
	}
	return 0, false
}

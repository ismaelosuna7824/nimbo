package kube

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/loader/archive"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"
	"helm.sh/helm/v4/pkg/ignore"
)

// helmRender is what a Helm source rendered with, for its source map.
type helmRender struct {
	chartDir string
	files    []*archive.BufferedFile
	// valuesFiles are the user values files in merge order (workflow
	// paths); the chart's values.yaml comes first when it exists.
	valuesFiles []string
	vals        map[string]any // merged user values
	release     string
	namespace   string
	// templates is the rendered text of every template (chart-relative
	// names, "templates/x.yaml", "charts/sub/templates/y.yaml").
	templates map[string]string
	// chartName is the top chart's name (render output prefix).
	chartName string
	// subFiles maps a subchart path ("charts/redis") to the workflow file
	// holding it when it is an archive ("<chart>/charts/redis-1.2.3.tgz").
	subArchives map[string]string
	// thirdParty are the enabled dependencies with a remote repository by
	// subchart path ("charts/redis"): their objects are grouped on the
	// canvas.
	thirdParty map[string]thirdPartyChart
}

// thirdPartyChart is a vendored dependency pulled from a chart repository.
type thirdPartyChart struct {
	name, version, appVersion, repository string
}

// remoteRepository reports a dependency repository Helm downloads from
// (https, oci, "@name"/"alias:name" repo references), as opposed to a
// local subchart ("" or file://).
func remoteRepository(repo string) bool {
	return repo != "" && !strings.HasPrefix(repo, "file://")
}

// thirdPartyCharts finds the remote dependencies of a chart (meta, as
// loaded) that render: the enabled subcharts of rendered (the same chart
// with its dependencies processed, renamed to their aliases).
func thirdPartyCharts(meta, rendered *chart.Chart) map[string]thirdPartyChart {
	out := map[string]thirdPartyChart{}
	for _, d := range meta.Metadata.Dependencies {
		if !remoteRepository(d.Repository) {
			continue
		}
		key := d.Name
		if d.Alias != "" {
			key = d.Alias
		}
		for _, sub := range rendered.Dependencies() {
			if sub.Name() == key {
				out["charts/"+key] = thirdPartyChart{name: d.Name, version: sub.Metadata.Version, appVersion: sub.Metadata.AppVersion, repository: d.Repository}
			}
		}
	}
	return out
}

// thirdPartyOf returns the subchart path of the remote dependency that
// renders a template ("charts/redis" for "charts/redis/templates/x.yaml").
func (h *helmRender) thirdPartyOf(template string) (string, bool) {
	for sub := range h.thirdParty {
		if strings.HasPrefix(template, sub+"/") {
			return sub, true
		}
	}
	return "", false
}

// chartFiles reads the files of the chart in dir, honoring .helmignore.
func (p *Project) chartFiles(dir string) ([]*archive.BufferedFile, error) {
	rules := ignore.Empty()
	if data, err := p.fs.read(path.Join(dir, ".helmignore")); err == nil {
		r, err := ignore.Parse(bytes.NewReader(data))
		if err != nil {
			return nil, &locatedError{file: path.Join(dir, ".helmignore"), err: err}
		}
		rules = r
	}
	rules.AddDefaults()
	var out []*archive.BufferedFile
	for _, f := range p.fs.under(dir) {
		rel := strings.TrimPrefix(f, dir+"/")
		if dir == "." {
			rel = f
		}
		if rules.Ignore(rel, fileInfo{name: path.Base(rel)}) || ignoredParent(rules, rel) {
			continue
		}
		data, err := p.fs.read(f)
		if err != nil {
			continue
		}
		out = append(out, &archive.BufferedFile{Name: rel, Data: data})
	}
	return out, nil
}

// ignoredParent reports whether a directory above rel is ignored.
func ignoredParent(rules *ignore.Rules, rel string) bool {
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		if rules.Ignore(strings.Join(parts[:i], "/"), fileInfo{name: parts[i-1], dir: true}) {
			return true
		}
	}
	return false
}

// fileInfo is the os.FileInfo .helmignore rules need.
type fileInfo struct {
	name string
	dir  bool
}

func (f fileInfo) Name() string { return f.name }
func (f fileInfo) Size() int64  { return 0 }
func (f fileInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir
	}
	return 0
}
func (f fileInfo) ModTime() time.Time { return time.Time{} }
func (f fileInfo) IsDir() bool        { return f.dir }
func (f fileInfo) Sys() any           { return nil }

// helmValuesFiles are the values files of a source for env, in merge
// order: ValuesFiles then the environment's file.
func (p *Project) helmValuesFiles(src model.KubeSource, env string) (files []string, envFile string) {
	for _, f := range src.ValuesFiles {
		files = append(files, cleanRel(f))
	}
	if env != "" {
		if e := p.envPaths(src)[env]; e != "" {
			envFile = cleanRel(e)
			if !slices.Contains(files, envFile) {
				files = append(files, envFile)
			}
		}
	}
	return files, envFile
}

// userValues merges values files like `helm -f a -f b`.
func (p *Project) userValues(files []string) (map[string]any, error) {
	vals := map[string]any{}
	for _, f := range files {
		data, err := p.fs.read(f)
		if err != nil {
			return nil, fmt.Errorf("values file %s: %w", f, err)
		}
		v, err := loader.LoadValues(bytes.NewReader(data))
		if err != nil {
			return nil, &locatedError{file: f, line: yamlErrLine(err), err: err}
		}
		vals = loader.MergeMaps(vals, v)
	}
	return vals, nil
}

var yamlLineNo = regexp.MustCompile(`line (\d+)`)

func yamlErrLine(err error) int {
	if m := yamlLineNo.FindStringSubmatch(err.Error()); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// HelmChart loads a Helm source's chart and its merged user values for
// env (what `helm upgrade --install -f ...` takes). dir is the workflow
// files' directory.
func HelmChart(dir string, src model.KubeSource, env string) (*chart.Chart, map[string]any, error) {
	p := &Project{Dir: dir, fs: newFiles(dir, nil)}
	chartDir := cleanRel(src.Path)
	files, err := p.chartFiles(chartDir)
	if err != nil {
		return nil, nil, err
	}
	c, err := loader.LoadFiles(files)
	if err != nil {
		return nil, nil, err
	}
	vf, _ := p.helmValuesFiles(src, env)
	vals, err := p.userValues(vf)
	if err != nil {
		return nil, nil, err
	}
	return c, vals, nil
}

// ReleaseName is a Helm source's release name (the chart name when unset).
func ReleaseName(src model.KubeSource, chartName string) string {
	if src.Release != "" {
		return src.Release
	}
	if chartName != "" {
		return chartName
	}
	return path.Base(cleanRel(src.Path))
}

// helmTemplate renders a chart like `helm template` (client only, pinned
// Kubernetes version): the rendered text of every template by
// chart-relative name.
func helmTemplate(files []*archive.BufferedFile, vals map[string]any, release, namespace string) (*chart.Chart, map[string]string, error) {
	c, err := loader.LoadFiles(files)
	if err != nil {
		return nil, nil, err
	}
	if err := chartutil.ProcessDependencies(c, vals); err != nil {
		return c, nil, err
	}
	caps := common.DefaultCapabilities.Copy()
	if kv, err := common.ParseKubeVersion(KubeVersion); err == nil {
		caps.KubeVersion = *kv
	}
	opts := common.ReleaseOptions{Name: release, Namespace: namespace, Revision: 1, IsInstall: true}
	values, err := util.ToRenderValuesWithSchemaValidation(c, vals, opts, caps, false)
	if err != nil {
		return c, nil, err
	}
	out, err := engine.Render(c, values)
	if err != nil {
		return c, nil, err
	}
	prefix := c.Name() + "/"
	tpls := make(map[string]string, len(out))
	for name, text := range out {
		tpls[strings.TrimPrefix(name, prefix)] = text
	}
	return c, tpls, nil
}

// renderHelm renders a Helm source for env.
func (p *Project) renderHelm(r *Rendered, env string) {
	dir := cleanRel(r.Source.Path)
	if p.fs.exists(path.Join(dir, "Chart.yaml")) == false {
		r.Err = &locatedError{file: dir, err: fmt.Errorf("no Chart.yaml in %s", dir)}
		return
	}
	files, err := p.chartFiles(dir)
	if err != nil {
		r.Err = err
		return
	}
	for _, f := range files {
		r.Files = append(r.Files, joinRel(dir, f.Name))
	}
	vf, envFile := p.helmValuesFiles(r.Source, env)
	r.Env = envFile
	for _, f := range vf {
		if !slices.Contains(r.Files, f) {
			r.Files = append(r.Files, f)
		}
	}
	slices.Sort(r.Files)
	vals, err := p.userValues(vf)
	if err != nil {
		r.Err = err
		return
	}
	meta, err := loader.LoadFiles(files)
	if err != nil {
		r.Err = &locatedError{file: path.Join(dir, "Chart.yaml"), err: err}
		return
	}
	release := ReleaseName(r.Source, meta.Name())
	ns := r.Source.Namespace
	if ns == "" {
		ns = "default"
	}
	h := &helmRender{chartDir: dir, files: files, vals: vals, release: release, namespace: ns, chartName: meta.Name(), subArchives: map[string]string{}}
	if p.fs.exists(path.Join(dir, "values.yaml")) {
		h.valuesFiles = append(h.valuesFiles, path.Join(dir, "values.yaml"))
	}
	h.valuesFiles = append(h.valuesFiles, vf...)
	for _, f := range files {
		if strings.HasPrefix(f.Name, "charts/") && strings.HasSuffix(f.Name, ".tgz") {
			if sub, err := loader.LoadArchive(bytes.NewReader(f.Data)); err == nil {
				h.subArchives["charts/"+sub.Name()] = joinRel(dir, f.Name)
			}
		}
	}
	r.helm = h
	// Missing dependencies: helm template refuses the chart; Nimbo
	// renders the rest and says how to vendor them.
	vendored := map[string]bool{}
	for _, d := range meta.Dependencies() {
		vendored[d.Name()] = true
	}
	for _, d := range meta.Metadata.Dependencies {
		if !vendored[d.Name] {
			p.diag(model.SeverityError, "k8s_dependency", path.Join(dir, "Chart.yaml"), chartDepLine(p, dir, d.Name),
				fmt.Sprintf("chart dependency %s %s (%s) is not in %s/charts: it is left out of the graph and the deploy fails", d.Name, d.Version, d.Repository, dir),
				"Vendor it: run `helm dependency build "+dir+"` (or `helm pull` it into charts/) and keep the files in the workflow.")
		}
	}
	c, tpls, err := helmTemplate(files, vals, release, ns)
	if err != nil {
		r.Err = helmError(err, dir, h)
		return
	}
	h.templates = tpls
	h.thirdParty = thirdPartyCharts(meta, c)
	var out []byte
	// CRDs first, like helm install.
	for _, crd := range c.CRDObjects() {
		name := strings.TrimPrefix(crd.Filename, c.Name()+"/")
		f := ParseFile(h.workflowFile(name), crd.File.Data)
		objs, _ := objectsOf(f)
		for _, o := range objs {
			o.Source, o.Template, o.Doc = r.Index, name, -1
			r.Objects = append(r.Objects, o)
		}
		out = appendDocs(out, crd.File.Data)
	}
	names := make([]string, 0, len(tpls))
	for name := range tpls {
		base := path.Base(name)
		if strings.HasPrefix(base, "_") || strings.EqualFold(base, "NOTES.txt") || !strings.Contains(name, "templates/") {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		text := tpls[name]
		if strings.TrimSpace(text) == "" {
			continue
		}
		file := h.workflowFile(name)
		f := ParseFile(file, helmDocs(text))
		for _, se := range f.Errors() {
			r.Err = &locatedError{file: file, err: fmt.Errorf("the template renders invalid YAML (rendered line %d): %s", se.Line, se.Message)}
			return
		}
		objs, _ := objectsOf(f)
		kindSeen := map[string]int{}
		var kept []byte
		for i, o := range objs {
			n := kindSeen[o.Kind]
			kindSeen[o.Kind]++
			if isTestHook(o) {
				continue
			}
			o.Source, o.Template, o.Doc = r.Index, name, -1
			o.matchKey, o.tplIndex = matchKey(name, o.Kind, n), i
			o.Line = templateKindLine(p, file, o.Kind, n)
			r.Objects = append(r.Objects, o)
			if o.Node != nil {
				if b, err := yaml.Marshal(o.Node); err == nil {
					kept = appendDocs(kept, append([]byte("# Source: "+c.Name()+"/"+name+"\n"), b...))
				}
			}
		}
		if len(kept) > 0 {
			out = appendDocs(out, kept)
		}
	}
	r.YAML = out
}

// workflowFile maps a chart-relative name to the workflow file holding
// it (a subchart archive when the subchart is packaged).
func (h *helmRender) workflowFile(name string) string {
	for sub, archive := range h.subArchives {
		if strings.HasPrefix(name, sub+"/") {
			return archive
		}
	}
	return joinRel(h.chartDir, name)
}

func joinRel(dir, name string) string {
	if dir == "." {
		return name
	}
	return dir + "/" + name
}

func isTestHook(o *Object) bool {
	hook := str(o.Obj, "metadata", "annotations", "helm.sh/hook")
	for h := range strings.SplitSeq(hook, ",") {
		if strings.HasPrefix(strings.TrimSpace(h), "test") {
			return true
		}
	}
	return false
}

// templateKindLine finds the line of the n-th "kind: <kind>" of a
// template (1 when it can't be found).
func templateKindLine(p *Project, file, kind string, n int) int {
	data, err := p.fs.read(file)
	if err != nil {
		return 1
	}
	re := regexp.MustCompile(`^\s*kind:\s*["']?` + regexp.QuoteMeta(kind) + `["']?\s*(#.*)?$`)
	seen := 0
	for i, l := range strings.Split(string(data), "\n") {
		if re.MatchString(l) {
			if seen == n {
				return i + 1
			}
			seen++
		}
	}
	return 1
}

func chartDepLine(p *Project, dir, name string) int {
	data, err := p.fs.read(path.Join(dir, "Chart.yaml"))
	if err != nil {
		return 0
	}
	re := regexp.MustCompile(`^\s*-?\s*name:\s*["']?` + regexp.QuoteMeta(name) + `["']?\s*$`)
	for i, l := range strings.Split(string(data), "\n") {
		if re.MatchString(l) {
			return i + 1
		}
	}
	return 0
}

// helmTplErr matches "template: chart/templates/x.yaml:12:5: ..." and
// "parse error at (chart/templates/x.yaml:12): ...".
var helmTplErr = regexp.MustCompile(`([\w.\-]+/[\w./\-]+\.(?:yaml|yml|tpl|txt|json)):(\d+)`)

func helmError(err error, dir string, h *helmRender) error {
	msg := err.Error()
	if m := helmTplErr.FindStringSubmatch(msg); m != nil {
		name := strings.TrimPrefix(m[1], h.chartName+"/")
		line, _ := strconv.Atoi(m[2])
		return &locatedError{file: h.workflowFile(name), line: line, err: err}
	}
	if strings.Contains(msg, "values don't meet the specifications of the schema") {
		return &locatedError{file: path.Join(dir, "values.schema.json"), err: err}
	}
	return &locatedError{file: path.Join(dir, "Chart.yaml"), err: err}
}

// helmValuesFilesConv finds the conventional values files of a chart:
// values-<env>.yaml, values.<env>.yaml, values_<env>.yaml (and .yml) and
// values/<env>.yaml.
var valuesEnvRe = regexp.MustCompile(`^values[-._]([A-Za-z0-9][\w-]*)\.ya?ml$`)

func (p *Project) helmEnvFiles(dir string) map[string]string {
	out := map[string]string{}
	for _, f := range p.fs.under(dir) {
		rel := strings.TrimPrefix(f, dir+"/")
		if dir == "." {
			rel = f
		}
		if m := valuesEnvRe.FindStringSubmatch(rel); m != nil && m[1] != "schema" {
			out[m[1]] = f
		}
		if d, file, ok := strings.Cut(rel, "/"); ok && d == "values" && !strings.Contains(file, "/") {
			if env, ok := strings.CutSuffix(file, ".yaml"); ok {
				out[env] = f
			} else if env, ok := strings.CutSuffix(file, ".yml"); ok {
				out[env] = f
			}
		}
	}
	return out
}

// chartFacts describes a Helm source's chart.
func (p *Project) chartFacts(r *Rendered) *model.KubeChart {
	if r.helm == nil {
		return nil
	}
	c, err := loader.LoadFiles(r.helm.files)
	if err != nil || c.Metadata == nil {
		return nil
	}
	out := &model.KubeChart{Name: c.Metadata.Name, Version: c.Metadata.Version, AppVersion: c.Metadata.AppVersion, Description: c.Metadata.Description, Icon: c.Metadata.Icon}
	vendored := map[string]bool{}
	for _, d := range c.Dependencies() {
		vendored[d.Name()] = true
	}
	enabled := map[string]bool{}
	if cp, err := loader.LoadFiles(r.helm.files); err == nil {
		if chartutil.ProcessDependencies(cp, r.helm.vals) == nil {
			for _, d := range cp.Metadata.Dependencies {
				enabled[d.Name] = d.Enabled
			}
		}
	}
	for _, d := range c.Metadata.Dependencies {
		out.Dependencies = append(out.Dependencies, model.KubeChartDependency{
			Name: d.Name, Version: d.Version, Repository: d.Repository, Alias: d.Alias, Condition: d.Condition,
			Vendored: vendored[d.Name], Enabled: enabled[d.Name],
		})
	}
	return out
}

// helmSep is how Helm splits rendered templates into documents: "---"
// after a line break, whatever follows it on the line ("---apiVersion"
// when a template trims the newline after the separator).
var helmSep = regexp.MustCompile(`(?:^|\s*\n)---\s*`)

// helmDocs rewrites a rendered template with one "---" line between
// documents, the way Helm splits it.
func helmDocs(text string) []byte {
	parts := helmSep.Split(text, -1)
	var b strings.Builder
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(part)
	}
	return []byte(b.String())
}

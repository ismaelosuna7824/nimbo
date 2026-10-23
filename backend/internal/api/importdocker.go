package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Docker projects found by the import scan: every folder with a compose
// file (one workflow per compose project: compose.yaml plus its override,
// as docker compose reads them), every folder with a Dockerfile that no
// compose project covers (imported with a generated compose.yaml) and, for
// sibling folders holding such Dockerfiles, their group as one workflow with
// one service per folder.
//
// A compose project may reference files outside its folder (repo/docker/
// compose.yaml building ../services/api, env_file ../config/.env). They are
// imported too and the workflow's root becomes the common ancestor of the
// folder and everything it references: the compose files keep their
// relative place (Compose.Files = docker/compose.yaml) and stay
// byte-identical, docker compose's project directory being the first
// compose file's folder, and export writes the same tree back. Rewriting the
// paths instead would change the user's files and break their own
// `docker compose up` from the exported folder.

// dockerPrefix marks the docker project of a folder that is also a
// Terraform project ("docker:infra").
const dockerPrefix = "docker:"

// dockerGroupPrefix marks a group of sibling Dockerfile folders
// ("dockerfiles:services").
const dockerGroupPrefix = "dockerfiles:"

// maxDockerImportBytes caps the files one docker workflow imports.
const maxDockerImportBytes = 100 << 20

// genericComposeDirs are folder names that say where compose files live, not
// what the project is: a workflow rooted above one is named after its root.
var genericComposeDirs = []string{"docker", "compose", "deploy", "deployment", "deployments", "ops", "devops", "infra", "containers"}

type dockerProject struct {
	dir     string   // scan-relative: the compose folder, a Dockerfile's folder or a group's parent
	files   []string // compose files in -f order, relative to dir; empty: a compose.yaml is generated
	members []string // a Dockerfile group: the sibling folders (scan-relative), one service each
}

// key is the project path of the scan: the folder, prefixed when the
// folder is a Terraform project too or for a group.
func (dp dockerProject) key(rs *repoScan) string {
	if len(dp.members) > 0 {
		return dockerGroupPrefix + dp.dir
	}
	if rs.dirs[dp.dir] != nil || rs.groups[dp.dir] != nil {
		return dockerPrefix + dp.dir
	}
	return dp.dir
}

// dockerProjects lists the compose projects, the Dockerfile groups and the
// lone Dockerfiles. A Dockerfile a compose project builds (even from
// outside its folder) is not a project of its own.
func (rs *repoScan) dockerProjects() []dockerProject {
	var out []dockerProject
	composeDirs := slices.Sorted(maps.Keys(rs.composeFiles))
	built := map[string]bool{} // scan-relative folders compose projects build
	for _, dir := range composeDirs {
		names := rs.composeFiles[dir]
		files := compose.DefaultFiles(func(n string) bool { return slices.Contains(names, n) })
		if len(files) == 0 {
			sorted := slices.Sorted(slices.Values(names))
			files = sorted[:1]
		}
		dp := dockerProject{dir: dir, files: files}
		out = append(out, dp)
		l := rs.composeLayout(dp)
		for _, r := range l.project.References() {
			switch r.Kind {
			case compose.RefContext:
				built[relSlash(rs.root, l.abs(r.Path))] = true
			case compose.RefDockerfile:
				built[path.Dir(relSlash(rs.root, l.abs(r.Path)))] = true
			}
		}
	}
	covered := func(dir string) bool {
		if built[dir] {
			return true
		}
		for _, cd := range composeDirs {
			if cd == "." || dir == cd || strings.HasPrefix(dir, cd+"/") {
				return true
			}
		}
		return false
	}
	var lone []string
	for _, dir := range slices.Sorted(maps.Keys(rs.dockerfiles)) {
		if !covered(dir) {
			lone = append(lone, dir)
		}
	}
	siblings := map[string][]string{}
	for _, d := range lone {
		if d != "." {
			siblings[path.Dir(d)] = append(siblings[path.Dir(d)], d)
		}
	}
	for _, d := range lone {
		// The group comes right before its first member.
		if parent := path.Dir(d); d != "." && len(siblings[parent]) > 1 && siblings[parent][0] == d {
			out = append(out, dockerProject{dir: parent, members: siblings[parent]})
		}
		out = append(out, dockerProject{dir: d})
	}
	return out
}

// dockerName suggests a workflow name: the folder's (the scanned folder's
// for the root; the workflow root's when the compose files sit in a folder
// such as docker/ and reference its siblings), with "-docker" when a
// Terraform project has that name.
func (rs *repoScan) dockerName(dp dockerProject, tfNames map[string]string) string {
	folderName := func(dir string) string {
		switch {
		case dir == ".":
			return rs.rootName
		case dir == ".." || strings.HasPrefix(dir, "../"):
			return filepath.Base(filepath.Join(rs.root, filepath.FromSlash(dir)))
		}
		return path.Base(dir)
	}
	name := folderName(dp.dir)
	if len(dp.files) > 0 && len(dp.members) == 0 && slices.Contains(genericComposeDirs, strings.ToLower(name)) {
		l := rs.composeLayout(dp)
		if l.base != l.sub {
			name = folderName(relSlash(rs.root, l.abs(l.base)))
		}
	}
	for _, n := range tfNames {
		if n == name {
			return name + "-docker"
		}
	}
	return name
}

// dockerSummary describes a docker project for the scan result.
func (rs *repoScan) dockerSummary(dp dockerProject, tfNames map[string]string) model.ImportProject {
	ip := model.ImportProject{
		Path: dp.key(rs), Name: rs.dockerName(dp, tfNames), Kind: model.WorkflowDocker, Layout: model.LayoutSingle,
		ComposeFiles: dp.files, Providers: []string{}, Clouds: []model.Cloud{}, TfvarsFiles: []string{}, Diagnostics: []model.Diagnostic{},
	}
	switch {
	case len(dp.members) > 0:
		ip.Services = len(dp.members)
		for _, m := range dp.members {
			ip.Members = append(ip.Members, dockerProject{dir: m}.key(rs))
			ip.Dockerfiles = append(ip.Dockerfiles, path.Join(relTo(dp.dir, m), primaryDockerfile(rs.dockerfiles[m])))
		}
	case len(dp.files) == 0:
		ip.Services = 1
		ip.Dockerfiles = slices.Clone(rs.dockerfiles[dp.dir])
	default:
		// As the workflow will read it: opened from its root, so file names
		// in the problems are the workflow's.
		l := rs.composeLayout(dp)
		sub := slashRel(l.base, l.sub)
		files := make([]string, len(dp.files))
		for i, f := range dp.files {
			files[i] = path.Join(sub, f)
		}
		p := compose.Open(context.Background(), l.abs(l.base), files, nil, compose.Options{})
		if p.Model != nil {
			ip.Services = len(p.Model.Services)
		}
		for df := range p.Dockerfiles {
			ip.Dockerfiles = append(ip.Dockerfiles, slashRel(sub, df))
		}
		slices.Sort(ip.Dockerfiles)
		for _, d := range p.Diagnostics {
			if d.Severity == model.SeverityError {
				ip.Diagnostics = append(ip.Diagnostics, d)
			}
		}
	}
	t, err := rs.dockerTree(dp)
	if err == nil {
		ip.Outside, ip.Bytes, ip.FileCount = t.outside, t.bytes, len(t.files)
		ip.Diagnostics = append(ip.Diagnostics, t.diags...)
		err = t.tooLarge()
	}
	if err != nil {
		ip.Diagnostics = append(ip.Diagnostics, model.Diagnostic{Severity: model.SeverityError, Code: "docker_import_size", Message: err.Error()})
	}
	return ip
}

// planDocker reads the files of a docker project (see dockerTree) into a
// new docker workflow.
func (rs *repoScan) planDocker(dp dockerProject, name string) (*importPlan, error) {
	t, err := rs.dockerTree(dp)
	if err != nil {
		return nil, err
	}
	if err := t.tooLarge(); err != nil {
		return nil, err
	}
	plan := &importPlan{project: dp.dir, files: make(map[string][]byte, len(t.files)+1), diags: t.diags}
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
	var files []string
	switch {
	case len(dp.members) > 0:
		var svcs []generatedService
		for _, m := range dp.members {
			ctx := relTo(dp.dir, m)
			df := primaryDockerfile(rs.dockerfiles[m])
			svcs = append(svcs, generatedService{name: path.Base(m), context: ctx, dockerfile: df, src: plan.files[path.Join(ctx, df)]})
		}
		files = []string{compose.DefaultFile}
		plan.files[compose.DefaultFile] = generatedCompose(svcs)
	case len(dp.files) == 0:
		df := primaryDockerfile(rs.dockerfiles[dp.dir])
		files = []string{compose.DefaultFile}
		plan.files[compose.DefaultFile] = generatedCompose([]generatedService{{name: name, context: ".", dockerfile: df, src: plan.files[df]}})
	default:
		for _, f := range dp.files {
			files = append(files, path.Join(t.sub, f))
		}
	}
	plan.wf = &model.Workflow{Name: name, Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Files: files}}
	return plan, nil
}

// ---------------------------------------------------------------------------
// Compose projects and what they reference

// composeLayout is a compose project opened from the scan's limit folder, so
// that its references above its own folder resolve, and the part of the
// limit folder the workflow keeps.
type composeLayout struct {
	project *compose.Project
	limit   string // absolute
	sub     string // the compose folder, relative to limit
	base    string // the workflow's root, relative to limit: sub or an ancestor
	refs    []dockerRef
	diags   []model.Diagnostic
}

// dockerRef is a folder or file outside the compose folder that the
// project references.
type dockerRef struct {
	path        string // relative to limit
	dir         bool
	kinds       []string
	dockerfiles []string // build contexts: the Dockerfiles built from it (relative to limit)
}

func (l *composeLayout) abs(rel string) string {
	return filepath.Join(l.limit, filepath.FromSlash(rel))
}

// fromSub is rel (relative to limit) as the compose files reach it: relative
// to the compose folder.
func (l *composeLayout) fromSub(rel string) string {
	return slashRel(l.sub, rel)
}

func (rs *repoScan) composeLayout(dp dockerProject) *composeLayout {
	if l := rs.layouts[dp.dir]; l != nil {
		return l
	}
	l := &composeLayout{limit: rs.limit, sub: relSlash(rs.limit, filepath.Join(rs.root, filepath.FromSlash(dp.dir)))}
	files := make([]string, len(dp.files))
	for i, f := range dp.files {
		files[i] = path.Join(l.sub, f)
	}
	l.project = compose.Open(context.Background(), l.limit, files, nil, compose.Options{})
	l.collectRefs()
	l.base = l.sub
	for _, r := range l.refs {
		l.base = commonDir(l.base, r.path)
	}
	rs.layouts[dp.dir] = l
	return l
}

// collectRefs keeps the references outside the compose folder that exist
// and can be imported; the others get a warning (bind mounts of host paths
// such as /var/run/docker.sock are left alone silently).
func (l *composeLayout) collectRefs() {
	byPath := map[string]*dockerRef{}
	warn := func(r compose.Reference, why string) {
		if r.Kind == compose.RefBind {
			return
		}
		of := ""
		if r.Service != "" {
			of = " of service " + r.Service
		}
		l.diags = append(l.diags, warning("docker_ref_not_imported",
			fmt.Sprintf("%s %s%s %s: not imported, so the workflow can't use it", r.Kind, l.fromSub(r.Path), of, why)))
	}
	for _, r := range l.project.References() {
		p := r.Path
		if p == ".." || strings.HasPrefix(p, "../") || filepath.IsAbs(p) {
			warn(r, "is outside "+filepath.Base(l.limit))
			continue
		}
		if l.sub == "." || p == l.sub || strings.HasPrefix(p, l.sub+"/") {
			continue // copied with the compose folder
		}
		fi, err := os.Stat(l.abs(p))
		if err != nil || !fi.IsDir() && !fi.Mode().IsRegular() {
			continue // a missing one is reported by the compose checks
		}
		dirs := strings.Split(p, "/")
		if !fi.IsDir() {
			dirs = dirs[:len(dirs)-1]
		}
		if slices.ContainsFunc(dirs, skipScanDir) {
			warn(r, "is in a hidden or dependency folder")
			continue
		}
		ref := byPath[p]
		if ref == nil {
			ref = &dockerRef{path: p, dir: fi.IsDir()}
			byPath[p] = ref
		}
		if !slices.Contains(ref.kinds, r.Kind) {
			ref.kinds = append(ref.kinds, r.Kind)
		}
		if r.Kind == compose.RefContext && r.Dockerfile != "" && !slices.Contains(ref.dockerfiles, r.Dockerfile) {
			ref.dockerfiles = append(ref.dockerfiles, r.Dockerfile)
		}
	}
	for _, p := range slices.Sorted(maps.Keys(byPath)) {
		l.refs = append(l.refs, *byPath[p])
	}
}

// ---------------------------------------------------------------------------
// Files

// dockerTree is what a docker project imports.
type dockerTree struct {
	base    string              // absolute: the workflow's root
	sub     string              // the project folder relative to base ("." when the same)
	files   map[string]treeFile // by base-relative slash path
	bytes   int64
	sizes   map[string]int64 // bytes under every folder (base-relative)
	outside []model.ImportOutside
	diags   []model.Diagnostic
}

type treeFile struct {
	abs  string
	size int64
	exec bool // an executable bit is set (kept: COPY keeps modes)
}

// dockerTree lists a docker project's files: its folder (dependency, VCS
// and hidden folders left out) and, for a compose project, every folder or
// file it references outside it; build contexts leave out what their
// .dockerignore excludes and regenerated build output. A Dockerfile group
// takes its member folders, each a build context.
func (rs *repoScan) dockerTree(dp dockerProject) (*dockerTree, error) {
	dir := filepath.Join(rs.root, filepath.FromSlash(dp.dir))
	t := &dockerTree{base: dir, sub: ".", files: map[string]treeFile{}, sizes: map[string]int64{}}
	if len(dp.members) > 0 {
		for _, m := range dp.members {
			ctx := filepath.Join(rs.root, filepath.FromSlash(m))
			if err := t.walk(ctx, newContextFilter(ctx, []string{filepath.Join(ctx, primaryDockerfile(rs.dockerfiles[m]))})); err != nil {
				return nil, err
			}
		}
		return t, nil
	}
	var l *composeLayout
	if len(dp.files) > 0 {
		l = rs.composeLayout(dp)
		t.base = l.abs(l.base)
		t.sub = slashRel(l.base, l.sub)
		t.diags = slices.Clone(l.diags)
	}
	if err := t.walk(dir, nil); err != nil {
		return nil, err
	}
	if l == nil {
		return t, nil
	}
	for _, r := range l.refs {
		abs := l.abs(r.path)
		if !r.dir {
			if fi, err := os.Stat(abs); err == nil {
				t.add(abs, fi)
			}
			continue
		}
		var f *contextFilter
		if slices.Contains(r.kinds, compose.RefContext) {
			dfs := make([]string, len(r.dockerfiles))
			for i, df := range r.dockerfiles {
				dfs[i] = l.abs(df)
			}
			f = newContextFilter(abs, dfs)
		}
		if err := t.walk(abs, f); err != nil {
			return nil, err
		}
	}
	// One entry per outermost folder or file, sized by what it brings.
	for _, r := range l.refs {
		if slices.ContainsFunc(l.refs, func(o dockerRef) bool { return o.dir && o.path != r.path && strings.HasPrefix(r.path, o.path+"/") }) {
			continue
		}
		rel := slashRel(l.base, r.path)
		o := model.ImportOutside{Path: l.fromSub(r.path), Dir: r.dir, Kinds: slices.Clone(r.kinds)}
		for _, inner := range l.refs {
			if strings.HasPrefix(inner.path, r.path+"/") {
				for _, k := range inner.kinds {
					if !slices.Contains(o.Kinds, k) {
						o.Kinds = append(o.Kinds, k)
					}
				}
			}
		}
		for p, tf := range t.files {
			if p == rel || strings.HasPrefix(p, rel+"/") || rel == "." {
				o.Bytes += tf.size
				o.Files++
			}
		}
		t.outside = append(t.outside, o)
	}
	return t, nil
}

// walk adds the regular files under dir (absolute); f filters a build
// context, nil keeps every file outside skipped folders.
func (t *dockerTree) walk(dir string, f *contextFilter) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			t.diags = append(t.diags, warning("docker_unreadable", fmt.Sprintf("%s is unreadable (%v): not imported", t.show(p), trimErr(err))))
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != dir && (skipScanDir(d.Name()) || f.skipDir(p)) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || skipImport(d.Name()) || f.skipFile(p) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		t.add(p, fi)
		return nil
	})
}

func (t *dockerTree) add(abs string, fi fs.FileInfo) {
	rel := relSlash(t.base, abs)
	if _, dup := t.files[rel]; dup {
		return
	}
	t.files[rel] = treeFile{abs: abs, size: fi.Size(), exec: fi.Mode().Perm()&0o111 != 0}
	t.bytes += fi.Size()
	for d := path.Dir(rel); ; d = path.Dir(d) {
		t.sizes[d] += fi.Size()
		if d == "." {
			break
		}
	}
}

// show is a base-relative or absolute path as the project folder reaches it.
func (t *dockerTree) show(p string) string {
	if filepath.IsAbs(p) {
		p = relSlash(t.base, p)
	}
	return slashRel(t.sub, p)
}

// tooLarge reports a tree over the import limits, naming its biggest
// folders.
func (t *dockerTree) tooLarge() error {
	if t.bytes <= maxDockerImportBytes && len(t.files) <= maxScanFiles {
		return nil
	}
	// A folder that is mostly one subfolder is named by that subfolder.
	biggestChild := map[string]int64{}
	for d, n := range t.sizes {
		if d != "." {
			biggestChild[path.Dir(d)] = max(biggestChild[path.Dir(d)], n)
		}
	}
	var cands []string
	for d, n := range t.sizes {
		if d != "." && biggestChild[d]*2 < n {
			cands = append(cands, d)
		}
	}
	slices.SortFunc(cands, func(a, b string) int { return cmp.Or(cmp.Compare(t.sizes[b], t.sizes[a]), strings.Compare(a, b)) })
	var biggest []string
	var chosen []string
	for _, d := range cands {
		if len(chosen) == 5 {
			break
		}
		if slices.ContainsFunc(chosen, func(c string) bool { return strings.HasPrefix(d, c+"/") || strings.HasPrefix(c, d+"/") }) {
			continue
		}
		chosen = append(chosen, d)
		biggest = append(biggest, fmt.Sprintf("%s (%s)", t.show(d), sizeLabel(t.sizes[d])))
	}
	return errorf(http.StatusBadRequest, "the project would import %s in %d files, over the limit of %s and %d files; biggest folders: %s. Leave them out with a .dockerignore in the build context, or import a narrower project",
		sizeLabel(t.bytes), len(t.files), sizeLabel(maxDockerImportBytes), maxScanFiles, strings.Join(biggest, ", "))
}

// contextFilter leaves out of a build context what docker build does not
// send (its .dockerignore, or <Dockerfile>.dockerignore next to the
// Dockerfile) and the build output the image rebuilds (Rust/Maven target,
// .NET bin/obj, Go bin, Node dist, __pycache__) unless a COPY/ADD of its
// Dockerfiles names it.
type contextFilter struct {
	root     string // absolute
	keep     map[string]bool
	ignore   []*patternmatcher.PatternMatcher // a path is left out when every one matches
	exclude  bool                             // some pattern re-includes (!x): never skip a folder whole
	copySrcs []string                         // COPY/ADD sources, relative to the context
}

func newContextFilter(root string, dockerfiles []string) *contextFilter {
	f := &contextFilter{root: root, keep: map[string]bool{}}
	rootIgnore := filepath.Join(root, ".dockerignore")
	ignoreFiles := map[string]bool{}
	everything := false // a Dockerfile without an ignore file gets the whole context
	pick := func(specific string) {
		switch {
		case specific != "" && fileExists(specific):
			ignoreFiles[specific] = true
		case fileExists(rootIgnore):
			ignoreFiles[rootIgnore] = true
		default:
			everything = true
		}
	}
	if len(dockerfiles) == 0 {
		pick("")
	}
	for _, df := range dockerfiles {
		f.keep[df] = true
		pick(df + ".dockerignore")
		data, err := os.ReadFile(df)
		if err != nil {
			continue
		}
		parsed, _ := compose.ParseDockerfile(data)
		for _, in := range parsed.Instructions {
			if in.Cmd != "COPY" && in.Cmd != "ADD" {
				continue
			}
			if _, from := in.Flag("from"); from {
				continue
			}
			args := in.JSON
			if args == nil {
				args = compose.Words(in.Args)
			}
			for i := 0; i+1 < len(args); i++ {
				src := args[i]
				if strings.Contains(src, "://") || strings.HasPrefix(src, "<<") || strings.Contains(src, "$") {
					continue
				}
				if src = path.Clean(strings.TrimPrefix(filepath.ToSlash(src), "/")); src != "." {
					f.copySrcs = append(f.copySrcs, src)
				}
			}
		}
	}
	for file := range ignoreFiles {
		f.keep[file] = true
	}
	if everything {
		return f
	}
	for _, file := range slices.Sorted(maps.Keys(ignoreFiles)) {
		r, err := os.Open(file)
		if err != nil {
			return f
		}
		patterns, err := ignorefile.ReadAll(r)
		r.Close()
		if err != nil {
			return f
		}
		pm, err := patternmatcher.New(patterns)
		if err != nil {
			return f
		}
		f.ignore = append(f.ignore, pm)
		f.exclude = f.exclude || pm.Exclusions()
	}
	return f
}

func (f *contextFilter) ignored(rel string) bool {
	if len(f.ignore) == 0 {
		return false
	}
	for _, pm := range f.ignore {
		if m, err := pm.MatchesOrParentMatches(filepath.FromSlash(rel)); err != nil || !m {
			return false
		}
	}
	return true
}

func (f *contextFilter) skipFile(abs string) bool {
	if f == nil || f.keep[abs] {
		return false
	}
	return f.ignored(relSlash(f.root, abs))
}

func (f *contextFilter) skipDir(abs string) bool {
	if f == nil {
		return false
	}
	rel := relSlash(f.root, abs)
	if !f.exclude && f.ignored(rel) {
		return !slices.ContainsFunc(slices.Collect(maps.Keys(f.keep)), func(k string) bool { return strings.HasPrefix(k, abs+string(filepath.Separator)) })
	}
	if !buildOutput(abs) {
		return false
	}
	return !slices.ContainsFunc(f.copySrcs, func(src string) bool {
		return src == rel || strings.HasPrefix(src, rel+"/") || strings.HasPrefix(rel, src+"/")
	})
}

// buildOutput reports a folder the stack next to it regenerates.
func buildOutput(dir string) bool {
	parent := filepath.Dir(dir)
	has := func(names ...string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return fileExists(filepath.Join(parent, n)) })
	}
	switch filepath.Base(dir) {
	case "__pycache__":
		return true
	case "target":
		return has("Cargo.toml", "pom.xml", "build.sbt")
	case "dist":
		return has("package.json")
	case "bin", "obj":
		if filepath.Base(dir) == "bin" && has("go.mod") {
			return true
		}
		for _, pattern := range []string{"*.csproj", "*.fsproj", "*.vbproj", "*.sln"} {
			if m, _ := filepath.Glob(filepath.Join(parent, pattern)); len(m) > 0 {
				return true
			}
		}
	}
	return false
}

// importLimit is the folder the docker projects of a scanned folder on disk
// may bring referenced files from: the git work tree holding dir (the
// repository is the project's boundary), else its outermost ancestor below
// the home folder or the file system root (the relative paths the compose
// files write are trusted; hidden and dependency folders never are).
func importLimit(dir string) string {
	home, _ := os.UserHomeDir()
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d || parent == home || filepath.Dir(parent) == parent {
			return d
		}
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// ---------------------------------------------------------------------------
// Generated compose files

// generatedService is a service of a generated compose file: a folder
// holding a Dockerfile.
type generatedService struct {
	name       string // suggested service name
	context    string // relative to the compose file: "." or a folder
	dockerfile string // relative to context
	src        []byte // the Dockerfile
}

// primaryDockerfile is the Dockerfile a folder holding names builds:
// "Dockerfile", else the first one by name.
func primaryDockerfile(names []string) string {
	if len(names) == 0 || slices.Contains(names, "Dockerfile") {
		return "Dockerfile"
	}
	return slices.Min(names)
}

// generatedCompose is the compose file of folders holding only a
// Dockerfile: one service each, publishing what its final stage EXPOSEs
// (a host port already taken moves to the next free one).
func generatedCompose(svcs []generatedService) []byte {
	var b strings.Builder
	from := make([]string, len(svcs))
	for i, s := range svcs {
		from[i] = path.Join(s.context, s.dockerfile)
	}
	fmt.Fprintf(&b, "# Generated by Nimbo from %s.\nservices:\n", strings.Join(from, ", "))
	names := map[string]bool{}
	hostPorts := map[string]bool{}
	for _, s := range svcs {
		svc := strings.Trim(serviceNameClean.ReplaceAllString(strings.ToLower(s.name), "-"), "-._")
		if svc == "" {
			svc = "app"
		}
		for i, base := 2, svc; names[svc]; i++ {
			svc = base + "-" + strconv.Itoa(i)
		}
		names[svc] = true
		ctx := s.context
		if ctx != "." {
			ctx = "./" + ctx
		}
		fmt.Fprintf(&b, "  %s:\n    build:\n      context: %s\n", svc, ctx)
		if s.dockerfile != "Dockerfile" {
			fmt.Fprintf(&b, "      dockerfile: %s\n", s.dockerfile)
		}
		parsed, _ := compose.ParseDockerfile(s.src)
		var ports []string
		if stages := parsed.Resolve(nil); len(stages) > 0 {
			for _, e := range stages[len(stages)-1].Expose {
				port, proto, _ := strings.Cut(e, "/")
				n, err := strconv.Atoi(port)
				if err != nil {
					continue
				}
				suffix := ""
				if proto != "" && proto != "tcp" {
					suffix = "/" + proto
				}
				host := n
				for hostPorts[strconv.Itoa(host)+suffix] {
					host++
				}
				hostPorts[strconv.Itoa(host)+suffix] = true
				ports = append(ports, strconv.Itoa(host)+":"+port+suffix)
			}
		}
		if len(ports) > 0 {
			b.WriteString("    ports:\n")
			for _, p := range ports {
				fmt.Fprintf(&b, "      - \"%s\"\n", p)
			}
		}
	}
	return []byte(b.String())
}

// ---------------------------------------------------------------------------
// Paths

// commonDir is the deepest folder holding the slash paths a and b ("."
// for none).
func commonDir(a, b string) string {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	n := 0
	for n < len(as) && n < len(bs) && as[n] == bs[n] && as[n] != "." {
		n++
	}
	if n == 0 {
		return "."
	}
	return strings.Join(as[:n], "/")
}

// slashRel is the slash path to target from folder base (both relative to
// the same root).
func slashRel(base, target string) string {
	r, err := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(target))
	if err != nil {
		return target
	}
	return filepath.ToSlash(r)
}

func sizeLabel(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func trimErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

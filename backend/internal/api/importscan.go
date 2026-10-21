package api

import (
	"encoding/json"
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

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// Limits of one folder scan/import; larger trees must be narrowed by the user.
const (
	maxScanFiles    = 5000
	maxScanProjects = 200
)

// skipScanDir reports directories never scanned nor copied: Terraform and
// Terragrunt working data, VCS metadata, dependency folders and every
// hidden directory.
func skipScanDir(name string) bool {
	switch name {
	case "node_modules", "vendor", ".terragrunt-cache":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// tfDir is a scanned directory containing *.tf files.
type tfDir struct {
	rel     string   // slash path relative to the scan root; "." for the root
	tfFiles []string // base names of *.tf
	tfvars  []string // relative to rel: "x.tfvars", "environments/x.tfvars"
	sum     hclcodec.Summary
	calls   []localCall
	diags   []model.Diagnostic // local module resolution problems
}

// localCall is a module call with a local source.
type localCall struct {
	name, source string
	target       string // resolved directory (slash, relative to root); "" when unusable
}

// repoScan is a walked folder.
type repoScan struct {
	root     string // absolute
	rootName string
	dirs     map[string]*tfDir
	order    []string            // sorted keys of dirs
	usedBy   map[string][]string // module dir -> directories calling it
	groups   map[string]*envGroup
	skipped  []string
}

// envGroup is a tree in the environments layout found by the scan:
// environment roots <base>/environments/<env> (or envs/<env>) that only call
// one module, <base>/modules/<service>. It is one project at base.
type envGroup struct {
	base    string   // "." or a slash path; not a Terraform directory itself
	service string   // the service module directory
	envs    []string // environment root directories, sorted
}

// findGroups records the environments-layout trees. A group needs every
// Terraform directory of its environments folder to be such a root.
func (rs *repoScan) findGroups() {
	cand := map[string]*envGroup{}
	bad := map[string]bool{}
	for _, rel := range rs.order {
		parent := path.Dir(rel)
		if b := path.Base(parent); (b != hclcodec.EnvironmentsDir && b != "envs") || parent == "." || len(rs.usedBy[rel]) > 0 {
			continue
		}
		base := path.Dir(parent)
		td := rs.dirs[rel]
		if td.sum.Resources > 0 || td.sum.DataSources > 0 || len(td.sum.Modules) != 1 || len(td.calls) != 1 ||
			path.Dir(td.calls[0].target) != path.Join(base, hclcodec.ModulesDir) {
			bad[parent] = true
			continue
		}
		g := cand[parent]
		if g == nil {
			g = &envGroup{base: base, service: td.calls[0].target}
			cand[parent] = g
		} else if g.service != td.calls[0].target {
			bad[parent] = true
		}
		g.envs = append(g.envs, rel)
	}
	rs.groups = map[string]*envGroup{}
	for _, parent := range slices.Sorted(maps.Keys(cand)) {
		g := cand[parent]
		if bad[parent] || rs.dirs[g.base] != nil || rs.groups[g.base] != nil {
			continue
		}
		rs.groups[g.base] = g
	}
}

// inGroup reports whether rel is an environment root of a group.
func (rs *repoScan) inGroup(rel string) bool {
	for _, g := range rs.groups {
		if slices.Contains(g.envs, rel) {
			return true
		}
	}
	return false
}

// scanRepo walks root and parses every directory containing *.tf files.
func scanRepo(root, rootName string) (*repoScan, error) {
	rs := &repoScan{root: root, rootName: rootName, dirs: map[string]*tfDir{}, usedBy: map[string][]string{}}
	tfvars := map[string][]string{}
	jsonFiles := map[string][]string{} // dir -> *.tf.json base names
	files := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		rel := relSlash(root, p)
		if err != nil {
			if p == root {
				return err
			}
			reason := err
			if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
				reason = pe.Err
			}
			rs.skipped = append(rs.skipped, fmt.Sprintf("%s: unreadable (%v)", rel, reason))
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && skipScanDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || skipImport(d.Name()) {
			return nil
		}
		if files++; files > maxScanFiles {
			return errorf(http.StatusBadRequest, "the folder has more than %d files; choose a narrower folder (only one project or a stacks folder)", maxScanFiles)
		}
		dir, base := path.Split(rel)
		dir = strings.TrimSuffix(dir, "/")
		if dir == "" {
			dir = "."
		}
		switch {
		case strings.HasSuffix(base, ".tf"):
			td := rs.dirs[dir]
			if td == nil {
				if len(rs.dirs) == maxScanProjects {
					return errorf(http.StatusBadRequest, "the folder has more than %d Terraform directories; choose a narrower folder", maxScanProjects)
				}
				td = &tfDir{rel: dir}
				rs.dirs[dir] = td
			}
			td.tfFiles = append(td.tfFiles, base)
		case strings.HasSuffix(base, ".tfvars"):
			if path.Base(dir) == "environments" {
				parent := path.Dir(dir)
				tfvars[parent] = append(tfvars[parent], "environments/"+base)
			} else {
				tfvars[dir] = append(tfvars[dir], base)
			}
		case strings.HasSuffix(base, ".tf.json"):
			jsonFiles[dir] = append(jsonFiles[dir], base)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for rel, td := range rs.dirs {
		rs.order = append(rs.order, rel)
		td.tfvars = tfvars[rel]
		slices.Sort(td.tfvars)
		src := make(map[string][]byte, len(td.tfFiles))
		for _, name := range td.tfFiles {
			if d, unsupported := hclcodec.UnsupportedFile(name); unsupported {
				td.diags = append(td.diags, d)
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel), name))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path.Join(rel, name), err)
			}
			src[name] = data
		}
		for _, name := range jsonFiles[rel] {
			d, _ := hclcodec.UnsupportedFile(name)
			td.diags = append(td.diags, d)
		}
		td.sum = hclcodec.Summarize(src)
		kept := td.sum.Diagnostics[:0]
		for _, d := range td.sum.Diagnostics {
			d.Detail = prefixDetail(rel, d.Detail)
			if d.Code == "path_outside_folder" {
				td.diags = append(td.diags, d) // also reported on the imported workflow
				continue
			}
			kept = append(kept, d)
		}
		td.sum.Diagnostics = kept
	}
	var jsonOnly []string
	for rel, names := range jsonFiles {
		if rs.dirs[rel] == nil {
			jsonOnly = append(jsonOnly, fmt.Sprintf("%s: only JSON configuration (%s), which Nimbo does not import", rel, strings.Join(names, ", ")))
		}
	}
	slices.Sort(jsonOnly)
	rs.skipped = append(rs.skipped, jsonOnly...)
	slices.Sort(rs.order)
	for _, rel := range rs.order {
		td := rs.dirs[rel]
		for _, m := range td.sum.Modules {
			if !hclcodec.IsLocalModuleSource(m.Source) {
				continue
			}
			call := localCall{name: m.Name, source: m.Source}
			target := hclcodec.ResolveLocalSource(rel, m.Source)
			switch {
			case target == ".." || strings.HasPrefix(target, "../"):
				td.diags = append(td.diags, warning("module_outside_folder",
					fmt.Sprintf("module.%s uses %s, which is outside the imported folder; its source is kept as is", m.Name, m.Source)))
			case rs.dirs[target] == nil:
				td.diags = append(td.diags, warning("module_not_found",
					fmt.Sprintf("module.%s uses %s, which has no .tf files", m.Name, m.Source)))
			case target == rel:
				td.diags = append(td.diags, warning("module_self", fmt.Sprintf("module.%s calls its own directory", m.Name)))
			default:
				call.target = target
				if !slices.Contains(rs.usedBy[target], rel) {
					rs.usedBy[target] = append(rs.usedBy[target], rel)
				}
			}
			td.calls = append(td.calls, call)
		}
	}
	for _, rel := range rs.order {
		if users := rs.usedBy[rel]; len(users) > 0 {
			rs.skipped = append(rs.skipped, fmt.Sprintf("%s: local module used by %s", rel, strings.Join(users, ", ")))
		}
	}
	rs.findGroups()
	return rs, nil
}

func relSlash(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

func prefixDetail(rel, detail string) string {
	if rel == "." {
		return detail
	}
	return rel + "/" + detail
}

func warning(code, msg string) model.Diagnostic {
	return model.Diagnostic{Severity: model.SeverityWarning, Code: code, Message: msg}
}

// projects returns the directories that are not local modules of another
// nor environment roots of a group, and the base of every group.
func (rs *repoScan) projects() []string {
	var out []string
	for _, rel := range rs.order {
		if len(rs.usedBy[rel]) == 0 && !rs.inGroup(rel) {
			out = append(out, rel)
		}
	}
	for base := range rs.groups {
		out = append(out, base)
	}
	slices.Sort(out)
	return out
}

// projectNames suggests a workflow name per project: its directory name
// ("envs/dev" -> "dev"; the root -> the folder name), extended with parent
// directories until unique ("stacks/api/dev" -> "api-dev").
func (rs *repoScan) projectNames() map[string]string {
	projects := rs.projects()
	names := make(map[string]string, len(projects))
	suffix := func(rel string, n int) string {
		parts := strings.Split(rel, "/")
		return strings.Join(parts[max(0, len(parts)-n):], "-")
	}
	for _, rel := range projects {
		if rel == "." {
			names[rel] = rs.rootName
			continue
		}
		depth := strings.Count(rel, "/") + 1
		n := 1
		for ; n < depth; n++ {
			clash := false
			for _, other := range projects {
				if other != rel && other != "." && suffix(other, n) == suffix(rel, n) {
					clash = true
					break
				}
			}
			if !clash {
				break
			}
		}
		names[rel] = suffix(rel, n)
	}
	return names
}

// moduleClosure returns every local module directory reachable from rel
// and its stable, unique name under modules/ (sorted by path; clashing base
// names get -2, -3, ...).
func (rs *repoScan) moduleClosure(rel string) (dirs []string, names map[string]string) {
	seen := map[string]bool{}
	queue := []string{rel}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range rs.dirs[cur].calls {
			if c.target != "" && c.target != rel && !seen[c.target] {
				seen[c.target] = true
				dirs = append(dirs, c.target)
				queue = append(queue, c.target)
			}
		}
	}
	slices.Sort(dirs)
	names = map[string]string{}
	used := map[string]bool{}
	for _, d := range dirs {
		base := path.Base(d)
		if d == "." {
			base = "root"
		}
		name := base
		for i := 2; used[name]; i++ {
			name = base + "-" + strconv.Itoa(i)
		}
		used[name] = true
		names[d] = name
	}
	return dirs, names
}

func (rs *repoScan) project(rel, name string) model.ImportProject {
	if g := rs.groups[rel]; g != nil {
		return rs.groupProject(g, name)
	}
	td := rs.dirs[rel]
	p := model.ImportProject{
		Layout:      model.LayoutSingle,
		Path:        rel,
		Name:        name,
		Resources:   td.sum.Resources,
		DataSources: td.sum.DataSources,
		Modules:     len(td.sum.Modules),
		Providers:   nonNil(td.sum.Providers),
		Clouds:      nonNil(td.sum.Clouds),
		TfvarsFiles: nonNil(td.tfvars),
		HasBackend:  td.sum.HasBackend,
		Diagnostics: slices.Concat(td.sum.Diagnostics, td.diags),
	}
	// Problems inside the modules it uses surface on the project too.
	mods, _ := rs.moduleClosure(rel)
	for _, m := range mods {
		p.Diagnostics = append(p.Diagnostics, rs.dirs[m].sum.Diagnostics...)
		p.Diagnostics = append(p.Diagnostics, rs.dirs[m].diags...)
	}
	if slices.Contains(strings.Split(rel, "/"), "modules") {
		p.Diagnostics = append(p.Diagnostics, model.Diagnostic{
			Severity: model.SeverityInfo,
			Code:     "unused_module",
			Message:  "Looks like a reusable module that nothing in this folder calls",
			Detail:   "It is under a modules/ folder. Import it only if you run Terraform in it directly.",
		})
	}
	p.Diagnostics = nonNil(p.Diagnostics)
	return p
}

// groupProject summarizes an environments-layout tree: the service
// module's content, the providers and backend of its roots.
func (rs *repoScan) groupProject(g *envGroup, name string) model.ImportProject {
	svc := rs.dirs[g.service]
	p := model.ImportProject{
		Path:        g.base,
		Name:        name,
		Layout:      model.LayoutEnvironments,
		Resources:   svc.sum.Resources,
		DataSources: svc.sum.DataSources,
		Modules:     len(svc.sum.Modules),
		Providers:   slices.Clone(svc.sum.Providers),
		Clouds:      slices.Clone(svc.sum.Clouds),
		TfvarsFiles: []string{},
		Diagnostics: slices.Concat(svc.sum.Diagnostics, svc.diags),
	}
	for _, env := range g.envs {
		td := rs.dirs[env]
		p.Environments = append(p.Environments, path.Base(env))
		for _, f := range td.tfvars {
			if f == "terraform.tfvars" || strings.HasSuffix(f, ".auto.tfvars") {
				p.TfvarsFiles = append(p.TfvarsFiles, relTo(g.base, path.Join(env, f)))
			}
		}
		for _, pr := range td.sum.Providers {
			if !slices.Contains(p.Providers, pr) {
				p.Providers = append(p.Providers, pr)
			}
		}
		for _, c := range td.sum.Clouds {
			if !slices.Contains(p.Clouds, c) {
				p.Clouds = append(p.Clouds, c)
			}
		}
		p.HasBackend = p.HasBackend || td.sum.HasBackend
		p.Diagnostics = append(p.Diagnostics, td.sum.Diagnostics...)
		p.Diagnostics = append(p.Diagnostics, td.diags...)
	}
	mods, _ := rs.moduleClosure(g.service)
	for _, m := range mods {
		p.Diagnostics = append(p.Diagnostics, rs.dirs[m].sum.Diagnostics...)
		p.Diagnostics = append(p.Diagnostics, rs.dirs[m].diags...)
	}
	slices.Sort(p.Providers)
	p.Providers, p.Clouds, p.Diagnostics = nonNil(p.Providers), nonNil(p.Clouds), nonNil(p.Diagnostics)
	return p
}

// relTo is the slash path p relative to directory base.
func relTo(base, p string) string {
	if base == "." {
		return p
	}
	return strings.TrimPrefix(p, base+"/")
}

func (rs *repoScan) result() model.ImportScan {
	names := rs.projectNames()
	out := model.ImportScan{Root: rs.root, Projects: []model.ImportProject{}, Skipped: nonNil(rs.skipped)}
	for _, rel := range rs.projects() {
		out.Projects = append(out.Projects, rs.project(rel, names[rel]))
	}
	return out
}

// ---------------------------------------------------------------------------
// Sources

type importSource struct {
	Dir   string            `json:"dir"`
	Files map[string]string `json:"files"`
}

// open resolves the request to a folder on disk: dir as is, files
// materialized into a temporary directory (cleanup removes it). When every
// file shares one top-level folder (a browser folder upload), that folder
// becomes the root and names the root project.
func (src importSource) open() (root, rootName string, cleanup func(), err error) {
	if (src.Dir == "") == (len(src.Files) == 0) {
		return "", "", nil, errorf(http.StatusBadRequest, "pass exactly one of dir or files")
	}
	if src.Dir != "" {
		dir, err := absDir(src.Dir)
		if err != nil {
			return "", "", nil, err
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return "", "", nil, errorf(http.StatusBadRequest, "%s is not a directory", dir)
		}
		return dir, filepath.Base(dir), func() {}, nil
	}
	if len(src.Files) > maxScanFiles {
		return "", "", nil, errorf(http.StatusBadRequest, "more than %d files; choose a narrower folder", maxScanFiles)
	}
	rels := make(map[string]string, len(src.Files))
	prefix, common := "", true
	for name := range src.Files {
		rel := path.Clean(filepath.ToSlash(name))
		if rel == "." || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, ":") {
			return "", "", nil, errorf(http.StatusBadRequest, "invalid file name %q", name)
		}
		rels[name] = rel
		first, _, nested := strings.Cut(rel, "/")
		if !nested || (prefix != "" && first != prefix) {
			common = false
		}
		prefix = first
	}
	rootName = "Imported workflow"
	strip := ""
	if common {
		rootName, strip = prefix, prefix+"/"
	}
	tmp, err := os.MkdirTemp("", "nimbo-import-")
	if err != nil {
		return "", "", nil, err
	}
	cleanup = func() { os.RemoveAll(tmp) }
	for name, rel := range rels {
		dst := filepath.Join(tmp, filepath.FromSlash(strings.TrimPrefix(rel, strip)))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			cleanup()
			return "", "", nil, err
		}
		if err := os.WriteFile(dst, []byte(src.Files[name]), 0o600); err != nil {
			cleanup()
			return "", "", nil, err
		}
	}
	return tmp, rootName, cleanup, nil
}

func (s *Server) scanImport(w http.ResponseWriter, r *http.Request) error {
	var req importSource
	if err := decode(w, r, maxImportBody, &req); err != nil {
		return err
	}
	root, rootName, cleanup, err := req.open()
	if err != nil {
		return err
	}
	defer cleanup()
	rs, err := scanRepo(root, rootName)
	if err != nil {
		return err
	}
	res := rs.result()
	if req.Dir == "" {
		res.Root = rootName
	}
	return ok(w, res)
}

// ---------------------------------------------------------------------------
// Import

type importRequest struct {
	importSource
	// Projects are scan paths to import, one workflow each; omitted = the
	// folder root only.
	Projects []string `json:"projects"`
	// Name names the workflow when exactly one project is imported.
	Name string `json:"name"`
	// Names overrides the suggested name per project path.
	Names map[string]string `json:"names"`
}

// importPlan is one workflow ready to be written.
type importPlan struct {
	wf      *model.Workflow
	diags   []model.Diagnostic
	project string
	assets  []string          // project-relative slash paths copied as is
	modules []string          // local module dirs (scan-relative)
	names   map[string]string // module dir -> name under modules/
	extra   map[string]string // asset path -> scan-relative file, also copied
}

func (s *Server) importWorkflow(w http.ResponseWriter, r *http.Request) error {
	var req importRequest
	if err := decode(w, r, maxImportBody, &req); err != nil {
		return err
	}
	root, rootName, cleanup, err := req.open()
	if err != nil {
		return err
	}
	defer cleanup()
	rs, err := scanRepo(root, rootName)
	if err != nil {
		return err
	}
	projects := req.Projects
	if len(projects) == 0 {
		switch {
		case rs.dirs["."] != nil, rs.groups["."] != nil:
			projects = []string{"."}
		default:
			if found := rs.projects(); len(found) > 0 {
				return errorf(http.StatusBadRequest, "no Terraform (*.tf) files at the folder root; it contains %d project(s) in subfolders (%s): scan the folder and pass projects",
					len(found), strings.Join(found[:min(len(found), 5)], ", "))
			}
			return errorf(http.StatusBadRequest, "no Terraform (*.tf) files found")
		}
	}

	suggested := rs.projectNames()
	plans := make([]*importPlan, 0, len(projects))
	seen := map[string]bool{}
	for _, p := range projects {
		rel := path.Clean(filepath.ToSlash(strings.TrimSpace(p)))
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if rs.dirs[rel] == nil && rs.groups[rel] == nil {
			return errorf(http.StatusBadRequest, "project %q has no Terraform (*.tf) files", p)
		}
		name := strings.TrimSpace(req.Names[rel])
		if name == "" && len(projects) == 1 {
			name = strings.TrimSpace(req.Name)
		}
		if name == "" {
			name = suggested[rel]
		}
		if name == "" { // a module directory imported on purpose
			name = path.Base(rel)
		}
		plan, err := rs.plan(rel, name)
		if err != nil {
			return err
		}
		plans = append(plans, plan)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	out := model.ImportResult{Workflows: make([]model.Workflow, 0, len(plans))}
	var created []string
	for _, plan := range plans {
		plan.wf.ID = store.NewID(plan.wf.Name)
		plan.wf.Icon = s.randomIcon() // earlier ones of this batch are now the most recent
		err := rs.write(plan, s.cfg.Store.AssetsDir(plan.wf.ID))
		if err == nil {
			err = s.save(plan.wf, plan.diags)
		}
		if err != nil {
			os.RemoveAll(filepath.Dir(s.cfg.Store.AssetsDir(plan.wf.ID)))
			for _, id := range created {
				if s.cfg.Store.Delete(id) == nil {
					s.cfg.Events.Publish(model.Event{Type: model.EventWorkflowDeleted, WorkflowID: id, Data: map[string]string{"id": id}})
				}
			}
			return fmt.Errorf("import %s: %w", plan.project, err)
		}
		created = append(created, plan.wf.ID)
		out.Workflows = append(out.Workflows, *plan.wf)
	}
	return writeJSON(w, http.StatusCreated, out)
}

// plan parses one project and lists the files to copy.
func (rs *repoScan) plan(rel, name string) (*importPlan, error) {
	if g := rs.groups[rel]; g != nil {
		return rs.planGroup(g, name)
	}
	plan := &importPlan{project: rel}
	plan.modules, plan.names = rs.moduleClosure(rel)
	tf := map[string][]byte{}
	err := rs.walkOwn(rel, func(p, sub string) error {
		if tfName := classify(sub); tfName != "" {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			// A .tf file with only comments has no block to remember its
			// file: it is kept as is so the project keeps its layout.
			if !strings.Contains(sub, "/") && strings.HasSuffix(sub, ".tf") && hclcodec.CommentOnly(data) {
				plan.assets = append(plan.assets, sub)
				return nil
			}
			tf[tfName] = data
		} else if !generatedScaffold(p, sub) {
			plan.assets = append(plan.assets, sub)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	wf, diags, err := hclcodec.Import(name, tf)
	if err == nil {
		err = syntaxError(diags)
	}
	if err != nil {
		if rel != "." {
			err = fmt.Errorf("%s: %w", rel, err)
		}
		return nil, &httpError{status: http.StatusBadRequest, msg: err.Error(), diagnostics: diags}
	}
	wf.Name = name
	// An imported project keeps its layout: a single root module, even
	// with several variable files.
	wf.Layout = model.LayoutSingle
	if err := pointModules(wf, plan.names, func(src string) string { return hclcodec.ResolveLocalSource(rel, src) }); err != nil {
		return nil, err
	}
	plan.diags = slices.Concat(diags, rs.dirs[rel].diags)
	// Problems inside the copied modules surface on the workflow too.
	for _, m := range plan.modules {
		plan.diags = append(plan.diags, rs.dirs[m].diags...)
	}
	plan.wf = wf
	return plan, nil
}

// pointModules points local module calls at their copies under modules/
// (names: module dir -> name); resolve maps a call's source to the
// scan-relative directory it reaches.
func pointModules(wf *model.Workflow, names map[string]string, resolve func(src string) string) error {
	for i := range wf.Nodes {
		nd := &wf.Nodes[i]
		if nd.Kind != model.KindModule {
			continue
		}
		for j := range nd.Body.Attrs {
			a := &nd.Body.Attrs[j]
			if a.Name != "source" {
				continue
			}
			var src string
			if json.Unmarshal(a.Value.Literal, &src) != nil || !hclcodec.IsLocalModuleSource(src) {
				continue
			}
			if m, ok := names[resolve(src)]; ok {
				lit, _ := json.Marshal("./modules/" + m)
				expr, err := hclcodec.ExprFromLiteral(lit)
				if err != nil {
					return err
				}
				a.Value = model.Value{Expr: expr, Literal: lit}
			}
		}
	}
	return nil
}

// planGroup parses an environments-layout tree into one workflow (see
// hclcodec.ImportEnvironments): the service module's files become the
// workflow's (its other files its assets), the local modules it calls are
// copied, each root gives an environment and the first root's dependency
// lock file is kept.
func (rs *repoScan) planGroup(g *envGroup, name string) (*importPlan, error) {
	plan := &importPlan{project: g.service, extra: map[string]string{}}
	plan.modules, plan.names = rs.moduleClosure(g.service)
	svc := path.Base(g.service)
	files := map[string][]byte{}
	err := rs.walkOwn(g.service, func(p, sub string) error {
		if !strings.Contains(sub, "/") && strings.HasSuffix(sub, ".tf") && !hclcodec.IsOverrideFile(sub) {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if !hclcodec.CommentOnly(data) {
				files[hclcodec.ModulesDir+"/"+svc+"/"+sub] = data
				return nil
			}
		}
		if !generatedScaffold(p, sub) {
			plan.assets = append(plan.assets, sub)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, env := range g.envs {
		envName := path.Base(env)
		err := rs.walkOwn(env, func(p, sub string) error {
			switch {
			case strings.Contains(sub, "/"):
				return nil
			case sub == lockFile:
				if plan.extra[lockFile] == "" {
					plan.extra[lockFile] = path.Join(env, sub)
				}
				return nil
			case strings.HasSuffix(sub, ".tf"), sub == "terraform.tfvars", strings.HasSuffix(sub, ".auto.tfvars"), sub == ".env.example":
			default:
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			files[hclcodec.EnvironmentsDir+"/"+envName+"/"+sub] = data
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	wf, diags, err := hclcodec.ImportEnvironments(name, files)
	if err == nil {
		err = syntaxError(diags)
	}
	if err != nil {
		if g.base != "." {
			err = fmt.Errorf("%s: %w", g.base, err)
		}
		return nil, &httpError{status: http.StatusBadRequest, msg: err.Error(), diagnostics: diags}
	}
	err = pointModules(wf, plan.names, func(src string) string {
		if strings.HasPrefix(src, "./"+hclcodec.ModulesDir+"/") {
			return path.Join(g.base, src) // already relative to the tree (see ImportEnvironments)
		}
		return hclcodec.ResolveLocalSource(g.service, src)
	})
	if err != nil {
		return nil, err
	}
	plan.diags = slices.Concat(diags, rs.dirs[g.service].diags)
	for _, env := range g.envs {
		plan.diags = append(plan.diags, rs.dirs[env].diags...)
	}
	for _, m := range plan.modules {
		plan.diags = append(plan.diags, rs.dirs[m].diags...)
	}
	plan.wf = wf
	return plan, nil
}

// generatedScaffold reports a project file a Nimbo export generated (README,
// .gitignore, terraform.tfvars.example still carrying their marker line):
// it is regenerated from the workflow instead of being kept as an asset.
func generatedScaffold(p, sub string) bool {
	switch sub {
	case hclcodec.ReadmeFile, hclcodec.GitignoreFile, hclcodec.TfvarsSampleFile, hclcodec.BackendTfExampleFile:
	default:
		return false
	}
	data, err := os.ReadFile(p)
	return err == nil && hclcodec.IsScaffold(sub, data)
}

// walkOwn visits the regular files of directory rel that belong to it:
// skipped directories, Terraform working data and nested Terraform
// directories (other projects or modules, imported separately) are left out.
// fn gets the absolute path and the slash path relative to rel.
func (rs *repoScan) walkOwn(rel string, fn func(p, sub string) error) error {
	dir := filepath.Join(rs.root, filepath.FromSlash(rel))
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != dir {
				return fs.SkipDir // reported as unreadable by the scan
			}
			return err
		}
		if p == dir {
			return nil
		}
		sub := relSlash(dir, p)
		if d.IsDir() {
			if skipScanDir(d.Name()) || rs.dirs[path.Join(rel, sub)] != nil {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || skipImport(d.Name()) {
			return nil
		}
		return fn(p, sub)
	})
}

// write copies the project assets and its local modules into assetsDir.
// Module directories land flat at modules/<name>; their own local module
// calls are rewritten to ../<name>.
func (rs *repoScan) write(plan *importPlan, assetsDir string) error {
	src := filepath.Join(rs.root, filepath.FromSlash(plan.project))
	for _, sub := range plan.assets {
		if err := copyFile(filepath.Join(src, filepath.FromSlash(sub)), filepath.Join(assetsDir, filepath.FromSlash(sub))); err != nil {
			return err
		}
	}
	for dst, from := range plan.extra {
		if err := copyFile(filepath.Join(rs.root, filepath.FromSlash(from)), filepath.Join(assetsDir, filepath.FromSlash(dst))); err != nil {
			return err
		}
	}
	for _, mod := range plan.modules {
		dst := filepath.Join(assetsDir, "modules", plan.names[mod])
		err := rs.walkOwn(mod, func(p, sub string) error {
			target := filepath.Join(dst, filepath.FromSlash(sub))
			if strings.Contains(sub, "/") || !strings.HasSuffix(sub, ".tf") {
				return copyFile(p, target)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out, changed, err := hclcodec.RewriteModuleSources(sub, data, func(source string) (string, bool) {
				if !hclcodec.IsLocalModuleSource(source) {
					return "", false
				}
				if m, ok := plan.names[hclcodec.ResolveLocalSource(mod, source)]; ok {
					return "../" + m, true
				}
				return "", false
			})
			if err != nil {
				return err
			}
			if !changed {
				return copyFile(p, target)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.WriteFile(target, out, 0o644)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

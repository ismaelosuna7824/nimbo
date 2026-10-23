package api

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The Code tab edits the workflow's files as an export writes them: the
// Terraform code (re-imported into the diagram) and the assets stored with
// the workflow (module folders, templates, scripts...), in one request.

// codeRequest is the body of PUT /workflows/{id}/terraform. Files and
// Changes/Moves are exclusive.
type codeRequest struct {
	// Files is the complete Terraform code (the keys GET /terraform
	// returns); a code file left out is deleted.
	Files map[string]string `json:"files"`
	// Changes sets the content of files at their export path (GET /files
	// paths, created when new) or deletes them (null). Moves run first.
	Changes map[string]*string `json:"changes"`
	Moves   []fileMove         `json:"moves"`
	// Format formats the HCL files written (terraform fmt).
	Format bool `json:"format"`
}

type fileMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// fileRules says what the files of a workflow's export layout are: which
// paths are Terraform code (re-imported), which generated code is fixed,
// and where a new asset is stored. Docker and Kubernetes workflows have
// no Terraform code: every file is stored as is (dotfiles such as .env
// included).
type fileRules struct {
	layout string
	svc    string   // environments layout: the service module
	envs   []string // environments layout: the environment roots, sorted
	kind   string   // the workflow's kind
}

// filesOnly reports a workflow whose files are stored as is.
func (r fileRules) filesOnly() bool { return r.kind != "" }

func newFileRules(wf *model.Workflow) (fileRules, error) {
	if !wf.IsTerraform() {
		return fileRules{layout: model.LayoutSingle, kind: wf.Kind}, nil
	}
	layout, err := hclcodec.ResolveLayout(wf)
	if err != nil {
		return fileRules{}, &httpError{status: http.StatusBadRequest, msg: err.Error()}
	}
	r := fileRules{layout: layout}
	if layout == model.LayoutEnvironments {
		r.svc, r.envs = hclcodec.ServiceName(wf), hclcodec.EnvironmentNames(wf)
	}
	return r, nil
}

func (r fileRules) environments() bool { return r.layout == model.LayoutEnvironments }

// serviceDir is the service module folder of the environments layout.
func (r fileRules) serviceDir() string { return hclcodec.ModulesDir + "/" + r.svc }

// envRoot splits environments/<env>/<base> for an environment of the
// workflow.
func (r fileRules) envRoot(p string) (env, base string, ok bool) {
	parts := strings.Split(p, "/")
	if !r.environments() || len(parts) != 3 || parts[0] != hclcodec.EnvironmentsDir || !slices.Contains(r.envs, parts[1]) {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// isCode reports whether p is Terraform code of the workflow: root *.tf
// files Terraform loads (single layout), or the service module's and the
// environment roots' (environments layout).
func (r fileRules) isCode(p string) bool {
	if r.filesOnly() {
		return false
	}
	dir, base := path.Split(p)
	if hclcodec.ValidFileName(base) != nil {
		return false
	}
	if !r.environments() {
		return dir == ""
	}
	if dir == r.serviceDir()+"/" {
		return true
	}
	_, _, ok := r.envRoot(p)
	return ok
}

// fixed explains why the file p can't be created, moved or deleted (""
// when it can): the environment roots are rendered for every environment.
func (r fileRules) fixed(p string) string {
	if env, _, ok := r.envRoot(p); ok || strings.HasPrefix(p, hclcodec.EnvironmentsDir+"/") && r.environments() {
		if env == "" {
			env = "<env>"
		}
		return fmt.Sprintf("%s/%s/ is an environment root, generated for every environment of the workflow: add code to %s/ (environments are managed in Variables & Environments)",
			hclcodec.EnvironmentsDir, env, r.serviceDir())
	}
	return ""
}

// codeReadOnly explains why generated code p can't be edited ("" when it
// can). The environments layout reads the roots back with
// hclcodec.ImportEnvironments: every root's main.tf (module call arguments
// set that environment's values), and the first root's provider, version
// and backend settings, which every root shares. The rest of a root is
// derived from the service module.
func (r fileRules) codeReadOnly(p string) string {
	env, base, ok := r.envRoot(p)
	if !ok {
		return ""
	}
	switch base {
	case "main.tf":
		return ""
	case "providers.tf", "versions.tf", "backend.tf":
		if env == r.envs[0] {
			return ""
		}
		return fmt.Sprintf("The same in every environment root: edit %s/%s/%s (the workflow keeps one provider configuration, Terraform settings and backend for all environments)",
			hclcodec.EnvironmentsDir, r.envs[0], base)
	}
	return fmt.Sprintf("Derived from the service module %s/ (the inputs it receives, the outputs it exposes): edit the module instead", r.serviceDir())
}

// assetPath is where a new asset exported at dst is stored in the assets
// directory (the inverse of hclcodec.AssetPlacement).
func (r fileRules) assetPath(dst string) (string, error) {
	if !r.environments() {
		return dst, nil
	}
	if rest, ok := strings.CutPrefix(dst, r.serviceDir()+"/"); ok {
		if strings.HasPrefix(rest, hclcodec.ModulesDir+"/") || rest == lockFile {
			return "", errorf(http.StatusBadRequest, "%s can't be stored in the environments layout: put local modules in %s/<name>/", dst, hclcodec.ModulesDir)
		}
		return rest, nil
	}
	if strings.HasPrefix(dst, hclcodec.ModulesDir+"/") && strings.Count(dst, "/") >= 2 {
		return dst, nil
	}
	return "", errorf(http.StatusBadRequest, "%s can't be stored in the environments layout: files go in the service module %s/ or a local module %s/<name>/",
		dst, r.serviceDir(), hclcodec.ModulesDir)
}

// newFilePath validates the path of a file created or moved by the Code
// tab. Docker and Kubernetes workflows may hold dotfiles (.env,
// .dockerignore, .helmignore).
func (r fileRules) newFilePath(p string) (string, error) {
	p, err := cleanFilePath(p)
	if err != nil {
		return "", err
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case strings.HasPrefix(seg, ".") && (!r.filesOnly() || seg == ".git"):
			return "", errorf(http.StatusBadRequest, "%s: hidden names (starting with .) are not exported", p)
		case skipImport(seg):
			return "", errorf(http.StatusBadRequest, "%s: Terraform state and working data are not stored", p)
		case strings.IndexFunc(seg, unicode.IsControl) >= 0, strings.ContainsAny(seg, `:*?"<>|`):
			return "", errorf(http.StatusBadRequest, "%s: names can't hold control characters or any of :*?\"<>|", p)
		}
	}
	return p, nil
}

// textContent validates file content sent as text.
func textContent(p string, data []byte) error {
	if len(data) > maxTextFile {
		return errorf(http.StatusRequestEntityTooLarge, "%s: content larger than %d bytes", p, maxTextFile)
	}
	if isBinary(data, true) {
		return errorf(http.StatusBadRequest, "%s: content must be UTF-8 text without NUL bytes", p)
	}
	return nil
}

// importCode re-imports the workflow's complete Terraform code into cur
// (see putTerraform).
func (s *Server) importCode(cur *model.Workflow, files map[string][]byte) ([]model.Diagnostic, error) {
	var (
		next  *model.Workflow
		diags []model.Diagnostic
		err   error
	)
	tree := hclcodec.IsEnvironmentsTree(files)
	if tree {
		next, diags, err = hclcodec.ImportEnvironments(cur.Name, files)
	} else {
		next, diags, err = hclcodec.Import(cur.Name, files)
	}
	if err == nil {
		err = syntaxError(diags)
	}
	if err != nil {
		return nil, &httpError{status: http.StatusBadRequest, msg: err.Error(), diagnostics: diags}
	}
	if tree {
		// Values are edited in the app, not in the code: the tree only
		// overrides what its module calls set directly.
		fromTree := next.Environments
		hclcodec.MergeImported(cur, next, s.cfg.Catalog.ResourceFile)
		hclcodec.MergeEnvironmentsTree(cur, next, fromTree)
	} else {
		hclcodec.MergeImported(cur, next, s.cfg.Catalog.ResourceFile)
	}
	*cur = *next
	return diags, nil
}

// fileEntry is one file of the workflow while a code request is applied.
type fileEntry struct {
	data    []byte // content; nil: the stored asset src, unchanged
	src     string // stored asset (slash path in the assets directory)
	origin  string // export path before the request ("" for a new file)
	changed bool   // data was set by the request
}

// applyFileChanges applies moves and changes of a code request to cur and
// its assets: the resulting Terraform code is re-imported when it differs,
// the other files are stored as assets. Everything is validated (paths,
// clashes, syntax, the import) before a file is written.
func (s *Server) applyFileChanges(cur *model.Workflow, req codeRequest) ([]model.Diagnostic, error) {
	rules, err := newFileRules(cur)
	if err != nil {
		return nil, err
	}
	generated, assets, err := s.exportFiles(cur, true)
	if err != nil {
		return nil, err
	}
	entries := map[string]*fileEntry{}
	locked := map[string]string{} // files that can't change, and why
	for p, data := range generated {
		if strings.HasSuffix(p, ".tf") && rules.isCode(p) {
			entries[p] = &fileEntry{data: data, origin: p}
			continue
		}
		locked[p] = fmt.Sprintf("%s can't change: %s", p, generatedFile(rules, p, data).ReadOnlyReason)
	}
	placed := placements(assets)
	owned := map[string]bool{} // stored assets the request may change
	for dst, src := range assets {
		if placed[src] > 1 {
			locked[dst] = fmt.Sprintf("%s is written into every environment root from one stored file", dst)
			continue
		}
		entries[dst] = &fileEntry{src: src, origin: dst}
		owned[src] = true
	}
	root, err := os.OpenRoot(s.cfg.Store.AssetsDir(cur.ID))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if root != nil {
		defer root.Close()
	}
	read := func(e *fileEntry) ([]byte, error) {
		if e.data != nil || root == nil {
			return e.data, nil
		}
		return root.ReadFile(filepath.FromSlash(e.src))
	}
	codeOf := func() (map[string][]byte, error) {
		code := map[string][]byte{}
		for p, e := range entries {
			if !rules.isCode(p) {
				continue
			}
			data, err := read(e)
			if err != nil {
				return nil, err
			}
			if isBinary(data, true) {
				return nil, errorf(http.StatusBadRequest, "%s is a binary file, not Terraform code", p)
			}
			code[p] = data
		}
		return code, nil
	}
	before, err := codeOf()
	if err != nil {
		return nil, err
	}
	missing := func(p string) error {
		if why := locked[p]; why != "" {
			return errorf(http.StatusConflict, "%s", why)
		}
		return errorf(http.StatusNotFound, "file %s not found", p)
	}

	for _, m := range req.Moves {
		from, err := cleanFilePath(m.From)
		if err != nil {
			return nil, err
		}
		to, err := rules.newFilePath(m.To)
		if err != nil {
			return nil, err
		}
		e := entries[from]
		if e == nil {
			return nil, missing(from)
		}
		for _, p := range []string{from, to} {
			if why := rules.fixed(p); why != "" {
				return nil, errorf(http.StatusConflict, "%s", why)
			}
		}
		if _, taken := entries[to]; taken || locked[to] != "" {
			return nil, errorf(http.StatusConflict, "%s already exists", to)
		}
		delete(entries, from)
		entries[to] = e
	}

	for _, p := range slices.Sorted(maps.Keys(req.Changes)) {
		content := req.Changes[p]
		if content == nil {
			p, err := cleanFilePath(p)
			if err != nil {
				return nil, err
			}
			if entries[p] == nil {
				return nil, missing(p)
			}
			if why := rules.fixed(p); why != "" {
				return nil, errorf(http.StatusConflict, "%s", why)
			}
			delete(entries, p)
			continue
		}
		data := []byte(*content)
		if err := textContent(p, data); err != nil {
			return nil, err
		}
		if e := entries[p]; e != nil {
			if why := rules.codeReadOnly(p); why != "" && !bytes.Equal(data, e.data) {
				return nil, errorf(http.StatusConflict, "%s can't be edited: %s", p, why)
			}
			if path.Base(p) == lockFile {
				return nil, errorf(http.StatusConflict, "%s is written by terraform init; it can't be edited", p)
			}
			e.data, e.changed = data, true
			continue
		}
		if why := locked[p]; why != "" {
			return nil, errorf(http.StatusConflict, "%s", why)
		}
		p, err := rules.newFilePath(p)
		if err != nil {
			return nil, err
		}
		if why := rules.fixed(p); why != "" {
			return nil, errorf(http.StatusConflict, "%s", why)
		}
		entries[p] = &fileEntry{data: data, changed: true}
	}

	// A file can't take the place of a folder, nor sit below a file.
	for p := range entries {
		for _, dir := range ancestorDirs(p) {
			if _, file := entries[dir]; file || locked[dir] != "" {
				return nil, errorf(http.StatusConflict, "%s needs a folder %s, but that is a file", p, dir)
			}
		}
	}
	for p := range locked {
		for _, dir := range ancestorDirs(p) {
			if _, file := entries[dir]; file {
				return nil, errorf(http.StatusConflict, "%s needs a folder %s, but that is a file", p, dir)
			}
		}
	}

	// Syntax (and formatting) of the HCL written.
	var syntax []model.Diagnostic
	for _, p := range slices.Sorted(maps.Keys(entries)) {
		e := entries[p]
		if !e.changed || !hclcodec.IsHCLFile(p) {
			continue
		}
		if req.Format {
			out, d := hclcodec.Format(p, e.data)
			if d == nil {
				e.data = out
			}
			syntax = append(syntax, d...)
		} else {
			syntax = append(syntax, hclcodec.CheckSyntax(p, e.data)...)
		}
	}
	switch rules.kind {
	case model.WorkflowDocker:
		syntax = append(syntax, dockerSyntax(cur, entries)...)
	case model.WorkflowKubernetes:
		syntax = append(syntax, kubeSyntax(cur, entries)...)
	}
	if len(syntax) > 0 {
		return nil, &httpError{status: http.StatusBadRequest, msg: "the code has syntax errors; nothing was saved", diagnostics: syntax}
	}

	after, err := codeOf()
	if err != nil {
		return nil, err
	}
	// Which files are stored as assets: everything but code, plus code
	// without blocks (empty but for comments, so not part of the workflow):
	// like a folder import keeps it, unless it is empty.
	asset := map[string]bool{}
	for p, e := range entries {
		asset[p] = !rules.isCode(p) || e.src != ""
	}
	next := cur
	var diags []model.Diagnostic
	if !maps.EqualFunc(before, after, bytes.Equal) {
		next = new(*cur) // importCode replaces it; cur stays as is on failure
		if diags, err = s.importCode(next, after); err != nil {
			return nil, err
		}
		out, err := s.exportLayout(next, false)
		if err != nil {
			return nil, err
		}
		for p, data := range after {
			e := entries[p]
			_, imported := out[p]
			switch {
			case imported:
				asset[p] = false
			case len(bytes.TrimSpace(data)) == 0:
				delete(entries, p)
				delete(asset, p)
			default:
				asset[p] = true
				e.data = data
				e.changed = e.changed || e.src == "" || e.origin != p
			}
		}
	}
	if err := s.storeAssets(cur.ID, rules, assets, owned, entries, asset); err != nil {
		return nil, err
	}
	switch rules.kind {
	case model.WorkflowDocker:
		followComposeFiles(cur, entries)
	case model.WorkflowKubernetes:
		followKubeSources(cur, entries)
	}
	*cur = *next
	return diags, nil
}

// ancestorDirs lists the folders above the slash path p, outermost first.
func ancestorDirs(p string) []string {
	var out []string
	for i := range len(p) {
		if p[i] == '/' {
			out = append(out, p[:i])
		}
	}
	return out
}

// storeAssets writes the asset side of a code request: entries asset marks
// are stored (moved, written); every other stored asset the request owned
// (an entry at its start) is removed. assets maps every export path to its
// stored path. Moves go through a staging folder next to the assets
// directory, so a swap or a chain never overwrites a file it still needs;
// emptied folders are removed.
func (s *Server) storeAssets(wfID string, rules fileRules, assets map[string]string, owned map[string]bool, entries map[string]*fileEntry, asset map[string]bool) error {
	type move struct{ from, to string }
	var moves []move
	writes := map[string][]byte{}
	kept := map[string]bool{}
	targets := map[string]string{}
	for _, dst := range slices.Sorted(maps.Keys(entries)) {
		e := entries[dst]
		if !asset[dst] {
			continue
		}
		target := e.src
		if e.src == "" || e.origin != dst {
			var err error
			if target, err = rules.assetPath(dst); err != nil {
				return err
			}
		}
		if other, dup := targets[target]; dup {
			return errorf(http.StatusConflict, "%s and %s would be stored as the same file", other, dst)
		}
		targets[target] = dst
		switch {
		case e.src == target:
			kept[target] = true
		case e.src != "":
			moves = append(moves, move{e.src, target})
		}
		if e.changed || (e.src == "" && e.data != nil) {
			writes[target] = e.data
		}
	}
	moved := map[string]bool{}
	for _, m := range moves {
		moved[m.from] = true
	}
	var removals []string
	for _, src := range slices.Sorted(maps.Keys(owned)) {
		if !kept[src] && !moved[src] && !slices.Contains(removals, src) {
			removals = append(removals, src)
		}
	}
	// A target must be free once removals and moves are done.
	for target, dst := range targets {
		if kept[target] {
			continue
		}
		for _, src := range assets {
			if src == target && !moved[src] && !slices.Contains(removals, src) {
				return errorf(http.StatusConflict, "%s would overwrite the stored file %s", dst, src)
			}
		}
	}
	if len(moves) == 0 && len(writes) == 0 && len(removals) == 0 {
		return nil
	}

	dir := s.cfg.Store.AssetsDir(wfID)
	parent, base := filepath.Dir(dir), filepath.Base(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	r, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer r.Close()
	in := func(p string) string { return filepath.Join(base, filepath.FromSlash(p)) }
	staging, err := os.MkdirTemp(parent, ".moving-")
	if err != nil {
		return err
	}
	stagingName := filepath.Base(staging)
	defer os.RemoveAll(staging)

	modes := map[string]fs.FileMode{}
	for i, m := range moves {
		if fi, err := r.Lstat(in(m.from)); err == nil {
			modes[m.to] = fi.Mode().Perm()
		}
		if err := r.Rename(in(m.from), filepath.Join(stagingName, fmt.Sprint(i))); err != nil {
			return err
		}
	}
	for _, src := range removals {
		if err := r.Remove(in(src)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	for i, m := range moves {
		if err := r.MkdirAll(filepath.Dir(in(m.to)), 0o755); err != nil {
			return err
		}
		if err := r.Rename(filepath.Join(stagingName, fmt.Sprint(i)), in(m.to)); err != nil {
			return err
		}
	}
	for _, target := range slices.Sorted(maps.Keys(writes)) {
		mode, ok := modes[target]
		if fi, err := r.Lstat(in(target)); err == nil {
			if !fi.Mode().IsRegular() {
				return errorf(http.StatusConflict, "%s is not a regular file", target)
			}
			mode, ok = fi.Mode().Perm(), true
		}
		if !ok {
			mode = 0o644
		}
		if err := r.MkdirAll(filepath.Dir(in(target)), 0o755); err != nil {
			return err
		}
		if err := r.WriteFile(in(target), writes[target], mode); err != nil {
			return err
		}
	}
	// Remove the folders the request emptied (never the assets directory).
	emptied := append([]string{}, removals...)
	for _, m := range moves {
		emptied = append(emptied, m.from)
	}
	for _, p := range emptied {
		dirs := ancestorDirs(p)
		for i := len(dirs) - 1; i >= 0; i-- {
			if r.Remove(in(dirs[i])) != nil {
				break
			}
		}
	}
	return nil
}

// formatCode formats HCL like terraform fmt (POST /terraform/format).
func (s *Server) formatCode(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Content *string `json:"content"`
		Path    string  `json:"path"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	if req.Content == nil {
		return errorf(http.StatusBadRequest, "content is required")
	}
	name := req.Path
	if name == "" {
		name = "main.tf"
	}
	if err := textContent(name, []byte(*req.Content)); err != nil {
		return err
	}
	out, diags := hclcodec.Format(name, []byte(*req.Content))
	if diags != nil {
		return &httpError{status: http.StatusBadRequest, msg: "the code has syntax errors; it was not formatted", diagnostics: diags}
	}
	return ok(w, map[string]any{"content": string(out), "changed": string(out) != *req.Content})
}

// moduleScaffold renders a new local module without saving it
// (POST /workflows/{id}/modules/scaffold): the Code tab adds its files as
// pending changes.
func (s *Server) moduleScaffold(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	if err := hclcodec.ValidModuleName(req.Name); err != nil {
		return errorf(http.StatusBadRequest, "%s", err.Error())
	}
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	rules, err := newFileRules(wf)
	if err != nil {
		return err
	}
	dir := hclcodec.ModulesDir + "/" + req.Name
	generated, assets, err := s.exportFiles(wf, true)
	if err != nil {
		return err
	}
	for _, p := range slices.Concat(slices.Collect(maps.Keys(generated)), slices.Collect(maps.Keys(assets))) {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return errorf(http.StatusConflict, "%s already exists", dir)
		}
	}
	source, callPath := "./"+dir, "main.tf"
	if rules.environments() {
		// The service module calls its siblings.
		source, callPath = "../"+req.Name, rules.serviceDir()+"/main.tf"
	}
	files, call := hclcodec.NewModule(wf, req.Name, source, s.cfg.Catalog.Providers())
	out := model.ModuleScaffold{Name: req.Name, Dir: dir, Files: map[string]string{}, Call: model.ModuleScaffoldCall{Path: callPath, Block: call}}
	for name, data := range files {
		out.Files[dir+"/"+name] = string(data)
	}
	return ok(w, out)
}

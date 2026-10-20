package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// registrySource matches Terraform Registry addresses:
// <namespace>/<name>/<provider>, optionally prefixed by a registry host.
var registrySource = regexp.MustCompile(`^([a-zA-Z0-9.-]+\.[a-z]+/)?[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+(//.*)?$`)

// moduleView returns the read-only content of a module call. The optional
// query `path` drills into nested calls ("module.subnets.module.x").
func (s *Server) moduleView(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	nd, err := findNode(wf, r.PathValue("nodeId"))
	if err != nil {
		return err
	}
	if nd.Kind != model.KindModule {
		return errorf(http.StatusBadRequest, "%s is not a module call", hclcodec.Address(nd))
	}
	var nested []string
	if p := strings.TrimSpace(r.URL.Query().Get("path")); p != "" {
		parts := strings.Split(p, ".")
		if len(parts)%2 != 0 {
			return errorf(http.StatusBadRequest, "path must look like module.<name>[.module.<name>...]")
		}
		for i := 0; i < len(parts); i += 2 {
			if parts[i] != "module" || parts[i+1] == "" {
				return errorf(http.StatusBadRequest, "path must look like module.<name>[.module.<name>...]")
			}
			nested = append(nested, parts[i+1])
		}
	}

	source, version := callSource(nd)
	root := s.cfg.Store.AssetsDir(wf.ID)
	view, dir := describeModule(source, version, root, root)
	for _, name := range nested {
		if dir == "" {
			break
		}
		files, err := readModuleFiles(dir)
		if err != nil {
			return err
		}
		call, ok := findCall(hclcodec.Summarize(files).Modules, name)
		if !ok {
			return errorf(http.StatusNotFound, "module.%s not found in %s", name, view.Source)
		}
		view, dir = describeModule(call.Source, call.Version, dir, root)
	}
	if dir == "" {
		return ok(w, view)
	}
	if rel, err := filepath.Rel(root, dir); err == nil {
		view.Dir = filepath.ToSlash(rel)
	}
	files, err := readModuleFiles(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		view.Message = fmt.Sprintf("Local module %s has no .tf files in this workflow's files", view.Source)
		return ok(w, view)
	}
	name := "module." + nd.Name
	for _, n := range nested {
		name += ".module." + n
	}
	mod, diags, err := hclcodec.Import(name, files)
	if err != nil {
		return err
	}
	mod.ID = wf.ID
	s.finalize(mod, diags)
	view.Workflow = mod
	return ok(w, view)
}

// callSource returns the constant source and version of a module call.
func callSource(nd *model.Node) (source, version string) {
	for _, a := range nd.Body.Attrs {
		var v string
		if json.Unmarshal(a.Value.Literal, &v) != nil {
			continue
		}
		switch a.Name {
		case "source":
			source = v
		case "version":
			version = v
		}
	}
	return source, version
}

// maxModuleDepth bounds nested module calls (local modules calling each
// other in a cycle would never end).
const maxModuleDepth = 8

// moduleTree returns the content of every module call of a workflow, nested
// calls included: local modules from the workflow files, registry and remote
// modules from Terraform's module cache in the sandbox working directory
// (when `terraform init` downloaded them). Each directory is read once.
func (s *Server) moduleTree(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	root := s.cfg.Store.AssetsDir(wf.ID)
	work := s.cfg.Sandbox.WorkDir(wf.ID)
	cache := downloadedModules(work)
	tree := model.ModuleTree{Calls: map[string]model.ModuleCallSource{}, Modules: map[string]*model.Workflow{}}
	parsed := map[string]bool{} // directory key -> read (with or without .tf files)

	// walk visits the calls among nodes, declared in directory from (inside
	// base: the workflow files or the working directory).
	var walk func(nodes []model.Node, path, key, from, base string, depth int) error
	walk = func(nodes []model.Node, path, key, from, base string, depth int) error {
		for i := range nodes {
			nd := &nodes[i]
			if nd.Kind != model.KindModule {
				continue
			}
			source, version := callSource(nd)
			call := model.ModuleCallSource{Source: source, Version: version, Local: hclcodec.IsLocalModuleSource(source)}
			callPath, callKey := path+"module."+nd.Name, key+nd.Name
			dir, dirBase := "", ""
			if call.Local {
				d := filepath.Join(from, filepath.FromSlash(strings.ReplaceAll(source, `\`, "/")))
				if rel, err := filepath.Rel(base, d); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					dir, dirBase = d, base
				}
			} else if d, ok := cache[callKey]; ok {
				dir, dirBase = d, work
			}
			var mod *model.Workflow
			if dir != "" {
				rel, err := filepath.Rel(dirBase, dir)
				if err != nil {
					return err
				}
				dirKey := filepath.ToSlash(rel)
				if !parsed[dirKey] {
					parsed[dirKey] = true
					files, err := readModuleFiles(dir)
					if err != nil {
						return err
					}
					if len(files) > 0 {
						m, diags, err := hclcodec.Import(callPath, files)
						if err != nil {
							return err
						}
						m.ID = wf.ID
						s.finalize(m, diags)
						tree.Modules[dirKey] = m
					}
				}
				if mod = tree.Modules[dirKey]; mod != nil {
					call.Dir = dirKey
				}
			}
			tree.Calls[callPath] = call
			if mod != nil && depth < maxModuleDepth {
				if err := walk(mod.Nodes, callPath+".", callKey+".", dir, dirBase, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(wf.Nodes, "", "", root, root, 1); err != nil {
		return err
	}
	return ok(w, tree)
}

// downloadedModules reads the module manifest `terraform init` writes
// (.terraform/modules/modules.json in the working directory): module key
// ("vpc", "app.db") -> absolute directory. Missing or unreadable manifests
// give none.
func downloadedModules(work string) map[string]string {
	data, err := os.ReadFile(filepath.Join(work, ".terraform", "modules", "modules.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Modules []struct {
			Key string `json:"Key"`
			Dir string `json:"Dir"`
		} `json:"Modules"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	out := map[string]string{}
	for _, m := range manifest.Modules {
		if m.Key == "" || m.Dir == "" {
			continue
		}
		d := filepath.Join(work, filepath.FromSlash(m.Dir))
		if rel, err := filepath.Rel(work, d); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		out[m.Key] = d
	}
	return out
}

// describeModule classifies a module source called from directory from.
// For local sources inside root it returns the module directory; otherwise
// dir is "" and Message explains where the content comes from.
func describeModule(source, version, from, root string) (view model.ModuleView, dir string) {
	view.Source = source
	switch {
	case source == "":
		view.Message = "The module source is not a constant string, so its content cannot be shown"
	case hclcodec.IsLocalModuleSource(source):
		view.Local = true
		d := filepath.Join(from, filepath.FromSlash(strings.ReplaceAll(source, `\`, "/")))
		if rel, err := filepath.Rel(root, d); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			view.Message = fmt.Sprintf("Local module %s is outside this workflow's files", source)
			return view, ""
		}
		return view, d
	case registrySource.MatchString(source) && !strings.Contains(source, "::"):
		v := ""
		if version != "" {
			v = " " + version
		}
		view.Message = fmt.Sprintf("Registry module %s%s — contents are downloaded at deploy time", source, v)
	default:
		view.Message = fmt.Sprintf("Remote module %s — contents are downloaded at deploy time", source)
	}
	return view, ""
}

// readModuleFiles reads the top-level *.tf files of a module directory
// (none when it does not exist).
func readModuleFiles(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		files[e.Name()] = data
	}
	return files, nil
}

func findCall(calls []hclcodec.ModuleCall, name string) (hclcodec.ModuleCall, bool) {
	for _, c := range calls {
		if c.Name == name {
			return c, true
		}
	}
	return hclcodec.ModuleCall{}, false
}

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

	source, version := "", ""
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

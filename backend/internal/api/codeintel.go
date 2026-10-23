package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/codeintel"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The code editor's Terraform language features: the catalog data it
// completes from (cached by the client) and the engine checks it shows as
// you type.

// maxSchemaBatch bounds GET /catalog/schemas.
const maxSchemaBatch = 200

func (s *Server) catalogTypes(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.cfg.Catalog.Types())
}

func (s *Server) catalogFunctions(w http.ResponseWriter, r *http.Request) error {
	fns, err := s.cfg.Catalog.Functions()
	if err != nil {
		return err
	}
	return ok(w, fns)
}

// catalogSchemas returns several schemas at once: ?types=resource/aws_sqs_queue,
// data/aws_iam_policy_document,provider/aws -> {"resource/aws_sqs_queue": ResourceSchema}.
// Unknown types are left out.
func (s *Server) catalogSchemas(w http.ResponseWriter, r *http.Request) error {
	keys := strings.Split(r.URL.Query().Get("types"), ",")
	if len(keys) > maxSchemaBatch {
		return errorf(http.StatusBadRequest, "at most %d types per request", maxSchemaBatch)
	}
	out := map[string]*model.ResourceSchema{}
	for _, key := range keys {
		kind, typ, found := strings.Cut(strings.TrimSpace(key), "/")
		if !found {
			continue
		}
		switch model.NodeKind(kind) {
		case model.KindResource, model.KindData, catalog.KindProvider:
		default:
			return errorf(http.StatusBadRequest, "invalid kind %q (resource, data or provider)", kind)
		}
		if sch, ok := s.cfg.Catalog.SchemaOf(model.NodeKind(kind), typ); ok {
			out[kind+"/"+typ] = sch
		}
	}
	return ok(w, out)
}

// codeWorkspace returns every Terraform source the editor resolves names in.
func (s *Server) codeWorkspace(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	files, err := s.terraformSources(wf)
	if err != nil {
		return err
	}
	ws := model.CodeWorkspace{Files: make(map[string]string, len(files)), Modules: downloadedModuleSources(s.cfg.Sandbox.WorkDir(wf.ID))}
	for p, data := range files {
		ws.Files[p] = string(data)
	}
	return ok(w, ws)
}

// checkCode reports the problems of one file being edited.
func (s *Server) checkCode(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	var req model.CodeCheckRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	rel, err := cleanFilePath(req.Path)
	if err != nil {
		return err
	}
	if wf.IsDocker() {
		problems, err := s.checkComposeCode(wf, req, rel)
		if err != nil {
			return err
		}
		return ok(w, map[string]any{"problems": problems})
	}
	if wf.IsKubernetes() {
		problems, err := s.checkKubeCode(wf, req, rel)
		if err != nil {
			return err
		}
		return ok(w, map[string]any{"problems": problems})
	}
	if !strings.HasSuffix(rel, ".tf") {
		return errorf(http.StatusBadRequest, "%s is not a Terraform file", rel)
	}
	files, err := s.terraformSources(wf)
	if err != nil {
		return err
	}
	for p, content := range req.Files {
		cp, err := cleanFilePath(p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(cp, ".tf") {
			files[cp] = []byte(content)
		}
	}
	if _, found := files[rel]; !found {
		return errorf(http.StatusBadRequest, "files has no content for %s", rel)
	}
	return ok(w, map[string]any{"problems": nonNil(codeintel.Check(s.cfg.Catalog, files, rel))})
}

// terraformSources returns every .tf file the file viewer lists (generated
// Terraform of the workflow's layout and .tf assets such as local modules)
// by workflow path.
func (s *Server) terraformSources(wf *model.Workflow) (map[string][]byte, error) {
	generated, assets, err := s.exportFiles(wf, true)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for p, data := range generated {
		if strings.HasSuffix(p, ".tf") {
			out[p] = data
		}
	}
	var root *os.Root
	for dst, src := range assets {
		if !strings.HasSuffix(dst, ".tf") {
			continue
		}
		if root == nil {
			if root, err = os.OpenRoot(s.cfg.Store.AssetsDir(wf.ID)); err != nil {
				return nil, err
			}
			defer root.Close()
		}
		info, err := root.Stat(filepath.FromSlash(src))
		if err != nil || info.Size() > maxTextFile {
			continue
		}
		data, err := root.ReadFile(filepath.FromSlash(src))
		if err != nil {
			return nil, err
		}
		out[dst] = data
	}
	return out, nil
}

// downloadedModuleSources reads the modules `terraform init` downloaded into
// the sandbox working directory (.terraform/modules/modules.json) with their
// top-level .tf files. Missing or unreadable manifests give none.
func downloadedModuleSources(work string) []model.DownloadedModule {
	data, err := os.ReadFile(filepath.Join(work, ".terraform", "modules", "modules.json"))
	if err != nil {
		return []model.DownloadedModule{}
	}
	var manifest struct {
		Modules []struct {
			Key     string `json:"Key"`
			Source  string `json:"Source"`
			Version string `json:"Version"`
			Dir     string `json:"Dir"`
		} `json:"Modules"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return []model.DownloadedModule{}
	}
	out := []model.DownloadedModule{}
	for _, m := range manifest.Modules {
		if m.Key == "" || !strings.HasPrefix(path.Clean(m.Dir), ".terraform/modules/") {
			// Local modules (Dir "modules/x") come from the workflow files.
			continue
		}
		dir := filepath.Join(work, filepath.FromSlash(path.Clean(m.Dir)))
		if rel, err := filepath.Rel(work, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		files, err := readModuleFiles(dir)
		if err != nil || len(files) == 0 {
			continue
		}
		dm := model.DownloadedModule{Key: m.Key, Source: m.Source, Version: m.Version, Files: map[string]string{}}
		for name, content := range files {
			dm.Files[name] = string(content)
		}
		out = append(out, dm)
	}
	return out
}

package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// catalogSchema answers hclcodec.ExtractModule's schema questions.
type catalogSchema struct{ c *catalog.Catalog }

func (l catalogSchema) Attribute(kind model.NodeKind, typ, attr string) (hclcodec.AttrInfo, bool) {
	s, ok := l.c.Schema(kind, typ)
	if !ok {
		return hclcodec.AttrInfo{}, false
	}
	for _, a := range s.Block.Attributes {
		if a.Name == attr {
			return hclcodec.AttrInfo{Type: hclcodec.TypeConstraint(a.Type), Description: a.Description, Sensitive: a.Sensitive}, true
		}
	}
	return hclcodec.AttrInfo{}, false
}

func (l catalogSchema) HasSensitive(kind model.NodeKind, typ string) bool {
	s, ok := l.c.Schema(kind, typ)
	if !ok {
		return false
	}
	for _, a := range s.Block.Attributes {
		if a.Sensitive {
			return true
		}
	}
	return false
}

func (s *Server) extractOptions() hclcodec.ExtractOptions {
	return hclcodec.ExtractOptions{Schema: catalogSchema{s.cfg.Catalog}, ResourceFile: s.cfg.Catalog.ResourceFile}
}

// decodeExtract reads and checks an extraction request; dir is the module
// directory it would create.
func (s *Server) decodeExtract(w http.ResponseWriter, r *http.Request) (req model.ModuleExtractRequest, dir string, err error) {
	if err = decode(w, r, maxBody, &req); err != nil {
		return req, "", err
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := hclcodec.ValidModuleName(req.Name); err != nil {
		return req, "", errorf(http.StatusBadRequest, "%s", err)
	}
	if len(req.NodeIDs) == 0 {
		return req, "", errorf(http.StatusBadRequest, "nodeIds is required: select the nodes to move into the module")
	}
	return req, filepath.Join(s.cfg.Store.AssetsDir(r.PathValue("id")), "modules", req.Name), nil
}

func moduleDirTaken(dir, name string) error {
	if _, err := os.Lstat(dir); err == nil {
		return errorf(http.StatusConflict, "modules/%s already exists in the workflow files; pick another name", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// previewExtract answers what extracting nodes into a module would create,
// without changing anything.
func (s *Server) previewExtract(w http.ResponseWriter, r *http.Request) error {
	req, dir, err := s.decodeExtract(w, r)
	if err != nil {
		return err
	}
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := moduleDirTaken(dir, req.Name); err != nil {
		return err
	}
	ex, err := hclcodec.ExtractModule(wf, req.NodeIDs, req.Name, s.extractOptions())
	if err != nil {
		return errorf(http.StatusBadRequest, "%s", err)
	}
	return ok(w, ex.ModuleExtraction)
}

// extractModule moves nodes into a new local module modules/<name> and
// calls it in their place (see hclcodec.ExtractModule).
func (s *Server) extractModule(w http.ResponseWriter, r *http.Request) error {
	req, dir, err := s.decodeExtract(w, r)
	if err != nil {
		return err
	}
	created := false
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := moduleDirTaken(dir, req.Name); err != nil {
			return nil, err
		}
		ex, err := hclcodec.ExtractModule(wf, req.NodeIDs, req.Name, s.extractOptions())
		if err != nil {
			return nil, errorf(http.StatusBadRequest, "%s", err)
		}
		created = true
		root := s.cfg.Store.AssetsDir(wf.ID)
		for p, data := range ex.Contents {
			dst := filepath.Join(root, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		if created {
			_ = os.RemoveAll(dir)
		}
		return err
	}
	return ok(w, wf)
}

// inlineModule replaces a local module call by the module's blocks (see
// hclcodec.InlineModule) and deletes the module's Terraform files.
func (s *Server) inlineModule(w http.ResponseWriter, r *http.Request) error {
	var dir string
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		nd, err := findNode(wf, r.PathValue("nodeId"))
		if err != nil {
			return nil, err
		}
		if nd.Kind != model.KindModule {
			return nil, errorf(http.StatusBadRequest, "%s is not a module call", hclcodec.Address(nd))
		}
		rel, ok := hclcodec.LocalModuleDir(nd)
		if !ok {
			return nil, errorf(http.StatusBadRequest, "%s is not a local module inside the workflow files (its source is downloaded at deploy time); only local modules can be inlined", hclcodec.Address(nd))
		}
		root := s.cfg.Store.AssetsDir(wf.ID)
		dir = filepath.Join(root, filepath.FromSlash(rel))
		if caller, err := otherModuleCaller(root, rel); err != nil {
			return nil, err
		} else if caller != "" {
			return nil, errorf(http.StatusConflict, "%s is also used by the local module %s; inline works on modules called once", rel, caller)
		}
		files, err := readModuleFiles(dir)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, errorf(http.StatusNotFound, "%s has no Terraform files", rel)
		}
		if _, err := hclcodec.InlineModule(wf, nd.ID, files); err != nil {
			return nil, errorf(http.StatusBadRequest, "%s", err)
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	if err := removeModuleFiles(dir); err != nil {
		s.log.Warn("remove inlined module files", "dir", dir, "err", err)
	}
	return ok(w, wf)
}

// otherModuleCaller returns the directory (relative to root) of a local
// module under root/modules that calls the module directory rel.
func otherModuleCaller(root, rel string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "modules"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		from := "modules/" + e.Name()
		if from == rel {
			continue
		}
		files, err := readModuleFiles(filepath.Join(root, "modules", e.Name()))
		if err != nil {
			return "", err
		}
		for _, c := range hclcodec.Summarize(files).Modules {
			if hclcodec.IsLocalModuleSource(c.Source) && hclcodec.ResolveLocalSource(from, c.Source) == rel {
				return from, nil
			}
		}
	}
	return "", nil
}

// removeModuleFiles deletes an inlined module's *.tf files and README.md;
// other files (sources, templates the blocks still read) stay. The directory
// goes when nothing is left.
func removeModuleFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type().IsRegular() && (strings.HasSuffix(e.Name(), ".tf") || e.Name() == "README.md") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	if rest, err := os.ReadDir(dir); err == nil && len(rest) == 0 {
		return os.Remove(dir)
	}
	return nil
}

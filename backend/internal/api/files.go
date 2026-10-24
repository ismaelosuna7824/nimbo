package api

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func (s *Server) getTerraform(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	files, err := s.exportLayout(wf, false)
	if err != nil {
		return err
	}
	out := make(map[string]string, len(files))
	for name, data := range files {
		out[name] = string(data)
	}
	return ok(w, map[string]any{"files": out})
}

// putTerraform edits the workflow's code: either the complete Terraform
// (files: root files of the single layout, or the service module and
// environment roots of the environments layout, as GET returns them) or
// changes and moves of any of its files (see codeRequest).
func (s *Server) putTerraform(w http.ResponseWriter, r *http.Request) error {
	var req codeRequest
	if err := decode(w, r, maxImportBody, &req); err != nil {
		return err
	}
	if req.Files != nil && (req.Changes != nil || req.Moves != nil) {
		return errorf(http.StatusBadRequest, "send either files or changes/moves")
	}
	if req.Files == nil && req.Changes == nil && req.Moves == nil {
		return errorf(http.StatusBadRequest, "files or changes are required")
	}
	wf, err := s.mutate(r.PathValue("id"), func(cur *model.Workflow) ([]model.Diagnostic, error) {
		if req.Files == nil {
			return s.applyFileChanges(cur, req)
		}
		if !cur.IsTerraform() {
			return nil, errorf(http.StatusBadRequest, "%s is a %s workflow: send changes/moves of its files, not files", cur.ID, model.KindLabel(cur.Kind))
		}
		files := make(map[string][]byte, len(req.Files))
		var syntax []model.Diagnostic
		for name, content := range req.Files {
			files[name] = []byte(content)
			if req.Format {
				out, d := hclcodec.Format(name, files[name])
				if d == nil {
					files[name] = out
				}
				syntax = append(syntax, d...)
			}
		}
		if len(syntax) > 0 {
			return nil, &httpError{status: http.StatusBadRequest, msg: "the code has syntax errors; nothing was saved", diagnostics: syntax}
		}
		return s.importCode(cur, files)
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// syntaxError rejects code that does not parse: hclcodec.Import keeps going
// past syntax errors, and saving that partial result would silently drop
// the user's code.
func syntaxError(diags []model.Diagnostic) error {
	for _, d := range diags {
		if d.Severity == model.SeverityError && d.Code == "parse_error" {
			return errors.New("the Terraform code has syntax errors; nothing was saved")
		}
	}
	return nil
}

// exportTerraform renders wf's Terraform as one root module (the sandbox
// runs it whatever the export layout) with the catalog's recommended file
// layout, and its environment files with environments.
func (s *Server) exportTerraform(wf *model.Workflow, environments bool) (map[string][]byte, error) {
	return hclcodec.Export(wf, hclcodec.ExportOptions{Environments: environments, ResourceFile: s.cfg.Catalog.ResourceFile})
}

// exportLayout renders wf's Terraform in its export layout
// (hclcodec.ResolveLayout).
func (s *Server) exportLayout(wf *model.Workflow, environments bool) (map[string][]byte, error) {
	files, err := hclcodec.ExportLayout(wf, hclcodec.ExportOptions{Environments: environments, ResourceFile: s.cfg.Catalog.ResourceFile})
	if err != nil {
		return nil, &httpError{status: http.StatusBadRequest, msg: err.Error()}
	}
	return files, nil
}

// exportFiles renders wf in its layout plus its assets: generated maps
// export paths to content, assets maps export paths to asset paths
// (relative to the workflow's assets directory; hclcodec.AssetPlacement).
// Generated Terraform wins over an asset with the same export path; the
// project scaffold (README.md, .gitignore, ...) is only generated where the
// workflow has no file of its own (e.g. a README that came with an import).
// Docker and Kubernetes workflows generate nothing: their files are
// exported as stored.
func (s *Server) exportFiles(wf *model.Workflow, environments bool) (generated map[string][]byte, assets map[string]string, err error) {
	place := func(rel string) []string { return []string{rel} }
	generated = map[string][]byte{}
	if wf.IsTerraform() {
		if generated, err = s.exportLayout(wf, environments); err != nil {
			return nil, nil, err
		}
		place = hclcodec.AssetPlacement(wf)
	}
	own := map[string]bool{}
	assets = map[string]string{}
	root := s.cfg.Store.AssetsDir(wf.ID)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			for _, dst := range place(rel) {
				own[dst] = true
				if _, clash := generated[dst]; !clash {
					assets[dst] = rel
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if !wf.IsTerraform() {
		return generated, assets, nil
	}
	for name, data := range hclcodec.Scaffold(wf, s.cfg.Catalog.ResourceFile) {
		if _, taken := generated[name]; !taken && !own[name] {
			generated[name] = data
		}
	}
	return generated, assets, nil
}

func (s *Server) exportDir(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Dir          string `json:"dir"`
		Environments bool   `json:"environments"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	dir, err := absDir(req.Dir)
	if err != nil {
		return err
	}
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	generated, assets, err := s.exportFiles(wf, req.Environments)
	if err != nil {
		return err
	}
	assetsRoot := s.cfg.Store.AssetsDir(wf.ID)
	var written []string
	for dst, src := range assets {
		if err := copyFile(filepath.Join(assetsRoot, filepath.FromSlash(src)), filepath.Join(dir, filepath.FromSlash(dst))); err != nil {
			return err
		}
		written = append(written, dst)
	}
	for rel, data := range generated {
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		written = append(written, rel)
	}
	sort.Strings(written)
	return ok(w, map[string]any{"dir": dir, "files": nonNil(written)})
}

func (s *Server) exportZip(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	generated, assets, err := s.exportFiles(wf, r.URL.Query().Get("environments") == "true")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(generated))
	for n := range generated {
		names = append(names, n)
	}
	sort.Strings(names)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, wf.ID))
	zw := zip.NewWriter(w)
	for _, n := range names {
		f, err := zw.Create(n)
		if err != nil {
			return nil // headers already sent; the client sees a truncated zip
		}
		if _, err := f.Write(generated[n]); err != nil {
			return nil
		}
	}
	root := s.cfg.Store.AssetsDir(wf.ID)
	dsts := make([]string, 0, len(assets))
	for dst := range assets {
		dsts = append(dsts, dst)
	}
	sort.Strings(dsts)
	for _, dst := range dsts {
		if err := zipFile(zw, filepath.Join(root, filepath.FromSlash(assets[dst])), dst); err != nil {
			s.log.Error("zip asset", "file", dst, "err", err)
			return nil
		}
	}
	if err := zw.Close(); err != nil {
		s.log.Error("zip export", "workflow", wf.ID, "err", err)
	}
	return nil
}

func zipFile(zw *zip.Writer, src, name string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	hdr, err := zip.FileInfoHeader(fi)
	if err != nil {
		return err
	}
	hdr.Name, hdr.Method = name, zip.Deflate
	out, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return err
}

// ---------------------------------------------------------------------------
// Import

// skipImport reports names never imported: Terraform working data, state
// and VCS metadata. The dependency lock file is kept: it pins the provider
// versions the configuration was written for, and export writes it back.
func skipImport(name string) bool {
	return (strings.HasPrefix(name, ".terraform") && name != ".terraform.lock.hcl") ||
		strings.Contains(name, ".tfstate") || name == ".git" || name == ".DS_Store"
}

// classify maps a relative slash path to its key for hclcodec.Import, or ""
// for an asset. Root *.tf/*.tfvars are Terraform; environments/<name>.tfvars
// and environments/<name>.env.example (as written by export) are passed
// through so Import restores environment values and env vars. Files Import
// does not interpret (override files, JSON configuration) are assets, so
// they are exported unchanged.
func classify(rel string) (tfName string) {
	dir, base := path.Split(rel)
	switch {
	case dir == "" && hclcodec.IsOverrideFile(base):
		return ""
	case dir == "" && (strings.HasSuffix(base, ".tf") || strings.HasSuffix(base, ".tfvars")):
		return base
	case dir == "environments/" && (strings.HasSuffix(base, ".tfvars") || strings.HasSuffix(base, ".env.example")):
		return rel
	}
	return ""
}

// ---------------------------------------------------------------------------
// File helpers

// absDir expands a leading ~ and requires an absolute path.
func absDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errorf(http.StatusBadRequest, "dir is required")
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) {
		return "", errorf(http.StatusBadRequest, "dir must be an absolute path, got %q", dir)
	}
	return filepath.Clean(dir), nil
}

// copyFile copies a regular file, creating parent directories and keeping
// the executable bit.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copyTree copies every regular file under src into dst, except Nimbo's
// own .nimbo folders (saved port remaps, generated overrides): they belong
// to the original's runs, and a copy must plan its host ports afresh.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".nimbo" {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		return copyFile(p, filepath.Join(dst, rel))
	})
}

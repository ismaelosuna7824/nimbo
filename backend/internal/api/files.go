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
	files, err := hclcodec.Export(wf, hclcodec.ExportOptions{})
	if err != nil {
		return err
	}
	out := make(map[string]string, len(files))
	for name, data := range files {
		out[name] = string(data)
	}
	return ok(w, map[string]any{"files": out})
}

func (s *Server) putTerraform(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Files map[string]string `json:"files"`
	}
	if err := decode(w, r, maxImportBody, &req); err != nil {
		return err
	}
	files := make(map[string][]byte, len(req.Files))
	for name, content := range req.Files {
		files[name] = []byte(content)
	}
	wf, err := s.mutate(r.PathValue("id"), func(cur *model.Workflow) ([]model.Diagnostic, error) {
		next, diags, err := hclcodec.Import(cur.Name, files)
		if err == nil {
			err = syntaxError(diags)
		}
		if err != nil {
			return nil, &httpError{status: http.StatusBadRequest, msg: err.Error(), diagnostics: diags}
		}
		hclcodec.MergeImported(cur, next)
		*cur = *next
		return diags, nil
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

// exportFiles renders wf plus its assets (generated files win on clashes).
func (s *Server) exportFiles(wf *model.Workflow, environments bool) (generated map[string][]byte, assets []string, err error) {
	generated, err = hclcodec.Export(wf, hclcodec.ExportOptions{Environments: environments})
	if err != nil {
		return nil, nil, err
	}
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
			if _, clash := generated[filepath.ToSlash(rel)]; !clash {
				assets = append(assets, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	return generated, assets, err
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
	for _, rel := range assets {
		if err := copyFile(filepath.Join(assetsRoot, filepath.FromSlash(rel)), filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			return err
		}
		written = append(written, rel)
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
	for _, rel := range assets {
		if err := zipFile(zw, filepath.Join(root, filepath.FromSlash(rel)), rel); err != nil {
			s.log.Error("zip asset", "file", rel, "err", err)
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
// and VCS metadata.
func skipImport(name string) bool {
	return strings.HasPrefix(name, ".terraform") || strings.Contains(name, ".tfstate") || name == ".git" || name == ".DS_Store"
}

// classify maps a relative slash path to its key for hclcodec.Import, or ""
// for an asset. Root *.tf/*.tfvars are Terraform; environments/<name>.tfvars
// and environments/<name>.env.example (as written by export) are passed
// through so Import restores environment values and env vars.
func classify(rel string) (tfName string) {
	dir, base := path.Split(rel)
	switch {
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

// copyTree copies every regular file under src into dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		return copyFile(p, filepath.Join(dst, rel))
	})
}

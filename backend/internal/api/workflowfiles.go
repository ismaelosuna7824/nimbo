package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The file viewer: everything an export writes (generated Terraform with
// environments, plus the workflow's assets), readable one file at a time,
// with text assets editable in place.

const (
	// maxTextFile is the largest file served as text and the largest text
	// asset that can be saved.
	maxTextFile = 2 << 20
	// sniffLen is how much of a file decides whether it is binary.
	sniffLen = 8 << 10
	// lockFile is written by terraform init; it is shown but not edited.
	lockFile = ".terraform.lock.hcl"
)

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	generated, assets, err := s.exportFiles(wf, true)
	if err != nil {
		return err
	}
	files := make([]model.WorkflowFile, 0, len(generated)+len(assets))
	for name, data := range generated {
		files = append(files, generatedFile(name, data))
	}
	if len(assets) > 0 {
		root, err := os.OpenRoot(s.cfg.Store.AssetsDir(wf.ID))
		if err != nil {
			return err
		}
		defer root.Close()
		for _, rel := range assets {
			f, err := assetFile(root, rel)
			if err != nil {
				return err
			}
			files = append(files, f)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return ok(w, map[string]any{"files": files})
}

func (s *Server) getFileContent(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	rel, err := cleanFilePath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	f, data, err := s.lookupFile(wf, rel)
	if err != nil {
		return err
	}
	if f.Binary || f.Size > maxTextFile {
		status, code, msg := http.StatusUnsupportedMediaType, "binary", "is a binary file"
		if !f.Binary {
			status, code, msg = http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("is larger than %d bytes", maxTextFile)
		}
		return writeJSON(w, status, map[string]any{
			"error":    fmt.Sprintf("%s %s", rel, msg),
			"code":     code,
			"size":     f.Size,
			"download": downloadPath(wf.ID, rel),
		})
	}
	if data == nil {
		root, err := os.OpenRoot(s.cfg.Store.AssetsDir(wf.ID))
		if err != nil {
			return err
		}
		defer root.Close()
		if data, err = root.ReadFile(filepath.FromSlash(rel)); err != nil {
			return err
		}
	}
	return ok(w, model.WorkflowFileContent{WorkflowFile: f, Content: string(data)})
}

// getFileRaw downloads one file as stored (assets) or rendered (generated).
func (s *Server) getFileRaw(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	rel, err := cleanFilePath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	f, data, err := s.lookupFile(wf, rel)
	if err != nil {
		return err
	}
	var body io.Reader = bytes.NewReader(data)
	if data == nil {
		root, err := os.OpenRoot(s.cfg.Store.AssetsDir(wf.ID))
		if err != nil {
			return err
		}
		defer root.Close()
		in, err := root.Open(filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		defer in.Close()
		body = in
	}
	contentType := "text/plain; charset=utf-8"
	if f.Binary {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(rel)}))
	w.Header().Set("Content-Length", fmt.Sprint(f.Size))
	_, _ = io.Copy(w, body) // headers are sent; a failed copy is a truncated download
	return nil
}

// putFileContent replaces the content of an editable text asset. Generated
// Terraform is edited through PUT /workflows/{id}/terraform instead.
func (s *Server) putFileContent(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Content *string `json:"content"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	rel, err := cleanFilePath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	if req.Content == nil {
		return errorf(http.StatusBadRequest, "content is required")
	}
	data := []byte(*req.Content)
	if len(data) > maxTextFile {
		return errorf(http.StatusRequestEntityTooLarge, "content larger than %d bytes", maxTextFile)
	}
	if isBinary(data, true) {
		return errorf(http.StatusBadRequest, "content must be UTF-8 text without NUL bytes")
	}
	var saved model.WorkflowFile
	wf, err := s.mutate(r.PathValue("id"), func(cur *model.Workflow) ([]model.Diagnostic, error) {
		f, generated, err := s.lookupFile(cur, rel)
		if err != nil {
			return nil, err
		}
		switch {
		case generated != nil:
			return nil, errorf(http.StatusConflict, "%s is generated from the diagram; edit it with PUT /workflows/%s/terraform", rel, cur.ID)
		case !f.Editable:
			return nil, errorf(http.StatusConflict, "%s cannot be edited", rel)
		}
		root, err := os.OpenRoot(s.cfg.Store.AssetsDir(cur.ID))
		if err != nil {
			return nil, err
		}
		defer root.Close()
		name := filepath.FromSlash(rel)
		fi, err := root.Lstat(name)
		if err != nil {
			return nil, err
		}
		if err := root.WriteFile(name, data, fi.Mode().Perm()); err != nil {
			return nil, err
		}
		saved, err = assetFile(root, rel)
		return nil, err
	})
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"file": saved, "workflow": wf})
}

// lookupFile describes the export file rel. For generated files it also
// returns their content; assets are read by the caller. Files an export
// does not write are 404: assets hidden by a generated file of the same
// name, and anything reached through a symbolic link.
func (s *Server) lookupFile(wf *model.Workflow, rel string) (model.WorkflowFile, []byte, error) {
	generated, err := s.exportGenerated(wf)
	if err != nil {
		return model.WorkflowFile{}, nil, err
	}
	if data, found := generated[rel]; found {
		return generatedFile(rel, data), data, nil
	}
	notFound := errorf(http.StatusNotFound, "file %s not found", rel)
	root, err := os.OpenRoot(s.cfg.Store.AssetsDir(wf.ID))
	if errors.Is(err, fs.ErrNotExist) {
		return model.WorkflowFile{}, nil, notFound
	}
	if err != nil {
		return model.WorkflowFile{}, nil, err
	}
	defer root.Close()
	// Export walks the assets without following links; so does this.
	parts := strings.Split(rel, "/")
	for i := range parts {
		fi, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if errors.Is(err, fs.ErrNotExist) {
			return model.WorkflowFile{}, nil, notFound
		}
		if err != nil {
			return model.WorkflowFile{}, nil, err
		}
		last := i == len(parts)-1
		if (last && !fi.Mode().IsRegular()) || (!last && !fi.IsDir()) {
			return model.WorkflowFile{}, nil, notFound
		}
	}
	f, err := assetFile(root, rel)
	return f, nil, err
}

func (s *Server) exportGenerated(wf *model.Workflow) (map[string][]byte, error) {
	return hclcodec.Export(wf, hclcodec.ExportOptions{Environments: true})
}

func generatedFile(name string, data []byte) model.WorkflowFile {
	kind := fileKind(name)
	return model.WorkflowFile{
		Path:      name,
		Size:      int64(len(data)),
		Kind:      kind,
		Generated: true,
		// Only Terraform code re-imports; values and env vars are edited in the app.
		Editable: kind == model.FileTerraform,
		Language: fileLanguage(name),
	}
}

// assetFile describes the asset rel, sniffing its first bytes.
func assetFile(root *os.Root, rel string) (model.WorkflowFile, error) {
	in, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return model.WorkflowFile{}, err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return model.WorkflowFile{}, err
	}
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(in, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return model.WorkflowFile{}, err
	}
	binary := isBinary(head[:n], int64(n) == fi.Size())
	return model.WorkflowFile{
		Path:     rel,
		Size:     fi.Size(),
		Kind:     fileKind(rel),
		Binary:   binary,
		Editable: !binary && fi.Size() <= maxTextFile && path.Base(rel) != lockFile,
		Language: fileLanguage(rel),
	}, nil
}

// isBinary reports whether b is not UTF-8 text or holds a NUL byte. When b
// is only the start of a file (complete is false), a rune cut at the end
// does not count.
func isBinary(b []byte, complete bool) bool {
	if bytes.IndexByte(b, 0) >= 0 {
		return true
	}
	if !complete {
		for n := 1; n <= utf8.UTFMax && n <= len(b); n++ {
			if utf8.RuneStart(b[len(b)-n]) {
				if !utf8.FullRune(b[len(b)-n:]) {
					b = b[:len(b)-n]
				}
				break
			}
		}
	}
	return !utf8.Valid(b)
}

func fileKind(rel string) model.FileKind {
	base := path.Base(rel)
	switch {
	case strings.HasPrefix(rel, "modules/"):
		return model.FileModule
	case strings.HasSuffix(base, ".tfvars"), strings.HasSuffix(base, ".tfvars.json"):
		return model.FileTfvars
	case strings.HasSuffix(base, ".env.example"):
		return model.FileEnv
	case !strings.Contains(rel, "/") && strings.HasSuffix(base, ".tf"):
		return model.FileTerraform
	}
	return model.FileAsset
}

func fileLanguage(rel string) string {
	base := strings.ToLower(path.Base(rel))
	switch {
	case strings.HasSuffix(base, ".tf.json"), strings.HasSuffix(base, ".tfvars.json"):
		return "json"
	case strings.HasSuffix(base, ".env.example"), base == ".env", strings.HasPrefix(base, ".env."):
		return "shell"
	}
	switch path.Ext(base) {
	case ".tf", ".tfvars", ".hcl":
		return "hcl"
	case ".json":
		return "json"
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".ts", ".mts", ".cts", ".tsx":
		return "typescript"
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".yaml", ".yml":
		return "yaml"
	case ".md", ".markdown":
		return "markdown"
	case ".sh", ".bash", ".zsh":
		return "shell"
	}
	return "text"
}

// cleanFilePath validates a file path from a request: slash-separated,
// relative, already clean and without "." or ".." elements.
func cleanFilePath(p string) (string, error) {
	if p == "" {
		return "", errorf(http.StatusBadRequest, "path is required")
	}
	if strings.ContainsAny(p, "\\\x00") || !fs.ValidPath(p) || p == "." {
		return "", errorf(http.StatusBadRequest, "path must be a clean relative path inside the workflow files, got %q", p)
	}
	return p, nil
}

func downloadPath(id, rel string) string {
	return "/api/v1/workflows/" + url.PathEscape(id) + "/files/raw?path=" + url.QueryEscape(rel)
}
